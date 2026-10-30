package expr

import (
	"fmt"
	"strings"
)

const (
	openDelim  = "${{"
	closeDelim = "}}"
)

// HasTemplate reports whether s contains a ${{ }} expression.
func HasTemplate(s string) bool {
	return strings.Contains(s, openDelim)
}

// findClose returns the index of the "}}" closing the expression that starts
// at s[0], skipping JS string literals and nested braces.
func findClose(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'', '"', '`':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		case '{':
			depth++
		case '}':
			if depth == 0 {
				if i+1 < len(s) && s[i+1] == '}' {
					return i
				}
				return -1
			}
			depth--
		}
	}
	return -1
}

// singleTemplate returns the inner expression if s consists of exactly one
// ${{ }} template.
func singleTemplate(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, openDelim) {
		return "", false
	}
	rest := t[len(openDelim):]
	end := findClose(rest)
	if end < 0 || end+len(closeDelim) != len(rest) {
		return "", false
	}
	return strings.TrimSpace(rest[:end]), true
}

// Template renders ${{ }} expressions in s. If s consists of a single
// expression the raw value is returned (which may be a list, map, bool...),
// otherwise the result is a string. "$${{" is an escaped literal "${{".
func (e *Engine) Template(s string) (any, error) {
	if !HasTemplate(s) {
		return s, nil
	}
	if inner, ok := singleTemplate(s); ok && !strings.HasPrefix(strings.TrimSpace(s), "$"+openDelim) {
		v, err := e.Eval(inner)
		if err != nil {
			return nil, fmt.Errorf("in ${{ %s }}: %w", inner, err)
		}
		return v, nil
	}
	var b strings.Builder
	for {
		i := strings.Index(s, openDelim)
		if i < 0 {
			b.WriteString(s)
			break
		}
		if i > 0 && s[i-1] == '$' {
			b.WriteString(s[:i-1])
			b.WriteString(openDelim)
			s = s[i+len(openDelim):]
			continue
		}
		b.WriteString(s[:i])
		rest := s[i+len(openDelim):]
		end := findClose(rest)
		if end < 0 {
			return nil, fmt.Errorf("unterminated ${{ in %q", truncate(s[i:], 40))
		}
		inner := strings.TrimSpace(rest[:end])
		v, err := e.Eval(inner)
		if err != nil {
			return nil, fmt.Errorf("in ${{ %s }}: %w", inner, err)
		}
		b.WriteString(Stringify(v))
		s = rest[end+len(closeDelim):]
	}
	return b.String(), nil
}

// TemplateString renders s and always returns a string.
func (e *Engine) TemplateString(s string) (string, error) {
	v, err := e.Template(s)
	if err != nil {
		return "", err
	}
	return Stringify(v), nil
}

// Render recursively renders all strings in v (maps, lists and scalars).
func (e *Engine) Render(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return e.Template(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			r, err := e.Render(vv)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			r, err := e.Render(vv)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
