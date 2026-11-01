package shell

import (
	"regexp"
	"strings"

	"github.com/SvenTo/osb/internal/flavor"
)

// helper is a function, a global variable or an exported setting of the
// helper library.
type helper struct {
	name    string
	text    string // definition including its leading comment
	fn      bool
	section int  // part of the library (common, apt, pacman)
	export  bool // "export X=..." lines: needed by every function of the section
}

var (
	funcStart = regexp.MustCompile(`^([a-z_][a-z0-9_]*)\(\) *\{`)
	globalDef = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=`)
	exportDef = regexp.MustCompile(`^export ([A-Z_][A-Z0-9_]*)=`)
)

// parseLibrary splits the library into functions and global variables, in order.
func parseLibrary(lib string) []helper {
	var out []helper
	var comment []string
	section := 0
	lines := strings.Split(lib, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "# ---"):
			comment = nil
			section++
		case strings.HasPrefix(l, "# shellcheck"), strings.HasPrefix(l, "# SPDX"):
			// lint directives and the license line are left out
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
			out = append(out, helper{name: name, text: text, fn: true, section: section})
			comment = nil
		case exportDef.MatchString(l):
			out = append(out, helper{name: exportDef.FindStringSubmatch(l)[1], text: l + "\n", section: section, export: true})
			comment = nil
		case globalDef.MatchString(l):
			out = append(out, helper{name: globalDef.FindStringSubmatch(l)[1], text: l + "\n", section: section})
			comment = nil
		default:
			comment = nil
		}
	}
	return out
}

func usesName(text, name string, fn bool) bool {
	if fn {
		// a command: at the start of a line or after ; & | ( { ` or a keyword like then/do
		return regexp.MustCompile("(?m)(^|[;&|({`]|\\b(then|do|else|if|!)\\s)\\s*" + name + `(\s|$|;|\)|&|\|)`).MatchString(text)
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
	sections := map[int]bool{}
	for _, h := range all {
		if h.fn && need[h.name] {
			sections[h.section] = true
		}
	}
	var globals, funcs strings.Builder
	for _, h := range all {
		if !need[h.name] && !(h.export && sections[h.section]) {
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

// Colored replaces the output helpers (log, warn, die, step) in helpers with
// colored versions and adds the terminal detection they need.
func Colored(helpers string) string {
	src, _ := libFS.ReadFile("lib/colors.sh")
	parts := strings.SplitN(string(src), "# --- colored output helpers", 2)
	setup := strings.TrimPrefix(parts[0], parts[0][:strings.Index(parts[0], "\n")+1])
	replaced := false
	for _, h := range parseLibrary("# ---" + parts[1]) {
		if !h.fn {
			continue
		}
		for _, old := range parseLibrary(helpers) {
			if old.fn && old.name == h.name {
				helpers = strings.Replace(helpers, old.text, h.text, 1)
				replaced = true
			}
		}
	}
	if !replaced {
		return helpers
	}
	return setup + helpers
}
