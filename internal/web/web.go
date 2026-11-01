// Package web is the API of the WebAssembly build used by the website: it
// renders configurations in memory (no file system, imports or builds).
package web

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SvenTo/osb/internal/actions"
	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/flavor"
	"github.com/SvenTo/osb/internal/render/cloudinit"
	"github.com/SvenTo/osb/internal/render/dockerfile"
	"github.com/SvenTo/osb/internal/render/shell"
	"github.com/SvenTo/osb/internal/schema"
	"github.com/SvenTo/osb/internal/target"
	"github.com/SvenTo/osb/library"
)

// Request asks for one rendering.
type Request struct {
	Config string            `json:"config"`
	Flavor string            `json:"flavor"`
	Target string            `json:"target"`
	Args   map[string]string `json:"args"`
	// Placeholders fills missing required arguments with dummy values.
	Placeholders bool `json:"placeholders"`
}

// File is one generated file.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Argument describes a configuration argument (for generated forms).
type Argument struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Options     []any  `json:"options,omitempty"`
}

// Step is a resolved step, for the plan view.
type Step struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Action string `json:"action"`
	Ops    int    `json:"ops"`
}

// Response is the result of Render.
type Response struct {
	Error     string     `json:"error,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
	Flavor    string     `json:"flavor,omitempty"`
	Flavors   []string   `json:"flavors,omitempty"`
	Arguments []Argument `json:"arguments,omitempty"`
	Steps     []Step     `json:"steps,omitempty"`
	Files     []File     `json:"files,omitempty"`
	// Command is the osb command producing this output on a real machine.
	Command string `json:"command,omitempty"`
	// Note explains what the browser cannot do for this target.
	Note string `json:"note,omitempty"`
}

// Version is set by the WebAssembly main package.
var Version = "dev"

// Render validates and renders a configuration for one flavor and target.
func Render(req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = Response{Error: fmt.Sprintf("internal error: %v", r)}
		}
	}()
	cfg, err := config.Parse("osb.yaml", []byte(req.Config), config.LoadOptions{Validate: schema.Validate})
	if err != nil {
		return Response{Error: err.Error()}
	}
	if len(cfg.Imports) > 0 {
		return Response{Error: "osb.yaml: imports are not available in the browser; use the osb binary"}
	}
	cfg.File = "osb.yaml"
	for _, a := range library.Actions() {
		if cfg.Actions.Get(a.Name) == nil {
			cfg.Actions = append(cfg.Actions, a)
		}
	}
	resp.Flavors = engine.EnabledFlavors(cfg)
	for _, a := range cfg.Arguments {
		arg := Argument{Name: a.Name, Type: a.Type, Description: a.Description, Required: a.Required, Options: a.Options}
		if arg.Type == "" {
			arg.Type = "string"
		}
		if a.HasDefault() {
			arg.Default = a.Default.V
		}
		resp.Arguments = append(resp.Arguments, arg)
	}
	tname := req.Target
	if tname == "" {
		tname = "shell"
	}
	tg, err := target.Get(tname)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	args := map[string]string{}
	for k, v := range req.Args {
		if v != "" {
			args[k] = v
		}
	}
	fl := req.Flavor
	if fl == "" && len(resp.Flavors) > 0 {
		fl = resp.Flavors[0]
	}
	plan, err := engine.Evaluate(cfg, engine.Options{Flavor: fl, Target: tname,
		Args: engine.ArgInput{CLI: args, Placeholders: req.Placeholders}})
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	resp.Flavor = plan.Flavor.Name
	for _, w := range plan.Warnings {
		resp.Warnings = append(resp.Warnings, plan.Secrets.String(w))
	}
	for _, s := range plan.Steps {
		resp.Steps = append(resp.Steps, Step{Index: s.Index, Title: plan.Secrets.String(s.Title), Action: s.Action, Ops: len(s.Ops)})
	}
	cmd := fmt.Sprintf("osb %s %s -f osb.yaml --flavor %s", map[string]string{"render": "render", "build": "build", "live": "apply"}[tg.Kind], tname, plan.Flavor.Name)
	if tg.Kind == "live" {
		cmd = "osb apply -f osb.yaml"
	}
	for _, k := range sortedKeys(req.Args) {
		if v := req.Args[k]; v != "" {
			cmd += " -a " + shell.Q(k+"="+v)
		}
	}
	resp.Command = plan.Secrets.String(cmd)

	add := func(path, content string) {
		resp.Files = append(resp.Files, File{Path: path, Content: plan.Secrets.String(content)})
	}
	switch tg.Renderer {
	case target.RendererShell:
		add(scriptName(plan), shell.Render(plan.ShellDoc(Version, resp.Command)))
		if tg.Kind == "live" {
			resp.Note = "osb apply shows this plan, asks for confirmation and runs the script with sudo on the machine."
		}
	case target.RendererDockerfile:
		res, err := dockerfile.Generate(plan, dockerfile.Options{Version: Version, Command: resp.Command})
		if err != nil {
			resp.Error = err.Error()
			return resp
		}
		for _, f := range res.Files {
			if f.Path == ".osb/lib.sh" {
				continue // the same helper library for every config
			}
			add(f.Path, string(f.Content))
		}
		switch tname {
		case "oci":
			resp.Note = "osb build oci builds this Dockerfile with docker (BuildKit) or podman and tags the image."
		case "wsl":
			resp.Note = "osb build wsl adds wsl.conf (default user, systemd) to this Dockerfile, builds it and exports the file system as a .wsl archive."
		}
	case target.RendererCloudInit:
		res, err := cloudinit.Render(plan, cloudinit.Options{Version: Version, Command: resp.Command, Build: tg.Image})
		if err != nil {
			resp.Error = err.Error()
			return resp
		}
		add("user-data", string(res.UserData))
		add("meta-data", string(res.MetaData))
		if res.Script != "" {
			add("run.sh (inside user-data)", res.Script)
		}
		switch tname {
		case "qcow2":
			resp.Note = "osb build qcow2 boots the flavor's official cloud image with this user-data in QEMU/KVM or libvirt, waits for the result and saves the disk."
		case "ova":
			resp.Note = "osb build ova builds the qcow2 image and packs it as a VMDK with an OVF descriptor for VMware and VirtualBox."
		}
	}
	return resp
}

func scriptName(plan *engine.Plan) string {
	name := plan.Config.Name
	if name == "" {
		name = "osb"
	}
	return name + "-" + plan.Flavor.Name + ".sh"
}

func sortedKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ActionInfo describes a built-in or extra action.
type ActionInfo struct {
	Name        string     `json:"name"`
	Category    string     `json:"category"`
	Extra       bool       `json:"extra,omitempty"`
	Description string     `json:"description"`
	Flavors     []string   `json:"flavors,omitempty"`
	Args        []Argument `json:"args"`
	Example     string     `json:"example,omitempty"`
}

// Catalog describes everything the website offers in its builder and editor.
type Catalog struct {
	Version string       `json:"version"`
	Actions []ActionInfo `json:"actions"`
	Flavors []NamedInfo  `json:"flavors"`
	Targets []NamedInfo  `json:"targets"`
	Schema  string       `json:"schema"`
}

// NamedInfo is a flavor or target.
type NamedInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind,omitempty"`
}

