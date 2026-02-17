package storage

import (
	"errors"
	"fmt"
	"testing"
)

// ---- test helpers ------------------------------------------------------------

func testSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
		{Name: "score", Type: TypeDOUBLE},
		{Name: "note", Type: TypeTEXT, Nullable: true},
	})
	if err != nil {
		t.Fatalf("testSchema: %v", err)
	}
	return s
}

func testRow(id int32, name string, score float64, note string) Row {
	var noteVal Value
	if note == "" {
		noteVal = NewNullValue()
	} else {
		noteVal = NewTextValue(note)
	}
	return Row{NewIntValue(id), NewVarcharValue(name), NewDoubleValue(score), noteVal}
}

func openHeap(t *testing.T) (*HeapFile, *Schema) {
	t.Helper()
	dir := t.TempDir()
	h, err := OpenHeapFile(dir, "test")
	if err != nil {
		t.Fatalf("OpenHeapFile: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	return h, testSchema(t)
}

func rowsEq(a, b Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- OpenHeapFile ------------------------------------------------------------

func TestOpenHeapFile_CreateNew(t *testing.T) {
	dir := t.TempDir()
	h, err := OpenHeapFile(dir, "users")
	if err != nil {
		t.Fatalf("OpenHeapFile: %v", err)
	}
	defer h.Close()
	if h.PageCount() != 2 {
		t.Errorf("PageCount: got %d, want 2", h.PageCount())
	}
}

func TestOpenHeapFile_Reopen(t *testing.T) {
	dir := t.TempDir()
	schema, _ := NewSchema([]Column{{Name: "id", Type: TypeINT}})

	h, _ := OpenHeapFile(dir, "users")
	rid, err := h.Insert(schema, Row{NewIntValue(99)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	h.Close()

	h2, err := OpenHeapFile(dir, "users")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer h2.Close()

	row, err := h2.Fetch(schema, rid)
	if err != nil {
		t.Fatalf("Fetch after reopen: %v", err)
	}
	if row[0].AsInt() != 99 {
		t.Errorf("fetched id: got %d, want 99", row[0].AsInt())
	}
}

// ---- Insert / Fetch ----------------------------------------------------------

func TestHeapFile_InsertFetch_Single(t *testing.T) {
	h, schema := openHeap(t)
	want := testRow(1, "Alice", 9.5, "top student")
	rid, err := h.Insert(schema, want)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if rid.PageID != 1 {
		t.Errorf("RID.PageID: got %d, want 1", rid.PageID)
	}
	got, err := h.Fetch(schema, rid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !rowsEq(got, want) {
		t.Errorf("fetched row mismatch:\n got  %v\n want %v", got, want)
	}
}

func TestHeapFile_InsertFetch_Multiple(t *testing.T) {
	h, schema := openHeap(t)
	const n = 50
	rids := make([]RID, n)
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = testRow(int32(i), fmt.Sprintf("user%d", i), float64(i)*0.1, "")
		rid, err := h.Insert(schema, rows[i])
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
		rids[i] = rid
	}
	for i := 0; i < n; i++ {
		got, err := h.Fetch(schema, rids[i])
		if err != nil {
			t.Fatalf("Fetch %d: %v", i, err)
		}
		if !rowsEq(got, rows[i]) {
			t.Errorf("row %d mismatch: got %v, want %v", i, got, rows[i])
		}
	}
}

func TestHeapFile_Insert_FillPage(t *testing.T) {
	// Use a schema with no var-length cols to make size predictable.
	schema, _ := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "val", Type: TypeBIGINT},
	})
	h, err := OpenHeapFile(t.TempDir(), "t")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	// Keep inserting until we get a second data page.
	for i := 0; i < 10000; i++ {
		row := Row{NewIntValue(int32(i)), NewBigIntValue(int64(i))}
		_, err := h.Insert(schema, row)
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
		if h.PageCount() > 2 {
			// Successfully spilled to page 2.
			return
		}
	}
	t.Error("never spilled to a second data page after 10000 inserts")
}

// ---- Delete ------------------------------------------------------------------

func TestHeapFile_Delete(t *testing.T) {
	h, schema := openHeap(t)
	rid, _ := h.Insert(schema, testRow(1, "Alice", 1.0, ""))
	if err := h.Delete(rid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := h.Fetch(schema, rid)
	var e *ErrDeletedSlot
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrDeletedSlot after delete, got %T: %v", err, err)
	}
}

func TestHeapFile_Delete_InvalidRID(t *testing.T) {
	h, _ := openHeap(t)
	err := h.Delete(RID{PageID: 0, SlotID: 0})
	var e *ErrInvalidRID
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrInvalidRID for meta page, got %T: %v", err, err)
	}
}

func TestHeapFile_Delete_AlreadyDeleted(t *testing.T) {
	h, schema := openHeap(t)
	rid, _ := h.Insert(schema, testRow(1, "Bob", 2.0, ""))
	h.Delete(rid)
	err := h.Delete(rid)
	var e *ErrDeletedSlot
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrDeletedSlot on double-delete, got %T: %v", err, err)
	}
}

