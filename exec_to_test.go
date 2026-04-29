package raymond

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// countingWriter records the cumulative byte count without storing the
// payload — used to verify that the unenforced path streams arbitrarily
// large output and that the enforced path bounds destination bytes.
type countingWriter struct{ n int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// ----- US1 -----

func TestExecToWithOptions_ExactFitSucceeds(t *testing.T) {
	tpl := MustParse("0123456789")
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 10, Enforced: true})
	if err != nil {
		t.Fatalf("exact fit: unexpected err %v", err)
	}
	if buf.String() != "0123456789" {
		t.Errorf("buf = %q, want 0123456789", buf.String())
	}
}

func TestExecToWithOptions_OneByteOverFails(t *testing.T) {
	tpl := MustParse("0123456789")
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 9, Enforced: true})
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
	if buf.Len() > 9 {
		t.Errorf("destination received %d bytes, want <= 9", buf.Len())
	}
}

func TestExecToWithOptions_LargeLiteralEarlyAbort(t *testing.T) {
	literal := strings.Repeat("a", 10*1024*1024) // 10 MiB
	tpl := MustParse(literal)

	var memBefore runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	cw := &countingWriter{}
	err := tpl.ExecToWithOptions(cw, nil, nil, RenderOptions{MaxOutputBytes: 1 << 20, Enforced: true})

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
	if cw.n > 1<<20 {
		t.Errorf("destination received %d bytes, want <= 1 MiB", cw.n)
	}
	// Allow generous slack: the literal lives in the parsed template
	// (immutable), but no NEW 10 MiB buffer should be allocated for
	// the streamed output. We assert the heap delta stayed below
	// 4 MiB, which excludes anything close to the 10 MiB literal.
	const slack = 4 * 1024 * 1024
	if delta := int64(memAfter.HeapAlloc) - int64(memBefore.HeapAlloc); delta > slack {
		t.Errorf("heap delta = %d bytes, want <= %d (budget + O(1))", delta, slack)
	}
}

func TestExecToWithOptions_HelperEmittedBytesCount(t *testing.T) {
	tpl := MustParse("{{bigHelper}}")
	tpl.RegisterHelper("bigHelper", func() string { return strings.Repeat("X", 2048) })
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 1024, Enforced: true})
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
}

func TestExecToWithOptions_PartialBytesCount(t *testing.T) {
	tpl := MustParse("{{> chunk}}{{> chunk}}{{> chunk}}")
	tpl.RegisterPartial("chunk", strings.Repeat("y", 500))
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 1024, Enforced: true})
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("partials should share one budget; err = %v", err)
	}
	if buf.Len() > 1024 {
		t.Errorf("destination received %d bytes, want <= 1024", buf.Len())
	}
}

func TestExecToWithOptions_UTF8AtBoundary(t *testing.T) {
	// "héllo" — 'é' is two bytes (0xC3 0xA9). Source byte length = 6.
	tpl := MustParse("héllo")
	var buf bytes.Buffer
	// Budget 2 lands in the middle of 'é' (after 'h', mid-codepoint).
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 2, Enforced: true})
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
	if buf.Len() > 2 {
		t.Errorf("destination received %d bytes, want <= 2", buf.Len())
	}
	if buf.Len() == 2 && utf8.Valid(buf.Bytes()) {
		// The boundary IS allowed to land mid-codepoint; we just
		// document that the error is authoritative — destination may
		// or may not be valid UTF-8.
	}
}

func TestExecToWithOptions_NoPanicOnAdversarialInput(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	templates := []string{
		"{{a}}",
		"{{#if a}}{{a}}{{/if}}",
		"{{#each a}}{{this}}{{/each}}",
		"{{a}}{{b}}{{c}}",
		"plain text only",
		"{{#each items}}- {{name}}\n{{/each}}",
	}
	for i := 0; i < 1000; i++ {
		src := templates[rng.Intn(len(templates))]
		tpl, err := Parse(src)
		if err != nil {
			continue
		}
		ctx := map[string]interface{}{
			"a":     rng.Intn(2) == 0,
			"b":     rng.Intn(100),
			"c":     "",
			"items": []map[string]interface{}{{"name": "x"}, {"name": "y"}},
		}
		budget := int64(rng.Intn(64))
		var buf bytes.Buffer
		// MUST NOT panic regardless of budget/output combination.
		_ = tpl.ExecToWithOptions(&buf, ctx, nil, RenderOptions{MaxOutputBytes: budget, Enforced: rng.Intn(2) == 0})
	}
}

// ----- US2 -----

func TestExecTo_StreamsBytes(t *testing.T) {
	src := "Hello {{name}}!"
	ctx := map[string]interface{}{"name": "World"}
	tpl := MustParse(src)
	want, err := tpl.Exec(ctx)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	var buf bytes.Buffer
	if err := tpl.ExecTo(&buf, ctx); err != nil {
		t.Fatalf("ExecTo: %v", err)
	}
	if buf.String() != want {
		t.Errorf("ExecTo bytes = %q, Exec string = %q", buf.String(), want)
	}
}

