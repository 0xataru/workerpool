# workerpool

<p align="center">
  <a href="VERSION"><img src="https://img.shields.io/badge/version-0.0.3-blue" alt="Version" /></a>
  <a href="https://pkg.go.dev/github.com/0xataru/workerpool"><img src="https://pkg.go.dev/badge/github.com/0xataru/workerpool.svg" alt="Go Reference" /></a>
  <a href="https://github.com/0xataru/workerpool/actions/workflows/ci.yml"><img src="https://github.com/0xataru/workerpool/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/0xataru/workerpool/actions/workflows/coverage.yml"><img src="https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/0xataru/workerpool/badges/coverage.json" alt="Coverage" /></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/0xataru/workerpool"><img src="https://api.scorecard.dev/projects/github.com/0xataru/workerpool/badge" alt="OpenSSF Scorecard" /></a>
  <a href="https://goreportcard.com/report/github.com/0xataru/workerpool"><img src="https://goreportcard.com/badge/github.com/0xataru/workerpool" alt="Go Report Card" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.23+-00ADD8?logo=go&logoColor=white" alt="Go 1.23+" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/dependencies-none-success" alt="Zero dependencies" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?logo=apache&logoColor=white" alt="License: Apache 2.0" /></a>
</p>

Run a function over many inputs on a fixed number of goroutines.

No lifecycle to manage, no channels to wire, no dependencies. Two functions and
two types — that is the whole library.

```sh
go get github.com/0xataru/workerpool
```

## Quick start

```go
results := workerpool.Map(ctx, 8, urls, fetch)

for _, r := range results {
    if r.Err != nil {
        log.Printf("%s: %v", r.Input, r.Err)
        continue
    }
    fmt.Println(r.Input, r.Value)
}
```

`fetch` is your own function, `func(context.Context, string) (Page, error)`. The
type parameters are inferred from it, so you never write them out.

For input that does not fit in memory, or when you want results as they arrive:

```go
for r := range workerpool.Stream(ctx, 8, lines, parse) {
    ...
}
```

## What it guarantees

- **`Map` preserves order.** One `Result` per input, in the order you passed
  them, no matter which worker finished first.
- **`Stream` yields as results complete**, in no particular order.
- **Cancellation is honoured everywhere.** `Map` still returns one `Result` per
  input: work that finished is kept, and the inputs it never reached carry
  `ctx.Err()`. `Stream` stops yielding.
- **Breaking out of a `Stream` cleans up.** The workers stop and the input
  sequence stops being pulled — safe with infinite sequences.
- **Nothing is buffered.** A slow consumer slows the workers down instead of
  letting results pile up in memory.
- **A failing input never aborts the batch.** The error is reported in
  `Result.Err`.
- **Panics are not recovered.** A panic in your function crashes the program
  exactly as it would without a pool. Recover in your own function if you want
  something else.

## API

```go
type Process[In, Out any] func(ctx context.Context, in In) (Out, error)

type Result[In, Out any] struct {
    Input In
    Value Out
    Err   error
}

func Map[In, Out any](ctx context.Context, workers int, inputs []In, process Process[In, Out]) []Result[In, Out]
func Stream[In, Out any](ctx context.Context, workers int, inputs iter.Seq[In], process Process[In, Out]) iter.Seq[Result[In, Out]]
```

Both panic if `workers` is below one or `process` is nil. Those are caller bugs
that would otherwise fail later and more confusingly — a nil function panicking
inside a worker goroutine, or a pool that quietly never finishes.

`Map` reads the `inputs` slice for as long as the call is running, so do not
modify it until `Map` returns.

## How it is tested

Claims about concurrency are worth as much as the evidence behind them, so here
is the evidence:

- **Race detector** on every run, in CI, across Go versions and with
  `GOMAXPROCS` pinned to 1, 2 and 4 — single-core scheduling surfaces different
  interleavings than a multi-core machine.
- **Mutation testing.** Every guarantee above was re-checked by deliberately
  breaking the implementation and confirming a test fails. Tests that pass on
  broken code prove nothing, and two of these did until they were fixed.
- **Stress tests** with randomised input sizes, worker counts and stop points,
  each run logging its seed so a failure can be replayed:
  `go test -race -count=20 -run Stress ./...`
- **Goroutine-leak checks** after runs that ended by cancellation and by `break`,
  half of them on a context nobody cancels, so only the package itself can clean
  up.
- **Concurrency is tested with barriers, not timeouts.** A worker count is
  verified by making every worker block until all of them have arrived, so the
  test cannot pass by getting lucky on a fast machine.
- **Fuzzing.** `FuzzMap` and `FuzzStream` let the fuzzer pick input sizes,
  worker counts and stop points, steered by coverage toward the branches random
  sampling rarely reaches: `make fuzz`.
- **The panic policy is tested too**, in a child process, since an unrecovered
  panic takes the test binary with it.

