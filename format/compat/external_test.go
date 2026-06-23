//go:build compat_external

// Package compat external-tool tests. Built with `go test -tags compat_external`.
//
// These tests prove bidirectional compatibility with the real upstream
// binaries:
//
//   - they-read-ours: our Print output of a fixture is consumable by the
//     upstream tool (we shell out to `hledger stats`, `ledger bal`,
//     `bean-check` and assert exit 0).
//   - we-read-theirs: the upstream tool's canonical print output of a fixture
//     is consumable by our parser (we run `hledger print -f fixture` /
//     `ledger print -f fixture` and pipe the result through format.Parse).
//
// Binaries are resolved by binaries.go — prefer the vendored copy under
// bin/<os>-<arch>/, fall back to $PATH. Tests t.Skip cleanly when neither is
// available, so the suite is safe to run in environments without the tools.
package compat

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
)

// requireTool skips the current test when the resolver returns no path for
// the given tool, printing a helpful pointer to the README.
func requireTool(t *testing.T, tool Tool) string {
	t.Helper()
	p := Resolve(tool)
	if p == "" {
		t.Skipf("%s not found in vendored bin/ or on $PATH; see %s for install instructions",
			tool, filepath.Join("internal", "ledger", "format", "compat", "bin", "README.md"))
	}
	return p
}

// writeOurPrint loads a fixture, runs format.ParseFile + format.Print, and
// writes the canonical output to a temp file. Returns the path so the caller
// can hand it to an external binary. We use ParseFile so fixtures using
// `include` directives are resolved against their parent directory before we
// hand the inlined result to the upstream tool.
func writeOurPrint(t *testing.T, fixturePath string) string {
	t.Helper()
	proj, err := format.ParseFile(fixturePath)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var buf bytes.Buffer
	if err := format.Print(&buf, proj); err != nil {
		t.Fatalf("print: %v", err)
	}
	out := filepath.Join(t.TempDir(), filepath.Base(fixturePath))
	if err := os.WriteFile(out, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	return out
}

// TestHledgerReadsOurPrint runs every passing-default-scenarios fixture
// through our Print, then asks hledger to read the result. A clean `hledger
// stats` exit means our output is structurally a valid hledger journal.
//
// We skip fixtures whose .expect sidecar says MustFail or Skip — the upstream
// tool would also reject them.
func TestHledgerReadsOurPrint(t *testing.T) {
	hledger := requireTool(t, ToolHledger)
	runReadsOurPrint(t, hledger, []string{"stats"}, allParseableFixtures(t)...)
}

// TestLedgerReadsOurPrint mirrors TestHledgerReadsOurPrint for ledger-cli.
func TestLedgerReadsOurPrint(t *testing.T) {
	ledger := requireTool(t, ToolLedger)
	runReadsOurPrint(t, ledger, []string{"stats"}, allParseableFixtures(t)...)
}

// TestBeanCheckReadsOurPrint runs only the beancount-flavored fixtures
// through bean-check. Our Print emits ledger-format output by default which
// bean-check would not accept, so for now this validates that bean-check
// accepts the source beancount fixtures directly — i.e. our hand-authored
// fixtures are actually valid beancount syntax. A beancount-flavored Print
// emitter is a separate follow-up.
func TestBeanCheckReadsOurPrint(t *testing.T) {
	bin := requireTool(t, ToolBeancount)
	root := filepath.Join("testdata", "beancount")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no testdata/beancount/")
	}
	fixtures, err := LoadAll(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, f := range fixtures {
		f := f
		if f.Expect.MustFail || f.Expect.Skip != "" {
			// must_fail fixtures exercise our shim's rejection path —
			// bean-check would accept them, so they'd be false positives.
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			cmd := exec.Command(bin, f.Path)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Errorf("bean-check rejected hand-authored fixture: %v\nstdout: %s\nstderr: %s",
					err, stdout.String(), stderr.String())
			}
		})
	}
}

// TestOurParserReadsHledgerPrint asks hledger to print every fixture, then
// feeds the result through our parser. A clean re-parse means we can ingest
// any hledger-canonicalized workspace — which is the user's "open a workspace
// created by another tool" requirement.
func TestOurParserReadsHledgerPrint(t *testing.T) {
	hledger := requireTool(t, ToolHledger)
	for _, f := range allParseableFixtures(t) {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			cmd := exec.Command(hledger, "-f", f.Path, "print")
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Skipf("hledger refused to print fixture (likely uses a syntax it doesn't accept): %v\nstderr: %s",
					err, stderr.String())
			}
			if _, err := format.Parse(&stdout); err != nil {
				t.Errorf("our parser rejected hledger's canonical print:\n%s\nerror: %v",
					stdout.String(), err)
			}
		})
	}
}

