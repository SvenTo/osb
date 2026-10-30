# osb — OS builder specification

`osb` is a single Go binary that turns one declarative YAML configuration into
system images and configuration artifacts, or applies it to the running system.

This document is the design reference. End-user documentation lives in
[`docs/`](docs/README.md).

## 1. Goals

- One configuration, many outputs:

  | Target       | Kind   | Output                                                         |
  |--------------|--------|----------------------------------------------------------------|
  | `shell`      | render | Readable, dependency-free POSIX `sh` script                    |
  | `dockerfile` | render | Directory with `Dockerfile` and build context                  |
  | `cloud-init` | render | `user-data` (`#cloud-config`), `meta-data`, `seed.iso`         |
  | `oci`        | build  | Container image (docker/BuildKit), optionally an OCI archive   |
  | `wsl`        | build  | WSL 2 root filesystem (`.wsl` = `tar.gz`)                      |
  | `qcow2`      | build  | VM disk image: boots the cloud image, seed served over HTTP    |
  | `ova`        | build  | OVA appliance (streamOptimized VMDK + OVF + manifest)          |
  | `apply`      | live   | Configure the running system after showing the plan            |

- Flavors (distributions): `ubuntu`, `debian`, `archlinux`. A config can define
  derived flavors (`based-on: ubuntu`).
- Built-in actions prefer native features of the target (Dockerfile `COPY`/`ADD`/`ENV`,
  cloud-init `write_files`/`users`/`packages`) and fall back to shell.
- Single static binary: built-in actions, the shell helper library and the JSON
  schema are compiled in.
- Reusable libraries of custom actions, imported from the filesystem or git,
  with a lockfile and unit tests.

## 2. Architecture

```
YAML + imports ─► load & merge ─► schema + semantic validation
               ─► evaluate for (flavor, target): arguments, JS, templates, conditions
               ─► list of resolved steps, each a list of primitive operations (ops)
               ─► renderer: shell | dockerfile | cloud-init
               ─► builder:  oci (dockerfile) → wsl ; qcow2 (cloud-init) → ova ; apply (shell)
```

Primitive ops: `file` (write a file), `shell` (a snippet using the helper
library), `packages` (install), `user` (create a user), `hostname`, `env`,
`download`, `note` (a skipped/informational message). Every op has a shell
fallback; renderers map ops to native instructions when that keeps semantics.

## 3. Configuration

```yaml
name: example                 # used for image names and file names
description: Example machine

arguments:                    # map: argument name -> definition
  user:
    type: string              # string | bool | int | option | array | secret
    description: The name of the user to create
    default: example-user
    required: false
    pattern: "^[a-z_][a-z0-9_-]*$"   # string only
  shell:
    type: option
    options: [/bin/bash, /bin/sh]
    default: /bin/bash
  password:
    type: secret              # redacted in all log, plan and error output

targets: [shell, dockerfile, oci, cloud-init, qcow2, ova, wsl, apply]  # default: all

flavors:                      # default: all built-in flavors enabled
  ubuntu:
    vars: { apt-options: "--no-install-recommends" }   # flavor.vars in expressions
  debian: {}
  archlinux:
    enabled: false
  mint:
    based-on: ubuntu
    base-image: linuxmintd/mint22-amd64

artifacts:                    # target-specific settings, all templated
  dockerfile:
    base-image: "ubuntu:24.04"   # default per flavor
    env: { LANG: C.UTF-8 }
    user: ""                     # final USER
    workdir: ""
    entrypoint: []
    cmd: []
    expose: []
    labels: {}
  oci:
    tags: ["${{ args.user }}/example:latest"]
    push: false
    archive: ""                  # write an OCI archive instead of loading into docker
    platform: ""
  wsl:
    default-user: "${{ args.user }}"
    systemd: true
  qcow2:
    base-image: ""               # URL or path; default per flavor (official cloud image)
    disk-size: 10G
    memory: 2048                 # MiB
    cpus: 2
    driver: qemu                 # qemu | libvirt
    timeout: 30m
    compress: true
  ova:
    memory: 2048
    cpus: 2
    os-type: ubuntu64Guest
  cloud-init:
    hostname: ""
    extra: {}                    # merged verbatim into #cloud-config

imports:
  - library/*.yaml                                  # glob, relative to this file
  - git: https://github.com/example/osb-lib.git     # git import
    ref: v1.2.0                                     # tag, branch or commit
    path: actions/*.yaml

actions:                      # custom actions, usable as `action: <name>`
  setup-dotfiles:
    description: Clone dotfiles for a user
    arguments:
      user: { type: string, required: true }
    output:
      home: "/home/${{ args.user }}"
    steps:
      - action: git-clone
        args: { repo: https://example.com/dotfiles.git, dest: "/home/${{ args.user }}/.dotfiles", user: "${{ args.user }}" }

steps:
  - name: add-example-file    # [A-Za-z0-9_-]+, optional, needed to reference outputs
    title: Add example file   # optional, shown in logs
    action: write-file
    if: 'args.user == "sven"' # JS, evaluated at generation time
    flavors: [ubuntu, debian] # shorthand filter
    targets: [shell, apply]   # shorthand filter
    on-error: fail            # fail | continue
    pre-script: "var id = args.user.toUpperCase()"   # JS, its globals are visible below
    pre-validation: "id.length > 0"                  # JS assertion: must be truthy
    post-validation: "rendered.indexOf('hello') >= 0" # JS assertion after rendering
    requires:
      packages: [jq]                                 # or { ubuntu: [jq], archlinux: [jq] }
    only-if: "test -d /etc/example"                  # run-time guards (shell)
    unless: "test -f /etc/example/done"
    creates: /etc/example/done
    args:
      path: "/etc/example/${{ id }}"
      content: |
        hello world
        ${{ id }}
        ${{ 1 + 1 }}
        ${{ shell(id) }}
    output:
      id: "${{ id }}"

  - title: Create users
    action: add-user
    foreach: "${{ ['alice', 'bob'] }}"      # item / index available
    args: { name: "${{ item }}" }

  - title: Group of steps
    if: 'steps["add-example-file"].enabled'
    steps:                                   # a block: nested steps share the condition
      - action: log
        args: { message: "id was ${{ steps['add-example-file'].output.id }}" }
```

