package raymond

import (
	"fmt"
	"reflect"
	"sync"
)

// Options represents the options argument provided to helpers and context functions.
type Options struct {
	// new-engine state and deferred error (record-and-continue for
	// Fn()'s missing error channel)
	s   *state
	err error

	// params
	params []any
	// hash is the interface-typed hash exposed to legacy helpers. It is
	// built lazily from hashV on the first Hash() call, so a helper that
	// never reads its hash pays nothing for the conversion.
	hash  map[string]any
	hashV map[string]Value
}

// helperEntry holds either a legacy reflected helper or a streaming one.
type helperEntry struct {
	legacy    reflect.Value
	streaming Helper
}

func (e helperEntry) valid() bool { return e.streaming != nil || e.legacy.IsValid() }

// helpers stores all globally registered helpers
var helpers = make(map[string]helperEntry)

// protects global helpers
var helpersMutex sync.RWMutex

func init() {
	// register builtin helpers
	mustRegisterHelper("if", HelperFunc(builtinIf))
	mustRegisterHelper("unless", HelperFunc(builtinUnless))
	mustRegisterHelper("with", HelperFunc(builtinWith))
	mustRegisterHelper("each", HelperFunc(builtinEach))
	mustRegisterHelper("log", HelperFunc(builtinLog))
	mustRegisterHelper("lookup", HelperFunc(builtinLookup))
	mustRegisterHelper("equal", HelperFunc(builtinEqual))
}

// mustRegisterHelper registers a builtin helper, panicking on error. Used
// only for our own constant builtins, where a failure is a programming bug
// (analogous to regexp.MustCompile of a literal).
func mustRegisterHelper(name string, helper any) {
	if err := RegisterHelper(name, helper); err != nil {
		panic(err)
	}
}

// RegisterHelper registers a global helper. That helper will be available to all templates.
//
// helper may be a classic Go function (invoked through reflection, with
// optional trailing *Options parameter) or a streaming Helper /
// func(*HelperCall) error.
//
// Returns an error if a helper with the same name is already registered or
// the helper is invalid.
func RegisterHelper(name string, helper any) error {
	helpersMutex.Lock()
	defer helpersMutex.Unlock()

	if helpers[name].valid() {
		return fmt.Errorf("Helper already registered: %s", name)
	}

	switch h := helper.(type) {
	case Helper:
		helpers[name] = helperEntry{streaming: h}
	case func(*HelperCall) error:
		helpers[name] = helperEntry{streaming: HelperFunc(h)}
	default:
		val := reflect.ValueOf(helper)
		if err := ensureValidHelper(name, val); err != nil {
			return err
		}
		helpers[name] = helperEntry{legacy: val}
	}
	return nil
}

// RegisterHelpers registers several global helpers. Those helpers will be available to all templates.
// Returns the first error encountered, if any.
func RegisterHelpers(helpers map[string]any) error {
	for name, helper := range helpers {
		if err := RegisterHelper(name, helper); err != nil {
			return err
		}
	}
	return nil
}

// RemoveHelper unregisters a global helper
func RemoveHelper(name string) {
	helpersMutex.Lock()
	defer helpersMutex.Unlock()

	delete(helpers, name)
}

// RemoveAllHelpers unregisters all global helpers
func RemoveAllHelpers() {
	helpersMutex.Lock()
	defer helpersMutex.Unlock()

	helpers = make(map[string]helperEntry)
}

// ensureValidHelper returns an error if given helper is not valid.
func ensureValidHelper(name string, funcValue reflect.Value) error {
	if funcValue.Kind() != reflect.Func {
		return fmt.Errorf("Helper must be a function: %s", name)
	}

	if funcValue.Type().NumOut() != 1 {
		return fmt.Errorf("Helper function must return a string or a SafeString: %s", name)
	}

	// @todo Check if first returned value is a string, SafeString or interface{} ?
	return nil
}

// findHelper finds a globally registered helper
func findHelper(name string) helperEntry {
	helpersMutex.RLock()
	defer helpersMutex.RUnlock()

	return helpers[name]
}

