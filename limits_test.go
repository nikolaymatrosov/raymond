package raymond

import (
	"errors"
	"testing"
)

func TestLimitError_Sentinels(t *testing.T) {
	cases := []struct {
		kind     string
		sentinel error
	}{
		{"output bytes", ErrOutputLimit},
		{"substitutions", ErrSubstitutionLimit},
		{"steps", ErrStepLimit},
		{"nodes", ErrTemplateTooComplex},
		{"depth", ErrTemplateTooComplex},
		{"source size", ErrTemplateTooLarge},
	}
	for _, c := range cases {
		err := newLimitError(c.kind, 42, c.sentinel)
		if !errors.Is(err, c.sentinel) {
			t.Errorf("kind %q: errors.Is sentinel = false", c.kind)
		}
		var le *LimitError
		if !errors.As(err, &le) || le.Kind != c.kind || le.Limit != 42 {
			t.Errorf("kind %q: As/fields failed: %v", c.kind, err)
		}
	}
}
