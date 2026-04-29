---
description: "Task list for feature 002: Render Output Budget via Capped Writer"
---

# Tasks: Render Output Budget via Capped Writer

**Input**: Design documents from `/specs/002-capped-writer-budget/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/api.md, quickstart.md

**Tests**: Tests are INCLUDED. Constitution Principle III (Test-First Development) is in force, and `contracts/api.md` enumerates the failing tests each new API surface MUST satisfy before implementation lands.

**Organization**: Tasks are grouped by user story. The three user stories (US1 budget enforcement, US2 streaming destination, US3 typed-error distinguishability) share an implementation core; foundational tasks build that core once and each user story phase then adds its story-specific tests + the smallest behaviour delta needed to satisfy them.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no incomplete-task dependencies)
- **[Story]**: Maps task to spec.md user story (US1, US2, US3)
- All file paths are relative to repo root `/Users/nikthespirit/Documents/experiment/raymond/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish file scaffolding for the new package files. The repo already has Go module + tooling; no project-init work is needed.

- [X] T001 Confirm Go toolchain (`go version` ≥ 1.26 per `go.mod`) and that baseline `go test ./...` passes on branch `002-capped-writer-budget` before any changes
- [X] T002 [P] Add `Unreleased` section header for feature 002 in `CHANGELOG.md` (placeholder entry; bullets filled in during polish phase)
- [X] T003 [P] Add `## Feature 002 — Render Output Budget` section header in `BENCHMARKS.md` (numbers filled in during polish phase)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Build the type surface (`RenderOptions`, two error types, `cappedWriter`) and the streaming refactor scaffolding that ALL three user stories rely on. None of US1/US2/US3 can be implemented or tested until this phase is complete.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

### Type definitions (parallelisable — distinct new files)

- [X] T004 [P] Define `RenderOptions` struct (`MaxOutputBytes int64`, `Enforced bool`) with godoc per `data-model.md` §RenderOptions in `render_options.go`
- [X] T005 [P] Define `RenderBudgetExceededError` (`Kind string`, `Limit int64`, `Error() string`) and `RenderDestinationError` (`Cause error`, `Error() string`, `Unwrap() error`) per `data-model.md` §RenderBudgetExceededError / §RenderDestinationError in `render_errors.go`
- [X] T006 [P] Define internal `cappedWriter` struct (`dst io.Writer`, `limit int64`, `written int64`), `newCappedWriter(dst, limit)` constructor, `Write([]byte) (int, error)` implementing the 5-step state-transition table from `data-model.md` §cappedWriter, and unexported sentinel `errBudgetOverflow = errors.New(...)` in `capped_writer.go`

### Foundational tests (must precede implementation per Principle III)

- [X] T007 [P] Write failing table tests covering the full `cappedWriter` state-transition table (exact-fit, off-by-one over, off-by-one under, multi-Write accumulating to limit, multi-Write straddling the limit, zero-budget rejects non-empty Write, zero-budget accepts empty Write, underlying-writer error surfaced unchanged, underlying short-write `n<len(p), nil` surfaced unchanged) in `capped_writer_test.go` per `contracts/api.md` §7
- [X] T008 [P] Write failing tests for `RenderOptions` defaults: `TestRenderOptions_ZeroValueIsLegacy`, `TestRenderOptions_EnforcedZeroBudget`, `TestRenderOptions_NegativeBudgetRejected` in `render_options_test.go` per `contracts/api.md` §1
- [X] T009 [P] Write failing tests for typed error fields and `errors.As`/`errors.Is` discrimination: `TestRenderBudgetExceededError_FieldsPopulated`, `TestRenderBudgetExceededError_IdentifiableViaErrorsAs`, `TestRenderBudgetExceededError_DistinctFromDestinationError`, `TestRenderDestinationError_WrapsCause`, `TestRenderDestinationError_DistinctFromBudgetError` in `render_errors_test.go` per `contracts/api.md` §2 and §3

### Foundational refactor (single-file, sequential — both touch `eval.go` / `template.go`)

