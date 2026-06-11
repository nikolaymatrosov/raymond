package raymond

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/aymerick/raymond/ast"
	"github.com/aymerick/raymond/parser"
)

// Template represents a handlebars template.
type Template struct {
	source   string
	program  *ast.Program
	helpers  map[string]reflect.Value
	partials map[string]*partial
	report   ParseReport  // populated by ParseWithOptions when the visitor runs
	mutex    sync.RWMutex // protects helpers and partials

	// seam closures are pure functions of tpl (they resolve helpers and
	// partials dynamically at call time), so they are built once and
	// reused across executes instead of reallocated per MustExec.
	seamOnce     sync.Once
	helperSeamF  func(string) coreHelper
	partialSeamF func(string) (*ast.Program, error)

	// helperCache memoizes resolved template-local helper wrappers
	// (guarded by mutex). Only add-only template helpers are cached;
	// see helperSeam.
	helperCache map[string]coreHelper
}

// seams returns the memoized helper/partial resolution closures.
func (tpl *Template) seams() (func(string) coreHelper, func(string) (*ast.Program, error)) {
	tpl.seamOnce.Do(func() {
		tpl.helperSeamF = tpl.helperSeam()
		tpl.partialSeamF = tpl.partialSeam()
	})
	return tpl.helperSeamF, tpl.partialSeamF
}

// newTemplate instanciate a new template without parsing it
func newTemplate(source string) *Template {
	return &Template{
		source:   source,
		helpers:  make(map[string]reflect.Value),
		partials: make(map[string]*partial),
	}
}

// Parse instanciates a template by parsing given source.
func Parse(source string) (*Template, error) {
	tpl := newTemplate(source)

	// parse template
	if err := tpl.parse(); err != nil {
		return nil, err
	}

	return tpl, nil
}

// ParseWithOptions parses source under the given options.
//
// On success returns a *Template carrying a ParseReport (retrievable via
// (*Template).Report()). On a budget breach returns *BudgetExceededError.
// On a capability violation returns *CapabilityError. Otherwise returns
// whatever the parser returns (syntax error).
//
// A zero-valued ParseOptions{} is documented as legacy behaviour: the
// capability/budget visitor is not invoked and Report() returns the zero
// report.
func ParseWithOptions(source string, opts ParseOptions) (*Template, error) {
	tpl := newTemplate(source)
	if err := tpl.parse(); err != nil {
		return nil, err
	}
	// Skip the visitor entirely on the legacy/zero-options path so the
	// opt-out benchmark remains identical to legacy Parse.
	if opts.Mode == ModeFull &&
		!opts.Capabilities.If &&
		!opts.Capabilities.Iteration &&
		!opts.Capabilities.Partials &&
		!opts.Budget.Enforced {
		return tpl, nil
	}
	report, err := runCapVisitor(tpl.program, opts)
	if err != nil {
		return nil, err
	}
	tpl.report = report
	return tpl, nil
}

// Report returns a copy of the parse report attached to a successfully
// parsed template. Templates parsed via the legacy Parse / MustParse /
// ParseFile entry points carry the zero-valued report.
func (tpl *Template) Report() ParseReport {
	out := ParseReport{Substitutions: tpl.report.Substitutions}
	if len(tpl.report.Constructs) > 0 {
		out.Constructs = append([]string(nil), tpl.report.Constructs...)
	}
	return out
}

// MustParse instanciates a template by parsing given source. It panics on error.
func MustParse(source string) *Template {
	result, err := Parse(source)
	if err != nil {
		panic(err)
	}
	return result
}

// ParseFile reads given file and returns parsed template.
func ParseFile(filePath string) (*Template, error) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	return Parse(string(b))
}

// parse parses the template
//
// It can be called several times, the parsing will be done only once.
func (tpl *Template) parse() error {
	if tpl.program == nil {
		var err error

		tpl.program, err = parser.Parse(tpl.source)
		if err != nil {
			return err
		}
	}

	return nil
}

// Clone returns a copy of that template.
func (tpl *Template) Clone() *Template {
	result := newTemplate(tpl.source)

	result.program = tpl.program

	tpl.mutex.RLock()
	defer tpl.mutex.RUnlock()

	for name, helper := range tpl.helpers {
		result.RegisterHelper(name, helper.Interface())
	}

	for name, partial := range tpl.partials {
		result.addPartial(name, partial.source, partial.tpl)
	}

	return result
}

