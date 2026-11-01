// Package schema generates the JSON schema of the configuration format and
// validates documents against it.
package schema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/SvenTo/osb/internal/actions"
	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/target"
	"github.com/SvenTo/osb/library"
)

// ID is the schema identifier.
const ID = "https://github.com/SvenTo/osb/schema.json"

type obj = map[string]any

func str(desc string) obj { return obj{"type": "string", "description": desc} }

func templatable(types ...string) obj {
	return obj{"type": append(types, "string")}
}

func argSchema(s actions.ArgSpec) obj {
	var o obj
	switch s.Type {
	case actions.TString, actions.TText:
		o = obj{"type": []string{"string", "number", "boolean"}}
	case actions.TBool:
		o = templatable("boolean")
	case actions.TInt:
		o = templatable("integer")
	case actions.TMode:
		o = templatable("integer")
	case actions.TList:
		o = obj{"type": []string{"array", "string", "number"}}
	case actions.TMap:
		o = obj{"type": "object"}
	default:
		o = obj{}
	}
	o = obj{"anyOf": []any{o, obj{"$ref": "#/$defs/byFlavor"}}}
	desc := s.Description
	if s.Default != nil {
		desc += fmt.Sprintf(" (default: %v)", s.Default)
	}
	if len(s.Enum) > 0 {
		desc += " One of: " + strings.Join(s.Enum, ", ") + "."
	}
	o["description"] = desc
	return o
}

func artifactSchemas() obj {
	strList := obj{"type": "array", "items": obj{"type": "string"}}
	return obj{
		"dockerfile": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"base-image": str("Base image (default per flavor)"),
			"env":        obj{"type": "object", "description": "ENV instructions"},
			"user":       str("Final USER"),
			"workdir":    str("Final WORKDIR"),
			"entrypoint": obj{"type": []string{"array", "string"}, "description": "ENTRYPOINT"},
			"cmd":        obj{"type": []string{"array", "string"}, "description": "CMD"},
			"expose":     obj{"type": []string{"array", "string"}, "description": "EXPOSE ports"},
			"labels":     obj{"type": "object", "description": "LABEL instructions"},
			"volumes":    strList,
		}},
		"oci": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"tags":     obj{"type": []string{"array", "string"}, "description": "Image tags"},
			"push":     obj{"type": []string{"boolean", "string"}, "description": "Push after building"},
			"archive":  str("Write an OCI archive to this file instead of loading the image"),
			"platform": str("Target platform, e.g. linux/arm64"),
			"builder":  str("Container CLI: docker (default) or podman"),
		}},
		"wsl": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"default-user": str("Default user of the distribution"),
			"systemd":      obj{"type": []string{"boolean", "string"}, "description": "Enable systemd (default true)"},
			"hostname":     str("Host name"),
			"name":         str("Distribution name shown in WSL (default: config name)"),
		}},
		"qcow2": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"base-image":  str("Cloud image URL or path (default per flavor)"),
			"sha256":      str("Expected SHA-256 of the base image"),
			"disk-size":   str("Disk size, e.g. 20G"),
			"memory":      obj{"type": []string{"integer", "string"}, "description": "Memory in MiB"},
			"cpus":        obj{"type": []string{"integer", "string"}, "description": "CPU count"},
			"driver":      str("qemu or libvirt"),
			"timeout":     str("Build timeout, e.g. 30m"),
			"compress":    obj{"type": []string{"boolean", "string"}, "description": "Compress the image"},
			"libvirt-uri": str("libvirt connection URI (default qemu:///session)"),
		}},
		"ova": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"memory":  obj{"type": []string{"integer", "string"}, "description": "Memory in MiB"},
			"cpus":    obj{"type": []string{"integer", "string"}, "description": "CPU count"},
			"os-type": str("VMware guest OS identifier"),
			"name":    str("Virtual machine name"),
		}},
		"cloud-init": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"hostname": str("Host name"),
			"extra":    obj{"type": "object", "description": "Merged verbatim into #cloud-config"},
		}},
		"shell": obj{"type": "object", "additionalProperties": false, "properties": obj{}},
	}
}

