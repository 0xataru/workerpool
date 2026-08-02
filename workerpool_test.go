// Tests live outside the package on purpose: they see exactly the API a caller
// sees. An invariant that cannot be expressed from out here would mean the
// exported surface is incomplete.
package workerpool_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xataru/workerpool"
)

const wait = 5 * time.Second

func double(_ context.Context, i int) (int, error) { return i * 2, nil }

func seq(n int) []int {
	inputs := make([]int, n)
	for i := range inputs {
		inputs[i] = i + 1
	}
	return inputs
}

// waitFor fails the test instead of hanging when a channel never delivers.
func waitFor[T any](t *testing.T, ch <-chan T, timeout time.Duration) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		t.Fatalf("nothing arrived within %v", timeout)
		var zero T
		return zero
	}
}

// runAsync calls fn in a goroutine so a hang shows up as a test failure rather
// than as go test's 10-minute timeout panic.
func runAsync[T any](fn func() T) <-chan T {
	out := make(chan T, 1)
	go func() { out <- fn() }()
	return out
}

// --- Map ---------------------------------------------------------------------

func TestMapReturnsOneResultPerInputInOrder(t *testing.T) {
	t.Parallel()

	inputs := seq(10)
	results := workerpool.Map(context.Background(), 5, inputs, double)

	if len(results) != len(inputs) {
		t.Fatalf("got %d results, want %d", len(results), len(inputs))
	}
	for i, r := range results {
		if r.Input != inputs[i] {
			t.Errorf("position %d: got input %d, want %d — order is not preserved", i, r.Input, inputs[i])
		}
		if r.Err != nil {
			t.Errorf("input %d: unexpected error %v", r.Input, r.Err)
		}
		if r.Value != inputs[i]*2 {
			t.Errorf("input %d: got value %d, want %d", r.Input, r.Value, inputs[i]*2)
		}
	}
}

// The tail of the queue must never be dropped, however many workers there are
// relative to the input count.
func TestMapHandlesAnyWorkerCount(t *testing.T) {
	t.Parallel()

	for _, workers := range []int{1, 3, 5, 20} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			t.Parallel()

			inputs := seq(10)
			results := waitFor(t, runAsync(func() []workerpool.Result[int, int] {
				return workerpool.Map(context.Background(), workers, inputs, double)
			}), wait)

			if len(results) != len(inputs) {
				t.Fatalf("got %d results, want %d", len(results), len(inputs))
			}
			for i, r := range results {
				if r.Value != inputs[i]*2 {
					t.Errorf("position %d: got %d, want %d", i, r.Value, inputs[i]*2)
				}
			}
		})
	}
}

