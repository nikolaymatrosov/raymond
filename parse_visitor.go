package raymond

import (
	"sort"

	"github.com/aymerick/raymond/ast"
)

// capVisitor enforces a ParseOptions configuration over a parsed AST. It is
// invoked from ParseWithOptions and never from the legacy Parse path.
//
// On the first violation it sets err and short-circuits subsequent visits.
// Substitution counting and budget enforcement happen on every
// MustacheStatement; capability rejection happens at the offending node.
type capVisitor struct {
	opts       ParseOptions
	simple     bool
	granular   bool
	fullLegacy bool

	subs       int
	constructs map[string]struct{}
	err        error
}

func newCapVisitor(opts ParseOptions) *capVisitor {
	v := &capVisitor{
		opts:       opts,
		constructs: make(map[string]struct{}),
	}
	switch {
	case opts.Mode == ModeSimple:
		v.simple = true
	case opts.Capabilities.If || opts.Capabilities.Iteration || opts.Capabilities.Partials || opts.Budget.Enforced:
		v.granular = true
	default:
		v.fullLegacy = true
	}
	return v
}

// runCapVisitor walks prog under opts and produces either a ParseReport or
// a typed error (*CapabilityError or *BudgetExceededError). When opts
// resolve to legacy semantics it returns the zero report without walking.
func runCapVisitor(prog *ast.Program, opts ParseOptions) (ParseReport, error) {
	v := newCapVisitor(opts)
	if v.fullLegacy {
		return ParseReport{}, nil
	}
	prog.Accept(v)
	if v.err != nil {
		return ParseReport{}, v.err
	}
	return v.buildReport(), nil
}

func (v *capVisitor) buildReport() ParseReport {
	parts := make([]string, 0, len(v.constructs))
	for c := range v.constructs {
		parts = append(parts, c)
	}
	sort.Strings(parts)
	return ParseReport{Substitutions: v.subs, Constructs: parts}
}

func (v *capVisitor) reject(construct string, loc ast.Loc) {
	if v.err == nil {
		v.err = &CapabilityError{Construct: construct, Loc: loc}
	}
}

// VisitProgram iterates the program body, short-circuiting on the first error.
func (v *capVisitor) VisitProgram(node *ast.Program) interface{} {
	for _, n := range node.Body {
		if v.err != nil {
			return nil
		}
		n.Accept(v)
	}
	return nil
}

// VisitMustache classifies the mustache and either counts it as a plain
// substitution (subject to the budget) or rejects it.
func (v *capVisitor) VisitMustache(node *ast.MustacheStatement) interface{} {
	if v.err != nil {
		return nil
	}
	plain, simpleConstruct := classifyMustache(node.Expression)
	if plain {
		v.subs++
		if v.opts.Budget.Enforced && v.subs > v.opts.Budget.MaxSubstitutions {
			v.err = &BudgetExceededError{
				Kind:     "substitutions",
				Limit:    v.opts.Budget.MaxSubstitutions,
				Observed: v.subs,
			}
		}
		return nil
	}
	if v.simple {
		v.reject(simpleConstruct, node.Loc)
		return nil
	}
	// granular: any non-plain mustache is a helper-style mustache
	v.reject("helper", node.Loc)
	return nil
}

// classifyMustache reports whether a mustache expression is a plain
// current-context substitution. When false, it returns the simple-mode
// construct name to use in a CapabilityError.
func classifyMustache(expr *ast.Expression) (plain bool, simpleConstruct string) {
	if expr == nil {
		return false, "helper"
	}
	path, ok := expr.Path.(*ast.PathExpression)
	if !ok {
		// numeric / string / boolean literal in mustache position — treat as helper.
		return false, "helper"
	}
	if path.Depth > 0 {
		return false, "parent-path"
	}
	if path.Data {
		return false, "data-var"
	}
	if len(expr.Params) > 0 || expr.Hash != nil {
		return false, "helper"
	}
	return true, ""
}

// VisitBlock classifies the block helper and either records it (recursing
// into Program/Inverse) or rejects it. {{else}} branches travel with their
// parent block (no separate toggle).
func (v *capVisitor) VisitBlock(node *ast.BlockStatement) interface{} {
	if v.err != nil {
		return nil
	}
	construct := classifyBlockConstruct(blockHelperName(node))

	if v.simple {
		v.reject(construct, node.Loc)
		return nil
	}
	allowed := false
	switch construct {
	case "if", "unless":
		allowed = v.opts.Capabilities.If
	case "each":
		allowed = v.opts.Capabilities.Iteration
	}
	// "with" and "helper" always rejected in granular mode.
	if !allowed {
		v.reject(construct, node.Loc)
		return nil
	}
	v.constructs[construct] = struct{}{}
	if node.Program != nil {
		node.Program.Accept(v)
	}
	if v.err == nil && node.Inverse != nil {
		node.Inverse.Accept(v)
	}
	return nil
}

func blockHelperName(node *ast.BlockStatement) string {
	if node == nil || node.Expression == nil {
		return ""
	}
	path, ok := node.Expression.Path.(*ast.PathExpression)
	if !ok {
		return ""
	}
	if len(path.Parts) == 0 {
		return ""
	}
	return path.Parts[0]
}

func classifyBlockConstruct(name string) string {
	switch name {
	case "if":
		return "if"
	case "unless":
		return "unless"
	case "each":
		return "each"
	case "with":
		return "with"
	default:
		return "helper"
	}
}

// VisitPartial covers all four partial forms (static name, dynamic
// (lookup .), inline {{#*inline}}, partial-block {{#> name}}) — they all
// arrive as *ast.PartialStatement.
func (v *capVisitor) VisitPartial(node *ast.PartialStatement) interface{} {
	if v.err != nil {
		return nil
	}
	if v.simple || !v.opts.Capabilities.Partials {
		v.reject("partial", node.Loc)
		return nil
	}
	v.constructs["partial"] = struct{}{}
	return nil
}

func (v *capVisitor) VisitContent(*ast.ContentStatement) interface{} { return nil }
func (v *capVisitor) VisitComment(*ast.CommentStatement) interface{} { return nil }

// The remaining Visitor methods are unused — capability classification is
// done at statement level via the four Visit*Statement methods above. They
// exist only to satisfy the ast.Visitor interface.
func (v *capVisitor) VisitExpression(*ast.Expression) interface{}     { return nil }
func (v *capVisitor) VisitSubExpression(*ast.SubExpression) interface{} {
	return nil
}
func (v *capVisitor) VisitPath(*ast.PathExpression) interface{} { return nil }
func (v *capVisitor) VisitString(*ast.StringLiteral) interface{} {
	return nil
}
func (v *capVisitor) VisitBoolean(*ast.BooleanLiteral) interface{} {
	return nil
}
func (v *capVisitor) VisitNumber(*ast.NumberLiteral) interface{} { return nil }
func (v *capVisitor) VisitHash(*ast.Hash) interface{}            { return nil }
func (v *capVisitor) VisitHashPair(*ast.HashPair) interface{}    { return nil }