func stepSchema() obj {
	props := obj{
		"name":            obj{"type": "string", "pattern": "^[A-Za-z0-9_-]+$", "description": "Step name, used to reference outputs (steps[name].output)"},
		"title":           str("Title shown in logs"),
		"action":          str("Built-in or custom action"),
		"if":              obj{"type": []string{"string", "boolean"}, "description": "JavaScript condition evaluated at generation time"},
		"flavors":         obj{"type": []string{"array", "string"}, "items": obj{"type": "string"}, "description": "Only run for these flavors"},
		"targets":         obj{"type": []string{"array", "string"}, "items": obj{"type": "string"}, "description": "Only run for these targets"},
		"on-error":        obj{"enum": []string{"fail", "continue"}, "description": "What to do when the step fails at run time"},
		"pre-script":      str("JavaScript run before rendering; its global variables are visible in templates"),
		"pre-validation":  str("JavaScript assertion evaluated before rendering"),
		"post-validation": str("JavaScript assertion evaluated after rendering (sees output and rendered)"),
		"requires": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"packages": obj{"type": []string{"array", "object", "string"}, "description": "Packages installed if missing, or a mapping of flavor to packages"},
		}},
		"only-if": str("Run-time shell condition; the step runs only if it succeeds"),
		"unless":  str("Run-time shell condition; the step is skipped if it succeeds"),
		"creates": str("Skip the step at run time if this path exists"),
		"foreach": obj{"type": []string{"array", "object", "string"}, "description": "Repeat the step for each item (item, index)"},
		"args":    obj{"type": "object", "description": "Action arguments"},
		"output":  obj{"type": "object", "description": "Output values available to later steps"},
		"steps":   obj{"type": "array", "items": obj{"$ref": "#/$defs/step"}, "description": "Nested steps (block)"},
	}
	var allOf []any
	for _, a := range actions.All() {
		argProps := obj{}
		var required []string
		for _, s := range a.Args {
			argProps[s.Name] = argSchema(s)
			if s.Required {
				required = append(required, s.Name)
			}
		}
		args := obj{"type": "object", "additionalProperties": false, "properties": argProps}
		if len(required) > 0 {
			args["required"] = required
		}
		allOf = append(allOf, obj{
			"if":   obj{"properties": obj{"action": obj{"const": a.Name}}, "required": []string{"action"}},
			"then": obj{"properties": obj{"args": args}},
		})
	}
	// Extra actions: argument completion only. A configuration may replace an
	// extra action with its own definition, so the arguments are not closed.
	for _, a := range library.Actions() {
		argProps := obj{}
		for _, d := range a.Arguments {
			argProps[d.Name] = extraArgSchema(d)
		}
		allOf = append(allOf, obj{
			"if":   obj{"properties": obj{"action": obj{"const": a.Name}}, "required": []string{"action"}},
			"then": obj{"properties": obj{"args": obj{"type": "object", "properties": argProps}}},
		})
	}
	return obj{"type": "object", "additionalProperties": false, "properties": props, "allOf": allOf}
}

func extraArgSchema(d *config.ArgDef) obj {
	var o obj
	switch d.Type {
	case "bool":
		o = templatable("boolean")
	case "int":
		o = templatable("integer")
	case "array":
		o = obj{"type": []string{"array", "string"}}
	case "option":
		o = obj{"type": []string{"string", "number", "boolean"}}
	default:
		o = obj{"type": []string{"string", "number", "boolean"}}
	}
	o = obj{"anyOf": []any{o, obj{"$ref": "#/$defs/byFlavor"}}}
	desc := d.Description
	if d.HasDefault() && d.Default.V != nil {
		desc += fmt.Sprintf(" (default: %v)", d.Default.V)
	}
	if len(d.Options) > 0 {
		desc += fmt.Sprintf(" One of: %v.", d.Options)
	}
	o["description"] = strings.TrimSpace(desc)
	return o
}

