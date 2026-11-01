package shell

import (
	"regexp"
	"strings"

	"github.com/SvenTo/osb/internal/flavor"
)

// helper is a function or global variable of the helper library.
type helper struct {
	name string
	text string // definition including its leading comment
	fn   bool
}

// Plain versions of the output helpers for scripts that run without the library.
var plainHelpers = map[string]string{
	"log":  "log() { printf '==> %s\\n' \"$*\"; }\n",
	"warn": "warn() { printf 'WARN: %s\\n' \"$*\" >&2; }\n",
	"die":  "die() { printf 'ERROR: %s\\n' \"$*\" >&2; exit 1; }\n",
	"step": "step() { printf '\\n[%s]\\n' \"$*\"; }\n",
}

var (
	funcStart = regexp.MustCompile(`^([a-z_][a-z0-9_]*)\(\) *\{`)
	globalDef = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=`)
)

// parseLibrary splits the library into functions and global variables, in order.
func parseLibrary(lib string) []helper {
	var out []helper
	var comment []string
	lines := strings.Split(lib, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "# ---"):
			comment = nil
		case strings.HasPrefix(l, "# shellcheck"):
			// lint directives are left out of inlined helpers
		case strings.HasPrefix(l, "#"):
			comment = append(comment, l)
		case funcStart.MatchString(l):
			name := funcStart.FindStringSubmatch(l)[1]
			body := []string{l}
			if !strings.HasSuffix(strings.TrimSpace(l), "}") {
				for i+1 < len(lines) {
					i++
					body = append(body, lines[i])
					if lines[i] == "}" {
						break
					}
				}
			}
			text := strings.Join(append(append([]string{}, comment...), body...), "\n") + "\n"
			if p, ok := plainHelpers[name]; ok {
				text = p
			}
			out = append(out, helper{name: name, text: text, fn: true})
			comment = nil
		case globalDef.MatchString(l):
			out = append(out, helper{name: globalDef.FindStringSubmatch(l)[1], text: l + "\n"})
			comment = nil
		default:
			comment = nil
		}
	}
	return out
}

func usesName(text, name string, fn bool) bool {
	if fn {
		// a command: at the start of a line or after ; & | ( ` or a keyword like then/do
		return regexp.MustCompile("(?m)(^|[;&|(`]|\\b(then|do|else|if|!)\\s)\\s*" + name + `(\s|$|;|\)|&|\|)`).MatchString(text)
	}
	return regexp.MustCompile(`\$\{?` + name + `\b|(^|[^A-Za-z0-9_])` + name + `=`).MatchString(text)
}

// Helpers returns the definitions of the library functions (and the global
// variables they use) that script calls, so that it runs without the library.
// It returns "" when the script needs none.
func Helpers(script string, fl *flavor.Flavor) string {
	all := parseLibrary(Library(fl))
	need := map[string]bool{}
	queue := []string{script}
	for len(queue) > 0 {
		text := queue[0]
		queue = queue[1:]
		for _, h := range all {
			if !need[h.name] && usesName(text, h.name, h.fn) {
				need[h.name] = true
				queue = append(queue, h.text)
			}
		}
	}
	var globals, funcs strings.Builder
	for _, h := range all {
		if !need[h.name] {
			continue
		}
		if h.fn {
			funcs.WriteString(h.text)
		} else {
			globals.WriteString(h.text)
		}
	}
	return globals.String() + funcs.String()
}

// PackagesCommand renders a package installation as plain commands for the
// flavor, followed by removing the package caches (for container images).
func PackagesCommand(names []string, fl *flavor.Flavor) string {
	pkgs := strings.Join(quoteAll(names), " ")
	if fl.IsArch() {
		return "pacman -Syu --noconfirm --needed " + pkgs + "\nrm -rf /var/cache/pacman/pkg/*\n"
	}
	opts := ""
	if o, ok := fl.Vars["apt-options"].(string); ok && o != "" {
		opts = o + " "
	}
	return "apt-get update -q\napt-get install -y -q " + opts + pkgs + "\nrm -rf /var/lib/apt/lists/*\n"
}
