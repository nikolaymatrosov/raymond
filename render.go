package raymond

import (
	"io"

	"github.com/aymerick/raymond/ast"
)

// renderProgram walks a program body, streaming every statement
// (replaces VisitProgram's buffer, eval.go:790-813).
func (s *state) renderProgram(node *ast.Program) error {
	s.at(node)
	for _, n := range node.Body {
		if err := s.renderStatement(n); err != nil {
			return err
		}
	}
	return nil
}

// renderProgramWith is the evalProgram port (eval.go:250-293): block
// params, optional context push, optional frame swap, then render.
func (s *state) renderProgramWith(program *ast.Program, ctx Value, data *DataFrame, key interface{}) error {
	blockParams := make(map[string]Value)

	if len(program.BlockParams) > 0 {
		blockParams[program.BlockParams[0]] = ctx
	}
	if (len(program.BlockParams) > 1) && (key != nil) {
		blockParams[program.BlockParams[1]] = adaptValue(key)
	}

	if len(blockParams) > 0 {
		s.pushBlockParams(blockParams)
	}
	if ctx.IsValid() {
		s.pushCtx(ctx)
	}
	if data != nil {
		s.setDataFrame(data)
	}

	err := s.renderProgram(program)

	if data != nil {
		s.popDataFrame()
	}
	if ctx.IsValid() {
		s.popCtx()
	}
	if len(blockParams) > 0 {
		s.popBlockParams()
	}
	return err
}

// renderStatement dispatches one statement node.
func (s *state) renderStatement(node ast.Node) error {
	if err := s.step(1); err != nil {
		return err
	}
	switch n := node.(type) {
	case *ast.ContentStatement:
		s.at(n)
		if n.Value == "" {
			return nil
		}
		if err := s.writeSteps(len(n.Value)); err != nil {
			return err
		}
		_, err := io.WriteString(s.w, n.Value)
		return err
	case *ast.MustacheStatement:
		return s.renderMustache(n)
	case *ast.BlockStatement:
		return s.renderBlock(n)
	case *ast.PartialStatement:
		return s.renderPartial(n)
	case *ast.CommentStatement:
		s.at(n)
		return nil
	}
	return nil
}

// renderMustache ports VisitMustache (eval.go:816-833), streaming.
func (s *state) renderMustache(node *ast.MustacheStatement) error {
	s.at(node)

	s.subs++
	if s.limits.MaxSubstitutions > 0 && s.subs > s.limits.MaxSubstitutions {
		return newLimitError("substitutions", s.limits.MaxSubstitutions, ErrSubstitutionLimit)
	}

	pos := posMustache
	if node.Unescaped {
		pos = posRawMustache
	}
	val, err := s.evalExpression(node.Expression, pos)
	if err != nil {
		return err
	}
	if !val.IsValid() {
		return nil
	}

	str := val.Str()
	if str == "" {
		return nil
	}
	if err := s.writeSteps(len(str)); err != nil {
		return err
	}
	if val.Kind() == KindSafeString || node.Unescaped {
		_, werr := io.WriteString(s.w, str)
		return werr
	}
	return escape(stringWriterFor(s.w), str)
}

// renderBlock ports VisitBlock (eval.go:836-882), streaming.
func (s *state) renderBlock(node *ast.BlockStatement) error {
	s.at(node)
	s.pushBlock(node)
	defer s.popBlock()

	expr, err := s.evalExpression(node.Expression, posBlock)
	if err != nil {
		return err
	}

	if s.isHelperCall(node.Expression) || s.exprFunc[node.Expression] {
		// helper/lambda owns the block; its returned value is the
		// block output, written raw (VisitProgram wrote Str(result)
		// unescaped)
		if str := expr.Str(); str != "" {
			if err := s.writeSteps(len(str)); err != nil {
				return err
			}
			if _, werr := io.WriteString(s.w, str); werr != nil {
				return werr
			}
		}
		return nil
	}

	if expr.Truthy() {
		if node.Program == nil {
			return nil
		}
		if expr.Kind() == KindList {
			// array context: per-element iteration frame (eval.go:855-868)
			l := expr.list
			for i := 0; i < l.Len(); i++ {
				if err := s.step(1); err != nil {
					return err
				}
				frame := s.frame.newIterDataFrame(l.Len(), i, nil)
				if err := s.renderProgramWith(node.Program, l.Index(i), frame, i); err != nil {
					return err
				}
			}
			return nil
		}
		return s.renderProgramWith(node.Program, expr, nil, nil)
	}
	if node.Inverse != nil {
		return s.renderProgram(node.Inverse)
	}
	return nil
}

