# Vendored upstream binaries

Cross-tool compatibility tests (`go test -tags compat_external
./cli/internal/ledger/format/compat/...`) shell out to the *real*
`hledger`, `ledger`, and `bean-check` binaries to prove our plain-text
accounting output is consumable by each, and vice versa.

To keep the test environment deterministic and CI-friendly, those binaries
are vendored here, pinned to specific upstream releases, and tracked via
Git LFS.

## Layout

```
bin/
  THIRD_PARTY_LICENSES.md  # see cli/THIRD_PARTY_LICENSES.md (full text)
  versions.json            # { "hledger": "1.52.1", "ledger": "3.4.1", "beancount": "3.2.3" }
  SHA256SUMS               # checked at test startup
  darwin-arm64/
    hledger
    ledger
    bean-check             # zipapp wrapping the pinned beancount wheel
  darwin-amd64/   (same layout)
  linux-amd64/    (same layout)
```

The harness resolves `runtime.GOOS-runtime.GOARCH` to pick the right
subdirectory. Platforms without bundled binaries get a clean `t.Skip`
in the external test suite.

## Bootstrapping locally

```sh
git lfs install                 # one-time
git lfs pull                    # fetch the binaries
make compat-test-external       # run external-tool tests
```

## Refreshing pins

Maintainer only:

```sh
cd cli
make compat-vendor-bins         # downloads + extracts + updates SHA256SUMS + versions.json
git diff bin/versions.json bin/SHA256SUMS
# review, commit, push (LFS will handle the binaries)
```

The script (`cli/scripts/vendor-compat-binaries.sh`) only touches the
binaries for the host platform. To update darwin-amd64 or linux-amd64,
run the same script on a host of that platform and merge the results.

## License notes

See `cli/THIRD_PARTY_LICENSES.md` for the full BSD / GPL-3 / GPL-2
license texts and source-availability notes. Short version:

- **ledger** binary: BSD-3-Clause, freely redistributable with attribution.
- **hledger** binary: GPL-3, redistributable but downstream recipients
  must be able to obtain source from upstream.
- **bean-check** (beancount): GPL-2, same as hledger.

The CLI itself does not link or embed any of these projects — they are
invoked only as subprocesses during testing.
