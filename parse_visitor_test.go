package raymond

import (
	"errors"
	"strings"
	"testing"
)

func TestVisitor_Budget(t *testing.T) {
	t.Parallel()

	mk := func(n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString("{{x}}")
		}
		return sb.String()
	}

	t.Run("exactly-at-limit", func(t *testing.T) {
		opts := ParseOptions{Budget: Budget{MaxSubstitutions: 100, Enforced: true}}
		tpl, err := ParseWithOptions(mk(100), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := tpl.Report().Substitutions; got != 100 {
			t.Errorf("Substitutions = %d, want 100", got)
		}
	})

	t.Run("over-limit", func(t *testing.T) {
		opts := ParseOptions{Budget: Budget{MaxSubstitutions: 100, Enforced: true}}
		tpl, err := ParseWithOptions(mk(101), opts)
		if tpl != nil {
			t.Errorf("expected nil *Template on failure")
		}
		var be *BudgetExceededError
		if !errors.As(err, &be) {
			t.Fatalf("expected *BudgetExceededError, got %T %v", err, err)
		}
		if be.Kind != "substitutions" || be.Limit != 100 || be.Observed != 101 {
			t.Errorf("got %+v", be)
		}
	})

	t.Run("zero-substitutions-allowed", func(t *testing.T) {
		opts := ParseOptions{Budget: Budget{MaxSubstitutions: 100, Enforced: true}}
		if _, err := ParseWithOptions("hello world", opts); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("zero-budget-rejects-any", func(t *testing.T) {
		opts := ParseOptions{Budget: Budget{MaxSubstitutions: 0, Enforced: true}}
		_, err := ParseWithOptions("{{x}}", opts)
		var be *BudgetExceededError
		if !errors.As(err, &be) {
			t.Fatalf("expected *BudgetExceededError, got %T %v", err, err)
		}
		if be.Limit != 0 || be.Observed != 1 {
			t.Errorf("got %+v", be)
		}
	})

	t.Run("negative-unenforced-no-limit", func(t *testing.T) {
		opts := ParseOptions{Budget: Budget{MaxSubstitutions: -1, Enforced: false}}
		if _, err := ParseWithOptions(mk(500), opts); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestVisitor_SimpleMode(t *testing.T) {
	t.Parallel()
	opts := ParseOptions{Mode: ModeSimple}

	type row struct {
		src     string
		ok      bool
		want    string // expected Construct on failure
	}
	rows := []row{
		// success rows
		{`hello {{name}} you have {{count}} items`, true, ""},
		{`{{user.email}}`, true, ""},
		{`{{items.[0]}}`, true, ""},
		{`{{this}}`, true, ""},
		{`{{! comment }}`, true, ""},
		{`{{!-- block --}}`, true, ""},
		{`{{{name}}}`, true, ""},
		{`{{~name~}}`, true, ""},
		// failure rows
		{`{{#if x}}y{{/if}}`, false, "if"},
		{`{{#unless x}}y{{/unless}}`, false, "unless"},
		{`{{#each xs}}x{{/each}}`, false, "each"},
		{`{{#with x}}y{{/with}}`, false, "with"},
		{`{{> header}}`, false, "partial"},
		{`{{upper name}}`, false, "helper"},
		{`{{../x}}`, false, "parent-path"},
		{`{{@root.x}}`, false, "data-var"},
		{`{{@key}}`, false, "data-var"},
		{`{{@index}}`, false, "data-var"},
	}

	for _, r := range rows {
		t.Run(r.src, func(t *testing.T) {
			tpl, err := ParseWithOptions(r.src, opts)
			if r.ok {
				if err != nil {
					t.Errorf("expected success, got %v", err)
				}
				return
			}
			if tpl != nil {
				t.Errorf("expected nil *Template on failure")
			}
			var ce *CapabilityError
			if !errors.As(err, &ce) {
				t.Fatalf("expected *CapabilityError, got %T %v", err, err)
			}
			if ce.Construct != r.want {
				t.Errorf("Construct = %q, want %q", ce.Construct, r.want)
			}
			if ce.Loc.Line <= 0 {
				t.Errorf("Loc.Line = %d, want > 0", ce.Loc.Line)
			}
		})
	}
}

func TestVisitor_Granular(t *testing.T) {
	t.Parallel()

	type probe struct {
		src      string
		want     string // empty = success
	}
	probes := []probe{
		{`{{#if x}}y{{/if}}`, "if"},
		{`{{#unless x}}y{{/unless}}`, "unless"},
		{`{{#each xs}}x{{/each}}`, "each"},
		{`{{> header}}`, "partial"},
		{`{{#with x}}y{{/with}}`, "with"},
		{`{{upper name}}`, "helper"},
	}

	combos := []struct {
		caps  Capabilities
		allow map[string]bool // construct → allowed?
	}{
		{Capabilities{If: true}, map[string]bool{"if": true, "unless": true}},
		{Capabilities{Iteration: true}, map[string]bool{"each": true}},
		{Capabilities{Partials: true}, map[string]bool{"partial": true}},
		{Capabilities{If: true, Iteration: true, Partials: true}, map[string]bool{"if": true, "unless": true, "each": true, "partial": true}},
	}

	for _, c := range combos {
		c := c
		t.Run("", func(t *testing.T) {
			opts := ParseOptions{Capabilities: c.caps}
			for _, p := range probes {
				_, err := ParseWithOptions(p.src, opts)
				if c.allow[p.want] {
					if err != nil {
						t.Errorf("caps=%+v src=%q: expected success, got %v", c.caps, p.src, err)
					}
					continue
				}
				var ce *CapabilityError
				if !errors.As(err, &ce) {
					t.Errorf("caps=%+v src=%q: expected *CapabilityError, got %T %v", c.caps, p.src, err, err)
					continue
				}
				if ce.Construct != p.want {
					t.Errorf("caps=%+v src=%q: Construct=%q want %q", c.caps, p.src, ce.Construct, p.want)
				}
			}
		})
	}

	// All toggles false but Budget enforced → granular: rejects every block/partial/helper.
	t.Run("all-false-but-budget-enforced", func(t *testing.T) {
		opts := ParseOptions{Budget: Budget{MaxSubstitutions: 1000, Enforced: true}}
		for _, p := range probes {
			_, err := ParseWithOptions(p.src, opts)
			var ce *CapabilityError
			if !errors.As(err, &ce) {
				t.Errorf("src=%q: expected *CapabilityError, got %T %v", p.src, err, err)
			}
		}
	})

	// Zero ParseOptions{} → legacy: no capability error even on full feature set.
	t.Run("zero-options-legacy", func(t *testing.T) {
		opts := ParseOptions{}
		for _, p := range probes {
			if _, err := ParseWithOptions(p.src, opts); err != nil {
				t.Errorf("src=%q legacy unexpectedly errored: %v", p.src, err)
			}
		}
	})
}

func TestVisitor_NoPanic(t *testing.T) {
	t.Parallel()

	corpus := []string{
		"",
		"plain text",
		"{{x}}",
		strings.Repeat("{{x}}", 1024),
		"{{#if x}}{{#each xs}}{{#with y}}{{a}}{{/with}}{{/each}}{{/if}}",
		"{{> a}}{{>b}}{{> (lookup .)}}",
		"{{upper (lower name) suffix=z}}",
		"{{@root.x}}{{../foo}}{{this}}",
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
	}()

	for _, src := range corpus {
		_, _ = ParseWithOptions(src, ParseOptions{Mode: ModeSimple})
		_, _ = ParseWithOptions(src, ParseOptions{
			Capabilities: Capabilities{If: true, Iteration: true, Partials: true},
			Budget:       Budget{MaxSubstitutions: 1 << 20, Enforced: true},
		})
		_, _ = ParseWithOptions(src, ParseOptions{Budget: Budget{MaxSubstitutions: 1, Enforced: true}})
	}
}
