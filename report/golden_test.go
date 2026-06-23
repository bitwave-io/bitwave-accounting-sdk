package report_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/report"
)

// updateGolden regenerates testdata/*.golden files from current report output.
// Run: go test ./internal/ledger/report -update
var updateGolden = flag.Bool("update", false, "rewrite golden files")

func TestGolden_Reports(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*bytes.Buffer, *testing.T)
	}{
		{"balance.golden", func(b *bytes.Buffer, t *testing.T) {
			if err := report.Balance(b, makeProject(t), report.Filter{}); err != nil {
				t.Fatal(err)
			}
		}},
		{"register_cash.golden", func(b *bytes.Buffer, t *testing.T) {
			if err := report.Register(b, makeProject(t), report.Filter{AccountMatch: "Assets:Cash"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"equity.golden", func(b *bytes.Buffer, t *testing.T) {
			asOf, err := time.Parse("2006-01-02", "2024-02-01")
			if err != nil {
				t.Fatal(err)
			}
			if err := report.Equity(b, makeProject(t), report.Filter{To: asOf}); err != nil {
				t.Fatal(err)
			}
		}},
		{"csv.golden", func(b *bytes.Buffer, t *testing.T) {
			if err := report.CSVPrint(b, makeProject(t), report.Filter{}); err != nil {
				t.Fatal(err)
			}
		}},
		{"stats.golden", func(b *bytes.Buffer, t *testing.T) {
			if err := report.Stats(b, makeProject(t)); err != nil {
				t.Fatal(err)
			}
		}},
		{"print.golden", func(b *bytes.Buffer, t *testing.T) {
			if err := report.Print(b, makeProject(t), report.Filter{}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got bytes.Buffer
			c.fn(&got, t)
			path := filepath.Join("testdata", c.name)
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got.Bytes(), 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden file %s — run with -update to create: %v", path, err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("output diff for %s\n--- got ---\n%s\n--- want ---\n%s", c.name, got.String(), string(want))
			}
		})
	}
}
