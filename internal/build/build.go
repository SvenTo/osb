// Package build builds images: OCI, WSL, qcow2 and OVA.
package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SvenTo/osb/internal/engine"
	"github.com/SvenTo/osb/internal/expr"
	"github.com/SvenTo/osb/internal/imports"
)

// Context holds the inputs of a build.
type Context struct {
	Plan        *engine.Plan
	OutDir      string
	Version     string
	Command     string
	Verbose     bool
	KeepWorkDir bool
	Log         func(format string, a ...any)
}

// Output lists the build results.
type Output struct {
	Files  []string
	Images []string
}

func (c *Context) log(format string, a ...any) {
	if c.Log != nil {
		c.Log(format, a...)
	}
}

func (c *Context) name() string {
	if c.Plan.Config.Name != "" {
		return c.Plan.Config.Name
	}
	return "osb"
}

// baseName is the output file name without extension.
func (c *Context) baseName() string {
	return c.name() + "-" + c.Plan.Flavor.Name
}

// run executes a command. Output is streamed in verbose mode; otherwise it is
// captured and shown on failure.
func (c *Context) run(name string, args ...string) error {
	return c.runIn("", nil, name, args...)
}

func (c *Context) runIn(dir string, env []string, name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is required but not installed", name)
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "+ %s %s\n", name, strings.Join(args, " "))
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		return cmd.Run()
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, tail(c.Plan.Secrets.String(buf.String()), 40))
	}
	return nil
}

func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func setting(m map[string]any, key, def string) string {
	if v, ok := m[key]; ok && v != nil {
		if s := expr.Stringify(v); s != "" {
			return s
		}
	}
	return def
}

func intSetting(m map[string]any, key string, def int) (int, error) {
	s := setting(m, key, strconv.Itoa(def))
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: expected an integer, got %q", key, s)
	}
	return n, nil
}

func boolSetting(m map[string]any, key string, def bool) bool {
	s := setting(m, key, strconv.FormatBool(def))
	b, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return b
}

func listSetting(m map[string]any, key string) []string {
	switch t := m[key].(type) {
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, expr.Stringify(e))
		}
		return out
	case string:
		if t != "" {
			return []string{t}
		}
	}
	return nil
}

// fetch downloads url into the cache (or returns a local path) and verifies sha256.
func (c *Context) fetch(src, sum string) (string, error) {
	if !strings.Contains(src, "://") || strings.HasPrefix(src, "file://") {
		p := strings.TrimPrefix(src, "file://")
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.Plan.Config.Dir, p)
		}
		if _, err := os.Stat(p); err != nil {
			return "", err
		}
		return p, verify(p, sum)
	}
	h := sha256.Sum256([]byte(src))
	dir := filepath.Join(imports.DefaultCacheDir(), "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, hex.EncodeToString(h[:])[:16]+"-"+filepath.Base(src))
	if st, err := os.Stat(p); err == nil && st.Size() > 0 && time.Since(st.ModTime()) < 7*24*time.Hour {
		c.log("using cached base image %s", p)
		return p, verify(p, sum)
	}
	c.log("downloading %s", src)
	resp, err := http.Get(src)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", src, resp.Status)
	}
	tmp := p + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	pr := &progress{total: resp.ContentLength, log: c.log, start: time.Now()}
	if _, err := io.Copy(f, io.TeeReader(resp.Body, pr)); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return p, verify(p, sum)
}

func verify(p, sum string) error {
	if sum == "" {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", p, got, sum)
	}
	return nil
}

type progress struct {
	total, done int64
	last        int64
	log         func(string, ...any)
	start       time.Time
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 0 {
		pct := p.done * 100 / p.total
		if pct/10 > p.last/10 {
			p.last = pct
			p.log("  %d%% of %d MiB", pct, p.total>>20)
		}
	}
	return len(b), nil
}

func writeFile(p string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, content, mode)
}
