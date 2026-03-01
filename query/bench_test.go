package query

import (
	"fmt"
	"testing"

	"github.com/vvshulga/db_internals/storage"
)

// ---- helpers ----------------------------------------------------------------

func openBenchEngine(b *testing.B) (*Engine, *storage.DB) {
	b.Helper()
	db, err := storage.OpenDB(b.TempDir())
	if err != nil {
		b.Fatalf("OpenDB: %v", err)
	}
	b.Cleanup(func() { db.Close() })
	return NewEngine(db), db
}

// seedRows inserts n rows into "bench" table (id INT, name VARCHAR(64), score BIGINT)
// using the storage API directly so setup cost is not charged to the benchmark.
func seedRows(b *testing.B, db *storage.DB, n int) {
	b.Helper()
	schema, err := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
		{Name: "score", Type: storage.TypeBIGINT},
	})
	if err != nil {
		b.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("bench", schema)
	if err != nil {
		b.Fatalf("CreateTable: %v", err)
	}
	for i := 0; i < n; i++ {
		_, err := tbl.Insert(storage.Row{
			storage.NewIntValue(int32(i)),
			storage.NewVarcharValue(fmt.Sprintf("user%d", i)),
			storage.NewBigIntValue(int64(i * 10)),
		})
		if err != nil {
			b.Fatalf("Insert row %d: %v", i, err)
		}
	}
}

// ---- INSERT -----------------------------------------------------------------

// BenchmarkSQL_Insert measures end-to-end INSERT via the SQL engine.
func BenchmarkSQL_Insert(b *testing.B) {
	eng, db := openBenchEngine(b)
	schema, _ := storage.NewSchema([]storage.Column{
		{Name: "id", Type: storage.TypeINT},
		{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
		{Name: "score", Type: storage.TypeBIGINT},
	})
	db.CreateTable("bench", schema)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := eng.Execute(fmt.Sprintf("INSERT INTO bench VALUES (%d, 'user%d', %d)", i, i, i*10))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ---- SELECT — full scan -----------------------------------------------------

// BenchmarkSQL_Select_FullScan measures SELECT * on tables of increasing size.
func BenchmarkSQL_Select_FullScan(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			eng, db := openBenchEngine(b)
			seedRows(b, db, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rs, err := eng.Execute("SELECT * FROM bench")
				if err != nil {
					b.Fatal(err)
				}
				_ = rs
			}
		})
	}
}

// ---- SELECT — WHERE filter --------------------------------------------------

// BenchmarkSQL_Select_Filter measures SELECT with a WHERE clause (full scan + filter).
func BenchmarkSQL_Select_Filter(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			eng, db := openBenchEngine(b)
			seedRows(b, db, n)
			target := n / 2
			sql := fmt.Sprintf("SELECT * FROM bench WHERE id = %d", target)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rs, err := eng.Execute(sql)
				if err != nil {
					b.Fatal(err)
				}
				_ = rs
			}
		})
	}
}

// ---- SELECT — index lookup --------------------------------------------------

// BenchmarkSQL_Select_IndexLookup measures SELECT with an indexed point lookup.
func BenchmarkSQL_Select_IndexLookup(b *testing.B) {
	const n = 1000
	eng, db := openBenchEngine(b)
	seedRows(b, db, n)
	tbl, err := db.OpenTable("bench")
	if err != nil {
		b.Fatalf("OpenTable: %v", err)
	}
	if err := tbl.CreateIndex("id", false); err != nil {
		b.Fatalf("CreateIndex: %v", err)
	}
	target := n / 2
	sql := fmt.Sprintf("SELECT * FROM bench WHERE id = %d", target)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rs, err := eng.Execute(sql)
		if err != nil {
			b.Fatal(err)
		}
		_ = rs
	}
}

// ---- SELECT — aggregate (COUNT + GROUP BY) ----------------------------------

// BenchmarkSQL_Select_Aggregate measures SELECT with COUNT(*) GROUP BY.
func BenchmarkSQL_Select_Aggregate(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			eng, db := openBenchEngine(b)
			// Use mod 10 for id so GROUP BY produces 10 groups
			schema, _ := storage.NewSchema([]storage.Column{
				{Name: "id", Type: storage.TypeINT},
				{Name: "name", Type: storage.TypeVARCHAR, MaxLen: 64},
				{Name: "score", Type: storage.TypeBIGINT},
			})
			tbl, _ := db.CreateTable("bench", schema)
			for i := 0; i < n; i++ {
				tbl.Insert(storage.Row{
					storage.NewIntValue(int32(i % 10)),
					storage.NewVarcharValue(fmt.Sprintf("user%d", i)),
					storage.NewBigIntValue(int64(i)),
				})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rs, err := eng.Execute("SELECT id, COUNT(*) FROM bench GROUP BY id")
				if err != nil {
					b.Fatal(err)
				}
				_ = rs
			}
		})
	}
}

// ---- SELECT — ORDER BY + LIMIT ----------------------------------------------

// BenchmarkSQL_Select_OrderByLimit measures SELECT with ORDER BY + LIMIT (materialises + sorts).
func BenchmarkSQL_Select_OrderByLimit(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			eng, db := openBenchEngine(b)
			seedRows(b, db, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rs, err := eng.Execute("SELECT * FROM bench ORDER BY score DESC LIMIT 10")
				if err != nil {
					b.Fatal(err)
				}
				_ = rs
			}
		})
	}
}

// ---- UPDATE -----------------------------------------------------------------

// BenchmarkSQL_Update measures UPDATE via SQL (full scan + filter + delete/reinsert).
func BenchmarkSQL_Update(b *testing.B) {
	const n = 100
	eng, db := openBenchEngine(b)
	seedRows(b, db, n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := i % n
		_, err := eng.Execute(fmt.Sprintf("UPDATE bench SET score = %d WHERE id = %d", i*99, target))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ---- DELETE -----------------------------------------------------------------

// BenchmarkSQL_Delete measures DELETE via SQL (full scan + filter + tombstone).
// Rows are pre-inserted outside the timer; b.N is capped by table size.
func BenchmarkSQL_Delete(b *testing.B) {
	eng, db := openBenchEngine(b)
	// Pre-insert enough rows to cover any b.N
	const maxN = 100000
	seedRows(b, db, maxN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := eng.Execute(fmt.Sprintf("DELETE FROM bench WHERE id = %d LIMIT 1", i%maxN))
		if err != nil {
			b.Fatal(err)
		}
	}
}
