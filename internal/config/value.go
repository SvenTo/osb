package config

import (
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Pos is a position in a configuration file.
type Pos struct {
	File string
	Line int
}

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("line %d", p.Line)
	}
	if p.Line == 0 {
		return p.File
	}
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// Errorf creates an error prefixed with the position.
func (p Pos) Errorf(format string, a ...any) error {
	return fmt.Errorf("%s: %s", p, fmt.Sprintf(format, a...))
}

// NodeToValue converts a YAML node into plain Go values (map[string]any,
// []any, string, int64, float64, bool, nil).
//
// Integers written with a leading zero (e.g. file modes like 0644) are kept as
// strings so they are not silently converted from octal.
func NodeToValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return NodeToValue(n.Content[0])
	case yaml.AliasNode:
		return NodeToValue(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == "<<" && k.Tag == "!!merge" {
				merged, err := NodeToValue(v)
				if err != nil {
					return nil, err
				}
				if mm, ok := merged.(map[string]any); ok {
					for mk, mv := range mm {
						if _, exists := m[mk]; !exists {
							m[mk] = mv
						}
					}
				}
				continue
			}
			val, err := NodeToValue(v)
			if err != nil {
				return nil, err
			}
			m[k.Value] = val
		}
		return m, nil
	case yaml.SequenceNode:
		l := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := NodeToValue(c)
			if err != nil {
				return nil, err
			}
			l = append(l, v)
		}
		return l, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int":
			raw := n.Value
			if len(raw) > 1 && raw[0] == '0' && !strings.HasPrefix(raw, "0x") && !strings.HasPrefix(raw, "0o") {
				return raw, nil
			}
			var i int64
			if err := n.Decode(&i); err != nil {
				return raw, nil
			}
			return i, nil
		case "!!float":
			f, err := strconv.ParseFloat(n.Value, 64)
			if err != nil {
				var ff float64
				if err := n.Decode(&ff); err != nil {
					return n.Value, nil
				}
				return ff, nil
			}
			return f, nil
		default:
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}

// Value holds an arbitrary YAML value decoded with NodeToValue.
type Value struct {
	V   any
	Pos Pos
}

func (v *Value) UnmarshalYAML(n *yaml.Node) error {
	val, err := NodeToValue(n)
	if err != nil {
		return err
	}
	v.V = val
	v.Pos.Line = n.Line
	return nil
}

// Map is a string-keyed map decoded with NodeToValue.
type Map map[string]any

func (m *Map) UnmarshalYAML(n *yaml.Node) error {
	val, err := NodeToValue(n)
	if err != nil {
		return err
	}
	if val == nil {
		*m = Map{}
		return nil
	}
	mm, ok := val.(map[string]any)
	if !ok {
		return fmt.Errorf("line %d: expected a mapping", n.Line)
	}
	*m = mm
	return nil
}

// Expr is a scalar holding an expression; booleans and numbers are kept as
// their textual form.
type Expr string

func (e *Expr) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: expected a scalar expression", n.Line)
	}
	*e = Expr(n.Value)
	return nil
}

// StringList accepts a single string or a list of strings.
type StringList []string

func (s *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.ShortTag() == "!!null" {
			*s = nil
			return nil
		}
		*s = StringList{n.Value}
		return nil
	case yaml.SequenceNode:
		out := make(StringList, 0, len(n.Content))
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: expected a string", c.Line)
			}
			out = append(out, c.Value)
		}
		*s = out
		return nil
	}
	return fmt.Errorf("line %d: expected a string or a list of strings", n.Line)
}
