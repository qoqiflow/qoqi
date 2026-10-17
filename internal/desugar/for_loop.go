package desugar

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// desugarForStmt transforms a for-in loop into a while loop
//
// Before:
//
//	for item in collection {
//	  body
//	}
//
// After (for arrays):
//
//	{
//	  let __iter_N = 0;
//	  let __len_N = __len(collection);
//	  while __iter_N < __len_N {
//	    let item = collection[__iter_N];
//	    __iter_N = __iter_N + 1;
//	    body
//	  }
//	}
func (d *Desugarer) desugarForStmt(forStmt *ast.ForStmt) ast.Stmt {
	pos := forStmt.Position()

	// A loop over numbers counts, and makes no array
	if numbers, ok := forStmt.Iterable.(*ast.RangeExpr); ok {
		return d.desugarRangeLoop(forStmt, numbers)
	}

	// A loop over a range kept as a value counts as well
	if kept, ok := d.typeInfo.Types[forStmt.Iterable].(*types.StructType); ok && kept.Name == rangeStruct {
		return d.desugarKeptRangeLoop(forStmt, kept)
	}

	// A loop over a struct goes through the array the struct gives
	forStmt = d.iterated(forStmt)

	// Validate that the iterable is actually iterable
	if !d.validateForLoopIterable(forStmt) {
		// Return the original statement on error - compilation will fail anyway
		return forStmt
	}

	// Desugar the iterable expression first
	iterableExpr := d.desugarExpr(forStmt.Iterable)

	// Generate unique variable names
	iterVar := d.generateIterVar()
	lenVar := d.generateLenVar()

	// Evaluate the iterable only once: anything but a plain variable is
	// stored in a temporary first.
	var srcInit ast.Stmt
	if _, isIdent := iterableExpr.(*ast.Ident); !isIdent {
		srcVar := d.generateTmpVar()
		srcInit = &ast.LetStmt{
			BaseNode: ast.BaseNode{Pos: pos},
			Name:     srcVar,
			Value:    iterableExpr,
		}
		srcIdent := d.createIdent(srcVar, pos)
		if t, ok := d.typeInfo.Types[forStmt.Iterable]; ok {
			d.typeInfo.Types[srcIdent] = t
		}
		iterableExpr = srcIdent
	}

	// Create iterator initialization: let __iter_N = 0;
	iterInit := &ast.LetStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     iterVar,
		Type: &ast.Type{
			BaseNode: ast.BaseNode{Pos: pos},
			Kind:     ast.TypeBasic,
			Name:     "Int",
		},
		Value:   d.createIntLiteral("0", pos),
		IsConst: false,
	}

	// Register the type for the iterator variable
	iterIdent := d.createIdent(iterVar, pos)
	d.typeInfo.Types[iterIdent] = types.Int

	// Create length variable: let __len_N = __len(collection);
	lengthExpr := &ast.CallExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Func:     d.createIdent(resolve.LengthFunction, pos),
		Args:     []ast.Expr{iterableExpr},
	}
	d.typeInfo.Types[lengthExpr] = types.Int
	lenInit := &ast.LetStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     lenVar,
		Type: &ast.Type{
			BaseNode: ast.BaseNode{Pos: pos},
			Kind:     ast.TypeBasic,
			Name:     "Int",
		},
		Value:   lengthExpr,
		IsConst: false,
	}

	// Register the type for the length variable
	lenIdent := d.createIdent(lenVar, pos)
	d.typeInfo.Types[lenIdent] = types.Int

	// Create while condition: __iter_N < __len_N
	iterVarExpr := d.createIdent(iterVar, pos)
	lenVarExpr := d.createIdent(lenVar, pos)
	d.typeInfo.Types[iterVarExpr] = types.Int
	d.typeInfo.Types[lenVarExpr] = types.Int

	whileCond := d.createBinaryExpr(iterVarExpr, "<", lenVarExpr, pos)
	d.typeInfo.Types[whileCond] = types.Bool

	// Create loop variable initialization: let item = collection[__iter_N];
	iterVarForIndex := d.createIdent(iterVar, pos)
	d.typeInfo.Types[iterVarForIndex] = types.Int

	indexExpr := d.createIndexExpr(iterableExpr, iterVarForIndex, pos)

	// Determine the type of the loop variable from the iterable type
	var loopVarType *ast.Type
	if iterableType, ok := d.typeInfo.Types[forStmt.Iterable]; ok {
		switch t := iterableType.(type) {
		case *types.ArrayType:
			// For arrays, the loop variable type is the element type
			loopVarType = d.typeToASTType(t.ElementType, pos)
			d.typeInfo.Types[indexExpr] = t.ElementType
		case *types.PrimitiveType:
			if t.Name == types.PrimitiveString {
				// For strings, the loop variable type is String
				loopVarType = &ast.Type{
					BaseNode: ast.BaseNode{Pos: pos},
					Kind:     ast.TypeBasic,
					Name:     "String",
				}
				d.typeInfo.Types[indexExpr] = types.String
			}
		}
	}

	// When the element type is not known the declaration gets no annotation
	// and takes the type of the element expression when it is checked again.

	loopVarInit := &ast.LetStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     forStmt.Var,
		Type:     loopVarType,
		Value:    indexExpr,
		IsConst:  false,
	}

	// Desugar the loop body
	desugaredBody := d.desugarStmt(forStmt.Body).(*ast.BlockStmt)

	// Create increment statement: __iter_N = __iter_N + 1;
	iterVarForIncr := d.createIdent(iterVar, pos)
	iterVarForAdd := d.createIdent(iterVar, pos)
	d.typeInfo.Types[iterVarForIncr] = types.Int
	d.typeInfo.Types[iterVarForAdd] = types.Int

	incrExpr := d.createBinaryExpr(
		iterVarForAdd,
		"+",
		d.createIntLiteral("1", pos),
		pos,
	)
	d.typeInfo.Types[incrExpr] = types.Int

	incrStmt := &ast.ExprStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Expr: &ast.BinaryExpr{
			BaseNode: ast.BaseNode{Pos: pos},
			Left:     iterVarForIncr,
			Op:       "=",
			Right:    incrExpr,
		},
	}

	// Build the while body: { let item = ...; __iter_N = __iter_N + 1; <original body> }
	// The increment comes before the body so that `continue` cannot skip it.
	whileBodyStmts := make([]ast.Stmt, 0, len(desugaredBody.Stmts)+3)
	whileBodyStmts = append(whileBodyStmts, loopVarInit)
	if forStmt.Index != nil {
		// for i, item in ...: i is a copy of the position, taken before
		// the increment. The type checker rejects assignments to it.
		position := d.createIdent(iterVar, pos)
		d.typeInfo.Types[position] = types.Int
		whileBodyStmts = append(whileBodyStmts, &ast.LetStmt{
			BaseNode: forStmt.Index.BaseNode,
			Name:     forStmt.Index.Name,
			Type:     &ast.Type{BaseNode: ast.BaseNode{Pos: pos}, Kind: ast.TypeBasic, Name: "Int"},
			Value:    position,
		})
	}
	whileBodyStmts = append(whileBodyStmts, incrStmt)
	whileBodyStmts = append(whileBodyStmts, desugaredBody.Stmts...)

	whileBody := &ast.BlockStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Stmts:    whileBodyStmts,
	}

	// Create the while statement
	whileStmt := &ast.WhileStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Cond:     whileCond,
		Body:     whileBody,
	}

	// Wrap everything in a block
	blockStmts := []ast.Stmt{iterInit, lenInit, whileStmt}
	if srcInit != nil {
		blockStmts = append([]ast.Stmt{srcInit}, blockStmts...)
	}
	block := d.wrapInBlock(blockStmts, pos)

	// Record the mapping
	d.info.Mapping[forStmt] = block

	return block
}

