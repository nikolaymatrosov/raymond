# Design: Streaming, Budgeted, Closed-Model Render Core

**Date:** 2026-06-11
**Branch:** `003-streaming-core`
**Status:** Draft for review

## 1. Problem

Raymond's render core (`eval.go`, 1027 lines) has three structural problems
that make it unsuitable for rendering untrusted templates in a multi-tenant
hot path:

1. **String assembly.** Output is built by string concatenation and
   per-program `bytes.Buffer`s (`concat += ...` for block arrays,
   buffer-per-`VisitProgram`). Memory is proportional to output size at
   every nesting level, and the existing output budget (feature 002) had to
   be bolted on with per-statement slicing and a panicked sentinel.
2. **Panic as control flow.** Runtime errors (`errPanic`, `errorf`), the
   budget sentinel (`errBudgetOverflow`), and helper validation all panic
   and rely on `errRecover` at the top of `Exec`. This obscures error paths
   and makes every internal interface lie about its failure modes.
3. **Reflection woven through evaluation.** Every field lookup, method
   call, and helper invocation goes through `reflect` inside the evaluator
   (`evalField`, `evalMethod`, `callFunc`). This is slow, hard to audit,
   and — for untrusted input — scary: data lookup has the *capability* to
   call methods on arbitrary caller types.

Additionally, the engine has no CPU bound: a template looping over
attacker-controlled ranges can spin for unbounded time (the classic
`text/template` weakness), and there is no cancellation point.

## 2. Goals and non-goals

### Goals

- `text/template` shape: compile once to an immutable AST, execute many
  times against an `io.Writer`. `Execute` safe for concurrent use; all
  mutable state in a per-execution struct.
- No string assembly in the core walk: every node writes into a (wrapped)
  writer; `io.WriteString` fast path for literals.
- Plain `error` returns through the entire render walk. Zero
  panic/recover-as-control-flow in the new core. (`Must*` keep panicking —
  they are explicit panic-on-error wrappers; the parser's internal
  panic/recover stays, it is contained and pre-existing.)
- Budgets in two phases:
  - **Parse-time** (cheapest place to fail): `MaxTemplateSize` (bytes of
    source, checked before lexing), `MaxNodes` (counted *inside* the
    parser — a 100k-deep `{{#if}}` bomb fails at node N+1, not after
    building the tree), `MaxDepth` (nesting).
  - **Execution-time**: `MaxOutputBytes` (an `io.Writer` wrapper, so it
    caps everything uniformly including huge user-data values),
    `MaxSubstitutions`, `MaxSteps` (deterministic CPU fuel; builtins
    charged proportional to work), plus amortized `ctx.Done()` checks for
    wall-clock deadlines.
- Sentinel errors (`ErrOutputLimit`, `ErrSubstitutionLimit`, `ErrStepLimit`,
  `ErrTemplateTooComplex`, `ErrTemplateTooLarge`) wrapped so callers can
  `errors.Is`/`errors.As` and map breaches to per-tenant 4xx vs transient
  I/O retry.
- Closed data model in the core: `Data.Lookup(path) (Value, bool)` over a
  small tagged union. The core walk imports no `reflect`.

### Non-goals / hard constraints

- **`go test ./...` stays green with zero test changes.** 66 test
  functions, including the ported handlebars.js suite, the mustache YAML
  corpus, and the budget suites. Tests pin reflection semantics
  (struct fields, `strings.Title` promotion, `handlebars:` tags, methods
  as values, funcs in maps), arbitrary legacy helper signatures,
  `Options.Fn() string`, the full legacy API, `PrintAST()` format,
  white-box internals (`newTemplate`, `tpl.source`, `tpl.partials`,
  `newCappedWriter`, `errors.Is(err, errBudgetOverflow)` identity), and
  exact budget byte semantics.
- No new dependencies (stdlib only).
- No behavior change for any existing entry point.

## 3. Decisions (made with the user)

1. **Closed core + reflection adapter.** The core engine sees only the
   closed `Value`/`Data` model. One adapter layer (quarantined to
   `adapt*.go`, enforced by a test) converts arbitrary Go values lazily via
   reflection. Reflection survives — as a *boundary capability*, not a core
   one.
