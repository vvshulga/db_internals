package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"testing"
)

// ---- TestNewPage --------------------------------------------------------------

func TestNewPage(t *testing.T) {
	p := NewPage(42, DataPage)

	if p.hdr.PageID != 42 {
		t.Errorf("PageID: got %d, want 42", p.hdr.PageID)
	}
	if p.hdr.PageType != DataPage {
		t.Errorf("PageType: got %d, want DataPage", p.hdr.PageType)
	}
	if p.hdr.NumSlots != 0 {
		t.Errorf("NumSlots: got %d, want 0", p.hdr.NumSlots)
	}
	if p.hdr.FreeSpaceOffset != HeaderSize {
		t.Errorf("FreeSpaceOffset: got %d, want %d", p.hdr.FreeSpaceOffset, HeaderSize)
	}
	if p.hdr.FreeSpaceEnd != PageSize {
		t.Errorf("FreeSpaceEnd: got %d, want %d", p.hdr.FreeSpaceEnd, PageSize)
	}
	if len(p.slots) != 0 {
		t.Errorf("slots len: got %d, want 0", len(p.slots))
	}
	want := PageSize - HeaderSize
	if p.FreeSpace() != want {
		t.Errorf("FreeSpace: got %d, want %d", p.FreeSpace(), want)
	}
}

// ---- TestInsertTuple ---------------------------------------------------------

func TestInsertTuple(t *testing.T) {
	t.Run("single insert", func(t *testing.T) {
		p := NewPage(1, DataPage)
		data := []byte("hello")
		id, err := p.InsertTuple(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != 0 {
			t.Errorf("SlotID: got %d, want 0", id)
		}
		if p.hdr.NumSlots != 1 {
			t.Errorf("NumSlots: got %d, want 1", p.hdr.NumSlots)
		}
		wantFree := PageSize - HeaderSize - SlotSize - len(data)
		if p.FreeSpace() != wantFree {
			t.Errorf("FreeSpace: got %d, want %d", p.FreeSpace(), wantFree)
		}
	})

	t.Run("second insert gets SlotID 1", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("first"))
		id, err := p.InsertTuple([]byte("second"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != 1 {
			t.Errorf("SlotID: got %d, want 1", id)
		}
		if p.hdr.NumSlots != 2 {
			t.Errorf("NumSlots: got %d, want 2", p.hdr.NumSlots)
		}
	})

	t.Run("tuple written at correct raw offset", func(t *testing.T) {
		p := NewPage(1, DataPage)
		data := []byte{0xAB, 0xCD}
		p.InsertTuple(data)
		// Tuple must be at the end of the page.
		wantOffset := PageSize - len(data)
		if got := p.raw[wantOffset : wantOffset+len(data)]; !bytes.Equal(got, data) {
			t.Errorf("raw bytes at %d: got %v, want %v", wantOffset, got, data)
		}
	})

	t.Run("slot entry written at correct raw offset", func(t *testing.T) {
		p := NewPage(1, DataPage)
		data := []byte{0xAB, 0xCD}
		p.InsertTuple(data)
		slotOff := HeaderSize // first slot starts right after header
		gotOffset := binary.LittleEndian.Uint16(p.raw[slotOff:])
		gotLength := binary.LittleEndian.Uint16(p.raw[slotOff+2:])
		wantTupleOffset := uint16(PageSize - len(data))
		if gotOffset != wantTupleOffset {
			t.Errorf("slot Offset: got %d, want %d", gotOffset, wantTupleOffset)
		}
		if gotLength != uint16(len(data)) {
			t.Errorf("slot Length: got %d, want %d", gotLength, len(data))
		}
	})
}

// ---- TestInsertTupleErrors ---------------------------------------------------

func TestInsertTupleErrors(t *testing.T) {
	t.Run("empty tuple", func(t *testing.T) {
		p := NewPage(1, DataPage)
		_, err := p.InsertTuple([]byte{})
		if err == nil {
			t.Fatal("expected error for empty tuple, got nil")
		}
	})

	t.Run("page full", func(t *testing.T) {
		p := NewPage(1, DataPage)
		// Fill the page with 1-byte tuples.
		for {
			_, err := p.InsertTuple([]byte{0xFF})
			if err != nil {
				var ef *ErrPageFull
				if !errors.As(err, &ef) {
					t.Fatalf("expected *ErrPageFull, got %T: %v", err, err)
				}
				if ef.Available < 0 {
					t.Errorf("ErrPageFull.Available is negative: %d", ef.Available)
				}
				if ef.Requested != SlotSize+1 {
					t.Errorf("ErrPageFull.Requested: got %d, want %d", ef.Requested, SlotSize+1)
				}
				break
			}
		}
	})
}

// ---- TestGetTuple ------------------------------------------------------------

func TestGetTuple(t *testing.T) {
	t.Run("data round-trip", func(t *testing.T) {
		p := NewPage(1, DataPage)
		data := []byte("round-trip data")
		id, _ := p.InsertTuple(data)
		got, err := p.GetTuple(id)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("got %v, want %v", got, data)
		}
	})

	t.Run("returned slice is a copy", func(t *testing.T) {
		p := NewPage(1, DataPage)
		data := []byte("original")
		id, _ := p.InsertTuple(data)
		got, _ := p.GetTuple(id)
		got[0] = 0xFF // mutate the returned slice
		again, _ := p.GetTuple(id)
		if again[0] == 0xFF {
			t.Error("GetTuple returned a sub-slice of raw; mutations should not be visible")
		}
	})

	t.Run("invalid slot", func(t *testing.T) {
		p := NewPage(1, DataPage)
		_, err := p.GetTuple(99)
		var e *ErrInvalidSlot
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidSlot, got %T: %v", err, err)
		}
		if e.ID != 99 {
			t.Errorf("ErrInvalidSlot.ID: got %d, want 99", e.ID)
		}
	})

	t.Run("deleted slot", func(t *testing.T) {
		p := NewPage(1, DataPage)
		id, _ := p.InsertTuple([]byte("to delete"))
		p.DeleteTuple(id)
		_, err := p.GetTuple(id)
		var e *ErrDeletedSlot
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrDeletedSlot, got %T: %v", err, err)
		}
		if e.ID != id {
			t.Errorf("ErrDeletedSlot.ID: got %d, want %d", e.ID, id)
		}
	})
}