// RegisterHelper registers a helper for that template.
func (tpl *Template) RegisterHelper(name string, helper any) {
	switch helper.(type) {
	case Helper, func(*HelperCall) error:
		panic(fmt.Sprintf("Streaming helpers are not supported on Template; register %s globally with RegisterHelper or on a Compiled template", name))
	}

	tpl.mutex.Lock()
	defer tpl.mutex.Unlock()

	if tpl.helpers[name] != zero {
		panic(fmt.Sprintf("Helper %s already registered", name))
	}

	val := reflect.ValueOf(helper)
	if err := ensureValidHelper(name, val); err != nil {
		panic(err)
	}

	tpl.helpers[name] = val
}

// RegisterHelpers registers several helpers for that template.
func (tpl *Template) RegisterHelpers(helpers map[string]any) {
	for name, helper := range helpers {
		tpl.RegisterHelper(name, helper)
	}
}

func (tpl *Template) addPartial(name string, source string, template *Template) {
	tpl.mutex.Lock()
	defer tpl.mutex.Unlock()

	if tpl.partials[name] != nil {
		panic(fmt.Sprintf("Partial %s already registered", name))
	}

	tpl.partials[name] = newPartial(name, source, template)
}

func (tpl *Template) findPartial(name string) *partial {
	tpl.mutex.RLock()
	defer tpl.mutex.RUnlock()

	return tpl.partials[name]
}

// RegisterPartial registers a partial for that template.
func (tpl *Template) RegisterPartial(name string, source string) {
	tpl.addPartial(name, source, nil)
}

// RegisterPartials registers several partials for that template.
func (tpl *Template) RegisterPartials(partials map[string]string) {
	for name, partial := range partials {
		tpl.RegisterPartial(name, partial)
	}
}

// RegisterPartialFile reads given file and registers its content as a partial with given name.
func (tpl *Template) RegisterPartialFile(filePath string, name string) error {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	tpl.RegisterPartial(name, string(b))

	return nil
}

// RegisterPartialFiles reads several files and registers them as partials, the filename base is used as the partial name.
func (tpl *Template) RegisterPartialFiles(filePaths ...string) error {
	if len(filePaths) == 0 {
		return nil
	}

	for _, filePath := range filePaths {
		name := fileBase(filePath)

		if err := tpl.RegisterPartialFile(filePath, name); err != nil {
			return err
		}
	}

	return nil
}

// RegisterPartialTemplate registers an already parsed partial for that template.
func (tpl *Template) RegisterPartialTemplate(name string, template *Template) {
	tpl.addPartial(name, "", template)
}

// Exec evaluates template with given context.
func (tpl *Template) Exec(ctx any) (result string, err error) {
	return tpl.ExecWith(ctx, nil)
}

// MustExec evaluates template with given context. It panics on error.
func (tpl *Template) MustExec(ctx any) string {
	result, err := tpl.Exec(ctx)
	if err != nil {
		panic(err)
	}
	return result
}

