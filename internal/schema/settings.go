package schema

// Setting is a target setting: artifacts.<target>.<name>.
type Setting struct {
	Name string
	// Type: string, int, bool, list, map, or list|string (a list or one string)
	Type        string
	Default     string // Markdown, "" when there is no default
	Description string // Markdown
	Enum        []string
}

// TargetSettings are the settings of one target, in documentation order.
type TargetSettings struct {
	Target   string
	Summary  string // Markdown
	Settings []Setting
	Note     string // Markdown, shown after the table
}

// Targets documents artifacts.<target>; the JSON schema and the settings
// reference are generated from it. All values may use ${{ }} expressions.
var Targets = []TargetSettings{
	{
		Target:  "shell",
		Summary: "The generated POSIX shell script: `osb render shell`, `osb apply` and the run script inside cloud-init.",
		Settings: []Setting{
			{Name: "colors", Type: "bool", Default: "`false`", Description: "Color the messages (steps, warnings, errors) when the script runs in a terminal. Without it, the script contains no terminal handling at all."},
		},
	},
	{
		Target:  "dockerfile",
		Summary: "The self-contained Dockerfile: `osb render dockerfile`, and the base of `oci` and `wsl`.",
		Settings: []Setting{
			{Name: "base-image", Type: "string", Default: "the flavor's image: `ubuntu:24.04`, `debian:13`, `archlinux:latest`", Description: "Image for `FROM`. It must belong to the flavor's distribution family."},
			{Name: "env", Type: "map", Description: "Environment variables, written as `ENV` right after `FROM`: available to all steps and in the running container."},
			{Name: "labels", Type: "map", Description: "Additional `LABEL`s. osb always sets `org.opencontainers.image.title` (the configuration's `name`) and `org.opencontainers.image.description`."},
			{Name: "user", Type: "string", Description: "`USER` at the end of the file: the user the container runs as. All steps run as root before it."},
			{Name: "workdir", Type: "string", Description: "`WORKDIR` at the end of the file: the working directory of the container."},
			{Name: "expose", Type: "list", Description: "Ports for `EXPOSE`, e.g. `[80, 443/tcp, 53/udp]`. Documentation only: publish them with `docker run -p`."},
			{Name: "volumes", Type: "list", Description: "Paths for `VOLUME`, e.g. `[/data]`."},
			{Name: "entrypoint", Type: "list|string", Description: "`ENTRYPOINT`: a list is written in exec form (`[\"/usr/bin/tini\", \"--\"]`), a string in shell form."},
			{Name: "cmd", Type: "list|string", Default: "`[\"/bin/bash\"]` when neither `cmd` nor `entrypoint` is set", Description: "`CMD`: a list is written in exec form, a string in shell form."},
			{Name: "package-cache", Type: "string", Default: "`tmpfs`", Enum: []string{"tmpfs", "cache", "remove"}, Description: "How package downloads and lists stay out of the image. `tmpfs`: `RUN`s that use the package manager mount a temporary file system over the cache directories (`--mount=type=tmpfs`); it lives in memory, so very large installations need enough RAM. `cache`: a BuildKit cache mount that keeps the downloads on the builder between builds, for fast rebuilds. `remove`: delete the caches at the end of each `RUN`."},
			{Name: "merge-steps", Type: "bool", Default: "`true`", Description: "Merge consecutive steps without secrets into one `RUN`, so that a file changed by several steps is stored once and the image has fewer layers. `false` gives one `RUN` per step, so a changed step rebuilds less."},
			{Name: "slim", Type: "bool", Default: "`false`", Description: "Leave out documentation: man and info pages (and on Debian and Ubuntu docs and translations, keeping the copyright files) of the packages installed by the steps; each `RUN` also removes `/tmp`, `/var/tmp`, `/root/.cache` and the package manager logs."},
		},
	},
	{
		Target:  "oci",
		Summary: "`osb build oci`: builds the Dockerfile into a container image. `builder` and `platform` are also used by `wsl`.",
		Settings: []Setting{
			{Name: "tags", Type: "list|string", Default: "`<name>:<flavor>`", Description: "Image names, e.g. `[\"registry.example.com/devbox:${{ flavor.name }}\", devbox:latest]`."},
			{Name: "builder", Type: "string", Default: "`docker`", Enum: []string{"docker", "podman"}, Description: "Container CLI that builds (and saves and pushes) the image. docker needs BuildKit, the default since Docker 23."},
			{Name: "platform", Type: "string", Description: "Build for another platform, e.g. `linux/arm64` (`--platform`). Building for a foreign architecture needs QEMU emulation (binfmt)."},
			{Name: "archive", Type: "string", Description: "Additionally save the image to this tar file (`docker save`). A relative path is relative to the output directory."},
			{Name: "push", Type: "bool", Default: "`false`", Description: "Push all tags after the build. Log in to the registry first (`docker login`)."},
		},
	},
	{
		Target:  "wsl",
		Summary: "`osb build wsl`: builds the image like `oci`, adds the WSL configuration and exports `<name>-<flavor>.wsl`.",
		Settings: []Setting{
			{Name: "name", Type: "string", Default: "the configuration's `name`", Description: "Distribution name that `wsl --install --from-file` suggests (`[oobe] defaultName` in `/etc/wsl-distribution.conf`)."},
			{Name: "default-user", Type: "string", Default: "root", Description: "User that WSL logs in as (`[user] default` in `/etc/wsl.conf`). Create the user with `add-user`."},
			{Name: "systemd", Type: "bool", Default: "`true`", Description: "Boot the distribution with systemd (`[boot] systemd`), so services run. systemd is installed when the image does not have it."},
			{Name: "hostname", Type: "string", Default: "the Windows computer name", Description: "Host name of the distribution (`[network] hostname`)."},
		},
		Note: "The image is built with `artifacts.oci.builder` and `artifacts.oci.platform`.",
	},
	{
		Target:  "cloud-init",
		Summary: "The `#cloud-config` user-data: `osb render cloud-init`, and the configuration of the `qcow2` and `ova` build VMs.",
		Settings: []Setting{
			{Name: "hostname", Type: "string", Description: "Host name, set with cloud-init's `hostname` module. A `set-hostname` step takes precedence."},
			{Name: "extra", Type: "map", Description: "cloud-config merged into the generated user-data: new keys are added, lists are appended to osb's lists (for example `runcmd` entries run after osb's), and other values replace osb's."},
		},
	},
	{
		Target:  "qcow2",
		Summary: "`osb build qcow2`: boots a cloud image with the configuration as cloud-init and saves the disk as `<name>-<flavor>.qcow2`. `ova` builds with these settings, too.",
		Settings: []Setting{
			{Name: "base-image", Type: "string", Default: "the flavor's official cloud image (Ubuntu 24.04, Debian 13 genericcloud, Arch Linux cloudimg)", Description: "URL or path of a cloud image with cloud-init; a relative path is relative to the configuration. Downloads are cached for 7 days in `~/.cache/osb/images`."},
			{Name: "sha256", Type: "string", Description: "Expected SHA-256 checksum of the base image; the build fails on a mismatch."},
			{Name: "disk-size", Type: "string", Default: "`10G`", Description: "Virtual size of the disk (`qemu-img` sizes: `512M`, `20G`). The cloud image grows its root file system to it on boot."},
			{Name: "memory", Type: "int", Default: "`2048`", Description: "Memory of the build VM in MiB."},
			{Name: "cpus", Type: "int", Default: "`2`", Description: "Number of CPUs of the build VM."},
			{Name: "driver", Type: "string", Default: "`qemu`", Enum: []string{"qemu", "libvirt"}, Description: "`qemu` runs `qemu-system-x86_64` directly (with KVM when `/dev/kvm` is usable); `libvirt` creates a transient domain with `virsh`."},
			{Name: "libvirt-uri", Type: "string", Default: "`qemu:///session`", Description: "libvirt connection for `driver: libvirt`, e.g. `qemu:///system`."},
			{Name: "timeout", Type: "string", Default: "`30m`", Description: "Maximum duration of the build VM run, e.g. `90s`, `45m`, `1h30m`."},
			{Name: "compress", Type: "bool", Default: "`true`", Description: "Compress the image (`qemu-img convert -c`): a smaller file, slightly slower first reads."},
		},
			Note: "At the end of the build, osb removes the package caches, temporary files and logs and discards the free blocks of the disk (`fstrim`), so they do not take space in the image.",
	},
	{
		Target:  "ova",
		Summary: "`osb build ova`: builds the `qcow2` image (with `artifacts.qcow2`) and packages it as `<name>-<flavor>.ova` for VMware and VirtualBox.",
		Settings: []Setting{
			{Name: "name", Type: "string", Default: "`<name>-<flavor>`", Description: "Name of the virtual machine in the OVF descriptor."},
			{Name: "memory", Type: "int", Default: "`2048`", Description: "Memory of the virtual machine in MiB, written to the OVF (the build VM uses `artifacts.qcow2.memory`)."},
			{Name: "cpus", Type: "int", Default: "`2`", Description: "Number of CPUs of the virtual machine, written to the OVF."},
			{Name: "os-type", Type: "string", Default: "per flavor: `ubuntu64Guest`, `debian11_64Guest`, `other5xLinux64Guest`", Description: "VMware guest OS identifier; VirtualBox maps it to its own OS type."},
		},
	},
}

