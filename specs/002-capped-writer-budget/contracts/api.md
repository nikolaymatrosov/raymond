# API Contract: Render Output Budget via Capped Writer

This document is the public Go API contract for feature 002. Each
section pairs the API addition with the failing test it must satisfy
(per Constitution Principle III, Test-First Development). All tests
live in the root `raymond` package.

---

## 1. `RenderOptions` (new)

### Signature

```go
package raymond

type RenderOptions struct {
    MaxOutputBytes int64
    Enforced       bool
}
```

### Tests (`render_options_test.go`)

- **TestRenderOptions_ZeroValueIsLegacy**: a zero-value
  `RenderOptions{}` passed to `ExecToWithOptions` MUST produce
  byte-for-byte identical output to `ExecTo` with the same template
  and context, AND identical to the legacy `Exec`'s string output
  (FR-007, SC-005).
- **TestRenderOptions_EnforcedZeroBudget**: `RenderOptions{
  MaxOutputBytes: 0, Enforced: true}` rejects any render that would
  produce one or more bytes; an empty-output template succeeds
  (FR-011).
- **TestRenderOptions_NegativeBudgetRejected**: `RenderOptions{
  MaxOutputBytes: -1, Enforced: true}` returns
  `*RenderBudgetExceededError` with `Limit == -1` *before* writing
  any bytes to the destination.

---

## 2. `RenderBudgetExceededError` (new)

### Signature

```go
package raymond

type RenderBudgetExceededError struct {
    Kind  string
    Limit int64
}

func (e *RenderBudgetExceededError) Error() string
```

### Tests (`render_errors_test.go`)

- **TestRenderBudgetExceededError_FieldsPopulated**: on overflow,
  `Kind == "output bytes"` and `Limit` equals the configured
  `MaxOutputBytes` (FR-004).
- **TestRenderBudgetExceededError_IdentifiableViaErrorsAs**: the
  returned error matches `errors.As(err, &*RenderBudgetExceededError
  {})` and does NOT match for a non-budget render failure (SC-003).
- **TestRenderBudgetExceededError_DistinctFromDestinationError**: a
  destination I/O failure does NOT match
  `*RenderBudgetExceededError` (FR-009).

---

## 3. `RenderDestinationError` (new)

### Signature

```go
package raymond

type RenderDestinationError struct {
    Cause error
}

func (e *RenderDestinationError) Error() string
func (e *RenderDestinationError) Unwrap() error
```

### Tests (`render_errors_test.go`)

- **TestRenderDestinationError_WrapsCause**: `errors.Is(err,
  io.ErrShortWrite)` is true when the destination returns a short
  write; `errors.Is(err, fooErr)` is true when the destination's
  `Write` returns `fooErr`.
- **TestRenderDestinationError_DistinctFromBudgetError**: a budget
  overflow does NOT match `*RenderDestinationError` (FR-009).

---

## 4. `(*Template).ExecTo` (new)

### Signature

```go
func (tpl *Template) ExecTo(w io.Writer, ctx interface{}) error
```

### Behaviour

Equivalent to `ExecToWithOptions(w, ctx, nil, RenderOptions{})`.
Streams rendered bytes to `w` without a budget. Returns the error
returned by the writer, wrapped in `*RenderDestinationError`, if the
writer fails.

### Tests (`exec_to_test.go`)

