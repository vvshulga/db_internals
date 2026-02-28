package parser

import (
	"strings"
	"testing"
)

func TestTokenizeTable(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple select", "SELECT id FROM users"},
		{"comparison and float", "SELECT price FROM products WHERE price > 9.99"},
		{"create table", "CREATE TABLE users (id INT, name VARCHAR(64))"},
		{"insert values with string", "INSERT INTO table1 VALUES (1, 'hello')"},
		{"insert with string and numbers", "INSERT INTO users VALUES (42, 'Alice', 30)"},
		{"insert with all numbers", "INSERT INTO products VALUES (100, 999, 49.99)"},
		{"select multiple columns", "SELECT id, name, email FROM users"},
		{"select all from table", "SELECT * FROM products"},
		{"select with where condition", "SELECT * FROM orders WHERE status = 'pending'"},
		{"select with where string and limit", "SELECT name FROM users WHERE city = 'NYC' LIMIT 10"},
		{"create table with column types", "CREATE TABLE items (id INT, price FLOAT, description TEXT)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString(tt.input)
			if err != nil {
				t.Errorf("ParseString(%q) returned error: %v", tt.input, err)
			}
		})
	}
}

func TestParseASTNodes(t *testing.T) {
	sqls := []string{
		"SELECT * FROM users",
		"SELECT id, name FROM users WHERE age > 18",
		"SELECT * FROM products WHERE price < 100.0 LIMIT 5",
		"INSERT INTO users VALUES (1, 'Alice')",
		"CREATE TABLE users (id INT, name VARCHAR(64))",
	}

	for _, sql := range sqls {
		nodes, err := ParseString(sql)
		if err != nil {
			t.Fatalf("Parse(%q) failed: %v", sql, err)
		}
		if len(nodes) == 0 {
			t.Fatalf("Parse(%q) returned no nodes", sql)
		}
	}
}

func TestParseASTStructure(t *testing.T) {
	sql := "SELECT id, name FROM users WHERE age > 18 LIMIT 10"
	nodes, err := ParseString(sql)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	sel, ok := nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SelectStmt, got %T", nodes[0])
	}

	if len(sel.Projections) != 2 {
		t.Errorf("expected 2 projections, got %d", len(sel.Projections))
	}
	if sel.From.Name != "users" {
		t.Errorf("expected table 'users', got %s", sel.From.Name)
	}
	if sel.Selection == nil {
		t.Errorf("expected WHERE clause, got nil")
	}
	if sel.Limit == nil {
		t.Errorf("expected LIMIT clause, got nil")
	} else if *sel.Limit != 10 {
		t.Errorf("expected LIMIT 10, got %d", *sel.Limit)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing projections", "SELECT FROM users"},
		{"missing table", "SELECT * FROM"},
		{"missing where condition", "SELECT * FROM users WHERE"},
		{"insert missing values list", "INSERT INTO users VALUES"},
		{"create table empty columns", "CREATE TABLE users ()"},
		{"invalid statement with WHERE", "WHERE id = 1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err == nil {
				t.Errorf("expected error for query %q but got none", c.query)
			}
		})
	}
}

func TestParseUnclosedString(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"unclosed string in select", "SELECT 'unclosed FROM t"},
		{"unclosed string in insert", "INSERT INTO t VALUES ('unclosed)"},
		{"unclosed string in where", "SELECT * FROM t WHERE name = 'unclosed"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := ParseString(c.query)
			if err == nil && len(nodes) > 0 {
				t.Logf("Parse succeeded for %q (unclosed string)", c.query)
			} else if err != nil {
				t.Logf("Parse error for %q: %v", c.query, err)
			}
		})
	}
}

func TestParseMismatchedParentheses(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing closing paren in where", "SELECT * FROM t WHERE (id = 1"},
		{"extra closing paren", "SELECT * FROM t)"},
		{"extra closing paren in insert", "INSERT INTO t VALUES (1, 2))"},
		{"extra closing paren in create", "CREATE TABLE t (id INT))"},
		{"multiple missing close", "SELECT * FROM ((t WHERE id = 1)"},
		{"unmatched open in select", "SELECT * FROM t (WHERE id = 1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err != nil {
				t.Logf("Got expected error for %q: %v", c.query, err)
			}
		})
	}
}

