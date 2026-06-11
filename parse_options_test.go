package raymond

import (
	"errors"
	"testing"
)

// TestParseOptions_Precedence covers the precedence rules from data-model §2:
//  1. ModeSimple → simple semantics (Capabilities ignored).
//  2. else if any toggle / Budget.Enforced → granular.
//  3. else → legacy (visitor not invoked).
func TestParseOptions_Precedence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		opts    ParseOptions
		src     string
		wantErr bool
	}{
		{"zero-options-legacy-allows-everything", ParseOptions{}, `{{#if x}}{{> p}}{{/if}}`, false},
		{"simple-overrides-caps", ParseOptions{Mode: ModeSimple, Capabilities: Capabilities{If: true, Iteration: true, Partials: true}}, `{{#if x}}y{{/if}}`, true},
		{"granular-rejects-with", ParseOptions{Capabilities: Capabilities{If: true, Iteration: true, Partials: true}}, `{{#with x}}y{{/with}}`, true},
		{"granular-via-budget-only-rejects-blocks", ParseOptions{Budget: Budget{MaxSubstitutions: 100, Enforced: true}}, `{{#if x}}y{{/if}}`, true},
		{"granular-via-budget-allows-plain-subs", ParseOptions{Budget: Budget{MaxSubstitutions: 100, Enforced: true}}, `{{a}}{{b}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseWithOptions(c.src, c.opts)
			if c.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseOptions_LegacyIdenticalToParse(t *testing.T) {
	t.Parallel()
	src := `{{#if x}}{{name}}{{else}}none{{/if}}{{#each xs}}{{this}}{{/each}}{{> p}}{{upper foo}}`
	a, errA := Parse(src)
	b, errB := ParseWithOptions(src, ParseOptions{})
	if (errA == nil) != (errB == nil) {
		t.Fatalf("Parse err=%v, ParseWithOptions err=%v", errA, errB)
	}
	if errA != nil {
		return
	}
	if a.PrintAST() != b.PrintAST() {
		t.Errorf("AST mismatch between Parse and ParseWithOptions(zero opts)")
	}
	var ce *CapabilityError
	var be *BudgetExceededError
	if errors.As(errB, &ce) || errors.As(errB, &be) {
		t.Errorf("legacy path produced typed error: %T %v", errB, errB)
	}
}