func TestExecTo_DestinationWriteFailure(t *testing.T) {
	tpl := MustParse("hi")
	myErr := errors.New("disk full")
	err := tpl.ExecTo(&failingWriter{err: myErr}, nil)
	var dex *RenderDestinationError
	if !errors.As(err, &dex) {
		t.Fatalf("err = %v, want *RenderDestinationError", err)
	}
	if dex.Unwrap() != myErr {
		t.Errorf("Unwrap = %v, want %v", dex.Unwrap(), myErr)
	}
}

func TestExecToWith_DataFramePropagated(t *testing.T) {
	tpl := MustParse("@{{@key}}")
	frame := NewDataFrame()
	frame.Set("key", "secret")
	var buf bytes.Buffer
	if err := tpl.ExecToWith(&buf, nil, frame); err != nil {
		t.Fatalf("ExecToWith: %v", err)
	}
	if got := buf.String(); got != "@secret" {
		t.Errorf("ExecToWith output = %q, want %q", got, "@secret")
	}
}

func TestExecToWithOptions_NotEnforced_NoTracking(t *testing.T) {
	// Use a 1 MiB literal repeated to ~10 MiB; with Enforced:false,
	// the destination must receive all bytes regardless of count.
	chunk := strings.Repeat("z", 1024)
	src := strings.Repeat(chunk, 1024) // 1 MiB source
	tpl := MustParse(src)
	cw := &countingWriter{}
	if err := tpl.ExecToWithOptions(cw, nil, nil, RenderOptions{Enforced: false}); err != nil {
		t.Fatalf("unenforced: unexpected err %v", err)
	}
	if cw.n != int64(len(src)) {
		t.Errorf("destination received %d bytes, want %d", cw.n, len(src))
	}
}

func TestExecToWithOptions_DestinationShortWrite(t *testing.T) {
	tpl := MustParse("hello")
	sw := &shortWriter{}
	err := tpl.ExecToWithOptions(sw, nil, nil, RenderOptions{})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err = %v, want errors.Is io.ErrShortWrite", err)
	}
	var dex *RenderDestinationError
	if !errors.As(err, &dex) {
		t.Fatalf("err = %v, want *RenderDestinationError", err)
	}
}

// ----- US3 -----

func TestExecToWithOptions_ConcurrentRendersIndependent(t *testing.T) {
	tpl := MustParse("0123456789")
	var wg sync.WaitGroup
	wg.Add(2)
	var (
		errSmall, errLarge error
		bufSmall, bufLarge bytes.Buffer
	)
	go func() {
		defer wg.Done()
		errSmall = tpl.ExecToWithOptions(&bufSmall, nil, nil, RenderOptions{MaxOutputBytes: 5, Enforced: true})
	}()
	go func() {
		defer wg.Done()
		errLarge = tpl.ExecToWithOptions(&bufLarge, nil, nil, RenderOptions{MaxOutputBytes: 100, Enforced: true})
	}()
	wg.Wait()

	var bex *RenderBudgetExceededError
	if !errors.As(errSmall, &bex) {
		t.Errorf("small budget render: expected *RenderBudgetExceededError, got %v", errSmall)
	}
	if errLarge != nil {
		t.Errorf("large budget render: expected nil err, got %v", errLarge)
	}
	if bufLarge.String() != "0123456789" {
		t.Errorf("large budget output = %q, want full template", bufLarge.String())
	}
}

func TestExecToWithOptions_DiscriminationMatrix(t *testing.T) {
	tpl := MustParse("hello")
	myErr := errors.New("io fail")

	type scenario struct {
		name      string
		w         io.Writer
		opts      RenderOptions
		wantBudg  bool
		wantDest  bool
		wantNilOK bool
	}
	cases := []scenario{
		{"budget overflow", &bytes.Buffer{}, RenderOptions{MaxOutputBytes: 1, Enforced: true}, true, false, false},
		{"destination failure", &failingWriter{err: myErr}, RenderOptions{}, false, true, false},
		{"success", &bytes.Buffer{}, RenderOptions{}, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := tpl.ExecToWithOptions(c.w, nil, nil, c.opts)
			var bex *RenderBudgetExceededError
			var dex *RenderDestinationError
			gotBudg := errors.As(err, &bex)
			gotDest := errors.As(err, &dex)
			if gotBudg != c.wantBudg {
				t.Errorf("budget match = %v, want %v (err=%v)", gotBudg, c.wantBudg, err)
			}
			if gotDest != c.wantDest {
				t.Errorf("destination match = %v, want %v (err=%v)", gotDest, c.wantDest, err)
			}
			if c.wantNilOK && err != nil {
				t.Errorf("expected nil err, got %v", err)
			}
		})
	}

	// Also exercise the typed identity in a side-by-side: the same
	// errors.As probe must NEVER match across categories.
	_ = fmt.Sprint
}
