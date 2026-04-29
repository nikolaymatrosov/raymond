---
description: "Task list for Parse Budget & Template Capability Modes"
---

# Tasks: Parse Budget & Template Capability Modes

**Input**: Design documents from `/specs/001-parse-budget-template-modes/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/api.md, quickstart.md

**Tests**: TDD is mandatory per Constitution Principle III and plan.md Phase 1 (each contract item maps to a failing test). Test tasks are included.

**Organization**: Tasks are grouped by user story (US1–US4) so each story can be implemented and validated independently. All paths are repository-root-relative; the project is a flat-root Go library — no `src/` or `tests/` subdirs.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on incomplete tasks)
- **[Story]**: User-story label (US1, US2, US3, US4) — present only on story-phase tasks
- File paths are exact and repo-root-relative

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Confirm the repository's existing Go toolchain accepts the new files; no new tooling is introduced.

- [ ] T001 Verify Go module floor and existing test/bench commands by running `go test ./...` and `go test -bench=. -benchtime=1x ./...` from repo root; record the baseline so post-feature numbers in T037 can be compared against it.
- [ ] T002 [P] Add an "Unreleased" entry stub to `CHANGELOG.md` describing the upcoming opt-in `ParseWithOptions` API (filled in fully in T036).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Public types and constructors that every user story depends on. The visitor itself, error types, and report type are foundational because every story's tests import them.

**⚠️ CRITICAL**: No user-story tasks may begin until this phase is complete.

- [ ] T003 [P] Create `parse_options.go` at repo root with exported types `Mode` (`ModeFull = iota`, `ModeSimple`), `Capabilities{If, Iteration, Partials bool}`, `Budget{MaxSubstitutions int; Enforced bool}`, and `ParseOptions{Mode, Capabilities, Budget}` per [contracts/api.md C1](contracts/api.md) and [data-model.md §1–§4](data-model.md). Add godoc on every exported identifier; zero value of `ParseOptions{}` must be documented as "legacy behaviour".
- [ ] T004 [P] Create `parse_report.go` at repo root with exported type `ParseReport{Substitutions int; Constructs []string}`; `Constructs` is documented as sorted, deduplicated, drawn from the closed set `{"if","unless","each","with","partial","helper"}` per [data-model.md §5](data-model.md).
- [ ] T005 [P] Create `parse_errors.go` at repo root with exported types `BudgetExceededError{Kind string; Limit, Observed int}` and `CapabilityError{Construct string; Loc ast.Loc}`, each implementing `error` via pointer receiver `Error() string` returning a precise human-readable message that includes every field. Import `github.com/aymerick/raymond/ast` for `ast.Loc`.
- [ ] T006 Modify `template.go` at repo root to add an unexported `report ParseReport` field on `*Template`, an exported method `(*Template) Report() ParseReport` that returns a copy of `report`, and an exported function `ParseWithOptions(source string, opts ParseOptions) (*Template, error)` that calls the existing parse path, then (when `opts` triggers granular/simple/budget enforcement) runs the visitor from T007 and either returns a typed error or stores the visitor's `ParseReport` on the template. Existing `Parse`, `MustParse`, `ParseFile` signatures and behaviour MUST be unchanged. Depends on T003–T005.
- [ ] T007 Create `parse_visitor.go` at repo root implementing the unexported `capVisitor` type per [data-model.md §8](data-model.md) and [research.md R1–R3](research.md). It MUST: (a) implement `ast.Visitor` (all `Visit*` methods); (b) precompute `simple`, `granular`, and `fullLegacy` flags from `ParseOptions`; (c) short-circuit on first `err`; (d) classify each `*ast.MustacheStatement` / `*ast.BlockStatement` / `*ast.PartialStatement` per the [R3 table](research.md); (e) count substitutions on `MustacheStatement` only; (f) record used constructs into a `map[string]struct{}`; (g) on completion, compare `subs` to `Budget` and emit `*BudgetExceededError` if exceeded; (h) never `panic`. Provide an unexported `runCapVisitor(prog *ast.Program, opts ParseOptions) (ParseReport, error)` entry point used by T006. Depends on T003–T005.

**Checkpoint**: Foundational complete — public surface compiles, visitor runnable; user-story phases may now proceed in parallel.

---

## Phase 3: User Story 1 — Substitution-count budget (Priority: P1) 🎯 MVP

**Goal**: Operators can refuse a template whose substitution count exceeds a configured ceiling, observable purely at parse time, with a typed error carrying kind/limit/observed.

**Independent Test**: With `Budget{MaxSubstitutions: 100, Enforced: true}`, a 100-substitution template loads (report says 100); a 101-substitution template fails with `*BudgetExceededError{Kind:"substitutions", Limit:100, Observed:101}`. With no opts, behaviour is unchanged. Maps to [contracts/api.md C2 and C3](contracts/api.md).

### Tests for User Story 1 (write FIRST; must FAIL before T012)

- [ ] T008 [P] [US1] Add backward-compat regression cases to `template_test.go` at repo root verifying that pre-existing `Parse(s)` and `MustParse(s)` callers see identical behaviour to the legacy implementation across a sample of templates that use every Handlebars construct family (text, mustache, `#if`, `#each`, `#with`, partials, helpers) — no `*BudgetExceededError` and no `*CapabilityError` is ever returned (covers C2, FR-010, SC-004).
- [ ] T009 [P] [US1] Create `parse_visitor_test.go` at repo root with a `t.Run("budget", ...)` subtest table covering each row of [C3](contracts/api.md): exactly-at-limit (100/100) success; over-limit (101/100) failing with `*BudgetExceededError{Kind:"substitutions", Limit:100, Observed:101}` via `errors.As`; zero-substitution template under `Enforced:true` budget; `MaxSubstitutions:0, Enforced:true` rejects any substitution; `MaxSubstitutions:-1, Enforced:false` is no-limit; returned `*Template` is nil on failure.
- [ ] T010 [P] [US1] Create `parse_errors_test.go` at repo root asserting that `(*BudgetExceededError).Error()` includes the strings `"substitutions"`, the limit, and the observed count; and that two `BudgetExceededError` instances with different fields produce different messages.
- [ ] T011 [P] [US1] Add a `t.Run("nopanic-budget", ...)` subtest in `parse_visitor_test.go` that runs `ParseWithOptions` over a corpus of crafted templates (deeply nested, oversized, malformed-but-parser-accepted) under an enforced budget inside `defer func(){ if r := recover(); r != nil { t.Fatal(r) } }()` (covers C8 budget half).

