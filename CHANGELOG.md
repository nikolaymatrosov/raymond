# Raymond Changelog

### Unreleased

- [IMPROVEMENT] Streaming-core rewrite: new `Compile` / `Execute` API with
  unified `Limits` struct (`MaxTemplateSize`, `MaxNodes`, `MaxDepth`,
  `MaxOutputBytes`, `MaxSubstitutions`, `MaxSteps`). New sentinel errors
  `ErrOutputLimit`, `ErrSubstitutionLimit`, `ErrStepLimit`,
  `ErrTemplateTooComplex`, `ErrTemplateTooLarge`; all wrapped by `LimitError`
  (which exposes `Kind` and `Limit` and unwraps to the sentinel for
  `errors.Is`). New streaming helper API: `Helper` interface,
  `HelperFunc` adapter, and `HelperCall` invocation context. New parser
  entry point `parser.ParseWithLimits`. The rendering engine was rewritten
  to stream into `io.Writer` with plain error returns; no behavior change
  for existing entry points — the full compatibility suite passes unchanged.

- [IMPROVEMENT] Feature 002 — Render Output Budget: opt-in render output
  budget via `(*Template).ExecTo`, `(*Template).ExecToWith`, and
  `(*Template).ExecToWithOptions`, plus a `RenderOptions` struct
  (`MaxOutputBytes`, `Enforced`). New typed errors
  `RenderBudgetExceededError`, `RenderDestinationError`; both are
  distinguishable via `errors.As`, and `RenderDestinationError`
  unwraps to the underlying writer cause (e.g. `io.ErrShortWrite`).
  Peak in-process buffer memory on overflow is bounded by
  `MaxOutputBytes + O(1)`. Existing `Exec` / `MustExec` / `ExecWith`
  / `Render` / `MustRender` are byte-for-byte unchanged.

### HEAD

- [IMPROVEMENT] Add opt-in `ParseWithOptions(source, ParseOptions)` API for
  parse-time enforcement of a substitution-count budget and a capability
  mode. New exported identifiers: `Mode`, `ModeFull`, `ModeSimple`,
  `Capabilities`, `Budget`, `ParseOptions`, `ParseReport`,
  `BudgetExceededError`, `CapabilityError`, `ParseWithOptions`,
  `(*Template).Report`. Existing `Parse`, `MustParse`, `ParseFile`, and
  `Render` are unchanged.
- [IMPROVEMENT] Add `RemoveHelper` and `RemoveAllHelpers` functions

### Raymond 2.0.2 _(March 22, 2018)_

- [IMPROVEMENT] Add the #equal helper (#7)
- [IMPROVEMENT] Add struct tag template variable support (#8)

### Raymond 2.0.1 _(June 01, 2016)_

- [BUGFIX] Removes data races [#3](https://github.com/aymerick/raymond/issues/3) - Thanks [@markbates](https://github.com/markbates)

### Raymond 2.0.0 _(May 01, 2016)_

- [BUGFIX] Fixes passing of context in helper options [#2](https://github.com/aymerick/raymond/issues/2) - Thanks [@GhostRussia](https://github.com/GhostRussia)
- [BREAKING] Renames and unexports constants:

  - `handlebars.DUMP_TPL`
  - `lexer.ESCAPED_ESCAPED_OPEN_MUSTACHE`
  - `lexer.ESCAPED_OPEN_MUSTACHE`
  - `lexer.OPEN_MUSTACHE`
  - `lexer.CLOSE_MUSTACHE`
  - `lexer.CLOSE_STRIP_MUSTACHE`
  - `lexer.CLOSE_UNESCAPED_STRIP_MUSTACHE`
  - `lexer.DUMP_TOKEN_POS`
  - `lexer.DUMP_ALL_TOKENS_VAL`

### Raymond 1.1.0 _(June 15, 2015)_

- Permits templates references with lowercase versions of struct fields.
- Adds `ParseFile()` function.
- Adds `RegisterPartialFile()`, `RegisterPartialFiles()` and `Clone()` methods on `Template`.
- Helpers can now be struct methods.
- Ensures safe concurrent access to helpers and partials.

### Raymond 1.0.0 _(June 09, 2015)_

- This is the first release. Raymond supports almost all handlebars features. See <https://github.com/aymerick/raymond#limitations> for a list of differences with the javascript implementation.