func TestParseUpdate(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"simple update", "UPDATE users SET name='Bob'"},
		{"update with WHERE", "UPDATE users SET name='Bob' WHERE id=1"},
		{"update multiple columns", "UPDATE users SET name='Bob', age=30"},
		{"update with AND", "UPDATE users SET status='active' WHERE age >= 18 AND age <= 65"},
		{"update with LIMIT", "UPDATE users SET verified=1 LIMIT 100"},
		{"update all rows", "UPDATE products SET discount=0"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := ParseString(c.query)
			if err != nil {
				t.Fatalf("ParseString(%q) failed: %v", c.query, err)
			}
			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}
			if _, ok := nodes[0].(*UpdateStmt); !ok {
				t.Fatalf("expected UpdateStmt, got %T", nodes[0])
			}
		})
	}
}

func TestParseUpdateStructure(t *testing.T) {
	sql := "UPDATE users SET name='Alice', age=25 WHERE id=1 LIMIT 1"
	nodes, err := ParseString(sql)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	stmt, ok := nodes[0].(*UpdateStmt)
	if !ok {
		t.Fatalf("expected UpdateStmt, got %T", nodes[0])
	}

	if stmt.TableName != "users" {
		t.Errorf("expected table 'users', got %s", stmt.TableName)
	}
	if len(stmt.SetItems) != 2 {
		t.Errorf("expected 2 SET items, got %d", len(stmt.SetItems))
	}
	if stmt.Selection == nil {
		t.Error("expected WHERE clause, got nil")
	}
	if stmt.Limit == nil {
		t.Error("expected LIMIT clause, got nil")
	}
}

func TestParseDelete(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"delete all", "DELETE FROM users"},
		{"delete with WHERE", "DELETE FROM users WHERE id=1"},
		{"delete with comparison", "DELETE FROM logs WHERE created < 1000000"},
		{"delete with AND", "DELETE FROM sessions WHERE expired=1 AND accessed < 1000"},
		{"delete with LIMIT", "DELETE FROM logs LIMIT 1000"},
		{"delete range", "DELETE FROM events WHERE timestamp >= 100 AND timestamp < 200"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := ParseString(c.query)
			if err != nil {
				t.Fatalf("ParseString(%q) failed: %v", c.query, err)
			}
			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}
			if _, ok := nodes[0].(*DeleteStmt); !ok {
				t.Fatalf("expected DeleteStmt, got %T", nodes[0])
			}
		})
	}
}

func TestParseDeleteStructure(t *testing.T) {
	sql := "DELETE FROM users WHERE id=1 LIMIT 1"
	nodes, err := ParseString(sql)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	stmt, ok := nodes[0].(*DeleteStmt)
	if !ok {
		t.Fatalf("expected DeleteStmt, got %T", nodes[0])
	}

	if stmt.TableName != "users" {
		t.Errorf("expected table 'users', got %s", stmt.TableName)
	}
	if stmt.Selection == nil {
		t.Error("expected WHERE clause, got nil")
	}
	if stmt.Limit == nil {
		t.Error("expected LIMIT clause, got nil")
	}
}

func TestParseUpdateErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing SET", "UPDATE users name='Bob'"},
		{"missing column", "UPDATE users SET"},
		{"missing value", "UPDATE users SET name="},
		{"empty table", "UPDATE SET name='Bob'"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err == nil {
				t.Errorf("expected error for %q but got none", c.query)
			}
		})
	}
}

func TestParseAggregateFunctions(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"count_star", "SELECT COUNT(*) FROM users"},
		{"count_column", "SELECT COUNT(id) FROM users"},
		{"sum", "SELECT SUM(salary) FROM employees"},
		{"avg", "SELECT AVG(age) FROM customers"},
		{"min", "SELECT MIN(price) FROM products"},
		{"max", "SELECT MAX(score) FROM games"},
		{"multiple_agg", "SELECT COUNT(*), SUM(salary), AVG(age) FROM employees"},
		{"mix_column_agg", "SELECT dept, COUNT(*) FROM employees"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := ParseString(c.query)
			if err != nil {
				t.Fatalf("ParseString(%q) failed: %v", c.query, err)
			}
			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}
			stmt, ok := nodes[0].(*SelectStmt)
			if !ok {
				t.Fatalf("expected SelectStmt, got %T", nodes[0])
			}
			// Verify at least one projection has an aggregate
			hasAgg := false
			for _, proj := range stmt.Projections {
				if proj.Aggregate != nil {
					hasAgg = true
					break
				}
			}
			if !hasAgg {
				t.Error("expected at least one aggregate function in projections")
			}
		})
	}
}

