# Implementation Plan: Render Output Budget via Capped Writer

**Branch**: `002-capped-writer-budget` | **Date**: 2026-04-29 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/002-capped-writer-budget/spec.md`

## Summary

Add a per-render **output-byte budget** and an **operator-supplied
destination** (`io.Writer`) to Raymond's render path. Enforcement is
performed by a small internal wrapper writer (`cappedWriter`) that
counts bytes flowing to the operator's destination and short-circuits
the moment the cumulative count would strictly exceed the configured
limit, returning a sentinel error that the evaluator translates into a
typed `*RenderBudgetExceededError`. Destination I/O failures are
surfaced as a separate typed `*RenderDestinationError`. The existing
`Exec` / `MustExec` / `ExecWith` / `Render` / `MustRender` keep their
signatures and behaviour unchanged; new behaviour is opt-in via three
additive entry points: `(*Template).ExecTo(w io.Writer, ctx) error`,
`(*Template).ExecToWith(w io.Writer, ctx, *DataFrame) error`, and a
single options struct `RenderOptions{MaxOutputBytes int64; Enforced
bool}` consumed by `(*Template).ExecToWithOptions(w, ctx, *DataFrame,
RenderOptions) error`. Memory is bounded by streaming each top-level
program statement to the capped writer as soon as it is produced, and
by short-circuiting nested concatenations when their accumulated size
would already exceed the budget.

## Technical Context

**Language/Version**: Go 1.26 (per `go.mod`).
**Primary Dependencies**: Standard library only (`io`, `errors`); no
new external deps. Reuses existing `ast`/`parser`/`lexer`.
**Storage**: N/A (library, in-memory AST + streaming writer).
**Testing**: `go test ./...`; per-package `*_test.go` plus the existing
`handlebars/` and `mustache/` compatibility suites which MUST remain
green and byte-for-byte identical for the legacy `Exec` path (FR-007,
SC-005).
**Target Platform**: Pure-Go library, all platforms supported by the
toolchain.
**Project Type**: single-project Go library (flat root package +
`ast`/`lexer`/`parser`/`handlebars`/`mustache` subpackages).
**Performance Goals**:
- Legacy `Exec` (no options) MUST be byte-for-byte identical and show
  no measurable regression versus pre-feature (SC-005).
- Budget-tracking overhead on a successful render MUST stay within
  10% of the same render with no budget configured (SC-004).
**Constraints**:
- Constitution Principle I — no new `panic` paths on caller-controlled
  input. Existing evaluator panics caught by `errRecover` remain;
  internal sentinel propagation MUST go through `errRecover` and
  resurface as the typed error.
- Constitution Principle IV — no I/O, goroutines, clocks added to the
  evaluator core; the writer itself is *the* I/O surface, supplied by
  the caller.
- Memory: peak in-process memory used by the library for output
  buffering on overflow MUST be bounded by `MaxOutputBytes + O(1)`
  (SC-001); this is the design constraint that drives streaming each
  top-level statement to the writer rather than concatenating into a
  single root string.
**Scale/Scope**: One internal type (`cappedWriter`), two new exported
error types (`RenderBudgetExceededError`, `RenderDestinationError`),
one options struct (`RenderOptions`), three new methods on
`*Template`. One narrow refactor of the top-level program execution
in `template.go`/`eval.go` to write per-statement to the writer.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Evaluating against `/Users/nikthespirit/Documents/experiment/raymond/.specify/memory/constitution.md` v1.0.0:

| Principle / Section | Status | Notes |
|---|---|---|
| I. Compute Safety by Default | ✅ PASS | This feature *is* the render-output axis of the evaluation budget required by Principle I and the Resource Budget Standards section ("max render output size"). New errors are typed (`RenderBudgetExceededError`, `RenderDestinationError`); no `panic` is introduced on caller-controlled input. The internal sentinel used to short-circuit evaluation flows through the existing `errRecover` and is replaced with the typed error before returning. |
| II. Backward-Compatible Public API | ✅ PASS | `Render`, `MustRender`, `Parse`, `MustParse`, `Exec`, `MustExec`, `ExecWith` keep their signatures and observable behaviour. FR-007 and SC-005 are explicit gates: an `Exec` call with no `RenderOptions` produces byte-for-byte identical output to pre-feature. New API is purely additive (`ExecTo`, `ExecToWith`, `ExecToWithOptions`, `RenderOptions`, two error types). No exported identifier is removed or renamed. |
| III. Test-First Development | ✅ PASS | Phase 1 contracts define the failing tests first: exact-fit, off-by-one overflow, zero-budget, multi-byte UTF-8 boundary, helper-emitted bytes, partials/nested, destination short-write, destination I/O failure, no-budget legacy parity, and concurrent-render independence (FR-001…FR-011, edge cases). Both within-budget and over-budget paths covered, no-panic property asserted explicitly. |
| IV. Deterministic, Side-Effect-Free Evaluation | ✅ PASS | The evaluator core gains no new I/O, no goroutines, no clock reads. The only I/O is the operator-supplied writer, which is by design the destination. The `cappedWriter` wrapper is pure byte-counting + delegation; deterministic given the same input. |
| V. Performance Transparency | ✅ PASS — with action item | Plan adds two benchmarks to `benchmark_test.go`: (a) `BenchmarkExec_NoBudget_Legacy` (must be unchanged within noise — no opt-in, FR-007/SC-005), and (b) `BenchmarkExecTo_WithBudget` over a representative template (must stay within 10% of (a) per SC-004). Before/after numbers reported in the PR description per Principle V. |
| Resource Budget Standards | ✅ PASS | Adds the **render output size** axis required by the Resource Budget Standards section, complementary to the parse budget added in feature 001. The `RenderOptions` struct is shaped to carry future axes (eval steps, recursion depth, deadline) without API churn — only `MaxOutputBytes`+`Enforced` are populated in this feature; other fields are reserved (documented but not added until their feature ships). Default behaviour with no options preserves legacy behaviour (FR-007), so this introduces no breakage of pre-existing valid templates. |
| Development Workflow & Quality Gates | ✅ PASS | All new behaviour lands with `go test ./...` coverage; benchmark smoke runs; package godoc on every new exported identifier; `CHANGELOG.md` entry under Unreleased for the new opt-in API and error types; `BENCHMARKS.md` entry recording the SC-004/SC-005 measurements. |

**Gate result**: PASS. No violations to record in Complexity Tracking.

**Post-design re-check (after Phase 1)**: Re-evaluated against the
contracts in `contracts/api.md` and the data model in `data-model.md`.
No new violations introduced. The `cappedWriter` is unexported (the
operator never sees it directly — they pass an arbitrary `io.Writer`),
so no public-API surface is added beyond the three methods, two error
types, and one options struct enumerated above. The streaming-per-
top-level-statement refactor is local to the root package
(`template.go` / `eval.go`) and does not touch `ast`, `parser`,
`lexer`, `handlebars/`, or `mustache/`.

## Project Structure

### Documentation (this feature)

```text
specs/002-capped-writer-budget/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
│   └── api.md           # Public Go API contract for this feature
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