// renderPartial ports VisitPartial + evalPartial + partialContext
// (eval.go:692-750, 885-906), streaming through an indentWriter.
func (s *state) renderPartial(node *ast.PartialStatement) error {
	s.at(node)

	name, ok := ast.HelperNameStr(node.Name)
	if !ok {
		if subExpr, isSub := node.Name.(*ast.SubExpression); isSub {
			v, err := s.evalExpression(subExpr.Expression, posSubExpr)
			if err != nil {
				return err
			}
			name = v.Str()
		}
	}
	if name == "" {
		return s.errorf("Unexpected partial name: %q", node.Name)
	}

	program, err := s.partials(name)
	if err != nil {
		return err
	}
	if program == nil {
		return s.errorf("Partial not found: %s", name)
	}

	// partial context (eval.go:704-723)
	if nb := len(node.Params); nb > 1 {
		return s.errorf("Unsupported number of partial arguments: %d", nb)
	}
	if (len(node.Params) > 0) && (node.Hash != nil) {
		return s.errorf("Passing both context and named parameters to a partial is not allowed")
	}

	ctx := Value{}
	if len(node.Params) == 1 {
		ctx, err = s.evalParam(node.Params[0])
		if err != nil {
			return err
		}
	} else if node.Hash != nil {
		hash, raws, herr := s.evalHash(node.Hash)
		if herr != nil {
			return herr
		}
		ctx = mapValue(valueMap(hash), len(hash) > 0, raws)
	}

	if ctx.IsValid() {
		s.pushCtx(ctx)
		defer s.popCtx()
	}

	if node.Indent != "" {
		old := s.w
		s.w = newIndentWriter(s.w, node.Indent)
		err = s.renderProgram(program)
		s.w = old
		return err
	}
	return s.renderProgram(program)
}

// evalExpression ports VisitExpression (eval.go:927-968).
func (s *state) evalExpression(node *ast.Expression, pos callPosition) (Value, error) {
	s.at(node)
	if err := s.step(1); err != nil {
		return Value{}, err
	}

	s.pushExpr(node)
	defer s.popExpr()

	// helper call
	if helperName := node.HelperName(); helperName != "" {
		if helper := s.findHelper(helperName); helper != nil {
			return s.callHelper(helperName, helper, node, pos)
		}
	}

	// literal-as-field
	if literal, ok := node.LiteralStr(); ok {
		val, err := s.lookupField(s.curCtx(), literal, true)
		if err != nil {
			return Value{}, err
		}
		if val.IsValid() {
			return val, nil
		}
	}

	// field path
	if path := node.FieldPath(); path != nil {
		val, err := s.evalPathExpression(path, true)
		if err != nil {
			return Value{}, err
		}
		if val.IsValid() {
			return val, nil
		}
	}

	return Value{}, nil
}

// evalParam evaluates a param/hash-value node (the old engine's
// Accept dispatch for params).
func (s *state) evalParam(node ast.Node) (Value, error) {
	switch n := node.(type) {
	case *ast.SubExpression:
		s.at(n)
		return s.evalExpression(n.Expression, posSubExpr)
	case *ast.PathExpression:
		return s.evalPathExpression(n, false)
	case *ast.StringLiteral:
		s.at(n)
		return stringValue(n.Value, false), nil
	case *ast.BooleanLiteral:
		s.at(n)
		return boolValue(n.Value), nil
	case *ast.NumberLiteral:
		s.at(n)
		// Number() returns int or float64 (ast/node.go)
		return adaptValue(n.Number()), nil
	}
	return Value{}, nil
}