// ---- Update ------------------------------------------------------------------

func TestHeapFile_Update(t *testing.T) {
	h, schema := openHeap(t)
	rid, _ := h.Insert(schema, testRow(1, "Alice", 1.0, ""))
	newRow := testRow(1, "Alice-Updated", 9.9, "promoted")
	newRID, err := h.Update(schema, rid, newRow)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Old RID must be dead.
	_, err = h.Fetch(schema, rid)
	var e *ErrDeletedSlot
	if !errors.As(err, &e) {
		t.Fatalf("old RID not deleted after Update: got %T: %v", err, err)
	}
	// New RID must return new data.
	got, err := h.Fetch(schema, newRID)
	if err != nil {
		t.Fatalf("Fetch new RID: %v", err)
	}
	if !rowsEq(got, newRow) {
		t.Errorf("updated row mismatch: got %v, want %v", got, newRow)
	}
}

// ---- Scan --------------------------------------------------------------------

func TestHeapFile_Scan_AllLive(t *testing.T) {
	h, schema := openHeap(t)
	const n = 20
	for i := 0; i < n; i++ {
		h.Insert(schema, testRow(int32(i), fmt.Sprintf("u%d", i), 0, ""))
	}
	count := 0
	err := h.Scan(schema, func(rid RID, row Row) (bool, error) {
		count++
		return true, nil
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if count != n {
		t.Errorf("Scan count: got %d, want %d", count, n)
	}
}

func TestHeapFile_Scan_SkipDeleted(t *testing.T) {
	h, schema := openHeap(t)
	const n = 10
	rids := make([]RID, n)
	for i := 0; i < n; i++ {
		rid, _ := h.Insert(schema, testRow(int32(i), fmt.Sprintf("u%d", i), 0, ""))
		rids[i] = rid
	}
	// Delete odd-indexed rows.
	for i := 1; i < n; i += 2 {
		h.Delete(rids[i])
	}
	count := 0
	h.Scan(schema, func(rid RID, row Row) (bool, error) {
		count++
		return true, nil
	})
	if count != n/2 {
		t.Errorf("Scan count after deletes: got %d, want %d", count, n/2)
	}
}

func TestHeapFile_Scan_EarlyExit(t *testing.T) {
	h, schema := openHeap(t)
	for i := 0; i < 10; i++ {
		h.Insert(schema, testRow(int32(i), "u", 0, ""))
	}
	count := 0
	h.Scan(schema, func(rid RID, row Row) (bool, error) {
		count++
		return count < 3, nil // stop after 3
	})
	if count != 3 {
		t.Errorf("Scan early exit: got %d iterations, want 3", count)
	}
}

func TestHeapFile_Scan_FnError(t *testing.T) {
	h, schema := openHeap(t)
	h.Insert(schema, testRow(1, "a", 0, ""))
	sentinel := fmt.Errorf("sentinel error")
	err := h.Scan(schema, func(rid RID, row Row) (bool, error) {
		return false, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("Scan: expected sentinel error, got %v", err)
	}
}

// ---- RID validation ----------------------------------------------------------

func TestHeapFile_Fetch_MetaPage(t *testing.T) {
	h, schema := openHeap(t)
	_, err := h.Fetch(schema, RID{PageID: 0, SlotID: 0})
	var e *ErrInvalidRID
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrInvalidRID for meta page, got %T: %v", err, err)
	}
}

func TestHeapFile_Fetch_OutOfRange(t *testing.T) {
	h, schema := openHeap(t)
	_, err := h.Fetch(schema, RID{PageID: 999999, SlotID: 0})
	var e *ErrInvalidRID
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrInvalidRID for out-of-range PageID, got %T: %v", err, err)
	}
}

// ---- Durability --------------------------------------------------------------

func TestHeapFile_Durability(t *testing.T) {
	dir := t.TempDir()
	schema := testSchema(t)
	rows := []Row{
		testRow(1, "Alice", 9.5, "a"),
		testRow(2, "Bob", 8.0, ""),
		testRow(3, "Carol", 7.5, "c"),
	}

	// Write and close.
	{
		h, err := OpenHeapFile(dir, "persist")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if _, err := h.Insert(schema, row); err != nil {
				t.Fatalf("Insert: %v", err)
			}
		}
		h.Close()
	}

	// Reopen and verify.
	{
		h, err := OpenHeapFile(dir, "persist")
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer h.Close()
		i := 0
		err = h.Scan(schema, func(rid RID, row Row) (bool, error) {
			if i >= len(rows) {
				return false, fmt.Errorf("more rows than expected")
			}
			if !rowsEq(row, rows[i]) {
				return false, fmt.Errorf("row %d mismatch: got %v, want %v", i, row, rows[i])
			}
			i++
			return true, nil
		})
		if err != nil {
			t.Fatalf("Scan after reopen: %v", err)
		}
		if i != len(rows) {
			t.Errorf("row count after reopen: got %d, want %d", i, len(rows))
		}
	}
}

