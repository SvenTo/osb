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
		"oci":        "pkg_install git openssh",
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
