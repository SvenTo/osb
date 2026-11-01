// Package expr evaluates JavaScript expressions and ${{ }} templates.
package expr

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"go.yaml.in/yaml/v3"

	"github.com/SvenTo/osb/internal/vfs"
)

// Engine is a JavaScript runtime with osb's helper functions. An Engine is
// not safe for concurrent use.
type Engine struct {
	vm  *goja.Runtime
	dir string
}

// New creates an engine. dir is used to resolve file() paths.
func New(dir string) *Engine {
	e := &Engine{vm: goja.New(), dir: dir}
	e.vm.SetFieldNameMapper(goja.UncapFieldNameMapper())
	e.install()
	return e
}

// SetDir changes the directory used by file().
func (e *Engine) SetDir(dir string) { e.dir = dir }

// Set defines a global variable.
func (e *Engine) Set(name string, value any) {
	_ = e.vm.Set(name, value)
}

// Get returns a global variable as an exported Go value.
func (e *Engine) Get(name string) any {
	v := e.vm.Get(name)
	if v == nil {
		return nil
	}
	if _, isFn := goja.AssertFunction(v); isFn {
		return nil
	}
	return Export(v)
}

// GlobalNames returns the names of all global variables.
func (e *Engine) GlobalNames() map[string]bool {
	out := map[string]bool{}
	for _, k := range e.vm.GlobalObject().Keys() {
		out[k] = true
	}
	return out
}

func (e *Engine) install() {
	e.Set("shell", func(v goja.Value) string { return ShellQuote(Stringify(Export(v))) })
	e.Set("json", func(v goja.Value) (string, error) {
		b, err := json.Marshal(Export(v))
		return string(b), err
	})
	e.Set("yaml", func(v goja.Value) (string, error) {
		b, err := yaml.Marshal(Export(v))
		return strings.TrimSuffix(string(b), "\n"), err
	})
	e.Set("b64", func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) })
	e.Set("unb64", func(s string) (string, error) {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		return string(b), err
	})
	e.Set("sha256", func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	})
	e.Set("file", func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(e.dir, p)
		}
		b, err := vfs.Current.ReadFile(p)
		return string(b), err
	})
	e.Set("env", func(name string, def goja.Value) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if def == nil || goja.IsUndefined(def) {
			return ""
		}
		return def.String()
	})
	e.Set("indent", func(s string, n int) string {
		pad := strings.Repeat(" ", n)
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			if l != "" {
				lines[i] = pad + l
			}
		}
		return strings.Join(lines, "\n")
	})
	e.Set("fail", func(msg string) error { return errors.New(msg) })
}

// Run executes a script (statements). Global variables it declares remain visible.
func (e *Engine) Run(script string) error {
	_, err := e.vm.RunString(script)
	return wrapErr(err)
}

// Eval evaluates an expression and returns an exported Go value.
func (e *Engine) Eval(expression string) (any, error) {
	v, err := e.vm.RunString("(" + expression + "\n)")
	if err != nil {
		return nil, wrapErr(err)
	}
	return Export(v), nil
}

// Truthy evaluates an expression and converts the result to a boolean.
// An expression may also be written as a single ${{ }} template.
func (e *Engine) Truthy(expression string) (bool, error) {
	expression = strings.TrimSpace(expression)
	if inner, ok := singleTemplate(expression); ok {
		expression = inner
	}
	v, err := e.vm.RunString("(" + expression + "\n)")
	if err != nil {
		return false, wrapErr(err)
	}
	return v.ToBoolean(), nil
}

func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg := ex.Value().String()
		for _, p := range []string{"GoError: ", "Error: "} {
			msg = strings.TrimPrefix(msg, p)
		}
		return errors.New(msg)
	}
	var se *goja.CompilerSyntaxError
	if errors.As(err, &se) {
		return fmt.Errorf("syntax error: %s", se.Message)
	}
	return err
}

// Export converts a JS value into plain Go values.
func Export(v goja.Value) any {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return normalize(v.Export())
}

func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = normalize(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = normalize(vv)
		}
		return out
	case []string:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = vv
		}
		return out
	case int:
		return int64(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1<<53 {
			return int64(t)
		}
		return t
	}
	return v
}

// Stringify converts a value to its textual template form.
func Stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// ShellQuote quotes s for POSIX sh.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
