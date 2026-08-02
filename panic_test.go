package workerpool_test

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/0xataru/workerpool"
)

// The package documents that a panic inside process is not recovered: it takes
// the program down exactly as it would without a pool. That is a promise about
// behaviour, so it deserves a test — but a panicking goroutine kills the test
// binary too, so the run has to happen in a child process.
//
// The child is this same binary, re-invoked with an environment variable that
// tells it to panic instead of running the parent half of the test.
const panicModeEnv = "WORKERPOOL_PANIC_MODE"

func TestProcessPanicIsNotRecovered(t *testing.T) {
	if mode := os.Getenv(panicModeEnv); mode != "" {
		panicChild(mode)
		return
	}

	for _, mode := range []string{"map", "stream"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			cmd := exec.Command(os.Args[0], "-test.run=^TestProcessPanicIsNotRecovered$")
			cmd.Env = append(os.Environ(), panicModeEnv+"="+mode)

			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("child exited cleanly, want a crash; output:\n%s", out)
			}
			if !strings.Contains(string(out), "boom") {
				t.Errorf("child crashed without the panic value in its output:\n%s", out)
			}
			// A recovered panic turned into an error would not print this.
			if !strings.Contains(string(out), "panic:") {
				t.Errorf("child did not report a panic:\n%s", out)
			}
		})
	}
}

func panicChild(mode string) {
	explode := func(context.Context, int) (int, error) { panic("boom") }

	switch mode {
	case "map":
		workerpool.Map(context.Background(), 2, seq(4), explode)
	case "stream":
		for range workerpool.Stream(context.Background(), 2, slices.Values(seq(4)), explode) {
		}
	}
}
