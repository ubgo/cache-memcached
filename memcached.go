package memcachedcache

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/ubgo/cache"
)

// client is the subset of *memcache.Client this adapter needs. It is the test
// seam: newWith injects an in-process fake so every supported op (and the
// counter race) is unit-testable with no Memcached server. Keep it minimal —
// widening this interface widens the fake.
type client interface {
	Get(key string) (*memcache.Item, error)
	Set(item *memcache.Item) error
	Add(item *memcache.Item) error
	Delete(key string) error
	Increment(key string, delta uint64) (uint64, error)
	Decrement(key string, delta uint64) (uint64, error)
	Touch(key string, seconds int32) error
	DeleteAll() error
	Ping() error
}

// Cache adapts a gomemcache client to cache.Cache (partially — see package doc).
type Cache struct {
	mc     client
	closed atomic.Bool
}

// New wraps a *memcache.Client.
func New(mc *memcache.Client) *Cache { return &Cache{mc: mc} }

// newWith is the test seam for the in-process fake.
func newWith(c client) *Cache { return &Cache{mc: c} }

// secs encodes a contract TTL into Memcached's whole-second Expiration field.
// ttl<=0 means "no expiry" per the cache contract, which Memcached spells as
// 0. Sub-second precision is lost (Memcached has none) and, critically, the
// remaining TTL can never be read back — which is why TTL() is unsupported.
func secs(ttl time.Duration) int32 {
	if ttl <= 0 {
		return 0 // memcached: 0 = no expiry
	}
	return int32(ttl.Seconds())
}

// mapErr normalises Memcached's "no such key" sentinel to the contract's
// ErrNotFound so callers stay backend-agnostic; other errors pass through.

func mapErr(err error) error {
	if errors.Is(err, memcache.ErrCacheMiss) {
		return cache.ErrNotFound
	}
	return err
}

// Get implements cache.Cache.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	if c.closed.Load() {
		return nil, cache.ErrClosed
	}
	it, err := c.mc.Get(key)
	if err != nil {
		return nil, mapErr(err)
	}
	return it.Value, nil
}

// GetMulti implements cache.Cache.
func (c *Cache) GetMulti(ctx context.Context, keys []string) (map[string][]byte, error) {
	if c.closed.Load() {
		return nil, cache.ErrClosed
	}
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		if it, err := c.mc.Get(k); err == nil {
			out[k] = it.Value
		} else if !errors.Is(err, memcache.ErrCacheMiss) {
			return nil, err
		}
	}
	return out, nil
}

