package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vvshulga/db_internals/storage"
)

func setupEngineDB(t *testing.T) (*Engine, *storage.DB) {
	t.Helper()
	db, err := storage.OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewEngine(db), db
}

func mustCreateTable(t *testing.T, db *storage.DB) {
	t.Helper()
	schema, err := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
		{Name: "score", Type: storage.TypeBIGINT},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	if _, err := db.CreateTable("users", schema); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
}

func TestEngine_Execute_CreateTable(t *testing.T) {
	eng, _ := setupEngineDB(t)

	rs, err := eng.Execute("CREATE TABLE products (id INT, title VARCHAR(128))")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 result row, got %d", len(rs.Rows))
	}
	msg := rs.Rows[0][0].AsString()
	if !strings.Contains(msg, "products") {
		t.Errorf("expected message to mention table name, got %q", msg)
	}
}

func TestEngine_Execute_InsertAndSelect(t *testing.T) {
	eng, db := setupEngineDB(t)
	mustCreateTable(t, db)

	// INSERT
	rs, err := eng.Execute("INSERT INTO users VALUES (1, 'Alice', 100)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 result row from INSERT, got %d", len(rs.Rows))
	}

	// INSERT second row
	if _, err := eng.Execute("INSERT INTO users VALUES (2, 'Bob', 200)"); err != nil {
		t.Fatalf("INSERT 2: %v", err)
	}

	// SELECT *
	rs, err = eng.Execute("SELECT * FROM users")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(rs.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rs.Rows))
	}
	if rs.Schema.NumColumns() != 3 {
		t.Fatalf("expected 3 columns, got %d", rs.Schema.NumColumns())
	}

	// Check first row values
	if rs.Rows[0][0].AsInt() != 1 {
		t.Errorf("row[0].id: want 1, got %v", rs.Rows[0][0].AsInt())
	}
	if rs.Rows[0][1].AsString() != "Alice" {
		t.Errorf("row[0].name: want Alice, got %v", rs.Rows[0][1].AsString())
	}
	if rs.Rows[0][2].AsBigInt() != 100 {
		t.Errorf("row[0].score: want 100, got %v", rs.Rows[0][2].AsBigInt())
	}
}

func TestEngine_Execute_Update(t *testing.T) {
	eng, db := setupEngineDB(t)
	mustCreateTable(t, db)

	if _, err := eng.Execute("INSERT INTO users VALUES (1, 'Alice', 100)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rs, err := eng.Execute("UPDATE users SET score = 999 WHERE id = 1")
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 result row from UPDATE, got %d", len(rs.Rows))
	}
	if rs.Rows[0][0].AsBigInt() != 1 {
		t.Errorf("updated_rows: want 1, got %v", rs.Rows[0][0].AsBigInt())
	}

	// Verify the update
	sel, err := eng.Execute("SELECT * FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row after update, got %d", len(sel.Rows))
	}
	if sel.Rows[0][2].AsBigInt() != 999 {
		t.Errorf("score after update: want 999, got %v", sel.Rows[0][2].AsBigInt())
	}
}

func TestEngine_Execute_Delete(t *testing.T) {
	eng, db := setupEngineDB(t)
	mustCreateTable(t, db)

	if _, err := eng.Execute("INSERT INTO users VALUES (1, 'Alice', 100)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if _, err := eng.Execute("INSERT INTO users VALUES (2, 'Bob', 200)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rs, err := eng.Execute("DELETE FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if rs.Rows[0][0].AsBigInt() != 1 {
		t.Errorf("deleted_rows: want 1, got %v", rs.Rows[0][0].AsBigInt())
	}

	// Verify only Bob remains
	sel, err := eng.Execute("SELECT * FROM users")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row after delete, got %d", len(sel.Rows))
	}
	if sel.Rows[0][0].AsInt() != 2 {
		t.Errorf("remaining id: want 2, got %v", sel.Rows[0][0].AsInt())
	}
}

func TestEngine_ExecuteScript(t *testing.T) {
	eng, _ := setupEngineDB(t)

	script := `CREATE TABLE items (id INT, label VARCHAR(64));
INSERT INTO items VALUES (1, 'first');
SELECT * FROM items`

	results, err := eng.ExecuteScript(script)
	if err != nil {
		t.Fatalf("ExecuteScript: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 ResultSets, got %d", len(results))
	}

	// CREATE TABLE → message row
	if len(results[0].Rows) != 1 {
		t.Errorf("CREATE TABLE: expected 1 row, got %d", len(results[0].Rows))
	}

	// INSERT → inserted_page_id row
	if len(results[1].Rows) != 1 {
		t.Errorf("INSERT: expected 1 row, got %d", len(results[1].Rows))
	}

	// SELECT → the inserted row
	if len(results[2].Rows) != 1 {
		t.Errorf("SELECT: expected 1 row, got %d", len(results[2].Rows))
	}
	if results[2].Rows[0][1].AsString() != "first" {
		t.Errorf("SELECT label: want 'first', got %q", results[2].Rows[0][1].AsString())
	}
}

func TestEngine_Execute_ParseError(t *testing.T) {
	eng, _ := setupEngineDB(t)

	_, err := eng.Execute("NOT VALID SQL !!!!")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestEngine_Execute_UnknownTable(t *testing.T) {
	eng, _ := setupEngineDB(t)

	_, err := eng.Execute("SELECT * FROM nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown table, got nil")
	}
}

