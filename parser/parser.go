package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vvshulga/db_internals/lexer"
)

// AstNode represents a top-level statement
type AstNode interface{}

// SelectStmt: SELECT projections FROM table [WHERE selection] [GROUP BY columns] [LIMIT limit]
type SelectStmt struct {
	Projections []ProjectionItem // columns list or *
	From        TableRef         // 1 table
	Selection   Expr             // WHERE clause (optional)
	GroupBy     []string         // GROUP BY columns (optional)
	Limit       *uint64          // LIMIT (optional)
}

type ProjectionItem struct {
	All       bool
	Column    string
	Aggregate *AggregateFunctionCall // Non-nil if this is an aggregate function
}

type TableRef struct {
	Name string
}

// InsertStmt: INSERT INTO table VALUES (expr, ...)
type InsertStmt struct {
	TableName string
	Values    []Expr // single row of expressions
}

// CreateTableStmt: CREATE TABLE table (col1 type1, col2 type2, ...)
type CreateTableStmt struct {
	TableName string
	Columns   []ColumnDef
}

type ColumnDef struct {
	Name string
	Type string
}

// UpdateStmt: UPDATE table SET col1=expr1, col2=expr2 [WHERE selection] [LIMIT limit]
type UpdateStmt struct {
	TableName string
	SetItems  []SetItem // column assignments
	Selection Expr      // WHERE clause (optional)
	Limit     *uint64   // LIMIT (optional)
}

type SetItem struct {
	Column string
	Value  Expr // Reuse Expr types: LiteralInt, LiteralString, ColumnRef
}

// DeleteStmt: DELETE FROM table [WHERE selection] [LIMIT limit]
type DeleteStmt struct {
	TableName string
	Selection Expr    // WHERE clause (optional)
	Limit     *uint64 // LIMIT (optional)
}

// Expr represents expressions in WHERE clauses and VALUES
type Expr interface{}

type ColumnRef struct {
	Name string
}

type LiteralInt struct {
	Value uint64
}

type LiteralString struct {
	Value string
}

type BinaryOp struct {
	Left  Expr
	Op    string
	Right Expr
}

type LogicalOp struct {
	Left  Expr
	Op    string // AND, OR
	Right Expr
}

type ComparisonOp struct {
	Left  Expr
	Op    string
	Right Expr
}

// AggregateFunctionCall represents aggregate functions like COUNT, SUM, AVG, MIN, MAX
type AggregateFunctionCall struct {
	Function string // COUNT, SUM, AVG, MIN, MAX
	Argument Expr   // Column reference or * for COUNT(*)
	IsStar   bool   // True for COUNT(*)
}

// ParseString tokenizes and parses input into AST nodes
func ParseString(input string) ([]AstNode, error) {
	toks := lexer.Tokenize(input)
	p := &parser{tokens: toks}
	return p.parseStatements()
}

// internal parser
type parser struct {
	tokens []lexer.Token
	pos    int
}

func (p *parser) peek() *lexer.Token {
	if p.pos >= len(p.tokens) {
		return nil
	}
	return &p.tokens[p.pos]
}

func (p *parser) next() *lexer.Token {
	if p.pos >= len(p.tokens) {
		return nil
	}
	t := &p.tokens[p.pos]
	p.pos++
	return t
}

func (p *parser) consumeKeyword(name string) bool {
	t := p.peek()
	if t == nil {
		return false
	}
	if t.Type == lexer.TokenKeyword && strings.EqualFold(t.Value, name) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expectKeyword(name string) error {
	if p.consumeKeyword(name) {
		return nil
	}
	t := p.peek()
	if t == nil {
		return fmt.Errorf("expected keyword %s, got eof", name)
	}
	return fmt.Errorf("expected keyword %s, got %s", name, t.Value)
}

func (p *parser) parseStatements() ([]AstNode, error) {
	var out []AstNode
	for p.peek() != nil {
		// skip stray semicolons
		if p.peek().Type == lexer.TokenSeparator && p.peek().Value == ";" {
			p.next()
			continue
		}
		node, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		out = append(out, node)
		// optional trailing semicolon
		if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == ";" {
			p.next()
		}
	}
	return out, nil
}

func (p *parser) parseStatement() (AstNode, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("unexpected eof")
	}
	if t.Type == lexer.TokenKeyword {
		switch strings.ToUpper(t.Value) {
		case "SELECT":
			return p.parseSelect()
		case "INSERT":
			return p.parseInsert()
		case "CREATE":
			return p.parseCreateTable()
		case "UPDATE":
			return p.parseUpdate()
		case "DELETE":
			return p.parseDelete()
		}
	}
	return nil, fmt.Errorf("unsupported statement starting with %v", t.Value)
}