// ---- TestDeleteTuple ---------------------------------------------------------

func TestDeleteTuple(t *testing.T) {
	t.Run("tombstone written to slots and raw", func(t *testing.T) {
		p := NewPage(1, DataPage)
		id, _ := p.InsertTuple([]byte("bye"))
		err := p.DeleteTuple(id)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.slots[id].isDeleted() {
			t.Error("slot not marked deleted in-memory")
		}
		// Check raw bytes at slot position.
		slotOff := HeaderSize + int(id)*SlotSize
		rawOff := binary.LittleEndian.Uint16(p.raw[slotOff:])
		rawLen := binary.LittleEndian.Uint16(p.raw[slotOff+2:])
		if rawOff != DeletedOffset || rawLen != DeletedLength {
			t.Errorf("raw sentinel: got Offset=%d Length=%d, want %d %d",
				rawOff, rawLen, DeletedOffset, DeletedLength)
		}
	})

	t.Run("NumSlots unchanged after delete", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("a"))
		p.InsertTuple([]byte("b"))
		p.DeleteTuple(0)
		if p.hdr.NumSlots != 2 {
			t.Errorf("NumSlots: got %d, want 2", p.hdr.NumSlots)
		}
	})

	t.Run("FreeSpace unchanged after delete", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("data"))
		freeBefore := p.FreeSpace()
		p.DeleteTuple(0)
		if p.FreeSpace() != freeBefore {
			t.Errorf("FreeSpace changed after delete: before %d, after %d", freeBefore, p.FreeSpace())
		}
	})

	t.Run("double delete returns error", func(t *testing.T) {
		p := NewPage(1, DataPage)
		id, _ := p.InsertTuple([]byte("once"))
		p.DeleteTuple(id)
		err := p.DeleteTuple(id)
		var e *ErrDeletedSlot
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrDeletedSlot, got %T: %v", err, err)
		}
	})

	t.Run("invalid slot", func(t *testing.T) {
		p := NewPage(1, DataPage)
		err := p.DeleteTuple(99)
		var e *ErrInvalidSlot
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidSlot, got %T: %v", err, err)
		}
	})
}

