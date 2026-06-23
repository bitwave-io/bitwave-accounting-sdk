package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/config"
	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// File names within a local project directory.
const (
	AccountsFile    = "accounts.ledger"
	CommoditiesFile = "commodities.ledger"
	JournalFile     = "journal.ledger"
	PricesFile      = "prices.ledger"
)

// LocalStore reads/writes a project as plain-text files in a directory.
type LocalStore struct {
	Dir string
	Cfg *config.Config
}

// NewLocal opens an existing local project at dir.
func NewLocal(dir string) (*LocalStore, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	if cfg.Mode != config.ModeLocal {
		return nil, fmt.Errorf("project %s is in %s mode, not local", dir, cfg.Mode)
	}
	return &LocalStore{Dir: dir, Cfg: cfg}, nil
}

// InitLocal scaffolds an empty project at dir.
func InitLocal(dir, projectName, baseCurrency string) (*LocalStore, error) {
	return InitLocalWithStrict(dir, projectName, baseCurrency, false)
}

// InitLocalWithStrict is InitLocal with an explicit strict-mode toggle.
// When strict is true, the resulting project rejects postings and prices
// that reference undeclared accounts or commodities.
func InitLocalWithStrict(dir, projectName, baseCurrency string, strict bool) (*LocalStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	// Refuse to clobber an existing project.
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil {
		return nil, fmt.Errorf("project already initialized at %s", dir)
	}
	cfg := &config.Config{
		Mode:         config.ModeLocal,
		BaseCurrency: baseCurrency,
		ProjectName:  projectName,
		Strict:       strict,
	}
	if err := config.Save(dir, cfg); err != nil {
		return nil, err
	}
	for _, f := range []string{AccountsFile, CommoditiesFile, JournalFile, PricesFile} {
		path := filepath.Join(dir, f)
		if err := os.WriteFile(path, []byte(""), 0644); err != nil {
			return nil, err
		}
	}
	return &LocalStore{Dir: dir, Cfg: cfg}, nil
}

func (s *LocalStore) path(name string) string { return filepath.Join(s.Dir, name) }

func (s *LocalStore) parseFile(name string) (*model.Project, error) {
	path := s.path(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &model.Project{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return format.Parse(bytes.NewReader(data))
}

// Project parses every .ledger file in the project directory and merges them.
func (s *LocalStore) Project(ctx context.Context) (*model.Project, error) {
	out := &model.Project{
		Name:         s.Cfg.ProjectName,
		BaseCurrency: s.Cfg.BaseCurrency,
	}
	for _, f := range []string{AccountsFile, CommoditiesFile, JournalFile, PricesFile} {
		p, err := s.parseFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out.Accounts = append(out.Accounts, p.Accounts...)
		out.Commodities = append(out.Commodities, p.Commodities...)
		out.Entries = append(out.Entries, p.Entries...)
		out.Prices = append(out.Prices, p.Prices...)
	}
	// Stable insertion order: assign synthetic IDs for entries that lack one,
	// derived from date + index so `bw ledger clear <id>` is reproducible.
	for i := range out.Entries {
		if out.Entries[i].ID == "" {
			out.Entries[i].ID = syntheticEntryID(out.Entries[i], i)
		}
	}
	return out, nil
}

// syntheticEntryID returns a stable id for a parsed entry: "<date>-<seq>".
// Local mode doesn't persist IDs in the .ledger format itself, so the id is
// derived from position. This is good enough for `clear`/`unclear` since the
// CLI re-reads the file before mutating.
func syntheticEntryID(e model.Entry, idx int) string {
	return fmt.Sprintf("%s-%04d", e.Date.Format("20060102"), idx)
}

func (s *LocalStore) AddAccount(ctx context.Context, a model.Account) error {
	if a.Type == "" {
		a.Type = model.InferAccountType(a.Name)
	}
	// Append-only — duplicate detection is the parser's job.
	line := fmt.Sprintf("account %s\n", a.Name)
	if a.Note != "" {
		line = fmt.Sprintf("account %s    ; %s\n", a.Name, a.Note)
	}
	return appendFile(s.path(AccountsFile), line)
}

func (s *LocalStore) AddCommodity(ctx context.Context, c model.Commodity) error {
	// Use the printer so the on-disk format matches a freshly-parsed file
	// exactly, including the indented subdirectives.
	tmp := &model.Project{Commodities: []model.Commodity{c}}
	var buf bytes.Buffer
	if err := format.Print(&buf, tmp); err != nil {
		return err
	}
	return appendFile(s.path(CommoditiesFile), strings.TrimSpace(buf.String())+"\n")
}

func (s *LocalStore) AddEntry(ctx context.Context, e model.Entry) (string, error) {
	if e.Date.IsZero() {
		e.Date = time.Now()
	}
	// Render via the printer to ensure identical formatting to the rest of the
	// file. Wrap in a tiny project so Print emits an entry block.
	tmp := &model.Project{Entries: []model.Entry{e}}
	var buf bytes.Buffer
	if err := format.Print(&buf, tmp); err != nil {
		return "", err
	}
	out := strings.TrimSpace(buf.String()) + "\n\n"
	if err := appendFile(s.path(JournalFile), out); err != nil {
		return "", err
	}
	// Re-derive the synthetic ID so the caller can pass it back to
	// SetEntryStatus / clear / unclear without surprises. Local files don't
	// store IDs in the format itself.
	proj, err := s.Project(ctx)
	if err != nil {
		return "", err
	}
	if len(proj.Entries) == 0 {
		return "", nil
	}
	return proj.Entries[len(proj.Entries)-1].ID, nil
}

func (s *LocalStore) AddPrice(ctx context.Context, p model.Price) error {
	tmp := &model.Project{Prices: []model.Price{p}}
	var buf bytes.Buffer
	if err := format.Print(&buf, tmp); err != nil {
		return err
	}
	return appendFile(s.path(PricesFile), strings.TrimSpace(buf.String())+"\n")
}

// SetEntryStatus rewrites journal.ledger with the entry's status (and
// optionally one posting's status) flipped. Local-mode entries are matched by
// the synthetic ID computed in Project().
func (s *LocalStore) SetEntryStatus(ctx context.Context, entryID string, status model.Status, postingAccount string) error {
	proj, err := s.Project(ctx)
	if err != nil {
		return err
	}
	hit := false
	for i := range proj.Entries {
		if proj.Entries[i].ID != entryID {
			continue
		}
		hit = true
		if postingAccount != "" {
			updated := false
			for j := range proj.Entries[i].Postings {
				if proj.Entries[i].Postings[j].Account == postingAccount {
					st := status
					proj.Entries[i].Postings[j].Status = &st
					updated = true
				}
			}
			if !updated {
				return fmt.Errorf("entry %s has no posting on account %s", entryID, postingAccount)
			}
		} else {
			proj.Entries[i].Status = status
		}
		break
	}
	if !hit {
		return fmt.Errorf("entry not found: %s", entryID)
	}
	// Rewrite journal.ledger from the in-memory entries.
	var buf bytes.Buffer
	tmp := &model.Project{Entries: proj.Entries}
	if err := format.Print(&buf, tmp); err != nil {
		return err
	}
	return os.WriteFile(s.path(JournalFile), buf.Bytes(), 0644)
}

func (s *LocalStore) AppendRaw(ctx context.Context, p *model.Project) error {
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

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(content)
	return err
}
