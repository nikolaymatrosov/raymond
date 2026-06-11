package raymond

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/aymerick/raymond/ast"
)

const ctxCheckInterval = 1024

// writeCostShift: writes charge 1 + n>>8 fuel so output-amplifying
// helpers pay proportionally.
const writeCostShift = 8

// coreHelper is the engine-internal helper shape; legacy and streaming
// helpers both wrap into it.
type coreHelper interface {
	callCore(hc *HelperCall) (Value, error)
}

// state carries all mutable data of one render.
type state struct {
	tctx context.Context

	w   io.Writer     // current sink; swapped around captures/partials
	cap *cappedWriter // outermost output cap, nil when unbounded

	limits       Limits
	steps        int64
	subs         int64
	nextCtxCheck int64

	helpers  func(name string) coreHelper
	partials func(name string) (*ast.Program, error)

	ctxStack    []Value
	frame       *DataFrame
	blockParams []map[string]Value
	blocks      []*ast.BlockStatement
	exprs       []*ast.Expression
	exprFunc    map[*ast.Expression]bool
	curNode     ast.Node
}

func (s *state) step(n int64) error {
	s.steps += n
	if s.limits.MaxSteps > 0 && s.steps > s.limits.MaxSteps {
		return newLimitError("steps", s.limits.MaxSteps, ErrStepLimit)
	}
	if s.steps >= s.nextCtxCheck {
		s.nextCtxCheck = s.steps + ctxCheckInterval
		if err := s.tctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) writeSteps(n int) error {
	return s.step(1 + int64(n)>>writeCostShift)
}

// errorf mirrors evalVisitor.errorf/errPanic (eval.go:236-243) as a
// returned error, identical message shape.
func (s *state) errorf(format string, args ...interface{}) error {
	err := fmt.Errorf(format, args...)
	return fmt.Errorf("Evaluation error: %s\nCurrent node:\n\t%s", err, s.curNode)
}

func (s *state) at(node ast.Node) { s.curNode = node }

// context stack (ports eval.go:86-121)
func (s *state) pushCtx(v Value) { s.ctxStack = append(s.ctxStack, v) }
func (s *state) popCtx() {
	if len(s.ctxStack) > 0 {
		s.ctxStack = s.ctxStack[:len(s.ctxStack)-1]
	}
}
func (s *state) rootCtx() Value { return s.ctxStack[0] }
func (s *state) curCtx() Value  { return s.ancestorCtx(0) }
func (s *state) ancestorCtx(depth int) Value {
	index := len(s.ctxStack) - 1 - depth
	if index < 0 {
		return Value{}
	}
	return s.ctxStack[index]
}

// data frame / block params / blocks / exprs stacks (eval.go:127-229)
func (s *state) setDataFrame(frame *DataFrame) { s.frame = frame }
func (s *state) popDataFrame()                 { s.frame = s.frame.parent }

func (s *state) pushBlockParams(p map[string]Value) { s.blockParams = append(s.blockParams, p) }
func (s *state) popBlockParams() {
	if len(s.blockParams) > 0 {
		s.blockParams = s.blockParams[:len(s.blockParams)-1]
	}
}
func (s *state) blockParam(name string) (Value, bool) {
	for i := len(s.blockParams) - 1; i >= 0; i-- {
		if v, ok := s.blockParams[i][name]; ok {
			return v, true
		}
	}
	return Value{}, false
}

func (s *state) pushBlock(b *ast.BlockStatement) { s.blocks = append(s.blocks, b) }
func (s *state) popBlock() {
	if len(s.blocks) > 0 {
		s.blocks = s.blocks[:len(s.blocks)-1]
	}
}
func (s *state) curBlock() *ast.BlockStatement {
	if len(s.blocks) == 0 {
		return nil
	}
	return s.blocks[len(s.blocks)-1]
}

func (s *state) pushExpr(e *ast.Expression) { s.exprs = append(s.exprs, e) }
func (s *state) popExpr() {
	if len(s.exprs) > 0 {
		s.exprs = s.exprs[:len(s.exprs)-1]
	}
}
func (s *state) curExpr() *ast.Expression {
	if len(s.exprs) == 0 {
		return nil
	}
	return s.exprs[len(s.exprs)-1]
}

// capture renders fn's writes into a buffer and returns the string
// (legacy Options.Fn contract). With an active cap the buffer is
// itself capped at the remaining global budget so speculative helper
// output never exceeds limit + O(1) memory.
func (s *state) capture(fn func() error) (string, error) {
	var buf bytes.Buffer
	var sink io.Writer = &buf
	if s.cap != nil {
		remaining := s.cap.limit - s.cap.written
		if remaining < 0 {
			remaining = 0
		}
		sink = newCappedWriter(&buf, remaining)
	}

	old := s.w
	s.w = sink
	err := fn()
	s.w = old

	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// destError tags errors from the user-supplied writer so shims can wrap
// them as *RenderDestinationError.
type destError struct{ cause error }

func (e *destError) Error() string { return e.cause.Error() }
func (e *destError) Unwrap() error { return e.cause }

type destWriter struct{ w io.Writer }

func (dw *destWriter) Write(p []byte) (int, error) {
	n, err := dw.w.Write(p)
	if n < 0 {
		n = 0
	}
	if err != nil {
		return n, &destError{cause: err}
	}
	if n < len(p) {
		return n, &destError{cause: io.ErrShortWrite}
	}
	return n, nil
}

// indentWriter is the streaming indentLines (eval.go:753-771): indent
// lazily before the first byte of each line — middle empty lines get
// the indent, the empty tail after a final newline does not.
type indentWriter struct {
	w       io.Writer
	indent  string
	pending bool
}

func newIndentWriter(w io.Writer, indent string) *indentWriter {
	return &indentWriter{w: w, indent: indent, pending: true}
}

func (iw *indentWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		if iw.pending {
			if _, err := io.WriteString(iw.w, iw.indent); err != nil {
				return total, err
			}
			iw.pending = false
		}
		nl := bytes.IndexByte(p, '\n')
		chunk := p
		if nl >= 0 {
			chunk = p[:nl+1]
			iw.pending = true
		}
		n, err := iw.w.Write(chunk)
		total += n
		if err != nil {
			return total, err
		}
		p = p[len(chunk):]
	}
	return total, nil
}

func (iw *indentWriter) WriteString(str string) (int, error) {
	return iw.Write([]byte(str))
}

// escapeWriter HTML-escapes streamed helper output in escaped-mustache
// position, reusing escape() (escape.go).
type escapeWriter struct{ w io.Writer }

func (ew *escapeWriter) Write(p []byte) (int, error) {
	if err := escape(stringWriterFor(ew.w), string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (ew *escapeWriter) WriteString(str string) (int, error) {
	if err := escape(stringWriterFor(ew.w), str); err != nil {
		return 0, err
	}
	return len(str), nil
}

type plainStringWriter struct{ w io.Writer }

func (p plainStringWriter) WriteString(s string) (int, error) {
	return io.WriteString(p.w, s)
}

func stringWriterFor(w io.Writer) writer {
	if sw, ok := w.(writer); ok {
		return sw
	}
	return plainStringWriter{w: w}
}