### 3.1 Expressions and templates

- One language: JavaScript (ES5.1+, goja). One delimiter: `${{ expr }}`;
  `$${{` produces a literal `${{`. Plain `$VAR` / `${VAR}` stays untouched, so
  shell code can be written naturally.
- A string that consists of exactly one `${{ }}` returns the raw value
  (array, object, bool, number); otherwise values are stringified.
- `if`, `pre-script`, `pre-validation` and `post-validation` are plain JS
  (no delimiters). `if` and `foreach` also accept `${{ }}`.
- All JS runs at **generation time**. It can see arguments, flavor, target and
  outputs of earlier steps, not the state of the target machine. Use the
  run-time guards `only-if`, `unless`, `creates` (shell) for that.
- Context: `args`, `flavor` (`name`, `family`, `vars`), `target` (`name`,
  `live`, `image`), `steps[name]` (`enabled`, `output`), `item`, `index`,
  `root.args` (top-level arguments inside custom actions).
- Helpers: `shell(s)` (POSIX quote), `json(v)`, `yaml(v)`, `b64(s)`,
  `unb64(s)`, `sha256(s)`, `file(path)` (read file relative to the config),
  `env(name)`, `indent(s, n)`, `fail(msg)`.
- Validation expressions are assertions: falsy result or an exception fails the step.
  `post-validation` additionally sees `output` and `rendered` (the shell form).

### 3.2 Arguments

Sources in increasing priority: `default`, `--args-file values.yaml`,
environment `OSB_ARG_<NAME>` (upper-case, `-` → `_`), `--arg name=value`.
Missing required arguments are prompted for on a TTY (secrets without echo).
`array` values on the command line are comma separated.

### 3.3 Secrets

- Values are redacted (`***`) in logs, plans, errors and printed scripts.
- `dockerfile`/`oci`: a `RUN` step whose script contains a secret is passed as a
  BuildKit secret mount, never stored in a layer or the build context.
- `shell`/`cloud-init`: secrets are embedded; `osb` warns and writes files with
  mode `0600`.
- `set-password` with `random: true` generates the password on the target at
  run time and prints it; for images it is generated once per build (warned).

### 3.4 Imports and libraries

- Filesystem globs relative to the importing file; git imports via a pure-Go git
  client, cached in `$XDG_CACHE_HOME/osb`, pinned in `osb.lock` next to the root
  config (`osb imports update` refreshes it).
- Imported files may contribute `actions`, `arguments` (defaults only) and
  `steps` (appended in import order before the importer's steps).
- `<name>.test.yaml` next to a library file defines tests run by `osb test`:
  render for a flavor/target and assert `contains`/`not-contains`/`golden`/`error`;
  optional `run: true` executes the shell script in a container of the flavor.

### 3.5 Validation

1. JSON Schema (`osb schema`) generated from the built-in action definitions,
   usable by editors via `yaml-language-server`.
2. Semantic checks: unique step names, valid references, action args by type,
   known flavors/targets, recursion in custom actions, argument types and patterns.

Errors are reported as `file:line: message`.

### 3.6 cloud-init ordering

cloud-init runs modules in a fixed order (`write_files` → `users` → `packages`
→ deferred `write_files` → `runcmd`). The renderer keeps a monotonic phase: an op
is rendered natively only if its module runs at or after the current phase,
otherwise it is rendered into the generated run script, which preserves the
declared step order.

## 4. Built-in actions

Files: `write-file`, `template-file`, `copy-files`, `delete-file`, `mkdir`,
`symlink`, `set-permissions`, `replace-in-file`, `line-in-file`,
`set-config-value` (json, yaml, ini, env), `download-file`, `extract-archive`,
`git-clone`.

Commands: `run-command` (argv, no shell by default), `run-script`.

Users: `add-user`, `remove-user`, `add-group`, `add-user-to-group`,
`authorize-ssh-key`, `set-password`.

Packages: `install-packages`, `remove-packages`, `upgrade-system`,
`add-repository`, `install-package-file`, `hold-packages`.

System: `trust-ca`, `set-hostname`, `set-timezone`, `set-locale`, `sysctl`,
`load-kernel-module`, `set-environment`, `add-sudoers`, `add-mount`,
`add-swap-file`.

systemd: `add-systemd-unit`, `enable-systemd-unit`, `disable-systemd-unit`,
`add-systemd-drop-in`, `restart-systemd-unit`.

Control: `log`, `assert`, `fail`, `wait-for`, `reboot-required`.

Reference: `osb actions list`, `osb actions show <name>`, generated into
`docs/reference/actions.md` by `osb docs`.

## 5. CLI

```
osb validate   -f config.yaml [--flavor F] [--arg k=v]
osb render     <shell|dockerfile|cloud-init> -f config.yaml --flavor F -o out/
osb build      <oci|wsl|qcow2|ova> -f config.yaml --flavor F -o out/
osb apply      -f config.yaml [--flavor auto] [--yes] [--show-script]
osb actions    list | show <name>
osb flavors | osb targets
osb schema
osb test       [paths...] [--update] [--run]
osb imports    update | list
osb docs       -o docs/reference
osb init       [dir]
```

## 6. Out of scope (for now)

Handlers/notify, firewall management, flatpak/snap, Windows guests,
other distribution families.
