package memcachedcache

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/ubgo/cache"
)

// fakeMC is an in-process stand-in implementing the minimal client interface
// with memcached semantics, so the adapter is fully testable without a server.
type fakeMC struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newFake() *fakeMC { return &fakeMC{m: map[string][]byte{}} }

func (f *fakeMC) Get(key string) (*memcache.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[key]
	if !ok {
		return nil, memcache.ErrCacheMiss
	}
	return &memcache.Item{Key: key, Value: append([]byte(nil), v...)}, nil
}

func (f *fakeMC) Set(it *memcache.Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[it.Key] = append([]byte(nil), it.Value...)
	return nil
}

func (f *fakeMC) Add(it *memcache.Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[it.Key]; ok {
		return memcache.ErrNotStored
	}
	f.m[it.Key] = append([]byte(nil), it.Value...)
	return nil
}

func (f *fakeMC) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[key]; !ok {
		return memcache.ErrCacheMiss
	}
	delete(f.m, key)
	return nil
}

func (f *fakeMC) addDelta(key string, delta uint64, neg bool) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.m[key]
	if !ok {
		return 0, memcache.ErrCacheMiss
	}
	n, _ := strconv.ParseUint(string(cur), 10, 64)
	if neg {
		if delta > n {
			n = 0
		} else {
			n -= delta
		}
	} else {
		n += delta
	}
	f.m[key] = []byte(strconv.FormatUint(n, 10))
	return n, nil
}

func (f *fakeMC) Increment(key string, d uint64) (uint64, error) { return f.addDelta(key, d, false) }
func (f *fakeMC) Decrement(key string, d uint64) (uint64, error) { return f.addDelta(key, d, true) }

func (f *fakeMC) Touch(key string, _ int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[key]; !ok {
		return memcache.ErrCacheMiss
	}
	return nil
}

func (f *fakeMC) DeleteAll() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m = map[string][]byte{}
	return nil
}

func (f *fakeMC) Ping() error { return nil }

func newTestCache() *Cache { return newWith(newFake()) }

func TestSupportedOps(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()

	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Get(ctx, "k"); err != nil || string(v) != "v" {
		t.Fatalf("get: %q %v", v, err)
	}
	if _, err := c.Get(ctx, "nope"); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("miss should be ErrNotFound, got %v", err)
	}
	if ok, _ := c.SetNX(ctx, "k", []byte("x"), 0); ok {
		t.Fatal("SetNX on existing key must return false")
	}
	if ok, _ := c.SetNX(ctx, "fresh", []byte("y"), 0); !ok {
		t.Fatal("SetNX on new key must return true")
	}
	if err := c.Del(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if has, _ := c.Has(ctx, "k"); has {
		t.Fatal("key should be gone after Del")
	}
}

func TestCounters(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()

	if v, err := c.Incr(ctx, "ctr", 5); err != nil || v != 5 {
		t.Fatalf("incr-from-missing init wrong: %d %v", v, err)
	}
	if v, _ := c.Incr(ctx, "ctr", 3); v != 8 {
		t.Fatalf("incr want 8, got %d", v)
	}
	if v, _ := c.Decr(ctx, "ctr", 2); v != 6 {
		t.Fatalf("decr want 6, got %d", v)
	}
	// Floors at 0 (unsigned memcached counter).
	if v, _ := c.Decr(ctx, "ctr", 999); v != 0 {
		t.Fatalf("decr below zero must floor at 0, got %d", v)
	}
}

func TestConcurrentIncr(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := c.Incr(ctx, "n", 1); err != nil {
					t.Errorf("incr: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if v, _ := c.Incr(ctx, "n", 0); v != 4000 {
		t.Fatalf("counter race: got %d want 4000", v)
	}
}

func TestUnsupportedOpsAreExplicit(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	if _, err := c.TTL(ctx, "k"); !errors.Is(err, cache.ErrUnsupported) {
		t.Fatalf("TTL should be ErrUnsupported, got %v", err)
	}
	if err := c.DeleteByPrefix(ctx, "p:"); !errors.Is(err, cache.ErrUnsupported) {
		t.Fatalf("DeleteByPrefix should be ErrUnsupported, got %v", err)
	}
	it := c.Iterate(ctx, cache.IterateOpts{})
	if it.Next() || !errors.Is(it.Err(), cache.ErrUnsupported) {
		t.Fatal("Iterate should yield nothing with ErrUnsupported")
	}
}

func TestClosed(t *testing.T) {
	c := newTestCache()
	_ = c.Close()
	if err := c.Close(); err != nil {
		t.Fatalf("Close must be idempotent: %v", err)
	}
	if _, err := c.Get(context.Background(), "k"); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
}
