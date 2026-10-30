// Package ops defines the primitive operations that actions produce and
// renderers translate into their output format.
package ops

// Op is a primitive operation.
type Op interface{ opName() string }

// File writes a file.
type File struct {
	Path    string
	Content []byte
	Mode    string // octal, e.g. "0644"
	Owner   string
	Group   string
	Append  bool
}

// Shell is a shell snippet that may use functions of the helper library.
type Shell struct {
	Script string
}

// Packages installs packages (already mapped to flavor-specific names).
type Packages struct {
	Names []string
	// Missing installs only packages that are not installed yet (requirements).
	Missing bool
}

// User creates a user if it does not exist.
type User struct {
	Name       string
	UID        string
	Comment    string
	Home       string
	Shell      string
	Groups     []string
	System     bool
	CreateHome bool
}

// Hostname sets the host name.
type Hostname struct {
	Name string
}

// Env sets persistent environment variables (/etc/environment).
type Env struct {
	Vars []KV
}

// KV is an ordered key/value pair.
type KV struct {
	Key   string
	Value string
}

// Download fetches a URL to a path.
type Download struct {
	URL    string
	Path   string
	SHA256 string
	Mode   string
	Owner  string
	Group  string
}

// Note is an informational message; Warn notes are also reported at
// generation time.
type Note struct {
	Message string
	Warn    bool
}

func (File) opName() string     { return "file" }
func (Shell) opName() string    { return "shell" }
func (Packages) opName() string { return "packages" }
func (User) opName() string     { return "user" }
func (Hostname) opName() string { return "hostname" }
func (Env) opName() string      { return "env" }
func (Download) opName() string { return "download" }
func (Note) opName() string     { return "note" }

// Name returns the kind of an op.
func Name(o Op) string { return o.opName() }

// Guard is a run-time condition evaluated by the shell.
type Guard struct {
	Kind  string // only-if, unless, creates
	Value string
}

// Step is a fully evaluated step ready to be rendered.
type Step struct {
	Index   int
	Name    string
	Title   string
	Action  string
	Ops     []Op
	Guards  []Guard
	OnError string // fail or continue
	// Secret is true when the step's content includes a secret value.
	Secret bool
	// Source is the position in the configuration, for comments.
	Source string
}
