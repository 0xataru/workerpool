# workerpool — Changelog

All notable changes are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/).

Versions below 1.0.0 may break the API between releases.

## [Unreleased]

## [0.0.3] - 2026-08-04

### Changed

- `Map` dispatches work through an atomic counter instead of a channel. Overhead
  per input drops from ~330ns to ~40ns at 8 workers, and the point where the pool
  beats a plain sequential loop moves from ~1µs of work per input to ~60ns. Load
  balancing is unchanged — workers still claim the next index as they free up.
- `Map` no longer allocates a bookkeeping slice: the inputs it finished are the
  prefix below the counter, so which ones were skipped follows from the counter
  alone.

- Licence changed from MIT to Apache License 2.0. Copies obtained under MIT stay
  under MIT — relicensing only applies going forward.

### Added

- `benchmarks/` — a separate module comparing this package against conc, pond,
  ants and errgroup on the same task. Separate so the library keeps zero
  dependencies; the root `./...` does not descend into it.

## [0.0.2] - 2026-08-02

### Added

- `Makefile` with the usual targets (`check`, `test`, `stress`, `bench`, `cover`)
  and a release flow: `make bump VERSION=X.Y.Z` then `make tag`
- `scripts/bump.sh` — syncs `VERSION`, `CHANGELOG.md` and the README version
  badge; refuses an empty `[Unreleased]`, a malformed version or a repeat
- This changelog, `VERSION` and `.gitignore`
- README badges: version, pkg.go.dev, CI, Go Report Card, Go version, zero
  dependencies, licence
- README section documenting how the concurrency claims are tested, and what
  that evidence does not prove
- CI: `GOMAXPROCS` matrix (1, 2, 4) and a staticcheck job
- Nightly workflow running the stress suite with `-count=100`

### Changed

- `Map` now documents that the `inputs` slice must not be modified while the
  call is running

### Fixed

- Changelog entry for 0.0.1 referred to a `Submit` method that this package
  does not have

## [0.0.1] - 2026-08-02

### Added

- `Map` — runs a function over a slice on N workers, results in input order
- `Stream` — the same over an `iter.Seq`, yielding results as they complete
- `Result[In, Out]` pairing each outcome with the input it came from
- Cancellation on every blocking path: job dispatch, job receive and result send
- `Map` reports one `Result` per input even when cancelled; unreached inputs
  carry `ctx.Err()`
- Breaking out of a `Stream` cancels the run and stops the input being pulled
- Panic policy: a panic in `process` is not recovered, and it is tested
- Examples: `examples/tracking` (Map vs Stream) and `examples/checksum`
  (building an `iter.Seq` from a directory walk)
- Test suite: mutation-checked unit tests, randomised stress tests with logged
  seeds, goroutine-leak checks, barrier-based concurrency tests
- Benchmarks covering per-item overhead and the sequential crossover point