// desugarRangeLoop transforms a loop over a range into a while loop that
// counts:
//
//	for i in low..high {
//	  body
//	}
//
// becomes
//
//	{
//	  let i: Int = low;
//	  let __len_N: Int = high;
//	  while i < __len_N { body } update { i = i + 1; }
//	}
//
// The update runs after each iteration, one ended by `continue` included.
// Both ends are worked out once, before the first iteration.
//
// A loop that includes its end tests for the last number before counting
// past it, so that it can end at the largest Int:
//
//	{
//	  let i: Int = low;
//	  let __len_N: Int = high;
//	  if i <= __len_N {
//	    while { body } update { if i == __len_N { break; } i = i + 1; }
//	  }
//	}
//
// The loop counts with i itself when the body cannot tell the two apart:
// when no function made in it, and no deferred statement, reads i. Other
// loops count with a number of their own, __iter_N, and the body starts
// with `let i: Int = __iter_N;`, an i for each iteration. So does a loop
// with a position, `for n, i in low..high`, where n is how far i is from
// where it began.
func (d *Desugarer) desugarRangeLoop(forStmt *ast.ForStmt, numbers *ast.RangeExpr) ast.Stmt {
	pos := forStmt.Position()
	at := ast.BaseNode{Pos: pos}
	intType := func() *ast.Type { return &ast.Type{BaseNode: at, Kind: ast.TypeBasic, Name: "Int"} }
	assign := func(name string, value ast.Expr) ast.Stmt {
		return &ast.ExprStmt{BaseNode: at, Expr: &ast.BinaryExpr{BaseNode: at, Left: d.createIdent(name, pos), Op: "=", Right: value}}
	}

	body := d.desugarBlockStmt(forStmt.Body)
	counter := forStmt.Var
	ownCounter := forStmt.Index != nil || changesOrKeeps(body, forStmt.Var)
	if ownCounter {
		counter = d.generateIterVar()
	}
	endVar := d.generateLenVar()
	count := func() ast.Expr { return d.createIdent(counter, pos) }
	end := func() ast.Expr { return d.createIdent(endVar, pos) }

	stmts := []ast.Stmt{
		&ast.LetStmt{BaseNode: at, Name: counter, Type: intType(), Value: d.desugarExpr(numbers.Low)},
		&ast.LetStmt{BaseNode: at, Name: endVar, Type: intType(), Value: d.desugarExpr(numbers.High)},
	}
	if ownCounter {
		head := []ast.Stmt{&ast.LetStmt{BaseNode: at, Name: forStmt.Var, Type: intType(), Value: count()}}
		if forStmt.Index != nil {
			startVar := d.generateTmpVar()
			stmts = append(stmts, &ast.LetStmt{BaseNode: at, Name: startVar, Type: intType(), Value: count()})
			head = append(head, &ast.LetStmt{
				BaseNode: forStmt.Index.BaseNode,
				Name:     forStmt.Index.Name,
				Type:     intType(),
				Value:    d.createBinaryExpr(count(), "-", d.createIdent(startVar, pos), pos),
			})
		}
		body = &ast.BlockStmt{BaseNode: body.BaseNode, Stmts: append(head, body.Stmts...)}
	}
	step := assign(counter, d.createBinaryExpr(count(), "+", d.createIntLiteral("1", pos), pos))

	if numbers.Inclusive {
		last := &ast.IfStmt{
			BaseNode: at,
			Cond:     d.createBinaryExpr(count(), "==", end(), pos),
			Then:     &ast.BlockStmt{BaseNode: at, Stmts: []ast.Stmt{&ast.BreakStmt{BaseNode: at}}},
		}
		loop := &ast.WhileStmt{BaseNode: at, Body: body, Update: &ast.BlockStmt{BaseNode: at, Stmts: []ast.Stmt{last, step}}}
		stmts = append(stmts, &ast.IfStmt{
			BaseNode: at,
			Cond:     d.createBinaryExpr(count(), "<=", end(), pos),
			Then:     &ast.BlockStmt{BaseNode: at, Stmts: []ast.Stmt{loop}},
		})
	} else {
		stmts = append(stmts, &ast.WhileStmt{BaseNode: at, Cond: d.createBinaryExpr(count(), "<", end(), pos), Body: body, Update: step})
	}

	block := d.wrapInBlock(stmts, pos)
	d.info.Mapping[forStmt] = block
	return block
}

