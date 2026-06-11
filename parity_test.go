package raymond

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// execNew builds the template exactly the way launchTests does
// (Parse + RegisterHelpers + RegisterPartials + privData), then renders
// through the NEW streaming engine via tpl.execute.
func execNew(t *testing.T, test Test) (output string, err error) {
	t.Helper()

	tpl, err := Parse(test.input)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	if len(test.helpers) > 0 {
		tpl.RegisterHelpers(test.helpers)
	}

	if len(test.partials) > 0 {
		tpl.RegisterPartials(test.partials)
	}

	var privData *DataFrame
	if test.privData != nil {
		privData = NewDataFrame()
		for k, v := range test.privData {
			privData.Set(k, v)
		}
	}

	// user-supplied legacy helpers may panic, like the old engine path;
	// recover them into an error the same way ExecWith does
	defer errRecover(&err)

	var sb strings.Builder
	err = tpl.execute(context.Background(), &sb, nil, test.data, privData, Limits{})
	return sb.String(), err
}

// runParity renders every test through the new engine and applies the
// same acceptance logic as launchTests (string equality, or membership
// when output is a []string).
func runParity(t *testing.T, tests []Test) {
	t.Helper()

	for _, test := range tests {
		output, err := execNew(t, test)
		if err != nil {
			t.Errorf("Parity test '%s' failed\ninput:\n\t'%s'\ndata:\n\t%s\nerror:\n\t%s", test.name, test.input, Str(test.data), err)
			continue
		}

		if expectedArr, ok := test.output.([]string); ok {
			match := false
			for _, expectedStr := range expectedArr {
				if expectedStr == output {
					match = true
					break
				}
			}
			if !match {
				t.Errorf("Parity test '%s' failed\ninput:\n\t'%s'\ndata:\n\t%s\npartials:\n\t%s\nexpected\n\t%q\ngot\n\t%q", test.name, test.input, Str(test.data), Str(test.partials), expectedArr, output)
			}
			continue
		}

		expectedStr, ok := test.output.(string)
		if !ok {
			panic(fmt.Errorf("Erroneous test output description: %q", test.output))
		}
		if expectedStr != output {
			t.Errorf("Parity test '%s' failed\ninput:\n\t'%s'\ndata:\n\t%s\npartials:\n\t%s\nexpected\n\t%q\ngot\n\t%q", test.name, test.input, Str(test.data), Str(test.partials), expectedStr, output)
		}
	}
}

// runParityErrors mirrors launchErrorTests: the new engine must return
// an error whose text matches the same regexp acceptance.
func runParityErrors(t *testing.T, tests []Test) {
	t.Helper()

	for _, test := range tests {
		output, err := execNew(t, test)
		if err == nil {
			t.Errorf("Parity test '%s' failed - Error expected\ninput:\n\t'%s'\ngot\n\t%q", test.name, test.input, output)
			continue
		}

		var errMatch error
		match := false

		if expectedArr, ok := test.output.([]string); ok {
			if len(expectedArr) > 0 {
				for _, expectedStr := range expectedArr {
					match, errMatch = regexp.MatchString(regexp.QuoteMeta(expectedStr), fmt.Sprint(err))
					if errMatch != nil {
						panic("Failed to match regexp")
					}
					if match {
						break
					}
				}
			} else {
				match = true
			}
		} else {
			expectedStr, ok := test.output.(string)
			if !ok {
				panic(fmt.Errorf("Erroneous test output description: %q", test.output))
			}
			if expectedStr != "" {
				match, errMatch = regexp.MatchString(regexp.QuoteMeta(expectedStr), fmt.Sprint(err))
				if errMatch != nil {
					panic("Failed to match regexp")
				}
			} else {
				match = true
			}
		}

		if !match {
			t.Errorf("Parity test '%s' failed - Incorrect error returned\ninput:\n\t'%s'\ndata:\n\t%s\nexpected\n\t%q\ngot\n\t%q", test.name, test.input, Str(test.data), test.output, err)
		}
	}
}

//
// One TestParity_* per in-package table
//

func TestParity_Eval(t *testing.T) {
	t.Logf("parity cases: %d", len(evalTests))
	runParity(t, evalTests)
}

func TestParity_EvalErrors(t *testing.T) {
	t.Logf("parity cases: %d", len(evalErrors))
	runParityErrors(t, evalErrors)
}

func TestParity_Helper(t *testing.T) {
	t.Logf("parity cases: %d", len(helperTests))
	runParity(t, helperTests)
}

func TestParity_Mustache(t *testing.T) {
	skipFiles := map[string]bool{
		// mustache lambdas differ from handlebars lambdas (TestMustache parity)
		"~lambdas.yml": true,
	}

	total := 0
	for _, fileName := range mustacheTestFiles() {
		if skipFiles[fileName] {
			continue
		}
		tests := testsFromMustacheFile(fileName)
		total += len(tests)
		runParity(t, tests)
	}
	t.Logf("parity cases: %d", total)
}

// parityLambdaInterMult is a parity-local counter: the package-level
// mustacheLambdasTests table shares musTestLambdaInterMult with
// TestMustacheLambdas, so the stateful entries are replicated here with
// independent state instead of reusing the shared table verbatim.
var parityLambdaInterMult = 0

var parityMustacheLambdasTests = []Test{
	{
		"Interpolation",
		"Hello, {{lambda}}!",
		map[string]interface{}{"lambda": func() string { return "world" }},
		nil, nil, nil,
		"Hello, world!",
	},
	{
		"Interpolation - Multiple Calls",
		"{{lambda}} == {{{lambda}}} == {{lambda}}",
		map[string]interface{}{"lambda": func() string {
			parityLambdaInterMult++
			return Str(parityLambdaInterMult)
		}},
		nil, nil, nil,
		"1 == 2 == 3",
	},
	{
		"Escaping",
		"<{{lambda}}{{{lambda}}}",
		map[string]interface{}{"lambda": func() string { return ">" }},
		nil, nil, nil,
		"<&gt;>",
	},
	{
		"Section - Multiple Calls",
		"{{#lambda}}FILE{{/lambda}} != {{#lambda}}LINE{{/lambda}}",
		map[string]interface{}{"lambda": func(options *Options) string {
			return "__" + options.Fn() + "__"
		}},
		nil, nil, nil,
		"__FILE__ != __LINE__",
	},
}

func TestParity_MustacheLambdas(t *testing.T) {
	t.Logf("parity cases: %d", len(parityMustacheLambdasTests))
	runParity(t, parityMustacheLambdasTests)
}
