package raymond

import (
	"fmt"
	"reflect"
)

// callLegacyFunc invokes a reflected Go function (legacy helper,
// lambda, method) with raymond's argument conventions. Port of
// evalVisitor.callFunc (eval.go:594-658) with errors instead of
// panics; message strings are byte-identical.
func callLegacyFunc(s *state, name string, funcVal reflect.Value, opts *Options) (Value, error) {
	if err := ensureValidHelperErr(name, funcVal); err != nil {
		return Value{}, s.errorf("%s", err.Error())
	}

	params := opts.Params()
	funcType := funcVal.Type()

	strType := reflect.TypeOf("")
	boolType := reflect.TypeOf(true)

	addOptions := false
	numIn := funcType.NumIn()

	if numIn == len(params)+1 {
		lastArgType := funcType.In(numIn - 1)
		if reflect.TypeOf(opts).AssignableTo(lastArgType) {
			addOptions = true
		}
	}

	if !addOptions && (len(params) != numIn) {
		return Value{}, s.errorf("Helper '%s' called with wrong number of arguments, needed %d but got %d", name, numIn, len(params))
	}

	args := make([]reflect.Value, numIn)
	for i, param := range params {
		arg := reflect.ValueOf(param)
		argType := funcType.In(i)

		if !arg.IsValid() {
			if canBeNil(argType) {
				arg = reflect.Zero(argType)
			} else if argType.Kind() == reflect.String {
				arg = reflect.ValueOf("")
			} else {
				// callFunc returns reflect.Zero(strType) here: empty string
				return stringValue("", false), nil
			}
		}

		if !arg.Type().AssignableTo(argType) {
			if strType.AssignableTo(argType) {
				arg = reflect.ValueOf(strValue(arg))
			} else if boolType.AssignableTo(argType) {
				val, _ := isTrueValue(arg)
				arg = reflect.ValueOf(val)
			} else {
				return Value{}, s.errorf("Helper %s called with argument %d with type %s but it should be %s", name, i, arg.Type(), argType)
			}
		}

		args[i] = arg
	}

	if addOptions {
		args[numIn-1] = reflect.ValueOf(opts)
	}

	result := funcVal.Call(args)

	// a failure recorded by Options.Fn/Inverse during the call wins
	if opts.err != nil {
		return Value{}, opts.err
	}

	out := result[0]
	if !out.IsValid() {
		return Value{}, nil
	}
	return adaptReflectValue(out), nil
}

// legacyHelper adapts a reflected legacy helper func to coreHelper.
type legacyHelper struct {
	name string
	fn   reflect.Value
}

func (lh *legacyHelper) callCore(hc *HelperCall) (Value, error) {
	opts := &Options{
		s:      hc.s,
		params: rawParams(hc.params),
		hash:   rawHash(hc.hash),
	}
	return callLegacyFunc(hc.s, lh.name, lh.fn, opts)
}

func rawParams(params []Value) []interface{} {
	if params == nil {
		return nil
	}
	out := make([]interface{}, len(params))
	for i, p := range params {
		out[i] = p.Interface()
	}
	return out
}

func rawHash(hash map[string]Value) map[string]interface{} {
	if len(hash) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(hash))
	for k, v := range hash {
		out[k] = v.Interface()
	}
	return out
}

// ensureValidHelperErr is ensureValidHelper (helper.go:76-88) as an
// error for exec-time lambda validation (evalFieldFunc runs it before
// building options, eval.go:388); registration keeps the panic.
func ensureValidHelperErr(name string, funcValue reflect.Value) error {
	if funcValue.Kind() != reflect.Func {
		return fmt.Errorf("Helper must be a function: %s", name)
	}
	if funcValue.Type().NumOut() != 1 {
		return fmt.Errorf("Helper function must return a string or a SafeString: %s", name)
	}
	return nil
}
