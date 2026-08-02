// Command checksum hashes every file under a directory, several files at a time.
//
//	go run ./examples/checksum [dir]
//
// It is the counterpart to the tracking example. That one has a slice of inputs
// known up front, so Map fits. Here the input is a directory walk: the file
// count is unknown, the list could be huge, and there is no reason to wait for
// the walk to finish before hashing anything. That is what Stream is for.
//
// The interesting part is files(), which turns filepath.WalkDir into an
// iter.Seq[string]. Writing a sequence is the one thing callers of this package
// usually have to do themselves, and the rule is short: stop walking as soon as
// yield returns false.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0xataru/workerpool"
)

// files yields every regular file under root.
//
// Unreadable directories are skipped rather than aborting the walk: one bad
// directory should not cost the caller the whole tree. Problems with individual
// files surface later, as a Result.Err from checksum.
func files(root string) iter.Seq[string] {
	return func(yield func(string) bool) {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a walk error here means "skip", not "fail"
			}

			if !yield(path) {
				// The consumer stopped — Stream was cancelled or the caller
				// broke out of the range. Ending the walk here is what keeps it
				// from reading the rest of the tree for nothing.
				return filepath.SkipAll
			}
			return nil
		})
	}
}

// digest is the Out type of the pool: what we learned about one file.
type digest struct {
	Sum  string
	Size int64
}

// checksum is the process function. Note that it takes the context all the way
// down into the read loop — a process function that ignores ctx makes the whole
// run uncancellable, however well the pool behaves.
func checksum(ctx context.Context, path string) (digest, error) {
	f, err := os.Open(path)
	if err != nil {
		return digest{}, err
	}
	defer f.Close()

	h := sha256.New()
	size, err := io.Copy(h, reader{ctx: ctx, r: f})
	if err != nil {
		return digest{}, err
	}

	return digest{Sum: hex.EncodeToString(h.Sum(nil)), Size: size}, nil
}

// reader makes a long read give up when ctx is cancelled. io.Copy on a multi-
// gigabyte file would otherwise run to completion no matter what.
type reader struct {
	ctx context.Context
	r   io.Reader
}

func (r reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func main() {
	// Cancel on Ctrl+C rather than being killed by it: the walk stops, the
	// workers finish what they are holding, and the totals below still print.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	// Hashing is CPU-bound, so one worker per core is the sensible default. For
	// network calls the right number is usually far higher than the core count —
	// pick it from what the remote side tolerates, not from the hardware.
	workers := runtime.NumCPU()

	var hashed, failed int
	var bytes int64
	start := time.Now()

	for r := range workerpool.Stream(ctx, workers, files(root), checksum) {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "%s: %v\n", r.Input, r.Err)
			continue
		}

		hashed++
		bytes += r.Value.Size
		fmt.Printf("%s  %s\n", r.Value.Sum[:16], r.Input)
	}

	fmt.Printf("\n%d files, %d bytes, %d unreadable, %d workers, %v\n",
		hashed, bytes, failed, workers, time.Since(start).Round(time.Millisecond))

	if err := ctx.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "interrupted:", err)
		os.Exit(1)
	}
}
