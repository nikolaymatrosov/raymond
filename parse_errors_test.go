package raymond

import (
	"errors"
	"strings"
	"testing"

	"github.com/aymerick/raymond/ast"
)

func TestBudgetExceededError_Message(t *testing.T) {
	t.Parallel()
	e := &BudgetExceededError{Kind: "substitutions", Limit: 100, Observed: 101}
	msg := e.Error()
	for _, want := range []string{"substitutions", "100", "101"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing %q", msg, want)
		}
	}
	other := &BudgetExceededError{Kind: "substitutions", Limit: 50, Observed: 51}
	if e.Error() == other.Error() {
		t.Errorf("expected distinct messages for distinct field values")
	}
}

func TestCapabilityError_Message(t *testing.T) {
	t.Parallel()
	e := &CapabilityError{Construct: "if", Loc: ast.Loc{Pos: 12, Line: 3}}
	msg := e.Error()
	for _, want := range []string{"if", "3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing %q", msg, want)
		}
	}
}

func TestErrorCategoryDistinguishability(t *testing.T) {
	t.Parallel()

	// Capability error.
	_, err := ParseWithOptions("{{#if x}}y{{/if}}", ParseOptions{Mode: ModeSimple})
	var ce *CapabilityError
	var be *BudgetExceededError
	if !errors.As(err, &ce) || errors.As(err, &be) {
		t.Errorf("expected *CapabilityError only, got %T", err)
	}

	// Budget error.
	_, err = ParseWithOptions("{{a}}{{b}}", ParseOptions{Budget: Budget{MaxSubstitutions: 1, Enforced: true}})
	ce, be = nil, nil
	if errors.As(err, &ce) || !errors.As(err, &be) {
		t.Errorf("expected *BudgetExceededError only, got %T", err)
	}

	// Syntax error path is unchanged shape.
	_, err = ParseWithOptions("{{", ParseOptions{})
	ce, be = nil, nil
	if err == nil || errors.As(err, &ce) || errors.As(err, &be) {
		t.Errorf("expected plain syntax error, got %T %v", err, err)
	}
}
