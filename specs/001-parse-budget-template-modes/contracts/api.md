# Phase 1 Contract: Public Go API for Parse Budget & Capability Modes

**Feature**: 001-parse-budget-template-modes
**Surface**: package `raymond` (root). All identifiers below are
*additive*; no existing identifier changes signature or behaviour.

This contract is the source of truth for the failing tests written in
Phase 3 / `/speckit.tasks`. Each contract item below maps to one or
more tests.

---

## C1. New exported identifiers (additive)

```go
// Mode selects a capability preset.
type Mode int
const (
    ModeFull   Mode = iota // zero value — legacy behaviour
    ModeSimple
)

// Capabilities are the independently-toggleable construct families.
// Honoured when Mode == ModeFull and at least one toggle is set, OR
// when a Budget is enforced. Ignored when Mode == ModeSimple.
type Capabilities struct {
    If        bool
    Iteration bool
    Partials  bool
}

// Budget caps parse-time resource consumption.
type Budget struct {
    MaxSubstitutions int
    Enforced         bool
}

// ParseOptions bundles capability and budget configuration.
// The zero value is identical to legacy Parse behaviour.
type ParseOptions struct {
    Mode         Mode
    Capabilities Capabilities
    Budget       Budget
}

// ParseReport is the read-only observability snapshot for a successful parse.
type ParseReport struct {
    Substitutions int
    Constructs    []string // sorted, deduplicated
}

// BudgetExceededError is returned when a parse budget is breached.
type BudgetExceededError struct {
    Kind     string // "substitutions" for this feature
    Limit    int
    Observed int
}
func (*BudgetExceededError) Error() string

// CapabilityError is returned when the template uses a construct
// disallowed by the active capability mode.
type CapabilityError struct {
    Construct string
    Loc       ast.Loc
}
func (*CapabilityError) Error() string

// ParseWithOptions parses source under the given options.
// On success returns *Template carrying a ParseReport.
// On budget breach returns *BudgetExceededError.
// On capability violation returns *CapabilityError.
// Otherwise returns whatever parser.Parse returns (syntax error).
func ParseWithOptions(source string, opts ParseOptions) (*Template, error)

// Report returns a copy of the parse report attached to a successfully
// parsed template. For templates parsed via legacy Parse (no opts),
// Substitutions is still populated; Constructs is the empty slice if
// the visitor was not run.
func (*Template) Report() ParseReport
```

## C2. Behavioural contract — backward compatibility

| Scenario                                                         | Expected outcome                                             | Spec ref       |
|---|---|---|
| `Parse(s)` (any pre-existing call site)                           | Identical to pre-feature behaviour; never returns a budget or capability error. | FR-010, SC-004 |
| `ParseWithOptions(s, ParseOptions{})`                             | Identical to `Parse(s)`. No visitor run; `Report()` returns zero-valued report. | FR-010 |
| `MustParse(s)`                                                    | Unchanged. Panics only on syntax errors as before.            | Principle II   |

## C3. Behavioural contract — substitution budget (US1)

Given `opts := ParseOptions{Budget: Budget{MaxSubstitutions: 100, Enforced: true}}`:

| Input                                              | Expected                                                          |
|---|---|
| Template with exactly 100 substitutions             | Success. `Report().Substitutions == 100`.                         |
| Template with 101 substitutions                     | `errors.As(err, **BudgetExceededError) == true` with `Kind == "substitutions"`, `Limit == 100`, `Observed == 101`. Returned `*Template` is nil. |
| Template with 0 substitutions (pure literal)        | Success.                                                          |
| `Budget{MaxSubstitutions: 0, Enforced: true}`, template with any substitution | `BudgetExceededError{Kind:"substitutions", Limit:0, Observed:≥1}`. |
| `Budget{MaxSubstitutions: -1, Enforced: false}`     | Treated as no limit; success regardless of substitution count.    |

## C4. Behavioural contract — simple mode (US2)

Given `opts := ParseOptions{Mode: ModeSimple}`:

| Input                                                            | Expected                                                                            |
|---|---|
| `"hello {{name}} you have {{count}} items"`                      | Success.                                                                            |
| `"{{#if x}}y{{/if}}"`                                            | `*CapabilityError{Construct:"if", Loc: <opener line/col>}`.                         |
| `"{{#each xs}}…{{/each}}"`                                       | `*CapabilityError{Construct:"each", …}`.                                            |
| `"{{#with x}}…{{/with}}"`                                        | `*CapabilityError{Construct:"with", …}`.                                            |
| `"{{> header}}"`, `"{{> (lookup .)}}"`, `"{{#*inline}}…"`, `"{{#> name}}…"` | `*CapabilityError{Construct:"partial", …}` for each.                        |
| `"{{upper name}}"` (helper invocation)                           | `*CapabilityError{Construct:"helper", …}`.                                          |
| `"{{../x}}"`                                                     | `*CapabilityError{Construct:"parent-path", …}`.                                     |
| `"{{@root.x}}"`, `"{{@key}}"`, `"{{@index}}"`                    | `*CapabilityError{Construct:"data-var", …}` for each.                               |
| `"{{this}}"`, `"{{user.email}}"`, `"{{items.[0]}}"`              | Success (current-context paths only).                                               |
| `"{{! comment }}"`, `"{{!-- block --}}"`                         | Success (comments allowed in all modes).                                            |
| `"{{{name}}}"`, `"{{~name~}}"`, `"{{- name -}}"`                  | Success (treated as plain substitution; counted as 1 each).                         |

