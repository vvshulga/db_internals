package query

import (
	"fmt"
	"io"
	"strings"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

// PhysicalOperator is the Volcano-model iterator interface.
// All physical operators implement this interface.
type PhysicalOperator interface {
	Open() error                      // Initialize resources
	Next() (storage.Row, error)       // Get next row; io.EOF when exhausted
	Close() error                     // Release resources
	Schema() *storage.Schema          // Output schema
}

// IndexScanType specifies the type of index scan.
type IndexScanType int

const (
	IndexScanExact IndexScanType = iota // Exact match (col = value)
	IndexScanRange                       // Range scan (col >= lo AND col <= hi)
)

// ---- PhysicalTableScan --------------------------------------------------------

// PhysicalTableScan performs a full table scan.
type PhysicalTableScan struct {
	table   *storage.TableHandle
	scanner *storage.Scanner
	schema  *storage.Schema
}

// NewPhysicalTableScan creates a new table scan operator.
func NewPhysicalTableScan(table *storage.TableHandle) *PhysicalTableScan {
	return &PhysicalTableScan{
		table:  table,
		schema: table.Schema(),
	}
}

func (op *PhysicalTableScan) Open() error {
	op.scanner = op.table.Scan()
	return nil
}

func (op *PhysicalTableScan) Next() (storage.Row, error) {
	if !op.scanner.Next() {
		if err := op.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return op.scanner.Row(), nil
}

func (op *PhysicalTableScan) Close() error {
	// Scanner has no close method
	return nil
}

func (op *PhysicalTableScan) Schema() *storage.Schema {
	return op.schema
}

// ---- PhysicalIndexScan --------------------------------------------------------

// PhysicalIndexScan uses an index to scan matching rows.
type PhysicalIndexScan struct {
	table    *storage.TableHandle
	colName  string
	value    storage.Value   // For exact match
	loVal    *storage.Value  // For range scan (nil = unbounded)
	hiVal    *storage.Value
	scanType IndexScanType

	// State for iteration
	rids   []storage.RID
	ridIdx int
	schema *storage.Schema
}

// NewPhysicalIndexScanExact creates an exact match index scan (col = value).
func NewPhysicalIndexScanExact(table *storage.TableHandle, colName string, value storage.Value) *PhysicalIndexScan {
	return &PhysicalIndexScan{
		table:    table,
		colName:  colName,
		value:    value,
		scanType: IndexScanExact,
		schema:   table.Schema(),
	}
}

// NewPhysicalIndexScanRange creates a range index scan (col >= lo AND col <= hi).
// Either lo or hi can be nil for unbounded ranges.
func NewPhysicalIndexScanRange(table *storage.TableHandle, colName string, lo, hi *storage.Value) *PhysicalIndexScan {
	return &PhysicalIndexScan{
		table:    table,
		colName:  colName,
		loVal:    lo,
		hiVal:    hi,
		scanType: IndexScanRange,
		schema:   table.Schema(),
	}
}

func (op *PhysicalIndexScan) Open() error {
	switch op.scanType {
	case IndexScanExact:
		rids, err := op.table.LookupExact(op.colName, op.value)
		if err != nil {
			return err
		}
		op.rids = rids
		op.ridIdx = 0

	case IndexScanRange:
		// Collect all RIDs in range
		var rids []storage.RID
		err := op.table.RangeScan(op.colName, op.loVal, op.hiVal, func(rid storage.RID, row storage.Row) (bool, error) {
			rids = append(rids, rid)
			return true, nil // continue
		})
		if err != nil {
			return err
		}
		op.rids = rids
		op.ridIdx = 0
	}
	return nil
}

func (op *PhysicalIndexScan) Next() (storage.Row, error) {
	for op.ridIdx < len(op.rids) {
		rid := op.rids[op.ridIdx]
		op.ridIdx++

		row, found, err := op.table.Get(rid)
		if err != nil {
			return nil, err
		}
		if !found {
			continue // Deleted since index lookup
		}
		return row, nil
	}
	return nil, io.EOF
}

func (op *PhysicalIndexScan) Close() error {
	return nil
}

func (op *PhysicalIndexScan) Schema() *storage.Schema {
	return op.schema
}

// ---- PhysicalFilter -----------------------------------------------------------

// PhysicalFilter filters rows based on a WHERE predicate.
type PhysicalFilter struct {
	input     PhysicalOperator
	predicate parser.Expr
	evaluator *Evaluator
	schema    *storage.Schema
}

// NewPhysicalFilter creates a new filter operator.
func NewPhysicalFilter(input PhysicalOperator, predicate parser.Expr) *PhysicalFilter {
	return &PhysicalFilter{
		input:     input,
		predicate: predicate,
		evaluator: NewEvaluator(input.Schema()),
		schema:    input.Schema(),
	}
}

func (op *PhysicalFilter) Open() error {
	return op.input.Open()
}

func (op *PhysicalFilter) Next() (storage.Row, error) {
	for {
		row, err := op.input.Next()
		if err != nil {
			return nil, err
		}

		// Evaluate WHERE predicate
		result, err := op.evaluator.Eval(op.predicate, row)
		if err != nil {
			return nil, err
		}

		// SQL three-valued logic: NULL or false → skip row
		if result.IsNull() || !result.AsBoolean() {
			continue
		}

		return row, nil
	}
}

func (op *PhysicalFilter) Close() error {
	return op.input.Close()
}

func (op *PhysicalFilter) Schema() *storage.Schema {
	return op.schema
}

// ---- PhysicalProjection -------------------------------------------------------

// PhysicalProjection evaluates SELECT column list.
type PhysicalProjection struct {
	input        PhysicalOperator
	projections  []ProjectionExpr
	evaluator    *Evaluator
	outputSchema *storage.Schema
}

// NewPhysicalProjection creates a new projection operator.
func NewPhysicalProjection(input PhysicalOperator, projections []ProjectionExpr, outputSchema *storage.Schema) *PhysicalProjection {
	return &PhysicalProjection{
		input:        input,
		projections:  projections,
		evaluator:    NewEvaluator(input.Schema()),
		outputSchema: outputSchema,
	}
}

func (op *PhysicalProjection) Open() error {
	return op.input.Open()
}

func (op *PhysicalProjection) Next() (storage.Row, error) {
	inputRow, err := op.input.Next()
	if err != nil {
		return nil, err
	}

	outputRow := make(storage.Row, len(op.projections))
	for i, proj := range op.projections {
		val, err := op.evaluator.Eval(proj.Expr, inputRow)
		if err != nil {
			return nil, fmt.Errorf("projection %d (%s): %w", i, proj.OutputCol, err)
		}
		outputRow[i] = val
	}
	return outputRow, nil
}

func (op *PhysicalProjection) Close() error {
	return op.input.Close()
}

func (op *PhysicalProjection) Schema() *storage.Schema {
	return op.outputSchema
}

// ---- PhysicalLimit ------------------------------------------------------------

// PhysicalLimit limits the number of rows returned.
type PhysicalLimit struct {
	input  PhysicalOperator
	limit  uint64
	count  uint64 // Rows returned so far
	schema *storage.Schema
}

// NewPhysicalLimit creates a new limit operator.
func NewPhysicalLimit(input PhysicalOperator, limit uint64) *PhysicalLimit {
	return &PhysicalLimit{
		input:  input,
		limit:  limit,
		schema: input.Schema(),
	}
}

func (op *PhysicalLimit) Open() error {
	op.count = 0
	return op.input.Open()
}

func (op *PhysicalLimit) Next() (storage.Row, error) {
	if op.count >= op.limit {
		return nil, io.EOF
	}
	row, err := op.input.Next()
	if err != nil {
		return nil, err
	}
	op.count++
	return row, nil
}

func (op *PhysicalLimit) Close() error {
	return op.input.Close()
}

func (op *PhysicalLimit) Schema() *storage.Schema {
	return op.schema
}

// ---- PhysicalInsert -----------------------------------------------------------

// PhysicalInsert inserts a row into a table.
type PhysicalInsert struct {
	table  *storage.TableHandle
	values storage.Row
	rid    storage.RID
	done   bool
}

// NewPhysicalInsert creates a new insert operator.
func NewPhysicalInsert(table *storage.TableHandle, values storage.Row) *PhysicalInsert {
	return &PhysicalInsert{
		table:  table,
		values: values,
	}
}

func (op *PhysicalInsert) Open() error {
	var err error
	op.rid, err = op.table.Insert(op.values)
	op.done = false
	return err
}

func (op *PhysicalInsert) Next() (storage.Row, error) {
	if op.done {
		return nil, io.EOF
	}
	op.done = true
	// Return inserted RID as a single-column row for display
	return storage.Row{storage.NewBigIntValue(int64(op.rid.PageID))}, nil
}

func (op *PhysicalInsert) Close() error {
	return nil
}

func (op *PhysicalInsert) Schema() *storage.Schema {
	// INSERT returns a single column with the page ID
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "inserted_page_id", Type: storage.TypeBIGINT},
	})
	return schema
}

