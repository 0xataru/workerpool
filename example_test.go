package workerpool_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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