- **TestExecTo_StreamsBytes**: rendering a template into a
  `*bytes.Buffer` via `ExecTo` produces the same bytes as
  `(*Template).Exec` returns as a string (Story 2 acceptance #1).
- **TestExecTo_DestinationWriteFailure**: a writer whose `Write`
  returns a custom error causes `ExecTo` to return a
  `*RenderDestinationError` whose `Unwrap()` is that error (Story 2
  acceptance #3).

---

## 5. `(*Template).ExecToWith` (new)

### Signature

```go
func (tpl *Template) ExecToWith(w io.Writer, ctx interface{}, privData *DataFrame) error
```

### Behaviour

Equivalent to `ExecToWithOptions(w, ctx, privData, RenderOptions{})`.

### Tests (`exec_to_test.go`)

- **TestExecToWith_DataFramePropagated**: a `*DataFrame` containing
  a private value is visible inside the template via `@key` exactly
  as it would be from `ExecWith`.

---

## 6. `(*Template).ExecToWithOptions` (new)

### Signature

```go
func (tpl *Template) ExecToWithOptions(
    w io.Writer,
    ctx interface{},
    privData *DataFrame,
    opts RenderOptions,
) error
```

### Behaviour

- Streams rendered bytes to `w` as they are produced.
- When `opts.Enforced` is true, enforces `opts.MaxOutputBytes` as a
  strict upper bound: `bytes-written > limit` ⇒ abort with
  `*RenderBudgetExceededError`; `bytes-written == limit` ⇒ success.
- Bytes emitted by helpers, partials, inline partials, and nested
  template invocations all count against the same budget (FR-008).
- Each call carries its own budget state (FR-010).
- A destination `Write` returning `(n < len(p), nil)` or any non-nil
  error is surfaced as `*RenderDestinationError` (FR-009).
- Returns `nil` on success.

### Tests (`exec_to_test.go`)

- **TestExecToWithOptions_ExactFitSucceeds**: budget = N, output =
  N → returns nil; destination receives all N bytes (FR-006, edge
  case "Exact-fit boundary").
- **TestExecToWithOptions_OneByteOverFails**: budget = N, output =
  N+1 → returns `*RenderBudgetExceededError`; destination receives
  exactly N bytes (FR-005, Story 1 acceptance #2).
- **TestExecToWithOptions_LargeLiteralEarlyAbort**: a template with
  a 10 MiB literal section under a 1 MiB budget aborts before fully
  writing the literal; destination receives at most 1 MiB; total
  buffer memory (measured via runtime.MemStats delta) is bounded by
  budget + O(1) (Story 1 acceptance #3, SC-001).
- **TestExecToWithOptions_HelperEmittedBytesCount**: a helper that
  writes 2 KiB into the rendered output under a 1 KiB budget aborts
  with `*RenderBudgetExceededError` (FR-008, edge case "Helpers that
  emit output").
- **TestExecToWithOptions_PartialBytesCount**: a partial expansion
  whose total emitted bytes exceed the budget aborts; there is no
  per-partial sub-budget (FR-008, edge case "Partials and nested
  templates").
- **TestExecToWithOptions_ConcurrentRendersIndependent**: two
  goroutines rendering the same template under different budgets
  produce results consistent with their own budgets, with no
  cross-talk (FR-010, edge case "Concurrent renders").
- **TestExecToWithOptions_NotEnforced_NoTracking**: with
  `Enforced: false`, an output of 100 MiB succeeds and the
  destination receives all 100 MiB (FR-007).
- **TestExecToWithOptions_UTF8AtBoundary**: a template producing
  multi-byte UTF-8 with a budget that falls mid-codepoint aborts at
  the byte boundary; destination may end mid-codepoint; error is
  authoritative (edge case "Multi-byte UTF-8 sequences at the
  boundary").
- **TestExecToWithOptions_NoPanicOnAdversarialInput**: a fuzz-style
  smoke test that runs ~1000 randomised templates+contexts under
  random budgets and asserts no test ever produces a Go runtime
  panic (Constitution Principle I, Resource Budget Standards
  "Failure mode: Budget exhaustion MUST NOT be enforced by panic").

---

## 7. Internal: `cappedWriter`

Unexported. Tests live in `capped_writer_test.go` and verify the
state-transition table from `data-model.md` exhaustively:

- exact-fit, off-by-one over, off-by-one under
- multi-Write accumulating to limit
- multi-Write where second Write straddles the limit
- zero-budget rejects non-empty Write, accepts empty Write
- writer error from underlying `dst` is surfaced (not converted to
  the budget sentinel)
- short write (`n < len(p), nil`) from underlying `dst` is surfaced

---

## 8. Unchanged surface

The following exported identifiers MUST keep their current
signatures and observable behaviour. The compatibility test suites
(`handlebars/`, `mustache/`, root `*_test.go`) MUST continue to
pass byte-for-byte:

- `func Render(source string, ctx interface{}) (string, error)`
- `func MustRender(source string, ctx interface{}) string`
- `func Parse(source string) (*Template, error)`
- `func MustParse(source string) *Template`
- `func ParseFile(filePath string) (*Template, error)`
- `func ParseWithOptions(source string, opts ParseOptions) (*Template, error)` *(feature 001)*
- `func (tpl *Template) Exec(ctx interface{}) (string, error)`
- `func (tpl *Template) MustExec(ctx interface{}) string`
- `func (tpl *Template) ExecWith(ctx interface{}, privData *DataFrame) (string, error)`
- `func (tpl *Template) Report() ParseReport` *(feature 001)*
- `func (tpl *Template) RegisterHelper(...)` and friends
- `func (tpl *Template) RegisterPartial(...)` and friends
- `func (tpl *Template) Clone() *Template`
- `func (tpl *Template) PrintAST() string`
- `BudgetExceededError`, `CapabilityError`, `ParseOptions`,
  `ParseReport`, `Mode`, `Capabilities`, `Budget` *(feature 001)*

`renderVisitor` is internal and may be modified.
