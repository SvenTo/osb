// Package flavor describes the supported Linux distributions.
package flavor

import (
	"fmt"
	"sort"
	"strings"
)

// Package manager families.
const (
	FamilyDebian = "debian"
	FamilyArch   = "arch"
)

// Flavor is a distribution osb can target.
type Flavor struct {
	Name        string
	Description string
	// Family selects package manager and file layout conventions.
	Family string
	// BasedOn names the built-in flavor a custom flavor derives from.
	BasedOn string
	// OSReleaseIDs are accepted values of ID/ID_LIKE in /etc/os-release.
	OSReleaseIDs []string
	// BaseImage is the default container base image.
	BaseImage string
	// CloudImage is the default cloud image URL used for VM builds.
	CloudImage string
	// OVAOSType is the VMware guest OS identifier used in OVF files.
	OVAOSType string
	// SudoGroup is the group granting sudo rights.
	SudoGroup string
	// Vars are user-defined variables (flavor.vars in expressions).
	Vars map[string]any
	// Aliases maps generic package names to flavor-specific ones.
	Aliases map[string]string
}

var debianAliases = map[string]string{
	"base-devel": "build-essential",
	"openssh":    "openssh-server",
	"go-yq":      "yq",
	"python":     "python3",
	"python-pip": "python3-pip",
	"xz":         "xz-utils",
	"bind":       "dnsutils",
}

var archAliases = map[string]string{
	"build-essential":     "base-devel",
	"openssh-server":      "openssh",
	"openssh-client":      "openssh",
	"yq":                  "go-yq",
	"python3":             "python",
	"python3-pip":         "python-pip",
	"python3-venv":        "python",
	"xz-utils":            "xz",
	"dnsutils":            "bind",
	"netcat-openbsd":      "openbsd-netcat",
	"locales":             "glibc",
	"apt-transport-https": "",
	"gnupg":               "gnupg",
	"iputils-ping":        "iputils",
}

var builtins = map[string]*Flavor{
	"ubuntu": {
		Name:         "ubuntu",
		Description:  "Ubuntu 24.04 LTS (apt)",
		Family:       FamilyDebian,
		OSReleaseIDs: []string{"ubuntu"},
		BaseImage:    "ubuntu:24.04",
		CloudImage:   "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		OVAOSType:    "ubuntu64Guest",
		SudoGroup:    "sudo",
		Aliases:      debianAliases,
	},
	"debian": {
		Name:         "debian",
		Description:  "Debian 13 trixie (apt)",
		Family:       FamilyDebian,
		OSReleaseIDs: []string{"debian"},
		BaseImage:    "debian:13",
		CloudImage:   "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2",
		OVAOSType:    "debian11_64Guest",
		SudoGroup:    "sudo",
		Aliases:      debianAliases,
	},
	"archlinux": {
		Name:         "archlinux",
		Description:  "Arch Linux, rolling (pacman)",
		Family:       FamilyArch,
		OSReleaseIDs: []string{"arch", "archlinux"},
		BaseImage:    "archlinux:latest",
		CloudImage:   "https://geo.mirror.pkgbuild.com/images/latest/Arch-Linux-x86_64-cloudimg.qcow2",
		OVAOSType:    "other5xLinux64Guest",
		SudoGroup:    "wheel",
		Aliases:      archAliases,
	},
}

// Builtin returns a copy of the built-in flavor with the given name.
func Builtin(name string) (*Flavor, bool) {
	f, ok := builtins[name]
	if !ok {
		return nil, false
	}
	c := *f
	c.Vars = map[string]any{}
	return &c, true
}

// BuiltinNames returns the sorted names of the built-in flavors.
func BuiltinNames() []string {
	names := make([]string, 0, len(builtins))
	for n := range builtins {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Derive creates a custom flavor based on a built-in one.
func Derive(name, basedOn string) (*Flavor, error) {
	base, ok := Builtin(basedOn)
	if !ok {
		return nil, fmt.Errorf("flavor %q: based-on %q is not a built-in flavor (%s)", name, basedOn, strings.Join(BuiltinNames(), ", "))
	}
	base.Name = name
	base.BasedOn = basedOn
	base.Description = fmt.Sprintf("custom flavor based on %s", basedOn)
	base.OSReleaseIDs = append([]string{name}, base.OSReleaseIDs...)
	return base, nil
}

// Package maps a generic package name to the flavor's name. An empty result
// means the package is not needed on this flavor.
func (f *Flavor) Package(name string) string {
	if a, ok := f.Aliases[name]; ok {
		return a
	}
	return name
}

// Packages maps a list of package names, dropping empty aliases and duplicates.
func (f *Flavor) Packages(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		p := f.Package(n)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// IsDebian reports whether the flavor uses apt.
func (f *Flavor) IsDebian() bool { return f.Family == FamilyDebian }

// IsArch reports whether the flavor uses pacman.
func (f *Flavor) IsArch() bool { return f.Family == FamilyArch }

// JS returns the representation of the flavor in expressions.
func (f *Flavor) JS() map[string]any {
	return map[string]any{
		"name":      f.Name,
		"family":    f.Family,
		"basedOn":   f.BasedOn,
		"baseImage": f.BaseImage,
		"vars":      f.Vars,
	}
}
