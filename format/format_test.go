package format

import (
	"bytes"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

func TestParseAndPrintRoundTrip(t *testing.T) {
	input := `; acme-2024 — USD ledger

account Assets:Cash
account Assets:Crypto:BTC
account Expenses:Food

P 2024-01-15 BTC $50000.00

2024-01-15 * Coffee shop
    Expenses:Food                           $5.00
    Assets:Cash                            -$5.00

2024-01-16 ! Bought BTC                                     ; lot opened
    Assets:Crypto:BTC                       1.00 BTC @ $50000.00
    Assets:Cash                        -$50000.00
`
	p, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p.Name = "acme-2024"
	p.BaseCurrency = "USD"

	if got, want := len(p.Accounts), 3; got != want {
		t.Errorf("accounts: got %d, want %d", got, want)
	}
	if got, want := len(p.Entries), 2; got != want {
		t.Errorf("entries: got %d, want %d", got, want)
	}
	if got, want := len(p.Prices), 1; got != want {
		t.Errorf("prices: got %d, want %d", got, want)
	}

	if !p.Entries[0].IsBalanced("USD") {
		t.Errorf("first entry should balance, got %s", p.Entries[0].Balance("USD").FloatString(2))
	}
	if !p.Entries[1].IsBalanced("USD") {
		t.Errorf("second entry should balance with @ price, got %s", p.Entries[1].Balance("USD").FloatString(2))
	}

	var out bytes.Buffer
	if err := Print(&out, p); err != nil {
		t.Fatalf("print: %v", err)
	}
	// Re-parse the printed output — must yield equivalent structure.
	p2, err := Parse(&out)
	if err != nil {
		t.Fatalf("reparse: %v\noutput was:\n%s", err, out.String())
	}
	if got, want := len(p2.Entries), len(p.Entries); got != want {
		t.Errorf("reparsed entries: got %d, want %d", got, want)
	}
	if got := p2.Entries[1].Postings[0].Amount.Commodity; got != "BTC" {
		t.Errorf("reparsed BTC posting commodity: got %q", got)
	}
}

func TestParseAmountForms(t *testing.T) {
	cases := []struct {
		in        string
		commodity string
		want      string // FloatString(2)
	}{
		{"$5.00", "USD", "5.00"},
		{"-$5.00", "USD", "-5.00"},
		{"5.00 USD", "USD", "5.00"},
		{"-5.00 USD", "USD", "-5.00"},
		{"1 BTC", "BTC", "1.00"},
		{"50,000.00 USD", "USD", "50000.00"},
		{"+5 USD", "USD", "5.00"},
	}
	for _, c := range cases {
		got, err := parseAmount(c.in)
		if err != nil {
			t.Errorf("%q: parse failed: %v", c.in, err)
			continue
		}
		if got.Commodity != c.commodity {
			t.Errorf("%q: commodity = %q, want %q", c.in, got.Commodity, c.commodity)
		}
		if g := got.Quantity.FloatString(2); g != c.want {
			t.Errorf("%q: quantity = %s, want %s", c.in, g, c.want)
		}
	}
}

func TestParse_TransactionCode(t *testing.T) {
	// `(CODE)` after the status flag — check number, invoice ref, etc.
	// Must survive round-trip.
	src := `account Assets:Checking
account Expenses:Utilities

2024-04-01 * (CHK#1042) PG&E
    Expenses:Utilities       $87.21
    Assets:Checking         -$87.21

2024-04-15 ! (INV-2024-006) Acme Consulting
    Expenses:Utilities      $100.00
    Assets:Checking        -$100.00
`
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Entries) != 2 {
		t.Fatalf("entries: got %d, want 2", len(p.Entries))
	}
	if got, want := p.Entries[0].Code, "CHK#1042"; got != want {
		t.Errorf("entry[0].Code = %q, want %q", got, want)
	}
	if got, want := p.Entries[0].Payee, "PG&E"; got != want {
		t.Errorf("entry[0].Payee = %q, want %q", got, want)
	}
	if got, want := p.Entries[1].Code, "INV-2024-006"; got != want {
		t.Errorf("entry[1].Code = %q, want %q", got, want)
	}

	// Round-trip: Print → Parse must preserve the code.
	var buf bytes.Buffer
	if err := Print(&buf, p); err != nil {
		t.Fatalf("print: %v", err)
	}
	p2, err := Parse(&buf)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if p2.Entries[0].Code != "CHK#1042" {
		t.Errorf("round-trip dropped code: got %q", p2.Entries[0].Code)
	}
}

