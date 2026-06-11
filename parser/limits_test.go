package parser

import (
	"errors"
	"strings"
	"testing"
)

// countNodes parses with unlimited limits and returns the number of AST nodes
// the parser constructed, so off-by-one tests don't hardcode node counts.
func countNodes(t *testing.T, input string) int {
	t.Helper()

	p := newParser(input, Limits{})
	if _, err := p.parse(); err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	return p.nodeCount
}

func TestParseWithLimits_Unlimited(t *testing.T) {
	program, err := ParseWithLimits("{{foo}} bar {{#if baz}}x{{/if}}", Limits{})
	if err != nil || program == nil {
		t.Fatalf("unlimited parse failed: %v", err)
	}
}

func TestParseWithLimits_MaxNodesExact(t *testing.T) {
	input := "{{foo}} bar {{#if baz}}x{{/if}}"
	n := countNodes(t, input)

	// exactly enough nodes: must succeed
	if _, err := ParseWithLimits(input, Limits{MaxNodes: n}); err != nil {
		t.Fatalf("parse with MaxNodes=%d failed: %v", n, err)
	}

	// one fewer: must fail with the typed error
	_, err := ParseWithLimits(input, Limits{MaxNodes: n - 1})
	if err == nil {
		t.Fatalf("parse with MaxNodes=%d should have failed", n-1)
	}

	var limitErr *LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("err = %v, want *LimitError", err)
	}
	if limitErr.Kind != "nodes" || limitErr.Limit != n-1 {
		t.Errorf("LimitError = %+v, want Kind=nodes Limit=%d", limitErr, n-1)
	}
	if !errors.Is(err, ErrTooComplex) {
		t.Errorf("errors.Is(err, ErrTooComplex) = false, want true")
	}
}

// TestParseWithLimits_MaxNodesFailsEarly proves the limit is enforced inside
// the parser: a deeply repetitive template must fail without building the
// full tree, observable through the parser's node counter stopping at N+1.
func TestParseWithLimits_MaxNodesFailsEarly(t *testing.T) {
	input := strings.Repeat("{{x}}", 10000)

	p := newParser(input, Limits{MaxNodes: 10})
	_, err := p.parse()
	if err == nil {
		t.Fatal("expected node limit error")
	}
	if p.nodeCount != 11 {
		t.Errorf("nodeCount = %d, want 11 (fail at node N+1)", p.nodeCount)
	}
}

func TestParseWithLimits_MaxDepth(t *testing.T) {
	// Root program is depth 1; each nested block adds one.
	nested := "{{#if a}}{{#if b}}{{#if c}}x{{/if}}{{/if}}{{/if}}"

	if _, err := ParseWithLimits(nested, Limits{MaxDepth: 4}); err != nil {
		t.Fatalf("parse with MaxDepth=4 failed: %v", err)
	}

	_, err := ParseWithLimits(nested, Limits{MaxDepth: 3})
	if err == nil {
		t.Fatal("parse with MaxDepth=3 should have failed")
	}

	var limitErr *LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("err = %v, want *LimitError", err)
	}
	if limitErr.Kind != "depth" || limitErr.Limit != 3 {
		t.Errorf("LimitError = %+v, want Kind=depth Limit=3", limitErr)
	}
}

func TestParseWithLimits_MaxDepthSubExpression(t *testing.T) {
	// Subexpressions nest too: root(1) + 3 sexpr levels.
	input := "{{a (b (c (d e)))}}"

	if _, err := ParseWithLimits(input, Limits{MaxDepth: 4}); err != nil {
		t.Fatalf("parse with MaxDepth=4 failed: %v", err)
	}

	if _, err := ParseWithLimits(input, Limits{MaxDepth: 3}); err == nil {
		t.Fatal("parse with MaxDepth=3 should have failed")
	}
}

func TestParseWithLimits_SyntaxErrorNotLimitError(t *testing.T) {
	_, err := ParseWithLimits("{{#foo}}{{/bar}}", Limits{MaxNodes: 1000})
	if err == nil {
		t.Fatal("expected syntax error")
	}

	var limitErr *LimitError
	if errors.As(err, &limitErr) {
		t.Errorf("syntax error must not be a *LimitError, got %v", err)
	}
}
