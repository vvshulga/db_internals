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
		_, err = physical.Next()
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

func TestQuery_OrderBySingleColumnASC(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	table, _ := db.CreateTable("users", schema)

	// Insert in random order
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewVarcharValue("Charlie")})
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewVarcharValue("Bob")})

	// Query: SELECT id, name FROM users ORDER BY id ASC
	sql := "SELECT id, name FROM users ORDER BY id ASC"
	nodes, err := parser.ParseString(sql)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}
	logical, err := NewPlanner(db).Plan(nodes[0])
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	physical, err := NewOptimizer(db).Optimize(logical)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if err = physical.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer physical.Close()

	// Verify order: Alice (id=1), Bob (id=2), Charlie (id=3)
	expected := []struct{ id int32; name string }{
		{1, "Alice"},
		{2, "Bob"},
		{3, "Charlie"},
	}
	for i, exp := range expected {
		row, err := physical.Next()
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if row[0].AsInt() != exp.id || row[1].AsString() != exp.name {
			t.Errorf("row %d: expected (id=%d, name=%s), got (id=%d, name=%s)",
				i, exp.id, exp.name, row[0].AsInt(), row[1].AsString())
		}
	}

	_, err = physical.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestQuery_OrderBySingleColumnDESC(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	table, _ := db.CreateTable("users", schema)

	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewVarcharValue("Charlie")})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewVarcharValue("Bob")})

	// Query: SELECT id, name FROM users ORDER BY id DESC
	sql := "SELECT id, name FROM users ORDER BY id DESC"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Verify order: Charlie (id=3), Bob (id=2), Alice (id=1)
	expected := []struct{ id int32; name string }{
		{3, "Charlie"},
		{2, "Bob"},
		{1, "Alice"},
	}
	for i, exp := range expected {
		row, err := physical.Next()
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if row[0].AsInt() != exp.id || row[1].AsString() != exp.name {
			t.Errorf("row %d: expected (id=%d, name=%s), got (id=%d, name=%s)",
				i, exp.id, exp.name, row[0].AsInt(), row[1].AsString())
		}
	}

	_, err := physical.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestQuery_OrderByMultipleColumns(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "dept", Type: storage.TypeVARCHAR, MaxLen: 32},
		{Name: "salary", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
	})
	table, _ := db.CreateTable("employees", schema)

	// Insert test data (mixed departments and salaries)
	table.Insert(storage.Row{storage.NewVarcharValue("Sales"), storage.NewIntValue(50000), storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewVarcharValue("Engineering"), storage.NewIntValue(80000), storage.NewVarcharValue("Bob")})
	table.Insert(storage.Row{storage.NewVarcharValue("Sales"), storage.NewIntValue(60000), storage.NewVarcharValue("Charlie")})
	table.Insert(storage.Row{storage.NewVarcharValue("Engineering"), storage.NewIntValue(90000), storage.NewVarcharValue("Diana")})

	// ORDER BY dept ASC, salary DESC
	sql := "SELECT dept, salary, name FROM employees ORDER BY dept ASC, salary DESC"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Expected order:
	// 1. Engineering, 90000, Diana (dept ASC, salary DESC within Engineering)
	// 2. Engineering, 80000, Bob
	// 3. Sales, 60000, Charlie (dept ASC, salary DESC within Sales)
	// 4. Sales, 50000, Alice

	expected := []struct {
		dept   string
		salary int32
		name   string
	}{
		{"Engineering", 90000, "Diana"},
		{"Engineering", 80000, "Bob"},
		{"Sales", 60000, "Charlie"},
		{"Sales", 50000, "Alice"},
	}

	for i, exp := range expected {
		row, err := physical.Next()
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if row[0].AsString() != exp.dept || row[1].AsInt() != exp.salary || row[2].AsString() != exp.name {
			t.Errorf("row %d: expected (%s, %d, %s), got (%s, %d, %s)",
				i, exp.dept, exp.salary, exp.name,
				row[0].AsString(), row[1].AsInt(), row[2].AsString())
		}
	}
}

func TestQuery_OrderByWithNulls(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "score", Type: storage.TypeINT, Nullable: true},
	})
	table, _ := db.CreateTable("scores", schema)

	// Insert data with NULLs
	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(100)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewNullValue()})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(50)})
	table.Insert(storage.Row{storage.NewIntValue(4), storage.NewNullValue()})

	// ORDER BY score ASC (NULLs should come first)
	sql := "SELECT id, score FROM scores ORDER BY score ASC"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Expected: NULL, NULL, 50, 100
	row1, _ := physical.Next()
	if !row1[1].IsNull() {
		t.Errorf("expected NULL first, got %v", row1[1])
	}

	row2, _ := physical.Next()
	if !row2[1].IsNull() {
		t.Errorf("expected NULL second, got %v", row2[1])
	}

	row3, _ := physical.Next()
	if row3[1].AsInt() != 50 {
		t.Errorf("expected 50, got %d", row3[1].AsInt())
	}

	row4, _ := physical.Next()
	if row4[1].AsInt() != 100 {
		t.Errorf("expected 100, got %d", row4[1].AsInt())
	}
}

