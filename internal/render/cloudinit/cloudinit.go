// Package cloudinit renders a plan as cloud-init user-data.
//
// cloud-init runs its modules in a fixed order, not in the order of the
// configuration. To preserve the declared step order the renderer keeps a
// monotonic phase: an op is rendered with a native module only if that module
// runs at or after the current phase; otherwise it goes into the generated
// run script (executed by runcmd, the last phase).
package cloudinit

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/expr"
	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/render/shell"
)

// Phases in cloud-init execution order.
const (
	phaseWriteFiles = iota // init stage: write_files
	phaseUsers             // init stage: users-groups
	phasePackages          // final stage: package-update-upgrade-install
	phaseDeferred          // final stage: write_files (defer: true)
	phaseScript            // final stage: runcmd (scripts-user)
)

// ScriptPath is where the run script is installed on the target.
const ScriptPath = "/var/lib/osb/run.sh"

// Markers printed to the serial console when building images.
const (
	MarkerOK     = "OSB-BUILD-RESULT: OK"
	MarkerFailed = "OSB-BUILD-RESULT: FAILED"
)

// Options configures rendering.
type Options struct {
	Version string
	Command string
	// Build adds image build handling: result markers on the console,
	// machine-id cleanup and power off.
	Build bool
}

// Result is the rendered cloud-init configuration.
type Result struct {
	UserData []byte
	MetaData []byte
	// Script is the run script embedded in user-data ("" if not needed).
	Script string
	// Native counts ops rendered with native cloud-init modules.
	Native int
}

type item struct {
	comment string
	value   any
}

type renderer struct {
	plan       *engine.Plan
	phase      int
	writeFiles []item
	users      []item
	packages   []string
	hostname   string
	script     []*ops.Step
	notes      []string
	native     int
}

// Render renders user-data and meta-data.
func Render(plan *engine.Plan, opts Options) (*Result, error) {
	r := &renderer{plan: plan}
	for _, s := range plan.Steps {
		r.step(s)
	}
	settings := plan.Artifact("cloud-init")
	if h := expr.Stringify(settings["hostname"]); h != "" && r.hostname == "" {
		r.hostname = h
	}

	res := &Result{Native: r.native}
	var scriptSteps []*ops.Step
	scriptSteps = append(scriptSteps, r.script...)
	if len(scriptSteps) > 0 || opts.Build {
		doc := plan.ShellDoc(opts.Version, opts.Command)
		doc.Steps = scriptSteps
		doc.Total = len(plan.Steps)
		doc.Notes = r.notes
		res.Script = shell.Render(doc)
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, value any, comment string) error {
		var n yaml.Node
		if err := n.Encode(value); err != nil {
			return err
		}
		k := &yaml.Node{Kind: yaml.ScalarNode, Value: key, HeadComment: comment}
		root.Content = append(root.Content, k, &n)
		return nil
	}
	seq := func(key string, items []item, comment string) error {
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range items {
			var c yaml.Node
			if err := c.Encode(it.value); err != nil {
				return err
			}
			c.HeadComment = it.comment
			n.Content = append(n.Content, &c)
		}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key, HeadComment: comment}, n)
		return nil
	}

	if r.hostname != "" {
		_ = add("preserve_hostname", false, "")
		_ = add("hostname", r.hostname, "")
	}
	if len(r.users) > 0 {
		users := append([]item{{value: "default"}}, r.users...)
		if err := seq("users", users, ""); err != nil {
			return nil, err
		}
	}
	if len(r.packages) > 0 {
		_ = add("package_update", true, "")
		if err := add("packages", r.packages, ""); err != nil {
			return nil, err
		}
	}
	wf := r.writeFiles
	if res.Script != "" {
		wf = append(wf, item{comment: "osb run script (executed by runcmd)", value: map[string]any{
			"path": ScriptPath, "permissions": "0700", "defer": true, "content": res.Script,
		}})
	}
	if len(wf) > 0 {
		if err := seq("write_files", wf, ""); err != nil {
			return nil, err
		}
	}
	if res.Script != "" || opts.Build {
		var runcmd []any
		if opts.Build {
			runcmd = append(runcmd, []any{"sh", "-c", buildWrapper()})
		} else {
			runcmd = append(runcmd, []any{"sh", ScriptPath})
		}
		if err := add("runcmd", runcmd, ""); err != nil {
			return nil, err
		}
	}
	if opts.Build {
		_ = add("power_state", map[string]any{"mode": "poweroff", "condition": true, "timeout": 30, "message": "osb: build finished"}, "")
	}
	if extra, ok := settings["extra"].(map[string]any); ok {
		if err := mergeExtra(root, extra); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	header := fmt.Sprintf("#cloud-config\n# Generated by osb %s - do not edit, regenerate instead.\n# Flavor: %s, target: %s\n", opts.Version, plan.Flavor.Name, plan.Target.Name)
	res.UserData = append([]byte(header), out...)

	sum := sha256.Sum256(res.UserData)
	name := plan.Config.Name
	if name == "" {
		name = "osb"
	}
	host := r.hostname
	if host == "" {
		host = name
	}
	res.MetaData = []byte(fmt.Sprintf("instance-id: osb-%s-%s\nlocal-hostname: %s\n", name, hex.EncodeToString(sum[:])[:12], host))
	return res, nil
}