Why each of these is there, and what the code they test looks like:
[`docs/design.md`](docs/design.md).

What this does not mean: the race detector reports races it observes, not races
that exist. This package has no known defects and its tests are demonstrably
able to catch defects of this class — that is a different claim from proof.

## Performance

Measured with `make bench` on an 8-core machine, 10 000 inputs and a process
function that does nothing, so every number is pure overhead:

|                                             | ns per input | allocations |
| ------------------------------------------- | ------------ | ----------- |
| Plain `for` loop, no concurrency            | 0.6          | 1           |
| `Map`, 8 workers                            | **40**       | 11          |
| `Map`, 64 workers                           | 42           | 67          |
| Goroutine per input, bounded by a semaphore | 282          | 10 003      |

Allocations are the bigger story: `Map` allocates a constant number regardless of
how many inputs there are, because it reuses a fixed set of workers and hands
work out through an atomic counter rather than a channel.

None of that matters unless the work per input is small. The break-even point
against a plain loop is around **60ns of work per input** — below that, do not
use a pool at all; well above it, the pool scales:

| Work per input | Plain loop | `Map`, 8 workers |
| -------------- | ---------- | ---------------- |
| 5ns            | 4.8        | 39 (pool loses)  |
| 60ns           | 60         | 48               |
| 740ns          | 736        | 149              |
| 7.4µs          | 7443       | 1251             |

For anything doing I/O — an HTTP call, a database query, reading a file — the
overhead is irrelevant and the only question is how many workers the other side
tolerates.

Numbers come from one machine and a single run each; use `benchstat` over
`-count=10` before drawing conclusions from small differences.

### Against the alternatives

Same task — 10 000 inputs, 8 workers, one result per input in input order:

| | ns per input | allocs/op |
| --- | --- | --- |
| [conc](https://github.com/sourcegraph/conc) `iter.Map` | 39.2 – 39.8 | 13 |
| **this package** | 40.0 – 40.8 | 12 |
| [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) + `SetLimit` | 305 – 310 | 20 003 |
| [pond](https://github.com/alitto/pond) v2 | 314 – 324 | 43 472 |
| [ants](https://github.com/panjf2000/ants) v2 | 343 – 346 | 0 |

We are **tied for fastest, not fastest**: `conc` matches us within noise, and both
are about 8× ahead of the rest. `ants` allocates nothing once its pool is reused,
which is what it is for. The reasons to pick this package are elsewhere —
maintenance, the streaming API, per-input results — and the benchmarks exist to
show that picking it costs nothing in speed.

Full table, methodology and where each comparison is unfair:
[`benchmarks/README.md`](benchmarks/README.md).

## Recipes

Retries, rate limits and the rest are deliberately not built in. Each is a few
lines around your own `process`, and each is a tested example on
[pkg.go.dev](https://pkg.go.dev/github.com/0xataru/workerpool#pkg-examples):

| You want | How | Example |
| --- | --- | --- |
| Retries with backoff | wrap `process` in a retry loop | [`Map (Retry)`](https://pkg.go.dev/github.com/0xataru/workerpool#example-Map-Retry) |
| Stop at the first error | cancel a derived context from inside `process` | [`Map (FailFast)`](https://pkg.go.dev/github.com/0xataru/workerpool#example-Map-FailFast) |
| A timeout or Ctrl+C | pass a cancellable `ctx`; unreached inputs get `ctx.Err()` | [`Map (Cancellation)`](https://pkg.go.dev/github.com/0xataru/workerpool#example-Map-Cancellation) |
| N calls per second | a shared `time.Ticker` every worker waits on | [`Stream (RateLimit)`](https://pkg.go.dev/github.com/0xataru/workerpool#example-Stream-RateLimit) |
| Progress reporting | count in the `range` loop — results arrive in your goroutine | [`Stream (Progress)`](https://pkg.go.dev/github.com/0xataru/workerpool#example-Stream-Progress) |
| Stop once you have an answer | `break` out of a `Stream` | [`Stream (EarlyExit)`](https://pkg.go.dev/github.com/0xataru/workerpool#example-Stream-EarlyExit) |

For whole programs, see [`examples/tracking`](examples/tracking) (Map vs Stream
on a mock API) and [`examples/checksum`](examples/checksum) (turning a directory
walk into an `iter.Seq`).

## When you do not need this

- **One input.** Just call the function.
- **You only care whether anything failed**, not about per-input results —
  [`errgroup`](https://pkg.go.dev/golang.org/x/sync/errgroup) with `SetLimit` is
  a better fit.
- **You need priorities, persistent queues or a long-lived pool you submit to
  over time.** Deliberately out of scope — this package runs one batch per call.
  Retries and rate limits are not, though: see [Recipes](#recipes).

## Requirements

Go 1.23 or newer (`Stream` uses `iter.Seq`). No third-party dependencies.

## License

[Apache License 2.0](LICENSE)
