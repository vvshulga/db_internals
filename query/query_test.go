package query

import (
	"io"
	"testing"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

func setupTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestQuery_CreateTable(t *testing.T) {
	db := setupTestDB(t)

	// Create table programmatically (parser syntax for CREATE TABLE with
	// parentheses is not fully supported yet)
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	_, err := db.CreateTable("users", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Verify table was created and can be queried
	sql := "SELECT * FROM users"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	if err := physical.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer physical.Close()

	// Table should be empty
	_, err = physical.Next()
	if err != io.EOF {
		t.Errorf("expected empty table, got: %v", err)
	}
}

func TestQuery_InsertAndSelect(t *testing.T) {
	db := setupTestDB(t)

	// Create table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	_, err := db.CreateTable("users", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Insert a row
	insertSQL := "INSERT INTO users VALUES (1, 'Alice')"
	nodes, _ := parser.ParseString(insertSQL)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)
	physical.Open()
	physical.Next()
	physical.Close()

	// Select it back
	selectSQL := "SELECT * FROM users"
	nodes, _ = parser.ParseString(selectSQL)
	logical, _ = NewPlanner(db).Plan(nodes[0])
	physical, _ = NewOptimizer(db).Optimize(logical)

	if err := physical.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsInt() != 1 {
		t.Errorf("expected id=1, got %d", row[0].AsInt())
	}
	if row[1].AsString() != "Alice" {
		t.Errorf("expected name='Alice', got %s", row[1].AsString())
	}

	// Should be no more rows
	_, err = physical.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestQuery_SelectWithWhere(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "age", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("users", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(25)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(30)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(35)})

	// SELECT with WHERE
	sql := "SELECT id FROM users WHERE age > 28"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	var ids []int32
	for {
		row, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		ids = append(ids, row[0].AsInt())
	}

	if len(ids) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(ids))
	}
	if ids[0] != 2 || ids[1] != 3 {
		t.Errorf("unexpected ids: %v", ids)
	}
}

func TestQuery_SelectWithLimit(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("numbers", schema)
	for i := int32(1); i <= 10; i++ {
		table.Insert(storage.Row{storage.NewIntValue(i)})
	}

	// SELECT with LIMIT
	sql := "SELECT id FROM numbers LIMIT 3"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	count := 0
	for {
		_, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		count++
	}

	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}
}

func TestQuery_IndexScan(t *testing.T) {
	db := setupTestDB(t)

	// Create table with index
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	table, _ := db.CreateTable("users", schema)
	table.CreateIndex("id", false)

	// Insert data
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewVarcharValue("Bob")})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewVarcharValue("Charlie")})

	// Query that should use index
	sql := "SELECT name FROM users WHERE id = 2"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	// Verify it's using index scan (the optimizer should have created a PhysicalIndexScan)
	// We can't directly check the type due to wrapping, but we can verify it works

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsString() != "Bob" {
		t.Errorf("expected Bob, got %s", row[0].AsString())
	}

	// Should be only one result
	_, err = physical.Next()
	if err != io.EOF {
		t.Errorf("expected single result, got more")
	}
}

func TestQuery_Update(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	table, _ := db.CreateTable("users", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewVarcharValue("Bob")})

	// UPDATE all rows
	sql := "UPDATE users SET name = 'Updated'"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	row, err := physical.Next()
	physical.Close()

	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	// Should return count = 2
	if row[0].AsBigInt() != 2 {
		t.Errorf("expected 2 rows updated, got %d", row[0].AsBigInt())
	}

	// Verify rows were updated
	selectSQL := "SELECT name FROM users"
	nodes, _ = parser.ParseString(selectSQL)
	logical, _ = NewPlanner(db).Plan(nodes[0])
	physical, _ = NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	for {
		row, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if row[0].AsString() != "Updated" {
			t.Errorf("expected 'Updated', got %s", row[0].AsString())
		}
	}
}

func TestQuery_UpdateWithWhere(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "age", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("users", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(25)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(30)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(35)})

	// UPDATE with WHERE
	sql := "UPDATE users SET age = 99 WHERE id = 2"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	row, err := physical.Next()
	physical.Close()

	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	// Should update only 1 row
	if row[0].AsBigInt() != 1 {
		t.Errorf("expected 1 row updated, got %d", row[0].AsBigInt())
	}

	// Verify only id=2 was updated
	selectSQL := "SELECT id, age FROM users"
	nodes, _ = parser.ParseString(selectSQL)
	logical, _ = NewPlanner(db).Plan(nodes[0])
	physical, _ = NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	for {
		row, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		id := row[0].AsInt()
		age := row[1].AsInt()

		if id == 2 && age != 99 {
			t.Errorf("expected id=2 to have age=99, got %d", age)
		}
		if id != 2 && age == 99 {
			t.Errorf("unexpected age=99 for id=%d", id)
		}
	}
}