// ---- Nullable columns --------------------------------------------------------

func TestHeapFile_NullableColumns(t *testing.T) {
	h, schema := openHeap(t)
	rowWithNull := testRow(7, "Dave", 5.0, "")  // note is NULL
	rowWithVal := testRow(8, "Eve", 6.0, "note") // note is non-NULL
	r1, _ := h.Insert(schema, rowWithNull)
	r2, _ := h.Insert(schema, rowWithVal)

	got1, _ := h.Fetch(schema, r1)
	if !got1[3].IsNull() {
		t.Errorf("col 3 should be NULL, got %v", got1[3])
	}
	got2, _ := h.Fetch(schema, r2)
	if got2[3].AsString() != "note" {
		t.Errorf("col 3 should be 'note', got %v", got2[3])
	}
}

// ---- All data types ----------------------------------------------------------

func TestHeapFile_AllDataTypes(t *testing.T) {
	schema, _ := NewSchema([]Column{
		{Name: "i", Type: TypeINT},
		{Name: "bi", Type: TypeBIGINT},
		{Name: "f", Type: TypeFLOAT},
		{Name: "d", Type: TypeDOUBLE},
		{Name: "b", Type: TypeBOOLEAN},
		{Name: "dt", Type: TypeDATETIME},
		{Name: "vc", Type: TypeVARCHAR, MaxLen: 32},
		{Name: "tx", Type: TypeTEXT},
	})
	h, err := OpenHeapFile(t.TempDir(), "allTypes")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	want := Row{
		NewIntValue(-1),
		NewBigIntValue(1 << 40),
		NewFloatValue(1.5),
		NewDoubleValue(3.14159),
		NewBooleanValue(true),
		NewDatetimeValue(1_700_000_000_000_000_000),
		NewVarcharValue("varchar value"),
		NewTextValue("text value"),
	}
	rid, err := h.Insert(schema, want)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := h.Fetch(schema, rid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !rowsEq(got, want) {
		t.Errorf("all-types round-trip:\n got  %v\n want %v", got, want)
	}
}
