package raymond

import (
	"bytes"
	"errors"
	"testing"
)

func TestRenderOptions_ZeroValueIsLegacy(t *testing.T) {
	src := "Hello {{name}}! {{#if show}}visible{{/if}}"
	tpl := MustParse(src)
	ctx := map[string]any{"name": "World", "show": true}

	want, err := tpl.Exec(ctx)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	var buf bytes.Buffer
	if err := tpl.ExecToWithOptions(&buf, ctx, nil, RenderOptions{}); err != nil {
		t.Fatalf("ExecToWithOptions zero opts: %v", err)
	}
	if buf.String() != want {
		t.Errorf("zero RenderOptions output = %q, want %q (legacy parity)", buf.String(), want)
	}

	buf.Reset()
	if err := tpl.ExecTo(&buf, ctx); err != nil {
		t.Fatalf("ExecTo: %v", err)
	}
	if buf.String() != want {
		t.Errorf("ExecTo output = %q, want %q", buf.String(), want)
	}
}

func TestRenderOptions_EnforcedZeroBudget(t *testing.T) {
	tpl := MustParse("hi")
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 0, Enforced: true})
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
	if bex.Limit != 0 {
		t.Errorf("Limit = %d, want 0", bex.Limit)
	}

	// empty-output template succeeds at zero budget
	buf.Reset()
	emptyTpl := MustParse("")
	if err := emptyTpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: 0, Enforced: true}); err != nil {
		t.Errorf("empty template at zero budget: unexpected err %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty template wrote %d bytes, want 0", buf.Len())
	}
}

func TestRenderOptions_NegativeBudgetRejected(t *testing.T) {
	tpl := MustParse("hi")
	var buf bytes.Buffer
	err := tpl.ExecToWithOptions(&buf, nil, nil, RenderOptions{MaxOutputBytes: -1, Enforced: true})
	var bex *RenderBudgetExceededError
	if !errors.As(err, &bex) {
		t.Fatalf("err = %v, want *RenderBudgetExceededError", err)
	}
	if bex.Limit != -1 {
		t.Errorf("Limit = %d, want -1", bex.Limit)
	}
	if buf.Len() != 0 {
		t.Errorf("destination received %d bytes; expected 0 (rejection before any write)", buf.Len())
	}
}
