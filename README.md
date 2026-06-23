# bitwave-accounting-sdk

Plain-text double-entry accounting for Go, compatible with the
plain-text-accounting ecosystem (`hledger`, `ledger`, `beancount`).

It speaks the same journal grammar as those tools, so the books you keep with it
can be read, audited, and reported on by every other tool in the ecosystem — and
it can run against a cloud general ledger over HTTP when you want multi-user
persistence.

```
go get github.com/bitwave-io/bitwave-accounting-sdk
```

Requires Go 1.25+. No external dependencies.

## Packages

| Package | What it is |
|---|---|
| `model` | The double-entry domain: `Entry`, `Posting`, `Amount`, `Account`, `Commodity`, `Price`, `Project`, with balance-checking and base-currency valuation. |
| `store` | A `Store` interface with local-file and cloud (HTTP general-ledger) backends. |
| `report` | `ledger`-cli-style reports: balance, register, equity, stats, CSV. |
| `format` | hledger / ledger / beancount read + write interop (incl. a cross-tool compatibility suite under `format/compat`). |
| `config` | The per-project `.bw-ledger.json` local/cloud marker file. |

## Why

- **Plain text.** Every transaction is a line in a journal file — diff it, blame
  it, branch it, commit it. No opaque blob, no API lock-in.
- **Real double-entry.** Every entry must balance before it is written.
- **Cross-tool compatible.** Output is consumable by `hledger`, `ledger`, and
  `bean-check`; the `format/compat` suite proves it.
- **Local *or* cloud, same surface.** The same `Store` interface targets a
  directory of journal files or a cloud general ledger.

This SDK is the accounting foundation used by
[`bitwave-wallet-sdk`](https://github.com/bitwave-io/bitwave-wallet-sdk), which
adds on-chain wallets and an on-chain → journal sync bridge on top.

## License

See repository.
