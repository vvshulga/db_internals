package query

import (
	"fmt"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

// Planner converts AST nodes to logical plans.
type Planner struct {
	db *storage.DB
}

// NewPlanner creates a new planner.
func NewPlanner(db *storage.DB) *Planner {
	return &Planner{db: db}
}

// Plan converts an AST node to a logical plan.
func (p *Planner) Plan(stmt parser.AstNode) (LogicalPlan, error) {
	switch s := stmt.(type) {
	case *parser.SelectStmt:
		return p.planSelect(s)
	case *parser.InsertStmt:
		return p.planInsert(s)
	case *parser.CreateTableStmt:
		return p.planCreateTable(s)
	case *parser.UpdateStmt:
		return p.planUpdate(s)
	case *parser.DeleteStmt:
		return p.planDelete(s)
	case *parser.DropTableStmt:
		return p.planDropTable(s)
	case *parser.CreateDatabaseStmt:
		return p.planCreateDatabase(s)
	case *parser.RenameDatabaseStmt:
		return p.planRenameDatabase(s)
	case *parser.DropDatabaseStmt:
		return p.planDropDatabase(s)
	default:
		return nil, fmt.Errorf("unsupported statement type %T", stmt)
	}
}

// planSelect converts a SELECT statement to a logical plan.
func (p *Planner) planSelect(stmt *parser.SelectStmt) (LogicalPlan, error) {
	// 1. Validate table exists
	table, err := p.db.OpenTable(stmt.From.Name)
	if err != nil {
		return nil, &ErrTableNotFound{Name: stmt.From.Name}
	}
	schema := table.Schema()

	// 2. Base: LogicalScan
	var plan LogicalPlan = &LogicalScan{
		TableName: stmt.From.Name,
		schema:    schema,
	}

	// 3. Add LogicalFilter if WHERE exists
	if stmt.Selection != nil {
		if err := p.validateExpr(stmt.Selection, schema); err != nil {
			return nil, fmt.Errorf("WHERE clause: %w", err)
		}
		plan = &LogicalFilter{
			Input:     plan,
			Predicate: stmt.Selection,
		}
	}

	// 4. Check if this is an aggregate query
	hasAggregates := false
	for _, item := range stmt.Projections {
		if item.Aggregate != nil {
			hasAggregates = true
			break
		}
	}

	if hasAggregates {
		// Build LogicalAggregate with GROUP BY columns from AST
		aggregatePlan, err := p.buildAggregate(stmt.Projections, stmt.GroupBy, schema, plan)
		if err != nil {
			return nil, err
		}
		plan = aggregatePlan
	} else {
		// Regular projection
		projections, outputSchema, err := p.buildProjections(stmt.Projections, schema)
		if err != nil {
			return nil, err
		}
		plan = &LogicalProjection{
			Input:        plan,
			Projections:  projections,
			outputSchema: outputSchema,
		}
	}

	// 4.5 Add LogicalDistinct if DISTINCT specified
	if stmt.Distinct {
		plan = &LogicalDistinct{
			Input: plan,
		}
	}

	// 5. Add LogicalSort if ORDER BY exists
	if len(stmt.OrderBy) > 0 {
		// Validate ORDER BY columns against current schema
		currentSchema := plan.Schema()
		sortKeys := make([]SortKey, len(stmt.OrderBy))

		for i, ob := range stmt.OrderBy {
			// Check column exists
			if _, ok := currentSchema.ColumnIndex(ob.Column); !ok {
				return nil, &ErrUnknownColumn{Name: ob.Column}
			}

			sortKeys[i] = SortKey{
				Column:    ob.Column,
				Direction: ob.Direction,
			}
		}

		plan = &LogicalSort{
			Input:    plan,
			SortKeys: sortKeys,
		}
	}

	// 6. Add LogicalLimit if specified
	if stmt.Limit != nil {
		plan = &LogicalLimit{
			Input: plan,
			Count: *stmt.Limit,
		}
	}

	return plan, nil
}

// buildAggregate creates a LogicalAggregate node from projections with aggregate functions.
func (p *Planner) buildAggregate(items []parser.ProjectionItem, groupByColumns []string, inputSchema *storage.Schema, input LogicalPlan) (LogicalPlan, error) {
	var aggregates []AggregateExpr
	var groupByCols []string
	var outputCols []storage.Column

	for _, item := range items {
		if item.Aggregate != nil {
			agg := item.Aggregate

			// Validate aggregate function
			if !agg.IsStar && agg.Argument != nil {
				// Validate the argument column exists
				if colRef, ok := agg.Argument.(*parser.ColumnRef); ok {
					if _, ok := inputSchema.ColumnIndex(colRef.Name); !ok {
						return nil, &ErrUnknownColumn{Name: colRef.Name}
					}
				}
			}

			// Determine output column name
			outputName := fmt.Sprintf("%s", agg.Function)
			if agg.IsStar {
				outputName = fmt.Sprintf("%s(*)", agg.Function)
			} else if colRef, ok := agg.Argument.(*parser.ColumnRef); ok {
				outputName = fmt.Sprintf("%s(%s)", agg.Function, colRef.Name)
			}

			aggregates = append(aggregates, AggregateExpr{
				Function:   agg.Function,
				Argument:   agg.Argument,
				IsStar:     agg.IsStar,
				OutputName: outputName,
			})

			// Determine output type based on function
			var outputType storage.DataType
			switch agg.Function {
			case "COUNT":
				outputType = storage.TypeBIGINT
			case "SUM":
				// Infer from input column type (for now, use BIGINT)
				outputType = storage.TypeBIGINT
			case "AVG":
				outputType = storage.TypeDOUBLE
			case "MIN", "MAX":
				// Use input column type
				if !agg.IsStar && agg.Argument != nil {
					if colRef, ok := agg.Argument.(*parser.ColumnRef); ok {
						if idx, ok := inputSchema.ColumnIndex(colRef.Name); ok {
							outputType = inputSchema.Column(idx).Type
						}
					}
				} else {
					outputType = storage.TypeBIGINT
				}
			default:
				outputType = storage.TypeBIGINT
			}

			outputCols = append(outputCols, storage.Column{
				Name: outputName,
				Type: outputType,
			})
		} else {
			// Regular column reference - must be in GROUP BY
			colName := item.Column

			// Validate: if GROUP BY exists, column must be in GROUP BY list
			if len(groupByColumns) > 0 {
				found := false
				for _, gbCol := range groupByColumns {
					if gbCol == colName {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("column '%s' must appear in GROUP BY clause or be used in an aggregate function", colName)
				}
			} else {
				// No GROUP BY but mixing columns with aggregates - invalid
				return nil, fmt.Errorf("cannot mix aggregate functions and regular columns without GROUP BY")
			}

			// Validate column exists in input schema
			idx, ok := inputSchema.ColumnIndex(colName)
			if !ok {
				return nil, &ErrUnknownColumn{Name: colName}
			}

			// Add to group-by columns and output schema
			groupByCols = append(groupByCols, colName)
			outputCols = append(outputCols, inputSchema.Column(idx))
		}
	}

	// Reorder output columns: group-by columns first, then aggregates
	// This matches SQL convention and makes result interpretation easier
	finalOutputCols := make([]storage.Column, 0, len(outputCols))

	// Add group-by columns first
	for _, colName := range groupByCols {
		for i, col := range outputCols {
			if col.Name == colName {
				finalOutputCols = append(finalOutputCols, outputCols[i])
				break
			}
		}
	}

	// Add aggregate columns
	for _, col := range outputCols {
		isGroupByCol := false
		for _, gbCol := range groupByCols {
			if col.Name == gbCol {
				isGroupByCol = true
				break
			}
		}
		if !isGroupByCol {
			finalOutputCols = append(finalOutputCols, col)
		}
	}

	outputSchema, err := storage.NewSchema(finalOutputCols)
	if err != nil {
		return nil, err
	}

	return &LogicalAggregate{
		Input:       input,
		GroupByKeys: groupByCols,
		Aggregates:  aggregates,
		schema:      outputSchema,
	}, nil
}

// buildProjections converts parser projection items to projection expressions.
func (p *Planner) buildProjections(items []parser.ProjectionItem, inputSchema *storage.Schema) (
	[]ProjectionExpr, *storage.Schema, error) {

	if len(items) == 1 && items[0].All {
		// SELECT * - project all columns
		projs := make([]ProjectionExpr, inputSchema.NumColumns())
		cols := make([]storage.Column, inputSchema.NumColumns())
		for i := 0; i < inputSchema.NumColumns(); i++ {
			col := inputSchema.Column(i)
			projs[i] = ProjectionExpr{
				Expr:      &parser.ColumnRef{Name: col.Name},
				OutputCol: col.Name,
			}
			cols[i] = col
		}
		schema, _ := storage.NewSchema(cols)
		return projs, schema, nil
	}

	// Explicit column list
	projs := make([]ProjectionExpr, len(items))
	cols := make([]storage.Column, len(items))

	for i, item := range items {
		idx, ok := inputSchema.ColumnIndex(item.Column)
		if !ok {
			return nil, nil, &ErrUnknownColumn{Name: item.Column}
		}

		col := inputSchema.Column(idx)
		projs[i] = ProjectionExpr{
			Expr:      &parser.ColumnRef{Name: item.Column},
			OutputCol: col.Name,
		}
		cols[i] = col
	}

	schema, _ := storage.NewSchema(cols)
	return projs, schema, nil
}

// validateExpr validates that an expression only references valid columns.
func (p *Planner) validateExpr(expr parser.Expr, schema *storage.Schema) error {
	switch e := expr.(type) {
	case *parser.ColumnRef:
		if _, ok := schema.ColumnIndex(e.Name); !ok {
			return &ErrUnknownColumn{Name: e.Name}
		}
	case *parser.ComparisonOp:
		if err := p.validateExpr(e.Left, schema); err != nil {
			return err
		}
		if err := p.validateExpr(e.Right, schema); err != nil {
			return err
		}
	case *parser.LogicalOp:
		if err := p.validateExpr(e.Left, schema); err != nil {
			return err
		}
		if err := p.validateExpr(e.Right, schema); err != nil {
			return err
		}
	case *parser.LiteralInt, *parser.LiteralString:
		// Always valid
	default:
		return fmt.Errorf("unsupported expression type %T", e)
	}
	return nil
}

// planInsert converts an INSERT statement to a logical plan.
func (p *Planner) planInsert(stmt *parser.InsertStmt) (LogicalPlan, error) {
	table, err := p.db.OpenTable(stmt.TableName)
	if err != nil {
		return nil, &ErrTableNotFound{Name: stmt.TableName}
	}

	// Validate number of values matches schema
	schema := table.Schema()
	if len(stmt.Values) != schema.NumColumns() {
		return nil, &ErrSchemaMismatch{
			Expected: schema.NumColumns(),
			Got:      len(stmt.Values),
		}
	}

	return &LogicalInsert{
		TableName: stmt.TableName,
		Values:    stmt.Values,
		schema:    schema,
	}, nil
}

// planCreateTable converts a CREATE TABLE statement to a logical plan.
func (p *Planner) planCreateTable(stmt *parser.CreateTableStmt) (LogicalPlan, error) {
	// Convert parser column definitions to storage columns
	cols := make([]storage.Column, len(stmt.Columns))
	for i, c := range stmt.Columns {
		col, err := convertColumnDef(c)
		if err != nil {
			return nil, fmt.Errorf("column %d (%s): %w", i, c.Name, err)
		}
		cols[i] = col
	}

	return &LogicalCreateTable{
		TableName: stmt.TableName,
		Columns:   cols,
	}, nil
}

func (p *Planner) planDropTable(stmt *parser.DropTableStmt) (LogicalPlan, error) {
	return &LogicalDropTable{TableName: stmt.TableName}, nil
}

func (p *Planner) planCreateDatabase(stmt *parser.CreateDatabaseStmt) (LogicalPlan, error) {
	return &LogicalCreateDatabase{DBName: stmt.DBName}, nil
}

func (p *Planner) planRenameDatabase(stmt *parser.RenameDatabaseStmt) (LogicalPlan, error) {
	return &LogicalRenameDatabase{OldName: stmt.OldName, NewName: stmt.NewName}, nil
}

func (p *Planner) planDropDatabase(stmt *parser.DropDatabaseStmt) (LogicalPlan, error) {
	return &LogicalDropDatabase{DBName: stmt.DBName}, nil
}

// convertColumnDef converts a parser column definition to a storage column.
func convertColumnDef(c parser.ColumnDef) (storage.Column, error) {
	col := storage.Column{
		Name:     c.Name,
		Nullable: c.Nullable, // Use parsed nullable flag
	}

	// Map parser type to storage type
	switch c.Type {
	case "INT":
		col.Type = storage.TypeINT
	case "BIGINT":
		col.Type = storage.TypeBIGINT
	case "FLOAT":
		col.Type = storage.TypeFLOAT
	case "DOUBLE":
		col.Type = storage.TypeDOUBLE
	case "BOOLEAN":
		col.Type = storage.TypeBOOLEAN
	case "DATETIME":
		col.Type = storage.TypeDATETIME
	case "VARCHAR":
		if c.TypeLen == nil {
			return storage.Column{}, fmt.Errorf("VARCHAR column %q missing length", c.Name)
		}
		col.Type = storage.TypeVARCHAR
		col.MaxLen = *c.TypeLen
	case "TEXT":
		col.Type = storage.TypeTEXT
	default:
		return storage.Column{}, fmt.Errorf("unknown type %q for column %q", c.Type, c.Name)
	}

	return col, nil
}

// planUpdate converts an UPDATE statement to a logical plan.
func (p *Planner) planUpdate(stmt *parser.UpdateStmt) (LogicalPlan, error) {
	// 1. Validate table exists
	table, err := p.db.OpenTable(stmt.TableName)
	if err != nil {
		return nil, &ErrTableNotFound{Name: stmt.TableName}
	}
	schema := table.Schema()

	// 2. Base: LogicalScan
	var input LogicalPlan = &LogicalScan{
		TableName: stmt.TableName,
		schema:    schema,
	}

	// 3. Add LogicalFilter if WHERE exists
	if stmt.Selection != nil {
		if err := p.validateExpr(stmt.Selection, schema); err != nil {
			return nil, fmt.Errorf("WHERE clause: %w", err)
		}
		input = &LogicalFilter{
			Input:     input,
			Predicate: stmt.Selection,
		}
	}

	// 4. Add LogicalLimit if specified
	if stmt.Limit != nil {
		input = &LogicalLimit{
			Input: input,
			Count: *stmt.Limit,
		}
	}

	// 5. Validate SET items
	for _, setItem := range stmt.SetItems {
		// Validate column exists
		if _, ok := schema.ColumnIndex(setItem.Column); !ok {
			return nil, &ErrUnknownColumn{Name: setItem.Column}
		}
		// Validate value expression
		if err := p.validateExpr(setItem.Value, schema); err != nil {
			return nil, fmt.Errorf("SET %s: %w", setItem.Column, err)
		}
	}

	return &LogicalUpdate{
		Input:     input,
		TableName: stmt.TableName,
		SetItems:  stmt.SetItems,
		schema:    schema,
	}, nil
}

// planDelete converts a DELETE statement to a logical plan.
func (p *Planner) planDelete(stmt *parser.DeleteStmt) (LogicalPlan, error) {
	// 1. Validate table exists
	table, err := p.db.OpenTable(stmt.TableName)
	if err != nil {
		return nil, &ErrTableNotFound{Name: stmt.TableName}
	}
	schema := table.Schema()

	// 2. Base: LogicalScan
	var input LogicalPlan = &LogicalScan{
		TableName: stmt.TableName,
		schema:    schema,
	}

	// 3. Add LogicalFilter if WHERE exists
	if stmt.Selection != nil {
		if err := p.validateExpr(stmt.Selection, schema); err != nil {
			return nil, fmt.Errorf("WHERE clause: %w", err)
		}
		input = &LogicalFilter{
			Input:     input,
			Predicate: stmt.Selection,
		}
	}

	// 4. Add LogicalLimit if specified
	if stmt.Limit != nil {
		input = &LogicalLimit{
			Input: input,
			Count: *stmt.Limit,
		}
	}

	return &LogicalDelete{
		Input:     input,
		TableName: stmt.TableName,
		schema:    schema,
	}, nil
}