//
// Context Values
//

// Value returns field value from current context.
func (options *Options) Value(name string) any {
	v, err := options.s.lookupField(options.s.curCtx(), name, false)
	if err != nil {
		options.err = err
		return nil
	}
	return v.Interface()
}

// ValueStr returns string representation of field value from current context.
func (options *Options) ValueStr(name string) string {
	return Str(options.Value(name))
}

// Ctx returns current evaluation context.
func (options *Options) Ctx() any {
	return options.s.curCtx().Interface()
}

//
// Hash Arguments
//

// HashProp returns hash property.
func (options *Options) HashProp(name string) any {
	return options.Hash()[name]
}

// HashStr returns string representation of hash property.
func (options *Options) HashStr(name string) string {
	return Str(options.Hash()[name])
}

// Hash returns entire hash. The interface-typed map is built lazily from
// hashV on first access so helpers that ignore their hash pay nothing.
func (options *Options) Hash() map[string]any {
	if options.hash == nil && options.hashV != nil {
		options.hash = rawHash(options.hashV)
	}
	return options.hash
}

//
// Parameters
//

// Param returns parameter at given position.
func (options *Options) Param(pos int) any {
	if len(options.params) > pos {
		return options.params[pos]
	}

	return nil
}

// ParamStr returns string representation of parameter at given position.
func (options *Options) ParamStr(pos int) string {
	return Str(options.Param(pos))
}

// Params returns all parameters.
func (options *Options) Params() []any {
	return options.params
}

//
// Private data
//

// Data returns private data value.
func (options *Options) Data(name string) any {
	return options.s.frame.Get(name)
}

// DataStr returns string representation of private data value.
func (options *Options) DataStr(name string) string {
	return Str(options.Data(name))
}

// DataFrame returns current private data frame.
func (options *Options) DataFrame() *DataFrame {
	return options.s.frame
}

// NewDataFrame instanciates a new data frame that is a copy of current evaluation data frame.
//
// Parent of returned data frame is set to current evaluation data frame.
func (options *Options) NewDataFrame() *DataFrame {
	return options.s.frame.Copy()
}

//
// Evaluation
//

// evalBlock evaluates block with given context, private data and iteration key
func (options *Options) evalBlock(ctx any, data *DataFrame, key any) string {
	if options.err != nil {
		return ""
	}
	if block := options.s.curBlock(); (block != nil) && (block.Program != nil) {
		out, err := options.s.capture(func() error {
			return options.s.renderProgramWith(block.Program, adaptValue(ctx), data, key)
		})
		if err != nil {
			options.err = err
			return ""
		}
		return out
	}
	return ""
}

// Fn evaluates block with current evaluation context.
func (options *Options) Fn() string {
	return options.evalBlock(nil, nil, nil)
}

// FnCtxData evaluates block with given context and private data frame.
func (options *Options) FnCtxData(ctx any, data *DataFrame) string {
	return options.evalBlock(ctx, data, nil)
}

// FnWith evaluates block with given context.
func (options *Options) FnWith(ctx any) string {
	return options.evalBlock(ctx, nil, nil)
}

// FnData evaluates block with given private data frame.
func (options *Options) FnData(data *DataFrame) string {
	return options.evalBlock(nil, data, nil)
}

// Inverse evaluates "else block".
func (options *Options) Inverse() string {
	if options.err != nil {
		return ""
	}
	if block := options.s.curBlock(); (block != nil) && (block.Inverse != nil) {
		out, err := options.s.capture(func() error {
			return options.s.renderProgram(block.Inverse)
		})
		if err != nil {
			options.err = err
			return ""
		}
		return out
	}
	return ""
}

// Eval evaluates field for given context.
func (options *Options) Eval(ctx any, field string) any {
	if ctx == nil {
		return nil
	}

	if field == "" {
		return nil
	}

	v, err := options.s.lookupField(adaptValue(ctx), field, false)
	if err != nil {
		options.err = err
		return nil
	}
	return v.Interface()
}
