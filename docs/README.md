# cache-memcached — feature cookbook

Exhaustive, example-driven reference for every exported identifier in
`github.com/ubgo/cache-memcached` (package `memcachedcache`).

Import path:

```go
import memcachedcache "github.com/ubgo/cache-memcached"
```

`memcachedcache.Cache` is a **partial-by-protocol-design** implementation of
[`cache.Cache`](https://github.com/ubgo/cache): Memcached's wire protocol is
intentionally minimal, so three operations cannot be served and return
`cache.ErrUnsupported` — explicitly, never a silent empty result. It does
**not** pass the full `cachetest.Run` suite, by design. Use it for drop-in
interop with an existing Memcached fleet; prefer `cache-redis` when you need
TTL introspection or prefix ops.

## Pages

- [Construction](construction.md) — `New`, the `Cache` type.
- [Cache methods](cache-methods.md) — every supported method, the three `ErrUnsupported` ops, and the Decr-floors-at-0 / counter-init semantics.

## "Partial by protocol" — at a glance

| Operation | Status | Why |
|---|---|---|
| `Get` `GetMulti` `Has` | supported | `GET` |
| `Set` `SetMulti` | supported | `SET` |
| `SetNX` | supported | `ADD` |
| `Expire` `Touch` | supported | `TOUCH` (whole-second precision) |
| `Incr` `Decr` | supported | `INCR`/`DECR` + atomic `ADD` init; **Decr floors at 0** (unsigned) |
| `Del` | supported | `DELETE` (absent key not an error) |
| `Flush` | supported | `flush_all` |
| `Ping` `Close` `Stats` | supported | `Stats` is a zero snapshot |
| **`TTL`** | **`ErrUnsupported`** | Memcached cannot report remaining TTL |
| **`DeleteByPrefix`** | **`ErrUnsupported`** | no server-side key scan |
| **`Iterate`** | **`ErrUnsupported`** | no cursor / key enumeration (returns a zero-yield iterator whose `Err()` says why) |

## Capability matrix

| Exported symbol | Kind | Page |
|---|---|---|
| `New` | constructor | [Construction](construction.md#new) |
| `Cache` | type | [Construction](construction.md#cache) |
| `Get` / `GetMulti` / `Has` | method | [Cache methods](cache-methods.md#read) |
| `Set` / `SetMulti` / `SetNX` / `Expire` / `Touch` | method | [Cache methods](cache-methods.md#write) |
| `Incr` / `Decr` | method | [Cache methods](cache-methods.md#counters) |
| `Del` / `Flush` | method | [Cache methods](cache-methods.md#delete) |
| `TTL` | method (unsupported) | [Cache methods](cache-methods.md#ttl-unsupported) |
| `DeleteByPrefix` | method (unsupported) | [Cache methods](cache-methods.md#deletebyprefix-unsupported) |
| `Iterate` | method (unsupported) | [Cache methods](cache-methods.md#iterate-unsupported) |
| `Ping` / `Close` / `Stats` | method | [Cache methods](cache-methods.md#lifecycle) |