// settingSchema converts a setting to JSON schema. Strings are allowed for
// every type because values may be ${{ }} expressions.
func settingSchema(s Setting) obj {
	var o obj
	switch s.Type {
	case "bool":
		o = obj{"type": []string{"boolean", "string"}}
	case "int":
		o = obj{"type": []string{"integer", "string"}}
	case "list":
		o = obj{"type": []string{"array", "string", "integer"}}
	case "list|string":
		o = obj{"type": []string{"array", "string"}}
	case "map":
		o = obj{"type": "object"}
	default:
		o = obj{"type": "string"}
	}
	desc := plainMarkdown(s.Description)
	if len(s.Enum) > 0 {
		desc += " One of: " + joinWords(s.Enum) + "."
	}
	if s.Default != "" {
		desc += " Default: " + plainMarkdown(s.Default) + "."
	}
	o["description"] = desc
	return o
}

func artifactSchemas() obj {
	out := obj{}
	for _, t := range Targets {
		props := obj{}
		for _, s := range t.Settings {
			props[s.Name] = settingSchema(s)
		}
		out[t.Target] = obj{"type": "object", "additionalProperties": false, "properties": props, "description": plainMarkdown(t.Summary)}
	}
	return out
}

// plainMarkdown removes the Markdown code markers for editor tooltips.
func plainMarkdown(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '`' {
			b = append(b, s[i])
		}
	}
	return string(b)
}

func joinWords(l []string) string {
	out := ""
	for i, w := range l {
		if i > 0 {
			out += ", "
		}
		out += w
	}
	return out
}
