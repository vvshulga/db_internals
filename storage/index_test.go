package storage

import (
	"errors"
	"fmt"
	"testing"
)

// ---- helpers -----------------------------------------------------------------

func openIndexTable(t *testing.T) (*DB, *TableHandle) {
	t.Helper()
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("users", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	return db, tbl
}

func idRow(id int32, name string) Row {
	return Row{NewIntValue(id), NewVarcharValue(name)}
}

// ---- Functional tests --------------------------------------------------------

// TestIndex_CreateAndLookup verifies that CreateIndex + LookupExact return the
// correct RID after inserting rows.
func TestIndex_CreateAndLookup(t *testing.T) {
	_, tbl := openIndexTable(t)

	rid1, _ := tbl.Insert(idRow(1, "Alice"))
	rid2, _ := tbl.Insert(idRow(2, "Bob"))

	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	rids, err := tbl.LookupExact("id", NewIntValue(1))
	if err != nil {
		t.Fatalf("LookupExact id=1: %v", err)
	}
	if len(rids) != 1 || rids[0] != rid1 {
		t.Errorf("expected [%v], got %v", rid1, rids)
	}

	rids, err = tbl.LookupExact("id", NewIntValue(2))
	if err != nil {
		t.Fatalf("LookupExact id=2: %v", err)
	}
	if len(rids) != 1 || rids[0] != rid2 {
		t.Errorf("expected [%v], got %v", rid2, rids)
	}
}

// TestIndex_DuplicateValues verifies that a non-unique index stores all RIDs
// for the same indexed value.
func TestIndex_DuplicateValues(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("name", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	rid1, _ := tbl.Insert(idRow(1, "Alice"))
	rid2, _ := tbl.Insert(idRow(2, "Alice")) // same name

	rids, err := tbl.LookupExact("name", NewVarcharValue("Alice"))
	if err != nil {
		t.Fatalf("LookupExact: %v", err)
	}
	got := map[RID]bool{rids[0]: true, rids[1]: true}
	if !got[rid1] || !got[rid2] || len(rids) != 2 {
		t.Errorf("expected both RIDs %v %v, got %v", rid1, rid2, rids)
	}
}

// TestIndex_DeleteUpdatesIndex verifies that deleting a row removes its entry
// from the index.
func TestIndex_DeleteUpdatesIndex(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	rid, _ := tbl.Insert(idRow(42, "Eve"))
	if _, err := tbl.Delete(rid); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	rids, err := tbl.LookupExact("id", NewIntValue(42))
	if err != nil {
		t.Fatalf("LookupExact: %v", err)
	}
	if len(rids) != 0 {
		t.Errorf("expected empty after delete, got %v", rids)
	}
}

// TestIndex_UpdateUpdatesIndex verifies that updating a row moves its index
// entry from the old value to the new value.
func TestIndex_UpdateUpdatesIndex(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	rid, _ := tbl.Insert(idRow(10, "Dan"))
	newRID, _, err := tbl.Update(rid, idRow(99, "Dan"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Old value gone.
	rids, _ := tbl.LookupExact("id", NewIntValue(10))
	if len(rids) != 0 {
		t.Errorf("old value still present: %v", rids)
	}
	// New value present.
	rids, _ = tbl.LookupExact("id", NewIntValue(99))
	if len(rids) != 1 || rids[0] != newRID {
		t.Errorf("expected newRID %v, got %v", newRID, rids)
	}
}

// TestIndex_RangeScan verifies that RangeScan returns only rows whose indexed
// value falls within [lo, hi] in ascending order.
func TestIndex_RangeScan(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	for i := int32(1); i <= 10; i++ {
		tbl.Insert(idRow(i, fmt.Sprintf("u%d", i)))
	}

	lo := NewIntValue(3)
	hi := NewIntValue(7)
	var ids []int32
	err := tbl.RangeScan("id", &lo, &hi, func(_ RID, row Row) (bool, error) {
		ids = append(ids, row[0].AsInt())
		return true, nil
	})
	if err != nil {
		t.Fatalf("RangeScan: %v", err)
	}
	if len(ids) != 5 {
		t.Fatalf("expected 5 rows, got %d: %v", len(ids), ids)
	}
	for i, id := range ids {
		if id != int32(i+3) {
			t.Errorf("ids[%d] = %d, want %d", i, id, i+3)
		}
	}
}

// TestIndex_NullValues verifies that NULL values are correctly indexed and
// retrievable via LookupExact.
func TestIndex_NullValues(t *testing.T) {
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "tag", Type: TypeVARCHAR, MaxLen: 32, Nullable: true},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("t", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if err := tbl.CreateIndex("tag", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	ridNull, _ := tbl.Insert(Row{NewIntValue(1), NewNullValue()})
	tbl.Insert(Row{NewIntValue(2), NewVarcharValue("x")})

	rids, err := tbl.LookupExact("tag", NewNullValue())
	if err != nil {
		t.Fatalf("LookupExact NULL: %v", err)
	}
	if len(rids) != 1 || rids[0] != ridNull {
		t.Errorf("expected [%v], got %v", ridNull, rids)
	}
}

// TestIndex_Persistence verifies that Checkpoint + DB reopen + OpenTable
// correctly restores the index from disk.
func TestIndex_Persistence(t *testing.T) {
	dir := t.TempDir()

	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("users", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	rid, _ := tbl.Insert(idRow(7, "Grace"))
	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen
	db2, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB 2: %v", err)
	}
	defer db2.Close()
	tbl2, err := db2.OpenTable("users")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}

	if !tbl2.HasIndex("id") {
		t.Fatal("index not auto-loaded after reopen")
	}
	rids, err := tbl2.LookupExact("id", NewIntValue(7))
	if err != nil {
		t.Fatalf("LookupExact: %v", err)
	}
	if len(rids) != 1 || rids[0] != rid {
		t.Errorf("expected [%v], got %v", rid, rids)
	}
}

// TestIndex_UniqueViolation verifies that a unique index rejects a second
// insert with the same value.
func TestIndex_UniqueViolation(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("id", true); err != nil {
		t.Fatalf("CreateIndex unique: %v", err)
	}

	if _, err := tbl.Insert(idRow(5, "Hank")); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err := tbl.Insert(idRow(5, "Iris"))
	var uv *ErrUniqueViolation
	if !errors.As(err, &uv) {
		t.Fatalf("expected ErrUniqueViolation, got %T: %v", err, err)
	}
	if uv.Column != "id" {
		t.Errorf("Column: got %q, want %q", uv.Column, "id")
	}

	// Heap row inserted before unique check fails should have been rolled back.
	// Verify via scan: only 1 live row with id=5.
	var count int
	s := tbl.Scan()
	for s.Next() {
		if s.Row()[0].AsInt() == 5 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 row with id=5 after violation, got %d", count)
	}
}

// TestIndex_DropIndex verifies that DropIndex removes the file and makes
// subsequent LookupExact return ErrIndexNotFound.
func TestIndex_DropIndex(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	if err := tbl.DropIndex("id"); err != nil {
		t.Fatalf("DropIndex: %v", err)
	}
	if tbl.HasIndex("id") {
		t.Error("HasIndex should be false after drop")
	}
	_, err := tbl.LookupExact("id", NewIntValue(1))
	var enf *ErrIndexNotFound
	if !errors.As(err, &enf) {
		t.Fatalf("expected ErrIndexNotFound, got %T: %v", err, err)
	}
}

// TestIndex_AutoOpenOnReopenDB verifies the full lifecycle: data + index
// persist across close/reopen and the auto-open path in OpenTable works.
func TestIndex_AutoOpenOnReopenDB(t *testing.T) {
	dir := t.TempDir()

	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("users", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Insert 20 rows and build an index.
	rids := make([]RID, 20)
	for i := 0; i < 20; i++ {
		rids[i], _ = tbl.Insert(idRow(int32(i), fmt.Sprintf("user%d", i)))
	}
	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and verify every row can be found via index.
	db2, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB 2: %v", err)
	}
	defer db2.Close()
	tbl2, err := db2.OpenTable("users")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}

	for i := 0; i < 20; i++ {
		got, err := tbl2.LookupExact("id", NewIntValue(int32(i)))
		if err != nil {
			t.Fatalf("LookupExact %d: %v", i, err)
		}
		if len(got) != 1 || got[0] != rids[i] {
			t.Errorf("id=%d: expected [%v], got %v", i, rids[i], got)
		}
	}
}

// TestIndex_IndexExists verifies that creating an index twice returns
// ErrIndexExists.
func TestIndex_IndexExists(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("first CreateIndex: %v", err)
	}
	err := tbl.CreateIndex("id", false)
	var ei *ErrIndexExists
	if !errors.As(err, &ei) {
		t.Fatalf("expected ErrIndexExists, got %T: %v", err, err)
	}
}

// TestIndex_RangeScanOpenEnded verifies open-ended range queries (nil lo / hi).
func TestIndex_RangeScanOpenEnded(t *testing.T) {
	_, tbl := openIndexTable(t)

	if err := tbl.CreateIndex("id", false); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	for i := int32(1); i <= 5; i++ {
		tbl.Insert(idRow(i, fmt.Sprintf("u%d", i)))
	}

	// nil lo → from the beginning
	hi := NewIntValue(3)
	var ids []int32
	tbl.RangeScan("id", nil, &hi, func(_ RID, row Row) (bool, error) {
		ids = append(ids, row[0].AsInt())
		return true, nil
	})
	if len(ids) != 3 {
		t.Errorf("nil lo: expected 3, got %d: %v", len(ids), ids)
	}

	// nil hi → to the end
	lo := NewIntValue(4)
	ids = nil
	tbl.RangeScan("id", &lo, nil, func(_ RID, row Row) (bool, error) {
		ids = append(ids, row[0].AsInt())
		return true, nil
	})
	if len(ids) != 2 {
		t.Errorf("nil hi: expected 2, got %d: %v", len(ids), ids)
	}
}

// TestIndexUniquePersistedOnReload verifies that the unique constraint flag
// is persisted in the index file and restored correctly on DB reopen.
func TestIndexUniquePersistedOnReload(t *testing.T) {
	dir := t.TempDir()

	// Phase 1: Create unique index and insert data.
	var savedRID RID
	{
		db, err := OpenDB(dir)
		if err != nil {
			t.Fatalf("OpenDB: %v", err)
		}

		schema, err := NewSchema([]Column{
			{Name: "id", Type: TypeINT},
			{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
		})
		if err != nil {
			t.Fatalf("NewSchema: %v", err)
		}

		tbl, err := db.CreateTable("users", schema)
		if err != nil {
			t.Fatalf("CreateTable: %v", err)
		}

		// Insert a row.
		savedRID, err = tbl.Insert(idRow(42, "Alice"))
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}

		// Create unique index on id column.
		if err := tbl.CreateIndex("id", true); err != nil {
			t.Fatalf("CreateIndex: %v", err)
		}

		// Verify uniqueness works before closing.
		_, err = tbl.Insert(idRow(42, "Bob"))
		if err == nil {
			t.Fatal("Insert duplicate: expected unique violation, got nil")
		}
		var uniqueErr *ErrUniqueViolation
		if !errors.As(err, &uniqueErr) {
			t.Fatalf("Insert duplicate: expected ErrUniqueViolation, got %T: %v", err, err)
		}

		if err := db.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	// Phase 2: Reopen DB and verify unique constraint is still enforced.
	{
		db, err := OpenDB(dir)
		if err != nil {
			t.Fatalf("OpenDB (reopen): %v", err)
		}
		defer db.Close()

		tbl, err := db.OpenTable("users")
		if err != nil {
			t.Fatalf("OpenTable: %v", err)
		}

		// Verify the index was reloaded with unique=true.
		idx, ok := tbl.indexes["id"]
		if !ok {
			t.Fatal("index 'id' not found after reopen")
		}
		if !idx.unique {
			t.Fatal("index 'id' should be unique after reload, but unique=false")
		}

		// Verify data survived.
		row, ok, err := tbl.Get(savedRID)
		if err != nil {
			t.Fatalf("Get after reopen: %v", err)
		}
		if !ok {
			t.Fatal("Get after reopen: row not found")
		}
		if row[0].AsInt() != 42 || row[1].AsString() != "Alice" {
			t.Fatalf("Get after reopen: got %v, want (42, Alice)", row)
		}

		// Most importantly: verify uniqueness is still enforced.
		_, err = tbl.Insert(idRow(42, "Charlie"))
		if err == nil {
			t.Fatal("Insert duplicate after reopen: expected unique violation, got nil")
		}
		var uniqueErr *ErrUniqueViolation
		if !errors.As(err, &uniqueErr) {
			t.Fatalf("Insert duplicate after reopen: expected ErrUniqueViolation, got %T: %v", err, err)
		}

		// Verify different value works.
		rid2, err := tbl.Insert(idRow(99, "Dave"))
		if err != nil {
			t.Fatalf("Insert different id: %v", err)
		}
		if rid2 == savedRID {
			t.Fatal("Insert: got same RID for different row")
		}
	}
}