func (p *parser) parseSelect() (AstNode, error) {
	// consume SELECT
	p.next()
	proj := []ProjectionItem{}
	// projection list
	if p.peek() == nil {
		return nil, fmt.Errorf("unexpected eof after SELECT")
	}
	if p.peek().Type == lexer.TokenSeparator && p.peek().Value == "*" {
		p.next()
		proj = append(proj, ProjectionItem{All: true})
	} else {
		for {
			t := p.peek()
			if t == nil {
				return nil, fmt.Errorf("unexpected end of input in projection list")
			}

			// Check if this is an aggregate function
			if t.Type == lexer.TokenKeyword {
				funcName := strings.ToUpper(t.Value)
				if funcName == "COUNT" || funcName == "SUM" || funcName == "AVG" || funcName == "MIN" || funcName == "MAX" {
					aggCall, err := p.parseAggregateFunction()
					if err != nil {
						return nil, err
					}
					proj = append(proj, ProjectionItem{Aggregate: aggCall})
				} else {
					return nil, fmt.Errorf("unexpected keyword in projection: %s", t.Value)
				}
			} else if t.Type == lexer.TokenIdentifier {
				// Regular column reference
				proj = append(proj, ProjectionItem{Column: t.Value})
				p.next()
			} else {
				return nil, fmt.Errorf("expected projection identifier or aggregate function, got %v", t)
			}

			if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "," {
				p.next()
				continue
			}
			break
		}
	}
	// FROM
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	// table
	if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
		return nil, fmt.Errorf("expected table identifier after FROM")
	}
	table := p.next().Value
	var selection Expr
	// optional WHERE
	if p.peek() != nil && p.peek().Type == lexer.TokenKeyword && strings.EqualFold(p.peek().Value, "WHERE") {
		p.next()
		expr, err := p.parseLogical()
		if err != nil {
			return nil, err
		}
		selection = expr
	}
	// optional GROUP BY
	var groupBy []string
	if p.peek() != nil && p.peek().Type == lexer.TokenKeyword && strings.EqualFold(p.peek().Value, "GROUP") {
		p.next() // consume GROUP
		if p.peek() == nil || p.peek().Type != lexer.TokenKeyword || !strings.EqualFold(p.peek().Value, "BY") {
			return nil, fmt.Errorf("expected BY after GROUP")
		}
		p.next() // consume BY

		// Parse comma-separated column list
		for {
			if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
				return nil, fmt.Errorf("expected column name in GROUP BY")
			}
			groupBy = append(groupBy, p.next().Value)

			// Check for comma (continue) or end of GROUP BY clause
			if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "," {
				p.next() // consume comma
				continue
			}
			break
		}
	}
	// optional LIMIT
	var limit *uint64
	if p.peek() != nil && p.peek().Type == lexer.TokenKeyword && strings.EqualFold(p.peek().Value, "LIMIT") {
		p.next()
		if p.peek() == nil || p.peek().Type != lexer.TokenNumber {
			return nil, fmt.Errorf("expected number after LIMIT")
		}
		v := p.next().Value
		u, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, err
		}
		limit = &u
	}
	return &SelectStmt{Projections: proj, From: TableRef{Name: table}, Selection: selection, GroupBy: groupBy, Limit: limit}, nil
}

