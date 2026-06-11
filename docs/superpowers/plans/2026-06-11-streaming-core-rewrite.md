# Streaming Core Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace raymond's reflection/panic/string-concat evaluator (`eval.go`) with a streaming, error-returning, budgeted core over a closed `Value`/`Data` model, while every existing test passes unchanged.

**Architecture:** New `Compile(src, Limits)` / `Execute(ctx, w, data)` engine with a type-switch walker writing into a writer chain; reflection quarantined into adapter files; all legacy entry points become shims. Built alongside the old engine, flipped one entry-point family at a time, old engine deleted last.

**Tech Stack:** Go stdlib only. Spec: `docs/superpowers/specs/2026-06-11-streaming-core-rewrite-design.md`.

**Spec amendment (decided during planning):** `Data.Lookup(name string) (Value, bool)` resolves a *single* field name, not a `path []string`. Reason: the old engine invokes lambdas *mid-path* (`evalField` eval.go:358-364 calls a func found at any path part, with the exec state's params/options), so path walking must live in the core where the state is; a full-path `Lookup` cannot express call-a-lambda-then-continue. The `ok` bit means "this name resolved in this container" and feeds the core's `partResolved` logic.

**Porting convention used in this plan:** Steps that say "port `eval.go:X-Y`" mean: open that exact range in git history (`git show 832d99d:eval.go` once eval.go is deleted; it is present in the working tree until Task 12) and transcribe the logic with these mechanical transformations — `reflect.Value` → `Value`, `interface{}` results → `Value`, `v.errorf(...)`/`v.errPanic(...)` → `return s.errorf(...)` (an `error`), `panic(errBudgetOverflow)` → never (writer returns it), `Accept(v)` dispatch → direct method call. Error message strings are copied byte-for-byte. These citations are normative parts of this plan, not placeholders: the referenced code is in the repository.

**Verification gate after EVERY task:** `go build ./... && go test ./...` — the 66-function suite must be green before committing.

---

## File structure

| File | Status | Responsibility |
|---|---|---|
| `parser/limits.go`, `parser/limits_test.go` | done (Task 1 commits) | in-parser MaxNodes/MaxDepth |
| `limits.go` | new | `Limits`, sentinels, `LimitError` |
| `value.go` | new | `Kind`, `Value`, `Data`, `List`, `Iterable`, `valueMap` — **no reflect import** |
| `adapt.go` | new | `adaptValue`/`adaptReflectValue`, `reflectData`, `reflectList` (reflection quarantine) |
| `adapt_helpers.go` | new | `legacyFunc` callable, `callLegacyFunc` (port of `callFunc`), legacy-helper bridge |
| `exec_state.go` | new | `state`, fuel/ctx checks, `capture`, `destWriter`, `indentWriter`, `escapeWriter` |
| `render.go` | new | type-switch walker: statements, expressions, paths, partials |
| `builtins.go` | new | streaming `if/unless/with/each/log/lookup/equal` |
| `helper_call.go` | new | public `Helper`, `HelperFunc`, `HelperCall` |
| `compile.go` | new | `Compile`, `Compiled`, `Execute`/`ExecuteData` |
| `helper.go` | modified | `Options` over `state`; registry dual dispatch; legacy builtin bodies removed |
| `template.go` | modified | all `Exec*` become shims over `tpl.execute`; `errRecover` family deleted |
| `capped_writer.go` | modified | `errBudgetOverflow` wraps `ErrOutputLimit`; add `WriteString` |
| `eval.go` | **deleted** (Task 12) | old engine |
| `compile_test.go`, `value_test.go`, `adapt_parity_test.go`, `parity_test.go`, `quarantine_test.go`, `exec_state_test.go` | new | new-API + parity + quarantine tests |

Existing test files are NEVER edited.

---

### Task 1: Commit M0 (parser limits — already implemented in working tree)

**Files:** `parser/limits.go`, `parser/limits_test.go`, `parser/parser.go` (already modified)

- [ ] **Step 1:** Run full suite: `go test ./...` → all `ok`.
- [ ] **Step 2:** Commit:

```bash
git add parser/limits.go parser/limits_test.go parser/parser.go
git commit -m "feat(parser): in-parser MaxNodes/MaxDepth limits via ParseWithLimits"
```

---

### Task 2: Root `limits.go` — Limits struct and sentinel errors

**Files:** Create `limits.go`, `limits_test.go`

- [ ] **Step 1: Write failing test** `limits_test.go`:

```go
package raymond

import (
    "errors"
    "testing"
)

func TestLimitError_Sentinels(t *testing.T) {
    cases := []struct {
        kind     string
        sentinel error
    }{
        {"output bytes", ErrOutputLimit},
        {"substitutions", ErrSubstitutionLimit},
        {"steps", ErrStepLimit},
        {"nodes", ErrTemplateTooComplex},
        {"depth", ErrTemplateTooComplex},
        {"source size", ErrTemplateTooLarge},
    }
    for _, c := range cases {
        err := newLimitError(c.kind, 42, c.sentinel)
        if !errors.Is(err, c.sentinel) {
            t.Errorf("kind %q: errors.Is sentinel = false", c.kind)
        }
        var le *LimitError
        if !errors.As(err, &le) || le.Kind != c.kind || le.Limit != 42 {
            t.Errorf("kind %q: As/fields failed: %v", c.kind, err)
        }
    }
}
```

- [ ] **Step 2:** `go test -run TestLimitError ./...` → FAIL (undefined symbols).
- [ ] **Step 3: Implement** `limits.go`:

```go
package raymond

import (
    "errors"
    "fmt"
)

// Limits bounds what a single Compile or Execute call may consume.
// The zero value means unlimited on every axis.
type Limits struct {
    // Parse-time, consumed by Compile.
    MaxTemplateSize int // bytes of source, checked before lexing
    MaxNodes        int // AST nodes, enforced inside the parser
    MaxDepth        int // nesting depth (programs + subexpressions)

    // Execution-time, consumed by Execute.
    MaxOutputBytes   int64 // bytes delivered to the destination writer
    MaxSubstitutions int64 // mustache substitutions rendered
    MaxSteps         int64 // CPU fuel
}

var (
    ErrTemplateTooLarge   = errors.New("raymond: template source exceeds size limit")
    ErrTemplateTooComplex = errors.New("raymond: template exceeds structural limit")
    ErrOutputLimit        = errors.New("raymond: output byte limit exceeded")
    ErrSubstitutionLimit  = errors.New("raymond: substitution limit exceeded")
    ErrStepLimit          = errors.New("raymond: step limit exceeded")
)

// LimitError reports which limit was breached; Unwrap yields the sentinel.
type LimitError struct {
    Kind     string
    Limit    int64
    sentinel error
}

func newLimitError(kind string, limit int64, sentinel error) *LimitError {
    return &LimitError{Kind: kind, Limit: limit, sentinel: sentinel}
}

func (e *LimitError) Error() string {
    return fmt.Sprintf("raymond: %s limit exceeded (limit %d)", e.Kind, e.Limit)
}

func (e *LimitError) Unwrap() error { return e.sentinel }
```

- [ ] **Step 4:** `go test -run TestLimitError ./...` → PASS. Full gate: `go test ./...` → green.
- [ ] **Step 5:** `git add limits.go limits_test.go && git commit -m "feat: Limits struct and sentinel errors for the new engine"`

---

### Task 3: `value.go` — closed Value model (zero reflection)

**Files:** Create `value.go`, `value_test.go`

- [ ] **Step 1: Write failing test** `value_test.go`:

```go
package raymond

import "testing"

func TestValue_ScalarStrAndTruth(t *testing.T) {
    cases := []struct {
        v     Value
        str   string
        truth bool
    }{
        {stringValue("hi", false), "hi", true},
        {stringValue("", false), "", false},
        {stringValue("<b>", true), "<b>", true}, // SafeString
        {boolValue(true), "true", true},
        {boolValue(false), "false", false},
        {intValue(-3, int(-3)), "-3", true},
        {intValue(0, int(0)), "0", false},
        {uintValue(7, uint8(7)), "7", true},
        {floatValue(3.5, 3.5), "3.5", true},
        {floatValue(0, 0.0), "0", false},
        {Value{}, "", false}, // invalid
    }
    for i, c := range cases {
        if got := c.v.Str(); got != c.str {
            t.Errorf("case %d: Str() = %q, want %q", i, got, c.str)
        }
        if got := c.v.Truthy(); got != c.truth {
            t.Errorf("case %d: Truthy() = %v, want %v", i, got, c.truth)
        }
    }
}

func TestValueMap_Lookup(t *testing.T) {
    m := valueMap{"a": intValue(1, 1)}
    v, ok := m.Lookup("a")
    if !ok || v.Str() != "1" {
        t.Errorf("Lookup(a) = %v,%v", v, ok)
    }
    if _, ok := m.Lookup("b"); ok {
        t.Error("Lookup(b) should not resolve")
    }
}

func TestValue_InterfaceRoundTrip(t *testing.T) {
    v := intValue(5, int(5))
    if n, ok := v.Interface().(int); !ok || n != 5 {
        t.Errorf("Interface() = %#v, want int 5", v.Interface())
    }
}
```

- [ ] **Step 2:** `go test -run 'TestValue' .` → FAIL (undefined).
- [ ] **Step 3: Implement** `value.go` (note: **no `reflect` import allowed** — Task 13's quarantine test enforces it):

```go
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
// legacy helper params round-trip exactly ({{options.Param(0).(int)}}).
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

func (v Value) Kind() Kind       { return v.kind }
func (v Value) IsValid() bool    { return v.kind != KindInvalid }
func (v Value) Truthy() bool     { return v.truth }
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
```

Note: `callable.call` references `*state` and `*Options`, which don't exist yet — to keep the package compiling at this milestone, ALSO add to `value.go` a temporary forward declaration nothing else uses yet? **No.** Instead: until Task 6 lands, declare `callable` with `call(s *state, opts *Options)` only when `state` exists. For THIS task, define `callable` as:

```go
type callable interface {
    helperName() string
}
```

and extend it to the full two-method form in Task 6 (it is package-private; no external impact).

- [ ] **Step 4:** `go test -run 'TestValue' .` → PASS. Gate: `go test ./...` → green.
- [ ] **Step 5:** `git add value.go value_test.go && git commit -m "feat: closed Value/Data model for the streaming core"`

---

### Task 4: `adapt.go` — reflection adapter

**Files:** Create `adapt.go`, `adapt_parity_test.go`

This file owns ALL data-shape reflection. It ports `evalField`/`evalMethod`/`evalStructTag` (eval.go:319-422) *minus invocation*: funcs come back as `KindFunc` Values tagged `fromMethod`; the core invokes them (methods get one re-invocation if their result is again a func, plain field funcs do not — eval.go:358-364 vs 368-384).

- [ ] **Step 1: Write failing parity test** `adapt_parity_test.go` with: (a) `TestAdapt_StrAndTruthParity` — corpus of `nil, "", "x", SafeString("<b>"), true, false, 0, 5, -5, int8(3), uint8(7), uint64(9), float32(3.14), 3.14, 0.0, []string{"a","b"}, []interface{}{1,"x"}, []int{}, map[string]interface{}{"a":1}, map[string]string{}, struct values, struct pointers` asserting `adaptValue(x).Str() == Str(x)` and `adaptValue(x).Truthy() == IsTrue(x)` for every element; (b) `TestAdapt_StructLookup` — a struct with exported field `Title`, tag `handlebars:"alias"`, pointer-receiver method `Subject()`: assert `Lookup("title")` resolves via `strings.Title` promotion, `Lookup("alias")` resolves via tag, `Lookup("subject")` returns `KindFunc` with `fromMethod=true`, `Lookup("nope")` returns `ok=false`; (c) `TestAdapt_MapAndSliceLookup` — map key lookup, slice `Kind()==KindList` with `Len/Index`, and numeric-name lookup `Lookup("1")` on a slice (evalField's Array branch).
- [ ] **Step 2:** `go test -run TestAdapt .` → FAIL.
- [ ] **Step 3: Implement** `adapt.go`:

```go
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
// eval.go:368-371).
func adaptReflectValue(rv reflect.Value) Value {
    ind, _ := indirect(rv)
    if !ind.IsValid() {
        return Value{}
    }

    raw := ind.Interface()
    truth, _ := isTrueValue(rv) // truth of the ORIGINAL, like VisitBlock

    switch ind.Kind() {
    case reflect.String:
        if ss, ok := raw.(SafeString); ok {
            return Value{kind: KindSafeString, truth: len(ss) > 0, str: string(ss), raw: raw}
        }
        if _, ok := raw.(fmt.Stringer); ok {
            // Stringer-typed strings keep legacy Str() promotion
            return opaqueValue(ind, raw, truth)
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
        return Value{kind: KindList, truth: truth, list: rd, data: rd, raw: raw, strFn: legacyStrFn(ind)}
    case reflect.Map, reflect.Struct:
        rd := &reflectData{rv: ind}
        return Value{kind: KindMap, truth: truth, data: rd, raw: raw, strFn: legacyStrFn(ind)}
    case reflect.Func:
        return funcValue(&legacyFunc{name: "", fn: ind}, false, raw)
    default:
        return opaqueValue(ind, raw, truth)
    }
}

func opaqueValue(ind reflect.Value, raw interface{}, truth bool) Value {
    return Value{kind: KindOpaque, truth: truth, raw: raw, strFn: legacyStrFn(ind)}
}

// legacyStrFn defers to strValue for full fidelity (Stringer/error
// promotion, panic on unprintables) on non-scalar kinds.
func legacyStrFn(rv reflect.Value) func() string {
    return func() string { return strValue(rv) }
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
        return funcValue(&legacyFunc{name: name, fn: m}, true, m.Interface()), true
    }

    var result reflect.Value
    switch ctx.Kind() {
    case reflect.Struct:
        expName := strings.Title(name)
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
        if i, err := strconv.Atoi(name); (err == nil) && (i < ctx.Len()) {
            result = ctx.Index(i)
        }
    }

    if !result.IsValid() {
        return Value{}, false
    }

    // indirect + deferred func detection (eval.go:358-364)
    ind, _ := indirect(result)
    if ind.Kind() == reflect.Func {
        return funcValue(&legacyFunc{name: name, fn: ind}, false, ind.Interface()), true
    }
    return adaptReflectValue(result), true
}

// lookupMethod ports evalMethod (eval.go:368-384) minus the call.
func lookupMethod(ctx reflect.Value, name string) (reflect.Value, bool) {
    if ctx.Kind() != reflect.Interface && ctx.CanAddr() {
        ctx = ctx.Addr()
    }
    method := ctx.MethodByName(name)
    if !method.IsValid() {
        method = ctx.MethodByName(strings.Title(name))
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

// List over the same container.
func (rd *reflectData) Len() int          { return rd.rv.Len() }
func (rd *reflectData) Index(i int) Value { return adaptReflectValue(rd.rv.Index(i)) }

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
        var exported []int
        for i := 0; i < val.NumField(); i++ {
            if tField := val.Type().Field(i); tField.PkgPath == "" {
                exported = append(exported, i)
            }
        }
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
// call body lands in Task 6.
type legacyFunc struct {
    name string
    fn   reflect.Value
}

func (l *legacyFunc) helperName() string { return l.name }
```

- [ ] **Step 4:** `go test -run TestAdapt .` → PASS. Gate: `go test ./...` green.
- [ ] **Step 5:** `git add adapt.go adapt_parity_test.go && git commit -m "feat: reflection adapter mapping Go values onto the closed model"`

---

### Task 5: `exec_state.go` — state, fuel, capture, writer chain

**Files:** Create `exec_state.go`, `exec_state_test.go`. Extend `callable` in `value.go`.

- [ ] **Step 1: Write failing tests** `exec_state_test.go`:
  - `TestIndentWriter_MatchesIndentLines` — for each input in `{"", "a", "a\n", "a\nb", "a\nb\n", "a\n\nb\n", "\n", "\n\n"}`, stream through `newIndentWriter(&buf, "  ")` via `io.WriteString` and assert output equals `indentLines(in, "  ")` (the old helper, still present until Task 12).
  - `TestIndentWriter_SplitWrites` — write `"a\n"` then `"b"` separately; expect `"_a\n_b"` with indent `"_"`.
  - `TestDestWriter_TagsErrors` — wrap a failing writer (`errWriter` from capped_writer_test.go is reusable in-package); assert the error `errors.As`-matches `*destError` and its cause `errors.Is` the original.
  - `TestStateStep_FuelAndCtx` — `Limits{MaxSteps: 10}`: 10 steps ok, 11th returns `errors.Is(_, ErrStepLimit)`; pre-canceled ctx returns `context.Canceled` within `ctxCheckInterval+1` steps.
  - `TestCapture_BoundedByRemainingBudget` — state with `cap = newCappedWriter(&sink, 5)`; `capture` of a 100-byte write returns `errors.Is(_, errBudgetOverflow)` and sink stays empty.
- [ ] **Step 2:** Run them → FAIL (undefined).
- [ ] **Step 3: Implement** `exec_state.go`:

```go
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

// state carries all mutable data of one render.
type state struct {
    tctx context.Context

    w   io.Writer     // current sink; swapped around captures/partials
    cap *cappedWriter // outermost output cap, nil when unbounded

    limits       Limits
    steps        int64
    subs         int64
    nextCtxCheck int64

    // helpers/partials resolver seams are ADDED IN TASK 7 (they
    // reference coreHelper, which needs HelperCall):
    //   helpers  func(name string) coreHelper
    //   partials func(name string) (*ast.Program, error)

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
```

`coreHelper` and the `helpers`/`partials` seam fields are NOT declared in this task — they reference `HelperCall` and land in Task 7. The `state` struct above compiles standalone.

- [ ] **Step 4:** Tests → PASS. Gate: `go test ./...` green.
- [ ] **Step 5:** Extend `callable` in `value.go` to the two-method form `helperName() string; call(s *state, opts *Options) (Value, error)` and add to `adapt.go` a temporary `legacyFunc.call` returning `Value{}, s.errorf("legacy bridge not wired")` (replaced in Task 6).
- [ ] **Step 6:** `git add exec_state.go exec_state_test.go value.go adapt.go && git commit -m "feat: per-execution state, fuel, capture, and writer chain"`

---

### Task 6: `adapt_helpers.go` — legacy func invocation bridge

**Files:** Create `adapt_helpers.go`; replace the Task 5 stub body of `legacyFunc.call`; modify `helper.go` (struct only).

To avoid migrating `Options` twice, the new-engine fields are ADDED to the existing struct now; method bodies still use the old engine until Task 9:

- [ ] **Step 1:** In `helper.go`, extend the struct:

```go
type Options struct {
    // evaluation visitor (old engine; removed with eval.go in Task 12)
    eval *evalVisitor

    // new-engine state and deferred error (record-and-continue for
    // Fn()'s missing error channel); nil/zero while the old engine runs
    s   *state
    err error

    // params
    params []interface{}
    hash   map[string]interface{}
}
```

- [ ] **Step 2: Implement** `adapt_helpers.go` — port of `callFunc` (eval.go:594-658), errors instead of panics, byte-identical messages:

```go
package raymond

import (
    "fmt"
    "reflect"
)

// callLegacyFunc invokes a reflected Go function (legacy helper,
// lambda, method) with raymond's argument conventions. Port of
// evalVisitor.callFunc (eval.go:594-658).
func callLegacyFunc(s *state, name string, funcVal reflect.Value, opts *Options) (Value, error) {
    if err := ensureValidHelperErr(name, funcVal); err != nil {
        return Value{}, s.errorf("%s", err)
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
```

Replace the `legacyFunc.call` stub in `adapt.go`:

```go
func (l *legacyFunc) call(s *state, opts *Options) (Value, error) {
    return callLegacyFunc(s, l.name, l.fn, opts)
}
```

- [ ] **Step 3:** Gate: `go build ./... && go test ./...` → green (bridge dormant; old suite untouched).
- [ ] **Step 4:** `git add adapt_helpers.go adapt.go helper.go && git commit -m "feat: legacy helper invocation bridge with error returns"`

---

### Task 7: `render.go` + `helper_call.go` — the walker and helper dispatch

**Files:** Create `render.go`, `helper_call.go`. No test file of its own — Task 8's parity harness is the oracle; the package must compile and the old suite stay green.

**Call-position rule** (derived from VisitMustache/VisitBlock/VisitSubExpression): a helper's streamed bytes go through an `escapeWriter` in escaped-mustache position, raw `s.w` in block/raw-mustache position, and a capture buffer in subexpression/param/hash/partial-name position (the captured string becomes the expression's Value). Legacy helpers never stream — they return Values.

- [ ] **Step 1: Implement** `helper_call.go`:

```go
package raymond

import (
    "context"
    "io"
)

// Helper is the streaming helper interface for the new engine.
type Helper interface {
    CallHelper(hc *HelperCall) error
}

// HelperFunc adapts a function to Helper.
type HelperFunc func(hc *HelperCall) error

func (f HelperFunc) CallHelper(hc *HelperCall) error { return f(hc) }

// HelperCall is the invocation context passed to streaming helpers.
type HelperCall struct {
    s      *state
    name   string
    expr   *ast.Expression
    params []Value
    hash   map[string]Value
    w      io.Writer // position-appropriate sink
    rawW   io.Writer // position sink WITHOUT escaping (for WriteSafe)
}

func (hc *HelperCall) Context() context.Context { return hc.s.tctx }
func (hc *HelperCall) Name() string             { return hc.name }
func (hc *HelperCall) NumParams() int           { return len(hc.params) }

func (hc *HelperCall) Param(i int) Value {
    if i < len(hc.params) {
        return hc.params[i]
    }
    return Value{}
}

func (hc *HelperCall) Hash(name string) Value { return hc.hash[name] }
func (hc *HelperCall) Ctx() Value             { return hc.s.curCtx() }

// Lookup resolves a field on the current context (Options.Value parity:
// single field, exprRoot=false).
func (hc *HelperCall) Lookup(name string) (Value, error) {
    return hc.s.lookupField(hc.s.curCtx(), name, false)
}

// Data resolves a private-data key on the current frame.
func (hc *HelperCall) Data(name string) Value {
    return adaptValue(hc.s.frame.Get(name))
}

func (hc *HelperCall) DataFrame() *DataFrame    { return hc.s.frame }
func (hc *HelperCall) NewDataFrame() *DataFrame { return hc.s.frame.Copy() }

// Write streams helper output; escaped in escaped-mustache position,
// charged against output budget and fuel.
func (hc *HelperCall) Write(p []byte) (int, error) {
    if err := hc.s.writeSteps(len(p)); err != nil {
        return 0, err
    }
    return hc.w.Write(p)
}

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

// fnWithKey renders the current block's program into hc's sink
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

// --- coreHelper wrappers ---

// position of a helper call decides where streamed bytes land.
type callPosition uint8

const (
    posSubExpr  callPosition = iota // capture; captured string becomes the Value
    posMustache                     // escaped write-through
    posRawMustache                  // unescaped write-through
    posBlock                        // unescaped write-through
)

// streamingHelper adapts Helper to coreHelper.
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

```

Add a `pos callPosition` field to `HelperCall` and import `"github.com/aymerick/raymond/ast"`. **In `adapt_helpers.go`** (NOT here — `helper_call.go` must stay reflection-free) add the legacy bridge:

```go
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
    out := make(map[string]interface{})
    for k, v := range hash {
        out[k] = v.Interface()
    }
    return out
}
```

Also add to `exec_state.go` now (deferred from Task 5): the `coreHelper` interface and the seam fields on `state`:

```go
// coreHelper is the engine-internal helper shape; legacy and streaming
// helpers both wrap into it.
type coreHelper interface {
    callCore(hc *HelperCall) (Value, error)
}
```

plus `helpers func(name string) coreHelper` and `partials func(name string) (*ast.Program, error)` fields on `state`.

- [ ] **Step 2: Implement** `render.go`. Full structure (bodies port the cited ranges):

```go
package raymond

import (
    "io"

    "github.com/aymerick/raymond/ast"
)

// renderProgram walks a program body, streaming every statement
// (replaces VisitProgram's buffer, eval.go:790-813).
func (s *state) renderProgram(node *ast.Program) error {
    s.at(node)
    for _, n := range node.Body {
        if err := s.renderStatement(n); err != nil {
            return err
        }
    }
    return nil
}

// renderProgramWith is the evalProgram port (eval.go:250-293): block
// params, optional context push, optional frame swap, then render.
func (s *state) renderProgramWith(program *ast.Program, ctx Value, data *DataFrame, key interface{}) error {
    blockParams := make(map[string]Value)

    if len(program.BlockParams) > 0 {
        blockParams[program.BlockParams[0]] = ctx
    }
    if (len(program.BlockParams) > 1) && (key != nil) {
        blockParams[program.BlockParams[1]] = adaptValue(key)
    }

    if len(blockParams) > 0 {
        s.pushBlockParams(blockParams)
    }
    if ctx.IsValid() {
        s.pushCtx(ctx)
    }
    if data != nil {
        s.setDataFrame(data)
    }

    err := s.renderProgram(program)

    if data != nil {
        s.popDataFrame()
    }
    if ctx.IsValid() {
        s.popCtx()
    }
    if len(blockParams) > 0 {
        s.popBlockParams()
    }
    return err
}

func (s *state) renderStatement(node ast.Node) error {
    if err := s.step(1); err != nil {
        return err
    }
    switch n := node.(type) {
    case *ast.ContentStatement:
        s.at(n)
        if n.Value == "" {
            return nil
        }
        if err := s.writeSteps(len(n.Value)); err != nil {
            return err
        }
        _, err := io.WriteString(s.w, n.Value)
        return err
    case *ast.MustacheStatement:
        return s.renderMustache(n)
    case *ast.BlockStatement:
        return s.renderBlock(n)
    case *ast.PartialStatement:
        return s.renderPartial(n)
    case *ast.CommentStatement:
        s.at(n)
        return nil
    }
    return nil
}

// renderMustache ports VisitMustache (eval.go:816-833), streaming.
func (s *state) renderMustache(node *ast.MustacheStatement) error {
    s.at(node)

    if s.limits.MaxSubstitutions > 0 {
        if s.subs++; s.subs > s.limits.MaxSubstitutions {
            return newLimitError("substitutions", s.limits.MaxSubstitutions, ErrSubstitutionLimit)
        }
    } else {
        s.subs++
    }

    pos := posMustache
    if node.Unescaped {
        pos = posRawMustache
    }
    val, err := s.evalExpression(node.Expression, pos)
    if err != nil {
        return err
    }
    if !val.IsValid() {
        return nil
    }

    str := val.Str()
    if str == "" {
        return nil
    }
    if err := s.writeSteps(len(str)); err != nil {
        return err
    }
    if val.Kind() == KindSafeString || node.Unescaped {
        _, werr := io.WriteString(s.w, str)
        return werr
    }
    return escape(stringWriterFor(s.w), str)
}

// renderBlock ports VisitBlock (eval.go:836-882), streaming.
func (s *state) renderBlock(node *ast.BlockStatement) error {
    s.at(node)
    s.pushBlock(node)
    defer s.popBlock()

    expr, err := s.evalExpression(node.Expression, posBlock)
    if err != nil {
        return err
    }

    if s.isHelperCall(node.Expression) || s.exprFunc[node.Expression] {
        // helper/lambda owns the block; its returned value is the
        // block output, written raw (VisitProgram wrote Str(result)
        // unescaped)
        if str := expr.Str(); str != "" {
            if err := s.writeSteps(len(str)); err != nil {
                return err
            }
            if _, werr := io.WriteString(s.w, str); werr != nil {
                return werr
            }
        }
        return nil
    }

    if expr.Truthy() {
        if node.Program == nil {
            return nil
        }
        if expr.Kind() == KindList {
            // array context: per-element iteration frame (eval.go:855-868)
            l := expr.list
            for i := 0; i < l.Len(); i++ {
                if err := s.step(1); err != nil {
                    return err
                }
                frame := s.frame.newIterDataFrame(l.Len(), i, nil)
                if err := s.renderProgramWith(node.Program, l.Index(i), frame, i); err != nil {
                    return err
                }
            }
            return nil
        }
        return s.renderProgramWith(node.Program, expr, nil, nil)
    }
    if node.Inverse != nil {
        return s.renderProgram(node.Inverse)
    }
    return nil
}

// renderPartial ports VisitPartial + evalPartial + partialContext
// (eval.go:692-750, 885-906), streaming through an indentWriter.
func (s *state) renderPartial(node *ast.PartialStatement) error {
    s.at(node)

    name, ok := ast.HelperNameStr(node.Name)
    if !ok {
        if subExpr, isSub := node.Name.(*ast.SubExpression); isSub {
            v, err := s.evalExpression(subExpr.Expression, posSubExpr)
            if err != nil {
                return err
            }
            name = v.Str()
        }
    }
    if name == "" {
        return s.errorf("Unexpected partial name: %q", node.Name)
    }

    program, err := s.partials(name)
    if err != nil {
        return err
    }
    if program == nil {
        return s.errorf("Partial not found: %s", name)
    }

    // partial context (eval.go:704-723)
    if nb := len(node.Params); nb > 1 {
        return s.errorf("Unsupported number of partial arguments: %d", nb)
    }
    if (len(node.Params) > 0) && (node.Hash != nil) {
        return s.errorf("Passing both context and named parameters to a partial is not allowed")
    }

    ctx := Value{}
    if len(node.Params) == 1 {
        ctx, err = s.evalParam(node.Params[0])
        if err != nil {
            return err
        }
    } else if node.Hash != nil {
        hash, raws, herr := s.evalHash(node.Hash)
        if herr != nil {
            return herr
        }
        ctx = mapValue(valueMap(hash), len(hash) > 0, raws)
    }

    if ctx.IsValid() {
        s.pushCtx(ctx)
        defer s.popCtx()
    }

    if node.Indent != "" {
        old := s.w
        s.w = newIndentWriter(s.w, node.Indent)
        err = s.renderProgram(program)
        s.w = old
        return err
    }
    return s.renderProgram(program)
}

// --- expressions ---

// evalExpression ports VisitExpression (eval.go:927-968).
func (s *state) evalExpression(node *ast.Expression, pos callPosition) (Value, error) {
    s.at(node)
    if err := s.step(1); err != nil {
        return Value{}, err
    }

    s.pushExpr(node)
    defer s.popExpr()

    // helper call
    if helperName := node.HelperName(); helperName != "" {
        if helper := s.findHelper(helperName); helper != nil {
            return s.callHelper(helperName, helper, node, pos)
        }
    }

    // literal-as-field
    if literal, ok := node.LiteralStr(); ok {
        val, err := s.lookupField(s.curCtx(), literal, true)
        if err != nil {
            return Value{}, err
        }
        if val.IsValid() {
            return val, nil
        }
    }

    // field path
    if path := node.FieldPath(); path != nil {
        val, err := s.evalPathExpression(path, true)
        if err != nil {
            return Value{}, err
        }
        if val.IsValid() {
            return val, nil
        }
    }

    return Value{}, nil
}

// evalParam evaluates a param/hash-value node (the old engine's
// Accept dispatch for params).
func (s *state) evalParam(node ast.Node) (Value, error) {
    switch n := node.(type) {
    case *ast.SubExpression:
        s.at(n)
        return s.evalExpression(n.Expression, posSubExpr)
    case *ast.PathExpression:
        return s.evalPathExpression(n, false)
    case *ast.StringLiteral:
        s.at(n)
        return stringValue(n.Value, false), nil
    case *ast.BooleanLiteral:
        s.at(n)
        return boolValue(n.Value), nil
    case *ast.NumberLiteral:
        s.at(n)
        // Number() returns int or float64 (ast/node.go)
        return adaptValue(n.Number()), nil
    }
    return Value{}, nil
}

// evalHash ports VisitHash (eval.go:1008-1020): nil-valued pairs are
// skipped. Returns both Value map and raw map.
func (s *state) evalHash(node *ast.Hash) (map[string]Value, map[string]interface{}, error) {
    s.at(node)
    values := make(map[string]Value)
    raws := make(map[string]interface{})
    for _, pair := range node.Pairs {
        s.at(pair)
        v, err := s.evalParam(pair.Val)
        if err != nil {
            return nil, nil, err
        }
        if v.IsValid() && v.Interface() != nil {
            values[pair.Key] = v
            raws[pair.Key] = v.Interface()
        }
    }
    return values, raws, nil
}

// --- helper dispatch ---

func (s *state) isHelperCall(node *ast.Expression) bool {
    if helperName := node.HelperName(); helperName != "" {
        return s.findHelper(helperName) != nil
    }
    return false
}

func (s *state) callHelper(name string, helper coreHelper, node *ast.Expression, pos callPosition) (Value, error) {
    var params []Value
    for _, paramNode := range node.Params {
        p, err := s.evalParam(paramNode)
        if err != nil {
            return Value{}, err
        }
        params = append(params, p)
    }

    var hash map[string]Value
    if node.Hash != nil {
        var err error
        hash, _, err = s.evalHash(node.Hash)
        if err != nil {
            return Value{}, err
        }
    }

    hc := &HelperCall{s: s, name: name, expr: node, params: params, hash: hash, pos: pos}
    return helper.callCore(hc)
}

// --- path resolution (ports eval.go:296-568) ---

// evalPathExpression: block param > @root context-then-data > data >
// context (eval.go:437-482).
func (s *state) evalPathExpression(node *ast.PathExpression, exprRoot bool) (Value, error) {
    if len(node.Parts) > 0 {
        if bp, found := s.blockParam(node.Parts[0]); found {
            synthetic := mapValue(valueMap{node.Parts[0]: bp}, true,
                map[string]interface{}{node.Parts[0]: bp.Interface()})
            s.pushCtx(synthetic)
            result, err := s.evalCtxPathExpression(node, exprRoot)
            s.popCtx()
            return result, err
        }
    }

    var result Value
    var err error
    ctxTried := false

    if node.IsDataRoot() {
        result, err = s.evalCtxPathExpression(node, exprRoot)
        if err != nil {
            return Value{}, err
        }
        ctxTried = true
    }

    if !result.IsValid() && node.Data {
        result, err = s.evalDataPathExpression(node, exprRoot)
        if err != nil {
            return Value{}, err
        }
    }

    if !result.IsValid() && !ctxTried {
        result, err = s.evalCtxPathExpression(node, exprRoot)
        if err != nil {
            return Value{}, err
        }
    }

    return result, nil
}

// evalDataPathExpression (eval.go:485-499): walk frame parents by
// Depth, then resolve parts against frame data.
func (s *state) evalDataPathExpression(node *ast.PathExpression, exprRoot bool) (Value, error) {
    frame := s.frame
    for i := node.Depth; i > 0; i-- {
        if frame.parent == nil {
            return Value{}, nil
        }
        frame = frame.parent
    }
    result, _, err := s.evalCtxPath(adaptValue(frame.data), node.Parts, exprRoot)
    return result, err
}

// evalCtxPathExpression (eval.go:502-514).
func (s *state) evalCtxPathExpression(node *ast.PathExpression, exprRoot bool) (Value, error) {
    s.at(node)

    if node.IsDataRoot() {
        parts := node.Parts[1:]
        result, _, err := s.evalCtxPath(s.rootCtx(), parts, exprRoot)
        return result, err
    }
    return s.evalDepthPath(node.Depth, node.Parts, exprRoot)
}

// evalDepthPath (eval.go:517-537): parent fallback gated by
// partResolved ("Dotted Names - Context Precedence").
func (s *state) evalDepthPath(depth int, parts []string, exprRoot bool) (Value, error) {
    var result Value
    partResolved := false

    ctx := s.ancestorCtx(depth)

    for !result.IsValid() && ctx.IsValid() && (depth <= len(s.ctxStack) && !partResolved) {
        var err error
        result, partResolved, err = s.evalCtxPath(ctx, parts, exprRoot)
        if err != nil {
            return Value{}, err
        }
        if !partResolved && !result.IsValid() {
            depth++
            ctx = s.ancestorCtx(depth)
        }
    }
    return result, nil
}

// evalCtxPath (eval.go:540-568): array contexts map the path over
// elements and ALWAYS return a valid (possibly empty) list, which
// terminates the depth walk exactly like the old `result = results`.
func (s *state) evalCtxPath(ctx Value, parts []string, exprRoot bool) (Value, bool, error) {
    if ctx.Kind() == KindList {
        var values []Value
        var raws []interface{}
        for i := 0; i < ctx.list.Len(); i++ {
            v, _, err := s.resolveParts(ctx.list.Index(i), parts, exprRoot)
            if err != nil {
                return Value{}, false, err
            }
            if v.IsValid() {
                values = append(values, v)
                raws = append(raws, v.Interface())
            }
        }
        return listValue(sliceList(values), len(values) > 0, raws), false, nil
    }

    v, partResolved, err := s.resolveParts(ctx, parts, exprRoot)
    return v, partResolved, err
}

// resolveParts ports evalPath (eval.go:296-317): bracket-stripping and
// per-part field resolution with lambda invocation interleaved.
func (s *state) resolveParts(ctx Value, parts []string, exprRoot bool) (Value, bool, error) {
    partResolved := false
    for i := 0; i < len(parts); i++ {
        part := parts[i]
        if (len(part) >= 2) && (part[0] == '[') && (part[len(part)-1] == ']') {
            part = part[1 : len(part)-1]
        }
        var err error
        ctx, err = s.lookupField(ctx, part, exprRoot)
        if err != nil {
            return Value{}, partResolved, err
        }
        if !ctx.IsValid() {
            break
        }
        partResolved = true
    }
    return ctx, partResolved, nil
}

// lookupField resolves one name in a container and applies the old
// engine's func-invocation topology: any func result is invoked once;
// a METHOD's func result is invoked once more if still a func
// (eval.go:319-364 vs 368-384).
func (s *state) lookupField(ctx Value, name string, exprRoot bool) (Value, error) {
    if err := s.step(1); err != nil {
        return Value{}, err
    }
    if !ctx.IsValid() {
        return Value{}, nil
    }

    var res Value
    switch {
    case ctx.data != nil:
        res, _ = ctx.data.Lookup(name)
    case ctx.Kind() == KindFunc:
        // funcs as intermediate contexts resolve nothing (parity:
        // evalField on a func ctx finds no methods/fields)
        return Value{}, nil
    default:
        return Value{}, nil
    }

    if res.Kind() == KindFunc {
        fromMethod := res.fromMethod
        out, err := s.invokeFunc(res, exprRoot)
        if err != nil {
            return Value{}, err
        }
        if fromMethod && out.Kind() == KindFunc {
            out, err = s.invokeFunc(out, exprRoot)
            if err != nil {
                return Value{}, err
            }
        }
        return out, nil
    }
    return res, nil
}

// invokeFunc ports evalFieldFunc (eval.go:387-405): at expression root
// the lambda receives the full params/hash and the expression is
// memoized as a function call; elsewhere it gets empty options.
func (s *state) invokeFunc(fnVal Value, exprRoot bool) (Value, error) {
    var opts *Options
    if exprRoot {
        expr := s.curExpr()
        var err error
        opts, err = s.helperOptions(expr)
        if err != nil {
            return Value{}, err
        }
        s.exprFunc[expr] = true
    } else {
        opts = &Options{s: s, hash: make(map[string]interface{})}
    }
    return fnVal.fn.call(s, opts)
}

// helperOptions ports eval.go:672-686 for lambda invocation.
func (s *state) helperOptions(node *ast.Expression) (*Options, error) {
    var params []interface{}
    for _, paramNode := range node.Params {
        p, err := s.evalParam(paramNode)
        if err != nil {
            return nil, err
        }
        params = append(params, p.Interface())
    }

    var hash map[string]interface{}
    if node.Hash != nil {
        var err error
        _, hash, err = s.evalHash(node.Hash)
        if err != nil {
            return nil, err
        }
    }

    return &Options{s: s, params: params, hash: hash}, nil
}

// findHelper resolves template helpers then globals via the state seam.
func (s *state) findHelper(name string) coreHelper {
    return s.helpers(name)
}

// sliceList is a []Value-backed List for array-context path results.
type sliceList []Value

func (l sliceList) Len() int          { return len(l) }
func (l sliceList) Index(i int) Value { return l[i] }
```

Move `sliceList` to `value.go` (it has no reflection). NOTE on `evalCtxPath` raws: the old engine produced `[]interface{}` including when empty (nil slice boxed in a non-nil interface) — `listValue(sliceList(nil), false, raws)` with `raws` a `[]interface{}` variable (possibly nil) reproduces both the always-valid result and the falsy emptiness.

- [ ] **Step 3:** Gate: `go build ./... && go test ./...` → green (new code dormant until Task 9 wires `Compiled`/state constructors; `state.partials`/`state.helpers` funcs are supplied there).
- [ ] **Step 4:** `git add render.go helper_call.go value.go adapt_helpers.go && git commit -m "feat: streaming walker, helper dispatch, and path resolution"`

---

### Task 8: `builtins.go` — streaming builtin helpers

**Files:** Create `builtins.go`. Do NOT touch `helper.go`'s registrations yet (the old engine still drives; flipping the registry happens in Task 9).

Builtins replicate the legacy reflect-driven arity errors (`callFunc` counted the `*Options` parameter in `numIn`): `if/unless/with/each` report `needed 2`, `lookup/equal` `needed 3`, `log` `needed 1`.

- [ ] **Step 1: Implement** `builtins.go`:

```go
package raymond

import "log"

// requireParams reproduces callFunc's arity error for builtins, where
// numIn counted the trailing *Options parameter.
func requireParams(hc *HelperCall, needed int) error {
    if hc.NumParams() != needed-1 {
        return hc.s.errorf("Helper '%s' called with wrong number of arguments, needed %d but got %d",
            hc.name, needed, hc.NumParams())
    }
    return nil
}

// isIncludableZero ports Options.isIncludableZero (helper.go:280-290).
func includableZero(hc *HelperCall) bool {
    if b, ok := hc.Hash("includeZero").Interface().(bool); ok && b {
        if nb, ok := hc.Param(0).Interface().(int); ok && nb == 0 {
            return true
        }
    }
    return false
}

func builtinIf(hc *HelperCall) error {
    if err := requireParams(hc, 2); err != nil {
        return err
    }
    if includableZero(hc) || hc.Param(0).Truthy() {
        return hc.Fn()
    }
    return hc.Inverse()
}

func builtinUnless(hc *HelperCall) error {
    if err := requireParams(hc, 2); err != nil {
        return err
    }
    if includableZero(hc) || hc.Param(0).Truthy() {
        return hc.Inverse()
    }
    return hc.Fn()
}

func builtinWith(hc *HelperCall) error {
    if err := requireParams(hc, 2); err != nil {
        return err
    }
    if hc.Param(0).Truthy() {
        return hc.FnWith(hc.Param(0).Interface())
    }
    return hc.Inverse()
}

func builtinEach(hc *HelperCall) error {
    if err := requireParams(hc, 2); err != nil {
        return err
    }
    ctx := hc.Param(0)
    if !ctx.Truthy() {
        return hc.Inverse()
    }

    it := iterableOf(ctx)
    if it == nil {
        return nil
    }
    length := it.Len()
    return it.Each(func(i int, key interface{}, val Value) error {
        if err := hc.s.step(1); err != nil {
            return err
        }
        // arrays: frame key nil, block-param key = index (helper.go:334-339)
        blockKey := key
        if blockKey == nil {
            blockKey = i
        }
        frame := hc.s.frame.newIterDataFrame(length, i, key)
        return hc.fnWithKey(val, frame, blockKey)
    })
}

// iterableOf extracts the Iterable behind a Value (adapter containers
// and valueMap implement it; sliceList iterates by index).
func iterableOf(v Value) Iterable {
    if v.data != nil {
        if it, ok := v.data.(Iterable); ok {
            return it
        }
    }
    if v.list != nil {
        return listIterable{v.list}
    }
    return nil
}

type listIterable struct{ l List }

func (li listIterable) Len() int { return li.l.Len() }

func (li listIterable) Each(fn func(i int, key interface{}, val Value) error) error {
    for i := 0; i < li.l.Len(); i++ {
        if err := fn(i, nil, li.l.Index(i)); err != nil {
            return err
        }
    }
    return nil
}

func builtinLog(hc *HelperCall) error {
    if err := requireParams(hc, 1); err != nil {
        return err
    }
    log.Print(hc.Param(0).Str())
    return nil
}

func builtinLookup(hc *HelperCall) error {
    if err := requireParams(hc, 3); err != nil {
        return err
    }
    // lookupHelper = Str(options.Eval(obj, field)) (helper.go:386-388)
    obj := hc.Param(0)
    field := hc.Param(1).Str()
    if !obj.IsValid() || field == "" {
        return nil
    }
    v, err := hc.s.lookupField(obj, field, false)
    if err != nil {
        return err
    }
    if str := v.Str(); str != "" {
        _, werr := hc.WriteString(str)
        return werr
    }
    return nil
}

func builtinEqual(hc *HelperCall) error {
    if err := requireParams(hc, 3); err != nil {
        return err
    }
    if hc.Param(0).Str() == hc.Param(1).Str() {
        return hc.Fn()
    }
    return nil
}
```

**Escaping caveat:** `builtinLookup` WRITES its result, so in escaped-mustache position the `escapeWriter` escapes it — matching the legacy string return being escaped by `VisitMustache`. Same logic makes every write-through builtin parity-safe.

- [ ] **Step 2:** Gate: `go build ./... && go test ./...` → green (still dormant).
- [ ] **Step 3:** `git add builtins.go && git commit -m "feat: streaming builtin helpers"`

---

### Task 9: dual-mode `Options`, `tpl.execute`, `compile.go`, and the parity harness

**Files:** Modify `helper.go` (method bodies become dual-mode), `template.go` (add private `execute` + seams; public API untouched), `capped_writer.go` (sentinel wraps `ErrOutputLimit`). Create `compile.go`, `parity_test.go`.

This is the M2 milestone: the new engine becomes fully drivable while the old engine still serves every public entry point. The parity harness is where divergences surface — expect iteration here.

- [ ] **Step 1: Dual-mode `Options`.** Every `Options` method body gains a new-engine branch `if options.s != nil { ... }` ahead of the existing old-engine body (which stays verbatim until Task 12). The new branches:

```go
func (options *Options) Value(name string) interface{} {
    if options.s != nil {
        v, err := options.s.lookupField(options.s.curCtx(), name, false)
        if err != nil {
            options.err = err
            return nil
        }
        return v.Interface()
    }
    // ... existing body unchanged ...
}

func (options *Options) Ctx() interface{} {
    if options.s != nil {
        return options.s.curCtx().Interface()
    }
    // ... existing body ...
}

func (options *Options) Data(name string) interface{} {
    if options.s != nil {
        return options.s.frame.Get(name)
    }
    // ... existing body ...
}

// DataStr, DataFrame, NewDataFrame, newIterDataFrame: same pattern,
// substituting options.s.frame for options.eval.dataFrame.

func (options *Options) evalBlock(ctx interface{}, data *DataFrame, key interface{}) string {
    if options.s != nil {
        if options.err != nil {
            return ""
        }
        result := ""
        if block := options.s.curBlock(); (block != nil) && (block.Program != nil) {
            out, err := options.s.capture(func() error {
                return options.s.renderProgramWith(block.Program, adaptValue(ctx), data, key)
            })
            if err != nil {
                options.err = err
                return ""
            }
            result = out
        }
        return result
    }
    // ... existing body ...
}

func (options *Options) Inverse() string {
    if options.s != nil {
        if options.err != nil {
            return ""
        }
        result := ""
        if block := options.s.curBlock(); (block != nil) && (block.Inverse != nil) {
            out, err := options.s.capture(func() error {
                return options.s.renderProgram(block.Inverse)
            })
            if err != nil {
                options.err = err
                return ""
            }
            result = out
        }
        return result
    }
    // ... existing body ...
}

func (options *Options) Eval(ctx interface{}, field string) interface{} {
    if options.s != nil {
        if ctx == nil || field == "" {
            return nil
        }
        v, err := options.s.lookupField(adaptValue(ctx), field, false)
        if err != nil {
            options.err = err
            return nil
        }
        return v.Interface()
    }
    // ... existing body ...
}
```

`Param/ParamStr/Params/Hash/HashStr/HashProp/ValueStr/Fn/FnWith/FnCtxData/FnData/isIncludableZero` need no branch — they already operate on fields or delegate to the branched methods.

- [ ] **Step 2: sentinel chaining** in `capped_writer.go` — replace the var only:

```go
var errBudgetOverflow = fmt.Errorf("render output budget exceeded: %w", ErrOutputLimit)
```

(import `"fmt"`, drop `"errors"` if now unused). `capped_writer_test.go`'s `errors.Is(err, errBudgetOverflow)` passes by identity; new code can also match `errors.Is(err, ErrOutputLimit)`.

- [ ] **Step 3: `tpl.execute` + seams** in `template.go` (private; nothing public changes yet):

```go
// execute drives the streaming engine for this template. cap, when
// non-nil, is the outermost output cap already wrapping w.
func (tpl *Template) execute(c context.Context, w io.Writer, cap *cappedWriter,
    ctx interface{}, privData *DataFrame, limits Limits) error {

    frame := privData
    if frame == nil {
        frame = NewDataFrame()
    }

    s := &state{
        tctx:         c,
        w:            w,
        cap:          cap,
        limits:       limits,
        nextCtxCheck: ctxCheckInterval,
        helpers:      tpl.helperSeam(),
        partials:     tpl.partialSeam(),
        ctxStack:     []Value{adaptValue(ctx)},
        frame:        frame,
        exprFunc:     make(map[*ast.Expression]bool),
    }
    return s.renderProgram(tpl.program)
}

// helperSeam: template helpers, then globals (eval.go:583-591).
func (tpl *Template) helperSeam() func(string) coreHelper {
    return func(name string) coreHelper {
        if h := tpl.findHelper(name); h != zero {
            return &legacyHelper{name: name, fn: h}
        }
        if h := findHelper(name); h != zero {
            return &legacyHelper{name: name, fn: h}
        }
        return nil
    }
}

// partialSeam: template partials, then globals, lazily parsed
// (eval.go:693-701, 726-731).
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
        return ptpl.program, nil
    }
}
```

- [ ] **Step 4: `compile.go`** — the new public API:

```go
package raymond

import (
    "context"
    "errors"
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

func (c *Compiled) exec(tctx context.Context, w io.Writer, root Value) error {
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

    err := s.renderProgram(c.program)
    if err != nil && errors.Is(err, errBudgetOverflow) {
        return newLimitError("output bytes", c.limits.MaxOutputBytes, ErrOutputLimit)
    }
    return err
}

// helperSeam: own streaming helpers, then the global registry
// (legacy funcs bridged).
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

func (c *Compiled) partialSeam() func(string) (*ast.Program, error) {
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
```

(Add `"fmt"` import; delete the two placeholder-flag lines around `processCompiled` — they exist in this plan only to forbid inventing such a hook.)

- [ ] **Step 5: parity harness** `parity_test.go`. Find the package-level test tables first: `grep -n '\[\]raymondTest{' *_test.go` → expected hits include `eval_test.go`, `helper_test.go`, `mustache_test.go` (confirm names; `base_test.go` defines the `raymondTest` struct and `launchTests`). The harness re-runs every table entry through `tpl.execute` and applies the same acceptance as `launchTests` (string equality, or membership when `output` is `[]string`):

```go
package raymond

import (
    "context"
    "strings"
    "testing"
)

// execNew renders a test template through the streaming engine.
func execNew(t *testing.T, test raymondTest) (string, error) {
    t.Helper()
    tpl := MustParse(test.input)
    if test.helpers != nil {
        tpl.RegisterHelpers(test.helpers)
    }
    if test.partials != nil {
        tpl.RegisterPartials(test.partials)
    }
    var sb strings.Builder
    var priv *DataFrame
    if test.privData != nil {
        priv = NewDataFrame()
        for k, v := range test.privData {
            priv.Set(k, v)
        }
    }
    err := tpl.execute(context.Background(), &sb, nil, test.ctx, priv, Limits{})
    return sb.String(), err
}

func runParity(t *testing.T, tests []raymondTest) {
    t.Helper()
    for _, test := range tests {
        out, err := execNew(t, test)
        if err != nil {
            t.Errorf("parity %q: unexpected error: %v", test.name, err)
            continue
        }
        // same acceptance logic as launchTests (base_test.go)
        switch expected := test.output.(type) {
        case string:
            if out != expected {
                t.Errorf("parity %q:\n  got  %q\n  want %q", test.name, out, expected)
            }
        case []string:
            match := false
            for _, e := range expected {
                if out == e {
                    match = true
                }
            }
            if !match {
                t.Errorf("parity %q: got %q, want one of %v", test.name, out, expected)
            }
        }
    }
}

func TestParity_Eval(t *testing.T)    { runParity(t, evalTests) }
func TestParity_Helpers(t *testing.T) { runParity(t, helperTests) }
```

**Adjust to reality:** the exact field names of `raymondTest` (`privData` may already be a `*DataFrame`, `output` may be `interface{}`) and table variable names MUST be read from `base_test.go`/`eval_test.go` before writing this file; replicate `launchTests`' construction logic exactly (it may use `ParseWithOptions` or register helpers differently). Add a third call for the mustache corpus if its loader exposes a table (`mustache_test.go`); if it drives `launchTests` directly from YAML inside its own function, copy that loader call here.

- [ ] **Step 6: Iterate to parity.** `go test -run TestParity .` — fix divergences in `render.go`/`adapt.go` until green. Known likely suspects (check in this order): block-param synthetic context shape, `evalCtxPath` array-always-valid rule, hash nil-skipping, SafeString detection, `{{this}}`/`{{.}}` (`IsDataRoot`/`Parts` empty → `resolveParts` with zero parts returns ctx itself with `partResolved=false` — verify `{{.}}` renders current ctx), partial indent, whitespace `Strip` handling (whitespace is parse-time — `processWhitespaces` already ran — nothing to do at render time).
- [ ] **Step 7:** Full gate: `go test ./...` green (old suite + parity).
- [ ] **Step 8:** `git add -A && git commit -m "feat: Compile/Execute API, dual-mode Options, and old/new parity harness"`

---

### Task 10: Flip every legacy entry point onto the new engine

**Files:** Modify `template.go` (Exec/ExecWith/ExecTo/ExecToWith/ExecToWithOptions bodies), `capped_writer.go` (add `WriteString`).

- [ ] **Step 1:** Add `WriteString` to `capped_writer.go` — required so 10 MiB literals never materialize as `[]byte` (`TestExecToWithOptions` heap-delta test):

```go
// WriteString mirrors Write's state machine while slicing the string
// BEFORE any []byte conversion, so only bytes that fit are copied.
func (cw *cappedWriter) WriteString(s string) (int, error) {
    if len(s) == 0 {
        return 0, nil
    }

    remaining := cw.limit - cw.written
    if remaining < 0 {
        remaining = 0
    }

    if int64(len(s)) <= remaining {
        n, err := io.WriteString(cw.dst, s)
        if n < 0 {
            n = 0
        }
        cw.written += int64(n)
        return n, err
    }

    if remaining == 0 {
        return 0, errBudgetOverflow
    }

    n, err := io.WriteString(cw.dst, s[:remaining])
    if n < 0 {
        n = 0
    }
    cw.written += int64(n)
    if err != nil {
        return n, err
    }
    if int64(n) < remaining {
        return n, io.ErrShortWrite
    }
    return n, errBudgetOverflow
}
```

- [ ] **Step 2:** Replace `ExecWith` (template.go:253-270):

```go
// ExecWith evaluates template with given context and private data frame.
func (tpl *Template) ExecWith(ctx interface{}, privData *DataFrame) (string, error) {
    if err := tpl.parse(); err != nil {
        return "", err
    }

    var sb strings.Builder
    if err := tpl.execute(context.Background(), &sb, nil, ctx, privData, Limits{}); err != nil {
        return "", err
    }
    return sb.String(), nil
}
```

(`Exec`, `MustExec`, `Render`, `MustRender` are wrappers over this and need no edit.)

- [ ] **Step 3:** Replace `ExecToWithOptions` (template.go:304-380) — godoc comment stays:

```go
func (tpl *Template) ExecToWithOptions(w io.Writer, ctx interface{}, privData *DataFrame, opts RenderOptions) error {
    if opts.Enforced && opts.MaxOutputBytes < 0 {
        return &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
    }

    if err := tpl.parse(); err != nil {
        return err
    }

    dw := &destWriter{w: w}
    var sink io.Writer = dw
    var capped *cappedWriter
    if opts.Enforced {
        capped = newCappedWriter(dw, opts.MaxOutputBytes)
        sink = capped
    }

    err := tpl.execute(context.Background(), sink, capped, ctx, privData, Limits{})
    if err == nil {
        return nil
    }
    if errors.Is(err, errBudgetOverflow) {
        return &RenderBudgetExceededError{Kind: "output bytes", Limit: opts.MaxOutputBytes}
    }
    var de *destError
    if errors.As(err, &de) {
        return &RenderDestinationError{Cause: de.cause}
    }
    return err
}
```

`ExecTo`/`ExecToWith` already delegate here. Delete `execToErrRecover` and `errRecover` (no longer referenced — `eval.go` still defines its own panics but nothing reaches them; if `go vet` flags unused, keep until Task 12). Imports: add `"context"`, `"strings"`; `"runtime"` becomes unused — remove.

- [ ] **Step 4:** Gate: `go test ./...` — THE flip test. Iterate on divergences (this is where handlebars/, mustache/, exec_to, render_options suites all run the new engine). Run `go test -race ./...` too (concurrent exec_to tests).
- [ ] **Step 5:** `git add -A && git commit -m "feat!: drive all Exec entry points through the streaming engine"`

---

### Task 11: Dual registry + streaming builtins live

**Files:** Modify `helper.go` (registry + init), delete legacy builtin funcs there.

- [ ] **Step 1:** Registry entries replace bare `reflect.Value`:

```go
// helperEntry holds either a legacy reflected helper or a streaming one.
type helperEntry struct {
    legacy    reflect.Value
    streaming Helper
}

func (e helperEntry) valid() bool { return e.streaming != nil || e.legacy.IsValid() }

var helpers = make(map[string]helperEntry)
```

`RegisterHelper` dispatches:

```go
func RegisterHelper(name string, helper interface{}) {
    helpersMutex.Lock()
    defer helpersMutex.Unlock()

    if helpers[name].valid() {
        panic(fmt.Errorf("Helper already registered: %s", name))
    }

    switch h := helper.(type) {
    case Helper:
        helpers[name] = helperEntry{streaming: h}
    case func(*HelperCall) error:
        helpers[name] = helperEntry{streaming: HelperFunc(h)}
    default:
        val := reflect.ValueOf(helper)
        ensureValidHelper(name, val)
        helpers[name] = helperEntry{legacy: val}
    }
}
```

`findHelper` returns `helperEntry`; `init()` registers the streaming builtins:

```go
func init() {
    RegisterHelper("if", HelperFunc(builtinIf))
    RegisterHelper("unless", HelperFunc(builtinUnless))
    RegisterHelper("with", HelperFunc(builtinWith))
    RegisterHelper("each", HelperFunc(builtinEach))
    RegisterHelper("log", HelperFunc(builtinLog))
    RegisterHelper("lookup", HelperFunc(builtinLookup))
    RegisterHelper("equal", HelperFunc(builtinEqual))
}
```

Delete `ifHelper/unlessHelper/withHelper/eachHelper/logHelper/lookupHelper/equalHelper` (helper.go:296-398).

- [ ] **Step 2:** Update the two consumers of the registry shape:
  - `template.go` `helperSeam`: global branch becomes

```go
        if e := findHelper(name); e.valid() {
            if e.streaming != nil {
                return &streamingHelper{h: e.streaming}
            }
            return &legacyHelper{name: name, fn: e.legacy}
        }
        return nil
```

  - `compile.go` `helperSeam`: same substitution for its `findHelper` branch.
  - `eval.go`'s `findHelper` usage (old engine, still compiled): it compares against `zero` — update its body to bridge or simply make old-engine `findHelper` return `e.legacy` (old engine is dead code now; builtins as streaming are invisible to it, which is fine since nothing executes it). Minimal change: in `eval.go:583-591` replace `findHelper(name)` with `findHelper(name).legacy`.
  - `Template.RegisterHelper` (template-level) keeps accepting legacy funcs only (unchanged); `Clone` unchanged.

- [ ] **Step 3:** Gate: `go test ./...` — builtins now stream. Watch: `RemoveHelper("if")` test (shadowing), `RegisterHelper("if", ...)` duplicate panic, `{{#if}}` arity errors, `{{log}}`, `{{lookup}}` escaping, `{{equal}}`.
- [ ] **Step 4:** `git add -A && git commit -m "feat: dual helper registry with streaming builtins"`

---

### Task 12: Delete the old engine

**Files:** Delete `eval.go`. Modify `helper.go`, `template.go`, `string.go` (only if dangling references).

- [ ] **Step 1:** `git rm eval.go`.
- [ ] **Step 2:** Remove dangling references; the authoritative list is the compiler's, expected fallout:
  - `helper.go`: drop the `eval *evalVisitor` field from `Options`; delete every `// ... existing body ...` old-engine branch (un-nest the `if options.s != nil` bodies); delete `newOptions`/`newEmptyOptions` if unreferenced.
  - `template.go`: `errRecover`/`execToErrRecover` already gone (Task 10); nothing else.
  - `eval.go`'s `errorType`/`fmtStringerType`/`zero` vars are used by `string.go`/`utils.go`/`helper.go` — MOVE these three declarations into `string.go` (they are adapter-layer reflection, allowed there).
  - `indentLines` lived in eval.go and is referenced by `exec_state_test.go`'s parity test — move `indentLines` into `exec_state_test.go` itself (test-only fixture now).
- [ ] **Step 3:** Gate: `go build ./... && go vet ./... && go test ./...` → green. Also `go test -race ./...`.
- [ ] **Step 4:** Confirm no panic-as-control-flow remains in the render path: `grep -n "panic(" *.go | grep -v _test.go` → hits only in registration validation (`RegisterHelper`, `addPartial`, `ensureValidHelper`, `RegisterPartial*`), `Must*` wrappers, `Str` (public API contract), and `escape.go`'s unreachable default.
- [ ] **Step 5:** `git add -A && git commit -m "refactor!: delete the legacy evaluator"`

---

### Task 13: Hardening tests — limits, cancellation, concurrency, quarantine

**Files:** Create `compile_test.go`, `quarantine_test.go`.

- [ ] **Step 1:** `compile_test.go`:

```go
package raymond

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "strings"
    "sync"
    "testing"
)

func TestCompile_SourceSizeLimit(t *testing.T) {
    src := strings.Repeat("a", 100)
    if _, err := Compile(src, Limits{MaxTemplateSize: 100}); err != nil {
        t.Fatalf("exact fit failed: %v", err)
    }
    _, err := Compile(src, Limits{MaxTemplateSize: 99})
    if !errors.Is(err, ErrTemplateTooLarge) {
        t.Errorf("err = %v, want ErrTemplateTooLarge", err)
    }
}

func TestCompile_NodeAndDepthLimits(t *testing.T) {
    if _, err := Compile(strings.Repeat("{{x}}", 100), Limits{MaxNodes: 10}); !errors.Is(err, ErrTemplateTooComplex) {
        t.Errorf("nodes: err = %v, want ErrTemplateTooComplex", err)
    }
    deep := "{{#if a}}{{#if b}}{{#if c}}x{{/if}}{{/if}}{{/if}}"
    if _, err := Compile(deep, Limits{MaxDepth: 3}); !errors.Is(err, ErrTemplateTooComplex) {
        t.Errorf("depth: err = %v, want ErrTemplateTooComplex", err)
    }
}

func TestExecute_OutputLimit(t *testing.T) {
    c, err := Compile("{{name}}", Limits{MaxOutputBytes: 5})
    if err != nil {
        t.Fatal(err)
    }
    var buf bytes.Buffer
    if err := c.Execute(context.Background(), &buf, map[string]string{"name": "12345"}); err != nil {
        t.Fatalf("exact fit failed: %v", err)
    }
    buf.Reset()
    err = c.Execute(context.Background(), &buf, map[string]string{"name": "123456"})
    if !errors.Is(err, ErrOutputLimit) {
        t.Errorf("err = %v, want ErrOutputLimit", err)
    }
    if buf.Len() > 5 {
        t.Errorf("destination got %d bytes, budget 5", buf.Len())
    }
}

func TestExecute_SubstitutionLimit(t *testing.T) {
    c, err := Compile("{{a}}{{a}}{{a}}", Limits{MaxSubstitutions: 2})
    if err != nil {
        t.Fatal(err)
    }
    err = c.Execute(context.Background(), &bytes.Buffer{}, map[string]string{"a": "x"})
    if !errors.Is(err, ErrSubstitutionLimit) {
        t.Errorf("err = %v, want ErrSubstitutionLimit", err)
    }
}

func TestExecute_StepLimit(t *testing.T) {
    items := make([]int, 10000)
    c, err := Compile("{{#each items}}{{this}}{{/each}}", Limits{MaxSteps: 100})
    if err != nil {
        t.Fatal(err)
    }
    err = c.Execute(context.Background(), &bytes.Buffer{}, map[string]interface{}{"items": items})
    if !errors.Is(err, ErrStepLimit) {
        t.Errorf("err = %v, want ErrStepLimit", err)
    }
}

func TestExecute_ContextCancellation(t *testing.T) {
    items := make([]int, 100000)
    c, err := Compile("{{#each items}}{{this}}{{/each}}", Limits{})
    if err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    err = c.Execute(ctx, &bytes.Buffer{}, map[string]interface{}{"items": items})
    if !errors.Is(err, context.Canceled) {
        t.Errorf("err = %v, want context.Canceled", err)
    }
}

func TestExecute_Concurrent(t *testing.T) {
    c, err := Compile("Hello {{name}}!", Limits{})
    if err != nil {
        t.Fatal(err)
    }
    var wg sync.WaitGroup
    for i := 0; i < 32; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            var sb strings.Builder
            name := fmt.Sprintf("g%d", i)
            if err := c.Execute(context.Background(), &sb, map[string]string{"name": name}); err != nil {
                t.Errorf("goroutine %d: %v", i, err)
                return
            }
            if want := "Hello " + name + "!"; sb.String() != want {
                t.Errorf("goroutine %d: got %q want %q", i, sb.String(), want)
            }
        }(i)
    }
    wg.Wait()
}

func TestExecute_StreamingHelper(t *testing.T) {
    c, err := Compile("{{shout msg}}", Limits{})
    if err != nil {
        t.Fatal(err)
    }
    c.RegisterHelper("shout", HelperFunc(func(hc *HelperCall) error {
        _, werr := hc.WriteString(strings.ToUpper(hc.Param(0).Str()) + "!")
        return werr
    }))
    var sb strings.Builder
    if err := c.Execute(context.Background(), &sb, map[string]string{"msg": "h<i"}); err != nil {
        t.Fatal(err)
    }
    // escaped-mustache position escapes streamed bytes
    if sb.String() != "H&lt;I!" {
        t.Errorf("got %q, want %q", sb.String(), "H&lt;I!")
    }
}

func TestExecute_CompiledPartial(t *testing.T) {
    p, err := Compile("[{{x}}]", Limits{})
    if err != nil {
        t.Fatal(err)
    }
    c, err := Compile("{{> box}}", Limits{})
    if err != nil {
        t.Fatal(err)
    }
    c.RegisterPartial("box", p)
    var sb strings.Builder
    if err := c.Execute(context.Background(), &sb, map[string]string{"x": "v"}); err != nil {
        t.Fatal(err)
    }
    if sb.String() != "[v]" {
        t.Errorf("got %q, want %q", sb.String(), "[v]")
    }
}
```

- [ ] **Step 2:** `quarantine_test.go` — reflection stays in the adapter:

```go
package raymond

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

// reflectAllowed lists the only non-test files permitted to import
// reflect: the adapter layer and the legacy public utilities.
var reflectAllowed = map[string]bool{
    "adapt.go":         true,
    "adapt_helpers.go": true,
    "string.go":        true,
    "utils.go":         true,
    "data_frame.go":    true, // mapStringInterface
    "helper.go":        true, // legacy registration validation
    "template.go":      true, // legacy tpl.helpers map
}

func TestReflectionQuarantine(t *testing.T) {
    files, err := filepath.Glob("*.go")
    if err != nil {
        t.Fatal(err)
    }
    for _, f := range files {
        if strings.HasSuffix(f, "_test.go") || reflectAllowed[f] {
            continue
        }
        src, err := os.ReadFile(f)
        if err != nil {
            t.Fatal(err)
        }
        if strings.Contains(string(src), `"reflect"`) {
            t.Errorf("%s imports reflect — core files must stay reflection-free", f)
        }
    }
}
```

- [ ] **Step 3:** `go test -run 'TestCompile|TestExecute|TestReflectionQuarantine' .` → PASS; full gate incl. `-race`.
- [ ] **Step 4:** `git add compile_test.go quarantine_test.go && git commit -m "test: limits, cancellation, concurrency, and reflection-quarantine coverage"`

---

### Task 14: Benchmarks and wrap-up

**Files:** None new (benchmark_test.go already exists with `BenchmarkExec_NoBudget_Legacy`, `BenchmarkExecTo_WithBudget`).

- [ ] **Step 1:** `go test -bench . -benchmem -run '^$' .` — compare against `BENCHMARKS.md`. The streaming core must not regress the no-budget path; expect improvement (no per-program buffers).
- [ ] **Step 2:** Heap-bound flake check: `go test -run TestExecToWithOptions -count=5 .` → stable PASS.
- [ ] **Step 3:** Final sweep:

```bash
go build ./... && go vet ./... && go test ./... && go test -race ./...
grep -rn '"reflect"' --include='*.go' . | grep -v _test | grep -v adapt | grep -v string.go | grep -v utils.go | grep -v data_frame.go | grep -v helper.go | grep -v template.go
# expected: no output
```

- [ ] **Step 4:** Update `CHANGELOG.md` (Unreleased): new `Compile`/`Execute` API, `Limits`, sentinel errors, streaming helpers, internal rewrite note ("no behavior change for existing entry points"). Record benchmark numbers in `BENCHMARKS.md`.
- [ ] **Step 5:** `git add -A && git commit -m "docs: changelog and benchmark numbers for the streaming core"`

---

## Execution risk notes (read before Tasks 9-11)

1. **Parity harness is the workhorse.** Task 9 Step 6 is intentionally open-ended: expect most divergences there, where BOTH engines are still alive and diffable. Do not proceed to Task 10 with known diffs.
2. **`{{this}}` / `{{.}}`:** paths with `Parts == []` resolve to the current context via `resolveParts`' zero-iteration return — verify against `basic_test.go`'s "current context" cases in the flip (Task 10), they run only through the public API.
3. **Raw blocks** (`{{{{raw}}}}`): parsed as BlockStatement whose Program contains one ContentStatement; the walker handles them with no special casing — but `helpers_test.go` has raw-block-with-helper cases: the helper's returned string is written raw (renderBlock helper branch) — verify.
4. **`log` builtin returns nothing:** legacy returned `""`; the streaming one writes nothing — both produce empty output, but `{{log "x"}}` in mustache position must not print "&lt;nil&gt;" — the `Value{}` → `!val.IsValid()` early return in `renderMustache` covers it.
5. **Error wrapping:** `errors.Is` through `s.errorf` is broken by design (old `errPanic` used `%s` too) — limit errors and ctx errors must NEVER pass through `s.errorf`; they propagate raw. Check every `return` in render.go: only template-semantic failures go through `errorf`.




