package actions

import (
	"fmt"
	"strings"

	"github.com/SvenTo/osb/internal/ops"
	"github.com/SvenTo/osb/internal/render/shell"
)

func init() {
	register(&Action{
		Name: "add-user", Category: "users",
		Description: "Create a user (if missing) and add it to groups. Rendered natively as cloud-init users when possible.",
		Args: []ArgSpec{
			{Name: "name", Type: TString, Required: true, Description: "User name"},
			{Name: "uid", Type: TString, Description: "Numeric user id"},
			{Name: "groups", Type: TList, Description: "Supplementary groups (created when missing)"},
			{Name: "shell", Type: TString, Description: "Login shell, e.g. /bin/bash"},
			{Name: "home", Type: TString, Description: "Home directory"},
			{Name: "comment", Type: TString, Description: "Full name / GECOS"},
			{Name: "system", Type: TBool, Default: false, Description: "Create a system account"},
			{Name: "create-home", Type: TBool, Default: true, Description: "Create the home directory"},
			{Name: "sudo", Type: TBool, Default: false, Description: "Grant sudo rights (sudo group on Debian/Ubuntu, wheel on Arch)"},
		},
		Example: "action: add-user\nargs:\n  name: alice\n  shell: /bin/bash\n  groups: [docker]\n  sudo: true\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			u := ops.User{Name: a.Str("name"), UID: a.Str("uid"), Shell: a.Str("shell"), Home: a.Str("home"),
				Comment: a.Str("comment"), System: a.Bool("system"), CreateHome: a.Bool("create-home"), Groups: a.List("groups")}
			var res []ops.Op
			if a.Bool("sudo") {
				res = append(res, ops.Packages{Names: c.Flavor.Packages([]string{"sudo"}), Missing: true})
				u.Groups = append(u.Groups, c.Flavor.SudoGroup)
				if c.Flavor.IsArch() {
					res = append(res, ops.File{Path: "/etc/sudoers.d/10-wheel", Mode: "0440", Content: []byte("%wheel ALL=(ALL:ALL) ALL\n")})
				}
			}
			return append(res, u), nil
		},
	})

	register(&Action{
		Name: "remove-user", Category: "users",
		Description: "Remove a user if it exists.",
		Args: []ArgSpec{
			{Name: "name", Type: TString, Required: true, Description: "User name"},
			{Name: "remove-home", Type: TBool, Default: false, Description: "Also remove the home directory and mail spool"},
		},
		Example: "action: remove-user\nargs:\n  name: ubuntu\n  remove-home: true\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			r := ""
			if a.Bool("remove-home") {
				r = "--remove "
			}
			n := q(a.Str("name"))
			return []ops.Op{ops.Shell{Script: fmt.Sprintf("if id -u %s >/dev/null 2>&1; then userdel %s%s; fi\n", n, r, n)}}, nil
		},
	})

	register(&Action{
		Name: "add-group", Category: "users",
		Description: "Create a group if it does not exist.",
		Args: []ArgSpec{
			{Name: "name", Type: TString, Required: true, Description: "Group name"},
			{Name: "gid", Type: TString, Description: "Numeric group id"},
			{Name: "system", Type: TBool, Default: false, Description: "Create a system group"},
		},
		Example: "action: add-group\nargs:\n  name: developers\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			args := []string{"groupadd"}
			if a.Bool("system") {
				args = append(args, "--system")
			}
			if g := a.Str("gid"); g != "" {
				args = append(args, "--gid", q(g))
			}
			n := q(a.Str("name"))
			args = append(args, n)
			return []ops.Op{ops.Shell{Script: fmt.Sprintf("getent group %s >/dev/null || %s\n", n, strings.Join(args, " "))}}, nil
		},
	})

	register(&Action{
		Name: "add-user-to-group", Category: "users",
		Description: "Add an existing user to groups (groups are created when missing).",
		Args: []ArgSpec{
			{Name: "user", Type: TString, Required: true, Description: "User name"},
			{Name: "groups", Type: TList, Required: true, Description: "Groups"},
		},
		Example: "action: add-user-to-group\nargs:\n  user: alice\n  groups: [docker, video]\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			var b strings.Builder
			for _, g := range a.List("groups") {
				fmt.Fprintf(&b, "getent group %s >/dev/null || groupadd %s\n", q(g), q(g))
			}
			fmt.Fprintf(&b, "usermod -aG %s %s\n", q(strings.Join(a.List("groups"), ",")), q(a.Str("user")))
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "authorize-ssh-key", Category: "users",
		Description: "Add SSH public keys to a user's ~/.ssh/authorized_keys.",
		Args: []ArgSpec{
			{Name: "user", Type: TString, Required: true, Description: "User name"},
			{Name: "key", Type: TList, Description: "Public key(s)"},
			{Name: "source", Type: TString, Description: "Local file with public keys, relative to the configuration file"},
		},
		Example: "action: authorize-ssh-key\nargs:\n  user: alice\n  source: keys/alice.pub\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			keys := a.List("key")
			if a.Has("source") {
				b, err := c.ReadSource(a.Str("source"))
				if err != nil {
					return nil, err
				}
				keys = append(keys, strings.Split(string(b), "\n")...)
			}
			var b strings.Builder
			n := 0
			for _, k := range keys {
				k = strings.TrimSpace(k)
				if k == "" || strings.HasPrefix(k, "#") {
					continue
				}
				fmt.Fprintf(&b, "authorize_key %s %s\n", q(a.Str("user")), q(k))
				n++
			}
			if n == 0 {
				return nil, fmt.Errorf("no keys given (key or source)")
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})

	register(&Action{
		Name: "set-password", Category: "users",
		Description: "Set a user's password from a (secret) value, a crypt hash, or a random password that is printed. For images a random password is generated once at build time.",
		Args: []ArgSpec{
			{Name: "user", Type: TString, Required: true, Description: "User name"},
			{Name: "password", Type: TString, Description: "Plain text password (use a secret argument)"},
			{Name: "hash", Type: TString, Description: "Password hash as produced by mkpasswd/openssl passwd"},
			{Name: "random", Type: TBool, Default: false, Description: "Generate a random password and print it"},
			{Name: "length", Type: TInt, Default: int64(20), Description: "Length of a random password"},
			{Name: "expire", Type: TBool, Default: false, Description: "Force a password change at first login"},
		},
		Example: "action: set-password\nargs:\n  user: alice\n  random: true\n",
		Build: func(c *Ctx, a Args) ([]ops.Op, error) {
			user := a.Str("user")
			n := 0
			for _, k := range []string{"password", "hash"} {
				if a.Has(k) {
					n++
				}
			}
			if a.Bool("random") {
				n++
			}
			if n != 1 {
				return nil, fmt.Errorf("exactly one of password, hash or random is required")
			}
			var b strings.Builder
			switch {
			case a.Has("password"):
				b.WriteString("chpasswd " + shell.Heredoc(user+":"+a.Str("password")+"\n"))
			case a.Has("hash"):
				fmt.Fprintf(&b, "usermod -p %s %s\n", q(a.Str("hash")), q(user))
			default:
				if c.Target.Image {
					c.Warn(fmt.Sprintf("set-password: the random password for %s is generated at build time and shared by every instance of the image", user))
				}
				fmt.Fprintf(&b, "osb_pw=$(random_password %d)\n", a.Int("length"))
				fmt.Fprintf(&b, "printf '%%s:%%s\\n' %s \"$osb_pw\" | chpasswd\n", q(user))
				fmt.Fprintf(&b, "log \"generated password for %s: $osb_pw\"\n", strings.ReplaceAll(user, `"`, ""))
			}
			if a.Bool("expire") {
				fmt.Fprintf(&b, "passwd --expire %s\n", q(user))
			}
			return []ops.Op{ops.Shell{Script: b.String()}}, nil
		},
	})
}