// GetCatalog returns the catalog.
func GetCatalog() Catalog {
	c := Catalog{Version: Version, Schema: string(schema.JSON())}
	for _, a := range actions.All() {
		info := ActionInfo{Name: a.Name, Category: a.Category, Description: a.Description, Example: a.Example}
		for _, s := range a.Args {
			desc := s.Description
			if len(s.Enum) > 0 {
				desc += " One of: " + strings.Join(s.Enum, ", ") + "."
			}
			info.Args = append(info.Args, Argument{Name: s.Name, Type: string(s.Type), Description: desc, Default: s.Default, Required: s.Required})
		}
		c.Actions = append(c.Actions, info)
	}
	for _, g := range library.Groups() {
		for _, a := range g.Actions {
			info := ActionInfo{Name: a.Name, Category: g.Name, Extra: true, Description: strings.TrimSpace(a.Description), Flavors: a.Flavors}
			for _, d := range a.Arguments {
				arg := Argument{Name: d.Name, Type: d.Type, Description: d.Description, Required: d.Required, Options: d.Options}
				if arg.Type == "" {
					arg.Type = "string"
				}
				if d.HasDefault() {
					arg.Default = d.Default.V
				}
				info.Args = append(info.Args, arg)
			}
			c.Actions = append(c.Actions, info)
		}
	}
	for _, n := range flavor.BuiltinNames() {
		f, _ := flavor.Builtin(n)
		c.Flavors = append(c.Flavors, NamedInfo{Name: f.Name, Description: f.Description})
	}
	for _, t := range target.All() {
		c.Targets = append(c.Targets, NamedInfo{Name: t.Name, Description: t.Description, Kind: t.Kind})
	}
	return c
}
