package store_test

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-accounting-sdk/store"
)

type storeFactory func(t *testing.T, projectName, baseCurrency string) store.Store

func runStoreContract(t *testing.T, factory storeFactory) {
	t.Helper()
	t.Run("EmptyProject", func(t *testing.T) { contractEmpty(t, factory) })
	t.Run("AddAccount_Persists", func(t *testing.T) { contractAddAccount(t, factory) })
	t.Run("AddEntry_ReturnsID_AndPersists", func(t *testing.T) { contractAddEntry(t, factory) })
	t.Run("AddEntry_AssignsUniqueIDs", func(t *testing.T) { contractUniqueIDs(t, factory) })
	t.Run("AddPrice_Persists", func(t *testing.T) { contractAddPrice(t, factory) })
	t.Run("SetEntryStatus_Cleared", func(t *testing.T) { contractStatusEntry(t, factory) })
	t.Run("SetEntryStatus_PostingOnly", func(t *testing.T) { contractStatusPosting(t, factory) })
	t.Run("SetEntryStatus_UnknownEntry_Errors", func(t *testing.T) { contractStatusUnknownEntry(t, factory) })
	t.Run("SetEntryStatus_UnknownPosting_Errors", func(t *testing.T) { contractStatusUnknownPosting(t, factory) })
	t.Run("AppendRaw_AddsAllParts", func(t *testing.T) { contractAppendRaw(t, factory) })
	t.Run("EntryWithUnitPrice_RoundTrips", func(t *testing.T) { contractUnitPrice(t, factory) })
	t.Run("EntryOrder_Preserved", func(t *testing.T) { contractEntryOrder(t, factory) })
}

func contractEmpty(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	p, err := s.Project(context.Background())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if p.Name != "demo" {
		t.Errorf("project name = %q, want demo", p.Name)
	}
	if p.BaseCurrency != "USD" {
		t.Errorf("base currency = %q, want USD", p.BaseCurrency)
	}
	if len(p.Entries) != 0 || len(p.Accounts) != 0 || len(p.Prices) != 0 {
		t.Errorf("empty project should be empty, got %d/%d/%d", len(p.Entries), len(p.Accounts), len(p.Prices))
	}
}

func contractAddAccount(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	if err := s.AddAccount(ctx, model.Account{Name: "Assets:Cash", Type: model.AccountAsset}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	if err := s.AddAccount(ctx, model.Account{Name: "Expenses:Food", Note: "groceries"}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	p, err := s.Project(ctx)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(p.Accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(p.Accounts))
	}
	names := map[string]bool{}
	for _, a := range p.Accounts {
		names[a.Name] = true
	}
	if !names["Assets:Cash"] || !names["Expenses:Food"] {
		t.Errorf("account names lost: %v", names)
	}
}

func contractAddEntry(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	e := buildBalancedEntry(t, "2024-01-15", "Coffee", model.StatusUncleared, "Expenses:Food", "5.00", "Assets:Cash", "-5.00")
	id, err := s.AddEntry(ctx, e)
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if id == "" {
		t.Errorf("AddEntry should return non-empty id")
	}
	p, err := s.Project(ctx)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(p.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(p.Entries))
	}
	got := p.Entries[0]
	if got.ID != id {
		t.Errorf("persisted entry id = %q, want %q", got.ID, id)
	}
	if got.Payee != "Coffee" {
		t.Errorf("payee = %q, want Coffee", got.Payee)
	}
	if !got.IsBalanced("USD") {
		t.Errorf("entry should still balance after roundtrip; sum=%s", got.Balance("USD").FloatString(2))
	}
}

func contractUniqueIDs(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	e1 := buildBalancedEntry(t, "2024-01-15", "First", model.StatusUncleared, "Expenses:Food", "5.00", "Assets:Cash", "-5.00")
	e2 := buildBalancedEntry(t, "2024-01-15", "Second", model.StatusUncleared, "Expenses:Food", "10.00", "Assets:Cash", "-10.00")
	id1, err := s.AddEntry(ctx, e1)
	if err != nil {
		t.Fatalf("AddEntry 1: %v", err)
	}
	id2, err := s.AddEntry(ctx, e2)
	if err != nil {
		t.Fatalf("AddEntry 2: %v", err)
	}
	if id1 == id2 {
		t.Errorf("expected unique entry ids, got %q twice", id1)
	}
}

