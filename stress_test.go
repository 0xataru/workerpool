package workerpool_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xataru/workerpool"
)

// The tests in this file hammer the package with randomised sizes, worker counts
// and stop points. The targeted tests next door each pin down one invariant in
// one scenario; these check that the same invariants survive combinations nobody
// thought to write down — in particular cancellation landing at an awkward
// moment, which is where every bug in this package has lived so far.
//
// Run them harder with:
//
//	go test -race -count=20 -run Stress ./...
//
// and lighter with -short.

// newRand seeds from a fresh random value and logs it. A stress failure is only
// useful if it can be replayed, and go test prints the log of a failing test.
func newRand(t *testing.T) *rand.Rand {
	t.Helper()

	seed := rand.Uint64()
	t.Logf("stress seed: %d (hardcode it in newRand to replay this run)", seed)

	return rand.New(rand.NewPCG(seed, 0x5EED))
}

func rounds(short, long int) int {
	if testing.Short() {
		return short
	}
	return long
}

// Map's contract has to hold no matter when the cancellation lands: one Result
// per input, in input order, and every error either the caller's or ctx's.
func TestStressMapUnderRandomCancellation(t *testing.T) {
	t.Parallel()

	rng := newRand(t)

	for round := range rounds(30, 400) {
		inputs := seq(1 + rng.IntN(1000))
		workers := 1 + rng.IntN(32)
		// 0 means "let the round finish"; otherwise cancel once that many inputs
		// have been processed, which lands the cancellation mid-flight.
		cancelAt := rng.IntN(len(inputs) + 1)

		ctx, cancel := context.WithCancel(context.Background())
		var completed atomic.Int64

		results := waitFor(t, runAsync(func() []workerpool.Result[int, int] {
			return workerpool.Map(ctx, workers, inputs, func(_ context.Context, i int) (int, error) {
				if cancelAt > 0 && int(completed.Add(1)) == cancelAt {
					cancel()
				}
				return i * 2, nil
			})
		}), wait)
		cancel()

		if len(results) != len(inputs) {
			t.Fatalf("round %d (inputs=%d workers=%d cancelAt=%d): got %d results, want %d",
				round, len(inputs), workers, cancelAt, len(results), len(inputs))
		}

		for i, r := range results {
			if r.Input != inputs[i] {
				t.Fatalf("round %d (inputs=%d workers=%d cancelAt=%d): position %d holds input %d, want %d",
					round, len(inputs), workers, cancelAt, i, r.Input, inputs[i])
			}

			switch {
			case r.Err == nil:
				if r.Value != inputs[i]*2 {
					t.Fatalf("round %d: input %d: got value %d, want %d",
						round, r.Input, r.Value, inputs[i]*2)
				}
			case errors.Is(r.Err, context.Canceled):
				// Expected for inputs the run never reached.
			default:
				t.Fatalf("round %d: input %d: unexpected error %v", round, r.Input, r.Err)
			}
		}

		if cancelAt == 0 {
			for _, r := range results {
				if r.Err != nil {
					t.Fatalf("round %d: input %d failed in a round that was never cancelled: %v",
						round, r.Input, r.Err)
				}
			}
		}
	}
}

// Stream is stopped at a random point, half the time by cancelling and half by
// breaking out of the range. Either way it must terminate, and it must never
// invent, duplicate or lose a result.
func TestStressStreamUnderRandomStops(t *testing.T) {
	t.Parallel()

	rng := newRand(t)

	for round := range rounds(30, 400) {
		inputs := seq(1 + rng.IntN(1000))
		workers := 1 + rng.IntN(32)
		stopAfter := rng.IntN(len(inputs) + 1) // 0 means "let it finish"
		byCancel := rng.IntN(2) == 0

		ctx, cancel := context.WithCancel(context.Background())

		seen := waitFor(t, runAsync(func() []int {
			var seen []int
			for r := range workerpool.Stream(ctx, workers, slices.Values(inputs), double) {
				if r.Err != nil {
					t.Errorf("round %d: input %d: unexpected error %v", round, r.Input, r.Err)
				}
				seen = append(seen, r.Input)

				if stopAfter > 0 && len(seen) == stopAfter {
					if byCancel {
						cancel()
					} else {
						break
					}
				}
			}
			return seen
		}), wait)
		cancel()

		where := func() string {
			if byCancel {
				return "cancel"
			}
			return "break"
		}()

		if len(seen) > len(inputs) {
			t.Fatalf("round %d (inputs=%d workers=%d stopAfter=%d via %s): got %d results, more than inputs",
				round, len(inputs), workers, stopAfter, where, len(seen))
		}

		sorted := slices.Clone(seen)
		slices.Sort(sorted)
		if unique := slices.Compact(slices.Clone(sorted)); len(unique) != len(sorted) {
			t.Fatalf("round %d (inputs=%d workers=%d stopAfter=%d via %s): %d results but only %d distinct inputs",
				round, len(inputs), workers, stopAfter, where, len(sorted), len(unique))
		}
		for _, in := range sorted {
			if in < 1 || in > len(inputs) {
				t.Fatalf("round %d: yielded input %d, which was never submitted", round, in)
			}
		}

		switch {
		case stopAfter == 0 && len(seen) != len(inputs):
			t.Fatalf("round %d (inputs=%d workers=%d): got %d results in a round that ran to completion",
				round, len(inputs), workers, len(seen))
		case stopAfter > 0 && !byCancel && len(seen) != stopAfter:
			// A break stops the range immediately, so the count is exact. A
			// cancel is racy by nature: results already in flight still arrive.
			t.Fatalf("round %d (inputs=%d workers=%d): break at %d yielded %d results",
				round, len(inputs), workers, stopAfter, len(seen))
		case stopAfter > 0 && byCancel && len(seen) < stopAfter:
			t.Fatalf("round %d (inputs=%d workers=%d): cancel at %d yielded only %d results",
				round, len(inputs), workers, stopAfter, len(seen))
		}
	}
}

// Goroutines must not accumulate across many short runs, whichever way each run
// ended. Counting is only meaningful in isolation, so this test is not parallel:
// go test finishes the sequential tests before resuming the parallel ones.
func TestStressLeavesNoGoroutinesBehind(t *testing.T) {
	rng := newRand(t)
	n := rounds(40, 400)

	before := runtime.NumGoroutine()

	for range n {
		inputs := seq(1 + rng.IntN(50))
		workers := 1 + rng.IntN(8)
		stopAfter := rng.IntN(len(inputs) + 1)

		// Half the rounds run on a context nobody ever cancels, so the only thing
		// that can clean up after a break is the package itself. Cancelling in
		// every round would hide a missing internal cancel: the parent context
		// would unwind the workers on the test's behalf.
		ctx := context.Background()
		cancel := func() {}
		if rng.IntN(2) == 0 {
			ctx, cancel = context.WithCancel(ctx)
		}

		count := 0
		for range workerpool.Stream(ctx, workers, slices.Values(inputs), double) {
			count++
			if stopAfter > 0 && count == stopAfter {
				break
			}
		}
		workerpool.Map(ctx, workers, inputs, double)

		cancel()
	}

	// The workers unwind asynchronously, so poll rather than sample once.
	for range 200 {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutines outlived %d runs: %d before, %d after", n, before, runtime.NumGoroutine())
}