func TestParse_IncludeRejectedWithoutFilesystem(t *testing.T) {
	// Plain Parse cannot resolve includes — there's no directory context.
	// We surface a clear error so callers know to switch to ParseFile.
	src := "include child.journal\n"
	_, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatal("Parse should reject `include` without filesystem context")
	}
	if !strings.Contains(err.Error(), "ParseFile") {
		t.Errorf("error %q should point user at ParseFile", err.Error())
	}
}

func TestParseFile_IncludeBasic(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.journal")
	if err := os.WriteFile(child, []byte(`account Assets:Cash
account Income:Salary

2024-01-15 * Acme
    Assets:Cash      $100.00
    Income:Salary   -$100.00
`), 0644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "parent.journal")
	if err := os.WriteFile(parent, []byte(`; parent with one include
include child.journal

2024-02-01 * Coffee shop
    Expenses:Food    $5.00
    Assets:Cash     -$5.00
`), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := ParseFile(parent)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got, want := len(p.Entries), 2; got != want {
		t.Errorf("entries: got %d, want %d (%v)", got, want, p.Entries)
	}
	if got, want := len(p.Accounts), 2; got != want {
		t.Errorf("accounts: got %d, want %d (%v)", got, want, p.Accounts)
	}
}

func TestParseFile_IncludeCycleDetected(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.journal")
	b := filepath.Join(dir, "b.journal")
	if err := os.WriteFile(a, []byte("include b.journal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("include a.journal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseFile(a)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q should mention cycle", err.Error())
	}
}

func TestParseFile_IncludeBangFormSupported(t *testing.T) {
	// ledger-cli historically used `!include` rather than bare `include`.
	dir := t.TempDir()
	child := filepath.Join(dir, "c.journal")
	if err := os.WriteFile(child, []byte("account Assets:Cash\n"), 0644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "p.journal")
	if err := os.WriteFile(parent, []byte("!include c.journal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := ParseFile(parent)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(p.Accounts) != 1 {
		t.Errorf("want 1 account, got %d", len(p.Accounts))
	}
}

func TestPrint_InexactUnitPriceEmitsTotal(t *testing.T) {
	// 12 EUR @@ 10 GBP gives a unit price of 5/6 GBP/EUR, which has no
	// finite decimal expansion. To make Parse(Print(x)) round-trip
	// exactly, the printer must emit `@@ 10.00 GBP` rather than rounding
	// to `@ 0.83333333 GBP`.
	src := `account Assets:Cash
account Expenses:Phone

2024-01-10 * Phone bill
    Expenses:Phone        12.00 EUR @@ 10.00 GBP
    Assets:Cash          -10.00 GBP
`
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := Print(&buf, p); err != nil {
		t.Fatalf("print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "@@") {
		t.Errorf("expected @@ total-price form in output (price 5/6 has no finite decimal):\n%s", out)
	}
	// Re-parse must yield identical unit-price quantity.
	p2, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	got := p2.Entries[0].Postings[0].UnitPrice.Quantity
	want := p.Entries[0].Postings[0].UnitPrice.Quantity
	if got.Cmp(want) != 0 {
		t.Errorf("unit price drifted: %v -> %v", want, got)
	}
}

func TestParse_TotalPriceAtAt(t *testing.T) {
	// "2 BTC @@ $100000" must yield a unit price of $50000, not $100000.
	// Pre-fix this would balance against $100000 cash credit at a
	// "$100000 per BTC" implied rate — silently wrong by a factor of 2x.
	src := `account Assets:Crypto:BTC
account Assets:Cash

2024-01-15 * Bought BTC
    Assets:Crypto:BTC          2 BTC @@ $100000.00
    Assets:Cash             -$100000.00
`
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Entries) != 1 || len(p.Entries[0].Postings) != 2 {
		t.Fatalf("unexpected shape: %+v", p.Entries)
	}
	post := p.Entries[0].Postings[0]
	if post.UnitPrice == nil {
		t.Fatal("UnitPrice not set")
	}
	if got := post.UnitPrice.Quantity.FloatString(2); got != "50000.00" {
		t.Errorf("@@ total-price -> unit price: got %s, want 50000.00", got)
	}
	if !p.Entries[0].IsBalanced("USD") {
		t.Errorf("entry should balance with @@: got %s",
			p.Entries[0].Balance("USD").FloatString(4))
	}
}

func TestParse_HashAndStarComments(t *testing.T) {
	// hledger SPEC: column-0 `#` and `*` are comments. Inline `;` is the
	// canonical ledger comment. Account-name `#` mid-line is *not* a
	// comment.
	src := `# hledger-style file comment
* org-mode-style header comment, also a file comment
; conventional comment

account Assets:Cash
account Expenses:Travel

2024-03-01 * lunch     ; this ; is allowed
    Expenses:Travel    $10.00
    Assets:Cash       -$10.00
`
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Accounts) != 2 {
		t.Errorf("accounts: got %d, want 2 (%v)", len(p.Accounts), p.Accounts)
	}
	if len(p.Entries) != 1 {
		t.Errorf("entries: got %d, want 1", len(p.Entries))
	}
	if len(p.Entries) > 0 && p.Entries[0].Status != model.StatusCleared {
		t.Errorf("status: got %v, want cleared", p.Entries[0].Status)
	}
}

func TestParseFlexibleDate(t *testing.T) {
	cases := []struct {
		in   string
		want string // canonical YYYY-MM-DD
		fail bool
	}{
		{in: "2024-01-15", want: "2024-01-15"},
		{in: "2024/01/15", want: "2024-01-15"},
		{in: "2024.01.15", want: "2024-01-15"},
		{in: "2024-1-15", fail: true},  // leading-zero required (hledger SPEC allows but we don't yet)
		{in: "2024/01-15", fail: true}, // mixed separators
		{in: "24-01-15", fail: true},   // 2-digit year
		{in: "not a date", fail: true},
	}
	for _, c := range cases {
		got, err := parseFlexibleDate(c.in)
		if c.fail {
			if err == nil {
				t.Errorf("%q: expected error, got %s", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if g := got.Format(dateLayout); g != c.want {
			t.Errorf("%q: got %s, want %s", c.in, g, c.want)
		}
	}
}

func TestParse_AccountTags_StructuredOnly(t *testing.T) {
	src := "account Assets:Crypto:base:treasury    ; wallet:wlt_ABC address:0x1234 network:base\n"
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Accounts) != 1 {
		t.Fatalf("want 1 account, got %d", len(p.Accounts))
	}
	a := p.Accounts[0]
	want := map[string]string{
		"wallet":  "wlt_ABC",
		"address": "0x1234",
		"network": "base",
	}
	for k, v := range want {
		if got := a.Tags[k]; got != v {
			t.Fatalf("tag %s = %q, want %q", k, got, v)
		}
	}
	if a.Note != "" {
		t.Fatalf("note should be empty; got %q", a.Note)
	}
}

func TestParse_AccountTags_MixedProse(t *testing.T) {
	src := "account Assets:Bank:Chase    ; primary checking wallet:wlt_X some prose\n"
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a := p.Accounts[0]
	if a.Tags["wallet"] != "wlt_X" {
		t.Fatalf("wallet tag = %q", a.Tags["wallet"])
	}
	if !strings.Contains(a.Note, "primary") || !strings.Contains(a.Note, "checking") || !strings.Contains(a.Note, "prose") {
		t.Fatalf("note dropped prose: %q", a.Note)
	}
	if strings.Contains(a.Note, "wallet:") {
		t.Fatalf("note should not contain structured token: %q", a.Note)
	}
}

func TestPrint_AccountTags_StableOrder(t *testing.T) {
	p := &model.Project{
		Accounts: []model.Account{{
			Name: "Assets:Crypto:base:treasury",
			Type: model.AccountAsset,
			Tags: map[string]string{
				"zebra":   "z",
				"address": "0xabc",
				"network": "base",
				"wallet":  "wlt_1",
			},
		}},
	}
	var buf bytes.Buffer
	if err := Print(&buf, p); err != nil {
		t.Fatalf("print: %v", err)
	}
	out := buf.String()
	wantOrder := []string{"wallet:wlt_1", "address:0xabc", "network:base", "zebra:z"}
	prev := -1
	for _, tok := range wantOrder {
		idx := strings.Index(out, tok)
		if idx < 0 {
			t.Fatalf("missing %q in:\n%s", tok, out)
		}
		if idx <= prev {
			t.Fatalf("token %q out of order in:\n%s", tok, out)
		}
		prev = idx
	}
}

func TestPrint_AccountTagsAndNote(t *testing.T) {
	p := &model.Project{
		Accounts: []model.Account{{
			Name: "Assets:Crypto:base:treasury",
			Tags: map[string]string{"wallet": "wlt_1", "network": "base"},
			Note: "free form prose",
		}},
	}
	var buf bytes.Buffer
	if err := Print(&buf, p); err != nil {
		t.Fatalf("print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "wallet:wlt_1") || !strings.Contains(out, "network:base") {
		t.Fatalf("missing tags: %s", out)
	}
	if !strings.Contains(out, "free form prose") {
		t.Fatalf("missing prose: %s", out)
	}
}

func TestRoundTrip_AccountTags(t *testing.T) {
	src := &model.Project{
		Accounts: []model.Account{{
			Name: "Assets:Crypto:ethereum:treasury",
			Type: model.AccountAsset,
			Tags: map[string]string{
				"wallet":  "wlt_DEF",
				"address": "0xdeadbeef",
				"network": "ethereum",
			},
		}},
	}
	var buf bytes.Buffer
	if err := Print(&buf, src); err != nil {
		t.Fatalf("print: %v", err)
	}
	got, err := Parse(&buf)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("accounts = %d", len(got.Accounts))
	}
	a := got.Accounts[0]
	for k, v := range src.Accounts[0].Tags {
		if a.Tags[k] != v {
			t.Fatalf("round-trip lost tag %s: got %q want %q", k, a.Tags[k], v)
		}
	}
}

func TestParsePrice_DateOnly(t *testing.T) {
	src := "P 2024-01-15 BTC $50000.00\n"
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Prices) != 1 {
		t.Fatalf("prices = %d", len(p.Prices))
	}
	pr := p.Prices[0]
	if pr.HasTime {
		t.Fatalf("date-only price should not have HasTime=true")
	}
	if pr.Commodity != "BTC" || pr.QuoteCurrency != "USD" {
		t.Fatalf("got %s/%s", pr.Commodity, pr.QuoteCurrency)
	}
	if got := pr.Date.Format("2006-01-02"); got != "2024-01-15" {
		t.Fatalf("date = %s", got)
	}
}

func TestParsePrice_Timestamped_WithOffset(t *testing.T) {
	src := "P 2024-06-21 02:18:02-05:00 BTC $50000.00\n"
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Prices) != 1 {
		t.Fatalf("prices = %d", len(p.Prices))
	}
	pr := p.Prices[0]
	if !pr.HasTime {
		t.Fatalf("timestamped price should have HasTime=true")
	}
	_, offset := pr.Date.Zone()
	if offset != -5*3600 {
		t.Fatalf("offset = %ds, want -18000", offset)
	}
	if pr.Date.Hour() != 2 || pr.Date.Minute() != 18 || pr.Date.Second() != 2 {
		t.Fatalf("clock = %02d:%02d:%02d", pr.Date.Hour(), pr.Date.Minute(), pr.Date.Second())
	}
}

func TestParsePrice_Timestamped_UTC_Z(t *testing.T) {
	src := "P 2024-06-21 02:18:02Z BTC $50000.00\n"
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pr := p.Prices[0]
	if !pr.HasTime {
		t.Fatalf("Z should mark HasTime=true")
	}
	if pr.Date.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", pr.Date.Location())
	}
}

func TestParsePrice_RejectsNaiveTime(t *testing.T) {
	src := "P 2024-06-21 02:18:02 BTC $50000.00\n"
	_, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatalf("expected error for naive timestamp")
	}
	if !strings.Contains(err.Error(), "timezone") {
		t.Fatalf("error should mention timezone, got: %v", err)
	}
}

