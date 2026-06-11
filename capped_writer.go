package raymond

import (
	"fmt"
	"io"
)

// errBudgetOverflow is the unexported sentinel returned by cappedWriter
// when a Write would push cumulative bytes-written strictly above the
// configured limit. It never escapes to callers: the streaming driver
// converts it into a *RenderBudgetExceededError. It wraps ErrOutputLimit
// so errors.Is(err, ErrOutputLimit) holds throughout the chain.
var errBudgetOverflow = fmt.Errorf("render output budget exceeded: %w", ErrOutputLimit)

// cappedWriter is an io.Writer wrapper that delegates to dst while
// counting bytes and refusing to forward any byte that would push
// written strictly above limit.
//
// Behaviour summary (per Write call, with remaining := limit - written):
//
//  1. len(p) <= remaining: forward whole p, update written, return
//     whatever the underlying writer returned.
//  2. len(p) > remaining and remaining > 0: forward exactly
//     p[:remaining], update written, return (n, errBudgetOverflow).
//  3. len(p) > 0 and remaining == 0: write nothing, return
//     (0, errBudgetOverflow).
//  4. len(p) == 0: write nothing, return (0, nil).
//
// Errors from the underlying dst (including short writes) are surfaced
// unchanged; they are NOT converted into errBudgetOverflow.
type cappedWriter struct {
	dst     io.Writer
	limit   int64
	written int64
}

// newCappedWriter constructs a capped writer wrapping dst with the
// given byte limit. limit may be zero (any non-empty write fails) but
// callers are expected to validate negatives before invoking.
func newCappedWriter(dst io.Writer, limit int64) *cappedWriter {
	return &cappedWriter{dst: dst, limit: limit}
}

// Write implements io.Writer per the state-transition table above.
func (cw *cappedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	remaining := cw.limit - cw.written
	if remaining < 0 {
		remaining = 0
	}

	if int64(len(p)) <= remaining {
		n, err := cw.dst.Write(p)
		if n < 0 {
			n = 0
		}
		cw.written += int64(n)
		return n, err
	}

	if remaining == 0 {
		return 0, errBudgetOverflow
	}

	// len(p) > remaining > 0: forward only the bytes that fit and
	// surface the budget sentinel. If the underlying writer returns
	// its own error or a short write, surface that instead so the
	// driver can wrap it as a RenderDestinationError.
	n, err := cw.dst.Write(p[:remaining])
	if n < 0 {
		n = 0
	}
	cw.written += int64(n)
	if err != nil {
		return n, err
	}
	if int64(n) < remaining {
		return n, io.ErrShortWrite
	}
	return n, errBudgetOverflow
}

// WriteString mirrors Write's state machine while slicing the string
// BEFORE any []byte conversion, so only bytes that fit are copied.
func (cw *cappedWriter) WriteString(s string) (int, error) {
	if len(s) == 0 {
		return 0, nil
	}

	remaining := cw.limit - cw.written
	if remaining < 0 {
		remaining = 0
	}

	if int64(len(s)) <= remaining {
		n, err := io.WriteString(cw.dst, s)
		if n < 0 {
			n = 0
		}
		cw.written += int64(n)
		return n, err
	}

	if remaining == 0 {
		return 0, errBudgetOverflow
	}

	n, err := io.WriteString(cw.dst, s[:remaining])
	if n < 0 {
		n = 0
	}
	cw.written += int64(n)
	if err != nil {
		return n, err
	}
	if int64(n) < remaining {
		return n, io.ErrShortWrite
	}
	return n, errBudgetOverflow
}
