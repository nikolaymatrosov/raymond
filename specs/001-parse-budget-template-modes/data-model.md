# Phase 1 Data Model: Parse Budget & Template Capability Modes

**Feature**: 001-parse-budget-template-modes
**Date**: 2026-04-29

This feature is library-internal; "data model" here means the Go types
that constitute the new public API and the internal visitor state. All
types live in the root `raymond` package unless noted.

---

## 1. `Mode` (enum)

```go
type Mode int

const (
    ModeFull   Mode = iota // 0 — default, legacy behaviour (full Handlebars)
    ModeSimple             // text + plain current-context substitutions only
)
```

- **Why `ModeFull == 0`**: a zero-valued `ParseOptions{}` must mean
  "no enforcement, behave like legacy `Parse`" (FR-010, SC-004).
- **Validation**: any unknown integer value MUST be treated as
  `ModeFull` for forward-compatibility, and a future preset addition
  is a MINOR change.
- **State transitions**: none — value type.

## 2. `Capabilities` (struct)

```go
type Capabilities struct {
    If         bool // permits {{#if}} / {{#unless}} (else branches follow their parent)
    Iteration  bool // permits {{#each}}
    Partials   bool // permits {{> name}}, {{> (expr)}}, {{#*inline}}, {{#> name}} uniformly
}
```

- **Semantics**: Read only when `Mode == ModeFull` is *not* in effect
  AND the caller has not explicitly chosen another preset. In
  `ModeSimple`, `Capabilities` is ignored (everything except plain
  substitution is rejected). In `ModeFull`, `Capabilities` is
  effectively all-true regardless of the field values.
- **Why these three fields and no more**: spec FR-005 names exactly
  conditional, iteration, and partials as the independent toggles.
  `{{#with}}` is intentionally *not* a field (FR-005, clarified
  2026-04-29 — full-mode-only).
- **Granular-mode interpretation**: When `Mode == ModeFull` the struct
  is ignored. When the caller wants granular behaviour they keep
  `Mode == ModeFull` *not* the right choice — they leave `Mode` at the
  default and set fields. Concretely the precedence is:
  1. If `Mode == ModeSimple` → simple semantics, ignore `Capabilities`.
  2. Else if any of `If`/`Iteration`/`Partials` is true OR a non-zero
     `Budget` is present → granular mode: only the specified
     capabilities are permitted, plus plain substitution and text;
     `{{#with}}` and helper-style mustaches are rejected.
  3. Else (zero `ParseOptions{}`) → legacy "full" semantics — no
     capability checks, no budget.
  This makes the zero value back-compatible while letting callers opt
  into a granular mode without naming a preset.

## 3. `Budget` (struct)

```go
type Budget struct {
    MaxSubstitutions int // ≤ 0 means "no limit"
}
```