func TestPrintPrice_Timestamped_RoundTrip(t *testing.T) {
	loc := time.FixedZone("test", -5*3600)
	src := &model.Project{
		Prices: []model.Price{{
			Date:          time.Date(2024, 6, 21, 2, 18, 2, 0, loc),
			HasTime:       true,
			Commodity:     "BTC",
			QuoteCurrency: "USD",
			Price:         new(big.Rat).SetInt64(50000),
		}},
	}
	var buf bytes.Buffer
	if err := Print(&buf, src); err != nil {
		t.Fatalf("print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "P 2024-06-21 02:18:02-05:00 BTC") {
		t.Fatalf("printed output missing timestamped form:\n%s", out)
	}
	got, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !got.Prices[0].HasTime {
		t.Fatalf("HasTime lost in round-trip")
	}
	_, offset := got.Prices[0].Date.Zone()
	if offset != -5*3600 {
		t.Fatalf("offset = %d after round-trip", offset)
	}
}

func TestPrintPrice_DateOnly_RoundTrip(t *testing.T) {
	src := &model.Project{
		Prices: []model.Price{{
			Date:          time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Commodity:     "BTC",
			QuoteCurrency: "USD",
			Price:         new(big.Rat).SetInt64(50000),
		}},
	}
	var buf bytes.Buffer
	if err := Print(&buf, src); err != nil {
		t.Fatalf("print: %v", err)
	}
	out := buf.String()
	// Must not contain a time component when HasTime is false.
	if strings.Contains(out, "00:00:00") {
		t.Fatalf("date-only print emitted spurious time:\n%s", out)
	}
	if !strings.Contains(out, "P 2024-01-15 BTC") {
		t.Fatalf("date-only form missing:\n%s", out)
	}
}

func TestParseCommodity_AllSubdirectives(t *testing.T) {
	src := `commodity USD
    note American Dollars
    format $1,000.00
    nomarket
    default
`
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Commodities) != 1 {
		t.Fatalf("commodities = %d", len(p.Commodities))
	}
	c := p.Commodities[0]
	if c.Symbol != "USD" {
		t.Errorf("symbol = %q", c.Symbol)
	}
	if c.Note != "American Dollars" {
		t.Errorf("note = %q", c.Note)
	}
	if c.Format != "$1,000.00" {
		t.Errorf("format = %q", c.Format)
	}
	if !c.NoMarket {
		t.Error("nomarket not set")
	}
	if !c.IsDefault {
		t.Error("default not set")
	}
}