## C5. Behavioural contract — granular toggles (US3)

For each of the 8 on/off combinations of `Capabilities.{If, Iteration, Partials}`
under `Mode == ModeFull` (the granular path triggers because at least one
toggle is true OR a budget is enforced):

| Capability set      | `if`/`unless` template | `each` template | partial template | `with` template | helper-style mustache |
|---|---|---|---|---|---|
| `{If:true}`         | success | `CapabilityError("each")` | `CapabilityError("partial")` | `CapabilityError("with")` | `CapabilityError("helper")` |
| `{Iteration:true}`  | `CapabilityError("if")` | success | `CapabilityError("partial")` | `CapabilityError("with")` | `CapabilityError("helper")` |
| `{Partials:true}`   | `CapabilityError("if")` | `CapabilityError("each")` | success (all 4 partial forms) | `CapabilityError("with")` | `CapabilityError("helper")` |
| All three true      | success | success | success | `CapabilityError("with")` | `CapabilityError("helper")` |
| All three false (with `Budget.Enforced:true`) | rejects every block/partial/helper | — | — | — | — |
| (zero opts)         | unchecked — legacy behaviour | — | — | — | — |

`{{else}}` branches of an allowed `{{#if}}` / `{{#each}}` are
permitted as part of their parent (no separate toggle, spec edge case).

## C6. Error category distinguishability (FR-008)

```go
_, err := ParseWithOptions(src, opts)
var be *BudgetExceededError
var ce *CapabilityError
errors.As(err, &be) // true iff this is a budget violation
errors.As(err, &ce) // true iff this is a capability violation
// otherwise err is a parser syntax error (unchanged shape).
```

The three categories MUST be mutually exclusive on any given returned
error.

## C7. Observability contract (US4 / FR-009 / SC-006)

After a successful `ParseWithOptions`:

```go
tpl, _ := ParseWithOptions(src, opts)
r := tpl.Report()
// r.Substitutions == count of substitution-producing mustaches
// r.Constructs is sorted, deduplicated, and uses the closed
// vocabulary {"if","unless","each","with","partial","helper"}.
```

For an *unsuccessful* parse on a budget violation, the substitution
count observed up to (and including) the violating mustache MUST be
reported via `BudgetExceededError.Observed` (which is exactly the
count that crossed the threshold; spec edge case "first count that
crosses the threshold").

For a capability violation, `CapabilityError.Loc` MUST identify the
offending node's source location.

## C8. No-panic contract (FR-013 / Principle I)

`ParseWithOptions` MUST NOT panic on:
- arbitrarily nested templates within whatever the parser already accepts;
- templates that combine multiple violations (it returns *one* of them);
- any zero-valued field in `ParseOptions`.

A failing test calls `ParseWithOptions` over a corpus of crafted
templates inside `func() { defer func(){ if r := recover(); r != nil { t.Fatal(r) } }() }`.

## C9. Performance contract (Principle V, SC-001)

A new benchmark `BenchmarkParseWithOptions` in `benchmark_test.go`
parses a fixed representative template under
`ParseOptions{Mode: ModeFull}` (granular path off) and under
`ParseOptions{Capabilities: {If:true, Iteration:true, Partials:true}, Budget: {MaxSubstitutions: 1<<20, Enforced: true}}`
(granular path on). The visitor-on path MUST stay within the same
order of magnitude as the visitor-off path — concretely the PR
description reports both numbers and any regression > 10% on the
visitor-off path is a blocker (Principle V).

---

## Test mapping (for `/speckit.tasks`)

- C2 → `template_test.go` regression tests of legacy callers.
- C3 → `parse_visitor_test.go` ("budget" subtests).
- C4 → `parse_visitor_test.go` ("simple-mode" subtests).
- C5 → `parse_visitor_test.go` ("granular" subtests, table over the 8
  combinations).
- C6 → `parse_errors_test.go`.
- C7 → `parse_report_test.go`.
- C8 → `parse_visitor_test.go` ("nopanic" subtest).
- C9 → `benchmark_test.go` additions.
