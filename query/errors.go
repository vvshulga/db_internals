package query

import (
	"fmt"

	"github.com/vvshulga/db_internals/storage"
)

// ErrTableNotFound is returned when a table is not found in the catalog.
type ErrTableNotFound struct {
	Name string
}

func (e *ErrTableNotFound) Error() string {
	return fmt.Sprintf("table not found: %s", e.Name)
}

// ErrUnknownColumn is returned when a column is referenced that doesn't exist in the schema.
type ErrUnknownColumn struct {
	Name string
}

func (e *ErrUnknownColumn) Error() string {
	return fmt.Sprintf("unknown column: %s", e.Name)
}

// ErrTypeMismatch is returned when comparing values of incompatible types.
type ErrTypeMismatch struct {
	Left  storage.ValueKind
	Right storage.ValueKind
}

func (e *ErrTypeMismatch) Error() string {
	return fmt.Sprintf("type mismatch: cannot compare %v with %v", e.Left, e.Right)
}

// ErrSchemaMismatch is returned when a row doesn't match the expected schema.
type ErrSchemaMismatch struct {
	Expected int
	Got      int
}

func (e *ErrSchemaMismatch) Error() string {
	return fmt.Sprintf("schema mismatch: expected %d columns, got %d", e.Expected, e.Got)
}
