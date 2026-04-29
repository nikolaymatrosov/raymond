# Phase 0 Research: Parse Budget & Template Capability Modes

**Feature**: 001-parse-budget-template-modes
**Date**: 2026-04-29

The Technical Context in `plan.md` carries no `NEEDS CLARIFICATION`
markers — the spec's three clarification rounds (2026-04-29) resolved
the only ambiguities (`{{#with}}` classification, simple-mode path
forms, partials toggle). The remaining work is to anchor the
implementation choices in what the existing codebase already provides.
Each item below records a decision, the rationale, and the alternatives
considered.

---

## R1. Where to enforce capability and budget rules

- **Decision**: Enforce by walking `*ast.Program` after `parser.Parse`
  returns successfully, using a new internal type that implements
  `ast.Visitor`. The walk runs inside `(*Template).parse` only when the
  caller used `ParseWithOptions`.
- **Rationale**: The `ast` package already exposes a complete `Visitor`
  interface (`VisitProgram`, `VisitMustache`, `VisitBlock`,
  `VisitPartial`, `VisitContent`, `VisitComment`, `VisitExpression`,
  `VisitSubExpression`, `VisitPath`, …) and every node implements
  `Accept(Visitor)`. Each node carries `ast.Loc` for line/column. This
  gives us substitution counting, construct detection, and source
  locations for free, without modifying the parser or lexer (which the
  constitution and spec both prefer). It also keeps the feature
  surgical: no AST changes, no parser changes, no `panic` paths.
- **Alternatives considered**:
  - *Hook into the parser/lexer*: Rejected. Higher blast radius; the
    parser currently uses `panic`/`recover` for error propagation, and
    threading new error categories through it would be more invasive
    than a post-parse walk. Constitution flags new panic paths on
    caller input as something reviewers MUST reject.
  - *Enforce at render time*: Rejected by spec FR-011/FR-012 — must be
    parse-time and context-data-independent.

## R2. How to count "substitutions"

- **Decision**: A substitution is one `*ast.MustacheStatement` whose
  expression produces output. Concretely: every `MustacheStatement`
  visited counts as exactly 1, except those whose underlying token is
  a comment (handlebars comments parse as `*ast.CommentStatement`, not
  mustaches, so they are naturally excluded). Triple-stash `{{{x}}}`
  and whitespace-control `{{~x~}}` / `{{- x -}}` parse to the same
  `MustacheStatement` node and so count as 1 each. `BlockStatement`
  nodes (the `#if`/`#each`/`#with` openers) and `PartialStatement`
  nodes do NOT contribute to the substitution count — they are
  governed by capability mode, per spec assumption.
- **Rationale**: Matches spec edge cases (whitespace-control and triple
  stash count as 1; comments are 0; substitutions inside permitted
  blocks count normally) and follows directly from the existing AST
  shape — no new node bookkeeping required.
- **Alternatives considered**:
  - *Count by lexer token*: Rejected. Conflates whitespace-control
    variants and triple-stash counting and would require lexer-level
    introspection not exposed today.
  - *Count by `ast.Expression`*: Rejected. Would over-count
    sub-expressions inside helper invocations.

## R3. Capability detection per construct

- **Decision**: Map AST nodes to capability families as follows:

  | AST node / shape                                        | Family            | Allowed when |
  |---|---|---|
  | `*ast.ContentStatement`                                 | text              | always |
  | `*ast.CommentStatement`                                 | comment           | always |
  | `*ast.MustacheStatement` with a `*ast.PathExpression` whose path is current-context (bare / dotted / indexed / `this`) and no params/hash | plain substitution | always (counted) |
  | `*ast.MustacheStatement` with params, hash, sub-expr, parent path (`../x`), or `@`-data path | helper-style      | full mode only |
  | `*ast.BlockStatement` with helper name `if` / `unless`  | conditional       | if `Capabilities.If` true |
  | `*ast.BlockStatement` with helper name `each`           | iteration         | if `Capabilities.Iteration` true |
  | `*ast.BlockStatement` with helper name `with`           | context-shift     | full mode only (FR-005, no independent toggle) |
  | `*ast.BlockStatement` with any other helper name        | helper-style block| full mode only |
  | `*ast.PartialStatement` (any of static, dynamic, inline `{{#*inline}}`, or partial-block `{{#> name}}`) | partial | if `Capabilities.Partials` true |

- **Rationale**: Path classification is read directly off
  `PathExpression.Data` (true for `@`-data) and `PathExpression.Depth`
  (>0 for `../`); the current-context whitelist is exactly those with
  `Data == false`, `Depth == 0`, and parts that are identifiers,
  dotted, indexed, or the literal `this`. Block-helper kind is read off
  `BlockStatement.Expression.Path` (a `PathExpression` whose first part
  is the helper name). Partial-block forms are all represented as
  `*ast.PartialStatement` variants in the existing AST, so a single
  visit method covers the FR-005 requirement that the partials toggle
  applies uniformly across all forms.
