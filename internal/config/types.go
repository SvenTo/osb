package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Config is a parsed configuration file, after imports have been merged.
type Config struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Arguments   ArgDefs        `yaml:"arguments"`
	Targets     StringList     `yaml:"targets"`
	Flavors     FlavorConfigs  `yaml:"flavors"`
	Artifacts   map[string]Map `yaml:"artifacts"`
	Imports     []Import       `yaml:"imports"`
	Actions     ActionDefs     `yaml:"actions"`
	Steps       []*Step        `yaml:"steps"`
	Tests       []*TestCase    `yaml:"tests"`

	// File is the absolute path of the root configuration file.
	File string `yaml:"-"`
	// Dir is the directory of File.
	Dir string `yaml:"-"`
	// Sources lists every file that contributed to the config.
	Sources []string `yaml:"-"`
}

// ArgDef defines an input argument.
type ArgDef struct {
	Name        string `yaml:"-"`
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Default     Value  `yaml:"default"`
	Required    bool   `yaml:"required"`
	Pattern     string `yaml:"pattern"`
	Options     []any  `yaml:"-"`
	Pos         Pos    `yaml:"-"`
}

// HasDefault reports whether a default value was given.
func (a *ArgDef) HasDefault() bool { return a.Default.Pos.Line != 0 }

func (a *ArgDef) UnmarshalYAML(n *yaml.Node) error {
	type raw ArgDef
	var r struct {
		raw     `yaml:",inline"`
		Options Value `yaml:"options"`
	}
	if err := n.Decode(&r); err != nil {
		return err
	}
	*a = ArgDef(r.raw)
	if l, ok := r.Options.V.([]any); ok {
		a.Options = l
	}
	if a.Type == "" {
		a.Type = "string"
	}
	a.Pos.Line = n.Line
	return nil
}

// ArgDefs is an ordered list of argument definitions, written as a mapping.
type ArgDefs []*ArgDef

func (d *ArgDefs) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		if n.ShortTag() == "!!null" {
			return nil
		}
		return fmt.Errorf("line %d: arguments must be a mapping of name to definition", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		def := &ArgDef{}
		if n.Content[i+1].ShortTag() != "!!null" {
			if err := n.Content[i+1].Decode(def); err != nil {
				return err
			}
		} else {
			def.Type = "string"
		}
		def.Name = n.Content[i].Value
		def.Pos.Line = n.Content[i].Line
		*d = append(*d, def)
	}
	return nil
}

// Get returns the definition with the given name.
func (d ArgDefs) Get(name string) *ArgDef {
	for _, a := range d {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// FlavorConfig customizes or defines a flavor.
type FlavorConfig struct {
	Name       string `yaml:"-"`
	Enabled    *bool  `yaml:"enabled"`
	BasedOn    string `yaml:"based-on"`
	BaseImage  string `yaml:"base-image"`
	CloudImage string `yaml:"cloud-image"`
	Vars       Map    `yaml:"vars"`
	Pos        Pos    `yaml:"-"`
}

// IsEnabled reports whether the flavor is enabled (default true).
func (f *FlavorConfig) IsEnabled() bool { return f.Enabled == nil || *f.Enabled }

// FlavorConfigs is an ordered list of flavor configurations, written as a mapping.
type FlavorConfigs []*FlavorConfig

func (f *FlavorConfigs) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		if n.ShortTag() == "!!null" {
			return nil
		}
		return fmt.Errorf("line %d: flavors must be a mapping", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		fc := &FlavorConfig{}
		if n.Content[i+1].ShortTag() != "!!null" {
			if err := n.Content[i+1].Decode(fc); err != nil {
				return err
			}
		}
		fc.Name = n.Content[i].Value
		fc.Pos.Line = n.Content[i].Line
		*f = append(*f, fc)
	}
	return nil
}

// Get returns the configuration for a flavor or nil.
func (f FlavorConfigs) Get(name string) *FlavorConfig {
	for _, fc := range f {
		if fc.Name == name {
			return fc
		}
	}
	return nil
}

// Import is a filesystem glob or a git source.
type Import struct {
	Path string `yaml:"path"`
	Git  string `yaml:"git"`
	Ref  string `yaml:"ref"`
	Pos  Pos    `yaml:"-"`
}

func (i *Import) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		i.Path = n.Value
	} else {
		type raw Import
		if err := n.Decode((*raw)(i)); err != nil {
			return err
		}
	}
	i.Pos.Line = n.Line
	return nil
}