// Has implements cache.Cache.
func (c *Cache) Has(ctx context.Context, key string) (bool, error) {
	_, err := c.Get(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// TTL is unsupported: memcached cannot report a key's remaining TTL.
func (c *Cache) TTL(_ context.Context, _ string) (time.Duration, error) {
	return 0, cache.ErrUnsupported
}

// Set implements cache.Cache.
func (c *Cache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if c.closed.Load() {
		return cache.ErrClosed
	}
	return c.mc.Set(&memcache.Item{Key: key, Value: val, Expiration: secs(ttl)})
}

// SetMulti implements cache.Cache.
func (c *Cache) SetMulti(ctx context.Context, items map[string]cache.Item) error {
	for k, it := range items {
		if err := c.Set(ctx, k, it.Value, it.TTL); err != nil {
			return err
		}
	}
	return nil
}

// SetNX implements cache.Cache (memcached ADD).
func (c *Cache) SetNX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	if c.closed.Load() {
		return false, cache.ErrClosed
	}
	err := c.mc.Add(&memcache.Item{Key: key, Value: val, Expiration: secs(ttl)})
	if errors.Is(err, memcache.ErrNotStored) {
		return false, nil // key already exists
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Expire implements cache.Cache via memcached TOUCH.
func (c *Cache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if c.closed.Load() {
		return cache.ErrClosed
	}
	return mapErr(c.mc.Touch(key, secs(ttl)))
}

// Touch implements cache.Cache.
func (c *Cache) Touch(ctx context.Context, key string) error {
	return c.Expire(ctx, key, time.Hour)
}

// addInt is the shared Incr/Decr core. Memcached incr/decr require a
// pre-existing key and operate on UNSIGNED counters, while the cache contract
// says a missing key starts at 0. The reconciliation: on ErrCacheMiss, ADD the
// key (atomic create). The ADD is the linearisation point — if a racer created
// it first we get ErrNotStored and re-issue the incr/decr against the racer's
// value, so concurrent first-writers never lose an update and no lock is
// needed. Decrement floors at 0 (native unsigned semantics, surfaced as-is).
func (c *Cache) addInt(key string, delta int64) (int64, error) {
	if c.closed.Load() {
		return 0, cache.ErrClosed
	}
	if delta >= 0 {
		v, err := c.mc.Increment(key, uint64(delta))
		if errors.Is(err, memcache.ErrCacheMiss) {
			// Initialise atomically; if a racer created it first, retry incr.
			addErr := c.mc.Add(&memcache.Item{Key: key, Value: []byte(strconv.FormatInt(delta, 10))})
			if errors.Is(addErr, memcache.ErrNotStored) {
				v, err = c.mc.Increment(key, uint64(delta))
				return int64(v), err
			}
			return delta, addErr
		}
		return int64(v), err
	}
	v, err := c.mc.Decrement(key, uint64(-delta))
	if errors.Is(err, memcache.ErrCacheMiss) {
		// Counters are unsigned; a missing key decremented floors at 0.
		if addErr := c.mc.Add(&memcache.Item{Key: key, Value: []byte("0")}); addErr != nil &&
			!errors.Is(addErr, memcache.ErrNotStored) {
			return 0, addErr
		}
		v, err = c.mc.Decrement(key, uint64(-delta))
		return int64(v), err
	}
	return int64(v), err
}

// Incr implements cache.Cache.
func (c *Cache) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	return c.addInt(key, delta)
}

// Decr implements cache.Cache (floors at 0 — memcached counter semantics).
func (c *Cache) Decr(ctx context.Context, key string, delta int64) (int64, error) {
	return c.addInt(key, -delta)
}

// Del implements cache.Cache.
func (c *Cache) Del(ctx context.Context, keys ...string) error {
	if c.closed.Load() {
		return cache.ErrClosed
	}
	for _, k := range keys {
		if err := c.mc.Delete(k); err != nil && !errors.Is(err, memcache.ErrCacheMiss) {
			return err
		}
	}
	return nil
}

// DeleteByPrefix is unsupported: memcached has no server-side key scan.
func (c *Cache) DeleteByPrefix(_ context.Context, _ string) error {
	return cache.ErrUnsupported
}

// Flush implements cache.Cache (memcached flush_all).
func (c *Cache) Flush(ctx context.Context) error {
	if c.closed.Load() {
		return cache.ErrClosed
	}
	return c.mc.DeleteAll()
}

// Iterate is unsupported: memcached cannot enumerate keys. Returns an iterator
// that immediately stops with ErrUnsupported rather than nil so callers that
// don't check Err() still loop zero times instead of nil-panicking — and a
// caller that does check sees an explicit reason, never a silent empty scan.
func (c *Cache) Iterate(_ context.Context, _ cache.IterateOpts) cache.Iterator {
	return unsupportedIter{}
}

// unsupportedIter is a zero-yield iterator whose Err() explains why. It exists
// because the contract says non-scannable adapters return such an iterator
// (not an error from Iterate itself).
type unsupportedIter struct{}

func (unsupportedIter) Next() bool    { return false }
func (unsupportedIter) Key() string   { return "" }
func (unsupportedIter) Value() []byte { return nil }
func (unsupportedIter) Err() error    { return cache.ErrUnsupported }
func (unsupportedIter) Close() error  { return nil }

// Ping implements cache.Cache.
func (c *Cache) Ping(ctx context.Context) error {
	if c.closed.Load() {
		return cache.ErrClosed
	}
	return c.mc.Ping()
}

// Close implements cache.Cache (idempotent; does not close a shared client).
func (c *Cache) Close() error {
	c.closed.Store(true)
	return nil
}

// Stats implements cache.Cache. Memcached exposes server stats out of band;
// this adapter reports a zero snapshot.
func (c *Cache) Stats() cache.Stats { return cache.Stats{} }

var _ cache.Cache = (*Cache)(nil)
