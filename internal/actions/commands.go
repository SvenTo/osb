package actions

import (
	"fmt"
	"strings"

	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/render/shell"
)

// SplitCommand splits a command line into words like a POSIX shell would,
// supporting single quotes, double quotes and backslash escapes, without any
// expansion.
func SplitCommand(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("unterminated single quote in %q", s)
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated double quote in %q", s)
			}
			inWord = true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// commandPrefix builds "cd DIR && env K=V as_user USER" style prefixes.
func runWrapped(a Args, argv string) string {
	cmd := argv
	if env := a.KVs("env"); len(env) > 0 {
		var parts []string
		for _, kv := range env {
			parts = append(parts, q(kv.Key+"="+kv.Value))
		}
		cmd = "env " + strings.Join(parts, " ") + " " + cmd
	}
	if u := a.Str("user"); u != "" {
		cmd = "as_user " + q(u) + " " + cmd
	}
	if d := a.Str("dir"); d != "" {
		cmd = "(cd " + q(d) + " && " + cmd + ")"
	}
	return cmd + "\n"
}

func runArgs() []ArgSpec {
	return []ArgSpec{
		{Name: "user", Type: TString, Description: "Run as this user"},
		{Name: "dir", Type: TString, Description: "Working directory"},
		{Name: "env", Type: TMap, Description: "Environment variables"},
	}
}

func init() {
	register(&Action{
		Name: "run-command", Category: "commands",
		Description: "Run a command. By default it is executed directly (no shell): a string is split into words, a list is used as is. With shell: true the string is run by /bin/sh.",
		Args: append([]ArgSpec{
			{Name: "command", Type: TAny, Required: true, Description: "Command line (string) or argv (list)"},
			{Name: "shell", Type: TBool, Default: false, Description: "Run the command with /bin/sh -c"},
		}, runArgs()...),
		Example: "action: run-command\nargs:\n  command: [systemctl, set-default, multi-user.target]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			var argv []string
			switch t := a["command"].(type) {
			case []any:
				for _, e := range t {
					argv = append(argv, fmt.Sprint(e))
				}
			case string:
				if a.Bool("shell") {
					if !a.Has("user") && !a.Has("dir") && !a.Has("env") {
						s := t
						if !strings.HasSuffix(s, "\n") {
							s += "\n"
						}
						return []ops.Op{ops.Shell{Script: s}}, nil
					}
					argv = []string{"sh", "-c", t}
				} else {
					var err error
					if argv, err = SplitCommand(t); err != nil {
						return nil, err
					}
				}
			default:
				return nil, fmt.Errorf("command must be a string or a list")
			}
			if len(argv) == 0 {
				return nil, fmt.Errorf("command is empty")
			}
			quoted := make([]string, len(argv))
			for i, w := range argv {
				quoted[i] = shell.Q(w)
			}
			return []ops.Op{ops.Shell{Script: runWrapped(a, strings.Join(quoted, " "))}}, nil
		},
	})

	register(&Action{
		Name: "run-script", Category: "commands",
		Description: "Run an inline script or a local script file with an interpreter. A /bin/sh script without user, dir and env is inlined into the generated output.",
		Args: append([]ArgSpec{
			{Name: "script", Type: TText, Description: "Inline script"},
			{Name: "source", Type: TString, Description: "Local script file, relative to the configuration file"},
			{Name: "interpreter", Type: TString, Default: "/bin/sh", Description: "Interpreter, e.g. /bin/bash or /usr/bin/python3"},
			{Name: "args", Type: TList, Description: "Arguments passed to the script"},
		}, runArgs()...),
		Example: "action: run-script\nargs:\n  script: |\n    for i in 1 2 3; do echo \"$i\"; done\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			var content []byte
			switch {
			case a.Has("script") && a.Has("source"):
				return nil, fmt.Errorf("use either script or source")
			case a.Has("source"):
				b, err := c.ReadSource(a.Str("source"))
				if err != nil {
					return nil, err
				}
				content = b
			case a.Has("script"):
				content = []byte(a.Str("script"))
			default:
				return nil, fmt.Errorf("script or source is required")
			}
			s := string(content)
			if !strings.HasSuffix(s, "\n") {
				s += "\n"
			}
			interp := a.Str("interpreter")
			if interp == "/bin/sh" && !a.Has("user") && !a.Has("dir") && !a.Has("env") && !a.Has("args") && !strings.HasPrefix(s, "#!") {
				return []ops.Op{ops.Shell{Script: "(\n" + s + ")\n"}}, nil
			}
			words := []string{shell.Q(interp), `"$osb_script"`}
			for _, w := range a.List("args") {
				words = append(words, shell.Q(w))
			}
			var b strings.Builder
			b.WriteString("osb_script=$(mktemp)\n")
			b.WriteString(`write_file "$osb_script" 0755 ` + shell.Heredoc(s))
			b.WriteString(runWrapped(a, strings.Join(words, " ")))
			b.WriteString(`rm -f "$osb_script"` + "\n")
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})
}