// ---- TestFreeSpace -----------------------------------------------------------

func TestFreeSpace(t *testing.T) {
	p := NewPage(1, DataPage)
	initial := p.FreeSpace()
	if initial != PageSize-HeaderSize {
		t.Errorf("initial FreeSpace: got %d, want %d", initial, PageSize-HeaderSize)
	}

	data := []byte("test")
	p.InsertTuple(data)
	afterInsert := p.FreeSpace()
	wantAfter := initial - SlotSize - len(data)
	if afterInsert != wantAfter {
		t.Errorf("FreeSpace after insert: got %d, want %d", afterInsert, wantAfter)
	}

	p.DeleteTuple(0)
	if p.FreeSpace() != afterInsert {
		t.Errorf("FreeSpace changed after delete (should be unchanged until compact)")
	}

	p.Compact()
	// After compact all space from the deleted tuple's bytes is reclaimed.
	wantAfterCompact := initial - SlotSize // slot entry remains; tuple bytes freed
	if p.FreeSpace() != wantAfterCompact {
		t.Errorf("FreeSpace after compact: got %d, want %d", p.FreeSpace(), wantAfterCompact)
	}
}

// ---- TestCompact -------------------------------------------------------------

func TestCompact(t *testing.T) {
	t.Run("live slots accessible after compact", func(t *testing.T) {
		p := NewPage(1, DataPage)
		dataA := []byte("AAAA")
		dataB := []byte("BBBB")
		dataC := []byte("CCCC")
		idA, _ := p.InsertTuple(dataA)
		idB, _ := p.InsertTuple(dataB)
		idC, _ := p.InsertTuple(dataC)

		p.DeleteTuple(idB)
		if err := p.Compact(); err != nil {
			t.Fatalf("Compact error: %v", err)
		}

		gotA, err := p.GetTuple(idA)
		if err != nil || !bytes.Equal(gotA, dataA) {
			t.Errorf("slot A after compact: err=%v data=%v", err, gotA)
		}
		gotC, err := p.GetTuple(idC)
		if err != nil || !bytes.Equal(gotC, dataC) {
			t.Errorf("slot C after compact: err=%v data=%v", err, gotC)
		}
		_, err = p.GetTuple(idB)
		var e *ErrDeletedSlot
		if !errors.As(err, &e) {
			t.Errorf("slot B after compact: expected ErrDeletedSlot, got %v", err)
		}
	})

	t.Run("free space reclaimed", func(t *testing.T) {
		p := NewPage(1, DataPage)
		data := []byte("to-delete-data")
		id, _ := p.InsertTuple(data)
		freeAfterInsert := p.FreeSpace()
		p.DeleteTuple(id)
		p.Compact()
		// Slot entry bytes stay; only the tuple bytes are reclaimed.
		want := freeAfterInsert + len(data)
		if p.FreeSpace() != want {
			t.Errorf("FreeSpace after compact: got %d, want %d", p.FreeSpace(), want)
		}
	})

	t.Run("insert after compact uses reclaimed space", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("AAA"))
		p.InsertTuple([]byte("BBB"))
		p.DeleteTuple(0)
		p.Compact()
		id, err := p.InsertTuple([]byte("new"))
		if err != nil {
			t.Fatalf("insert after compact failed: %v", err)
		}
		if id != 2 {
			t.Errorf("SlotID after compact insert: got %d, want 2", id)
		}
	})

	t.Run("compact with no deletions is a no-op", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("x"))
		freeBefore := p.FreeSpace()
		p.Compact()
		if p.FreeSpace() != freeBefore {
			t.Errorf("FreeSpace changed on no-op compact: before %d, after %d", freeBefore, p.FreeSpace())
		}
	})

	t.Run("NumSlots unchanged after compact", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("a"))
		p.InsertTuple([]byte("b"))
		p.InsertTuple([]byte("c"))
		p.DeleteTuple(1)
		p.Compact()
		if p.hdr.NumSlots != 3 {
			t.Errorf("NumSlots after compact: got %d, want 3", p.hdr.NumSlots)
		}
	})
}