// evalHash ports VisitHash (eval.go:1008-1020): nil-valued pairs are
// skipped. Returns both Value map and raw map.
func (s *state) evalHash(node *ast.Hash) (map[string]Value, map[string]interface{}, error) {
	s.at(node)
	values := make(map[string]Value)
	raws := make(map[string]interface{})
	for _, pair := range node.Pairs {
		s.at(pair)
		v, err := s.evalParam(pair.Val)
		if err != nil {
			return nil, nil, err
		}
		if v.IsValid() && v.Interface() != nil {
			values[pair.Key] = v
			raws[pair.Key] = v.Interface()
		}
	}
	return values, raws, nil
}

// isHelperCall ports eval.go:575-580.
func (s *state) isHelperCall(node *ast.Expression) bool {
	if helperName := node.HelperName(); helperName != "" {
		return s.findHelper(helperName) != nil
	}
	return false
}

// findHelper resolves a helper through the seam installed by the
// driver (template or Compiled).
func (s *state) findHelper(name string) coreHelper {
	if s.helpers == nil {
		return nil
	}
	return s.helpers(name)
}

// callHelper evaluates params/hash and dispatches to the helper.
func (s *state) callHelper(name string, helper coreHelper, node *ast.Expression, pos callPosition) (Value, error) {
	var params []Value
	for _, paramNode := range node.Params {
		p, err := s.evalParam(paramNode)
		if err != nil {
			return Value{}, err
		}
		params = append(params, p)
	}

	var hash map[string]Value
	if node.Hash != nil {
		var err error
		hash, _, err = s.evalHash(node.Hash)
		if err != nil {
			return Value{}, err
		}
	}

	hc := &HelperCall{s: s, name: name, expr: node, params: params, hash: hash, pos: pos}
	return helper.callCore(hc)
}

// evalPathExpression: block param > @root context-then-data > data >
// context (eval.go:437-482).
func (s *state) evalPathExpression(node *ast.PathExpression, exprRoot bool) (Value, error) {
	if len(node.Parts) > 0 {
		if bp, found := s.blockParam(node.Parts[0]); found {
			synthetic := mapValue(valueMap{node.Parts[0]: bp}, true,
				map[string]interface{}{node.Parts[0]: bp.Interface()})
			s.pushCtx(synthetic)
			result, err := s.evalCtxPathExpression(node, exprRoot)
			s.popCtx()
			return result, err
		}
	}

	var result Value
	var err error
	ctxTried := false

	if node.IsDataRoot() {
		result, err = s.evalCtxPathExpression(node, exprRoot)
		if err != nil {
			return Value{}, err
		}
		ctxTried = true
	}

	if !result.IsValid() && node.Data {
		result, err = s.evalDataPathExpression(node, exprRoot)
		if err != nil {
			return Value{}, err
		}
	}

	if !result.IsValid() && !ctxTried {
		result, err = s.evalCtxPathExpression(node, exprRoot)
		if err != nil {
			return Value{}, err
		}
	}

	return result, nil
}

// evalDataPathExpression (eval.go:485-499): walk frame parents by
// Depth, then resolve parts against frame data.
func (s *state) evalDataPathExpression(node *ast.PathExpression, exprRoot bool) (Value, error) {
	frame := s.frame
	for i := node.Depth; i > 0; i-- {
		if frame.parent == nil {
			return Value{}, nil
		}
		frame = frame.parent
	}
	result, _, err := s.evalCtxPath(adaptValue(frame.data), node.Parts, exprRoot)
	return result, err
}

// evalCtxPathExpression (eval.go:502-514).
func (s *state) evalCtxPathExpression(node *ast.PathExpression, exprRoot bool) (Value, error) {
	s.at(node)

	if node.IsDataRoot() {
		parts := node.Parts[1:]
		result, _, err := s.evalCtxPath(s.rootCtx(), parts, exprRoot)
		return result, err
	}
	return s.evalDepthPath(node.Depth, node.Parts, exprRoot)
}

