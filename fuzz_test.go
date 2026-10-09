package workerpool_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/0xataru/workerpool"
)

// The fuzz targets check the same invariants as the stress tests, but let the
// fuzzer pick the shape of each run instead of a seeded PRNG. Coverage guidance
// steers it toward the branches random sampling rarely reaches, and any failing
// input is saved under testdata/fuzz and replayed by every plain go test.
//
// Without -fuzz they run only the seed corpus below, so they cost nothing in CI.
// Explore with:
//
//	go test -run '^$' -fuzz FuzzMap -fuzztime 1m
//	go test -run '^$' -fuzz FuzzStream -fuzztime 1m

func FuzzMap(f *testing.F) {
	f.Add(uint16(0), uint8(1), uint16(0))
	f.Add(uint16(1), uint8(1), uint16(1))
	f.Add(uint16(100), uint8(8), uint16(0))
	f.Add(uint16(100), uint8(8), uint16(50))
	f.Add(uint16(3), uint8(64), uint16(2))

	f.Fuzz(func(t *testing.T, size uint16, workerSeed uint8, cancelSeed uint16) {
		inputs := seq(int(size % 2000))
		workers := 1 + int(workerSeed%64)
		// 0 means "let the run finish"; otherwise cancel once that many inputs
		// have been processed.
		cancelAt := int(cancelSeed) % (len(inputs) + 1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var completed atomic.Int64
		results := waitFor(t, runAsync(func() []workerpool.Result[int, int] {
			return workerpool.Map(ctx, workers, inputs, func(_ context.Context, i int) (int, error) {
				if cancelAt > 0 && int(completed.Add(1)) == cancelAt {
					cancel()
				}
				return i * 2, nil
			})
		}), wait)

		if len(results) != len(inputs) {
			t.Fatalf("inputs=%d workers=%d cancelAt=%d: got %d results",
				len(inputs), workers, cancelAt, len(results))
		}

		for i, r := range results {
			if r.Input != inputs[i] {
				t.Fatalf("position %d holds input %d, want %d", i, r.Input, inputs[i])
			}
			switch {
			case r.Err == nil:
				if r.Value != inputs[i]*2 {
					t.Fatalf("input %d: got value %d, want %d", r.Input, r.Value, inputs[i]*2)
				}
			case errors.Is(r.Err, context.Canceled):
				if cancelAt == 0 {
					t.Fatalf("input %d cancelled in a run that never was", r.Input)
				}
			default:
				t.Fatalf("input %d: unexpected error %v", r.Input, r.Err)
			}
		}
	})
}

func FuzzStream(f *testing.F) {
	f.Add(uint16(0), uint8(1), uint16(0), false)
	f.Add(uint16(1), uint8(1), uint16(1), true)
	f.Add(uint16(100), uint8(8), uint16(0), false)
	f.Add(uint16(100), uint8(8), uint16(50), false)
	f.Add(uint16(100), uint8(8), uint16(50), true)

	f.Fuzz(func(t *testing.T, size uint16, workerSeed uint8, stopSeed uint16, byCancel bool) {
		inputs := seq(int(size % 2000))
		workers := 1 + int(workerSeed%64)
		stopAfter := int(stopSeed) % (len(inputs) + 1) // 0 means "let it finish"

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		got := waitFor(t, runAsync(func() []int {
			var got []int
			for r := range workerpool.Stream(ctx, workers, slices.Values(inputs), double) {
				if r.Err != nil {
					t.Errorf("input %d: unexpected error %v", r.Input, r.Err)
				}
				if r.Value != r.Input*2 {
					t.Errorf("input %d: got value %d, want %d", r.Input, r.Value, r.Input*2)
				}
				got = append(got, r.Input)

				if stopAfter > 0 && len(got) == stopAfter {
					if !byCancel {
						break
					}
					// Keep ranging: a cancelled Stream has to end on its own.
					cancel()
				}
			}
			return got
		}), wait)

		slices.Sort(got)
		if len(slices.Compact(slices.Clone(got))) != len(got) {
			t.Fatalf("inputs=%d workers=%d stopAfter=%d: a result was yielded twice",
				len(inputs), workers, stopAfter)
		}
		for _, in := range got {
			if in < 1 || in > len(inputs) {
				t.Fatalf("yielded input %d that was never passed in", in)
			}
		}
		if stopAfter == 0 && len(got) != len(inputs) {
			t.Fatalf("inputs=%d workers=%d: run was never stopped but yielded %d results",
				len(inputs), workers, len(got))
		}
	})
}
