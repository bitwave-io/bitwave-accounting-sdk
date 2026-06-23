// Package report implements all ledger-cli style reports against an in-memory
// model.Project. Local-mode `bw ledger <report>` calls these directly; cloud
// mode delegates to gl-svc, which produces the same outputs server-side.
package report

import (
	"bufio"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// Filter narrows reports to a date range, account prefix, and/or cleared
// status. Zero-value fields mean "no filter".
type Filter struct {
	From         time.Time
	To           time.Time
	AccountMatch string // substring match on account names
	ClearedOnly  bool
}

func (f Filter) include(e model.Entry) bool {
	if !f.From.IsZero() && e.Date.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && e.Date.After(f.To) {
		return false
	}
	if f.ClearedOnly && e.Status != model.StatusCleared {
		return false
	}
	return true
}

func (f Filter) includePosting(p model.Posting) bool {
	if f.AccountMatch == "" {
		return true
	}
	return strings.Contains(strings.ToLower(p.Account), strings.ToLower(f.AccountMatch))
}

// Balance prints account balances tree-style. Native commodities are summed
// per-commodity; if a posting has @ pricing, it also contributes to the base
// currency total via the unit-price.
func Balance(w io.Writer, p *model.Project, f Filter) error {
	bw := bufio.NewWriter(w)
	defer func() { _ = bw.Flush() }()

	// Map: account -> commodity -> sum
	tot := make(map[string]map[string]*big.Rat)
	for _, e := range p.Entries {
		if !f.include(e) {
			continue
		}
		for _, post := range e.Postings {
			if !f.includePosting(post) {
				continue
			}
			if post.Amount.Quantity == nil {
				continue
			}
			byC, ok := tot[post.Account]
			if !ok {
				byC = map[string]*big.Rat{}
				tot[post.Account] = byC
			}
			c := post.Amount.Commodity
			if byC[c] == nil {
				byC[c] = new(big.Rat)
			}
			byC[c].Add(byC[c], post.Amount.Quantity)
		}
	}

	accs := make([]string, 0, len(tot))
	for a := range tot {
		accs = append(accs, a)
	}
	sort.Strings(accs)

	rollups := rollupTotals(tot)
	for _, a := range accs {
		printBalanceLine(bw, a, tot[a])
	}
	if len(accs) > 0 {
		_, _ = fmt.Fprintln(bw, strings.Repeat("-", 60))
		printBalanceLine(bw, "", rollups)
	}
	return nil
}

// rollupTotals sums every commodity across every account.
func rollupTotals(tot map[string]map[string]*big.Rat) map[string]*big.Rat {
	out := map[string]*big.Rat{}
	for _, byC := range tot {
		for c, q := range byC {
			if out[c] == nil {
				out[c] = new(big.Rat)
			}
			out[c].Add(out[c], q)
		}
	}
	return out
}

func printBalanceLine(w io.Writer, account string, byC map[string]*big.Rat) {
	commodities := make([]string, 0, len(byC))
	for c := range byC {
		commodities = append(commodities, c)
	}
	sort.Strings(commodities)
	for i, c := range commodities {
		amt := model.Amount{Quantity: byC[c], Commodity: c}
		label := ""
		if i == 0 {
			label = account
		}
		_, _ = fmt.Fprintf(w, "%20s  %s\n", formatAmount(amt), label)
	}
}

func formatAmount(a model.Amount) string {
	if a.Quantity == nil {
		return ""
	}
	q := new(big.Rat).Set(a.Quantity)
	neg := q.Sign() < 0
	if neg {
		q.Neg(q)
	}
	num := q.FloatString(2)
	sign := ""
	if neg {
		sign = "-"
	}
	if a.Commodity == "USD" {
		return fmt.Sprintf("%s$%s", sign, num)
	}
	return fmt.Sprintf("%s%s %s", sign, num, a.Commodity)
}

// Register prints postings chronologically with a running balance per
// commodity for the matched account.
func Register(w io.Writer, p *model.Project, f Filter) error {
	bw := bufio.NewWriter(w)
	defer func() { _ = bw.Flush() }()

	type row struct {
		date    time.Time
		payee   string
		account string
		amount  model.Amount
	}
	var rows []row
	for _, e := range p.Entries {
		if !f.include(e) {
			continue
		}
		for _, post := range e.Postings {
			if !f.includePosting(post) {
				continue
			}
			if post.Amount.Quantity == nil {
				continue
			}
			rows = append(rows, row{
				date:    e.Date,
				payee:   e.Payee,
				account: post.Account,
				amount:  post.Amount,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].date.Before(rows[j].date) })

	running := map[string]*big.Rat{}
	for _, r := range rows {
		c := r.amount.Commodity
		if running[c] == nil {
			running[c] = new(big.Rat)
		}
		running[c].Add(running[c], r.amount.Quantity)
		bal := model.Amount{Quantity: new(big.Rat).Set(running[c]), Commodity: c}
		_, _ = fmt.Fprintf(bw, "%s  %-30s  %-30s  %15s  %15s\n",
			r.date.Format("2006-01-02"),
			truncate(r.payee, 30),
			truncate(r.account, 30),
			formatAmount(r.amount),
			formatAmount(bal),
		)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// Print emits the project in canonical ledger format (filtered).
func Print(w io.Writer, p *model.Project, f Filter) error {
	out := &model.Project{
		Name:         p.Name,
		BaseCurrency: p.BaseCurrency,
		Accounts:     p.Accounts,
		Prices:       p.Prices,
	}
	for _, e := range p.Entries {
		if !f.include(e) {
			continue
		}
		// Filter postings if account match set.
		if f.AccountMatch == "" {
			out.Entries = append(out.Entries, e)
			continue
		}
		var keep []model.Posting
		for _, post := range e.Postings {
			if f.includePosting(post) {
				keep = append(keep, post)
			}
		}
		if len(keep) > 0 {
			ne := e
			ne.Postings = keep
			out.Entries = append(out.Entries, ne)
		}
	}
	return format.Print(w, out)
}

// Accounts prints declared + observed accounts.
func Accounts(w io.Writer, p *model.Project, f Filter) error {
	for _, a := range p.AccountNames() {
		if f.AccountMatch != "" && !strings.Contains(strings.ToLower(a), strings.ToLower(f.AccountMatch)) {
			continue
		}
		_, _ = fmt.Fprintln(w, a)
	}
	return nil
}

// Payees prints distinct payees.
func Payees(w io.Writer, p *model.Project) error {
	for _, payee := range p.Payees() {
		_, _ = fmt.Fprintln(w, payee)
	}
	return nil
}

// Commodities prints distinct commodities. Declared commodities (with their
// note / format / flags) are listed first, then any observed-but-undeclared
// symbols. The output is one symbol per line for declared rows that have no
// metadata; declared rows with metadata get an inline `; ...` annotation.
func Commodities(w io.Writer, p *model.Project) error {
	declared := map[string]model.Commodity{}
	for _, c := range p.Commodities {
		declared[c.Symbol] = c
	}
	for _, sym := range p.CommoditySymbols() {
		if c, ok := declared[sym]; ok {
			ann := commodityAnnotation(c)
			if ann != "" {
				_, _ = fmt.Fprintf(w, "%s    ; %s\n", sym, ann)
			} else {
				_, _ = fmt.Fprintln(w, sym)
			}
		} else {
			_, _ = fmt.Fprintln(w, sym)
		}
	}
	return nil
}

// Cleared prints only cleared entries.
func Cleared(w io.Writer, p *model.Project) error {
	return Print(w, p, Filter{ClearedOnly: true})
}

// commodityAnnotation renders the inline `; note ; format $ … ; default ; nomarket`
// blurb for a declared commodity. Used by the Commodities report.
func commodityAnnotation(c model.Commodity) string {
	var parts []string
	if c.IsDefault {
		parts = append(parts, "default")
	}
	if c.NoMarket {
		parts = append(parts, "nomarket")
	}
	if c.Format != "" {
		parts = append(parts, "format="+c.Format)
	}
	if c.Note != "" {
		parts = append(parts, c.Note)
	}
	return strings.Join(parts, " ")
}

// Prices prints all P-directives. Timestamped prices render with their
// timezone offset; date-only prices render with date precision.
func Prices(w io.Writer, p *model.Project) error {
	prices := append([]model.Price(nil), p.Prices...)
	sort.SliceStable(prices, func(i, j int) bool { return prices[i].Date.Before(prices[j].Date) })
	for _, pr := range prices {
		amt := model.Amount{Quantity: pr.Price, Commodity: pr.QuoteCurrency}
		stamp := pr.Date.Format("2006-01-02")
		if pr.HasTime {
			stamp = pr.Date.Format("2006-01-02 15:04:05Z07:00")
		}
		_, _ = fmt.Fprintf(w, "%s  %-8s  %s\n", stamp, pr.Commodity, formatAmount(amt))
	}
	return nil
}

// Stats prints summary counts.
func Stats(w io.Writer, p *model.Project) error {
	entries := len(p.Entries)
	postings := 0
	balanced := 0
	cleared := 0
	pending := 0
	for _, e := range p.Entries {
		postings += len(e.Postings)
		if e.IsBalanced(p.BaseCurrency) {
			balanced++
		}
		switch e.Status {
		case model.StatusCleared:
			cleared++
		case model.StatusPending:
			pending++
		}
	}
	_, _ = fmt.Fprintf(w, "Project:       %s\n", p.Name)
	_, _ = fmt.Fprintf(w, "Base currency: %s\n", p.BaseCurrency)
	_, _ = fmt.Fprintf(w, "Accounts:      %d declared, %d observed\n", len(p.Accounts), len(p.AccountNames()))
	_, _ = fmt.Fprintf(w, "Entries:       %d (%d balanced, %d cleared, %d pending)\n", entries, balanced, cleared, pending)
	_, _ = fmt.Fprintf(w, "Postings:      %d\n", postings)
	_, _ = fmt.Fprintf(w, "Commodities:   %d\n", len(p.CommoditySymbols()))
	_, _ = fmt.Fprintf(w, "Prices:        %d\n", len(p.Prices))
	if entries > 0 {
		first := p.Entries[0].Date
		last := first
		for _, e := range p.Entries {
			if e.Date.Before(first) {
				first = e.Date
			}
			if e.Date.After(last) {
				last = e.Date
			}
		}
		_, _ = fmt.Fprintf(w, "Date range:    %s -> %s\n", first.Format("2006-01-02"), last.Format("2006-01-02"))
	}
	return nil
}

// Equity prints an equity-style snapshot — a single synthetic entry that
// would zero out every account if posted.
func Equity(w io.Writer, p *model.Project, f Filter) error {
	// Compute net balance per (account, commodity)
	tot := make(map[string]map[string]*big.Rat)
	for _, e := range p.Entries {
		if !f.include(e) {
			continue
		}
		for _, post := range e.Postings {
			if post.Amount.Quantity == nil {
				continue
			}
			byC, ok := tot[post.Account]
			if !ok {
				byC = map[string]*big.Rat{}
				tot[post.Account] = byC
			}
			if byC[post.Amount.Commodity] == nil {
				byC[post.Amount.Commodity] = new(big.Rat)
			}
			byC[post.Amount.Commodity].Add(byC[post.Amount.Commodity], post.Amount.Quantity)
		}
	}
	accs := make([]string, 0, len(tot))
	for a := range tot {
		accs = append(accs, a)
	}
	sort.Strings(accs)
	asOf := time.Now()
	if !f.To.IsZero() {
		asOf = f.To
	}
	_, _ = fmt.Fprintf(w, "%s * Opening Balances\n", asOf.Format("2006-01-02"))
	for _, a := range accs {
		commodities := make([]string, 0, len(tot[a]))
		for c := range tot[a] {
			commodities = append(commodities, c)
		}
		sort.Strings(commodities)
		for _, c := range commodities {
			q := tot[a][c]
			if q.Sign() == 0 {
				continue
			}
			_, _ = fmt.Fprintf(w, "    %-36s    %s\n", a, formatAmount(model.Amount{Quantity: q, Commodity: c}))
		}
	}
	_, _ = fmt.Fprintf(w, "    %-36s\n", "Equity:Opening-Balances")
	return nil
}

// CSVPrint emits the postings as CSV (date,payee,account,amount,commodity).
func CSVPrint(w io.Writer, p *model.Project, f Filter) error {
	bw := bufio.NewWriter(w)
	defer func() { _ = bw.Flush() }()
	_, _ = fmt.Fprintln(bw, "date,payee,status,account,amount,commodity,unit_price,unit_price_commodity")
	for _, e := range p.Entries {
		if !f.include(e) {
			continue
		}
		for _, post := range e.Postings {
			if !f.includePosting(post) {
				continue
			}
			amt := ""
			com := ""
			if post.Amount.Quantity != nil {
				amt = post.Amount.Quantity.FloatString(8)
				com = post.Amount.Commodity
			}
			up := ""
			upc := ""
			if post.UnitPrice != nil {
				up = post.UnitPrice.Quantity.FloatString(8)
				upc = post.UnitPrice.Commodity
			}
			_, _ = fmt.Fprintf(bw, "%s,%s,%s,%s,%s,%s,%s,%s\n",
				e.Date.Format("2006-01-02"),
				csvEscape(e.Payee),
				e.Status.Flag(),
				csvEscape(post.Account),
				amt, com,
				up, upc,
			)
		}
	}
	return nil
}

func csvEscape(s string) string {
	if !strings.ContainsAny(s, ",\"\n") {
		return s
	}
	return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
}
