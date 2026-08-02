// Command tracking is a runnable example of the workerpool package: shipment
// tracking numbers are looked up through a mock carrier API on several workers.
//
// It shows the same batch handled three ways, which is the main question a
// caller has — Map or Stream:
//
//	report     Map, for a stable report printed once everything is in
//	live       Stream, for output as each result arrives
//	findFirst  Stream with a break, to stop as soon as an answer is found
//
// Run it with:
//
//	go run ./examples/tracking
//
// Press Ctrl+C at any point: the run is cancelled rather than killed, and the
// output shows how each entry point reports the interruption.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/0xataru/workerpool"
)

const (
	numTrackingNumbers = 500
	workers            = 50
)

// TrackingNumber is the domain type the pool knows nothing about.
type TrackingNumber struct {
	Number string
}

var errNoRecord = errors.New("carrier has no record")

// fetchStatus stands in for a real carrier API call. Its signature is what the
// pool infers its type parameters from: In is TrackingNumber, Out is string.
//
// It takes the context so a slow call can be abandoned on cancellation — every
// process function that does I/O should do the same.
func fetchStatus(ctx context.Context, tn TrackingNumber) (string, error) {
	select {
	case <-time.After(200 * time.Millisecond): // pretend this is a network call
		// Fail a few numbers on purpose, to show a bad input being reported
		// instead of aborting the batch.
		//
		// The error deliberately does not mention the tracking number: Result
		// pairs it with Input, so naming it here would only print it twice.
		if strings.HasSuffix(tn.Number, "7") {
			return "", errNoRecord
		}
		return "Delivered", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func trackingNumbers(n int) []TrackingNumber {
	numbers := make([]TrackingNumber, n)
	for i := range numbers {
		numbers[i] = TrackingNumber{Number: fmt.Sprintf("TN%03d", i+1)}
	}
	return numbers
}

// report uses Map: it waits for the whole batch and gets the results back in the
// order the numbers were submitted, whichever worker finished first. Use Map
// when you want a stable report, or when the results feed something that expects
// input order.
func report(ctx context.Context, numbers []TrackingNumber) {
	start := time.Now()
	results := workerpool.Map(ctx, workers, numbers, fetchStatus)

	var delivered, failed, skipped int
	for _, r := range results {
		switch {
		case r.Err == nil:
			delivered++
		case errors.Is(r.Err, context.Canceled):
			// Map returns one Result per input even when the run is cut short,
			// so the numbers it never reached are visible rather than missing.
			skipped++
		default:
			failed++
			fmt.Fprintf(os.Stderr, "  %s: %v\n", r.Input.Number, r.Err)
		}
	}

	fmt.Printf("Map: %d delivered, %d failed, %d never attempted (%v)\n",
		delivered, failed, skipped, time.Since(start).Round(time.Millisecond))
}

// live uses Stream: results are handed over as they complete, in no particular
// order. Use Stream when the input does not fit in memory, or when a result is
// worth acting on before the batch is done.
func live(ctx context.Context, numbers []TrackingNumber) {
	fmt.Println("Stream, in completion order:")

	for r := range workerpool.Stream(ctx, workers, slices.Values(numbers), fetchStatus) {
		if r.Err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", r.Input.Number, r.Err)
			continue
		}
		fmt.Printf("  %s: %s\n", r.Input.Number, r.Value)
	}
}

// findFirst breaks out of a Stream as soon as it has an answer. The break
// cancels the run: the remaining workers stop and the input stops being pulled,
// so nothing keeps running in the background after this function returns.
func findFirst(ctx context.Context, numbers []TrackingNumber) {
	for r := range workerpool.Stream(ctx, workers, slices.Values(numbers), fetchStatus) {
		if r.Err == nil {
			fmt.Printf("Stream with break: %s came back first, stopping\n", r.Input.Number)
			return
		}
	}

	fmt.Println("Stream with break: nothing was delivered")
}

func main() {
	// Cancel on Ctrl+C rather than being killed by it, so the workers wind down
	// and every entry point below reports what it managed to do.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	numbers := trackingNumbers(numTrackingNumbers)

	report(ctx, numbers)
	live(ctx, numbers[:10])
	findFirst(ctx, numbers)

	if err := ctx.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "interrupted:", err)
		os.Exit(1)
	}
}