func TestQuery_UpdateMultipleColumns(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
		{Name: "age", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("users", schema)
	table.Insert(storage.Row{
		storage.NewIntValue(1),
		storage.NewVarcharValue("Alice"),
		storage.NewIntValue(25),
	})

	// UPDATE multiple columns
	sql := "UPDATE users SET name = 'Updated', age = 99 WHERE id = 1"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	row, err := physical.Next()
	physical.Close()

	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	if row[0].AsBigInt() != 1 {
		t.Errorf("expected 1 row updated, got %d", row[0].AsBigInt())
	}

	// Verify both columns were updated
	selectSQL := "SELECT name, age FROM users WHERE id = 1"
	nodes, _ = parser.ParseString(selectSQL)
	logical, _ = NewPlanner(db).Plan(nodes[0])
	physical, _ = NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err = physical.Next()
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if row[0].AsString() != "Updated" {
		t.Errorf("expected name='Updated', got %s", row[0].AsString())
	}
	if row[1].AsInt() != 99 {
		t.Errorf("expected age=99, got %d", row[1].AsInt())
	}
}

func TestQuery_UpdateWithLimit(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "status", Type: storage.TypeVARCHAR, MaxLen: 32},
	})
	table, _ := db.CreateTable("tasks", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewVarcharValue("pending")})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewVarcharValue("pending")})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewVarcharValue("pending")})

	// UPDATE with LIMIT
	sql := "UPDATE tasks SET status = 'done' LIMIT 2"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	row, err := physical.Next()
	physical.Close()

	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	// Should update exactly 2 rows
	if row[0].AsBigInt() != 2 {
		t.Errorf("expected 2 rows updated, got %d", row[0].AsBigInt())
	}
}

func TestQuery_Delete(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	table, _ := db.CreateTable("users", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewVarcharValue("Bob")})

	// DELETE all rows
	sql := "DELETE FROM users"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	row, err := physical.Next()
	physical.Close()

	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Should delete 2 rows
	if row[0].AsBigInt() != 2 {
		t.Errorf("expected 2 rows deleted, got %d", row[0].AsBigInt())
	}

	// Verify table is empty
	selectSQL := "SELECT * FROM users"
	nodes, _ = parser.ParseString(selectSQL)
	logical, _ = NewPlanner(db).Plan(nodes[0])
	physical, _ = NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	_, err = physical.Next()
	if err != io.EOF {
		t.Errorf("expected empty table after DELETE, got: %v", err)
	}
}

func TestQuery_DeleteWithWhere(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "age", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("users", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(25)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(30)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(35)})

	// DELETE with WHERE
	sql := "DELETE FROM users WHERE age > 28"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	row, err := physical.Next()
	physical.Close()

	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Should delete 2 rows (age=30 and age=35)
	if row[0].AsBigInt() != 2 {
		t.Errorf("expected 2 rows deleted, got %d", row[0].AsBigInt())
	}

	// Verify only id=1 remains
	selectSQL := "SELECT id FROM users"
	nodes, _ = parser.ParseString(selectSQL)
	logical, _ = NewPlanner(db).Plan(nodes[0])
	physical, _ = NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err = physical.Next()
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if row[0].AsInt() != 1 {
		t.Errorf("expected id=1 to remain, got %d", row[0].AsInt())
	}

	// Should be no more rows
	_, err = physical.Next()
	if err != io.EOF {
		t.Errorf("expected only 1 row remaining")
	}
}

func TestQuery_DeleteWithLimit(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("items", schema)
	table.Insert(storage.Row{storage.NewIntValue(1)})
	table.Insert(storage.Row{storage.NewIntValue(2)})
	table.Insert(storage.Row{storage.NewIntValue(3)})
	table.Insert(storage.Row{storage.NewIntValue(4)})

	// DELETE with LIMIT
	sql := "DELETE FROM items LIMIT 2"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	row, err := physical.Next()
	physical.Close()

	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Should delete exactly 2 rows
	if row[0].AsBigInt() != 2 {
		t.Errorf("expected 2 rows deleted, got %d", row[0].AsBigInt())
	}

	// Verify 2 rows remain
	selectSQL := "SELECT COUNT(*) FROM items"
	// Note: COUNT not implemented yet, so we'll count manually
	selectSQL = "SELECT id FROM items"
	nodes, _ = parser.ParseString(selectSQL)
	logical, _ = NewPlanner(db).Plan(nodes[0])
	physical, _ = NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	count := 0
	for {
		_, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		count++
	}

	if count != 2 {
		t.Errorf("expected 2 rows remaining, got %d", count)
	}
}

