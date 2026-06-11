package raymond

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestIndentWriter_MatchesIndentLines(t *testing.T) {
	cases := []string{"", "a", "a\n", "a\nb", "a\nb\n", "a\n\nb\n", "\n", "\n\n"}
	indent := "  "
	for _, in := range cases {
		t.Run("input="+in, func(t *testing.T) {
			var buf bytes.Buffer
			iw := newIndentWriter(&buf, indent)
			if _, err := io.WriteString(iw, in); err != nil {
				t.Fatalf("WriteString error: %v", err)
			}
			got := buf.String()
			want := indentLines(in, indent)
			if got != want {
				t.Errorf("indentWriter(%q) = %q, want %q (indentLines)", in, got, want)
			}
		})
	}
}

func TestIndentWriter_SplitWrites(t *testing.T) {
	var buf bytes.Buffer
	iw := newIndentWriter(&buf, "_")
	if _, err := io.WriteString(iw, "a\n"); err != nil {
		t.Fatalf("first write error: %v", err)
	}
	if _, err := io.WriteString(iw, "b"); err != nil {
		t.Fatalf("second write error: %v", err)
	}
	got := buf.String()
	want := "_a\n_b"
	if got != want {
		t.Errorf("split writes = %q, want %q", got, want)
	}
}

func TestDestWriter_TagsErrors(t *testing.T) {
	myErr := errors.New("underlying failure")
	dw := &destWriter{w: &errWriter{err: myErr}}
	_, err := dw.Write([]byte("hello"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var de *destError
	if !errors.As(err, &de) {
		t.Fatalf("error %v is not *destError", err)
	}
	if !errors.Is(err, myErr) {
		t.Errorf("cause not wrapped: errors.Is(err, myErr) = false")
	}
}

func TestStateStep_FuelAndCtx(t *testing.T) {
	t.Run("step limit", func(t *testing.T) {
		s := &state{
			tctx:         context.Background(),
			limits:       Limits{MaxSteps: 10},
			nextCtxCheck: ctxCheckInterval,
		}
		for i := 0; i < 10; i++ {
			if err := s.step(1); err != nil {
				t.Fatalf("step %d unexpected error: %v", i, err)
			}
		}
		err := s.step(1)
		if err == nil {
			t.Fatal("expected step limit error, got nil")
		}
		if !errors.Is(err, ErrStepLimit) {
			t.Errorf("error %v does not wrap ErrStepLimit", err)
		}
	})

	t.Run("ctx canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := &state{
			tctx:         ctx,
			nextCtxCheck: ctxCheckInterval,
		}
		var lastErr error
		for i := 0; i <= ctxCheckInterval+1; i++ {
			lastErr = s.step(1)
			if lastErr != nil {
				break
			}
		}
		if lastErr == nil {
			t.Fatal("expected context.Canceled error, got nil")
		}
		if !errors.Is(lastErr, context.Canceled) {
			t.Errorf("error %v does not wrap context.Canceled", lastErr)
		}
	})
}

func TestCapture_BoundedByRemainingBudget(t *testing.T) {
	var sink bytes.Buffer
	cap := newCappedWriter(&sink, 5)

	s := &state{
		tctx: context.Background(),
		w:    cap,
		cap:  cap,
	}

	_, err := s.capture(func() error {
		payload := make([]byte, 100)
		_, werr := io.WriteString(s.w, string(payload))
		return werr
	})

	if err == nil {
		t.Fatal("expected budget overflow error, got nil")
	}
	if !errors.Is(err, errBudgetOverflow) {
		t.Errorf("error %v does not wrap errBudgetOverflow", err)
	}
	if sink.Len() != 0 {
		t.Errorf("sink has %d bytes, want 0", sink.Len())
	}
}
