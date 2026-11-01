// Package cli implements the osb command line interface.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/SvenTo/osb/internal/actions"
	"github.com/SvenTo/osb/internal/apply"
	"github.com/SvenTo/osb/internal/build"
	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/docs"
	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/flavor"
	"github.com/SvenTo/osb/internal/imports"
	"github.com/SvenTo/osb/internal/render/cloudinit"
	"github.com/SvenTo/osb/internal/render/dockerfile"
	"github.com/SvenTo/osb/internal/render/shell"
	"github.com/SvenTo/osb/internal/schema"
	"github.com/SvenTo/osb/internal/target"
	"github.com/SvenTo/osb/internal/testrunner"
	"github.com/SvenTo/osb/library"
)

// Execute runs the CLI.
func Execute() int {
	root := newRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	g := &globalOpts{}
	root := &cobra.Command{
		Use:           "osb",
		Short:         "Build images and configure systems from one YAML configuration",
		Long:          "osb turns a declarative YAML configuration into shell scripts, Dockerfiles,\ncontainer images, WSL distributions, cloud-init configs and VM images (qcow2, OVA),\nor applies it to the running system.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&g.file, "file", "f", "", "configuration file (default osb.yaml)")
	pf.StringVar(&g.flavor, "flavor", "", "flavor (distribution): "+strings.Join(flavor.BuiltinNames(), ", ")+" or a custom flavor")
	pf.StringArrayVarP(&g.args, "arg", "a", nil, "argument value name=value (repeatable)")
	pf.StringVar(&g.argsFile, "args-file", "", "YAML file with argument values")
	pf.BoolVar(&g.noPrompt, "no-prompt", false, "never prompt for missing arguments")
	pf.BoolVarP(&g.verbose, "verbose", "v", false, "verbose output")

	root.AddCommand(
		validateCmd(g), renderCmd(g), buildCmd(g), applyCmd(g),
		actionsCmd(), flavorsCmd(), targetsCmd(), schemaCmd(), testCmd(g),
		importsCmd(g), docsCmd(), initCmd(),
	)
	return root
}

func validateCmd(g *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the configuration for all (or the selected) flavors and targets",
		Long: "Validates the configuration against the schema, then evaluates it for every enabled\n" +
			"flavor and target. Missing required arguments are replaced by placeholders.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig(g.configFile(), false)
			if err != nil {
				return err
			}
			in, err := g.argInput(false)
			if err != nil {
				return err
			}
			in.Placeholders = true
			flavors := engine.EnabledFlavors(cfg)
			if g.flavor != "" {
				flavors = []string{g.flavor}
			}
			var failed []string
			for _, fl := range flavors {
				var warnings []string
				nsteps := 0
				for _, t := range target.All() {
					if !engine.TargetAllowed(cfg, t) {
						continue
					}
					plan, err := engine.Evaluate(cfg, engine.Options{Flavor: fl, Target: t.Name, Args: in})
					if err != nil {
						failed = append(failed, fmt.Sprintf("%s/%s: %v", fl, t.Name, err))
						continue
					}
					if t.Name == "shell" || nsteps == 0 {
						nsteps = len(plan.Steps)
					}
					for _, w := range plan.Warnings {
						warnings = appendUniq(warnings, t.Name+": "+w)
					}
				}
				fmt.Printf("%-12s %d steps\n", fl, nsteps)
				if g.verbose {
					for _, w := range warnings {
						fmt.Printf("  warning: %s\n", w)
					}
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("validation failed:\n  %s", strings.Join(failed, "\n  "))
			}
			fmt.Printf("%s is valid (%d files)\n", config.DisplayPath(cfg.File), len(cfg.Sources))
			return nil
		},
	}
}

func appendUniq(l []string, s string) []string {
	for _, e := range l {
		if e == s {
			return l
		}
	}
	return append(l, s)
}