2. **New API + legacy shims.** A new `Compile(src, Limits)` /
   `Execute(ctx, w, data)` API is the real engine. Every existing public
   identifier remains and becomes a thin wrapper over it.
3. **Dual helper API.** Legacy helpers (reflect-invoked, `Options.Fn()`
   returning `string`) keep working through a bridge; a new streaming
   helper interface (`Fn() error` writing into the execution's writer) is
   added alongside for new code.

### Alternatives considered and rejected

- *Keep reflection in data resolution, only rewrite the render path.*
  Smaller diff, but leaves the audit problem (data lookup can call
  methods) woven through the evaluator and forfeits the closed-model
  benefit that motivated the rewrite.
- *Clean-break v2 module.* Cleanest API, but abandons the requirement that
  the existing suite keeps passing in this codebase.
- *Panic/recover as internal non-local exit (the `text/template` idiom).*
  Genuinely idiomatic in this niche, but since node interfaces are being
  designed fresh, plain `error` returns are simpler and equally clean.
- *All-or-nothing output via unbounded internal buffer.* Reintroduces the
  OOM the budget exists to prevent. Streaming with "≤ budget bytes
  delivered, then a typed error" is what feature 002 pinned; callers who
  need all-or-nothing can render into their own capped buffer.

## 4. Architecture

```
                 ┌────────────────────────────────────────────┐
   legacy API    │  template.go (shims)                       │
 Exec/ExecTo*───▶│  Render/Parse/ParseWithOptions/Must*       │
                 └──────────────┬─────────────────────────────┘
                                ▼
              ┌──────────────────────────────────┐
 new API      │  compile.go                      │
 Compile ────▶│  *Compiled{program, limits,      │
 Execute      │   helpers, partials}  (immutable)│
              └──────────────┬───────────────────┘
                             ▼ per call
              ┌──────────────────────────────────┐
              │  exec_state.go: state{ctx, w,    │
              │   fuel, subs, ctxStack, frame…}  │
              │  render.go: type-switch walker   │──── error returns
              │  builtins.go: streaming if/each… │
              └───────┬──────────────┬───────────┘
                      ▼              ▼
        ┌───────────────────┐  ┌──────────────────────────┐
        │ value.go (closed) │  │ writer chain:            │
        │ Value/Data/List/  │  │ escapeWriter→indentWriter│
        │ Iterable          │  │ →cappedWriter→destWriter │
        └─────────▲─────────┘  │ →user io.Writer          │
                  │            └──────────────────────────┘
        ┌─────────┴─────────┐
        │ adapt.go (reflect │   ◀── ONLY place reflection lives
        │ quarantine):      │       (with adapt_helpers.go,
        │ any → Value       │        string.go, utils.go)
        └───────────────────┘
```

`ast/`, `lexer/`, `parser/whitespace.go`, `escape.go` (already
writer-based), `data_frame.go`, `partial.go`, and the entire
`ParseOptions` capability/budget subsystem are reused as-is. The parser
gains in-parser node/depth counting (`parser/limits.go`,
`parser.ParseWithLimits`). `eval.go` is deleted at the end.

## 5. Components

### 5.1 Public API (new)

```go
type Limits struct {
    // Parse-time (consumed by Compile)
    MaxTemplateSize int // bytes of source; 0 = unlimited
    MaxNodes        int // AST nodes; fails at node N+1 inside the parser
    MaxDepth        int // nesting depth (programs + subexpressions)

    // Execution-time (consumed by Execute)
    MaxOutputBytes   int64 // bytes delivered to w
    MaxSubstitutions int64 // mustache substitutions rendered
    MaxSteps         int64 // CPU fuel
}

func Compile(source string, limits Limits) (*Compiled, error)

func (c *Compiled) RegisterHelper(name string, h Helper)
func (c *Compiled) RegisterPartial(name string, p *Compiled)
func (c *Compiled) Execute(ctx context.Context, w io.Writer, data any) error      // adapter
func (c *Compiled) ExecuteData(ctx context.Context, w io.Writer, data Data) error // closed, zero reflect

var (
    ErrTemplateTooLarge   = errors.New(...)
    ErrTemplateTooComplex = errors.New(...)
    ErrOutputLimit        = errors.New(...)
    ErrSubstitutionLimit  = errors.New(...)
    ErrStepLimit          = errors.New(...)
)

type LimitError struct{ Kind string; Limit int64 } // Unwrap() → matching sentinel
```

`Compile` (not `Parse` — taken by the legacy API) takes `Limits` by value,
not variadic options: budgets are the point of this API; `Limits{}` means
unlimited and the call site shows it.

### 5.2 Closed data model (`value.go`, no reflect import)

```go
type Kind uint8 // Invalid, String, SafeString, Bool, Int, Uint, Float,
                // List, Map, Func, Opaque

type Value struct {
    kind  Kind
    truth bool      // truthiness precomputed by constructor/adapter
    // scalar payloads...
    list  List
    data  Data
    fn    coreHelper
    raw   any       // ORIGINAL Go value — legacy Options round-trip
}

func (v Value) Kind() Kind
func (v Value) Truthy() bool
func (v Value) Interface() any  // returns raw: options.Param(0).(int) keeps working
func (v Value) Str() string     // mirrors legacy strValue kind-by-kind

type Data interface {
    // ok reports whether the FIRST path part resolved in this container
    // (even if deeper parts then failed) — this bit drives handlebars'
    // parent-context fallback semantics ("Dotted Names - Context
    // Precedence" in the mustache corpus).
    Lookup(path []string) (Value, bool)
}

type List interface { Len() int; Index(i int) Value }
type Iterable interface { Len() int; Each(fn func(key, val Value) error) error } // #each
```

### 5.3 Reflection adapter (`adapt.go`, `adapt_helpers.go`)

`adaptValue(any) Value` reproduces `evalField`'s exact resolution order:
indirect through pointers/interfaces → `MethodByName(name)` then
`MethodByName(strings.Title(name))` (with `Addr()` when addressable) →
exported field `strings.Title(name)` → `handlebars:` struct-tag scan.
Funcs (map values, methods, methods returning funcs) become `KindFunc`
wrapping the legacy helper bridge — this is how mustache lambdas and
handlebars functions-as-values work unchanged. Float32 widens to float64;
`SafeString` maps to `KindSafeString`; maps use exact-key assignability as
today.

`adapt_helpers.go` ports `callFunc`'s argument count/type checking and
string/bool coercion verbatim, but **returns errors instead of
panicking**, with byte-identical messages (tests substring-match
`"Helper 'foo' called with wrong number of arguments, needed 2 but got
1"`, `"Helper function must return a string or a SafeString"`).

### 5.4 Execution state and walker (`exec_state.go`, `render.go`)

A per-call `state` carries: ctx, current writer, outermost `cappedWriter`
(if any), limits, fuel/substitution counters, amortized ctx-check
threshold, adapter seam, helper/partial resolvers (snapshots), context
stack (`[]Value`), `DataFrame`, block params, enclosing blocks (for legacy
`Fn()`), and the `exprFunc` memo.

```go
func (s *state) step(n int64) error {
    s.steps += n
    if s.limits.MaxSteps > 0 && s.steps > s.limits.MaxSteps { return ...ErrStepLimit }
    if s.steps >= s.nextCtxCheck {            // every 1024 steps
        s.nextCtxCheck = s.steps + ctxCheckInterval
        if err := s.tctx.Err(); err != nil { return err }
    }
    return nil
}
```

The walker is a **type switch over `ast.Node`**, not the existing
`ast.Visitor` interface — `Accept(Visitor) interface{}` fights `error`
returns and forces boxing. The `ast` package is untouched; the visitor
stays for `ast.Print` and the `ParseOptions` cap visitor.

Charging: 1 step per statement, 1 per expression/path eval, 1 per block
iteration, and writes charge `1 + len/256` so a `repeat`-style helper pays
proportional to output, not per call.

### 5.5 Writer chain (no strings anywhere)

- `cappedWriter` — existing, test-pinned; kept. `errBudgetOverflow` is
  redefined to wrap `ErrOutputLimit` (identity `errors.Is` still passes).
  Gains `WriteString` (prefix-slicing, never materializes the payload —
  this is what keeps the 10 MiB-literal-under-1 MiB-budget test under its
  4 MiB heap delta).
- `destWriter` — new; innermost wrapper around the user's writer, tags
  failures and short-writes as `*destError{cause}` so the shim can
  distinguish destination failures (`RenderDestinationError`) from render
  errors.
- `indentWriter` — new; streaming replacement for `indentLines` in partial
  rendering: lazily emits the indent after each `\n`, no indent on the
  empty tail after a final newline. Stacks for nested partials.
- `escapeWriter` — wraps `escape.go`'s existing writer-based escaper for
  streaming-helper output in escaped-mustache position.

### 5.6 Dual helper API

**New streaming helpers** (`helper_call.go`):

```go
type Helper interface{ CallHelper(hc *HelperCall) error }
type HelperFunc func(hc *HelperCall) error

// HelperCall: Context(), budget-charged Write/WriteString/WriteSafe,
// Param/Hash/Lookup/Data/Ctx returning Value, Fn()/FnWith()/Inverse()
// streaming the block body, NumParams.
```

**Legacy helpers** keep their exact semantics. `RegisterHelper(name,
interface{})` dispatches: `Helper` / `func(*HelperCall) error` →
streaming; anything else → legacy reflect path (validated and panicking
at registration as today). Builtins (`if`, `unless`, `with`, `each`,
`log`, `lookup`, `equal`) are rewritten as streaming helpers and live in
the global registry via `init()`, so duplicate-registration panics and
`RemoveHelper("if")` shadowing behave exactly as today.

**Legacy `Options.Fn() string` bridging:** `Fn()` renders the block into a
capture buffer wrapped in a `cappedWriter` bounded by the *remaining*
global output budget — speculative helper buffers can never exceed
`budget + O(1)` memory. Fuel, substitutions, and ctx checks keep running
inside captures (shared `state`). Because `Fn()` has no error channel,
`Options` records the first failure in an `err` field
(record-and-continue: later `Fn*/Inverse` return `""` immediately) and the
bridge surfaces it after the helper returns.

### 5.7 Legacy shims (`template.go`)

Everything funnels through one private method:

```go
func (tpl *Template) execute(ctx context.Context, w io.Writer,
    cap *cappedWriter, data any, priv *DataFrame, limits Limits) error
```

- `Exec/ExecWith` → `strings.Builder` sink, `context.Background()`, no
  caps; return built string or error. No `errRecover`.
- `ExecToWithOptions` → pinned semantics reproduced exactly:
  `Enforced && MaxOutputBytes < 0` rejected before any write with
  `&RenderBudgetExceededError{Limit: -1}`; `Enforced` wraps the
  destination in `newCappedWriter` (including the legal `Enforced`+0
  case); error mapping `ErrOutputLimit → *RenderBudgetExceededError`,
  `*destError → *RenderDestinationError` (short writes pre-converted to
  `io.ErrShortWrite`). "≤ budget bytes delivered" holds structurally —
  `cappedWriter` only ever forwards the fitting prefix.
- `ParseOptions`' static capability/budget visitor is **orthogonal** to
  the new `Limits` (post-parse static counting vs in-parser structural vs
  runtime-dynamic) and stays byte-for-byte unchanged, as do `Report()`,
  all four existing error types, `Str`, `IsTrue`, `Escape`, `SafeString`,
  `DataFrame`, and the partial registries.

## 6. Data flow (one render)

1. Shim or caller builds `state` with adapter seam, helper/partial
   snapshots, writer chain, and limits.
2. `renderProgram` walks `Program.Body`; each statement charges fuel and
   dispatches by type.
3. Mustache: evaluate expression → `Value`; SafeString/unescaped →
   `io.WriteString`; else `escape(w, str)`; charge one substitution.
4. Block: evaluate; helper or function-as-value result *is* the block
   output; otherwise truthy → Program (arrays iterate with per-element
   `DataFrame`s), falsy → Inverse.
5. Path resolution walks the context stack with the *partResolved* rule
   encoded in `Lookup`'s `ok` bit; `@data` paths resolve through the
   `DataFrame` chain; depth (`../`) indexes up the stack.
6. Partials resolve to a compiled program, wrap the writer in an
   `indentWriter`, and recurse.
7. First non-nil error aborts the walk and propagates up; shims map it to
   legacy typed errors.

## 7. Error handling

| Failure | Core representation | Legacy surface |
|---|---|---|
| Output budget breach | `errBudgetOverflow` wrapping `ErrOutputLimit` | `*RenderBudgetExceededError` |
| Destination write failure / short write | `*destError{cause}` | `*RenderDestinationError` (wraps cause) |
| Fuel / substitutions / ctx | `*LimitError` → sentinels; `ctx.Err()` | new API only (legacy paths set no such limits) |
| Template runtime errors (missing partial, bad helper args…) | plain `error`, identical message text | unchanged strings |
| Parse structural limits | `parser.LimitError` → `ErrTemplateTooComplex` | new API only |
| Registration misuse, `Must*` | panic (explicit, unchanged) | unchanged |

## 8. Testing

- **Oracle:** the existing 66-function suite, unchanged, green at every
  milestone.
- **Parity harness** (during the alongside phase): every `evalTests` and
  mustache-corpus case rendered through both engines, outputs diffed.
- **Adapter parity:** `Str(x) == adaptValue(x).Str()` and
  `IsTrue(x) == adaptValue(x).Truthy()` over a corpus of scalars, slices,
  maps, structs, pointers, SafeStrings, funcs.
- **New-API tests:** `Compile` limit off-by-ones; ctx cancellation
  mid-`each` aborts within one check interval; `MaxSteps` exhaustion
  (incl. helper-amplified); `MaxSubstitutions`/`MaxOutputBytes` exact
  boundaries; 32 goroutines sharing one `*Compiled` under `-race`.
- **Quarantine test:** asserts `"reflect"` is imported only by the
  adapter/legacy files.
- Benchmarks against `BENCHMARKS.md` baseline (a concat-free core should
  win, not regress).

## 9. Implementation milestones (suite green after each)

| # | Milestone | Status |
|---|---|---|
| M0 | `parser/limits.go` — in-parser node/depth counting + tests | done on branch |
| M1 | `value.go`, `limits.go`, `adapt.go` + parity tests (old engine still serves everything) | pending |
| M2 | Full new core alongside old engine + parity harness — bulk of the work | pending |
| M3 | Flip `ExecWith` (→ `Exec`, `MustExec`, `Render`, both compat suites); `Options` over `state` | pending |
| M4 | Flip `ExecTo*` family; `destWriter` + error mapping, byte-exact budget tests | pending |
| M5 | Delete `eval.go`, `errRecover`, `indentLines`, dead reflect paths | pending |
| M6 | Hardening tests: ctx/fuel/race/quarantine | pending |

## 10. Risks

1. **`Fn() string` has no error channel** → record-and-continue
   (`options.err`); captures fail fast once budget/fuel is gone. Go user
   code can't be preempted — true today too.
2. **Path fallback semantics** (*partResolved*, depth walking,
   array-context mapping, `@root` ctx-then-data order) — port
   `evalDepthPath` line-by-line; the mustache corpus is the net.
3. **Funcs-as-values vs blocks** (`exprFunc` memo, method-before-field
   order, `CanAddr→Addr`) — easy to subtly invert.
4. **Capture budgets + partial indentation** — capture buffers bounded by
   remaining global cap (heap test); `indentWriter` must match
   `indentLines` exactly including the no-indent-on-empty-tail rule.
5. **`Str()` fidelity** — float formatting, list concat, `fmt.Stringer`/
   `error` promotion incl. pointer receivers; error strings are
   substring-matched by tests, so messages port verbatim.
