package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vvshulga/db_internals/lexer"
)

// AstNode represents a top-level statement
type AstNode interface{}

// SelectStmt: SELECT projections FROM table [WHERE selection] [LIMIT limit]
type SelectStmt struct {
	Projections []ProjectionItem // columns list or *
	From        TableRef         // 1 table
	Selection   Expr             // WHERE clause (optional)
	Limit       *uint64          // LIMIT (optional)
}

type ProjectionItem struct {
	All    bool
	Column string
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
			if t == nil || t.Type != lexer.TokenIdentifier {
				return nil, fmt.Errorf("expected projection identifier, got %v", t)
			}
			proj = append(proj, ProjectionItem{All: false, Column: t.Value})
			p.next()
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
	return &SelectStmt{Projections: proj, From: TableRef{Name: table}, Selection: selection, Limit: limit}, nil
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
