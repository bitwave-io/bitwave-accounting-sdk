// Package config handles the per-project .bw-ledger.json file that records
// whether a directory is a local or cloud-backed ledger project.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the project marker file inside <project-dir>.
const FileName = ".bw-ledger.json"

// Mode is "local" or "cloud".
type Mode string

const (
	ModeLocal Mode = "local"
	ModeCloud Mode = "cloud"
)

// Config is the on-disk per-project state.
//
// Strict, when true, mirrors the ledger-cli `--strict` flag: postings and
// prices are rejected at write time if their account or commodity isn't
// declared. Cloud-mode projects mirror the same flag on the gl-svc
// workspace; the JSON copy is the local source of truth used by the
// `bw ledger import` validator before the cloud round-trip.
type Config struct {
	Mode         Mode   `json:"mode"`
	BaseCurrency string `json:"baseCurrency"`
	ProjectName  string `json:"projectName"`
	OrgID        string `json:"orgId,omitempty"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
	Strict       bool   `json:"strict,omitempty"`
}

// Load reads the .bw-ledger.json file from dir.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not a ledger project — no %s in %s (run: bw ledger init)", FileName, dir)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.BaseCurrency == "" {
		c.BaseCurrency = "USD"
	}
	return &c, nil
}

// Save writes the config to <dir>/.bw-ledger.json with 0644 perms.
func Save(dir string, c *Config) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), data, 0644)
}
