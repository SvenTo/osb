// Package target describes the artifacts osb can produce.
package target

import (
	"fmt"
	"sort"
	"strings"
)

// Renderer names.
const (
	RendererShell      = "shell"
	RendererDockerfile = "dockerfile"
	RendererCloudInit  = "cloud-init"
)

// Target is an output kind.
type Target struct {
	Name        string
	Description string
	// Renderer produces the intermediate output for this target.
	Renderer string
	// Kind is "render", "build" or "live".
	Kind string
	// Live is true when the configuration runs on a booted system with an
	// init system (services can be started, the hostname can be set...).
	Live bool
	// Image is true when the result is an image that is instantiated later.
	Image bool
}

var all = map[string]Target{
	"shell":      {Name: "shell", Description: "POSIX shell script that configures a running system", Renderer: RendererShell, Kind: "render", Live: true},
	"dockerfile": {Name: "dockerfile", Description: "Dockerfile and build context", Renderer: RendererDockerfile, Kind: "render", Image: true},
	"cloud-init": {Name: "cloud-init", Description: "cloud-init user-data and meta-data", Renderer: RendererCloudInit, Kind: "render", Live: true},
	"oci":        {Name: "oci", Description: "Container image built with docker/BuildKit", Renderer: RendererDockerfile, Kind: "build", Image: true},
	"wsl":        {Name: "wsl", Description: "WSL 2 distribution (.wsl root filesystem archive)", Renderer: RendererDockerfile, Kind: "build", Image: true},
	"qcow2":      {Name: "qcow2", Description: "VM disk image built from a cloud image with QEMU or libvirt", Renderer: RendererCloudInit, Kind: "build", Live: true, Image: true},
	"ova":        {Name: "ova", Description: "OVA appliance (VMDK + OVF)", Renderer: RendererCloudInit, Kind: "build", Live: true, Image: true},
	"apply":      {Name: "apply", Description: "Configure the running system", Renderer: RendererShell, Kind: "live", Live: true},
}

// Get returns the target with the given name.
func Get(name string) (Target, error) {
	t, ok := all[name]
	if !ok {
		return Target{}, fmt.Errorf("unknown target %q (known: %s)", name, strings.Join(Names(), ", "))
	}
	return t, nil
}

// Names returns all target names.
func Names() []string {
	n := make([]string, 0, len(all))
	for k := range all {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// All returns all targets sorted by name.
func All() []Target {
	var out []Target
	for _, n := range Names() {
		out = append(out, all[n])
	}
	return out
}

// JS returns the representation of the target in expressions.
func (t Target) JS() map[string]any {
	return map[string]any{
		"name":     t.Name,
		"renderer": t.Renderer,
		"live":     t.Live,
		"image":    t.Image,
	}
}