func renderCmd(g *globalOpts) *cobra.Command {
	var out string
	var redact bool
	cmd := &cobra.Command{
		Use:   "render <shell|dockerfile|cloud-init>",
		Short: "Render a shell script, a Dockerfile with build context, or cloud-init user-data",
		Long: "render shell       prints the script (or writes it with -o FILE|DIR/)\n" +
			"render dockerfile  writes Dockerfile and build context to -o DIR\n" +
			"render cloud-init  writes user-data, meta-data (and seed.iso if an ISO tool is installed) to -o DIR",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"shell", "dockerfile", "cloud-init"},
		RunE: func(cmd *cobra.Command, a []string) error {
			kind := a[0]
			switch kind {
			case "shell", "dockerfile", "cloud-init":
			default:
				return fmt.Errorf("unknown renderer %q (shell, dockerfile, cloud-init); use `osb build` for images", kind)
			}
			plan, err := g.evaluate(kind, true)
			if err != nil {
				return err
			}
			switch kind {
			case "shell":
				script := shell.Render(plan.ShellDoc(Version, commandLine(plan)))
				if out == "" {
					if redact || term.IsTerminal(int(os.Stdout.Fd())) {
						script = plan.Secrets.String(script)
					}
					fmt.Print(script)
					return nil
				}
				p := out
				if strings.HasSuffix(out, "/") || isDir(out) {
					name := plan.Config.Name
					if name == "" {
						name = "osb"
					}
					p = filepath.Join(out, fmt.Sprintf("%s-%s.sh", name, plan.Flavor.Name))
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return err
				}
				mode := os.FileMode(0o755)
				if !plan.Secrets.Empty() && plan.Secrets.Contains(script) {
					mode = 0o700
					warnf("%s contains secret values", p)
				}
				if err := os.WriteFile(p, []byte(script), mode); err != nil {
					return err
				}
				_ = os.Chmod(p, mode)
				info("wrote %s", p)
			case "dockerfile":
				if out == "" {
					out = defaultOutDir(plan, "dockerfile")
				}
				res, err := dockerfile.Render(plan, out, dockerfile.Options{Version: Version, Command: commandLine(plan)})
				if err != nil {
					return err
				}
				info("wrote %s", filepath.Join(out, "Dockerfile"))
				build := "docker build"
				if len(res.BuildArgs) > 0 {
					build += " " + strings.Join(res.BuildArgs, " ")
					var envs []string
					for _, s := range res.Secrets {
						envs = append(envs, s.Env)
					}
					info("secrets are read from the environment at build time: %s", strings.Join(envs, ", "))
				}
				info("build with: %s -t IMAGE %s", build, out)
			case "cloud-init":
				if out == "" {
					out = defaultOutDir(plan, "cloud-init")
				}
				res, err := cloudinit.Render(plan, cloudinit.Options{Version: Version, Command: commandLine(plan)})
				if err != nil {
					return err
				}
				files, err := cloudinit.WriteFiles(res, out, !plan.Secrets.Empty())
				if err != nil {
					return err
				}
				if !plan.Secrets.Empty() {
					warnf("cloud-init user-data contains secret values in plain text")
				}
				for _, f := range files {
					info("wrote %s", f)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "output file or directory")
	cmd.Flags().BoolVar(&redact, "redact", false, "redact secrets when printing the script")
	return cmd
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func buildCmd(g *globalOpts) *cobra.Command {
	var out string
	var keep bool
	cmd := &cobra.Command{
		Use:   "build <oci|wsl|qcow2|ova>",
		Short: "Build a container image, WSL distribution or VM image",
		Long: "build oci    builds a container image with docker (BuildKit) or podman\n" +
			"build wsl    builds a WSL 2 distribution archive (.wsl) from a container image\n" +
			"build qcow2  boots the flavor's cloud image with cloud-init in QEMU/libvirt and saves the disk\n" +
			"build ova    builds a qcow2 image and converts it into an OVA appliance",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"oci", "wsl", "qcow2", "ova"},
		RunE: func(cmd *cobra.Command, a []string) error {
			kind := a[0]
			fn := map[string]func(*build.Context) (*build.Output, error){
				"oci": build.OCI, "wsl": build.WSL, "qcow2": build.QCOW2, "ova": build.OVA,
			}[kind]
			if fn == nil {
				return fmt.Errorf("unknown build target %q (oci, wsl, qcow2, ova); use `osb render` for scripts", kind)
			}
			plan, err := g.evaluate(kind, true)
			if err != nil {
				return err
			}
			if out == "" {
				out = defaultOutDir(plan, kind)
			}
			ctx := &build.Context{Plan: plan, OutDir: out, Version: Version, Command: commandLine(plan), Verbose: g.verbose, KeepWorkDir: keep,
				Log: func(format string, a ...any) { info("==> "+format, a...) }}
			res, err := fn(ctx)
			if err != nil {
				return err
			}
			for _, img := range res.Images {
				info("image: %s", img)
			}
			for _, f := range res.Files {
				info("wrote %s", f)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "output directory")
	cmd.Flags().BoolVar(&keep, "keep", false, "keep intermediate files")
	return cmd
}

func applyCmd(g *globalOpts) *cobra.Command {
	var opts apply.Options
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Show the plan and configure the running system after confirmation",
		Long: "Detects the flavor from /etc/os-release (unless --flavor is given), prints the steps\n" +
			"that will run and asks for confirmation before executing the generated script as root.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.flavor == "" || g.flavor == "auto" {
				cfg, _, err := loadConfig(g.configFile(), false)
				if err != nil {
					return err
				}
				fl, err := apply.DetectFlavor(cfg, "/etc/os-release")
				if err != nil {
					return err
				}
				g.flavor = fl
				info("detected flavor: %s", fl)
			}
			plan, err := g.evaluate("apply", true)
			if err != nil {
				return err
			}
			opts.Version = Version
			opts.Command = commandLine(plan)
			return apply.Run(plan, opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&opts.ShowScript, "show-script", false, "print the full script (secrets redacted) before asking")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "only print the plan")
	return cmd
}

func actionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List and describe built-in and extra actions",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List built-in and extra actions",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			cat := ""
			for _, a := range actions.All() {
				if a.Category != cat {
					if cat != "" {
						fmt.Fprintln(w)
					}
					cat = a.Category
					fmt.Fprintf(w, "%s:\n", strings.ToUpper(cat))
				}
				fmt.Fprintf(w, "  %s\t%s\n", a.Name, firstSentence(a.Description))
			}
			for _, g := range library.Groups() {
				fmt.Fprintf(w, "\nEXTRA %s:\n", strings.ToUpper(g.Name))
				for _, a := range g.Actions {
					fmt.Fprintf(w, "  %s\t%s\n", a.Name, firstSentence(strings.TrimSpace(a.Description)))
				}
			}
			w.Flush()
		},
	}, &cobra.Command{
		Use:   "show <action>",
		Short: "Show arguments and an example of an action",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			if act, ok := actions.Get(a[0]); ok {
				fmt.Print(docs.ActionText(act))
				return nil
			}
			if def := library.Get(a[0]); def != nil {
				fmt.Print(docs.ExtraActionText(def))
				return nil
			}
			return fmt.Errorf("unknown action %q", a[0])
		},
	}, &cobra.Command{
		Use:   "source <action|file>",
		Short: "Print the YAML source of an extra action's library file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			file := strings.TrimSuffix(a[0], ".yaml") + ".yaml"
			if g := library.GroupOf(a[0]); g != "" {
				file = g + ".yaml"
			}
			data, err := library.File(file)
			if err != nil {
				return fmt.Errorf("%q is neither an extra action nor a library file (%s)", a[0], strings.Join(library.FileNames(), ", "))
			}
			os.Stdout.Write(data)
			return nil
		},
	})
	return cmd
}