func TestQuery_OrderByWithLimit(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "value", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("numbers", schema)

	// Insert 10 numbers
	for i := 10; i >= 1; i-- {
		table.Insert(storage.Row{storage.NewIntValue(int32(i))})
	}

	// ORDER BY + LIMIT should return top 3
	sql := "SELECT value FROM numbers ORDER BY value DESC LIMIT 3"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	expected := []int32{10, 9, 8}
	for i, exp := range expected {
		row, err := physical.Next()
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if row[0].AsInt() != exp {
			t.Errorf("row %d: expected %d, got %d", i, exp, row[0].AsInt())
		}
	}

	_, err := physical.Next()
	if err != io.EOF {
		t.Errorf("expected EOF after 3 rows, got %v", err)
	}
}

func TestQuery_OrderByEmptyTable(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
	})
	_, _ = db.CreateTable("empty", schema)

	sql := "SELECT * FROM empty ORDER BY id"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	_, err := physical.Next()
	if err != io.EOF {
		t.Errorf("expected EOF on empty table, got %v", err)
	}
}

func TestQuery_OrderByWithWhere(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "age", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("users", schema)

	table.Insert(storage.Row{storage.NewIntValue(1), storage.NewIntValue(25)})
	table.Insert(storage.Row{storage.NewIntValue(2), storage.NewIntValue(15)})
	table.Insert(storage.Row{storage.NewIntValue(3), storage.NewIntValue(35)})
	table.Insert(storage.Row{storage.NewIntValue(4), storage.NewIntValue(20)})

	// WHERE age > 18 ORDER BY age ASC
	sql := "SELECT id, age FROM users WHERE age > 18 ORDER BY age ASC"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Expected: id=4 (age=20), id=1 (age=25), id=3 (age=35)
	expected := []struct{ id, age int32 }{
		{4, 20},
		{1, 25},
		{3, 35},
	}

	for i, exp := range expected {
		row, err := physical.Next()
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if row[0].AsInt() != exp.id || row[1].AsInt() != exp.age {
			t.Errorf("row %d: expected (id=%d, age=%d), got (id=%d, age=%d)",
				i, exp.id, exp.age, row[0].AsInt(), row[1].AsInt())
		}
	}
}

func TestQuery_DistinctSingleColumn(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate table with duplicates
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "dept", Type: storage.TypeVARCHAR, MaxLen: 32},
	})
	table, _ := db.CreateTable("employees", schema)
	table.Insert(storage.Row{storage.NewVarcharValue("Engineering")})
	table.Insert(storage.Row{storage.NewVarcharValue("Sales")})
	table.Insert(storage.Row{storage.NewVarcharValue("Engineering")}) // duplicate
	table.Insert(storage.Row{storage.NewVarcharValue("Sales")})       // duplicate
	table.Insert(storage.Row{storage.NewVarcharValue("HR")})

	// SELECT DISTINCT dept FROM employees
	sql := "SELECT DISTINCT dept FROM employees"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Collect results
	results := make(map[string]bool)
	for {
		row, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		results[row[0].AsString()] = true
	}

	// Should have exactly 3 unique departments
	if len(results) != 3 {
		t.Errorf("expected 3 unique departments, got %d", len(results))
	}

	// Verify we got the right departments
	expected := map[string]bool{
		"Engineering": true,
		"Sales":       true,
		"HR":          true,
	}
	for dept := range expected {
		if !results[dept] {
			t.Errorf("missing department: %s", dept)
		}
	}
}

func TestQuery_DistinctMultipleColumns(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "dept", Type: storage.TypeVARCHAR, MaxLen: 32},
		{Name: "level", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("employees", schema)

	// Insert rows with some duplicates
	table.Insert(storage.Row{storage.NewVarcharValue("Eng"), storage.NewIntValue(1)})
	table.Insert(storage.Row{storage.NewVarcharValue("Eng"), storage.NewIntValue(2)})
	table.Insert(storage.Row{storage.NewVarcharValue("Eng"), storage.NewIntValue(1)}) // duplicate
	table.Insert(storage.Row{storage.NewVarcharValue("Sales"), storage.NewIntValue(1)})

	sql := "SELECT DISTINCT dept, level FROM employees"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Count unique rows
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

	// Should have exactly 3 unique (dept, level) pairs
	if count != 3 {
		t.Errorf("expected 3 unique rows, got %d", count)
	}
}

