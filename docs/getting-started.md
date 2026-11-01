# Getting started

## Install

osb is a single static binary. Download it from the
[GitHub releases](https://github.com/SvenTo/osb/releases) (Linux and macOS,
amd64 and arm64):

```bash
curl -fsSL https://github.com/SvenTo/osb/releases/latest/download/osb_linux_amd64.tar.gz | tar -xz osb
```

```bash
sudo install osb /usr/local/bin/
```

```bash
osb --version
```

Every release has a `checksums.txt` with the SHA-256 checksums of the archives.
With Go installed you can also build it from source:
`go install github.com/SvenTo/osb/cmd/osb@latest`.

Rendering (`osb render`, `osb validate`, `osb test`) works anywhere. Building
images needs the tools of the target: docker or podman for `oci` and `wsl`,
QEMU (or libvirt) and `qemu-img` for `qcow2` and `ova`. `osb apply` runs on
the machine it configures (Ubuntu, Debian or Arch Linux).

You can also try osb without installing it: the
[website](https://svento.github.io/osb/) runs it in the browser.

### Editor support

- **VS Code:** install `osb.vsix` from the
  [releases](https://github.com/SvenTo/osb/releases)
  (`code --install-extension osb.vsix`). It completes keys, actions and their
  arguments, shows their documentation on hover, runs `osb validate` on save
  and renders shell scripts, Dockerfiles and cloud-init from the command
  palette.
- **Other editors** with the YAML language server: `osb init` adds
  `# yaml-language-server: $schema=./osb.schema.json` to `osb.yaml`; the schema
  is also published at <https://svento.github.io/osb/osb.schema.json>.

## 1. Create a configuration

```bash
osb init my-machine
```

```bash
cd my-machine
```

This writes `osb.yaml` and `osb.schema.json`. The first line of `osb.yaml`
(`# yaml-language-server: $schema=./osb.schema.json`) gives you completion and
validation in editors that use the YAML language server (VS Code with the
Red Hat YAML extension, Neovim, Helix, ...).

A configuration is a list of **steps**. Each step runs an **action** with
**args**:

```yaml
name: my-machine

arguments:
  user: { type: string, default: dev }

steps:
  - title: Install tools
    action: install-packages
    args:
      packages: [curl, git, vim]

  - title: Create ${{ args.user }}
    action: add-user
    args:
      name: ${{ args.user }}
      shell: /bin/bash
      sudo: true
```

`osb actions list` prints all built-in actions, and `osb actions show add-user`
shows the arguments of one action. The full list is in the
[action reference](reference/actions.md).

## 2. Validate

```bash
osb validate
```

`validate` checks the file against the schema and evaluates it for every
enabled flavor and target, so mistakes such as a typo in an argument name or a
step that only works on one distribution show up early, with `file:line`.

## 3. Render or build

Pick a **flavor** (distribution) with `--flavor` and a target:

```bash
osb render shell --flavor ubuntu > setup.sh
```

```bash
osb render dockerfile --flavor debian -o out/docker
```

```bash
osb build oci --flavor archlinux
```

```bash
osb build qcow2 --flavor ubuntu
```

Arguments are passed with `--arg name=value` (repeatable), an `--args-file`,
environment variables `OSB_ARG_<NAME>`, or interactively when a required
argument is missing:

```bash
osb render shell --flavor ubuntu --arg user=alice
```

Outputs go to `out/<name>-<flavor>-<target>/` unless `-o` is given.

## 4. Configure a running machine

```bash
sudo osb apply
```

`apply` detects the flavor from `/etc/os-release`, prints every step with a
short description of what it does (secrets are masked), asks for confirmation,
and then runs the generated script. `--dry-run` only prints the plan,
`--show-script` also prints the full script, `--yes` skips the question.

The generated script is the same one `osb render shell` produces, so you can
also copy it to a machine that does not have `osb` installed:

```bash
osb render shell --flavor debian | ssh root@server sh -s
```

## 5. Next steps

- [Configuration reference](configuration.md): arguments, flavors, artifacts, steps
- [Expressions](expressions.md): `${{ }}` templates, conditions, step outputs
- [Targets](targets.md): what each output contains and what it needs
- [Libraries](libraries.md): custom actions, git imports and tests