// firstSentence returns s up to the first ". " followed by an upper-case letter
// (so "e.g. foo" does not end the sentence).
func firstSentence(s string) string {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '.' && s[i+1] == ' ' && s[i+2] >= 'A' && s[i+2] <= 'Z' {
			return s[:i+1]
		}
	}
	return s
}

func flavorsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "flavors",
		Short: "List built-in flavors",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tFAMILY\tBASE IMAGE\tDESCRIPTION")
			for _, n := range flavor.BuiltinNames() {
				f, _ := flavor.Builtin(n)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Name, f.Family, f.BaseImage, f.Description)
			}
			w.Flush()
		},
	}
}

func targetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "targets",
		Short: "List targets",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCOMMAND\tRENDERER\tDESCRIPTION")
			for _, t := range target.All() {
				c := map[string]string{"render": "osb render " + t.Name, "build": "osb build " + t.Name, "live": "osb apply"}[t.Kind]
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, c, t.Renderer, t.Description)
			}
			w.Flush()
		},
	}
}

func schemaCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON schema of the configuration format",
		Long:  "Print the JSON schema. Use it in editors with yaml-language-server:\n  # yaml-language-server: $schema=./osb.schema.json",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if out != "" {
				return os.WriteFile(out, schema.JSON(), 0o644)
			}
			_, err := os.Stdout.Write(schema.JSON())
			return err
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "write to a file")
	return cmd
}

