package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// CloudStore talks to gl-svc /api/v1/orgs/{orgId}/ledger/workspaces/{workspaceId}.
type CloudStore struct {
	BaseURL       string
	OrgID         string
	WorkspaceID   string
	TokenResolver func() (string, error)
	HTTPClient    *http.Client
}

// NewCloud builds a cloud store. tokenResolver should return an org-scoped
// token (use makeOrgTokenResolver in cmd/).
func NewCloud(baseURL, orgID, workspaceID string, tokenResolver func() (string, error)) *CloudStore {
	return &CloudStore{
		BaseURL:       baseURL,
		OrgID:         orgID,
		WorkspaceID:   workspaceID,
		TokenResolver: tokenResolver,
		HTTPClient:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *CloudStore) base() string {
	return fmt.Sprintf("%s/api/v1/orgs/%s/ledger/workspaces/%s", c.BaseURL, c.OrgID, c.WorkspaceID)
}

func (c *CloudStore) do(method, path, contentType string, body io.Reader) ([]byte, error) {
	tok, err := c.TokenResolver()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, path, string(data))
	}
	return data, nil
}

func (c *CloudStore) getJSON(path string, out any) error {
	data, err := c.do("GET", path, "", nil)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *CloudStore) postJSON(path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	data, err := c.do("POST", path, "application/json", r)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// --- DTOs (must match gl-svc dto/ledger_*.go shapes) ---

type accountDTO struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Note string `json:"note,omitempty"`
}

type postingDTO struct {
	Account      string `json:"account"`
	Quantity     string `json:"quantity"`
	Commodity    string `json:"commodity"`
	UnitPrice    string `json:"unitPrice,omitempty"`
	UnitCurrency string `json:"unitCurrency,omitempty"`
	Status       string `json:"status,omitempty"`
	Note         string `json:"note,omitempty"`
}

type entryDTO struct {
	ID       string       `json:"id,omitempty"`
	Date     string       `json:"date"`
	Payee    string       `json:"payee,omitempty"`
	Note     string       `json:"note,omitempty"`
	Status   string       `json:"status,omitempty"`
	Postings []postingDTO `json:"postings"`
}

// priceDTO is the wire shape POSTed to gl-svc's /prices endpoint and
// received back inside the workspace snapshot. Field names mirror gl-svc's
// dto.AddLedgerPriceRequestDTO and dto.LedgerPriceResponse.
//
// PriceTime, when non-empty, is the timezone-aware time component of an
// intraday price observation. Format: HH:MM:SS±HH:MM or HH:MM:SSZ. Required
// when the originating P directive included a time — gl-svc rejects naive
// intraday times because crypto prices are timezone-sensitive.
type priceDTO struct {
	PriceDate     string `json:"priceDate"`
	PriceTime     string `json:"priceTime,omitempty"`
	Asset         string `json:"asset"`
	QuoteCurrency string `json:"quoteCurrency"`
	Price         string `json:"price"`
}

type workspaceDTO struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	BaseCurrency string       `json:"baseCurrency"`
	Accounts     []accountDTO `json:"accounts"`
	Entries      []entryDTO   `json:"entries"`
	Prices       []priceDTO   `json:"prices"`
}

func entryToDTO(e model.Entry) entryDTO {
	d := entryDTO{
		ID:     e.ID,
		Date:   e.Date.Format("2006-01-02"),
		Payee:  e.Payee,
		Note:   e.Note,
		Status: statusToWire(e.Status),
	}
	for _, p := range e.Postings {
		pd := postingDTO{
			Account: p.Account,
			Note:    p.Note,
		}
		if p.Amount.Quantity != nil {
			pd.Quantity = p.Amount.Quantity.FloatString(8)
			pd.Commodity = p.Amount.Commodity
		}
		if p.UnitPrice != nil {
			pd.UnitPrice = p.UnitPrice.Quantity.FloatString(8)
			pd.UnitCurrency = p.UnitPrice.Commodity
		}
		if p.Status != nil {
			pd.Status = statusToWire(*p.Status)
		}
		d.Postings = append(d.Postings, pd)
	}
	return d
}

