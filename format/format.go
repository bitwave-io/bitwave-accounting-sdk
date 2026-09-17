// Package format parses and prints the ledger-cli-compatible plain-text
// accounting format. The grammar implemented is intentionally a subset:
//
//	; comment
//	account Assets:Cash
//	account Assets:Cash    ; with description
//	P 2024-01-15 BTC $50000.00
//	2024-01-15 [*|!] Payee  ; optional note
//	    [*|!] Account:Sub    AMOUNT [COMMODITY] [@ UNIT_PRICE]
//	    Account:Sub          ; elided amount (computed from balance)
//
// Amounts can be written `$5.00`, `5.00 USD`, `1 BTC`, or `-5.00 USD`. The `@`
// price annotation is unit-price; `@@` (total price) is not supported.
package format

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

const (
	dateLayout = "2006-01-02"
	// priceTimeLayout matches a timezone-aware time component on a P
	// directive. Crypto pricing without a timezone is meaningless, so the
	// parser refuses naive times — see parsePrice for the rejection path.
	priceTimeLayout = "2006-01-02 15:04:05Z07:00"
)

// parseFlexibleDate accepts any of the three date separators commonly used by
// hledger and ledger-cli: dash, slash, or dot. The canonical form for the
// printer remains ISO (YYYY-MM-DD); this only affects what we *read*.
//
// See: hledger SPEC-journal — date-sep := "-" | "/" | "."
//
//	ledger doc        — "YYYY/MM/DD format is also recognised"
func parseFlexibleDate(s string) (time.Time, error) {
	// Cheap fast path: ISO dates are what our own Print emits, so most
	// inputs hit this branch.
	if t, err := time.Parse(dateLayout, s); err == nil {
		return t, nil
	}
	// Try the alternative separators. We only swap separators if the
	// input has the YYYY?MM?DD shape (4-digit-prefix), so we don't
	// accidentally chew partial dates.
	if len(s) == 10 && (s[4] == '/' || s[4] == '.') && s[4] == s[7] {
		norm := []byte(s)
		norm[4] = '-'
		norm[7] = '-'
		if t, err := time.Parse(dateLayout, string(norm)); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q (want YYYY-MM-DD, YYYY/MM/DD, or YYYY.MM.DD)", s)
}

// Parse reads ledger-format text and returns a partial Project. The Project's
// Name and BaseCurrency are not set by Parse — callers (config loader, store)
// fill them in.
//
// `include` directives are NOT supported through this entry point — there's
// no filesystem context to resolve them against. Use ParseFile if your input
// uses includes.
func Parse(r io.Reader) (*model.Project, error) {
	p := &model.Project{}
	if err := parseInto(r, p, parseCtx{}); err != nil {
		return nil, err
	}
	return p, nil
}

// ParseFile reads a ledger-format file and returns a partial Project, with
// `include <relpath>` directives resolved relative to each file's directory.
// Cycles are detected and rejected with a clear error.
//
// Files with `.beancount` or `.bean` extensions are routed through the
// beancount syntax shim (see beancount.go). All other extensions get the
// ledger-cli / hledger grammar.
//
// The returned Project's Name and BaseCurrency are not set by ParseFile —
// callers fill them in.
func ParseFile(path string) (*model.Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("include: resolve %q: %w", path, err)
	}
	p := &model.Project{}
	ctx := parseCtx{
		baseDir: filepath.Dir(abs),
		visited: map[string]bool{abs: true},
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	ext := strings.ToLower(filepath.Ext(abs))
	if ext == ".beancount" || ext == ".bean" {
		if err := parseBeancountInto(f, p, ctx); err != nil {
			return nil, err
		}
		return p, nil
	}
	if err := parseInto(f, p, ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// ParseFS parses a file and its includes through an explicit filesystem
// capability. Names use io/fs slash-separated paths relative to fsys's root;
// absolute paths and includes that leave that root are rejected. A filesystem
// that follows symlinks must itself enforce confinement (for example os.Root.FS).
// Format detection and include semantics otherwise match ParseFile.
func ParseFS(fsys fs.FS, name string) (*model.Project, error) {
	if fsys == nil || !fs.ValidPath(name) {
		return nil, fmt.Errorf("invalid filesystem path %q", name)
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p := &model.Project{}
	ctx := parseCtx{baseDir: path.Dir(name), visited: map[string]bool{name: true}, fs: fsys}
	ext := strings.ToLower(path.Ext(name))
	if ext == ".beancount" || ext == ".bean" {
		if err := parseBeancountInto(f, p, ctx); err != nil {
			return nil, err
		}
	} else if err := parseInto(f, p, ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// parseCtx threads filesystem context (for include resolution) through the
// recursive parser. The zero value disables include support.
type parseCtx struct {
	baseDir string          // directory to resolve `include <relpath>` against; "" disables includes
	visited map[string]bool // set of absolute file paths already being parsed (cycle detection)
	fs      fs.FS           // optional capability for ParseFS; nil preserves ParseFile
}

func parseInto(r io.Reader, p *model.Project, ctx parseCtx) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		lineNo           int
		current          *model.Entry
		currentCommodity *model.Commodity
	)

	// flush commits any in-progress block (entry or commodity declaration)
	// and clears the pointer. Called at every top-level boundary.
	flush := func() error {
		if current != nil {
			if err := inferElidedAmount(current); err != nil {
				return err
			}
			p.Entries = append(p.Entries, *current)
			current = nil
		}
		if currentCommodity != nil {
			p.Commodities = append(p.Commodities, *currentCommodity)
			currentCommodity = nil
		}
		return nil
	}

	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		// Comment / blank.
		//
		// We accept:
		//   - any-column `;` (canonical ledger-cli / hledger inline comment)
		//   - column-0 `#` (hledger SPEC-journal allows # as a line comment)
		//   - column-0 `*` (hledger org-mode-style file comment)
		//
		// We deliberately do NOT treat `#` or `*` as comments mid-line,
		// because account names like `Expenses:Travel#trip-7` and the
		// `* description` status prefix on entries both use those chars.
		trimmed := strings.TrimRight(raw, " \t")
		if trimmed == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		leftTrimmed := strings.TrimLeft(trimmed, " \t")
		if strings.HasPrefix(leftTrimmed, ";") {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		// Column-0 `#` or `*` comment — hledger compatibility.
		if len(raw) > 0 && (raw[0] == '#' || raw[0] == '*') {
			if err := flush(); err != nil {
				return err
			}
			continue
		}

		indented := raw[0] == ' ' || raw[0] == '\t'
		body, lineNote := splitInlineComment(trimmed)
		body = strings.TrimRight(body, " \t")

		switch {
		case indented && currentCommodity != nil:
			// Subdirective on a commodity declaration block.
			if err := applyCommoditySubdirective(currentCommodity, strings.TrimLeft(body, " \t")); err != nil {
				return fmt.Errorf("line %d: %w", lineNo, err)
			}

		case indented:
			if current == nil {
				return fmt.Errorf("line %d: posting without entry header", lineNo)
			}
			post, err := parsePosting(strings.TrimLeft(body, " \t"), lineNote)
			if err != nil {
				return fmt.Errorf("line %d: %w", lineNo, err)
			}
			current.Postings = append(current.Postings, post)

		case strings.HasPrefix(body, "account "):
			if err := flush(); err != nil {
				return err
			}
			name := strings.TrimSpace(strings.TrimPrefix(body, "account "))
			if name == "" {
				return fmt.Errorf("line %d: empty account declaration", lineNo)
			}
			tags, prose := extractTags(lineNote)
			p.Accounts = append(p.Accounts, model.Account{
				Name: name,
				Type: model.InferAccountType(name),
				Note: prose,
				Tags: tags,
			})

		case strings.HasPrefix(body, "commodity "):
			if err := flush(); err != nil {
				return err
			}
			sym := strings.TrimSpace(strings.TrimPrefix(body, "commodity "))
			if sym == "" {
				return fmt.Errorf("line %d: empty commodity declaration", lineNo)
			}
			currentCommodity = &model.Commodity{Symbol: sym}

		case strings.HasPrefix(body, "P "):
			if err := flush(); err != nil {
				return err
			}
			pr, err := parsePrice(strings.TrimPrefix(body, "P "))
			if err != nil {
				return fmt.Errorf("line %d: %w", lineNo, err)
			}
			p.Prices = append(p.Prices, pr)

		case strings.HasPrefix(body, "include ") || strings.HasPrefix(body, "!include "):
			// hledger spec uses `include`; ledger-cli historically used
			// `!include`. We accept both.
			if err := flush(); err != nil {
				return err
			}
			rel := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(body, "!"), "include "))
			if rel == "" {
				return fmt.Errorf("line %d: empty include path", lineNo)
			}
			if ctx.baseDir == "" {
				return fmt.Errorf("line %d: include %q requires ParseFile (no filesystem context for Parse)", lineNo, rel)
			}
			var absTarget string
			var err error
			var child io.ReadCloser
			if ctx.fs != nil {
				if path.IsAbs(rel) || strings.Contains(rel, "\\") {
					return fmt.Errorf("line %d: include path %q must stay within the filesystem root", lineNo, rel)
				}
				absTarget = path.Join(ctx.baseDir, rel)
				if !fs.ValidPath(absTarget) {
					return fmt.Errorf("line %d: include path %q leaves the filesystem root", lineNo, rel)
				}
				if ctx.visited[absTarget] {
					return fmt.Errorf("line %d: include cycle detected at %q", lineNo, rel)
				}
				child, err = ctx.fs.Open(absTarget)
			} else {
				target := rel
				if !filepath.IsAbs(target) {
					target = filepath.Join(ctx.baseDir, rel)
				}
				absTarget, err = filepath.Abs(target)
				if err != nil {
					return fmt.Errorf("line %d: include %q: %w", lineNo, rel, err)
				}
				if ctx.visited[absTarget] {
					return fmt.Errorf("line %d: include cycle detected at %q", lineNo, rel)
				}
				child, err = os.Open(absTarget)
			}
			if err != nil {
				return fmt.Errorf("line %d: include %q: %w", lineNo, rel, err)
			}
			childCtx := parseCtx{
				baseDir: filepath.Dir(absTarget),
				visited: cloneVisited(ctx.visited),
				fs:      ctx.fs,
			}
			if ctx.fs != nil {
				childCtx.baseDir = path.Dir(absTarget)
			}
			childCtx.visited[absTarget] = true
			if perr := parseInto(child, p, childCtx); perr != nil {
				_ = child.Close()
				return fmt.Errorf("line %d: include %q: %w", lineNo, rel, perr)
			}
			_ = child.Close()

		default:
			if err := flush(); err != nil {
				return err
			}
			e, err := parseEntryHeader(body, lineNote)
			if err != nil {
				return fmt.Errorf("line %d: %w", lineNo, err)
			}
			current = &e
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	return flush()
}

// cloneVisited returns a shallow copy of the include-cycle visited-set so
// child parses don't pollute the parent's view.
func cloneVisited(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// extractTags pulls structured `key:value` (or `key=value`) tokens out of an
// inline comment. Tokens are whitespace-separated. Anything that isn't a
// recognizable key:value remains in the free-form note.
//
// A key must start with a letter and contain only letters/digits/`_`/`-`.
// The value runs from the colon to the next whitespace.
func extractTags(note string) (map[string]string, string) {
	if note == "" {
		return nil, ""
	}
	tags := map[string]string{}
	var prose []string
	for _, tok := range strings.Fields(note) {
		k, v, ok := splitKV(tok)
		if !ok {
			prose = append(prose, tok)
			continue
		}
		tags[k] = v
	}
	if len(tags) == 0 {
		return nil, note
	}
	return tags, strings.Join(prose, " ")
}

func splitKV(tok string) (key, value string, ok bool) {
	sep := -1
	for i, c := range tok {
		if c == ':' || c == '=' {
			sep = i
			break
		}
	}
	if sep <= 0 || sep == len(tok)-1 {
		return "", "", false
	}
	k := tok[:sep]
	if !validTagKey(k) {
		return "", "", false
	}
	return k, tok[sep+1:], true
}

func validTagKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case (c >= '0' && c <= '9') || c == '_' || c == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func splitInlineComment(s string) (body, note string) {
	// Comments start with `;` — but only when the `;` is preceded by whitespace
	// or at start of line. Inside strings we don't care because amounts and
	// account names don't contain `;`.
	for i, ch := range s {
		if ch != ';' {
			continue
		}
		if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
			return strings.TrimRight(s[:i], " \t"), strings.TrimSpace(s[i+1:])
		}
	}
	return s, ""
}

// inferElidedAmount fills in at most one posting whose Amount.Quantity is nil
// (elided in the source) with the value needed to balance the entry in the
// commodities already present. Mirrors `hledger print -x` / `ledger`'s
// implicit balancing rule.
//
// Constraints:
//   - At most one elided posting per entry (more than one is ambiguous).
//   - All other postings must share a single commodity (we don't try to
//     infer a multi-commodity counter-posting).
//
// If the entry has zero elided postings, this is a no-op. If any posting
// carries an `@` unit price its converted value is used in the sum (so a
// purchase like `2 BTC @ $50000` contributes $100000 to the running total
// even though the native commodity is BTC).
func inferElidedAmount(e *model.Entry) error {
	var elidedIdx = -1
	commodity := ""
	sum := new(big.Rat)
	for i, p := range e.Postings {
		if p.Amount.Quantity == nil {
			if elidedIdx >= 0 {
				return fmt.Errorf("entry %q: at most one posting may have an elided amount", e.Payee)
			}
			elidedIdx = i
			continue
		}
		// Effective commodity: the priced one if a unit price is present,
		// else the native commodity. This is what determines what the
		// elided posting needs to balance against.
		effComm := p.Amount.Commodity
		effQty := new(big.Rat).Set(p.Amount.Quantity)
		if p.UnitPrice != nil {
			effComm = p.UnitPrice.Commodity
			effQty.Mul(effQty, p.UnitPrice.Quantity)
		}
		if commodity == "" {
			commodity = effComm
		} else if effComm != commodity {
			// Multi-commodity entry that can't be resolved to a single
			// counter-commodity. Refuse to infer; the resulting unbalanced
			// entry will surface to the caller.
			return nil
		}
		sum.Add(sum, effQty)
	}
	if elidedIdx < 0 {
		return nil
	}
	neg := new(big.Rat).Neg(sum)
	e.Postings[elidedIdx].Amount = model.Amount{
		Quantity:  neg,
		Commodity: commodity,
	}
	return nil
}

func parseEntryHeader(body, note string) (model.Entry, error) {
	parts := strings.SplitN(body, " ", 2)
	if len(parts) < 2 {
		return model.Entry{}, fmt.Errorf("entry header missing payee: %q", body)
	}
	date, err := parseFlexibleDate(parts[0])
	if err != nil {
		return model.Entry{}, err
	}
	rest := strings.TrimSpace(parts[1])
	status := model.StatusUncleared
	if strings.HasPrefix(rest, "* ") {
		status = model.StatusCleared
		rest = strings.TrimSpace(rest[2:])
	} else if strings.HasPrefix(rest, "! ") {
		status = model.StatusPending
		rest = strings.TrimSpace(rest[2:])
	} else if rest == "*" || rest == "!" {
		// Header with no payee — degenerate but accept it
		if rest == "*" {
			status = model.StatusCleared
		} else {
			status = model.StatusPending
		}
		rest = ""
	}
	// Optional `(CODE)` field — ledger / hledger common convention for check
	// numbers, invoice refs, transaction hashes, etc. Lives between the
	// status flag and the payee.
	code := ""
	if strings.HasPrefix(rest, "(") {
		if end := strings.Index(rest, ")"); end > 0 {
			code = rest[1:end]
			rest = strings.TrimSpace(rest[end+1:])
		}
	}
	return model.Entry{
		Date:   date,
		Status: status,
		Code:   code,
		Payee:  rest,
		Note:   note,
	}, nil
}

func parsePosting(body, note string) (model.Posting, error) {
	post := model.Posting{Note: note}
	// Optional posting-level status flag: leading `*` or `!`
	if strings.HasPrefix(body, "* ") {
		s := model.StatusCleared
		post.Status = &s
		body = strings.TrimSpace(body[2:])
	} else if strings.HasPrefix(body, "! ") {
		s := model.StatusPending
		post.Status = &s
		body = strings.TrimSpace(body[2:])
	}

	// Posting layout: ACCOUNT  AMOUNT [@ UNIT_PRICE]
	// Account names can contain single spaces; the separator between account
	// and amount is two-or-more spaces (or a tab). Match ledger-cli rule.
	acct, amtRaw, ok := splitAccountAmount(body)
	if !ok {
		// elided amount — just an account
		post.Account = strings.TrimSpace(body)
		return post, nil
	}
	post.Account = acct

	amtStr, priceStr, priceIsTotal := splitAtPrice(amtRaw)
	amt, err := parseAmount(amtStr)
	if err != nil {
		return post, fmt.Errorf("posting amount: %w", err)
	}
	post.Amount = amt
	if priceStr != "" {
		price, err := parseAmount(priceStr)
		if err != nil {
			return post, fmt.Errorf("posting price: %w", err)
		}
		if priceIsTotal {
			// Convert total → unit price by dividing by abs(quantity).
			// Without this, "1 BTC @@ $50000" and "1 BTC @ $50000" would
			// both produce a UnitPrice of $50000 — correct for the first
			// line but wrong for "2 BTC @@ $50000" where the unit price
			// should be $25000.
			if amt.Quantity == nil || amt.Quantity.Sign() == 0 {
				return post, fmt.Errorf("posting price: @@ total price requires non-zero quantity")
			}
			absQty := new(big.Rat).Abs(amt.Quantity)
			price.Quantity = new(big.Rat).Quo(price.Quantity, absQty)
		}
		post.UnitPrice = &price
	}
	return post, nil
}

// splitAccountAmount splits at the first run of 2+ spaces (or a tab),
// matching ledger-cli's rule. Returns ok=false when no such separator exists.
func splitAccountAmount(s string) (account, amount string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
		}
		if s[i] == ' ' && i+1 < len(s) && s[i+1] == ' ' {
			j := i
			for j < len(s) && s[j] == ' ' {
				j++
			}
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[j:]), true
		}
	}
	return "", "", false
}

// splitAtPrice extracts the amount and price portions of a posting amount
// expression. ledger-cli (and hledger) recognise two price annotations:
//
//	@   unit price   — "1 BTC @ $50000"   (price is per-unit)
//	@@  total price  — "1 BTC @@ $50000"  (price is the total for the lot)
//
// isTotal distinguishes the two. The caller must convert total → unit price
// (divide by abs(quantity)) so downstream balance math is correct regardless
// of which annotation appeared in the source.
func splitAtPrice(s string) (amount, price string, isTotal bool) {
	idx := strings.Index(s, "@")
	if idx < 0 {
		return s, "", false
	}
	if idx+1 < len(s) && s[idx+1] == '@' {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+2:]), true
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), false
}