- [X] T010 Add unexported fields `out *cappedWriter` and `committed int64` to `evalVisitor` in `eval.go`, gated so every budget-related branch checks `v.out != nil`; legacy `ExecWith` path leaves them zero (see `data-model.md` §evalVisitor and `research.md` R3)
- [X] T011 Implement private top-level streaming driver `execToWithOptionsImpl(w io.Writer, ctx interface{}, privData *DataFrame, opts RenderOptions) error` on `*Template` in `template.go` that: (a) validates `opts` (negative `MaxOutputBytes` with `Enforced:true` → return `*RenderBudgetExceededError{Kind:"output bytes", Limit:opts.MaxOutputBytes}` before any write); (b) wraps `w` in `newCappedWriter` when `opts.Enforced`, otherwise passes `w` through; (c) installs `out`/`committed` on a fresh `evalVisitor`; (d) iterates `program.Body` invoking `statement.Accept(v).(string)` and writing each fragment to the capped writer; (e) uses `errRecover` to convert internal `errBudgetOverflow` panics into `*RenderBudgetExceededError`; (f) wraps any non-sentinel writer error (incl. short writes) into `*RenderDestinationError`. References `research.md` R3, R4, R9.
- [X] T012 Make `evalProgram` in `eval.go` short-circuit for streaming renders: after each appended sub-program statement string, if `v.out != nil` and `v.committed + int64(len(currentResult)) > v.out.limit`, panic with `errBudgetOverflow` so `errRecover` resurfaces it as the typed error (research.md R3). Confirm legacy path (`v.out == nil`) executes the existing branch unchanged.

**Checkpoint**: All foundational types compile, type-only tests (T007–T009) pass, the streaming driver `execToWithOptionsImpl` exists and the refactored `evalProgram` is wired. User stories can now layer their public methods + behaviour tests on top.

---

## Phase 3: User Story 1 - Cap rendered output size and fail fast on overflow (Priority: P1) 🎯 MVP

**Goal**: Operators can configure a per-call output-byte budget; rendering aborts the moment cumulative output would strictly exceed the limit, with peak in-process buffer bounded by `limit + O(1)`.

**Independent Test**: Configure budget = N. Render template+context producing exactly N bytes → success; destination receives all N. Render template+context producing N+1 bytes → fails with `*RenderBudgetExceededError`; destination receives at most N. Verified by `TestExecToWithOptions_ExactFitSucceeds`, `TestExecToWithOptions_OneByteOverFails`, `TestExecToWithOptions_LargeLiteralEarlyAbort`.

### Tests for User Story 1 (write FIRST, ensure FAIL before T020)

- [X] T013 [P] [US1] Failing test `TestExecToWithOptions_ExactFitSucceeds` (budget=N, output=N → nil; destination receives all N bytes) in `exec_to_test.go` per `contracts/api.md` §6
- [X] T014 [P] [US1] Failing test `TestExecToWithOptions_OneByteOverFails` (budget=N, output=N+1 → `*RenderBudgetExceededError`; destination receives exactly N bytes) in `exec_to_test.go`
- [X] T015 [P] [US1] Failing test `TestExecToWithOptions_LargeLiteralEarlyAbort` (10 MiB literal under 1 MiB budget aborts before fully writing the literal; destination receives ≤ 1 MiB; `runtime.MemStats` delta bounded by budget + O(1)) in `exec_to_test.go` — covers Story 1 acceptance #3 / SC-001
- [X] T016 [P] [US1] Failing test `TestExecToWithOptions_HelperEmittedBytesCount` (helper writing 2 KiB under 1 KiB budget → `*RenderBudgetExceededError`) in `exec_to_test.go` — FR-008
- [X] T017 [P] [US1] Failing test `TestExecToWithOptions_PartialBytesCount` (partial whose total emitted bytes exceed the budget aborts; no per-partial sub-budget) in `exec_to_test.go` — FR-008
- [X] T018 [P] [US1] Failing test `TestExecToWithOptions_UTF8AtBoundary` (multi-byte UTF-8 with budget falling mid-codepoint aborts at byte boundary; destination may end mid-codepoint; error authoritative) in `exec_to_test.go`
- [X] T019 [P] [US1] Failing test `TestExecToWithOptions_NoPanicOnAdversarialInput` (~1000 randomised templates+contexts × random budgets; assert never panics) in `exec_to_test.go` — Constitution Principle I

### Implementation for User Story 1

