package compat

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// Fixture is a single cross-tool compatibility test case. A fixture is one
// source file (.journal | .ledger | .beancount) plus optional sidecar files
// describing expected outcomes:
//
//	<name>.journal           — the input
//	<name>.balances.json     — expected per-account balances after parsing
//	<name>.expect            — high-level pass/fail expectation, parser errors, etc.
//
// The sidecar files are all optional; their absence means "don't check that
// scenario". The harness records which scenarios ran per fixture so the compat
// matrix doc can be regenerated mechanically.
type Fixture struct {
	Path         string   // absolute path to the source file
	Name         string   // file basename without extension
	Format       Format   // detected from extension
	BaseCurrency string   // from sidecar; defaults to "USD"
	Expect       Expect   // pass/fail expectation
	Balances     Balances // expected per-account balances (may be nil)
}

// Format identifies the input syntax. We use it both to dispatch tool-specific
// parsing and to know which upstream binary to run in external tests.
type Format string

const (
	FormatLedger    Format = "ledger"
	FormatHledger   Format = "hledger"
	FormatBeancount Format = "beancount"
)

// Expect describes the expected high-level outcome of parsing a fixture. The
// default (zero value) is "parse must succeed with a balanced book". Use
// ExpectError when the fixture exercises a syntax we deliberately reject (so
// the test passes when our parser surfaces a clear error).
type Expect struct {
	// MustFail is true when Parse is expected to return a non-nil error.
	MustFail bool
	// ErrorContains, when MustFail is true, asserts the error message
	// contains this substring. Empty = no substring check.
	ErrorContains string
	// AllowUnbalanced relaxes the "every entry must balance" check (used by
	// fixtures that exercise inferred amounts or known-unbalanced edge cases).
	AllowUnbalanced bool
	// Skip, when non-empty, causes the harness to t.Skip with this reason
	// (used for known-unsupported features that we want documented in the
	// matrix but not run).
	Skip string
}

// Balances is the parsed contents of a <name>.balances.json sidecar:
//
//	{
//	  "Assets:Cash":     "1000.00 USD",
//	  "Income:Salary":   "-1000.00 USD",
//	  "Expenses:Food":   "5.25 USD"
//	}
//
// The "AMOUNT COMMODITY" string format mirrors how a user would type a
// balance, e.g. "$1000.00" or "1.5 BTC" — but the canonical form expected by
// the parser is "<number> <commodity>".
type Balances map[string]string

// expectFile is the JSON shape of the optional <name>.expect sidecar.
type expectFile struct {
	BaseCurrency    string `json:"base_currency,omitempty"`
	MustFail        bool   `json:"must_fail,omitempty"`
	ErrorContains   string `json:"error_contains,omitempty"`
	AllowUnbalanced bool   `json:"allow_unbalanced,omitempty"`
	Skip            string `json:"skip,omitempty"`
}

// LoadFixture reads a fixture and its sidecars. The fixture path must point
// to a file whose extension identifies the format.
func LoadFixture(path string) (*Fixture, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(abs))
	var fmtKind Format
	switch ext {
	case ".journal":
		fmtKind = FormatHledger
	case ".ledger", ".dat", ".test":
		fmtKind = FormatLedger
	case ".beancount", ".bean":
		fmtKind = FormatBeancount
	default:
		return nil, fmt.Errorf("compat: unknown fixture extension %q", ext)
	}

	base := strings.TrimSuffix(filepath.Base(abs), ext)
	f := &Fixture{
		Path:         abs,
		Name:         base,
		Format:       fmtKind,
		BaseCurrency: "USD",
	}

	// Optional <name>.expect sidecar.
	if data, err := os.ReadFile(strings.TrimSuffix(abs, ext) + ".expect"); err == nil {
		var ef expectFile
		if err := json.Unmarshal(data, &ef); err != nil {
			return nil, fmt.Errorf("compat: parse %s.expect: %w", base, err)
		}
		if ef.BaseCurrency != "" {
			f.BaseCurrency = ef.BaseCurrency
		}
		f.Expect = Expect{
			MustFail:        ef.MustFail,
			ErrorContains:   ef.ErrorContains,
			AllowUnbalanced: ef.AllowUnbalanced,
			Skip:            ef.Skip,
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("compat: read %s.expect: %w", base, err)
	}

	// Optional <name>.balances.json sidecar.
	if data, err := os.ReadFile(strings.TrimSuffix(abs, ext) + ".balances.json"); err == nil {
		var b Balances
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, fmt.Errorf("compat: parse %s.balances.json: %w", base, err)
		}
		f.Balances = b
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("compat: read %s.balances.json: %w", base, err)
	}

	return f, nil
}

