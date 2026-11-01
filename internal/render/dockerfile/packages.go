package dockerfile

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/SvenTo/osb/internal/render/shell"
)

// usesPackages matches scripts that run the package manager.
var usesPackages = regexp.MustCompile(`(?m)(^|[\s;&|(])(apt-get|apt|dpkg|pacman|makepkg|pkg_[a-z_]+)(\s|$)`)

// packagesCommand renders a package installation as plain commands. The
// package caches are handled per RUN (see packageMounts and cleanup).
func (r *renderer) packagesCommand(names []string) string {
	pkgs := make([]string, len(names))
	for i, n := range names {
		pkgs[i] = shell.Q(n)
	}
	list := strings.Join(pkgs, " ")
	fl := r.plan.Flavor
	if fl.IsArch() {
		// The system was upgraded in the first RUN (see setup).
		return "pacman -S --noconfirm --needed " + list + "\n"
	}
	opts := ""
	if o, ok := fl.Vars["apt-options"].(string); ok && o != "" {
		opts = o + " "
	}
	return "apt-get update -q\napt-get install -y -q " + opts + list + "\n"
}

// packageMounts returns the RUN mounts that keep the package caches out of
// the image.
func (r *renderer) packageMounts() []string {
	var dirs []string
	if r.plan.Flavor.IsArch() {
		dirs = []string{"/var/cache/pacman/pkg"}
	} else {
		dirs = []string{"/var/cache/apt", "/var/lib/apt/lists"}
	}
	var out []string
	for _, d := range dirs {
		switch r.cache {
		case "tmpfs":
			out = append(out, "--mount=type=tmpfs,target="+d)
		case "cache":
			out = append(out, "--mount=type=cache,target="+d+",sharing=locked")
		}
	}
	return out
}

// cleanup returns the commands that end a RUN: removing the package caches
// (package-cache: remove) and, with slim, temporary files and logs.
func (r *renderer) cleanup(packages bool) []string {
	arch := r.plan.Flavor.IsArch()
	var out []string
	if packages && r.cache == "remove" {
		if arch {
			out = append(out, "rm -rf /var/cache/pacman/pkg/*")
		} else {
			out = append(out, "apt-get clean", "rm -rf /var/lib/apt/lists/*")
		}
	}
	if r.slim {
		logs := "/var/log/apt/* /var/log/dpkg.log /var/log/alternatives.log"
		if arch {
			logs = "/var/log/pacman.log"
		}
		out = append(out, "rm -rf /tmp/* /var/tmp/* /root/.cache "+logs)
	}
	return out
}

// setup returns the instructions before the first step: excluding
// documentation (slim), keeping downloads in the BuildKit cache, and upgrading
// Arch Linux once.
func (r *renderer) setup() []instr {
	packages := false
	for _, in := range r.items {
		if in.text == "" && usesPackages.MatchString(in.script+shell.Helpers(in.script, r.plan.Flavor)) {
			packages = true
			break
		}
	}
	arch := r.plan.Flavor.IsArch()
	var out []instr
	if r.slim {
		if arch {
			out = append(out, instr{
				header: "# Skip man and info pages when installing packages (artifacts.dockerfile.slim);\n# the image already skips documentation and translations.",
				script: "sed -i '/^\\[options\\]/a NoExtract = usr/share/man/* usr/share/info/*' /etc/pacman.conf\n",
			})
		} else {
			out = append(out, instr{
				header: "# Skip documentation and translations when installing packages (artifacts.dockerfile.slim)",
				text: "COPY <<'EOF' /etc/dpkg/dpkg.cfg.d/slim\n" +
					"path-exclude=/usr/share/doc/*\npath-include=/usr/share/doc/*/copyright\n" +
					"path-exclude=/usr/share/man/*\npath-exclude=/usr/share/info/*\n" +
					"path-exclude=/usr/share/locale/*\npath-include=/usr/share/locale/locale.alias\n" +
					"path-exclude=/usr/share/lintian/*\npath-exclude=/usr/share/groff/*\nEOF",
			})
		}
	}
	if !packages {
		return out
	}
	if r.cache == "cache" && !arch {
		out = append(out, instr{
			header: "# Keep downloaded packages in the BuildKit cache (artifacts.dockerfile.package-cache: cache)",
			script: "rm -f /etc/apt/apt.conf.d/docker-clean\n" +
				"echo 'Binary::apt::APT::Keep-Downloaded-Packages \"true\";' > /etc/apt/apt.conf.d/keep-cache\n",
		})
	}
	if arch {
		r.upgraded = true
		out = append(out, instr{
			header: "# Upgrade the base image once: Arch Linux does not support partial upgrades",
			script: "pacman -Syu --noconfirm\n",
		})
	}
	return out
}

