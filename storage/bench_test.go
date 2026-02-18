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

// ---- Index helpers ----------------------------------------------------------

// openBenchTableWithIndex creates a table pre-populated with n rows and a
// non-unique index on the "id" column.
func openBenchTableWithIndex(b *testing.B, n int) *TableHandle {
	b.Helper()
	tbl := openBenchTable(b)
	for i := 0; i < n; i++ {
		if _, err := tbl.Insert(Row{NewIntValue(int32(i)), NewVarcharValue(fmt.Sprintf("u%d", i))}); err != nil {
			b.Fatal(err)
		}
	}
	if err := tbl.CreateIndex("id", false); err != nil {
		b.Fatalf("CreateIndex: %v", err)
	}
	return tbl
}

// ---- BenchmarkIndexInsert ---------------------------------------------------

// BenchmarkIndexInsert measures the overhead of inserting a row into a table
// that has one INT index on the "id" column.
func BenchmarkIndexInsert(b *testing.B) {
	tbl := openBenchTable(b)
	if err := tbl.CreateIndex("id", false); err != nil {
		b.Fatalf("CreateIndex: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tbl.Insert(Row{NewIntValue(int32(i)), NewVarcharValue("Alice")}); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- BenchmarkIndexLookup ---------------------------------------------------

// BenchmarkIndexLookup measures the time to look up a single row by its
// indexed INT column value.
func BenchmarkIndexLookup(b *testing.B) {
	const n = 1000
	tbl := openBenchTableWithIndex(b, n)
	target := NewIntValue(n / 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rids, err := tbl.LookupExact("id", target)
		if err != nil || len(rids) == 0 {
			b.Fatalf("LookupExact: err=%v rids=%v", err, rids)
		}
	}
}

// ---- BenchmarkIndexRangeScan ------------------------------------------------

// BenchmarkIndexRangeScan measures a range scan that returns ~10% of rows.
func BenchmarkIndexRangeScan(b *testing.B) {
	const n = 1000
	tbl := openBenchTableWithIndex(b, n)
	lo := NewIntValue(int32(n * 45 / 100))
	hi := NewIntValue(int32(n * 55 / 100))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tbl.RangeScan("id", &lo, &hi, func(_ RID, _ Row) (bool, error) {
			return true, nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- BenchmarkIndexVsFullScan -----------------------------------------------

// BenchmarkIndexVsFullScan compares indexed point-lookup vs a full-table Scan
// for a table with 1000 rows. The sub-benchmarks share the same data set.
func BenchmarkIndexVsFullScan(b *testing.B) {
	const n = 1000
	target := NewIntValue(int32(n / 2))

	b.Run("FullScan", func(b *testing.B) {
		tbl := openBenchTable(b)
		for i := 0; i < n; i++ {
			tbl.Insert(Row{NewIntValue(int32(i)), NewVarcharValue(fmt.Sprintf("u%d", i))})
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s := tbl.Scan()
			for s.Next() {
				if s.Row()[0] == target {
					break
				}
			}
			if err := s.Err(); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("IndexLookup", func(b *testing.B) {
		tbl := openBenchTableWithIndex(b, n)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rids, err := tbl.LookupExact("id", target)
			if err != nil || len(rids) == 0 {
				b.Fatalf("LookupExact: err=%v rids=%v", err, rids)
			}
		}
	})
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
