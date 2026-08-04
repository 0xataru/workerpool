// Package benchmarks compares this package against the alternatives people
// actually reach for. It lives in its own module so the library itself keeps zero
// dependencies — `go test ./...` at the repo root does not descend here.
//
//	cd benchmarks && go test -run '^$' -bench . -benchmem
//
// Every benchmark performs the same task: apply a function to N inputs on at most
// W goroutines and collect one result per input, in input order. That is the task
// this library exists for, so it is the one worth timing. Libraries built around
// a different task (a long-lived pool shared across many batches, fail-fast error
// propagation) are still included, because callers use them for this anyway.
//
// The reported ns/item is overhead: the work function does almost nothing, so any
// difference is dispatch and collection. Numbers with real work per item converge
// as the work grows — see the crossover benchmark in the root module.
package benchmarks

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/0xataru/workerpool"
	pond "github.com/alitto/pond/v2"
	ants "github.com/panjf2000/ants/v2"
	conciter "github.com/sourcegraph/conc/iter"
	"golang.org/x/sync/errgroup"
)

const items = 10_000

var workerCounts = []int{8, 64}

// work is deliberately trivial and not inlinable away: the point is to measure
// the harness around it, not the arithmetic.
func work(i int) int { return i*31 + 7 }

func inputs(n int) []int {
	in := make([]int, n)
	for i := range in {
		in[i] = i + 1
	}
	return in
}

func perItem(b *testing.B) {
	b.Helper()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*items), "ns/item")
}

// check keeps an accidentally-empty benchmark from reporting a great score.
func check(b *testing.B, got []int) {
	b.Helper()
	if len(got) != items {
		b.Fatalf("got %d results, want %d", len(got), items)
	}
}

func each(b *testing.B, run func(b *testing.B, workers int, in []int) []int) {
	in := inputs(items)
	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				check(b, run(b, workers, in))
			}
			perItem(b)
		})
	}
}

// --- this package ------------------------------------------------------------

func BenchmarkWorkerpoolMap(b *testing.B) {
	ctx := context.Background()
	each(b, func(_ *testing.B, workers int, in []int) []int {
		results := workerpool.Map(ctx, workers, in, func(_ context.Context, i int) (int, error) {
			return work(i), nil
		})
		out := make([]int, len(results))
		for i, r := range results {
			out[i] = r.Value
		}
		return out
	})
}

// --- alternatives ------------------------------------------------------------

// errgroup is not a pool: SetLimit is a semaphore, so every item still gets its
// own goroutine. It is here because it is what most projects already have.
func BenchmarkErrgroup(b *testing.B) {
	each(b, func(_ *testing.B, workers int, in []int) []int {
		out := make([]int, len(in))
		var g errgroup.Group
		g.SetLimit(workers)
		for i, v := range in {
			g.Go(func() error {
				out[i] = work(v)
				return nil
			})
		}
		_ = g.Wait()
		return out
	})
}

// conc's iter.Map is the closest analogue in spirit: same task, same shape.
// The package has been unmaintained since v0.3.0 (2023).
func BenchmarkConcIterMap(b *testing.B) {
	each(b, func(_ *testing.B, workers int, in []int) []int {
		mapper := conciter.Mapper[int, int]{MaxGoroutines: workers}
		return mapper.Map(in, func(v *int) int { return work(*v) })
	})
}

// pond v2, via a result task group: the API closest to collecting per-item
// results in submission order.
func BenchmarkPond(b *testing.B) {
	each(b, func(b *testing.B, workers int, in []int) []int {
		pool := pond.NewResultPool[int](workers)
		defer pool.StopAndWait()

		group := pool.NewGroup()
		for _, v := range in {
			group.Submit(func() int { return work(v) })
		}
		out, err := group.Wait()
		if err != nil {
			b.Fatalf("pond: %v", err)
		}
		return out
	})
}

// BenchmarkPondReused keeps the pool across iterations and only creates a task
// group per batch, which is how pond is designed to be used.
func BenchmarkPondReused(b *testing.B) {
	in := inputs(items)

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			pool := pond.NewResultPool[int](workers)
			defer pool.StopAndWait()

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				group := pool.NewGroup()
				for _, v := range in {
					group.Submit(func() int { return work(v) })
				}
				out, err := group.Wait()
				if err != nil {
					b.Fatalf("pond: %v", err)
				}
				check(b, out)
			}
			perItem(b)
		})
	}
}

// ants reuses goroutines across submissions, which is its whole point. Result
// collection is the caller's job, so this is the fair amount of glue.
func BenchmarkAnts(b *testing.B) {
	each(b, func(b *testing.B, workers int, in []int) []int {
		out := make([]int, len(in))
		var wg sync.WaitGroup

		type job struct{ idx, val int }
		pool, err := ants.NewPoolWithFuncGeneric(workers, func(j job) {
			defer wg.Done()
			out[j.idx] = work(j.val)
		})
		if err != nil {
			b.Fatalf("ants: %v", err)
		}
		defer pool.Release()

		for i, v := range in {
			wg.Add(1)
			if err := pool.Invoke(job{idx: i, val: v}); err != nil {
				b.Fatalf("ants invoke: %v", err)
			}
		}
		wg.Wait()
		return out
	})
}

// BenchmarkAntsReused keeps one pool across all iterations, which is how ants is
// meant to be used: the cost of creating the pool is amortised away.
func BenchmarkAntsReused(b *testing.B) {
	in := inputs(items)

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			out := make([]int, len(in))
			var wg sync.WaitGroup

			type job struct{ idx, val int }
			pool, err := ants.NewPoolWithFuncGeneric(workers, func(j job) {
				defer wg.Done()
				out[j.idx] = work(j.val)
			})
			if err != nil {
				b.Fatalf("ants: %v", err)
			}
			defer pool.Release()

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for i, v := range in {
					wg.Add(1)
					if err := pool.Invoke(job{idx: i, val: v}); err != nil {
						b.Fatalf("ants invoke: %v", err)
					}
				}
				wg.Wait()
				check(b, out)
			}
			perItem(b)
		})
	}
}

// --- floor -------------------------------------------------------------------

// BenchmarkSequential is the floor every one of the above has to beat before
// concurrency is worth anything at all.
func BenchmarkSequential(b *testing.B) {
	each(b, func(_ *testing.B, _ int, in []int) []int {
		out := make([]int, len(in))
		for i, v := range in {
			out[i] = work(v)
		}
		return out
	})
}
