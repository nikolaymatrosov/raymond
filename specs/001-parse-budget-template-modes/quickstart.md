# Quickstart: Parse Budget & Capability Modes

**Feature**: 001-parse-budget-template-modes
**Audience**: Operators embedding Raymond who want to bound or
restrict caller-supplied templates at parse time.

This document is what a downstream consumer sees once the feature
ships. It also serves as the manual-validation script for the feature.

---

## Install / upgrade

No new module to install — the feature is additive in the existing
`github.com/aymerick/raymond` package.

```bash
go get github.com/aymerick/raymond@latest
```

## 1. Existing code keeps working

```go
import "github.com/aymerick/raymond"

tpl, err := raymond.Parse("hello {{name}}")
out, err := tpl.Exec(map[string]any{"name": "Ada"})
// out == "hello Ada" — unchanged from prior versions.
```

If you do nothing, nothing changes (FR-010 / SC-004).

## 2. Cap how many substitutions a template may contain

```go
opts := raymond.ParseOptions{
    Budget: raymond.Budget{MaxSubstitutions: 100, Enforced: true},
}
tpl, err := raymond.ParseWithOptions(userSuppliedSource, opts)
if err != nil {
    var be *raymond.BudgetExceededError
    if errors.As(err, &be) {
        log.Printf("template too large: kind=%s limit=%d observed=%d",
            be.Kind, be.Limit, be.Observed)
    }
    return err
}
```

## 3. Restrict to "simple" templates (no control flow, no partials, no helpers)

```go
opts := raymond.ParseOptions{Mode: raymond.ModeSimple}
tpl, err := raymond.ParseWithOptions(userSource, opts)
// Rejects {{#if}}, {{#each}}, {{#with}}, {{>partial}}, {{helper x}},
// {{../x}}, {{@root}}. Permits {{name}}, {{user.email}},
// {{items.[0]}}, {{this}}, comments, and literal text.
```

## 4. Mix and match individual capabilities

```go
opts := raymond.ParseOptions{
    Capabilities: raymond.Capabilities{
        If:        true,  // permit {{#if}}/{{#unless}}
        Iteration: false, // forbid {{#each}}
        Partials:  true,  // permit all partial forms
    },
    Budget: raymond.Budget{MaxSubstitutions: 1000, Enforced: true},
}
tpl, err := raymond.ParseWithOptions(userSource, opts)
```

`{{#with}}` and helper-style mustaches are full-mode-only; granular
mode rejects them regardless of toggle state.

## 5. Inspect what was actually used

```go
tpl, err := raymond.ParseWithOptions(src, opts)
if err == nil {
    r := tpl.Report()
    log.Printf("substitutions=%d constructs=%v", r.Substitutions, r.Constructs)
}
```

## 6. Tell the three error categories apart

```go
_, err := raymond.ParseWithOptions(src, opts)
switch {
case err == nil:
    // success
case errors.As(err, new(*raymond.BudgetExceededError)):
    // user template too large
case errors.As(err, new(*raymond.CapabilityError)):
    // user template used a forbidden construct
default:
    // syntax error (same as legacy Parse)
}
```

---

## Manual validation script (`/speckit.implement` smoke test)

Run after implementation, before opening the PR. Each numbered step
maps to one acceptance scenario in the spec.

1. **US1 #1** — Budget=100 + 100 substitutions → success, report says 100.
2. **US1 #2** — Budget=100 + 101 substitutions → `BudgetExceededError{"substitutions", 100, 101}`.
3. **US1 #3** — No opts + any template → unchanged behaviour.
4. **US2 #1** — Simple mode + plain text + `{{name}}` → success.
5. **US2 #2** — Simple mode + `{{#if x}}…{{/if}}` → `CapabilityError{"if", loc}`.
6. **US2 #3** — Simple mode + `{{#each xs}}…{{/each}}` → `CapabilityError{"each", loc}`.
7. **US2 #4** — Simple mode + `{{> header}}` → `CapabilityError{"partial", loc}`.
8. **US3 #1–#6** — Iterate the 8 capability toggle combinations (table in `contracts/api.md` C5).
9. **US4 #1** — After successful load, `Report().Substitutions` and `Report().Constructs` reflect the source.
10. **US4 #2** — On budget/capability failure, the error carries the same diagnostic fields.
11. **No-panic** — Run `ParseWithOptions` over `handlebars/` test fixtures with `Mode: ModeSimple`; assert no panic regardless of which fixtures are rejected.
12. **Bench** — `go test -bench=ParseWithOptions -benchmem` shows the visitor-off path within noise of legacy `Parse`.