- **Validation**: A `MaxSubstitutions` value of 0 or negative is
  treated as "no enforcement" (back-compat, FR-010, plus spec edge
  case "Negative or absent budget"). A positive value is enforced
  inclusively — `MaxSubstitutions = 100` permits exactly 100, rejects
  101 (spec acceptance scenarios US1 #1 and #2).
- **Edge case**: A budget of *exactly* 0 is "no limit" by this rule;
  callers wanting "permit no substitutions at all" express it as
  `ModeSimple` with a budget that cannot be exceeded by zero
  substitutions, OR via a future explicit `Limited bool` flag. Spec's
  edge case "Budget set to zero … causes any template containing at
  least one substitution to be rejected" is therefore implemented by
  treating `MaxSubstitutions = 0` differently *only* when the caller
  has explicitly opted into a limit. To keep the zero-value semantics
  for `Budget{}` clean, a sentinel is used: a separate boolean
  `Enforced` field. Final shape (revised):

```go
type Budget struct {
    MaxSubstitutions int  // ceiling (used when Enforced is true)
    Enforced         bool // true means "MaxSubstitutions is a real limit"
}
```

  - `Budget{}` → no limit (back-compat).
  - `Budget{MaxSubstitutions: 100, Enforced: true}` → limit of 100.
  - `Budget{MaxSubstitutions: 0, Enforced: true}` → reject any
    substitution (matches the spec's "budget set to zero" edge case).
- **Forward-compat**: future axes (e.g. `MaxNodes`, `MaxDepth`) can be
  added as additional fields without breaking the contract.

## 4. `ParseOptions` (struct)

```go
type ParseOptions struct {
    Mode         Mode
    Capabilities Capabilities
    Budget       Budget
}
```

- **Zero value**: `ModeFull` + empty caps + unenforced budget = legacy
  behaviour (FR-010).
- **Lifecycle**: caller-owned, read-only from the library's
  perspective once passed to `ParseWithOptions`.

## 5. `ParseReport` (struct)

```go
type ParseReport struct {
    Substitutions int      // observed count of substitution-producing mustaches
    Constructs    []string // sorted, deduplicated set: subset of {"if","unless","each","with","partial","helper"}
}
```

- **Semantics**: Returned by `(*Template).Report()` after a successful
  `ParseWithOptions`. Spec FR-009, SC-006.
- **Constructs vocabulary** (closed set for this feature): `"if"`,
  `"unless"`, `"each"`, `"with"`, `"partial"`, `"helper"` (any
  mustache or block whose helper is none of the above). Plain
  substitution is *not* a "construct" — it is reflected in
  `Substitutions`.
- **Mirror on error**: For `BudgetExceededError` and `CapabilityError`,
  the same diagnostic fields (observed count where meaningful, source
  loc for capability) are present on the error itself (spec US4
  Acceptance Scenario #2).
- **Mutability**: `Report()` returns a copy — callers cannot mutate
  the template's stored report.

## 6. `BudgetExceededError` (struct, implements `error`)

```go
type BudgetExceededError struct {
    Kind     string // for this feature, always "substitutions"
    Limit    int
    Observed int
}

func (e *BudgetExceededError) Error() string
```

- **Why a `Kind` string**: future axes (nodes, depth) reuse the same
  type, distinguishing themselves by `Kind`. Matches the spec's
  "Parse Budget" entity description.

## 7. `CapabilityError` (struct, implements `error`)

```go
type CapabilityError struct {
    Construct string  // one of: "if","unless","each","with","partial","helper","parent-path","data-var"
    Loc       ast.Loc // line/column from the AST node that triggered the rejection
}

func (e *CapabilityError) Error() string
```

- **Why these construct names**: covers FR-007 ("names the offending
  construct") and the spec's edge cases — parent-context paths and
  `@`-data variables in simple mode get their own construct names
  rather than being lumped under `"helper"`, so callers can give a
  precise diagnostic.

## 8. Internal: capability/budget visitor

Lives in `parse_visitor.go`, root package, *not* exported.

```go
type capVisitor struct {
    opts        ParseOptions
    granular    bool  // pre-computed: capabilities-or-budget enforcement active
    simple      bool  // opts.Mode == ModeSimple
    fullLegacy  bool  // zero-options path — visitor never runs

    subs       int
    constructs map[string]struct{}

    err error // first violation; subsequent visits short-circuit
}
```

- **Walk strategy**: Implements `ast.Visitor`. For each node it (a)
  short-circuits if `err != nil`; (b) updates `subs`/`constructs`;
  (c) on detecting a forbidden construct sets `err =
  &CapabilityError{...}`; (d) after walking the program, checks
  `subs` against the budget and sets `err = &BudgetExceededError{...}`
  if exceeded.
- **No-panic property**: All recursion is via `Accept`, which already
  does not panic; no map-of-nil dereference paths (maps initialised in
  the constructor).

---

## Cross-reference to spec

- FR-001, FR-002, FR-003 → §3 `Budget`, §6 `BudgetExceededError`.
- FR-004 → §1 `Mode`, §8 visitor's simple-mode branch (path
  classification per research R3).
- FR-005 → §2 `Capabilities` (exactly three fields).
- FR-006 → §1 `Mode.ModeFull`.
- FR-007 → §7 `CapabilityError` (Construct + Loc).
- FR-008 → §6 + §7 (distinct concrete types; syntax errors come from
  `parser.Parse` and remain a third category).
- FR-009, SC-006 → §5 `ParseReport`.
- FR-010, SC-004 → §1, §3, §4 zero-value semantics.
- FR-011, FR-012 → all checks performed inside the parse-time visitor;
  no field on these types references render-time data.
- FR-013 → §8 no-panic invariant.