// ---- TestMarshalUnmarshal ----------------------------------------------------

func TestMarshalUnmarshal(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		p := NewPage(7, DataPage)
		data1 := []byte("first tuple")
		data2 := []byte("second tuple")
		p.InsertTuple(data1)
		p.InsertTuple(data2)

		raw, err := p.Marshal()
		if err != nil {
			t.Fatalf("Marshal error: %v", err)
		}
		if len(raw) != PageSize {
			t.Fatalf("Marshal length: got %d, want %d", len(raw), PageSize)
		}

		p2 := &Page{}
		if err := p2.Unmarshal(raw); err != nil {
			t.Fatalf("Unmarshal error: %v", err)
		}
		got1, _ := p2.GetTuple(0)
		got2, _ := p2.GetTuple(1)
		if !bytes.Equal(got1, data1) {
			t.Errorf("tuple 0: got %v, want %v", got1, data1)
		}
		if !bytes.Equal(got2, data2) {
			t.Errorf("tuple 1: got %v, want %v", got2, data2)
		}
		if p2.hdr.PageID != 7 {
			t.Errorf("PageID: got %d, want 7", p2.hdr.PageID)
		}
	})

	t.Run("marshal returns a copy", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("data"))
		raw, _ := p.Marshal()
		raw[0] = 0xFF // mutate the returned slice
		raw2, _ := p.Marshal()
		if raw2[0] == 0xFF {
			t.Error("Marshal returned a reference to internal raw; mutations must not affect page")
		}
	})

	t.Run("checksum field is non-zero after marshal", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("data"))
		raw, _ := p.Marshal()
		cs := binary.LittleEndian.Uint32(raw[hdrOffChecksum:])
		if cs == 0 {
			t.Error("checksum is zero after marshal (extremely unlikely for non-empty page)")
		}
	})
}

// ---- TestUnmarshalErrors -----------------------------------------------------

func TestUnmarshalErrors(t *testing.T) {
	t.Run("wrong length", func(t *testing.T) {
		p := &Page{}
		err := p.Unmarshal(make([]byte, PageSize-1))
		var e *ErrInvalidPage
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidPage, got %T: %v", err, err)
		}
	})

	t.Run("corrupt checksum", func(t *testing.T) {
		orig := NewPage(1, DataPage)
		orig.InsertTuple([]byte("data"))
		raw, _ := orig.Marshal()
		// Flip a data byte (not in the checksum field itself).
		raw[PageSize-1] ^= 0xFF
		p := &Page{}
		err := p.Unmarshal(raw)
		var e *ErrInvalidPage
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidPage, got %T: %v", err, err)
		}
	})

	t.Run("FreeSpaceOffset below HeaderSize", func(t *testing.T) {
		orig := NewPage(1, DataPage)
		raw, _ := orig.Marshal()
		// Write invalid FreeSpaceOffset.
		binary.LittleEndian.PutUint16(raw[hdrOffFreeSpaceOffset:], HeaderSize-1)
		// Recompute checksum so it passes that check.
		var tmp [PageSize]byte
		copy(tmp[:], raw)
		tmp[hdrOffChecksum] = 0
		tmp[hdrOffChecksum+1] = 0
		tmp[hdrOffChecksum+2] = 0
		tmp[hdrOffChecksum+3] = 0
		cs := crc32ChecksumIEEE(tmp[:])
		binary.LittleEndian.PutUint32(raw[hdrOffChecksum:], cs)
		p := &Page{}
		err := p.Unmarshal(raw)
		var e *ErrInvalidPage
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidPage, got %T: %v", err, err)
		}
	})

	t.Run("FreeSpaceOffset greater than FreeSpaceEnd", func(t *testing.T) {
		orig := NewPage(1, DataPage)
		raw, _ := orig.Marshal()
		binary.LittleEndian.PutUint16(raw[hdrOffFreeSpaceEnd:], HeaderSize-1)
		var tmp [PageSize]byte
		copy(tmp[:], raw)
		tmp[hdrOffChecksum] = 0
		tmp[hdrOffChecksum+1] = 0
		tmp[hdrOffChecksum+2] = 0
		tmp[hdrOffChecksum+3] = 0
		cs := crc32ChecksumIEEE(tmp[:])
		binary.LittleEndian.PutUint32(raw[hdrOffChecksum:], cs)
		p := &Page{}
		err := p.Unmarshal(raw)
		var e *ErrInvalidPage
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidPage, got %T: %v", err, err)
		}
	})
}

