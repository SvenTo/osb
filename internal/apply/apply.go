// Package apply configures the running system.
package apply

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/render/shell"
)

// Options configures Run.
type Options struct {
	Yes        bool
	ShowScript bool
	DryRun     bool
	Version    string
	Command    string
	In         io.Reader
	Out        io.Writer
}

// ParseOSRelease reads ID and ID_LIKE from an os-release file.
func ParseOSRelease(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ids []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok || (k != "ID" && k != "ID_LIKE") {
			continue
		}
		v = strings.Trim(v, `"'`)
		if k == "ID" {
			ids = append(strings.Fields(v), ids...)
		} else {
			ids = append(ids, strings.Fields(v)...)
		}
	}
	return ids, sc.Err()
}

// DetectFlavor picks the enabled flavor matching /etc/os-release.
func DetectFlavor(cfg *config.Config, osRelease string) (string, error) {
	ids, err := ParseOSRelease(osRelease)
	if err != nil {
		return "", fmt.Errorf("cannot detect the flavor (%v); use --flavor", err)
	}
	enabled := engine.EnabledFlavors(cfg)
	// Custom flavors named like the ID win over built-ins.
	for _, id := range ids {
		for _, name := range enabled {
			if name == id {
				return name, nil
			}
		}
	}
	for _, id := range ids {
		for _, name := range enabled {
			fl, err := engine.ResolveFlavor(cfg, name)
			if err != nil {
				continue
			}
			for _, oid := range fl.OSReleaseIDs {
				if oid == id {
					return name, nil
				}
			}
		}
	}
	return "", fmt.Errorf("the system (%s) matches none of the supported flavors (%s); use --flavor", strings.Join(ids, " "), strings.Join(enabled, ", "))
}

// Summary describes the plan for humans (secrets redacted).
func Summary(plan *engine.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan for %s (flavor %s), %d steps:\n", config.DisplayPath(plan.Config.File), plan.Flavor.Name, len(plan.Steps))
	for _, s := range plan.Steps {
		fmt.Fprintf(&b, "  %2d. %s", s.Index, s.Title)
		if s.Action != "" {
			fmt.Fprintf(&b, "  [%s]", s.Action)
		}
		b.WriteString("\n")
		for _, line := range describe(s) {
			fmt.Fprintf(&b, "        %s\n", plan.Secrets.String(line))
		}
	}
	if len(plan.Args) > 0 {
		b.WriteString("Arguments:\n")
		for _, d := range plan.Config.Arguments {
			v, ok := plan.Args[d.Name]
			if !ok {
				continue
			}
			s := fmt.Sprint(v)
			if d.Type == "secret" {
				s = "***"
			}
			fmt.Fprintf(&b, "  %s = %s\n", d.Name, s)
		}
	}
	return b.String()
}

func describe(s *ops.Step) []string {
	var out []string
	for _, g := range s.Guards {
		out = append(out, g.Kind+": "+g.Value)
	}
	if s.OnError == "continue" {
		out = append(out, "on error: continue")
	}
	for _, o := range s.Ops {
		switch t := o.(type) {
		case ops.File:
			verb := "write"
			if t.Append {
				verb = "append to"
			}
			out = append(out, fmt.Sprintf("%s %s (%d bytes, mode %s)", verb, t.Path, len(t.Content), t.Mode))
		case ops.Packages:
			verb := "install packages"
			if t.Missing {
				verb = "ensure packages"
			}
			out = append(out, verb+": "+strings.Join(t.Names, " "))
		case ops.User:
			out = append(out, "create user "+t.Name)
		case ops.Hostname:
			out = append(out, "set hostname "+t.Name)
		case ops.Env:
			for _, kv := range t.Vars {
				out = append(out, "set "+kv.Key+"="+kv.Value)
			}
		case ops.Download:
			out = append(out, "download "+t.URL+" -> "+t.Path)
		case ops.Note:
			out = append(out, "note: "+t.Message)
		case ops.Shell:
			lines := strings.Split(strings.TrimSpace(t.Script), "\n")
			for i, l := range lines {
				if i == 3 {
					out = append(out, fmt.Sprintf("... (%d more lines)", len(lines)-3))
					break
				}
				out = append(out, "$ "+l)
			}
		}
	}
	return out
}

// Run prints the plan, asks for confirmation and runs the generated script.
func Run(plan *engine.Plan, opts Options) error {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	script := shell.Render(plan.ShellDoc(opts.Version, opts.Command))
	fmt.Fprint(opts.Out, Summary(plan))
	if opts.ShowScript {
		fmt.Fprintln(opts.Out, "\n--- script ---")
		fmt.Fprint(opts.Out, plan.Secrets.String(script))
		fmt.Fprintln(opts.Out, "--- end of script ---")
	}
	if opts.DryRun {
		return nil
	}
	if len(plan.Steps) == 0 {
		fmt.Fprintln(opts.Out, "nothing to do")
		return nil
	}
	if !opts.Yes {
		fmt.Fprint(opts.Out, "\nApply these changes to this system? [y/N] ")
		line, _ := bufio.NewReader(opts.In).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return fmt.Errorf("aborted")
		}
	}
	f, err := os.CreateTemp("", "osb-apply-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), 0o700); err != nil {
		return err
	}
	argv := []string{"/bin/sh", f.Name()}
	if os.Geteuid() != 0 {
		sudo, err := exec.LookPath("sudo")
		if err != nil {
			return fmt.Errorf("apply needs root privileges; run osb as root")
		}
		argv = append([]string{sudo}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, opts.Out, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("configuration failed: %w", err)
	}
	return nil
}