func entryFromDTO(d entryDTO) (model.Entry, error) {
	t, err := time.Parse("2006-01-02", d.Date)
	if err != nil {
		return model.Entry{}, fmt.Errorf("entry date: %w", err)
	}
	st, _ := model.ParseStatus(d.Status)
	e := model.Entry{
		ID:     d.ID,
		Date:   t,
		Payee:  d.Payee,
		Note:   d.Note,
		Status: st,
	}
	for _, p := range d.Postings {
		post := model.Posting{Account: p.Account, Note: p.Note}
		if p.Quantity != "" {
			q, ok := new(big.Rat).SetString(p.Quantity)
			if !ok {
				return e, fmt.Errorf("invalid quantity %q", p.Quantity)
			}
			post.Amount = model.Amount{Quantity: q, Commodity: p.Commodity}
		}
		if p.UnitPrice != "" {
			q, ok := new(big.Rat).SetString(p.UnitPrice)
			if !ok {
				return e, fmt.Errorf("invalid unit price %q", p.UnitPrice)
			}
			post.UnitPrice = &model.Amount{Quantity: q, Commodity: p.UnitCurrency}
		}
		if p.Status != "" {
			s, _ := model.ParseStatus(p.Status)
			post.Status = &s
		}
		e.Postings = append(e.Postings, post)
	}
	return e, nil
}

func statusToWire(s model.Status) string {
	switch s {
	case model.StatusCleared:
		return "CLEARED"
	case model.StatusPending:
		return "PENDING"
	default:
		return "UNCLEARED"
	}
}

// --- Store impl ---

