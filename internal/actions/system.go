package actions

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/SvenTo/osb/internal/ops"
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)

func checkName(kind, n string) error {
	if !safeName.MatchString(n) {
		return fmt.Errorf("invalid %s %q (allowed: letters, digits, . _ @ -)", kind, n)
	}
	return nil
}

func init() {
	register(&Action{
		Name: "trust-ca", Category: "system",
		Description: "Add a CA certificate (PEM) to the system trust store (update-ca-certificates / update-ca-trust).",
		Args: []ArgSpec{
			{Name: "name", Type: TString, Required: true, Description: "Certificate name (file name without extension)"},
			{Name: "content", Type: TText, Description: "PEM certificate"},
			{Name: "source", Type: TString, Description: "Local PEM file"},
			{Name: "url", Type: TString, Description: "Download the certificate from this URL"},
		},
		Example: "action: trust-ca\nargs:\n  name: corp-root\n  source: certs/corp-root.pem\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			name := a.Str("name")
			if err := checkName("name", name); err != nil {
				return nil, err
			}
			dir, update := "/usr/local/share/ca-certificates", "update-ca-certificates"
			if c.Flavor.IsArch() {
				dir, update = "/etc/ca-certificates/trust-source/anchors", "update-ca-trust"
			}
			p := dir + "/" + name + ".crt"
			res := []ops.Op{ops.Packages{Names: c.Flavor.Packages([]string{"ca-certificates"}), Missing: true}}
			if a.Has("url") {
				res = append(res, ops.Download{URL: a.Str("url"), Path: p, Mode: "0644"})
			} else {
				content, err := contentOrSource(c, a)
				if err != nil {
					return nil, fmt.Errorf("content, source or url is required")
				}
				if !strings.Contains(string(content), "BEGIN CERTIFICATE") {
					return nil, fmt.Errorf("certificate is not PEM encoded")
				}
				res = append(res, ops.File{Path: p, Content: content, Mode: "0644"})
			}
			return append(res, ops.Shell{Script: update + "\n"}), nil
		},
	})

	register(&Action{
		Name: "set-hostname", Category: "system",
		Description: "Set the host name. Skipped for container targets (set at run time). Rendered natively in cloud-init.",
		Args: []ArgSpec{
			{Name: "hostname", Type: TString, Required: true, Description: "Host name"},
		},
		Example: "action: set-hostname\nargs:\n  hostname: devbox\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			h := a.Str("hostname")
			if !regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`).MatchString(h) {
				return nil, fmt.Errorf("invalid hostname %q", h)
			}
			if !c.Target.Live {
				return []ops.Op{ops.Note{Message: "set-hostname: the hostname of a container is set at run time, skipped"}}, nil
			}
			return []ops.Op{ops.Hostname{Name: h}}, nil
		},
	})

	register(&Action{
		Name: "set-timezone", Category: "system",
		Description: "Set the system time zone.",
		Args: []ArgSpec{
			{Name: "timezone", Type: TString, Required: true, Description: "IANA time zone, e.g. Europe/Berlin"},
		},
		Example: "action: set-timezone\nargs:\n  timezone: Europe/Berlin\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			tz := a.Str("timezone")
			if !regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`).MatchString(tz) {
				return nil, fmt.Errorf("invalid time zone %q", tz)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "[ -e %s ] || die %s\n", q("/usr/share/zoneinfo/"+tz), q("unknown time zone "+tz))
			fmt.Fprintf(&b, "ln -sf %s /etc/localtime\n", q("/usr/share/zoneinfo/"+tz))
			if c.Flavor.IsDebian() {
				fmt.Fprintf(&b, "printf '%%s\\n' %s | write_file /etc/timezone 0644\n", q(tz))
			}
			return []ops.Op{ops.Packages{Names: []string{"tzdata"}, Missing: true}, ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "set-locale", Category: "system",
		Description: "Generate a locale and make it the system default (LANG).",
		Args: []ArgSpec{
			{Name: "locale", Type: TString, Required: true, Description: "Locale, e.g. en_US.UTF-8"},
			{Name: "charset", Type: TString, Default: "UTF-8", Description: "Character set used in locale.gen"},
		},
		Example: "action: set-locale\nargs:\n  locale: de_DE.UTF-8\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			loc, cs := a.Str("locale"), a.Str("charset")
			if !regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`).MatchString(loc) {
				return nil, fmt.Errorf("invalid locale %q", loc)
			}
			line := loc + " " + cs
			var b strings.Builder
			var res []ops.Op
			if c.Flavor.IsDebian() {
				res = append(res, ops.Packages{Names: []string{"locales"}, Missing: true})
			}
			fmt.Fprintf(&b, "ensure_line /etc/locale.gen %s %s\n", q(line), q("^#?[[:space:]]*"+EscapeERE(line)+"[[:space:]]*$"))
			b.WriteString("locale-gen\n")
			if c.Flavor.IsDebian() {
				fmt.Fprintf(&b, "set_env_var /etc/default/locale LANG %s\n", q(loc))
			} else {
				fmt.Fprintf(&b, "set_env_var /etc/locale.conf LANG %s\n", q(loc))
			}
			return append(res, ops.Shell{Script: b.String()}), nil
		},
	})

	register(&Action{
		Name: "sysctl", Category: "system",
		Description: "Persist kernel parameters in /etc/sysctl.d and apply them on live systems.",
		Args: []ArgSpec{
			{Name: "settings", Type: TMap, Required: true, Description: "Parameter to value"},
			{Name: "name", Type: TString, Default: "osb", Description: "File name: /etc/sysctl.d/90-<name>.conf"},
		},
		Example: "action: sysctl\nargs:\n  settings:\n    net.ipv4.ip_forward: 1\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if err := checkName("name", a.Str("name")); err != nil {
				return nil, err
			}
			var content strings.Builder
			for _, kv := range a.KVs("settings") {
				fmt.Fprintf(&content, "%s = %s\n", kv.Key, kv.Value)
			}
			p := "/etc/sysctl.d/90-" + a.Str("name") + ".conf"
			res := []ops.Op{ops.File{Path: p, Content: []byte(content.String()), Mode: "0644"}}
			if c.Target.Live {
				res = append(res, ops.Shell{Script: fmt.Sprintf("sysctl -p %s || warn 'could not apply sysctl settings'\n", q(p))})
			}
			return res, nil
		},
	})

	register(&Action{
		Name: "load-kernel-module", Category: "system",
		Description: "Load kernel modules at boot (/etc/modules-load.d) and immediately on live systems.",
		Args: []ArgSpec{
			{Name: "modules", Type: TList, Required: true, Description: "Module names"},
			{Name: "name", Type: TString, Default: "osb", Description: "File name: /etc/modules-load.d/<name>.conf"},
		},
		Example: "action: load-kernel-module\nargs:\n  modules: [br_netfilter, overlay]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if err := checkName("name", a.Str("name")); err != nil {
				return nil, err
			}
			mods := a.List("modules")
			for _, m := range mods {
				if err := checkName("module", m); err != nil {
					return nil, err
				}
			}
			res := []ops.Op{ops.File{Path: "/etc/modules-load.d/" + a.Str("name") + ".conf", Content: []byte(strings.Join(mods, "\n") + "\n"), Mode: "0644"}}
			if c.Target.Live {
				res = append(res, ops.Shell{Script: fmt.Sprintf("# shellcheck disable=SC2043\nfor osb_m in %s; do modprobe \"$osb_m\" || warn \"could not load $osb_m\"; done\n", strings.Join(mods, " "))})
			}
			return res, nil
		},
	})

	register(&Action{
		Name: "set-environment", Category: "system",
		Description: "Set system-wide environment variables in /etc/environment (and ENV in Dockerfiles).",
		Args: []ArgSpec{
			{Name: "vars", Type: TMap, Required: true, Description: "Variable to value"},
		},
		Example: "action: set-environment\nargs:\n  vars:\n    EDITOR: vim\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			vars := a.KVs("vars")
			for _, kv := range vars {
				if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(kv.Key) {
					return nil, fmt.Errorf("invalid variable name %q", kv.Key)
				}
			}
			return []ops.Op{ops.Env{Vars: vars}}, nil
		},
	})

	register(&Action{
		Name: "add-sudoers", Category: "system",
		Description: "Add a sudoers drop-in file in /etc/sudoers.d; it is validated with visudo when available.",
		Args: []ArgSpec{
			{Name: "name", Type: TString, Required: true, Description: "File name"},
			{Name: "content", Type: TText, Description: "sudoers content"},
			{Name: "rules", Type: TList, Description: "sudoers lines"},
		},
		Example: "action: add-sudoers\nargs:\n  name: alice\n  rules: [\"alice ALL=(ALL) NOPASSWD: ALL\"]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			name := a.Str("name")
			if err := checkName("name", name); err != nil || strings.Contains(name, ".") {
				return nil, fmt.Errorf("invalid sudoers name %q (no dots allowed)", name)
			}
			content := a.Str("content")
			if rules := a.List("rules"); len(rules) > 0 {
				content += strings.Join(rules, "\n") + "\n"
			}
			if strings.TrimSpace(content) == "" {
				return nil, fmt.Errorf("content or rules is required")
			}
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			p := "/etc/sudoers.d/" + name
			return []ops.Op{
				ops.Packages{Names: []string{"sudo"}, Missing: true},
				ops.File{Path: p, Content: []byte(content), Mode: "0440"},
				ops.Shell{Script: fmt.Sprintf("if has_cmd visudo && ! visudo -cf %s >/dev/null; then\n  rm -f %s\n  die 'invalid sudoers file %s'\nfi\n", q(p), q(p), name)},
			}, nil
		},
	})

	register(&Action{
		Name: "add-mount", Category: "system",
		Description: "Add a file system to /etc/fstab and mount it on live systems.",
		Args: []ArgSpec{
			{Name: "device", Type: TString, Required: true, Description: "Device, UUID=..., LABEL=... or remote path"},
			{Name: "path", Type: TString, Required: true, Description: "Mount point"},
			{Name: "fstype", Type: TString, Required: true, Description: "File system type"},
			{Name: "options", Type: TString, Default: "defaults", Description: "Mount options"},
			{Name: "dump", Type: TInt, Default: int64(0), Description: "dump field"},
			{Name: "pass", Type: TInt, Default: int64(0), Description: "fsck pass number"},
			{Name: "mount", Type: TBool, Default: true, Description: "Mount immediately on live systems"},
		},
		Example: "action: add-mount\nargs:\n  device: LABEL=data\n  path: /data\n  fstype: ext4\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			p := a.Str("path")
			if err := checkPath(p); err != nil {
				return nil, err
			}
			line := fmt.Sprintf("%s %s %s %s %d %d", a.Str("device"), p, a.Str("fstype"), a.Str("options"), a.Int("dump"), a.Int("pass"))
			var b strings.Builder
			fmt.Fprintf(&b, "ensure_dir %s\n", q(p))
			fmt.Fprintf(&b, "ensure_line /etc/fstab %s %s\n", q(line), q("^[^#]*[[:space:]]"+EscapeERE(p)+"[[:space:]]"))
			if c.Target.Live && a.Bool("mount") {
				fmt.Fprintf(&b, "mountpoint -q %s || mount %s || warn %s\n", q(p), q(p), q("could not mount "+p))
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "add-swap-file", Category: "system",
		Description: "Create and enable a swap file. Skipped for container targets.",
		Args: []ArgSpec{
			{Name: "path", Type: TString, Default: "/swapfile", Description: "Swap file path"},
			{Name: "size", Type: TString, Required: true, Description: "Size, e.g. 2G or 512M"},
		},
		Example: "action: add-swap-file\nargs:\n  size: 2G\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			p, size := a.Str("path"), strings.ToUpper(a.Str("size"))
			if err := checkPath(p); err != nil {
				return nil, err
			}
			m := regexp.MustCompile(`^([0-9]+)([MG])$`).FindStringSubmatch(size)
			if m == nil {
				return nil, fmt.Errorf("invalid size %q (use e.g. 512M or 2G)", size)
			}
			if !c.Target.Live {
				return []ops.Op{ops.Note{Message: "add-swap-file: swap is not supported in containers, skipped"}}, nil
			}
			count := m[1]
			if m[2] == "G" {
				var n int
				fmt.Sscan(m[1], &n)
				count = fmt.Sprint(n * 1024)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "if [ ! -f %s ]; then\n", q(p))
			fmt.Fprintf(&b, "  dd if=/dev/zero of=%s bs=1M count=%s status=none\n", q(p), count)
			fmt.Fprintf(&b, "  chmod 0600 %s\n  mkswap %s\nfi\n", q(p), q(p))
			fmt.Fprintf(&b, "ensure_line /etc/fstab %s %s\n", q(p+" none swap defaults 0 0"), q("^"+EscapeERE(p)+"[[:space:]]"))
			fmt.Fprintf(&b, "swapon --show=NAME --noheadings | grep -qxF %s || swapon %s || warn 'could not enable swap'\n", q(p), q(p))
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})
}
