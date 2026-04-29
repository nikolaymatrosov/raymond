# Feature Specification: Render Output Budget via Capped Writer

**Feature Branch**: `002-capped-writer-budget`
**Created**: 2026-04-29
**Status**: Draft
**Input**: User description: "I want to control output of the template as early as possible. I want to be able to to provide capped writer to the template and be able to return error as soon as rendering result overflows given budget. For example I want to be able to limit rendering result 1mb top."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Cap rendered output size and fail fast on overflow (Priority: P1)

An operator embedding the templating library in a service that renders
caller-supplied data into a template needs to guarantee that no single
render produces more than a configured number of bytes (for example,
1 MB). The check happens *during* rendering, not after, so that a
template which would emit 100 MB of output stops as soon as the
configured ceiling is crossed — without ever holding more than the
budget's worth of bytes in memory or writing them downstream.

**Why this priority**: Output size is the most direct user-controlled
amplification vector at render time. Even a template that passed every
parse-time capability check (small placeholder count, no control flow)
can still emit unbounded output when fed a large data context (for
example, a single `{{long_string}}` against a 500 MB value, or
`{{#each}}` over a huge collection in a "full" template). Without an
output-byte budget enforced *during* rendering, the only safe options
are to render into a temporary buffer and discard it, or to mirror
output through a custom byte-counting wrapper — both costly and
error-prone. Capping at the source is the foundational render-time
safety control and is independently valuable on its own.

