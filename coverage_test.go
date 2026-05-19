// coverage_test.go — targeted branch coverage for the memcached adapter:
// New constructor, closed-adapter guards on every method, GetMulti partial +
// non-miss error, Has/Set error mapping, Expire/Touch not-found mapping,
// SetMulti, Flush, Ping, Stats, iterator accessors, and the addInt
// ErrNotStored-retry / Add-error branches via a configurable fake. The fake
// behaviors live in test code only — no production change.

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

var errNet = errors.New("network down")

// flakyMC wraps fakeMC and injects errors / ErrNotStored on demand to drive
// the adapter's error and counter-race branches deterministically.
type flakyMC struct {
	*fakeMC
	failGet     bool
	failGetErr  error
	failSet     bool
	failTouch   bool // returns a non-miss error from Touch
	failPing    bool
	failDelete  bool
	addErr      error // returned by Add when set
	addNotStore bool  // Add returns ErrNotStored once, then behaves normally
	addCount    int
}

func (f *flakyMC) Get(k string) (*memcache.Item, error) {
	if f.failGet {
		if f.failGetErr != nil {
			return nil, f.failGetErr
		}
		return nil, errNet
	}
	return f.fakeMC.Get(k)
}
func (f *flakyMC) Set(it *memcache.Item) error {
	if f.failSet {
		return errNet
	}
	return f.fakeMC.Set(it)
}
func (f *flakyMC) Add(it *memcache.Item) error {
	if f.addErr != nil {
		return f.addErr
	}
	if f.addNotStore {
		f.addCount++
		if f.addCount == 1 {
			// Simulate a racer having created the key first: store it so the
			// retried Increment/Decrement finds a value, and report NotStored.
			_ = f.fakeMC.Add(it)
			return memcache.ErrNotStored
		}
	}
	return f.fakeMC.Add(it)
}
func (f *flakyMC) Touch(k string, s int32) error {
	if f.failTouch {
		return errNet
	}
	return f.fakeMC.Touch(k, s)
}
func (f *flakyMC) Delete(k string) error {
	if f.failDelete {
		return errNet
	}
	return f.fakeMC.Delete(k)
}
func (f *flakyMC) Ping() error {
	if f.failPing {
		return errNet
	}
	return f.fakeMC.Ping()
}

func TestNewConstructor(t *testing.T) {
	// New must construct without dialing (gomemcache is lazy).
	c := New(memcache.New("127.0.0.1:0"))
	if c == nil {
		t.Fatal("New returned nil")
	}
}

func TestClosedReturnsErrClosedAllMethods(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	_ = c.Close()

	if _, err := c.Get(ctx, "k"); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.GetMulti(ctx, []string{"k"}); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("GetMulti: %v", err)
	}
	if _, err := c.Has(ctx, "k"); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Has: %v", err)
	}
	if err := c.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Set: %v", err)
	}
	if err := c.SetMulti(ctx, map[string]cache.Item{"k": {Value: []byte("v")}}); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("SetMulti: %v", err)
	}
	if _, err := c.SetNX(ctx, "k", []byte("v"), 0); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("SetNX: %v", err)
	}
	if err := c.Expire(ctx, "k", time.Minute); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Expire: %v", err)
	}
	if err := c.Touch(ctx, "k"); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Touch: %v", err)
	}
	if _, err := c.Incr(ctx, "k", 1); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Incr: %v", err)
	}
	if _, err := c.Decr(ctx, "k", 1); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Decr: %v", err)
	}
	if err := c.Del(ctx, "k"); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Del: %v", err)
	}
	if err := c.Flush(ctx); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Flush: %v", err)
	}
	if err := c.Ping(ctx); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Ping: %v", err)
	}
	// Unsupported ops do not gate on closed; still ErrUnsupported.
	if _, err := c.TTL(ctx, "k"); !errors.Is(err, cache.ErrUnsupported) {
		t.Fatalf("TTL: %v", err)
	}
	if err := c.DeleteByPrefix(ctx, "p"); !errors.Is(err, cache.ErrUnsupported) {
		t.Fatalf("DeleteByPrefix: %v", err)
	}
	if s := c.Stats(); !isZeroStats(s) {
		t.Fatalf("Stats: %+v", s)
	}
}

