package expense

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

func mkEntry(date string, payee, note, account string, amt int64, commodity string) model.Entry {
	d, _ := time.Parse("2006-01-02", date)
	return model.Entry{
		Date:  d,
		Payee: payee,
		Note:  note,
		Postings: []model.Posting{
			{Account: account, Amount: model.Amount{Quantity: big.NewRat(amt, 1), Commodity: commodity}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-amt, 1), Commodity: commodity}},
		},
	}
}

func TestBuild_FiltersByTag(t *testing.T) {
	entries := []model.Entry{
		mkEntry("2026-05-16", "Acme", "expense-report:Q1 merchant:Acme", "Expenses:Travel", 120, "USD"),
		mkEntry("2026-05-17", "Diner", "expense-report:Q1 merchant:Diner", "Expenses:Meals", 45, "USD"),
		mkEntry("2026-05-18", "Office", "expense-report:Q2", "Expenses:Supplies", 99, "USD"),
		mkEntry("2026-05-19", "Random", "no tags", "Expenses:Other", 10, "USD"),
	}
	r, err := Build(entries, Filter{ReportId: "Q1"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(r.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(r.Lines))
	}
	if r.Lines[0].Merchant != "Acme" || r.Lines[1].Merchant != "Diner" {
		t.Errorf("merchants: %s, %s", r.Lines[0].Merchant, r.Lines[1].Merchant)
	}
}

func TestBuild_DateRange(t *testing.T) {
	entries := []model.Entry{
		mkEntry("2026-05-01", "A", "expense-report:Q1", "Expenses:Travel", 10, "USD"),
		mkEntry("2026-05-15", "B", "expense-report:Q1", "Expenses:Travel", 20, "USD"),
		mkEntry("2026-05-31", "C", "expense-report:Q1", "Expenses:Travel", 30, "USD"),
	}
	from, _ := time.Parse("2006-01-02", "2026-05-10")
	to, _ := time.Parse("2006-01-02", "2026-05-20")
	r, _ := Build(entries, Filter{ReportId: "Q1", From: from, To: to})
	if len(r.Lines) != 1 {
		t.Fatalf("expected 1 line in window, got %d", len(r.Lines))
	}
	if r.Lines[0].Payee != "B" {
		t.Errorf("payee in window: %s", r.Lines[0].Payee)
	}
}

func TestBuild_TotalsAndCategories(t *testing.T) {
	entries := []model.Entry{
		mkEntry("2026-05-16", "A", "expense-report:Q1 reimbursable:true", "Expenses:Travel", 120, "USD"),
		mkEntry("2026-05-17", "B", "expense-report:Q1", "Expenses:Meals", 45, "USD"),
		mkEntry("2026-05-18", "C", "expense-report:Q1 reimbursable:true", "Expenses:Travel", 30, "USD"),
	}
	r, _ := Build(entries, Filter{ReportId: "Q1"})
	grand, _ := r.Totals.Grand.Float64()
	if grand != 195.0 {
		t.Errorf("Grand: want 195, got %v", grand)
	}
	reimb, _ := r.Totals.Reimbursable.Float64()
	if reimb != 150.0 {
		t.Errorf("Reimbursable: want 150, got %v", reimb)
	}
	non, _ := r.Totals.NonReimbursable.Float64()
	if non != 45.0 {
		t.Errorf("NonReimbursable: want 45, got %v", non)
	}
	travel, ok := r.Totals.ByCategory["Travel"]
	if !ok {
		t.Fatal("missing Travel category")
	}
	tf, _ := travel.Float64()
	if tf != 150.0 {
		t.Errorf("Travel: want 150, got %v", tf)
	}
}

func TestRenderCSV_Header(t *testing.T) {
	r, _ := Build([]model.Entry{
		mkEntry("2026-05-16", "Acme", "expense-report:Q1 merchant:Acme reimbursable:true", "Expenses:Travel", 120, "USD"),
	}, Filter{ReportId: "Q1"})
	var buf bytes.Buffer
	if err := RenderCSV(&buf, r); err != nil {
		t.Fatalf("render csv: %v", err)
	}
	want := "Date,Merchant,Category,Account,Amount,Currency,Reimbursable,Note,EntryId"
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if lines[0] != want {
		t.Errorf("CSV header:\nwant: %s\ngot:  %s", want, lines[0])
	}
	if !strings.Contains(buf.String(), "Acme") {
		t.Error("CSV missing merchant")
	}
}

func TestRenderCSV_EscapesQuotes(t *testing.T) {
	r, _ := Build([]model.Entry{
		mkEntry("2026-05-16", `Acme, "Big" Co`, "expense-report:Q1 merchant:Acme,_\"Big\"_Co", "Expenses:Travel", 50, "USD"),
	}, Filter{ReportId: "Q1"})
	// merchant token can't contain commas/quotes; the test really cares that
	// the payee column ("Acme, \"Big\" Co") is correctly quoted.
	var buf bytes.Buffer
	if err := RenderCSV(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"Acme, ""Big"" Co"`) {
		t.Errorf("CSV did not RFC-4180-quote the payee:\n%s", buf.String())
	}
}

func TestRenderQIF_Format(t *testing.T) {
	r, _ := Build([]model.Entry{
		mkEntry("2026-05-16", "Acme", "expense-report:Q1 merchant:Acme", "Expenses:Travel", 120, "USD"),
	}, Filter{ReportId: "Q1"})
	var buf bytes.Buffer
	if err := RenderQIF(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"!Type:Cash", "D2026-05-16", "T-120", "PAcme", "LExpenses:Travel", "^"} {
		if !strings.Contains(out, want) {
			t.Errorf("QIF missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderJSON_RoundTrip(t *testing.T) {
	r, _ := Build([]model.Entry{
		mkEntry("2026-05-16", "Acme", "expense-report:Q1 merchant:Acme", "Expenses:Travel", 120, "USD"),
	}, Filter{ReportId: "Q1"})
	var buf bytes.Buffer
	if err := RenderJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["reportId"] != "Q1" {
		t.Errorf("json reportId: %v", got["reportId"])
	}
}

func TestRenderHTML_Contains(t *testing.T) {
	r, _ := Build([]model.Entry{
		mkEntry("2026-05-16", "Acme", "expense-report:Q1-travel merchant:Acme reimbursable:true", "Expenses:Travel", 120, "USD"),
		mkEntry("2026-05-17", "Diner", "expense-report:Q1-travel merchant:Diner", "Expenses:Meals", 45, "USD"),
	}, Filter{ReportId: "Q1-travel"})
	var buf bytes.Buffer
	if err := RenderHTML(&buf, r); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{"Q1-travel", "Acme", "Diner", "120", "45", "Reimbursable"} {
		if !strings.Contains(s, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func TestRenderText_HasHeaderAndTotals(t *testing.T) {
	r, _ := Build([]model.Entry{
		mkEntry("2026-05-16", "Acme", "expense-report:Q1 merchant:Acme reimbursable:true", "Expenses:Travel", 120, "USD"),
		mkEntry("2026-05-17", "Diner", "expense-report:Q1 merchant:Diner", "Expenses:Meals", 45, "USD"),
	}, Filter{ReportId: "Q1"})
	var buf bytes.Buffer
	if err := RenderText(&buf, r); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{"Expense Report: Q1", "Acme", "Diner", "Subtotal", "Reimbursable"} {
		if !strings.Contains(s, want) {
			t.Errorf("Text missing %q:\n%s", want, s)
		}
	}
}
