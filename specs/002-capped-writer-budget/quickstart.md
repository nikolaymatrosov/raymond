# Quickstart: Render Output Budget via Capped Writer

A 60-second tour of feature 002 from an embedding operator's
perspective.

---

## Cap a render at 1 MiB

```go
package main

import (
    "errors"
    "io"
    "log"
    "net/http"

    "github.com/aymerick/raymond"
)

const oneMiB = 1 << 20

func render(w http.ResponseWriter, r *http.Request, src string, ctx interface{}) {
    tpl, err := raymond.Parse(src)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    err = tpl.ExecToWithOptions(w, ctx, nil, raymond.RenderOptions{
        MaxOutputBytes: oneMiB,
        Enforced:       true,
    })

    var bex *raymond.RenderBudgetExceededError
    var dex *raymond.RenderDestinationError
    switch {
    case err == nil:
        // Success: response body is exactly the rendered bytes.
        return
    case errors.As(err, &bex):
        log.Printf("render exceeded %d bytes (kind=%s)", bex.Limit, bex.Kind)
        // The first oneMiB bytes have already been written to w; the
        // client received a truncated response.
    case errors.As(err, &dex):
        log.Printf("destination write failed: %v", dex.Unwrap())
    default:
        log.Printf("render failed: %v", err)
    }
}
```

Key properties:

- The destination writer (`w`) receives at most 1 MiB even if the
  template + context would have produced 100 MiB.
- The library's peak in-process buffer for output is bounded by
  ~1 MiB + a small constant.
- `errors.As` distinguishes budget overflow from destination I/O
  failure with no string parsing.

---

## Stream to a writer without any cap

If you just want streaming and don't yet care about a budget:

```go
err := tpl.ExecTo(w, ctx)
```

This is equivalent to `ExecToWithOptions(w, ctx, nil,
RenderOptions{})`. Output is byte-for-byte identical to the legacy
`Exec` (which returns a string), but it streams as it renders rather
than materialising the full result first.

---

## Keep using the legacy `Exec` — nothing changes

```go
str, err := tpl.Exec(ctx) // unchanged
```

No budget tracking, no streaming, no behaviour change versus the
pre-feature library. This is the FR-007 / SC-005 guarantee: existing
callers see no difference.

---

## A budget of zero

```go
err := tpl.ExecToWithOptions(w, ctx, nil, raymond.RenderOptions{
    MaxOutputBytes: 0,
    Enforced:       true,
})
```

- A template that emits any output → `*RenderBudgetExceededError`
  with `Limit == 0`.
- A template that emits no output (empty template, or all content
  inside a falsy `{{#if}}`) → `nil`. The writer is never written to.

---

## Concurrent renders

Each `ExecTo*` call carries its own budget state. Two goroutines
rendering the same `*Template` under different budgets are
independent — there is no shared mutable budget state on
`*Template`.

```go
go tpl.ExecToWithOptions(w1, ctx1, nil, raymond.RenderOptions{MaxOutputBytes: 1 << 20, Enforced: true})
go tpl.ExecToWithOptions(w2, ctx2, nil, raymond.RenderOptions{MaxOutputBytes: 1 << 16, Enforced: true})
```

---

## Inspecting errors

```go
var bex *raymond.RenderBudgetExceededError
if errors.As(err, &bex) {
    metrics.Inc("render.budget.exceeded", "kind", bex.Kind)
    return
}

var dex *raymond.RenderDestinationError
if errors.As(err, &dex) {
    if errors.Is(dex.Unwrap(), io.ErrShortWrite) {
        // destination accepted fewer bytes than asked
    }
}
```

`*RenderBudgetExceededError` is the only typed error this feature
introduces for budget overflow. `*RenderDestinationError` is the
only typed error it introduces for destination I/O failures. They
are mutually exclusive (FR-009, SC-003).