// Generate returns the JSON schema as a Go value.
func Generate() map[string]any {
	var targetNames []any
	for _, n := range target.Names() {
		targetNames = append(targetNames, n)
	}
	targetNames = append(targetNames, target.RendererShell, target.RendererDockerfile, target.RendererCloudInit)
	targetNames = uniq(targetNames)

	argument := obj{"type": "object", "additionalProperties": false, "properties": obj{
		"type":        obj{"enum": []string{"string", "bool", "int", "option", "array", "secret"}, "description": "Argument type (default string)"},
		"description": str("Description"),
		"default":     obj{"description": "Default value; may use ${{ }} referring to earlier arguments"},
		"required":    obj{"type": "boolean", "description": "Fail (or prompt) when no value is given"},
		"pattern":     str("Regular expression the value must match (string, secret)"),
		"options":     obj{"type": "array", "description": "Allowed values (option)"},
	}}
	flavorDef := obj{"type": "object", "additionalProperties": false, "properties": obj{
		"enabled":     obj{"type": "boolean", "description": "Enable the flavor (default true)"},
		"based-on":    str("Built-in flavor a custom flavor derives from"),
		"base-image":  str("Container base image"),
		"cloud-image": str("Cloud image URL for VM builds"),
		"vars":        obj{"type": "object", "description": "Variables available as flavor.vars"},
	}}
	importDef := obj{"anyOf": []any{
		obj{"type": "string", "description": "Glob relative to this file"},
		obj{"type": "object", "additionalProperties": false, "properties": obj{
			"path": str("Glob (relative to the repository for git imports)"),
			"git":  str("Git repository URL"),
			"ref":  str("Branch, tag or commit"),
		}},
	}}
	action := obj{"type": "object", "additionalProperties": false, "required": []string{"steps"}, "properties": obj{
		"description": str("Description"),
		"arguments":   obj{"type": "object", "additionalProperties": obj{"anyOf": []any{obj{"$ref": "#/$defs/argument"}, obj{"type": "null"}}}},
		"output":      obj{"type": "object"},
		"flavors":     obj{"type": []string{"array", "string"}},
		"steps":       obj{"type": "array", "items": obj{"$ref": "#/$defs/step"}},
	}}
	testDef := obj{"type": "object", "additionalProperties": false, "properties": obj{
		"name":      str("Test name"),
		"flavor":    str("Flavor (default: first enabled)"),
		"target":    str("Target (default shell)"),
		"arguments": obj{"type": "object"},
		"steps":     obj{"type": "array", "items": obj{"$ref": "#/$defs/step"}},
		"run":       obj{"type": "boolean", "description": "Run the generated shell script in a container (osb test --run)"},
		"expect": obj{"type": "object", "additionalProperties": false, "properties": obj{
			"contains":     obj{"type": []string{"array", "string"}},
			"not-contains": obj{"type": []string{"array", "string"}},
			"golden":       str("File with the expected output"),
			"error":        str("Expected error (substring)"),
			"warnings":     obj{"type": []string{"array", "string"}},
		}},
	}}
	return obj{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  ID,
		"title":                "osb configuration",
		"type":                 "object",
		"additionalProperties": false,
		"properties": obj{
			"name":        str("Name used for images and output files"),
			"description": str("Description"),
			"arguments":   obj{"type": "object", "additionalProperties": obj{"anyOf": []any{obj{"$ref": "#/$defs/argument"}, obj{"type": "null"}}}},
			"targets":     obj{"type": "array", "items": obj{"enum": targetNames}},
			"flavors":     obj{"type": "object", "additionalProperties": obj{"anyOf": []any{obj{"$ref": "#/$defs/flavor"}, obj{"type": "null"}}}},
			"artifacts":   obj{"type": "object", "additionalProperties": false, "properties": artifactSchemas()},
			"imports":     obj{"type": "array", "items": obj{"$ref": "#/$defs/import"}},
			"actions":     obj{"type": "object", "additionalProperties": obj{"$ref": "#/$defs/action"}},
			"steps":       obj{"type": "array", "items": obj{"$ref": "#/$defs/step"}},
			"tests":       obj{"type": "array", "items": obj{"$ref": "#/$defs/test"}},
		},
		"$defs": obj{
			"argument": argument,
			"flavor":   flavorDef,
			"import":   importDef,
			"action":   action,
			"step":     stepSchema(),
			"test":     testDef,
			"byFlavor": obj{"type": "object", "additionalProperties": false, "required": []string{"by-flavor"},
				"properties": obj{"by-flavor": obj{"type": "object", "description": "Value per flavor name, based-on, family or default"}}},
		},
	}
}

