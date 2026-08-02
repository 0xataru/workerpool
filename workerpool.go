// Package workerpool runs a function over many inputs on a fixed number of
// goroutines.
//
// Two entry points cover the two shapes work usually arrives in:
//
//	results := workerpool.Map(ctx, 8, urls, fetch)             // slice in, slice out
//
//	for r := range workerpool.Stream(ctx, 8, lines, parse) {   // stream in, stream out
//		...
//	}
//
// Neither asks the caller to start, stop or wire anything. Each owns its
// goroutines and shuts them down before returning, so there is no lifecycle to
// get wrong and no way to deadlock the pool from the outside.
//
// # Guarantees
//
// Map returns exactly one Result per input, in the order of inputs. Stream
// yields results as they complete, in no particular order.
//
// Both stop early when ctx is cancelled. Map still returns one Result per input:
// the ones it never got to carry ctx.Err() in Result.Err, and work that finished
// before the cancellation is kept. Stream simply stops yielding.
//
// Neither buffers. A slow consumer of Stream slows the workers down rather than
// letting results pile up in memory.
//
// A panic inside process is not recovered; it crashes the program exactly as it
// would without a pool. Recover inside your own process function if you want
// different behaviour.
package workerpool

import (
	"context"
	"iter"
	"sync"
)

// Process is the work to run on every input. It receives the run's context, so a
// long call can give up when the run is cancelled.
type Process[In, Out any] func(ctx context.Context, in In) (Out, error)

// Result is the outcome of processing a single input. Err is whatever Process
// returned, or ctx.Err() for an input that was never processed because the run
// was cancelled. Value is meaningful only when Err is nil.
type Result[In, Out any] struct {
	Input In
	Value Out
	Err   error
}

// Map runs process over every input on the given number of worker goroutines and
// returns the results in the order of inputs. It blocks until the work is done
// or ctx is cancelled.
//
// The workers read from inputs for as long as the call is running, so the slice
// must not be modified until Map returns. Doing so is a data race in the calling
// code, and one that the race detector will blame on this package.
//
// It panics if workers is below one or process is nil. Both are caller bugs with
// worse consequences if allowed through: a nil process would panic later inside
// a worker goroutine, and a pool with no workers would never finish.
func Map[In, Out any](ctx context.Context, workers int, inputs []In, process Process[In, Out]) []Result[In, Out] {
	check(workers, process)

	results := make([]Result[In, Out], len(inputs))
	if len(inputs) == 0 {
		return results
	}

	// Each worker writes to a distinct index, so the slices need no lock, and
	// wg.Wait below is the happens-before edge that makes them safe to read.
	filled := make([]bool, len(inputs))
	queue := make(chan int)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case i, ok := <-queue:
					if !ok {
						return // the queue is closed and drained
					}

					value, err := process(ctx, inputs[i])
					results[i] = Result[In, Out]{Input: inputs[i], Value: value, Err: err}
					filled[i] = true
				}
			}
		}()
	}

feed:
	for i := range inputs {
		select {
		case queue <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()

	// Report the inputs the run never reached, so the caller still gets one
	// Result per input and can tell which ones were skipped.
	if err := ctx.Err(); err != nil {
		for i := range results {
			if !filled[i] {
				results[i] = Result[In, Out]{Input: inputs[i], Err: err}
			}
		}
	}

	return results
}

// Stream runs process over inputs on the given number of worker goroutines and
// yields each result as it completes. Use it when the input does not fit in
// memory or when results should be handled as they arrive.
//
// Breaking out of the returned sequence cancels the run: the workers stop, the
// input sequence stops being pulled, and the range statement returns once
// everything has wound down.
//
// It panics if workers is below one or process is nil.
func Stream[In, Out any](ctx context.Context, workers int, inputs iter.Seq[In], process Process[In, Out]) iter.Seq[Result[In, Out]] {
	check(workers, process)

	return func(yield func(Result[In, Out]) bool) {
		// A derived context is what lets an early break unwind the run: the
		// deferred cancel releases every goroutine started below.
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		queue := make(chan In)
		results := make(chan Result[In, Out])

		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					// The receive needs a cancellation arm, otherwise an idle
					// worker waits for a job that is never coming.
					select {
					case <-ctx.Done():
						return
					case in, ok := <-queue:
						if !ok {
							return
						}

						value, err := process(ctx, in)
						if ctx.Err() != nil {
							return // interrupted, there is nothing to report
						}

						// The send needs one too: once the caller stops reading,
						// a worker blocked here would never reach wg.Done.
						select {
						case results <- Result[In, Out]{Input: in, Value: value, Err: err}:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}

		// The feeder owns the queue, so closing it can never race with a send.
		go func() {
			defer close(queue)
			for in := range inputs {
				select {
				case queue <- in:
				case <-ctx.Done():
					return
				}
			}
		}()

		// Closing results is only safe once every writer has exited, which is
		// exactly what wg.Wait means.
		go func() {
			wg.Wait()
			close(results)
		}()

		for result := range results {
			if !yield(result) {
				return // the deferred cancel unwinds the goroutines above
			}
		}
	}
}

func check[In, Out any](workers int, process Process[In, Out]) {
	if workers < 1 {
		panic("workerpool: workers must be at least 1")
	}
	if process == nil {
		panic("workerpool: process must not be nil")
	}
}
