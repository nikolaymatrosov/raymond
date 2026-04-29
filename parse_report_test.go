package raymond

import (
	"errors"
	"sort"
	"testing"
)

func TestReport_PopulatedOnSuccess(t *testing.T) {
	t.Parallel()
	src := `{{a}}{{b}}{{#if x}}{{c}}{{/if}}{{#each xs}}{{d}}{{/each}}{{> p}}`
	opts := ParseOptions{
		Capabilities: Capabilities{If: true, Iteration: true, Partials: true},
		Budget:       Budget{MaxSubstitutions: 1000, Enforced: true},
	}
	tpl, err := ParseWithOptions(src, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := tpl.Report()
	if r.Substitutions != 4 {
		t.Errorf("Substitutions = %d, want 4", r.Substitutions)
	}
	if !sort.StringsAreSorted(r.Constructs) {
		t.Errorf("Constructs not sorted: %v", r.Constructs)
	}
	allowed := map[string]bool{"if": true, "unless": true, "each": true, "with": true, "partial": true, "helper": true}
	for _, c := range r.Constructs {
		if !allowed[c] {
			t.Errorf("unexpected construct %q", c)
		}
	}
	want := map[string]bool{"if": true, "each": true, "partial": true}
	got := map[string]bool{}
	for _, c := range r.Constructs {
		got[c] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing construct %q in %v", k, r.Constructs)
		}
	}
}

func TestReport_ReturnsCopy(t *testing.T) {
	t.Parallel()
	src := `{{#if x}}y{{/if}}{{> p}}`
	opts := ParseOptions{Capabilities: Capabilities{If: true, Partials: true}}
	tpl, err := ParseWithOptions(src, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r1 := tpl.Report()
	if len(r1.Constructs) > 0 {
		r1.Constructs[0] = "MUTATED"
	}
	r2 := tpl.Report()
	for _, c := range r2.Constructs {
		if c == "MUTATED" {
			t.Errorf("Report() returned mutable internal slice: %v", r2.Constructs)
		}
	}
}

func TestReport_LegacyParseIsZero(t *testing.T) {
	t.Parallel()
	tpl, err := Parse(`{{a}}{{#if x}}{{b}}{{/if}}`)
	if err != nil {
		t.Fatal(err)
	}
	r := tpl.Report()
	if r.Substitutions != 0 || len(r.Constructs) != 0 {
		t.Errorf("legacy Report() = %+v, want zero", r)
	}
	// Same for zero-options ParseWithOptions.
	tpl, err = ParseWithOptions(`{{a}}{{#if x}}{{b}}{{/if}}`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r = tpl.Report()
	if r.Substitutions != 0 || len(r.Constructs) != 0 {
		t.Errorf("zero-opts Report() = %+v, want zero", r)
	}
}

func TestReport_CapabilityError_HasLoc(t *testing.T) {
	t.Parallel()
	_, err := ParseWithOptions("\n\n{{#if x}}y{{/if}}", ParseOptions{Mode: ModeSimple})
	var ce *CapabilityError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CapabilityError, got %T %v", err, err)
	}
	if ce.Loc.Line <= 0 {
		t.Errorf("Loc.Line = %d, want > 0", ce.Loc.Line)
	}
}
