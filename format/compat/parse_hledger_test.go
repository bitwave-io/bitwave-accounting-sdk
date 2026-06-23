package compat

import (
	"path/filepath"
	"testing"
)

// TestParseHledger drives every fixture under testdata/hledger/ through the
// default scenario set. These fixtures are hand-authored from
// hledger.org/journal.html and hledger.org/SPEC-journal.html — no upstream
// hledger source is vendored (hledger is GPL-3, see THIRD_PARTY_LICENSES.md).
func TestParseHledger(t *testing.T) {
	fixtures, err := LoadAll(filepath.Join("testdata", "hledger"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no hledger fixtures found")
	}
	for _, f := range fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			RunDefaultScenarios(t, f)
		})
	}
}
