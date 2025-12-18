package parser

import (
	"testing"
)

func TestParseASTNodes(t *testing.T) {
	cases := []struct {
		in  string
		typ string
	}{
		{"SELECT * FROM users WHERE id = 123", "SelectStmt"},
		{"INSERT INTO table_name VALUES (1, 'Alice', 42);", "InsertStmt"},
		{"INSERT INTO table_name VALUES (1, 2, 3);", "InsertStmt"},
		{"SELECT col1, col2 FROM table_name;", "SelectStmt"},
		{"SELECT * FROM table_name;", "SelectStmt"},
		{"SELECT col1, col2 FROM table_name WHERE col1 > 10;", "SelectStmt"},
		{"SELECT col1 FROM table_name WHERE col2 = 'Alice' LIMIT 10;", "SelectStmt"},
		{"CREATE TABLE table_name (column_name1 INT,column_name2 TEXT);", "CreateTableStmt"},
	}

	for _, c := range cases {
		nodes, err := ParseString(c.in)
		if err != nil {
			t.Fatalf("parse failed for %q: %v", c.in, err)
		}
		if len(nodes) == 0 {
			t.Fatalf("no nodes returned for %q", c.in)
		}
		switch c.typ {
		case "SelectStmt":
			if _, ok := nodes[0].(*SelectStmt); !ok {
				t.Fatalf("expected SelectStmt for %q, got %T", c.in, nodes[0])
			}
		case "InsertStmt":
			if _, ok := nodes[0].(*InsertStmt); !ok {
				t.Fatalf("expected InsertStmt for %q, got %T", c.in, nodes[0])
			}
		case "CreateTableStmt":
			if _, ok := nodes[0].(*CreateTableStmt); !ok {
				t.Fatalf("expected CreateTableStmt for %q, got %T", c.in, nodes[0])
			}
		}
	}
}

func TestParseASTStructure(t *testing.T) {
	// SELECT * FROM users WHERE id = 123
	nodes, err := ParseString("SELECT * FROM users WHERE id = 123")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one node")
	}
	sel, ok := nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SELECT node, got %T", nodes[0])
	}
	if sel.From.Name != "users" {
		t.Fatalf("expected FROM users, got %v", sel.From.Name)
	}
	if len(sel.Projections) != 1 || !sel.Projections[0].All {
		t.Fatalf("expected projection '*'")
	}
	cmp, ok := sel.Selection.(*ComparisonOp)
	if !ok {
		t.Fatalf("expected WHERE comparison, got %T", sel.Selection)
	}
	if cref, ok := cmp.Left.(*ColumnRef); !ok || cref.Name != "id" {
		t.Fatalf("expected left column id, got %T %+v", cmp.Left, cmp.Left)
	}
	if lit, ok := cmp.Right.(*LiteralInt); !ok || lit.Value != 123 {
		t.Fatalf("expected right literal 123, got %T %+v", cmp.Right, cmp.Right)
	}

	// INSERT INTO table_name VALUES (1, 'Alice', 42);
	nodes, err = ParseString("INSERT INTO table_name VALUES (1, 'Alice', 42);")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one node")
	}
	ins, ok := nodes[0].(*InsertStmt)
	if !ok {
		t.Fatalf("expected INSERT node, got %T", nodes[0])
	}
	if ins.TableName != "table_name" {
		t.Fatalf("expected table_name, got %v", ins.TableName)
	}
	if len(ins.Values) != 3 {
		t.Fatalf("expected three values, got %v", len(ins.Values))
	}
	if a, ok := ins.Values[0].(*LiteralInt); !ok || a.Value != 1 {
		t.Fatalf("expected first value 1, got %T %+v", ins.Values[0], ins.Values[0])
	}
	if s, ok := ins.Values[1].(*LiteralString); !ok || s.Value != "Alice" {
		t.Fatalf("expected second value 'Alice', got %T %+v", ins.Values[1], ins.Values[1])
	}
	if b, ok := ins.Values[2].(*LiteralInt); !ok || b.Value != 42 {
		t.Fatalf("expected third value 42, got %T %+v", ins.Values[2], ins.Values[2])
	}

	// INSERT INTO table_name VALUES (1, 2, 3);
	nodes, err = ParseString("INSERT INTO table_name VALUES (1, 2, 3);")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	ins, ok = nodes[0].(*InsertStmt)
	if !ok {
		t.Fatalf("expected INSERT node")
	}
	if len(ins.Values) != 3 {
		t.Fatalf("expected three numeric values, got %v", len(ins.Values))
	}

	// SELECT col1, col2 FROM table_name;
	nodes, err = ParseString("SELECT col1, col2 FROM table_name;")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	sel, ok = nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SELECT node")
	}
	if len(sel.Projections) != 2 || sel.Projections[0].Column != "col1" || sel.Projections[1].Column != "col2" {
		t.Fatalf("unexpected projections: %+v", sel.Projections)
	}

	// SELECT col1, col2 FROM table_name WHERE col1 > 10;
	nodes, err = ParseString("SELECT col1, col2 FROM table_name WHERE col1 > 10;")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	sel, ok = nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SELECT node")
	}
	if cmp, ok := sel.Selection.(*ComparisonOp); !ok {
		t.Fatalf("expected comparison op in WHERE, got %T", sel.Selection)
	} else {
		if _, ok := cmp.Right.(*LiteralInt); !ok {
			t.Fatalf("expected numeric literal on right side")
		}
	}

	// SELECT ... LIMIT 10
	nodes, err = ParseString("SELECT col1 FROM table_name WHERE col2 = 'Alice' LIMIT 10;")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	sel, ok = nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SELECT node")
	}
	if sel.Limit == nil || *sel.Limit != 10 {
		t.Fatalf("expected LIMIT 10, got %v", sel.Limit)
	}

	// CREATE TABLE
	nodes, err = ParseString("CREATE TABLE table_name (column_name1 INT,column_name2 TEXT);")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	ct, ok := nodes[0].(*CreateTableStmt)
	if !ok {
		t.Fatalf("expected CREATE TABLE node, got %T", nodes[0])
	}
	if ct.TableName != "table_name" {
		t.Fatalf("unexpected table name: %v", ct.TableName)
	}
	if len(ct.Columns) != 2 || ct.Columns[0].Name != "column_name1" || ct.Columns[1].Type != "TEXT" {
		t.Fatalf("unexpected columns: %+v", ct.Columns)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing projections", "SELECT FROM t"},
		{"missing table", "SELECT * FROM"},
		{"missing where condition", "SELECT * FROM t WHERE"},
		{"insert missing values list", "INSERT INTO t VALUES"},
		{"create table empty columns", "CREATE TABLE t()"},
		{"invalid statement with WHERE", "WHERE"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err == nil {
				t.Fatalf("expected parse error for %q but got none", c.query)
			}
		})
	}
}