### Implementation for User Story 1

- [ ] T012 [US1] In `parse_visitor.go`, implement substitution counting on every `VisitMustache` invocation and the post-walk budget check that emits `*BudgetExceededError{Kind:"substitutions", Limit:opts.Budget.MaxSubstitutions, Observed:subs}`; ensure `Observed` is exactly the count that crossed the threshold per [C7](contracts/api.md). Ensure that when `opts.Budget.Enforced` is false (incl. zero `Budget{}`), no budget check runs. Make tests T008–T011 pass.
- [ ] T013 [US1] In `template.go` `ParseWithOptions`, on visitor budget error return `nil, err` (do not return a partially-populated `*Template`); on success attach the report to the template. Re-run T008–T011.

**Checkpoint**: US1 fully functional; the MVP guarantee (size-based rejection at parse time) is in place.

---

## Phase 4: User Story 2 — "Simple" capability mode (Priority: P1)

**Goal**: With `Mode: ModeSimple`, the parser rejects any control-flow, partials, helpers, parent-context paths, and `@`-data variables, naming the offending construct and its source location.

**Independent Test**: Plain text + `{{name}}` loads; `{{#if x}}…{{/if}}`, `{{#each}}…`, `{{#with}}…`, every partial form, helper invocations, `{{../x}}`, `{{@root}}`, etc. each fail with `*CapabilityError` carrying the right `Construct` and a non-zero `Loc`. Maps to [contracts/api.md C4](contracts/api.md).

### Tests for User Story 2 (write FIRST; must FAIL before T015)

