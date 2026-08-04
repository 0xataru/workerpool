# Benchmarks against the alternatives

Separate module, so the library itself keeps zero dependencies. `go test ./...`
at the repo root does not descend here.

```sh
cd benchmarks
go test -run '^$' -bench . -benchmem -count=5
```

## The task being measured

Every benchmark does the same thing: **apply a function to 10 000 inputs on at
most N goroutines and collect one result per input, in input order.** That is the
task this library exists for, so it is the one worth timing.

The work function is trivial (`i*31 + 7`), so what is measured is dispatch and
collection overhead, not the work. With real work per input the numbers converge —
see the crossover table in the root README.

## Results

8 workers, 10 000 inputs, 5 runs, Apple Silicon 8-core, Go 1.26, no `-race`:

| | ns per input | bytes/op | allocs/op |
| --- | --- | --- | --- |
| Plain `for` loop (floor, no concurrency) | 0.71 | 81 920 | 1 |
| [conc](https://github.com/sourcegraph/conc) `iter.Map` | **39.2 – 39.8** | 82 257 | 13 |
| **this package**, `Map` | **40.0 – 40.8** | 410 532 | 12 |
| [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) + `SetLimit` | 305 – 310 | 802 096 | 20 003 |
| [pond](https://github.com/alitto/pond) v2, pool reused | 314 – 324 | 2 011 719 | 43 472 |
| pond v2, pool per batch | 322 – 328 | 2 034 827 | 43 145 |
| [ants](https://github.com/panjf2000/ants) v2, pool reused | 343 – 346 | 86 | 0 |
| ants v2, pool per batch | 348 – 350 | 87 892 | 68 |

## Reading them honestly

**We are not the fastest — we are tied for fastest.** `conc`'s `iter.Map` matches
`Map` to within about 2%, which is inside the noise of this setup. Any claim of
being *the* fastest would be false. What is true: both are roughly **8× faster
than errgroup, pond and ants** at this particular task.

**`conc` uses 5× fewer bytes, and that difference is by design, not waste.** Our
`Result[In, Out]` carries the input, the value and an error — 32 bytes per item
against 8 for a bare `[]int`. That is what buys per-input error reporting: you
learn *which* input failed. `conc.iter.Map` has no error channel at all
(`MapErr` aggregates), takes no `context`, and cannot be cancelled — it is doing
strictly less work, which is also part of why it ties on speed.

**`ants` allocates essentially nothing** once its pool is reused: 86 bytes and 0
allocations per batch of 10 000. That is its entire purpose — recycling goroutines
under sustained load — and it delivers on it. It is 8.6× slower per item here
because the submission path costs more than an atomic counter does.

**`pond` allocates a future per task**: 43 000 allocations per batch. That is the
price of its `Submit` → `.Wait()` API, which offers something we do not.

**Where the comparison is unfair to `ants` and `pond`:** both are built for a
long-lived pool fed by many concurrent producers, and their own published
benchmarks measure that. Here a single goroutine submits in a loop, so their
submission path is on the critical path in a way it would not be under their
intended usage. Both were also measured with the pool reused across iterations to
remove setup cost — it changed little.

**Where the comparison is unfair to errgroup:** it is not a worker pool at all.
`SetLimit` is a semaphore, so every input still gets its own goroutine — hence
20 003 allocations. It is included because it is what most projects already have
in `go.mod`, not because it is trying to solve this problem.

## So what is the actual argument for this package

Not speed. On throughput we are level with `conc` and ahead of the rest, and that
is worth knowing, but it is not a reason to choose a library:

- `conc` has been unmaintained since v0.3.0 (February 2023) and never reached 1.0.
- Nothing on this list has an iterator-native streaming API, so unbounded input
  means materialising a slice first.
- Nothing on this list reports per-input results *and* honours cancellation *and*
  tells you which inputs it never reached.

Those are the differences that matter. The benchmarks exist to show that choosing
them costs nothing in speed.

## Caveats

One machine, one architecture, one work function. Differences under about 5% here
mean nothing. Use `benchstat` over `-count=10` before drawing conclusions from
small gaps, and re-measure with your own work function before assuming any of
this transfers.
