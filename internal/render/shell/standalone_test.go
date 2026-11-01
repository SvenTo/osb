package shell

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/SvenTo/osb/internal/flavor"
	"github.com/SvenTo/osb/internal/ops"
)

func doc(t *testing.T, colors bool, steps ...*ops.Step) string {
	fl, _ := flavor.Builtin("archlinux")
	for i, s := range steps {
		s.Index = i + 1
	}
	return Render(&Doc{Name: "t", Source: "osb.yaml", Flavor: fl, Target: "shell", Steps: steps, Colors: colors})
}

func defined(script string) []string {
	var out []string
	for _, l := range strings.Split(script, "\n") {
		if m := funcStart.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func TestOnlyNeededHelpers(t *testing.T) {
	s := doc(t, false, &ops.Step{Title: "hi", Ops: []ops.Op{ops.Shell{Script: "echo hi\n"}}})
	if got := strings.Join(defined(s), " "); got != "log die step require_root check_flavor" {
		t.Errorf("helpers = %q", got)
	}
	if strings.Contains(s, "OSB_C_") || strings.Contains(s, "OSB_FAILED") {
		t.Error("unexpected colors or failure counter")
	}
	// Packages pull in the package manager functions and their dependencies.
	s = doc(t, false, &ops.Step{Title: "p", Ops: []ops.Op{ops.Packages{Names: []string{"git"}, Missing: true}}})
	for _, want := range []string{"pkg_ensure", "pkg_installed", "pkg_install", "pkg_update", "OSB_PKG_UPDATED=0"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	// on-error: continue needs the failure counter and the summary.
	s = doc(t, false, &ops.Step{Title: "c", OnError: "continue", Ops: []ops.Op{ops.Shell{Script: "false\n"}}})
	if !strings.Contains(s, "osb_finish()") || !strings.Contains(s, "OSB_FAILED=0") || !strings.Contains(s, "warn()") {
		t.Error("on-error: continue helpers missing")
	}
	// run it here: without the root and distribution checks
	run := regexp.MustCompile(`(?m)^(require_root|check_flavor .*)$`).ReplaceAllString(s, "")
	out, err := exec.Command("sh", "-c", run).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "finished with 1 failed step(s)") {
		t.Errorf("script failed: %v\n%s", err, out)
	}
}

func TestColors(t *testing.T) {
	s := doc(t, true, &ops.Step{Title: "hi", Ops: []ops.Op{ops.Shell{Script: "echo hi\n"}}})
	if !strings.Contains(s, "[ -t 1 ]") || !strings.Contains(s, "$OSB_C_BOLD") {
		t.Error("colors missing")
	}
	if strings.Count(s, "step()") != 1 {
		t.Error("step defined twice")
	}
}
