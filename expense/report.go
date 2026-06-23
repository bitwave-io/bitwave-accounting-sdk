package expense

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// Filter selects which entries land in a report.
type Filter struct {
	ReportId string
	From, To time.Time
}

// Line is one row in the rendered report — flattens an entry down to its
// expense-side posting (the debit into an Expenses:* / Liabilities:*
// account). Postings that net to zero or non-expense legs are ignored.
type Line struct {
	Date         time.Time
	Payee        string
	Merchant     string
	Account      string
	Category     string
	Amount       *big.Rat
	Currency     string
	Reimbursable bool
	Note         string
	EntryId      string
}

// Totals summarises a Report. Grand / Reimbursable / NonReimbursable are
// only meaningful for single-currency reports — for multi-currency reports
// consumers should walk ByCurrency / ReimbursableByCurrency / etc.
type Totals struct {
	Grand                     *big.Rat
	Reimbursable              *big.Rat
	NonReimbursable           *big.Rat
	ByCategory                map[string]*big.Rat
	ByCurrency                map[string]*big.Rat
	ReimbursableByCurrency    map[string]*big.Rat
	NonReimbursableByCurrency map[string]*big.Rat
}

// Report is the fully-built, render-ready view.
type Report struct {
	ReportId    string
	From, To    time.Time
	GeneratedAt time.Time
	Lines       []Line
	Totals      Totals
}

// Build filters entries to those tagged expense-report:<ReportId> within the
// optional date window and emits a Report.
func Build(entries []model.Entry, f Filter) (Report, error) {
	r := Report{
		ReportId:    f.ReportId,
		From:        f.From,
		To:          f.To,
		GeneratedAt: time.Now().UTC(),
		Totals: Totals{
			Grand:                     new(big.Rat),
			Reimbursable:              new(big.Rat),
			NonReimbursable:           new(big.Rat),
			ByCategory:                map[string]*big.Rat{},
			ByCurrency:                map[string]*big.Rat{},
			ReimbursableByCurrency:    map[string]*big.Rat{},
			NonReimbursableByCurrency: map[string]*big.Rat{},
		},
	}

	hasFrom := !f.From.IsZero()
	hasTo := !f.To.IsZero()

	for _, e := range entries {
		if !HasReport(e.Note, f.ReportId) {
			continue
		}
		if hasFrom && e.Date.Before(f.From) {
			continue
		}
		if hasTo && e.Date.After(f.To) {
			continue
		}
		merchant := MerchantFromNote(e.Note)
		if merchant == "" {
			merchant = e.Payee
		}
		reimb := ReimbursableFromNote(e.Note)
		note := stripTags(e.Note)

		// Pick the expense leg: the first positive (debit) posting into an
		// Expenses:* or Liabilities:* account. Falls back to the first
		// positive posting if none match the prefix.
		leg, ok := pickExpenseLeg(e.Postings)
		if !ok {
			continue
		}
		amt := new(big.Rat).Set(leg.Amount.Quantity)
		category := lastSegment(leg.Account)
		line := Line{
			Date:         e.Date,
			Payee:        e.Payee,
			Merchant:     merchant,
			Account:      leg.Account,
			Category:     category,
			Amount:       amt,
			Currency:     leg.Amount.Commodity,
			Reimbursable: reimb,
			Note:         note,
			EntryId:      e.ID,
		}
		r.Lines = append(r.Lines, line)
		r.Totals.Grand.Add(r.Totals.Grand, amt)
		if reimb {
			r.Totals.Reimbursable.Add(r.Totals.Reimbursable, amt)
		} else {
			r.Totals.NonReimbursable.Add(r.Totals.NonReimbursable, amt)
		}
		bucket := r.Totals.ByCategory[category]
		if bucket == nil {
			bucket = new(big.Rat)
			r.Totals.ByCategory[category] = bucket
		}
		bucket.Add(bucket, amt)
		if leg.Amount.Commodity != "" {
			addToBucket(r.Totals.ByCurrency, leg.Amount.Commodity, amt)
			if reimb {
				addToBucket(r.Totals.ReimbursableByCurrency, leg.Amount.Commodity, amt)
			} else {
				addToBucket(r.Totals.NonReimbursableByCurrency, leg.Amount.Commodity, amt)
			}
		}
	}
	sort.SliceStable(r.Lines, func(i, j int) bool { return r.Lines[i].Date.Before(r.Lines[j].Date) })
	return r, nil
}

