# Remove Panics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace deliberate `panic(...)` calls in the raymond library and its tests with idiomatic Go error handling, keeping behavior and error-message text byte-identical.

**Architecture:** Three independent workstreams executed in order: **A** (test panics → `t.Fatalf`), **D** (registration API returns `error` instead of panicking), **C** (parser/lexer drop panic/recover in favor of a sticky `p.err` field). The existing mustache-spec + parity + parser/lexer suites are the regression oracle — they assert exact error strings, so every change is validated by keeping `go test ./...` green.

**Tech Stack:** Go 1.26, standard library only (`testing`, `reflect`, `errors`, `fmt`). No new dependencies.

**What stays panicking (deliberate, out of scope):**
- `MustParse` / `MustExec` (template.go) — idiomatic `Must*` contract.
- Render-time `errRecover` (template.go ~446/450) — converts *user helper* panics into returned errors; its purpose is panic→error, and user code can panic for reasons we can't prevent.
- `builtins`/init registration via a private `mustRegisterHelper` — failure there is a bug in our own constant helper set (like `regexp.MustCompile` of a literal).
- `handlebars/basic_test.go:437` `panic("fail")` — intentional fixture proving `{{.}}` never calls a helper.
- `parser/parser_test.go:190` `panic(err)` — inside `Example()`, which has no `*testing.T`.
- `escape.go:43` unreachable `default`; `string.go:33` `Str` on unknown type (no error channel).

**Verification baseline (run before starting):**
Run: `go test -count=1 ./... && go vet ./...`
Expected: all packages `ok`, no vet output.

---

## Workstream A — Test panics → `t.Fatalf`

### Task A1: `mustache_test.go` fixture-loader helpers take `*testing.T`

**Files:**
- Modify: `mustache_test.go` (functions `testsFromMustacheFile`, `mustacheTestFiles`, and their callers `TestMustache`, `testsFromMustacheFile` users in `parity_test.go`)

- [ ] **Step 1: Thread `t` into the loader helpers**

Change signatures and replace the three `panic` sites (lines ~66-68, ~72-73, ~124-126):

```go
func testsFromMustacheFile(t *testing.T, fileName string) []Test {
	t.Helper()
	result := []Test{}

	fileData, err := os.ReadFile(path.Join("mustache", "specs", fileName))
	if err != nil {
		t.Fatalf("read spec %s: %v", fileName, err)
	}

	var testFile mustacheTestFile
	if err := yaml.Unmarshal(fileData, &testFile); err != nil {
		t.Fatalf("unmarshal spec %s: %v", fileName, err)
	}
	// ... rest unchanged ...
}

func mustacheTestFiles(t *testing.T) []string {
	t.Helper()
	var result []string

	files, err := os.ReadDir(path.Join("mustache", "specs"))
	if err != nil {
		t.Fatalf("read specs dir: %v", err)
	}
	// ... rest unchanged ...
}
```

- [ ] **Step 2: Update the two call sites**

In `mustache_test.go` `TestMustache`:
```go
	for _, fileName := range mustacheTestFiles(t) {
		if skipFiles[fileName] {
			continue
		}
		launchTests(t, testsFromMustacheFile(t, fileName))
	}
```
In `parity_test.go` `TestParity_Mustache`:
```go
	for _, fileName := range mustacheTestFiles(t) {
		if skipFiles[fileName] {
			continue
		}
		tests := testsFromMustacheFile(t, fileName)
		total += len(tests)
		runParity(t, tests)
	}
```

- [ ] **Step 3: Run the mustache + parity suites**

Run: `go test -count=1 -run 'TestMustache|TestParity_Mustache' .`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add mustache_test.go parity_test.go
git commit -m "test: replace panic with t.Fatalf in mustache spec loaders"
```

### Task A2: Remaining test `panic` → `t.Fatalf`

**Files:**
- Modify: `base_test.go:69,128,142,148`, `parity_test.go:71,99,111,116`, `template_test.go:126,169`, `raymond_test.go:95`, `parser/parser_test.go:173`, `handlebars/base_test.go:39,85`, `handlebars/basic_test.go:642`

All these sites are inside functions that already have a `t *testing.T` in scope (helpers like `launchTests(t, ...)`, `runParity(t, ...)`, `launchErrorTests(t, ...)`, and `Test*` bodies).

- [ ] **Step 1: Convert each site**

Pattern for the "erroneous test output" panics (`base_test.go:69,142`, `parity_test.go:71,111`, `handlebars/base_test.go:85`):
```go
		t.Fatalf("Erroneous test output description: %q", test.output)