// writeItems writes the instructions; consecutive RUNs without secrets are
// merged into one (merge-steps), so that files changed by several steps are
// stored only once.
func (r *renderer) writeItems() {
	for i := 0; i < len(r.items); {
		in := r.items[i]
		if in.text != "" {
			r.comment(in.header)
			r.line("%s", in.text)
			i++
			continue
		}
		j := i + 1
		if r.merge && len(in.mounts) == 0 {
			for j < len(r.items) && r.items[j].text == "" && len(r.items[j].mounts) == 0 {
				j++
			}
		}
		r.writeRun(r.items[i:j])
		i = j
	}
}

func (r *renderer) comment(header string) {
	if header != "" {
		r.line("\n%s", header)
	} else {
		r.line("")
	}
}

// writeRun writes one RUN instruction for a group of steps. A single short
// script without helpers becomes one line; others a heredoc with the helper
// functions they use.
func (r *renderer) writeRun(group []instr) {
	var script string
	if len(group) == 1 {
		r.comment(group[0].header)
		script = group[0].script
	} else {
		r.line("")
		var parts []string
		for _, in := range group {
			part := in.script
			if in.header != "" {
				part = in.header + "\n" + part
			}
			parts = append(parts, part)
		}
		script = dropRepeatedUpdates(strings.Join(parts, "\n"))
	}
	fl := r.plan.Flavor
	helpers := shell.Helpers(script, fl)
	packages := usesPackages.MatchString(script + helpers)
	if r.upgraded {
		helpers = strings.Replace(helpers, "\nOSB_PKG_UPDATED=0\n", "\nOSB_PKG_UPDATED=1 # upgraded in the first RUN\n", 1)
	}
	if extra := r.cleanup(packages); len(extra) > 0 {
		script = strings.TrimRight(script, "\n") + "\n" + strings.Join(extra, "\n") + "\n"
	}
	var mounts []string
	for _, in := range group {
		mounts = append(mounts, in.mounts...)
	}
	if packages {
		mounts = append(mounts, r.packageMounts()...)
	}
	prefix := "RUN "
	if len(mounts) > 0 {
		prefix += strings.Join(mounts, " \\\n    ") + " "
	}
	lines := strings.Split(strings.TrimRight(script, "\n"), "\n")
	if len(group) == 1 && helpers == "" && simpleLines(lines) {
		r.line("%s%s", prefix, strings.Join(lines, " \\\n && "))
		return
	}
	full := script
	if helpers != "" {
		full = helpers + "\n" + script
	}
	d := shell.Delimiter(full, "EOF")
	r.line("%s<<'%s'\n%s%s", prefix, d, full, d)
}

// dropRepeatedUpdates removes `apt-get update` lines of merged steps when the
// package lists are already up to date in the same RUN.
func dropRepeatedUpdates(script string) string {
	var out []string
	updated := false
	for _, l := range strings.Split(script, "\n") {
		if l == "apt-get update -q" {
			if updated {
				continue
			}
			updated = true
		} else if strings.Contains(l, "/etc/apt/") || strings.Contains(l, "add-apt-repository") {
			updated = false // the sources changed
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func isTrue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, _ := strconv.ParseBool(t)
		return b
	}
	return false
}
