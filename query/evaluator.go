package query

import (
	"fmt"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

// Evaluator evaluates expressions against rows at runtime.
type Evaluator struct {
	schema *storage.Schema
}

// NewEvaluator creates a new evaluator for the given schema.
func NewEvaluator(schema *storage.Schema) *Evaluator {
	return &Evaluator{schema: schema}
}

// Eval evaluates an expression against a row, returning a storage.Value.
func (e *Evaluator) Eval(expr parser.Expr, row storage.Row) (storage.Value, error) {
	switch ex := expr.(type) {
	case *parser.ColumnRef:
		idx, ok := e.schema.ColumnIndex(ex.Name)
		if !ok {
			return storage.Value{}, &ErrUnknownColumn{Name: ex.Name}
		}
		if idx >= len(row) {
			return storage.Value{}, fmt.Errorf("column index %d out of range (row has %d columns)", idx, len(row))
		}
		return row[idx], nil

	case *parser.LiteralInt:
		// Use INT for small values, BIGINT for large ones
		if ex.Value <= 2147483647 {
			return storage.NewIntValue(int32(ex.Value)), nil
		}
		return storage.NewBigIntValue(int64(ex.Value)), nil

	case *parser.LiteralString:
		return storage.NewVarcharValue(ex.Value), nil

	case *parser.ComparisonOp:
		left, err := e.Eval(ex.Left, row)
		if err != nil {
			return storage.Value{}, err
		}
		right, err := e.Eval(ex.Right, row)
		if err != nil {
			return storage.Value{}, err
		}
		return e.evalComparison(ex.Op, left, right)

	case *parser.LogicalOp:
		left, err := e.Eval(ex.Left, row)
		if err != nil {
			return storage.Value{}, err
		}
		right, err := e.Eval(ex.Right, row)
		if err != nil {
			return storage.Value{}, err
		}
		return e.evalLogical(ex.Op, left, right)

	default:
		return storage.Value{}, fmt.Errorf("unsupported expression type %T", expr)
	}
}

// evalComparison evaluates a comparison operator on two values.
// Implements SQL NULL semantics: NULL compared to anything is NULL.
func (e *Evaluator) evalComparison(op string, left, right storage.Value) (storage.Value, error) {
	// SQL NULL semantics: NULL compared to anything is NULL
	if left.IsNull() || right.IsNull() {
		return storage.NewNullValue(), nil
	}

	// Type checking
	if left.Kind() != right.Kind() {
		return storage.Value{}, &ErrTypeMismatch{
			Left:  left.Kind(),
			Right: right.Kind(),
		}
	}

	// Use storage.CompareValues (must be exported from storage/index.go)
	cmp := storage.CompareValues(left, right)

	var result bool
	switch op {
	case "=":
		result = (cmp == 0)
	case "!=", "<>":
		result = (cmp != 0)
	case "<":
		result = (cmp < 0)
	case "<=":
		result = (cmp <= 0)
	case ">":
		result = (cmp > 0)
	case ">=":
		result = (cmp >= 0)
	default:
		return storage.Value{}, fmt.Errorf("unknown comparison operator %q", op)
	}

	return storage.NewBooleanValue(result), nil
}

// evalLogical evaluates a logical operator (AND/OR) on two values.
// Implements SQL three-valued logic with NULL handling.
func (e *Evaluator) evalLogical(op string, left, right storage.Value) (storage.Value, error) {
	switch op {
	case "AND":
		// NULL AND false = false
		// NULL AND true = NULL
		// false AND anything = false
		if !left.IsNull() && !left.AsBoolean() {
			return storage.NewBooleanValue(false), nil
		}
		if !right.IsNull() && !right.AsBoolean() {
			return storage.NewBooleanValue(false), nil
		}
		if left.IsNull() || right.IsNull() {
			return storage.NewNullValue(), nil
		}
		return storage.NewBooleanValue(true), nil

	case "OR":
		// NULL OR true = true
		// NULL OR false = NULL
		// true OR anything = true
		if !left.IsNull() && left.AsBoolean() {
			return storage.NewBooleanValue(true), nil
		}
		if !right.IsNull() && right.AsBoolean() {
			return storage.NewBooleanValue(true), nil
		}
		if left.IsNull() || right.IsNull() {
			return storage.NewNullValue(), nil
		}
		return storage.NewBooleanValue(false), nil

	default:
		return storage.Value{}, fmt.Errorf("unknown logical operator %q", op)
	}
}
