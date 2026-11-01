# Configuration reference

A configuration is a YAML file (default `osb.yaml`, select another one with
`-f`). All top-level keys are optional:

```yaml
name: devbox                 # used for image tags and output file names
description: My machine
arguments: {}                # input values
targets: []                  # restrict the allowed targets
flavors: {}                  # enable, disable, customize or derive distributions
artifacts: {}                # target-specific settings
imports: []                  # other configuration files (filesystem or git)
actions: {}                  # custom actions
steps: []                    # what to do
```

Generate the JSON schema with `osb schema -o osb.schema.json` and reference it
from the first line of the file for editor support:

```yaml
# yaml-language-server: $schema=./osb.schema.json
```

## Arguments

Arguments are the inputs of a configuration. They are available as `args.<name>`
in [expressions](expressions.md).

```yaml
arguments:
  user:
    type: string             # string (default), bool, int, option, array, secret
    description: Name of the user to create
    default: dev
    required: false
    pattern: "^[a-z_][a-z0-9_-]*$"     # string and secret only
  shell:
    type: option
    options: [/bin/bash, /bin/zsh]
    default: /bin/bash
  groups:
    type: array
    default: [docker]
  home:
    default: "/home/${{ args.user }}"  # defaults may refer to earlier arguments
  password:
    type: secret
    required: true
```

