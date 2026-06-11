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

### Full benchmark suite — original raymond vs. streaming core (same hardware)

An honest, same-machine comparison: the original pre-rewrite raymond (the
`baseline` branch fork point) vs. the current optimized streaming core, both on
the Apple M1 Pro, Go 1.26.1, `-count=6`, summarized with `benchstat`. "old" is
the pre-rewrite code, "new" is the streaming core after the allocation work.

| Benchmark                   | old ns/op | new ns/op | Δ time | Δ B/op | Δ allocs |
|-----------------------------|----------:|----------:|-------:|-------:|---------:|
| `BenchmarkArguments`        |      1117 |      1797 | +61%   | +180%  |   +4%    |
| `BenchmarkArrayEach`        |      3084 |      3032 |  ~     |   +1%  |  −37%    |
| `BenchmarkArrayMustache`    |      2723 |      2967 |  +9%   |   −3%  |  −39%    |
| `BenchmarkComplex`          |      8787 |      6610 | −25%   |  −25%  |  −52%    |
| `BenchmarkData`             |      4207 |      4207 |  ~     |   ~    |  −35%    |
| `BenchmarkDepth1`           |      3086 |      2943 |  ~     |   +2%  |  −38%    |
| `BenchmarkDepth2`           |      8617 |      7936 |  −8%   |   +7%  |  −38%    |
| `BenchmarkObjectMustache`   |      1232 |      1381 | +12%   |  +23%  |  −44%    |
| `BenchmarkObject`           |      1554 |      1520 |  −2%   |  +29%  |  −41%    |
| `BenchmarkPartialRecursion` |      4186 |      3819 |  −9%   |  +26%  |  −37%    |
| `BenchmarkPartial`          |      4910 |      3864 | −21%   |  −28%  |  −55%    |
| `BenchmarkPath`             |      1825 |      2002 | +10%   |  +13%  |  −28%    |
| `BenchmarkString`           |       252 |       174 | −31%   |  +22%  |  −40%    |
| `BenchmarkSubExpression`    |       788 |       942 | +19%   |  +73%  |   −5%    |
| `BenchmarkVariables`        |      1093 |       970 | −11%   |   +9%  |  −44%    |
| _geomean_                   |           |           |  −2%   |  +15%  |  −37%    |

The headline: **allocation count is down ~37%** across the suite, and **wall-clock
is at rough parity** (−2% geomean) — several templates are now meaningfully faster
than the original (`Complex` −25%, `String` −31%, `Partial` −21%, `Variables`
−11%). The remaining regressions are concentrated in **reflected-helper-heavy**
templates: `Arguments` (+61% time, +180% B), `SubExpression` (+19% / +73% B),
`ObjectMustache` (+12%), `Path` (+10%). Those carry the streaming core's extra
`Value`/`Options` round-trip on every reflected helper call; the byte geomean
(+15%) is almost entirely those few benchmarks.

> An even earlier version of this table reported 65–87% speedups. That was an
> artifact of comparing the original raymond's published numbers (a 2014 Intel
> Core i5) against M1 Pro runs — different hardware, so the deltas were
> meaningless. The table above is a same-machine comparison.

### Reproducing these numbers

Run the full suite on the current branch (the "new" column):

    go test -bench . -benchmem -run '^$' -count=6 . | tee new.txt

`-run '^$'` skips the unit tests so only benchmarks run, `-benchmem` adds the
`B/op` and `allocs/op` columns, and `-count=6` repeats each benchmark for
variance. Summarize with [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

    go install golang.org/x/perf/cmd/benchstat@latest
    benchstat new.txt

To regenerate the "baseline" column and the deltas, check out the true
pre-rewrite fork point — the `baseline` branch — then diff the two runs. (The
entire `003-streaming-core` branch is the rewrite; the cutover to the streaming
engine happened at `24a7ec6`, so any commit on the branch is already streaming.
`baseline` marks where the package was forked, before any of that work.)

    git checkout baseline                                       # original raymond, pre-rewrite
    go test -bench . -benchmem -run '^$' -count=6 . | tee old.txt
    git checkout 003-streaming-core
    go test -bench . -benchmem -run '^$' -count=6 . | tee new.txt
    benchstat old.txt new.txt                                   # delta column with p-values

Absolute ns/op is hardware-dependent (these were taken on an Apple M1 Pro,
Go 1.26.1), but the deltas should reproduce across machines.
