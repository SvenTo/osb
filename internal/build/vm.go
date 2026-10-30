package build

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SvenTo/osb/internal/render/cloudinit"
)

type vmSettings struct {
	baseImage string
	sha256    string
	diskSize  string
	memory    int
	cpus      int
	driver    string
	timeout   time.Duration
	compress  bool
	uri       string
}

func (c *Context) vmSettings() (*vmSettings, error) {
	m := c.Plan.Artifact("qcow2")
	s := &vmSettings{
		baseImage: setting(m, "base-image", c.Plan.Flavor.CloudImage),
		sha256:    setting(m, "sha256", ""),
		diskSize:  setting(m, "disk-size", "10G"),
		driver:    setting(m, "driver", "qemu"),
		compress:  boolSetting(m, "compress", true),
		uri:       setting(m, "libvirt-uri", "qemu:///session"),
	}
	var err error
	if s.memory, err = intSetting(m, "memory", 2048); err != nil {
		return nil, err
	}
	if s.cpus, err = intSetting(m, "cpus", 2); err != nil {
		return nil, err
	}
	if s.timeout, err = time.ParseDuration(setting(m, "timeout", "30m")); err != nil {
		return nil, fmt.Errorf("qcow2.timeout: %w", err)
	}
	if s.driver != "qemu" && s.driver != "libvirt" {
		return nil, fmt.Errorf("qcow2.driver must be qemu or libvirt, got %q", s.driver)
	}
	if s.baseImage == "" {
		return nil, fmt.Errorf("no cloud image for flavor %s; set artifacts.qcow2.base-image", c.Plan.Flavor.Name)
	}
	return s, nil
}

// seedServer serves NoCloud user-data and meta-data over HTTP. QEMU's user
// networking maps 10.0.2.2 to the host loopback interface.
type seedServer struct {
	srv    *http.Server
	port   int
	mu     sync.Mutex
	served map[string]bool
}

func startSeedServer(userData, metaData []byte) (*seedServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &seedServer{port: ln.Addr().(*net.TCPAddr).Port, served: map[string]bool{}}
	mux := http.NewServeMux()
	files := map[string][]byte{"/user-data": userData, "/meta-data": metaData, "/vendor-data": nil, "/network-config": nil}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		content, ok := files[r.URL.Path]
		if !ok || (content == nil && r.URL.Path == "/network-config") {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		s.served[r.URL.Path] = true
		s.mu.Unlock()
		_, _ = w.Write(content)
	})
	s.srv = &http.Server{Handler: mux}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *seedServer) url() string {
	return fmt.Sprintf("http://10.0.2.2:%d/", s.port)
}

func (s *seedServer) wasServed(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served[path]
}

func (s *seedServer) stop() { _ = s.srv.Close() }