// parseAggregateFunction parses aggregate function calls like COUNT(*), SUM(col), AVG(col)
func (p *parser) parseAggregateFunction() (*AggregateFunctionCall, error) {
	if p.peek() == nil || p.peek().Type != lexer.TokenKeyword {
		return nil, fmt.Errorf("expected aggregate function keyword")
	}

	funcName := strings.ToUpper(p.next().Value)

	// Expect opening parenthesis
	if p.peek() == nil || p.peek().Type != lexer.TokenSeparator || p.peek().Value != "(" {
		return nil, fmt.Errorf("expected '(' after %s", funcName)
	}
	p.next()

	// Parse argument
	aggCall := &AggregateFunctionCall{Function: funcName}

	// Check for COUNT(*)
	if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "*" {
		if funcName != "COUNT" {
			return nil, fmt.Errorf("only COUNT can use *, not %s", funcName)
		}
		aggCall.IsStar = true
		aggCall.Argument = nil
		p.next()
	} else {
		// Parse column reference
		if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
			return nil, fmt.Errorf("expected column name in %s()", funcName)
		}
		colName := p.next().Value
		aggCall.Argument = &ColumnRef{Name: colName}
		aggCall.IsStar = false
	}

	// Expect closing parenthesis
	if p.peek() == nil || p.peek().Type != lexer.TokenSeparator || p.peek().Value != ")" {
		return nil, fmt.Errorf("expected ')' after %s argument", funcName)
	}
	p.next()

	return aggCall, nil
}

// parseLogical handles expressions joined by AND/OR
func (p *parser) parseLogical() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peek() != nil && p.peek().Type == lexer.TokenKeyword && (strings.EqualFold(p.peek().Value, "AND") || strings.EqualFold(p.peek().Value, "OR")) {
		op := strings.ToUpper(p.next().Value)
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &LogicalOp{Left: left, Op: op, Right: right}
	}
	return left, nil
}

// parseComparison expects <identifier> <op> <literal|identifier>
func (p *parser) parseComparison() (Expr, error) {
	// left operand
	if p.peek() == nil {
		return nil, fmt.Errorf("unexpected eof in expression")
	}
	var left Expr
	if p.peek().Type == lexer.TokenIdentifier {
		left = &ColumnRef{Name: p.next().Value}
	} else {
		return nil, fmt.Errorf("expected identifier on left side of comparison, got %v", p.peek())
	}
	// operator
	if p.peek() == nil || p.peek().Type != lexer.TokenOperator {
		return nil, fmt.Errorf("expected comparison operator, got %v", p.peek())
	}
	op := p.next().Value
	// right operand
	if p.peek() == nil {
		return nil, fmt.Errorf("unexpected eof after operator")
	}
	switch p.peek().Type {
	case lexer.TokenNumber:
		v := p.next().Value
		if strings.Contains(v, ".") {
			v = strings.SplitN(v, ".", 2)[0]
		}
		u, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, err
		}
		return &ComparisonOp{Left: left, Op: op, Right: &LiteralInt{Value: u}}, nil
	case lexer.TokenString:
		s := p.next().Value
		return &ComparisonOp{Left: left, Op: op, Right: &LiteralString{Value: s}}, nil
	case lexer.TokenIdentifier:
		r := &ColumnRef{Name: p.next().Value}
		return &ComparisonOp{Left: left, Op: op, Right: r}, nil
	default:
		return nil, fmt.Errorf("unexpected token on right side of comparison: %v", p.peek())
	}
}