func TestQuery_CountStar(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	table, _ := db.CreateTable("users", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewVarcharValue("Bob")})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewVarcharValue("Charlie")})

	// COUNT(*)
	sql := "SELECT COUNT(*) FROM users"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsBigInt() != 3 {
		t.Errorf("expected COUNT(*)=3, got %d", row[0].AsBigInt())
	}

	// Should be only one result
	_, err = physical.Next()
	if err != io.EOF {
		t.Errorf("expected single result")
	}
}

func TestQuery_CountColumn(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table with NULL values
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "score", Type: storage.TypeINT, Nullable: true},
	})
	table, _ := db.CreateTable("scores", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(100)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewNullValue()})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(85)})

	// COUNT(score) should skip NULLs
	sql := "SELECT COUNT(score) FROM scores"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsBigInt() != 2 {
		t.Errorf("expected COUNT(score)=2 (skipping NULLs), got %d", row[0].AsBigInt())
	}
}

func TestQuery_Sum(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "amount", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("transactions", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(100)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(200)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(150)})

	// SUM(amount)
	sql := "SELECT SUM(amount) FROM transactions"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsInt() != 450 {
		t.Errorf("expected SUM(amount)=450, got %d", row[0].AsInt())
	}
}

func TestQuery_Avg(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "score", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("scores", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(80)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(90)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(100)})

	// AVG(score)
	sql := "SELECT AVG(score) FROM scores"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	avg := row[0].AsDouble()
	expected := 90.0
	if avg < expected-0.01 || avg > expected+0.01 {
		t.Errorf("expected AVG(score)=90.0, got %f", avg)
	}
}

func TestQuery_MinMax(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "price", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("products", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(50)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(120)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(75)})

	// MIN and MAX
	sql := "SELECT MIN(price), MAX(price) FROM products"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsInt() != 50 {
		t.Errorf("expected MIN(price)=50, got %d", row[0].AsInt())
	}
	if row[1].AsInt() != 120 {
		t.Errorf("expected MAX(price)=120, got %d", row[1].AsInt())
	}
}

func TestQuery_MultipleAggregates(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "value", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("data", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(10)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(20)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(30)})

	// Multiple aggregates
	sql := "SELECT COUNT(*), SUM(value), AVG(value), MIN(value), MAX(value) FROM data"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsBigInt() != 3 {
		t.Errorf("expected COUNT(*)=3, got %d", row[0].AsBigInt())
	}
	if row[1].AsInt() != 60 {
		t.Errorf("expected SUM(value)=60, got %d", row[1].AsInt())
	}
	if row[2].AsDouble() < 19.99 || row[2].AsDouble() > 20.01 {
		t.Errorf("expected AVG(value)=20.0, got %f", row[2].AsDouble())
	}
	if row[3].AsInt() != 10 {
		t.Errorf("expected MIN(value)=10, got %d", row[3].AsInt())
	}
	if row[4].AsInt() != 30 {
		t.Errorf("expected MAX(value)=30, got %d", row[4].AsInt())
	}
}

func TestQuery_AggregateEmptyTable(t *testing.T) {
	db := setupTestDB(t)

	// Create empty table
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "value", Type: storage.TypeINT},
	})
	_, _ = db.CreateTable("empty", schema)

	// COUNT(*) on empty table should return 0
	sql := "SELECT COUNT(*) FROM empty"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if row[0].AsBigInt() != 0 {
		t.Errorf("expected COUNT(*)=0 for empty table, got %d", row[0].AsBigInt())
	}
}

func TestQuery_AggregateWithNulls(t *testing.T) {
	db := setupTestDB(t)

	// Create table with all NULL values
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "value", Type: storage.TypeINT, Nullable: true},
	})
	table, _ := db.CreateTable("nulls", schema)
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewNullValue()})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewNullValue()})

	// SUM/AVG/MIN/MAX should return NULL when all values are NULL
	sql := "SELECT SUM(value), AVG(value), MIN(value), MAX(value) FROM nulls"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	row, err := physical.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	if !row[0].IsNull() {
		t.Error("expected SUM to return NULL when all values are NULL")
	}
	if !row[1].IsNull() {
		t.Error("expected AVG to return NULL when all values are NULL")
	}
	if !row[2].IsNull() {
		t.Error("expected MIN to return NULL when all values are NULL")
	}
	if !row[3].IsNull() {
		t.Error("expected MAX to return NULL when all values are NULL")
	}
}
