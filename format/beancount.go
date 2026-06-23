package format

import (
	"bufio"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// ErrUnsupportedDirective is returned when the beancount shim encounters a
// directive (or syntax element) we deliberately don't translate to our
// internal model. Tests can inspect the line number and directive name to
// decide whether to skip or assert-on-error.
type ErrUnsupportedDirective struct {
	Line      int
	Directive string
	Reason    string
}

func (e *ErrUnsupportedDirective) Error() string {
	return fmt.Sprintf("line %d: unsupported beancount directive %q: %s",
		e.Line, e.Directive, e.Reason)
}

// parseBeancountInto reads a beancount-flavored journal and appends to p.
//
// What we accept (and how it maps to our internal model):
//
//	open <ACCT> [CCY[,CCY...]]   -> account declaration
//	close <ACCT>                 -> account declaration (idempotent; close is noop)
//	commodity <SYM>              -> commodity declaration
//	price <SYM> <amount> <CCY>   -> P directive
//	* "Payee" "Narration"        -> entry (Payee from the second string,
//	                                or the first if only one is present)
//	! "Payee" "Narration"        -> entry with pending status
//	<ACCT>   <amount> <CCY>      -> posting (indented under a transaction)
//
// What we deliberately reject (with ErrUnsupportedDirective so the harness
// can choose to skip the fixture or assert-on-error):
//
//	pad, balance, note, document, query, event, custom
//	option, plugin, pushtag, poptag, include
//	{cost basis} / {{total cost}} annotations on postings
//	metadata key:"value" sub-lines under directives
//	tag/link tokens #foo / ^bar
//
// Inline comments use `;` like ledger. Lines starting with `*` at column 0
// (org-mode headers) and lines starting with `;` are skipped.
func parseBeancountInto(r io.Reader, p *model.Project, ctx parseCtx) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		lineNo  int
		current *model.Entry
	)

	flush := func() {
		if current != nil {
			p.Entries = append(p.Entries, *current)
			current = nil
		}
	}

	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		trimmed := strings.TrimRight(raw, " \t")
		if trimmed == "" {
			flush()
			continue
		}
		// Beancount inline comment: `;` (single or doubled).
		leftTrimmed := strings.TrimLeft(trimmed, " \t")
		if strings.HasPrefix(leftTrimmed, ";") {
			flush()
			continue
		}
		// Org-mode header at column 0 — beancount's example files use these
		// to group sections; they're ignored by bean-check.
		if len(raw) > 0 && raw[0] == '*' && (len(raw) == 1 || raw[1] == ' ') {
			flush()
			continue
		}

		// Strip inline `;` comments from the line body before parsing.
		body, _ := splitInlineComment(trimmed)
		body = strings.TrimRight(body, " \t")

		indented := raw[0] == ' ' || raw[0] == '\t'
		if indented {
			// Indented line inside a transaction: a posting, OR a metadata
			// `key: "value"` sub-line, OR a continuation comment.
			content := strings.TrimLeft(body, " \t")
			if content == "" {
				continue
			}
			// Metadata sub-lines look like `key: "value"`. Skip silently —
			// we don't model arbitrary metadata.
			if isMetadataLine(content) {
				continue
			}
			if current == nil {
				return fmt.Errorf("line %d: posting without transaction header", lineNo)
			}
			post, err := parseBeancountPosting(content)
			if err != nil {
				return fmt.Errorf("line %d: %w", lineNo, err)
			}
			current.Postings = append(current.Postings, post)
			continue
		}

		// Column-0 directive. Tokenize.
		fields := strings.Fields(body)
		if len(fields) < 2 {
			return fmt.Errorf("line %d: malformed directive %q", lineNo, body)
		}

		// Two shapes: `<DATE> <KEYWORD> ...` (most directives) or
		// `<KEYWORD> ...` (option / plugin / include / pushtag / poptag).
		first := fields[0]
		if _, err := parseFlexibleDate(first); err != nil {
			// Non-dated keyword directive — almost all unsupported.
			return &ErrUnsupportedDirective{
				Line:      lineNo,
				Directive: first,
				Reason:    "non-dated directives (option, plugin, include, pushtag, poptag, etc.) are not modeled by the shim",
			}
		}

		date, _ := parseFlexibleDate(first)
		if len(fields) < 2 {
			return fmt.Errorf("line %d: directive missing keyword", lineNo)
		}
		keyword := fields[1]
		args := fields[2:]
		flush()

		switch keyword {
		case "open":
			if len(args) < 1 {
				return fmt.Errorf("line %d: open requires an account", lineNo)
			}
			p.Accounts = append(p.Accounts, model.Account{
				Name: args[0],
				Type: model.InferAccountType(args[0]),
			})

		case "close":
			// We don't model account-close as an explicit event. Treat as
			// a no-op declaration (we still register the account name).
			if len(args) < 1 {
				return fmt.Errorf("line %d: close requires an account", lineNo)
			}
			p.Accounts = append(p.Accounts, model.Account{
				Name: args[0],
				Type: model.InferAccountType(args[0]),
			})

		case "commodity":
			if len(args) < 1 {
				return fmt.Errorf("line %d: commodity requires a symbol", lineNo)
			}
			p.Commodities = append(p.Commodities, model.Commodity{Symbol: args[0]})

		case "price":
			if len(args) < 3 {
				return fmt.Errorf("line %d: price needs SYM AMOUNT CCY", lineNo)
			}
			amt, err := parseAmount(args[1] + " " + args[2])
			if err != nil {
				return fmt.Errorf("line %d: price amount: %w", lineNo, err)
			}
			p.Prices = append(p.Prices, model.Price{
				Date:          date,
				Commodity:     args[0],
				QuoteCurrency: amt.Commodity,
				Price:         amt.Quantity,
			})

		case "*", "!", "txn":
			// Transaction header. Beancount transaction args are:
			//   [code-or-payee-string] [narration-string] [#tag ^link ...]
			// Both strings are optional. When only one quoted string is
			// present, it's the narration; when two are present, the first
			// is the payee.
			status := model.StatusCleared
			if keyword == "!" {
				status = model.StatusPending
			}
			payee, _ := extractBeancountStrings(fields[2:])
			current = &model.Entry{
				Date:   date,
				Status: status,
				Payee:  payee,
			}

		case "balance":
			// Balance assertions aren't enforced by our model — flag as
			// unsupported so the fixture's .expect sidecar can opt into
			// must_fail.
			return &ErrUnsupportedDirective{
				Line:      lineNo,
				Directive: "balance",
				Reason:    "balance assertions are not modeled",
			}

		case "pad", "note", "document", "query", "event", "custom":
			return &ErrUnsupportedDirective{
				Line:      lineNo,
				Directive: keyword,
				Reason:    "directive not modeled by the shim",
			}

		default:
			return &ErrUnsupportedDirective{
				Line:      lineNo,
				Directive: keyword,
				Reason:    "unknown beancount directive",
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	flush()
	return nil
}

// extractBeancountStrings pulls the two optional `"..."` arguments from a
// transaction header. Returns (payee, narration). Beancount semantics:
//   - one string  -> narration only, payee is the narration
//   - two strings -> first is payee, second is narration; we use payee as our Payee
func extractBeancountStrings(tokens []string) (payee, narration string) {
	var strs []string
	// Re-join then re-scan: tokens may have been split on whitespace inside
	// a quoted string. Walk character-by-character.
	src := strings.Join(tokens, " ")
	for i := 0; i < len(src); {
		if src[i] == '"' {
			end := strings.IndexByte(src[i+1:], '"')
			if end < 0 {
				break
			}
			strs = append(strs, src[i+1:i+1+end])
			i = i + 1 + end + 1
			continue
		}
		i++
	}
	switch len(strs) {
	case 0:
		return "", ""
	case 1:
		return strs[0], ""
	default:
		return strs[0], strs[1]
	}
}

// parseBeancountPosting handles the indented body of a posting line:
//
//	Assets:US:BofA:Checking   3971.01 USD
//	Assets:Crypto:BTC         1 BTC {50000 USD}
//	Assets:US:BofA:Checking   -2400.00 USD @ 1.5 EUR
//
// We extract the account, the numeric amount, and its commodity. Cost-basis
// annotations `{...}` and lot-price `@`/`@@` are tolerated (parsed and
// dropped from the amount string) but not modeled.
func parseBeancountPosting(content string) (model.Posting, error) {
	post := model.Posting{}
	// Extract cost-basis `{cost CCY}` annotation if present. Beancount uses
	// this as the lot cost; for our purposes it doubles as an @-style unit
	// price so the entry balances in the base currency. We don't model lot
	// tracking, but we DO preserve the cost as UnitPrice.
	var costStr string
	var costIsTotal bool
	if open := strings.Index(content, "{"); open >= 0 {
		// Detect `{{TOTAL CCY}}` (total-cost form) — both braces doubled.
		if open+1 < len(content) && content[open+1] == '{' {
			costIsTotal = true
			endIdx := strings.Index(content[open:], "}}")
			if endIdx > 0 {
				inner := content[open+2 : open+endIdx]
				costStr = strings.TrimSpace(inner)
				content = strings.TrimSpace(content[:open]) + " " + strings.TrimSpace(content[open+endIdx+2:])
			}
		} else {
			closeIdx := strings.Index(content[open:], "}")
			if closeIdx > 0 {
				inner := content[open+1 : open+closeIdx]
				costStr = strings.TrimSpace(inner)
				content = strings.TrimSpace(content[:open]) + " " + strings.TrimSpace(content[open+closeIdx+1:])
			}
		}
	}

	// Extract optional `@ price` / `@@ totalprice` and stash the price.
	var priceStr string
	var priceIsTotal bool
	if idx := strings.Index(content, "@"); idx >= 0 {
		amt := strings.TrimSpace(content[:idx])
		rest := content[idx+1:]
		if len(rest) > 0 && rest[0] == '@' {
			priceIsTotal = true
			rest = rest[1:]
		}
		priceStr = strings.TrimSpace(rest)
		content = amt
	}

	// Now content is "Account  AMOUNT CCY". Split on last whitespace token
	// boundary: numeric + commodity at the end.
	fields := strings.Fields(content)
	if len(fields) < 1 {
		return post, fmt.Errorf("empty posting")
	}
	if len(fields) == 1 {
		// Account-only line — elided amount.
		post.Account = fields[0]
		return post, nil
	}
	// The amount is the last 2 tokens (number, commodity) OR a single token
	// that looks like a signed number followed by a commodity glued
	// together (rare). Assume 2 tokens.
	if len(fields) < 3 {
		return post, fmt.Errorf("posting %q: expected `ACCOUNT AMOUNT CCY`", content)
	}
	post.Account = strings.Join(fields[:len(fields)-2], " ")
	amt, err := parseAmount(fields[len(fields)-2] + " " + fields[len(fields)-1])
	if err != nil {
		return post, fmt.Errorf("posting amount: %w", err)
	}
	post.Amount = amt
	// Cost basis takes precedence over @-price for our balance math (Beancount
	// semantics: the lot's cost is what affects equity).
	switch {
	case costStr != "":
		price, err := parseAmount(costStr)
		if err != nil {
			return post, fmt.Errorf("posting cost basis: %w", err)
		}
		if costIsTotal {
			if amt.Quantity == nil || amt.Quantity.Sign() == 0 {
				return post, fmt.Errorf("posting {{total cost}} requires non-zero quantity")
			}
			absQty := new(big.Rat).Abs(amt.Quantity)
			price.Quantity = new(big.Rat).Quo(price.Quantity, absQty)
		}
		post.UnitPrice = &price
	case priceStr != "":
		price, err := parseAmount(priceStr)
		if err != nil {
			return post, fmt.Errorf("posting price: %w", err)
		}
		if priceIsTotal {
			if amt.Quantity == nil || amt.Quantity.Sign() == 0 {
				return post, fmt.Errorf("posting @@ total price requires non-zero quantity")
			}
			absQty := new(big.Rat).Abs(amt.Quantity)
			price.Quantity = new(big.Rat).Quo(price.Quantity, absQty)
		}
		post.UnitPrice = &price
	}
	return post, nil
}

// isMetadataLine reports whether a posting-indented content line looks like a
// beancount metadata key/value pair (e.g. `name: "US Dollar"`). We skip these
// silently because we don't model arbitrary metadata.
func isMetadataLine(content string) bool {
	// Must contain `:` followed by whitespace, and must not start with a
	// digit (which would be an amount).
	if content == "" || (content[0] >= '0' && content[0] <= '9') {
		return false
	}
	colon := strings.IndexByte(content, ':')
	if colon <= 0 || colon+1 >= len(content) {
		return false
	}
	// `Assets:Cash` looks like this too — distinguish by requiring a space
	// after the colon (metadata has `key: "value"`, account paths don't).
	if content[colon+1] == ' ' || content[colon+1] == '\t' {
		// Now make sure the key portion is identifier-shaped (no further
		// `:` — accounts contain `:`).
		key := content[:colon]
		return !strings.ContainsAny(key, ":/\\ ")
	}
	return false
}
