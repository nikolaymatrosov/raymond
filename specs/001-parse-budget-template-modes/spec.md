# Feature Specification: Parse Budget & Template Capability Modes

**Feature Branch**: `001-parse-budget-template-modes`
**Created**: 2026-04-29
**Status**: Draft
**Input**: User description: "I need to add parse buget for the library. I want to specify if `for` or `if` constructs are permitted in the template. If I specify that I want `simple` template that means that only simple substituions are permitted. Also I want to count substitution places and return error if it is out of given budget."

## Clarifications

### Session 2026-04-29

- Q: How should `{{#with}}` (context-shift block) be classified for capability modes? → A: Reject `{{#with}}` in simple mode; allowed only in full mode; no separate toggle.
- Q: Which path forms count as "plain value substitution" allowed in simple mode? → A: Current-context paths only — bare names, dotted paths, indexed paths, and `this`. Reject parent-context (`../x`) and `@`-data variables (`@root`, `@key`, `@index`, `@first`, `@last`).
- Q: Should partials be independently toggleable, or only available in "full" mode? → A: Add an independent `partials` toggle alongside `if` and iteration. The toggle covers all partial forms uniformly (static, dynamic, inline, partial-blocks); finer-grained per-form toggles remain out of scope.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reject templates that exceed a substitution-count budget (Priority: P1)

An operator embedding the templating library in a service that accepts
caller-supplied templates needs to refuse any template whose number of
substitution placeholders exceeds a configured ceiling. The check
happens at template-load time so that a costly or abusive template
never reaches rendering.

**Why this priority**: Substitution count is the simplest, most
universal proxy for "how much work might this template do". Without
it, a hostile or buggy template can blow up rendering time and output
size before any other budget kicks in. This is the foundational
compute-safety control and the prerequisite for the other stories.

**Independent Test**: Configure a budget of N substitutions, load a
template with N substitutions (succeeds), then load a template with
N+1 substitutions (fails with a typed budget error that names the
limit and the observed count). Both outcomes are observable purely at
parse time, with no rendering required.

**Acceptance Scenarios**:

1. **Given** a parse budget allowing up to 100 substitutions, **When**
   the operator loads a template containing 100 substitution
   placeholders, **Then** the template loads successfully and the
   reported substitution count is 100.
2. **Given** a parse budget allowing up to 100 substitutions, **When**
   the operator loads a template containing 101 substitution
   placeholders, **Then** template loading fails with a budget-exceeded
   error that identifies the budget kind ("substitutions"), the
   configured limit (100), and the observed count (101).
3. **Given** no parse budget configured (legacy default), **When** any
   template is loaded, **Then** behaviour matches the library's
   pre-feature behaviour and no budget errors are produced.

---

### User Story 2 - Restrict templates to a "simple" capability mode (Priority: P1)

An operator that only needs string interpolation wants to declare the
template "simple" so that the parser rejects any template containing
control-flow constructs (conditionals, iteration, partials, helpers
that introduce control flow). This guarantees, before any rendering,
that the template cannot branch or loop.

**Why this priority**: Disabling control flow eliminates entire classes
of denial-of-service and logic-injection risks at the source. For the
common case of "fill these blanks in this string", this is the
strongest, simplest guarantee operators can ask for, and it is
independently valuable even if the substitution-count budget is not
used.

**Independent Test**: Configure capability mode "simple", load a
template containing only plain text and `{{name}}`-style substitutions
(succeeds), then load a template containing `{{#if x}}…{{/if}}` or
`{{#each xs}}…{{/each}}` (fails with a typed capability error that
identifies the disallowed construct and its source location).

**Acceptance Scenarios**:

1. **Given** capability mode "simple", **When** loading a template
   that contains only literal text and value substitutions, **Then**
   the template loads successfully.
2. **Given** capability mode "simple", **When** loading a template
   containing an `if` block, **Then** loading fails with a capability
   error naming the construct ("if") and its line/column in the source.
3. **Given** capability mode "simple", **When** loading a template
   containing a `for`/`each` block, **Then** loading fails with a
   capability error naming the construct ("each") and its source
   location.
4. **Given** capability mode "simple", **When** loading a template
   containing a partial reference or a non-built-in helper invocation,
   **Then** loading fails with a capability error naming the construct.

---

### User Story 3 - Selectively permit `if`, iteration, and/or partials (Priority: P2)

An operator with a slightly richer use case wants to allow conditional
rendering but not iteration, or iteration but not conditionals, or to
enable partials over a curated partial library while keeping
control-flow disabled — by flipping individual capability switches
independently of the "simple" preset.