// InsertedRID returns the RID of the inserted row.
func (op *PhysicalInsert) InsertedRID() storage.RID {
	return op.rid
}

// ---- PhysicalCreateTable ------------------------------------------------------

// PhysicalCreateTable creates a new table.
type PhysicalCreateTable struct {
	db        *storage.DB
	tableName string
	columns   []storage.Column
	done      bool
}

// NewPhysicalCreateTable creates a new CREATE TABLE operator.
func NewPhysicalCreateTable(db *storage.DB, tableName string, columns []storage.Column) *PhysicalCreateTable {
	return &PhysicalCreateTable{
		db:        db,
		tableName: tableName,
		columns:   columns,
	}
}

func (op *PhysicalCreateTable) Open() error {
	schema, err := storage.NewSchema(op.columns)
	if err != nil {
		return err
	}

	_, err = op.db.CreateTable(op.tableName, schema)
	op.done = false
	return err
}

func (op *PhysicalCreateTable) Next() (storage.Row, error) {
	if op.done {
		return nil, io.EOF
	}
	op.done = true
	// CREATE TABLE returns a single row indicating success
	return storage.Row{storage.NewVarcharValue(fmt.Sprintf("Table '%s' created", op.tableName))}, nil
}

func (op *PhysicalCreateTable) Close() error {
	return nil
}

