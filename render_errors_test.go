package raymond

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// failingWriter returns the configured error on every Write.
type failingWriter struct{ err error }

func (w *failingWriter) Write(p []byte) (int, error) { return 0, w.err }

func TestRenderBudgetExceededError_FieldsPopulated(t *testing.T) {
	tpl := MustParse("0123456789")
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 5, Enforced: true})
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
	if bex.Kind != "output bytes" {
		t.Errorf("Kind = %q, want %q", bex.Kind, "output bytes")
	}
	if bex.Limit != 5 {
		t.Errorf("Limit = %d, want 5", bex.Limit)
	}
}

func TestRenderBudgetExceededError_IdentifiableViaErrorsAs(t *testing.T) {
	tpl := MustParse("hello")
	if _, err := tpl.Exec(nil); err != nil {
		t.Fatalf("baseline Exec failed: %v", err)
	}

	// Successful render: must NOT match RenderBudgetExceededError
	var buf bytes.Buffer
	if err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 100, Enforced: true}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	var bex *RenderBudgetExceededError
	if errors.As(error(nil), &bex) {
		t.Errorf("nil err must not match *RenderBudgetExceededError")
	}
}

func TestRenderBudgetExceededError_DistinctFromDestinationError(t *testing.T) {
	tpl := MustParse("hello")
	myErr := errors.New("nope")
	err := tpl.ExecToWithOptions(&failingWriter{err: myErr}, nil, nil, RenderOptions{})

	var bex *RenderBudgetExceededError
	if errors.As(err, &bex) {
		t.Errorf("destination failure must NOT match *RenderBudgetExceededError; got %v", err)
	}
	var dex *RenderDestinationError
	if !errors.As(err, &dex) {
		t.Fatalf("err = %v, want *RenderDestinationError", err)
	}
	if !errors.Is(err, myErr) {
		t.Errorf("errors.Is(err, myErr) = false; want true")
	}
}

func TestRenderDestinationError_WrapsCause(t *testing.T) {
	tpl := MustParse("hello")

	// Custom error
	myErr := errors.New("boom")
	err := tpl.ExecToWithOptions(&failingWriter{err: myErr}, nil, nil, RenderOptions{})
	if !errors.Is(err, myErr) {
		t.Errorf("errors.Is(err, myErr) = false; want true (err=%v)", err)
	}
	var dex *RenderDestinationError
	if !errors.As(err, &dex) {
		t.Fatalf("err = %v, want *RenderDestinationError", err)
	}
	if dex.Unwrap() != myErr {
		t.Errorf("Unwrap = %v, want %v", dex.Unwrap(), myErr)
	}

	// Short write surfaces io.ErrShortWrite
	sw := &shortWriter{}
	err = tpl.ExecToWithOptions(sw, nil, nil, RenderOptions{})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("errors.Is(err, io.ErrShortWrite) = false; want true (err=%v)", err)
	}
}

func TestRenderDestinationError_DistinctFromBudgetError(t *testing.T) {
	tpl := MustParse("0123456789")
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 3, Enforced: true})
	var dex *RenderDestinationError
	if errors.As(err, &dex) {
		t.Errorf("budget overflow must NOT match *RenderDestinationError; got %v", err)
	}
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
}
