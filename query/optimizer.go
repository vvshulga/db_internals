package query

import (
	"fmt"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

// Optimizer converts logical plans to physical plans with optimizations.
type Optimizer struct {
	db *storage.DB
}

// NewOptimizer creates a new optimizer.
func NewOptimizer(db *storage.DB) *Optimizer {
	return &Optimizer{db: db}
}

// Optimize converts a logical plan to a physical plan.
func (o *Optimizer) Optimize(plan LogicalPlan) (PhysicalOperator, error) {
	switch p := plan.(type) {
	case *LogicalScan:
		return o.optimizeScan(p)
	case *LogicalFilter:
		return o.optimizeFilter(p)
	case *LogicalProjection:
		return o.optimizeProjection(p)
	case *LogicalLimit:
		return o.optimizeLimit(p)
	case *LogicalInsert:
		return o.optimizeInsert(p)
	case *LogicalCreateTable:
		return o.optimizeCreateTable(p)
	case *LogicalUpdate:
		return o.optimizeUpdate(p)
	case *LogicalDelete:
		return o.optimizeDelete(p)
	case *LogicalAggregate:
		return o.optimizeAggregate(p)
	case *LogicalSort:
		return o.optimizeSort(p)
	case *LogicalDistinct:
		return o.optimizeDistinct(p)
	case *LogicalDropTable:
		return o.optimizeDropTable(p)
	case *LogicalCreateDatabase:
		return o.optimizeCreateDatabase(p)
	case *LogicalRenameDatabase:
		return o.optimizeRenameDatabase(p)
	case *LogicalDropDatabase:
		return o.optimizeDropDatabase(p)
	default:
		return nil, fmt.Errorf("unsupported logical plan type %T", plan)
	}
}

// optimizeScan converts a logical scan to a physical table scan.
func (o *Optimizer) optimizeScan(scan *LogicalScan) (PhysicalOperator, error) {
	table, err := o.db.OpenTable(scan.TableName)
	if err != nil {
		return nil, &ErrTableNotFound{Name: scan.TableName}
	}

	return NewPhysicalTableScan(table), nil
}

// optimizeFilter tries to push the filter down to an index scan.
func (o *Optimizer) optimizeFilter(filter *LogicalFilter) (PhysicalOperator, error) {
	// OPTIMIZATION: Try to push filter down to index scan
	if scan, ok := filter.Input.(*LogicalScan); ok {
		if indexOp := o.tryIndexScan(scan, filter.Predicate); indexOp != nil {
			return indexOp, nil
		}
	}

	// Fallback: PhysicalTableScan + PhysicalFilter
	input, err := o.Optimize(filter.Input)
	if err != nil {
		return nil, err
	}

	return NewPhysicalFilter(input, filter.Predicate), nil
}

// tryIndexScan attempts to convert scan+filter into an index scan.
// Returns nil if no suitable index exists.
func (o *Optimizer) tryIndexScan(scan *LogicalScan, predicate parser.Expr) PhysicalOperator {
	table, err := o.db.OpenTable(scan.TableName)
	if err != nil {
		return nil
	}

	// Pattern 1: col = literal (exact match)
	if cmp, ok := predicate.(*parser.ComparisonOp); ok && cmp.Op == "=" {
		if colRef, ok := cmp.Left.(*parser.ColumnRef); ok {
			if table.HasIndex(colRef.Name) {
				val, err := o.evalLiteral(cmp.Right)
				if err == nil {
					return NewPhysicalIndexScanExact(table, colRef.Name, val)
				}
			}
		}
	}

	// Pattern 2: col >= literal (range scan with lower bound)
	if cmp, ok := predicate.(*parser.ComparisonOp); ok && (cmp.Op == ">=" || cmp.Op == ">") {
		if colRef, ok := cmp.Left.(*parser.ColumnRef); ok {
			if table.HasIndex(colRef.Name) {
				val, err := o.evalLiteral(cmp.Right)
				if err == nil {
					return NewPhysicalIndexScanRange(table, colRef.Name, &val, nil)
				}
			}
		}
	}

	// Pattern 3: col <= literal (range scan with upper bound)
	if cmp, ok := predicate.(*parser.ComparisonOp); ok && (cmp.Op == "<=" || cmp.Op == "<") {
		if colRef, ok := cmp.Left.(*parser.ColumnRef); ok {
			if table.HasIndex(colRef.Name) {
				val, err := o.evalLiteral(cmp.Right)
				if err == nil {
					return NewPhysicalIndexScanRange(table, colRef.Name, nil, &val)
				}
			}
		}
	}

	// Pattern 4: col >= X AND col <= Y (double-sided range)
	if logOp, ok := predicate.(*parser.LogicalOp); ok && logOp.Op == "AND" {
		// Try to extract range bounds from both sides
		var colName string
		var loVal, hiVal *storage.Value

		// Check left side
		if cmp, ok := logOp.Left.(*parser.ComparisonOp); ok {
			if colRef, ok := cmp.Left.(*parser.ColumnRef); ok {
				if cmp.Op == ">=" || cmp.Op == ">" {
					colName = colRef.Name
					if val, err := o.evalLiteral(cmp.Right); err == nil {
						loVal = &val
					}
				} else if cmp.Op == "<=" || cmp.Op == "<" {
					colName = colRef.Name
					if val, err := o.evalLiteral(cmp.Right); err == nil {
						hiVal = &val
					}
				}
			}
		}

		// Check right side
		if cmp, ok := logOp.Right.(*parser.ComparisonOp); ok {
			if colRef, ok := cmp.Left.(*parser.ColumnRef); ok {
				// Must be same column
				if colName == "" || colName == colRef.Name {
					if colName == "" {
						colName = colRef.Name
					}
					if cmp.Op == ">=" || cmp.Op == ">" {
						if val, err := o.evalLiteral(cmp.Right); err == nil {
							loVal = &val
						}
					} else if cmp.Op == "<=" || cmp.Op == "<" {
						if val, err := o.evalLiteral(cmp.Right); err == nil {
							hiVal = &val
						}
					}
				}
			}
		}

		// If we found bounds on the same column with an index, use range scan
		if colName != "" && (loVal != nil || hiVal != nil) && table.HasIndex(colName) {
			return NewPhysicalIndexScanRange(table, colName, loVal, hiVal)
		}
	}

	// No suitable index found
	return nil
}

// evalLiteral evaluates a literal expression to a storage value.
func (o *Optimizer) evalLiteral(expr parser.Expr) (storage.Value, error) {
	switch e := expr.(type) {
	case *parser.LiteralInt:
		// Use INT for small values, BIGINT for large ones
		if e.Value <= 2147483647 {
			return storage.NewIntValue(int32(e.Value)), nil
		}
		return storage.NewBigIntValue(int64(e.Value)), nil
	case *parser.LiteralString:
		return storage.NewVarcharValue(e.Value), nil
	default:
		return storage.Value{}, fmt.Errorf("not a literal: %T", expr)
	}
}

// optimizeProjection converts a logical projection to a physical projection.
func (o *Optimizer) optimizeProjection(proj *LogicalProjection) (PhysicalOperator, error) {
	input, err := o.Optimize(proj.Input)
	if err != nil {
		return nil, err
	}

	return NewPhysicalProjection(input, proj.Projections, proj.outputSchema), nil
}

// optimizeLimit converts a logical limit to a physical limit.
func (o *Optimizer) optimizeLimit(limit *LogicalLimit) (PhysicalOperator, error) {
	input, err := o.Optimize(limit.Input)
	if err != nil {
		return nil, err
	}

	return NewPhysicalLimit(input, limit.Count), nil
}

// optimizeInsert converts a logical insert to a physical insert.
func (o *Optimizer) optimizeInsert(ins *LogicalInsert) (PhysicalOperator, error) {
	table, err := o.db.OpenTable(ins.TableName)
	if err != nil {
		return nil, &ErrTableNotFound{Name: ins.TableName}
	}

	// Evaluate VALUES literals into storage.Row
	row := make(storage.Row, len(ins.Values))
	for i, expr := range ins.Values {
		val, err := o.evalLiteral(expr)
		if err != nil {
			return nil, fmt.Errorf("value %d: %w", i, err)
		}
		row[i] = val
	}

	return NewPhysicalInsert(table, row), nil
}

// optimizeCreateTable converts a logical CREATE TABLE to a physical CREATE TABLE.
func (o *Optimizer) optimizeCreateTable(ct *LogicalCreateTable) (PhysicalOperator, error) {
	return NewPhysicalCreateTable(o.db, ct.TableName, ct.Columns), nil
}

func (o *Optimizer) optimizeDropTable(dt *LogicalDropTable) (PhysicalOperator, error) {
	return NewPhysicalDropTable(o.db, dt.TableName), nil
}

func (o *Optimizer) optimizeCreateDatabase(cd *LogicalCreateDatabase) (PhysicalOperator, error) {
	return NewPhysicalCreateDatabase(o.db, cd.DBName), nil
}

func (o *Optimizer) optimizeRenameDatabase(rd *LogicalRenameDatabase) (PhysicalOperator, error) {
	return NewPhysicalRenameDatabase(o.db, rd.OldName, rd.NewName), nil
}

func (o *Optimizer) optimizeDropDatabase(dd *LogicalDropDatabase) (PhysicalOperator, error) {
	return NewPhysicalDropDatabase(o.db, dd.DBName), nil
}

// optimizeUpdate converts a logical update to a physical update.
func (o *Optimizer) optimizeUpdate(upd *LogicalUpdate) (PhysicalOperator, error) {
	table, err := o.db.OpenTable(upd.TableName)
	if err != nil {
		return nil, &ErrTableNotFound{Name: upd.TableName}
	}

	// Optimize the input plan (scan + optional filter + optional limit)
	// The optimizer will use index scans if applicable
	input, err := o.Optimize(upd.Input)
	if err != nil {
		return nil, err
	}

	return NewPhysicalUpdate(input, table, upd.SetItems), nil
}

// optimizeDelete converts a logical delete to a physical delete.
func (o *Optimizer) optimizeDelete(del *LogicalDelete) (PhysicalOperator, error) {
	table, err := o.db.OpenTable(del.TableName)
	if err != nil {
		return nil, &ErrTableNotFound{Name: del.TableName}
	}

	// Optimize the input plan (scan + optional filter + optional limit)
	// The optimizer will use index scans if applicable
	input, err := o.Optimize(del.Input)
	if err != nil {
		return nil, err
	}

	return NewPhysicalDelete(input, table), nil
}

// optimizeAggregate converts a logical aggregate to a physical aggregate.
func (o *Optimizer) optimizeAggregate(agg *LogicalAggregate) (PhysicalOperator, error) {
	// Optimize the input plan (scan + optional filter)
	input, err := o.Optimize(agg.Input)
	if err != nil {
		return nil, err
	}

	// Get input schema to resolve column indices
	inputSchema := input.Schema()

	// Convert group by keys to column indices
	groupByIndices := make([]int, len(agg.GroupByKeys))
	for i, key := range agg.GroupByKeys {
		idx, ok := inputSchema.ColumnIndex(key)
		if !ok {
			return nil, &ErrUnknownColumn{Name: key}
		}
		groupByIndices[i] = idx
	}

	// Convert aggregate expressions to computations
	computations := make([]AggregateComputation, len(agg.Aggregates))
	for i, aggExpr := range agg.Aggregates {
		comp := AggregateComputation{
			Function: aggExpr.Function,
			IsStar:   aggExpr.IsStar,
			ColIndex: -1, // Default for COUNT(*)
		}

		// Resolve column index for non-COUNT(*) aggregates
		if !aggExpr.IsStar && aggExpr.Argument != nil {
			if colRef, ok := aggExpr.Argument.(*parser.ColumnRef); ok {
				idx, ok := inputSchema.ColumnIndex(colRef.Name)
				if !ok {
					return nil, &ErrUnknownColumn{Name: colRef.Name}
				}
				comp.ColIndex = idx
			}
		}

		computations[i] = comp
	}

	return NewPhysicalAggregate(input, groupByIndices, computations, agg.Schema()), nil
}

// optimizeSort converts a logical sort to a physical sort.
func (o *Optimizer) optimizeSort(sort *LogicalSort) (PhysicalOperator, error) {
	// Optimize the input plan
	input, err := o.Optimize(sort.Input)
	if err != nil {
		return nil, err
	}

	// Resolve column names to indices
	inputSchema := input.Schema()
	physicalKeys := make([]SortKeyPhysical, len(sort.SortKeys))

	for i, key := range sort.SortKeys {
		idx, ok := inputSchema.ColumnIndex(key.Column)
		if !ok {
			return nil, &ErrUnknownColumn{Name: key.Column}
		}

		physicalKeys[i] = SortKeyPhysical{
			ColIndex:  idx,
			Direction: key.Direction,
		}
	}

	return NewPhysicalSort(input, physicalKeys), nil
}

// optimizeDistinct converts a logical distinct to a physical distinct.
func (o *Optimizer) optimizeDistinct(distinct *LogicalDistinct) (PhysicalOperator, error) {
	// Optimize the input plan
	input, err := o.Optimize(distinct.Input)
	if err != nil {
		return nil, err
	}

	return NewPhysicalDistinct(input), nil
}
