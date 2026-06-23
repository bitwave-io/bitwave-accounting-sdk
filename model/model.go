// Package model defines the in-memory domain types for a plain-text ledger
// project. The shapes match the ledger-cli grammar closely so parser and
// printer can round-trip without information loss.
package model

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// Status is the cleared/pending state of an entry or posting.
type Status int

const (
	StatusUncleared Status = iota
	StatusPending
	StatusCleared
)

func (s Status) Flag() string {
	switch s {
	case StatusPending:
		return "!"
	case StatusCleared:
		return "*"
	default:
		return ""
	}
}

func ParseStatus(s string) (Status, error) {
	switch strings.ToLower(s) {
	case "", "uncleared":
		return StatusUncleared, nil
	case "pending", "!":
		return StatusPending, nil
	case "cleared", "*":
		return StatusCleared, nil
	}
	return 0, fmt.Errorf("invalid status %q (want uncleared|pending|cleared)", s)
}

// AccountType matches PTA conventions and gl-svc canonical types.
type AccountType string

const (
	AccountAsset     AccountType = "ASSET"
	AccountLiability AccountType = "LIABILITY"
	AccountEquity    AccountType = "EQUITY"
	AccountIncome    AccountType = "INCOME"
	AccountExpense   AccountType = "EXPENSE"
)

// InferAccountType guesses an account type from its top-level segment.
// Mirrors ledger-cli convention: Assets:*, Liabilities:*, Equity:*, Income:*,
// Expenses:*. Returns empty if it can't tell.
func InferAccountType(name string) AccountType {
	top := strings.ToLower(strings.SplitN(name, ":", 2)[0])
	switch top {
	case "assets", "asset":
		return AccountAsset
	case "liabilities", "liability":
		return AccountLiability
	case "equity":
		return AccountEquity
	case "income", "revenue", "revenues":
		return AccountIncome
	case "expenses", "expense":
		return AccountExpense
	}
	return ""
}

// Account is a declared account (full colon-path name).
//
// Tags is structured metadata parsed from inline `; key:value` comments on
// the account directive (e.g. `wallet:wlt_… address:0x… network:base`).
// Free-form prose without a `key:value` shape stays in Note.
type Account struct {
	Name string
	Type AccountType
	Note string
	Tags map[string]string
}

// Amount is a signed quantity in a specific commodity. Positive = debit,
// negative = credit. Mirrors ledger-cli's signed-amount convention; balance
// reports sum these directly.
type Amount struct {
	Quantity  *big.Rat
	Commodity string // "USD", "EUR", "BTC", ...
}

func (a Amount) IsZero() bool { return a.Quantity == nil || a.Quantity.Sign() == 0 }

// Posting is one leg of an Entry (the indented line).
type Posting struct {
	Account string
	Amount  Amount

	// UnitPrice, if set, is the per-unit cost in the entry's base currency
	// — written as `@ $price` in ledger format.
	UnitPrice *Amount

	// Status overrides the parent entry status when non-nil.
	Status *Status

	Note string
}

// BaseAmount returns the posting's value in the project base currency, using
// UnitPrice when the commodity differs from the base. Returns the raw quantity
// when UnitPrice is nil.
func (p Posting) BaseAmount(baseCurrency string) *big.Rat {
	if p.Amount.Quantity == nil {
		return new(big.Rat)
	}
	if p.UnitPrice == nil || p.Amount.Commodity == baseCurrency {
		return new(big.Rat).Set(p.Amount.Quantity)
	}
	return new(big.Rat).Mul(p.Amount.Quantity, p.UnitPrice.Quantity)
}

// Entry is a single transaction with its postings.
//
// Code is an optional short identifier that follows the date and status in
// ledger/hledger entry headers, e.g. `2024-01-15 * (CHK#1234) Acme Corp`.
// It's commonly used for check numbers, invoice references, or transaction
// hashes. Empty when no code is present.
type Entry struct {
	ID       string
	Date     time.Time
	Code     string
	Payee    string
	Note     string
	Status   Status
	Postings []Posting
}

