// Package raymond — parse-time capability and budget options.
//
// This file declares the public option types consumed by ParseWithOptions.
// See specs/001-parse-budget-template-modes/quickstart.md for usage.
package raymond

// Mode selects a capability preset for ParseWithOptions.
//
// The zero value is ModeFull, which — combined with a zero ParseOptions —
// reproduces legacy Parse behaviour. Unknown integer values MUST be treated
// as ModeFull to keep the surface forward-compatible.
type Mode int

const (
	// ModeFull is the default zero-value preset. With no other fields set on
	// ParseOptions, behaviour is identical to legacy Parse.
	ModeFull Mode = iota
	// ModeSimple permits only literal text, comments, and plain
	// current-context substitutions. All control flow, partials, helpers,
	// parent-context paths, and @-data variables are rejected with
	// *CapabilityError.
	ModeSimple
)

// Capabilities are the independently-toggleable construct families honoured
// in granular mode. Granular mode is active when Mode is not ModeSimple and
// at least one of these toggles is true OR a Budget is enforced.
type Capabilities struct {
	If        bool
	Iteration bool
	Partials  bool
}

// Budget caps parse-time resource consumption.
//
// Budget{} (zero value) means no limit. Budget{Enforced:true,
// MaxSubstitutions:n} caps the substitution-producing mustache count at n
// inclusive — a template with exactly n substitutions parses, n+1 fails.
type Budget struct {
	MaxSubstitutions int
	Enforced         bool
}

// ParseOptions bundles capability and budget configuration for
// ParseWithOptions. The zero value ParseOptions{} is documented as legacy
// behaviour: identical to calling Parse(source).
//
// Precedence:
//
//  1. Mode == ModeSimple → simple semantics, Capabilities ignored.
//  2. Otherwise, if any Capabilities toggle is true OR Budget.Enforced is
//     true → granular mode.
//  3. Otherwise → legacy "full" semantics; visitor is not invoked.
type ParseOptions struct {
	Mode         Mode
	Capabilities Capabilities
	Budget       Budget
}
