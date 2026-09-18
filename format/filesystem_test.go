package format

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestParseFSIncludesAndFormats(t *testing.T) {
	files := fstest.MapFS{
		"main.journal":         {Data: []byte("include nested/child.journal\n")},
		"nested/child.journal": {Data: []byte("!include ../accounts.ledger\n")},
		"accounts.ledger":      {Data: []byte("account Assets:Cash\n")},
		"account.bean":         {Data: []byte("2024-01-01 open Assets:Cash USD\n")},
	}
	for _, name := range []string{"main.journal", "account.bean"} {
		p, err := ParseFS(files, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(p.Accounts) != 1 || p.Accounts[0].Name != "Assets:Cash" {
			t.Fatalf("%s: %+v", name, p)
		}
	}
}

func TestParseFSRejectsEscapesAndCycles(t *testing.T) {
	for _, include := range []string{"../outside.ledger", "/outside.ledger", "main.journal"} {
		t.Run(include, func(t *testing.T) {
			files := fstest.MapFS{"main.journal": {Data: []byte("include " + include + "\n")}}
			if _, err := ParseFS(files, "main.journal"); err == nil {
				t.Fatal("expected confinement/cycle error")
			}
		})
	}
}

func TestParseFSRootRejectsSymlinkInclude(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.ledger")
	if err := os.WriteFile(outside, []byte("account Assets:Outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.ledger")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.journal"), []byte("include linked.ledger\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := ParseFS(root.FS(), "main.journal"); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}