// Balance returns the entry's signed sum in the base currency. A balanced
// entry has Balance == 0.
func (e *Entry) Balance(baseCurrency string) *big.Rat {
	sum := new(big.Rat)
	for _, p := range e.Postings {
		sum.Add(sum, p.BaseAmount(baseCurrency))
	}
	return sum
}

// IsBalanced reports whether the entry's postings sum to zero in the base
// currency. Allows one elided posting (zero quantity, no commodity) which is
// auto-filled by the printer/parser.
func (e *Entry) IsBalanced(baseCurrency string) bool {
	return e.Balance(baseCurrency).Sign() == 0
}

// Commodity is a declared commodity (asset symbol). Mirrors the ledger-cli
// `commodity SYMBOL` directive with the subset of subdirectives that have
// well-defined semantics in our model:
//
//		commodity USD
//		    note American Dollars
//		    format $1,000.00
//		    nomarket
//		    default
//
//	  - Note is free-form prose.
//	  - Format is an example string showing the user's preferred rendering;
//	    captured for round-trip but not consulted by the printer in this
//	    version.
//	  - NoMarket marks the commodity as one that should not participate in
//	    market-price valuation (-V / -X in ledger-cli). We don't ship those
//	    reports yet, but the flag is preserved so it round-trips.
//	  - IsDefault marks this as the project's default commodity. Strict mode
//	    warns when the project's BaseCurrency disagrees.
type Commodity struct {
	Symbol    string
	Note      string
	Format    string
	NoMarket  bool
	IsDefault bool
}

// Price is a P-directive price observation.
//
// Date is the price observation timestamp. When HasTime is true the timestamp
// includes a wall-clock time (and is always timezone-aware — crypto pricing
// without a timezone is meaningless). When HasTime is false the price is
// recorded with date precision only and Date is set to midnight UTC.
//
// HasTime exists solely to drive round-trip rendering: a parser that read
// `P 2024-06-21 BTC $50000` round-trips back to the same line rather than
// the noisier `P 2024-06-21 00:00:00Z BTC $50000`.
type Price struct {
	Date          time.Time
	HasTime       bool
	Commodity     string
	QuoteCurrency string
	Price         *big.Rat
}

// Project is the parsed in-memory view of a ledger project.
type Project struct {
	Name         string
	BaseCurrency string
	Accounts     []Account
	Commodities  []Commodity
	Entries      []Entry
	Prices       []Price
}

// SortEntries orders entries by date then insertion-order.
func (p *Project) SortEntries() {
	sort.SliceStable(p.Entries, func(i, j int) bool {
		return p.Entries[i].Date.Before(p.Entries[j].Date)
	})
}

// AccountNames returns the union of declared accounts and accounts referenced
// by postings.
func (p *Project) AccountNames() []string {
	seen := map[string]bool{}
	for _, a := range p.Accounts {
		seen[a.Name] = true
	}
	for _, e := range p.Entries {
		for _, post := range e.Postings {
			seen[post.Account] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// CommoditySymbols returns distinct commodity symbols seen in declarations,
// postings, and prices. Use Project.Commodities directly for the declared
// commodity records (with subdirectives) and this method for the union with
// observed-but-undeclared symbols.
func (p *Project) CommoditySymbols() []string {
	seen := map[string]bool{}
	for _, c := range p.Commodities {
		if c.Symbol != "" {
			seen[c.Symbol] = true
		}
	}
	for _, e := range p.Entries {
		for _, post := range e.Postings {
			if post.Amount.Commodity != "" {
				seen[post.Amount.Commodity] = true
			}
		}
	}
	for _, pr := range p.Prices {
		seen[pr.Commodity] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Payees returns distinct entry payees.
func (p *Project) Payees() []string {
	seen := map[string]bool{}
	for _, e := range p.Entries {
		if e.Payee != "" {
			seen[e.Payee] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