**Independent Test**: Configure a render output budget of N bytes,
render a template+context that produces exactly N bytes (succeeds and
all bytes reach the caller's destination), then render a template+
context that would produce N+1 bytes (fails with a typed
budget-exceeded error that names the budget kind and the configured
limit; no bytes beyond the limit are written to the caller's
destination).

**Acceptance Scenarios**:

1. **Given** a render output budget of 1,048,576 bytes (1 MiB),
   **When** the operator renders a template+context whose total output
   is 1,048,576 bytes, **Then** rendering succeeds, all 1,048,576
   bytes are delivered to the caller's destination, and no
   budget-exceeded error is reported.
2. **Given** a render output budget of 1,048,576 bytes, **When** the
   operator renders a template+context whose total output would be
   1,048,577 bytes, **Then** rendering fails with a typed
   budget-exceeded error that identifies the budget kind ("output
   bytes"), the configured limit (1,048,576), and confirms the limit
   was crossed; the caller's destination receives at most the budget's
   worth of bytes.
3. **Given** a render output budget of 1,048,576 bytes and a template
   that, before reaching its first placeholder, would emit a 10 MB
   literal text section, **When** the operator triggers a render,
   **Then** rendering aborts before the 10 MB literal section is fully
   emitted; total work done is bounded by the budget plus a small
   constant, not by the would-be output size.
4. **Given** no render output budget configured, **When** any
   compatible template is rendered, **Then** behaviour matches the
   library's pre-feature behaviour: no budget tracking is performed
   and no budget errors are produced.

---

### User Story 2 - Stream rendered output to a destination of the operator's choice (Priority: P1)

An operator wants to render directly into a destination they already
have (an HTTP response, a file, a network connection, an in-memory
buffer) while still benefiting from the output-byte budget. The
library must accept the operator's destination and enforce the budget
on the bytes flowing into it, without forcing the operator to first
materialise the full result as a string.

**Why this priority**: Today, callers receive the rendered result as
a complete in-memory string, which means the full output is always
materialised before the caller can decide what to do with it. That
defeats the goal of "control output as early as possible": even with
a budget, materialising-then-checking still pays the memory cost.
Letting the operator pass their own destination, with the budget
enforced on the way through, is what makes early-abort actually early.
This story is what turns the budget from "a nicer error message" into
"a memory and bandwidth safeguard".

**Independent Test**: Provide a destination of the operator's choice
(for example, a buffer or a file), render a template into it under a
configured budget, and confirm two things: (a) on success, the
destination contains exactly the rendered bytes and the in-process
peak memory used by the library for output buffering does not grow
proportionally to the output size; (b) on overflow, the destination
contains at most the budget's worth of bytes and the operator
receives a typed budget-exceeded error.

**Acceptance Scenarios**:

1. **Given** an operator-supplied destination and a render output
   budget of 1 MiB, **When** the operator renders a template whose
   output is 500 KiB, **Then** the destination receives exactly the
   500 KiB of rendered bytes and rendering reports success.
2. **Given** an operator-supplied destination and a render output
   budget of 1 MiB, **When** the operator renders a template whose
   output would be 5 MiB, **Then** the destination receives at most
   1 MiB of bytes (the bytes produced up to the moment the budget was
   crossed) and the operator receives a typed budget-exceeded error.
3. **Given** an operator-supplied destination that itself reports a
   write failure (for example, a closed connection), **When** the
   operator renders into it, **Then** rendering aborts at the point
   of the destination's failure and the operator receives an error
   that distinguishes "destination write failure" from "render
   output budget exceeded".

---

### User Story 3 - Distinguish budget-exceeded errors from other render errors (Priority: P2)

An operator that runs many renders in production needs to tell apart
"this render was stopped because it would have produced too much
output" from other render-time failures (missing data, helper errors,
destination I/O failures). They want to react differently: count
budget overflows as a quota signal, surface helper errors to
developers, retry transient I/O failures.

**Why this priority**: A single opaque "render failed" error forces
operators to grep error message strings to triage, which is brittle
and silently breaks across library versions. A typed,
programmatically-distinguishable budget error is what makes the
budget feature actually usable in production observability and
control-flow code. It is lower priority than P1 because P1 already
delivers safety; this story makes the safety *operable*.

**Independent Test**: Trigger each of (a) a budget overflow, (b) a
non-budget render error such as an undefined helper or destination
write failure, and (c) a successful render. Confirm that the budget
overflow is identifiable through a typed error check that does not
rely on parsing human-readable error text, and that the non-budget
errors do not match that check.

**Acceptance Scenarios**:

1. **Given** any render that exceeds the configured output budget,
   **When** the operator inspects the returned error, **Then** the
   error can be identified as a budget-exceeded error through a
   stable, typed check, and exposes the budget kind ("output bytes")
   and the configured limit.
2. **Given** a render that fails for a non-budget reason (for
   example, the destination returns a write failure), **When** the
   operator inspects the returned error, **Then** the typed
   budget-exceeded check returns false and the underlying cause is
   surfaced.

---

### Edge Cases

- **Output budget of zero**: A budget of 0 bytes rejects any render
  that would produce any output. A template that produces no output
  (for example, an empty template or one whose contents are entirely
  inside a falsy `{{#if}}`) succeeds under a 0-byte budget.
- **Exact-fit boundary**: A render whose output is exactly the budget
  must succeed. Only crossing the budget — strictly greater than the
  configured limit — must fail. The boundary is checked as
  "bytes-written > limit", not "bytes-written ≥ limit".
- **Multi-byte UTF-8 sequences at the boundary**: The budget is
  measured in bytes, not characters. If the budget cuts mid-codepoint,
  rendering still aborts at the byte boundary; the partial content
  delivered to the destination may end mid-codepoint. The error and
  the byte count remain authoritative.
- **Helpers that emit output**: When a helper writes to the render
  output, those bytes count against the budget. Crossing the budget
  inside a helper aborts the helper and propagates a budget-exceeded
  error; the helper's own returned error (if any) is not used to mask
  the budget error.
- **Partials and nested templates**: Bytes emitted by partials,
  inline partials, and nested template invocations all count against
  the same single budget for the top-level render. There is no
  per-partial sub-budget.
- **Destination that silently accepts fewer bytes**: If the
  operator's destination accepts the write but reports fewer bytes
  written than requested, that is a destination failure (not a
  budget overflow) and is surfaced as such.
- **Concurrent renders**: Each render call carries its own budget
  state; budgets do not leak across concurrent renders of the same
  template.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The library MUST allow operators to configure, per
  render call, a maximum number of output bytes the render is
  permitted to produce.
- **FR-002**: The library MUST allow operators to supply their own
  destination for rendered output, so that rendered bytes are
  delivered to that destination as they are produced rather than
  only after the full result is assembled in memory.
- **FR-003**: The library MUST enforce the output-byte budget
  *during* rendering. Rendering MUST abort as soon as the cumulative
  bytes produced by the render would exceed the configured limit,
  without first producing or buffering the entire output.
- **FR-004**: When the output-byte budget is exceeded, the library
  MUST return a typed budget-exceeded error that identifies (a) the
  budget kind ("output bytes") and (b) the configured limit. The
  error MUST be programmatically distinguishable from other render-
  time failures without inspecting human-readable text.
- **FR-005**: When the output-byte budget is exceeded, the library
  MUST NOT deliver more than the configured limit's worth of bytes
  to the operator's destination.
- **FR-006**: The exact-fit case MUST succeed: a render whose total
  output equals the configured limit MUST complete normally with no
  budget error and MUST deliver all bytes to the destination.
- **FR-007**: When no output-byte budget is configured for a render
  call, the library MUST behave exactly as it did before this
  feature: no budget tracking, no budget errors, and no change to
  how the rendered result is delivered.
- **FR-008**: Bytes emitted by helpers, partials, inline partials,
  and nested template invocations MUST count against the same single
  budget for the enclosing top-level render. There MUST NOT be
  separate sub-budgets for these constructs.
- **FR-009**: A failure of the operator's destination (for example,
  a write returning an I/O failure or short write) MUST be
  surfaced as a destination failure and MUST be distinguishable from
  a budget-exceeded error.
