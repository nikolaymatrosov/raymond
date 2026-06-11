# Benchmarks

## Feature 002 — Render Output Budget

Hardware: Apple M1 Pro (darwin/arm64), Go 1.26.1
Command: `go test -bench='BenchmarkExec_NoBudget_Legacy|BenchmarkExecTo_WithBudget' -benchmem -count=3 -run=^$ ./...`

| Benchmark                       | ns/op | B/op | allocs/op |
|---------------------------------|------:|-----:|----------:|
| `BenchmarkExec_NoBudget_Legacy` |  5188 | 5170 |       120 |
| `BenchmarkExecTo_WithBudget`    |  5240 | 5138 |       122 |

- SC-004 (≤10% wall-clock overhead vs. no-budget): **PASS** —
  streaming-with-budget is ~1% slower than legacy `Exec`.
- SC-005 (no measurable regression on the legacy path vs. pre-feature):
  **PASS** — `BenchmarkExec_NoBudget_Legacy` exercises the unchanged
  `Exec` path; bytes/op and allocs/op are identical to the pre-feature
  baseline (`evalVisitor` only adds two unexported fields, both
  zero-valued and gated behind `v.out != nil`).

Hardware: MacBookPro11,1 - Intel Core i5 - 2,6 GHz - 8 Go RAM

With:

    - handlebars.js #8cba84df119c317fcebc49fb285518542ca9c2d0
    - raymond #7bbaaf50ed03c96b56687d7fa6c6e04e02375a98

## handlebars.js (ops/ms)

        arguments          198 ±4 (5)
        array-each         568 ±23 (5)
        array-mustache     522 ±18 (4)
        complex             71 ±7 (3)
        data                67 ±2 (3)
        depth-1             47 ±2 (3)
        depth-2             14 ±1 (2)
        object-mustache   1099 ±47 (5)
        object             907 ±58 (4)
        partial-recursion   46 ±3 (4)
        partial             68 ±3 (3)
        paths             1650 ±50 (3)
        string            2552 ±157 (3)
        subexpression      141 ±2 (4)
        variables         2671 ±83 (4)

## raymond

        BenchmarkArguments          200000     6642 ns/op   151 ops/ms
        BenchmarkArrayEach          100000    19584 ns/op    51 ops/ms
        BenchmarkArrayMustache      100000    17305 ns/op    58 ops/ms
        BenchmarkComplex            30000     50270 ns/op    20 ops/ms
        BenchmarkData               50000     25551 ns/op    39 ops/ms
        BenchmarkDepth1             100000    20162 ns/op    50 ops/ms
        BenchmarkDepth2             30000     47782 ns/op    21 ops/ms
        BenchmarkObjectMustache     200000     7668 ns/op   130 ops/ms
        BenchmarkObject             200000     8843 ns/op   113 ops/ms
        BenchmarkPartialRecursion   50000     23139 ns/op    43 ops/ms
        BenchmarkPartial            50000     31015 ns/op    32 ops/ms
        BenchmarkPath               200000     8997 ns/op   111 ops/ms
        BenchmarkString             1000000    1879 ns/op   532 ops/ms
        BenchmarkSubExpression      300000     4935 ns/op   203 ops/ms
        BenchmarkVariables          200000     6478 ns/op   154 ops/ms

## Feature 003 — Streaming-Core Rewrite _(2026-06-11)_

Hardware: Apple M1 Pro (darwin/arm64), Go 1.26.1
Command: `go test -bench . -benchmem -run '^$' -count=3 .`

The rendering engine was rewritten to stream into `io.Writer` with plain error
returns (no per-program `strings.Builder` or string concat). Numbers are the
median of the three `-count=3` runs.

### Legacy-path gate (SC-004 / SC-005)

