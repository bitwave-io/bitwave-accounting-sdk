package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-accounting-sdk/report"
)

// --- Balance: structural assertions on math ---

func TestBalance_AccountTotals(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Balance(&buf, p, report.Filter{}); err != nil {
		t.Fatalf("Balance: %v", err)
	}
	out := buf.String()

	// Expected USD-denominated totals (BTC posting kept in BTC, not converted):
	// Assets:Cash       = 1000 - 5.25 - 50000 - 75 = -49080.25
	// Assets:Crypto:BTC = 1 BTC
	// Expenses:Food     = 5.25 + 75 = 80.25
	// Income:Salary     = -1000
	wantLines := []string{
		"-$49080.25",
		"1.00 BTC",
		"$80.25",
		"-$1000.00",
		"Assets:Cash",
		"Assets:Crypto:BTC",
		"Expenses:Food",
		"Income:Salary",
	}
	for _, w := range wantLines {
		if !strings.Contains(out, w) {
			t.Errorf("balance output missing %q\n--- output ---\n%s", w, out)
		}
	}
}

func TestBalance_DateFilter(t *testing.T) {
	p := makeProject(t)
	from, _ := time.Parse("2006-01-02", "2024-02-01")
	to, _ := time.Parse("2006-01-02", "2024-02-28")
	var buf bytes.Buffer
	if err := report.Balance(&buf, p, report.Filter{From: from, To: to}); err != nil {
		t.Fatalf("Balance: %v", err)
	}
	out := buf.String()
	// Only the Feb 1 entry — Grocer $75. Cash -75, Food +75.
	if !strings.Contains(out, "$75.00") {
		t.Errorf("expected $75.00 in filtered output\n%s", out)
	}
	// Should NOT contain BTC or salary
	if strings.Contains(out, "BTC") || strings.Contains(out, "Income:Salary") {
		t.Errorf("date-filtered output should not contain pre-Feb data\n%s", out)
	}
}

func TestBalance_AccountFilter(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Balance(&buf, p, report.Filter{AccountMatch: "food"}); err != nil {
		t.Fatalf("Balance: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Expenses:Food") {
		t.Errorf("expected Expenses:Food in output\n%s", out)
	}
	if strings.Contains(out, "Assets:Cash") {
		t.Errorf("account filter leaked Assets:Cash\n%s", out)
	}
}

func TestBalance_ClearedOnly(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Balance(&buf, p, report.Filter{ClearedOnly: true}); err != nil {
		t.Fatalf("Balance: %v", err)
	}
	out := buf.String()
	// Only e1 ($1000 salary) and e4 ($75 grocer) are cleared.
	// Cash: 1000 - 75 = 925
	// Food: 75
	// Salary: -1000
	if !strings.Contains(out, "$925.00") {
		t.Errorf("cleared-only Cash expected $925.00\n%s", out)
	}
	if strings.Contains(out, "BTC") {
		t.Errorf("BTC entry is pending — should not appear in cleared-only\n%s", out)
	}
}

func TestBalance_EmptyProject(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Balance(&buf, &model.Project{}, report.Filter{}); err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty project should produce no output, got %q", buf.String())
	}
}

// --- Register ---

func TestRegister_RunningBalance(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Register(&buf, p, report.Filter{AccountMatch: "Assets:Cash"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	lines := splitNonEmpty(buf.String())
	if len(lines) != 4 {
		t.Fatalf("expected 4 cash postings, got %d:\n%s", len(lines), buf.String())
	}
	// Running balance after each entry (chronological order):
	//   +1000      -> 1000
	//   -5.25      -> 994.75
	//   -50000     -> -49005.25
	//   -75        -> -49080.25
	wantBalances := []string{"$1000.00", "$994.75", "-$49005.25", "-$49080.25"}
	for i, w := range wantBalances {
		if !strings.Contains(lines[i], w) {
			t.Errorf("line %d missing running balance %q: %s", i, w, lines[i])
		}
	}
}

func TestRegister_DateFilter(t *testing.T) {
	p := makeProject(t)
	from, _ := time.Parse("2006-01-02", "2024-02-01")
	var buf bytes.Buffer
	if err := report.Register(&buf, p, report.Filter{From: from}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Grocer") {
		t.Errorf("expected Grocer entry in Feb-only output\n%s", out)
	}
	if strings.Contains(out, "Coffee") || strings.Contains(out, "Acme") {
		t.Errorf("Jan entries leaked into Feb-only output\n%s", out)
	}
}

// --- Accounts / Payees / Commodities ---

func TestAccounts(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Accounts(&buf, p, report.Filter{}); err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	lines := splitNonEmpty(buf.String())
	want := []string{"Assets:Cash", "Assets:Crypto:BTC", "Expenses:Food", "Income:Salary"}
	if len(lines) != len(want) {
		t.Fatalf("accounts: got %d, want %d:\n%s", len(lines), len(want), buf.String())
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("[%d] = %q, want %q", i, lines[i], w)
		}
	}
}

func TestAccounts_Filter(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Accounts(&buf, p, report.Filter{AccountMatch: "expenses"}); err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "Expenses:Food" {
		t.Errorf("got %q, want Expenses:Food", got)
	}
}

func TestPayees_DistinctSorted(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Payees(&buf, p); err != nil {
		t.Fatalf("Payees: %v", err)
	}
	lines := splitNonEmpty(buf.String())
	want := []string{"Acme Corp", "Bought BTC", "Coffee shop", "Grocer"}
	if len(lines) != len(want) {
		t.Fatalf("got %d payees, want %d", len(lines), len(want))
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("[%d] = %q, want %q", i, lines[i], w)
		}
	}
}

