package storage

import (
	"fmt"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func openBenchDB(b *testing.B) *DB {
	b.Helper()
	db, err := OpenDB(b.TempDir())
	if err != nil {
		b.Fatalf("OpenDB: %v", err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

func openBenchTable(b *testing.B) *TableHandle {
	b.Helper()
	db := openBenchDB(b)
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
	})
	if err != nil {
		b.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("t", schema)
	if err != nil {
		b.Fatalf("CreateTable: %v", err)
	}
	return tbl
}

// ---- Insert -----------------------------------------------------------------

func BenchmarkInsert(b *testing.B) {
	tbl := openBenchTable(b)
	row := Row{NewIntValue(1), NewVarcharValue("Alice")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tbl.Insert(row); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- Get --------------------------------------------------------------------

func BenchmarkGet(b *testing.B) {
	tbl := openBenchTable(b)
	rid, err := tbl.Insert(Row{NewIntValue(1), NewVarcharValue("Alice")})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := tbl.Get(rid); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- Update -----------------------------------------------------------------

func BenchmarkUpdate(b *testing.B) {
	tbl := openBenchTable(b)
	rid, err := tbl.Insert(Row{NewIntValue(1), NewVarcharValue("Alice")})
	if err != nil {
		b.Fatal(err)
	}
	newRow := Row{NewIntValue(1), NewVarcharValue("Alice-Updated")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newRID, _, err := tbl.Update(rid, newRow)
		if err != nil {
			b.Fatal(err)
		}
		rid = newRID
	}
}

// ---- Delete -----------------------------------------------------------------

func BenchmarkDelete(b *testing.B) {
	tbl := openBenchTable(b)
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		rid, err := tbl.Insert(Row{NewIntValue(int32(i)), NewVarcharValue("Alice")})
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := tbl.Delete(rid); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- Scan -------------------------------------------------------------------

func BenchmarkScan(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			tbl := openBenchTable(b)
			for i := 0; i < n; i++ {
				if _, err := tbl.Insert(Row{NewIntValue(int32(i)), NewVarcharValue(fmt.Sprintf("u%d", i))}); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s := tbl.Scan()
				for s.Next() {
					_ = s.Row()
				}
				if err := s.Err(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