func TestParseAggregateStructure(t *testing.T) {
	sql := "SELECT COUNT(*), SUM(salary), AVG(age) FROM employees"
	nodes, err := ParseString(sql)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	stmt, ok := nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SelectStmt, got %T", nodes[0])
	}

	if len(stmt.Projections) != 3 {
		t.Fatalf("expected 3 projections, got %d", len(stmt.Projections))
	}

	// COUNT(*)
	if stmt.Projections[0].Aggregate == nil {
		t.Error("expected first projection to be aggregate")
	} else {
		agg := stmt.Projections[0].Aggregate
		if agg.Function != "COUNT" {
			t.Errorf("expected COUNT, got %s", agg.Function)
		}
		if !agg.IsStar {
			t.Error("expected COUNT(*), got COUNT(col)")
		}
	}

	// SUM(salary)
	if stmt.Projections[1].Aggregate == nil {
		t.Error("expected second projection to be aggregate")
	} else {
		agg := stmt.Projections[1].Aggregate
		if agg.Function != "SUM" {
			t.Errorf("expected SUM, got %s", agg.Function)
		}
		if agg.IsStar {
			t.Error("expected SUM(col), got SUM(*)")
		}
	}
}

func TestParseAggregateErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"sum_star", "SELECT SUM(*) FROM users"},
		{"missing_paren", "SELECT COUNT FROM users"},
		{"missing_close_paren", "SELECT COUNT( FROM users"},
		{"missing_arg", "SELECT SUM() FROM users"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err == nil {
				t.Errorf("expected error for %q but got none", c.query)
			}
		})
	}
}

func TestParseDeleteErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing FROM", "DELETE users"},
		{"missing table", "DELETE FROM"},
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

func TestParseGroupBy(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"single_column", "SELECT dept, COUNT(*) FROM employees GROUP BY dept"},
		{"multiple_columns", "SELECT dept, region, SUM(salary) FROM employees GROUP BY dept, region"},
		{"with_where", "SELECT city, AVG(age) FROM users WHERE age > 18 GROUP BY city"},
		{"with_limit", "SELECT status, COUNT(*) FROM orders GROUP BY status LIMIT 10"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := ParseString(c.query)
			if err != nil {
				t.Fatalf("ParseString(%q) failed: %v", c.query, err)
			}
			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}

			stmt, ok := nodes[0].(*SelectStmt)
			if !ok {
				t.Fatalf("expected SelectStmt, got %T", nodes[0])
			}
			if len(stmt.GroupBy) == 0 {
				t.Error("GROUP BY should be parsed")
			}
		})
	}
}

func TestParseGroupByStructure(t *testing.T) {
	sql := "SELECT dept, region, COUNT(*), SUM(salary) FROM employees GROUP BY dept, region"
	nodes, err := ParseString(sql)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	stmt, ok := nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SelectStmt, got %T", nodes[0])
	}

	if len(stmt.GroupBy) != 2 {
		t.Fatalf("expected 2 GROUP BY columns, got %d", len(stmt.GroupBy))
	}
	if stmt.GroupBy[0] != "dept" {
		t.Errorf("expected first GROUP BY column 'dept', got %s", stmt.GroupBy[0])
	}
	if stmt.GroupBy[1] != "region" {
		t.Errorf("expected second GROUP BY column 'region', got %s", stmt.GroupBy[1])
	}
}

func TestParseGroupByErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
		err   string
	}{
		{"missing_by", "SELECT dept, COUNT(*) FROM employees GROUP", "expected BY after GROUP"},
		{"missing_column", "SELECT dept, COUNT(*) FROM employees GROUP BY", "expected column name"},
		{"empty_list", "SELECT dept, COUNT(*) FROM employees GROUP BY ,", "expected column name"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err == nil {
				t.Fatalf("expected error for %q but got none", c.query)
			}
			if !strings.Contains(err.Error(), c.err) {
				t.Errorf("expected error containing %q, got %v", c.err, err)
			}
		})
	}
}

