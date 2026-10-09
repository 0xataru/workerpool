package workerpool_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xataru/workerpool"
)

// Map keeps the order of the inputs, so the output is predictable even though
// the work happens concurrently.
func ExampleMap() {
	upper := func(_ context.Context, s string) (string, error) {
		return strings.ToUpper(s), nil
	}

	for _, r := range workerpool.Map(context.Background(), 3, []string{"one", "two", "three"}, upper) {
		fmt.Println(r.Input, "->", r.Value)
	}

	// Output:
	// one -> ONE
	// two -> TWO
	// three -> THREE
}

// A failing input is reported through Result.Err rather than aborting the run,
// so one bad item never costs you the rest of the batch.
func ExampleMap_errors() {
	var errTooShort = errors.New("too short")

	check := func(_ context.Context, s string) (int, error) {
		if len(s) < 3 {
			return 0, fmt.Errorf("%q: %w", s, errTooShort)
		}
		return len(s), nil
	}

	for _, r := range workerpool.Map(context.Background(), 2, []string{"apple", "no", "cherry"}, check) {
		if r.Err != nil {
			fmt.Println("failed:", r.Err)
			continue
		}
		fmt.Printf("%s has %d letters\n", r.Input, r.Value)
	}

	// Output:
	// apple has 5 letters
	// failed: "no": too short
	// cherry has 6 letters
}

// Stream hands results over as they finish, in completion order. Sorting here
// only makes the example output deterministic.
func ExampleStream() {
	square := func(_ context.Context, i int) (int, error) { return i * i, nil }

	var squares []int
	for r := range workerpool.Stream(context.Background(), 4, slices.Values([]int{1, 2, 3, 4, 5}), square) {
		squares = append(squares, r.Value)
	}
	slices.Sort(squares)

	fmt.Println(squares)

	// Output:
	// [1 4 9 16 25]
}

// Breaking out of the range cancels the run: the workers stop and the input
// sequence stops being pulled, even though it is infinite here.
func ExampleStream_earlyExit() {
	naturals := func(yield func(int) bool) {
		for i := 1; ; i++ {
			if !yield(i) {
				return
			}
		}
	}
	identity := func(_ context.Context, i int) (int, error) { return i, nil }

	found := 0
	for r := range workerpool.Stream(context.Background(), 4, naturals, identity) {
		if r.Value%7 == 0 {
			found++
			if found == 1 {
				fmt.Println("found a multiple of 7")
				break
			}
		}
	}

	// Output:
	// found a multiple of 7
}

// Cancelling a Map keeps the work that finished and marks every input the run
// never reached with ctx.Err(), so the caller still gets one Result per input.
func ExampleMap_cancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// One worker makes the cut-off point deterministic for the example. With
	// more, any input in flight when cancel lands still finishes and is kept.
	process := func(_ context.Context, s string) (string, error) {
		if s == "b" {
			cancel() // stands in for a timeout, a signal or a caller giving up
		}
		return strings.ToUpper(s), nil
	}

	for _, r := range workerpool.Map(ctx, 1, []string{"a", "b", "c", "d"}, process) {
		if r.Err != nil {
			fmt.Println(r.Input, "skipped:", r.Err)
			continue
		}
		fmt.Println(r.Input, "->", r.Value)
	}

	// Output:
	// a -> A
	// b -> B
	// c skipped: context canceled
	// d skipped: context canceled
}

// Retries are deliberately not built in: wrapping the process function is a few
// lines, and the wrapper decides what is worth retrying and how long to wait.
func ExampleMap_retry() {
	var calls sync.Map // input -> *atomic.Int32
	flaky := func(_ context.Context, id int) (string, error) {
		n, _ := calls.LoadOrStore(id, new(atomic.Int32))
		if n.(*atomic.Int32).Add(1) < 3 {
			return "", errors.New("temporary failure")
		}
		return fmt.Sprintf("item %d", id), nil
	}

	withRetry := func(process workerpool.Process[int, string], attempts int) workerpool.Process[int, string] {
		return func(ctx context.Context, id int) (string, error) {
			var err error
			for attempt := range attempts {
				var out string
				if out, err = process(ctx, id); err == nil {
					return out, nil
				}
				select {
				case <-time.After(time.Duration(attempt+1) * time.Millisecond): // backoff
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return "", err
		}
	}

	for _, r := range workerpool.Map(context.Background(), 4, []int{1, 2, 3}, withRetry(flaky, 5)) {
		fmt.Println(r.Value, r.Err)
	}

	// Output:
	// item 1 <nil>
	// item 2 <nil>
	// item 3 <nil>
}

// By default one failure never aborts the batch. To stop at the first error
// instead, cancel a derived context from inside the process function.
func ExampleMap_failFast() {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	errBadInput := errors.New("bad input")
	validate := func(_ context.Context, n int) (int, error) {
		if n < 0 {
			cancel(errBadInput)
			return 0, errBadInput
		}
		return n, nil
	}

	results := workerpool.Map(ctx, 1, []int{1, 2, -3, 4, 5}, validate)

	fmt.Println("stopped because:", context.Cause(ctx))
	for _, r := range results {
		fmt.Println(r.Input, r.Err)
	}

	// Output:
	// stopped because: bad input
	// 1 <nil>
	// 2 <nil>
	// -3 bad input
	// 4 context canceled
	// 5 context canceled
}

// Rate limiting is a shared ticker that every worker waits on before starting a
// job. The worker count bounds concurrency; the ticker bounds throughput.
func ExampleStream_rateLimit() {
	tick := time.NewTicker(5 * time.Millisecond) // at most 200 calls per second
	defer tick.Stop()

	call := func(ctx context.Context, id int) (int, error) {
		select {
		case <-tick.C:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		return id * 10, nil // the rate-limited call goes here
	}

	var got []int
	for r := range workerpool.Stream(context.Background(), 8, slices.Values([]int{1, 2, 3, 4}), call) {
		got = append(got, r.Value)
	}
	slices.Sort(got)

	fmt.Println(got)

	// Output:
	// [10 20 30 40]
}

// Results arrive in the goroutine that ranges over the Stream, so progress
// reporting is a plain counter: no atomics, no locks, no callbacks.
func ExampleStream_progress() {
	inputs := []string{"a.txt", "b.txt", "c.txt"}
	process := func(_ context.Context, name string) (int, error) { return len(name), nil }

	done := 0
	for range workerpool.Stream(context.Background(), 2, slices.Values(inputs), process) {
		done++
		fmt.Printf("%d/%d done\n", done, len(inputs))
	}

	// Output:
	// 1/3 done
	// 2/3 done
	// 3/3 done
}