| Type | Accepted values |
|---|---|
| `string` | any scalar; checked against `pattern` if set |
| `bool` | `true`/`false` (also `1`, `0`, `t`, `f`) |
| `int` | integers |
| `option` | one of `options` |
| `array` | a YAML list, or a comma separated string on the command line |
| `secret` | a string that is masked (`***`) in all output; see [Secrets](#secrets) |

Values are taken from, in increasing priority:

1. `default`
2. `--args-file values.yaml` (a mapping of argument names to values)
3. environment variables `OSB_ARG_<NAME>` (upper case, `-` becomes `_`)
4. `--arg name=value`

A missing `required` argument is prompted for when osb runs in a terminal
(secrets without echo); otherwise it is an error. `--no-prompt` disables
prompting. An argument without a value and default is `undefined` in
expressions.

## Flavors

Flavors are the supported distributions:

| Flavor | Family | Container base image | Cloud image |
|---|---|---|---|
| `ubuntu` | apt | `ubuntu:24.04` | Ubuntu 24.04 cloud image |
| `debian` | apt | `debian:13` | Debian 13 genericcloud |
| `archlinux` | pacman | `archlinux:latest` | Arch Linux cloud image |

All built-in flavors are enabled by default. The `flavors` section enables,
disables or customizes them and defines derived flavors:

```yaml
flavors:
  ubuntu:
    vars:                          # available as flavor.vars in expressions
      apt-options: "--no-install-recommends"   # used by package installs
  debian:
    base-image: debian:13-slim     # container base image
  archlinux:
    enabled: false
  mint:                            # a custom flavor
    based-on: ubuntu               # inherits packages, paths and conventions
    base-image: linuxmintd/mint22-amd64
    cloud-image: https://example.com/mint-cloud.qcow2
```

A derived flavor matches `flavors:` filters and `by-flavor` keys by its own name,
its base flavor name and its family (`debian` or `arch`).

Generic package names are mapped to the flavor's names; for example
`build-essential` becomes `base-devel` and `openssh-server` becomes `openssh`
on Arch.

## Targets

`targets` restricts the targets a configuration may be used with (default: all).
Renderer names (`shell`, `dockerfile`, `cloud-init`) also allow the targets
built on them (`apply`; `oci`, `wsl`; `qcow2`, `ova`). See
[Targets](targets.md).

```yaml
targets: [shell, apply, oci]
```

## Artifacts

Target-specific settings: one block per target, all optional, all values may
use [expressions](expressions.md). Settings of targets you do not build are
ignored.

```yaml
artifacts:
  shell:      { colors: true }
  dockerfile: { base-image: debian:13-slim, expose: [80], cmd: [nginx, -g, "daemon off;"] }
  oci:        { tags: ["registry.example.com/web:${{ flavor.name }}"], push: true }
  wsl:        { default-user: dev, systemd: true }
  cloud-init: { hostname: web-01, extra: { ssh_pwauth: false } }
  qcow2:      { disk-size: 40G, memory: 4096, timeout: 1h }
  ova:        { memory: 4096, cpus: 2 }
```

[Targets](targets.md) explains what each setting does for its target, and the
[settings reference](reference/settings.md) lists all settings with their types
and defaults.

## Steps

```yaml
steps:
  - name: motd                   # optional; [A-Za-z0-9_-]; needed to reference outputs
    title: Message of the day    # optional; shown in logs and comments
    action: write-file           # built-in or custom action
    args:                        # action arguments
      path: /etc/motd
      content: Hello ${{ args.user }}
    output:                      # values for later steps: steps.motd.output.path
      path: /etc/motd
```

### Conditions and filters (generation time)

| Key | Meaning |
|---|---|
| `if` | JavaScript condition; the step is skipped when it is falsy |
| `flavors` | only for these flavors (names, base flavor or family) |
| `targets` | only for these targets or renderers |
| `foreach` | repeat the step for each element of a list (or mapping); `item` and `index` are set |
| `pre-script` | JavaScript run before the arguments are rendered; variables it defines can be used in templates |
| `pre-validation` | JavaScript assertion before rendering; falsy or an exception fails generation |
| `post-validation` | JavaScript assertion after rendering; sees `output` and `rendered` (the shell form of the step) |

These are evaluated **when osb generates the output**, not on the target machine.
See [Expressions](expressions.md).

### Run-time guards and error handling

| Key | Meaning |
|---|---|
| `only-if` | shell command; the step runs only if it succeeds |
| `unless` | shell command; the step is skipped if it succeeds |
| `creates` | path; the step is skipped if it exists |
| `on-error` | `fail` (default) or `continue`: log a warning and carry on |
| `requires.packages` | packages installed first if missing; a list, or a mapping of flavor to list |

```yaml
  - title: Initialize the database once
    action: run-command
    creates: /var/lib/app/.initialized
    on-error: continue
    requires:
      packages: { debian: [postgresql-client], archlinux: [postgresql] }
    args:
      command: [/usr/local/bin/app, init]
```

Steps with guards or `on-error: continue` are always rendered as shell, also in
Dockerfiles and cloud-init (instead of `COPY`, `write_files`, ...).

### Blocks

A step with `steps` instead of `action` groups steps. `if`, `flavors`, `targets`,
`foreach`, guards and `on-error` apply to all nested steps; variables from the
block's `pre-script` are visible inside.

```yaml
  - title: Desktop
    if: args.desktop
    steps:
      - action: install-packages
        args: { packages: [xfce4] }
      - action: enable-systemd-unit
        args: { units: [lightdm.service] }
```

### Values per flavor

Any argument value can be chosen per flavor with `by-flavor`; keys are flavor
names, base flavor names, families (`debian`, `arch`) or `default`:

```yaml
  - action: install-packages
    args:
      packages:
        by-flavor:
          arch: [docker]
          default: [docker.io]
```

### Step outputs

Every named step exposes `steps.<name>.enabled` (whether it ran at generation
time) and `steps.<name>.output`. A custom action's `output` becomes the output of
the step that uses it. A `foreach` step's output is a list with one entry per item.

```yaml
  - name: user
    action: add-user
    args: { name: alice }
    output: { home: /home/alice }
  - action: write-file
    if: steps.user.enabled
    args:
      path: ${{ steps.user.output.home }}/.hushlogin
      content: ""
```

## Secrets

Arguments of type `secret` are masked as `***` in logs, plans, error messages,
`apply --show-script` and scripts printed to a terminal.

- **Dockerfile / OCI / WSL:** secret values are never written into the
  Dockerfile. Steps that use them read the value from a BuildKit secret
  (`RUN --mount=type=secret,id=NAME,env=OSB_ARG_NAME`); build with
  `docker build --secret id=NAME,env=OSB_ARG_NAME` (`osb build` does that for
  you). The values are neither stored in an image layer nor in the build context.
- **shell / cloud-init:** the secret is part of the generated file. osb warns and
  writes the file with restrictive permissions.
- `set-password` with `random: true` generates the password on the machine and
  prints it. For images the password is generated during the build, so all
  instances share it; osb warns about that.

## Imports and custom actions

See [Libraries](libraries.md).