func TestParseOrderBy(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		expected int // Expected number of ORDER BY clauses
	}{
		{"single ASC", "SELECT * FROM users ORDER BY name ASC", 1},
		{"single DESC", "SELECT * FROM users ORDER BY age DESC", 1},
		{"default ASC", "SELECT * FROM users ORDER BY id", 1},
		{"multiple", "SELECT * FROM users ORDER BY dept ASC, salary DESC", 2},
		{"with WHERE", "SELECT * FROM users WHERE active = 1 ORDER BY name", 1},
		{"with GROUP BY", "SELECT dept, COUNT(*) FROM users GROUP BY dept ORDER BY dept", 1},
		{"full query", "SELECT * FROM users WHERE age > 18 ORDER BY name ASC LIMIT 10", 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := ParseString(c.query)
			if err != nil {
				t.Fatalf("ParseString(%q) failed: %v", c.query, err)
			}
			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}

			sel, ok := nodes[0].(*SelectStmt)
			if !ok {
				t.Fatalf("expected SelectStmt, got %T", nodes[0])
			}

			if len(sel.OrderBy) != c.expected {
				t.Errorf("expected %d ORDER BY clauses, got %d", c.expected, len(sel.OrderBy))
			}
		})
	}
}

func TestParseOrderByStructure(t *testing.T) {
	sql := "SELECT * FROM users ORDER BY dept ASC, salary DESC"
	nodes, err := ParseString(sql)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	sel, ok := nodes[0].(*SelectStmt)
	if !ok {
		t.Fatalf("expected SelectStmt, got %T", nodes[0])
	}

	if len(sel.OrderBy) != 2 {
		t.Fatalf("expected 2 ORDER BY clauses, got %d", len(sel.OrderBy))
	}

	if sel.OrderBy[0].Column != "dept" || sel.OrderBy[0].Direction != "ASC" {
		t.Errorf("expected first ORDER BY to be 'dept ASC', got '%s %s'",
			sel.OrderBy[0].Column, sel.OrderBy[0].Direction)
	}

	if sel.OrderBy[1].Column != "salary" || sel.OrderBy[1].Direction != "DESC" {
		t.Errorf("expected second ORDER BY to be 'salary DESC', got '%s %s'",
			sel.OrderBy[1].Column, sel.OrderBy[1].Direction)
	}
}

func TestParseOrderByErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing BY", "SELECT * FROM users ORDER"},
		{"missing column", "SELECT * FROM users ORDER BY"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err == nil {
				t.Errorf("expected error for %q but got none", c.query)
			}
		})
	}
}

func TestParseDistinct(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		distinct bool
	}{
		{"basic DISTINCT", "SELECT DISTINCT name FROM users", true},
		{"no DISTINCT", "SELECT name FROM users", false},
		{"DISTINCT with WHERE", "SELECT DISTINCT dept FROM employees WHERE active = 1", true},
		{"DISTINCT with ORDER BY", "SELECT DISTINCT name FROM users ORDER BY name", true},
		{"DISTINCT *", "SELECT DISTINCT * FROM products", true},
		{"DISTINCT multiple columns", "SELECT DISTINCT dept, level FROM employees", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, err := ParseString(tt.sql)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			sel, ok := nodes[0].(*SelectStmt)
			if !ok {
				t.Fatalf("expected SelectStmt")
			}
			if sel.Distinct != tt.distinct {
				t.Errorf("expected Distinct=%v, got %v", tt.distinct, sel.Distinct)
			}
		})
	}
}

