package raymond

import (
	"strconv"
	"strings"
)

// Kind discriminates the closed Value union the core engine operates on.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindString
	KindSafeString
	KindBool
	KindInt
	KindUint
	KindFloat
	KindList
	KindMap
	KindFunc
	KindOpaque
)

// callable is a function-shaped value (lambda, method, legacy helper)
// invocable by the core. Implemented in adapt_helpers.go.
type callable interface {
	helperName() string
	call(s *state, opts *Options) (Value, error)
}

// Data is the closed lookup interface the core resolves paths against.
// ok reports whether name resolved in this container (even if the value
// is nil/invalid) — it drives parent-context fallback (partResolved).
type Data interface {
	Lookup(name string) (Value, bool)
}

// List is an indexable sequence.
type List interface {
	Len() int
	Index(i int) Value
}

// Iterable supports #each iteration. key is nil for list-like
// containers (the builtin substitutes the index), the map key or
// struct field name otherwise.
type Iterable interface {
	Len() int
	Each(fn func(i int, key interface{}, val Value) error) error
}

// Value is a tagged union. raw always holds the original Go value so
// legacy helper params round-trip exactly (options.Param(0).(int)).
type Value struct {
	kind  Kind
	truth bool
	str   string
	i     int64
	u     uint64
	f     float64
	b     bool
	list  List
	data  Data
	fn    callable
	// fromMethod marks funcs found via method lookup: the old engine
	// re-invokes a method's func result once (eval.go:358-364), but a
	// plain field func only once total.
	fromMethod bool
	// strFn, when set by the adapter, computes Str() with full legacy
	// fidelity (Stringer/error promotion, panics on chan/func).
	strFn func() string
	raw   interface{}
}

func (v Value) Kind() Kind             { return v.kind }
func (v Value) IsValid() bool          { return v.kind != KindInvalid }
func (v Value) Truthy() bool           { return v.truth }
func (v Value) Interface() interface{} { return v.raw }

// Str mirrors strValue (string.go) kind-by-kind.
func (v Value) Str() string {
	switch v.kind {
	case KindInvalid:
		return ""
	case KindString, KindSafeString:
		return v.str
	case KindBool:
		if v.b {
			return "true"
		}
		return "false"
	case KindInt:
		return strconv.FormatInt(v.i, 10)
	case KindUint:
		return strconv.FormatUint(v.u, 10)
	case KindFloat:
		return strconv.FormatFloat(v.f, 'f', -1, 64)
	case KindList:
		var sb strings.Builder
		for i := 0; i < v.list.Len(); i++ {
			sb.WriteString(v.list.Index(i).Str())
		}
		return sb.String()
	default:
		if v.strFn != nil {
			return v.strFn()
		}
		return ""
	}
}

// Constructors used by the core and ExecuteData callers.

func stringValue(s string, safe bool) Value {
	k := KindString
	var raw interface{} = s
	if safe {
		k = KindSafeString
		raw = SafeString(s)
	}
	return Value{kind: k, truth: len(s) > 0, str: s, raw: raw}
}

func boolValue(b bool) Value {
	return Value{kind: KindBool, truth: b, b: b, raw: b}
}

func intValue(i int64, raw interface{}) Value {
	return Value{kind: KindInt, truth: i != 0, i: i, raw: raw}
}

func uintValue(u uint64, raw interface{}) Value {
	return Value{kind: KindUint, truth: u != 0, u: u, raw: raw}
}

func floatValue(f float64, raw interface{}) Value {
	return Value{kind: KindFloat, truth: f != 0, f: f, raw: raw}
}

func listValue(l List, truth bool, raw interface{}) Value {
	return Value{kind: KindList, truth: truth, list: l, raw: raw}
}

func mapValue(d Data, truth bool, raw interface{}) Value {
	return Value{kind: KindMap, truth: truth, data: d, raw: raw}
}

func funcValue(fn callable, fromMethod bool, raw interface{}) Value {
	return Value{kind: KindFunc, truth: true, fn: fn, fromMethod: fromMethod, raw: raw}
}

// valueMap backs synthetic contexts (block params, partial hash ctx).
type valueMap map[string]Value

func (m valueMap) Lookup(name string) (Value, bool) {
	v, ok := m[name]
	return v, ok
}

func (m valueMap) Len() int { return len(m) }

func (m valueMap) Each(fn func(i int, key interface{}, val Value) error) error {
	i := 0
	for k, v := range m {
		if err := fn(i, k, v); err != nil {
			return err
		}
		i++
	}
	return nil
}

// sliceList is a []Value-backed List for array-context path results.
type sliceList []Value

func (l sliceList) Len() int          { return len(l) }
func (l sliceList) Index(i int) Value { return l[i] }
