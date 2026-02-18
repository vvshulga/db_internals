package storage

import "fmt"

// ErrPageFull is returned by InsertTuple when insufficient free space remains
// to store the tuple data plus its slot entry.
type ErrPageFull struct {
	Available int
	Requested int
}

func (e *ErrPageFull) Error() string {
	return fmt.Sprintf("storage: page full: %d bytes available, %d bytes requested (including %d-byte slot entry)",
		e.Available, e.Requested, SlotSize)
}

// ErrInvalidSlot is returned when a SlotID does not exist in the page.
type ErrInvalidSlot struct {
	ID       SlotID
	NumSlots int
}

func (e *ErrInvalidSlot) Error() string {
	return fmt.Sprintf("storage: invalid slot id %d (page has %d slots)", e.ID, e.NumSlots)
}

// ErrDeletedSlot is returned when accessing a tombstoned slot.
type ErrDeletedSlot struct {
	ID SlotID
}

func (e *ErrDeletedSlot) Error() string {
	return fmt.Sprintf("storage: slot %d has been deleted", e.ID)
}

// ErrInvalidPage is returned when page bytes are structurally or
// cryptographically invalid.
type ErrInvalidPage struct {
	Reason string
}

func (e *ErrInvalidPage) Error() string {
	return fmt.Sprintf("storage: invalid page: %s", e.Reason)
}

// ErrInvalidRID is returned by Fetch/Delete/Update when the RID refers to
// the meta page (PageID == 0) or a page that does not exist yet.
type ErrInvalidRID struct {
	RID        RID
	TotalPages uint64
	Reason     string
}

func (e *ErrInvalidRID) Error() string {
	return fmt.Sprintf("storage: invalid RID %s: %s (total pages: %d)",
		e.RID, e.Reason, e.TotalPages)
}

// ErrSchemaMismatch is returned when the number of values in a Row does not
// match the number of columns in the Schema.
type ErrSchemaMismatch struct {
	Got  int
	Want int
}

func (e *ErrSchemaMismatch) Error() string {
	return fmt.Sprintf("storage: row has %d values, schema has %d columns", e.Got, e.Want)
}

// ErrNullConstraint is returned by Encode when a NULL value is supplied for a
// non-nullable column.
type ErrNullConstraint struct {
	ColumnIndex int
	ColumnName  string
}

func (e *ErrNullConstraint) Error() string {
	return fmt.Sprintf("storage: NULL value in non-nullable column %d (%s)",
		e.ColumnIndex, e.ColumnName)
}

// ErrVarcharTooLong is returned by Encode when a VARCHAR value exceeds the
// column's MaxLen constraint.
type ErrVarcharTooLong struct {
	ColumnIndex int
	ColumnName  string
	MaxLen      uint16
	ActualLen   int
}

func (e *ErrVarcharTooLong) Error() string {
	return fmt.Sprintf("storage: VARCHAR column %d (%s) value length %d exceeds MaxLen %d",
		e.ColumnIndex, e.ColumnName, e.ActualLen, e.MaxLen)
}

// ErrRowTooLarge is returned by Encode when the serialized row exceeds the
// maximum size that can ever fit in a single page.
type ErrRowTooLarge struct {
	Size    int
	MaxSize int
}

func (e *ErrRowTooLarge) Error() string {
	return fmt.Sprintf("storage: serialized row size %d exceeds maximum %d bytes",
		e.Size, e.MaxSize)
}

// ErrInvalidSchema is returned by NewSchema when a column definition is invalid.
type ErrInvalidSchema struct {
	Reason string
}

func (e *ErrInvalidSchema) Error() string {
	return fmt.Sprintf("storage: invalid schema: %s", e.Reason)
}

// ErrCorruptRecord is returned by Decode when the byte slice is too short or
// its internal directory pointers are out of bounds.
type ErrCorruptRecord struct {
	Reason string
}

func (e *ErrCorruptRecord) Error() string {
	return fmt.Sprintf("storage: corrupt record: %s", e.Reason)
}

// ErrTableExists is returned by DB.CreateTable when a table with that name
// already exists in the catalog.
type ErrTableExists struct {
	Name string
}

func (e *ErrTableExists) Error() string {
	return fmt.Sprintf("storage: table %q already exists", e.Name)
}

// ErrTableNotFound is returned by DB.OpenTable and DB.DropTable when the
// requested table does not exist in the catalog.
type ErrTableNotFound struct {
	Name string
}

func (e *ErrTableNotFound) Error() string {
	return fmt.Sprintf("storage: table %q not found", e.Name)
}

// ErrCorruptCatalog is returned by OpenDB when catalog.json exists but cannot
// be parsed or contains invalid schema definitions.
type ErrCorruptCatalog struct {
	Reason string
}

func (e *ErrCorruptCatalog) Error() string {
	return fmt.Sprintf("storage: corrupt catalog: %s", e.Reason)
}

// ErrIndexExists is returned by CreateIndex when an index for the column
// already exists on the table.
type ErrIndexExists struct {
	Table  string
	Column string
}

func (e *ErrIndexExists) Error() string {
	return fmt.Sprintf("storage: index on %q.%q already exists", e.Table, e.Column)
}

// ErrIndexNotFound is returned by DropIndex and LookupExact when no index
// exists for the named column.
type ErrIndexNotFound struct {
	Table  string
	Column string
}

func (e *ErrIndexNotFound) Error() string {
	return fmt.Sprintf("storage: index on %q.%q not found", e.Table, e.Column)
}

// ErrUniqueViolation is returned by Index.Insert when a unique index already
// contains a different RID for the same value.
type ErrUniqueViolation struct {
	Table  string
	Column string
	Value  Value
}

func (e *ErrUniqueViolation) Error() string {
	return fmt.Sprintf("storage: unique violation on %q.%q: value %v already exists",
		e.Table, e.Column, e.Value)
}
