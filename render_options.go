package raymond

// RenderOptions configures per-call render behaviour for the streaming
// Exec entry points (ExecTo, ExecToWith, ExecToWithOptions).
//
// The zero value is documented as "legacy behaviour": no budget tracking
// is performed and the rendered bytes flow to the writer unmodified.
// This makes ExecTo / ExecToWithOptions safe drop-in replacements for
// callers that want streaming without yet caring about budgets.
type RenderOptions struct {
	// MaxOutputBytes is the strict upper bound on the cumulative number
	// of bytes delivered to the destination writer. Consulted only when
	// Enforced is true.
	//
	// Semantics:
	//   - bytes-written == MaxOutputBytes  -> success (exact-fit)
	//   - bytes-written  > MaxOutputBytes  -> *RenderBudgetExceededError
	//
	// A value of 0 with Enforced == true is a legal, meaningful
	// configuration: any render that would produce one or more output
	// bytes fails; a render that produces zero bytes succeeds.
	MaxOutputBytes int64

	// Enforced toggles output-byte budget enforcement. When false,
	// MaxOutputBytes is ignored and the render streams to the
	// destination without a cap.
	Enforced bool
}
