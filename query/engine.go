package query

import (
	"fmt"
	"io"
	"strings"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

// ResultSet holds the output of a single SQL statement execution.
type ResultSet struct {
	Schema *storage.Schema // output column metadata; nil for statements with no schema
	Rows   []storage.Row   // all result rows (may be empty)
}

// Engine orchestrates the full SQL pipeline: Parse → Plan → Optimize → Execute.
type Engine struct {
	db *storage.DB
}

// NewEngine creates a new Engine backed by the given database.
func NewEngine(db *storage.DB) *Engine {
	return &Engine{db: db}
}

// Execute parses and runs a single SQL statement, returning its ResultSet.
// For DML statements the result rows contain the affected-row count or inserted RID.
func (e *Engine) Execute(sql string) (*ResultSet, error) {
	nodes, err := parser.ParseString(sql)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(nodes) == 0 {
		return &ResultSet{}, nil
	}
	return e.executeNode(nodes[0])
}

// ExecuteScript parses and runs all semicolon-separated statements in sql,
// returning one ResultSet per statement in order.
func (e *Engine) ExecuteScript(sql string) ([]*ResultSet, error) {
	nodes, err := parser.ParseString(sql)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	results := make([]*ResultSet, 0, len(nodes))
	for _, node := range nodes {
		rs, err := e.executeNode(node)
		if err != nil {
			return results, err
		}
		results = append(results, rs)
	}
	return results, nil
}

func (e *Engine) executeNode(node parser.AstNode) (*ResultSet, error) {
	logical, err := NewPlanner(e.db).Plan(node)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	physical, err := NewOptimizer(e.db).Optimize(logical)
	if err != nil {
		return nil, fmt.Errorf("optimize: %w", err)
	}
	if err := physical.Open(); err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}
	defer physical.Close()

	rs := &ResultSet{Schema: physical.Schema()}
	for {
		row, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("execute: %w", err)
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, nil
}

// Print writes the result set to w in a human-readable tabular format.
func (rs *ResultSet) Print(w io.Writer) {
	if rs.Schema != nil && rs.Schema.NumColumns() > 0 {
		cols := make([]string, rs.Schema.NumColumns())
		for i := range cols {
			cols[i] = rs.Schema.Column(i).Name
		}
		fmt.Fprintln(w, strings.Join(cols, " | "))
		fmt.Fprintln(w, "---")
	}
	for _, row := range rs.Rows {
		vals := make([]string, len(row))
		for i, v := range row {
			vals[i] = v.String()
		}
		fmt.Fprintln(w, strings.Join(vals, " | "))
	}
	fmt.Fprintf(w, "\n%d row(s) returned\n", len(rs.Rows))
}
