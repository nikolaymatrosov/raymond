package raymond

import "testing"

//
// Those tests come from:
//   https://github.com/wycats/handlebars.js/blob/master/bench/
//
// Note that handlebars.js does NOT benchmark template compilation, it only benchmarks evaluation.
//

func BenchmarkArguments(b *testing.B) {
	source := `{{foo person "person" 1 true foo=bar foo="person" foo=1 foo=true}}`

	ctx := map[string]bool{
		"bar": true,
	}

	tpl := MustParse(source)
	tpl.RegisterHelper("foo", func(a, b, c, d interface{}) string { return "" })

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkArrayEach(b *testing.B) {
	source := `{{#each names}}{{name}}{{/each}}`

	ctx := map[string][]map[string]string{
		"names": {
			{"name": "Moe"},
			{"name": "Larry"},
			{"name": "Curly"},
			{"name": "Shemp"},
		},
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkArrayMustache(b *testing.B) {
	source := `{{#names}}{{name}}{{/names}}`

	ctx := map[string][]map[string]string{
		"names": {
			{"name": "Moe"},
			{"name": "Larry"},
			{"name": "Curly"},
			{"name": "Shemp"},
		},
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkComplex(b *testing.B) {
	source := `<h1>{{header}}</h1>
{{#if items}}
  <ul>
    {{#each items}}
      {{#if current}}
        <li><strong>{{name}}</strong></li>
      {{^}}
        <li><a href="{{url}}">{{name}}</a></li>
      {{/if}}
    {{/each}}
  </ul>
{{^}}
  <p>The list is empty.</p>
{{/if}}
`

	ctx := map[string]interface{}{
		"header":   func() string { return "Colors" },
		"hasItems": true,
		"items": []map[string]interface{}{
			{"name": "red", "current": true, "url": "#Red"},
			{"name": "green", "current": false, "url": "#Green"},
			{"name": "blue", "current": false, "url": "#Blue"},
		},
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkData(b *testing.B) {
	source := `{{#each names}}{{@index}}{{name}}{{/each}}`

	ctx := map[string][]map[string]string{
		"names": {
			{"name": "Moe"},
			{"name": "Larry"},
			{"name": "Curly"},
			{"name": "Shemp"},
		},
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkDepth1(b *testing.B) {
	source := `{{#each names}}{{../foo}}{{/each}}`

	ctx := map[string]interface{}{
		"names": []map[string]string{
			{"name": "Moe"},
			{"name": "Larry"},
			{"name": "Curly"},
			{"name": "Shemp"},
		},
		"foo": "bar",
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkDepth2(b *testing.B) {
	source := `{{#each names}}{{#each name}}{{../bat}}{{../../foo}}{{/each}}{{/each}}`

	ctx := map[string]interface{}{
		"names": []map[string]interface{}{
			{"bat": "foo", "name": []string{"Moe"}},
			{"bat": "foo", "name": []string{"Larry"}},
			{"bat": "foo", "name": []string{"Curly"}},
			{"bat": "foo", "name": []string{"Shemp"}},
		},
		"foo": "bar",
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkObjectMustache(b *testing.B) {
	source := `{{#person}}{{name}}{{age}}{{/person}}`

	ctx := map[string]interface{}{
		"person": map[string]interface{}{
			"name": "Larry",
			"age":  45,
		},
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkObject(b *testing.B) {
	source := `{{#with person}}{{name}}{{age}}{{/with}}`

	ctx := map[string]interface{}{
		"person": map[string]interface{}{
			"name": "Larry",
			"age":  45,
		},
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkPartialRecursion(b *testing.B) {
	source := `{{name}}{{#each kids}}{{>recursion}}{{/each}}`

	ctx := map[string]interface{}{
		"name": 1,
		"kids": []map[string]interface{}{
			{
				"name": "1.1",
				"kids": []map[string]interface{}{
					{
						"name": "1.1.1",
						"kids": []map[string]interface{}{},
					},
				},
			},
		},
	}

	tpl := MustParse(source)

	partial := MustParse(`{{name}}{{#each kids}}{{>recursion}}{{/each}}`)
	tpl.RegisterPartialTemplate("recursion", partial)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkPartial(b *testing.B) {
	source := `{{#each peeps}}{{>variables}}{{/each}}`

	ctx := map[string]interface{}{
		"peeps": []map[string]interface{}{
			{"name": "Moe", "count": 15},
			{"name": "Moe", "count": 5},
			{"name": "Curly", "count": 1},
		},
	}

	tpl := MustParse(source)

	partial := MustParse(`Hello {{name}}! You have {{count}} new messages.`)
	tpl.RegisterPartialTemplate("variables", partial)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkPath(b *testing.B) {
	source := `{{person.name.bar.baz}}{{person.age}}{{person.foo}}{{animal.age}}`

	ctx := map[string]interface{}{
		"person": map[string]interface{}{
			"name": map[string]interface{}{
				"bar": map[string]string{
					"baz": "Larry",
				},
			},
			"age": 45,
		},
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

func BenchmarkString(b *testing.B) {
	source := `Hello world`

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(nil)
	}
}

func BenchmarkSubExpression(b *testing.B) {
	source := `{{echo (header)}}`

	ctx := map[string]interface{}{}

	tpl := MustParse(source)
	tpl.RegisterHelpers(map[string]interface{}{
		"echo":   func(v string) string { return "foo " + v },
		"header": func() string { return "Colors" },
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

// representativeTemplate mixes ~50 substitutions with an #if and an #each
// — used by BenchmarkParseWithOptions_* to compare visitor-on vs.
// visitor-off costs (Constitution Principle V, contract C9).
var representativeTemplate = func() string {
	s := "<h1>{{title}}</h1>\n"
	for i := 0; i < 48; i++ {
		s += "{{x}}"
	}
	s += "\n{{#if cond}}{{a}}{{/if}}\n{{#each xs}}{{this}}{{/each}}"
	return s
}()

// BenchmarkParseWithOptions_Full measures the opt-in zero-options path,
// which MUST not enter the visitor and so should track legacy Parse cost.
func BenchmarkParseWithOptions_Full(b *testing.B) {
	src := representativeTemplate
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseWithOptions(src, ParseOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseWithOptions_Granular measures the visitor-on path with
// every toggle enabled and a 1 MiB substitution budget.
func BenchmarkParseWithOptions_Granular(b *testing.B) {
	src := representativeTemplate
	opts := ParseOptions{
		Capabilities: Capabilities{If: true, Iteration: true, Partials: true},
		Budget:       Budget{MaxSubstitutions: 1 << 20, Enforced: true},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseWithOptions(src, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVariables(b *testing.B) {
	source := `Hello {{name}}! You have {{count}} new messages.`

	ctx := map[string]interface{}{
		"name":  "Mick",
		"count": 30,
	}

	tpl := MustParse(source)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

// BenchmarkExec_NoBudget_Legacy measures the legacy Exec path with no
// options at all. It is the pre-feature baseline for SC-005 (the
// legacy path must show no measurable regression versus pre-feature).
func BenchmarkExec_NoBudget_Legacy(b *testing.B) {
	source := `Hello {{name}}! {{#each items}}{{this}}/{{/each}}`
	ctx := map[string]interface{}{
		"name":  "Alice",
		"items": []string{"a", "b", "c", "d", "e", "f", "g", "h"},
	}
	tpl := MustParse(source)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tpl.MustExec(ctx)
	}
}

// BenchmarkExecTo_WithBudget measures the streaming path with a budget
// configured. Used together with BenchmarkExec_NoBudget_Legacy to gate
// SC-004 (within 10% of the same render with no budget).
func BenchmarkExecTo_WithBudget(b *testing.B) {
	source := `Hello {{name}}! {{#each items}}{{this}}/{{/each}}`
	ctx := map[string]interface{}{
		"name":  "Alice",
		"items": []string{"a", "b", "c", "d", "e", "f", "g", "h"},
	}
	tpl := MustParse(source)
	opts := RenderOptions{MaxOutputBytes: 1 << 20, Enforced: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tpl.ExecToWithOptions(discardWriter{}, ctx, nil, opts); err != nil {
			b.Fatal(err)
		}
	}
}

// discardWriter is a benchmark-local io.Writer that drops everything.
// We avoid a dependency on `io.Discard` here so the file's import set
// stays minimal.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