// ---- TestValidate ------------------------------------------------------------

func TestValidate(t *testing.T) {
	t.Run("valid page passes", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("hello"))
		p.hdr.Checksum = p.computeChecksum()
		if err := p.Validate(); err != nil {
			t.Errorf("valid page failed Validate: %v", err)
		}
	})

	t.Run("corrupt checksum detected", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("hello"))
		p.hdr.Checksum = p.computeChecksum()
		p.hdr.Checksum ^= 0xDEADBEEF // corrupt it
		err := p.Validate()
		var e *ErrInvalidPage
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidPage, got %T: %v", err, err)
		}
	})

	t.Run("NumSlots mismatch detected", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("hello"))
		p.hdr.Checksum = p.computeChecksum()
		p.hdr.NumSlots = 99 // desync from len(slots)
		err := p.Validate()
		var e *ErrInvalidPage
		if !errors.As(err, &e) {
			t.Fatalf("expected *ErrInvalidPage, got %T: %v", err, err)
		}
	})
}

// ---- TestChecksum ------------------------------------------------------------

func TestChecksum(t *testing.T) {
	t.Run("identical pages produce identical checksums", func(t *testing.T) {
		p1 := NewPage(5, DataPage)
		p1.InsertTuple([]byte("same"))
		p2 := NewPage(5, DataPage)
		p2.InsertTuple([]byte("same"))
		if p1.computeChecksum() != p2.computeChecksum() {
			t.Error("identical pages produced different checksums")
		}
	})

	t.Run("byte flip changes checksum", func(t *testing.T) {
		p := NewPage(1, DataPage)
		p.InsertTuple([]byte("data"))
		cs1 := p.computeChecksum()
		p.raw[PageSize-1] ^= 0xFF
		cs2 := p.computeChecksum()
		if cs1 == cs2 {
			t.Error("checksum unchanged after byte flip")
		}
	})

	t.Run("checksum field does not cover itself", func(t *testing.T) {
		p := NewPage(1, DataPage)
		cs1 := p.computeChecksum()
		p.hdr.Checksum = cs1
		p.marshalHeader()
		cs2 := p.computeChecksum()
		if cs1 != cs2 {
			t.Error("checksum changed after writing itself into the page (checksum covers itself)")
		}
	})
}