func parseAmount(s string) (model.Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return model.Amount{}, fmt.Errorf("empty amount")
	}
	// Handle "$NNN" prefix (USD shorthand) — also "-$NNN".
	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	s = strings.TrimSpace(s)

	commodity := ""
	numStr := ""
	if strings.HasPrefix(s, "$") {
		commodity = "USD"
		numStr = strings.TrimSpace(s[1:])
	} else {
		// Number first, then commodity (e.g. "5.00 USD" or "1 BTC")
		// Find the boundary between number and non-number.
		i := 0
		for i < len(s) {
			c := s[i]
			if (c >= '0' && c <= '9') || c == '.' || c == ',' {
				i++
				continue
			}
			break
		}
		numStr = strings.TrimSpace(s[:i])
		commodity = strings.TrimSpace(s[i:])
	}
	if numStr == "" {
		return model.Amount{}, fmt.Errorf("missing numeric quantity in %q", s)
	}
	numStr = strings.ReplaceAll(numStr, ",", "")

	q, ok := new(big.Rat).SetString(numStr)
	if !ok {
		return model.Amount{}, fmt.Errorf("invalid number %q", numStr)
	}
	if negative {
		q.Neg(q)
	}
	if commodity == "" {
		return model.Amount{}, fmt.Errorf("missing commodity in amount %q", s)
	}
	return model.Amount{Quantity: q, Commodity: commodity}, nil
}

