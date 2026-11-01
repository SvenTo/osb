# Targets

A target is what osb makes from a configuration. *Render* targets write files,
*build* targets produce images, and `apply` configures the machine osb runs on.

| Target | Command | Result | Built on | Needs |
|---|---|---|---|---|
| [`shell`](#shell) | `osb render shell` | POSIX shell script | – | nothing |
| [`dockerfile`](#dockerfile) | `osb render dockerfile` | self-contained `Dockerfile` | – | nothing |
| [`cloud-init`](#cloud-init) | `osb render cloud-init` | `user-data`, `meta-data`, `seed.iso` | – | optional: `xorriso`, `genisoimage`, `mkisofs` or `cloud-localds` for `seed.iso` |
| [`oci`](#oci) | `osb build oci` | container image | dockerfile | `docker` with BuildKit (default since Docker 23) or `podman` |
| [`wsl`](#wsl) | `osb build wsl` | WSL distribution (`.wsl`) | dockerfile | `docker` or `podman` |
| [`qcow2`](#qcow2) | `osb build qcow2` | VM disk image | cloud-init | `qemu-img`, `qemu-system-x86_64` (or `virsh`), KVM recommended |
| [`ova`](#ova) | `osb build ova` | VMware / VirtualBox appliance | cloud-init | as qcow2 |
| [`apply`](#apply) | `osb apply` | the configured machine | shell | root (or sudo) |

Every command takes the configuration with `-f` (default `osb.yaml`), the
flavor with `--flavor` and arguments with `-a name=value`. Results go to
`-o DIR`, by default `out/<name>-<flavor>-<target>/`.

## Target settings

Targets are configured in the `artifacts` section, one block per target. All
settings are optional and may use [expressions](expressions.md):

```yaml
artifacts:
  dockerfile:
    expose: [80]
  oci:
    tags: ["registry.example.com/web:${{ flavor.name }}"]
  qcow2:
    disk-size: 20G
```

The sections below show the important settings of each target; the
[settings reference](reference/settings.md) lists all of them with types and
defaults. Settings of a target that you do not build are ignored, so one
configuration can carry the settings of all its targets.

Some targets share settings:

- `oci` and `wsl` render the Dockerfile with `artifacts.dockerfile` and build it
  with `artifacts.oci.builder` and `artifacts.oci.platform`;
- `qcow2` and `ova` render cloud-init with `artifacts.cloud-init` and build the
  VM with `artifacts.qcow2`;
- `apply` and the cloud-init run script use `artifacts.shell`.

## How actions adapt

Container targets (`dockerfile`, `oci`, `wsl`) have no running init system, so
`set-hostname`, `add-swap-file`, `wait-for` and `restart-systemd-unit` are
skipped with a note, and `enable-systemd-unit` only creates the symlinks. In
expressions, `target.live` is true for targets that run on a booted system
(`shell`, `apply`, `cloud-init`, `qcow2`, `ova`) and `target.image` for targets
that produce an image (`dockerfile`, `oci`, `wsl`, `qcow2`, `ova`). Restrict a
step to some targets with `targets:`:

```yaml
steps:
  - title: Start nginx on running systems only
    targets: [shell, apply, cloud-init]
    action: enable-systemd-unit
    args: { units: [nginx.service], now: true }
```

## shell

A single POSIX `sh` script without dependencies beyond a base system
(coreutils, grep, sed, awk, util-linux), so it also runs in minimal container
images. It contains:

1. a header with the source, flavor and command used to generate it,
2. only the helper functions the script calls (`log`, `step`, `write_file`,
   `ensure_line`, `pkg_install`, ...), each with a short comment – a script
   that just installs packages and writes files has a handful of them,
3. `require_root` and a `check_flavor` guard that aborts on the wrong distribution,
4. one commented block per step.

```sh
# -----------------------------------------------------------------------------
# Step 3/4: Create user dev
# (name: user, action: add-user, osb.yaml:32)
# -----------------------------------------------------------------------------
step 3/4 'Create user dev'
id -u dev >/dev/null 2>&1 || useradd --create-home --shell /bin/bash dev
usermod -aG sudo dev
```

Actions are idempotent: running the script twice is safe (files are only
replaced, lines only added once, users only created when missing).

**Output:** `osb render shell` prints the script (secrets masked when printing
to a terminal) or writes it with `-o file.sh` / `-o dir/`.

**Settings** ([all](reference/settings.md#shell)):

```yaml
artifacts:
  shell:
    colors: true      # colored steps, warnings and errors in a terminal (default: plain text)
```

## dockerfile

`osb render dockerfile -o DIR` writes `DIR/Dockerfile`. It is
**self-contained**: no build context, no osb helper library and no extra
files. Build it with `docker build -t NAME DIR`, or take it as a template, edit
it and keep it in your repository without osb.

- Packages are installed with plain `apt-get` / `pacman` commands. The
  package caches never end up in the image: `RUN`s that use the package
  manager mount a tmpfs over them (see `package-cache` below). On Arch Linux
  the first `RUN` upgrades the base image once (`pacman -Syu`), later steps
  install with `pacman -S --needed`.
- Files (`write-file`, `template-file`, `copy-files`, `authorize-ssh-key`, ...)
  become `COPY --chmod --chown <<EOF` heredocs; downloads become
  `ADD --checksum`; `set-environment` becomes `ENV`.
- Everything else becomes `RUN`. Consecutive steps share one `RUN` (each
  starts with its `# N/TOTAL title` comment), so the image has few layers and
  a file changed by several steps is stored once. A single step is a command
  line or a heredoc script. When a script needs one of osb's shell helpers (for
  example `ensure_line`), the heredoc defines just those functions at its top.
- `RUN` uses `SHELL ["/bin/sh", "-eu", "-c"]`; Debian and Ubuntu images set
  `ARG DEBIAN_FRONTEND=noninteractive`.
- Secrets are never written into the file: steps that use them read the value
  from a BuildKit secret (`RUN --mount=type=secret,id=password,env=OSB_ARG_PASSWORD`).
  Build with `OSB_ARG_PASSWORD=... docker build --secret id=password,env=OSB_ARG_PASSWORD .`;
  the header of the Dockerfile shows the command, and `osb build oci` passes
  the values automatically.

It needs BuildKit (the default builder since Docker 23) for heredocs and
secret mounts.

**Settings** ([all](reference/settings.md#dockerfile)) control everything around
the steps – the base image and what the container looks like at run time:

```yaml
artifacts:
  dockerfile:
    base-image: debian:13-slim       # FROM (default: the flavor's image)
    env: { LANG: C.UTF-8 }           # ENV right after FROM, for all steps and the container
    labels:
      org.opencontainers.image.source: https://github.com/example/web
    user: www-data                   # USER at the end: the container runs as this user
    workdir: /srv/www                # WORKDIR at the end
    expose: [80]                     # EXPOSE
    volumes: [/srv/www/uploads]      # VOLUME
    entrypoint: [/usr/bin/tini, --]  # ENTRYPOINT (a list is exec form, a string shell form)
    cmd: [nginx, -g, daemon off;]    # CMD (default: ["/bin/bash"])
```

The steps always run as root; `user` only applies to the running container.

Three settings control the size of the image and the layers:

```yaml
artifacts:
  dockerfile:
    package-cache: tmpfs   # tmpfs (default), cache or remove
    merge-steps: true      # one RUN for consecutive steps (default: true)
    slim: true             # leave out documentation (default: false)
```

- `package-cache: tmpfs` mounts an empty in-memory file system over
  `/var/cache/apt` and `/var/lib/apt/lists` (Arch: `/var/cache/pacman/pkg`)
  while a `RUN` uses the package manager:

  ```dockerfile
  RUN --mount=type=tmpfs,target=/var/cache/apt \
      --mount=type=tmpfs,target=/var/lib/apt/lists apt-get update -q \
   && apt-get install -y -q --no-install-recommends curl vim
  ```

  The downloads stay in memory during the build; for very large installations
  (a desktop with thousands of packages) the builder needs enough RAM, or use
  `cache`. `cache` uses BuildKit cache mounts instead: downloads are kept on the
  builder and reused by the next build, which makes rebuilds much faster.
  `remove` deletes the caches at the end of each `RUN`, for builders without
  mount support.
- `merge-steps: false` gives every step its own `RUN`: more layers, but
  changing a late step reuses the cached layers before it. Steps that use a
  secret always get their own `RUN`, so the secret mount covers only them.
- `slim` keeps documentation out of the packages that the steps install
  (`path-exclude` in `/etc/dpkg/dpkg.cfg.d/slim`, `NoExtract` in
  `/etc/pacman.conf`): man and info pages, and on Debian docs and translations
  (the copyright files stay). The official Ubuntu and Arch images already leave
  out most documentation, so `slim` matters most for Debian. Each `RUN` also
  deletes `/tmp/*`, `/var/tmp/*`, `/root/.cache` and the package manager logs,
  so a step must not leave files in `/tmp` for a later step.

## oci

Renders the Dockerfile to `OUT/context` and builds it with `docker build` (or
`podman build`). The image is loaded into the local image store under its tags.

**Settings** ([all](reference/settings.md#oci)):

```yaml
artifacts:
  oci:
    tags:                              # default: <name>:<flavor>
      - registry.example.com/web:${{ flavor.name }}
      - registry.example.com/web:latest
    builder: podman                    # default: docker
    platform: linux/arm64              # build for another platform (needs QEMU/binfmt)
    archive: web.tar                   # also save the image to OUT/web.tar
    push: true                         # push all tags after the build (docker login first)
```

Secret arguments are passed to the build as BuildKit secrets from the
environment; they are neither in the image nor in its history.

## wsl

Builds the image like `oci`, adds `/etc/wsl.conf` and
`/etc/wsl-distribution.conf`, and exports the root file system as
`OUT/<name>-<flavor>.wsl` (a gzip compressed tar). Install it on Windows:

```powershell
wsl --install --from-file devbox-ubuntu.wsl
```

or, with older WSL versions:

```powershell
wsl --import DevBox C:\WSL\DevBox devbox-ubuntu.wsl
```

**Settings** ([all](reference/settings.md#wsl)):

```yaml
artifacts:
  wsl:
    name: DevBox          # name suggested by wsl --install (default: the config name)
    default-user: dev     # user WSL logs in as (default: root) – create it with add-user
    systemd: true         # boot with systemd, installed if missing (default: true)
    hostname: devbox      # default: the Windows computer name
```

The image is built with `artifacts.dockerfile` and `artifacts.oci.builder` /
`artifacts.oci.platform`.

## cloud-init

`osb render cloud-init -o DIR` writes `user-data`, `meta-data` and, if an ISO
tool is installed, a NoCloud `seed.iso`. Use them with any cloud or
virtualization platform that supports cloud-init, or attach `seed.iso` to a VM
booted from a cloud image.

cloud-init runs its modules in a fixed order (`write_files` → `users` →
`packages` → deferred `write_files` → `runcmd`), not in the order of your
steps. osb keeps your order: a step is rendered with a native module
(`write_files`, `users`, `packages`, `hostname`) only while that module runs
after everything rendered so far; from the first step that needs shell onwards,
steps go into a run script (`/var/lib/osb/run.sh`, executed by `runcmd`).
Comments in the script list which steps were rendered natively.

**Settings** ([all](reference/settings.md#cloud-init)):

```yaml
artifacts:
  cloud-init:
    hostname: web-01                # unless a set-hostname step sets one
    extra:                          # merged into the generated #cloud-config
      ssh_pwauth: false             # new keys are added
      runcmd: [echo extra]          # lists are appended (runs after osb's runcmd)
      timezone: Europe/Berlin       # other values replace osb's
```

`extra` is the escape hatch for cloud-init features osb has no action for, such
as `ntp`, `mounts` or `disk_setup`.

## qcow2

Builds a VM image the way a cloud would configure an instance:

1. downloads the flavor's official cloud image (cached for 7 days in
   `~/.cache/osb/images`; verify it with `sha256`),
2. creates an overlay disk of `disk-size`,
3. renders the configuration as cloud-init and serves it on a local HTTP port;
   the VM finds it through the NoCloud datasource (`-smbios ... ds=nocloud;s=http://10.0.2.2:PORT/`),
4. boots the VM with QEMU (KVM if `/dev/kvm` is usable) or libvirt,
5. waits until the configuration reports success on the serial console and the
   VM powers off (or `timeout` is reached),
6. writes a standalone `OUT/<name>-<flavor>.qcow2`, compressed with zstd by
   default, using all CPUs (`qemu-img convert -c -m N -W`).

At the end of the build osb removes the package caches, temporary files and
logs, and discards the free disk blocks (`fstrim`; the disk is attached with
`discard=unmap`), so deleted files take no space in the image. It resets the
machine id and cloud-init's state, and
configures cloud-init to run even without a datasource. Every VM started from the
image is therefore a new cloud-init instance: it gets new SSH host keys and a
network configuration for its own hardware (DHCP when no datasource provides one).

Use `-v` to follow the VM console, and `--keep` to keep the work directory
(`OUT/.work`) with the overlay disk, `user-data` and `console.log` for debugging.

**Settings** ([all](reference/settings.md#qcow2)):

```yaml
artifacts:
  qcow2:
    base-image: https://example.com/images/custom-cloud.qcow2   # default: the flavor's cloud image
    sha256: 3f1c...              # verify the base image
    disk-size: 40G               # virtual disk size (default: 10G)
    memory: 4096                 # MiB for the build VM (default: 2048)
    cpus: 4                      # build VM CPUs (default: 2)
    driver: libvirt              # qemu (default) or libvirt
    libvirt-uri: qemu:///system  # default: qemu:///session
    timeout: 1h                  # maximum build time (default: 30m)
    compression: zlib            # zstd (default) or zlib for tools older than QEMU 5.1
    compress: false              # faster first reads, larger file (default: true)
```

`memory` and `cpus` only size the build VM; the image itself has no fixed
hardware. Large installations (desktops, many packages) need more `disk-size`,
`memory` and `timeout`.

## ova

Builds the qcow2 image (with `artifacts.qcow2`), converts it to a
streamOptimized VMDK and packages it with an OVF descriptor (CPUs, memory, SATA
disk, E1000 NIC) and a SHA-256 manifest as `OUT/<name>-<flavor>.ova`,
importable in VMware and VirtualBox.

**Settings** ([all](reference/settings.md#ova)) describe the virtual machine in
the OVF:

```yaml
artifacts:
  ova:
    name: web-appliance     # VM name (default: <name>-<flavor>)
    memory: 4096            # MiB (default: 2048)
    cpus: 2                 # default: 2
    os-type: ubuntu64Guest  # VMware guest OS id (default per flavor)
```

## apply

Configures the machine osb runs on. It detects the flavor from
`/etc/os-release` (override with `--flavor`), prints a summary of every step
(secrets masked), asks for confirmation and runs the generated shell script
with `/bin/sh`, through `sudo` when not running as root. It uses
`artifacts.shell`.

```bash
sudo osb apply --dry-run
```

```bash
sudo osb apply --show-script
```

```bash
sudo osb apply --yes --arg user=alice
```
