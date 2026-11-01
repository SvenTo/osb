# osb — OS builder

`osb` turns **one YAML configuration** into ready-to-use system artifacts:

| Command | Result |
|---|---|
| `osb render shell` | A readable, dependency-free POSIX shell script |
| `osb render dockerfile` | A Dockerfile with its build context |
| `osb render cloud-init` | cloud-init `user-data` / `meta-data` (+ `seed.iso`) |
| `osb build oci` | A container image (docker/BuildKit or podman) |
| `osb build wsl` | A WSL 2 distribution (`.wsl`) |
| `osb build qcow2` | A VM disk image, built from the official cloud image |
| `osb build ova` | An OVA appliance for VMware / VirtualBox |
| `osb apply` | Configures the running system after showing the plan |

The same configuration works for **Ubuntu**, **Debian** and **Arch Linux** (and
custom flavors derived from them). `osb` is a single static binary; built-in
actions, a library of higher-level [extra actions](docs/reference/actions.md#extra-actions)
(written in YAML, e.g. `install-aur-package` or `letsencrypt-certificate`), the shell
helper library and the JSON schema are compiled in.

```yaml
name: devbox
arguments:
  user: { type: string, default: dev }
steps:
  - action: install-packages
    args: { packages: [curl, git, vim] }
  - action: add-user
    args: { name: "${{ args.user }}", shell: /bin/bash, sudo: true }
  - action: write-file
    args:
      path: /etc/motd
      content: |
        Welcome to ${{ flavor.name }}!
```

```bash
osb validate
```

```bash
osb render shell --flavor debian > setup.sh
```

```bash
osb build oci --flavor ubuntu
```

```bash
sudo osb apply
```

## Install

Download a binary from the [releases](https://github.com/SvenTo/osb/releases)
(Linux and macOS, amd64 and arm64):

```bash
curl -fsSL https://github.com/SvenTo/osb/releases/latest/download/osb_linux_amd64.tar.gz | tar -xz osb
```

Or build it with the Go version from `go.mod`:

```bash
go install github.com/SvenTo/osb/cmd/osb@latest
```

or from a checkout:

```bash
make build
```

Runtime tools are only needed for the targets that use them: `docker` (or
`podman`) for `oci`/`wsl`, `qemu-img` + `qemu-system-x86_64` (or libvirt) for
`qcow2`/`ova`.

## Editor support

A VS Code extension (completion, documentation on hover, validation, render
commands) is attached to every release as `osb.vsix`; its source is in
[editors/vscode](editors/vscode). The JSON schema (`osb schema`) works with any
editor that uses the YAML language server.

## Documentation

- [Getting started](docs/getting-started.md)
- [Configuration reference](docs/configuration.md)
- [Expressions and templates](docs/expressions.md)
- [Targets: render, build, apply](docs/targets.md)
- [Libraries, imports and tests](docs/libraries.md)
- [Built-in and extra actions](docs/reference/actions.md) (generated)
- [Command line](docs/reference/cli.md) (generated)
- [Design specification](spec.md)

Examples are in [`examples/`](examples).

## Development

```bash
make test
```

`make test` runs the Go unit tests and the example library tests. `make
integration` additionally runs the generated scripts in Ubuntu, Debian and Arch
containers (needs docker). After changing actions, run `make docs` to regenerate
the reference and the schema.

`make test-js` tests the website's editor support and the VS Code extension
(needs node); `make vscode-test` runs the extension in a real VS Code (needs
`xvfb-run`). `make site` builds the website into `out/site` and `make vscode`
packages the extension.
