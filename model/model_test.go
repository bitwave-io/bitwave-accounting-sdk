package model_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

func TestStatus_Flag(t *testing.T) {
	cases := []struct {
		s    model.Status
		want string
	}{
		{model.StatusUncleared, ""},
		{model.StatusPending, "!"},
		{model.StatusCleared, "*"},
	}
	for _, c := range cases {
		if got := c.s.Flag(); got != c.want {
			t.Errorf("Status(%d).Flag() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	cases := []struct {
		in      string
		want    model.Status
		wantErr bool
	}{
		{"", model.StatusUncleared, false},
		{"uncleared", model.StatusUncleared, false},
		{"pending", model.StatusPending, false},
		{"!", model.StatusPending, false},
		{"cleared", model.StatusCleared, false},
		{"*", model.StatusCleared, false},
		{"CLEARED", model.StatusCleared, false}, // case-insensitive
		{"bogus", 0, true},
	}
	for _, c := range cases {
		got, err := model.ParseStatus(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseStatus(%q) expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseStatus(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseStatus(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestInferAccountType(t *testing.T) {
	cases := []struct {
		name string
		want model.AccountType
	}{
		{"Assets:Cash", model.AccountAsset},
		{"assets:savings", model.AccountAsset},
		{"Asset:Solo", model.AccountAsset},
		{"Liabilities:CC", model.AccountLiability},
		{"Equity:Opening", model.AccountEquity},
		{"Income:Salary", model.AccountIncome},
		{"Revenue:Sales", model.AccountIncome},
		{"Expenses:Food", model.AccountExpense},
		{"Mystery:Box", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := model.InferAccountType(c.name); got != c.want {
			t.Errorf("InferAccountType(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAmount_IsZero(t *testing.T) {
	cases := []struct {
		name string
		a    model.Amount
		want bool
	}{
		{"nil quantity", model.Amount{}, true},
		{"zero quantity", model.Amount{Quantity: new(big.Rat)}, true},
		{"positive", model.Amount{Quantity: big.NewRat(5, 1)}, false},
		{"negative", model.Amount{Quantity: big.NewRat(-5, 1)}, false},
	}
	for _, c := range cases {
		if got := c.a.IsZero(); got != c.want {
			t.Errorf("%s: IsZero() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPosting_BaseAmount(t *testing.T) {
	cases := []struct {
		name string
		p    model.Posting
		base string
		want string // FloatString(2)
	}{
		{
			"nil quantity returns zero",
			model.Posting{Account: "A"},
			"USD",
			"0.00",
		},
		{
			"native USD posting passes through",
			model.Posting{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(500, 100), Commodity: "USD"}},
			"USD",
			"5.00",
		},
		{
			"BTC with USD unit price multiplies",
			model.Posting{
				Account:   "Assets:BTC",
				Amount:    model.Amount{Quantity: big.NewRat(1, 1), Commodity: "BTC"},
				UnitPrice: &model.Amount{Quantity: big.NewRat(50000, 1), Commodity: "USD"},
			},
			"USD",
			"50000.00",
		},
		{
			"matching commodity ignores unit price",
			model.Posting{
				Account:   "Assets:Cash",
				Amount:    model.Amount{Quantity: big.NewRat(100, 1), Commodity: "USD"},
				UnitPrice: &model.Amount{Quantity: big.NewRat(2, 1), Commodity: "USD"},
			},
			"USD",
			"100.00",
		},
		{
			"non-base commodity with no unit price returns raw",
			model.Posting{
				Account: "Assets:BTC",
				Amount:  model.Amount{Quantity: big.NewRat(2, 1), Commodity: "BTC"},
			},
			"USD",
			"2.00",
		},
	}
	for _, c := range cases {
		got := c.p.BaseAmount(c.base).FloatString(2)
		if got != c.want {
			t.Errorf("%s: BaseAmount = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestEntry_BalanceAndIsBalanced(t *testing.T) {
	d, _ := time.Parse("2006-01-02", "2024-01-15")

	t.Run("balanced USD entry", func(t *testing.T) {
		e := model.Entry{Date: d, Postings: []model.Posting{
			{Account: "Expenses:Food", Amount: model.Amount{Quantity: big.NewRat(5, 1), Commodity: "USD"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-5, 1), Commodity: "USD"}},
		}}
		if !e.IsBalanced("USD") {
			t.Errorf("expected balanced, sum=%s", e.Balance("USD").FloatString(2))
		}
	})

	t.Run("unbalanced entry", func(t *testing.T) {
		e := model.Entry{Date: d, Postings: []model.Posting{
			{Account: "Expenses:Food", Amount: model.Amount{Quantity: big.NewRat(5, 1), Commodity: "USD"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-3, 1), Commodity: "USD"}},
		}}
		if e.IsBalanced("USD") {
			t.Errorf("expected unbalanced; sum=%s", e.Balance("USD").FloatString(2))
		}
		if got := e.Balance("USD").FloatString(2); got != "2.00" {
			t.Errorf("Balance = %s, want 2.00", got)
		}
	})

	t.Run("balanced via @ unit price", func(t *testing.T) {
		e := model.Entry{Date: d, Postings: []model.Posting{
			{
				Account:   "Assets:BTC",
				Amount:    model.Amount{Quantity: big.NewRat(1, 1), Commodity: "BTC"},
				UnitPrice: &model.Amount{Quantity: big.NewRat(50000, 1), Commodity: "USD"},
			},
			{
				Account: "Assets:Cash",
				Amount:  model.Amount{Quantity: big.NewRat(-50000, 1), Commodity: "USD"},
			},
		}}
		if !e.IsBalanced("USD") {
			t.Errorf("expected balanced via @ price; sum=%s", e.Balance("USD").FloatString(2))
		}
	})

	t.Run("empty entry balances", func(t *testing.T) {
		e := model.Entry{Date: d}
		if !e.IsBalanced("USD") {
			t.Errorf("empty entry should balance trivially")
		}
	})

	t.Run("multi-leg entry balances", func(t *testing.T) {
		// Three-leg split: $30 expense paid from $20 cash + $10 credit card.
		e := model.Entry{Date: d, Postings: []model.Posting{
			{Account: "Expenses:Food", Amount: model.Amount{Quantity: big.NewRat(30, 1), Commodity: "USD"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-20, 1), Commodity: "USD"}},
			{Account: "Liabilities:CC", Amount: model.Amount{Quantity: big.NewRat(-10, 1), Commodity: "USD"}},
		}}
		if !e.IsBalanced("USD") {
			t.Errorf("3-leg entry should balance; sum=%s", e.Balance("USD").FloatString(2))
		}
	})
}

func TestProject_SortEntries(t *testing.T) {
	d1, _ := time.Parse("2006-01-02", "2024-01-15")
	d2, _ := time.Parse("2006-01-02", "2024-01-10")
	d3, _ := time.Parse("2006-01-02", "2024-02-01")
	p := &model.Project{
		Entries: []model.Entry{
			{Date: d1, Payee: "Mid"},
			{Date: d2, Payee: "Earliest"},
			{Date: d3, Payee: "Latest"},
		},
	}
	p.SortEntries()
	got := []string{p.Entries[0].Payee, p.Entries[1].Payee, p.Entries[2].Payee}
	want := []string{"Earliest", "Mid", "Latest"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Entries[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProject_SortEntries_StableForSameDate(t *testing.T) {
	d, _ := time.Parse("2006-01-02", "2024-01-15")
	p := &model.Project{
		Entries: []model.Entry{
			{Date: d, Payee: "First"},
			{Date: d, Payee: "Second"},
			{Date: d, Payee: "Third"},
		},
	}
	p.SortEntries()
	for i, want := range []string{"First", "Second", "Third"} {
		if p.Entries[i].Payee != want {
			t.Errorf("Entries[%d] = %q, want %q (insertion order should hold)", i, p.Entries[i].Payee, want)
		}
	}
}

func TestProject_AccountNames(t *testing.T) {
	d, _ := time.Parse("2006-01-02", "2024-01-15")
	p := &model.Project{
		Accounts: []model.Account{{Name: "Assets:Declared"}},
		Entries: []model.Entry{
			{Date: d, Postings: []model.Posting{
				{Account: "Assets:Observed", Amount: model.Amount{Quantity: big.NewRat(1, 1), Commodity: "USD"}},
				{Account: "Assets:Declared", Amount: model.Amount{Quantity: big.NewRat(-1, 1), Commodity: "USD"}},
			}},
		},
	}
	got := p.AccountNames()
	want := []string{"Assets:Declared", "Assets:Observed"} // sorted, deduped
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProject_Commodities(t *testing.T) {
	d, _ := time.Parse("2006-01-02", "2024-01-15")
	p := &model.Project{
		Entries: []model.Entry{
			{Date: d, Postings: []model.Posting{
				{Amount: model.Amount{Quantity: big.NewRat(1, 1), Commodity: "USD"}},
				{Amount: model.Amount{Quantity: big.NewRat(1, 1), Commodity: "BTC"}},
			}},
		},
		Prices: []model.Price{
			{Date: d, Commodity: "ETH", QuoteCurrency: "USD", Price: big.NewRat(3000, 1)},
		},
	}
	got := p.CommoditySymbols()
	want := []string{"BTC", "ETH", "USD"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProject_Payees_DistinctSorted(t *testing.T) {
	d, _ := time.Parse("2006-01-02", "2024-01-15")
	p := &model.Project{Entries: []model.Entry{
		{Date: d, Payee: "Bob"},
		{Date: d, Payee: "Alice"},
		{Date: d, Payee: "Bob"},
		{Date: d, Payee: ""}, // empty payees are skipped
	}}
	got := p.Payees()
	want := []string{"Alice", "Bob"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