// evalDepthPath (eval.go:517-537): parent fallback gated by
// partResolved ("Dotted Names - Context Precedence").
func (s *state) evalDepthPath(depth int, parts []string, exprRoot bool) (Value, error) {
	var result Value
	partResolved := false

	ctx := s.ancestorCtx(depth)

	for !result.IsValid() && ctx.IsValid() && (depth <= len(s.ctxStack) && !partResolved) {
		var err error
		result, partResolved, err = s.evalCtxPath(ctx, parts, exprRoot)
		if err != nil {
			return Value{}, err
		}
		if !partResolved && !result.IsValid() {
			depth++
			ctx = s.ancestorCtx(depth)
		}
	}
	return result, nil
}

// evalCtxPath (eval.go:540-568): array contexts map the path over
// elements and ALWAYS return a valid (possibly empty) list, which
// terminates the depth walk exactly like the old `result = results`.
func (s *state) evalCtxPath(ctx Value, parts []string, exprRoot bool) (Value, bool, error) {
	if ctx.Kind() == KindList {
		var values []Value
		var raws []interface{}
		for i := 0; i < ctx.list.Len(); i++ {
			v, _, err := s.resolveParts(ctx.list.Index(i), parts, exprRoot)
			if err != nil {
				return Value{}, false, err
			}
			if v.IsValid() {
				values = append(values, v)
				raws = append(raws, v.Interface())
			}
		}
		return listValue(sliceList(values), len(values) > 0, raws), false, nil
	}

	v, partResolved, err := s.resolveParts(ctx, parts, exprRoot)
	return v, partResolved, err
}

// resolveParts ports evalPath (eval.go:296-317): bracket-stripping and
// per-part field resolution with lambda invocation interleaved.
func (s *state) resolveParts(ctx Value, parts []string, exprRoot bool) (Value, bool, error) {
	partResolved := false
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if (len(part) >= 2) && (part[0] == '[') && (part[len(part)-1] == ']') {
			part = part[1 : len(part)-1]
		}
		var err error
		ctx, err = s.lookupField(ctx, part, exprRoot)
		if err != nil {
			return Value{}, partResolved, err
		}
		if !ctx.IsValid() {
			break
		}
		partResolved = true
	}
	return ctx, partResolved, nil
}

// lookupField resolves one name in a container and applies the old
// engine's func-invocation topology: any func result is invoked once;
// a METHOD's func result is invoked once more if still a func.
func (s *state) lookupField(ctx Value, name string, exprRoot bool) (Value, error) {
	if err := s.step(1); err != nil {
		return Value{}, err
	}
	if !ctx.IsValid() {
		return Value{}, nil
	}
	if ctx.data == nil {
		// scalars/funcs as contexts resolve nothing
		return Value{}, nil
	}

	res, _ := ctx.data.Lookup(name)

	if res.Kind() == KindFunc {
		fromMethod := res.fromMethod
		out, err := s.invokeFunc(res, exprRoot)
		if err != nil {
			return Value{}, err
		}
		if fromMethod && out.Kind() == KindFunc {
			out, err = s.invokeFunc(out, exprRoot)
			if err != nil {
				return Value{}, err
			}
		}
		return out, nil
	}
	return res, nil
}

// invokeFunc ports evalFieldFunc (eval.go:387-405): at expression root
// the lambda receives the full params/hash and the expression is
// memoized as a function call; elsewhere it gets empty options.
func (s *state) invokeFunc(fnVal Value, exprRoot bool) (Value, error) {
	var opts *Options
	if exprRoot {
		expr := s.curExpr()
		var err error
		opts, err = s.helperOptions(expr)
		if err != nil {
			return Value{}, err
		}
		s.exprFunc[expr] = true
	} else {
		opts = &Options{s: s, hash: make(map[string]interface{})}
	}
	return fnVal.fn.call(s, opts)
}

// helperOptions ports eval.go:672-686 for lambda invocation.
func (s *state) helperOptions(node *ast.Expression) (*Options, error) {
	var params []interface{}
	for _, paramNode := range node.Params {
		p, err := s.evalParam(paramNode)
		if err != nil {
			return nil, err
		}
		params = append(params, p.Interface())
	}

	var hash map[string]interface{}
	if node.Hash != nil {
		var err error
		_, hash, err = s.evalHash(node.Hash)
		if err != nil {
			return nil, err
		}
	}

	return &Options{s: s, params: params, hash: hash}, nil
}
