package actions

import (
	"fmt"
	"strings"

	"github.com/SvenTo/osb/internal/ops"
)

func init() {
	register(&Action{
		Name: "log", Category: "control",
		Description: "Print a message when the step runs.",
		Args: []ArgSpec{
			{Name: "message", Type: TString, Required: true, Description: "Message"},
			{Name: "level", Type: TString, Default: "info", Enum: []string{"info", "warn"}, Description: "info or warn"},
		},
		Example: "action: log\nargs:\n  message: \"Configuring ${{ args.user }}\"\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			fn := "log"
			if a.Str("level") == "warn" {
				fn = "warn"
			}
			return []ops.Op{ops.Shell{Script: fn + " " + q(a.Str("message")) + "\n"}}, nil
		},
	})

	register(&Action{
		Name: "assert", Category: "control",
		Description: "Fail unless a condition holds: `that` is checked at generation time (use ${{ }}), `command` at run time.",
		Args: []ArgSpec{
			{Name: "that", Type: TBool, Description: "Generation-time condition"},
			{Name: "command", Type: TString, Description: "Run-time shell condition"},
			{Name: "message", Type: TString, Default: "assertion failed", Description: "Error message"},
		},
		Example: "action: assert\nargs:\n  command: test -d /home\n  message: /home is missing\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if !a.Has("that") && !a.Has("command") {
				return nil, fmt.Errorf("that or command is required")
			}
			if a.Has("that") && !a.Bool("that") {
				return nil, fmt.Errorf("%s", a.Str("message"))
			}
			if !a.Has("command") {
				return nil, nil
			}
			return []ops.Op{ops.Shell{Script: fmt.Sprintf("( %s ) >/dev/null 2>&1 || die %s\n", a.Str("command"), q(a.Str("message")))}}, nil
		},
	})

	register(&Action{
		Name: "fail", Category: "control",
		Description: "Abort generation with a message (combine with `if`).",
		Args: []ArgSpec{
			{Name: "message", Type: TString, Required: true, Description: "Error message"},
		},
		Example: "if: flavor.name == 'archlinux' && args.legacy\naction: fail\nargs:\n  message: legacy mode is not supported on Arch\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			return nil, fmt.Errorf("%s", a.Str("message"))
		},
	})

	register(&Action{
		Name: "wait-for", Category: "control",
		Description: "Wait until a TCP port is open, a path exists or a command succeeds (live systems only).",
		Args: []ArgSpec{
			{Name: "port", Type: TInt, Description: "TCP port"},
			{Name: "host", Type: TString, Default: "127.0.0.1", Description: "Host for port checks"},
			{Name: "path", Type: TString, Description: "Path that must exist"},
			{Name: "command", Type: TString, Description: "Shell command that must succeed"},
			{Name: "timeout", Type: TInt, Default: int64(60), Description: "Timeout in seconds"},
		},
		Example: "action: wait-for\nargs:\n  port: 5432\n  timeout: 30\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if !c.Target.Live {
				return []ops.Op{ops.Note{Message: "wait-for: not running on a live system, skipped"}}, nil
			}
			t := a.Int("timeout")
			var b strings.Builder
			n := 0
			if a.Has("port") {
				fmt.Fprintf(&b, "wait_for %d port_open %s %d\n", t, q(a.Str("host")), a.Int("port"))
				n++
			}
			if a.Has("path") {
				fmt.Fprintf(&b, "wait_for %d test -e %s\n", t, q(a.Str("path")))
				n++
			}
			if a.Has("command") {
				fmt.Fprintf(&b, "wait_for %d sh -c %s\n", t, q(a.Str("command")))
				n++
			}
			if n == 0 {
				return nil, fmt.Errorf("port, path or command is required")
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "reboot-required", Category: "control",
		Description: "Mark that a reboot is required (/var/run/reboot-required) and print a message (live systems only).",
		Args: []ArgSpec{
			{Name: "message", Type: TString, Default: "a reboot is required to complete the configuration", Description: "Message"},
		},
		Example: "action: reboot-required\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			if !c.Target.Live || c.Target.Image {
				return nil, nil
			}
			return []ops.Op{ops.Shell{Script: "touch /var/run/reboot-required\nwarn " + q(a.Str("message")) + "\n"}}, nil
		},
	})
}