func (op *PhysicalCreateTable) Schema() *storage.Schema {
	// CREATE TABLE returns a message column
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "message", Type: storage.TypeVARCHAR, MaxLen: 255},
	})
	return schema
}

// ---- PhysicalUpdate -----------------------------------------------------------

// PhysicalUpdate updates rows in a table.
type PhysicalUpdate struct {
	input     PhysicalOperator
	table     *storage.TableHandle
	setItems  []parser.SetItem
	evaluator *Evaluator
	count     int64
	done      bool
}

// NewPhysicalUpdate creates a new update operator.
func NewPhysicalUpdate(input PhysicalOperator, table *storage.TableHandle, setItems []parser.SetItem) *PhysicalUpdate {
	return &PhysicalUpdate{
		input:     input,
		table:     table,
		setItems:  setItems,
		evaluator: NewEvaluator(table.Schema()),
	}
}

func (op *PhysicalUpdate) Open() error {
	if err := op.input.Open(); err != nil {
		return err
	}

	op.count = 0
	op.done = false

	// Execute updates: iterate through input rows
	for {
		row, err := op.input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Get scanner to find the RID
		scanner := op.table.Scan()
		var rid storage.RID
		found := false

		// Find the RID for this row by scanning
		for scanner.Next() {
			if rowsEqual(scanner.Row(), row) {
				rid = scanner.RID()
				found = true
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}

		if !found {
			continue // Row was deleted between filter and update
		}

		// Build updated row by evaluating SET expressions
		updatedRow := make(storage.Row, len(row))
		copy(updatedRow, row)

		for _, setItem := range op.setItems {
			// Find column index
			colIdx, ok := op.table.Schema().ColumnIndex(setItem.Column)
			if !ok {
				return &ErrUnknownColumn{Name: setItem.Column}
			}

			// Evaluate the new value
			newVal, err := op.evaluator.Eval(setItem.Value, row)
			if err != nil {
				return fmt.Errorf("evaluating SET %s: %w", setItem.Column, err)
			}

			updatedRow[colIdx] = newVal
		}

		// Update the row
		_, updated, err := op.table.Update(rid, updatedRow)
		if err != nil {
			return err
		}

		if updated {
			op.count++
		}
	}

	return nil
}

func (op *PhysicalUpdate) Next() (storage.Row, error) {
	if op.done {
		return nil, io.EOF
	}
	op.done = true
	// Return count of updated rows
	return storage.Row{storage.NewBigIntValue(op.count)}, nil
}

func (op *PhysicalUpdate) Close() error {
	return op.input.Close()
}

func (op *PhysicalUpdate) Schema() *storage.Schema {
	// UPDATE returns a single column with the count
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "updated_rows", Type: storage.TypeBIGINT},
	})
	return schema
}

// ---- PhysicalDelete -----------------------------------------------------------

// PhysicalDelete deletes rows from a table.
type PhysicalDelete struct {
	input PhysicalOperator
	table *storage.TableHandle
	count int64
	done  bool
}

// NewPhysicalDelete creates a new delete operator.
func NewPhysicalDelete(input PhysicalOperator, table *storage.TableHandle) *PhysicalDelete {
	return &PhysicalDelete{
		input: input,
		table: table,
	}
}

