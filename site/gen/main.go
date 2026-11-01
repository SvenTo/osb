// Command gen builds the static website: it renders the pages in site/pages
// and the Markdown documentation in docs/ with the layout in site/templates,
// and copies site/static. The WebAssembly binary is built by `make site`.
//
//	go run ./site/gen -o out/site
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"

	"github.com/SvenTo/osb/library"
)

// docPage is a page generated from a Markdown file in docs/.
type docPage struct {
	Src   string // relative to docs/
	Out   string // relative to the site root
	Title string // navigation title
}

var docPages = []docPage{
	{"README.md", "docs/index.html", "Overview"},
	{"getting-started.md", "docs/getting-started.html", "Getting started"},
	{"configuration.md", "docs/configuration.html", "Configuration"},
	{"expressions.md", "docs/expressions.html", "Expressions"},
	{"targets.md", "docs/targets.html", "Targets"},
	{"libraries.md", "docs/libraries.html", "Libraries & tests"},
	{"reference/actions.md", "docs/reference/actions.html", "Action reference"},
	{"reference/settings.md", "docs/reference/settings.html", "Target settings"},
	{"reference/cli.md", "docs/reference/cli.html", "Command line"},
}

// page is the data of the layout template.
type page struct {
	Title       string
	Description string
	Section     string // home, builder, playground, docs
	Root        string // relative path from the page to the site root ("", "../", ...)
	Path        string // output path, relative to the site root
	Content     template.HTML
	Docs        []navItem
	TOC         []navItem
	EditURL     string
	Scripts     []string
	Version     string
	Repo        string
}

type navItem struct {
	Title  string
	URL    string
	Active bool
}

var (
	out     = flag.String("o", "out/site", "output directory")
	srcDir  = flag.String("src", ".", "repository root")
	repo    = flag.String("repo", "https://github.com/SvenTo/osb", "GitHub repository URL")
	version = flag.String("version", "dev", "osb version shown on the site")
)

func main() {
	flag.Parse()
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	layout, err := template.ParseFiles(filepath.Join(*srcDir, "site/templates/layout.html"))
	if err != nil {
		return err
	}
	if err := copyDir(filepath.Join(*srcDir, "site/static"), *out); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(*srcDir, "docs/osb.schema.json"), filepath.Join(*out, "osb.schema.json")); err != nil {
		return err
	}

	// Pages: site/pages/*.html define the templates "title", "description", "main" and "scripts".
	pages, _ := filepath.Glob(filepath.Join(*srcDir, "site/pages/*.html"))
	for _, p := range pages {
		name := strings.TrimSuffix(filepath.Base(p), ".html")
		t, err := template.Must(layout.Clone()).ParseFiles(p)
		if err != nil {
			return err
		}
		section := name
		if name == "index" {
			section = "home"
		}
		data := page{Section: section, Path: name + ".html", Version: *version, Repo: *repo}
		data.Title = execString(t, "title", data)
		data.Description = execString(t, "description", data)
		if s := strings.TrimSpace(execString(t, "scripts", data)); s != "" {
			data.Scripts = strings.Fields(s)
		}
		var body bytes.Buffer
		if err := t.ExecuteTemplate(&body, "main", data); err != nil {
			return err
		}
		data.Content = template.HTML(body.String())
		if name == "404" {
			data.Section = ""
		}
		if err := write(t, data); err != nil {
			return err
		}
	}

	// Documentation
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	all := append(append([]docPage{}, docPages...), docPage{Out: "docs/library.html", Title: "Extra action sources"})
	for _, d := range all {
		data := page{Section: "docs", Path: d.Out, Root: rootOf(d.Out), Version: *version, Repo: *repo}
		for _, o := range all {
			data.Docs = append(data.Docs, navItem{Title: o.Title, URL: rel(d.Out, o.Out), Active: o.Out == d.Out})
		}
		var src []byte
		if d.Src != "" {
			src, err = os.ReadFile(filepath.Join(*srcDir, "docs", d.Src))
			if err != nil {
				return err
			}
			data.EditURL = *repo + "/blob/main/docs/" + d.Src
		} else {
			src = librarySource()
			data.EditURL = *repo + "/tree/main/library"
		}
		doc := md.Parser().Parse(text.NewReader(src))
		title := d.Title
		ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n := n.(type) {
			case *ast.Link:
				n.Destination = []byte(rewriteLink(d, string(n.Destination)))
			case *ast.Heading:
				id, _ := n.AttributeString("id")
				ids, _ := id.([]byte)
				switch n.Level {
				case 1:
					if title == d.Title {
						title = plainText(n, src)
					}
				case 2:
					data.TOC = append(data.TOC, navItem{Title: plainText(n, src), URL: "#" + string(ids)})
				}
			}
			return ast.WalkContinue, nil
		})
		var body bytes.Buffer
		if err := md.Renderer().Render(&body, src, doc); err != nil {
			return err
		}
		if len(data.TOC) < 3 {
			data.TOC = nil
		}
		data.Title = title
		data.Description = "osb documentation: " + d.Title
		data.Content = template.HTML(body.String())
		if err := write(layout, data); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", *out)
	return nil
}