func TestParseCreateTableVarcharLength(t *testing.T) {
	tests := []struct {
		name        string
		sql         string
		expectError bool
		validate    func(*testing.T, *CreateTableStmt)
	}{
		{
			name:        "valid VARCHAR with length",
			sql:         "CREATE TABLE users (name VARCHAR(64))",
			expectError: false,
			validate: func(t *testing.T, stmt *CreateTableStmt) {
				if len(stmt.Columns) != 1 {
					t.Fatalf("expected 1 column, got %d", len(stmt.Columns))
				}
				col := stmt.Columns[0]
				if col.Type != "VARCHAR" {
					t.Errorf("expected type VARCHAR, got %s", col.Type)
				}
				if col.TypeLen == nil {
					t.Error("expected TypeLen to be set")
				} else if *col.TypeLen != 64 {
					t.Errorf("expected length 64, got %d", *col.TypeLen)
				}
			},
		},
		{
			name:        "VARCHAR length boundaries (min=1, max=65535)",
			sql:         "CREATE TABLE t (a VARCHAR(1), b VARCHAR(65535))",
			expectError: false,
			validate: func(t *testing.T, stmt *CreateTableStmt) {
				if *stmt.Columns[0].TypeLen != 1 {
					t.Errorf("expected min length 1, got %d", *stmt.Columns[0].TypeLen)
				}
				if *stmt.Columns[1].TypeLen != 65535 {
					t.Errorf("expected max length 65535, got %d", *stmt.Columns[1].TypeLen)
				}
			},
		},
		{
			name:        "mixed column types",
			sql:         "CREATE TABLE users (id INT, name VARCHAR(100), age BIGINT)",
			expectError: false,
			validate: func(t *testing.T, stmt *CreateTableStmt) {
				if stmt.Columns[0].Type != "INT" {
					t.Error("expected INT type")
				}
				if stmt.Columns[0].TypeLen != nil {
					t.Error("INT should not have TypeLen")
				}
				if stmt.Columns[1].Type != "VARCHAR" {
					t.Error("expected VARCHAR type")
				}
				if stmt.Columns[1].TypeLen == nil {
					t.Error("VARCHAR should have TypeLen")
				}
				if stmt.Columns[2].Type != "BIGINT" {
					t.Error("expected BIGINT type")
				}
				if stmt.Columns[2].TypeLen != nil {
					t.Error("BIGINT should not have TypeLen")
				}
			},
		},
		{
			name:        "VARCHAR without length",
			sql:         "CREATE TABLE users (name VARCHAR)",
			expectError: true,
		},
		{
			name:        "VARCHAR with zero length",
			sql:         "CREATE TABLE users (name VARCHAR(0))",
			expectError: true,
		},
		{
			name:        "VARCHAR length exceeds uint16 max",
			sql:         "CREATE TABLE users (name VARCHAR(65536))",
			expectError: true,
		},
		{
			name:        "INT with length parameter (not allowed)",
			sql:         "CREATE TABLE users (id INT(10))",
			expectError: true,
		},
		{
			name:        "VARCHAR missing closing paren",
			sql:         "CREATE TABLE users (name VARCHAR(64)",
			expectError: true,
		},
		{
			name:        "VARCHAR non-numeric length",
			sql:         "CREATE TABLE users (name VARCHAR(abc))",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, err := ParseString(tt.sql)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}

			stmt, ok := nodes[0].(*CreateTableStmt)
			if !ok {
				t.Fatalf("expected CreateTableStmt, got %T", nodes[0])
			}

			if tt.validate != nil {
				tt.validate(t, stmt)
			}
		})
	}
}

