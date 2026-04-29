<!--
Sync Impact Report
==================
Version change: (template / unversioned) → 1.0.0
Rationale: Initial ratification of project constitution. All template
placeholders replaced with concrete principles tailored to Raymond, a Go
Handlebars implementation. MAJOR bump from unversioned template to 1.0.0.

Modified principles:
  - [PRINCIPLE_1_NAME] → I. Compute Safety by Default (NON-NEGOTIABLE)
  - [PRINCIPLE_2_NAME] → II. Backward-Compatible Public API
  - [PRINCIPLE_3_NAME] → III. Test-First Development (NON-NEGOTIABLE)
  - [PRINCIPLE_4_NAME] → IV. Deterministic, Side-Effect-Free Evaluation
  - [PRINCIPLE_5_NAME] → V. Performance Transparency

Added sections:
  - Resource Budget Standards (parse + evaluation budget requirements)
  - Development Workflow & Quality Gates

Removed sections: none

Templates requiring updates:
  - ✅ .specify/templates/plan-template.md (Constitution Check is generic;
    references this file — no edit required)
  - ✅ .specify/templates/spec-template.md (no constitution-specific
    sections — no edit required)
  - ✅ .specify/templates/tasks-template.md (no constitution-specific
    sections — no edit required)

Follow-up TODOs:
  - TODO(RATIFICATION_DATE): Original adoption date set to today
    (2026-04-29) since no prior ratified constitution existed for this
    project; revise if an earlier authoritative date is identified.
-->

# Raymond Constitution

Raymond is a Go implementation of Handlebars templates. As a library
embedded in other Go programs, it executes user- or operator-supplied
templates against caller-supplied data. This constitution governs how
the project evolves so that it remains safe, predictable, and
trustworthy for hosts that may receive untrusted templates or data.

## Core Principles

### I. Compute Safety by Default (NON-NEGOTIABLE)

Every code path that parses or evaluates a template MUST be bounded by
explicit, configurable resource budgets. At minimum:

- A **parse budget** MUST cap input size (bytes), maximum AST node
  count, and maximum nesting depth. Exceeding any limit MUST abort
  parsing with a typed error and MUST NOT panic.
- An **evaluation budget** MUST cap total evaluation steps (helper
  invocations, partial expansions, block iterations), maximum render
  output size, maximum recursion depth (including partials and dynamic
  partials), and an optional wall-clock deadline via `context.Context`.
- Defaults MUST be safe for embedding in services that accept
  untrusted templates. Removing or relaxing a budget for a single call
  MUST be an explicit opt-in on the caller's side, never the library
  default.

**Rationale**: Handlebars features such as partials, dynamic partials,
recursive helpers, and `{{#each}}` over user-controlled data make
unbounded compute and memory growth trivial to trigger. Compute-safety
must be a property of the library, not a discipline asked of every
caller.

### II. Backward-Compatible Public API

The exported Go API (functions, types, methods, and documented
behaviour in `raymond.go`, `template.go`, `eval.go`, `helper.go`,
`partial.go`, and the `ast`, `lexer`, `parser`, `handlebars`, and
`mustache` subpackages) is the project's contract.

- Removing or renaming exported identifiers, or changing their
  signatures, requires a MAJOR version bump.
- New budget enforcement MUST ship behind APIs whose default behaviour
  preserves the rendered output of pre-existing valid templates that
  fit within the new default budgets.
- When a default budget would reject previously accepted input, the
  change MUST be documented in `CHANGELOG.md` with the rationale and
  the opt-out path.

**Rationale**: Raymond is consumed as a Go module by downstream
projects; silent contract drift would force every consumer to audit
upgrades.

### III. Test-First Development (NON-NEGOTIABLE)

All behaviour changes MUST land with tests:

- New features and bug fixes MUST add a failing test first, then the
  implementation that makes it pass (red → green → refactor).
- Budget enforcement MUST have tests that exercise both the
  within-budget success path and the over-budget failure path,
  asserting the typed error and the absence of panics.
- Compatibility with `handlebars.js` 3.0 semantics MUST continue to be
  validated by the existing `handlebars/` and `mustache/` suites; any
  intentional divergence MUST be documented.

**Rationale**: Templating engines have wide surface area and subtle
edge cases (whitespace control, escaping, helper resolution). Tests
are the only durable record of intended behaviour.