// parsePrice handles both grammars:
//
//	P DATE COMMODITY AMOUNT                      (date-only)
//	P DATE TIME-WITH-TZ COMMODITY AMOUNT         (timestamped, tz-aware)
//
// A naked time without timezone offset is rejected: crypto prices observed
// in one market and quoted in another are wrong by hours when the timezone
// is dropped, so we force the writer to declare it. Accepted offsets follow
// RFC3339: `Z`, `+HH:MM`, `-HH:MM`.
func parsePrice(body string) (model.Price, error) {
	parts := strings.Fields(body)
	if len(parts) < 3 {
		return model.Price{}, fmt.Errorf("price directive needs date, commodity, amount: %q", body)
	}
	// Detect a time component: if parts[1] contains ':' it's intended to be a
	// time (commodity symbols don't legally contain ':').
	hasTime := strings.Contains(parts[1], ":")
	var (
		ts        time.Time
		commodity string
		amtStart  int
	)
	if hasTime {
		if len(parts) < 4 {
			return model.Price{}, fmt.Errorf("timestamped price needs date, time, commodity, amount: %q", body)
		}
		if !priceTimeHasTimezone(parts[1]) {
			return model.Price{}, fmt.Errorf("price time %q requires a timezone offset (e.g. -05:00 or Z); crypto prices are timezone-sensitive", parts[1])
		}
		var err error
		ts, err = time.Parse(priceTimeLayout, parts[0]+" "+parts[1])
		if err != nil {
			return model.Price{}, fmt.Errorf("invalid price timestamp %q %q: %w", parts[0], parts[1], err)
		}
		commodity = parts[2]
		amtStart = 3
	} else {
		var err error
		ts, err = parseFlexibleDate(parts[0])
		if err != nil {
			return model.Price{}, err
		}
		commodity = parts[1]
		amtStart = 2
	}
	amt, err := parseAmount(strings.Join(parts[amtStart:], " "))
	if err != nil {
		return model.Price{}, err
	}
	return model.Price{
		Date:          ts,
		HasTime:       hasTime,
		Commodity:     commodity,
		QuoteCurrency: amt.Commodity,
		Price:         amt.Quantity,
	}, nil
}

