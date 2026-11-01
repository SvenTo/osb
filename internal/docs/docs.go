// Package docs generates reference documentation from the built-in definitions.
package docs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SvenTo/osb/internal/actions"
	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/library"
)

func defaultString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// ActionText describes an action for the terminal.
func ActionText(a *actions.Action) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)\n\n%s\n", a.Name, a.Category, a.Description)
	if len(a.Requires) > 0 {
		fmt.Fprintf(&b, "\nInstalls when missing: %s\n", strings.Join(a.Requires, ", "))
	}
	if len(a.Args) > 0 {
		b.WriteString("\nArguments:\n")
		for _, s := range a.Args {
			req := ""
			if s.Required {
				req = ", required"
			}
			def := ""
			if d := defaultString(s.Default); d != "" {
				def = ", default " + d
			}
			fmt.Fprintf(&b, "  %-18s %s%s%s\n", s.Name, s.Type, req, def)
			fmt.Fprintf(&b, "  %-18s %s\n", "", s.Description)
		}
	}
	if a.Example != "" {
		b.WriteString("\nExample:\n")
		for i, l := range strings.Split(strings.TrimRight(a.Example, "\n"), "\n") {
			if i == 0 {
				b.WriteString("  - " + l + "\n")
			} else {
				b.WriteString("    " + l + "\n")
			}
		}
	}
	return b.String()
}

// argType describes the type of a custom action argument.
func argType(d *config.ArgDef) string {
	if d.Type == "" {
		return "string"
	}
	return d.Type
}