### IV. Deterministic, Side-Effect-Free Evaluation

Given the same template, context data, helpers, and partials, render
output MUST be deterministic. The core evaluator MUST NOT perform I/O,
read environment variables, access the filesystem, start goroutines,
or read clocks. Helpers supplied by callers may do any of these — but
the evaluator itself MUST treat helpers as opaque functions and MUST
enforce the evaluation budget around their invocations.

**Rationale**: Determinism is a prerequisite for caching, snapshot
testing, sandboxing, and reasoning about compute budgets. Hidden side
effects in the core would break all four.

### V. Performance Transparency

Performance-sensitive changes MUST be accompanied by benchmarks:

- Benchmarks live alongside the code they measure (`benchmark_test.go`
  and package-local `*_test.go` files using `testing.B`).
- Pull requests that change parsing, evaluation, or escaping MUST
  report before/after `go test -bench` numbers in the description.
- Regressions greater than 10% on any documented benchmark MUST be
  justified in `BENCHMARKS.md` or reverted.

**Rationale**: Raymond is on the hot path of services that render many
templates per request; silent perf regressions are user-visible.

## Resource Budget Standards

This section is normative and refines Principle I.

- **Configuration surface**: Budgets MUST be expressible per
  `Template` instance and MUST be overridable per `Exec`/`MustExec`
  call. A `context.Context`-aware execution entry point MUST exist so
  that callers can impose deadlines and cancellation.
- **Default budgets** (subject to refinement during implementation,
  documented in `CHANGELOG.md` whenever changed):
  - Max template source size: 1 MiB.
  - Max AST nodes: 100 000.
  - Max parse/eval nesting depth: 64.
  - Max evaluation steps: 1 000 000.
  - Max render output size: 16 MiB.
  - No default wall-clock deadline (callers opt in via `context`).
- **Failure mode**: Budget exhaustion MUST return a distinct, exported
  error type (e.g. `BudgetExceededError`) that identifies which budget
  was exceeded. Budgets MUST NOT be enforced by `panic`. Existing
  `panic`-based error paths in parser/lexer code MUST be migrated to
  typed errors as part of this work.
- **Observability**: The library MUST expose, at minimum, the budget
  consumption observed during a successful render (e.g. node count,
  step count, peak depth) so callers can tune limits.

## Development Workflow & Quality Gates

- **Code review**: Every change requires review. Reviewers MUST verify
  compliance with Principles I–V, and MUST reject changes that
  introduce unbounded loops, recursion without depth tracking, or new
  panics on caller-controlled input.
- **CI gates**: `go test ./...`, `go vet ./...`, and the benchmark
  suite (smoke run) MUST pass before merge. Budget-enforcement tests
  MUST be part of the standard test target, not a separate opt-in.
- **Fuzzing**: Parser and evaluator entry points SHOULD have Go fuzz
  targets (`testing.F`) exercised in CI. Crashers MUST be added as
  regression tests when fixed.
- **Documentation**: User-visible changes MUST update `README.md`
  and/or `CHANGELOG.md`. New budget knobs MUST appear in the package
  godoc with their default values.

## Governance

This constitution supersedes informal practices and prior conventions
where they conflict.

- **Amendment procedure**: Amendments are proposed via pull request
  that edits `.specify/memory/constitution.md`, includes a Sync Impact
  Report comment at the top of the file, and updates the version line
  at the bottom. Amendments require approval from a project
  maintainer.
- **Versioning policy**: Semantic versioning of this document.
  - MAJOR: Backward-incompatible removal or redefinition of a
    principle, or removal of a normative section.
  - MINOR: New principle or section, or material expansion of an
    existing one.
  - PATCH: Wording clarifications, typo fixes, non-semantic edits.
- **Compliance review**: Constitution compliance is checked at the
  Constitution Check gate of `/speckit.plan` and during code review.
  Violations MUST be either fixed or recorded in the plan's Complexity
  Tracking table with explicit justification.
- **Runtime guidance**: For day-to-day implementation guidance not
  covered here (style, layout, tooling), defer to `CLAUDE.md` and the
  `README.md`.

**Version**: 1.0.0 | **Ratified**: 2026-04-29 | **Last Amended**: 2026-04-29
