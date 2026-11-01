package actions

import (
	"fmt"
	"path"
	"strings"

	"github.com/SvenTo/osb/internal/ops"
)

// ParsePackageList parses a package list file: one or more names per line,
// # starts a comment.
func ParsePackageList(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		out = append(out, strings.Fields(line)...)
	}
	return out
}

func packageNames(c *Ctx, a Args) ([]string, error) {
	names := a.List("packages")
	if a.Has("source") {
		b, err := c.ReadSource(a.Str("source"))
		if err != nil {
			return nil, err
		}
		names = append(names, ParsePackageList(string(b))...)
	}
	names = c.Flavor.Packages(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no packages given")
	}
	return names, nil
}

func init() {
	register(&Action{
		Name: "install-packages", Category: "packages",
		Description: "Install packages from the distribution repositories. Generic names are mapped to flavor-specific names (e.g. build-essential -> base-devel on Arch). Rendered natively as cloud-init packages when possible.",
		Args: []ArgSpec{
			{Name: "packages", Type: TList, Description: "Package names"},
			{Name: "source", Type: TString, Description: "Local package list file (# comments allowed)"},
		},
		Example: "action: install-packages\nargs:\n  packages: [curl, git, vim]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			names, err := packageNames(c, a)
			if err != nil {
				return nil, err
			}
			return []ops.Op{ops.Packages{Names: names}}, nil
		},
	})

	register(&Action{
		Name: "remove-packages", Category: "packages",
		Description: "Remove (purge) packages.",
		Args: []ArgSpec{
			{Name: "packages", Type: TList, Description: "Package names"},
			{Name: "source", Type: TString, Description: "Local package list file"},
		},
		Example: "action: remove-packages\nargs:\n  packages: [snapd]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			names, err := packageNames(c, a)
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			b.WriteString("# shellcheck disable=SC2043\nfor osb_p in")
			for _, n := range names {
				b.WriteString(" " + q(n))
			}
			b.WriteString("; do\n  if pkg_installed \"$osb_p\"; then pkg_remove \"$osb_p\"; fi\ndone\n")
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "upgrade-system", Category: "packages",
		Description: "Upgrade all installed packages.",
		Example:     "action: upgrade-system\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			return []ops.Op{ops.Shell{Script: "pkg_upgrade\n"}}, nil
		},
	})

	register(&Action{
		Name: "hold-packages", Category: "packages",
		Description: "Prevent packages from being upgraded (apt-mark hold / pacman IgnorePkg).",
		Args: []ArgSpec{
			{Name: "packages", Type: TList, Required: true, Description: "Package names"},
		},
		Example: "action: hold-packages\nargs:\n  packages: [linux-image-generic]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			names, err := packageNames(c, a)
			if err != nil {
				return nil, err
			}
			return []ops.Op{ops.Shell{Script: "pkg_hold " + joinArgs(names...) + "\n"}}, nil
		},
	})

	register(&Action{
		Name: "install-package-file", Category: "packages",
		Description: "Install a package file (.deb / .pkg.tar.zst) that is on the target or copied from a local file.",
		Args: []ArgSpec{
			{Name: "path", Type: TString, Description: "Package file on the target"},
			{Name: "source", Type: TString, Description: "Local package file, copied to /var/cache/osb first"},
		},
		Example: "action: install-package-file\nargs:\n  source: packages/tool_1.0_amd64.deb\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			var res []ops.Op
			p := a.Str("path")
			if a.Has("source") {
				content, err := c.ReadSource(a.Str("source"))
				if err != nil {
					return nil, err
				}
				p = "/var/cache/osb/" + path.Base(a.Str("source"))
				res = append(res, ops.File{Path: p, Content: content, Mode: "0644"})
			}
			if p == "" {
				return nil, fmt.Errorf("path or source is required")
			}
			if !strings.HasPrefix(p, "/") {
				return nil, fmt.Errorf("path %q must be absolute", p)
			}
			return append(res, ops.Shell{Script: "pkg_install_file " + q(p) + "\n"}), nil
		},
	})

	register(&Action{
		Name: "add-repository", Category: "packages",
		Description: "Add a package repository. Takes flavor-specific settings: `apt` for Debian/Ubuntu (deb822 .sources file), `pacman` for Arch. Package lists are refreshed afterwards.",
		Args: []ArgSpec{
			{Name: "name", Type: TString, Required: true, Description: "Repository name (file name / pacman section)"},
			{Name: "apt", Type: TAny, Description: "apt settings: uri (required), suites (default: the release codename), components (default: main), key (armored key), key-url, architectures"},
			{Name: "pacman", Type: TAny, Description: "pacman settings: server (required), sig-level, key-id, keyserver"},
		},
		Example: "action: add-repository\nargs:\n  name: docker\n  apt:\n    uri: https://download.docker.com/linux/ubuntu\n    components: stable\n    key-url: https://download.docker.com/linux/ubuntu/gpg\n",
		Build:   buildAddRepository,
	})
}

