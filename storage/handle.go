package storage

import "errors"

// TableHandle is an open reference to a table. It bundles the schema and
// HeapFile so callers do not need to pass the schema on every operation.
//
// TableHandle instances are created and cached by DB. Do not call closeHeap
// directly; use DB.Close() or DB.DropTable() to release resources.
type TableHandle struct {
	name   string
	schema *Schema
	heap   *HeapFile
}

// Name returns the table's name.
func (t *TableHandle) Name() string { return t.name }

// Schema returns the table's schema.
func (t *TableHandle) Schema() *Schema { return t.schema }

// Insert encodes and stores row, returning its stable RID.
func (t *TableHandle) Insert(row Row) (RID, error) {
	return t.heap.Insert(t.schema, row)
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

// Update replaces the row at rid with newRow (delete-then-reinsert).
//   - (newRID, true, nil)   — updated; newRID is the new stable address.
//   - (RID{}, false, nil)   — row was deleted or rid is out of range.
//   - (RID{}, false, err)   — I/O or encoding error.
func (t *TableHandle) Update(rid RID, newRow Row) (RID, bool, error) {
	newRID, err := t.heap.Update(t.schema, rid, newRow)
	if err == nil {
		return newRID, true, nil
	}
	var eds *ErrDeletedSlot
	var eir *ErrInvalidRID
	if errors.As(err, &eds) || errors.As(err, &eir) {
		return RID{}, false, nil
	}
	return RID{}, false, err
}

// Delete marks the row at rid as deleted (tombstone).
//   - (true, nil)  — row deleted.
//   - (false, nil) — row was already deleted or rid is out of range.
//   - (false, err) — I/O error.
func (t *TableHandle) Delete(rid RID) (bool, error) {
	err := t.heap.Delete(rid)
	if err == nil {
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

// closeHeap shuts down the underlying HeapFile. Called by DB, not callers.
func (t *TableHandle) closeHeap() error {
	return t.heap.Close()
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
