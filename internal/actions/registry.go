// Package actions contains the built-in actions.
package actions

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SvenTo/osb/internal/expr"
	"github.com/SvenTo/osb/internal/flavor"
	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/target"
)

// Argument types of built-in actions.
const (
	TString = "string"
	TText   = "text" // multi-line string
	TBool   = "bool"
	TInt    = "int"
	TList   = "list" // list of strings; a single string is accepted
	TMap    = "map"  // mapping of string to scalar
	TMode   = "mode" // octal file mode
	TAny    = "any"
)

// ArgSpec describes an argument of a built-in action.
type ArgSpec struct {
	Name        string
	Type        string
	Description string
	Required    bool
	Default     any
	Enum        []string
}

// Ctx is passed to actions when they build ops.
type Ctx struct {
	Flavor *flavor.Flavor
	Target target.Target
	// Dir resolves relative source paths (the directory of the defining file).
	Dir string
	// Template renders ${{ }} expressions in a string (for template-file).
	Template func(string) (string, error)
	// Warn reports a generation-time warning.
	Warn func(string)
}

// Path resolves a source path relative to the defining file.
func (c *Ctx) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}

// ReadSource reads a source file relative to the defining file.
func (c *Ctx) ReadSource(p string) ([]byte, error) {
	b, err := os.ReadFile(c.Path(p))
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	return b, nil
}

// Action is a built-in action.
type Action struct {
	Name        string
	Category    string
	Description string
	Args        []ArgSpec
	// Requires are packages that must be installed to run the action (generic names).
	Requires []string
	Example  string
	Build    func(c *Ctx, a Args) ([]ops.Op, error)
}

// Arg returns the spec of an argument.
func (a *Action) Arg(name string) *ArgSpec {
	for i := range a.Args {
		if a.Args[i].Name == name {
			return &a.Args[i]
		}
	}
	return nil
}

var registry = map[string]*Action{}

func register(a *Action) {
	if _, dup := registry[a.Name]; dup {
		panic("duplicate action " + a.Name)
	}
	registry[a.Name] = a
}

// Get returns a built-in action.
func Get(name string) (*Action, bool) {
	a, ok := registry[name]
	return a, ok
}

// All returns all built-in actions sorted by category and name.
func All() []*Action {
	out := make([]*Action, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		ci, cj := categoryOrder(out[i].Category), categoryOrder(out[j].Category)
		if ci != cj {
			return ci < cj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Categories in display order.
var Categories = []string{"files", "commands", "users", "packages", "system", "systemd", "control"}

func categoryOrder(c string) int {
	for i, n := range Categories {
		if n == c {
			return i
		}
	}
	return len(Categories)
}

// Args holds coerced argument values.
type Args map[string]any

// Has reports whether an argument was given.
func (a Args) Has(k string) bool {
	v, ok := a[k]
	return ok && v != nil
}

// Str returns a string argument.
func (a Args) Str(k string) string {
	v, ok := a[k]
	if !ok || v == nil {
		return ""
	}
	return expr.Stringify(v)
}

// Bool returns a boolean argument.
func (a Args) Bool(k string) bool {
	b, _ := a[k].(bool)
	return b
}

// Int returns an integer argument.
func (a Args) Int(k string) int64 {
	i, _ := a[k].(int64)
	return i
}

// List returns a list argument.
func (a Args) List(k string) []string {
	l, _ := a[k].([]string)
	return l
}

// KVs returns a map argument as sorted key/value pairs.
func (a Args) KVs(k string) []ops.KV {
	m, _ := a[k].(map[string]string)
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ops.KV, 0, len(keys))
	for _, key := range keys {
		out = append(out, ops.KV{Key: key, Value: m[key]})
	}
	return out
}

// Coerce validates and converts the raw arguments of a step for an action.
func Coerce(a *Action, raw map[string]any) (Args, error) {
	out := Args{}
	for k := range raw {
		if a.Arg(k) == nil {
			var names []string
			for _, s := range a.Args {
				names = append(names, s.Name)
			}
			return nil, fmt.Errorf("action %s has no argument %q (arguments: %s)", a.Name, k, strings.Join(names, ", "))
		}
	}
	for _, spec := range a.Args {
		v, ok := raw[spec.Name]
		if !ok || v == nil {
			if spec.Required {
				return nil, fmt.Errorf("action %s: argument %q is required", a.Name, spec.Name)
			}
			if spec.Default == nil {
				continue
			}
			v = spec.Default
		}
		cv, err := coerce(spec, v)
		if err != nil {
			return nil, fmt.Errorf("action %s: argument %q: %w", a.Name, spec.Name, err)
		}
		if len(spec.Enum) > 0 {
			s := expr.Stringify(cv)
			found := false
			for _, e := range spec.Enum {
				if e == s {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("action %s: argument %q must be one of %s, got %q", a.Name, spec.Name, strings.Join(spec.Enum, ", "), s)
			}
		}
		out[spec.Name] = cv
	}
	return out, nil
}

func coerce(spec ArgSpec, v any) (any, error) {
	switch spec.Type {
	case TString, TText:
		switch v.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("expected a string")
		}
		return expr.Stringify(v), nil
	case TBool:
		switch t := v.(type) {
		case bool:
			return t, nil
		case string:
			b, err := strconv.ParseBool(t)
			if err != nil {
				return nil, fmt.Errorf("expected a boolean, got %q", t)
			}
			return b, nil
		}
		return nil, fmt.Errorf("expected a boolean")
	case TInt:
		switch t := v.(type) {
		case int64:
			return t, nil
		case float64:
			return int64(t), nil
		case string:
			i, err := strconv.ParseInt(t, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("expected an integer, got %q", t)
			}
			return i, nil
		}
		return nil, fmt.Errorf("expected an integer")
	case TMode:
		return Mode(v)
	case TList:
		switch t := v.(type) {
		case []any:
			out := make([]string, 0, len(t))
			for _, e := range t {
				if _, ok := e.(map[string]any); ok {
					return nil, fmt.Errorf("expected a list of strings")
				}
				out = append(out, expr.Stringify(e))
			}
			return out, nil
		case []string:
			return t, nil
		case map[string]any:
			return nil, fmt.Errorf("expected a list of strings")
		default:
			return []string{expr.Stringify(t)}, nil
		}
	case TMap:
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected a mapping")
		}
		out := make(map[string]string, len(m))
		for k, e := range m {
			out[k] = expr.Stringify(e)
		}
		return out, nil
	}
	return v, nil
}

// Mode converts a mode value (0644, "0644", "644", 644) to a 4 digit octal string.
func Mode(v any) (string, error) {
	s := expr.Stringify(v)
	s = strings.TrimPrefix(s, "0o")
	if s == "" {
		return "", nil
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil || n > 07777 {
		return "", fmt.Errorf("invalid file mode %q (use an octal value like \"0644\")", s)
	}
	return fmt.Sprintf("%04o", n), nil
}