func (p *parser) parseInsert() (AstNode, error) {
	// consume INSERT
	p.next()
	if err := p.expectKeyword("INTO"); err != nil {
		return nil, err
	}
	if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
		return nil, fmt.Errorf("expected table name after INTO")
	}
	table := p.next().Value
	// optional column list - skip if present
	if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "(" {
		p.next()
		for {
			if p.peek() == nil {
				return nil, fmt.Errorf("unexpected eof in column list")
			}
			if p.peek().Type == lexer.TokenSeparator && p.peek().Value == ")" {
				p.next()
				break
			}
			p.next()
			if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "," {
				p.next()
			}
		}
	}
	if err := p.expectKeyword("VALUES"); err != nil {
		return nil, err
	}
	// expect (
	if p.peek() == nil || !(p.peek().Type == lexer.TokenSeparator && p.peek().Value == "(") {
		return nil, fmt.Errorf("expected '(' to start VALUES list")
	}
	p.next()
	vals := []Expr{}
	hasValues := false
	for {
		if p.peek() == nil {
			return nil, fmt.Errorf("unexpected eof in values")
		}
		if p.peek().Type == lexer.TokenSeparator && p.peek().Value == ")" {
			if !hasValues {
				return nil, fmt.Errorf("expected at least one value in VALUES list")
			}
			break
		}
		if p.peek().Type == lexer.TokenNumber {
			v := p.next().Value
			if strings.Contains(v, ".") {
				v = strings.SplitN(v, ".", 2)[0]
			}
			u, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return nil, err
			}
			vals = append(vals, &LiteralInt{Value: u})
			hasValues = true
		} else if p.peek().Type == lexer.TokenString {
			s := p.next().Value
			vals = append(vals, &LiteralString{Value: s})
			hasValues = true
		} else if p.peek().Type == lexer.TokenIdentifier {
			id := p.next().Value
			vals = append(vals, &ColumnRef{Name: id})
			hasValues = true
		} else {
			return nil, fmt.Errorf("unexpected token in VALUES: %v", p.peek())
		}
		if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "," {
			p.next()
			continue
		}
		break
	}
	// expect )
	if p.peek() == nil || !(p.peek().Type == lexer.TokenSeparator && p.peek().Value == ")") {
		return nil, fmt.Errorf("expected ')' after values list")
	}
	p.next()
	return &InsertStmt{TableName: table, Values: vals}, nil
}

func (p *parser) parseCreateTable() (AstNode, error) {
	// consume CREATE
	p.next()
	if err := p.expectKeyword("TABLE"); err != nil {
		return nil, err
	}
	if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
		return nil, fmt.Errorf("expected table name after CREATE TABLE")
	}
	table := p.next().Value
	if p.peek() == nil || !(p.peek().Type == lexer.TokenSeparator && p.peek().Value == "(") {
		return nil, fmt.Errorf("expected '(' after table name")
	}
	p.next()
	cols := []ColumnDef{}
	hasColumns := false
	for {
		if p.peek() == nil {
			return nil, fmt.Errorf("unexpected eof in column definitions")
		}
		if p.peek().Type == lexer.TokenSeparator && p.peek().Value == ")" {
			if !hasColumns {
				return nil, fmt.Errorf("expected at least one column definition")
			}
			p.next()
			break
		}
		if p.peek().Type != lexer.TokenIdentifier {
			return nil, fmt.Errorf("expected column name, got %v", p.peek())
		}
		name := p.next().Value
		if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
			return nil, fmt.Errorf("expected column type for %s", name)
		}
		typ := p.next().Value
		cols = append(cols, ColumnDef{Name: name, Type: typ})
		hasColumns = true
		if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "," {
			p.next()
			continue
		}
	}
	return &CreateTableStmt{TableName: table, Columns: cols}, nil
}

