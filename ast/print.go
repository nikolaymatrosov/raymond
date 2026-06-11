package ast

import (
	"fmt"
	"strings"
)

// printVisitor implements the Visitor interface to print a AST.
type printVisitor struct {
	buf   string
	depth int

	original bool
	inBlock  bool
}

func newPrintVisitor() *printVisitor {
	return &printVisitor{}
}

// Print returns a string representation of given AST, that can be used for debugging purpose.
func Print(node Node) string {
	visitor := newPrintVisitor()
	node.Accept(visitor)
	return visitor.output()
}

func (v *printVisitor) output() string {
	return v.buf
}

func (v *printVisitor) indent() {
	for i := 0; i < v.depth; {
		v.buf += "  "
		i++
	}
}

func (v *printVisitor) str(val string) {
	v.buf += val
}

func (v *printVisitor) nl() {
	v.str("\n")
}

func (v *printVisitor) line(val string) {
	v.indent()
	v.str(val)
	v.nl()
}

//
// Visitor interface
//

// Statements

// VisitProgram implements corresponding Visitor interface method
func (v *printVisitor) VisitProgram(node *Program) any {
	if len(node.BlockParams) > 0 {
		v.line("BLOCK PARAMS: [ " + strings.Join(node.BlockParams, " ") + " ]")
	}

	for _, n := range node.Body {
		n.Accept(v)
	}

	return nil
}

// VisitMustache implements corresponding Visitor interface method
func (v *printVisitor) VisitMustache(node *MustacheStatement) any {
	v.indent()
	v.str("{{ ")

	node.Expression.Accept(v)

	v.str(" }}")
	v.nl()

	return nil
}

// VisitBlock implements corresponding Visitor interface method
func (v *printVisitor) VisitBlock(node *BlockStatement) any {
	v.inBlock = true

	v.line("BLOCK:")
	v.depth++

	node.Expression.Accept(v)

	if node.Program != nil {
		v.line("PROGRAM:")
		v.depth++
		node.Program.Accept(v)
		v.depth--
	}

	if node.Inverse != nil {
		// if node.Program != nil {
		// 	v.depth++
		// }

		v.line("{{^}}")
		v.depth++
		node.Inverse.Accept(v)
		v.depth--

		// if node.Program != nil {
		// 	v.depth--
		// }
	}

	v.inBlock = false

	return nil
}

// VisitPartial implements corresponding Visitor interface method
func (v *printVisitor) VisitPartial(node *PartialStatement) any {
	v.indent()
	v.str("{{> PARTIAL:")

	v.original = true
	node.Name.Accept(v)
	v.original = false

	if len(node.Params) > 0 {
		v.str(" ")
		node.Params[0].Accept(v)
	}

	// hash
	if node.Hash != nil {
		v.str(" ")
		node.Hash.Accept(v)
	}

	v.str(" }}")
	v.nl()

	return nil
}

// VisitContent implements corresponding Visitor interface method
func (v *printVisitor) VisitContent(node *ContentStatement) any {
	v.line("CONTENT[ '" + node.Value + "' ]")

	return nil
}

// VisitComment implements corresponding Visitor interface method
func (v *printVisitor) VisitComment(node *CommentStatement) any {
	v.line("{{! '" + node.Value + "' }}")

	return nil
}

// Expressions

// VisitExpression implements corresponding Visitor interface method
func (v *printVisitor) VisitExpression(node *Expression) any {
	if v.inBlock {
		v.indent()
	}

	// path
	node.Path.Accept(v)

	// params
	v.str(" [")
	for i, n := range node.Params {
		if i > 0 {
			v.str(", ")
		}
		n.Accept(v)
	}
	v.str("]")

	// hash
	if node.Hash != nil {
		v.str(" ")
		node.Hash.Accept(v)
	}

	if v.inBlock {
		v.nl()
	}

	return nil
}

// VisitSubExpression implements corresponding Visitor interface method
func (v *printVisitor) VisitSubExpression(node *SubExpression) any {
	node.Expression.Accept(v)

	return nil
}

// VisitPath implements corresponding Visitor interface method
func (v *printVisitor) VisitPath(node *PathExpression) any {
	if v.original {
		v.str(node.Original)
	} else {
		path := strings.Join(node.Parts, "/")

		result := ""
		if node.Data {
			result += "@"
		}

		v.str(result + "PATH:" + path)
	}

	return nil
}

// Literals

// VisitString implements corresponding Visitor interface method
func (v *printVisitor) VisitString(node *StringLiteral) any {
	if v.original {
		v.str(node.Value)
	} else {
		v.str("\"" + node.Value + "\"")
	}

	return nil
}

// VisitBoolean implements corresponding Visitor interface method
func (v *printVisitor) VisitBoolean(node *BooleanLiteral) any {
	if v.original {
		v.str(node.Original)
	} else {
		v.str(fmt.Sprintf("BOOLEAN{%s}", node.Canonical()))
	}

	return nil
}

// VisitNumber implements corresponding Visitor interface method
func (v *printVisitor) VisitNumber(node *NumberLiteral) any {
	if v.original {
		v.str(node.Original)
	} else {
		v.str(fmt.Sprintf("NUMBER{%s}", node.Canonical()))
	}

	return nil
}

// Miscellaneous

// VisitHash implements corresponding Visitor interface method
func (v *printVisitor) VisitHash(node *Hash) any {
	v.str("HASH{")

	for i, p := range node.Pairs {
		if i > 0 {
			v.str(", ")
		}
		p.Accept(v)
	}

	v.str("}")

	return nil
}

// VisitHashPair implements corresponding Visitor interface method
func (v *printVisitor) VisitHashPair(node *HashPair) any {
	v.str(node.Key + "=")
	node.Val.Accept(v)

	return nil
}