func (op *PhysicalDelete) Open() error {
	if err := op.input.Open(); err != nil {
		return err
	}

	op.count = 0
	op.done = false

	// Execute deletes: iterate through input rows
	for {
		row, err := op.input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Get scanner to find the RID
		scanner := op.table.Scan()
		var rid storage.RID
		found := false

		// Find the RID for this row by scanning
		for scanner.Next() {
			if rowsEqual(scanner.Row(), row) {
				rid = scanner.RID()
				found = true
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}

		if !found {
			continue // Row was already deleted
		}

		// Delete the row
		deleted, err := op.table.Delete(rid)
		if err != nil {
			return err
		}

		if deleted {
			op.count++
		}
	}

	return nil
}

func (op *PhysicalDelete) Next() (storage.Row, error) {
	if op.done {
		return nil, io.EOF
	}
	op.done = true
	// Return count of deleted rows
	return storage.Row{storage.NewBigIntValue(op.count)}, nil
}

func (op *PhysicalDelete) Close() error {
	return op.input.Close()
}

func (op *PhysicalDelete) Schema() *storage.Schema {
	// DELETE returns a single column with the count
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "deleted_rows", Type: storage.TypeBIGINT},
	})
	return schema
}

// ---- PhysicalAggregate --------------------------------------------------------

// PhysicalAggregate computes aggregations (with or without GROUP BY).
type PhysicalAggregate struct {
	input       PhysicalOperator
	groupByKeys []int                  // Column indices to group by (empty for global aggregation)
	aggregates  []AggregateComputation // Aggregate functions to compute
	schema      *storage.Schema        // Output schema

	// State
	groups      map[string][]*AggregateState // Group key -> array of aggregate states (one per aggregate)
	groupValues map[string]storage.Row       // Group key -> group-by column values
	results     []storage.Row                // Materialized results
	resultIdx   int                          // Current position in results
}

// AggregateComputation defines a single aggregate to compute.
type AggregateComputation struct {
	Function string // COUNT, SUM, AVG, MIN, MAX
	ColIndex int    // Column index to aggregate (-1 for COUNT(*))
	IsStar   bool   // True for COUNT(*)
}

// AggregateState holds the running state for one group's aggregates.
type AggregateState struct {
	count  int64         // Number of rows (for COUNT and AVG)
	sum    storage.Value // Running sum (for SUM and AVG)
	min    storage.Value // Running min
	max    storage.Value // Running max
	hasVal bool          // True if we've seen at least one non-NULL value
}

// NewPhysicalAggregate creates a new aggregate operator.
func NewPhysicalAggregate(
	input PhysicalOperator,
	groupByKeys []int,
	aggregates []AggregateComputation,
	schema *storage.Schema,
) *PhysicalAggregate {
	return &PhysicalAggregate{
		input:       input,
		groupByKeys: groupByKeys,
		aggregates:  aggregates,
		schema:      schema,
		groups:      make(map[string][]*AggregateState),
		groupValues: make(map[string]storage.Row),
	}
}

func (op *PhysicalAggregate) Open() error {
	if err := op.input.Open(); err != nil {
		return err
	}

	// Scan all input rows and accumulate aggregates
	for {
		row, err := op.input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Build group key
		groupKey := op.buildGroupKey(row)

		// Get or create aggregate state array for this group
		states, ok := op.groups[groupKey]
		if !ok {
			// Create one state per aggregate
			states = make([]*AggregateState, len(op.aggregates))
			for i := range states {
				states[i] = &AggregateState{}
			}
			op.groups[groupKey] = states

			// Store the group-by column values for this group
			if len(op.groupByKeys) > 0 {
				groupRow := make(storage.Row, len(op.groupByKeys))
				for i, colIdx := range op.groupByKeys {
					groupRow[i] = row[colIdx]
				}
				op.groupValues[groupKey] = groupRow
			}
		}

		// Update each aggregate's individual state
		for i, agg := range op.aggregates {
			op.updateAggregate(states[i], i, agg, row)
		}
	}

	// For global aggregation (no GROUP BY), ensure we always have one group
	// even if the input is empty (SQL semantics: COUNT(*)=0, others=NULL)
	if len(op.groupByKeys) == 0 && len(op.groups) == 0 {
		groupKey := ""
		states := make([]*AggregateState, len(op.aggregates))
		for i := range states {
			states[i] = &AggregateState{}
		}
		op.groups[groupKey] = states
	}

	// Materialize results
	op.results = make([]storage.Row, 0, len(op.groups))
	for groupKey, states := range op.groups {
		outputRow := make(storage.Row, len(op.groupByKeys)+len(op.aggregates))

		// Add group by columns
		if len(op.groupByKeys) > 0 {
			groupRow := op.groupValues[groupKey]
			copy(outputRow[:len(op.groupByKeys)], groupRow)
		}

		// Add aggregate results (each aggregate has its own state)
		for i, agg := range op.aggregates {
			outputRow[len(op.groupByKeys)+i] = op.finalizeAggregate(states[i], i, agg)
		}

		op.results = append(op.results, outputRow)
	}

	op.resultIdx = 0
	return nil
}

