package storage

import (
	"errors"
	"fmt"
	"os"
)

// TableHandle is an open reference to a table. It bundles the schema and
// HeapFile so callers do not need to pass the schema on every operation.
//
// TableHandle instances are created and cached by DB. Do not call closeHeap
// directly; use DB.Close() or DB.DropTable() to release resources.
type TableHandle struct {
	name    string
	schema  *Schema
	heap    *HeapFile
	indexes map[string]*Index // colName → Index; nil until first index is added
}

// Name returns the table's name.
func (t *TableHandle) Name() string { return t.name }

// Schema returns the table's schema.
func (t *TableHandle) Schema() *Schema { return t.schema }

// Insert encodes and stores row, returning its stable RID. All open indexes
// are updated after the heap insert. On a unique-index violation the insert
// is aborted and ErrUniqueViolation is returned.
func (t *TableHandle) Insert(row Row) (RID, error) {
	rid, err := t.heap.Insert(t.schema, row)
	if err != nil {
		return RID{}, err
	}
	for _, idx := range t.indexes {
		val := row[idx.colIndex]
		if err := idx.Insert(val, rid); err != nil {
			// Best-effort rollback: remove the heap row. We do not roll back
			// other indexes already updated in this loop — a proper WAL is
			// needed for atomic multi-index updates (out of scope).
			_ = t.heap.Delete(t.schema, rid)
			return RID{}, err
		}
	}
	return rid, nil
}

// Get fetches the row identified by rid.
//   - (row, true, nil)  — row found.
//   - (nil, false, nil) — row was deleted or rid is out of range.
//   - (nil, false, err) — I/O or decoding error.
func (t *TableHandle) Get(rid RID) (Row, bool, error) {
	row, err := t.heap.Fetch(t.schema, rid)
	if err == nil {
		return row, true, nil
	}
	var eds *ErrDeletedSlot
	var eir *ErrInvalidRID
	if errors.As(err, &eds) || errors.As(err, &eir) {
		return nil, false, nil
	}
	return nil, false, err
}

// Update replaces the row at rid with newRow (delete-then-reinsert). Open
// indexes are updated: the old column value is removed and the new one added.
//   - (newRID, true, nil)   — updated; newRID is the new stable address.
//   - (RID{}, false, nil)   — row was deleted or rid is out of range.
//   - (RID{}, false, err)   — I/O or encoding error.
func (t *TableHandle) Update(rid RID, newRow Row) (RID, bool, error) {
	// Fetch old row so we can remove its indexed values.
	var oldRow Row
	if len(t.indexes) > 0 {
		var found bool
		var err error
		oldRow, found, err = t.Get(rid)
		if err != nil {
			return RID{}, false, err
		}
		if !found {
			return RID{}, false, nil
		}
	}

	newRID, err := t.heap.Update(t.schema, rid, newRow)
	if err == nil {
		for _, idx := range t.indexes {
			idx.Delete(oldRow[idx.colIndex], rid)
			_ = idx.Insert(newRow[idx.colIndex], newRID)
		}
		return newRID, true, nil
	}
	var eds *ErrDeletedSlot
	var eir *ErrInvalidRID
	if errors.As(err, &eds) || errors.As(err, &eir) {
		return RID{}, false, nil
	}
	return RID{}, false, err
}

// Delete marks the row at rid as deleted (tombstone). Open indexes are updated
// by removing the corresponding entry.
//   - (true, nil)  — row deleted.
//   - (false, nil) — row was already deleted or rid is out of range.
//   - (false, err) — I/O error.
func (t *TableHandle) Delete(rid RID) (bool, error) {
	// Fetch old row so we can remove its indexed values.
	var oldRow Row
	if len(t.indexes) > 0 {
		var found bool
		var err error
		oldRow, found, err = t.Get(rid)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
	}

	err := t.heap.Delete(t.schema, rid)
	if err == nil {
		for _, idx := range t.indexes {
			idx.Delete(oldRow[idx.colIndex], rid)
		}
		return true, nil
	}
	var eds *ErrDeletedSlot
	var eir *ErrInvalidRID
	if errors.As(err, &eds) || errors.As(err, &eir) {
		return false, nil
	}
	return false, err
}

// Scan returns a pull-iterator over all live rows in page order.
//
//	s := table.Scan()
//	for s.Next() {
//	    rid, row := s.RID(), s.Row()
//	}
//	if err := s.Err(); err != nil { ... }
func (t *TableHandle) Scan() *Scanner {
	return newScanner(t)
}

// ---- Index management ---------------------------------------------------------