// isZeroStats reports whether s is the zero snapshot (cache.Stats holds an
// uncomparable map field, so == cannot be used).
func isZeroStats(s cache.Stats) bool {
	return s.Hits == 0 && s.Misses == 0 && s.Sets == 0 && s.Deletes == 0 &&
		s.Evictions == 0 && s.Entries == 0 && s.Bytes == 0 &&
		len(s.EvictionsByCause) == 0
}

func TestGetMultiPartialAndError(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	if err := c.Set(ctx, "a", []byte("av"), 0); err != nil {
		t.Fatal(err)
	}
	m, err := c.GetMulti(ctx, []string{"a", "missing"})
	if err != nil || len(m) != 1 || string(m["a"]) != "av" {
		t.Fatalf("partial GetMulti = %v %v", m, err)
	}
	// Non-miss error aborts.
	cf := newWith(&flakyMC{fakeMC: newFake(), failGet: true})
	if _, err := cf.GetMulti(ctx, []string{"x"}); !errors.Is(err, errNet) {
		t.Fatalf("GetMulti non-miss error must surface, got %v", err)
	}
}

func TestHasTrueFalseError(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	if err := c.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.Has(ctx, "k"); err != nil || !ok {
		t.Fatalf("Has true: %v %v", ok, err)
	}
	if ok, err := c.Has(ctx, "missing"); err != nil || ok {
		t.Fatalf("Has false: %v %v", ok, err)
	}
	cf := newWith(&flakyMC{fakeMC: newFake(), failGet: true})
	if _, err := cf.Has(ctx, "k"); !errors.Is(err, errNet) {
		t.Fatalf("Has error must surface, got %v", err)
	}
}

func TestSetErrorAndSetMulti(t *testing.T) {
	ctx := context.Background()
	cf := newWith(&flakyMC{fakeMC: newFake(), failSet: true})
	if err := cf.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, errNet) {
		t.Fatalf("Set error: %v", err)
	}
	// SetMulti surfaces the first Set error.
	if err := cf.SetMulti(ctx, map[string]cache.Item{"k": {Value: []byte("v")}}); !errors.Is(err, errNet) {
		t.Fatalf("SetMulti error: %v", err)
	}
	// SetMulti happy path.
	c := newTestCache()
	if err := c.SetMulti(ctx, map[string]cache.Item{"a": {Value: []byte("1")}, "b": {Value: []byte("2"), TTL: time.Minute}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get(ctx, "b"); string(v) != "2" {
		t.Fatalf("SetMulti b = %q", v)
	}
}

func TestSetNXErrorPath(t *testing.T) {
	ctx := context.Background()
	cf := newWith(&flakyMC{fakeMC: newFake(), addErr: errNet})
	if ok, err := cf.SetNX(ctx, "k", []byte("v"), 0); ok || !errors.Is(err, errNet) {
		t.Fatalf("SetNX Add error: %v %v", ok, err)
	}
}

func TestExpireTouchNotFoundMapping(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	// Touch on a missing key -> memcache.ErrCacheMiss -> mapped ErrNotFound.
	if err := c.Expire(ctx, "missing", time.Minute); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("Expire missing: %v", err)
	}
	if err := c.Touch(ctx, "missing"); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("Touch missing: %v", err)
	}
	// Existing key.
	if err := c.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Expire(ctx, "k", time.Minute); err != nil {
		t.Fatalf("Expire existing: %v", err)
	}
	if err := c.Touch(ctx, "k"); err != nil {
		t.Fatalf("Touch existing: %v", err)
	}
	// Non-miss Touch error passes through unmapped.
	cf := newWith(&flakyMC{fakeMC: newFake(), failTouch: true})
	if err := cf.Expire(ctx, "k", time.Minute); !errors.Is(err, errNet) {
		t.Fatalf("Expire non-miss error: %v", err)
	}
}

