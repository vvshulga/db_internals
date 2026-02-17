package storage

import (
	"fmt"
	"testing"
)

// ---- helpers -----------------------------------------------------------------

func openTestTable(t *testing.T) *TableHandle {
	t.Helper()
	db := openDB(t)
	schema := simpleSchema(t)
	tbl, err := db.CreateTable("t", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	return tbl
}

func trow(id int32, name string) Row {
	return Row{NewIntValue(id), NewVarcharValue(name)}
}

// ---- Insert / Get -----------------------------------------------------------

func TestTableHandle_InsertGet(t *testing.T) {
	tbl := openTestTable(t)
	want := trow(42, "Alice")
	rid, err := tbl.Insert(want)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: expected ok=true")
	}
	if got[0].AsInt() != 42 || got[1].AsString() != "Alice" {
		t.Errorf("Get: got %v, want %v", got, want)
	}
}

func TestTableHandle_Get_DeletedRow(t *testing.T) {
	tbl := openTestTable(t)
	rid, _ := tbl.Insert(trow(1, "Bob"))
	tbl.Delete(rid)

	got, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get after delete: unexpected error %v", err)
	}
	if ok {
		t.Errorf("Get after delete: expected ok=false, got %v", got)
	}
}

func TestTableHandle_Get_InvalidRID(t *testing.T) {
	tbl := openTestTable(t)
	_, ok, err := tbl.Get(RID{PageID: 999999, SlotID: 0})
	if err != nil {
		t.Fatalf("Get invalid RID: unexpected error %v", err)
	}
	if ok {
		t.Error("Get invalid RID: expected ok=false")
	}
}

// ---- Update -----------------------------------------------------------------

func TestTableHandle_Update(t *testing.T) {
	tbl := openTestTable(t)
	rid, _ := tbl.Insert(trow(1, "Carol"))
	newRID, ok, err := tbl.Update(rid, trow(1, "Carol-Updated"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !ok {
		t.Fatal("Update: expected ok=true")
	}
	// Old RID must be gone.
	_, oldOK, _ := tbl.Get(rid)
	if oldOK {
		t.Error("old RID still accessible after Update")
	}
	// New RID must have updated data.
	got, ok, err := tbl.Get(newRID)
	if err != nil || !ok {
		t.Fatalf("Get newRID: ok=%v err=%v", ok, err)
	}
	if got[1].AsString() != "Carol-Updated" {
		t.Errorf("updated name: got %q, want Carol-Updated", got[1].AsString())
	}
}

func TestTableHandle_Update_NotFound(t *testing.T) {
	tbl := openTestTable(t)
	rid, _ := tbl.Insert(trow(1, "Dave"))
	tbl.Delete(rid)

	_, ok, err := tbl.Update(rid, trow(1, "Dave2"))
	if err != nil {
		t.Fatalf("Update deleted row: unexpected error %v", err)
	}
	if ok {
		t.Error("Update deleted row: expected ok=false")
	}
}

// ---- Delete -----------------------------------------------------------------

func TestTableHandle_Delete(t *testing.T) {
	tbl := openTestTable(t)
	rid, _ := tbl.Insert(trow(5, "Eve"))

	ok, err := tbl.Delete(rid)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !ok {
		t.Error("Delete: expected ok=true")
	}
	// Second delete returns false (already deleted).
	ok2, err2 := tbl.Delete(rid)
	if err2 != nil {
		t.Fatalf("double delete: unexpected error %v", err2)
	}
	if ok2 {
		t.Error("double delete: expected ok=false")
	}
}

func TestTableHandle_Delete_InvalidRID(t *testing.T) {
	tbl := openTestTable(t)
	ok, err := tbl.Delete(RID{PageID: 0, SlotID: 0}) // meta page
	if err != nil {
		t.Fatalf("Delete invalid RID: unexpected error %v", err)
	}
	if ok {
		t.Error("Delete invalid RID: expected ok=false")
	}
}

// ---- Scan -------------------------------------------------------------------

func TestTableHandle_Scan_AllLive(t *testing.T) {
	tbl := openTestTable(t)
	const n = 15
	for i := 0; i < n; i++ {
		tbl.Insert(trow(int32(i), fmt.Sprintf("u%d", i)))
	}

	s := tbl.Scan()
	count := 0
	for s.Next() {
		count++
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if count != n {
		t.Errorf("Scan count: got %d, want %d", count, n)
	}
}

func TestTableHandle_Scan_SkipDeleted(t *testing.T) {
	tbl := openTestTable(t)
	rids := make([]RID, 10)
	for i := range rids {
		rid, _ := tbl.Insert(trow(int32(i), "u"))
		rids[i] = rid
	}
	// Delete every other row.
	for i := 1; i < len(rids); i += 2 {
		tbl.Delete(rids[i])
	}

	s := tbl.Scan()
	count := 0
	for s.Next() {
		count++
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if count != 5 {
		t.Errorf("Scan after deletes: got %d, want 5", count)
	}
}

func TestTableHandle_Scan_Empty(t *testing.T) {
	tbl := openTestTable(t)
	s := tbl.Scan()
	for s.Next() {
		t.Error("Scan on empty table should yield no rows")
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Scan empty: %v", err)
	}
}

func TestTableHandle_Scan_EarlyStop(t *testing.T) {
	tbl := openTestTable(t)
	for i := 0; i < 10; i++ {
		tbl.Insert(trow(int32(i), "u"))
	}

	s := tbl.Scan()
	count := 0
	for s.Next() {
		count++
		if count == 3 {
			break // stop early
		}
	}
	if count != 3 {
		t.Errorf("early stop: got %d iterations, want 3", count)
	}
}

func TestTableHandle_Scan_RIDAndRowConsistent(t *testing.T) {
	tbl := openTestTable(t)
	inserted := make(map[RID]int32)
	for i := int32(0); i < 5; i++ {
		rid, _ := tbl.Insert(trow(i, fmt.Sprintf("user%d", i)))
		inserted[rid] = i
	}

	s := tbl.Scan()
	for s.Next() {
		rid, row := s.RID(), s.Row()
		wantID, exists := inserted[rid]
		if !exists {
			t.Errorf("Scan returned unexpected RID %v", rid)
			continue
		}
		if row[0].AsInt() != wantID {
			t.Errorf("RID %v: got id=%d, want %d", rid, row[0].AsInt(), wantID)
		}
	}
}
