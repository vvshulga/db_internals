package storage

import (
	"errors"
	"testing"
)

// ---- helpers -----------------------------------------------------------------

func simpleSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
	})
	if err != nil {
		t.Fatalf("simpleSchema: %v", err)
	}
	return s
}

func openDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ---- OpenDB -----------------------------------------------------------------

func TestOpenDB_NewDir(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Reopen on empty dir should succeed.
	db2, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	db2.Close()
}

// ---- CreateTable ------------------------------------------------------------

func TestCreateTable_Basic(t *testing.T) {
	db := openDB(t)
	_, err := db.CreateTable("users", simpleSchema(t))
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	names := db.TableNames()
	if len(names) != 1 || names[0] != "users" {
		t.Errorf("TableNames: got %v, want [users]", names)
	}
}

func TestCreateTable_ReturnsOpenHandle(t *testing.T) {
	db := openDB(t)
	tbl, err := db.CreateTable("orders", simpleSchema(t))
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if tbl == nil {
		t.Fatal("CreateTable returned nil handle")
	}
	if tbl.Name() != "orders" {
		t.Errorf("Name: got %q, want %q", tbl.Name(), "orders")
	}
}

func TestCreateTable_Duplicate(t *testing.T) {
	db := openDB(t)
	db.CreateTable("users", simpleSchema(t))
	_, err := db.CreateTable("users", simpleSchema(t))
	var e *ErrTableExists
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrTableExists, got %T: %v", err, err)
	}
	if e.Name != "users" {
		t.Errorf("ErrTableExists.Name: got %q, want %q", e.Name, "users")
	}
}

// ---- DropTable --------------------------------------------------------------

func TestDropTable_Basic(t *testing.T) {
	db := openDB(t)
	db.CreateTable("orders", simpleSchema(t))
	if err := db.DropTable("orders"); err != nil {
		t.Fatalf("DropTable: %v", err)
	}
	if names := db.TableNames(); len(names) != 0 {
		t.Errorf("TableNames after drop: got %v, want []", names)
	}
}

func TestDropTable_NotFound(t *testing.T) {
	db := openDB(t)
	err := db.DropTable("ghost")
	var e *ErrTableNotFound
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrTableNotFound, got %T: %v", err, err)
	}
}

func TestDropTable_RemovesHeapFiles(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir)

	schema := simpleSchema(t)
	tbl, _ := db.CreateTable("tmp", schema)
	// Insert a row to ensure the file is non-empty.
	tbl.Insert(Row{NewIntValue(1), NewVarcharValue("x")})

	if err := db.DropTable("tmp"); err != nil {
		t.Fatalf("DropTable: %v", err)
	}
	db.Close()

	// Reopen: table must not exist.
	db2, _ := OpenDB(dir)
	defer db2.Close()
	if names := db2.TableNames(); len(names) != 0 {
		t.Errorf("tables after drop+reopen: got %v", names)
	}
}

// ---- OpenTable --------------------------------------------------------------

func TestOpenTable_NotFound(t *testing.T) {
	db := openDB(t)
	_, err := db.OpenTable("ghost")
	var e *ErrTableNotFound
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrTableNotFound, got %T: %v", err, err)
	}
}

func TestOpenTable_ReturnsCachedHandle(t *testing.T) {
	db := openDB(t)
	db.CreateTable("users", simpleSchema(t))
	t1, _ := db.OpenTable("users")
	t2, _ := db.OpenTable("users")
	if t1 != t2 {
		t.Error("OpenTable should return the same cached TableHandle")
	}
}

// ---- TableNames -------------------------------------------------------------

func TestTableNames_Sorted(t *testing.T) {
	db := openDB(t)
	s := simpleSchema(t)
	db.CreateTable("zebra", s)
	db.CreateTable("apple", s)
	db.CreateTable("mango", s)

	names := db.TableNames()
	want := []string{"apple", "mango", "zebra"}
	if len(names) != len(want) {
		t.Fatalf("len: got %d, want %d", len(names), len(want))
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d]: got %q, want %q", i, names[i], want[i])
		}
	}
}

// ---- Persistence ------------------------------------------------------------

func TestDB_Persistence_TablesAvailableAfterReopen(t *testing.T) {
	dir := t.TempDir()
	schema := simpleSchema(t)

	// Create and close.
	{
		db, _ := OpenDB(dir)
		db.CreateTable("users", schema)
		db.Close()
	}

	// Reopen: table must still exist with the same schema.
	{
		db, err := OpenDB(dir)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer db.Close()

		names := db.TableNames()
		if len(names) != 1 || names[0] != "users" {
			t.Errorf("TableNames after reopen: got %v", names)
		}
		tbl, err := db.OpenTable("users")
		if err != nil {
			t.Fatalf("OpenTable after reopen: %v", err)
		}
		if tbl.Schema().NumColumns() != schema.NumColumns() {
			t.Errorf("column count after reopen: got %d, want %d",
				tbl.Schema().NumColumns(), schema.NumColumns())
		}
	}
}

func TestDB_Persistence_DataSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	schema := simpleSchema(t)
	var savedRID RID

	{
		db, _ := OpenDB(dir)
		tbl, _ := db.CreateTable("users", schema)
		rid, err := tbl.Insert(Row{NewIntValue(99), NewVarcharValue("Alice")})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		savedRID = rid
		db.Close()
	}

	{
		db, _ := OpenDB(dir)
		defer db.Close()
		tbl, _ := db.OpenTable("users")
		row, ok, err := tbl.Get(savedRID)
		if err != nil || !ok {
			t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
		}
		if row[0].AsInt() != 99 || row[1].AsString() != "Alice" {
			t.Errorf("row after reopen: got %v", row)
		}
	}
}