// changesOrKeeps reports whether a loop body may assign to the variable
// of that name, or hand it to a function made inside it, which would keep
// it beyond the iteration. A deferred statement counts too. It looks at
// names only, so a variable of the same name declared inside counts as
// well: a no is certain, a yes may be too careful.
func changesOrKeeps(body *ast.BlockStmt, name string) bool {
	found := false
	mentions := func(node ast.Node) bool {
		seen := false
		ast.Walk(node, func(e ast.Expr) {
			if id, ok := e.(*ast.Ident); ok && id.Name == name {
				seen = true
			}
		}, func(ast.Stmt) {})
		return seen
	}
	ast.Walk(body, func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.BinaryExpr:
			if id, ok := e.Left.(*ast.Ident); ok && id.Name == name && ast.IsAssignment(e.Op) {
				found = true
			}
		case *ast.FnExpr:
			if !found && mentions(e) {
				found = true
			}
		}
	}, func(s ast.Stmt) {
		if deferred, ok := s.(*ast.DeferStmt); ok && !found && mentions(deferred) {
			found = true
		}
	})
	return found
}

// typeToASTType converts a types.Type to an ast.Type. It returns nil for a
// type that has no source form or is not known; a declaration without an
// annotation then takes the type of its initializer.
func (d *Desugarer) typeToASTType(t types.Type, pos token.Position) *ast.Type {
	basic := func(name string) *ast.Type {
		return &ast.Type{BaseNode: ast.BaseNode{Pos: pos}, Kind: ast.TypeBasic, Name: name}
	}

	switch t := t.(type) {
	case *types.PrimitiveType:
		if t.Name == types.PrimitiveUnknown {
			return nil
		}
		// Int and not int: the name the program is checked again with
		if written := types.TypeToAST(t, ast.BaseNode{Pos: pos}); written != nil {
			return written
		}
		return basic(string(t.Name))
	case *types.ArrayType:
		elemType := d.typeToASTType(t.ElementType, pos)
		if elemType == nil {
			return nil
		}
		return &ast.Type{
			BaseNode: ast.BaseNode{Pos: pos},
			Kind:     ast.TypeArray,
			ElemType: elemType,
		}
	case *types.StructType:
		result := basic(t.Name)
		for _, arg := range t.TypeArgs {
			argType := d.typeToASTType(arg, pos)
			if argType == nil {
				return nil
			}
			result.Generics = append(result.Generics, argType)
		}
		return result
	case *types.BehaviorType:
		return basic(t.Name)
	case *types.TypeParam:
		return basic(t.Name)
	case *types.FunctionType:
		paramTypes := make([]*ast.Type, len(t.Params))
		for i, param := range t.Params {
			if paramTypes[i] = d.typeToASTType(param, pos); paramTypes[i] == nil {
				return nil
			}
		}
		returnType := d.typeToASTType(t.ReturnType, pos)
		if returnType == nil || t.IsVariadic {
			return nil
		}
		return &ast.Type{
			BaseNode:   ast.BaseNode{Pos: pos},
			Kind:       ast.TypeFunction,
			ParamTypes: paramTypes,
			ReturnType: returnType,
		}
	case *types.UnionType:
		unionTypes := make([]*ast.Type, len(t.Types))
		for i, ut := range t.Types {
			if unionTypes[i] = d.typeToASTType(ut, pos); unionTypes[i] == nil {
				return nil
			}
		}
		return &ast.Type{
			BaseNode:   ast.BaseNode{Pos: pos},
			Kind:       ast.TypeUnion,
			UnionTypes: unionTypes,
		}
	default:
		return nil
	}
}