func (i Import) String() string {
	if i.Git != "" {
		s := i.Git
		if i.Ref != "" {
			s += "@" + i.Ref
		}
		if i.Path != "" {
			s += "//" + i.Path
		}
		return s
	}
	return i.Path
}

// ActionDef is a custom action composed of steps.
type ActionDef struct {
	Name        string     `yaml:"-"`
	Description string     `yaml:"description"`
	Arguments   ArgDefs    `yaml:"arguments"`
	Output      Map        `yaml:"output"`
	Flavors     StringList `yaml:"flavors"`
	Steps       []*Step    `yaml:"steps"`
	Pos         Pos        `yaml:"-"`
	// Dir is the directory of the file that defined the action.
	Dir string `yaml:"-"`
}

// ActionDefs is an ordered list of custom actions, written as a mapping.
type ActionDefs []*ActionDef

func (a *ActionDefs) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		if n.ShortTag() == "!!null" {
			return nil
		}
		return fmt.Errorf("line %d: actions must be a mapping", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		def := &ActionDef{}
		if err := n.Content[i+1].Decode(def); err != nil {
			return err
		}
		def.Name = n.Content[i].Value
		def.Pos.Line = n.Content[i].Line
		*a = append(*a, def)
	}
	return nil
}

// Get returns the custom action with the given name.
func (a ActionDefs) Get(name string) *ActionDef {
	for _, d := range a {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// Step is one entry of a steps list.
type Step struct {
	Name           string     `yaml:"name"`
	Title          string     `yaml:"title"`
	Action         string     `yaml:"action"`
	If             Expr       `yaml:"if"`
	Flavors        StringList `yaml:"flavors"`
	Targets        StringList `yaml:"targets"`
	OnError        string     `yaml:"on-error"`
	PreScript      string     `yaml:"pre-script"`
	PreValidation  string     `yaml:"pre-validation"`
	PostValidation string     `yaml:"post-validation"`
	Requires       Requires   `yaml:"requires"`
	OnlyIf         string     `yaml:"only-if"`
	Unless         string     `yaml:"unless"`
	Creates        string     `yaml:"creates"`
	Foreach        Value      `yaml:"foreach"`
	Args           Map        `yaml:"args"`
	Output         Map        `yaml:"output"`
	Steps          []*Step    `yaml:"steps"`

	Pos Pos `yaml:"-"`
	// Dir is the directory of the file that defined the step; relative
	// source paths are resolved against it.
	Dir string `yaml:"-"`
}

func (s *Step) UnmarshalYAML(n *yaml.Node) error {
	type raw Step
	if err := n.Decode((*raw)(s)); err != nil {
		return err
	}
	s.Pos.Line = n.Line
	return nil
}

// Label returns a human readable description of the step.
func (s *Step) Label() string {
	switch {
	case s.Title != "":
		return s.Title
	case s.Name != "":
		return s.Name
	case s.Action != "":
		return s.Action
	default:
		return "block"
	}
}

// Requires lists run-time requirements of a step.
type Requires struct {
	// Packages is a list of package names or a mapping of flavor name to a
	// list of package names.
	Packages Value `yaml:"packages"`
}

// TestCase is an entry of a *.test.yaml file.
type TestCase struct {
	Name      string     `yaml:"name"`
	Flavor    string     `yaml:"flavor"`
	Target    string     `yaml:"target"`
	Arguments Map        `yaml:"arguments"`
	Steps     []*Step    `yaml:"steps"`
	Expect    TestExpect `yaml:"expect"`
	Run       bool       `yaml:"run"`
	Pos       Pos        `yaml:"-"`
}

func (t *TestCase) UnmarshalYAML(n *yaml.Node) error {
	type raw TestCase
	if err := n.Decode((*raw)(t)); err != nil {
		return err
	}
	t.Pos.Line = n.Line
	return nil
}

// TestExpect holds the assertions of a test case.
type TestExpect struct {
	Contains    StringList `yaml:"contains"`
	NotContains StringList `yaml:"not-contains"`
	Golden      string     `yaml:"golden"`
	Error       string     `yaml:"error"`
	Warnings    StringList `yaml:"warnings"`
}