func TestCommodities(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Commodities(&buf, p); err != nil {
		t.Fatalf("Commodities: %v", err)
	}
	got := splitNonEmpty(buf.String())
	want := []string{"BTC", "USD"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// --- Cleared / Prices ---

func TestCleared_OnlyClearedEntries(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Cleared(&buf, p); err != nil {
		t.Fatalf("Cleared: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Acme Corp") || !strings.Contains(out, "Grocer") {
		t.Errorf("expected both cleared payees in output\n%s", out)
	}
	if strings.Contains(out, "Coffee shop") || strings.Contains(out, "Bought BTC") {
		t.Errorf("uncleared/pending entries leaked into cleared report\n%s", out)
	}
	// Must remain valid ledger format
	if _, err := format.Parse(strings.NewReader(out)); err != nil {
		t.Errorf("cleared output is not valid ledger format: %v\n%s", err, out)
	}
}

func TestPrices(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Prices(&buf, p); err != nil {
		t.Fatalf("Prices: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "2024-01-15") || !strings.Contains(out, "BTC") || !strings.Contains(out, "$50000.00") {
		t.Errorf("prices output missing fields\n%s", out)
	}
}

// --- CSV ---

func TestCSV_HeaderAndRowCount(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.CSVPrint(&buf, p, report.Filter{}); err != nil {
		t.Fatalf("CSVPrint: %v", err)
	}
	lines := splitNonEmpty(buf.String())
	if len(lines) != 9 {
		// 1 header + 8 postings (4 entries × 2 each)
		t.Fatalf("expected 9 lines, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "date,payee,status,account,amount") {
		t.Errorf("header malformed: %q", lines[0])
	}
}

func TestCSV_BTCRowHasUnitPrice(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.CSVPrint(&buf, p, report.Filter{AccountMatch: "BTC"}); err != nil {
		t.Fatalf("CSVPrint: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "1.00000000,BTC,50000.00000000,USD") {
		t.Errorf("CSV row missing BTC + unit price columns\n%s", out)
	}
}

func TestCSV_ClearedFilter(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.CSVPrint(&buf, p, report.Filter{ClearedOnly: true}); err != nil {
		t.Fatalf("CSVPrint: %v", err)
	}
	lines := splitNonEmpty(buf.String())
	// 1 header + 4 postings (2 entries × 2)
	if len(lines) != 5 {
		t.Errorf("expected 5 lines (1 header + 4 cleared postings), got %d:\n%s", len(lines), buf.String())
	}
}

// --- Equity ---

func TestEquity_HasSnapshotHeaderAndAccounts(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Equity(&buf, p, report.Filter{}); err != nil {
		t.Fatalf("Equity: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Opening Balances") {
		t.Errorf("equity should include 'Opening Balances' header\n%s", out)
	}
	if !strings.Contains(out, "Equity:Opening-Balances") {
		t.Errorf("equity should reference balancing account\n%s", out)
	}
	// Balances should reflect non-zero accounts.
	for _, a := range []string{"Assets:Cash", "Expenses:Food", "Income:Salary"} {
		if !strings.Contains(out, a) {
			t.Errorf("equity missing account %s\n%s", a, out)
		}
	}
}

// --- Stats ---

func TestStats_Counts(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Stats(&buf, p); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	out := buf.String()
	checks := []string{
		"Project:       fixture",
		"Base currency: USD",
		"Accounts:      4 declared",
		"Entries:       4 (4 balanced, 2 cleared, 1 pending)",
		"Postings:      8",
		"Commodities:   2",
		"Prices:        1",
		"Date range:    2024-01-10 -> 2024-02-01",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("stats output missing %q\n--- output ---\n%s", c, out)
		}
	}
}

func TestStats_EmptyProject(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Stats(&buf, &model.Project{Name: "empty", BaseCurrency: "USD"}); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	out := buf.String()
	// No "Date range:" line when there are no entries.
	if strings.Contains(out, "Date range:") {
		t.Errorf("empty project should omit Date range line:\n%s", out)
	}
	if !strings.Contains(out, "Entries:       0") {
		t.Errorf("expected zero-entry summary:\n%s", out)
	}
}

// --- Print (re-emit canonical format) ---

func TestPrint_RoundTripsThroughParser(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Print(&buf, p, report.Filter{}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	reparsed, err := format.Parse(&buf)
	if err != nil {
		t.Fatalf("printed output should re-parse: %v", err)
	}
	if len(reparsed.Entries) != 4 {
		t.Errorf("re-parsed entries: got %d, want 4", len(reparsed.Entries))
	}
	for i, e := range reparsed.Entries {
		if !e.IsBalanced("USD") {
			t.Errorf("entry %d should still balance after Print+Parse; sum=%s", i, e.Balance("USD").FloatString(2))
		}
	}
}

func TestPrint_AccountFilter_KeepsOnlyMatchingPostings(t *testing.T) {
	p := makeProject(t)
	var buf bytes.Buffer
	if err := report.Print(&buf, p, report.Filter{AccountMatch: "Crypto"}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Assets:Crypto:BTC") {
		t.Errorf("filtered print missing BTC posting:\n%s", out)
	}
	// Non-matching entries (no BTC posting) should be dropped entirely.
	if strings.Contains(out, "Coffee shop") || strings.Contains(out, "Acme Corp") {
		t.Errorf("filtered print leaked entries that didn't match account:\n%s", out)
	}
}

// --- helpers ---

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