func (p *parser) parseUpdate() (AstNode, error) {
	// consume UPDATE
	p.next()

	// table name
	if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
		return nil, fmt.Errorf("expected table name after UPDATE")
	}
	table := p.next().Value

	// SET keyword
	if err := p.expectKeyword("SET"); err != nil {
		return nil, err
	}

	// SET items: col1=val1, col2=val2, ...
	var setItems []SetItem
	for {
		// column name
		if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
			return nil, fmt.Errorf("expected column name in SET clause")
		}
		colName := p.next().Value

		// = operator
		if p.peek() == nil || p.peek().Type != lexer.TokenOperator || p.peek().Value != "=" {
			return nil, fmt.Errorf("expected '=' after column name in SET clause")
		}
		p.next()

		// value expression (literal or column ref)
		var valueExpr Expr
		if p.peek() == nil {
			return nil, fmt.Errorf("unexpected EOF after '=' in SET clause")
		}
		switch p.peek().Type {
		case lexer.TokenNumber:
			v := p.next().Value
			if strings.Contains(v, ".") {
				v = strings.SplitN(v, ".", 2)[0]
			}
			u, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return nil, err
			}
			valueExpr = &LiteralInt{Value: u}
		case lexer.TokenString:
			valueExpr = &LiteralString{Value: p.next().Value}
		case lexer.TokenIdentifier:
			valueExpr = &ColumnRef{Name: p.next().Value}
		default:
			return nil, fmt.Errorf("expected literal or identifier in SET clause, got %v", p.peek())
		}

		setItems = append(setItems, SetItem{Column: colName, Value: valueExpr})

		// check for comma (more SET items)
		if p.peek() != nil && p.peek().Type == lexer.TokenSeparator && p.peek().Value == "," {
			p.next()
			continue
		}
		break
	}

	if len(setItems) == 0 {
		return nil, fmt.Errorf("UPDATE requires at least one SET item")
	}

	// optional WHERE
	var selection Expr
	if p.peek() != nil && p.peek().Type == lexer.TokenKeyword && strings.EqualFold(p.peek().Value, "WHERE") {
		p.next()
		expr, err := p.parseLogical()
		if err != nil {
			return nil, err
		}
		selection = expr
	}

	// optional LIMIT
	var limit *uint64
	if p.peek() != nil && p.peek().Type == lexer.TokenKeyword && strings.EqualFold(p.peek().Value, "LIMIT") {
		p.next()
		if p.peek() == nil || p.peek().Type != lexer.TokenNumber {
			return nil, fmt.Errorf("expected number after LIMIT")
		}
		v := p.next().Value
		u, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, err
		}
		limit = &u
	}

	return &UpdateStmt{
		TableName: table,
		SetItems:  setItems,
		Selection: selection,
		Limit:     limit,
	}, nil
}

func (p *parser) parseDelete() (AstNode, error) {
	// consume DELETE
	p.next()

	// FROM keyword
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}

	// table name
	if p.peek() == nil || p.peek().Type != lexer.TokenIdentifier {
		return nil, fmt.Errorf("expected table name after FROM")
	}
	table := p.next().Value

	// optional WHERE
	var selection Expr
	if p.peek() != nil && p.peek().Type == lexer.TokenKeyword && strings.EqualFold(p.peek().Value, "WHERE") {
		p.next()
		expr, err := p.parseLogical()
		if err != nil {
			return nil, err
		}
		selection = expr
	}

	// optional LIMIT
	var limit *uint64
	if p.peek() != nil && p.peek().Type == lexer.TokenKeyword && strings.EqualFold(p.peek().Value, "LIMIT") {
		p.next()
		if p.peek() == nil || p.peek().Type != lexer.TokenNumber {
			return nil, fmt.Errorf("expected number after LIMIT")
		}
		v := p.next().Value
		u, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, err
		}
		limit = &u
	}

	return &DeleteStmt{
		TableName: table,
		Selection: selection,
		Limit:     limit,
	}, nil
}

// PrintAST returns a human-readable representation of the AST nodes.
func PrintAST(nodes []AstNode) string {
	var b strings.Builder
	for i, n := range nodes {
		b.WriteString(fmt.Sprintf("Node %d:\n", i))
		switch node := n.(type) {
		case *SelectStmt:
			b.WriteString(formatSelect(node, "  "))
		case *InsertStmt:
			b.WriteString(formatInsert(node, "  "))
		case *CreateTableStmt:
			b.WriteString(formatCreateTable(node, "  "))
		case *UpdateStmt:
			b.WriteString(formatUpdate(node, "  "))
		case *DeleteStmt:
			b.WriteString(formatDelete(node, "  "))
		}
	}
	return b.String()
}

func formatSelect(s *SelectStmt, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "SELECT\n")
	b.WriteString(indent + "  Projections:\n")
	for _, p := range s.Projections {
		if p.All {
			b.WriteString(indent + "    *\n")
		} else {
			b.WriteString(indent + "    " + p.Column + "\n")
		}
	}
	b.WriteString(indent + "  FROM: " + s.From.Name + "\n")
	if s.Selection != nil {
		b.WriteString(indent + "  WHERE:\n")
		b.WriteString(formatExpr(s.Selection, indent+"    ") + "\n")
	}
	if len(s.GroupBy) > 0 {
		b.WriteString(indent + "  GROUP BY: " + strings.Join(s.GroupBy, ", ") + "\n")
	}
	if s.Limit != nil {
		b.WriteString(fmt.Sprintf(indent+"  LIMIT: %d\n", *s.Limit))
	}
	return b.String()
}