// TestChecksum_Equivalence verifies that the optimized three-segment CRC
// computation produces identical results to the old full-copy implementation.
func TestChecksum_Equivalence(t *testing.T) {
	// Old implementation (for comparison)
	computeChecksumOld := func(p *Page) uint32 {
		var tmp [PageSize]byte
		copy(tmp[:], p.raw[:])
		tmp[hdrOffChecksum] = 0
		tmp[hdrOffChecksum+1] = 0
		tmp[hdrOffChecksum+2] = 0
		tmp[hdrOffChecksum+3] = 0
		return crc32.ChecksumIEEE(tmp[:])
	}

	// Test with empty page
	t.Run("empty page", func(t *testing.T) {
		pg := NewPage(123, DataPage)
		old := computeChecksumOld(pg)
		new := pg.computeChecksum()

		if old != new {
			t.Errorf("checksum mismatch on empty page: old=%d new=%d", old, new)
		}
	})

	// Test with single tuple
	t.Run("single tuple", func(t *testing.T) {
		pg := NewPage(456, DataPage)
		pg.InsertTuple([]byte("test data 1"))

		old := computeChecksumOld(pg)
		new := pg.computeChecksum()

		if old != new {
			t.Errorf("checksum mismatch with single tuple: old=%d new=%d", old, new)
		}
	})

	// Test with multiple tuples
	t.Run("multiple tuples", func(t *testing.T) {
		pg := NewPage(789, DataPage)
		pg.InsertTuple([]byte("test data 1"))
		pg.InsertTuple([]byte("test data 2"))
		pg.InsertTuple([]byte("longer test data string here"))

		old := computeChecksumOld(pg)
		new := pg.computeChecksum()

		if old != new {
			t.Errorf("checksum mismatch with multiple tuples: old=%d new=%d", old, new)
		}
	})
}

// ---- TestSlotIDStability -----------------------------------------------------

func TestSlotIDStability(t *testing.T) {
	p := NewPage(1, DataPage)
	tuples := [][]byte{
		[]byte("zero"), []byte("one"), []byte("two"), []byte("three"), []byte("four"),
	}
	ids := make([]SlotID, len(tuples))
	for i, d := range tuples {
		id, err := p.InsertTuple(d)
		if err != nil {
			t.Fatalf("InsertTuple %d: %v", i, err)
		}
		ids[i] = id
	}

	// Delete slots 1 and 3.
	p.DeleteTuple(ids[1])
	p.DeleteTuple(ids[3])
	p.Compact()

	// Live slots 0, 2, 4 must still return correct data.
	for _, i := range []int{0, 2, 4} {
		got, err := p.GetTuple(ids[i])
		if err != nil {
			t.Errorf("slot %d after compact: unexpected error %v", ids[i], err)
		}
		if !bytes.Equal(got, tuples[i]) {
			t.Errorf("slot %d after compact: got %v, want %v", ids[i], got, tuples[i])
		}
	}

	// Deleted slots must still return ErrDeletedSlot.
	for _, i := range []int{1, 3} {
		_, err := p.GetTuple(ids[i])
		var e *ErrDeletedSlot
		if !errors.As(err, &e) {
			t.Errorf("slot %d after compact: expected ErrDeletedSlot, got %v", ids[i], err)
		}
	}

	// New insert gets next sequential ID (5).
	newID, err := p.InsertTuple([]byte("five"))
	if err != nil {
		t.Fatalf("InsertTuple after compact: %v", err)
	}
	if newID != 5 {
		t.Errorf("new SlotID after compact: got %d, want 5", newID)
	}
}

// ---- TestMaxCapacity ---------------------------------------------------------

func TestMaxCapacity(t *testing.T) {
	p := NewPage(1, DataPage)
	count := 0
	for {
		_, err := p.InsertTuple([]byte{0xFF})
		if err != nil {
			var ef *ErrPageFull
			if !errors.As(err, &ef) {
				t.Fatalf("unexpected error type: %T %v", err, err)
			}
			break
		}
		count++
	}
	// Each 1-byte tuple costs SlotSize(4) + 1 = 5 bytes.
	// Available space: PageSize - HeaderSize = 8165 bytes.
	// Maximum tuples: floor(8165 / 5) = 1633.
	want := (PageSize - HeaderSize) / (SlotSize + 1)
	if count != want {
		t.Errorf("max 1-byte tuples: got %d, want %d", count, want)
	}
}

// ---- TestBinaryLayout --------------------------------------------------------

