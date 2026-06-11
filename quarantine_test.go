package raymond

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reflectAllowed lists the only non-test files permitted to import
// reflect: the adapter layer and the legacy public utilities.
var reflectAllowed = map[string]bool{
	"adapt.go":         true,
	"adapt_helpers.go": true,
	"string.go":        true,
	"utils.go":         true,
	"data_frame.go":    true, // mapStringInterface
	"helper.go":        true, // legacy registration validation
	"template.go":      true, // legacy tpl.helpers map
}

func TestReflectionQuarantine(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || reflectAllowed[f] {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), `"reflect"`) {
			t.Errorf("%s imports reflect — core files must stay reflection-free", f)
		}
	}
}
