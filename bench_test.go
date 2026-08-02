package workerpool_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/0xataru/workerpool"
)

// These benchmarks measure the package's overhead, not its speed at doing your
// work: the process function below does nothing, so every nanosecond reported is
// scheduling and channel traffic. The ns/item metric is the number that matters
// — it is what the pool adds to each item on top of the real work.
//
// Run them with:
//
//	go test -run '^$' -bench . -benchmem ./...
//
// Never with -race: the detector adds an order of magnitude to channel
// operations and makes the numbers meaningless.

func noop(_ context.Context, i int) (int, error) { return i, nil }

var benchSizes = []int{100, 10_000}

var benchWorkers = []int{1, 8, 64}

func perItem(b *testing.B, items int) {
	b.Helper()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*items), "ns/item")
}

func BenchmarkMap(b *testing.B) {
	ctx := context.Background()

	for _, items := range benchSizes {
		for _, workers := range benchWorkers {
			b.Run(fmt.Sprintf("items=%d/workers=%d", items, workers), func(b *testing.B) {
				inputs := seq(items)
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					if got := len(workerpool.Map(ctx, workers, inputs, noop)); got != items {
						b.Fatalf("got %d results, want %d", got, items)
					}
				}
				perItem(b, items)
			})
		}
	}
}

func BenchmarkStream(b *testing.B) {
	ctx := context.Background()

	for _, items := range benchSizes {
		for _, workers := range benchWorkers {
			b.Run(fmt.Sprintf("items=%d/workers=%d", items, workers), func(b *testing.B) {
				inputs := seq(items)
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					got := 0
					for range workerpool.Stream(ctx, workers, slices.Values(inputs), noop) {
						got++
					}
					if got != items {
						b.Fatalf("got %d results, want %d", got, items)
					}
				}
				perItem(b, items)
			})
		}
	}
}

// BenchmarkSemaphore is the hand-rolled alternative: one goroutine per item,
// bounded by a token channel, results written into a preallocated slice. This is
// what errgroup.SetLimit does under the hood and what most people write when they
// do not reach for a package at all.
func BenchmarkSemaphore(b *testing.B) {
	ctx := context.Background()

	for _, items := range benchSizes {
		for _, workers := range benchWorkers {
			b.Run(fmt.Sprintf("items=%d/workers=%d", items, workers), func(b *testing.B) {
				inputs := seq(items)
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					out := make([]int, len(inputs))
					sem := make(chan struct{}, workers)
					var wg sync.WaitGroup

					for i, in := range inputs {
						wg.Add(1)
						sem <- struct{}{}
						go func() {
							defer wg.Done()
							defer func() { <-sem }()
							out[i], _ = noop(ctx, in)
						}()
					}
					wg.Wait()

					if len(out) != items {
						b.Fatalf("got %d results, want %d", len(out), items)
					}
				}
				perItem(b, items)
			})
		}
	}
}

// BenchmarkSequential is the floor: no concurrency at all. Any pool has to beat
// this to be worth using, and with a no-op process none of them can.
func BenchmarkSequential(b *testing.B) {
	ctx := context.Background()

	for _, items := range benchSizes {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			inputs := seq(items)
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				out := make([]int, len(inputs))
				for i, in := range inputs {
					out[i], _ = noop(ctx, in)
				}
				if len(out) != items {
					b.Fatalf("got %d results, want %d", len(out), items)
				}
			}
			perItem(b, items)
		})
	}
}

// spin burns roughly a fixed amount of CPU, standing in for real per-item work.
// It is what turns the overhead numbers above into a decision: the pool pays for
// itself once the work per item is well above its per-item cost.
func spin(iterations int) func(context.Context, int) (int, error) {
	return func(_ context.Context, i int) (int, error) {
		x := i
		for range iterations {
			x = x*31 + 7
		}
		return x, nil
	}
}

// BenchmarkCrossover answers "how much work per item before the pool wins?" by
// running the same workload sequentially and through Map at several work sizes.
func BenchmarkCrossover(b *testing.B) {
	ctx := context.Background()
	const items = 2_000

	for _, work := range []int{10, 100, 1_000, 10_000} {
		process := spin(work)
		inputs := seq(items)

		b.Run(fmt.Sprintf("spin=%d/sequential", work), func(b *testing.B) {
			b.ResetTimer()
			for range b.N {
				out := make([]int, len(inputs))
				for i, in := range inputs {
					out[i], _ = process(ctx, in)
				}
			}
			perItem(b, items)
		})

		b.Run(fmt.Sprintf("spin=%d/map", work), func(b *testing.B) {
			b.ResetTimer()
			for range b.N {
				workerpool.Map(ctx, 8, inputs, process)
			}
			perItem(b, items)
		})
	}
}
