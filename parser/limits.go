package parser

import (
	"errors"
	"fmt"

	"github.com/aymerick/raymond/ast"
)

// ErrTooComplex is the sentinel error wrapped by every *LimitError, so
// callers can detect structural-limit breaches with errors.Is.
var ErrTooComplex = errors.New("parser: template exceeds structural limit")

// Limits bounds the structural complexity a single Parse call may build.
// The zero value means unlimited.
type Limits struct {
	// MaxNodes caps the number of AST nodes; parsing fails as soon as the
	// parser is about to construct node MaxNodes+1, without building the
	// rest of the tree.
	MaxNodes int

	// MaxDepth caps nesting depth. The root program is depth 1; each
	// nested block program or subexpression adds one.
	MaxDepth int
}

// LimitError reports which structural limit was exceeded.
type LimitError struct {
	Kind  string // "nodes" or "depth"
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("Parse error: template exceeds %s limit (%d)", e.Kind, e.Limit)
}

// Unwrap makes errors.Is(err, ErrTooComplex) true.
func (e *LimitError) Unwrap() error {
	return ErrTooComplex
}

// ParseWithLimits analyzes given input like Parse, but enforces structural
// limits while the tree is being built.
func ParseWithLimits(input string, limits Limits) (*ast.Program, error) {
	return newParser(input, limits).parse()
}

// countNode charges one AST node against the budget. It records a bare
// *LimitError in p.err (first error wins) like every other parser error.
// Once an error is sticky, it stops counting so the node counter freezes
// at the breach point (the same place the old panic unwound the stack).
func (p *parser) countNode() {
	if p.err != nil {
		return
	}
	p.nodeCount++
	if p.limits.MaxNodes > 0 && p.nodeCount > p.limits.MaxNodes {
		p.err = &LimitError{Kind: "nodes", Limit: p.limits.MaxNodes}
	}
}

// enterNesting tracks descent into a program or subexpression.
func (p *parser) enterNesting() {
	if p.err != nil {
		return
	}
	p.depth++
	if p.limits.MaxDepth > 0 && p.depth > p.limits.MaxDepth {
		p.err = &LimitError{Kind: "depth", Limit: p.limits.MaxDepth}
	}
}

func (p *parser) exitNesting() {
	p.depth--
}
