# How workerpool works, and how we know it does

A worker pool is the first concurrency pattern most Go programmers write, and
one of the easiest to get subtly wrong. Ours is about 150 lines. This is a tour
of the decisions in those lines, and of the testing that backs each one,
because for concurrent code the tests are most of the work.

## Two functions, no lifecycle

Most pool libraries give you an object: create it, submit to it, wait on it,
close it. Every one of those steps is a way to get something wrong: submitting
after close, forgetting to wait, closing twice, leaking the pool when an error
path returns early.

This package has no object. There are two calls:

```go
results := workerpool.Map(ctx, 8, inputs, process)          // slice in, slice out
for r := range workerpool.Stream(ctx, 8, seq, process) {...} // iter.Seq in, iter.Seq out
```

Each call starts its goroutines and has stopped all of them by the time it
returns. Because there is nothing to start or stop, nothing can be started or
stopped wrongly. That rules out a whole class of bugs by construction instead
of by documentation.

## Map: one atomic counter instead of a channel

The obvious design feeds indices to workers through a channel. It works, and it
gives you dynamic load balancing for free: whoever is idle takes the next job.
It is also slow. A channel hand-off costs roughly **330ns per input**, which for
cheap work was most of the cost of `Map`.

The current design keeps the load balancing without the hand-off. All workers
share one `atomic.Int64`, and each one claims its next index with `Add(1)`:

```go
i := int(next.Add(1)) - 1
if i >= len(inputs) {
    return // everything has been claimed
}
value, err := process(ctx, inputs[i])
results[i] = Result{Input: inputs[i], Value: value, Err: err}
```

This brought per-input overhead down to about **40ns**, and moved the point
where a pool beats a plain loop from around 1µs of work per input to around
60ns. The results slice needs no lock, since each index is written by exactly
one worker, and `wg.Wait()` provides the happens-before edge that makes reading
it afterwards safe.

### Cancellation comes for free from the counter

`Map` promises one `Result` per input even when cancelled, with `ctx.Err()` on
every input it never reached. The first version tracked that with a separate
bookkeeping slice. The counter made the slice unnecessary.

Indices are handed out in order from zero, and a worker always writes the index
it claimed before it loops again. So once every worker has returned, the
finished inputs are exactly the prefix below the counter, and everything at or
above it was never started:

```go
if err := ctx.Err(); err != nil {
    for i := min(int(next.Load()), len(inputs)); i < len(inputs); i++ {
        results[i] = Result{Input: inputs[i], Err: err}
    }
}
```

That is one invariant that was already true, so the extra state could be
deleted.

## Stream: every blocking point needs a way out

`Stream` cannot use the counter trick, because its input is an `iter.Seq`. There
is no length and no indexing, and the input may be infinite. So it uses
channels: a feeder goroutine pulls from the sequence into a queue, workers move
jobs from the queue to a results channel, and the caller's `range` loop drains
the results.

Each of those goroutines has a point where it can block forever, and each one
needs a cancellation arm:

1. **The feeder sending to the queue.** If the caller breaks out of the loop,
   nobody will take the next job.
2. **A worker receiving from the queue.** An idle worker waits for a job that
   will never arrive.
3. **A worker sending a result.** If the caller has stopped reading, a worker
   blocked here never reaches `wg.Done()`, and the pool never shuts down.

Miss any one of them and `break` turns into a goroutine leak. `Stream` derives
its own context and cancels it in a `defer`, so leaving the `range` loop by any
route unwinds all three.

Two more decisions follow from this:

- **Nothing is buffered.** The channels are unbuffered on purpose. A slow
  consumer slows the workers down, so a fast producer cannot fill memory with
  results nobody has read yet.
- **The feeder owns the queue.** Only the feeder sends on it, so only the
  feeder closes it, which means a close can never race with a send.

## Panics are not recovered

When a `process` function panics, the program crashes, just as it would have
without the pool. Recovering inside the pool would mean choosing for the caller
what a panic means: turn it into an error? Log it? Retry it? Every option hides
a bug in someone's code. A caller who wants one of those behaviours can recover
in their own `process`, where the choice is visible.

## How the claims are tested

Saying "it is concurrent and correct" means little unless the tests could have
caught it being wrong. The testing approach is built around that.

**Mutation testing.** Every guarantee in the README was checked by breaking the
implementation on purpose, for instance by removing a cancellation arm, and
confirming that some test fails. Two tests did not
fail on broken code. They passed, looked fine, and proved nothing, until they
were fixed. A test that passes on broken code is worse than none, because it
looks like evidence.

**Barriers, not timeouts.** To check that `Map` really runs N things at once,
every worker blocks until all N have arrived. With fewer than N concurrent
workers the test hangs and fails. It cannot pass just because the machine was
fast, which is the usual failure of `time.Sleep`-based concurrency tests.

**Schedules other than your laptop's.** CI runs the race detector with
`GOMAXPROCS` pinned to 1, 2 and 4. A single-core schedule surfaces different
interleavings than an 8-core one, and some bugs appear only there.

**Randomised stress, with replayable seeds.** The stress tests pick random input
sizes, worker counts and stop points, and log their seed so any failure can be
replayed exactly. A nightly job runs them 100 times on each of three
`GOMAXPROCS` settings.

**Fuzzing.** `FuzzMap` and `FuzzStream` check the same invariants, but let the
fuzzer choose the shape of each run, steered by coverage toward the branches
random sampling rarely reaches.

**Leak checks.** After runs that end by cancellation and by `break`, the tests
check that the goroutine count returns to where it started. Half of those runs
use a context nobody cancels, so only the package itself can have cleaned up.

**The panic policy too.** An unrecovered panic takes the test binary down with
it, so that test runs in a child process and checks how the child died.

None of this is proof. The race detector reports races it observes, not every
race that exists. The claim is narrower and checkable: the package has no known
defects, and its tests have been shown to catch defects of the kinds it is most
likely to have.

## Performance, honestly

With 10,000 inputs, 8 workers and a no-op `process`, `Map` costs about 40ns and
12 allocations per call, regardless of input count. That is about the same as
[conc](https://github.com/sourcegraph/conc) and about 8× faster than errgroup,
pond or ants on the same task. The full comparison, including where each
comparison is unfair, is in [`benchmarks/README.md`](../benchmarks/README.md).

The more useful number is the break-even point. Below about 60ns of work per
input, a plain `for` loop wins and you should not use a pool. For anything doing
I/O, pool overhead is irrelevant, and the only real question is how many
concurrent calls the other side can handle.
