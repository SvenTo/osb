package actions

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/SvenTo/osb/internal/ops"
)

var unitName = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]+\.(service|socket|timer|target|path|mount|automount|slice|scope|swap|device)$`)

func checkUnits(units []string) error {
	for _, u := range units {
		if !unitName.MatchString(u) {
			return fmt.Errorf("invalid unit name %q (include the suffix, e.g. nginx.service)", u)
		}
	}
	return nil
}

func init() {
	register(&Action{
		Name: "add-systemd-unit", Category: "systemd",
		Description: "Install a systemd unit file in /etc/systemd/system and enable it (default).",
		Args: []ArgSpec{
			{Name: "name", Type: TString, Required: true, Description: "Unit name including suffix, e.g. app.service"},
			{Name: "content", Type: TText, Description: "Unit file content"},
			{Name: "source", Type: TString, Description: "Local unit file"},
			{Name: "enable", Type: TBool, Default: true, Description: "Enable the unit"},
			{Name: "start", Type: TBool, Default: false, Description: "(Re)start the unit on live systems"},
		},
		Example: "action: add-systemd-unit\nargs:\n  name: hello.service\n  content: |\n    [Unit]\n    Description=Hello\n    [Service]\n    ExecStart=/bin/echo hello\n    [Install]\n    WantedBy=multi-user.target\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			name := a.Str("name")
			if err := checkUnits([]string{name}); err != nil {
				return nil, err
			}
			content, err := contentOrSource(c, a)
			if err != nil {
				return nil, err
			}
			res := []ops.Op{ops.File{Path: "/etc/systemd/system/" + name, Content: content, Mode: "0644"}}
			var b strings.Builder
			b.WriteString("daemon_reload\n")
			if a.Bool("enable") {
				fmt.Fprintf(&b, "unit_enable %s\n", q(name))
			}
			if a.Bool("start") && c.Target.Live {
				fmt.Fprintf(&b, "unit_restart %s\n", q(name))
			}
			return append(res, ops.Shell{Script: b.String()}), nil
		},
	})

	register(&Action{
		Name: "add-systemd-drop-in", Category: "systemd",
		Description: "Add a drop-in override (/etc/systemd/system/<unit>.d/<name>.conf).",
		Args: []ArgSpec{
			{Name: "unit", Type: TString, Required: true, Description: "Unit to override"},
			{Name: "name", Type: TString, Default: "osb", Description: "Drop-in name"},
			{Name: "content", Type: TText, Description: "Drop-in content"},
			{Name: "source", Type: TString, Description: "Local drop-in file"},
			{Name: "restart", Type: TBool, Default: false, Description: "Restart the unit on live systems"},
		},
		Example: "action: add-systemd-drop-in\nargs:\n  unit: getty@tty1.service\n  name: autologin\n  content: |\n    [Service]\n    ExecStart=\n    ExecStart=-/sbin/agetty --autologin alice --noclear %I $TERM\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			unit := a.Str("unit")
			if err := checkUnits([]string{unit}); err != nil {
				return nil, err
			}
			if err := checkName("name", a.Str("name")); err != nil {
				return nil, err
			}
			content, err := contentOrSource(c, a)
			if err != nil {
				return nil, err
			}
			res := []ops.Op{
				ops.File{Path: "/etc/systemd/system/" + unit + ".d/" + a.Str("name") + ".conf", Content: content, Mode: "0644"},
				ops.Shell{Script: "daemon_reload\n"},
			}
			if a.Bool("restart") && c.Target.Live {
				res = append(res, ops.Shell{Script: "unit_restart " + q(unit) + "\n"})
			}
			return res, nil
		},
	})

	register(&Action{
		Name: "enable-systemd-unit", Category: "systemd",
		Description: "Enable systemd units; optionally start them on live systems.",
		Args: []ArgSpec{
			{Name: "units", Type: TList, Required: true, Description: "Unit names"},
			{Name: "now", Type: TBool, Default: false, Description: "Also start the units on live systems"},
		},
		Example: "action: enable-systemd-unit\nargs:\n  units: [ssh.service]\n  now: true\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			units := a.List("units")
			if err := checkUnits(units); err != nil {
				return nil, err
			}
			s := "unit_enable " + joinArgs(units...) + "\n"
			if a.Bool("now") && c.Target.Live {
				s += "if systemd_available; then systemctl start " + joinArgs(units...) + "; fi\n"
			}
			return []ops.Op{ops.Shell{Script: s}}, nil
		},
	})

	register(&Action{
		Name: "disable-systemd-unit", Category: "systemd",
		Description: "Disable (and optionally mask or stop) systemd units.",
		Args: []ArgSpec{
			{Name: "units", Type: TList, Required: true, Description: "Unit names"},
			{Name: "mask", Type: TBool, Default: false, Description: "Mask the units"},
			{Name: "now", Type: TBool, Default: false, Description: "Also stop the units on live systems"},
		},
		Example: "action: disable-systemd-unit\nargs:\n  units: [apt-daily.timer]\n  mask: true\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			units := a.List("units")
			if err := checkUnits(units); err != nil {
				return nil, err
			}
			verb := "disable"
			if a.Bool("mask") {
				verb = "mask"
			}
			var b strings.Builder
			fmt.Fprintf(&b, "if has_cmd systemctl; then systemctl %s %s; else warn 'systemctl not found'; fi\n", verb, joinArgs(units...))
			if a.Bool("now") && c.Target.Live {
				fmt.Fprintf(&b, "if systemd_available; then systemctl stop %s; fi\n", joinArgs(units...))
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "restart-systemd-unit", Category: "systemd",
		Description: "Restart systemd units on live systems (no-op for images).",
		Args: []ArgSpec{
			{Name: "units", Type: TList, Required: true, Description: "Unit names"},
		},
		Example: "action: restart-systemd-unit\nargs:\n  units: [ssh.service]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			units := a.List("units")
			if err := checkUnits(units); err != nil {
				return nil, err
			}
			if !c.Target.Live {
				return []ops.Op{ops.Note{Message: "restart-systemd-unit: not running on a live system, skipped"}}, nil
			}
			return []ops.Op{ops.Shell{Script: "unit_restart " + joinArgs(units...) + "\n"}}, nil
		},
	})
}