func imageFormat(p string) (string, int64, error) {
	out, err := output("qemu-img", "info", "--output=json", p)
	if err != nil {
		return "", 0, err
	}
	var info struct {
		Format      string `json:"format"`
		VirtualSize int64  `json:"virtual-size"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", 0, err
	}
	return info.Format, info.VirtualSize, nil
}

// QCOW2 builds a VM disk image: the flavor's cloud image is booted with the
// rendered cloud-init configuration, which runs the steps and powers off.
func QCOW2(c *Context) (*Output, error) {
	p, err := c.buildQCOW2(filepath.Join(c.OutDir, c.baseName()+".qcow2"))
	if err != nil {
		return nil, err
	}
	return &Output{Files: []string{p}}, nil
}

func (c *Context) buildQCOW2(dest string) (string, error) {
	s, err := c.vmSettings()
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return "", fmt.Errorf("qemu-img is required (package qemu-img / qemu-utils)")
	}
	base, err := c.fetch(s.baseImage, s.sha256)
	if err != nil {
		return "", err
	}
	format, _, err := imageFormat(base)
	if err != nil {
		return "", err
	}
	work := filepath.Join(c.OutDir, ".work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", err
	}
	if !c.KeepWorkDir {
		defer os.RemoveAll(work)
	}
	disk := filepath.Join(work, "disk.qcow2")
	_ = os.Remove(disk)
	absBase, _ := filepath.Abs(base)
	if err := c.run("qemu-img", "create", "-f", "qcow2", "-F", format, "-b", absBase, disk, s.diskSize); err != nil {
		return "", err
	}

	ci, err := cloudinit.Render(c.Plan, cloudinit.Options{Version: c.Version, Command: c.Command, Build: true})
	if err != nil {
		return "", err
	}
	if c.KeepWorkDir {
		_ = writeFile(filepath.Join(work, "user-data"), ci.UserData, 0o600)
		_ = writeFile(filepath.Join(work, "meta-data"), ci.MetaData, 0o644)
	}
	seed, err := startSeedServer(ci.UserData, ci.MetaData)
	if err != nil {
		return "", err
	}
	defer seed.stop()

	console := filepath.Join(work, "console.log")
	_ = os.Remove(console)
	c.log("booting %s in a VM (%s, %d MiB, %d CPUs, timeout %s)", filepath.Base(base), s.driver, s.memory, s.cpus, s.timeout)
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	stopTail := c.tailConsole(console)
	if s.driver == "libvirt" {
		err = c.runLibvirt(ctx, s, disk, console, seed.url())
	} else {
		err = c.runQEMU(ctx, s, disk, console, seed.url())
	}
	stopTail()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("the build VM did not finish within %s; last console output:\n%s", s.timeout, consoleTail(console))
	}
	if err != nil {
		return "", err
	}
	log, _ := os.ReadFile(console)
	switch {
	case strings.Contains(string(log), cloudinit.MarkerOK):
		c.log("configuration finished successfully")
	case strings.Contains(string(log), cloudinit.MarkerFailed):
		return "", fmt.Errorf("the configuration failed inside the VM; console output:\n%s", consoleTail(console))
	case !seed.wasServed("/user-data"):
		return "", fmt.Errorf("the VM never fetched its cloud-init configuration (does the image support the NoCloud datasource?); console output:\n%s", consoleTail(console))
	default:
		return "", fmt.Errorf("the VM powered off without reporting a result; console output:\n%s", consoleTail(console))
	}

	c.log("writing %s", dest)
	args := []string{"convert", "-O", "qcow2"}
	if s.compress {
		args = append(args, "-c")
	}
	args = append(args, disk, dest+".part")
	if err := c.run("qemu-img", args...); err != nil {
		return "", err
	}
	if err := os.Rename(dest+".part", dest); err != nil {
		return "", err
	}
	return dest, nil
}

func consoleTail(p string) string {
	b, _ := os.ReadFile(p)
	return tail(strings.ReplaceAll(string(b), "\r", ""), 30)
}

// tailConsole streams the console log in verbose mode.
func (c *Context) tailConsole(p string) func() {
	if !c.Verbose {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var off int64
		for {
			if f, err := os.Open(p); err == nil {
				f.Seek(off, 0)
				buf := make([]byte, 64*1024)
				for {
					n, _ := f.Read(buf)
					if n <= 0 {
						break
					}
					os.Stderr.Write(buf[:n])
					off += int64(n)
				}
				f.Close()
			}
			select {
			case <-done:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	return func() { close(done); <-finished }
}

func kvmAvailable() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func (c *Context) runQEMU(ctx context.Context, s *vmSettings, disk, console, seedURL string) error {
	bin := "qemu-system-x86_64"
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s is required (package qemu-system-x86 / qemu-base)", bin)
	}
	accel, cpu := "tcg", "max"
	if kvmAvailable() {
		accel, cpu = "kvm", "host"
	} else {
		c.log("warning: /dev/kvm is not available, the build will be slow")
	}
	args := []string{
		"-machine", "q35,accel=" + accel, "-cpu", cpu,
		"-m", strconv.Itoa(s.memory), "-smp", strconv.Itoa(s.cpus),
		"-display", "none", "-monitor", "none", "-no-reboot",
		"-serial", "file:" + console,
		"-drive", "file=" + disk + ",if=virtio,format=qcow2,discard=unmap",
		"-nic", "user,model=virtio-net-pci",
		"-smbios", "type=1,serial=ds=nocloud;s=" + seedURL,
		"-device", "virtio-rng-pci",
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "+ %s %s\n", bin, strings.Join(args, " "))
	}
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("qemu: %v: %s", err, stderr.String())
	}
	return nil
}

const domainXML = `<domain type='%s'>
  <name>%s</name>
  <memory unit='MiB'>%d</memory>
  <vcpu>%d</vcpu>
  <sysinfo type='smbios'>
    <system><entry name='serial'>ds=nocloud;s=%s</entry></system>
  </sysinfo>
  <os>
    <type arch='x86_64' machine='q35'>hvm</type>
    <smbios mode='sysinfo'/>
  </os>
  <features><acpi/><apic/></features>
  <cpu mode='%s'/>
  <on_poweroff>destroy</on_poweroff>
  <on_reboot>destroy</on_reboot>
  <on_crash>destroy</on_crash>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='%s'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='user'><model type='virtio'/></interface>
    <serial type='file'><source path='%s'/><target port='0'/></serial>
    <video><model type='vga'/></video>
    <rng model='virtio'><backend model='random'>/dev/urandom</backend></rng>
  </devices>
</domain>
`

func (c *Context) runLibvirt(ctx context.Context, s *vmSettings, disk, console, seedURL string) error {
	if _, err := exec.LookPath("virsh"); err != nil {
		return fmt.Errorf("virsh is required for the libvirt driver")
	}
	absDisk, _ := filepath.Abs(disk)
	absConsole, _ := filepath.Abs(console)
	domType, cpu := "qemu", "maximum"
	if kvmAvailable() {
		domType, cpu = "kvm", "host-passthrough"
	}
	name := fmt.Sprintf("osb-build-%s-%d", c.baseName(), time.Now().Unix())
	xml := fmt.Sprintf(domainXML, domType, name, s.memory, s.cpus, strings.ReplaceAll(seedURL, "&", "&amp;"), cpu, absDisk, absConsole)
	xmlPath := filepath.Join(filepath.Dir(disk), "domain.xml")
	if err := os.WriteFile(xmlPath, []byte(xml), 0o644); err != nil {
		return err
	}
	if err := c.run("virsh", "-c", s.uri, "create", xmlPath); err != nil {
		return err
	}
	defer exec.Command("virsh", "-c", s.uri, "destroy", name).Run()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
		state, err := output("virsh", "-c", s.uri, "domstate", name)
		if err != nil || strings.Contains(state, "shut off") {
			return nil
		}
	}
}
