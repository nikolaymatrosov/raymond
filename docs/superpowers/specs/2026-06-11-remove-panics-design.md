# Design: Remove panics from raymond (code + tests)

Date: 2026-06-11
Branch: 004-update-mustache

## Goal

Remove deliberate `panic(...)` calls from the library and its tests "as much as
we can", converting them to idiomatic Go error handling. The only `recover()`
that survives is the render-time safety net whose *purpose* is to turn panics
(from arbitrary third-party helper code) into returned errors.

## Hard constraint: behavior and error strings must not change

The mustache spec suite (`TestMustache`), the parity suite (`TestParity_*`), and
the parser/lexer unit tests assert on **exact error message text** and on
program behavior. Every rewrite below must produce byte-identical error strings
and identical control flow. The full test suite (`go test ./...`) is the
regression oracle and must pass unchanged after each workstream.

## Inventory of panic sites (non-submodule)

Production code:

- `compile.go:76,88` — `(*Compiled)` register duplicate helper/partial
- `helper.go:60,101,107` — global `RegisterHelper` duplicate / invalid helper
- `partial.go:40,59` — global partial duplicate
- `template.go:172,179,200` — `(*Template)` streaming-helper unsupported / duplicate
- `template.go:117,268` — `MustParse` / `MustExec` (idiomatic — KEPT)
- `template.go:446,450` — render `errRecover` (KEPT — see Workstream C)
- `parser/parser.go:85,89,96` — `errRecover` re-panic + `errPanic`
- `parser/limits.go:53,61` — `countNode` / `enterNesting` limit panics
- `lexer/lexer.go:363,388,602` — lexer invariant assertions
- `escape.go:43`, `string.go:33` — defensive guards (category E — see below)

Tests: 20 `panic(...)` calls across `base_test.go`, `parity_test.go`,
`mustache_test.go`, `template_test.go`, `raymond_test.go`,
`parser/parser_test.go`, `handlebars/base_test.go`, `handlebars/basic_test.go`.

## Workstream A — Tests → `t.Fatal`

Replace the 20 test `panic(...)` calls with `t.Fatalf(...)`.

- Shared helpers `testsFromMustacheFile` and `mustacheTestFiles`
  (`mustache_test.go`) currently `panic` on fixture-load failure and are called
  from both `TestMustache` and `TestParity_Mustache`. Give them a
  `t *testing.T` parameter, call `t.Helper()`, and use `t.Fatalf`.
- The `panic(fmt.Errorf("Erroneous test output description"))` in
  `base_test.go` / `parity_test.go` becomes `t.Fatalf`.
- No production code touched.

Risk: low. Pure test-only change.

## Workstream C — Parser/lexer panic-recover → explicit errors

The public entry points already return errors and **do not change signature**:

- `parser.Parse(input string) (*ast.Program, error)`
- `parser.ParseWithLimits(input string, limits Limits) (*ast.Program, error)`

Internal changes (contained to `parser/` and `lexer/`):

1. **All 31 private `parse*` methods** change return type from `T` to
   `(T, error)`. Every call site becomes `x, err := p.parseX(); if err != nil {
   return ..., err }`. The recursive-descent structure is preserved; only error
   threading is added.
2. **Error constructors** `errPanic`/`errNode`/`errToken`/`errExpected` stop
   panicking and instead **return** an `error` with the identical message
   (`"Parse error on line %d:\n%s"`, etc.). Call sites return that error.
3. **`errRecover` (parser.go:80-92) is deleted.** With parse errors threaded
   explicitly, there is no deliberate panic to recover. A genuine runtime bug
   (nil deref) now propagates naturally as a Go panic, which is correct — we no
   longer catch-and-rethrow it. This removes `parser.go:85,89,96`.
4. **`parser/limits.go`**: `countNode` and `enterNesting` return `error`
   (the same `*LimitError` value) instead of panicking. Threaded through their
   ~20 call sites inside the parse methods. `Compile` still surfaces
   `*LimitError` as `ErrTemplateTooComplex` exactly as today.
