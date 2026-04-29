# Phase 0 Research: Render Output Budget via Capped Writer

This document records the technical decisions taken before design.
Each entry resolves a question raised by the spec or by the
"NEEDS CLARIFICATION" placeholders in the plan template's Technical
Context.

---

## R1. Destination type: introduce a new abstraction or reuse `io.Writer`?

**Decision**: Reuse `io.Writer` from the standard library as-is.

**Rationale**:

- The spec's Assumptions section calls for "whatever sink the host
  language conventionally uses for streaming bytes (the standard
  byte-stream abstraction)". In Go, that is unambiguously `io.Writer`.
- Every plausible destination an operator already has (HTTP response
  writers, `*os.File`, `net.Conn`, `bytes.Buffer`, gzip writers,
  `io/ioutil.Discard`, etc.) implements `io.Writer`. Introducing a
  bespoke `RenderSink` interface would force operators to write
  adapters and would not interoperate with their existing pipelines.
- `io.Writer`'s `(int, error)` return is a perfect fit for the spec's
  edge case "destination accepts the write but reports fewer bytes
  written than requested" → that is `io.ErrShortWrite` territory.

**Alternatives considered**:

- *New `RenderSink` interface*: rejected — gratuitous; no behaviour
  needed beyond `Write`.
- *Pass a `*bufio.Writer`*: rejected — over-specifies; an operator who
  wants buffering can wrap the writer themselves; an operator who
  passes an HTTP response writer doesn't want a hidden buffer between
  them and the wire.

---

## R2. How to enforce the budget without first materialising the full output?

**Decision**: Wrap the operator's `io.Writer` in an internal
`cappedWriter` that:

1. Counts cumulative bytes accepted.
2. Before delegating each `Write`, computes `remaining := limit - written`.
3. If the incoming chunk fits within `remaining`, forwards it whole
   and updates the count.