// argDefault formats the default of a custom action argument ("" if none).
func argDefault(d *config.ArgDef) string {
	if !d.HasDefault() || d.Default.V == nil {
		return ""
	}
	switch v := d.Default.V.(type) {
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = fmt.Sprint(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		if v == "" {
			return `""`
		}
	}
	return fmt.Sprint(d.Default.V)
}

// ExtraActionText describes an extra action for the terminal.
func ExtraActionText(a *config.ActionDef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (extra: %s)\n\n%s\n", a.Name, library.GroupOf(a.Name), strings.TrimSpace(a.Description))
	if len(a.Flavors) > 0 {
		fmt.Fprintf(&b, "\nFlavors: %s\n", strings.Join(a.Flavors, ", "))
	}
	if len(a.Arguments) > 0 {
		b.WriteString("\nArguments:\n")
		for _, d := range a.Arguments {
			extra := ""
			if d.Required {
				extra = ", required"
			}
			if def := argDefault(d); def != "" {
				extra += ", default " + def
			}
			fmt.Fprintf(&b, "  %-22s %s%s\n", d.Name, argType(d), extra)
			if d.Description != "" {
				fmt.Fprintf(&b, "  %-22s %s\n", "", d.Description)
			}
		}
	}
	if len(a.Output) > 0 {
		b.WriteString("\nOutput: " + strings.Join(sortedKeys(a.Output), ", ") + "\n")
	}
	fmt.Fprintf(&b, "\nSource: %s (osb actions source %s)\n", a.Pos, a.Name)
	return b.String()
}

func sortedKeys(m config.Map) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// extraActionsMarkdown documents the action library compiled into the binary.
func extraActionsMarkdown(b *strings.Builder) {
	b.WriteString("\n# Extra actions\n\n")
	b.WriteString("Extra actions are custom actions written in osb's own YAML format (see\n")
	b.WriteString("[Libraries](../libraries.md)) that are compiled into the binary from\n")
	b.WriteString("[`library/`](../../library). They are used exactly like built-in actions. A\n")
	b.WriteString("configuration that defines an action with the same name replaces the extra action.\n")
	b.WriteString("`osb actions source <action>` prints the source of the file that defines an extra action.\n\n")
	for _, g := range library.Groups() {
		fmt.Fprintf(b, "- **%s**:", g.Name)
		for _, a := range g.Actions {
			fmt.Fprintf(b, " [`%s`](#%s)", a.Name, a.Name)
		}
		b.WriteString("\n")
	}
	for _, g := range library.Groups() {
		fmt.Fprintf(b, "\n## Extra: %s\n\n", g.Name)
		if g.Description != "" {
			fmt.Fprintf(b, "%s Source: [`library/%s.yaml`](../../library/%s.yaml).\n", g.Description, g.Name, g.Name)
		}
		for _, a := range g.Actions {
			fmt.Fprintf(b, "\n### %s\n\n%s\n", a.Name, strings.TrimSpace(a.Description))
			if len(a.Flavors) > 0 {
				fmt.Fprintf(b, "\nFlavors: `%s`.\n", strings.Join(a.Flavors, "`, `"))
			}
			if len(a.Arguments) > 0 {
				b.WriteString("\n| Argument | Type | Required | Default | Description |\n|---|---|---|---|---|\n")
				for _, d := range a.Arguments {
					req := ""
					if d.Required {
						req = "yes"
					}
					def := argDefault(d)
					if def != "" {
						def = "`" + strings.ReplaceAll(def, "|", `\|`) + "`"
					}
					desc := strings.ReplaceAll(d.Description, "|", `\|`)
					if len(d.Options) > 0 {
						desc += fmt.Sprintf(" One of: `%v`.", d.Options)
					}
					fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n", d.Name, argType(d), req, def, desc)
				}
			}
			if len(a.Output) > 0 {
				fmt.Fprintf(b, "\nOutput: `%s`.\n", strings.Join(sortedKeys(a.Output), "`, `"))
			}
		}
	}
}

// ActionsMarkdown returns the reference of all built-in and extra actions.
func ActionsMarkdown() string {
	var b strings.Builder
	b.WriteString("# Actions\n\nosb has built-in actions, implemented in Go, and [extra actions](#extra-actions) written in YAML. Both are compiled into the binary and used the same way.\n\n## Built-in actions\n\n")
	b.WriteString("<!-- Generated by `osb docs -o docs/reference`. Do not edit. -->\n\n")
	b.WriteString("Every argument accepts `${{ }}` expressions and `{by-flavor: {...}}` values.\n")
	b.WriteString("Types: `string`, `text` (multi-line string), `bool`, `int`, `list` (list of strings; a single string is accepted),\n")
	b.WriteString("`map` (mapping of strings), `mode` (octal file mode, quote it: `\"0644\"`), `any`.\n")
	b.WriteString("Higher-level actions written in YAML are listed under [Extra actions](#extra-actions).\n\n")
	cat := ""
	for _, a := range actions.All() {
		if a.Category != cat {
			cat = a.Category
			fmt.Fprintf(&b, "- **%s**:", cat)
			for _, x := range actions.All() {
				if x.Category == cat {
					fmt.Fprintf(&b, " [`%s`](#%s)", x.Name, x.Name)
				}
			}
			b.WriteString("\n")
		}
	}
	cat = ""
	for _, a := range actions.All() {
		if a.Category != cat {
			cat = a.Category
			fmt.Fprintf(&b, "\n## %s%s\n", strings.ToUpper(cat[:1]), cat[1:])
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", a.Name, a.Description)
		if len(a.Requires) > 0 {
			fmt.Fprintf(&b, "\nInstalls when missing: %s.\n", "`"+strings.Join(a.Requires, "`, `")+"`")
		}
		if len(a.Args) > 0 {
			b.WriteString("\n| Argument | Type | Required | Default | Description |\n|---|---|---|---|---|\n")
			for _, s := range a.Args {
				req := ""
				if s.Required {
					req = "yes"
				}
				def := defaultString(s.Default)
				if def != "" {
					def = "`" + def + "`"
				}
				desc := strings.ReplaceAll(s.Description, "|", `\|`)
				if len(s.Enum) > 0 {
					desc += " One of: `" + strings.Join(s.Enum, "`, `") + "`."
				}
				fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", s.Name, s.Type, req, def, desc)
			}
		}
		if a.Example != "" {
			b.WriteString("\n```yaml\n")
			lines := strings.Split(strings.TrimRight(a.Example, "\n"), "\n")
			for i, l := range lines {
				if i == 0 {
					b.WriteString("- " + l + "\n")
				} else {
					b.WriteString("  " + l + "\n")
				}
			}
			b.WriteString("```\n")
		}
	}
	extraActionsMarkdown(&b)
	return b.String()
}

// CLIMarkdown documents all commands.
func CLIMarkdown(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString("# Command line reference\n\n<!-- Generated by `osb docs -o docs/reference`. Do not edit. -->\n\n")
	b.WriteString("Global flags:\n\n```\n" + root.PersistentFlags().FlagUsages() + "```\n")
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", sub.CommandPath(), sub.Short)
			if sub.Long != "" {
				b.WriteString("\n```\n" + sub.Long + "\n```\n")
			}
			fmt.Fprintf(&b, "\nUsage: `%s`\n", sub.UseLine())
			if f := sub.LocalNonPersistentFlags().FlagUsages(); f != "" {
				b.WriteString("\n```\n" + f + "```\n")
			}
			walk(sub)
		}
	}
	walk(root)
	return b.String()
}

// InitTemplate is the configuration written by `osb init`.
const InitTemplate = `# yaml-language-server: $schema=./osb.schema.json
name: example
description: Example machine created by osb init

arguments:
  user:
    type: string
    description: Name of the user to create
    default: dev
    pattern: "^[a-z_][a-z0-9_-]*$"
  timezone:
    type: string
    default: UTC

artifacts:
  oci:
    tags: ["example:latest"]
  dockerfile:
    cmd: [/bin/bash]

steps:
  - title: Install base packages
    action: install-packages
    args:
      packages: [ca-certificates, curl, git, sudo, vim]

  - title: Set the time zone
    action: set-timezone
    args:
      timezone: ${{ args.timezone }}

  - name: user
    title: Create user ${{ args.user }}
    action: add-user
    args:
      name: ${{ args.user }}
      shell: /bin/bash
      sudo: true
    output:
      home: /home/${{ args.user }}

  - title: Write a welcome message
    action: write-file
    args:
      path: ${{ steps.user.output.home }}/README.txt
      owner: ${{ args.user }}
      content: |
        Welcome ${{ args.user }}! This machine was configured by osb.
`
