# Contributing to ubgo/cache-memcached

Thanks for helping improve the Memcached adapter for `github.com/ubgo/cache`.

## Local gate (must be green before every commit / PR)

```sh
gofmt -w .
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
```

Or `task check`. CI is identical. Not done until **0 failures, 0 lint issues** (`golangci-lint`: revive, staticcheck, govet, errcheck, gocritic, misspell, unconvert, ineffassign, unused — see `.golangci.yml`).

## Partial by protocol design — the conformance contract

This adapter **intentionally does not pass** the shared `github.com/ubgo/cache/cachetest` suite. The Memcached wire protocol has no command to:

- read a key's remaining TTL → `TTL` returns `cache.ErrUnsupported`
- scan keys server-side → `DeleteByPrefix` returns `cache.ErrUnsupported`
- enumerate keys → `Iterate` returns an iterator with `Next()==false`, `Err()==cache.ErrUnsupported`

This is faithful to the contract's rule "optional ops an adapter cannot serve return `ErrUnsupported`". **Do not** add client-side emulation (e.g. a key registry to fake `Iterate`/prefix-delete) — it would be unbounded, racy, and dishonest. If you change which ops are supported, update the unsupported-ops table in `README.md`, `doc.go`, and `memcached_test.go`'s `TestUnsupportedOpsAreExplicit` in lockstep.

What the tests **do** pin (`memcached_test.go`):

- supported ops behave per contract (`ErrNotFound` on miss, `SetNX`/ADD semantics, idempotent `Close` → `ErrClosed`);
- counter init + concurrency: `Incr` from missing initialises to delta; `Decr` floors at `0`; 4000 concurrent increments converge exactly;
- unsupported ops are explicit (`errors.Is(err, cache.ErrUnsupported)`).

## Docker-free tests (in-process fake)

`memcached_test.go` defines `fakeMC`, an in-process map implementing the unexported `client` interface with Memcached semantics (unsigned counters that floor at 0, `ErrNotStored` on `Add` to an existing key, `ErrCacheMiss` on absent get). The adapter is built via the `newWith(client)` test seam, so every supported op runs without a server. Keep the `client` interface minimal — it is the seam; widening it widens the fake.

Optional real-server checks key off `MEMCACHED_ADDR` and are never part of the gate.

## Local dependency (`replace`)

`go.mod` carries `replace github.com/ubgo/cache => ../cache` (sibling, not yet tagged). **Do not edit `go.mod`, `go.sum`, `LICENSE`, `NOTICE`, or `.gitignore`** in a feature change; the `replace` is removed at release time.

```
ubgo/
  cache/             # contract + cachetest
  cache-memcached/   # this module (replace -> ../cache)
```

## Doc-comment style

- Every exported symbol has a doc comment starting with its name (`revive`).
- For unsupported ops, the comment states **why the protocol cannot serve it** (e.g. "memcached cannot report remaining TTL"), not just "unsupported". Preserve these.
- `ctx` stays a named parameter even when unused; `.golangci.yml` excludes that revive warning. Unsupported-op signatures use `_ context.Context, _ string` deliberately — keep them.
- Keep `doc.go` accurate: it is the godoc landing page and is the canonical statement that the adapter is partial by design.

## Pull requests

1. Keep the gate green.
2. Add/extend a test for any behaviour change; if support for an op changes, update README + doc.go + test together.
3. Update `README.md` / `CHANGELOG.md` on public behaviour changes.
4. One logical change per PR.
