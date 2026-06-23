// Package store abstracts persistence for ledger projects so the same CLI
// surface can target a local directory of .ledger files or the gl-svc cloud
// API.
package store

import (
	"context"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// Store is the operations the CLI commands need.
type Store interface {
	// Project returns the full in-memory view. Cloud stores fetch on demand;
	// local stores parse the directory.
	Project(ctx context.Context) (*model.Project, error)

	// AddAccount declares an account.
	AddAccount(ctx context.Context, a model.Account) error

	// AddCommodity declares a commodity with its subdirectives.
	AddCommodity(ctx context.Context, c model.Commodity) error

	// AddEntry appends an entry. Returns the entry ID assigned by the store.
	AddEntry(ctx context.Context, e model.Entry) (string, error)

	// AddPrice appends a price observation.
	AddPrice(ctx context.Context, p model.Price) error

	// SetEntryStatus flips the status of an entry (and optionally one posting).
	SetEntryStatus(ctx context.Context, entryID string, status model.Status, postingAccount string) error

	// AppendRaw imports a chunk of pre-parsed ledger text by appending its
	// entries / accounts / commodities / prices to the project. Used by
	// `bw ledger import`.
	AppendRaw(ctx context.Context, p *model.Project) error
}