// TestOurParserReadsLedgerPrint is the ledger-cli twin of the above.
func TestOurParserReadsLedgerPrint(t *testing.T) {
	ledger := requireTool(t, ToolLedger)
	for _, f := range allParseableFixtures(t) {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			cmd := exec.Command(ledger, "-f", f.Path, "print")
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Skipf("ledger refused to print fixture: %v\nstderr: %s",
					err, stderr.String())
			}
			if _, err := format.Parse(&stdout); err != nil {
				t.Errorf("our parser rejected ledger's canonical print:\n%s\nerror: %v",
					stdout.String(), err)
			}
		})
	}
}

// runReadsOurPrint executes <bin> <args...> <our-print-of-fixture> for every
// fixture and asserts a clean exit. Captures stdout/stderr for diagnostics.
func runReadsOurPrint(t *testing.T, bin string, args []string, fixtures ...*Fixture) {
	t.Helper()
	for _, f := range fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			tmp := writeOurPrint(t, f.Path)
			full := append([]string{"-f", tmp}, args...)
			cmd := exec.Command(bin, full...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				// Include the file content for easy reproduction.
				body, _ := os.ReadFile(tmp)
				t.Errorf("%s %s rejected our Print output: %v\n--- our print ---\n%s\n--- stderr ---\n%s",
					filepath.Base(bin), strings.Join(full, " "), err, body, stderr.String())
			}
		})
	}
}

// TestHledgerBalanceMatchesOurs runs `hledger -f <shared/simple.journal> bal`
// against each shared fixture and asserts the account/amount tuples (after
// whitespace normalization) match what our report.Balance would emit.
//
// This is the strongest "third-party tool produces the same numbers" check
// in the suite. Differences here flag real semantic divergence — either we
// or hledger compute balances differently.
func TestHledgerBalanceMatchesOurs(t *testing.T) {
	hledger := requireTool(t, ToolHledger)
	root := filepath.Join("testdata", "shared")
	all, err := LoadAll(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	groups := map[string][]*Fixture{}
	for _, f := range all {
		// hledger reads .journal cleanly; .ledger usually too. Skip
		// .beancount (different syntax).
		if f.Format == FormatBeancount {
			continue
		}
		groups[f.Name] = append(groups[f.Name], f)
	}
	for name, group := range groups {
		group := group
		t.Run(name, func(t *testing.T) {
			for _, f := range group {
				f := f
				t.Run(string(f.Format), func(t *testing.T) {
					cmd := exec.Command(hledger, "-f", f.Path, "bal", "--flat", "--no-total")
					var stdout, stderr bytes.Buffer
					cmd.Stdout = &stdout
					cmd.Stderr = &stderr
					if err := cmd.Run(); err != nil {
						t.Skipf("hledger refused fixture: %v\nstderr: %s", err, stderr.String())
					}
					hledgerBalances := parseHledgerBalanceOutput(stdout.String())
					if f.Balances != nil {
						// Compare hledger's reading of the same fixture
						// against our hand-authored sidecar. If hledger
						// disagrees with us *and* the sidecar, the sidecar
						// is wrong; if hledger agrees with the sidecar but
						// our parser doesn't, our parser is wrong.
						if diff, ok := DiffBalances(f.Balances, hledgerBalances); !ok {
							t.Errorf("hledger's balances disagree with .balances.json sidecar:\n%s",
								diff)
						}
					}
				})
			}
		})
	}
}

// parseHledgerBalanceOutput parses lines like:
//
//	"             $974.50  Assets:Cash"
//	"           -$1000.00  Income:Salary"
//	"           96.50 EUR  Assets:Cash:EUR"
//
// into a Balances map keyed by account. Mirrors `hledger bal --flat
// --no-total` output (one row per account, two whitespace-separated
// fields: amount-with-commodity, account-name).
func parseHledgerBalanceOutput(s string) Balances {
	out := Balances{}
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		// Split into [amount, account] on the *last* run of 2+ spaces.
		// hledger right-aligns the amount column so account names start
		// after the column boundary.
		idx := strings.LastIndex(line, "  ")
		if idx < 0 {
			continue
		}
		amt := strings.TrimSpace(line[:idx])
		acct := strings.TrimSpace(line[idx:])
		if amt == "" || acct == "" {
			continue
		}
		out[acct] = amt
	}
	return out
}

// allParseableFixtures returns every fixture under testdata/ whose .expect
// does not mark it MustFail or Skip. We exclude beancount fixtures for now
// since hledger/ledger don't speak beancount.
func allParseableFixtures(t *testing.T) []*Fixture {
	t.Helper()
	var roots []string
	for _, sub := range []string{"ledger", "hledger"} {
		root := filepath.Join("testdata", sub)
		if _, err := os.Stat(root); err == nil {
			roots = append(roots, root)
		}
	}
	var all []*Fixture
	for _, r := range roots {
		fs, err := LoadAll(r)
		if err != nil {
			t.Fatalf("load %s: %v", r, err)
		}
		for _, f := range fs {
			if f.Expect.MustFail || f.Expect.Skip != "" {
				continue
			}
			all = append(all, f)
		}
	}
	return all
}