func uniq(in []any) []any {
	seen := map[any]bool{}
	var out []any
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// JSON returns the schema as indented JSON.
func JSON() []byte {
	b, _ := json.MarshalIndent(Generate(), "", "  ")
	return append(b, '\n')
}

var (
	compiled    *jsonschema.Schema
	compileErr  error
	compileOnce sync.Once
	printer     = message.NewPrinter(language.English)
)

func compile() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		var doc any
		if err := json.Unmarshal(JSON(), &doc); err != nil {
			compileErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(ID, doc); err != nil {
			compileErr = err
			return
		}
		compiled, compileErr = c.Compile(ID)
	})
	return compiled, compileErr
}

// toJSONTypes converts int64 values to float64 for the validator.
func toJSONTypes(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = toJSONTypes(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = toJSONTypes(vv)
		}
		return out
	case int64:
		return float64(t)
	}
	return v
}

// Validate validates a parsed document. Errors carry file:line positions.
func Validate(file string, doc *yaml.Node, value any) error {
	sch, err := compile()
	if err != nil {
		return fmt.Errorf("internal schema error: %w", err)
	}
	if value == nil {
		return nil
	}
	err = sch.Validate(toJSONTypes(value))
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return err
	}
	type issue struct {
		line int
		path string
		msg  string
	}
	var issues []issue
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		path := "/" + strings.Join(e.InstanceLocation, "/")
		msg := e.ErrorKind.LocalizedString(printer)
		key := path + msg
		if seen[key] {
			return
		}
		seen[key] = true
		loc := e.InstanceLocation
		if ap, ok := e.ErrorKind.(*kind.AdditionalProperties); ok && len(ap.Properties) > 0 {
			loc = append(append([]string{}, loc...), ap.Properties[0])
		}
		issues = append(issues, issue{line: lineOf(doc, loc), path: path, msg: msg})
	}
	walk(ve)
	// Prefer the most specific (deepest) messages and drop alternatives of anyOf.
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].line < issues[j].line })
	var lines []string
	byPath := map[string]bool{}
	for _, is := range issues {
		if byPath[is.path] {
			continue
		}
		byPath[is.path] = true
		lines = append(lines, fmt.Sprintf("%s:%d: %s: %s", file, is.line, displayPath(is.path), is.msg))
	}
	return fmt.Errorf("%s", strings.Join(lines, "\n"))
}

func displayPath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	var b strings.Builder
	for _, s := range parts {
		if s == "" {
			continue
		}
		if _, err := strconv.Atoi(s); err == nil {
			b.WriteString("[" + s + "]")
		} else {
			if b.Len() > 0 {
				b.WriteString(".")
			}
			b.WriteString(s)
		}
	}
	if b.Len() == 0 {
		return "(document)"
	}
	return b.String()
}

// lineOf finds the line of the node at a JSON pointer location.
func lineOf(doc *yaml.Node, loc []string) int {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	line := n.Line
	for _, tok := range loc {
		switch n.Kind {
		case yaml.MappingNode:
			found := false
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == tok {
					line = n.Content[i].Line
					n = n.Content[i+1]
					found = true
					break
				}
			}
			if !found {
				return line
			}
		case yaml.SequenceNode:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx >= len(n.Content) {
				return line
			}
			n = n.Content[idx]
			line = n.Line
		default:
			return line
		}
	}
	return line
}
