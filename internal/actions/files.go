package actions

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/render/shell"
)

var q = shell.Q

func ownerArgs() []ArgSpec {
	return []ArgSpec{
		{Name: "owner", Type: TString, Description: "Owner (user name or uid)"},
		{Name: "group", Type: TString, Description: "Group (group name or gid)"},
	}
}

func checkPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q must be absolute", p)
	}
	if path.Clean(p) == "/" {
		return fmt.Errorf("refusing to operate on /")
	}
	return nil
}

func contentOrSource(c *Ctx, a Args) ([]byte, error) {
	switch {
	case a.Has("content") && a.Has("source"):
		return nil, fmt.Errorf("use either content or source, not both")
	case a.Has("source"):
		return c.ReadSource(a.Str("source"))
	case a.Has("content"):
		return []byte(a.Str("content")), nil
	}
	return nil, fmt.Errorf("content or source is required")
}

func init() {
	register(&Action{
		Name: "write-file", Category: "files",
		Description: "Write a file with inline content or the content of a local file. Rendered natively as COPY (Dockerfile) and write_files (cloud-init).",
		Args: append([]ArgSpec{
			{Name: "path", Type: TString, Required: true, Description: "Absolute destination path"},
			{Name: "content", Type: TText, Description: "File content"},
			{Name: "source", Type: TString, Description: "Local file, relative to the configuration file"},
			{Name: "mode", Type: TMode, Default: "0644", Description: "File mode"},
			{Name: "append", Type: TBool, Default: false, Description: "Append the content unless it is already present"},
		}, ownerArgs()...),
		Example: "action: write-file\nargs:\n  path: /etc/motd\n  content: |\n    Welcome!\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if err := checkPath(a.Str("path")); err != nil {
				return nil, err
			}
			content, err := contentOrSource(c, a)
			if err != nil {
				return nil, err
			}
			return []ops.Op{ops.File{Path: a.Str("path"), Content: content, Mode: a.Str("mode"),
				Owner: a.Str("owner"), Group: a.Str("group"), Append: a.Bool("append")}}, nil
		},
	})

	register(&Action{
		Name: "template-file", Category: "files",
		Description: "Render a local file containing ${{ }} expressions and write it to the target.",
		Args: append([]ArgSpec{
			{Name: "path", Type: TString, Required: true, Description: "Absolute destination path"},
			{Name: "source", Type: TString, Required: true, Description: "Template file, relative to the configuration file"},
			{Name: "mode", Type: TMode, Default: "0644", Description: "File mode"},
		}, ownerArgs()...),
		Example: "action: template-file\nargs:\n  path: /etc/nginx/sites-enabled/app.conf\n  source: templates/app.conf\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if err := checkPath(a.Str("path")); err != nil {
				return nil, err
			}
			raw, err := c.ReadSource(a.Str("source"))
			if err != nil {
				return nil, err
			}
			out, err := c.Template(string(raw))
			if err != nil {
				return nil, fmt.Errorf("template %s: %w", a.Str("source"), err)
			}
			return []ops.Op{ops.File{Path: a.Str("path"), Content: []byte(out), Mode: a.Str("mode"), Owner: a.Str("owner"), Group: a.Str("group")}}, nil
		},
	})

	register(&Action{
		Name: "copy-files", Category: "files",
		Description: "Copy local files, directories or glob matches to the target. Directories are copied recursively; a destination ending in / receives files by name.",
		Args: append([]ArgSpec{
			{Name: "source", Type: TString, Required: true, Description: "Local file, directory or glob, relative to the configuration file"},
			{Name: "dest", Type: TString, Required: true, Description: "Absolute destination (directory if it ends with / or the source is a directory/glob)"},
			{Name: "mode", Type: TMode, Description: "Mode for files (default: keep the local mode)"},
			{Name: "dir-mode", Type: TMode, Default: "0755", Description: "Mode for created directories"},
		}, ownerArgs()...),
		Example: "action: copy-files\nargs:\n  source: files/etc/\n  dest: /etc/\n",
		Build:   buildCopyFiles,
	})

	register(&Action{
		Name: "delete-file", Category: "files",
		Description: "Delete files or directories.",
		Args: []ArgSpec{
			{Name: "path", Type: TList, Required: true, Description: "Absolute path(s)"},
			{Name: "recursive", Type: TBool, Default: false, Description: "Delete directories recursively"},
		},
		Example: "action: delete-file\nargs:\n  path: [/etc/motd, /etc/update-motd.d/10-help-text]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			flag := "-f"
			if a.Bool("recursive") {
				flag = "-rf"
			}
			var b strings.Builder
			for _, p := range a.List("path") {
				if err := checkPath(p); err != nil {
					return nil, err
				}
				fmt.Fprintf(&b, "rm %s -- %s\n", flag, q(p))
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "mkdir", Category: "files",
		Description: "Create directories (including parents).",
		Args: append([]ArgSpec{
			{Name: "path", Type: TList, Required: true, Description: "Absolute path(s)"},
			{Name: "mode", Type: TMode, Default: "0755", Description: "Directory mode"},
		}, ownerArgs()...),
		Example: "action: mkdir\nargs:\n  path: /srv/app\n  owner: app\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			var b strings.Builder
			for _, p := range a.List("path") {
				if err := checkPath(p); err != nil {
					return nil, err
				}
				b.WriteString("ensure_dir " + joinArgs(p, a.Str("mode"), a.Str("owner"), a.Str("group")) + "\n")
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "symlink", Category: "files",
		Description: "Create a symbolic link.",
		Args: []ArgSpec{
			{Name: "path", Type: TString, Required: true, Description: "Absolute path of the link"},
			{Name: "target", Type: TString, Required: true, Description: "Link target"},
		},
		Example: "action: symlink\nargs:\n  path: /usr/local/bin/python\n  target: /usr/bin/python3\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			p := a.Str("path")
			if err := checkPath(p); err != nil {
				return nil, err
			}
			return []ops.Op{ops.Shell{Script: fmt.Sprintf("ensure_dir %s\nln -sfn %s %s\n", q(path.Dir(p)), q(a.Str("target")), q(p))}}, nil
		},
	})

	register(&Action{
		Name: "set-permissions", Category: "files",
		Description: "Change mode and/or ownership of existing paths.",
		Args: append([]ArgSpec{
			{Name: "path", Type: TList, Required: true, Description: "Absolute path(s)"},
			{Name: "mode", Type: TString, Description: "Mode, octal (0750) or symbolic (u+x)"},
			{Name: "recursive", Type: TBool, Default: false, Description: "Apply recursively"},
		}, ownerArgs()...),
		Example: "action: set-permissions\nargs:\n  path: /srv/app\n  owner: app\n  group: app\n  recursive: true\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			r := ""
			if a.Bool("recursive") {
				r = "-R "
			}
			owner := a.Str("owner")
			if a.Str("group") != "" {
				owner += ":" + a.Str("group")
			}
			if a.Str("mode") == "" && owner == "" {
				return nil, fmt.Errorf("mode, owner or group is required")
			}
			var b strings.Builder
			for _, p := range a.List("path") {
				if err := checkPath(p); err != nil {
					return nil, err
				}
				if m := a.Str("mode"); m != "" {
					if _, err := strconv.ParseUint(m, 8, 32); err == nil && len(m) == 3 {
						m = "0" + m
					}
					fmt.Fprintf(&b, "chmod %s%s %s\n", r, q(m), q(p))
				}
				if owner != "" {
					fmt.Fprintf(&b, "chown %s%s %s\n", r, q(owner), q(p))
				}
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "replace-in-file", Category: "files",
		Description: "Replace all matches of an extended regular expression (sed -E syntax) in a file. Use \\1 for groups in the replacement.",
		Args: []ArgSpec{
			{Name: "path", Type: TString, Required: true, Description: "Absolute path"},
			{Name: "regex", Type: TString, Required: true, Description: "POSIX extended regular expression"},
			{Name: "replace", Type: TString, Required: true, Description: "Replacement"},
		},
		Example: "action: replace-in-file\nargs:\n  path: /etc/ssh/sshd_config\n  regex: '^#?PasswordAuthentication.*'\n  replace: PasswordAuthentication no\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if err := checkPath(a.Str("path")); err != nil {
				return nil, err
			}
			re, rep := a.Str("regex"), a.Str("replace")
			if strings.Contains(re, "\n") || strings.Contains(rep, "\n") {
				return nil, fmt.Errorf("regex and replace must be single lines")
			}
			d := sedDelimiter(re + rep)
			rep = strings.ReplaceAll(rep, "&", `\&`)
			expr := "s" + d + re + d + rep + d + "g"
			return []ops.Op{ops.Shell{Script: fmt.Sprintf("sed -E -i %s %s\n", q(expr), q(a.Str("path")))}}, nil
		},
	})

	register(&Action{
		Name: "line-in-file", Category: "files",
		Description: "Ensure a line is present in a file (replacing the first line matching regex), or remove lines matching regex.",
		Args: []ArgSpec{
			{Name: "path", Type: TString, Required: true, Description: "Absolute path"},
			{Name: "line", Type: TString, Description: "The line (required for state present)"},
			{Name: "regex", Type: TString, Description: "Extended regular expression selecting the line to replace or remove"},
			{Name: "state", Type: TString, Default: "present", Enum: []string{"present", "absent"}, Description: "present or absent"},
		},
		Example: "action: line-in-file\nargs:\n  path: /etc/ssh/sshd_config\n  regex: '^#?PermitRootLogin'\n  line: PermitRootLogin no\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			p := a.Str("path")
			if err := checkPath(p); err != nil {
				return nil, err
			}
			if a.Str("state") == "absent" {
				re := a.Str("regex")
				if re == "" {
					if a.Str("line") == "" {
						return nil, fmt.Errorf("line or regex is required")
					}
					re = "^" + EscapeERE(a.Str("line")) + "$"
				}
				return []ops.Op{ops.Shell{Script: "remove_line " + q(p) + " " + q(re) + "\n"}}, nil
			}
			if !a.Has("line") {
				return nil, fmt.Errorf("line is required")
			}
			return []ops.Op{ops.Shell{Script: "ensure_line " + joinArgs(p, a.Str("line"), a.Str("regex")) + "\n"}}, nil
		},
	})

	register(&Action{
		Name: "set-config-value", Category: "files",
		Description: "Set a value in a structured configuration file. json uses jq, yaml uses yq (both installed when missing), ini and env need no extra tools. The key is a dot separated path for json/yaml.",
		Args: []ArgSpec{
			{Name: "path", Type: TString, Required: true, Description: "Absolute path of the file (created when missing)"},
			{Name: "format", Type: TString, Required: true, Enum: []string{"json", "yaml", "ini", "env"}, Description: "File format"},
			{Name: "key", Type: TString, Required: true, Description: "Key; dot separated path for json and yaml (a.b.0.c)"},
			{Name: "value", Type: TAny, Required: true, Description: "Value; any YAML value for json and yaml"},
			{Name: "section", Type: TString, Description: "INI section (empty for the top level)"},
		},
		Example: "action: set-config-value\nargs:\n  path: /etc/docker/daemon.json\n  format: json\n  key: log-driver\n  value: journald\n",
		Build:   buildSetConfigValue,
	})

	register(&Action{
		Name: "download-file", Category: "files",
		Description: "Download a URL to a file, optionally verifying its SHA-256 checksum. Rendered as ADD in Dockerfiles.",
		Args: append([]ArgSpec{
			{Name: "url", Type: TString, Required: true, Description: "URL"},
			{Name: "path", Type: TString, Required: true, Description: "Absolute destination path"},
			{Name: "sha256", Type: TString, Description: "Expected SHA-256 checksum (hex)"},
			{Name: "mode", Type: TMode, Default: "0644", Description: "File mode"},
		}, ownerArgs()...),
		Example: "action: download-file\nargs:\n  url: https://example.com/tool\n  path: /usr/local/bin/tool\n  mode: \"0755\"\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if err := checkPath(a.Str("path")); err != nil {
				return nil, err
			}
			return []ops.Op{ops.Download{URL: a.Str("url"), Path: a.Str("path"), SHA256: strings.ToLower(a.Str("sha256")),
				Mode: a.Str("mode"), Owner: a.Str("owner"), Group: a.Str("group")}}, nil
		},
	})

	register(&Action{
		Name: "extract-archive", Category: "files",
		Description: "Extract a tar (optionally compressed) or zip archive that exists on the target.",
		Args: append([]ArgSpec{
			{Name: "path", Type: TString, Required: true, Description: "Archive on the target"},
			{Name: "dest", Type: TString, Required: true, Description: "Destination directory (created)"},
			{Name: "strip-components", Type: TInt, Default: int64(0), Description: "Strip leading path components (tar only)"},
		}, ownerArgs()...),
		Example: "action: extract-archive\nargs:\n  path: /tmp/tool.tar.gz\n  dest: /opt/tool\n  strip-components: 1\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			src, dest := a.Str("path"), a.Str("dest")
			if err := checkPath(dest); err != nil {
				return nil, err
			}
			var res []ops.Op
			var b strings.Builder
			b.WriteString("ensure_dir " + q(dest) + "\n")
			lower := strings.ToLower(src)
			if strings.HasSuffix(lower, ".zip") {
				res = append(res, ops.Packages{Names: c.Flavor.Packages([]string{"unzip"}), Missing: true})
				fmt.Fprintf(&b, "unzip -o -q %s -d %s\n", q(src), q(dest))
			} else {
				if strings.HasSuffix(lower, ".xz") || strings.HasSuffix(lower, ".txz") {
					res = append(res, ops.Packages{Names: c.Flavor.Packages([]string{"xz-utils"}), Missing: true})
				}
				if strings.HasSuffix(lower, ".zst") {
					res = append(res, ops.Packages{Names: c.Flavor.Packages([]string{"zstd"}), Missing: true})
				}
				strip := ""
				if n := a.Int("strip-components"); n > 0 {
					strip = fmt.Sprintf(" --strip-components=%d", n)
				}
				fmt.Fprintf(&b, "tar -xf %s -C %s%s\n", q(src), q(dest), strip)
			}
			if a.Str("owner") != "" || a.Str("group") != "" {
				own := a.Str("owner")
				if a.Str("group") != "" {
					own += ":" + a.Str("group")
				}
				fmt.Fprintf(&b, "chown -R %s %s\n", q(own), q(dest))
			}
			return append(res, ops.Shell{Script: b.String()}), nil
		},
	})

	register(&Action{
		Name: "git-clone", Category: "files",
		Description: "Clone a git repository, or fetch and check out the ref if it already exists.",
		Args: []ArgSpec{
			{Name: "repo", Type: TString, Required: true, Description: "Repository URL"},
			{Name: "dest", Type: TString, Required: true, Description: "Destination directory"},
			{Name: "ref", Type: TString, Description: "Branch, tag or commit to check out"},
			{Name: "user", Type: TString, Description: "Clone as this user"},
			{Name: "depth", Type: TInt, Description: "Shallow clone depth (0 = full)"},
		},
		Requires: []string{"git", "ca-certificates"},
		Example:  "action: git-clone\nargs:\n  repo: https://github.com/example/dotfiles.git\n  dest: /home/alice/.dotfiles\n  user: alice\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			dest := a.Str("dest")
			if err := checkPath(dest); err != nil {
				return nil, err
			}
			pre := ""
			if u := a.Str("user"); u != "" {
				pre = "as_user " + q(u) + " "
			}
			depth := ""
			if d := a.Int("depth"); d > 0 {
				depth = fmt.Sprintf(" --depth %d", d)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "if [ -d %s ]; then\n", q(dest+"/.git"))
			fmt.Fprintf(&b, "  %sgit -C %s fetch --tags%s origin\n", pre, q(dest), depth)
			if ref := a.Str("ref"); ref != "" {
				fmt.Fprintf(&b, "  %sgit -C %s checkout %s\n", pre, q(dest), q(ref))
				fmt.Fprintf(&b, "  %sgit -C %s merge --ff-only 2>/dev/null || true\n", pre, q(dest))
			} else {
				fmt.Fprintf(&b, "  %sgit -C %s pull --ff-only\n", pre, q(dest))
			}
			b.WriteString("else\n")
			if u := a.Str("user"); u != "" {
				fmt.Fprintf(&b, "  ensure_dir %s 0755 %s\n", q(path.Dir(dest)), q(u))
			}
			fmt.Fprintf(&b, "  %sgit clone%s %s %s\n", pre, depth, q(a.Str("repo")), q(dest))
			if ref := a.Str("ref"); ref != "" {
				fmt.Fprintf(&b, "  %sgit -C %s checkout %s\n", pre, q(dest), q(ref))
			}
			b.WriteString("fi\n")
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})
}