- **Alternatives considered**:
  - *Allowlist of helper names broader than `if`/`each`/`with`*:
    Rejected — out of scope per spec Assumption ("Granular per-helper
    allowlists … are out of scope").

## R4. Errors: typing and category distinguishability (FR-008)

- **Decision**: Two new exported error types in the root package:
  - `BudgetExceededError{ Kind, Limit, Observed }`
  - `CapabilityError{ Construct string; Loc ast.Loc }`
  Both implement `error`. They are distinct concrete types so callers
  can use `errors.As`. Existing parser syntax errors (returned by
  `parser.Parse`) remain untouched, so the three categories — syntax,
  budget, capability — are distinguishable by type assertion.
- **Rationale**: Matches the spec's "separate error categories"
  requirement and the constitution's "distinct, exported error type"
  for budget breaches. `errors.As` is the idiomatic Go way; sentinel
  values would not carry the per-occurrence fields the spec mandates.
- **Alternatives considered**:
  - *Single `ParseError` with a kind enum*: Rejected — coarser API and
    more awkward for callers that want to handle one category but
    propagate the others.

## R5. Public API shape

- **Decision**: Add (all in the root `raymond` package):
  - `type Mode int` with `ModeFull` (default zero value chosen to
    preserve back-compat by accident-of-default — see R6) and
    `ModeSimple`.
  - `type Capabilities struct { If, Iteration, Partials bool }`.
  - `type Budget struct { MaxSubstitutions int }` where 0 means "no
    limit" (back-compat) and a positive value is the ceiling. Negative
    values are clamped to "no limit".
  - `type ParseOptions struct { Mode Mode; Capabilities Capabilities; Budget Budget }`
    where the zero `ParseOptions` value is "full mode, no budget" — i.e.
    identical behaviour to legacy `Parse`. Setting `Mode = ModeSimple`
    overrides individual `Capabilities` toggles.
  - `func ParseWithOptions(source string, opts ParseOptions) (*Template, error)`.
  - `func (*Template) Report() ParseReport` returning a copy.
  - `type ParseReport struct { Substitutions int; Constructs map[string]bool }`
    or a slice/set type — see data-model.md for the final shape.
- **Rationale**: An options struct (vs. variadic functional options)
  matches the existing project style — `template.go` uses plain
  structs and methods, no functional-option pattern in the codebase.
  Adding `ParseWithOptions` rather than mutating `Parse` keeps the
  Principle II contract for existing callers verbatim.
- **Alternatives considered**:
  - *Functional options* (`ParseWithOptions(source, WithBudget(n), WithSimpleMode())`):
    Rejected — inconsistent with the rest of the codebase.
  - *Mutating `Parse(source string, opts ...ParseOptions)`*: Rejected
    — any change to an exported signature is a Principle II hazard
    even when variadic, and the build might break consumers that take
    `Parse` by value as a `func(string) (*Template, error)`.

## R6. Default semantics — preserving FR-010 / SC-004

- **Decision**: A zero-valued `ParseOptions{}` results in: full mode,
  all capabilities effectively on (full mode bypasses the
  `Capabilities` struct entirely), and `Budget{}` meaning no limit. So
  even a caller that opts into `ParseWithOptions` but passes the zero
  value gets behaviour identical to legacy `Parse`. This is the
  property that keeps SC-004 trivially true.
- **Rationale**: Required by FR-010 and the constitution's Principle II.
- **Alternatives considered**: None — the spec is explicit.

## R7. Where the visitor lives in the package layout

- **Decision**: Root package (file `parse_visitor.go`), not the `ast`
  subpackage.
- **Rationale**: The visitor must reference root-package error types
  (`CapabilityError`) and is invoked from `(*Template).parse`. Placing
  it in `ast/` would create an import cycle (`ast` → root → `ast`).
  The root package already imports `ast` for `*ast.Program`, so the
  layout is natural.
- **Alternatives considered**:
  - *New `parseopt/` subpackage*: Rejected — overkill for ~4 small
    files and would force the same root-package import that the
    in-root layout already has, with no architectural gain.

## R8. Benchmarks (Constitution Principle V)

- **Decision**: Add `BenchmarkParseWithOptions_Full` and
  `BenchmarkParseWithOptions_Simple` next to the existing parse
  benchmark in `benchmark_test.go`, on a representative template that
  mixes literal text, substitutions, and an `if`/`each` block. Report
  before/after `go test -bench` deltas in the PR description per
  Principle V; default (legacy) `Parse` must show no measurable
  regression since it never enters the visitor.
- **Rationale**: Direct constitutional requirement.
- **Alternatives considered**: None.