```
Pattern for the "Failed to match regexp" panics (`base_test.go:128,148`, `parity_test.go:99,116`, `parser/parser_test.go:173`, `handlebars/basic_test.go:642`):
```go
			t.Fatalf("Failed to match regexp: %v", errMatch)
```
Pattern for the `panic(err)` fixture/setup panics (`template_test.go:126,169`, `raymond_test.go:95`, `handlebars/base_test.go:39`):
```go
		t.Fatalf("%v", err)
```
Where any of these helpers lack a `t` (verify each: `base_test.go` `launchErrorTests` has `t`; `handlebars/base_test.go:39` is inside `dumpTplFile`-style helper — if it has no `t`, add a `t *testing.T` parameter and pass it from the caller, mirroring Task A1).

**Do NOT touch** `handlebars/basic_test.go:437` (intentional helper panic) or `parser/parser_test.go:190` (`Example()`, no `t`).

- [ ] **Step 2: Run full suite**

Run: `go test -count=1 ./...`
Expected: all `ok`.

- [ ] **Step 3: Commit**

```bash
git add base_test.go parity_test.go template_test.go raymond_test.go parser/parser_test.go handlebars/base_test.go handlebars/basic_test.go
git commit -m "test: replace remaining test panics with t.Fatalf"
```

---

## Workstream D — Registration API returns `error`

Note: Go allows ignoring a function's return values, so existing call sites like `tpl.RegisterHelper("x", f)` keep compiling after the signature gains an `error` return. Only sites that *assert a panic* or need to *propagate* the error are touched.

### Task D1: Global `RegisterHelper`/`RegisterHelpers` + `ensureValidHelper` → `error`; builtins via `mustRegisterHelper`

**Files:**
- Modify: `helper.go` (`init` ~40-47, `RegisterHelper` ~55, `RegisterHelpers` ~76, `ensureValidHelper` ~99)
- Test: `helper_test.go`

- [ ] **Step 1: Write failing tests for the new error behavior**

Add to `helper_test.go`:
```go
func TestRegisterHelper_DuplicateReturnsError(t *testing.T) {
	RegisterHelper("dup_d1", func() string { return "" })
	defer RemoveHelper("dup_d1")
	if err := RegisterHelper("dup_d1", func() string { return "" }); err == nil {
		t.Fatal("re-registering a helper must return an error, got nil")
	}
}

