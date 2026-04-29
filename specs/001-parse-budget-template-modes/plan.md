# Implementation Plan: Parse Budget & Template Capability Modes

**Branch**: `001-parse-budget-template-modes` | **Date**: 2026-04-29 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-parse-budget-template-modes/spec.md`

## Summary

Add parse-time enforcement of (a) a caller-supplied **substitution-count budget**
and (b) a caller-supplied **capability mode** (preset `simple`/`full` plus
independent toggles for `if`, iteration, and partials) to Raymond's
`Parse` entry point. Enforcement happens by walking the existing
`*ast.Program` after `parser.Parse` succeeds, using a new internal
`ast.Visitor` implementation that counts substitution-producing
mustaches and rejects any node whose construct is disallowed by the
configured capabilities. Failures return new typed errors
(`BudgetExceededError`, `CapabilityError`) carrying budget kind/limit/
observed value or construct name and `ast.Loc`. On success a read-only
`ParseReport` is attached to `*Template`. Existing `Parse`/`MustParse`
keep their current signatures; new behaviour is opt-in via
`ParseWithOptions(source, ParseOptions)` so backward compatibility
with all existing callers is preserved (FR-010, SC-004).

## Technical Context

**Language/Version**: Go 1.18+ (existing module — `go.mod` to be confirmed; project uses `testing.B` benchmarks and the codebase predates generics, so the floor is whatever the project already targets).
**Primary Dependencies**: Internal only — `github.com/aymerick/raymond/ast`, `…/parser`, `…/lexer`. No new external deps.
**Storage**: N/A (library, in-memory AST).
**Testing**: Standard `go test ./...`; per-package `*_test.go` and root-level `raymond_test.go` / `template_test.go`. Existing handlebars/mustache compatibility suites must continue to pass unchanged.
**Target Platform**: Pure-Go library, all platforms supported by the toolchain.
**Project Type**: single-project Go library (flat root package + `ast`/`lexer`/`parser`/`handlebars`/`mustache` subpackages).
**Performance Goals**: Capability/budget walk MUST be O(nodes) and a single linear pass over the parsed AST; constitution SC-001 — rejection within the same order of magnitude as parsing the template.
**Constraints**: No panics on caller-controlled input (Constitution Principle I, FR-013). No render-time work; capability rejection MUST be context-data-independent (FR-011, FR-012). No new positional tracking — reuse `ast.Loc` already attached to every node.
**Scale/Scope**: API surface is small — one options struct, one report struct, two error types, one new public function, one new method on `*Template`. Internal addition: one `ast.Visitor` impl (~one file).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Evaluating against `/Users/nikthespirit/Documents/experiment/raymond/.specify/memory/constitution.md` v1.0.0:

| Principle / Section | Status | Notes |
|---|---|---|
| I. Compute Safety by Default | ✅ PASS | This feature *is* the parse-budget surface for substitution count, the first concrete instantiation of the constitution's "parse budget" requirement. New errors are typed (`BudgetExceededError`, `CapabilityError`); no `panic` paths added. Other parse-budget axes (bytes, AST nodes, nesting depth) remain follow-ups per the constitution; this plan does not regress them. |
| II. Backward-Compatible Public API | ✅ PASS | Existing `Parse`, `MustParse`, `ParseFile`, `Render`, `MustRender` keep their signatures and behaviour. Default behaviour with no opts is unchanged (FR-010). New API is additive (`ParseOptions`, `ParseWithOptions`, `(*Template).Report`, error types). No exported identifier is removed or renamed. |
| III. Test-First Development | ✅ PASS | Phase 1 contracts define the failing tests (capability/budget red cases + within-budget green cases + observability) before implementation. Both within-budget and over-budget paths covered, and the no-panic property is asserted explicitly. |
| IV. Deterministic, Side-Effect-Free Evaluation | ✅ PASS | Feature is purely parse-time. Adds no I/O, no goroutines, no clock reads. Does not touch the evaluator. |
| V. Performance Transparency | ✅ PASS — with action item | The capability/budget visitor adds one linear AST walk to opted-in `Parse` calls. Plan includes a benchmark addition to `benchmark_test.go` covering (a) baseline `Parse` (must be unchanged within noise — opt-in) and (b) `ParseWithOptions` over a representative template; before/after numbers reported in PR description per Principle V. |
| Resource Budget Standards | ✅ PASS | Adds the substitution-count axis to the parse-budget surface; `Budget` struct is shaped to carry future axes (node count, depth) without API churn (matches the spec's "Parse Budget" entity). Negative/absent budget → no limit, preserving back-compat. |
| Development Workflow & Quality Gates | ✅ PASS | All new behaviour lands with `go test ./...` coverage; benchmark smoke runs; package godoc on new exported identifiers; `CHANGELOG.md` entry for the new opt-in API. |

**Gate result**: PASS. No violations to record in Complexity Tracking.

**Post-design re-check (after Phase 1)**: Re-evaluated against the contracts and data model produced in Phase 1; no new violations introduced. The visitor lives in the root package (not a new subpackage) to avoid an import cycle with `ast`, which is the simplest layout consistent with the existing structure — no architectural complexity added.

## Project Structure

### Documentation (this feature)

```text
specs/001-parse-budget-template-modes/
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
to the existing root package and reuses the `ast` subpackage's visitor
infrastructure. No new packages are introduced.

```text
.
├── ast/                       # existing — Visitor interface & node types (unchanged)
│   ├── node.go
│   └── print.go
├── lexer/                     # existing — unchanged
├── parser/                    # existing — unchanged
├── handlebars/                # existing — handlebars.js compat suite (unchanged)
├── mustache/                  # existing — mustache compat suite (unchanged)
│
├── raymond.go                 # existing — Render/MustRender (unchanged)
├── template.go                # MODIFIED — add ParseWithOptions, (*Template).Report, wire visitor into parse
├── parse_options.go           # NEW — ParseOptions, Budget, Capabilities, Mode presets, builders
├── parse_report.go            # NEW — ParseReport struct + accessors
├── parse_errors.go            # NEW — BudgetExceededError, CapabilityError types
├── parse_visitor.go           # NEW — internal ast.Visitor impl: counts subs, enforces caps
│
├── parse_options_test.go      # NEW — table tests for ParseOptions surface (presets/toggles)
├── parse_report_test.go       # NEW — observability assertions
├── parse_errors_test.go       # NEW — typed-error fields + error-category distinguishability
├── parse_visitor_test.go      # NEW — capability/budget red+green paths, no-panic property
├── benchmark_test.go          # MODIFIED — add ParseWithOptions benchmark next to existing parse bench
└── CHANGELOG.md               # MODIFIED — entry under Unreleased: opt-in parse budget + capability modes
```

**Structure Decision**: Single-project library, flat root package. The
new visitor must live in the root package (not in `ast/`) because it
constructs root-package error types and is invoked from
`(*Template).parse`; placing it in `ast/` would create an import cycle.
The `ast.Visitor` interface is reused as-is — no changes to AST types
or to `parser`/`lexer`. This is the smallest layout that satisfies the
spec and constitution; no alternative structure was needed.

## Complexity Tracking

> No Constitution Check violations. Section intentionally empty.
