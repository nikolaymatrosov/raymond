package raymond

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// adaptValue converts an arbitrary Go value into the closed model.
func adaptValue(v interface{}) Value {
	return adaptReflectValue(reflect.ValueOf(v))
}

// adaptReflectValue works on reflect.Value (not interface{}) so
// addressability survives field chains: pointer-receiver methods on
// nested struct fields stay reachable (evalMethod CanAddr→Addr,
// eval.go).
func adaptReflectValue(rv reflect.Value) Value {
	ind, _ := indirect(rv)
	if !ind.IsValid() {
		return Value{}
	}

	raw := ind.Interface()
	// truth of the indirected value — the old engine truth-tested
	// post-indirect results (eval.go:359, 852)
	truth, _ := isTrueValue(ind)

	switch ind.Kind() {
	case reflect.String:
		if ss, ok := raw.(SafeString); ok {
			return Value{kind: KindSafeString, truth: len(ss) > 0, str: string(ss), raw: raw}
		}
		if _, ok := raw.(fmt.Stringer); ok {
			// Stringer-typed strings keep legacy Str() promotion
			return opaqueValue(raw, truth)
		}
		return Value{kind: KindString, truth: truth, str: ind.String(), raw: raw}
	case reflect.Bool:
		return Value{kind: KindBool, truth: truth, b: ind.Bool(), raw: raw}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Value{kind: KindInt, truth: truth, i: ind.Int(), raw: raw}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return Value{kind: KindUint, truth: truth, u: ind.Uint(), raw: raw}
	case reflect.Float32, reflect.Float64:
		return Value{kind: KindFloat, truth: truth, f: ind.Float(), raw: raw}
	case reflect.Array, reflect.Slice:
		rd := &reflectData{rv: ind}
		return Value{kind: KindList, truth: truth, list: rd, data: rd, raw: raw, legacyStr: true}
	case reflect.Map, reflect.Struct:
		rd := &reflectData{rv: ind}
		return Value{kind: KindMap, truth: truth, data: rd, raw: raw, legacyStr: true}
	case reflect.Func:
		fv := funcValue(&legacyFunc{name: "", fn: ind}, false, raw)
		fv.legacyStr = true
		return fv
	default:
		return opaqueValue(raw, truth)
	}
}

func opaqueValue(raw interface{}, truth bool) Value {
	return Value{kind: KindOpaque, truth: truth, raw: raw, legacyStr: true}
}

// reflectData adapts any reflected container. Lookup ports
// evalField/evalMethod/evalStructTag (eval.go:319-422) WITHOUT calling
// funcs — they come back as KindFunc for the core to invoke.
type reflectData struct {
	rv reflect.Value // already indirected
}

func (rd *reflectData) Lookup(name string) (Value, bool) {
	ctx := rd.rv

	// method check first (eval.go:328-329)
	if m, ok := lookupMethod(ctx, name); ok {
		fv := funcValue(&legacyFunc{name: name, fn: m}, true, m.Interface())
		fv.legacyStr = true
		return fv, true
	}

	var result reflect.Value
	switch ctx.Kind() {
	case reflect.Struct:
		expName := strings.Title(name) //nolint:staticcheck
		if tField, ok := ctx.Type().FieldByName(expName); ok && (tField.PkgPath == "") {
			result = ctx.FieldByIndex(tField.Index)
		} else {
			result = lookupStructTag(ctx, name)
		}
	case reflect.Map:
		nameVal := reflect.ValueOf(name)
		if nameVal.Type().AssignableTo(ctx.Type().Key()) {
			result = ctx.MapIndex(nameVal)
		}
	case reflect.Array, reflect.Slice:
		if i, err := strconv.Atoi(name); (err == nil) && (i >= 0) && (i < ctx.Len()) {
			result = ctx.Index(i)
		}
	}

	if !result.IsValid() {
		return Value{}, false
	}

	// indirect + deferred func detection (eval.go:358-364)
	ind, _ := indirect(result)
	if ind.Kind() == reflect.Func {
		fv := funcValue(&legacyFunc{name: name, fn: ind}, false, ind.Interface())
		fv.legacyStr = true
		return fv, true
	}
	return adaptReflectValue(result), true
}

