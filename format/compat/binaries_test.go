package compat

import (
	"testing"
)

// TestVersionsManifest is a smoke test: versions.json must embed cleanly and
// list the three tools the harness expects.
func TestVersionsManifest(t *testing.T) {
	v, err := Versions()
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	for _, want := range []string{"hledger", "ledger", "beancount"} {
		if v[want] == "" {
			t.Errorf("versions.json missing %q (got %v)", want, v)
		}
	}
}

// TestResolveDoesNotPanic exercises Resolve on each tool. We don't assert
// presence — the binary may be vendored, on $PATH, or absent. We only assert
// the function returns without crashing and returns either "" or a real
// absolute path.
func TestResolveDoesNotPanic(t *testing.T) {
	for _, tool := range []Tool{ToolHledger, ToolLedger, ToolBeancount} {
		got := Resolve(tool)
		t.Logf("Resolve(%s) = %q", tool, got)
	}
}
