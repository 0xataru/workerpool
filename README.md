# workerpool

<p align="center">
  <a href="https://github.com/0xataru/workerpool"><img src="https://img.shields.io/badge/github-0xataru%2Fworkerpool-181717?logo=github" alt="GitHub" /></a>
  <a href="VERSION"><img src="https://img.shields.io/badge/version-0.0.2-blue" alt="Version" /></a>
  <a href="https://pkg.go.dev/github.com/0xataru/workerpool"><img src="https://pkg.go.dev/badge/github.com/0xataru/workerpool.svg" alt="Go Reference" /></a>
  <a href="https://github.com/0xataru/workerpool/actions/workflows/ci.yml"><img src="https://github.com/0xataru/workerpool/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://goreportcard.com/report/github.com/0xataru/workerpool"><img src="https://goreportcard.com/badge/github.com/0xataru/workerpool" alt="Go Report Card" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.23+-00ADD8?logo=go&logoColor=white" alt="Go 1.23+" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/dependencies-none-success" alt="Zero dependencies" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="License: MIT" /></a>
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
- **The panic policy is tested too**, in a child process, since an unrecovered
  panic takes the test binary with it.

What this does not mean: the race detector reports races it observes, not races
that exist. This package has no known defects and its tests are demonstrably
able to catch defects of this class — that is a different claim from proof.

## When you do not need this

- **One input.** Just call the function.
- **You only care whether anything failed**, not about per-input results —
  [`errgroup`](https://pkg.go.dev/golang.org/x/sync/errgroup) with `SetLimit` is
  a better fit.
- **You need retries, rate limiting, priorities or metrics.** Deliberately out of
  scope. Wrap your own `process` — a retry loop around it is a few lines and
  keeps this API small.

## Requirements

Go 1.23 or newer (`Stream` uses `iter.Seq`). No third-party dependencies.

## License

MIT
