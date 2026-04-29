package raymond

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"reflect"
	"runtime"
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
	report   ParseReport // populated by ParseWithOptions when the visitor runs
	mutex    sync.RWMutex // protects helpers and partials
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
	b, err := ioutil.ReadFile(filePath)
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

func (tpl *Template) findHelper(name string) reflect.Value {
	tpl.mutex.RLock()
	defer tpl.mutex.RUnlock()

	return tpl.helpers[name]
}

// RegisterHelper registers a helper for that template.
func (tpl *Template) RegisterHelper(name string, helper interface{}) {
	tpl.mutex.Lock()
	defer tpl.mutex.Unlock()

	if tpl.helpers[name] != zero {
		panic(fmt.Sprintf("Helper %s already registered", name))
	}

	val := reflect.ValueOf(helper)
	ensureValidHelper(name, val)

	tpl.helpers[name] = val
}

// RegisterHelpers registers several helpers for that template.
func (tpl *Template) RegisterHelpers(helpers map[string]interface{}) {
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
	b, err := ioutil.ReadFile(filePath)
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
func (tpl *Template) Exec(ctx interface{}) (result string, err error) {
	return tpl.ExecWith(ctx, nil)
}

// MustExec evaluates template with given context. It panics on error.
func (tpl *Template) MustExec(ctx interface{}) string {
	result, err := tpl.Exec(ctx)
	if err != nil {
		panic(err)
	}
	return result
}

// ExecWith evaluates template with given context and private data frame.
func (tpl *Template) ExecWith(ctx interface{}, privData *DataFrame) (result string, err error) {
	defer errRecover(&err)

	// parses template if necessary
	err = tpl.parse()
	if err != nil {
		return
	}

	// setup visitor
	v := newEvalVisitor(tpl, ctx, privData)

	// visit AST
	result, _ = tpl.program.Accept(v).(string)

	// named return values
	return
}

// ExecTo evaluates the template with the given context and streams the
// rendered bytes into w as they are produced. It is equivalent to
// ExecToWithOptions(w, ctx, nil, RenderOptions{}). No output budget is
// enforced; a destination write failure is surfaced as
// *RenderDestinationError.
func (tpl *Template) ExecTo(w io.Writer, ctx interface{}) error {
	return tpl.ExecToWithOptions(w, ctx, nil, RenderOptions{})
}

// ExecToWith evaluates the template with the given context and private
// data frame and streams the rendered bytes into w as they are
// produced. It is equivalent to
// ExecToWithOptions(w, ctx, privData, RenderOptions{}).
func (tpl *Template) ExecToWith(w io.Writer, ctx interface{}, privData *DataFrame) error {
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
func (tpl *Template) ExecToWithOptions(w io.Writer, ctx interface{}, privData *DataFrame, opts RenderOptions) (err error) {
	if opts.Enforced && opts.MaxOutputBytes < 0 {
		return &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
	}

	defer execToErrRecover(&err, opts)

	if err = tpl.parse(); err != nil {
		return err
	}

	var dst io.Writer = w
	var capped *cappedWriter
	if opts.Enforced {
		capped = newCappedWriter(w, opts.MaxOutputBytes)
		dst = capped
	}

	v := newEvalVisitor(tpl, ctx, privData)
	v.out = capped

	for _, n := range tpl.program.Body {
		str := Str(n.Accept(v))
		if str == "" {
			continue
		}

		// When a budget is in force, allocate at most the bytes that
		// still fit so peak in-process buffer memory stays bounded by
		// limit + O(1) (SC-001) even for very large literal/helper
		// outputs.
		if capped != nil {
			remaining := opts.MaxOutputBytes - capped.written
			if remaining < 0 {
				remaining = 0
			}
			if int64(len(str)) > remaining {
				if remaining > 0 {
					prefix := []byte(str[:remaining])
					nW, werr := w.Write(prefix)
					if nW < 0 {
						nW = 0
					}
					capped.written += int64(nW)
					if werr != nil {
						return &RenderDestinationError{Cause: werr}
					}
					if nW < len(prefix) {
						return &RenderDestinationError{Cause: io.ErrShortWrite}
					}
				}
				return &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
			}
		}

		p := []byte(str)
		nWritten, werr := dst.Write(p)
		if nWritten < 0 {
			nWritten = 0
		}
		if capped != nil {
			v.committed = capped.written
		} else {
			v.committed += int64(nWritten)
		}
		if werr != nil {
			if errors.Is(werr, errBudgetOverflow) {
				return &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
			}
			return &RenderDestinationError{Cause: werr}
		}
		if nWritten < len(p) {
			return &RenderDestinationError{Cause: io.ErrShortWrite}
		}
	}
	return nil
}

// execToErrRecover handles panics from the evaluator during a streaming
// render. It mirrors errRecover but additionally converts the
// errBudgetOverflow sentinel into a *RenderBudgetExceededError so the
// sentinel never escapes to callers, and wraps writer errors that
// surfaced via errPanic into a *RenderDestinationError.
func execToErrRecover(errp *error, opts RenderOptions) {
	e := recover()
	if e == nil {
		return
	}
	switch err := e.(type) {
	case runtime.Error:
		panic(e)
	case error:
		if errors.Is(err, errBudgetOverflow) {
			*errp = &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
			return
		}
		*errp = err
	default:
		panic(e)
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
