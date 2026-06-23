package compat

import (
	"path/filepath"
	"testing"
)

// TestParseBeancount drives every fixture under testdata/beancount/ through
// the default scenario set. These are hand-authored from
// beancount.github.io/docs/ — beancount itself is GPL-2, so no upstream
// source is vendored. See cli/THIRD_PARTY_LICENSES.md.
//
// The shim deliberately maps a subset of beancount's grammar onto our
// existing model:
//
//   - open / close       -> account declarations
//   - commodity          -> commodity declarations
//   - price              -> P directive
//   - * / ! transactions -> entries (narration => Payee)
//
// Unsupported directives (balance, pad, note, document, query, custom,
// event, option, plugin, pushtag, poptag, include) and cost-basis
// annotations are documented in compat-matrix.md and surface as
// ErrUnsupportedDirective with line number, so fixtures testing those can
// opt into must_fail behavior.
func TestParseBeancount(t *testing.T) {
	fixtures, err := LoadAll(filepath.Join("testdata", "beancount"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no beancount fixtures found")
	}
	for _, f := range fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			RunDefaultScenarios(t, f)
		})
	}
}
