// Package library contains the extra actions: custom actions written in osb's
// YAML format that are compiled into the binary. Every configuration can use
// them like built-in actions; a configuration that defines an action with the
// same name replaces the extra action.
package library

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/SvenTo/osb/internal/config"
)

//go:embed *.yaml
var files embed.FS

// Prefix is prepended to the file names in positions and messages.
const Prefix = "osb:library/"

// Group is the set of extra actions defined in one library file.
type Group struct {
	// Name is the file name without extension, e.g. "arch".
	Name string
	// Description is the first comment line of the file.
	Description string
	Actions     config.ActionDefs
}

var (
	once   sync.Once
	groups []Group
	all    config.ActionDefs
)

func load() {
	names, err := fs.Glob(files, "*.yaml")
	if err != nil {
		panic(err)
	}
	sort.Strings(names)
	for _, n := range names {
		if strings.HasSuffix(n, ".test.yaml") {
			continue
		}
		data, err := files.ReadFile(n)
		if err != nil {
			panic(err)
		}
		cfg, err := config.Parse(Prefix+n, data, config.LoadOptions{})
		if err != nil {
			panic(fmt.Sprintf("extra actions: %v", err))
		}
		g := Group{Name: strings.TrimSuffix(n, path.Ext(n)), Actions: cfg.Actions}
		if first, _, _ := strings.Cut(string(data), "\n"); strings.HasPrefix(first, "#") {
			g.Description = strings.TrimSpace(strings.TrimPrefix(first, "#"))
		}
		groups = append(groups, g)
		all = append(all, cfg.Actions...)
	}
}

// Groups returns the extra actions grouped by library file, sorted by file name.
func Groups() []Group {
	once.Do(load)
	return groups
}

// Actions returns all extra actions.
func Actions() config.ActionDefs {
	once.Do(load)
	return all
}

// Get returns the extra action with the given name, or nil.
func Get(name string) *config.ActionDef {
	return Actions().Get(name)
}

// GroupOf returns the name of the group that defines an action.
func GroupOf(name string) string {
	for _, g := range Groups() {
		if g.Actions.Get(name) != nil {
			return g.Name
		}
	}
	return ""
}

// File returns the source of a library file, e.g. "arch.yaml".
func File(name string) ([]byte, error) {
	return files.ReadFile(name)
}

// FileNames returns the library files (without tests).
func FileNames() []string {
	var out []string
	for _, g := range Groups() {
		out = append(out, g.Name+".yaml")
	}
	return out
}
