package cloudinit

import (
	"strings"
	"testing"

	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/flavor"
	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/redact"
	"github.com/SvenTo/osb/internal/target"
)

func plan(steps ...*ops.Step) *engine.Plan {
	fl, _ := flavor.Builtin("ubuntu")
	tg, _ := target.Get("cloud-init")
	for i, s := range steps {
		s.Index = i + 1
	}
	return &engine.Plan{Config: &config.Config{Name: "t", File: "/t/osb.yaml"}, Flavor: fl, Target: tg, Steps: steps, Secrets: &redact.Set{}}
}

func TestOrderIsPreserved(t *testing.T) {
	p := plan(
		&ops.Step{Title: "file", Ops: []ops.Op{ops.File{Path: "/etc/a", Content: []byte("a\n")}}},
		&ops.Step{Title: "user", Ops: []ops.Op{ops.User{Name: "alice", CreateHome: true}}},
		&ops.Step{Title: "pkgs", Ops: []ops.Op{ops.Packages{Names: []string{"jq"}}}},
		&ops.Step{Title: "cmd", Ops: []ops.Op{ops.Shell{Script: "echo hi\n"}}},
		// After a shell step, a user must not be created natively (it would run earlier).
		&ops.Step{Title: "late user", Ops: []ops.Op{ops.User{Name: "bob", CreateHome: true}}},
		&ops.Step{Title: "late file", Ops: []ops.Op{ops.File{Path: "/etc/b", Content: []byte("b\n")}}},
	)
	res, err := Render(p, Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ud := string(res.UserData)
	if res.Native != 3 {
		t.Errorf("native ops = %d, want 3", res.Native)
	}
	for _, want := range []string{"path: /etc/a", "name: alice", "- jq", "runcmd:", "useradd --create-home bob", "write_file /etc/b"} {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data does not contain %q:\n%s", want, ud)
		}
	}
	if strings.Contains(ud, "name: bob") || strings.Contains(ud, "path: /etc/b\n") {
		t.Error("late ops were rendered natively")
	}
}

func TestFileOwnedByNewUserIsDeferred(t *testing.T) {
	p := plan(
		&ops.Step{Title: "user", Ops: []ops.Op{ops.User{Name: "alice"}}},
		&ops.Step{Title: "file", Ops: []ops.Op{ops.File{Path: "/home/alice/x", Owner: "alice", Content: []byte("x\n")}}},
	)
	res, err := Render(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.UserData), "defer: true") || res.Script != "" {
		t.Errorf("expected a deferred native file:\n%s", res.UserData)
	}
}

func TestBuildMode(t *testing.T) {
	res, err := Render(plan(), Options{Build: true})
	if err != nil {
		t.Fatal(err)
	}
	ud := string(res.UserData)
	for _, want := range []string{"power_state:", MarkerOK, "ds-identify.cfg", ScriptPath} {
		if !strings.Contains(ud, want) {
			t.Errorf("build user-data does not contain %q", want)
		}
	}
}
