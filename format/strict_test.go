package format

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

func mkProject() *model.Project {
	return &model.Project{
		BaseCurrency: "USD",
		Accounts: []model.Account{
			{Name: "Assets:Cash"},
			{Name: "Expenses:Food"},
		},
		Commodities: []model.Commodity{
			{Symbol: "USD"},
		},
	}
}

func TestValidateStrict_AllDeclared_OK(t *testing.T) {
	p := mkProject()
	p.Entries = []model.Entry{{
		Date: time.Now(),
		Postings: []model.Posting{
			{Account: "Expenses:Food", Amount: model.Amount{Quantity: big.NewRat(5, 1), Commodity: "USD"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-5, 1), Commodity: "USD"}},
		},
	}}
	if err := ValidateStrict(p, nil, nil); err != nil {
		t.Fatalf("want ok, got: %v", err)
	}
}

func TestValidateStrict_UndeclaredAccount(t *testing.T) {
	p := mkProject()
	p.Entries = []model.Entry{{
		Date: time.Now(),
		Postings: []model.Posting{
			{Account: "Expenses:Unknown", Amount: model.Amount{Quantity: big.NewRat(5, 1), Commodity: "USD"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-5, 1), Commodity: "USD"}},
		},
	}}
	err := ValidateStrict(p, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Expenses:Unknown") {
		t.Fatalf("want undeclared-account error, got: %v", err)
	}
}

func TestValidateStrict_UndeclaredCommodity(t *testing.T) {
	p := mkProject()
	p.Entries = []model.Entry{{
		Date: time.Now(),
		Postings: []model.Posting{
			{Account: "Expenses:Food", Amount: model.Amount{Quantity: big.NewRat(1, 1), Commodity: "BTC"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-50000, 1), Commodity: "USD"}},
		},
	}}
	err := ValidateStrict(p, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "BTC") {
		t.Fatalf("want undeclared-commodity error, got: %v", err)
	}
}

func TestValidateStrict_PriceCommodity(t *testing.T) {
	p := mkProject()
	p.Prices = []model.Price{{
		Date:          time.Now(),
		Commodity:     "ETH",
		QuoteCurrency: "USD",
		Price:         big.NewRat(3000, 1),
	}}
	err := ValidateStrict(p, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "ETH") {
		t.Fatalf("want undeclared-commodity error for price, got: %v", err)
	}
}

func TestValidateStrict_ExtraAllowsImport(t *testing.T) {
	p := mkProject()
	p.Entries = []model.Entry{{
		Date: time.Now(),
		Postings: []model.Posting{
			{Account: "Expenses:Travel", Amount: model.Amount{Quantity: big.NewRat(100, 1), Commodity: "EUR"}},
			{Account: "Assets:Cash", Amount: model.Amount{Quantity: big.NewRat(-100, 1), Commodity: "EUR"}},
		},
	}}
	// Pretend EUR and Expenses:Travel are being declared in the same batch.
	if err := ValidateStrict(p, []string{"Expenses:Travel"}, []string{"EUR"}); err != nil {
		t.Fatalf("want ok with extras, got: %v", err)
	}
}