func TestEngine_ResultSet_Print(t *testing.T) {
	eng, db := setupEngineDB(t)
	mustCreateTable(t, db)

	if _, err := eng.Execute("INSERT INTO users VALUES (42, 'TestUser', 777)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rs, err := eng.Execute("SELECT * FROM users")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}

	var buf strings.Builder
	rs.Print(&buf)
	output := buf.String()

	if !strings.Contains(output, "id | name | score") {
		t.Errorf("expected column headers in output, got:\n%s", output)
	}
	if !strings.Contains(output, "42") {
		t.Errorf("expected row value '42' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "TestUser") {
		t.Errorf("expected row value 'TestUser' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "1 row(s) returned") {
		t.Errorf("expected row count in output, got:\n%s", output)
	}
}

// setupEngineDBInSubdir opens the DB inside a named subdirectory so that
// filepath.Dir(db.Dir()) is a real parent where sibling databases can be created.
func setupEngineDBInSubdir(t *testing.T) (*Engine, *storage.DB, string) {
	t.Helper()
	parentDir := t.TempDir()
	db, err := storage.OpenDB(filepath.Join(parentDir, "main"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewEngine(db), db, parentDir
}

func TestEngine_Execute_DropTable(t *testing.T) {
	eng, db := setupEngineDB(t)
	mustCreateTable(t, db)

	rs, err := eng.Execute("DROP TABLE users")
	if err != nil {
		t.Fatalf("DROP TABLE: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 result row, got %d", len(rs.Rows))
	}
	if !strings.Contains(rs.Rows[0][0].AsString(), "users") {
		t.Errorf("expected message to mention table name, got %q", rs.Rows[0][0].AsString())
	}
	// Table should no longer exist
	if _, err := eng.Execute("SELECT * FROM users"); err == nil {
		t.Fatal("expected error after DROP TABLE, got nil")
	}
}

func TestEngine_Execute_DropTable_NotFound(t *testing.T) {
	eng, _ := setupEngineDB(t)
	if _, err := eng.Execute("DROP TABLE nonexistent"); err == nil {
		t.Fatal("expected error for unknown table, got nil")
	}
}

func TestEngine_Execute_CreateDatabase(t *testing.T) {
	eng, _, parentDir := setupEngineDBInSubdir(t)

	rs, err := eng.Execute("CREATE DATABASE sibling")
	if err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 result row, got %d", len(rs.Rows))
	}
	if !strings.Contains(rs.Rows[0][0].AsString(), "sibling") {
		t.Errorf("expected message to mention db name, got %q", rs.Rows[0][0].AsString())
	}
	if _, err := os.Stat(filepath.Join(parentDir, "sibling")); err != nil {
		t.Errorf("expected sibling directory to exist: %v", err)
	}
}

func TestEngine_Execute_CreateDatabase_AlreadyExists(t *testing.T) {
	eng, _, _ := setupEngineDBInSubdir(t)

	if _, err := eng.Execute("CREATE DATABASE sibling"); err != nil {
		t.Fatalf("first CREATE DATABASE: %v", err)
	}
	if _, err := eng.Execute("CREATE DATABASE sibling"); err == nil {
		t.Fatal("expected error on duplicate CREATE DATABASE, got nil")
	}
}

func TestEngine_Execute_DropDatabase(t *testing.T) {
	eng, _, parentDir := setupEngineDBInSubdir(t)

	if _, err := eng.Execute("CREATE DATABASE sibling"); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	rs, err := eng.Execute("DROP DATABASE sibling")
	if err != nil {
		t.Fatalf("DROP DATABASE: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 result row, got %d", len(rs.Rows))
	}
	if !strings.Contains(rs.Rows[0][0].AsString(), "sibling") {
		t.Errorf("expected message to mention db name, got %q", rs.Rows[0][0].AsString())
	}
	if _, err := os.Stat(filepath.Join(parentDir, "sibling")); !os.IsNotExist(err) {
		t.Errorf("expected sibling directory to be removed")
	}
}

func TestEngine_Execute_DropDatabase_NotFound(t *testing.T) {
	eng, _, _ := setupEngineDBInSubdir(t)
	if _, err := eng.Execute("DROP DATABASE nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent database, got nil")
	}
}

func TestEngine_Execute_RenameDatabase(t *testing.T) {
	eng, _, parentDir := setupEngineDBInSubdir(t)

	if _, err := eng.Execute("CREATE DATABASE oldname"); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	rs, err := eng.Execute("RENAME DATABASE oldname TO newname")
	if err != nil {
		t.Fatalf("RENAME DATABASE: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 result row, got %d", len(rs.Rows))
	}
	msg := rs.Rows[0][0].AsString()
	if !strings.Contains(msg, "oldname") || !strings.Contains(msg, "newname") {
		t.Errorf("unexpected rename message: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(parentDir, "oldname")); !os.IsNotExist(err) {
		t.Errorf("expected oldname directory to be removed")
	}
	if _, err := os.Stat(filepath.Join(parentDir, "newname")); err != nil {
		t.Errorf("expected newname directory to exist: %v", err)
	}
}
