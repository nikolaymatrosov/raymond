package raymond

import "fmt"

// RenderBudgetExceededError reports that a render call exceeded its
// configured output-byte budget. It is returned by the streaming Exec
// entry points (ExecTo, ExecToWith, ExecToWithOptions) when the
// cumulative bytes produced by the render strictly exceed
// RenderOptions.MaxOutputBytes.
//
// Operators can identify this error programmatically with errors.As:
//
//	var bex *raymond.RenderBudgetExceededError
//	if errors.As(err, &bex) { ... }
type RenderBudgetExceededError struct {
	// Kind is the budget axis that was exceeded. For this feature the
	// only value is "output bytes".
	Kind string

	// Limit is the configured MaxOutputBytes value at the time of the
	// call. Useful for metrics and error messages.
	Limit int64
}

// Error implements the error interface.
func (e *RenderBudgetExceededError) Error() string {
	return fmt.Sprintf("raymond: render budget exceeded (%s, limit=%d)", e.Kind, e.Limit)
}

// RenderDestinationError reports that the operator-supplied destination
// returned an error or a short write during rendering. The render
// aborts at the point of the destination's failure.
//
// The underlying cause is wrapped; errors.Is and errors.As work
// transparently:
//
//	if errors.Is(err, io.ErrShortWrite) { ... }
type RenderDestinationError struct {
	// Cause is the underlying error returned by the destination writer.
	// Never nil for an error returned to the caller.
	Cause error
}

// Error implements the error interface.
func (e *RenderDestinationError) Error() string {
	if e.Cause == nil {
		return "raymond: render destination error"
	}
	return "raymond: render destination error: " + e.Cause.Error()
}

// Unwrap returns the underlying destination cause, enabling
// errors.Is / errors.As traversal.
func (e *RenderDestinationError) Unwrap() error {
	return e.Cause
}
