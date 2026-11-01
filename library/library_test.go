package library_test

import (
	"testing"

	"github.com/SvenTo/osb/internal/actions"
	"github.com/SvenTo/osb/internal/config"
	"github.com/SvenTo/osb/internal/schema"
	"github.com/SvenTo/osb/library"
)

func TestLibrary(t *testing.T) {
	if len(library.Groups()) == 0 || len(library.Actions()) == 0 {
		t.Fatal("no extra actions")
	}
	seen := map[string]bool{}
	for _, a := range library.Actions() {
		if _, ok := actions.Get(a.Name); ok {
			t.Errorf("extra action %s shadows a built-in action", a.Name)
		}
		if seen[a.Name] {
			t.Errorf("extra action %s is defined twice", a.Name)
		}
		seen[a.Name] = true
		if a.Description == "" {
			t.Errorf("extra action %s has no description", a.Name)
		}
	}
}

func TestSchema(t *testing.T) {
	for _, n := range library.FileNames() {
		data, err := library.File(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := config.Parse(library.Prefix+n, data, config.LoadOptions{Validate: schema.Validate}); err != nil {
			t.Error(err)
		}
	}
}