func buildWrapper() string {
	// After configuring: let cloud-init run again on the first boot of every
	// instance (also without a datasource, falling back to DHCP networking),
	// reset the machine id, then report the result on the serial console.
	return fmt.Sprintf(`if sh %s; then r=%q; else r=%q; fi; `+
		`printf 'policy: search,found=all,maybe=all,notfound=enabled\n' > /etc/cloud/ds-identify.cfg; `+
		`cloud-init clean --logs --machine-id >/dev/null 2>&1 || truncate -s 0 /etc/machine-id; `+
		`rm -f /var/lib/dbus/machine-id; sync; `+
		`echo "$r"; echo "$r" > /dev/ttyS0 2>/dev/null || true`,
		ScriptPath, MarkerOK, MarkerFailed)
}

func (r *renderer) toScript(s *ops.Step) {
	r.phase = phaseScript
	r.script = append(r.script, s)
}

func (r *renderer) step(s *ops.Step) {
	if len(s.Guards) > 0 || s.OnError == "continue" {
		r.toScript(s)
		return
	}
	var rest []ops.Op
	comment := fmt.Sprintf("Step %d: %s", s.Index, s.Title)
	nativeKinds := map[string]bool{}
	for _, o := range s.Ops {
		if len(rest) > 0 {
			rest = append(rest, o)
			continue
		}
		if !r.native1(o, comment) {
			rest = append(rest, o)
			continue
		}
		nativeKinds[ops.Name(o)] = true
		r.native++
	}
	if len(nativeKinds) > 0 {
		var kinds []string
		for k := range nativeKinds {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		r.notes = append(r.notes, fmt.Sprintf("Step %d (%s): %s rendered natively in #cloud-config", s.Index, s.Title, strings.Join(kinds, ", ")))
	}
	if len(rest) > 0 {
		c := *s
		c.Ops = rest
		r.toScript(&c)
	}
}

// native1 tries to render an op natively and reports success.
func (r *renderer) native1(o ops.Op, comment string) bool {
	switch t := o.(type) {
	case ops.File:
		if t.Append {
			return false
		}
		own := t.Owner
		if own == "" {
			own = "root"
		}
		phase := phaseDeferred
		if own == "root" && (t.Group == "" || t.Group == "root") && r.phase <= phaseWriteFiles {
			phase = phaseWriteFiles
		}
		if r.phase > phase {
			return false
		}
		r.phase = phase
		entry := map[string]any{"path": t.Path}
		mode := t.Mode
		if mode == "" {
			mode = "0644"
		}
		entry["permissions"] = mode
		grp := t.Group
		if grp == "" {
			grp = own
			if t.Owner == "" {
				grp = "root"
			}
		}
		entry["owner"] = own + ":" + grp
		if shell.IsText(t.Content) {
			entry["content"] = string(t.Content)
		} else {
			entry["encoding"] = "b64"
			entry["content"] = base64.StdEncoding.EncodeToString(t.Content)
		}
		if phase == phaseDeferred {
			entry["defer"] = true
		}
		r.writeFiles = append(r.writeFiles, item{comment: comment, value: entry})
		return true
	case ops.User:
		if r.phase > phaseUsers {
			return false
		}
		r.phase = phaseUsers
		u := map[string]any{"name": t.Name, "lock_passwd": true}
		if t.UID != "" {
			u["uid"] = t.UID
		}
		if t.Comment != "" {
			u["gecos"] = t.Comment
		}
		if t.Home != "" {
			u["homedir"] = t.Home
		}
		if t.Shell != "" {
			u["shell"] = t.Shell
		}
		if len(t.Groups) > 0 {
			u["groups"] = strings.Join(t.Groups, ", ")
		}
		if t.System {
			u["system"] = true
		}
		if !t.CreateHome {
			u["no_create_home"] = true
		}
		r.users = append(r.users, item{comment: comment, value: u})
		return true
	case ops.Packages:
		if r.phase > phasePackages {
			return false
		}
		r.phase = phasePackages
		for _, p := range t.Names {
			if !contains(r.packages, p) {
				r.packages = append(r.packages, p)
			}
		}
		return true
	case ops.Hostname:
		if r.hostname != "" {
			return false
		}
		r.hostname = t.Name
		return true
	case ops.Note:
		return true
	}
	return false
}

func contains(l []string, s string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}

// mergeExtra merges user supplied cloud-config keys: lists are appended,
// other values replace generated ones.
func mergeExtra(root *yaml.Node, extra map[string]any) error {
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var n yaml.Node
		if err := n.Encode(extra[k]); err != nil {
			return err
		}
		replaced := false
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != k {
				continue
			}
			if root.Content[i+1].Kind == yaml.SequenceNode && n.Kind == yaml.SequenceNode {
				root.Content[i+1].Content = append(root.Content[i+1].Content, n.Content...)
			} else {
				root.Content[i+1] = &n
			}
			replaced = true
		}
		if !replaced {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &n)
		}
	}
	return nil
}

