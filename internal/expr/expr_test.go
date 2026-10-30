package expr

import (
	"reflect"
	"testing"
)

func TestTemplate(t *testing.T) {
	e := New(".")
	e.Set("args", map[string]any{"user": "sven", "list": []any{"a", "b"}, "n": int64(3)})
	tests := []struct {
		in   string
		want any
	}{
		{"plain $HOME ${VAR}", "plain $HOME ${VAR}"},
		{"hello ${{ args.user }}!", "hello sven!"},
		{"${{ args.list }}", []any{"a", "b"}},
		{"${{ args.n + 1 }}", int64(4)},
		{"x${{ args.n }}", "x3"},
		{"${{ 1 + 1 }} and ${{ 'a}}b' }}", "2 and a}}b"},
		{"${{ {a: {b: 1}}.a.b }}", int64(1)},
		{"$${{ literal }}", "${{ literal }}"},
		{"${{ shell(\"it's\") }}", `'it'\''s'`},
		{"${{ json(args.list) }}", `["a","b"]`},
		{"${{ args.list }} as text", `["a","b"] as text`},
		{"${{ args.missing }}", nil},
	}
	for _, tt := range tests {
		got, err := e.Template(tt.in)
		if err != nil {
			t.Errorf("Template(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Template(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestTemplateErrors(t *testing.T) {
	e := New(".")
	for _, in := range []string{"${{ nope.x }}", "${{ unterminated", "${{ fail('boom') }}"} {
		if _, err := e.Template(in); err == nil {
			t.Errorf("Template(%q): expected an error", in)
		}
	}
	if _, err := e.Template("${{ fail('boom') }}"); err == nil || err.Error() != "in ${{ fail('boom') }}: boom" {
		t.Errorf("fail(): got %v", err)
	}
}

func TestTruthy(t *testing.T) {
	e := New(".")
	e.Set("args", map[string]any{"name": "sven"})
	for in, want := range map[string]bool{
		`args.name == "sven"`:         true,
		`${{ args.name == "other" }}`: false,
		"true":                        true,
		"args.unset === undefined":    true,
	} {
		got, err := e.Truthy(in)
		if err != nil || got != want {
			t.Errorf("Truthy(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestRunGlobals(t *testing.T) {
	e := New(".")
	if err := e.Run("var id = 'x'; other = 2"); err != nil {
		t.Fatal(err)
	}
	if got := e.Get("id"); got != "x" {
		t.Errorf("id = %v", got)
	}
	if got := e.Get("other"); got != int64(2) {
		t.Errorf("other = %#v", got)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":          "''",
		"simple":    "simple",
		"/a/b-c.d":  "/a/b-c.d",
		"two words": "'two words'",
		"it's":      `'it'\''s'`,
		"$HOME":     "'$HOME'",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
