package compat

import (
	"path/filepath"
	"testing"
)

// TestParseLedger drives every fixture under testdata/ledger/ through the
// default scenario set: Parse, Balanced, Balances, Roundtrip. Each fixture
// becomes its own t.Run, and each scenario inside that becomes a sub-Run, so
// failures point precisely at "which fixture, which scenario".
func TestParseLedger(t *testing.T) {
	fixtures, err := LoadAll(filepath.Join("testdata", "ledger"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no ledger fixtures found")
	}
	for _, f := range fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			RunDefaultScenarios(t, f)
		})
	}
}
