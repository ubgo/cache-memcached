// doc.go — canonical package documentation (package memcachedcache, github.com/ubgo/cache-memcached).
//
// Package role: this file is the authoritative overview for the ubgo/cache
// Memcached adapter; start here before reading memcached.go (the adapter).
//
// This file: holds ONLY the package doc comment below — no code. It is the
// canonical statement of the partial-by-design contract: TTL,
// DeleteByPrefix and Iterate are ErrUnsupported; Decr floors at 0; the
// atomic counter-init dance for Incr/Decr on a missing key.
//
// AI-context: the // Package … block below is the godoc package doc; do not
// duplicate it (revive flags duplicate package comments). The blank line
// after this header keeps it a file header, not a second package comment.

// Package memcachedcache is the Memcached adapter for github.com/ubgo/cache,
// backed by github.com/bradfitz/gomemcache.
//
// Memcached's protocol is intentionally minimal, so this adapter is a partial
// implementation of cache.Cache and does NOT pass the shared conformance
// suite — by protocol design, not omission:
//
//   - TTL(key)        → ErrUnsupported (memcached cannot report remaining TTL)
//   - DeleteByPrefix  → ErrUnsupported (no server-side key scan)
//   - Iterate         → ErrUnsupported (no cursor / key enumeration)
//   - Decr below zero  floors at 0 (memcached counters are unsigned)
//
// Everything else (Get/Set/SetNX/Expire/Touch/Incr/Decr/Del/Flush/Has/Ping)
// works. Use this adapter for drop-in interop with an existing Memcached
// fleet; prefer cache-redis when you need TTL introspection or prefix ops.
//
// Design invariants worth knowing before editing this package:
//
//   - The unsupported ops return ErrUnsupported EXPLICITLY rather than a
//     silent empty result. Never add client-side emulation (e.g. a key
//     registry to fake Iterate / prefix-delete): it would be unbounded,
//     racy across processes, and dishonest about Memcached's guarantees.
//   - Incr/Decr on a missing key use an atomic ADD to initialise (Memcached
//     incr/decr require a pre-existing key). The ADD is the linearisation
//     point: a racing initialiser sees ErrNotStored and retries the
//     incr/decr, so concurrent first-writers never lose an update.
//   - Memcached counters are unsigned; Decr floors at 0. This is native
//     behaviour the adapter surfaces, not a bug to paper over.
//   - Close only flips a local flag; it never closes a possibly-shared
//     *memcache.Client.
//
// If you change which operations are supported, update this doc, the
// README unsupported-ops table, and memcached_test.go together.
package memcachedcache