// applyCommoditySubdirective parses one indented subdirective line of a
// `commodity SYMBOL` declaration block. Recognised forms:
//
//	note <free text>
//	format <example>
//	nomarket
//	default
//
// Unknown subdirectives return an error so a typo doesn't get silently
// dropped on the floor — agents reading this code shouldn't lose data.
func applyCommoditySubdirective(c *model.Commodity, line string) error {
	switch {
	case line == "nomarket":
		c.NoMarket = true
	case line == "default":
		c.IsDefault = true
	case strings.HasPrefix(line, "note "):
		c.Note = strings.TrimSpace(strings.TrimPrefix(line, "note "))
	case strings.HasPrefix(line, "format "):
		c.Format = strings.TrimSpace(strings.TrimPrefix(line, "format "))
	default:
		return fmt.Errorf("unknown commodity subdirective: %q", line)
	}
	return nil
}

// priceTimeHasTimezone returns true when s ends with `Z` or `±HH:MM` (or
// `±HHMM`). Used to reject naive times in price directives.
func priceTimeHasTimezone(s string) bool {
	if s == "" {
		return false
	}
	if s[len(s)-1] == 'Z' || s[len(s)-1] == 'z' {
		return true
	}
	// Look for a trailing +HH:MM / -HH:MM / +HHMM / -HHMM after the seconds.
	// We scan backwards for the first '+' or '-' that follows a digit (not
	// the date's first char). Minimum tail length is 3 (e.g. "-05").
	for i := len(s) - 1; i >= 1; i-- {
		c := s[i]
		if c == '+' || c == '-' {
			// Must follow a digit (i.e. the seconds field), and have at least
			// 2 chars after it.
			if s[i-1] >= '0' && s[i-1] <= '9' && len(s)-i >= 3 {
				return true
			}
			return false
		}
		if c == ':' || (c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return false
}

// Print writes a project to w in canonical ledger format. Section ordering:
// declared accounts, prices (date-sorted), entries (date-sorted).
func Print(w io.Writer, p *model.Project) error {
	bw := bufio.NewWriter(w)
	defer func() { _ = bw.Flush() }()

	if p.Name != "" {
		_, _ = fmt.Fprintf(bw, "; %s — %s ledger\n", p.Name, p.BaseCurrency)
		_, _ = fmt.Fprintln(bw)
	}

	// Accounts (sorted by name for stable output)
	accs := append([]model.Account(nil), p.Accounts...)
	sort.SliceStable(accs, func(i, j int) bool { return accs[i].Name < accs[j].Name })
	if len(accs) > 0 {
		for _, a := range accs {
			comment := formatAccountComment(a.Tags, a.Note)
			if comment != "" {
				_, _ = fmt.Fprintf(bw, "account %s    ; %s\n", a.Name, comment)
			} else {
				_, _ = fmt.Fprintf(bw, "account %s\n", a.Name)
			}
		}
		_, _ = fmt.Fprintln(bw)
	}

	// Commodities (declared) — emitted before prices so the resulting file
	// declares its symbols before they are referenced.
	commodities := append([]model.Commodity(nil), p.Commodities...)
	sort.SliceStable(commodities, func(i, j int) bool { return commodities[i].Symbol < commodities[j].Symbol })
	if len(commodities) > 0 {
		for _, c := range commodities {
			_, _ = fmt.Fprintf(bw, "commodity %s\n", c.Symbol)
			if c.Note != "" {
				_, _ = fmt.Fprintf(bw, "    note %s\n", c.Note)
			}
			if c.Format != "" {
				_, _ = fmt.Fprintf(bw, "    format %s\n", c.Format)
			}
			if c.NoMarket {
				_, _ = fmt.Fprintln(bw, "    nomarket")
			}
			if c.IsDefault {
				_, _ = fmt.Fprintln(bw, "    default")
			}
		}
		_, _ = fmt.Fprintln(bw)
	}

	// Prices
	prices := append([]model.Price(nil), p.Prices...)
	sort.SliceStable(prices, func(i, j int) bool { return prices[i].Date.Before(prices[j].Date) })
	if len(prices) > 0 {
		for _, pr := range prices {
			_, _ = fmt.Fprintf(bw, "P %s %s %s\n", formatPriceTimestamp(pr), pr.Commodity, formatPriceAmount(pr.Price, pr.QuoteCurrency))
		}
		_, _ = fmt.Fprintln(bw)
	}

	// Entries
	entries := append([]model.Entry(nil), p.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Date.Before(entries[j].Date) })
	for i, e := range entries {
		flag := e.Status.Flag()
		if flag != "" {
			flag = flag + " "
		}
		code := ""
		if e.Code != "" {
			code = "(" + e.Code + ") "
		}
		header := fmt.Sprintf("%s %s%s%s", e.Date.Format(dateLayout), flag, code, e.Payee)
		if e.Note != "" {
			_, _ = fmt.Fprintf(bw, "%s    ; %s\n", header, e.Note)
		} else {
			_, _ = fmt.Fprintf(bw, "%s\n", header)
		}
		acctW := postingAccountWidth(e.Postings)
		for _, post := range e.Postings {
			if err := printPosting(bw, post, acctW); err != nil {
				return err
			}
		}
		if i < len(entries)-1 {
			_, _ = fmt.Fprintln(bw)
		}
	}
	return nil
}

func postingAccountWidth(ps []model.Posting) int {
	w := 0
	for _, p := range ps {
		n := len(p.Account)
		if p.Status != nil {
			n += 2 // "* " prefix
		}
		if n > w {
			w = n
		}
	}
	if w < 36 {
		w = 36
	}
	return w
}

func printPosting(w io.Writer, p model.Posting, acctW int) error {
	prefix := ""
	if p.Status != nil {
		prefix = p.Status.Flag() + " "
	}
	left := prefix + p.Account
	pad := acctW - len(left) + 4
	if pad < 4 {
		pad = 4
	}
	if p.Amount.Quantity == nil {
		// elided
		if p.Note != "" {
			_, _ = fmt.Fprintf(w, "    %s    ; %s\n", left, p.Note)
		} else {
			_, _ = fmt.Fprintf(w, "    %s\n", left)
		}
		return nil
	}
	_, _ = fmt.Fprintf(w, "    %s%s%s", left, strings.Repeat(" ", pad), formatAmount(p.Amount))
	if p.UnitPrice != nil {
		// Prefer `@@ total` form when the unit price has a non-terminating
		// decimal expansion. This keeps round-trip exact: a parser reading
		// our output applies the same total/abs(quantity) conversion we did
		// at parse time, and the rational comes back identical. Without
		// this, fractions like 5/6 (from `12 EUR @@ 10 GBP`) would round
		// to 0.83333333 in `@ unit` form and never reverse cleanly.
		if hasExactDecimal(p.UnitPrice.Quantity, 8) {
			_, _ = fmt.Fprintf(w, " @ %s", formatAmount(*p.UnitPrice))
		} else {
			total := totalFromUnit(p.Amount, *p.UnitPrice)
			_, _ = fmt.Fprintf(w, " @@ %s", formatAmount(total))
		}
	}
	if p.Note != "" {
		_, _ = fmt.Fprintf(w, "    ; %s", p.Note)
	}
	_, _ = fmt.Fprintln(w)
	return nil
}

// formatAccountComment renders an account-directive comment: known tags
// (wallet, address, network) come first in fixed order, then any other tags
// alphabetically, then the free-form note prose. Returns empty when nothing
// to emit.
func formatAccountComment(tags map[string]string, note string) string {
	if len(tags) == 0 && note == "" {
		return ""
	}
	priority := []string{"wallet", "address", "network"}
	var tokens []string
	seen := map[string]bool{}
	for _, k := range priority {
		if v, ok := tags[k]; ok {
			tokens = append(tokens, k+":"+v)
			seen[k] = true
		}
	}
	var rest []string
	for k := range tags {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		tokens = append(tokens, k+":"+tags[k])
	}
	parts := strings.Join(tokens, " ")
	if note != "" {
		if parts != "" {
			return parts + " " + note
		}
		return note
	}
	return parts
}

// formatAmount renders an amount in canonical form: USD becomes "$1.23",
// other commodities render as "1.23 BTC". Negative amounts keep the sign on
// the numeric part (USD: "-$1.23"; other: "-1.23 BTC").
func formatAmount(a model.Amount) string {
	if a.Quantity == nil {
		return ""
	}
	q := new(big.Rat).Set(a.Quantity)
	neg := q.Sign() < 0
	if neg {
		q.Neg(q)
	}
	num := formatRat(q)
	sign := ""
	if neg {
		sign = "-"
	}
	if a.Commodity == "USD" {
		return fmt.Sprintf("%s$%s", sign, num)
	}
	return fmt.Sprintf("%s%s %s", sign, num, a.Commodity)
}

func formatPriceAmount(q *big.Rat, quote string) string {
	a := model.Amount{Quantity: q, Commodity: quote}
	return formatAmount(a)
}

// formatPriceTimestamp renders a price's timestamp. Date-only prices stay
// short ("2024-06-21"); timestamped prices always include a numeric offset
// ("2024-06-21 02:18:02-05:00") — we never emit a naive time because we
// refuse to parse one.
func formatPriceTimestamp(pr model.Price) string {
	if pr.HasTime {
		return pr.Date.Format(priceTimeLayout)
	}
	return pr.Date.Format(dateLayout)
}

// hasExactDecimal reports whether r can be represented exactly with at most
// `digits` decimal places. We use this to decide whether a unit price
// round-trips cleanly through Print/Parse, falling back to `@@ totalPrice`
// when it doesn't.
//
// Algorithm: r is exactly representable in N decimal places iff
// r * 10^N is an integer. Equivalently, the denominator (in lowest terms)
// divides 10^N. We test by computing num * 10^digits and checking that the
// remainder when divided by den is zero.
func hasExactDecimal(r *big.Rat, digits int) bool {
	if r == nil {
		return true
	}
	num := r.Num()
	den := r.Denom()
	if den.Sign() == 0 {
		return false
	}
	// Compute |num| * 10^digits and check divisibility by den.
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	scaled := new(big.Int).Mul(num, scale)
	rem := new(big.Int).Mod(scaled, den)
	return rem.Sign() == 0
}

// totalFromUnit returns the total price (amount.Quantity * unitPrice.Quantity,
// signed absolute) in the unit-price commodity. Used by Print to emit `@@`
// total-price form when the per-unit price isn't a finite decimal.
func totalFromUnit(amount, unitPrice model.Amount) model.Amount {
	abs := new(big.Rat).Abs(amount.Quantity)
	total := new(big.Rat).Mul(abs, unitPrice.Quantity)
	return model.Amount{
		Quantity:  total,
		Commodity: unitPrice.Commodity,
	}
}

// formatRat formats a *big.Rat with up to 8 fraction digits, trimming trailing
// zeros but keeping at least 2 digits after the decimal for fiat-style amounts.
func formatRat(r *big.Rat) string {
	// FloatString gives us a rounded decimal representation.
	s := r.FloatString(8)
	if !strings.Contains(s, ".") {
		return s + ".00"
	}
	// Trim trailing zeros after the decimal, but keep at least 2.
	dot := strings.Index(s, ".")
	intPart := s[:dot]
	frac := s[dot+1:]
	frac = strings.TrimRight(frac, "0")
	if len(frac) < 2 {
		frac = frac + strings.Repeat("0", 2-len(frac))
	}
	return intPart + "." + frac
}