func formatInsert(ins *InsertStmt, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "INSERT\n")
	b.WriteString(indent + "  Table: " + ins.TableName + "\n")
	b.WriteString(indent + "  Values:\n")
	for _, v := range ins.Values {
		b.WriteString(indent + "    " + formatExprInline(v) + "\n")
	}
	return b.String()
}

func formatCreateTable(ct *CreateTableStmt, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "CREATE TABLE " + ct.TableName + "\n")
	b.WriteString(indent + "  Columns:\n")
	for _, c := range ct.Columns {
		b.WriteString(indent + "    " + c.Name + " " + c.Type + "\n")
	}
	return b.String()
}

func formatUpdate(u *UpdateStmt, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "UPDATE " + u.TableName + "\n")
	b.WriteString(indent + "  SET:\n")
	for _, item := range u.SetItems {
		b.WriteString(indent + "    " + item.Column + " = " + formatExprInline(item.Value) + "\n")
	}
	if u.Selection != nil {
		b.WriteString(indent + "  WHERE:\n")
		b.WriteString(formatExpr(u.Selection, indent+"    ") + "\n")
	}
	if u.Limit != nil {
		b.WriteString(fmt.Sprintf(indent+"  LIMIT: %d\n", *u.Limit))
	}
	return b.String()
}

func formatDelete(d *DeleteStmt, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "DELETE FROM " + d.TableName + "\n")
	if d.Selection != nil {
		b.WriteString(indent + "  WHERE:\n")
		b.WriteString(formatExpr(d.Selection, indent+"    ") + "\n")
	}
	if d.Limit != nil {
		b.WriteString(fmt.Sprintf(indent+"  LIMIT: %d\n", *d.Limit))
	}
	return b.String()
}

func formatExprInline(e Expr) string {
	switch x := e.(type) {
	case *ColumnRef:
		return "col:" + x.Name
	case *LiteralInt:
		return fmt.Sprintf("int:%d", x.Value)
	case *LiteralString:
		return "str:'" + x.Value + "'"
	case *ComparisonOp:
		return formatExprInline(x.Left) + " " + x.Op + " " + formatExprInline(x.Right)
	case *LogicalOp:
		return "(" + formatExprInline(x.Left) + " " + x.Op + " " + formatExprInline(x.Right) + ")"
	case *BinaryOp:
		return "(" + formatExprInline(x.Left) + " " + x.Op + " " + formatExprInline(x.Right) + ")"
	default:
		return fmt.Sprintf("<expr %T>", e)
	}
}

func formatExpr(e Expr, indent string) string {
	switch x := e.(type) {
	case *ColumnRef:
		return indent + "Column: " + x.Name
	case *LiteralInt:
		return fmt.Sprintf(indent+"Integer: %d", x.Value)
	case *LiteralString:
		return indent + "String: '" + x.Value + "'"
	case *ComparisonOp:
		var b strings.Builder
		b.WriteString(indent + "Comparison: " + x.Op + "\n")
		b.WriteString(formatExpr(x.Left, indent+"  ") + "\n")
		b.WriteString(formatExpr(x.Right, indent+"  "))
		return b.String()
	case *LogicalOp:
		var b strings.Builder
		b.WriteString(indent + "Logical: " + x.Op + "\n")
		b.WriteString(formatExpr(x.Left, indent+"  ") + "\n")
		b.WriteString(formatExpr(x.Right, indent+"  "))
		return b.String()
	case *BinaryOp:
		var b strings.Builder
		b.WriteString(indent + "Binary: " + x.Op + "\n")
		b.WriteString(formatExpr(x.Left, indent+"  ") + "\n")
		b.WriteString(formatExpr(x.Right, indent+"  "))
		return b.String()
	default:
		return fmt.Sprintf(indent+"<expr %T>", e)
	}
}