// ExecWith evaluates template with given context and private data frame.
func (tpl *Template) ExecWith(ctx any, privData *DataFrame) (result string, err error) {
	defer errRecover(&err)

	// parses template if necessary
	if err = tpl.parse(); err != nil {
		return
	}

	var sb strings.Builder
	if err = tpl.execute(context.Background(), &sb, nil, ctx, privData, Limits{}); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// ExecTo evaluates the template with the given context and streams the
// rendered bytes into w as they are produced. It is equivalent to
// ExecToWithOptions(w, ctx, nil, RenderOptions{}). No output budget is
// enforced; a destination write failure is surfaced as
// *RenderDestinationError.
func (tpl *Template) ExecTo(w io.Writer, ctx any) error {
	return tpl.ExecToWithOptions(w, ctx, nil, RenderOptions{})
}

// ExecToWith evaluates the template with the given context and private
// data frame and streams the rendered bytes into w as they are
// produced. It is equivalent to
// ExecToWithOptions(w, ctx, privData, RenderOptions{}).
func (tpl *Template) ExecToWith(w io.Writer, ctx any, privData *DataFrame) error {
	return tpl.ExecToWithOptions(w, ctx, privData, RenderOptions{})
}

// ExecToWithOptions evaluates the template with the given context and
// private data frame and streams the rendered bytes into w. When
// opts.Enforced is true, opts.MaxOutputBytes is a strict upper bound
// on the cumulative number of bytes written: bytes-written greater
// than the limit aborts with *RenderBudgetExceededError; bytes-written
// equal to the limit succeeds (exact-fit). Bytes emitted by helpers,
// partials, inline partials, and nested template invocations all count
// against the same single budget (FR-008). Each call carries its own
// budget state; concurrent renders of the same *Template are
// independent (FR-010). A destination Write failure or short write is
// surfaced as *RenderDestinationError, which wraps the underlying
// cause for errors.Is / errors.As. The zero RenderOptions value is
// legacy behaviour: bytes flow to w unmodified, identical to ExecTo
// without options (FR-007); a zero MaxOutputBytes with Enforced is
// legal and means "any non-empty output fails" (FR-011).
func (tpl *Template) ExecToWithOptions(w io.Writer, ctx any, privData *DataFrame, opts RenderOptions) (err error) {
	if opts.Enforced && opts.MaxOutputBytes < 0 {
		return &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
	}

	defer errRecover(&err)

	if err = tpl.parse(); err != nil {
		return err
	}

	dw := &destWriter{w: w}
	var sink io.Writer = dw
	var capped *cappedWriter
	if opts.Enforced {
		capped = newCappedWriter(dw, opts.MaxOutputBytes)
		sink = capped
	}

	rerr := tpl.execute(context.Background(), sink, capped, ctx, privData, Limits{})
	if rerr == nil {
		return nil
	}
	if errors.Is(rerr, errBudgetOverflow) {
		return &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
	}
	if de, ok := errors.AsType[*destError](rerr); ok {
		return &RenderDestinationError{Cause: de.cause}
	}
	return rerr
}

// execute drives the streaming engine for this template. cap, when
// non-nil, is the outermost output cap already wrapping w.
func (tpl *Template) execute(c context.Context, w io.Writer, cap *cappedWriter,
	ctx any, privData *DataFrame, limits Limits) error {

	frame := privData
	if frame == nil {
		frame = NewDataFrame()
	}

	helpers, partials := tpl.seams()
	s := &state{
		tctx:         c,
		w:            w,
		cap:          cap,
		limits:       limits,
		nextCtxCheck: ctxCheckInterval,
		helpers:      helpers,
		partials:     partials,
		ctxStack:     []Value{adaptValue(ctx)},
		frame:        frame,
	}
	return s.renderProgram(tpl.program)
}

// helperSeam resolves helpers the way the old engine does: template
// helpers first, then globals (eval.go:583-591).
func (tpl *Template) helperSeam() func(string) coreHelper {
	return func(name string) coreHelper {
		tpl.mutex.RLock()
		if c, ok := tpl.helperCache[name]; ok {
			tpl.mutex.RUnlock()
			return c
		}
		h := tpl.helpers[name]
		tpl.mutex.RUnlock()

		// Template-local helpers are add-only (RegisterHelper panics on
		// a duplicate and there is no per-template remove), so a resolved
		// wrapper never goes stale — cache it to avoid reallocating
		// &legacyHelper on every call. Globals are NOT cached here:
		// RemoveHelper/RemoveAllHelpers could invalidate them.
		if h != zero {
			lh := &legacyHelper{name: name, fn: h}
			tpl.mutex.Lock()
			if tpl.helperCache == nil {
				tpl.helperCache = make(map[string]coreHelper)
			}
			tpl.helperCache[name] = lh
			tpl.mutex.Unlock()
			return lh
		}
		if e := findHelper(name); e.valid() {
			if e.streaming != nil {
				return &streamingHelper{h: e.streaming}
			}
			return &legacyHelper{name: name, fn: e.legacy}
		}
		return nil
	}
}

// partialSeam resolves partials the way the old engine does: template
// partials first, then globals, lazily parsed (eval.go:693-701,726-731).
func (tpl *Template) partialSeam() func(string) (*ast.Program, error) {
	return func(name string) (*ast.Program, error) {
		p := tpl.findPartial(name)
		if p == nil {
			p = findPartial(name)
		}
		if p == nil {
			return nil, nil
		}
		ptpl, err := p.template()
		if err != nil {
			return nil, err
		}
		// template() only parses when it builds the template from
		// source; a pre-built template registered via
		// RegisterPartialTemplate may still be unparsed.
		if perr := ptpl.parse(); perr != nil {
			return nil, perr
		}
		return ptpl.program, nil
	}
}

// errRecover recovers evaluation panic
func errRecover(errp *error) {
	e := recover()
	if e != nil {
		switch err := e.(type) {
		case runtime.Error:
			panic(e)
		case error:
			*errp = err
		default:
			panic(e)
		}
	}
}

// PrintAST returns string representation of parsed template.
func (tpl *Template) PrintAST() string {
	if err := tpl.parse(); err != nil {
		return fmt.Sprintf("PARSER ERROR: %s", err)
	}

	return ast.Print(tpl.program)
}