4. If the chunk would push the total *strictly above* `limit`,
   forwards exactly `remaining` bytes (so the destination receives the
   budget's worth and not one byte more — FR-005), then returns
   `(remaining_written, errBudgetOverflow)` where `errBudgetOverflow`
   is an unexported sentinel.
5. Surfaces destination errors as-is from the underlying `Write`.

The evaluator translates `errBudgetOverflow` into a typed
`*RenderBudgetExceededError`; any other error from the writer becomes
a `*RenderDestinationError` wrapping the cause.

**Rationale**:

- This decouples budget enforcement from the evaluator: every byte
  reaches the destination through one funnel, so there is exactly one
  place the limit is checked. Helpers, partials, nested templates,
  and literal content all funnel through the same writer (FR-008 — no
  per-construct sub-budgets).
- The exact-fit boundary (`bytes-written > limit`, not `≥`) is a
  one-line invariant in the wrapper (FR-006, edge case "Exact-fit
  boundary").
- Zero-budget is a degenerate case that falls out for free: with
  `limit == 0`, any non-empty write is rejected; an empty render
  performs no writes and succeeds (FR-011, edge case "Output budget
  of zero").

**Alternatives considered**:

- *Count bytes inside the evaluator*: rejected — would require
  threading the counter through every call site that produces bytes
  (literal content, escaped substitutions, helper return values,
  partial expansion); easy to miss a path. Centralising in the writer
  is the smallest correct surface.
- *Pre-compute output size from AST + context*: impossible in general
  — output size depends on context data, helper return values, and
  iteration counts. Not pursued.

---

## R3. How to keep peak memory bounded by the budget (SC-001)?

**Decision**: Stream each **top-level program statement** to the
capped writer as soon as it is evaluated. Inside sub-programs (block
helpers, partials), keep the existing string-returning `Accept`
contract — but make `evalVisitor` track committed-bytes-so-far and
short-circuit (via the existing `errRecover` panic/recover plumbing)
the moment a sub-program's accumulated string would, when added to
the bytes already committed to the writer, strictly exceed the
budget.

Concretely:

- `(*Template).ExecToWithOptions` becomes the new top-level driver.
  Instead of doing `program.Accept(v).(string)` in one shot, it
  iterates `program.Body` and, for each statement, computes its
  string fragment via `statement.Accept(v).(string)` and immediately
  writes that fragment to the capped writer.
- `evalVisitor` gains two unexported fields: `out *cappedWriter` and
  `committed int64` (bytes already flushed). These are only set on
  the `ExecTo*` paths; on the legacy `Exec` path they stay zero and
  the visitor behaves identically (FR-007, SC-005).
- Inside `evalProgram` (used for sub-programs called by helpers),
  after each appended statement string the visitor checks
  `committed + len(currentResult) > limit` and panics with the
  internal `errBudgetOverflow` sentinel if so. `errRecover` catches
  it and returns it as `*RenderBudgetExceededError`. This bounds the
  in-process buffer for any sub-program at roughly `limit` bytes
  (Story 1, SC-001).

**Rationale**:

- The spec's most demanding scenario is Story 1 acceptance #3: a
  10 MiB literal at the top of a template under a 1 MiB budget MUST
  abort before the 10 MiB is fully emitted, with total work bounded
  by `budget + O(1)`. Streaming top-level statements achieves this
  for the literal-content case (a `ContentStatement` is one statement
  at the top level — once `cappedWriter` rejects it, evaluation
  unwinds).
- Helpers (especially `{{#each}}` over large collections) call
  `options.Fn()` which evaluates a sub-program and returns its
  rendered string. The helper API contract relied on by every
  pre-existing helper in handlebars/mustache test suites returns a
  `string`, so changing `Fn()` to stream is a Principle II
  (back-compat) violation. The accepted compromise: the sub-program
  string can grow up to `~limit` bytes in memory, but no further —
  the moment its size + already-committed bytes exceeds `limit`, the
  visitor panics and we abort. Peak memory is therefore bounded by
  `limit + O(1)`, which satisfies SC-001 ("bounded by a small
  constant, independent of the would-be output size") because the
  budget itself is the operator's chosen small constant.
- The legacy `Exec` path runs none of this bookkeeping (`out == nil`
  ⇒ no checks), so byte-for-byte parity and zero perf regression
  (SC-005) are preserved.

**Alternatives considered**:

- *Stream from inside helpers via a new `Options.Writer` field*:
  rejected — would change the helper contract and risk silently
  breaking third-party helpers, violating Principle II. Out of scope
  for this feature; could be a follow-up.
- *Buffer-then-flush only at the root*: rejected — would let a 10 MiB
  literal materialise in full before being checked, violating
  SC-001 / Story 1 acceptance #3.

---

## R4. Error taxonomy: how do operators tell budget overflow apart from destination failures?

**Decision**: Introduce two new exported error types in the root
package:

```go
type RenderBudgetExceededError struct {
    Kind  string // always "output bytes" for this feature
    Limit int64  // the configured MaxOutputBytes
}

type RenderDestinationError struct {
    Cause error // the writer's own error (io.ErrShortWrite, *os.PathError, etc.)
}
```

Both implement `error`. `RenderDestinationError` implements
`Unwrap() error` so `errors.Is`/`errors.As` work transparently
against the underlying cause. Operators distinguish them with
`errors.As(err, &*RenderBudgetExceededError{})` (FR-004, SC-003).

**Rationale**:

- Spec Story 3 and FR-004/FR-009 require *programmatic*
  distinguishability that does not depend on parsing error text.
  Distinct exported types are the canonical Go idiom.
- `Kind` field is forward-compatible: future render-time budget axes
  (eval steps, recursion depth, wall-clock) can reuse the same type
  with a different `Kind` value rather than introducing a new error
  type per axis. Mirrors the design of `BudgetExceededError` in
  feature 001.
- Wrapping the cause (rather than swallowing it) lets operators write
  `errors.Is(err, io.ErrShortWrite)` for the short-write edge case
  without introducing a third error type.

**Alternatives considered**:

- *Single error type with a `Reason` enum*: rejected — `errors.As`
  ergonomics are worse and it conflates two genuinely different
  failure modes (caller's quota vs. external system).
- *Sentinel errors only*: rejected — operators need the `Limit`
  field for their own observability/metrics.

---

## R5. Where does `RenderOptions` live, and what fields does it carry now?

**Decision**: New file `render_options.go` in the root package:

```go
type RenderOptions struct {
    // MaxOutputBytes is the strict-> upper bound on bytes delivered
    // to the destination. Only consulted when Enforced is true.
    MaxOutputBytes int64

    // Enforced toggles output-byte budget enforcement. The explicit
    // boolean (rather than treating MaxOutputBytes == 0 as "off")
    // lets a budget of zero remain a legal, meaningful configuration
    // (FR-011).
    Enforced bool
}
```

**Rationale**:

- Mirrors the shape of `ParseOptions` from feature 001 (an `Enforced`
  flag separate from the numeric limit). Avoids the
  `0-means-disabled` ambiguity that the spec explicitly calls out as
  a supported configuration in FR-011 / edge case "Output budget of
  zero".
- Keeping the struct small and additive leaves room for future
  render-time axes without an API break — they will be appended as
  new fields with their own `Enforced`-style toggles.

**Alternatives considered**:

- *Use `*int64` (nil = disabled)*: rejected — slightly more allocation
  pressure on the hot path and a less idiomatic Go API than a
  zero-valued struct + bool.

---

## R6. Naming the new methods on `*Template`

**Decision**:

- `func (tpl *Template) ExecTo(w io.Writer, ctx interface{}) error`
- `func (tpl *Template) ExecToWith(w io.Writer, ctx interface{}, privData *DataFrame) error`
- `func (tpl *Template) ExecToWithOptions(w io.Writer, ctx interface{}, privData *DataFrame, opts RenderOptions) error`

`ExecTo` is the budget-less convenience wrapper (just stream to a
writer with no cap). `ExecToWith` adds the existing `*DataFrame`
extension. `ExecToWithOptions` is the canonical entry point that
takes everything.

**Rationale**:

- Mirrors the existing pair `Exec` / `ExecWith` and the feature-001
  pair `Parse` / `ParseWithOptions`. A reader who knows one knows
  the other.
- Three methods rather than one keep the common case (just stream)
  one call away — operators who don't yet care about the budget can
  migrate from `Exec` to `ExecTo` for the streaming benefit alone
  (Story 2) without learning the options struct.

**Alternatives considered**:

- *One method `ExecToWithOptions` only*: rejected — forces every
  caller to construct an empty `RenderOptions{}` and pass `nil` for
  `privData`, which is noisier than the existing `Exec` pair.
- *Reuse `Exec` with a variadic `...RenderOption` functional-options
  pattern*: rejected — incompatible with Principle II (would change
  the `Exec` signature, breaking every downstream caller's code that
  takes `(*Template).Exec` as a method value).

---

## R7. Multi-byte UTF-8 at the budget boundary

**Decision**: The budget is measured in bytes, not runes. The capped
writer truncates exactly at the byte boundary defined by the limit;
the partial content delivered to the destination may end mid-
codepoint. The error and byte count are authoritative.

**Rationale**:

- Matches the spec's Edge Cases section verbatim ("The budget is
  measured in bytes, not characters … the partial content delivered
  to the destination may end mid-codepoint. The error and the byte
  count remain authoritative.").
- Operators reason about budgets in bytes ("limit the response to
  1 MB"), not characters; a rune-aware boundary would surprise them
  by allowing a 1 MB cap to deliver up to 4 MB-1 in adversarial
  multi-byte cases.
- Keeps the wrapper a few lines long with no UTF-8 decoding hot path.

**Alternatives considered**:

- *Truncate at the last full rune before the limit*: rejected — see
  above; contradicts the spec's explicit decision.

---

## R8. Concurrent renders & per-call state

**Decision**: All budget state lives on the per-call `evalVisitor` /
`cappedWriter`, never on `*Template`. `*Template` carries no
mutable budget fields.

**Rationale**:

- FR-010 mandates per-call independence even for renders of the same
  template. The existing evaluator already builds a fresh
  `evalVisitor` per `ExecWith` call, so this is the natural extension
  point. The `*Template` mutex (which today only guards
  helpers/partials maps) is *not* taken for budget bookkeeping —
  there is nothing shared to guard.

**Alternatives considered**:

- *Stash the last budget state on `*Template` for observability*:
  rejected — would either need locking on the hot path (perf cost,
  Principle V risk) or expose a non-thread-safe field. Operators who
  want observability can construct their own writer wrapper and read
  it after the call returns.

---

## R9. Interaction with the existing `errRecover`

**Decision**: Reuse the existing `errRecover` deferred recover in
`ExecWith` / new `ExecTo*` methods. The internal
`errBudgetOverflow` sentinel is recognised inside the recover and
converted into the typed `*RenderBudgetExceededError`. Writer errors
that surface as normal returns (not panics) from `evalProgram` are
captured directly in the new top-level driver and converted into
`*RenderDestinationError` (or passed through as
`*RenderBudgetExceededError` if the sentinel matches).

**Rationale**:

- The evaluator already treats unrecoverable issues by panicking and
  catching at the boundary. Adding a parallel error-return channel
  would require touching every `Accept` / `evalXxx` helper — much
  more invasive than the recover path.
- Constitution Principle I says new behaviour MUST NOT introduce
  panics on caller-controlled input. The sentinel is internal
  (unexported) and is *always* converted to a typed error before
  returning — it never escapes to the caller as a panic.

**Alternatives considered**:

- *Refactor evaluator to return errors instead of panicking*:
  rejected — orders of magnitude bigger change than this feature
  warrants; out of scope.

---

## R10. Benchmarks to add

**Decision**: Two new entries in `benchmark_test.go`:

1. `BenchmarkExec_NoBudget_Legacy` — re-runs the existing
   `BenchmarkExec` shape via `(*Template).Exec` with no options, used
   as the baseline for the SC-005 zero-regression claim.
2. `BenchmarkExecTo_WithBudget` — same template, rendered via
   `ExecToWithOptions(io.Discard, ctx, nil, RenderOptions{
   MaxOutputBytes: 1<<20, Enforced: true})`, used to validate the
   SC-004 ≤10% overhead claim.

Both report ns/op and B/op. Numbers from `go test -bench` go in the
PR description (Principle V) and in `BENCHMARKS.md`.

**Rationale**:

- SC-004 and SC-005 are the two measurable performance gates the
  spec sets; the benchmarks must be the things that gate them.
- Using `io.Discard` as the destination removes the OS write cost
  from the measurement, isolating the library's overhead.

**Alternatives considered**:

- *Benchmark against a `*bytes.Buffer`*: also useful, but adds buffer
  growth noise to the measurement; can be added later if the Discard
  number is ambiguous.
