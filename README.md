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
actions, the shell helper library and the JSON schema are compiled in.

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

Requires the Go version from `go.mod`:

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

## Documentation

- [Getting started](docs/getting-started.md)
- [Configuration reference](docs/configuration.md)
- [Expressions and templates](docs/expressions.md)
- [Targets: render, build, apply](docs/targets.md)
- [Libraries, imports and tests](docs/libraries.md)
- [Built-in actions](docs/reference/actions.md) (generated)
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