- [ ] T014 [P] [US2] In `parse_visitor_test.go`, add a `t.Run("simple-mode", ...)` subtest table iterating every row of [C4](contracts/api.md): success rows (`{{name}}`, dotted, indexed, `this`, comments, triple-stash, whitespace-control) and failure rows (`if`, `each`, `with`, four partial forms → `"partial"`, helper invocation → `"helper"`, `../x` → `"parent-path"`, `@root.x`/`@key`/`@index` → `"data-var"`). For every failure row assert: `errors.As(err, **CapabilityError) == true`, `Construct` matches the expected token, and `Loc.Line > 0 && Loc.Column > 0`.

### Implementation for User Story 2

- [ ] T015 [US2] In `parse_visitor.go`, implement the `simple` branch: when `opts.Mode == ModeSimple`, every `VisitBlock` rejects with the block helper's name (`"if"`, `"unless"`, `"each"`, `"with"`, else `"helper"`); every `VisitPartial` (covering static, dynamic, inline, and partial-block forms uniformly) rejects with `"partial"`; every `VisitMustache` whose expression has params/hash/sub-expr rejects with `"helper"`; `VisitPath` rejects with `"parent-path"` when `Depth > 0` and `"data-var"` when `Data == true`. `Loc` MUST come from the offending AST node. Make T014 pass.
- [ ] T016 [US2] Verify against the [quickstart.md §3 manual checklist](quickstart.md) (US2 #1–#4) that simple-mode behaviour matches the documented examples; note any discrepancy and fix in `parse_visitor.go`. No new file.

**Checkpoint**: US2 fully functional; MVP can ship with US1 + US2 together (both are P1).

---

## Phase 5: User Story 3 — Granular toggles for `if`, iteration, partials (Priority: P2)

**Goal**: Independent on/off switches for conditionals, iteration, and partials, honoured under `Mode == ModeFull` whenever any switch is true OR a `Budget` is enforced.

**Independent Test**: For each of the 8 toggle combinations, exactly the disabled families are rejected and exactly the enabled families are accepted; `{{#with}}` and helper-style mustaches are rejected in every granular combination. Maps to [contracts/api.md C5](contracts/api.md).

### Tests for User Story 3 (write FIRST; must FAIL before T018)

- [ ] T017 [P] [US3] In `parse_visitor_test.go`, add a `t.Run("granular", ...)` subtest with a table of 8 rows (`If` × `Iteration` × `Partials`) plus a row with all-false but `Budget.Enforced:true`. For each row probe a fixture set: `{{#if}}`, `{{#unless}}`, `{{#each}}`, all four partial forms, `{{#with}}`, and `{{upper name}}`; assert success vs `*CapabilityError{Construct: …}` exactly per [C5](contracts/api.md). Confirm that the all-false-no-budget combination (the zero `ParseOptions{}`) takes the legacy path and returns no capability error.

### Implementation for User Story 3

- [ ] T018 [US3] In `parse_visitor.go`, implement the `granular` branch precedence per [data-model.md §2](data-model.md): if `simple` not set and (any of `If`/`Iteration`/`Partials` is true OR `Budget.Enforced` is true), then plain text + plain current-context substitution are always allowed; `{{#if}}`/`{{#unless}}` allowed iff `Capabilities.If`; `{{#each}}` allowed iff `Capabilities.Iteration`; partials (all four forms) allowed iff `Capabilities.Partials`; `{{#with}}` and any other helper block always rejected with `"with"` / `"helper"`; helper-style mustaches always rejected with `"helper"`. Make T017 pass without regressing T009–T016.
- [ ] T019 [US3] Walk through the 8-combination table in [quickstart.md](quickstart.md) §4 by hand against the implementation; record any deviation and fix in `parse_visitor.go`.

**Checkpoint**: US3 functional; all three on/off combinations work without enabling the full language.

---

## Phase 6: User Story 4 — Observability via `ParseReport` (Priority: P3)

**Goal**: After a successful load, the operator can read back the observed substitution count and the set of constructs encountered without re-parsing; on a parse failure, the typed error carries the same diagnostic fields.

**Independent Test**: Load a known template under generous budget + capabilities; `tpl.Report().Substitutions` matches the source count; `tpl.Report().Constructs` is sorted, deduplicated, and a subset of the closed vocabulary. On a budget breach, `*BudgetExceededError.Observed` is exactly the count that crossed the threshold; on a capability breach, `*CapabilityError.Loc` points at the offending node. Maps to [contracts/api.md C7](contracts/api.md).

### Tests for User Story 4 (write FIRST; must FAIL before T021)

- [ ] T020 [P] [US4] Create `parse_report_test.go` at repo root asserting: (a) on a known template successful under all-true capabilities + a generous budget, `tpl.Report().Substitutions` matches the source's literal substitution count and `tpl.Report().Constructs` is a sorted dedup'd slice using only the closed vocabulary `{"if","unless","each","with","partial","helper"}`; (b) `tpl.Report()` returns a copy — mutating the returned `Constructs` slice does not affect a subsequent call; (c) for templates parsed via legacy `Parse(s)`, `tpl.Report()` returns the documented zero-valued report (`Substitutions == 0`, empty `Constructs`); (d) on a `*CapabilityError` return, `Loc.Line > 0 && Loc.Column > 0`.

### Implementation for User Story 4

- [ ] T021 [US4] In `parse_visitor.go`, finalise the `ParseReport` populated at end of walk: convert the `map[string]struct{}` into a sorted, deduplicated `[]string`. Ensure helper-style mustaches that are *allowed* (full-mode legacy path doesn't even run the visitor, so this only matters under granular/simple — both reject helpers — therefore `"helper"` only appears in the report on the future full-mode-with-visitor path; document in code comment that `"helper"` is reserved for that case).
- [ ] T022 [US4] In `template.go`, ensure `(*Template).Report()` returns a copy where the `Constructs` slice is freshly allocated so callers cannot mutate the template's internal state. Make T020 pass.

**Checkpoint**: US4 functional; all four user stories independently complete.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: No-panic invariant, performance benchmark, options-surface table tests, documentation, and the quickstart smoke run.

- [ ] T023 [P] Add a `t.Run("nopanic-corpus", ...)` subtest in `parse_visitor_test.go` that walks the existing fixture corpora under `handlebars/` and `mustache/` (read fixtures via the same helpers those packages already use, or replicate a minimal load of `*.tmpl` files) calling `ParseWithOptions(src, ParseOptions{Mode: ModeSimple})` and `ParseWithOptions(src, ParseOptions{Capabilities: Capabilities{If:true, Iteration:true, Partials:true}, Budget: Budget{MaxSubstitutions: 1<<20, Enforced: true}})`, each inside a `defer recover()` that fails the test on any panic (covers [C8](contracts/api.md), FR-013).
- [ ] T024 [P] Create `parse_options_test.go` at repo root with a table test that exercises the precedence rules from [data-model.md §2](data-model.md): zero `ParseOptions{}` → legacy; `ModeSimple` overrides `Capabilities`; `ModeFull` + any toggle true → granular; `ModeFull` + all toggles false + `Budget.Enforced:true` → granular (rejects everything but plain substitution and text). Each row asserts the resulting acceptance/rejection of a known fixture template.
- [ ] T025 Modify `benchmark_test.go` at repo root to add `BenchmarkParseWithOptions_Full` (zero-options path — must not enter the visitor) and `BenchmarkParseWithOptions_Granular` (all toggles on + 1 MiB budget) over a representative template that mixes ~50 substitutions, an `#if`, and an `#each`. Per [C9](contracts/api.md) and Constitution Principle V, the visitor-off path must show no measurable regression vs. the existing parse benchmark; the visitor-on path must stay within an order of magnitude.
- [ ] T026 [P] Add package-level godoc comments at the top of `parse_options.go`, `parse_report.go`, `parse_errors.go`, and `parse_visitor.go` summarising the feature and pointing readers to the quickstart; ensure every exported identifier has a doc comment that begins with its name (Go convention).
- [ ] T027 Update `CHANGELOG.md` "Unreleased" entry from T002 with the final list of new exported identifiers (`Mode`, `ModeFull`, `ModeSimple`, `Capabilities`, `Budget`, `ParseOptions`, `ParseReport`, `BudgetExceededError`, `CapabilityError`, `ParseWithOptions`, `(*Template).Report`) and a one-line note that `Parse`/`MustParse`/`ParseFile`/`Render` are unchanged.
- [ ] T028 Run the [quickstart.md manual validation script](quickstart.md) (steps 1–12) end-to-end against the built binary; record observed `go test -bench=ParseWithOptions -benchmem` numbers for inclusion in the PR description per Principle V.
- [ ] T029 Run `go test ./...` from repo root and confirm: (a) every existing test still passes; (b) all new tests T008–T024 pass; (c) `go vet ./...` is clean; (d) no new exported identifier lacks a godoc comment.

---

## Dependencies & Execution Order

### Phase Dependencies

- Phase 1 (Setup): no dependencies.
- Phase 2 (Foundational): depends on Phase 1; **blocks all user stories**.
- Phase 3 (US1): depends on Phase 2.
- Phase 4 (US2): depends on Phase 2; independent of US1.
- Phase 5 (US3): depends on Phase 2; independent of US1/US2.
- Phase 6 (US4): depends on Phase 2; reads report data populated by the visitor (US1/US2/US3 implementations) but its tests only need the visitor to compile and run a single full-coverage path.
- Phase 7 (Polish): depends on whichever user stories are intended to ship.

### Within Each User Story

- Tests come before implementation (T008–T011 before T012; T014 before T015; T017 before T018; T020 before T021).
- T006 depends on T003–T005 and T007.
- T007 depends on T003–T005.

### Parallel Opportunities

- Phase 2: T003, T004, T005 are all `[P]` (different files). T007 can start as soon as T003–T005 land. T006 depends on T007.
- Phase 3 tests T008, T009, T010, T011 are `[P]` (T008 in `template_test.go`, T009/T011 in `parse_visitor_test.go` distinct subtests, T010 in `parse_errors_test.go`).
- Across user stories: once Phase 2 is done, US1, US2, US3, US4 can proceed in parallel by different developers — each story's red tests live in different files or different `t.Run` subtests, and the implementations all converge on `parse_visitor.go` (sequential merging required there).
- Phase 7: T023, T024, T026 are `[P]`; T025 modifies `benchmark_test.go` alone; T027 modifies `CHANGELOG.md` alone.

---

## Parallel Example: Phase 2 Foundational

```bash
# Launch the three options/report/error type files in parallel:
Task: "Create parse_options.go (T003)"
Task: "Create parse_report.go (T004)"
Task: "Create parse_errors.go (T005)"
# Then in sequence:
Task: "Create parse_visitor.go (T007)"
Task: "Wire ParseWithOptions in template.go (T006)"
```

## Parallel Example: User Story 1 Tests

```bash
Task: "Backward-compat regression in template_test.go (T008)"
Task: "Budget subtest table in parse_visitor_test.go (T009)"
Task: "Error-message tests in parse_errors_test.go (T010)"
Task: "No-panic-budget subtest in parse_visitor_test.go (T011)"
```

---

## Implementation Strategy

### MVP First (US1 + US2)

Both P1 stories are required for the MVP because the constitution's compute-safety guarantee requires both a count ceiling and a control-flow ceiling.

1. Phase 1 → Phase 2.
2. Phase 3 (US1): land budget enforcement; ship internal preview.
3. Phase 4 (US2): land simple-mode rejection; **MVP ready**.
4. Run [quickstart.md](quickstart.md) §1, §2, §3 against the build.

### Incremental Delivery

5. Phase 5 (US3): granular toggles unlock real-world cases.
6. Phase 6 (US4): observability completes the operator story.
7. Phase 7: polish, benchmarks, docs.

### Parallel Team Strategy

After Phase 2:
- Developer A: US1 (T008–T013).
- Developer B: US2 (T014–T016).
- Developer C: US3 (T017–T019).
- Developer D: US4 (T020–T022) — coordinates with A/B/C on `parse_visitor.go` edits.

---

## Notes

- `[P]` = different files (or independent subtests) and no dependency on incomplete tasks.
- `[Story]` label appears only on Phase 3–6 tasks per the format spec.
- All file paths are repo-root-relative; this project has no `src/`/`tests/` subdirs.
- TDD is enforced: every implementation task is preceded by a test task that must fail first.
- Visitor edits in `parse_visitor.go` are sequential — coordinate merges across US1/US2/US3.
- Public API (`Parse`, `MustParse`, `ParseFile`, `Render`, `MustRender`) MUST stay byte-identical in behaviour for any caller that does not opt in.
