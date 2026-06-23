package format

import (
	"fmt"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// ValidateStrict enforces ledger-cli's `--strict` rule: every account named
// by a posting and every commodity used in a posting or price directive must
// be declared in the project. The first violation is returned with a hint
// describing how to fix it.
//
// Lookup sets include both the project's own declarations and any extras
// passed in (used by ImportText to validate a batch against the merged set
// of pre-existing + about-to-be-declared records).
func ValidateStrict(p *model.Project, extraAccounts, extraCommodities []string) error {
	accounts := map[string]struct{}{}
	for _, a := range p.Accounts {
		accounts[a.Name] = struct{}{}
	}
	for _, a := range extraAccounts {
		accounts[a] = struct{}{}
	}

	commodities := map[string]struct{}{}
	for _, c := range p.Commodities {
		commodities[c.Symbol] = struct{}{}
	}
	for _, c := range extraCommodities {
		commodities[c] = struct{}{}
	}

	for _, e := range p.Entries {
		for _, post := range e.Postings {
			if _, ok := accounts[post.Account]; !ok {
				return fmt.Errorf("strict: account %q not declared (run: bw ledger account add %q)", post.Account, post.Account)
			}
			if post.Amount.Commodity != "" {
				if _, ok := commodities[post.Amount.Commodity]; !ok {
					return fmt.Errorf("strict: commodity %q not declared (run: bw ledger commodity add %s)", post.Amount.Commodity, post.Amount.Commodity)
				}
			}
			if post.UnitPrice != nil && post.UnitPrice.Commodity != "" {
				if _, ok := commodities[post.UnitPrice.Commodity]; !ok {
					return fmt.Errorf("strict: commodity %q not declared (run: bw ledger commodity add %s)", post.UnitPrice.Commodity, post.UnitPrice.Commodity)
				}
			}
		}
	}
	for _, pr := range p.Prices {
		if _, ok := commodities[pr.Commodity]; !ok {
			return fmt.Errorf("strict: commodity %q not declared (run: bw ledger commodity add %s)", pr.Commodity, pr.Commodity)
		}
		if _, ok := commodities[pr.QuoteCurrency]; !ok {
			return fmt.Errorf("strict: commodity %q not declared (run: bw ledger commodity add %s)", pr.QuoteCurrency, pr.QuoteCurrency)
		}
	}
	return nil
}
