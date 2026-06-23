package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/config"
)

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &config.Config{
		Mode:         config.ModeLocal,
		BaseCurrency: "USD",
		ProjectName:  "demo",
	}
	if err := config.Save(dir, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Mode != in.Mode || out.BaseCurrency != in.BaseCurrency || out.ProjectName != in.ProjectName {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", out, in)
	}
}

func TestLoad_DefaultsBaseCurrencyToUSD(t *testing.T) {
	dir := t.TempDir()
	// Write a config with empty baseCurrency.
	path := filepath.Join(dir, config.FileName)
	body := `{"mode":"local","baseCurrency":"","projectName":"demo"}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.BaseCurrency != "USD" {
		t.Errorf("BaseCurrency = %q, want USD", out.BaseCurrency)
	}
}

func TestLoad_MissingFile_ReturnsHelpfulError(t *testing.T) {
	dir := t.TempDir()
	_, err := config.Load(dir)
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "bw ledger init") {
		t.Errorf("expected init hint in error, got: %v", err)
	}
}

func TestLoad_InvalidJSON_Errors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(dir)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSave_CloudConfigPreservesIDs(t *testing.T) {
	dir := t.TempDir()
	in := &config.Config{
		Mode:         config.ModeCloud,
		BaseCurrency: "EUR",
		ProjectName:  "cloud-demo",
		OrgID:        "org_123",
		WorkspaceID:  "ws_456",
	}
	if err := config.Save(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.OrgID != "org_123" || out.WorkspaceID != "ws_456" {
		t.Errorf("cloud IDs lost: got %+v", out)
	}
}