func (c *CloudStore) Project(ctx context.Context) (*model.Project, error) {
	var dto workspaceDTO
	if err := c.getJSON(c.base(), &dto); err != nil {
		return nil, err
	}
	out := &model.Project{Name: dto.Name, BaseCurrency: dto.BaseCurrency}
	for _, a := range dto.Accounts {
		out.Accounts = append(out.Accounts, model.Account{
			Name: a.Name, Type: model.AccountType(a.Type), Note: a.Note,
		})
	}
	for _, p := range dto.Prices {
		var (
			t       time.Time
			err     error
			hasTime bool
		)
		if p.PriceTime != "" {
			t, err = time.Parse("2006-01-02 15:04:05Z07:00", p.PriceDate+" "+p.PriceTime)
			hasTime = true
		} else {
			t, err = time.Parse("2006-01-02", p.PriceDate)
		}
		if err != nil {
			return nil, fmt.Errorf("price date: %w", err)
		}
		q, _ := new(big.Rat).SetString(p.Price)
		out.Prices = append(out.Prices, model.Price{
			Date: t, HasTime: hasTime, Commodity: p.Asset, QuoteCurrency: p.QuoteCurrency, Price: q,
		})
	}
	for _, e := range dto.Entries {
		entry, err := entryFromDTO(e)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func (c *CloudStore) AddAccount(ctx context.Context, a model.Account) error {
	dto := accountDTO{Name: a.Name, Type: string(a.Type), Note: a.Note}
	return c.postJSON(c.base()+"/accounts", dto, nil)
}

// commodityDTO is the wire shape for gl-svc's commodity endpoints.
type commodityDTO struct {
	Symbol    string `json:"symbol"`
	Note      string `json:"note,omitempty"`
	Format    string `json:"format,omitempty"`
	NoMarket  bool   `json:"nomarket,omitempty"`
	IsDefault bool   `json:"default,omitempty"`
}

func (c *CloudStore) AddCommodity(ctx context.Context, m model.Commodity) error {
	d := commodityDTO{
		Symbol:    m.Symbol,
		Note:      m.Note,
		Format:    m.Format,
		NoMarket:  m.NoMarket,
		IsDefault: m.IsDefault,
	}
	return c.postJSON(c.base()+"/commodities", d, nil)
}

func (c *CloudStore) AddEntry(ctx context.Context, e model.Entry) (string, error) {
	return c.AddEntryToJournal(ctx, "", e)
}

// AddEntryToJournal posts an entry to the named journal. Empty journalId
// falls back to the workspace's default journal ("general").
func (c *CloudStore) AddEntryToJournal(ctx context.Context, journalId string, e model.Entry) (string, error) {
	d := entryToDTO(e)
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.postJSON(fmt.Sprintf("%s/journals/%s/entries", c.base(), journalOrDefault(journalId)), d, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *CloudStore) AddPrice(ctx context.Context, p model.Price) error {
	d := priceDTO{
		PriceDate:     p.Date.Format("2006-01-02"),
		Asset:         p.Commodity,
		QuoteCurrency: p.QuoteCurrency,
		Price:         p.Price.FloatString(8),
	}
	if p.HasTime {
		d.PriceTime = p.Date.Format("15:04:05Z07:00")
	}
	return c.postJSON(c.base()+"/prices", d, nil)
}

func (c *CloudStore) SetEntryStatus(ctx context.Context, entryID string, status model.Status, postingAccount string) error {
	return c.SetEntryStatusInJournal(ctx, "", entryID, status, postingAccount)
}

// SetEntryStatusInJournal flips status on an entry within the named journal.
// Empty journalId falls back to the workspace's default journal ("general").
func (c *CloudStore) SetEntryStatusInJournal(ctx context.Context, journalId, entryID string, status model.Status, postingAccount string) error {
	body := map[string]string{
		"status":         statusToWire(status),
		"postingAccount": postingAccount,
	}
	return c.postJSON(fmt.Sprintf("%s/journals/%s/entries/%s/status", c.base(), journalOrDefault(journalId), entryID), body, nil)
}

func (c *CloudStore) AppendRaw(ctx context.Context, p *model.Project) error {
	// Bulk import endpoint accepts ledger-format text body. Callers with
	// richer payloads should use Import(ctx, rawText) directly.
	return fmt.Errorf("AppendRaw on CloudStore is not used directly — use Import")
}

// Import POSTs raw ledger-format text to the bulk import endpoint.
func (c *CloudStore) Import(ctx context.Context, ledgerText string) error {
	return c.ImportToJournal(ctx, "", ledgerText)
}

// ImportToJournal posts ledger text into the named journal. Empty journalId
// falls back to the workspace's default journal ("general").
func (c *CloudStore) ImportToJournal(ctx context.Context, journalId, ledgerText string) error {
	return c.postJSON(fmt.Sprintf("%s/journals/%s/import", c.base(), journalOrDefault(journalId)), map[string]string{"text": ledgerText}, nil)
}

// journalOrDefault returns the supplied journalId, or "general" when empty.
func journalOrDefault(journalId string) string {
	if journalId == "" {
		return "general"
	}
	return journalId
}

// CreateWorkspaceRequest creates a new ledger workspace on gl-svc and returns its ID.
type CreateWorkspaceRequest struct {
	Name         string `json:"name"`
	BaseCurrency string `json:"baseCurrency"`
	Strict       bool   `json:"strict,omitempty"`
}

// CreateWorkspace is a static helper because it's called before a CloudStore
// instance has a workspace ID.
func CreateWorkspace(baseURL, orgID string, tokenResolver func() (string, error), req CreateWorkspaceRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	tok, err := tokenResolver()
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/api/v1/orgs/%s/ledger/workspaces", baseURL, url.PathEscape(orgID))
	httpReq, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Export GETs the canonical ledger-format text for the workspace. Optional
// from/to/account filters become query params.
func (c *CloudStore) Export(ctx context.Context, from, to, account string) (string, error) {
	u, err := url.Parse(c.base() + "/export")
	if err != nil {
		return "", err
	}
	q := u.Query()
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	if account != "" {
		q.Set("account", account)
	}
	u.RawQuery = q.Encode()
	data, err := c.do("GET", u.String(), "", nil)
	if err != nil {
		return "", err
	}
	var env struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("export: decode envelope: %w", err)
	}
	return env.Text, nil
}
