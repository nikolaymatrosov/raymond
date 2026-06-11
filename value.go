package raymond

import (
	"math"
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
	b     bool
	// fromMethod marks funcs found via method lookup: the old engine
	// re-invokes a method's func result once (eval.go:358-364), but a
	// plain field func only once total.
	fromMethod bool
	// legacyStr marks values whose Str() must run through the package
	// Str(raw) for full legacy fidelity (Stringer/error promotion,
	// panics on chan/func). raw already holds the value, so no closure
	// is needed — the flag alone avoids a per-value allocation.
	legacyStr bool
	str       string
	// num holds the numeric payload for KindInt/KindUint/KindFloat: an
	// int64 reinterpreted via uint64(i)/int64(num), a uint64 directly, or
	// a float64 via math.Float64bits/Float64frombits. Unioning the three
	// 8-byte numeric fields into one shrinks Value (it sits in every
	// params slice, hash bucket and ctx stack).
	num  uint64
	list List
	data Data
	fn   callable
	raw  interface{}
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
		return strconv.FormatInt(int64(v.num), 10)
	case KindUint:
		return strconv.FormatUint(v.num, 10)
	case KindFloat:
		return strconv.FormatFloat(math.Float64frombits(v.num), 'f', -1, 64)
	case KindList:
		// adapted lists carry strFn so stringification runs through
		// strValue's own recursion (legacy promotion rules); synthetic
		// sliceList values concatenate element Strs (eval.go:540-556
		// array-context results were stringified element-wise too)
		if v.legacyStr {
			return Str(v.raw)
		}
		var sb strings.Builder
		for i := 0; i < v.list.Len(); i++ {
			sb.WriteString(v.list.Index(i).Str())
		}
		return sb.String()
	default:
		if v.legacyStr {
			return Str(v.raw)
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
	return Value{kind: KindInt, truth: i != 0, num: uint64(i), raw: raw}
}

func uintValue(u uint64, raw interface{}) Value {
	return Value{kind: KindUint, truth: u != 0, num: u, raw: raw}
}

func floatValue(f float64, raw interface{}) Value {
	return Value{kind: KindFloat, truth: f != 0, num: math.Float64bits(f), raw: raw}
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