- [X] T020 [US1] Expose `func (tpl *Template) ExecToWithOptions(w io.Writer, ctx interface{}, privData *DataFrame, opts RenderOptions) error` in `template.go` as a thin public wrapper around `execToWithOptionsImpl` from T011 (godoc explicitly states FR-006 exact-fit semantics, FR-008 single-budget-across-helpers/partials, FR-010 per-call independence, FR-011 zero-budget legality)
- [X] T021 [US1] Verify `errBudgetOverflow` returned by `cappedWriter.Write` propagates through `evalProgram`'s `Accept` chain (which currently `panic`s on non-nil internal errors via `errPanic`) and is recognised in `errRecover` of `execToWithOptionsImpl` to produce `*RenderBudgetExceededError{Kind:"output bytes", Limit: opts.MaxOutputBytes}`. Wire the recognition in `eval.go` / `template.go` as needed.
- [X] T022 [US1] Run `go test -run 'TestExecToWithOptions_(ExactFit|OneByteOver|LargeLiteralEarlyAbort|HelperEmittedBytesCount|PartialBytesCount|UTF8AtBoundary|NoPanicOnAdversarialInput)$' ./...` and confirm all US1 tests now pass

**Checkpoint**: User Story 1 is fully functional. The library can cap render output, abort cleanly on overflow with a typed error, and bound peak buffer memory by the budget. MVP is shippable here.

---

## Phase 4: User Story 2 - Stream rendered output to a destination of the operator's choice (Priority: P1)

**Goal**: Operators pass their own `io.Writer`; rendered bytes flow into it as they are produced, with the budget (when configured) enforced on the way through. Destination I/O failures surface as a distinct typed error.

**Independent Test**: Provide an `io.Writer` (buffer or file). Render a 500 KiB output under a 1 MiB budget → destination receives exactly the 500 KiB, `err == nil`. Render a 5 MiB output under a 1 MiB budget → destination receives at most 1 MiB. Closed/failing destination → `*RenderDestinationError`. Verified by `TestExecTo_StreamsBytes`, `TestExecTo_DestinationWriteFailure`, `TestExecToWith_DataFramePropagated`, `TestExecToWithOptions_NotEnforced_NoTracking`.

### Tests for User Story 2 (write FIRST)

- [X] T023 [P] [US2] Failing test `TestExecTo_StreamsBytes` (rendering into `*bytes.Buffer` via `ExecTo` produces same bytes as `Exec` returns as a string) in `exec_to_test.go` per `contracts/api.md` §4
- [X] T024 [P] [US2] Failing test `TestExecTo_DestinationWriteFailure` (writer whose `Write` returns a custom error → `ExecTo` returns `*RenderDestinationError` with `Unwrap() == that error`) in `exec_to_test.go`
- [X] T025 [P] [US2] Failing test `TestExecToWith_DataFramePropagated` (`*DataFrame` private value visible via `@key` exactly as in `ExecWith`) in `exec_to_test.go` per `contracts/api.md` §5
- [X] T026 [P] [US2] Failing test `TestExecToWithOptions_NotEnforced_NoTracking` (with `Enforced:false`, a 100 MiB output succeeds and destination receives all 100 MiB; use a counting `io.Discard`-style writer to assert byte count) in `exec_to_test.go` — FR-007
- [X] T027 [P] [US2] Failing test asserting short-write surfacing: a destination returning `(n<len(p), nil)` produces `*RenderDestinationError` with `errors.Is(err, io.ErrShortWrite)` true (covers spec edge case "Destination that silently accepts fewer bytes" / FR-009) in `exec_to_test.go`

### Implementation for User Story 2

- [X] T028 [US2] Expose `func (tpl *Template) ExecTo(w io.Writer, ctx interface{}) error` in `template.go` as `ExecToWithOptions(w, ctx, nil, RenderOptions{})` (godoc per `contracts/api.md` §4)
- [X] T029 [US2] Expose `func (tpl *Template) ExecToWith(w io.Writer, ctx interface{}, privData *DataFrame) error` in `template.go` as `ExecToWithOptions(w, ctx, privData, RenderOptions{})` (godoc per `contracts/api.md` §5)
- [X] T030 [US2] In `execToWithOptionsImpl` (`template.go`), confirm short-writes from the underlying writer (when `cappedWriter` forwards or when no `cappedWriter` is in use) are surfaced as `*RenderDestinationError{Cause: io.ErrShortWrite}`; add an explicit short-write check around each `dst.Write` call so the wrapping happens uniformly
- [X] T031 [US2] Run `go test -run 'TestExecTo|TestExecToWith_DataFramePropagated|TestExecToWithOptions_NotEnforced_NoTracking' ./...` and confirm all US2 tests pass

**Checkpoint**: User Story 2 is fully functional. Operators can stream into any `io.Writer`, with or without a budget. Both P1 stories now work end-to-end.

---