func TestQuery_DistinctWithNulls(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 32, Nullable: true},
	})
	table, _ := db.CreateTable("users", schema)

	// Insert rows with NULL values
	table.Insert(storage.Row{storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewNullValue()})           // NULL
	table.Insert(storage.Row{storage.NewNullValue()})           // NULL (duplicate)
	table.Insert(storage.Row{storage.NewVarcharValue("Bob")})
	table.Insert(storage.Row{storage.NewVarcharValue("Alice")}) // duplicate

	sql := "SELECT DISTINCT name FROM users"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	count := 0
	nullCount := 0
	for {
		row, err := physical.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		count++
		if row[0].IsNull() {
			nullCount++
		}
	}

	// Should have 3 unique values: Alice, Bob, NULL
	if count != 3 {
		t.Errorf("expected 3 unique values (including NULL), got %d", count)
	}

	// Should have exactly 1 NULL (two NULLs should be deduplicated)
	if nullCount != 1 {
		t.Errorf("expected 1 NULL value, got %d", nullCount)
	}
}

func TestQuery_DistinctWithWhere(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "dept", Type: storage.TypeVARCHAR, MaxLen: 32},
		{Name: "active", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("employees", schema)

	table.Insert(storage.Row{storage.NewVarcharValue("Eng"), storage.NewIntValue(1)})
	table.Insert(storage.Row{storage.NewVarcharValue("Eng"), storage.NewIntValue(1)})    // duplicate
	table.Insert(storage.Row{storage.NewVarcharValue("Sales"), storage.NewIntValue(1)})
	table.Insert(storage.Row{storage.NewVarcharValue("HR"), storage.NewIntValue(0)})

	sql := "SELECT DISTINCT dept FROM employees WHERE active = 1"
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

	// Should have 2 unique active departments: Eng, Sales (HR is filtered out)
	if count != 2 {
		t.Errorf("expected 2 unique active departments, got %d", count)
	}
}

func TestQuery_DistinctWithOrderBy(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 32},
	})
	table, _ := db.CreateTable("users", schema)

	table.Insert(storage.Row{storage.NewVarcharValue("Charlie")})
	table.Insert(storage.Row{storage.NewVarcharValue("Alice")})
	table.Insert(storage.Row{storage.NewVarcharValue("Bob")})
	table.Insert(storage.Row{storage.NewVarcharValue("Alice")}) // duplicate

	sql := "SELECT DISTINCT name FROM users ORDER BY name ASC"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Results should be distinct AND sorted
	expected := []string{"Alice", "Bob", "Charlie"}
	for i, expectedName := range expected {
		row, err := physical.Next()
		if err != nil {
			t.Fatalf("expected row %d, got error: %v", i, err)
		}
		if row[0].AsString() != expectedName {
			t.Errorf("row %d: expected %s, got %s", i, expectedName, row[0].AsString())
		}
	}

	// No more rows
	_, err := physical.Next()
	if err != io.EOF {
		t.Errorf("expected EOF after 3 rows, got: %v", err)
	}
}

func TestQuery_DistinctWithLimit(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "value", Type: storage.TypeINT},
	})
	table, _ := db.CreateTable("numbers", schema)

	// Insert many duplicates
	for i := 1; i <= 5; i++ {
		table.Insert(storage.Row{storage.NewIntValue(int32(i))})
		table.Insert(storage.Row{storage.NewIntValue(int32(i))}) // duplicate each
	}

	sql := "SELECT DISTINCT value FROM numbers LIMIT 3"
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

	// Should return only 3 unique values (LIMIT 3)
	if count != 3 {
		t.Errorf("expected 3 rows (LIMIT 3), got %d", count)
	}
}

func TestQuery_DistinctEmptyTable(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
	})
	_, _ = db.CreateTable("empty", schema)

	sql := "SELECT DISTINCT id FROM empty"
	nodes, _ := parser.ParseString(sql)
	logical, _ := NewPlanner(db).Plan(nodes[0])
	physical, _ := NewOptimizer(db).Optimize(logical)

	physical.Open()
	defer physical.Close()

	// Should return no rows
	_, err := physical.Next()
	if err != io.EOF {
		t.Errorf("expected EOF on empty table, got: %v", err)
	}
}

func TestQuery_DistinctAllDataTypes(t *testing.T) {
	db := setupTestDB(t)

	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "i", Type: storage.TypeINT},
		{Name: "b", Type: storage.TypeBIGINT},
		{Name: "f", Type: storage.TypeFLOAT},
		{Name: "d", Type: storage.TypeDOUBLE},
		{Name: "bool", Type: storage.TypeBOOLEAN},
		{Name: "str", Type: storage.TypeVARCHAR, MaxLen: 32},
	})
	table, _ := db.CreateTable("types", schema)

	// Insert duplicate rows with various types
	row1 := storage.Row{
		storage.NewIntValue(42),
		storage.NewBigIntValue(1000),
		storage.NewFloatValue(3.14),
		storage.NewDoubleValue(2.718),
		storage.NewBooleanValue(true),
		storage.NewVarcharValue("test"),
	}

	table.Insert(row1)
	table.Insert(row1) // exact duplicate

	sql := "SELECT DISTINCT * FROM types"
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

	// Should deduplicate to 1 unique row
	if count != 1 {
		t.Errorf("expected 1 unique row, got %d", count)
	}
}