func TestMapWithNoInputsReturnsEmpty(t *testing.T) {
	t.Parallel()

	results := waitFor(t, runAsync(func() []workerpool.Result[int, int] {
		return workerpool.Map(context.Background(), 3, nil, double)
	}), wait)

	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

var errBoom = errors.New("boom")

// A failing process must not kill the worker or drop the input: the error
// travels back inside Result, wrapped and still unwrappable.
func TestMapReportsProcessErrors(t *testing.T) {
	t.Parallel()

	inputs := seq(10)
	results := workerpool.Map(context.Background(), 3, inputs, func(_ context.Context, i int) (int, error) {
		if i%2 == 0 {
			return 0, fmt.Errorf("input %d: %w", i, errBoom)
		}
		return i, nil
	})

	if len(results) != len(inputs) {
		t.Fatalf("got %d results, want %d — a failing input must still report back", len(results), len(inputs))
	}

	failed := 0
	for _, r := range results {
		if r.Input%2 == 0 {
			failed++
			if !errors.Is(r.Err, errBoom) {
				t.Errorf("input %d: got error %v, want it to wrap errBoom", r.Input, r.Err)
			}
		} else if r.Err != nil {
			t.Errorf("input %d: unexpected error %v", r.Input, r.Err)
		}
	}
	if want := len(inputs) / 2; failed != want {
		t.Errorf("got %d failures, want %d", failed, want)
	}
}

// Every worker must be inside process at the same moment. A Map that ran its
// inputs one after another would pass every test above, so this one blocks each
// call until all of them have arrived: with fewer concurrent workers than inputs
// the WaitGroup never reaches zero and the test times out. No wall-clock
// threshold involved.
func TestMapRunsWorkersConcurrently(t *testing.T) {
	t.Parallel()

	const workers = 5
	var arrived sync.WaitGroup
	arrived.Add(workers)
	release := make(chan struct{})

	done := runAsync(func() []workerpool.Result[int, int] {
		return workerpool.Map(context.Background(), workers, seq(workers), func(_ context.Context, i int) (int, error) {
			arrived.Done()
			<-release // held until every worker is in flight
			return i, nil
		})
	})

	allArrived := make(chan struct{})
	go func() {
		arrived.Wait()
		close(allArrived)
	}()
	waitFor(t, allArrived, wait)

	close(release)
	if results := waitFor(t, done, wait); len(results) != workers {
		t.Errorf("got %d results, want %d", len(results), workers)
	}
}

// An already cancelled context must not hang and must still describe every
// input.
func TestMapWithCancelledContextReportsEveryInput(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inputs := seq(10)
	results := waitFor(t, runAsync(func() []workerpool.Result[int, int] {
		return workerpool.Map(ctx, 3, inputs, double)
	}), wait)

	if len(results) != len(inputs) {
		t.Fatalf("got %d results, want %d", len(results), len(inputs))
	}
	for i, r := range results {
		if r.Input != inputs[i] {
			t.Errorf("position %d: got input %d, want %d", i, r.Input, inputs[i])
		}
		if !errors.Is(r.Err, context.Canceled) {
			t.Errorf("input %d: got error %v, want context.Canceled", r.Input, r.Err)
		}
	}
}

// Cancelling mid-run keeps the work already done and marks the rest, so the
// caller can tell exactly how far the run got.
func TestMapCancelledMidRunKeepsFinishedWork(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	const inputs, cancelAfter = 200, 5

	var completed atomic.Int64
	results := waitFor(t, runAsync(func() []workerpool.Result[int, int] {
		return workerpool.Map(ctx, 2, seq(inputs), func(_ context.Context, i int) (int, error) {
			if completed.Add(1) == cancelAfter {
				cancel()
			}
			return i, nil
		})
	}), wait)

	if len(results) != inputs {
		t.Fatalf("got %d results, want %d", len(results), inputs)
	}

	var ok, skipped int
	for _, r := range results {
		switch {
		case r.Err == nil:
			ok++
		case errors.Is(r.Err, context.Canceled):
			skipped++
		default:
			t.Errorf("input %d: unexpected error %v", r.Input, r.Err)
		}
	}
	if ok < cancelAfter {
		t.Errorf("kept %d finished results, want at least %d", ok, cancelAfter)
	}
	if skipped == 0 {
		t.Error("no input was marked as skipped, the cancellation had no effect")
	}
}

// --- Stream ------------------------------------------------------------------

func TestStreamYieldsEveryResult(t *testing.T) {
	t.Parallel()

	inputs := seq(10)
	collected := waitFor(t, runAsync(func() []int {
		var values []int
		for r := range workerpool.Stream(context.Background(), 4, slices.Values(inputs), double) {
			if r.Err != nil {
				t.Errorf("input %d: unexpected error %v", r.Input, r.Err)
			}
			values = append(values, r.Value)
		}
		return values
	}), wait)

	slices.Sort(collected) // Stream makes no ordering promise
	want := make([]int, 0, len(inputs))
	for _, in := range inputs {
		want = append(want, in*2)
	}
	if !slices.Equal(collected, want) {
		t.Errorf("got %v, want %v", collected, want)
	}
}

func TestStreamRunsWorkersConcurrently(t *testing.T) {
	t.Parallel()

	const workers = 5
	var arrived sync.WaitGroup
	arrived.Add(workers)
	release := make(chan struct{})

	counted := runAsync(func() int {
		n := 0
		for range workerpool.Stream(context.Background(), workers, slices.Values(seq(workers)),
			func(_ context.Context, i int) (int, error) {
				arrived.Done()
				<-release
				return i, nil
			}) {
			n++
		}
		return n
	})

	allArrived := make(chan struct{})
	go func() {
		arrived.Wait()
		close(allArrived)
	}()
	waitFor(t, allArrived, wait)

	close(release)
	if got := waitFor(t, counted, wait); got != workers {
		t.Errorf("got %d results, want %d", got, workers)
	}
}

// Breaking out of the range must stop the run. The input here is infinite, so a
// feeder that kept going would pull forever.
func TestStreamBreakStopsPullingInput(t *testing.T) {
	t.Parallel()

	var pulled atomic.Int64
	infinite := func(yield func(int) bool) {
		for i := 1; ; i++ {
			pulled.Add(1)
			if !yield(i) {
				return
			}
		}
	}

	got := waitFor(t, runAsync(func() int {
		n := 0
		for range workerpool.Stream(context.Background(), 3, infinite, double) {
			n++
			if n == 5 {
				break
			}
		}
		return n
	}), wait)

	if got != 5 {
		t.Fatalf("got %d results before the break, want 5", got)
	}

	time.Sleep(50 * time.Millisecond) // let any in-flight pull settle
	first := pulled.Load()
	time.Sleep(200 * time.Millisecond)
	if second := pulled.Load(); second != first {
		t.Errorf("the input kept being pulled after the break: %d then %d", first, second)
	}
}

// Goroutine counting is only meaningful in isolation, so this test is not
// parallel: go test runs the sequential tests before resuming the parallel ones.
func TestStreamLeavesNoGoroutinesBehind(t *testing.T) {
	before := runtime.NumGoroutine()

	for range workerpool.Stream(context.Background(), 4, slices.Values(seq(100)), double) {
		break
	}

	// The workers unwind asynchronously, so poll rather than sample once.
	for range 100 {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutines outlived the stream: %d before, %d after", before, runtime.NumGoroutine())
}

func TestStreamStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var pulled atomic.Int64
	infinite := func(yield func(int) bool) {
		for i := 1; ; i++ {
			pulled.Add(1)
			if !yield(i) {
				return
			}
		}
	}

	got := waitFor(t, runAsync(func() int {
		n := 0
		for range workerpool.Stream(ctx, 3, infinite, double) {
			n++
			if n == 5 {
				cancel()
			}
		}
		return n
	}), wait)

	if got < 5 {
		t.Errorf("got %d results, want at least 5 before the cancel", got)
	}
}

// --- shared ------------------------------------------------------------------

// The whole point of the type parameters: the same code serves any pair of
// types, inferred from the process function.
func TestWorksForAnyTypePair(t *testing.T) {
	t.Parallel()

	t.Run("string to int", func(t *testing.T) {
		t.Parallel()

		results := workerpool.Map(context.Background(), 3, []string{"a", "bb", "ccc"},
			func(_ context.Context, s string) (int, error) { return len(s), nil })

		for _, r := range results {
			if r.Value != len(r.Input) {
				t.Errorf("input %q: got %d, want %d", r.Input, r.Value, len(r.Input))
			}
		}
	})

	t.Run("struct to struct", func(t *testing.T) {
		t.Parallel()

		type request struct{ ID int }
		type response struct{ Label string }

		results := workerpool.Map(context.Background(), 2, []request{{ID: 1}, {ID: 2}},
			func(_ context.Context, req request) (response, error) {
				return response{Label: fmt.Sprintf("#%d", req.ID)}, nil
			})

		for _, r := range results {
			if want := fmt.Sprintf("#%d", r.Input.ID); r.Value.Label != want {
				t.Errorf("input %+v: got %q, want %q", r.Input, r.Value.Label, want)
			}
		}
	})
}

func TestPanicsOnInvalidArguments(t *testing.T) {
	t.Parallel()

	t.Run("Map with no workers", func(t *testing.T) {
		t.Parallel()
		defer wantPanic(t)

		workerpool.Map(context.Background(), 0, seq(3), double)
	})

	// The explicit type arguments are required here: a bare nil carries no type,
	// so inference has nothing to work from.
	t.Run("Map with nil process", func(t *testing.T) {
		t.Parallel()
		defer wantPanic(t)

		workerpool.Map[int, int](context.Background(), 1, seq(3), nil)
	})

	// Stream validates when it is called, not when the sequence is first ranged
	// over, so the panic does not surface somewhere far from the mistake.
	t.Run("Stream with no workers", func(t *testing.T) {
		t.Parallel()
		defer wantPanic(t)

		workerpool.Stream(context.Background(), 0, slices.Values(seq(3)), double)
	})

	t.Run("Stream with nil process", func(t *testing.T) {
		t.Parallel()
		defer wantPanic(t)

		workerpool.Stream[int, int](context.Background(), 1, slices.Values(seq(3)), nil)
	})
}

func wantPanic(t *testing.T) {
	t.Helper()

	if recover() == nil {
		t.Error("want a panic, got none")
	}
}
