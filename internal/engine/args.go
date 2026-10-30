package engine

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/expr"
	"github.com/SvenTo/osb/internal/redact"
)

// ArgTypes are the supported argument types.
var ArgTypes = []string{"string", "bool", "int", "option", "array", "secret"}

// ArgInput holds raw argument values from the different sources.
type ArgInput struct {
	// Values from an arguments file or a test case (typed YAML values).
	Values map[string]any
	// CLI holds --arg name=value pairs (highest priority).
	CLI map[string]string
	// Env enables OSB_ARG_<NAME> environment variables.
	Env bool
	// Prompt asks for missing required arguments; nil disables prompting.
	Prompt func(def *config.ArgDef) (string, error)
	// Placeholders fills missing required arguments with dummy values (validation).
	Placeholders bool
}

// EnvName returns the environment variable for an argument.
func EnvName(arg string) string {
	return "OSB_ARG_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(arg))
}

// ResolveArgs resolves argument values in definition order. Defaults may use
// ${{ }} expressions referring to earlier arguments.
func ResolveArgs(defs config.ArgDefs, in ArgInput, secrets *redact.Set, dir string) (map[string]any, error) {
	for name := range in.CLI {
		if defs.Get(name) == nil {
			return nil, fmt.Errorf("unknown argument %q (defined: %s)", name, argNames(defs))
		}
	}
	for name := range in.Values {
		if defs.Get(name) == nil {
			return nil, fmt.Errorf("unknown argument %q (defined: %s)", name, argNames(defs))
		}
	}
	out := map[string]any{}
	for _, def := range defs {
		if !validType(def.Type) {
			return nil, def.Pos.Errorf("argument %s: unknown type %q (use %s)", def.Name, def.Type, strings.Join(ArgTypes, ", "))
		}
		var raw any
		found := false
		if v, ok := in.Values[def.Name]; ok {
			raw, found = v, true
		}
		if in.Env {
			if v, ok := os.LookupEnv(EnvName(def.Name)); ok {
				raw, found = v, true
			}
		}
		if v, ok := in.CLI[def.Name]; ok {
			raw, found = v, true
		}
		placeholder := false
		if !found && def.HasDefault() {
			raw, found = def.Default.V, true
			if s, ok := raw.(string); ok && expr.HasTemplate(s) {
				e := expr.New(dir)
				e.Set("args", out)
				v, err := e.Template(s)
				if err != nil {
					return nil, def.Pos.Errorf("argument %s: default: %v", def.Name, err)
				}
				raw = v
			}
		}
		if !found && def.Required {
			switch {
			case in.Prompt != nil:
				v, err := in.Prompt(def)
				if err != nil {
					return nil, err
				}
				raw, found = v, true
			case in.Placeholders:
				raw, found, placeholder = placeholderValue(def), true, true
			default:
				return nil, fmt.Errorf("missing required argument %q (%s); pass --arg %s=... or set %s", def.Name, def.Pos, def.Name, EnvName(def.Name))
			}
		}
		if !found {
			continue
		}
		v, err := convertArg(def, raw, placeholder)
		if err != nil {
			return nil, fmt.Errorf("argument %s: %w", def.Name, err)
		}
		if def.Type == "secret" {
			secrets.Add(expr.Stringify(v))
		}
		out[def.Name] = v
	}
	return out, nil
}

func argNames(defs config.ArgDefs) string {
	if len(defs) == 0 {
		return "none"
	}
	var n []string
	for _, d := range defs {
		n = append(n, d.Name)
	}
	return strings.Join(n, ", ")
}

func validType(t string) bool {
	for _, a := range ArgTypes {
		if a == t {
			return true
		}
	}
	return false
}

func placeholderValue(def *config.ArgDef) any {
	switch def.Type {
	case "bool":
		return false
	case "int":
		return int64(0)
	case "array":
		return []any{}
	case "option":
		if len(def.Options) > 0 {
			return def.Options[0]
		}
	case "secret":
		return "placeholder-secret"
	}
	return "placeholder"
}

func convertArg(def *config.ArgDef, raw any, placeholder bool) (any, error) {
	switch def.Type {
	case "string", "secret":
		switch raw.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("expected a string")
		}
		s := expr.Stringify(raw)
		if def.Pattern != "" && !placeholder {
			re, err := regexp.Compile(def.Pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid pattern: %w", err)
			}
			if !re.MatchString(s) {
				shown := s
				if def.Type == "secret" {
					shown = redact.Mask
				}
				return nil, fmt.Errorf("value %q does not match pattern %s", shown, def.Pattern)
			}
		}
		return s, nil
	case "bool":
		switch t := raw.(type) {
		case bool:
			return t, nil
		case string:
			b, err := strconv.ParseBool(t)
			if err != nil {
				return nil, fmt.Errorf("expected true or false, got %q", t)
			}
			return b, nil
		}
		return nil, fmt.Errorf("expected a boolean")
	case "int":
		switch t := raw.(type) {
		case int64:
			return t, nil
		case float64:
			return int64(t), nil
		case string:
			i, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("expected an integer, got %q", t)
			}
			return i, nil
		}
		return nil, fmt.Errorf("expected an integer")
	case "option":
		s := expr.Stringify(raw)
		for _, o := range def.Options {
			if expr.Stringify(o) == s {
				return o, nil
			}
		}
		var opts []string
		for _, o := range def.Options {
			opts = append(opts, expr.Stringify(o))
		}
		return nil, fmt.Errorf("value %q is not one of: %s", s, strings.Join(opts, ", "))
	case "array":
		switch t := raw.(type) {
		case []any:
			return t, nil
		case string:
			out := []any{}
			for _, p := range strings.Split(t, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out, nil
		}
		return nil, fmt.Errorf("expected a list")
	}
	return raw, nil
}