5. **Lexer invariants (lexer.go:363,388,602)**: these "can't happen" guards
   emit a `TokenError` via the existing `errorf` mechanism instead of panicking,
   so they flow through the normal lexer→parser `TokenError` path (which the
   parser already converts to a "Lexer error" parse error in `shift`). Message
   text preserved.

What stays:

- **Render-time `errRecover` (template.go:446,450).** Its job is to catch
  panics thrown by *arbitrary user helper functions* and convert them into the
  error returned by `Exec`/`ExecWith`. Error returns cannot replace it because
  third-party helpers can panic for reasons outside our control, and the public
  contract is that `Exec` returns an error rather than crashing. Removing it
  would break that contract and the parity tests that rely on it. This is a
  panic→error boundary, consistent with the goal.

Risk: high relative to the rest (largest diff), but fully contained to `parser/`
and covered by the parity + spec suites. Error strings preserved verbatim.

## Workstream D — Registration API → `error` (breaking)

Exported registration functions return `error` instead of panicking. `Must*`
variants keep panicking (idiomatic).

Signature changes:

- Global: `RegisterHelper`, `RegisterHelpers`, `RegisterPartial`,
  `RegisterPartials` → return `error`.
- `(*Template)`: `RegisterHelper`, `RegisterHelpers`, `RegisterPartial`,
  `RegisterPartials`, `RegisterPartialTemplate` → return `error`.
  (`RegisterPartialFile`/`RegisterPartialFiles` already return `error`.)
- `(*Compiled)`: `RegisterHelper`, `RegisterPartial` → return `error`.

Internal changes:

- `ensureValidHelper` (helper.go) returns `error` instead of panicking on a
  non-function helper / wrong return type. Same message text.
- `(*Template).addPartial` returns `error` on duplicate.
- Internal callers that previously relied on panic-free registration:
  - `(*Template).Clone` re-registers helpers/partials from an already-valid
    template; it propagates any error (should not occur, but no panic).
  - Builtins registration at init (`builtins.go`) registers known-good helpers;
    wrap in a small internal `mustRegister` that panics only on a programmer
    invariant (our own builtins), OR assert no error. Decision: keep builtins
    using a private `mustRegisterHelper` that panics, since a failure there is a
    compile-time-constant bug in our own code, not user input. (This is the one
    place a panic is acceptable — analogous to `regexp.MustCompile` of a
    constant.)

Ripple: every in-repo caller (`render.go`, `builtins.go`, tests across the
package and `handlebars/`), plus README examples, updated to handle the returned
error (tests use `t.Fatalf` on unexpected registration error).

Risk: mechanical but wide; breaking change to the public API.

## Category E (NOT in scope)

`escape.go:43` (unreachable `default` in a switch over a fixed char set) and
`string.go:33` (`Str` on an unsupported type) are defensive guards. They are not
being converted: `Str` returns a string (no error channel) and is called
pervasively; the escape default is provably unreachable. Left as-is.

## Sequencing

A → D → C, each landed and verified independently:

1. **A** first (test-only, establishes green baseline with clearer failures).
2. **D** next (mechanical, breaks/updates many call sites; do before C so the
   parser rewrite lands on an otherwise-stable tree).
3. **C** last (largest, riskiest; verified by the now-clearer test suite).

After each workstream: `go test ./...`, `go vet ./...`, and `modernize ./...`
must be clean.

## Verification

- Full suite green after each workstream (`go test -count=1 ./...`).
- `go vet ./...` clean.
- `grep -rn "panic(" --include="*.go" . | grep -v /mustache/` shows only:
  `template.go` `MustParse`/`MustExec`, the render `errRecover` re-panics, the
  builtins `mustRegisterHelper`, and category-E guards (`escape.go`,
  `string.go`). Everything else removed.