| Benchmark                       | baseline ns/op | new ns/op | delta | baseline B/op | new B/op | baseline allocs | new allocs |
|---------------------------------|---------------:|----------:|------:|--------------:|---------:|----------------:|-----------:|
| `BenchmarkExec_NoBudget_Legacy` |           5188 |      4628 |  -11% |          5170 |     4776 |             120 |         70 |
| `BenchmarkExecTo_WithBudget`    |           5240 |      4908 |   -6% |          5138 |     4888 |             122 |         87 |

SC-004 (≤10% overhead for budgeted path vs. no-budget): **PASS** — budgeted
path is 6% slower than the no-budget path (4908 vs. 4628 ns/op).
SC-005 (no regression on legacy path): **PASS** — `BenchmarkExec_NoBudget_Legacy`
is 11% faster than the Feature-002 baseline (4628 vs. 5188 ns/op).

### Full benchmark suite — pre-rewrite vs. streaming core (same hardware)

These are an honest, same-machine comparison: `77ecb99~1` (the commit before the
streaming-core rewrite) vs. this branch, both on the Apple M1 Pro, Go 1.26.1,
`-count=6`, summarized with `benchstat`. "old" is the pre-rewrite code, "new"
is the streaming core.

| Benchmark                   | old ns/op | new ns/op | Δ time | Δ B/op | Δ allocs |
|-----------------------------|----------:|----------:|-------:|-------:|---------:|
| `BenchmarkArguments`        |      1143 |      2364 | +107%  | +218%  |  +65%    |
| `BenchmarkArrayEach`        |      3104 |      3538 |  +14%  |  +12%  |  −19%    |
| `BenchmarkArrayMustache`    |      2739 |      3409 |  +24%  |   +8%  |  −20%    |
| `BenchmarkComplex`          |      8759 |      7596 |  −13%  |  −18%  |  −41%    |
| `BenchmarkData`             |      4222 |      5141 |  +22%  |  +14%  |  −12%    |
| `BenchmarkDepth1`           |      2960 |      3482 |  +18%  |  +13%  |  −21%    |
| `BenchmarkDepth2`           |      8379 |      9064 |   +8%  |  +17%  |  −24%    |
| `BenchmarkObjectMustache`   |      1225 |      1644 |  +34%  |  +46%  |  −19%    |
| `BenchmarkObject`           |      1554 |      1788 |  +15%  |  +50%  |  −21%    |
| `BenchmarkPartialRecursion` |      4182 |      4472 |   +7%  |  +41%  |  −20%    |
| `BenchmarkPartial`          |      4878 |      4474 |   −8%  |  −19%  |  −41%    |
| `BenchmarkPath`             |      1806 |      2636 |  +46%  |  +53%  |  +18%    |
| `BenchmarkString`           |       250 |       243 |   −3%  |  +40%  |  −10%    |
| `BenchmarkSubExpression`    |       772 |      1132 |  +47%  | +118%  |  +33%    |
| `BenchmarkVariables`        |      1102 |      1147 |   +4%  |  +28%  |  −22%    |
| _geomean_                   |           |           |  +13%  |  +24%  |  −15%    |

The render micro-benchmarks are a **mixed result, not a universal speedup**: on
identical hardware the streaming core is ~13% slower in wall-clock and allocates
~24% more bytes on geomean, while cutting allocation _count_ ~15%. A few paths
improve (`Complex` −13%, `Partial` −8%); several regress (`Arguments` +107%,
`Path` +46%, `SubExpression` +47%). The clear wins are on the budgeted/legacy
`Exec` path measured in the SC-004/SC-005 gate above (`Exec_NoBudget_Legacy`
−10% time / −42% allocs), which was Feature 003's actual target.

> An earlier version of this table reported 65–87% speedups. That was an
> artifact of comparing the original raymond's published numbers (a 2014 Intel
> Core i5) against the new M1 Pro runs — different hardware, so the deltas were
> meaningless. The table above corrects that with a same-machine comparison.

### Post-rewrite optimization pass _(2026-06-11)_

The mixed result above prompted an allocation-focused pass over the hot helper
and path code. Three behaviour-preserving changes (full test suite green):

