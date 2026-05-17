# Construction

### New

`func New(mc *memcache.Client) *Cache`

What it is: wraps a `*github.com/bradfitz/gomemcache/memcache.Client` as a
(partial) `cache.Cache`. `Close` only flips a local flag; it never closes the
possibly-shared client.

Use cases:

- Back code written against `cache.Cache` with an existing Memcached fleet,
  without standing up Redis or Postgres.
- Drop-in interop where you only need the supported subset.

```go
package main

import (
	"context"
	"fmt"

	"github.com/bradfitz/gomemcache/memcache"
	memcachedcache "github.com/ubgo/cache-memcached"
)

func main() {
	mc := memcache.New("localhost:11211")
	c := memcachedcache.New(mc)
	defer c.Close()

	ctx := context.Background()
	_ = c.Set(ctx, "k", []byte("v"), 0)
	v, _ := c.Get(ctx, "k")
	fmt.Println(string(v)) // v
}
```

### Cache

`type Cache struct { ... }`

What it is: the adapter. Concurrency-safe (delegates to the gomemcache client).
Three methods are intentionally unsupported by Memcached's protocol — see
[Cache methods](cache-methods.md). Hold it as `cache.Cache` for backend-agnostic
code, but be aware `TTL`/`DeleteByPrefix`/`Iterate` will return
`cache.ErrUnsupported` here.

```go
import "github.com/ubgo/cache"

var generic cache.Cache = memcachedcache.New(mc)
// Code that calls TTL/DeleteByPrefix/Iterate must tolerate cache.ErrUnsupported.
```
