package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// LoadOptions configures Load.
type LoadOptions struct {
	// Validate is called for every file with the parsed document before it is
	// decoded. It is typically the JSON schema validator.
	Validate func(file string, doc *yaml.Node, value any) error
	// ResolveGit returns a local directory containing the checkout of a git import.
	ResolveGit func(imp Import, from string) (string, error)
	// ExtraImports are prepended to the imports of the root file.
	ExtraImports []Import
}

// Load reads a configuration file and all of its imports.
func Load(path string, opts LoadOptions) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	l := &loader{opts: opts, seen: map[string]bool{}}
	cfg, err := l.loadFile(abs)
	if err != nil {
		return nil, err
	}
	cfg.File = abs
	cfg.Dir = filepath.Dir(abs)
	cfg.Imports = append(append([]Import{}, opts.ExtraImports...), cfg.Imports...)
	if err := l.processImports(cfg, cfg, cfg.Dir); err != nil {
		return nil, err
	}
	cfg.Sources = l.order
	return cfg, nil
}

// Parse parses a single document without resolving imports.
func Parse(file string, data []byte, opts LoadOptions) (*Config, error) {
	l := &loader{opts: opts, seen: map[string]bool{}}
	return l.parse(file, data)
}

type loader struct {
	opts  LoadOptions
	seen  map[string]bool
	order []string
}

// DisplayPath returns path relative to the working directory when possible.
func DisplayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func (l *loader) loadFile(abs string) (*Config, error) {
	l.seen[abs] = true
	l.order = append(l.order, abs)
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	return l.parse(abs, data)
}

func (l *loader) parse(abs string, data []byte) (*Config, error) {
	disp := DisplayPath(abs)
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		if err.Error() == "EOF" {
			return &Config{File: abs, Dir: filepath.Dir(abs)}, nil
		}
		return nil, fmt.Errorf("%s: %w", disp, err)
	}
	if l.opts.Validate != nil {
		val, err := NodeToValue(&doc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", disp, err)
		}
		if err := l.opts.Validate(disp, &doc, val); err != nil {
			return nil, err
		}
	}
	cfg := &Config{}
	if err := doc.Decode(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", disp, err)
	}
	cfg.File = abs
	cfg.Dir = filepath.Dir(abs)
	setFile(cfg, disp, cfg.Dir)
	return cfg, nil
}

func setFile(cfg *Config, disp, dir string) {
	for _, a := range cfg.Arguments {
		a.Pos.File = disp
	}
	for _, f := range cfg.Flavors {
		f.Pos.File = disp
	}
	for i := range cfg.Imports {
		cfg.Imports[i].Pos.File = disp
	}
	for _, a := range cfg.Actions {
		a.Pos.File = disp
		a.Dir = dir
		for _, arg := range a.Arguments {
			arg.Pos.File = disp
		}
		setStepFile(a.Steps, disp, dir)
	}
	setStepFile(cfg.Steps, disp, dir)
	for _, t := range cfg.Tests {
		t.Pos.File = disp
		setStepFile(t.Steps, disp, dir)
	}
}

func setStepFile(steps []*Step, disp, dir string) {
	for _, s := range steps {
		s.Pos.File = disp
		s.Dir = dir
		setStepFile(s.Steps, disp, dir)
	}
}

func (l *loader) processImports(root, cfg *Config, dir string) error {
	var importedSteps []*Step
	for _, imp := range cfg.Imports {
		files, err := l.resolveImport(imp, dir)
		if err != nil {
			return imp.Pos.Errorf("import %q: %v", imp.String(), err)
		}
		for _, f := range files {
			if l.seen[f] {
				continue
			}
			sub, err := l.loadFile(f)
			if err != nil {
				return err
			}
			if err := l.processImports(root, sub, filepath.Dir(f)); err != nil {
				return err
			}
			if err := merge(root, sub); err != nil {
				return err
			}
			importedSteps = append(importedSteps, sub.Steps...)
		}
	}
	if cfg == root {
		root.Steps = append(importedSteps, root.Steps...)
	} else {
		cfg.Steps = append(importedSteps, cfg.Steps...)
	}
	return nil
}

func (l *loader) resolveImport(imp Import, dir string) ([]string, error) {
	base := dir
	pattern := imp.Path
	if imp.Git != "" {
		if l.opts.ResolveGit == nil {
			return nil, fmt.Errorf("git imports are not supported here")
		}
		checkout, err := l.opts.ResolveGit(imp, dir)
		if err != nil {
			return nil, err
		}
		base = checkout
		if pattern == "" {
			pattern = "*.yaml"
		}
	}
	if pattern == "" {
		return nil, fmt.Errorf("empty import path")
	}
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(base, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, m := range matches {
		if strings.HasSuffix(m, ".test.yaml") || strings.HasSuffix(m, ".test.yml") {
			continue
		}
		if st, err := os.Stat(m); err == nil && !st.IsDir() {
			files = append(files, m)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files match %s", DisplayPath(pattern))
	}
	sort.Strings(files)
	return files, nil
}

// merge adds the definitions of an imported config to root. Steps are merged
// by processImports to keep import order.
func merge(root, sub *Config) error {
	for _, a := range sub.Actions {
		if existing := root.Actions.Get(a.Name); existing != nil {
			if existing.Pos == a.Pos {
				continue
			}
			return a.Pos.Errorf("action %q is already defined at %s", a.Name, existing.Pos)
		}
		root.Actions = append(root.Actions, a)
	}
	for _, a := range sub.Arguments {
		if root.Arguments.Get(a.Name) == nil {
			root.Arguments = append(root.Arguments, a)
		}
	}
	for _, f := range sub.Flavors {
		if root.Flavors.Get(f.Name) == nil {
			root.Flavors = append(root.Flavors, f)
		}
	}
	for k, v := range sub.Artifacts {
		if root.Artifacts == nil {
			root.Artifacts = map[string]Map{}
		}
		dst, ok := root.Artifacts[k]
		if !ok {
			dst = Map{}
			root.Artifacts[k] = dst
		}
		for kk, vv := range v {
			if _, exists := dst[kk]; !exists {
				dst[kk] = vv
			}
		}
	}
	return nil
}