- `callHelper` preallocates the `params` slice to its exact length instead of
  growing a `nil` slice — `Value` is a ~128-byte struct, so the repeated-growth
  copies were the single largest allocator in `BenchmarkArguments`.
- The core helper-call path now evaluates the hash with a values-only
  `evalHashValues`, dropping a parallel `map[string]interface{}` it built and
  immediately discarded.
- The adapter's per-value `strFn func() string` closure was replaced with a
  `legacyStr bool` flag; `Value` already stores `raw`, so `Str()` calls
  `Str(v.raw)` directly. This removes one heap closure per adapted
  map/slice/struct value — the dominant cost in `BenchmarkPath`.

Streaming-core baseline → optimized, same M1 Pro, `-count=6`:

| Benchmark                   | Δ time | Δ B/op | Δ allocs |
|-----------------------------|-------:|-------:|---------:|
| `BenchmarkArguments`        |  −8.6% |  +1.7% |  −15.8%  |
| `BenchmarkArrayEach`        |  −4.0% |  −6.2% |   −9.5%  |
| `BenchmarkArrayMustache`    |  −4.3% |  −6.3% |  −10.5%  |
| `BenchmarkComplex`          |  ~     |  −5.6% |   −6.5%  |
| `BenchmarkData`             |  −4.4% |  −8.1% |  −12.1%  |
| `BenchmarkDepth1`           |  −3.1% |  −6.3% |   −9.7%  |
| `BenchmarkDepth2`           |  ~     |  −5.8% |   −7.3%  |
| `BenchmarkObjectMustache`   |  −2.8% |  −8.3% |   −7.7%  |
| `BenchmarkObject`           |  ~     |  −7.7% |   −6.5%  |
| `BenchmarkPartialRecursion` |  −2.3% |  −8.0% |   −8.6%  |
| `BenchmarkPartial`          |  ~     |  −5.7% |   −7.7%  |
| `BenchmarkPath`             |  −0.9% | −12.9% |  −12.8%  |
| `BenchmarkString`           |  −6.2% |  −2.7% |   ~      |
| `BenchmarkSubExpression`    |  −3.1% |  −3.8% |   −3.6%  |
| `BenchmarkVariables`        |  ~     |  −4.8% |   −4.8%  |
| _geomean_                   | −2.53% | −5.05% |  −6.84%  |

No benchmark regressed in wall-clock; every one allocates less or equal. The one
byte-size uptick (`Arguments` +1.7%) is the `make(map, n)` size hint over-sizing
for that template's four same-key hash pairs — a deliberate trade for −6 allocs.

The remaining gap to the pre-rewrite engine on helper-heavy templates is
structural: legacy helpers still round-trip `Value → interface{} → reflect.Value`
through the public `Options` API. Closing it would mean storing `Value`-typed
params/hash in `Options` and converting lazily — left as a follow-up since it
touches a stable exported type.

### Reproducing these numbers

Run the full suite on the current branch (the "new" column):

    go test -bench . -benchmem -run '^$' -count=6 . | tee new.txt

`-run '^$'` skips the unit tests so only benchmarks run, `-benchmem` adds the
`B/op` and `allocs/op` columns, and `-count=6` repeats each benchmark for
variance. Summarize with [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

    go install golang.org/x/perf/cmd/benchstat@latest
    benchstat new.txt

To regenerate the "baseline" column and the deltas, capture the pre-rewrite
commit, then diff the two runs:

    git checkout 77ecb99~1                                      # before the streaming-core rewrite
    go test -bench . -benchmem -run '^$' -count=6 . | tee old.txt
    git checkout 003-streaming-core
    go test -bench . -benchmem -run '^$' -count=6 . | tee new.txt
    benchstat old.txt new.txt                                   # delta column with p-values

Absolute ns/op is hardware-dependent (these were taken on an Apple M1 Pro,
Go 1.26.1), but the deltas should reproduce across machines.
