// Package vfs is where osb reads local files during evaluation (action
// sources, file(), copy-files): the operating system's file system, or an
// in-memory one in the browser build.
package vfs

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Files is the subset of file system operations osb needs.
type Files interface {
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)
	Glob(pattern string) ([]string, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
}

// Current is used by evaluation. The browser build replaces it for each rendering.
var Current Files = OS{}

// OS uses the operating system's file system.
type OS struct{}

func (OS) ReadFile(name string) ([]byte, error)         { return os.ReadFile(name) }
func (OS) Stat(name string) (fs.FileInfo, error)        { return os.Stat(name) }
func (OS) Glob(pattern string) ([]string, error)        { return filepath.Glob(pattern) }
func (OS) WalkDir(root string, fn fs.WalkDirFunc) error { return filepath.WalkDir(root, fn) }

// FS adapts an fs.FS (slash-separated paths relative to its root). Relative
// and absolute names are both resolved against the root.
type FS struct{ FS fs.FS }

func (m FS) name(p string) string {
	p = path.Clean(filepath.ToSlash(p))
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "."
	}
	return p
}

func (m FS) ReadFile(name string) ([]byte, error)  { return fs.ReadFile(m.FS, m.name(name)) }
func (m FS) Stat(name string) (fs.FileInfo, error) { return fs.Stat(m.FS, m.name(name)) }
func (m FS) Glob(pattern string) ([]string, error) { return fs.Glob(m.FS, m.name(pattern)) }
func (m FS) WalkDir(root string, fn fs.WalkDirFunc) error {
	return fs.WalkDir(m.FS, m.name(root), fn)
}