**Why this priority**: Once "simple" exists, granular toggles are an
incremental refinement. They unlock real-world cases (e.g. "show this
block only if the user is verified", or "compose from a vetted
partial library") without re-enabling the full language. They are P2
because the simple/full presets cover the extremes and most users
will pick one of them.

**Independent Test**: For each of the three toggles (`if`, iteration,
partials), configure a mode that permits exactly that toggle and
forbids the others. Load a template using only the permitted
construct (succeeds); load a template using one of the forbidden
constructs (fails with that construct identified).

**Acceptance Scenarios**:

1. **Given** capability mode permits `if` but forbids iteration and
   partials, **When** loading a template using only `if`/`unless`,
   **Then** loading succeeds.
2. **Given** capability mode permits `if` but forbids iteration,
   **When** loading a template using `each`, **Then** loading fails
   with a capability error naming "each".
3. **Given** capability mode permits iteration but forbids `if`,
   **When** loading a template using `if`, **Then** loading fails
   with a capability error naming "if".
4. **Given** capability mode permits partials but forbids `if` and
   iteration, **When** loading a template that uses `{{> header}}`
   (or any other partial form), **Then** loading succeeds.
5. **Given** capability mode forbids partials, **When** loading a
   template that uses any partial form (static, dynamic, inline, or
   partial block), **Then** loading fails with a capability error
   naming "partial".
6. **Given** capability mode permits `if`, iteration, and partials,
   **When** loading a template that uses all three, **Then** loading
   succeeds.

---

### User Story 4 - Inspect observed budget consumption after a successful load (Priority: P3)

When a template loads successfully under a budget, the operator wants
to read back the observed counts (e.g. number of substitutions, set of
constructs used) so they can tune budgets and detect drift over time.

**Why this priority**: Useful for operations and tuning but not
required for the safety guarantees themselves. Without it, operators
can still enforce budgets — they just can't easily right-size them.

**Independent Test**: Load a known template under a generous budget
and capability mode and assert that the reported substitution count,
nesting depth, and used-construct set match the template's contents.

**Acceptance Scenarios**:

1. **Given** a template that loads successfully, **When** the operator
   reads the parse report, **Then** the report contains at least the
   observed substitution count and the set of control-flow constructs
   used.
2. **Given** a template that fails to load due to a budget or
   capability violation, **When** the operator inspects the error,
   **Then** the error carries the same diagnostic fields (offending
   construct or count, configured limit, source location where
   applicable).

---

### Edge Cases

- **Empty template**: Substitution count is 0; loads under any budget
  ≥ 0 and any capability mode.
- **Substitutions inside disallowed blocks**: A template with an `if`
  block that wraps 50 substitutions is rejected on the capability
  violation alone in "simple" mode; the substitution count is still
  reported in the diagnostic for tuning purposes.
- **Whitespace-control variants** (`{{- name -}}`, `{{~name~}}`):
  Counted as one substitution each, identical to plain `{{name}}`.
- **Triple-stash / unescaped substitutions** (`{{{name}}}`): Counted
  as substitutions for budget purposes regardless of escaping mode.
- **Comments** (`{{! ... }}`, `{{!-- ... --}}`): Not counted as
  substitutions; allowed in all capability modes.
- **Built-in `else` branch of an allowed `if`/`each`**: Permitted
  whenever its parent block is permitted; not a separate toggle.
- **Helper invocations in expression position** (e.g. `{{upper name}}`):
  Treated as substitutions for counting purposes; in "simple" mode,
  only current-context path lookups are permitted (bare, dotted,
  indexed, and `this`) — any helper invocation, parent-context path
  (`../x`), or `@`-data variable is rejected.
- **Partials and dynamic partials**: Rejected in "simple" mode.
  Independently toggleable in granular modes via the partials switch
  (FR-005); the toggle covers static, dynamic, inline, and
  partial-block forms uniformly.
- **Budget set to zero**: A budget of 0 substitutions causes any
  template containing at least one substitution to be rejected; a
  template of pure literal text still loads.
- **Negative or absent budget**: Treated as "no limit" so behaviour is
  backward-compatible with existing callers.
- **Multiple violations in one template**: The parser reports the
  first offending construct or the first count that crosses the
  threshold; it is not required to enumerate every violation in a
  single error.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The library MUST allow callers to attach a parse budget
  and a capability mode to a template before parsing.
- **FR-002**: The parse budget MUST include, at minimum, a maximum
  substitution count.
- **FR-003**: When the substitution count of the parsed template
  exceeds the configured maximum, parsing MUST fail with a typed
  budget-exceeded error that names the budget kind, the configured
  limit, and the observed count.
- **FR-004**: The library MUST expose a "simple" capability mode that
  permits only literal text and plain value substitutions, and rejects
  every other construct (conditionals, iteration, context-shift blocks
  such as `with`, partials, helper invocations beyond plain path
  lookup, raw blocks). For the purposes of this mode, a "plain value
  substitution" is a mustache expression whose path resolves against
  the current context only: bare identifiers (`name`), dotted paths
  (`user.email`), indexed paths (`items.[0]`), and the explicit
  current-context reference `this`. Parent-context paths (`../x`) and
  `@`-data variables (`@root`, `@key`, `@index`, `@first`, `@last`,
  etc.) MUST be rejected in simple mode.
- **FR-005**: The library MUST expose independent capability switches
  for at least conditional constructs (`if`/`unless`), iteration
  constructs (`each` / equivalent), and partials, so that each can be
  permitted or forbidden without affecting the others. The partials
  toggle MUST cover all partial forms uniformly: static partials
  (`{{> name}}`), dynamic partials (`{{> (expr)}}`), inline partials
  (`{{#*inline}}`), and partial blocks (`{{#> name}}`). Context-shift
  blocks such as `{{#with}}` are NOT independently toggleable in this
  feature: they are permitted only in "full" mode and rejected in
  "simple" mode and in any granular mode that is not "full".
- **FR-006**: The library MUST also expose a "full" / unrestricted
  capability mode whose behaviour is identical to the library's
  pre-feature default, for backward compatibility.
- **FR-007**: When a template uses a construct that the configured
  capability mode forbids, parsing MUST fail with a typed capability
  error that names the offending construct and reports its source
  location (line and column where the parser already tracks them).
- **FR-008**: Capability and budget errors MUST be distinguishable from
  ordinary syntax errors and from each other (separate error
  categories).
- **FR-009**: When parsing succeeds, the library MUST make the
  observed substitution count and the set of control-flow constructs
  used readable by the caller.
- **FR-010**: Default behaviour, when neither a budget nor a
  capability mode is supplied by the caller, MUST be identical to the
  library's pre-feature behaviour: no parse-time rejection on count or
  capability grounds.
- **FR-011**: Capability and budget enforcement MUST happen at parse
  time (template load), not at render time.
- **FR-012**: Capability-mode rejection MUST NOT depend on render-time
  context data; the same template with the same mode either always
  loads or always fails.
- **FR-013**: The library MUST NOT panic on capability or budget
  violations; all such conditions MUST be reported as returned errors.

### Key Entities *(include if feature involves data)*

- **Parse Budget**: A caller-supplied set of numeric ceilings applied
  during template parsing. For this feature it carries at minimum a
  maximum substitution count; it is the carrier for additional
  parse-time ceilings the project may add later (e.g. node count,
  nesting depth) without API churn.
- **Capability Mode**: A caller-supplied declaration of which language
  constructs the parser will accept. Comprises at least the presets
  "simple" and "full", plus independent toggles for conditional and
  iteration constructs.
- **Parse Report**: A read-only summary attached to a successfully
  parsed template, containing the observed substitution count and the
  set of control-flow constructs encountered. Returned alongside the
  parsed template; mirrored as fields on parse-failure errors where
  meaningful.
- **Budget-Exceeded Error**: A typed error category produced when a
  parse budget is breached. Carries the budget kind, configured limit,
  and observed value.
- **Capability Error**: A typed error category produced when the
  parsed template uses a construct disallowed by the active capability
  mode. Carries the construct name and its source location.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can refuse a template whose substitution
  count exceeds a configured limit before any rendering work is
  performed; rejection occurs in the same order of magnitude of time
  as parsing the template itself, with no extra rendering performed.
- **SC-002**: With "simple" capability mode active, 100% of templates
  that contain any conditional, iteration, partial, or helper-call
  construct are rejected at load time, and 100% of templates composed
  only of literal text and value substitutions load successfully.
- **SC-003**: Granular toggles for conditionals, iteration, and
  partials are independently effective: in every one of the eight
  on/off combinations of the three switches, exactly the disabled
  families of constructs are rejected and exactly the enabled
  families are accepted.
- **SC-004**: Existing callers of the library that do not opt into a
  budget or capability mode observe no change in parse acceptance,
  parse error messages they previously relied on, or render output,
  for any template that worked before this feature shipped.
- **SC-005**: Every budget-exceeded and capability error returned by
  the library identifies the violation precisely enough that an
  operator can locate it in the source template (construct name and
  line/column for capability errors; budget kind, limit, and observed
  value for budget errors) without re-running the parser in a debug
  mode.
- **SC-006**: After a successful load, an operator can read the
  observed substitution count and used-construct set without re-parsing
  the template.

## Assumptions

- The default behaviour when no budget or capability mode is supplied
  is "no enforcement", to preserve backward compatibility with
  existing library callers. Hardening the defaults is a follow-up
  decision, not part of this feature.
- "Simple" mode forbids partials, context-shift blocks (`{{#with}}`),
  and any helper invocation beyond plain path lookup. Granular
  per-helper allowlists, per-partial allowlists, and a separate
  toggle for context-shift blocks are out of scope for this feature
  and may be added later; for now, anything that is not `if`/`unless`,
  iteration, or partials toggles only via the full/simple binary.
- Substitution counting treats every distinct mustache expression that
  produces output as one substitution, regardless of escaping mode
  (`{{x}}`, `{{{x}}}`) and whitespace-control variants (`{{~x~}}`,
  `{{- x -}}`).
- Comments and pure literal text do not count toward the substitution
  budget.
- Block helpers (`#if`, `#each`, etc.) themselves are accounted for
  via the capability mode, not the substitution count; substitutions
  inside permitted blocks count normally.
- Source locations (line/column) reported in capability errors come
  from the data the lexer/parser already tracks; this feature does not
  require adding new positional tracking.
- Evaluation-time budgets (render step counts, output-size caps,
  wall-clock deadlines) are governed by a separate feature per the
  project constitution and are not in scope here.
