# Phase 1 Data Model: Render Output Budget via Capped Writer

This feature adds a small number of types to the root `raymond`
package. No types are removed or renamed. No fields are added to
existing exported types.

---

## Exported types

### `RenderOptions` (new)

```go
// RenderOptions configures per-call render behaviour for the
// streaming Exec entry points (ExecTo, ExecToWith, ExecToWithOptions).
//
// The zero value is documented as "legacy behaviour": no budget
// tracking is performed and the rendered bytes flow to the writer
// unmodified. This is what makes ExecTo / ExecToWithOptions safe
// drop-in replacements for callers that want streaming without yet
// caring about budgets.
type RenderOptions struct {
    // MaxOutputBytes is the strict upper bound on the cumulative
    // number of bytes delivered to the destination writer.
    // Consulted only when Enforced is true.
    //
    // Semantics:
    //   - bytes-written == MaxOutputBytes  → success (exact-fit)
    //   - bytes-written  > MaxOutputBytes  → *RenderBudgetExceededError
    //
    // A value of 0 with Enforced == true is a legal, meaningful
    // configuration: any render that would produce one or more
    // output bytes fails; a render that produces zero bytes
    // succeeds.
    MaxOutputBytes int64

    // Enforced toggles output-byte budget enforcement. When false,
    // MaxOutputBytes is ignored and the render streams to the
    // destination without a cap.
    Enforced bool
}
```

**Validation rules**:

- `MaxOutputBytes` MUST be `>= 0` when `Enforced` is `true`. A
  negative value is rejected at the call site by returning a
  non-`nil` error from `ExecToWithOptions` immediately, before any
  bytes reach the writer. The error is a `*RenderBudgetExceededError`
  with `Limit` set to the offending negative value, so operators do
  not need a third error category to handle this.
- No other fields exist; the struct is shaped for forward
  compatibility but other axes are out of scope for this feature.

**State**: stateless (value type, copied at the call boundary).

---

### `RenderBudgetExceededError` (new)

```go
// RenderBudgetExceededError reports that a render call exceeded its
// configured output-byte budget. It is returned by the streaming
// Exec entry points when the cumulative bytes produced by the
// render strictly exceed RenderOptions.MaxOutputBytes.
//
// Operators can identify this error programmatically with errors.As:
//
//     var bex *raymond.RenderBudgetExceededError
//     if errors.As(err, &bex) { ... }
type RenderBudgetExceededError struct {
    // Kind is the budget axis that was exceeded. For this feature
    // the only value is "output bytes".
    Kind string

    // Limit is the configured MaxOutputBytes value at the time of
    // the call. Useful for metrics and error messages.
    Limit int64
}

func (e *RenderBudgetExceededError) Error() string
```

**Constraints**:

- `Kind` is always `"output bytes"` for renders produced by this
  feature.
- `Limit` echoes the value the caller configured.

---

### `RenderDestinationError` (new)

```go
// RenderDestinationError reports that the operator-supplied
// destination returned an error or a short write during rendering.
// The render aborts at the point of the destination's failure.
//
// The underlying cause is wrapped; errors.Is and errors.As work
// transparently:
//
//     if errors.Is(err, io.ErrShortWrite) { ... }
type RenderDestinationError struct {
    Cause error
}

func (e *RenderDestinationError) Error() string
func (e *RenderDestinationError) Unwrap() error
```

**Constraints**:

- `Cause` is never `nil` for an error returned to the caller.
- A `*RenderDestinationError` is never returned for a budget
  overflow; those go through `*RenderBudgetExceededError` exclusively
  (FR-009, SC-003).

---

## Unexported types

### `cappedWriter` (new, internal)

```go
type cappedWriter struct {
    dst     io.Writer
    limit   int64 // strict upper bound; bytes-written must not exceed limit
    written int64
}

func newCappedWriter(dst io.Writer, limit int64) *cappedWriter
func (cw *cappedWriter) Write(p []byte) (int, error)
```

**State transitions** (per Write call):

1. `remaining := cw.limit - cw.written`
2. If `len(p) <= remaining`: forward whole `p`, update `written`,
   return whatever the underlying writer returned.
3. If `len(p) > remaining` and `remaining > 0`: forward exactly
   `p[:remaining]`, update `written`, return `(n, errBudgetOverflow)`.
4. If `len(p) > 0` and `remaining == 0`: write nothing, return
   `(0, errBudgetOverflow)`.
5. If `len(p) == 0`: write nothing, return `(0, nil)`.

**Sentinel**: `var errBudgetOverflow = errors.New("raymond: render
output budget exceeded")` — unexported; never escapes to callers.

---

## Modified types

### `evalVisitor` (gains two unexported fields)

```go
type evalVisitor struct {
    // ... existing fields unchanged ...

    // out, when non-nil, indicates a streaming exec (ExecTo / ExecToWithOptions).
    // The capped writer counts bytes delivered to the operator's
    // destination; nil on the legacy Exec path.
    out *cappedWriter

    // committed mirrors out.written at sub-program boundaries so
    // that nested concatenations can short-circuit the moment
    // committed + len(currentResult) > limit.
    // Zero on the legacy Exec path.
    committed int64
}
```

These fields are populated only by the new `ExecTo*` entry points.
The legacy `ExecWith` path leaves them zero, and every
budget-related branch in the visitor is guarded by `v.out != nil`,
so legacy renders run identical code paths to today (FR-007, SC-005).

### `*Template` (no fields added)

The new methods are pure additions; no struct fields change.

---

## Relationships

```
                    +---------------------+
   caller --------> |   (*Template)       |
                    |   ExecToWithOptions |
                    +----------+----------+
                               |
                               v
                    +---------------------+
                    | newCappedWriter     |
                    | (cappedWriter)      |
                    +----------+----------+
                               |
                               v
                    +---------------------+
                    | evalVisitor         |
                    | (.out, .committed)  |
                    +----------+----------+
                               |
                               v
                    +---------------------+
                    | program.Body        |
                    | iterated, each      |
                    | statement streamed  |
                    | to cappedWriter     |
                    +----------+----------+
                               |
              errBudgetOverflow|writer error
                               v
                    +---------------------+
                    | *RenderBudget…Error |
                    | *RenderDest…Error   |
                    +---------------------+
```

A render call produces **one** capped writer, **one** evaluator, and
at most **one** terminal error. The capped writer is the sole
chokepoint for the byte count.
