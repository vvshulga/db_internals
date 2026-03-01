package query

import (
	"fmt"
	"strings"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

// LogicalPlan is the interface all logical plan nodes implement.
// Logical plans represent WHAT to compute, independent of storage implementation.
type LogicalPlan interface {
	Schema() *storage.Schema
	String() string
}

// LogicalScan represents a full table scan.
type LogicalScan struct {
	TableName string
	schema    *storage.Schema
}

func (s *LogicalScan) Schema() *storage.Schema {
	return s.schema
}

func (s *LogicalScan) String() string {
	return fmt.Sprintf("Scan(%s)", s.TableName)
}

// LogicalFilter represents a WHERE predicate.
type LogicalFilter struct {
	Input     LogicalPlan
	Predicate parser.Expr
}

func (f *LogicalFilter) Schema() *storage.Schema {
	return f.Input.Schema()
}

func (f *LogicalFilter) String() string {
	return fmt.Sprintf("Filter(%s, %s)", f.Input.String(), formatExpr(f.Predicate))
}

// LogicalProjection represents a SELECT column list.
type LogicalProjection struct {
	Input        LogicalPlan
	Projections  []ProjectionExpr
	outputSchema *storage.Schema
}

// ProjectionExpr represents a single projection (column or expression).
type ProjectionExpr struct {
	Expr      parser.Expr
	OutputCol string
}

func (p *LogicalProjection) Schema() *storage.Schema {
	return p.outputSchema
}

func (p *LogicalProjection) String() string {
	cols := make([]string, len(p.Projections))
	for i, proj := range p.Projections {
		cols[i] = proj.OutputCol
	}
	return fmt.Sprintf("Project([%s], %s)", strings.Join(cols, ", "), p.Input.String())
}

// LogicalLimit represents a LIMIT clause.
type LogicalLimit struct {
	Input LogicalPlan
	Count uint64
}

func (l *LogicalLimit) Schema() *storage.Schema {
	return l.Input.Schema()
}

func (l *LogicalLimit) String() string {
	return fmt.Sprintf("Limit(%d, %s)", l.Count, l.Input.String())
}

// LogicalInsert represents an INSERT INTO statement.
type LogicalInsert struct {
	TableName string
	Values    []parser.Expr
	schema    *storage.Schema
}

func (i *LogicalInsert) Schema() *storage.Schema {
	return i.schema
}

func (i *LogicalInsert) String() string {
	return fmt.Sprintf("Insert(%s, %d values)", i.TableName, len(i.Values))
}

// LogicalCreateTable represents a CREATE TABLE statement.
type LogicalCreateTable struct {
	TableName string
	Columns   []storage.Column
}

func (c *LogicalCreateTable) Schema() *storage.Schema {
	return nil // DDL statements don't return rows
}

func (c *LogicalCreateTable) String() string {
	return fmt.Sprintf("CreateTable(%s, %d columns)", c.TableName, len(c.Columns))
}

// LogicalDropTable represents a DROP TABLE statement.
type LogicalDropTable struct {
	TableName string
}

func (d *LogicalDropTable) Schema() *storage.Schema { return nil }
func (d *LogicalDropTable) String() string {
	return fmt.Sprintf("DropTable(%s)", d.TableName)
}

// LogicalCreateDatabase represents a CREATE DATABASE statement.
type LogicalCreateDatabase struct {
	DBName string
}

func (c *LogicalCreateDatabase) Schema() *storage.Schema { return nil }
func (c *LogicalCreateDatabase) String() string {
	return fmt.Sprintf("CreateDatabase(%s)", c.DBName)
}

// LogicalRenameDatabase represents a RENAME DATABASE statement.
type LogicalRenameDatabase struct {
	OldName string
	NewName string
}

func (r *LogicalRenameDatabase) Schema() *storage.Schema { return nil }
func (r *LogicalRenameDatabase) String() string {
	return fmt.Sprintf("RenameDatabase(%s -> %s)", r.OldName, r.NewName)
}

// LogicalDropDatabase represents a DROP DATABASE statement.
type LogicalDropDatabase struct {
	DBName string
}

func (d *LogicalDropDatabase) Schema() *storage.Schema { return nil }
func (d *LogicalDropDatabase) String() string {
	return fmt.Sprintf("DropDatabase(%s)", d.DBName)
}

// LogicalShowTables represents a SHOW TABLES statement.
type LogicalShowTables struct{}

func (s *LogicalShowTables) Schema() *storage.Schema { return nil }
func (s *LogicalShowTables) String() string          { return "ShowTables" }

// LogicalShowDatabases represents a SHOW DATABASES statement.
type LogicalShowDatabases struct{}

func (s *LogicalShowDatabases) Schema() *storage.Schema { return nil }
func (s *LogicalShowDatabases) String() string          { return "ShowDatabases" }

// LogicalUpdate represents an UPDATE statement.
type LogicalUpdate struct {
	Input     LogicalPlan             // Input rows to update (Scan + optional Filter)
	TableName string                  // Table to update
	SetItems  []parser.SetItem        // Column assignments
	schema    *storage.Schema
}

func (u *LogicalUpdate) Schema() *storage.Schema {
	return u.schema
}

func (u *LogicalUpdate) String() string {
	return fmt.Sprintf("Update(%s, %d columns, %s)", u.TableName, len(u.SetItems), u.Input.String())
}

// LogicalDelete represents a DELETE statement.
type LogicalDelete struct {
	Input     LogicalPlan  // Input rows to delete (Scan + optional Filter)
	TableName string       // Table to delete from
	schema    *storage.Schema
}

func (d *LogicalDelete) Schema() *storage.Schema {
	return d.schema
}

func (d *LogicalDelete) String() string {
	return fmt.Sprintf("Delete(%s, %s)", d.TableName, d.Input.String())
}

// LogicalAggregate represents an aggregation operation (GROUP BY + aggregates).
type LogicalAggregate struct {
	Input       LogicalPlan                    // Input rows
	GroupByKeys []string                       // Column names to group by (empty for global aggregation)
	Aggregates  []AggregateExpr                // Aggregate functions to compute
	schema      *storage.Schema                // Output schema
}

// AggregateExpr represents a single aggregate function.
type AggregateExpr struct {
	Function   string       // COUNT, SUM, AVG, MIN, MAX
	Argument   parser.Expr  // Column reference (nil for COUNT(*))
	IsStar     bool         // True for COUNT(*)
	OutputName string       // Name of output column
}

func (a *LogicalAggregate) Schema() *storage.Schema {
	return a.schema
}

func (a *LogicalAggregate) String() string {
	if len(a.GroupByKeys) == 0 {
		return fmt.Sprintf("Aggregate(%d functions, %s)", len(a.Aggregates), a.Input.String())
	}
	return fmt.Sprintf("Aggregate(group_by=[%s], %d functions, %s)",
		strings.Join(a.GroupByKeys, ", "), len(a.Aggregates), a.Input.String())
}

// LogicalSort represents an ORDER BY clause.
type LogicalSort struct {
	Input    LogicalPlan
	SortKeys []SortKey
}

// SortKey represents a single sort column with direction.
type SortKey struct {
	Column    string // Column name
	Direction string // "ASC" or "DESC"
}

func (s *LogicalSort) Schema() *storage.Schema {
	return s.Input.Schema()
}

func (s *LogicalSort) String() string {
	keys := make([]string, len(s.SortKeys))
	for i, key := range s.SortKeys {
		keys[i] = fmt.Sprintf("%s %s", key.Column, key.Direction)
	}
	return fmt.Sprintf("Sort([%s], %s)", strings.Join(keys, ", "), s.Input.String())
}

// LogicalDistinct represents a DISTINCT clause that removes duplicate rows.
type LogicalDistinct struct {
	Input LogicalPlan
}

func (d *LogicalDistinct) Schema() *storage.Schema {
	return d.Input.Schema() // DISTINCT doesn't change schema, just filters rows
}

func (d *LogicalDistinct) String() string {
	return fmt.Sprintf("Distinct(%s)", d.Input.String())
}

// formatExpr converts an expression to a string for display.
func formatExpr(expr parser.Expr) string {
	switch e := expr.(type) {
	case *parser.ColumnRef:
		return e.Name
	case *parser.LiteralInt:
		return fmt.Sprintf("%d", e.Value)
	case *parser.LiteralString:
		return fmt.Sprintf("'%s'", e.Value)
	case *parser.ComparisonOp:
		return fmt.Sprintf("(%s %s %s)", formatExpr(e.Left), e.Op, formatExpr(e.Right))
	case *parser.LogicalOp:
		return fmt.Sprintf("(%s %s %s)", formatExpr(e.Left), e.Op, formatExpr(e.Right))
	default:
		return fmt.Sprintf("%T", e)
	}
}
