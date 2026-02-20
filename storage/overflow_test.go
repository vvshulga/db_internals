package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ---- helpers -----------------------------------------------------------------

func openOverflowTable(t *testing.T) *TableHandle {
	t.Helper()
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "body", Type: TypeTEXT},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("t", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	return tbl
}

// bigText returns a string of exactly n bytes filled with the repeating pattern.
func bigText(n int) string {
	const pat = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := strings.Builder{}
	b.Grow(n)
	for b.Len() < n {
		remaining := n - b.Len()
		chunk := pat
		if remaining < len(chunk) {
			chunk = chunk[:remaining]
		}
		b.WriteString(chunk)
	}
	return b.String()
}

// ---- functional tests --------------------------------------------------------

// TestOverflow_SmallText verifies that TEXT values small enough to fit inline
// round-trip correctly with the new 12-byte directory format.
func TestOverflow_SmallText(t *testing.T) {
	tbl := openOverflowTable(t)
	text := "hello, world"
	rid, err := tbl.Insert(Row{NewIntValue(1), NewTextValue(text)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: row not found")
	}
	if got := row[1].AsString(); got != text {
		t.Fatalf("body mismatch: got %q, want %q", got, text)
	}
}

// TestOverflow_ExactlyOneChunk inserts a TEXT value that fills exactly one
// overflow page (overflowChunkSize bytes).
func TestOverflow_ExactlyOneChunk(t *testing.T) {
	tbl := openOverflowTable(t)
	text := bigText(overflowChunkSize)
	rid, err := tbl.Insert(Row{NewIntValue(2), NewTextValue(text)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: row not found")
	}
	if got := row[1].AsString(); got != text {
		t.Fatalf("length mismatch: got %d bytes, want %d", len(got), len(text))
	}
}

// TestOverflow_MultiChunk inserts a TEXT value that spans multiple overflow
// pages (10× overflowChunkSize ≈ 80 KiB).
func TestOverflow_MultiChunk(t *testing.T) {
	tbl := openOverflowTable(t)
	text := bigText(10 * overflowChunkSize)
	rid, err := tbl.Insert(Row{NewIntValue(3), NewTextValue(text)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: row not found")
	}
	if got := row[1].AsString(); got != text {
		t.Fatalf("content mismatch at length %d vs %d", len(got), len(text))
	}
}

// TestOverflow_LargeValue inserts a TEXT value of 256 KiB (≈ 32 overflow pages).
func TestOverflow_LargeValue(t *testing.T) {
	tbl := openOverflowTable(t)
	const size = 256 * 1024
	text := bigText(size)
	rid, err := tbl.Insert(Row{NewIntValue(4), NewTextValue(text)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: row not found")
	}
	if got := row[1].AsString(); got != text {
		t.Fatalf("length mismatch: got %d, want %d", len(got), size)
	}
}

// TestOverflow_NullText ensures NULL TEXT columns do not create overflow pages.
func TestOverflow_NullText(t *testing.T) {
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "body", Type: TypeTEXT, Nullable: true},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("t", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	before := tbl.heap.PageCount()
	rid, err := tbl.Insert(Row{NewIntValue(1), NewNullValue()})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	after := tbl.heap.PageCount()
	// No overflow pages should be allocated for a NULL value.
	if after != before {
		t.Fatalf("expected no new pages for NULL TEXT, got %d new page(s)", after-before)
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: row not found")
	}
	if !row[1].IsNull() {
		t.Fatalf("expected NULL body, got %q", row[1].AsString())
	}
}

// TestOverflow_Scan verifies that Scan visits overflow rows and returns the
// full value, while skipping internal overflow pages.
func TestOverflow_Scan(t *testing.T) {
	tbl := openOverflowTable(t)
	want := map[int32]string{
		1: "short",
		2: bigText(overflowChunkSize),     // exactly one overflow page
		3: bigText(3 * overflowChunkSize), // three overflow pages
	}
	for id, text := range want {
		if _, err := tbl.Insert(Row{NewIntValue(id), NewTextValue(text)}); err != nil {
			t.Fatalf("Insert id=%d: %v", id, err)
		}
	}

	got := map[int32]string{}
	s := tbl.Scan()
	for s.Next() {
		row := s.Row()
		got[row[0].AsInt()] = row[1].AsString()
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	for id, text := range want {
		if got[id] != text {
			t.Fatalf("row %d: got %d bytes, want %d bytes", id, len(got[id]), len(text))
		}
	}
}

// TestOverflow_Update replaces an overflow row with a different large value.
func TestOverflow_Update(t *testing.T) {
	tbl := openOverflowTable(t)
	old := bigText(3 * overflowChunkSize)
	rid, err := tbl.Insert(Row{NewIntValue(1), NewTextValue(old)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	newText := bigText(5 * overflowChunkSize)
	newRID, ok, err := tbl.Update(rid, Row{NewIntValue(1), NewTextValue(newText)})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !ok {
		t.Fatal("Update: row not found")
	}

	row, ok, err := tbl.Get(newRID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if !ok {
		t.Fatal("Get after update: row not found")
	}
	if got := row[1].AsString(); got != newText {
		t.Fatalf("length mismatch after update: got %d, want %d", len(got), len(newText))
	}
}

// TestOverflow_Delete verifies that deleting a row with overflow works
// (old RID returns not-found; no crash).
func TestOverflow_Delete(t *testing.T) {
	tbl := openOverflowTable(t)
	text := bigText(2 * overflowChunkSize)
	rid, err := tbl.Insert(Row{NewIntValue(1), NewTextValue(text)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	ok, err := tbl.Delete(rid)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !ok {
		t.Fatal("Delete: row not found")
	}
	_, ok, err = tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Fatal("Get after delete: row still present")
	}
}

// TestOverflow_Persistence verifies that overflow rows survive a database
// close-and-reopen cycle.
func TestOverflow_Persistence(t *testing.T) {
	dir := t.TempDir()
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "body", Type: TypeTEXT},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	text := bigText(5 * overflowChunkSize)
	var savedRID RID

	// Write phase.
	{
		db, err := OpenDB(dir)
		if err != nil {
			t.Fatalf("OpenDB: %v", err)
		}
		tbl, err := db.CreateTable("t", schema)
		if err != nil {
			t.Fatalf("CreateTable: %v", err)
		}
		savedRID, err = tbl.Insert(Row{NewIntValue(99), NewTextValue(text)})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	// Read phase.
	{
		db, err := OpenDB(dir)
		if err != nil {
			t.Fatalf("OpenDB (reopen): %v", err)
		}
		defer db.Close()
		tbl, err := db.OpenTable("t")
		if err != nil {
			t.Fatalf("OpenTable: %v", err)
		}
		row, ok, err := tbl.Get(savedRID)
		if err != nil {
			t.Fatalf("Get after reopen: %v", err)
		}
		if !ok {
			t.Fatal("Get after reopen: row not found")
		}
		if got := row[1].AsString(); got != text {
			t.Fatalf("content mismatch after reopen: got %d bytes, want %d", len(got), len(text))
		}
	}
}

// TestOverflow_MixedColumns verifies schemas with both inline and overflow TEXT
// columns in the same row.
func TestOverflow_MixedColumns(t *testing.T) {
	db, err := OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
		{Name: "body", Type: TypeTEXT},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	tbl, err := db.CreateTable("t", schema)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	wantName := "Alice"
	wantBody := bigText(4 * overflowChunkSize)
	rid, err := tbl.Insert(Row{
		NewIntValue(7),
		NewVarcharValue(wantName),
		NewTextValue(wantBody),
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: row not found")
	}
	if row[0].AsInt() != 7 {
		t.Fatalf("id: got %d, want 7", row[0].AsInt())
	}
	if row[1].AsString() != wantName {
		t.Fatalf("name: got %q, want %q", row[1].AsString(), wantName)
	}
	if row[2].AsString() != wantBody {
		t.Fatalf("body: got %d bytes, want %d", len(row[2].AsString()), len(wantBody))
	}
}

// TestOverflow_MultipleOverflowRows inserts many rows with overflow values and
// verifies all can be read back via Scan.
func TestOverflow_MultipleOverflowRows(t *testing.T) {
	tbl := openOverflowTable(t)
	const n = 20
	texts := make([]string, n)
	rids := make([]RID, n)
	for i := 0; i < n; i++ {
		texts[i] = bigText((i + 1) * overflowChunkSize)
		var err error
		rids[i], err = tbl.Insert(Row{NewIntValue(int32(i)), NewTextValue(texts[i])})
		if err != nil {
			t.Fatalf("Insert row %d: %v", i, err)
		}
	}

	// Verify via Get.
	for i, rid := range rids {
		row, ok, err := tbl.Get(rid)
		if err != nil {
			t.Fatalf("Get row %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("Get row %d: not found", i)
		}
		if got := row[1].AsString(); got != texts[i] {
			t.Fatalf("row %d: length mismatch: got %d, want %d", i, len(got), len(texts[i]))
		}
	}
}

// TestOverflowChainCycleDetection verifies that readOverflowChain detects and
// rejects cyclic chains (A→B→C→A) that could result from corruption.
func TestOverflowChainCycleDetection(t *testing.T) {
	tbl := openOverflowTable(t)
	heap := tbl.heap

	// Manually craft a cycle: page 1 → page 2 → page 1.
	// This simulates corruption that could hang the database.

	// Create page 1: next=2, data="chunk1"
	pg1 := NewPage(1, DataPage)
	var body1 [8 + 6]byte
	binary.LittleEndian.PutUint64(body1[:8], 2) // next = page 2
	copy(body1[8:], []byte("chunk1"))
	if _, err := pg1.InsertTuple(body1[:]); err != nil {
		t.Fatalf("InsertTuple page 1: %v", err)
	}
	if err := heap.writePage(pg1); err != nil {
		t.Fatalf("writePage 1: %v", err)
	}

	// Create page 2: next=1, data="chunk2" (cycle back to page 1)
	pg2 := NewPage(2, DataPage)
	var body2 [8 + 6]byte
	binary.LittleEndian.PutUint64(body2[:8], 1) // next = page 1 (CYCLE!)
	copy(body2[8:], []byte("chunk2"))
	if _, err := pg2.InsertTuple(body2[:]); err != nil {
		t.Fatalf("InsertTuple page 2: %v", err)
	}
	if err := heap.writePage(pg2); err != nil {
		t.Fatalf("writePage 2: %v", err)
	}

	// Update heap metadata to reflect that we have 3 pages total (0=meta, 1, 2).
	heap.meta.TotalPages = 3
	if err := heap.writeMeta(); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}

	// Try to read the cyclic chain starting from page 1.
	_, err := readOverflowChain(heap, 1)
	if err == nil {
		t.Fatal("readOverflowChain: expected cycle detection error, got nil")
	}

	// Verify it's the right error type.
	var corruptErr *ErrCorruptRecord
	if !errors.As(err, &corruptErr) {
		t.Fatalf("readOverflowChain: expected ErrCorruptRecord, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("readOverflowChain: error should mention 'cycle', got: %v", err)
	}
}

// TestOverflowChainLengthLimit verifies that excessively long overflow chains
// (>10000 pages) are rejected to prevent memory exhaustion attacks.
func TestOverflowChainLengthLimit(t *testing.T) {
	tbl := openOverflowTable(t)
	heap := tbl.heap

	// Create a chain of 10001 pages: 1→2→3→...→10001→0.
	// This exceeds the 10000-page limit.
	const chainLen = 10001
	for i := uint64(1); i <= chainLen; i++ {
		pg := NewPage(i, DataPage)
		var body [8 + 4]byte
		if i < chainLen {
			binary.LittleEndian.PutUint64(body[:8], i+1) // next page
		} else {
			binary.LittleEndian.PutUint64(body[:8], 0) // end of chain
		}
		copy(body[8:], []byte("data"))
		if _, err := pg.InsertTuple(body[:]); err != nil {
			t.Fatalf("InsertTuple page %d: %v", i, err)
		}
		if err := heap.writePage(pg); err != nil {
			t.Fatalf("writePage %d: %v", i, err)
		}
	}

	heap.meta.TotalPages = chainLen + 1 // +1 for meta page
	if err := heap.writeMeta(); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}

	// Try to read the chain - should fail at 10001st page.
	_, err := readOverflowChain(heap, 1)
	if err == nil {
		t.Fatal("readOverflowChain: expected length limit error, got nil")
	}

	var corruptErr *ErrCorruptRecord
	if !errors.As(err, &corruptErr) {
		t.Fatalf("readOverflowChain: expected ErrCorruptRecord, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("readOverflowChain: error should mention 'too long', got: %v", err)
	}
}

// ---- benchmarks --------------------------------------------------------------

func openBenchOverflowTable(b *testing.B) *TableHandle {
	b.Helper()
	db, err := OpenDB(b.TempDir())
	if err != nil {
		b.Fatalf("OpenDB: %v", err)
	}
	b.Cleanup(func() { db.Close() })
	schema, err := NewSchema([]Column{
		{Name: "id", Type: TypeINT},
		{Name: "body", Type: TypeTEXT},
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

// BenchmarkOverflowInsert measures insert throughput for TEXT values of
// increasing size: from inline (512 B) to multi-page overflow (256 KiB).
func BenchmarkOverflowInsert(b *testing.B) {
	sizes := []int{512, overflowChunkSize, 8 * overflowChunkSize, 32 * overflowChunkSize}
	for _, sz := range sizes {
		sz := sz
		text := bigText(sz)
		b.Run(fmt.Sprintf("size=%d", sz), func(b *testing.B) {
			tbl := openBenchOverflowTable(b)
			row := Row{NewIntValue(1), NewTextValue(text)}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := tbl.Insert(row); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkOverflowGet measures fetch latency for a pre-inserted overflow row.
func BenchmarkOverflowGet(b *testing.B) {
	sizes := []int{512, overflowChunkSize, 8 * overflowChunkSize, 32 * overflowChunkSize}
	for _, sz := range sizes {
		sz := sz
		text := bigText(sz)
		b.Run(fmt.Sprintf("size=%d", sz), func(b *testing.B) {
			tbl := openBenchOverflowTable(b)
			rid, err := tbl.Insert(Row{NewIntValue(1), NewTextValue(text)})
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := tbl.Get(rid); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkOverflowScan measures scan throughput when rows contain overflow TEXT.
func BenchmarkOverflowScan(b *testing.B) {
	for _, n := range []int{10, 100} {
		n := n
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			tbl := openBenchOverflowTable(b)
			text := bigText(2 * overflowChunkSize) // ~16 KiB per row
			for i := 0; i < n; i++ {
				if _, err := tbl.Insert(Row{NewIntValue(int32(i)), NewTextValue(text)}); err != nil {
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
