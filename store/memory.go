package store

import (
	"context"
	"fmt"
	"sync"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// MemoryStore is an in-memory Store. Goroutine-safe. Used for tests and for
// dry-run / demo workflows where no persistence is required.
type MemoryStore struct {
	mu           sync.Mutex
	name         string
	baseCurrency string
	accounts     []model.Account
	commodities  []model.Commodity
	entries      []model.Entry
	prices       []model.Price
	nextID       int
}

// NewMemory returns an empty in-memory store. projectName and baseCurrency
// populate Project()'s metadata fields the same way LocalStore does.
func NewMemory(projectName, baseCurrency string) *MemoryStore {
	if baseCurrency == "" {
		baseCurrency = "USD"
	}
	return &MemoryStore{name: projectName, baseCurrency: baseCurrency}
}

func (s *MemoryStore) Project(ctx context.Context) (*model.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &model.Project{
		Name:         s.name,
		BaseCurrency: s.baseCurrency,
		Accounts:     append([]model.Account(nil), s.accounts...),
		Commodities:  append([]model.Commodity(nil), s.commodities...),
		Entries:      cloneEntries(s.entries),
		Prices:       append([]model.Price(nil), s.prices...),
	}, nil
}

func (s *MemoryStore) AddAccount(ctx context.Context, a model.Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.Type == "" {
		a.Type = model.InferAccountType(a.Name)
	}
	s.accounts = append(s.accounts, a)
	return nil
}

func (s *MemoryStore) AddCommodity(ctx context.Context, c model.Commodity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commodities = append(s.commodities, c)
	return nil
}

func (s *MemoryStore) AddEntry(ctx context.Context, e model.Entry) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		s.nextID++
		e.ID = fmt.Sprintf("mem-%04d", s.nextID)
	}
	s.entries = append(s.entries, cloneEntry(e))
	return e.ID, nil
}

func (s *MemoryStore) AddPrice(ctx context.Context, p model.Price) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prices = append(s.prices, p)
	return nil
}

func (s *MemoryStore) SetEntryStatus(ctx context.Context, entryID string, status model.Status, postingAccount string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].ID != entryID {
			continue
		}
		if postingAccount == "" {
			s.entries[i].Status = status
			return nil
		}
		updated := false
		for j := range s.entries[i].Postings {
			if s.entries[i].Postings[j].Account == postingAccount {
				st := status
				s.entries[i].Postings[j].Status = &st
				updated = true
			}
		}
		if !updated {
			return fmt.Errorf("entry %s has no posting on account %s", entryID, postingAccount)
		}
		return nil
	}
	return fmt.Errorf("entry not found: %s", entryID)
}

func (s *MemoryStore) AppendRaw(ctx context.Context, p *model.Project) error {
	for _, a := range p.Accounts {
		if err := s.AddAccount(ctx, a); err != nil {
			return err
		}
	}
	for _, c := range p.Commodities {
		if err := s.AddCommodity(ctx, c); err != nil {
			return err
		}
	}
	for _, pr := range p.Prices {
		if err := s.AddPrice(ctx, pr); err != nil {
			return err
		}
	}
	for _, e := range p.Entries {
		if _, err := s.AddEntry(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

func cloneEntry(e model.Entry) model.Entry {
	out := e
	out.Postings = make([]model.Posting, len(e.Postings))
	for i, p := range e.Postings {
		out.Postings[i] = p
		if p.Status != nil {
			st := *p.Status
			out.Postings[i].Status = &st
		}
	}
	return out
}

func cloneEntries(in []model.Entry) []model.Entry {
	out := make([]model.Entry, len(in))
	for i, e := range in {
		out[i] = cloneEntry(e)
	}
	return out
}