func addToBucket(m map[string]*big.Rat, key string, amt *big.Rat) {
	b := m[key]
	if b == nil {
		b = new(big.Rat)
		m[key] = b
	}
	b.Add(b, amt)
}

func pickExpenseLeg(postings []model.Posting) (model.Posting, bool) {
	var firstPositive *model.Posting
	for i := range postings {
		p := postings[i]
		if p.Amount.Quantity == nil || p.Amount.Quantity.Sign() <= 0 {
			continue
		}
		if strings.HasPrefix(p.Account, "Expenses:") || strings.HasPrefix(p.Account, "Liabilities:") {
			return p, true
		}
		if firstPositive == nil {
			firstPositive = &p
		}
	}
	if firstPositive != nil {
		return *firstPositive, true
	}
	return model.Posting{}, false
}

func lastSegment(account string) string {
	i := strings.LastIndex(account, ":")
	if i < 0 || i == len(account)-1 {
		return account
	}
	return account[i+1:]
}

// stripTags removes "key:value" tokens from a note and returns the free-form
// remainder. Used to surface the human-readable portion to renderers.
func stripTags(note string) string {
	s := strings.TrimSpace(note)
	s = strings.TrimPrefix(s, ";")
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if _, _, ok := splitTag(p); ok {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}

// ---------- Renderers ----------

func RenderText(out io.Writer, r Report) error {
	_, _ = fmt.Fprintf(out, "Expense Report: %s\n", r.ReportId)
	if !r.From.IsZero() || !r.To.IsZero() {
		_, _ = fmt.Fprintf(out, "Period:         %s to %s\n", fmtDateOrAll(r.From), fmtDateOrAll(r.To))
	}
	_, _ = fmt.Fprintf(out, "Generated:      %s\n\n", r.GeneratedAt.Format("2006-01-02"))

	colDate, colMerchant, colCat, colAcct, colAmt := 10, 16, 9, 21, 8
	for _, l := range r.Lines {
		if w := len(l.Merchant); w > colMerchant {
			colMerchant = w
		}
		if w := len(l.Category); w > colCat {
			colCat = w
		}
		if w := len(l.Account); w > colAcct {
			colAcct = w
		}
		if w := len(fmtMoney(l.Amount, l.Currency)); w > colAmt {
			colAmt = w
		}
	}

	hdr := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %*s  %s",
		colDate, "Date",
		colMerchant, "Merchant",
		colCat, "Category",
		colAcct, "Account",
		colAmt, "Amount",
		"Reimb")
	_, _ = fmt.Fprintln(out, hdr)
	_, _ = fmt.Fprintln(out, strings.Repeat("-", len(hdr)))

	for _, l := range r.Lines {
		reimb := "N"
		if l.Reimbursable {
			reimb = "Y"
		}
		_, _ = fmt.Fprintf(out, "%-*s  %-*s  %-*s  %-*s  %*s  %s\n",
			colDate, l.Date.Format("2006-01-02"),
			colMerchant, l.Merchant,
			colCat, l.Category,
			colAcct, l.Account,
			colAmt, fmtMoney(l.Amount, l.Currency),
			reimb)
	}

	_, _ = fmt.Fprintln(out, strings.Repeat("-", len(hdr)))
	labelWidth := colDate + colMerchant + colCat + colAcct + 6

	currencies := sortedCurrencyKeys(r.Totals.ByCurrency)
	switch len(currencies) {
	case 0:
		// No lines at all — emit zero totals in the workspace base format
		// so existing single-currency callers still see a Subtotal line.
		_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Subtotal", colAmt, fmtMoneyPlain(r.Totals.Grand))
		_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Reimbursable", colAmt, fmtMoneyPlain(r.Totals.Reimbursable))
		_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Company-funded", colAmt, fmtMoneyPlain(r.Totals.NonReimbursable))
	case 1:
		cur := currencies[0]
		_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Subtotal", colAmt, fmtMoney(r.Totals.ByCurrency[cur], cur))
		_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Reimbursable", colAmt, fmtMoney(getOrZero(r.Totals.ReimbursableByCurrency, cur), cur))
		_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Company-funded", colAmt, fmtMoney(getOrZero(r.Totals.NonReimbursableByCurrency, cur), cur))
	default:
		// Multi-currency: one line per currency so the totals stay
		// meaningful instead of summing apples and oranges.
		for _, cur := range currencies {
			_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Subtotal ("+cur+")", colAmt, fmtMoney(r.Totals.ByCurrency[cur], cur))
		}
		for _, cur := range currencies {
			if v, ok := r.Totals.ReimbursableByCurrency[cur]; ok && v.Sign() != 0 {
				_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Reimbursable ("+cur+")", colAmt, fmtMoney(v, cur))
			}
		}
		for _, cur := range currencies {
			if v, ok := r.Totals.NonReimbursableByCurrency[cur]; ok && v.Sign() != 0 {
				_, _ = fmt.Fprintf(out, "%*s  %*s\n", labelWidth, "Company-funded ("+cur+")", colAmt, fmtMoney(v, cur))
			}
		}
	}
	return nil
}