func librarySource() []byte {
	var b bytes.Buffer
	b.WriteString("# Extra action sources\n\n")
	b.WriteString("The [extra actions](reference/actions.md#extra-actions) are written in osb's YAML format and compiled into the binary. ")
	b.WriteString("These are their sources (`osb actions source <action>` prints them, too); the tests are next to them in [`library/`](../library).\n\n")
	for _, g := range library.Groups() {
		data, _ := library.File(g.Name + ".yaml")
		fmt.Fprintf(&b, "## %s.yaml\n\n%s\n\n", g.Name, g.Description)
		for _, a := range g.Actions {
			fmt.Fprintf(&b, "[`%s`](reference/actions.md#%s) ", a.Name, a.Name)
		}
		fmt.Fprintf(&b, "\n\n````yaml\n%s````\n\n", data)
	}
	return b.Bytes()
}

func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(src))
		} else {
			b.WriteString(plainText(c, src))
		}
	}
	return b.String()
}

// rewriteLink maps links between Markdown files to the generated pages and
// links to other repository files to GitHub.
func rewriteLink(d docPage, dest string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") {
		return dest
	}
	target, frag, _ := strings.Cut(dest, "#")
	if frag != "" {
		frag = "#" + frag
	}
	from := d.Src
	if from == "" {
		from = "library.md"
	}
	repoPath := path.Clean(path.Join("docs", path.Dir(from), target)) // path in the repository
	for _, o := range docPages {
		if repoPath == "docs/"+o.Src {
			return rel(d.Out, o.Out) + frag
		}
	}
	if repoPath == "docs/library.md" {
		return rel(d.Out, "docs/library.html") + frag
	}
	if repoPath == "docs/osb.schema.json" {
		return rel(d.Out, "osb.schema.json")
	}
	kind := "blob"
	if st, err := os.Stat(filepath.Join(*srcDir, repoPath)); err == nil && st.IsDir() {
		kind = "tree"
	}
	return *repo + "/" + kind + "/main/" + repoPath + frag
}

func rootOf(p string) string {
	return strings.Repeat("../", strings.Count(p, "/"))
}

// rel returns the relative URL from page "from" to page "to" (both relative to the root).
func rel(from, to string) string {
	return rootOf(from) + to
}

func execString(t *template.Template, name string, data any) string {
	if t.Lookup(name) == nil {
		return ""
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		log.Fatal(err)
	}
	return strings.TrimSpace(b.String())
}

func write(t *template.Template, data page) error {
	p := filepath.Join(*out, filepath.FromSlash(data.Path))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, "layout", data); err != nil {
		return fmt.Errorf("%s: %w", data.Path, err)
	}
	return os.WriteFile(p, b.Bytes(), 0o644)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, r), 0o755)
		}
		return copyFile(p, filepath.Join(dst, r))
	})
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
