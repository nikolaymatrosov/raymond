package raymond

import (
	"fmt"

	"github.com/aymerick/raymond/ast"
)

// BudgetExceededError is returned by ParseWithOptions when a parse-time
// budget axis is breached. Kind identifies which axis; for this release
// Kind is always "substitutions". Limit is the configured ceiling and
// Observed is the count that crossed it.
type BudgetExceededError struct {
	Kind     string
	Limit    int
	Observed int
}

// Error implements the error interface.
func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("parse budget exceeded: kind=%s limit=%d observed=%d",
		e.Kind, e.Limit, e.Observed)
}

// CapabilityError is returned by ParseWithOptions when the template uses a
// construct disallowed by the active capability mode. Construct is one of:
// "if", "unless", "each", "with", "partial", "helper", "parent-path",
// "data-var". Loc points to the AST node that triggered the rejection.
type CapabilityError struct {
	Construct string
	Loc       ast.Loc
}

// Error implements the error interface.
func (e *CapabilityError) Error() string {
	return fmt.Sprintf("capability error: construct=%s line=%d pos=%d",
		e.Construct, e.Loc.Line, e.Loc.Pos)
}
