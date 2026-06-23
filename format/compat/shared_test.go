package compat

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
)

// TestSharedCrossFormat walks testdata/shared/ looking for groups of files
// that share a basename across the three supported extensions
// (`.ledger`, `.journal`, `.beancount`). For each group it:
//
//  1. Parses each format via format.ParseFile.
//  2. Computes the per-account balance sheet from each parse.
//  3. Asserts all three balance sheets are identical to each other and to
//     the shared `<basename>.balances.json` sidecar.
//
// This is the strongest form of cross-tool compatibility assertion: the
// same logical book authored in three syntaxes must produce identical
// numbers when read through our parser.
func TestSharedCrossFormat(t *testing.T) {
	root := filepath.Join("testdata", "shared")
	all, err := LoadAll(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no shared fixtures found")
	}

	// Group by basename (the part before the extension).
	groups := map[string][]*Fixture{}
	for _, f := range all {
		groups[f.Name] = append(groups[f.Name], f)
	}

	// Order group names for deterministic test output.
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		group := groups[name]
		if len(group) < 2 {
			// A book must exist in at least 2 formats to be worth checking.
			continue
		}
		t.Run(name, func(t *testing.T) {
			// We expect the same balances sidecar across the group; pick
			// the first non-empty one.
			var want Balances
			for _, f := range group {
				if f.Balances != nil {
					want = f.Balances
					break
				}
			}
			if want == nil {
				t.Fatalf("shared fixture %s: no .balances.json sidecar", name)
			}

			// Parse each format and assert balances agree with `want`.
			perFormat := map[Format]Balances{}
			for _, f := range group {
				proj, err := format.ParseFile(f.Path)
				if err != nil {
					t.Errorf("%s (%s): parse: %v", f.Format, filepath.Base(f.Path), err)
					continue
				}
				proj.BaseCurrency = f.BaseCurrency
				got := ComputeBalances(proj)
				if diff, ok := DiffBalances(want, got); !ok {
					t.Errorf("%s (%s) balances disagree with sidecar:\n%s",
						f.Format, filepath.Base(f.Path), diff)
				}
				perFormat[f.Format] = got
			}

			// Pairwise: every format must agree with every other.
			// (Diff against sidecar above also catches this, but a direct
			// inter-format compare gives clearer error messages when one
			// fixture has a typo and the sidecar happens to match the
			// majority.)
			formats := make([]Format, 0, len(perFormat))
			for k := range perFormat {
				formats = append(formats, k)
			}
			sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
			for i := 0; i < len(formats); i++ {
				for j := i + 1; j < len(formats); j++ {
					a, b := formats[i], formats[j]
					if diff, ok := DiffBalances(perFormat[a], perFormat[b]); !ok {
						t.Errorf("%s vs %s balances differ:\n%s",
							a, b, indent(diff, "    "))
					}
				}
			}
		})
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}