// lookupMethod ports evalMethod (eval.go:368-384) minus the call.
func lookupMethod(ctx reflect.Value, name string) (reflect.Value, bool) {
	if ctx.Kind() != reflect.Interface && ctx.CanAddr() {
		ctx = ctx.Addr()
	}
	// A type with no methods can't match — skip MethodByName and the
	// strings.Title fallback allocation entirely. This is the common case
	// for map/struct contexts (e.g. map[string]interface{}).
	if ctx.NumMethod() == 0 {
		return reflect.Value{}, false
	}
	method := ctx.MethodByName(name)
	if !method.IsValid() {
		method = ctx.MethodByName(strings.Title(name)) //nolint:staticcheck
	}
	if !method.IsValid() {
		return reflect.Value{}, false
	}
	return method, true
}

// lookupStructTag ports evalStructTag (eval.go:410-422).
func lookupStructTag(ctx reflect.Value, name string) reflect.Value {
	val := reflect.ValueOf(ctx.Interface())
	for i := 0; i < val.NumField(); i++ {
		field := val.Type().Field(i)
		if field.Tag.Get("handlebars") == name {
			return val.Field(i)
		}
	}
	return reflect.Value{}
}

// exportedFieldIndices lists exported struct fields in declaration
// order (eachHelper's struct iteration rule).
func (rd *reflectData) exportedFieldIndices() []int {
	var exported []int
	for i := 0; i < rd.rv.NumField(); i++ {
		if tField := rd.rv.Type().Field(i); tField.PkgPath == "" {
			exported = append(exported, i)
		}
	}
	return exported
}

// List over the same container. For structs Len is the exported field
// count, matching the legacy eachHelper's len(exportedFields).
func (rd *reflectData) Len() int {
	if rd.rv.Kind() == reflect.Struct {
		return len(rd.exportedFieldIndices())
	}
	return rd.rv.Len()
}

func (rd *reflectData) Index(i int) Value {
	if rd.rv.Kind() == reflect.Struct {
		fields := rd.exportedFieldIndices()
		if i < 0 || i >= len(fields) {
			return Value{}
		}
		return adaptReflectValue(rd.rv.Field(fields[i]))
	}
	return adaptReflectValue(rd.rv.Index(i))
}

// Each ports eachHelper's container branches (helper.go:331-374):
// slices key=nil, maps in MapKeys order, structs exported fields in
// declaration order with key = field name.
func (rd *reflectData) Each(fn func(i int, key interface{}, val Value) error) error {
	val := rd.rv
	switch val.Kind() {
	case reflect.Array, reflect.Slice:
		for i := 0; i < val.Len(); i++ {
			if err := fn(i, nil, adaptReflectValue(val.Index(i))); err != nil {
				return err
			}
		}
	case reflect.Map:
		keys := val.MapKeys()
		for i := 0; i < len(keys); i++ {
			if err := fn(i, keys[i].Interface(), adaptReflectValue(val.MapIndex(keys[i]))); err != nil {
				return err
			}
		}
	case reflect.Struct:
		exported := rd.exportedFieldIndices()
		for i, fieldIndex := range exported {
			key := val.Type().Field(fieldIndex).Name
			if err := fn(i, key, adaptReflectValue(val.Field(fieldIndex))); err != nil {
				return err
			}
		}
	}
	return nil
}

// legacyFunc wraps a reflected Go func (lambda/method/helper).
// Its call method lands in a later task.
type legacyFunc struct {
	name string
	fn   reflect.Value
}

func (l *legacyFunc) helperName() string { return l.name }

func (l *legacyFunc) call(s *state, opts *Options) (Value, error) {
	return callLegacyFunc(s, l.name, l.fn, opts)
}
