package web

import (
	"strings"
	"testing"
)

const cfg = `
name: demo
arguments:
  user: { required: true }
  password: { type: secret }
steps:
  - action: add-user
    args: { name: "${{ args.user }}", sudo: true }
  - action: set-password
    if: args.password
    args: { user: "${{ args.user }}", password: "${{ args.password }}" }
  - action: install-packages
    args: { packages: [git, openssh-server] }
  - action: mkdir-home
    args: { user: "${{ args.user }}", paths: [Projects] }
`

func TestRenderTargets(t *testing.T) {
	for target, want := range map[string]string{
		"shell":      "useradd --create-home alice",
		"apply":      "useradd --create-home alice",
		"dockerfile": "FROM archlinux:latest",
		"oci":        "pacman -S --noconfirm --needed git openssh",
		"wsl":        "FROM archlinux:latest",
		"cloud-init": "#cloud-config",
		"qcow2":      "OSB-BUILD-RESULT",
		"ova":        "power_state",
	} {
		resp := Render(Request{Config: cfg, Flavor: "archlinux", Target: target, Args: map[string]string{"user": "alice", "password": "hunter22"}})
		if resp.Error != "" {
			t.Fatalf("%s: %s", target, resp.Error)
		}
		all := ""
		for _, f := range resp.Files {
			all += f.Content
		}
		if !strings.Contains(all, want) {
			t.Errorf("%s: output does not contain %q", target, want)
		}
		if strings.Contains(all, "hunter22") || strings.Contains(resp.Command, "hunter22") {
			t.Errorf("%s: secret not redacted", target)
		}
		if !strings.Contains(all, "/home/alice/Projects") {
			t.Errorf("%s: extra action mkdir-home not rendered", target)
		}
	}
}

func TestErrors(t *testing.T) {
	if r := Render(Request{Config: cfg, Target: "shell"}); !strings.Contains(r.Error, "user") {
		t.Errorf("missing argument: got %q", r.Error)
	}
	if r := Render(Request{Config: cfg, Target: "shell", Placeholders: true}); r.Error != "" || len(r.Arguments) != 2 {
		t.Errorf("placeholders: %+v", r)
	}
	if r := Render(Request{Config: "steps:\n  - action: nope\n", Target: "shell"}); !strings.Contains(r.Error, "nope") {
		t.Errorf("unknown action: got %q", r.Error)
	}
	if r := Render(Request{Config: "imports: [x.yaml]\n"}); !strings.Contains(r.Error, "imports") {
		t.Errorf("imports: got %q", r.Error)
	}
	if r := Render(Request{Config: "steps: [\n"}); r.Error == "" {
		t.Error("syntax error not reported")
	}
}

func TestCatalog(t *testing.T) {
	c := GetCatalog()
	var extra, builtin bool
	for _, a := range c.Actions {
		extra = extra || (a.Extra && a.Name == "install-aur-package")
		builtin = builtin || (!a.Extra && a.Name == "write-file")
	}
	if !extra || !builtin || len(c.Flavors) != 3 || len(c.Targets) != 8 || c.Schema == "" {
		t.Errorf("catalog incomplete: extra=%v builtin=%v flavors=%d targets=%d", extra, builtin, len(c.Flavors), len(c.Targets))
	}
}

func TestUserFiles(t *testing.T) {
	cfg := `steps:
  - action: write-file
    args: { path: /etc/motd, source: files/motd }
  - action: copy-files
    args: { source: conf/, dest: /etc/app/ }
  - action: log
    args: { message: "${{ file('files/motd').trim() }}" }
`
	files := []File{
		{Path: "files/motd", Content: "Hello from a file\n"},
		{Path: "conf/a.ini", Content: "a=1\n"},
		{Path: "conf/sub/b.bin", Content: "AAEC", Base64: true},
	}
	r := Render(Request{Config: cfg, Flavor: "debian", Target: "dockerfile", Files: files})
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	out := r.Files[0].Content
	for _, want := range []string{"Hello from a file", "/etc/app/a.ini", "/etc/app/sub/b.bin", "base64"} {
		if !strings.Contains(out, want) {
			t.Errorf("Dockerfile does not contain %q:\n%s", want, out)
		}
	}
	if r := Render(Request{Config: cfg, Flavor: "debian"}); !strings.Contains(r.Error, "Files panel") {
		t.Errorf("missing file: %q", r.Error)
	}
}

func renderDockerfile(t *testing.T, flavor, artifacts string) string {
	t.Helper()
	c := `
name: demo
` + artifacts + `
steps:
  - action: install-packages
    args: { packages: [git] }
  - action: install-packages
    args: { packages: [curl] }
  - action: run-command
    args: { command: "echo hi > /etc/hi" }
`
	resp := Render(Request{Config: c, Flavor: flavor, Target: "dockerfile"})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	return resp.Files[0].Content
}

func TestDockerfileLayers(t *testing.T) {
	df := renderDockerfile(t, "ubuntu", "")
	if strings.Count(df, "\nRUN ") != 1 {
		t.Errorf("consecutive steps are not merged into one RUN:\n%s", df)
	}
	if !strings.Contains(df, "--mount=type=tmpfs,target=/var/lib/apt/lists") || strings.Contains(df, "rm -rf /var/lib/apt") {
		t.Errorf("package caches are not on a tmpfs:\n%s", df)
	}
	if strings.Count(df, "apt-get update") != 1 {
		t.Errorf("repeated apt-get update in one RUN:\n%s", df)
	}

	df = renderDockerfile(t, "ubuntu", "artifacts: { dockerfile: { package-cache: remove, merge-steps: false } }")
	if strings.Count(df, "\nRUN ") != 3 || strings.Count(df, "rm -rf /var/lib/apt/lists/*") != 2 || strings.Contains(df, "--mount") {
		t.Errorf("package-cache: remove, merge-steps: false:\n%s", df)
	}

	df = renderDockerfile(t, "debian", "artifacts: { dockerfile: { package-cache: cache, slim: true } }")
	for _, want := range []string{"/etc/dpkg/dpkg.cfg.d/slim", "rm -f /etc/apt/apt.conf.d/docker-clean", "--mount=type=cache,target=/var/cache/apt,sharing=locked", "rm -rf /tmp/*"} {
		if !strings.Contains(df, want) {
			t.Errorf("cache and slim: missing %q:\n%s", want, df)
		}
	}

	df = renderDockerfile(t, "archlinux", "")
	if strings.Count(df, "pacman -Syu") != 1 || !strings.Contains(df, "pacman -S --noconfirm --needed git") {
		t.Errorf("Arch Linux is not upgraded exactly once:\n%s", df)
	}

	if r := Render(Request{Config: "name: demo\nartifacts: { dockerfile: { package-cache: none } }\nsteps: []\n", Flavor: "ubuntu", Target: "dockerfile"}); r.Error == "" {
		t.Error("invalid package-cache accepted")
	}
}