// WriteFiles writes user-data, meta-data and (when a tool is available) seed.iso.
func WriteFiles(res *Result, dir string, secrets bool) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o644)
	if secrets {
		mode = 0o600
	}
	ud := filepath.Join(dir, "user-data")
	md := filepath.Join(dir, "meta-data")
	if err := os.WriteFile(ud, res.UserData, mode); err != nil {
		return nil, err
	}
	if err := os.WriteFile(md, res.MetaData, 0o644); err != nil {
		return nil, err
	}
	written := []string{ud, md}
	iso := filepath.Join(dir, "seed.iso")
	if err := MakeSeedISO(iso, ud, md); err == nil {
		written = append(written, iso)
	}
	return written, nil
}

// MakeSeedISO creates a NoCloud seed image with an available ISO tool.
func MakeSeedISO(out, userData, metaData string) error {
	tools := [][]string{
		{"cloud-localds", out, userData, metaData},
		{"xorriso", "-as", "mkisofs", "-output", out, "-volid", "cidata", "-joliet", "-rock", userData, metaData},
		{"genisoimage", "-output", out, "-volid", "cidata", "-joliet", "-rock", userData, metaData},
		{"mkisofs", "-output", out, "-volid", "cidata", "-joliet", "-rock", userData, metaData},
	}
	for _, t := range tools {
		if _, err := exec.LookPath(t[0]); err != nil {
			continue
		}
		cmd := exec.Command(t[0], t[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %s", t[0], err, out)
		}
		return nil
	}
	return fmt.Errorf("no ISO tool found (cloud-localds, xorriso, genisoimage or mkisofs)")
}