// LoadAll discovers fixtures under dir (recursively). Files matching any of
// the recognized extensions become fixtures; sidecars are auto-detected.
func LoadAll(dir string) ([]*Fixture, error) {
	var out []*Fixture
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".journal", ".ledger", ".dat", ".test", ".beancount", ".bean":
		default:
			return nil
		}
		f, ferr := LoadFixture(path)
		if ferr != nil {
			return ferr
		}
		out = append(out, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// RunDefaultScenarios runs the in-process (no external binary) scenarios for
// a fixture, reporting each as a t.Run subtest:
//
//	Parse        — fixture parses with the expected outcome
//	Balanced     — every entry's postings sum to zero in the base currency
//	Balances     — computed per-account balances match the sidecar (if present)
//	Roundtrip    — Parse(Print(Parse(input))) is structurally equal
//
// The external-binary scenarios live in external_test.go (build-tagged).
func RunDefaultScenarios(t *testing.T, f *Fixture) {
	t.Helper()
	if f.Expect.Skip != "" {
		t.Skip(f.Expect.Skip)
	}

	t.Run("Parse", func(t *testing.T) {
		_, perr := format.ParseFile(f.Path)
		if f.Expect.MustFail {
			if perr == nil {
				t.Fatalf("expected parse to fail, got success")
			}
			if f.Expect.ErrorContains != "" && !strings.Contains(perr.Error(), f.Expect.ErrorContains) {
				t.Fatalf("error %q does not contain %q", perr.Error(), f.Expect.ErrorContains)
			}
			return
		}
		if perr != nil {
			t.Fatalf("parse: %v", perr)
		}
	})

	// Everything below requires a successful parse.
	if f.Expect.MustFail {
		return
	}
	proj, err := format.ParseFile(f.Path)
	if err != nil {
		// Parse subtest will have already failed above.
		return
	}
	proj.BaseCurrency = f.BaseCurrency

	t.Run("Balanced", func(t *testing.T) {
		if f.Expect.AllowUnbalanced {
			t.Skip("AllowUnbalanced set on fixture")
		}
		for i, e := range proj.Entries {
			if !e.IsBalanced(f.BaseCurrency) {
				t.Errorf("entry %d (%s) does not balance in %s: %s",
					i, e.Date.Format("2006-01-02"), f.BaseCurrency,
					e.Balance(f.BaseCurrency).FloatString(4))
			}
		}
	})

	if f.Balances != nil {
		t.Run("Balances", func(t *testing.T) {
			got := ComputeBalances(proj)
			diff, ok := DiffBalances(f.Balances, got)
			if !ok {
				t.Errorf("balance mismatch:\n%s", diff)
			}
		})
	}

	t.Run("Roundtrip", func(t *testing.T) {
		var buf strings.Builder
		if err := format.Print(&buf, proj); err != nil {
			t.Fatalf("print: %v", err)
		}
		proj2, err := format.Parse(strings.NewReader(buf.String()))
		if err != nil {
			t.Fatalf("reparse:\n%s\nerror: %v", buf.String(), err)
		}
		proj2.BaseCurrency = f.BaseCurrency
		if err := EqualProjects(proj, proj2); err != nil {
			t.Errorf("roundtrip not structurally equal: %v\n--- printed ---\n%s", err, buf.String())
		}
	})
}

// ComputeBalances returns the per-account closing balance map for a parsed
// project. Multi-commodity balances are summed per commodity; the resulting
// string format mirrors `1000.00 USD` and is exactly what we expect in the
// .balances.json sidecar files.
//
// Accounts with a single commodity are rendered as "<amount> <commodity>".
// Accounts that touch multiple commodities are rendered as a comma-joined
// list sorted by commodity, e.g. "1.00 BTC, -50000.00 USD".
func ComputeBalances(proj *model.Project) Balances {
	type key struct {
		Account   string
		Commodity string
	}
	sums := map[key]*big.Rat{}
	for _, e := range proj.Entries {
		for _, p := range e.Postings {
			if p.Amount.Quantity == nil {
				continue
			}
			k := key{Account: p.Account, Commodity: p.Amount.Commodity}
			if sums[k] == nil {
				sums[k] = new(big.Rat)
			}
			sums[k].Add(sums[k], p.Amount.Quantity)
		}
	}
	// Group per-account, render canonical strings.
	perAccount := map[string][]string{}
	for k, v := range sums {
		if v.Sign() == 0 {
			continue
		}
		perAccount[k.Account] = append(perAccount[k.Account], fmt.Sprintf("%s %s", v.FloatString(2), k.Commodity))
	}
	out := Balances{}
	for acct, parts := range perAccount {
		sort.Strings(parts)
		out[acct] = strings.Join(parts, ", ")
	}
	return out
}

// DiffBalances compares the expected sidecar balances against computed.
// Both maps are normalized (whitespace, sign rendering) before comparison to
// avoid spurious diffs.
func DiffBalances(want, got Balances) (string, bool) {
	allAccounts := map[string]bool{}
	for a := range want {
		allAccounts[a] = true
	}
	for a := range got {
		allAccounts[a] = true
	}
	keys := make([]string, 0, len(allAccounts))
	for a := range allAccounts {
		keys = append(keys, a)
	}
	sort.Strings(keys)

	var diff strings.Builder
	ok := true
	for _, a := range keys {
		w := normalizeBalance(want[a])
		g := normalizeBalance(got[a])
		if w == g {
			continue
		}
		ok = false
		fmt.Fprintf(&diff, "  %s: want %q, got %q\n", a, w, g)
	}
	return diff.String(), ok
}

// normalizeBalance collapses whitespace and strips the literal "$" prefix
// so "$5.00" and "5.00 USD" compare equal.
func normalizeBalance(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "$") {
		s = strings.TrimPrefix(s, "$") + " USD"
	} else if strings.HasPrefix(s, "-$") {
		s = "-" + strings.TrimPrefix(s, "-$") + " USD"
	}
	// Collapse repeated whitespace.
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}

