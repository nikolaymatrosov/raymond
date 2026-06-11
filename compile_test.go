package raymond

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestCompile_SourceSizeLimit(t *testing.T) {
	src := strings.Repeat("a", 100)
	if _, err := Compile(src, Limits{MaxTemplateSize: 100}); err != nil {
		t.Fatalf("exact fit failed: %v", err)
	}
	_, err := Compile(src, Limits{MaxTemplateSize: 99})
	if !errors.Is(err, ErrTemplateTooLarge) {
		t.Errorf("err = %v, want ErrTemplateTooLarge", err)
	}
}

func TestCompile_NodeAndDepthLimits(t *testing.T) {
	if _, err := Compile(strings.Repeat("{{x}}", 100), Limits{MaxNodes: 10}); !errors.Is(err, ErrTemplateTooComplex) {
		t.Errorf("nodes: err = %v, want ErrTemplateTooComplex", err)
	}
	deep := "{{#if a}}{{#if b}}{{#if c}}x{{/if}}{{/if}}{{/if}}"
	if _, err := Compile(deep, Limits{MaxDepth: 3}); !errors.Is(err, ErrTemplateTooComplex) {
		t.Errorf("depth: err = %v, want ErrTemplateTooComplex", err)
	}
}

func TestExecute_OutputLimit(t *testing.T) {
	c, err := Compile("{{name}}", Limits{MaxOutputBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := c.Execute(context.Background(), &buf, map[string]string{"name": "12345"}); err != nil {
		t.Fatalf("exact fit failed: %v", err)
	}
	buf.Reset()
	err = c.Execute(context.Background(), &buf, map[string]string{"name": "123456"})
	if !errors.Is(err, ErrOutputLimit) {
		t.Errorf("err = %v, want ErrOutputLimit", err)
	}
	if buf.Len() > 5 {
		t.Errorf("destination got %d bytes, budget 5", buf.Len())
	}
}

func TestExecute_SubstitutionLimit(t *testing.T) {
	c, err := Compile("{{a}}{{a}}{{a}}", Limits{MaxSubstitutions: 2})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Execute(context.Background(), &bytes.Buffer{}, map[string]string{"a": "x"})
	if !errors.Is(err, ErrSubstitutionLimit) {
		t.Errorf("err = %v, want ErrSubstitutionLimit", err)
	}
}

func TestExecute_StepLimit(t *testing.T) {
	items := make([]int, 10000)
	c, err := Compile("{{#each items}}{{this}}{{/each}}", Limits{MaxSteps: 100})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Execute(context.Background(), &bytes.Buffer{}, map[string]interface{}{"items": items})
	if !errors.Is(err, ErrStepLimit) {
		t.Errorf("err = %v, want ErrStepLimit", err)
	}
}

func TestExecute_ContextCancellation(t *testing.T) {
	items := make([]int, 100000)
	c, err := Compile("{{#each items}}{{this}}{{/each}}", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = c.Execute(ctx, &bytes.Buffer{}, map[string]interface{}{"items": items})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestExecute_Concurrent(t *testing.T) {
	c, err := Compile("Hello {{name}}!", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var sb strings.Builder
			name := fmt.Sprintf("g%d", i)
			if err := c.Execute(context.Background(), &sb, map[string]string{"name": name}); err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			if want := "Hello " + name + "!"; sb.String() != want {
				t.Errorf("goroutine %d: got %q want %q", i, sb.String(), want)
			}
		}(i)
	}
	wg.Wait()
}

func TestExecute_StreamingHelper(t *testing.T) {
	c, err := Compile("{{shout msg}}", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	c.RegisterHelper("shout", HelperFunc(func(hc *HelperCall) error {
		_, werr := hc.WriteString(strings.ToUpper(hc.Param(0).Str()) + "!")
		return werr
	}))
	var sb strings.Builder
	if err := c.Execute(context.Background(), &sb, map[string]string{"msg": "h<i"}); err != nil {
		t.Fatal(err)
	}
	// escaped-mustache position escapes streamed bytes
	if sb.String() != "H&lt;I!" {
		t.Errorf("got %q, want %q", sb.String(), "H&lt;I!")
	}
}

func TestExecute_CompiledPartial(t *testing.T) {
	p, err := Compile("[{{x}}]", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile("{{> box}}", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	c.RegisterPartial("box", p)
	var sb strings.Builder
	if err := c.Execute(context.Background(), &sb, map[string]string{"x": "v"}); err != nil {
		t.Fatal(err)
	}
	if sb.String() != "[v]" {
		t.Errorf("got %q, want %q", sb.String(), "[v]")
	}
}