// CreateIndex builds a B-tree index on colName, scanning the table to
// populate it, then persisting it to <tableName>.<colName>.idx. If unique is
// true, the index enforces one RID per distinct value.
// Returns ErrIndexExists if an index on that column already exists.
func (t *TableHandle) CreateIndex(colName string, unique bool) error {
	ci, ok := t.schema.ColumnIndex(colName)
	if !ok {
		return fmt.Errorf("storage: CreateIndex: column %q not found in table %q", colName, t.name)
	}
	if t.indexes == nil {
		t.indexes = make(map[string]*Index)
	}
	if _, exists := t.indexes[colName]; exists {
		return &ErrIndexExists{Table: t.name, Column: colName}
	}

	dir := t.heap.dir
	idx, err := OpenIndex(dir, t.name, colName, ci, unique)
	if err != nil {
		return fmt.Errorf("storage: CreateIndex %q.%q: %w", t.name, colName, err)
	}

	// Populate from heap.
	err = t.heap.Scan(t.schema, func(rid RID, row Row) (bool, error) {
		val := row[ci]
		if ierr := idx.Insert(val, rid); ierr != nil {
			return false, ierr
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("storage: CreateIndex %q.%q: scan: %w", t.name, colName, err)
	}

	if err := idx.Checkpoint(); err != nil {
		return fmt.Errorf("storage: CreateIndex %q.%q: checkpoint: %w", t.name, colName, err)
	}
	t.indexes[colName] = idx
	return nil
}

// DropIndex removes the index on colName from memory and deletes its file.
// Returns ErrIndexNotFound if no index exists for that column.
func (t *TableHandle) DropIndex(colName string) error {
	idx, ok := t.indexes[colName]
	if !ok {
		return &ErrIndexNotFound{Table: t.name, Column: colName}
	}
	delete(t.indexes, colName)
	path := idx.indexPath()
	_ = idx.Close() // flush any in-memory state; ignore error on drop
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: DropIndex %q.%q: remove file: %w", t.name, colName, err)
	}
	return nil
}

// HasIndex reports whether a column index currently exists for colName.
func (t *TableHandle) HasIndex(colName string) bool {
	_, ok := t.indexes[colName]
	return ok
}

// LookupExact returns all RIDs whose value for colName equals val.
// Returns ErrIndexNotFound if no index exists for that column.
func (t *TableHandle) LookupExact(colName string, val Value) ([]RID, error) {
	idx, ok := t.indexes[colName]
	if !ok {
		return nil, &ErrIndexNotFound{Table: t.name, Column: colName}
	}
	return idx.Lookup(val), nil
}

// RangeScan iterates over all live rows whose value for colName falls in
// [lo, hi] (nil = open-ended bound) and calls fn for each matching row.
// Returns ErrIndexNotFound if no index exists for that column.
func (t *TableHandle) RangeScan(colName string, lo, hi *Value,
	fn func(RID, Row) (bool, error)) error {
	idx, ok := t.indexes[colName]
	if !ok {
		return &ErrIndexNotFound{Table: t.name, Column: colName}
	}

	var iterErr error
	idx.RangeScan(lo, hi, func(e IndexEntry) bool {
		row, found, err := t.Get(e.RID)
		if err != nil {
			iterErr = err
			return false
		}
		if !found {
			return true // row deleted; skip
		}
		cont, err := fn(e.RID, row)
		if err != nil {
			iterErr = err
			return false
		}
		return cont
	})
	return iterErr
}

// ---- package-internal helpers ------------------------------------------------

// attachIndex adds an already-opened Index to this handle. Called by DB when
// re-opening a table that has persisted index files.
func (t *TableHandle) attachIndex(idx *Index) {
	if t.indexes == nil {
		t.indexes = make(map[string]*Index)
	}
	t.indexes[idx.col] = idx
}

// closeHeap shuts down the underlying HeapFile and all open indexes.
// Called by DB, not callers.
// Flush syncs the heap and all indexes to disk, ensuring all previous writes
// are durable.
func (t *TableHandle) Flush() error {
	if err := t.heap.Flush(); err != nil {
		return err
	}
	for _, idx := range t.indexes {
		if err := idx.Checkpoint(); err != nil {
			return err
		}
	}
	return nil
}

func (t *TableHandle) closeHeap() error {
	var firstErr error
	for _, idx := range t.indexes {
		if err := idx.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := t.heap.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// ---- Scanner -----------------------------------------------------------------

type scanRow struct {
	rid RID
	row Row
}

// Scanner is a pull-based iterator over all live rows in a table, in page
// order. It follows the bufio.Scanner convention: call Next() in a loop,
// then check Err() after the loop ends.
type Scanner struct {
	rows []scanRow
	pos  int
	err  error
}

func newScanner(t *TableHandle) *Scanner {
	s := &Scanner{pos: -1}
	var rows []scanRow
	err := t.heap.Scan(t.schema, func(rid RID, row Row) (bool, error) {
		rows = append(rows, scanRow{rid: rid, row: row})
		return true, nil
	})
	if err != nil {
		s.err = err
		return s
	}
	s.rows = rows
	return s
}

// Next advances to the next live row. Returns false when exhausted or on error.
func (s *Scanner) Next() bool {
	if s.err != nil {
		return false
	}
	s.pos++
	return s.pos < len(s.rows)
}

// RID returns the current row's identifier. Undefined if Next() returned false.
func (s *Scanner) RID() RID { return s.rows[s.pos].rid }

// Row returns the current row's data. Undefined if Next() returned false.
func (s *Scanner) Row() Row { return s.rows[s.pos].row }

// Err returns the error that caused Next() to return false, if any.
func (s *Scanner) Err() error { return s.err }
