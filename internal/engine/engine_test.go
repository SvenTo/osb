package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/render/shell"
	"github.com/SvenTo/osb/internal/schema"
)

func load(t *testing.T, files map[string]string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join(dir, "osb.yaml"), config.LoadOptions{Validate: schema.Validate})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func eval(t *testing.T, cfg *config.Config, fl, tg string, args map[string]string) *Plan {
	t.Helper()
	p, err := Evaluate(cfg, Options{Flavor: fl, Target: tg, Args: ArgInput{CLI: args}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func script(p *Plan) string {
	return shell.Render(p.ShellDoc("test", ""))
}

func TestConditionsOutputsForeach(t *testing.T) {
	cfg := load(t, map[string]string{"osb.yaml": `
arguments:
  user: { default: sven }
  users: { type: array, default: [a, b] }
steps:
  - name: first
    action: write-file
    if: args.user == "sven"
    pre-script: var id = args.user.toUpperCase()
    args: { path: "/etc/${{ id }}", content: "hi ${{ id }}\n" }
    output: { id: "${{ id }}" }
  - name: skipped
    action: log
    if: "false"
    args: { message: never }
  - action: log
    args: { message: "out=${{ steps.first.output.id }} skipped=${{ steps.skipped.enabled }}" }
  - action: add-user
    foreach: ${{ args.users }}
    args: { name: "u-${{ item }}-${{ index }}" }
`})
	p := eval(t, cfg, "ubuntu", "shell", nil)
	s := script(p)
	for _, want := range []string{"write_file /etc/SVEN 0644", "hi SVEN", "out=SVEN skipped=false", "useradd --create-home u-a-0", "u-b-1"} {
		if !strings.Contains(s, want) {
			t.Errorf("script does not contain %q", want)
		}
	}
	if strings.Contains(s, "never") {
		t.Error("skipped step was rendered")
	}
	if len(p.Steps) != 4 {
		t.Errorf("got %d steps, want 4", len(p.Steps))
	}
}

func TestFlavorFiltersAndByFlavor(t *testing.T) {
	cfg := load(t, map[string]string{"osb.yaml": `
flavors:
  archlinux: { vars: { greeting: servus } }
  mint: { based-on: ubuntu }
steps:
  - action: install-packages
    args:
      packages:
        by-flavor: { arch: [pacman-contrib], debian: [apt-utils] }
  - action: log
    flavors: [archlinux]
    args: { message: "${{ flavor.vars.greeting }}" }
  - action: install-packages
    args: { packages: [build-essential] }
`})
	arch := script(eval(t, cfg, "archlinux", "shell", nil))
	if !strings.Contains(arch, "pkg_install pacman-contrib") || !strings.Contains(arch, "log servus") || !strings.Contains(arch, "pkg_install base-devel") {
		t.Errorf("archlinux script:\n%s", arch)
	}
	mint := eval(t, cfg, "mint", "shell", nil)
	ms := script(mint)
	if !strings.Contains(ms, "pkg_install apt-utils") || strings.Contains(ms, "servus") || !strings.Contains(ms, "check_flavor mint ubuntu") {
		t.Errorf("mint script:\n%s", ms)
	}
}

func TestCustomActionsAndImports(t *testing.T) {
	cfg := load(t, map[string]string{
		"osb.yaml": `
imports: [lib/*.yaml]
steps:
  - name: greet
    action: greet
    args: { who: world }
  - action: log
    args: { message: "file=${{ steps.greet.output.file }}" }
`,
		"lib/greet.yaml": `
actions:
  greet:
    arguments:
      who: { required: true }
    output: { file: "/tmp/${{ args.who }}" }
    steps:
      - action: write-file
        args: { path: "/tmp/${{ args.who }}", content: "hello ${{ args.who }}\n" }
`,
	})
	s := script(eval(t, cfg, "debian", "shell", nil))
	if !strings.Contains(s, "hello world") || !strings.Contains(s, "file=/tmp/world") || !strings.Contains(s, "greet › write-file") {
		t.Errorf("script:\n%s", s)
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"unknown action":     "steps:\n  - action: nope\n",
		"recursive":          "actions:\n  a:\n    steps:\n      - action: a\nsteps:\n  - action: a\n",
		"duplicate name":     "steps:\n  - {name: x, action: upgrade-system}\n  - {name: x, action: upgrade-system}\n",
		"missing arg":        "steps:\n  - action: write-file\n    args: {content: x}\n",
		"pre-validation":     "steps:\n  - action: upgrade-system\n    pre-validation: 1 == 2\n",
		"relative path":      "steps:\n  - action: write-file\n    args: {path: etc/x, content: x}\n",
		"required argument":  "arguments:\n  x: {required: true}\nsteps: []\n",
		"pattern":            "arguments:\n  x: {default: Abc, pattern: '^[a-z]+$'}\n",
		"add-repository apt": "steps:\n  - action: add-repository\n    args: {name: x, pacman: {server: http://x}}\n",
	}
	for name, yml := range cases {
		dir := t.TempDir()
		p := filepath.Join(dir, "osb.yaml")
		os.WriteFile(p, []byte(yml), 0o644)
		cfg, err := config.Load(p, config.LoadOptions{Validate: schema.Validate})
		if err != nil {
			continue // rejected by the schema
		}
		if _, err := Evaluate(cfg, Options{Flavor: "ubuntu"}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSchemaErrorsHaveLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "osb.yaml")
	os.WriteFile(p, []byte("steps:\n  - action: write-file\n    args:\n      path: /x\n      contnet: typo\n"), 0o644)
	_, err := config.Load(p, config.LoadOptions{Validate: schema.Validate})
	if err == nil || !strings.Contains(err.Error(), ":5:") || !strings.Contains(err.Error(), "contnet") {
		t.Errorf("got %v", err)
	}
}

func TestSecretsAndGuards(t *testing.T) {
	cfg := load(t, map[string]string{"osb.yaml": `
arguments:
  pw: { type: secret, default: s3cr3t-value }
steps:
  - action: set-password
    args: { user: root, password: "${{ args.pw }}" }
  - action: run-command
    creates: /done
    on-error: continue
    args: { command: "touch /done" }
`})
	p := eval(t, cfg, "ubuntu", "shell", nil)
	if !p.Steps[0].Secret || p.Steps[1].Secret {
		t.Error("secret detection is wrong")
	}
	if got := p.Secrets.String("pw is s3cr3t-value"); got != "pw is ***" {
		t.Errorf("redaction: %q", got)
	}
	if g := p.Steps[1].Guards; len(g) != 1 || g[0] != (ops.Guard{Kind: "creates", Value: "/done"}) {
		t.Errorf("guards: %v", g)
	}
	s := script(p)
	if !strings.Contains(s, "if [ ! -e /done ]; then") || !strings.Contains(s, "set +e\n(\nset -e\ntouch /done") {
		t.Errorf("script:\n%s", s)
	}
}

func TestTargetSpecificBehaviour(t *testing.T) {
	cfg := load(t, map[string]string{"osb.yaml": `
targets: [shell, dockerfile]
steps:
  - action: set-hostname
    args: { hostname: box }
  - action: log
    targets: [dockerfile]
    args: { message: only-in-docker }
`})
	sh := script(eval(t, cfg, "ubuntu", "shell", nil))
	if !strings.Contains(sh, "hostnamectl") || strings.Contains(sh, "only-in-docker") {
		t.Errorf("shell:\n%s", sh)
	}
	oci := eval(t, cfg, "ubuntu", "oci", nil)
	if _, ok := oci.Steps[0].Ops[0].(ops.Note); !ok {
		t.Errorf("hostname should be skipped for containers: %#v", oci.Steps[0].Ops)
	}
	if len(oci.Steps) != 2 {
		t.Errorf("oci steps: %d", len(oci.Steps))
	}
	if _, err := Evaluate(cfg, Options{Flavor: "ubuntu", Target: "qcow2"}); err == nil {
		t.Error("qcow2 should not be allowed")
	}
}

func TestArgTypes(t *testing.T) {
	cfg := load(t, map[string]string{"osb.yaml": `
arguments:
  n: { type: int, default: 1 }
  b: { type: bool, default: false }
  o: { type: option, options: [x, y], default: x }
  l: { type: array }
  d: { default: "${{ args.o }}-${{ args.n }}" }
`})
	p := eval(t, cfg, "ubuntu", "shell", map[string]string{"n": "5", "b": "true", "o": "y", "l": "a, b"})
	want := map[string]any{"n": int64(5), "b": true, "o": "y", "l": []any{"a", "b"}, "d": "y-5"}
	for k, v := range want {
		if got := p.Args[k]; !equal(got, v) {
			t.Errorf("%s = %#v, want %#v", k, got, v)
		}
	}
	if _, err := Evaluate(cfg, Options{Flavor: "ubuntu", Args: ArgInput{CLI: map[string]string{"o": "z"}}}); err == nil {
		t.Error("invalid option accepted")
	}
}

func equal(a, b any) bool { return reflect.DeepEqual(a, b) }