func TestRegisterHelper_InvalidReturnsError(t *testing.T) {
	if err := RegisterHelper("notafunc_d1", 42); err == nil {
		t.Fatal("registering a non-function helper must return an error, got nil")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 -run 'TestRegisterHelper_' .`
Expected: FAIL to compile (`RegisterHelper(...) used as value`) — the signature change in Step 3 fixes it.

- [ ] **Step 3: Convert the functions**

```go
// ensureValidHelper returns an error if given helper is not valid.
func ensureValidHelper(name string, funcValue reflect.Value) error {
	if funcValue.Kind() != reflect.Func {
		return fmt.Errorf("Helper must be a function: %s", name)
	}
	if funcValue.Type().NumOut() != 1 {
		return fmt.Errorf("Helper function must return a string or a SafeString: %s", name)
	}
	return nil
}

// RegisterHelper registers a global helper. Returns an error if a helper
// with the same name is already registered or the helper is invalid.
func RegisterHelper(name string, helper any) error {
	helpersMutex.Lock()
	defer helpersMutex.Unlock()

	if helpers[name].valid() {
		return fmt.Errorf("Helper already registered: %s", name)
	}

	switch h := helper.(type) {
	case Helper:
		helpers[name] = helperEntry{streaming: h}
	case func(*HelperCall) error:
		helpers[name] = helperEntry{streaming: HelperFunc(h)}
	default:
		val := reflect.ValueOf(helper)
		if err := ensureValidHelper(name, val); err != nil {
			return err
		}
		helpers[name] = helperEntry{legacy: val}
	}
	return nil
}

// RegisterHelpers registers several global helpers. Returns the first error.
func RegisterHelpers(helpers map[string]any) error {
	for name, helper := range helpers {
		if err := RegisterHelper(name, helper); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Add `mustRegisterHelper` and use it in `init`**

Replace the `init` block (helper.go ~40-47):
```go
// mustRegisterHelper registers a builtin helper, panicking on error. Used
// only for our own constant builtins, where a failure is a programming bug
// (analogous to regexp.MustCompile of a literal).
func mustRegisterHelper(name string, helper any) {
	if err := RegisterHelper(name, helper); err != nil {
		panic(err)
	}
}

func init() {
	mustRegisterHelper("if", HelperFunc(builtinIf))
	mustRegisterHelper("unless", HelperFunc(builtinUnless))
	mustRegisterHelper("with", HelperFunc(builtinWith))
	mustRegisterHelper("each", HelperFunc(builtinEach))
	mustRegisterHelper("log", HelperFunc(builtinLog))
	mustRegisterHelper("lookup", HelperFunc(builtinLookup))
	mustRegisterHelper("equal", HelperFunc(builtinEqual))
}
```
(Keep whatever the exact current `init` function wrapper is; only the body lines change to `mustRegisterHelper`.)

- [ ] **Step 5: Run tests**

Run: `go test -count=1 -run 'TestRegisterHelper_|TestHelper' .`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add helper.go helper_test.go
git commit -m "feat: global RegisterHelper(s) return error instead of panicking"
```

### Task D2: Global partials → `error`

**Files:**
- Modify: `partial.go` (`RegisterPartial` ~35, `RegisterPartials` ~47, `RegisterPartialTemplate` ~54)
- Test: `raymond_test.go` or `partial`-adjacent test file

- [ ] **Step 1: Add a failing duplicate test**

Add to `raymond_test.go`:
```go
func TestRegisterPartial_DuplicateReturnsError(t *testing.T) {
	RegisterPartial("dup_d2", "x")
	defer RemovePartial("dup_d2")
	if err := RegisterPartial("dup_d2", "y"); err == nil {
		t.Fatal("re-registering a partial must return an error, got nil")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 -run TestRegisterPartial_Duplicate .`
Expected: FAIL (`used as value`) until Step 3.

- [ ] **Step 3: Convert the functions**

```go
func RegisterPartial(name string, source string) error {
	partialsMutex.Lock()
	defer partialsMutex.Unlock()

	if partials[name] != nil {
		return fmt.Errorf("Partial already registered: %s", name)
	}
	partials[name] = newPartial(name, source, nil)
	return nil
}

func RegisterPartials(partials map[string]string) error {
	for name, p := range partials {
		if err := RegisterPartial(name, p); err != nil {
			return err
		}
	}
	return nil
}

func RegisterPartialTemplate(name string, tpl *Template) error {
	partialsMutex.Lock()
	defer partialsMutex.Unlock()

	if partials[name] != nil {
		return fmt.Errorf("Partial already registered: %s", name)
	}
	partials[name] = newPartial(name, "", tpl)
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -count=1 -run TestRegisterPartial .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add partial.go raymond_test.go
git commit -m "feat: global RegisterPartial(s)/Template return error instead of panicking"
```

### Task D3: `(*Template)` registration → `error`

**Files:**
- Modify: `template.go` (`RegisterHelper` ~169, `RegisterHelpers` ~189, `addPartial` ~195, `RegisterPartial` ~214, `RegisterPartials` ~219, `RegisterPartialTemplate` ~255, `Clone` ~149-166)

- [ ] **Step 1: Convert `RegisterHelper` and `RegisterHelpers`**

```go
// RegisterHelper registers a helper for that template. Returns an error if a
// streaming helper is supplied (unsupported on Template) or the name is taken.
func (tpl *Template) RegisterHelper(name string, helper any) error {
	switch helper.(type) {
	case Helper, func(*HelperCall) error:
		return fmt.Errorf("Streaming helpers are not supported on Template; register %s globally with RegisterHelper or on a Compiled template", name)
	}

	tpl.mutex.Lock()
	defer tpl.mutex.Unlock()

	if tpl.helpers[name] != zero {
		return fmt.Errorf("Helper %s already registered", name)
	}

	val := reflect.ValueOf(helper)
	if err := ensureValidHelper(name, val); err != nil {
		return err
	}
	tpl.helpers[name] = val
	return nil
}

func (tpl *Template) RegisterHelpers(helpers map[string]any) error {
	for name, helper := range helpers {
		if err := tpl.RegisterHelper(name, helper); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 2: Convert `addPartial`, `RegisterPartial(s)`, `RegisterPartialTemplate`**

```go
func (tpl *Template) addPartial(name string, source string, template *Template) error {
	tpl.mutex.Lock()
	defer tpl.mutex.Unlock()

	if tpl.partials[name] != nil {
		return fmt.Errorf("Partial %s already registered", name)
	}
	tpl.partials[name] = newPartial(name, source, template)
	return nil
}

func (tpl *Template) RegisterPartial(name string, source string) error {
	return tpl.addPartial(name, source, nil)
}

func (tpl *Template) RegisterPartials(partials map[string]string) error {
	for name, partial := range partials {
		if err := tpl.RegisterPartial(name, partial); err != nil {
			return err
		}
	}
	return nil
}

func (tpl *Template) RegisterPartialTemplate(name string, template *Template) error {
	return tpl.addPartial(name, "", template)
}
```

- [ ] **Step 3: Update internal callers of `addPartial` / `RegisterHelper` inside `template.go`**

`Clone` (~149-166) calls `result.RegisterHelper(...)` and `result.addPartial(...)` in loops. These re-register from an already-valid template and cannot realistically fail; ignore the returned error explicitly to keep `Clone`'s signature:
```go
	for name, helper := range tpl.helpers {
		_ = result.RegisterHelper(name, helper.Interface())
	}
	for name, partial := range tpl.partials {
		_ = result.addPartial(name, partial.source, partial.tpl)
	}
```
`RegisterPartialFile` (~232) calls `tpl.RegisterPartial(...)`; propagate:
```go
	return tpl.RegisterPartial(name, string(b))
```
(`RegisterPartialFile` already returns `error`.)

- [ ] **Step 4: Run package tests**

Run: `go test -count=1 .`
Expected: PASS (existing call sites that ignore the new return still compile).

- [ ] **Step 5: Commit**

```bash
git add template.go
git commit -m "feat: (*Template) registration returns error instead of panicking"
```

### Task D4: `(*Compiled)` registration → `error`

**Files:**
- Modify: `compile.go` (`RegisterHelper` ~72, `RegisterPartial` ~84)
- Test: `compile_test.go`

- [ ] **Step 1: Add failing duplicate test**

Add to `compile_test.go`:
```go
func TestCompiledRegisterHelper_DuplicateReturnsError(t *testing.T) {
	c, err := Compile("{{x}}")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	h := HelperFunc(func(hc *HelperCall) error { return nil })
	if err := c.RegisterHelper("dup_d4", h); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := c.RegisterHelper("dup_d4", h); err == nil {
		t.Fatal("re-registering must return an error, got nil")
	}
}
```
(Use the exact `Compile` entry point that `compile_test.go` already uses; adjust if it is `CompileWithOptions` or similar.)

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 -run TestCompiledRegisterHelper_Duplicate .`
Expected: FAIL (`used as value`) until Step 3.

- [ ] **Step 3: Convert the functions**

```go
func (c *Compiled) RegisterHelper(name string, h Helper) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.helpers[name]; ok {
		return fmt.Errorf("Helper already registered: %s", name)
	}
	c.helpers[name] = h
	return nil
}

func (c *Compiled) RegisterPartial(name string, p *Compiled) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.partials[name]; ok {
		return fmt.Errorf("Partial already registered: %s", name)
	}
	c.partials[name] = p
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -count=1 -run TestCompiled .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add compile.go compile_test.go
git commit -m "feat: (*Compiled) registration returns error instead of panicking"
```

### Task D5: Rewrite the panic-asserting test + README

**Files:**
- Modify: `adapt_parity_test.go` (`TestTemplateRegisterHelper_RejectsStreaming` ~139-146)
- Modify: `README.md` (registration examples, if any show `RegisterHelper(...)` without error handling and you want them idiomatic — optional, keep diffs minimal)

- [ ] **Step 1: Convert the streaming-rejection test from panic to error**

```go
func TestTemplateRegisterHelper_RejectsStreaming(t *testing.T) {
	tpl := MustParse("{{x}}")
	err := tpl.RegisterHelper("s", HelperFunc(func(hc *HelperCall) error { return nil }))
	if err == nil {
		t.Error("registering a streaming helper on a Template must return an error")
	}
}
```

- [ ] **Step 2: Run the full suite + vet**

Run: `go test -count=1 ./... && go vet ./...`
Expected: all `ok`, no vet output.

- [ ] **Step 3: Commit**

```bash
git add adapt_parity_test.go README.md
git commit -m "test: assert error return (not panic) for streaming helper on Template"
```

---

## Workstream C — Parser/lexer: sticky `p.err`, no panic/recover

The exported entry points `parser.Parse` and `parser.ParseWithLimits` keep their signatures `(*ast.Program, error)`. Internally, the parser accumulates the first error in a new `p.err` field instead of panicking; the recursive-descent methods keep their existing return types, gaining only guard checks. Error strings are preserved verbatim.

### Task C1: Lexer invariants → `errorf` (TokenError)

**Files:**
- Modify: `lexer/lexer.go:363, 388, 602`

- [ ] **Step 1: Replace the three invariant panics**

`lexOpenMustache` (~363):
```go
	} else {
		return l.errorf("Current pos MUST be an opening mustache")
	}
```
`lexCloseMustache` (~388):
```go
	} else {
		return l.errorf("Current pos MUST be a closing mustache")
	}
```
`lexIdentifier` (~602):
```go
	if len(str) == 0 {
		return l.errorf("Identifier expected")
	}
```
Each of these functions returns `lexFunc`, and `errorf` returns `lexFunc` (emitting a `TokenError`), so `return l.errorf(...)` type-checks and stops the state machine — the parser surfaces the `TokenError` through its normal `shift` path.

- [ ] **Step 2: Run lexer + parser tests**

Run: `go test -count=1 ./lexer/... ./parser/...`
Expected: PASS (these invariants are unreachable on valid lexer input; no test asserts them).

- [ ] **Step 3: Commit**

```bash
git add lexer/lexer.go
git commit -m "refactor(lexer): emit error token instead of panicking on invariants"
```

### Task C2: Parser sticky-error infrastructure

**Files:**
- Modify: `parser/parser.go` (`parser` struct ~19, helpers ~80-112, `shift` ~834, `parse` ~58-76)
- Modify: `parser/limits.go` (`countNode`, `enterNesting`)

- [ ] **Step 1: Add `err` field to the `parser` struct**

In the `type parser struct { ... }` block (~19), add:
```go
	err error // first error encountered; sticky, set via the err* helpers
```

- [ ] **Step 2: Convert the error helpers to sticky methods, delete `errRecover`**

Replace `errRecover`, `errPanic`, `errNode`, `errToken`, `errExpected` (parser.go ~80-112) with:
```go
// setErr records the first parse error (sticky); later calls are ignored.
func (p *parser) setErr(err error, line int) {
	if p.err == nil {
		p.err = fmt.Errorf("Parse error on line %d:\n%s", line, err)
	}
}

func (p *parser) errNode(node ast.Node, msg string) {
	p.setErr(fmt.Errorf("%s\nNode: %s", msg, node), node.Location().Line)
}

func (p *parser) errToken(tok *lexer.Token, msg string) {
	p.setErr(fmt.Errorf("%s\nToken: %s", msg, tok), tok.Line)
}

func (p *parser) errExpected(expect lexer.TokenKind, tok *lexer.Token) {
	p.setErr(fmt.Errorf("Expecting %s, got: '%s'", expect, tok), tok.Line)
}
```
Remove the `runtime` import from `parser.go` if it is now unused (it was only used by `errRecover`).

- [ ] **Step 3: Update every error-helper call site to the method form**

These call sites become `p.err*` (lines per current file): `errToken(token, "Syntax error")` (~69) → `p.errToken(...)`; `errExpected(...)` at ~184, 256, 273, 291, 447, 461, 480, 512, 537, 594, 654, 660, 758, 773 → `p.errExpected(...)`; `errNode(...)` at ~281, 285, 469, 474 → `p.errNode(...)`; `errToken(...)` at ~781, 843 → `p.errToken(...)`. (The `errToken` at ~716 lives in `parseNumber`, handled in Task C3.)

- [ ] **Step 4: Convert `shift` to set the sticky error**

```go
func (p *parser) shift() *lexer.Token {
	var result *lexer.Token
	p.ensure(0)
	result, p.tokens = p.tokens[0], p.tokens[1:]
	if result.Kind == lexer.TokenError {
		p.errToken(result, "Lexer error")
	}
	return result
}
```
(`shift` still always returns a non-nil token, so existing `tok.Kind` / `tok.Pos` derefs at call sites remain safe even after an error is recorded.)

- [ ] **Step 5: Convert limit checks to sticky errors**

In `parser/limits.go`, `countNode` and `enterNesting` record the bare `*LimitError` directly in `p.err` (first-wins) — NOT via `setErr`, which would wrap it in a "Parse error on line %d" string. This matches the old behavior exactly: the previous `panic(&LimitError{...})` was caught by `errRecover`'s `case error: *errp = err`, so the `*LimitError` was returned unwrapped.
```go
func (p *parser) countNode() {
	p.nodeCount++
	if p.err == nil && p.limits.MaxNodes > 0 && p.nodeCount > p.limits.MaxNodes {
		p.err = &LimitError{Kind: "nodes", Limit: p.limits.MaxNodes}
	}
}

func (p *parser) enterNesting() {
	p.depth++
	if p.err == nil && p.limits.MaxDepth > 0 && p.depth > p.limits.MaxDepth {
		p.err = &LimitError{Kind: "depth", Limit: p.limits.MaxDepth}
	}
}
```
Rationale: `parser/limits_test.go` asserts the breach is a `*LimitError` (via `errors.AsType[*LimitError]`) and wraps `ErrTooComplex`; recording the bare `*LimitError` preserves both, exactly as the old `panic(&LimitError{...})` did (it was never wrapped in the "Parse error on line" string).

- [ ] **Step 6: Convert top-level `parse()` to check `p.err`, remove `defer errRecover`**

```go
func (p *parser) parse() (*ast.Program, error) {
	result := p.parseProgram()
	if p.err != nil {
		return nil, p.err
	}

	token := p.shift()
	if token.Kind != lexer.TokenEOF {
		p.errToken(token, "Syntax error")
	}
	if p.err != nil {
		return nil, p.err
	}

	processWhitespaces(result)
	return result, nil
}
```

- [ ] **Step 7: Build (call sites in Task C3 still pending — expect loop/parseNumber errors only)**

Run: `go build ./parser/...`
Expected: may report unresolved `parseNumber`/loop issues addressed in Task C3; if it builds, even better. Do not commit yet.

### Task C3: Parser loop guards, `parseNumber` method, verify green

**Files:**
- Modify: `parser/parser.go` (loops ~122, 570, 604, 649, 765; `parseNumber` ~702 and its caller)

- [ ] **Step 1: Add `p.err == nil` guards to the five driver loops**

So parsing stops at the first error and no `nil` sub-node is appended:
```go
	for p.err == nil && p.isStatement() {        // ~122 parseProgram
	for p.err == nil && p.isParam() {            // ~570 parseParams
	for p.err == nil && p.isHashSegment() {      // ~604 parseHash
	for p.err == nil && p.isID() {               // ~649 parseBlockParams
	for p.err == nil && p.isPathSep() {          // ~765 parsePath
```

- [ ] **Step 2: Make `parseNumber` a parser method so it can record errors**

```go
func (p *parser) parseNumber(tok *lexer.Token) (result float64, isInt bool) {
	var valInt int
	var err error

	valInt, err = strconv.Atoi(tok.Val)
	if err == nil {
		isInt = true
		result = float64(valInt)
	} else {
		isInt = false
		result, err = strconv.ParseFloat(tok.Val, 64)
		if err != nil {
			p.errToken(tok, fmt.Sprintf("Failed to parse number: %s", tok.Val))
		}
	}
	return
}
```
Update its single caller (in `parseHelperName`/number-parsing path) from `parseNumber(tok)` to `p.parseNumber(tok)`.

- [ ] **Step 3: Build and run the parser + full suites**

Run: `go build ./... && go test -count=1 ./...`
Expected: all `ok`. The parser/lexer unit tests and the mustache+parity suites assert exact error strings — green here means the refactor preserved every message.

- [ ] **Step 4: Run vet + modernize**

Run: `go vet ./... && GOFLAGS=-mod=mod go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest ./...`
Expected: no vet output; modernize prints nothing.

- [ ] **Step 5: Commit**

```bash
git add parser/parser.go parser/limits.go
git commit -m "refactor(parser): sticky p.err instead of panic/recover for parse errors"
```

---

## Final verification

- [ ] **Confirm only the deliberate panics remain**

Run: `grep -rn "panic(" --include="*.go" . | grep -v /mustache/specs`
Expected — only these:
- `template.go` `MustParse`, `MustExec`
- `template.go` render `errRecover` re-panics (runtime errors / non-handlebars)
- `helper.go` `mustRegisterHelper`
- `escape.go:43`, `string.go:33` (category-E guards)
- `handlebars/basic_test.go:437` (intentional fixture)
- `parser/parser_test.go:190` (`Example()`)

- [ ] **Full green**

Run: `go test -count=1 ./... && go vet ./...`
Expected: all `ok`, no vet output.
