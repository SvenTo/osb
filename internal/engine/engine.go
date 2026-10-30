// Package engine evaluates a configuration for a flavor and target into a
// list of steps made of primitive ops.
package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/SvenTo/osb/internal/actions"
	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/expr"
	"github.com/SvenTo/osb/internal/flavor"
	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/redact"
	"github.com/SvenTo/osb/internal/render/shell"
	"github.com/SvenTo/osb/internal/target"
)

// Options configures Evaluate.
type Options struct {
	Flavor string
	Target string
	Args   ArgInput
}

// Plan is the result of evaluating a configuration.
type Plan struct {
	Config    *config.Config
	Flavor    *flavor.Flavor
	Target    target.Target
	Args      map[string]any
	Steps     []*ops.Step
	Artifacts map[string]map[string]any
	Warnings  []string
	Secrets   *redact.Set
}

// Artifact returns the rendered settings for a target (never nil).
func (p *Plan) Artifact(name string) map[string]any {
	if m, ok := p.Artifacts[name]; ok {
		return m
	}
	return map[string]any{}
}

// ShellDoc converts the plan into the input of the shell renderer.
func (p *Plan) ShellDoc(version, command string) *shell.Doc {
	return &shell.Doc{
		Name:    p.Config.Name,
		Source:  config.DisplayPath(p.Config.File),
		Flavor:  p.Flavor,
		Target:  p.Target.Name,
		Command: command,
		Version: version,
		Steps:   p.Steps,
	}
}

var stepNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// EnabledFlavors returns the names of the flavors a config supports.
func EnabledFlavors(cfg *config.Config) []string {
	var names []string
	seen := map[string]bool{}
	for _, fc := range cfg.Flavors {
		seen[fc.Name] = true
		if fc.IsEnabled() {
			names = append(names, fc.Name)
		}
	}
	for _, n := range flavor.BuiltinNames() {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// ResolveFlavor returns the flavor with config overrides applied.
func ResolveFlavor(cfg *config.Config, name string) (*flavor.Flavor, error) {
	enabled := EnabledFlavors(cfg)
	if name == "" {
		if len(enabled) == 1 {
			name = enabled[0]
		} else {
			return nil, fmt.Errorf("select a flavor with --flavor (supported: %s)", strings.Join(enabled, ", "))
		}
	}
	fc := cfg.Flavors.Get(name)
	if fc != nil && !fc.IsEnabled() {
		return nil, fmt.Errorf("flavor %q is disabled in %s", name, fc.Pos)
	}
	var fl *flavor.Flavor
	if fc != nil && fc.BasedOn != "" {
		var err error
		if fl, err = flavor.Derive(name, fc.BasedOn); err != nil {
			return nil, fc.Pos.Errorf("%v", err)
		}
	} else {
		var ok bool
		if fl, ok = flavor.Builtin(name); !ok {
			return nil, fmt.Errorf("unknown flavor %q (built-in: %s; define custom flavors with based-on)", name, strings.Join(flavor.BuiltinNames(), ", "))
		}
	}
	if fc != nil {
		if fc.BaseImage != "" {
			fl.BaseImage = fc.BaseImage
		}
		if fc.CloudImage != "" {
			fl.CloudImage = fc.CloudImage
		}
		for k, v := range fc.Vars {
			fl.Vars[k] = v
		}
	}
	return fl, nil
}

// TargetAllowed reports whether the config permits a target.
func TargetAllowed(cfg *config.Config, t target.Target) bool {
	if len(cfg.Targets) == 0 {
		return true
	}
	for _, n := range cfg.Targets {
		if n == t.Name || n == t.Renderer {
			return true
		}
	}
	return false
}

// Evaluate evaluates a configuration.
func Evaluate(cfg *config.Config, opts Options) (*Plan, error) {
	fl, err := ResolveFlavor(cfg, opts.Flavor)
	if err != nil {
		return nil, err
	}
	tname := opts.Target
	if tname == "" {
		tname = "shell"
	}
	tg, err := target.Get(tname)
	if err != nil {
		return nil, err
	}
	if !TargetAllowed(cfg, tg) {
		return nil, fmt.Errorf("target %q is not enabled in this configuration (targets: %s)", tg.Name, strings.Join(cfg.Targets, ", "))
	}
	plan := &Plan{Config: cfg, Flavor: fl, Target: tg, Secrets: &redact.Set{}, Artifacts: map[string]map[string]any{}}
	plan.Args, err = ResolveArgs(cfg.Arguments, opts.Args, plan.Secrets, cfg.Dir)
	if err != nil {
		return nil, err
	}
	ev := &evaluator{plan: plan, cfg: cfg}
	root := &scope{args: plan.Args, results: map[string]any{}, dir: cfg.Dir, onError: "fail", vars: map[string]any{}}
	if err := ev.steps(cfg.Steps, root); err != nil {
		return nil, err
	}
	for i, s := range plan.Steps {
		s.Index = i + 1
	}
	// Artifact settings may refer to arguments and step outputs.
	e := ev.newEngine(root, cfg.Dir)
	for name, m := range cfg.Artifacts {
		v, err := e.Render(map[string]any(m))
		if err != nil {
			return nil, fmt.Errorf("artifacts.%s: %w", name, err)
		}
		plan.Artifacts[name] = resolveByFlavor(v, fl).(map[string]any)
	}
	return plan, nil
}

type evaluator struct {
	plan  *Plan
	cfg   *config.Config
	stack []string
}

type scope struct {
	args    map[string]any
	results map[string]any // step name -> {enabled, output}
	dir     string
	guards  []ops.Guard
	onError string
	prefix  string
	vars    map[string]any
}

func (ev *evaluator) warn(msg string) {
	for _, w := range ev.plan.Warnings {
		if w == msg {
			return
		}
	}
	ev.plan.Warnings = append(ev.plan.Warnings, msg)
}

func (ev *evaluator) newEngine(sc *scope, dir string) *expr.Engine {
	e := expr.New(dir)
	e.Set("args", sc.args)
	e.Set("root", map[string]any{"args": ev.plan.Args})
	e.Set("flavor", ev.plan.Flavor.JS())
	e.Set("target", ev.plan.Target.JS())
	e.Set("steps", sc.results)
	for k, v := range sc.vars {
		e.Set(k, v)
	}
	return e
}

func matches(list []string, names ...string) bool {
	for _, l := range list {
		for _, n := range names {
			if n != "" && l == n {
				return true
			}
		}
	}
	return false
}

func (ev *evaluator) steps(steps []*config.Step, sc *scope) error {
	seen := map[string]*config.Step{}
	for _, s := range steps {
		if s.Name != "" {
			if !stepNameRe.MatchString(s.Name) {
				return s.Pos.Errorf("invalid step name %q (allowed: A-Z a-z 0-9 _ -)", s.Name)
			}
			if prev, dup := seen[s.Name]; dup {
				return s.Pos.Errorf("duplicate step name %q (first defined at %s)", s.Name, prev.Pos)
			}
			seen[s.Name] = s
		}
		if err := ev.step(s, sc); err != nil {
			return err
		}
	}
	return nil
}

func (ev *evaluator) record(sc *scope, s *config.Step, enabled bool, output any) {
	if s.Name == "" {
		return
	}
	if output == nil {
		output = map[string]any{}
	}
	sc.results[s.Name] = map[string]any{"enabled": enabled, "output": output}
}

func (ev *evaluator) step(s *config.Step, sc *scope) error {
	fl, tg := ev.plan.Flavor, ev.plan.Target
	if len(s.Flavors) > 0 && !matches(s.Flavors, fl.Name, fl.BasedOn, fl.Family) {
		ev.record(sc, s, false, nil)
		return nil
	}
	if len(s.Targets) > 0 && !matches(s.Targets, tg.Name, tg.Renderer) {
		ev.record(sc, s, false, nil)
		return nil
	}
	if s.Action == "" && len(s.Steps) == 0 {
		return s.Pos.Errorf("step %q needs an action or nested steps", s.Label())
	}
	if s.Action != "" && len(s.Steps) > 0 {
		return s.Pos.Errorf("step %q has both an action and nested steps", s.Label())
	}
	if s.OnError != "" && s.OnError != "fail" && s.OnError != "continue" {
		return s.Pos.Errorf("on-error must be fail or continue, got %q", s.OnError)
	}
	if s.Foreach.Pos.Line == 0 {
		out, enabled, err := ev.one(s, sc, nil)
		if err != nil {
			return err
		}
		ev.record(sc, s, enabled, out)
		return nil
	}
	e := ev.newEngine(sc, s.Dir)
	items, err := ev.foreachItems(e, s)
	if err != nil {
		return s.Pos.Errorf("foreach: %v", err)
	}
	var outputs []any
	anyEnabled := false
	for i, item := range items {
		out, enabled, err := ev.one(s, sc, &loop{item: item, index: i})
		if err != nil {
			return err
		}
		anyEnabled = anyEnabled || enabled
		outputs = append(outputs, out)
	}
	if outputs == nil {
		outputs = []any{}
	}
	ev.record(sc, s, anyEnabled, outputs)
	return nil
}

type loop struct {
	item  any
	index int
}

func (ev *evaluator) foreachItems(e *expr.Engine, s *config.Step) ([]any, error) {
	v := s.Foreach.V
	if str, ok := v.(string); ok {
		var err error
		if expr.HasTemplate(str) {
			v, err = e.Template(str)
		} else {
			v, err = e.Eval(str)
		}
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		if v, err = e.Render(v); err != nil {
			return nil, err
		}
	}
	switch t := v.(type) {
	case []any:
		return t, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []any
		for _, k := range keys {
			out = append(out, map[string]any{"key": k, "value": t[k]})
		}
		return out, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("expected a list or mapping, got %T", v)
}

// one evaluates a single step (or one foreach iteration). It returns the
// step's output and whether it was enabled.
func (ev *evaluator) one(s *config.Step, sc *scope, lp *loop) (any, bool, error) {
	vars := map[string]any{}
	for k, v := range sc.vars {
		vars[k] = v
	}
	if lp != nil {
		vars["item"] = lp.item
		vars["index"] = int64(lp.index)
	}
	local := &scope{args: sc.args, results: sc.results, dir: s.Dir, guards: sc.guards, onError: sc.onError, prefix: sc.prefix, vars: vars}
	e := ev.newEngine(local, s.Dir)
	errorf := func(format string, a ...any) error {
		return s.Pos.Errorf("step %q: %s", s.Label(), ev.plan.Secrets.String(fmt.Sprintf(format, a...)))
	}

	if s.If != "" {
		ok, err := e.Truthy(string(s.If))
		if err != nil {
			return nil, false, errorf("if: %v", err)
		}
		if !ok {
			return nil, false, nil
		}
	}
	if s.PreScript != "" {
		before := e.GlobalNames()
		if err := e.Run(s.PreScript); err != nil {
			return nil, false, errorf("pre-script: %v", err)
		}
		for n := range e.GlobalNames() {
			if !before[n] {
				if v := e.Get(n); v != nil {
					vars[n] = v
				}
			}
		}
	}
	if s.PreValidation != "" {
		ok, err := e.Truthy(s.PreValidation)
		if err != nil {
			return nil, false, errorf("pre-validation: %v", err)
		}
		if !ok {
			return nil, false, errorf("pre-validation failed: %s", s.PreValidation)
		}
	}

	title, err := e.TemplateString(s.Label())
	if err != nil {
		return nil, false, errorf("title: %v", err)
	}
	if lp != nil && !expr.HasTemplate(s.Title) {
		if _, scalar := lp.item.(map[string]any); !scalar {
			title += " (" + expr.Stringify(lp.item) + ")"
		}
	}
	if sc.prefix != "" {
		title = sc.prefix + " › " + title
	}

	guards := append([]ops.Guard{}, sc.guards...)
	for _, g := range []struct{ kind, val string }{{"only-if", s.OnlyIf}, {"unless", s.Unless}, {"creates", s.Creates}} {
		if g.val == "" {
			continue
		}
		v, err := e.TemplateString(g.val)
		if err != nil {
			return nil, false, errorf("%s: %v", g.kind, err)
		}
		guards = append(guards, ops.Guard{Kind: g.kind, Value: v})
	}
	onError := sc.onError
	if s.OnError != "" {
		onError = s.OnError
	}

	var reqOps []ops.Op
	if s.Requires.Packages.Pos.Line != 0 {
		v, err := e.Render(s.Requires.Packages.V)
		if err != nil {
			return nil, false, errorf("requires: %v", err)
		}
		names, err := packagesFor(v, ev.plan.Flavor)
		if err != nil {
			return nil, false, errorf("requires.packages: %v", err)
		}
		if len(names) > 0 {
			reqOps = append(reqOps, ops.Packages{Names: ev.plan.Flavor.Packages(names), Missing: true})
		}
	}

	var stepOps []ops.Op
	var actionOutput map[string]any
	child := &scope{args: sc.args, results: sc.results, dir: s.Dir, guards: guards, onError: onError, prefix: title, vars: vars}
	startIdx := len(ev.plan.Steps)
	switch {
	case len(s.Steps) > 0:
		if len(reqOps) > 0 {
			ev.plan.Steps = append(ev.plan.Steps, &ops.Step{Title: title + " › requirements", Name: s.Name, Ops: reqOps, Guards: guards, OnError: onError, Source: s.Pos.String()})
		}
		if err := ev.steps(s.Steps, child); err != nil {
			return nil, false, err
		}
	default:
		if b, ok := actions.Get(s.Action); ok {
			stepOps, err = ev.builtin(b, s, e)
			if err != nil {
				return nil, false, errorf("%v", err)
			}
			stepOps = append(reqOps, stepOps...)
		} else if def := ev.cfg.Actions.Get(s.Action); def != nil {
			if len(reqOps) > 0 {
				ev.plan.Steps = append(ev.plan.Steps, &ops.Step{Title: title + " › requirements", Name: s.Name, Action: s.Action, Ops: reqOps, Guards: guards, OnError: onError, Source: s.Pos.String()})
			}
			actionOutput, err = ev.custom(def, s, e, child)
			if err != nil {
				return nil, false, err
			}
		} else {
			return nil, false, errorf("unknown action %q (see `osb actions list`)", s.Action)
		}
	}

	if b, ok := actions.Get(s.Action); ok && len(s.Steps) == 0 {
		step := &ops.Step{Name: s.Name, Title: title, Action: b.Name, Ops: stepOps, Guards: guards, OnError: onError, Source: s.Pos.String()}
		for _, o := range stepOps {
			if n, ok := o.(ops.Note); ok && n.Warn {
				ev.warn(n.Message)
			}
		}
		body := shell.Body(step, ev.plan.Flavor)
		step.Secret = ev.plan.Secrets.Contains(body)
		ev.plan.Steps = append(ev.plan.Steps, step)
	}

	output := map[string]any{}
	for k, v := range actionOutput {
		output[k] = v
	}
	if len(s.Output) > 0 {
		rv, err := e.Render(map[string]any(s.Output))
		if err != nil {
			return nil, false, errorf("output: %v", err)
		}
		for k, v := range rv.(map[string]any) {
			output[k] = v
		}
	}
	if s.PostValidation != "" {
		var rendered strings.Builder
		for _, st := range ev.plan.Steps[startIdx:] {
			rendered.WriteString(shell.Body(st, ev.plan.Flavor))
		}
		e.Set("output", output)
		e.Set("rendered", rendered.String())
		ok, err := e.Truthy(s.PostValidation)
		if err != nil {
			return nil, false, errorf("post-validation: %v", err)
		}
		if !ok {
			return nil, false, errorf("post-validation failed: %s", s.PostValidation)
		}
	}
	return output, true, nil
}

func (ev *evaluator) builtin(b *actions.Action, s *config.Step, e *expr.Engine) ([]ops.Op, error) {
	rendered, err := e.Render(map[string]any(s.Args))
	if err != nil {
		return nil, fmt.Errorf("args.%v", err)
	}
	raw := resolveByFlavor(rendered, ev.plan.Flavor).(map[string]any)
	args, err := actions.Coerce(b, raw)
	if err != nil {
		return nil, err
	}
	ctx := &actions.Ctx{
		Flavor:   ev.plan.Flavor,
		Target:   ev.plan.Target,
		Dir:      s.Dir,
		Template: e.TemplateString,
		Warn:     ev.warn,
	}
	res, err := b.Build(ctx, args)
	if err != nil {
		return nil, err
	}
	if len(b.Requires) > 0 {
		res = append([]ops.Op{ops.Packages{Names: ev.plan.Flavor.Packages(b.Requires), Missing: true}}, res...)
	}
	return res, nil
}

func (ev *evaluator) custom(def *config.ActionDef, s *config.Step, e *expr.Engine, child *scope) (map[string]any, error) {
	for _, n := range ev.stack {
		if n == def.Name {
			return nil, s.Pos.Errorf("recursive use of action %q (%s)", def.Name, strings.Join(append(ev.stack, def.Name), " → "))
		}
	}
	if len(def.Flavors) > 0 && !matches(def.Flavors, ev.plan.Flavor.Name, ev.plan.Flavor.BasedOn, ev.plan.Flavor.Family) {
		return nil, s.Pos.Errorf("action %q does not support flavor %s (supported: %s)", def.Name, ev.plan.Flavor.Name, strings.Join(def.Flavors, ", "))
	}
	ev.stack = append(ev.stack, def.Name)
	defer func() { ev.stack = ev.stack[:len(ev.stack)-1] }()

	rendered, err := e.Render(map[string]any(s.Args))
	if err != nil {
		return nil, s.Pos.Errorf("step %q: args.%v", s.Label(), err)
	}
	raw := resolveByFlavor(rendered, ev.plan.Flavor).(map[string]any)
	args, err := ResolveArgs(def.Arguments, ArgInput{Values: raw}, ev.plan.Secrets, def.Dir)
	if err != nil {
		return nil, s.Pos.Errorf("step %q (action %s): %v", s.Label(), def.Name, err)
	}
	inner := &scope{args: args, results: map[string]any{}, dir: def.Dir, guards: child.guards, onError: child.onError, prefix: child.prefix, vars: map[string]any{}}
	if err := ev.steps(def.Steps, inner); err != nil {
		return nil, fmt.Errorf("%w\n  in action %s called at %s", err, def.Name, s.Pos)
	}
	if len(def.Output) == 0 {
		return nil, nil
	}
	oe := ev.newEngine(inner, def.Dir)
	out, err := oe.Render(map[string]any(def.Output))
	if err != nil {
		return nil, def.Pos.Errorf("action %s: output: %v", def.Name, err)
	}
	return out.(map[string]any), nil
}

// resolveByFlavor replaces {by-flavor: {...}} mappings with the value for the
// current flavor (name, based-on, family, then default).
func resolveByFlavor(v any, fl *flavor.Flavor) any {
	switch t := v.(type) {
	case map[string]any:
		if bf, ok := t["by-flavor"].(map[string]any); ok && len(t) == 1 {
			for _, k := range []string{fl.Name, fl.BasedOn, fl.Family, "default"} {
				if k == "" {
					continue
				}
				if val, ok := bf[k]; ok {
					return resolveByFlavor(val, fl)
				}
			}
			return nil
		}
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = resolveByFlavor(vv, fl)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = resolveByFlavor(vv, fl)
		}
		return out
	}
	return v
}

func packagesFor(v any, fl *flavor.Flavor) ([]string, error) {
	v = resolveByFlavor(v, fl)
	if m, ok := v.(map[string]any); ok {
		v = resolveByFlavor(map[string]any{"by-flavor": m}, fl)
	}
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		return strings.Fields(t), nil
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, expr.Stringify(e))
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected a list of packages or a mapping of flavor to packages")
}