func (op *PhysicalAggregate) Next() (storage.Row, error) {
	if op.resultIdx >= len(op.results) {
		return nil, io.EOF
	}
	row := op.results[op.resultIdx]
	op.resultIdx++
	return row, nil
}

func (op *PhysicalAggregate) Close() error {
	return op.input.Close()
}

func (op *PhysicalAggregate) Schema() *storage.Schema {
	return op.schema
}

// buildGroupKey creates a string key from the group-by columns.
// For now, we use a simple string concatenation (not production-ready).
func (op *PhysicalAggregate) buildGroupKey(row storage.Row) string {
	if len(op.groupByKeys) == 0 {
		return "" // Global aggregation - single group
	}

	// Simple concatenation (not robust for all data types)
	var key strings.Builder
	for i, colIdx := range op.groupByKeys {
		if i > 0 {
			key.WriteString("|")
		}
		key.WriteString(row[colIdx].String())
	}
	return key.String()
}

// updateAggregate updates the aggregate state with a new row.
func (op *PhysicalAggregate) updateAggregate(state *AggregateState, aggIdx int, agg AggregateComputation, row storage.Row) {
	var val storage.Value
	if agg.IsStar {
		// COUNT(*) - always count the row
		state.count++
		return
	}

	// Get the column value
	val = row[agg.ColIndex]

	// Skip NULL values for all aggregates except COUNT(*)
	if val.IsNull() {
		return
	}

	state.hasVal = true

	switch agg.Function {
	case "COUNT":
		// Count non-NULL values
		state.count++
	case "SUM", "AVG":
		// Increment count for AVG denominator
		state.count++
		if state.sum.IsNull() {
			state.sum = val
		} else {
			// Add to sum
			state.sum = addValues(state.sum, val)
		}
	case "MIN":
		if state.min.IsNull() || storage.CompareValues(val, state.min) < 0 {
			state.min = val
		}
	case "MAX":
		if state.max.IsNull() || storage.CompareValues(val, state.max) > 0 {
			state.max = val
		}
	}
}

// finalizeAggregate computes the final result for an aggregate.
func (op *PhysicalAggregate) finalizeAggregate(state *AggregateState, aggIdx int, agg AggregateComputation) storage.Value {
	if agg.Function == "COUNT" {
		return storage.NewBigIntValue(state.count)
	}

	// All other aggregates return NULL if no non-NULL values seen
	if !state.hasVal {
		return storage.NewNullValue()
	}

	switch agg.Function {
	case "SUM":
		return state.sum
	case "AVG":
		if state.count == 0 {
			return storage.NewNullValue()
		}
		// Compute average
		return divideValues(state.sum, storage.NewBigIntValue(state.count))
	case "MIN":
		return state.min
	case "MAX":
		return state.max
	default:
		return storage.NewNullValue()
	}
}

// addValues adds two numeric values.
func addValues(a, b storage.Value) storage.Value {
	switch a.Kind() {
	case storage.KindInt:
		return storage.NewIntValue(a.AsInt() + b.AsInt())
	case storage.KindBigInt:
		return storage.NewBigIntValue(a.AsBigInt() + b.AsBigInt())
	case storage.KindFloat:
		return storage.NewFloatValue(a.AsFloat() + b.AsFloat())
	case storage.KindDouble:
		return storage.NewDoubleValue(a.AsDouble() + b.AsDouble())
	default:
		return storage.NewNullValue()
	}
}

// divideValues divides two numeric values for AVG computation.
func divideValues(sum storage.Value, count storage.Value) storage.Value {
	countVal := float64(count.AsBigInt())
	switch sum.Kind() {
	case storage.KindInt:
		return storage.NewDoubleValue(float64(sum.AsInt()) / countVal)
	case storage.KindBigInt:
		return storage.NewDoubleValue(float64(sum.AsBigInt()) / countVal)
	case storage.KindFloat:
		return storage.NewFloatValue(sum.AsFloat() / float32(countVal))
	case storage.KindDouble:
		return storage.NewDoubleValue(sum.AsDouble() / countVal)
	default:
		return storage.NewNullValue()
	}
}

// ---- Helper Functions ---------------------------------------------------------

// rowsEqual compares two rows for equality.
func rowsEqual(a, b storage.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if storage.CompareValues(a[i], b[i]) != 0 {
			return false
		}
	}
	return true
}