func TestParseCommodity_UnknownSubdirective(t *testing.T) {
	src := `commodity USD
    bogus thing
`
	_, err := Parse(strings.NewReader(src))
	if err == nil || !strings.Contains(err.Error(), "unknown commodity subdirective") {
		t.Fatalf("want unknown-subdirective error, got: %v", err)
	}
}

func TestParseCommodity_BareSymbol(t *testing.T) {
	src := "commodity BTC\n"
	p, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Commodities) != 1 || p.Commodities[0].Symbol != "BTC" {
		t.Fatalf("got %+v", p.Commodities)
	}
	c := p.Commodities[0]
	if c.Note != "" || c.Format != "" || c.NoMarket || c.IsDefault {
		t.Errorf("bare commodity carried extra fields: %+v", c)
	}
}

func TestCommodity_RoundTrip(t *testing.T) {
	src := &model.Project{
		Commodities: []model.Commodity{
			{Symbol: "USD", Note: "American Dollars", Format: "$1,000.00", IsDefault: true},
			{Symbol: "BTC", NoMarket: true},
		},
	}
	var buf bytes.Buffer
	if err := Print(&buf, src); err != nil {
		t.Fatalf("print: %v", err)
	}
	got, err := Parse(&buf)
	if err != nil {
		t.Fatalf("reparse: %v\noutput:\n%s", err, buf.String())
	}
	if len(got.Commodities) != 2 {
		t.Fatalf("commodities = %d", len(got.Commodities))
	}
	// Printer sorts by symbol — so BTC comes first.
	if got.Commodities[0].Symbol != "BTC" || !got.Commodities[0].NoMarket {
		t.Errorf("BTC lost nomarket: %+v", got.Commodities[0])
	}
	if got.Commodities[1].Symbol != "USD" || got.Commodities[1].Note != "American Dollars" || got.Commodities[1].Format != "$1,000.00" || !got.Commodities[1].IsDefault {
		t.Errorf("USD round-trip lost fields: %+v", got.Commodities[1])
	}
}

func TestPostingStatusOverride(t *testing.T) {
	input := `2024-01-01 ! partial clear
    * Assets:Cash                       $10.00
    Income:Tips                        -$10.00
`
	p, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Entries) != 1 {
		t.Fatalf("entries: got %d", len(p.Entries))
	}
	e := p.Entries[0]
	if e.Postings[0].Status == nil {
		t.Fatalf("first posting should have status override")
	}
	if e.Postings[1].Status != nil {
		t.Errorf("second posting should inherit (no override)")
	}
}
