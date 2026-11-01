# Targets

| Target | Command | Built on | Needs |
|---|---|---|---|
| `shell` | `osb render shell` | – | nothing |
| `dockerfile` | `osb render dockerfile` | – | nothing |
| `cloud-init` | `osb render cloud-init` | – | optional: `xorriso`, `genisoimage`, `mkisofs` or `cloud-localds` for `seed.iso` |
| `oci` | `osb build oci` | dockerfile | `docker` with BuildKit (default since Docker 23) or `podman` |
| `wsl` | `osb build wsl` | dockerfile | `docker` or `podman` |
| `qcow2` | `osb build qcow2` | cloud-init | `qemu-img`, `qemu-system-x86_64` (or `virsh` for the libvirt driver), KVM recommended |
| `ova` | `osb build ova` | cloud-init | as qcow2 |
| `apply` | `osb apply` | shell | root (or sudo) |

Actions adapt to the target. Container targets (`dockerfile`, `oci`, `wsl`) have
no running init system, so `set-hostname`, `add-swap-file`, `wait-for` and
`restart-systemd-unit` are skipped with a note, and `enable-systemd-unit` only
creates the symlinks. In expressions, `target.live` is true for targets that
run on a booted system and `target.image` for targets that produce an image.

## shell

A single POSIX `sh` script without dependencies beyond a base system
(coreutils, grep, sed, awk, util-linux), so it also runs in minimal container
images. It contains:

1. a header with the source, flavor and command used to generate it,
2. a small helper library (`log`, `warn`, `die`, `step`, `as_user`,
   `write_file`, `ensure_line`, `pkg_install`, ...), with short comments,
3. `require_root` and a `check_flavor` guard that aborts on the wrong distribution,
4. one commented block per step.

```sh
# -----------------------------------------------------------------------------
# Step 3/4: Create user dev
# (name: user, action: add-user, osb.yaml:32)
# -----------------------------------------------------------------------------
step 'Create user dev'
id -u dev >/dev/null 2>&1 || useradd --create-home --shell /bin/bash dev
usermod -aG sudo dev
```

Actions are idempotent: running the script twice is safe (files are only
replaced, lines only added once, users only created when missing).

`osb render shell` prints the script (secrets masked when printing to a
terminal) or writes it with `-o file.sh` / `-o dir/`.

## dockerfile

`osb render dockerfile -o DIR` writes `DIR/Dockerfile`. It is **self-contained**:
no build context, no osb helper library and no extra files. Build it with
`docker build -t NAME DIR`, or take it as a template, edit it and keep it in your
repository without osb.

- Packages are installed with plain `apt-get` / `pacman` commands, and the
  package caches are removed in the same layer.
- Files (`write-file`, `template-file`, `copy-files`, `authorize-ssh-key`, ...)
  become `COPY --chmod --chown <<EOF` heredocs; downloads become
  `ADD --checksum`; `set-environment` becomes `ENV`.
- Everything else is a `RUN` per step: a single command line, or a heredoc
  script. When a step needs one of osb's shell helpers (for example
  `ensure_line`), the heredoc defines just those functions at its top.
- `RUN` uses `SHELL ["/bin/sh", "-eu", "-c"]`; Debian and Ubuntu images set
  `ARG DEBIAN_FRONTEND=noninteractive`.
- The file ends with `CMD ["/bin/bash"]` unless `artifacts.dockerfile` sets
  `cmd` or `entrypoint`.
- Secrets are never written into the file: steps that use them read the value
  from a BuildKit secret (`RUN --mount=type=secret,id=password,env=OSB_ARG_PASSWORD`).
  Build with `OSB_ARG_PASSWORD=... docker build --secret id=password,env=OSB_ARG_PASSWORD .`;
  the header of the Dockerfile shows the command, and `osb build oci` passes
  the values automatically.

It needs BuildKit (the default builder since Docker 23) for heredocs and
secret mounts.

## oci

Renders the Dockerfile to `OUT/context` and runs `docker build` (or `podman
build` with `artifacts.oci.builder: podman`). Tags default to
`<name>:<flavor>`. With `artifacts.oci.archive` the image is also saved to a
tar file; with `push: true` the tags are pushed.

## wsl

Builds the image like `oci`, adds `/etc/wsl.conf` (systemd, default user,
hostname) and `/etc/wsl-distribution.conf`, and exports the root file system
as `OUT/<name>-<flavor>.wsl` (a gzip compressed tar). Install it on Windows:

```powershell
wsl --install --from-file devbox-ubuntu.wsl
```

or, with older WSL versions:

```powershell
wsl --import DevBox C:\WSL\DevBox devbox-ubuntu.wsl
```

## cloud-init

`osb render cloud-init -o DIR` writes `user-data`, `meta-data` and, if an ISO
tool is installed, a NoCloud `seed.iso`.

cloud-init runs its modules in a fixed order (`write_files` → `users` →
`packages` → deferred `write_files` → `runcmd`), not in the order of your
steps. osb keeps your order: a step is rendered with a native module
(`write_files`, `users`, `packages`, `hostname`) only while that module runs
after everything rendered so far; from the first step that needs shell onwards,
steps go into a run script (`/var/lib/osb/run.sh`, executed by `runcmd`).
Comments in the script list which steps were rendered natively.

`artifacts.cloud-init.extra` is merged into the generated `#cloud-config`.

## qcow2

Builds a VM image the way a cloud would configure an instance:

1. downloads the flavor's official cloud image (cached for 7 days in
   `~/.cache/osb/images`; verify with `artifacts.qcow2.sha256`),
2. creates an overlay disk of `disk-size`,
3. renders the configuration as cloud-init and serves it on a local HTTP port;
   the VM finds it through the NoCloud datasource (`-smbios ... ds=nocloud;s=http://10.0.2.2:PORT/`),
4. boots the VM with QEMU (KVM if `/dev/kvm` is usable) or libvirt
   (`driver: libvirt`, default URI `qemu:///session`),
5. waits until the configuration reports success on the serial console and the
   VM powers off (`timeout`, default 30 minutes),
6. writes a compressed standalone `OUT/<name>-<flavor>.qcow2`.

At the end of the build osb resets the machine id and cloud-init's state, and
configures cloud-init to run even without a datasource. Every VM started from the
image is therefore a new cloud-init instance: it gets new SSH host keys and a
network configuration for its own hardware (DHCP when no datasource provides one).

Use `-v` to follow the VM console, and `--keep` to keep the work directory
(`OUT/.work`) with the overlay disk, `user-data` and `console.log` for debugging.

## ova

Builds the qcow2 image, converts it to a streamOptimized VMDK and packages it
with an OVF descriptor (CPUs, memory, SATA disk, E1000 NIC) and a SHA-256
manifest as `OUT/<name>-<flavor>.ova`, importable in VMware and VirtualBox.

## apply

Configures the machine osb runs on. It detects the flavor from
`/etc/os-release` (override with `--flavor`), prints a summary of every step
(secrets masked), asks for confirmation and runs the generated shell script
with `/bin/sh`, through `sudo` when not running as root.

```bash
sudo osb apply --dry-run
```

```bash
sudo osb apply --show-script
```

```bash
sudo osb apply --yes --arg user=alice
```
