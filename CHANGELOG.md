# Changelog

All notable changes to `github.com/ubgo/cache-memcached` are documented here.
Format follows Keep a Changelog; the project follows SemVer (pre-GA in `v0.x`).

## [Unreleased]

### Added

- Memcached adapter (gomemcache) implementing the supported subset of
  `cache.Cache`: Get/Set/SetNX(ADD)/Expire(TOUCH)/Touch/Incr/Decr/Del/Flush/
  Has/Ping.
- `TTL`, `DeleteByPrefix`, `Iterate` return `cache.ErrUnsupported` (memcached
  protocol limitation, documented).
- Unit-tested via an in-process fake (no server); optional real-server checks
  gated by `MEMCACHED_ADDR`.

[Unreleased]: https://github.com/ubgo/cache-memcached/commits/main
