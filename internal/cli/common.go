package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/term"

	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/imports"
	"github.com/SvenTo/osb/internal/schema"
)

// Version is set at build time.
var Version = "dev"

type globalOpts struct {
	file     string
	flavor   string
	args     []string
	argsFile string
	noPrompt bool
	verbose  bool
}

func (g *globalOpts) configFile() string {
	if g.file != "" {
		return g.file
	}
	for _, n := range []string{"osb.yaml", "osb.yml"} {
		if _, err := os.Stat(n); err == nil {
			return n
		}
	}
	return "osb.yaml"
}

func info(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

func warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", a...)
}

// loadConfig loads a configuration with schema validation and git imports.
func loadConfig(file string, updateImports bool) (*config.Config, *imports.Resolver, error) {
	res := imports.NewResolver(file, updateImports)
	res.Log = func(s string) { info("%s", s) }
	cfg, err := config.Load(file, config.LoadOptions{Validate: schema.Validate, ResolveGit: res.Resolve})
	if err != nil {
		return nil, nil, err
	}
	if err := res.Save(updateImports); err != nil {
		return nil, nil, err
	}
	return cfg, res, nil
}

func (g *globalOpts) argInput(interactive bool) (engine.ArgInput, error) {
	in := engine.ArgInput{CLI: map[string]string{}, Env: true}
	for _, a := range g.args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return in, fmt.Errorf("--arg %q: expected name=value", a)
		}
		in.CLI[k] = v
	}
	if g.argsFile != "" {
		b, err := os.ReadFile(g.argsFile)
		if err != nil {
			return in, err
		}
		var n yaml.Node
		if err := yaml.Unmarshal(b, &n); err != nil {
			return in, fmt.Errorf("%s: %w", g.argsFile, err)
		}
		v, err := config.NodeToValue(&n)
		if err != nil {
			return in, err
		}
		if v != nil {
			m, ok := v.(map[string]any)
			if !ok {
				return in, fmt.Errorf("%s: expected a mapping of argument names to values", g.argsFile)
			}
			in.Values = m
		}
	}
	if interactive && !g.noPrompt && term.IsTerminal(int(os.Stdin.Fd())) {
		in.Prompt = prompt
	}
	return in, nil
}

var stdin = bufio.NewReader(os.Stdin)

func prompt(def *config.ArgDef) (string, error) {
	label := def.Name
	if def.Description != "" {
		label += " (" + def.Description + ")"
	}
	if len(def.Options) > 0 {
		var o []string
		for _, v := range def.Options {
			o = append(o, fmt.Sprint(v))
		}
		label += " [" + strings.Join(o, "|") + "]"
	}
	fmt.Fprintf(os.Stderr, "%s: ", label)
	if def.Type == "secret" {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := stdin.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// evaluate loads and evaluates the configuration for a target.
func (g *globalOpts) evaluate(targetName string, interactive bool) (*engine.Plan, error) {
	cfg, _, err := loadConfig(g.configFile(), false)
	if err != nil {
		return nil, err
	}
	in, err := g.argInput(interactive)
	if err != nil {
		return nil, err
	}
	plan, err := engine.Evaluate(cfg, engine.Options{Flavor: g.flavor, Target: targetName, Args: in})
	if err != nil {
		return nil, err
	}
	for _, w := range plan.Warnings {
		warnf("%s", plan.Secrets.String(w))
	}
	return plan, nil
}

// commandLine reproduces the invocation for generated file headers with
// secret values redacted.
func commandLine(plan *engine.Plan) string {
	return plan.Secrets.String("osb " + strings.Join(os.Args[1:], " "))
}

func defaultOutDir(plan *engine.Plan, kind string) string {
	name := plan.Config.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(plan.Config.File), filepath.Ext(plan.Config.File))
	}
	return filepath.Join("out", fmt.Sprintf("%s-%s-%s", name, plan.Flavor.Name, kind))
}