func TestBinaryLayout(t *testing.T) {
	p := NewPage(42, DataPage)

	t.Run("PageID in raw bytes", func(t *testing.T) {
		got := binary.LittleEndian.Uint64(p.raw[hdrOffPageID:])
		if got != 42 {
			t.Errorf("raw PageID: got %d, want 42", got)
		}
	})

	t.Run("PageType in raw bytes", func(t *testing.T) {
		if p.raw[hdrOffPageType] != uint8(DataPage) {
			t.Errorf("raw PageType: got %d, want %d", p.raw[hdrOffPageType], uint8(DataPage))
		}
	})

	t.Run("NumSlots in raw bytes", func(t *testing.T) {
		got := binary.LittleEndian.Uint16(p.raw[hdrOffNumSlots:])
		if got != 0 {
			t.Errorf("raw NumSlots: got %d, want 0", got)
		}
	})

	t.Run("FreeSpaceOffset in raw bytes", func(t *testing.T) {
		got := binary.LittleEndian.Uint16(p.raw[hdrOffFreeSpaceOffset:])
		if got != HeaderSize {
			t.Errorf("raw FreeSpaceOffset: got %d, want %d", got, HeaderSize)
		}
	})

	t.Run("FreeSpaceEnd in raw bytes", func(t *testing.T) {
		got := binary.LittleEndian.Uint16(p.raw[hdrOffFreeSpaceEnd:])
		if got != PageSize {
			t.Errorf("raw FreeSpaceEnd: got %d, want %d", got, PageSize)
		}
	})

	t.Run("tuple and slot layout after insert", func(t *testing.T) {
		p2 := NewPage(1, DataPage)
		data := []byte{0xAB, 0xCD}
		p2.InsertTuple(data)

		// Tuple must be at the last 2 bytes.
		if p2.raw[PageSize-2] != 0xAB || p2.raw[PageSize-1] != 0xCD {
			t.Errorf("tuple bytes at end of page: got %v, want [0xAB 0xCD]",
				p2.raw[PageSize-2:])
		}
		// Slot[0] Offset must encode PageSize-2.
		slotOff := HeaderSize
		gotOffset := binary.LittleEndian.Uint16(p2.raw[slotOff:])
		gotLength := binary.LittleEndian.Uint16(p2.raw[slotOff+2:])
		if gotOffset != uint16(PageSize-2) {
			t.Errorf("slot[0].Offset: got %d, want %d", gotOffset, PageSize-2)
		}
		if gotLength != 2 {
			t.Errorf("slot[0].Length: got %d, want 2", gotLength)
		}
	})
}

// ---- helpers -----------------------------------------------------------------

// crc32ChecksumIEEE is a package-level helper used only in tests that need to
// manually compute CRC32 when crafting corrupt page fixtures.
func crc32ChecksumIEEE(data []byte) uint32 {
	p := &Page{}
	copy(p.raw[:], data)
	return p.computeChecksum()
}

// ---- benchmarks --------------------------------------------------------------

// BenchmarkCompact measures the performance of page compaction with varying
// numbers of live tuples, verifying that buffer pooling eliminates per-tuple
// allocations.
func BenchmarkCompact(b *testing.B) {
	testCases := []struct {
		nTuples int
		data    []byte
	}{
		{10, []byte("test data 1234567890")},     // 21 bytes × 10 = 210 bytes
		{100, []byte("test data 1234567890")},    // 21 bytes × 100 = 2.1 KB
		{1000, []byte("xy")},                      // 2 bytes × 1000 = 2 KB (fits with slots)
	}

	for _, tc := range testCases {
		b.Run(fmt.Sprintf("tuples=%d", tc.nTuples), func(b *testing.B) {
			pg := NewPage(1, DataPage)
			for i := 0; i < tc.nTuples; i++ {
				_, err := pg.InsertTuple(tc.data)
				if err != nil {
					b.Fatalf("InsertTuple: %v", err)
				}
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				// Create a copy to restore state between iterations
				pgCopy := *pg
				b.StartTimer()

				if err := pgCopy.Compact(); err != nil {
					b.Fatalf("Compact: %v", err)
				}
			}
		})
	}
}