func joinArgs(args ...string) string {
	for len(args) > 0 && args[len(args)-1] == "" {
		args = args[:len(args)-1]
	}
	out := make([]string, len(args))
	for i, s := range args {
		out[i] = q(s)
	}
	return strings.Join(out, " ")
}

func sedDelimiter(s string) string {
	for _, d := range []string{"|", "#", "@", "%", "!", ",", "~", ";", ":"} {
		if !strings.Contains(s, d) {
			return d
		}
	}
	return "\x01"
}

// EscapeERE escapes s for use in a POSIX extended regular expression.
func EscapeERE(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.^$|?*+()[]{}`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func buildCopyFiles(c *Ctx, a Args) ([]ops.Op, error) {
	src, dest := a.Str("source"), a.Str("dest")
	if !strings.HasPrefix(dest, "/") {
		return nil, fmt.Errorf("dest %q must be absolute", dest)
	}
	pattern := c.Path(src)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("source %q matches no files", src)
	}
	isGlob := strings.ContainsAny(src, "*?[")
	type entry struct {
		rel  string
		path string
		mode fs.FileMode
	}
	var files []entry
	dirs := map[string]bool{}
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			base := ""
			if isGlob {
				base = filepath.Base(m)
			}
			err := filepath.WalkDir(m, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(m, p)
				rel = filepath.ToSlash(filepath.Join(base, rel))
				if d.IsDir() {
					if rel != "." {
						dirs[rel] = true
					}
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return nil
				}
				files = append(files, entry{rel: rel, path: p, mode: info.Mode()})
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		files = append(files, entry{rel: filepath.Base(m), path: m, mode: st.Mode()})
	}
	singleFile := len(matches) == 1 && !isGlob && len(dirs) == 0 && len(files) == 1 && !strings.HasSuffix(dest, "/")
	if st, err := os.Stat(matches[0]); err == nil && st.IsDir() {
		singleFile = false
	}
	var res []ops.Op
	owner, group := a.Str("owner"), a.Str("group")
	if !singleFile {
		names := make([]string, 0, len(dirs))
		for d := range dirs {
			names = append(names, d)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("ensure_dir " + joinArgs(dest, a.Str("dir-mode"), owner, group) + "\n")
		for _, d := range names {
			b.WriteString("ensure_dir " + joinArgs(path.Join(dest, d), a.Str("dir-mode"), owner, group) + "\n")
		}
		res = append(res, ops.Shell{Script: b.String()})
	}
	for _, f := range files {
		content, err := os.ReadFile(f.path)
		if err != nil {
			return nil, err
		}
		mode := a.Str("mode")
		if mode == "" {
			mode = fmt.Sprintf("%04o", f.mode.Perm())
		}
		p := path.Join(dest, f.rel)
		if singleFile {
			p = dest
		}
		res = append(res, ops.File{Path: p, Content: content, Mode: mode, Owner: owner, Group: group})
	}
	return res, nil
}

func jsonPath(key string) (string, error) {
	var parts []any
	for _, p := range strings.Split(key, ".") {
		if p == "" {
			return "", fmt.Errorf("invalid key %q", key)
		}
		if n, err := strconv.Atoi(p); err == nil {
			parts = append(parts, n)
		} else {
			parts = append(parts, p)
		}
	}
	b, err := json.Marshal(parts)
	return string(b), err
}

func buildSetConfigValue(c *Ctx, a Args) ([]ops.Op, error) {
	p := a.Str("path")
	if err := checkPath(p); err != nil {
		return nil, err
	}
	key := a.Str("key")
	switch a.Str("format") {
	case "json", "yaml":
		jp, err := jsonPath(key)
		if err != nil {
			return nil, err
		}
		val, err := json.Marshal(a["value"])
		if err != nil {
			return nil, err
		}
		if a.Str("format") == "json" {
			return []ops.Op{
				ops.Packages{Names: c.Flavor.Packages([]string{"jq"}), Missing: true},
				ops.Shell{Script: fmt.Sprintf("set_json %s %s %s\n", q(p), q(jp), q(string(val)))},
			}, nil
		}
		return []ops.Op{
			ops.Packages{Names: c.Flavor.Packages([]string{"yq"}), Missing: true},
			ops.Shell{Script: fmt.Sprintf("set_yaml %s %s %s\n", q(p), q(jp), q(string(val)))},
		}, nil
	case "ini":
		return []ops.Op{ops.Shell{Script: "set_ini " + q(p) + " " + q(a.Str("section")) + " " + q(key) + " " + q(a.Str("value")) + "\n"}}, nil
	case "env":
		return []ops.Op{ops.Shell{Script: "set_env_var " + q(p) + " " + q(key) + " " + q(a.Str("value")) + "\n"}}, nil
	}
	return nil, fmt.Errorf("unknown format")
}