## Phase 5: User Story 3 - Distinguish budget-exceeded errors from other render errors (Priority: P2)

**Goal**: Operators programmatically tell apart budget overflows, destination I/O failures, and other render errors using `errors.As` / `errors.Is` — without parsing human-readable text.

**Independent Test**: Trigger (a) budget overflow, (b) destination write failure, (c) successful render. Confirm `errors.As(&RenderBudgetExceededError{})` matches only (a); `errors.As(&RenderDestinationError{})` matches only (b); both miss (c). Verified by tests already written in T009 plus the cross-render test below.

### Tests for User Story 3 (write FIRST)

- [X] T032 [P] [US3] Failing test `TestExecToWithOptions_ConcurrentRendersIndependent` (two goroutines render the same `*Template` with different `MaxOutputBytes`; each receives errors consistent with its own budget; no cross-talk) in `exec_to_test.go` per `contracts/api.md` §6 — FR-010
- [X] T033 [P] [US3] Failing test that triggers all three outcomes (budget overflow, destination write failure, success) in one sub-test sweep and asserts the discrimination matrix from spec Story 3 acceptance #1/#2 holds; reuse `errors.As`/`errors.Is` (no string matching) in `render_errors_test.go`

### Implementation for User Story 3

- [X] T034 [US3] Audit `execToWithOptionsImpl` and `evalProgram` to confirm: the `errBudgetOverflow` sentinel never escapes to the caller (always converted), `*RenderBudgetExceededError` is returned only on overflow, `*RenderDestinationError` is returned only on writer failure, and the two are mutually exclusive (FR-009, SC-003). Fix any leak by adding the conversion at the right boundary in `template.go` / `eval.go`.
- [X] T035 [US3] Run `go test -run 'TestExecToWithOptions_ConcurrentRendersIndependent|TestRenderBudgetExceededError|TestRenderDestinationError' ./...` and confirm all US3 tests pass

**Checkpoint**: All three user stories are independently functional and testable. The full feature is behaviourally complete.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Performance gates (SC-004, SC-005), back-compat verification (FR-007), documentation, and changelog.

- [X] T036 [P] Add `BenchmarkExec_NoBudget_Legacy` (re-uses existing `BenchmarkExec` shape; calls `(*Template).Exec`) in `benchmark_test.go` per `research.md` R10
- [X] T037 [P] Add `BenchmarkExecTo_WithBudget` (same template, calls `ExecToWithOptions(io.Discard, ctx, nil, RenderOptions{MaxOutputBytes: 1<<20, Enforced: true})`) in `benchmark_test.go`
- [X] T038 Run `go test -bench=BenchmarkExec_NoBudget_Legacy -benchmem -count=5 ./...` and `go test -bench=BenchmarkExecTo_WithBudget -benchmem -count=5 ./...`; confirm SC-004 (≤10% wall-clock overhead vs no-budget) and SC-005 (no measurable regression vs pre-feature) hold
- [X] T039 Run full back-compat suite: `go test ./...` including `handlebars/` and `mustache/`; confirm byte-for-byte parity with pre-feature output for every existing test (FR-007, SC-005)
- [X] T040 [P] Fill in `CHANGELOG.md` Unreleased section: "feat: opt-in render output budget via `ExecTo` / `ExecToWith` / `ExecToWithOptions` + `RenderOptions`; new typed errors `RenderBudgetExceededError`, `RenderDestinationError`. Existing `Exec` / `MustExec` / `ExecWith` / `Render` / `MustRender` unchanged."
- [X] T041 [P] Record SC-004 / SC-005 numbers from T038 in `BENCHMARKS.md` under the feature 002 section
- [X] T042 [P] Verify package godoc on every new exported identifier (`RenderOptions`, `RenderBudgetExceededError`, `RenderDestinationError`, `ExecTo`, `ExecToWith`, `ExecToWithOptions`) renders correctly via `go doc github.com/aymerick/raymond` and includes a usage example block lifted from `quickstart.md`
- [X] T043 Walk through `quickstart.md` end-to-end against the built library (run each code block as a smoke test) and confirm every snippet behaves exactly as documented

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Setup — BLOCKS all user stories
- **User Story 1 (Phase 3)**: Depends on Foundational — independently shippable as MVP
- **User Story 2 (Phase 4)**: Depends on Foundational — uses the same `execToWithOptionsImpl` driver from T011 but its tests + public methods (`ExecTo`, `ExecToWith`) are independent of US1 tests
- **User Story 3 (Phase 5)**: Depends on Foundational; tests from US1+US2 surface the errors US3 verifies, but US3's discrimination tests can be written and validated against the same `execToWithOptionsImpl` once it returns typed errors (i.e., after T011 + T020)
- **Polish (Phase 6)**: Depends on US1+US2 (US3 optional but recommended) being complete