func contractAddPrice(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	date, _ := time.Parse("2006-01-02", "2024-01-15")
	if err := s.AddPrice(ctx, model.Price{Date: date, Commodity: "BTC", QuoteCurrency: "USD", Price: big.NewRat(50000, 1)}); err != nil {
		t.Fatalf("AddPrice: %v", err)
	}
	p, err := s.Project(ctx)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(p.Prices) != 1 {
		t.Fatalf("got %d prices, want 1", len(p.Prices))
	}
	if p.Prices[0].Commodity != "BTC" {
		t.Errorf("commodity = %q, want BTC", p.Prices[0].Commodity)
	}
	if p.Prices[0].Price.Cmp(big.NewRat(50000, 1)) != 0 {
		t.Errorf("price = %s, want 50000", p.Prices[0].Price.FloatString(2))
	}
}

func contractStatusEntry(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	e := buildBalancedEntry(t, "2024-01-15", "Coffee", model.StatusUncleared, "Expenses:Food", "5.00", "Assets:Cash", "-5.00")
	id, err := s.AddEntry(ctx, e)
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if err := s.SetEntryStatus(ctx, id, model.StatusCleared, ""); err != nil {
		t.Fatalf("SetEntryStatus: %v", err)
	}
	p, _ := s.Project(ctx)
	if p.Entries[0].Status != model.StatusCleared {
		t.Errorf("status = %v, want Cleared", p.Entries[0].Status)
	}
}

func contractStatusPosting(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	e := buildBalancedEntry(t, "2024-01-15", "Coffee", model.StatusUncleared, "Expenses:Food", "5.00", "Assets:Cash", "-5.00")
	id, _ := s.AddEntry(ctx, e)
	if err := s.SetEntryStatus(ctx, id, model.StatusCleared, "Assets:Cash"); err != nil {
		t.Fatalf("SetEntryStatus: %v", err)
	}
	p, _ := s.Project(ctx)
	entry := p.Entries[0]
	if entry.Status == model.StatusCleared {
		t.Errorf("entry-level status should still be Uncleared")
	}
	var cashStatus, foodStatus *model.Status
	for _, post := range entry.Postings {
		switch post.Account {
		case "Assets:Cash":
			cashStatus = post.Status
		case "Expenses:Food":
			foodStatus = post.Status
		}
	}
	if cashStatus == nil || *cashStatus != model.StatusCleared {
		t.Errorf("Assets:Cash posting status = %v, want Cleared", cashStatus)
	}
	if foodStatus != nil {
		t.Errorf("Expenses:Food posting status should be nil, got %v", *foodStatus)
	}
}

func contractStatusUnknownEntry(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	if err := s.SetEntryStatus(context.Background(), "no-such-id", model.StatusCleared, ""); err == nil {
		t.Errorf("SetEntryStatus on unknown id should return error")
	}
}

func contractStatusUnknownPosting(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	e := buildBalancedEntry(t, "2024-01-15", "Coffee", model.StatusUncleared, "Expenses:Food", "5.00", "Assets:Cash", "-5.00")
	id, _ := s.AddEntry(ctx, e)
	if err := s.SetEntryStatus(ctx, id, model.StatusCleared, "Assets:NoSuch"); err == nil {
		t.Errorf("SetEntryStatus on unknown posting account should return error")
	}
}

func contractAppendRaw(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	date, _ := time.Parse("2006-01-02", "2024-01-15")
	raw := &model.Project{
		Accounts: []model.Account{
			{Name: "Assets:Cash", Type: model.AccountAsset},
			{Name: "Expenses:Food", Type: model.AccountExpense},
		},
		Entries: []model.Entry{
			buildBalancedEntry(t, "2024-01-15", "Coffee", model.StatusUncleared, "Expenses:Food", "5.00", "Assets:Cash", "-5.00"),
		},
		Prices: []model.Price{
			{Date: date, Commodity: "BTC", QuoteCurrency: "USD", Price: big.NewRat(50000, 1)},
		},
	}
	if err := s.AppendRaw(ctx, raw); err != nil {
		t.Fatalf("AppendRaw: %v", err)
	}
	p, _ := s.Project(ctx)
	if len(p.Accounts) != 2 || len(p.Entries) != 1 || len(p.Prices) != 1 {
		t.Errorf("AppendRaw counts: accounts=%d entries=%d prices=%d", len(p.Accounts), len(p.Entries), len(p.Prices))
	}
}