The repository is a single-project Go library; this feature adds files
to the existing root package and performs one narrow refactor of the
top-level execution loop. No new packages are introduced.

```text
.
├── ast/                       # existing — unchanged
├── lexer/                     # existing — unchanged
├── parser/                    # existing — unchanged
├── handlebars/                # existing — handlebars.js compat suite (unchanged)
├── mustache/                  # existing — mustache compat suite (unchanged)
│
├── raymond.go                 # existing — Render/MustRender (unchanged)
├── template.go                # MODIFIED — add ExecTo, ExecToWith, ExecToWithOptions; refactor top-level program execution to stream per-statement
├── eval.go                    # MODIFIED — evalVisitor gains optional writer + budget bookkeeping; sub-program concatenation short-circuits when committed+pending bytes would exceed budget
├── render_options.go          # NEW — RenderOptions struct
├── render_errors.go           # NEW — RenderBudgetExceededError, RenderDestinationError types + errors.Is helpers
├── capped_writer.go           # NEW — internal cappedWriter (io.Writer wrapper) + sentinel errOverflow
│
├── render_options_test.go     # NEW — table tests for RenderOptions defaults and zero-value parity (FR-007)
├── render_errors_test.go      # NEW — typed error fields + errors.Is/As distinguishability (FR-004, FR-009, SC-003)
├── capped_writer_test.go      # NEW — exact-fit, off-by-one, zero-budget, short-write, write-error propagation, multi-byte UTF-8 boundary
├── exec_to_test.go            # NEW — end-to-end ExecTo / ExecToWithOptions: helper-emitted bytes, partials/nested, concurrent renders, 10 MB literal early-abort, destination receives ≤ budget bytes on overflow
├── benchmark_test.go          # MODIFIED — add BenchmarkExec_NoBudget_Legacy and BenchmarkExecTo_WithBudget
├── CHANGELOG.md               # MODIFIED — entry under Unreleased: opt-in render output budget + capped writer
└── BENCHMARKS.md              # MODIFIED — record SC-004/SC-005 numbers
```

**Structure Decision**: Single-project library, flat root package.
The `cappedWriter` lives in the root package (unexported) so the
evaluator can reach for it directly without an import cycle and
without enlarging the public surface. The top-level streaming change
lives in `template.go`'s `ExecWith` (and the new `ExecTo*` methods),
not in the existing `evalVisitor.evalProgram` — the latter must keep
returning strings to preserve the helper / `Options.Fn()` contract
relied upon by the handlebars/mustache compatibility suites. This is
the smallest layout that satisfies the spec and the constitution; no
alternative structure was needed.

## Complexity Tracking

> No Constitution Check violations. Section intentionally empty.