func settings(a Args, key string) (map[string]string, error) {
	raw, ok := a[key]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping", key)
	}
	out := map[string]string{}
	for k, v := range m {
		switch t := v.(type) {
		case []any:
			var parts []string
			for _, e := range t {
				parts = append(parts, fmt.Sprint(e))
			}
			out[k] = strings.Join(parts, " ")
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out, nil
}

func buildAddRepository(c *Ctx, a Args) ([]ops.Op, error) {
	name := a.Str("name")
	if strings.ContainsAny(name, "/ \t[]") {
		return nil, fmt.Errorf("invalid repository name %q", name)
	}
	if c.Flavor.IsDebian() {
		s, err := settings(a, "apt")
		if err != nil {
			return nil, err
		}
		if s == nil {
			return nil, fmt.Errorf("add-repository needs `apt` settings for flavor %s (or restrict the step with `flavors:`)", c.Flavor.Name)
		}
		if s["uri"] == "" {
			return nil, fmt.Errorf("apt.uri is required")
		}
		var res []ops.Op
		keyPath := ""
		switch {
		case s["key"] != "":
			keyPath = "/etc/apt/keyrings/" + name + ".asc"
			res = append(res, ops.File{Path: keyPath, Content: []byte(strings.TrimSpace(s["key"]) + "\n"), Mode: "0644"})
		case s["key-url"] != "":
			keyPath = "/etc/apt/keyrings/" + name + ".asc"
			res = append(res, ops.Download{URL: s["key-url"], Path: keyPath, Mode: "0644"})
		}
		comps := s["components"]
		if comps == "" {
			comps = "main"
		}
		var b strings.Builder
		suites := q(s["suites"])
		if s["suites"] == "" {
			b.WriteString("osb_codename=$(. /etc/os-release && echo \"${VERSION_CODENAME}\")\n")
			suites = `"$osb_codename"`
		}
		format := "Types: deb\\nURIs: %s\\nSuites: %s\\nComponents: %s\\n"
		args := []string{q(s["uri"]), suites, q(comps)}
		if keyPath != "" {
			format += "Signed-By: %s\\n"
			args = append(args, q(keyPath))
		}
		if s["architectures"] != "" {
			format += "Architectures: %s\\n"
			args = append(args, q(s["architectures"]))
		}
		fmt.Fprintf(&b, "printf '%s' \\\n  %s |\n  write_file %s 0644\n", format, strings.Join(args, " "), q("/etc/apt/sources.list.d/"+name+".sources"))
		b.WriteString("pkg_refresh\n")
		return append(res, ops.Shell{Script: b.String()}), nil
	}
	s, err := settings(a, "pacman")
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("add-repository needs `pacman` settings for flavor %s (or restrict the step with `flavors:`)", c.Flavor.Name)
	}
	if s["server"] == "" {
		return nil, fmt.Errorf("pacman.server is required")
	}
	var b strings.Builder
	if id := s["key-id"]; id != "" {
		ks := ""
		if s["keyserver"] != "" {
			ks = " --keyserver " + q(s["keyserver"])
		}
		fmt.Fprintf(&b, "pacman-key --recv-keys %s%s\npacman-key --lsign-key %s\n", q(id), ks, q(id))
	}
	section := "[" + name + "]\n"
	if s["sig-level"] != "" {
		section += "SigLevel = " + s["sig-level"] + "\n"
	}
	section += "Server = " + s["server"] + "\n"
	fmt.Fprintf(&b, "if ! grep -qxF %s /etc/pacman.conf; then\nprintf '\\n%%s' %s >> /etc/pacman.conf\nfi\n", q("["+name+"]"), q(section))
	b.WriteString("pkg_refresh\n")
	return []ops.Op{ops.Shell{Script: b.String()}}, nil
}
