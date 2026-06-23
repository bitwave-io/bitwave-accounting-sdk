// Package compat is the cross-tool compatibility test harness for the
// plain-text ledger format. It drives our parser/printer against fixtures
// authored to match hledger, ledger-cli, and beancount syntax, and (under
// the compat_external build tag) shells out to the real upstream binaries
// to prove round-trip compatibility in both directions.
package compat

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Tool identifies one of the upstream plain-text accounting tools we test
// against.
type Tool string

const (
	ToolHledger   Tool = "hledger"
	ToolLedger    Tool = "ledger"
	ToolBeancount Tool = "bean-check"
)

// String satisfies fmt.Stringer for cleaner test output.
func (t Tool) String() string { return string(t) }

//go:embed bin/versions.json
var versionsJSON []byte

// Versions returns the pinned upstream versions of each tool, sourced from
// bin/versions.json. The map is keyed by short name ("hledger", "ledger",
// "beancount").
func Versions() (map[string]string, error) {
	var v map[string]string
	if err := json.Unmarshal(versionsJSON, &v); err != nil {
		return nil, fmt.Errorf("compat: parse versions.json: %w", err)
	}
	return v, nil
}

// Resolve returns an absolute path to the upstream binary for the given
// tool, or "" if neither the vendored copy nor $PATH has it. The resolution
// order is:
//
//  1. format/compat/bin/<GOOS>-<GOARCH>/<tool>
//     (looked up relative to the running test binary)
//  2. exec.LookPath(<tool>) — falls back to a system-installed copy.
//
// Tests calling Resolve must skip when "" is returned and they need the
// binary. RequireToolForTesting (below) is the usual entrypoint.
func Resolve(tool Tool) string {
	if p := vendoredPath(tool); p != "" {
		return p
	}
	if p, err := exec.LookPath(string(tool)); err == nil {
		return p
	}
	return ""
}

// vendoredPath returns the in-repo binary path for the running platform
// if it exists and is executable, otherwise "".
func vendoredPath(tool Tool) string {
	dir := vendoredBinDir()
	if dir == "" {
		return ""
	}
	candidate := filepath.Join(dir, string(tool))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		// We don't check the executable bit explicitly — on darwin/linux
		// the OS will refuse to exec a non-x file and we surface that as a
		// test failure with a clear error, which is more useful than a
		// silent "binary missing".
		return candidate
	}
	return ""
}

// vendoredBinDir returns the directory holding the bundled binaries for
// the current platform, or "" when this package wasn't compiled with the
// usual project layout (eg vendored as a library elsewhere).
func vendoredBinDir() string {
	root := packageRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH)
}

// packageRoot resolves to the directory holding this source file at
// compile time. The result is baked in at build, so it stays valid even
// when the test binary is moved.
//
// We use a Go //line directive trick via runtime.Caller — see init() for
// the actual capture.
var packageRootCached string

func init() {
	// runtime.Caller(0) returns binaries.go's source path at the moment
	// `init` runs, which is the original compile-time path. That's exactly
	// what we want for resolving the vendored bin/ tree.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	packageRootCached = filepath.Dir(file)
}

func packageRoot() string { return packageRootCached }