func TestAddIntErrNotStoredRetry(t *testing.T) {
	ctx := context.Background()
	// Increment-from-missing -> Add returns ErrNotStored (racer won) -> retry
	// Increment against the racer's value.
	c := newWith(&flakyMC{fakeMC: newFake(), addNotStore: true})
	v, err := c.Incr(ctx, "n", 5)
	if err != nil {
		t.Fatalf("Incr retry: %v", err)
	}
	// fake stored "5" via the racer-Add then retried Increment(+5) -> 10.
	if v != 10 {
		t.Fatalf("Incr ErrNotStored retry value = %d, want 10", v)
	}
}

func TestAddIntAddErrorPropagates(t *testing.T) {
	ctx := context.Background()
	// Increment-from-missing, Add fails with a non-NotStored error -> return.
	c := newWith(&flakyMC{fakeMC: newFake(), addErr: errNet})
	if _, err := c.Incr(ctx, "n", 1); !errors.Is(err, errNet) {
		t.Fatalf("Incr Add error must propagate, got %v", err)
	}
	// Decrement-from-missing, Add fails with a non-NotStored error -> return.
	c2 := newWith(&flakyMC{fakeMC: newFake(), addErr: errNet})
	if _, err := c2.Decr(ctx, "n", 1); !errors.Is(err, errNet) {
		t.Fatalf("Decr Add error must propagate, got %v", err)
	}
}

func TestDecrFromMissingFloorsAtZero(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	// Decr a missing key: Add "0" then Decrement -> floors at 0.
	if v, err := c.Decr(ctx, "d", 5); err != nil || v != 0 {
		t.Fatalf("Decr from missing must floor at 0, got %d %v", v, err)
	}
}

func TestDelMissTolerated(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	// Deleting a missing key is tolerated (ErrCacheMiss swallowed).
	if err := c.Del(ctx, "missing"); err != nil {
		t.Fatalf("Del miss must be tolerated: %v", err)
	}
	// Non-miss delete error surfaces.
	cf := newWith(&flakyMC{fakeMC: newFake(), failDelete: true})
	if err := cf.Del(ctx, "k"); !errors.Is(err, errNet) {
		t.Fatalf("Del non-miss error: %v", err)
	}
}

func TestFlushAndPing(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	if err := c.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if has, _ := c.Has(ctx, "k"); has {
		t.Fatal("Flush did not clear")
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	cf := newWith(&flakyMC{fakeMC: newFake(), failPing: true})
	if err := cf.Ping(ctx); !errors.Is(err, errNet) {
		t.Fatalf("Ping error: %v", err)
	}
}

func TestUnsupportedIterAccessors(t *testing.T) {
	c := newTestCache()
	it := c.Iterate(context.Background(), cache.IterateOpts{})
	if it.Next() {
		t.Fatal("Next must be false")
	}
	if it.Key() != "" {
		t.Fatalf("Key = %q", it.Key())
	}
	if it.Value() != nil {
		t.Fatalf("Value = %v", it.Value())
	}
	if !errors.Is(it.Err(), cache.ErrUnsupported) {
		t.Fatalf("Err = %v", it.Err())
	}
	if err := it.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
}

func TestStatsZero(t *testing.T) {
	if s := newTestCache().Stats(); !isZeroStats(s) {
		t.Fatalf("Stats not zero: %+v", s)
	}
}

// Sanity: the concurrent retry path stays correct even with a one-shot
// ErrNotStored injected, exercised under -race elsewhere; here we just pin
// a deterministic single-goroutine retry to a known value.
func TestConcurrentRetryDeterministic(t *testing.T) {
	ctx := context.Background()
	c := newWith(&flakyMC{fakeMC: newFake(), addNotStore: true})
	var wg sync.WaitGroup
	wg.Add(1)
	var got int64
	go func() {
		defer wg.Done()
		got, _ = c.Incr(ctx, "x", 3)
	}()
	wg.Wait()
	if got != 6 { // racer-Add stored "3", retry Increment(+3) -> 6
		t.Fatalf("retry value = %d, want 6 (raw=%s)", got, strconv.Itoa(int(got)))
	}
}