- **FR-010**: Each render call MUST track its budget independently
  of any other concurrent or subsequent render call, including
  renders of the same template.
- **FR-011**: A budget of zero bytes MUST be a valid configuration:
  any render that would produce one or more bytes MUST fail with a
  budget-exceeded error; a render that produces zero bytes MUST
  succeed.

### Key Entities *(include if feature involves data)*

- **Render output budget**: A per-render configuration value
  expressing the maximum number of output bytes the render is
  permitted to produce. Optional; absence means unbounded (legacy
  behaviour).
- **Render destination**: The operator-supplied target that receives
  rendered bytes as they are produced. Together with the budget, it
  determines how much of the output reaches the operator on overflow.
- **Budget-exceeded error**: A typed error reported when a render
  crosses its configured output-byte budget. Carries the budget kind
  and the configured limit.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can configure a 1 MiB output cap and a
  render that would otherwise produce 100 MiB stops with a budget
  error after delivering at most 1 MiB to the destination, with peak
  in-process memory used by the library for output buffering bounded
  by a small constant (independent of the 100 MiB would-be size).
- **SC-002**: A render whose output is exactly equal to the
  configured budget succeeds in 100% of cases; a render whose output
  is one byte larger fails with a budget-exceeded error in 100% of
  cases.
- **SC-003**: Operators can programmatically distinguish budget-
  exceeded errors from other render-time errors (helper errors,
  destination I/O failures) in 100% of cases without parsing human-
  readable error text.
- **SC-004**: For any render that does not exceed its budget, the
  per-render time and memory overhead introduced by budget tracking
  is small enough that operators can leave the budget enabled in
  production by default — measured as no more than a 10% increase in
  render wall-clock time on a representative mix of templates,
  compared to the same render with no budget configured.
- **SC-005**: Renders performed without a configured budget produce
  byte-for-byte identical output to the pre-feature library and show
  no measurable performance regression on a representative mix of
  templates.

## Assumptions

- The library currently exposes rendering primarily as "render to a
  returned string"; this feature adds the ability to render to an
  operator-supplied destination, but does not require removing or
  changing the existing string-returning entry points.
- The output budget is measured in bytes of the rendered output as
  delivered to the destination (post-encoding), not in characters or
  in pre-escape bytes. This matches the unit operators reason about
  ("limit the response to 1 MB").
- Budgets configured for a single render apply to that render only;
  there is no library-wide ambient budget, and budget configuration
  is not persisted across calls.
- This feature focuses on the output-byte budget. Other render-time
  budgets (wall-clock time, helper invocation count, recursion
  depth) are out of scope for this feature and may be addressed
  separately.
- The existing parse-time capability and substitution-count budgets
  introduced in feature 001 remain unchanged; the render output
  budget is complementary and is configured independently.
- "Destination" here is whatever sink the host language conventionally
  uses for streaming bytes (the standard byte-stream abstraction);
  the feature does not require introducing a new destination type
  unique to this library.