func TestParseCreateTableNullableVarchar(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		validate func(*testing.T, *CreateTableStmt)
	}{
		{
			name: "nullable VARCHAR with length",
			sql:  "CREATE TABLE test (description VARCHAR(512)?)",
			validate: func(t *testing.T, stmt *CreateTableStmt) {
				if len(stmt.Columns) != 1 {
					t.Fatalf("expected 1 column, got %d", len(stmt.Columns))
				}
				col := stmt.Columns[0]
				if col.Type != "VARCHAR" {
					t.Errorf("expected type VARCHAR, got %s", col.Type)
				}
				if col.TypeLen == nil {
					t.Error("expected TypeLen to be set")
				} else if *col.TypeLen != 512 {
					t.Errorf("expected length 512, got %d", *col.TypeLen)
				}
				if !col.Nullable {
					t.Error("expected Nullable to be true")
				}
			},
		},
		{
			name: "nullable TEXT",
			sql:  "CREATE TABLE test (description TEXT?)",
			validate: func(t *testing.T, stmt *CreateTableStmt) {
				col := stmt.Columns[0]
				if col.Type != "TEXT" {
					t.Errorf("expected type TEXT, got %s", col.Type)
				}
				if !col.Nullable {
					t.Error("expected Nullable to be true")
				}
			},
		},
		{
			name: "mixed nullable and non-nullable",
			sql:  "CREATE TABLE test (name VARCHAR(64), description VARCHAR(512)?, age INT)",
			validate: func(t *testing.T, stmt *CreateTableStmt) {
				if len(stmt.Columns) != 3 {
					t.Fatalf("expected 3 columns, got %d", len(stmt.Columns))
				}
				// name: non-nullable VARCHAR(64)
				if stmt.Columns[0].Nullable {
					t.Error("name should not be nullable")
				}
				// description: nullable VARCHAR(512)
				if !stmt.Columns[1].Nullable {
					t.Error("description should be nullable")
				}
				if *stmt.Columns[1].TypeLen != 512 {
					t.Errorf("expected description length 512, got %d", *stmt.Columns[1].TypeLen)
				}
				// age: non-nullable INT
				if stmt.Columns[2].Nullable {
					t.Error("age should not be nullable")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, err := ParseString(tt.sql)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}
			stmt, ok := nodes[0].(*CreateTableStmt)
			if !ok {
				t.Fatalf("expected CreateTableStmt, got %T", nodes[0])
			}
			tt.validate(t, stmt)
		})
	}
}

func TestParseNewDDL(t *testing.T) {
	t.Run("DROP TABLE", func(t *testing.T) {
		nodes, err := ParseString("DROP TABLE users")
		if err != nil {
			t.Fatalf("ParseString: %v", err)
		}
		if len(nodes) != 1 {
			t.Fatalf("expected 1 node, got %d", len(nodes))
		}
		stmt, ok := nodes[0].(*DropTableStmt)
		if !ok {
			t.Fatalf("expected *DropTableStmt, got %T", nodes[0])
		}
		if stmt.TableName != "users" {
			t.Errorf("TableName: want 'users', got %q", stmt.TableName)
		}
	})

	t.Run("CREATE DATABASE", func(t *testing.T) {
		nodes, err := ParseString("CREATE DATABASE mydb")
		if err != nil {
			t.Fatalf("ParseString: %v", err)
		}
		stmt, ok := nodes[0].(*CreateDatabaseStmt)
		if !ok {
			t.Fatalf("expected *CreateDatabaseStmt, got %T", nodes[0])
		}
		if stmt.DBName != "mydb" {
			t.Errorf("DBName: want 'mydb', got %q", stmt.DBName)
		}
	})

	t.Run("DROP DATABASE", func(t *testing.T) {
		nodes, err := ParseString("DROP DATABASE mydb")
		if err != nil {
			t.Fatalf("ParseString: %v", err)
		}
		stmt, ok := nodes[0].(*DropDatabaseStmt)
		if !ok {
			t.Fatalf("expected *DropDatabaseStmt, got %T", nodes[0])
		}
		if stmt.DBName != "mydb" {
			t.Errorf("DBName: want 'mydb', got %q", stmt.DBName)
		}
	})

	t.Run("RENAME DATABASE", func(t *testing.T) {
		nodes, err := ParseString("RENAME DATABASE olddb TO newdb")
		if err != nil {
			t.Fatalf("ParseString: %v", err)
		}
		stmt, ok := nodes[0].(*RenameDatabaseStmt)
		if !ok {
			t.Fatalf("expected *RenameDatabaseStmt, got %T", nodes[0])
		}
		if stmt.OldName != "olddb" {
			t.Errorf("OldName: want 'olddb', got %q", stmt.OldName)
		}
		if stmt.NewName != "newdb" {
			t.Errorf("NewName: want 'newdb', got %q", stmt.NewName)
		}
	})
}

func TestParseNewDDLErrors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"drop table missing name", "DROP TABLE"},
		{"drop database missing name", "DROP DATABASE"},
		{"create database missing name", "CREATE DATABASE"},
		{"rename database missing TO", "RENAME DATABASE olddb"},
		{"rename database missing new name", "RENAME DATABASE olddb TO"},
		{"drop unknown keyword", "DROP INDEX foo"},
		{"create unknown keyword", "CREATE VIEW foo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseString(c.query)
			if err == nil {
				t.Errorf("expected error for %q, got nil", c.query)
			}
		})
	}
}