### Within Each User Story

- All `[P]`-marked tests in a story phase can be written in parallel (different test funcs, same file `exec_to_test.go` — coordinate within one PR or write each as a separate commit to avoid merge churn)
- Tests are written FIRST and confirmed to FAIL before the corresponding implementation task
- Implementation tasks within a story are sequenced because they all touch `template.go` / `eval.go`

### Parallel Opportunities

- **Phase 1**: T002, T003 in parallel
- **Phase 2 type defs**: T004, T005, T006 in parallel (three new files); T007, T008, T009 in parallel after their respective types compile
- **Phase 2 refactor (T010, T011, T012)**: sequential (same files)
- **Phase 3**: T013–T019 all `[P]` (all live in `exec_to_test.go` but as separate test functions)
- **Phase 4**: T023–T027 all `[P]`
- **Phase 5**: T032, T033 in parallel
- **Phase 6**: T036, T037, T040, T041, T042 in parallel

---

## Parallel Example: Foundational Phase

```bash
# After T001 confirms baseline green, launch type-definition tasks together:
Task: "Define RenderOptions struct in render_options.go"          # T004
Task: "Define RenderBudgetExceededError + RenderDestinationError in render_errors.go" # T005
Task: "Define cappedWriter + sentinel in capped_writer.go"        # T006

# Then their failing tests in parallel:
Task: "Write failing cappedWriter table tests in capped_writer_test.go"   # T007
Task: "Write failing RenderOptions tests in render_options_test.go"        # T008
Task: "Write failing typed-error tests in render_errors_test.go"           # T009
```

## Parallel Example: User Story 1

```bash
# All US1 tests in parallel (separate funcs in exec_to_test.go):
Task: "TestExecToWithOptions_ExactFitSucceeds"                # T013
Task: "TestExecToWithOptions_OneByteOverFails"                # T014
Task: "TestExecToWithOptions_LargeLiteralEarlyAbort"          # T015
Task: "TestExecToWithOptions_HelperEmittedBytesCount"         # T016
Task: "TestExecToWithOptions_PartialBytesCount"               # T017
Task: "TestExecToWithOptions_UTF8AtBoundary"                  # T018
Task: "TestExecToWithOptions_NoPanicOnAdversarialInput"       # T019
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1: Setup (T001–T003)
2. Phase 2: Foundational (T004–T012) — **CRITICAL, blocks everything**
3. Phase 3: User Story 1 (T013–T022) — operators can cap output and fail fast
4. **STOP & VALIDATE**: Story 1 acceptance scenarios pass end-to-end
5. Optional ship gate: feature is independently valuable here as "render with a budget into nothing" is achievable by passing `io.Discard`

### Incremental Delivery

1. Foundation + US1 → MVP (operator can cap any render that runs through `ExecToWithOptions` into any `io.Writer` they already have, including `io.Discard`)
2. + US2 → Streaming becomes ergonomic (`ExecTo`, `ExecToWith` no-options helpers; short-write handling polished)
3. + US3 → Production observability (concurrent-render guarantees, full discrimination test coverage)
4. + Polish → Benchmarks, changelog, docs, back-compat audit

### Parallel Team Strategy

- One developer can carry all three stories sequentially in roughly one PR per story
- With two developers post-Foundational: A takes US1 (the safety semantics), B takes US2 (the streaming ergonomics + short-write surfacing); US3 is small and can be picked up by whoever finishes first

---

## Notes

- Tests are mandatory for this feature (Constitution Principle III + `contracts/api.md` enumerates exactly which tests must exist)
- `[P]` tasks within `exec_to_test.go` are still parallelisable as separate test funcs; just take care with merge ordering
- Every new exported identifier MUST land with godoc in the same commit as its implementation (Development Workflow & Quality Gates)
- The legacy `Exec` / `MustExec` / `ExecWith` / `Render` / `MustRender` paths MUST remain byte-for-byte identical (FR-007, SC-005); T039 is the gate
- Verify each test FAILS before its implementation task (red → green discipline)
- Commit after each task or logical group; do NOT batch the foundational phase into a single commit — type defs and refactor are separately reviewable