// rangeStruct is what a range kept as a value is: the Range of the
// standard library (see modules.RangeStruct).
const rangeStruct = "std.ops.Range"

// desugarKeptRangeLoop transforms a loop over a Range, a range that was
// kept as a value, into a while loop that counts: no array is made.
//
//	for i in numbers { body }
//
//	{
//	  let __tmp_N = numbers;
//	  let __iter_N: Int = __tmp_N.from;
//	  let __len_N: Int = __tmp_N.stop();   // the first number past the end
//	  let __tmp_M: Int = __tmp_N.by;
//	  while __iter_N != __len_N {
//	    let i: Int = __iter_N;
//	    __iter_N = __iter_N + __tmp_M;
//	    body
//	  }
//	}
//
// The range is read once, before the first iteration: what the body does
// to it changes nothing of the loop. `stop` is a whole number of steps
// from `from`, so the loop meets it exactly, going up or down.
func (d *Desugarer) desugarKeptRangeLoop(forStmt *ast.ForStmt, kept *types.StructType) ast.Stmt {
	pos := forStmt.Position()
	at := ast.BaseNode{Pos: pos}
	intType := func() *ast.Type { return &ast.Type{BaseNode: at, Kind: ast.TypeBasic, Name: "Int"} }

	rangeVar, iterVar, stopVar, stepVar := d.generateTmpVar(), d.generateIterVar(), d.generateLenVar(), d.generateTmpVar()
	numbers := func() ast.Expr {
		ident := d.createIdent(rangeVar, pos)
		d.typeInfo.Types[ident] = kept
		return ident
	}
	iter := func() ast.Expr { return d.createIdent(iterVar, pos) }
	stop := d.call(numbers(), "stop", at)
	d.typeInfo.Types[stop] = types.Int

	stmts := []ast.Stmt{
		&ast.LetStmt{BaseNode: at, Name: rangeVar, Value: d.desugarExpr(forStmt.Iterable)},
		&ast.LetStmt{BaseNode: at, Name: iterVar, Type: intType(), Value: d.createMemberExpr(numbers(), "from", pos)},
		&ast.LetStmt{BaseNode: at, Name: stopVar, Type: intType(), Value: stop},
		&ast.LetStmt{BaseNode: at, Name: stepVar, Type: intType(), Value: d.createMemberExpr(numbers(), "by", pos)},
	}

	body := []ast.Stmt{&ast.LetStmt{BaseNode: at, Name: forStmt.Var, Type: intType(), Value: iter()}}
	if forStmt.Index != nil {
		// How many numbers came before this one
		countVar := d.generateTmpVar()
		stmts = append(stmts, &ast.LetStmt{BaseNode: at, Name: countVar, Type: intType(), Value: d.createIntLiteral("0", pos)})
		body = append(body,
			&ast.LetStmt{BaseNode: forStmt.Index.BaseNode, Name: forStmt.Index.Name, Type: intType(), Value: d.createIdent(countVar, pos)},
			&ast.ExprStmt{BaseNode: at, Expr: &ast.BinaryExpr{
				BaseNode: at,
				Left:     d.createIdent(countVar, pos),
				Op:       "=",
				Right:    d.createBinaryExpr(d.createIdent(countVar, pos), "+", d.createIntLiteral("1", pos), pos),
			}})
	}
	// The step comes before the body so that `continue` cannot skip it
	body = append(body, &ast.ExprStmt{BaseNode: at, Expr: &ast.BinaryExpr{
		BaseNode: at,
		Left:     iter(),
		Op:       "=",
		Right:    d.createBinaryExpr(iter(), "+", d.createIdent(stepVar, pos), pos),
	}})
	body = append(body, d.desugarBlockStmt(forStmt.Body).Stmts...)

	stmts = append(stmts, &ast.WhileStmt{
		BaseNode: at,
		Cond:     d.createBinaryExpr(iter(), "!=", d.createIdent(stopVar, pos), pos),
		Body:     &ast.BlockStmt{BaseNode: at, Stmts: body},
	})

	block := d.wrapInBlock(stmts, pos)
	d.info.Mapping[forStmt] = block
	return block
}
