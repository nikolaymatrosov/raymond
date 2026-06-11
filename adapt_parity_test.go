package raymond

import (
	"testing"
)

type adaptParityFoo struct {
	Title   string
	Num     int
	Aliased string `handlebars:"alias"`
	Inner   adaptParityInner
}

type adaptParityInner struct{ Name string }

func (f *adaptParityFoo) Subject() string { return "subj" }

func TestAdapt_StrAndTruthParity(t *testing.T) {
	corpus := []any{
		nil, "", "x", SafeString("<b>"), true, false, 0, 5, -5,
		int8(3), uint8(7), uint64(9), float32(3.14), 3.14, 0.0,
		[]string{"a", "b"}, []any{1, "x"}, []int{},
		map[string]any{"a": 1}, map[string]string{},
		adaptParityInner{Name: "n"}, &adaptParityInner{Name: "n"},
	}

	for _, x := range corpus {
		v := adaptValue(x)
		wantStr := Str(x)
		wantTruth := IsTrue(x)
		if got := v.Str(); got != wantStr {
			t.Errorf("adaptValue(%#v).Str() = %q, want %q", x, got, wantStr)
		}
		if got := v.Truthy(); got != wantTruth {
			t.Errorf("adaptValue(%#v).Truthy() = %v, want %v", x, got, wantTruth)
		}
	}
}

func TestAdapt_StructLookup(t *testing.T) {
	foo := &adaptParityFoo{
		Title:   "T",
		Num:     42,
		Aliased: "A",
		Inner:   adaptParityInner{Name: "inner-name"},
	}

	v := adaptValue(foo)
	if v.Kind() != KindMap {
		t.Fatalf("adaptValue(&adaptParityFoo{}).Kind() = %v, want KindMap", v.Kind())
	}

	// title field lookup
	titleVal, ok := v.asData().Lookup("title")
	if !ok {
		t.Fatal("Lookup(\"title\") not found")
	}
	if got := titleVal.Str(); got != "T" {
		t.Errorf("Lookup(\"title\").Str() = %q, want \"T\"", got)
	}

	// alias struct tag lookup
	aliasVal, ok := v.asData().Lookup("alias")
	if !ok {
		t.Fatal("Lookup(\"alias\") not found")
	}
	if got := aliasVal.Str(); got != "A" {
		t.Errorf("Lookup(\"alias\").Str() = %q, want \"A\"", got)
	}

	// method lookup (subject)
	subjectVal, ok := v.asData().Lookup("subject")
	if !ok {
		t.Fatal("Lookup(\"subject\") not found")
	}
	if subjectVal.Kind() != KindFunc {
		t.Errorf("Lookup(\"subject\").Kind() = %v, want KindFunc", subjectVal.Kind())
	}
	if !subjectVal.fromMethod {
		t.Error("Lookup(\"subject\").fromMethod = false, want true")
	}

	// inner struct lookup
	innerVal, ok := v.asData().Lookup("inner")
	if !ok {
		t.Fatal("Lookup(\"inner\") not found")
	}
	if innerVal.Kind() != KindMap {
		t.Errorf("Lookup(\"inner\").Kind() = %v, want KindMap", innerVal.Kind())
	}

	// missing key
	_, ok = v.asData().Lookup("nope")
	if ok {
		t.Error("Lookup(\"nope\") returned ok=true, want false")
	}
}

func TestAdapt_MapAndSliceLookup(t *testing.T) {
	// map lookup
	m := map[string]any{"foo": "bar", "num": 99}
	mv := adaptValue(m)
	fooVal, ok := mv.asData().Lookup("foo")
	if !ok {
		t.Fatal("map Lookup(\"foo\") not found")
	}
	if got := fooVal.Str(); got != "bar" {
		t.Errorf("map Lookup(\"foo\").Str() = %q, want \"bar\"", got)
	}

	// slice
	sl := []string{"a", "b"}
	sv := adaptValue(sl)
	if sv.Kind() != KindList {
		t.Fatalf("adaptValue([]string{}).Kind() = %v, want KindList", sv.Kind())
	}
	if sv.asList().Len() != 2 {
		t.Fatalf("list.Len() = %d, want 2", sv.asList().Len())
	}
	if got := sv.asList().Index(1).Str(); got != "b" {
		t.Errorf("list.Index(1).Str() = %q, want \"b\"", got)
	}

	// numeric-name lookup
	bVal, ok := sv.asData().Lookup("1")
	if !ok {
		t.Fatal("slice Lookup(\"1\") not found")
	}
	if got := bVal.Str(); got != "b" {
		t.Errorf("slice Lookup(\"1\").Str() = %q, want \"b\"", got)
	}

	// negative indices must not panic, just fail to resolve
	if _, ok := sv.asData().Lookup("-1"); ok {
		t.Error("Lookup(-1) should not resolve")
	}
}

func TestTemplateRegisterHelper_RejectsStreaming(t *testing.T) {
	tpl := MustParse("{{x}}")
	defer func() {
		if recover() == nil {
			t.Error("registering a streaming helper on a Template must panic")
		}
	}()
	tpl.RegisterHelper("s", HelperFunc(func(hc *HelperCall) error { return nil }))
}

func TestAdapt_PointerTruthIndirects(t *testing.T) {
	empty := ""
	full := "x"
	if adaptValue(&empty).Truthy() {
		t.Error("pointer to empty string must be falsy (old engine indirects before truth)")
	}
	if !adaptValue(&full).Truthy() {
		t.Error("pointer to non-empty string must be truthy")
	}
	var nilPtr *string
	if adaptValue(nilPtr).Truthy() {
		t.Error("nil pointer must be falsy")
	}
}