func sortedCurrencyKeys(m map[string]*big.Rat) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func getOrZero(m map[string]*big.Rat, key string) *big.Rat {
	if v, ok := m[key]; ok {
		return v
	}
	return new(big.Rat)
}

func fmtDateOrAll(t time.Time) string {
	if t.IsZero() {
		return "(open)"
	}
	return t.Format("2006-01-02")
}

func fmtMoney(amt *big.Rat, currency string) string {
	if amt == nil {
		return ""
	}
	v := fmtRat(amt)
	switch strings.ToUpper(currency) {
	case "USD", "":
		return "$" + v
	default:
		return v + " " + currency
	}
}

func fmtMoneyPlain(amt *big.Rat) string {
	if amt == nil {
		return "$0.00"
	}
	return "$" + fmtRat(amt)
}

func fmtRat(amt *big.Rat) string {
	return amt.FloatString(2)
}

// RenderCSV writes RFC-4180 CSV. encoding/csv handles quoting.
func RenderCSV(out io.Writer, r Report) error {
	w := csv.NewWriter(out)
	if err := w.Write([]string{"Date", "Merchant", "Category", "Account", "Amount", "Currency", "Reimbursable", "Note", "EntryId"}); err != nil {
		return err
	}
	for _, l := range r.Lines {
		reimb := "false"
		if l.Reimbursable {
			reimb = "true"
		}
		// Use Payee in the Merchant column when we have one — agents
		// reading the CSV want the human-recognizable vendor name, not the
		// underscore-encoded tag value.
		merchant := l.Merchant
		if merchant == "" {
			merchant = l.Payee
		}
		// If the tag-encoded merchant matches Payee character-for-character
		// after decoding we prefer Payee (preserves original spacing /
		// punctuation).
		if merchant == l.Merchant && l.Payee != "" && strings.EqualFold(l.Merchant, l.Payee) {
			merchant = l.Payee
		}
		if err := w.Write([]string{
			l.Date.Format("2006-01-02"),
			merchant,
			l.Category,
			l.Account,
			l.Amount.FloatString(2),
			l.Currency,
			reimb,
			l.Note,
			l.EntryId,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// RenderQIF emits a minimal QIF !Type:Cash block. One ^-terminated record
// per Line. T is negated (QIF cash records expect outflow as negative).
func RenderQIF(out io.Writer, r Report) error {
	if _, err := fmt.Fprintln(out, "!Type:Cash"); err != nil {
		return err
	}
	for _, l := range r.Lines {
		neg := new(big.Rat).Neg(l.Amount)
		merchant := l.Merchant
		if merchant == "" {
			merchant = l.Payee
		}
		_, _ = fmt.Fprintf(out, "D%s\n", l.Date.Format("2006-01-02"))
		_, _ = fmt.Fprintf(out, "T%s\n", neg.FloatString(2))
		_, _ = fmt.Fprintf(out, "P%s\n", merchant)
		if l.Note != "" {
			_, _ = fmt.Fprintf(out, "M%s\n", l.Note)
		}
		_, _ = fmt.Fprintf(out, "L%s\n", l.Account)
		_, _ = fmt.Fprintln(out, "^")
	}
	return nil
}

func RenderJSON(out io.Writer, r Report) error {
	type jsonLine struct {
		Date         string `json:"date"`
		Payee        string `json:"payee"`
		Merchant     string `json:"merchant"`
		Account      string `json:"account"`
		Category     string `json:"category"`
		Amount       string `json:"amount"`
		Currency     string `json:"currency,omitempty"`
		Reimbursable bool   `json:"reimbursable"`
		Note         string `json:"note,omitempty"`
		EntryId      string `json:"entryId,omitempty"`
	}
	type jsonTotals struct {
		Grand           string            `json:"grand"`
		Reimbursable    string            `json:"reimbursable"`
		NonReimbursable string            `json:"nonReimbursable"`
		ByCategory      map[string]string `json:"byCategory,omitempty"`
		ByCurrency      map[string]string `json:"byCurrency,omitempty"`
	}
	type jsonReport struct {
		ReportId    string     `json:"reportId"`
		From        string     `json:"from,omitempty"`
		To          string     `json:"to,omitempty"`
		GeneratedAt string     `json:"generatedAt"`
		Lines       []jsonLine `json:"lines"`
		Totals      jsonTotals `json:"totals"`
	}

	doc := jsonReport{
		ReportId:    r.ReportId,
		GeneratedAt: r.GeneratedAt.UTC().Format(time.RFC3339),
	}
	if !r.From.IsZero() {
		doc.From = r.From.Format("2006-01-02")
	}
	if !r.To.IsZero() {
		doc.To = r.To.Format("2006-01-02")
	}
	for _, l := range r.Lines {
		doc.Lines = append(doc.Lines, jsonLine{
			Date:         l.Date.Format("2006-01-02"),
			Payee:        l.Payee,
			Merchant:     l.Merchant,
			Account:      l.Account,
			Category:     l.Category,
			Amount:       l.Amount.FloatString(2),
			Currency:     l.Currency,
			Reimbursable: l.Reimbursable,
			Note:         l.Note,
			EntryId:      l.EntryId,
		})
	}
	doc.Totals = jsonTotals{
		Grand:           r.Totals.Grand.FloatString(2),
		Reimbursable:    r.Totals.Reimbursable.FloatString(2),
		NonReimbursable: r.Totals.NonReimbursable.FloatString(2),
	}
	if len(r.Totals.ByCategory) > 0 {
		doc.Totals.ByCategory = ratMap(r.Totals.ByCategory)
	}
	if len(r.Totals.ByCurrency) > 0 {
		doc.Totals.ByCurrency = ratMap(r.Totals.ByCurrency)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func ratMap(m map[string]*big.Rat) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v.FloatString(2)
	}
	return out
}

func RenderHTML(out io.Writer, r Report) error {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="robots" content="noindex,nofollow">
<title>Expense Report: %s</title>
<style>
 body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 1080px; margin: 2em auto; padding: 0 1em; color: #1a1a1a; }
 h1 { margin: 0 0 .2em; }
 .banner { color: #666; font-size: 0.9em; margin-bottom: 1em; }
 table { width: 100%%; border-collapse: collapse; font-size: 0.92em; }
 th, td { padding: 0.5em 0.6em; border-bottom: 1px solid #eee; text-align: left; }
 th { background: #f6f8fb; font-weight: 600; }
 tr:nth-child(even) td { background: #fbfbfd; }
 td.num { text-align: right; font-variant-numeric: tabular-nums; }
 .totals { margin-top: 1.2em; border: 1px solid #e5e7eb; border-radius: 6px; padding: 0.8em 1em; background: #fafbfd; max-width: 360px; }
 .totals .row { display:flex; justify-content: space-between; padding: 0.15em 0; }
 .totals .grand { font-weight: 600; border-top: 1px solid #e5e7eb; padding-top: 0.4em; margin-top: 0.4em; }
 .yes { color: #058; font-weight: 600; }
 footer { margin-top: 2em; color: #888; font-size: 0.85em; }
 @media print { .banner, footer { color: #444; } body { margin: 0; max-width: none; } }
</style>
</head><body>
<h1>Expense Report: %s</h1>
<div class="banner">Period: %s to %s · Generated %s</div>
<table>
<thead><tr>
<th>Date</th><th>Merchant</th><th>Category</th><th>Account</th>
<th class="num">Amount</th><th>Currency</th><th>Reimbursable</th><th>Note</th>
</tr></thead><tbody>
`,
		html.EscapeString(r.ReportId),
		html.EscapeString(r.ReportId),
		html.EscapeString(fmtDateOrAll(r.From)),
		html.EscapeString(fmtDateOrAll(r.To)),
		html.EscapeString(r.GeneratedAt.Format("2006-01-02")),
	)
	for _, l := range r.Lines {
		reimb := ""
		if l.Reimbursable {
			reimb = `<span class="yes">Yes</span>`
		} else {
			reimb = "No"
		}
		merchant := l.Merchant
		if merchant == "" {
			merchant = l.Payee
		}
		_, _ = fmt.Fprintf(&b,
			`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="num">%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`+"\n",
			html.EscapeString(l.Date.Format("2006-01-02")),
			html.EscapeString(merchant),
			html.EscapeString(l.Category),
			html.EscapeString(l.Account),
			html.EscapeString(l.Amount.FloatString(2)),
			html.EscapeString(l.Currency),
			reimb,
			html.EscapeString(l.Note),
		)
	}
	_, _ = fmt.Fprintf(&b, "</tbody></table>\n<div class=\"totals\">\n")
	currencies := sortedCurrencyKeys(r.Totals.ByCurrency)
	switch len(currencies) {
	case 0:
		_, _ = fmt.Fprintf(&b, ` <div class="row"><span>Reimbursable</span><span>%s</span></div>`+"\n",
			html.EscapeString(fmtMoneyPlain(r.Totals.Reimbursable)))
		_, _ = fmt.Fprintf(&b, ` <div class="row"><span>Company-funded</span><span>%s</span></div>`+"\n",
			html.EscapeString(fmtMoneyPlain(r.Totals.NonReimbursable)))
		_, _ = fmt.Fprintf(&b, ` <div class="row grand"><span>Total</span><span>%s</span></div>`+"\n",
			html.EscapeString(fmtMoneyPlain(r.Totals.Grand)))
	case 1:
		cur := currencies[0]
		_, _ = fmt.Fprintf(&b, ` <div class="row"><span>Reimbursable</span><span>%s</span></div>`+"\n",
			html.EscapeString(fmtMoney(getOrZero(r.Totals.ReimbursableByCurrency, cur), cur)))
		_, _ = fmt.Fprintf(&b, ` <div class="row"><span>Company-funded</span><span>%s</span></div>`+"\n",
			html.EscapeString(fmtMoney(getOrZero(r.Totals.NonReimbursableByCurrency, cur), cur)))
		_, _ = fmt.Fprintf(&b, ` <div class="row grand"><span>Total</span><span>%s</span></div>`+"\n",
			html.EscapeString(fmtMoney(r.Totals.ByCurrency[cur], cur)))
	default:
		// Multi-currency: one row per currency under each label so we never
		// sum apples and oranges.
		for _, cur := range currencies {
			if v, ok := r.Totals.ReimbursableByCurrency[cur]; ok && v.Sign() != 0 {
				_, _ = fmt.Fprintf(&b, ` <div class="row"><span>Reimbursable (%s)</span><span>%s</span></div>`+"\n",
					html.EscapeString(cur), html.EscapeString(fmtMoney(v, cur)))
			}
		}
		for _, cur := range currencies {
			if v, ok := r.Totals.NonReimbursableByCurrency[cur]; ok && v.Sign() != 0 {
				_, _ = fmt.Fprintf(&b, ` <div class="row"><span>Company-funded (%s)</span><span>%s</span></div>`+"\n",
					html.EscapeString(cur), html.EscapeString(fmtMoney(v, cur)))
			}
		}
		for _, cur := range currencies {
			_, _ = fmt.Fprintf(&b, ` <div class="row grand"><span>Total (%s)</span><span>%s</span></div>`+"\n",
				html.EscapeString(cur), html.EscapeString(fmtMoney(r.Totals.ByCurrency[cur], cur)))
		}
	}
	_, _ = fmt.Fprintf(&b, `</div>
<footer>Read-only view served by Bitwave.</footer>
</body></html>`)
	_, err := io.WriteString(out, b.String())
	return err
}
