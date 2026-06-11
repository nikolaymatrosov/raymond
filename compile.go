package raymond

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/aymerick/raymond/ast"
	"github.com/aymerick/raymond/parser"
)

// Compiled is an immutable compiled template: compile once, Execute
// concurrently. All mutable render state lives in the per-call state.
type Compiled struct {
	source   string
	program  *ast.Program
	limits   Limits
	helpers  map[string]Helper
	partials map[string]*Compiled
	mu       sync.RWMutex
}

// Compile parses source under the given limits. Limits{} is unlimited.
func Compile(source string, limits Limits) (*Compiled, error) {
	if limits.MaxTemplateSize > 0 && len(source) > limits.MaxTemplateSize {
		return nil, newLimitError("source size", int64(limits.MaxTemplateSize), ErrTemplateTooLarge)
	}

	program, err := parser.ParseWithLimits(source, parser.Limits{
		MaxNodes: limits.MaxNodes,
		MaxDepth: limits.MaxDepth,
	})
	if err != nil {
		var ple *parser.LimitError
		if errors.As(err, &ple) {
			return nil, newLimitError(ple.Kind, int64(ple.Limit), ErrTemplateTooComplex)
		}
		return nil, err
	}

	return &Compiled{
		source:   source,
		program:  program,
		limits:   limits,
		helpers:  make(map[string]Helper),
		partials: make(map[string]*Compiled),
	}, nil
}

// RegisterHelper registers a streaming helper on this template.
func (c *Compiled) RegisterHelper(name string, h Helper) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.helpers[name]; ok {
		panic(fmt.Errorf("Helper already registered: %s", name))
	}
	c.helpers[name] = h
}

// RegisterPartial registers a compiled partial on this template.
// Helpers and partials referenced inside a partial resolve against the
// template that started the render, matching the legacy engine.
func (c *Compiled) RegisterPartial(name string, p *Compiled) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.partials[name]; ok {
		panic(fmt.Errorf("Partial already registered: %s", name))
	}
	c.partials[name] = p
}

// Execute renders into w with arbitrary Go data via the reflection
// adapter. Safe for concurrent use.
func (c *Compiled) Execute(ctx context.Context, w io.Writer, data interface{}) error {
	return c.exec(ctx, w, adaptValue(data))
}

// ExecuteData renders with a caller-implemented closed data model;
// no reflection is involved anywhere in the render.
func (c *Compiled) ExecuteData(ctx context.Context, w io.Writer, data Data) error {
	return c.exec(ctx, w, mapValue(data, true, data))
}

func (c *Compiled) exec(tctx context.Context, w io.Writer, root Value) (err error) {
	defer errRecover(&err)

	if tctx == nil {
		tctx = context.Background()
	}

	var sink io.Writer = w
	var capped *cappedWriter
	if c.limits.MaxOutputBytes > 0 {
		capped = newCappedWriter(w, c.limits.MaxOutputBytes)
		sink = capped
	}

	s := &state{
		tctx:         tctx,
		w:            sink,
		cap:          capped,
		limits:       c.limits,
		nextCtxCheck: ctxCheckInterval,
		helpers:      c.helperSeam(),
		partials:     c.partialSeam(),
		ctxStack:     []Value{root},
		frame:        NewDataFrame(),
		exprFunc:     make(map[*ast.Expression]bool),
	}

	err = s.renderProgram(c.program)
	if err != nil && errors.Is(err, errBudgetOverflow) {
		return newLimitError("output bytes", c.limits.MaxOutputBytes, ErrOutputLimit)
	}
	return err
}

// helperSeam resolves the template's own streaming helpers first, then
// the global registry (legacy funcs bridged).
func (c *Compiled) helperSeam() func(string) coreHelper {
	return func(name string) coreHelper {
		c.mu.RLock()
		h, ok := c.helpers[name]
		c.mu.RUnlock()
		if ok {
			return &streamingHelper{h: h}
		}
		if g := findHelper(name); g != zero {
			return &legacyHelper{name: name, fn: g}
		}
		return nil
	}
}

func (c *Compiled) partialSeam() func(name string) (*ast.Program, error) {
	return func(name string) (*ast.Program, error) {
		c.mu.RLock()
		p := c.partials[name]
		c.mu.RUnlock()
		if p == nil {
			return nil, nil
		}
		return p.program, nil
	}
}
