package raymond

import (
	"errors"
	"fmt"
)

// Limits bounds what a single Compile or Execute call may consume.
// The zero value means unlimited on every axis.
type Limits struct {
	// Parse-time, consumed by Compile.
	MaxTemplateSize int // bytes of source, checked before lexing
	MaxNodes        int // AST nodes, enforced inside the parser
	MaxDepth        int // nesting depth (programs + subexpressions)

	// Execution-time, consumed by Execute.
	MaxOutputBytes   int64 // bytes delivered to the destination writer
	MaxSubstitutions int64 // mustache substitutions rendered
	MaxSteps         int64 // CPU fuel
}

var (
	ErrTemplateTooLarge   = errors.New("raymond: template source exceeds size limit")
	ErrTemplateTooComplex = errors.New("raymond: template exceeds structural limit")
	ErrOutputLimit        = errors.New("raymond: output byte limit exceeded")
	ErrSubstitutionLimit  = errors.New("raymond: substitution limit exceeded")
	ErrStepLimit          = errors.New("raymond: step limit exceeded")
)

// LimitError reports which limit was breached; Unwrap yields the sentinel.
type LimitError struct {
	Kind     string
	Limit    int64
	sentinel error
}

func newLimitError(kind string, limit int64, sentinel error) *LimitError {
	return &LimitError{Kind: kind, Limit: limit, sentinel: sentinel}
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("raymond: %s limit exceeded (limit %d)", e.Kind, e.Limit)
}

func (e *LimitError) Unwrap() error { return e.sentinel }
