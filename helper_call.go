package raymond

import (
	"context"
	"io"

	"github.com/aymerick/raymond/ast"
)

// Helper is the streaming helper interface for the new engine.
type Helper interface {
	CallHelper(hc *HelperCall) error
}

// HelperFunc adapts a function to Helper.
type HelperFunc func(hc *HelperCall) error

// CallHelper implements Helper.
func (f HelperFunc) CallHelper(hc *HelperCall) error { return f(hc) }

// callPosition tells a helper call where its streamed bytes land.
type callPosition uint8

const (
	posSubExpr     callPosition = iota // capture; captured string becomes the Value
	posMustache                        // escaped write-through
	posRawMustache                     // unescaped write-through
	posBlock                           // unescaped write-through
)

// HelperCall is the invocation context passed to streaming helpers.
type HelperCall struct {
	s      *state
	name   string
	expr   *ast.Expression
	params []Value
	hash   map[string]Value
	pos    callPosition
	w      io.Writer // position-appropriate sink
	rawW   io.Writer // position sink WITHOUT escaping (for WriteSafe and block bodies)
}

// Context returns the execution context of the running render.
func (hc *HelperCall) Context() context.Context { return hc.s.tctx }

// Name returns the helper name as written in the template.
func (hc *HelperCall) Name() string { return hc.name }

// NumParams returns the number of positional params.
func (hc *HelperCall) NumParams() int { return len(hc.params) }

// Param returns the positional param at i, or an invalid Value.
func (hc *HelperCall) Param(i int) Value {
	if i < len(hc.params) {
		return hc.params[i]
	}
	return Value{}
}

// Hash returns the named hash argument, or an invalid Value.
func (hc *HelperCall) Hash(name string) Value { return hc.hash[name] }

// Ctx returns the current evaluation context.
func (hc *HelperCall) Ctx() Value { return hc.s.curCtx() }

// Lookup resolves a single field on the current context (legacy
// Options.Value parity: exprRoot=false).
func (hc *HelperCall) Lookup(name string) (Value, error) {
	return hc.s.lookupField(hc.s.curCtx(), name, false)
}

// Data resolves a private-data key on the current frame.
func (hc *HelperCall) Data(name string) Value {
	return adaptValue(hc.s.frame.Get(name))
}

// DataFrame returns the current private data frame.
func (hc *HelperCall) DataFrame() *DataFrame { return hc.s.frame }

// NewDataFrame returns a copy of the current frame, parented to it.
func (hc *HelperCall) NewDataFrame() *DataFrame { return hc.s.frame.Copy() }

// Write streams helper output; escaped in escaped-mustache position,
// charged against output budget and fuel.
func (hc *HelperCall) Write(p []byte) (int, error) {
	if err := hc.s.writeSteps(len(p)); err != nil {
		return 0, err
	}
	return hc.w.Write(p)
}

// WriteString streams helper output like Write.
func (hc *HelperCall) WriteString(str string) (int, error) {
	if err := hc.s.writeSteps(len(str)); err != nil {
		return 0, err
	}
	return io.WriteString(hc.w, str)
}

// WriteSafe bypasses position escaping (SafeString analogue).
func (hc *HelperCall) WriteSafe(str string) (int, error) {
	if err := hc.s.writeSteps(len(str)); err != nil {
		return 0, err
	}
	return io.WriteString(hc.rawW, str)
}

// Fn streams the block body with the current context.
func (hc *HelperCall) Fn() error { return hc.fnWithKey(Value{}, nil, nil) }

// FnWith streams the block body with a new context.
func (hc *HelperCall) FnWith(ctx interface{}) error {
	return hc.fnWithKey(adaptValue(ctx), nil, nil)
}

// FnData streams the block body with a private data frame.
func (hc *HelperCall) FnData(frame *DataFrame) error {
	return hc.fnWithKey(Value{}, frame, nil)
}

// fnWithKey renders the current block's program into hc's raw sink
// (Options.evalBlock analogue, streaming).
func (hc *HelperCall) fnWithKey(ctx Value, frame *DataFrame, key interface{}) error {
	block := hc.s.curBlock()
	if block == nil || block.Program == nil {
		return nil
	}
	old := hc.s.w
	hc.s.w = hc.rawW
	err := hc.s.renderProgramWith(block.Program, ctx, frame, key)
	hc.s.w = old
	return err
}

// Inverse streams the else block (plain render: no ctx push, no block
// params — VisitBlock parity, eval.go:874-876).
func (hc *HelperCall) Inverse() error {
	block := hc.s.curBlock()
	if block == nil || block.Inverse == nil {
		return nil
	}
	old := hc.s.w
	hc.s.w = hc.rawW
	err := hc.s.renderProgram(block.Inverse)
	hc.s.w = old
	return err
}

// streamingHelper adapts Helper to the engine-internal coreHelper.
type streamingHelper struct{ h Helper }

func (sh *streamingHelper) callCore(hc *HelperCall) (Value, error) {
	s := hc.s
	switch hc.pos {
	case posMustache:
		hc.rawW = s.w
		hc.w = &escapeWriter{w: s.w}
		return Value{}, sh.h.CallHelper(hc)
	case posRawMustache, posBlock:
		hc.rawW = s.w
		hc.w = s.w
		return Value{}, sh.h.CallHelper(hc)
	default: // capture
		out, err := s.capture(func() error {
			hc.rawW = s.w
			hc.w = s.w
			return sh.h.CallHelper(hc)
		})
		if err != nil {
			return Value{}, err
		}
		return stringValue(out, false), nil
	}
}
