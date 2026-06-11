package raymond

import "testing"

func TestValue_ScalarStrAndTruth(t *testing.T) {
	cases := []struct {
		v     Value
		str   string
		truth bool
	}{
		{stringValue("hi", false), "hi", true},
		{stringValue("", false), "", false},
		{stringValue("<b>", true), "<b>", true}, // SafeString
		{boolValue(true), "true", true},
		{boolValue(false), "false", false},
		{intValue(-3, int(-3)), "-3", true},
		{intValue(0, int(0)), "0", false},
		{uintValue(7, uint8(7)), "7", true},
		{floatValue(3.5, 3.5), "3.5", true},
		{floatValue(0, 0.0), "0", false},
		{Value{}, "", false}, // invalid
	}
	for i, c := range cases {
		if got := c.v.Str(); got != c.str {
			t.Errorf("case %d: Str() = %q, want %q", i, got, c.str)
		}
		if got := c.v.Truthy(); got != c.truth {
			t.Errorf("case %d: Truthy() = %v, want %v", i, got, c.truth)
		}
	}
}

func TestValueMap_Lookup(t *testing.T) {
	m := valueMap{"a": intValue(1, 1)}
	v, ok := m.Lookup("a")
	if !ok || v.Str() != "1" {
		t.Errorf("Lookup(a) = %v,%v", v, ok)
	}
	if _, ok := m.Lookup("b"); ok {
		t.Error("Lookup(b) should not resolve")
	}
}

func TestValue_InterfaceRoundTrip(t *testing.T) {
	v := intValue(5, int(5))
	if n, ok := v.Interface().(int); !ok || n != 5 {
		t.Errorf("Interface() = %#v, want int 5", v.Interface())
	}
}