func contractUnitPrice(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	date, _ := time.Parse("2006-01-02", "2024-01-15")
	e := model.Entry{
		Date: date, Payee: "Bought BTC", Status: model.StatusUncleared,
		Postings: []model.Posting{
			{Account: "Assets:Crypto:BTC", Amount: model.Amount{Quantity: big.NewRat(1, 1), Commodity: "BTC"},
				UnitPrice: &model.Amount{Quantity: big.NewRat(50000, 1), Commodity: "USD"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-50000, 1), Commodity: "USD"}},
		},
	}
	if !e.IsBalanced("USD") {
		t.Fatalf("test fixture should balance, got %s", e.Balance("USD").FloatString(2))
	}
	if _, err := s.AddEntry(ctx, e); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	p, _ := s.Project(ctx)
	got := p.Entries[0]
	if !got.IsBalanced("USD") {
		t.Errorf("roundtripped entry should still balance; sum=%s", got.Balance("USD").FloatString(2))
	}
	var btcPost *model.Posting
	for i := range got.Postings {
		if strings.HasPrefix(got.Postings[i].Account, "Assets:Crypto") {
			btcPost = &got.Postings[i]
		}
	}
	if btcPost == nil {
		t.Fatalf("BTC posting lost in roundtrip")
	}
	if btcPost.UnitPrice == nil {
		t.Errorf("UnitPrice nil after roundtrip")
	} else if btcPost.UnitPrice.Quantity.Cmp(big.NewRat(50000, 1)) != 0 {
		t.Errorf("UnitPrice = %s, want 50000", btcPost.UnitPrice.Quantity.FloatString(2))
	}
}

func contractEntryOrder(t *testing.T, factory storeFactory) {
	s := factory(t, "demo", "USD")
	ctx := context.Background()
	e1 := buildBalancedEntry(t, "2024-01-15", "First", model.StatusUncleared, "Expenses:Food", "5.00", "Assets:Cash", "-5.00")
	e2 := buildBalancedEntry(t, "2024-01-15", "Second", model.StatusUncleared, "Expenses:Food", "10.00", "Assets:Cash", "-10.00")
	e3 := buildBalancedEntry(t, "2024-01-16", "Third", model.StatusUncleared, "Expenses:Food", "15.00", "Assets:Cash", "-15.00")
	for _, e := range []model.Entry{e1, e2, e3} {
		if _, err := s.AddEntry(ctx, e); err != nil {
			t.Fatalf("AddEntry: %v", err)
		}
	}
	p, _ := s.Project(ctx)
	if len(p.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(p.Entries))
	}
	want := []string{"First", "Second", "Third"}
	for i, w := range want {
		if p.Entries[i].Payee != w {
			t.Errorf("entry[%d].Payee = %q, want %q", i, p.Entries[i].Payee, w)
		}
	}
}

func buildBalancedEntry(t *testing.T, date, payee string, status model.Status, acct1, amt1, acct2, amt2 string) model.Entry {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("date: %v", err)
	}
	q1, ok := new(big.Rat).SetString(amt1)
	if !ok {
		t.Fatalf("parse amt1 %q", amt1)
	}
	q2, ok := new(big.Rat).SetString(amt2)
	if !ok {
		t.Fatalf("parse amt2 %q", amt2)
	}
	return model.Entry{
		Date: d, Payee: payee, Status: status,
		Postings: []model.Posting{
			{Account: acct1, Amount: model.Amount{Quantity: q1, Commodity: "USD"}},
			{Account: acct2, Amount: model.Amount{Quantity: q2, Commodity: "USD"}},
		},
	}
}
