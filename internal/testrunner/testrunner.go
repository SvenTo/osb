// Package testrunner runs *.test.yaml library tests.
package testrunner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/imports"
	"github.com/SvenTo/osb/internal/render/cloudinit"
	"github.com/SvenTo/osb/internal/render/dockerfile"
	"github.com/SvenTo/osb/internal/render/shell"
	"github.com/SvenTo/osb/internal/schema"
	"github.com/SvenTo/osb/internal/target"
	"github.com/SvenTo/osb/library"
)

// Options configures Run.
type Options struct {
	Update  bool
	Run     bool
	Verbose bool
	Version string
}

// Result summarizes a test run.
type Result struct {
	Total  int
	Failed int
}

// Find returns the test files below the given paths.
func Find(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			files = append(files, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path != p && (strings.HasPrefix(d.Name(), ".") || d.Name() == "out" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && (strings.HasSuffix(path, ".test.yaml") || strings.HasSuffix(path, ".test.yml")) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// Run runs all tests found in paths.
func Run(paths []string, opts Options) (*Result, error) {
	files, err := Find(paths)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	for _, f := range files {
		if err := runFile(f, opts, res); err != nil {
			res.Total++
			res.Failed++
			fmt.Printf("FAIL %s: %v\n", config.DisplayPath(f), err)
		}
	}
	return res, nil
}

func runFile(file string, opts Options, res *Result) error {
	var extra []config.Import
	base := strings.TrimSuffix(strings.TrimSuffix(file, ".yaml"), ".yml")
	base = strings.TrimSuffix(base, ".test")
	for _, ext := range []string{".yaml", ".yml"} {
		if _, err := os.Stat(base + ext); err == nil {
			extra = append(extra, config.Import{Path: filepath.Base(base + ext), Pos: config.Pos{File: config.DisplayPath(file)}})
			break
		}
	}
	resolver := imports.NewResolver(file, false)
	cfg, err := config.Load(file, config.LoadOptions{Validate: schema.Validate, ResolveGit: resolver.Resolve, ExtraImports: extra, ExtraActions: library.Actions()})
	if err != nil {
		return err
	}
	if len(cfg.Tests) == 0 {
		return fmt.Errorf("no tests defined")
	}
	baseSteps := cfg.Steps
	for i, tc := range cfg.Tests {
		res.Total++
		name := tc.Name
		if name == "" {
			name = fmt.Sprintf("test %d", i+1)
		}
		label := fmt.Sprintf("%s: %s", config.DisplayPath(file), name)
		if err := runCase(cfg, baseSteps, tc, opts); err != nil {
			res.Failed++
			fmt.Printf("FAIL %s\n     %s\n", label, strings.ReplaceAll(err.Error(), "\n", "\n     "))
			continue
		}
		if opts.Verbose {
			fmt.Printf("PASS %s\n", label)
		}
	}
	return nil
}

func runCase(cfg *config.Config, baseSteps []*config.Step, tc *config.TestCase, opts Options) error {
	c := *cfg
	c.Steps = append(append([]*config.Step{}, baseSteps...), tc.Steps...)
	tname := tc.Target
	if tname == "" {
		tname = "shell"
	}
	fl := tc.Flavor
	if fl == "" {
		fl = engine.EnabledFlavors(&c)[0]
	}
	out, warnings, plan, err := render(&c, fl, tname, tc, opts)
	exp := tc.Expect
	if exp.Error != "" {
		if err == nil {
			return fmt.Errorf("expected error containing %q, got none", exp.Error)
		}
		if !strings.Contains(err.Error(), exp.Error) {
			return fmt.Errorf("expected error containing %q, got: %v", exp.Error, err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	var problems []string
	for _, s := range exp.Contains {
		if !strings.Contains(out, s) {
			problems = append(problems, fmt.Sprintf("output does not contain %q", s))
		}
	}
	for _, s := range exp.NotContains {
		if strings.Contains(out, s) {
			problems = append(problems, fmt.Sprintf("output contains %q", s))
		}
	}
	for _, w := range exp.Warnings {
		found := false
		for _, got := range warnings {
			if strings.Contains(got, w) {
				found = true
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("no warning containing %q (got: %s)", w, strings.Join(warnings, "; ")))
		}
	}
	if exp.Golden != "" {
		p := filepath.Join(cfg.Dir, exp.Golden)
		if opts.Update {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
				return err
			}
		} else {
			want, err := os.ReadFile(p)
			if err != nil {
				problems = append(problems, fmt.Sprintf("golden file: %v (run osb test --update)", err))
			} else if string(want) != out {
				problems = append(problems, "output differs from golden file "+exp.Golden+" (run osb test --update to accept)\n"+diff(string(want), out))
			}
		}
	}
	if opts.Run && tc.Run {
		if tname != "shell" {
			problems = append(problems, "run: true is only supported for target shell")
		} else if err := runInContainer(plan, out); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func render(cfg *config.Config, flavorName, tname string, tc *config.TestCase, opts Options) (string, []string, *engine.Plan, error) {
	in := engine.ArgInput{Values: tc.Arguments, Placeholders: true}
	plan, err := engine.Evaluate(cfg, engine.Options{Flavor: flavorName, Target: tname, Args: in})
	if err != nil {
		return "", nil, nil, err
	}
	t, _ := target.Get(tname)
	var out string
	switch t.Renderer {
	case target.RendererShell:
		doc := plan.ShellDoc("test", "")
		out = shell.Render(doc)
	case target.RendererDockerfile:
		dir, err := os.MkdirTemp("", "osb-test-")
		if err != nil {
			return "", nil, nil, err
		}
		defer os.RemoveAll(dir)
		if _, err := dockerfile.Render(plan, dir, dockerfile.Options{Version: "test"}); err != nil {
			return "", nil, nil, err
		}
		b, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
		if err != nil {
			return "", nil, nil, err
		}
		out = string(b)
	case target.RendererCloudInit:
		r, err := cloudinit.Render(plan, cloudinit.Options{Version: "test"})
		if err != nil {
			return "", nil, nil, err
		}
		out = string(r.UserData)
	}
	// Make paths independent of the working directory.
	out = strings.ReplaceAll(out, config.DisplayPath(cfg.File), filepath.Base(cfg.File))
	if d := config.DisplayPath(cfg.Dir); d != "." {
		out = strings.ReplaceAll(out, d+string(filepath.Separator), "")
	}
	return out, plan.Warnings, plan, nil
}

func runInContainer(plan *engine.Plan, script string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("run: docker is not installed")
	}
	cmd := exec.Command("docker", "run", "--rm", "-i", plan.Flavor.BaseImage, "sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		if len(lines) > 25 {
			lines = lines[len(lines)-25:]
		}
		return fmt.Errorf("script failed in %s: %v\n%s", plan.Flavor.BaseImage, err, strings.Join(lines, "\n"))
	}
	return nil
}

// diff returns a short line diff.
func diff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, c string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			c = g[i]
		}
		if a == c {
			continue
		}
		fmt.Fprintf(&b, "  line %d:\n    - %s\n    + %s\n", i+1, a, c)
		shown++
		if shown == 5 {
			b.WriteString("  ...\n")
			break
		}
	}
	return b.String()
}