func testCmd(g *globalOpts) *cobra.Command {
	var opts testrunner.Options
	cmd := &cobra.Command{
		Use:   "test [paths...]",
		Short: "Run *.test.yaml library tests",
		Long: "Finds *.test.yaml files (recursively in directories, default .) and runs their test cases.\n" +
			"Each test file imports the library file next to it (foo.test.yaml -> foo.yaml).",
		RunE: func(_ *cobra.Command, a []string) error {
			if len(a) == 0 {
				a = []string{"."}
			}
			opts.Verbose = g.verbose
			opts.Version = Version
			res, err := testrunner.Run(a, opts)
			if err != nil {
				return err
			}
			if res.Failed > 0 {
				return fmt.Errorf("%d of %d tests failed", res.Failed, res.Total)
			}
			if res.Total == 0 {
				return fmt.Errorf("no tests found")
			}
			fmt.Printf("ok: %d tests passed\n", res.Total)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.Update, "update", false, "update golden files")
	cmd.Flags().BoolVar(&opts.Run, "run", false, "also run tests marked run: true in containers (needs docker)")
	return cmd
}

func importsCmd(g *globalOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "imports", Short: "Manage git imports and the lockfile"}
	cmd.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Resolve git refs again and rewrite osb.lock",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			_, res, err := loadConfig(g.configFile(), true)
			if err != nil {
				return err
			}
			printLock(res.Entries())
			return nil
		},
	}, &cobra.Command{
		Use:   "list",
		Short: "List all files the configuration consists of and the locked git imports",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, res, err := loadConfig(g.configFile(), false)
			if err != nil {
				return err
			}
			for _, s := range cfg.Sources {
				fmt.Println(config.DisplayPath(s))
			}
			printLock(res.Entries())
			return nil
		},
	})
	return cmd
}

func printLock(entries map[string]imports.LockEntry) {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s -> %s\n", k, entries[k].Commit)
	}
}

func docsCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate the action reference (Markdown)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if out == "" {
				fmt.Print(docs.ActionsMarkdown())
				return nil
			}
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			files := map[string]string{
				"actions.md": docs.ActionsMarkdown(),
				"cli.md":     docs.CLIMarkdown(newRoot()),
			}
			for name, content := range files {
				p := filepath.Join(out, name)
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					return err
				}
				info("wrote %s", p)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "output directory")
	return cmd
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [dir]",
		Short: "Create an example osb.yaml and the JSON schema",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			dir := "."
			if len(a) == 1 {
				dir = a[0]
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			p := filepath.Join(dir, "osb.yaml")
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("%s already exists", p)
			}
			if err := os.WriteFile(p, []byte(docs.InitTemplate), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "osb.schema.json"), schema.JSON(), 0o644); err != nil {
				return err
			}
			info("wrote %s and %s", p, filepath.Join(dir, "osb.schema.json"))
			info("next: osb validate -f %s && osb render shell -f %s --flavor ubuntu", p, p)
			return nil
		},
	}
}
