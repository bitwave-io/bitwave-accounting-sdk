package report_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// makeProject builds a deterministic fixture project used by every report test.
// 4 entries spanning two months, mixing USD-only and BTC@USD postings, with a
// pending and a cleared status to exercise the filter paths.
func makeProject(t *testing.T) *model.Project {
	t.Helper()
	d := func(s string) time.Time {
		v, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("date %q: %v", s, err)
		}
		return v
	}
	rat := func(num, den int64) *big.Rat { return big.NewRat(num, den) }

	usd := func(n int64) model.Amount {
		return model.Amount{Quantity: rat(n, 1), Commodity: "USD"}
	}
	usdRat := func(num, den int64) model.Amount {
		return model.Amount{Quantity: rat(num, den), Commodity: "USD"}
	}

	return &model.Project{
		Name:         "fixture",
		BaseCurrency: "USD",
		Accounts: []model.Account{
			{Name: "Assets:Cash", Type: model.AccountAsset},
			{Name: "Assets:Crypto:BTC", Type: model.AccountAsset},
			{Name: "Expenses:Food", Type: model.AccountExpense},
			{Name: "Income:Salary", Type: model.AccountIncome},
		},
		Prices: []model.Price{
			{Date: d("2024-01-15"), Commodity: "BTC", QuoteCurrency: "USD", Price: rat(50000, 1)},
		},
		Entries: []model.Entry{
			{
				ID: "e1", Date: d("2024-01-10"), Payee: "Acme Corp", Status: model.StatusCleared,
				Postings: []model.Posting{
					{Account: "Assets:Cash", Amount: usd(1000)},
					{Account: "Income:Salary", Amount: usd(-1000)},
				},
			},
			{
				ID: "e2", Date: d("2024-01-15"), Payee: "Coffee shop", Status: model.StatusUncleared,
				Postings: []model.Posting{
					{Account: "Expenses:Food", Amount: usdRat(525, 100)},
					{Account: "Assets:Cash", Amount: usdRat(-525, 100)},
				},
			},
			{
				ID: "e3", Date: d("2024-01-15"), Payee: "Bought BTC", Status: model.StatusPending,
				Postings: []model.Posting{
					{
						Account:   "Assets:Crypto:BTC",
						Amount:    model.Amount{Quantity: rat(1, 1), Commodity: "BTC"},
						UnitPrice: &model.Amount{Quantity: rat(50000, 1), Commodity: "USD"},
					},
					{Account: "Assets:Cash", Amount: usd(-50000)},
				},
			},
			{
				ID: "e4", Date: d("2024-02-01"), Payee: "Grocer", Status: model.StatusCleared,
				Postings: []model.Posting{
					{Account: "Expenses:Food", Amount: usd(75)},
					{Account: "Assets:Cash", Amount: usd(-75)},
				},
			},
		},
	}
}
