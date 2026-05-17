# Cache methods

Memcached's protocol is intentionally minimal, so this adapter is **partial by
protocol design, not omission**. The three unsupported ops return
`cache.ErrUnsupported` *explicitly* — never a silent empty result, and never
client-side emulation (a fake key registry would be unbounded, racy across
processes, and dishonest about Memcached's guarantees). Snippets assume:

```go
ctx := context.Background()
mc := memcache.New("localhost:11211")
c := memcachedcache.New(mc)
defer c.Close()
```

## Read

### Get / GetMulti / Has

`Get` → Memcached `GET`; a miss (`ErrCacheMiss`) is normalised to
`cache.ErrNotFound`. `GetMulti` issues a `GET` per key and omits absent keys.
`Has` is `Get` with the not-found case mapped to `false`.

```go
v, err := c.Get(ctx, "user:42")
if errors.Is(err, cache.ErrNotFound) { /* miss */ }
m, _ := c.GetMulti(ctx, []string{"a", "b"})
ok, _ := c.Has(ctx, "k")
```

## Write

### Set / SetMulti

`Set` → `SET` with whole-second `Expiration` (sub-second precision is lost —
Memcached has none; `ttl <= 0` = no expiry, encoded as 0). `SetMulti` is a
per-key `Set` loop (stops on first error).

```go
_ = c.Set(ctx, "k", []byte("v"), 5*time.Minute)
_ = c.SetMulti(ctx, map[string]cache.Item{"a": {Value: []byte("1")}})
```

### SetNX

`SetNX` → Memcached `ADD`. `ErrNotStored` (key already exists) →
`(false, nil)`; success → `(true, nil)`.

Use cases: distributed lock / write-once marker across a Memcached fleet.

```go
ok, _ := c.SetNX(ctx, "lock:job", []byte("1"), 30*time.Second)
if ok { /* acquired */ }
```

### Expire / Touch

`Expire` → Memcached `TOUCH` (whole-second). `Touch` == `Expire(key, 1h)`. A
miss maps to `cache.ErrNotFound`.

```go
_ = c.Expire(ctx, "session:42", time.Hour)
_ = c.Touch(ctx, "session:42")
```

## Counters

### Incr / Decr

Memcached `INCR`/`DECR` require a pre-existing key and operate on **unsigned**
counters, while the contract says a missing key starts at 0. Reconciliation: on
a miss the adapter `ADD`s the key — the `ADD` is the linearisation point, so if
a racer created it first (`ErrNotStored`) the incr/decr is re-issued against the
racer's value and **concurrent first-writers never lose an update** (no lock
needed).

**`Decr` floors at 0** — native unsigned Memcached semantics, surfaced as-is,
not a bug. (This differs from `cache-redis`/`cache-mem`, where counters may go
negative.)

Use cases: rate-limit / view counters where a 0 floor on decrement is
acceptable.

```go
n, _ := c.Incr(ctx, "rl:ip:1.2.3.4", 1) // missing key initialised atomically
f, _ := c.Decr(ctx, "gauge", 5)         // never goes below 0
_ = n
_ = f
```

## Delete

### Del

`Del` → `DELETE` per key. Deleting an absent key is **not** an error
(`ErrCacheMiss` is swallowed).

```go
_ = c.Del(ctx, "a", "b")
```

### Flush

`Flush` → Memcached `flush_all` (clears the whole server).

```go
_ = c.Flush(ctx)
```

## Unsupported by Memcached protocol

### TTL — unsupported

`TTL(ctx, key)` → always `(0, cache.ErrUnsupported)`. Memcached cannot report a
key's remaining TTL.

```go
_, err := c.TTL(ctx, "k")
// errors.Is(err, cache.ErrUnsupported) == true
```

### DeleteByPrefix — unsupported

`DeleteByPrefix(ctx, prefix)` → always `cache.ErrUnsupported`. Memcached has no
server-side key scan. Use `WithPrefix` on `cache-redis` instead if you need
this.

```go
err := c.DeleteByPrefix(ctx, "user:")
// errors.Is(err, cache.ErrUnsupported) == true
```

### Iterate — unsupported

`Iterate(ctx, opts)` returns a **zero-yield iterator** whose first `Next()` is
`false` and whose `Err()` is `cache.ErrUnsupported` (it returns this iterator,
not an error from `Iterate` itself, per the contract). A caller that does not
check `Err()` simply loops zero times instead of nil-panicking; a caller that
does check sees an explicit reason, never a silent empty scan.

```go
it := c.Iterate(ctx, cache.IterateOpts{Prefix: "user:"})
defer it.Close()
for it.Next() { /* never entered */ }
if err := it.Err(); errors.Is(err, cache.ErrUnsupported) {
	// explicit: Memcached cannot enumerate keys
}
```

## Lifecycle

### Ping / Close / Stats

`Ping` → Memcached `Ping` (`cache.ErrClosed` after `Close`). `Close` is
idempotent and only flips a local flag — it never closes a possibly-shared
client. `Stats` returns a **zero snapshot** (`cache.Stats{}`): Memcached
exposes server stats out of band, not through this adapter.

```go
if err := c.Ping(ctx); err != nil { log.Fatal("memcached down:", err) }
_ = c.Stats() // always cache.Stats{} for this adapter
```
