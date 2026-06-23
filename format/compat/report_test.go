package compat

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-accounting-sdk/report"
)

var updateReportGoldens = flag.Bool("update-report-goldens", false,
	"rewrite testdata/shared/*.bal.golden / *.reg.golden / *.print.golden files")

// TestReports_SharedFixtures runs report.Balance / report.Register /
// report.Print against each shared/*.{ledger,journal,beancount} group and
// asserts:
//
//  1. All three formats produce identical reports (cross-syntax equivalence).
//  2. The reports match committed golden files (regression guard).
//
// Run `go test ./internal/ledger/format/compat -update-report-goldens` to
// regenerate the goldens after intentional output changes.
func TestReports_SharedFixtures(t *testing.T) {
	root := filepath.Join("testdata", "shared")
	all, err := LoadAll(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	groups := map[string][]*Fixture{}
	for _, f := range all {
		groups[f.Name] = append(groups[f.Name], f)
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		group := groups[name]
		if len(group) < 2 {
			continue
		}
		t.Run(name, func(t *testing.T) {
			perFormat := map[Format]reportSet{}
			for _, f := range group {
				proj, err := format.ParseFile(f.Path)
				if err != nil {
					t.Fatalf("%s parse: %v", f.Path, err)
				}
				proj.BaseCurrency = f.BaseCurrency
				perFormat[f.Format] = renderReports(t, proj)
			}

			// Pick the first format (alphabetically) as canonical and use
			// it for the golden comparison.
			formats := make([]Format, 0, len(perFormat))
			for k := range perFormat {
				formats = append(formats, k)
			}
			sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
			canonical := perFormat[formats[0]]

			for _, kind := range []struct {
				ext, content string
			}{
				{".bal.golden", canonical.bal},
				{".reg.golden", canonical.reg},
				{".print.golden", canonical.prt},
			} {
				goldenPath := filepath.Join(root, name+kind.ext)
				if *updateReportGoldens {
					if err := os.WriteFile(goldenPath, []byte(kind.content), 0644); err != nil {
						t.Fatalf("write golden: %v", err)
					}
					continue
				}
				want, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatalf("missing golden %s — run with -update-report-goldens to create: %v",
						goldenPath, err)
				}
				if string(want) != kind.content {
					t.Errorf("%s report differs from golden:\n--- want ---\n%s\n--- got ---\n%s",
						kind.ext, string(want), kind.content)
				}
			}

			// Every other format must produce identical reports.
			for _, other := range formats[1:] {
				got := perFormat[other]
				if got.bal != canonical.bal {
					t.Errorf("%s vs %s: Balance differs:\n--- %s ---\n%s\n--- %s ---\n%s",
						formats[0], other, formats[0], canonical.bal, other, got.bal)
				}
				if got.reg != canonical.reg {
					t.Errorf("%s vs %s: Register differs:\n--- %s ---\n%s\n--- %s ---\n%s",
						formats[0], other, formats[0], canonical.reg, other, got.reg)
				}
				if got.prt != canonical.prt {
					t.Errorf("%s vs %s: Print differs:\n--- %s ---\n%s\n--- %s ---\n%s",
						formats[0], other, formats[0], canonical.prt, other, got.prt)
				}
			}
		})
	}
}

type reportSet struct {
	bal string
	reg string
	prt string
}

func renderReports(t *testing.T, proj *model.Project) reportSet {
	t.Helper()
	var bal, reg, prt bytes.Buffer
	if err := report.Balance(&bal, proj, report.Filter{}); err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if err := report.Register(&reg, proj, report.Filter{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := report.Print(&prt, proj, report.Filter{}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	return reportSet{bal: bal.String(), reg: reg.String(), prt: prt.String()}
}
