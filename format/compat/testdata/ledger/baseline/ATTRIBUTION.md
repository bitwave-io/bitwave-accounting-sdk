# Vendored ledger-cli test fixtures

Source: https://github.com/ledger/ledger
Upstream pinned at commit `33517328da46edfbbad5488855775eea844ae856`.
License: BSD 3-Clause — see `cli/THIRD_PARTY_LICENSES.md`.

Each `<name>.ledger` here is the journal-only portion of the matching
`test/baseline/<name>.test` file from the upstream repo (the prefix before
the first `test <command>` block). The expected-output sections of the
`.test` files are not vendored; we compare our own canonical Print and the
real `ledger` binary's output via the compat_external suite instead.

## File mapping

| File | Origin |
|---|---|
| cmd-accounts.ledger | test/baseline/cmd-accounts.test |
| cmd-balance.ledger | test/baseline/cmd-balance.test |
| cmd-cleared.ledger | test/baseline/cmd-cleared.test |
| cmd-commodities.ledger | test/baseline/cmd-commodities.test |
| cmd-equity.ledger | test/baseline/cmd-equity.test |
| cmd-payees.ledger | test/baseline/cmd-payees.test |
| cmd-print.ledger | test/baseline/cmd-print.test |
| cmd-register.ledger | test/baseline/cmd-register.test |
| cmd-tags.ledger | test/baseline/cmd-tags.test |
| dir-account.ledger | test/baseline/dir-account.test |
| dir-alias.ledger | test/baseline/dir-alias.test |
| dir-commodity.ledger | test/baseline/dir-commodity.test |
| dir-payee.ledger | test/baseline/dir-payee.test |
| opt-cleared.ledger | test/baseline/opt-cleared.test |
| opt-pending.ledger | test/baseline/opt-pending.test |
| opt-strict.ledger | test/baseline/opt-strict.test |
| opt-uncleared.ledger | test/baseline/opt-uncleared.test |

## Refreshing

Run `cli/scripts/vendor-ledger-fixtures.sh` (the same shell logic used to
populate this directory originally) against a fresh clone of ledger/ledger
at the desired commit. Update the commit hash and the table above when
bumping the pin.

## Copyright

```
Copyright (c) 2003-2025, John Wiegley. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

- Redistributions of source code must retain the above copyright notice,
  this list of conditions and the following disclaimer.
- Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.
- Neither the name of New Artisans LLC nor the names of its contributors
  may be used to endorse or promote products derived from this software
  without specific prior written permission.
```

Full text at `cli/THIRD_PARTY_LICENSES.md`.