// EqualProjects compares two parsed projects for structural equality after a
// Parse → Print → Parse round-trip. We deliberately ignore:
//
//   - Project.Name and Project.BaseCurrency (set by the caller, not the parser)
//   - Account.Note whitespace (printer may reflow long notes)
//   - Entry.Note formatting (same reason)
//
// We *do* require equal counts, equal account names, equal entry dates, and
// equal posting amounts (down to rational equality).
func EqualProjects(a, b *model.Project) error {
	// Accounts: Print emits them sorted, so compare as a set (by name).
	if len(a.Accounts) != len(b.Accounts) {
		return fmt.Errorf("account count: %d vs %d", len(a.Accounts), len(b.Accounts))
	}
	aNames := make(map[string]bool, len(a.Accounts))
	for _, acct := range a.Accounts {
		aNames[acct.Name] = true
	}
	for _, acct := range b.Accounts {
		if !aNames[acct.Name] {
			return fmt.Errorf("account %q present after round-trip but not before", acct.Name)
		}
		delete(aNames, acct.Name)
	}
	if len(aNames) > 0 {
		missing := make([]string, 0, len(aNames))
		for n := range aNames {
			missing = append(missing, n)
		}
		sort.Strings(missing)
		return fmt.Errorf("accounts dropped after round-trip: %v", missing)
	}
	if len(a.Entries) != len(b.Entries) {
		return fmt.Errorf("entry count: %d vs %d", len(a.Entries), len(b.Entries))
	}
	// Print sorts entries by date — for round-trip equality we apply the
	// same ordering before comparison so source files whose entries aren't
	// already sorted still pass.
	aEntries := append([]model.Entry(nil), a.Entries...)
	bEntries := append([]model.Entry(nil), b.Entries...)
	sortEntries(aEntries)
	sortEntries(bEntries)
	for i := range aEntries {
		ea, eb := aEntries[i], bEntries[i]
		if !ea.Date.Equal(eb.Date) {
			return fmt.Errorf("entry[%d] date: %s vs %s", i, ea.Date, eb.Date)
		}
		if ea.Payee != eb.Payee {
			return fmt.Errorf("entry[%d] payee: %q vs %q", i, ea.Payee, eb.Payee)
		}
		if ea.Code != eb.Code {
			return fmt.Errorf("entry[%d] code: %q vs %q", i, ea.Code, eb.Code)
		}
		if ea.Status != eb.Status {
			return fmt.Errorf("entry[%d] status: %v vs %v", i, ea.Status, eb.Status)
		}
		if len(ea.Postings) != len(eb.Postings) {
			return fmt.Errorf("entry[%d] posting count: %d vs %d", i, len(ea.Postings), len(eb.Postings))
		}
		for j := range ea.Postings {
			pa, pb := ea.Postings[j], eb.Postings[j]
			if pa.Account != pb.Account {
				return fmt.Errorf("entry[%d].posting[%d] account: %q vs %q", i, j, pa.Account, pb.Account)
			}
			if pa.Amount.Commodity != pb.Amount.Commodity {
				return fmt.Errorf("entry[%d].posting[%d] commodity: %q vs %q", i, j, pa.Amount.Commodity, pb.Amount.Commodity)
			}
			if !ratEqual(pa.Amount.Quantity, pb.Amount.Quantity) {
				return fmt.Errorf("entry[%d].posting[%d] quantity: %v vs %v", i, j, pa.Amount.Quantity, pb.Amount.Quantity)
			}
			if (pa.UnitPrice == nil) != (pb.UnitPrice == nil) {
				return fmt.Errorf("entry[%d].posting[%d] unit-price presence: %v vs %v", i, j, pa.UnitPrice != nil, pb.UnitPrice != nil)
			}
			if pa.UnitPrice != nil {
				if pa.UnitPrice.Commodity != pb.UnitPrice.Commodity {
					return fmt.Errorf("entry[%d].posting[%d] unit-price commodity: %q vs %q", i, j, pa.UnitPrice.Commodity, pb.UnitPrice.Commodity)
				}
				if !ratEqual(pa.UnitPrice.Quantity, pb.UnitPrice.Quantity) {
					return fmt.Errorf("entry[%d].posting[%d] unit-price quantity: %v vs %v", i, j, pa.UnitPrice.Quantity, pb.UnitPrice.Quantity)
				}
			}
		}
	}
	return nil
}

// sortEntries provides a deterministic post-Print order: by date, then by
// payee. Mirrors what format.Print does internally (sort.SliceStable by
// date), with payee as a tiebreaker so same-date entries always pair up.
func sortEntries(entries []model.Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].Date.Equal(entries[j].Date) {
			return entries[i].Date.Before(entries[j].Date)
		}
		return entries[i].Payee < entries[j].Payee
	})
}

func ratEqual(a, b *big.Rat) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Cmp(b) == 0
}
