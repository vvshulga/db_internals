// Package storage provides the low-level disk I/O primitives for the database
// engine. The fundamental unit is the Page: a fixed-size 8192-byte buffer that
// implements a slotted layout suitable for storing variable-length tuples.
package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// ---- Constants ----------------------------------------------------------------

const (
	// PageSize is the fixed size of every page in bytes (8 KiB).
	PageSize = 8192

	// HeaderSize is the number of bytes occupied by the page header.
	// Layout: PageID(8) + PageType(1) + NumSlots(2) + FreeSpaceOffset(2) +
	//         FreeSpaceEnd(2) + Checksum(4) = 19 bytes.
	HeaderSize = 19

	// SlotSize is the number of bytes per slot entry (Offset uint16 + Length uint16).
	SlotSize = 4

	// DeletedOffset and DeletedLength are the sentinel values written into a slot
	// entry when the corresponding tuple is deleted. A tuple can never legitimately
	// start at offset 0xFFFF in an 8192-byte page.
	DeletedOffset = uint16(0xFFFF)
	DeletedLength = uint16(0xFFFF)
)

// Byte offsets within the raw header region (used only in marshal/unmarshal helpers).
const (
	hdrOffPageID          = 0  // uint64, 8 bytes
	hdrOffPageType        = 8  // uint8,  1 byte
	hdrOffNumSlots        = 9  // uint16, 2 bytes
	hdrOffFreeSpaceOffset = 11 // uint16, 2 bytes
	hdrOffFreeSpaceEnd    = 13 // uint16, 2 bytes
	hdrOffChecksum        = 15 // uint32, 4 bytes
)

// ---- PageType -----------------------------------------------------------------

// PageType classifies the role of a page within the storage file.
type PageType uint8

const (
	DataPage     PageType = 1
	OverflowPage PageType = 2
	MetaPage     PageType = 3
	FreelistPage PageType = 4
)

// ---- SlotID -------------------------------------------------------------------

// SlotID is the stable index of a tuple within a page's slot array.
// SlotIDs are never reused or renumbered, even after compaction.
type SlotID uint16

// ---- Internal structs ---------------------------------------------------------

// header holds the parsed representation of the first HeaderSize bytes of a page.
type header struct {
	PageID          uint64
	PageType        PageType
	NumSlots        uint16
	FreeSpaceOffset uint16 // byte offset to end of slot array
	FreeSpaceEnd    uint16 // byte offset to start of tuple data region
	Checksum        uint32
}

// slotEntry records the location of one tuple within the page.
type slotEntry struct {
	Offset uint16
	Length uint16
}

func (s slotEntry) isDeleted() bool {
	return s.Offset == DeletedOffset && s.Length == DeletedLength
}

// ---- Page ---------------------------------------------------------------------

// Page is a fixed-size 8192-byte database page implementing a slotted layout.
//
// Memory layout:
//
//	[0..18]       Header (19 bytes, little-endian encoded)
//	[19..]        Slot array, grows toward higher addresses
//	              ...free space...
//	[..8191]      Tuple data, grows toward lower addresses
//
// The raw field is the authoritative representation for disk I/O. The hdr and
// slots fields are the in-memory parsed view, kept in sync by all mutating
// methods.
type Page struct {
	raw   [PageSize]byte
	hdr   header
	slots []slotEntry
}

// NewPage creates a new empty page with the given ID and type.
// The page is ready for tuple insertions immediately.
func NewPage(id uint64, pageType PageType) *Page {
	p := &Page{}
	p.hdr.PageID = id
	p.hdr.PageType = pageType
	p.hdr.FreeSpaceOffset = HeaderSize
	p.hdr.FreeSpaceEnd = PageSize
	p.hdr.NumSlots = 0
	p.slots = make([]slotEntry, 0)
	p.marshalHeader()
	return p
}

// FreeSpace returns the number of bytes available between the end of the slot
// array and the start of the tuple data region.
func (p *Page) FreeSpace() int {
	return int(p.hdr.FreeSpaceEnd) - int(p.hdr.FreeSpaceOffset)
}

// PageID returns the page's unique identifier.
func (p *Page) PageID() uint64 {
	return p.hdr.PageID
}

// PageType returns the page's type.
func (p *Page) Type() PageType {
	return p.hdr.PageType
}

// NumSlots returns the total number of slot entries, including deleted ones.
func (p *Page) NumSlots() int {
	return int(p.hdr.NumSlots)
}

// InsertTuple stores data on the page and returns the stable SlotID that
// identifies this tuple. The tuple is written at the current end of the tuple
// region and a new slot entry is appended to the slot array.
//
// Returns ErrPageFull if the page cannot accommodate the tuple plus its slot entry.
// Returns an error if data is empty.
func (p *Page) InsertTuple(data []byte) (SlotID, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("storage: cannot insert empty tuple")
	}
	if len(data) > PageSize {
		return 0, fmt.Errorf("storage: tuple size %d exceeds page size %d", len(data), PageSize)
	}
	needed := SlotSize + len(data)
	if p.FreeSpace() < needed {
		return 0, &ErrPageFull{Available: p.FreeSpace(), Requested: needed}
	}

	// Write tuple data at the top of the free region.
	newEnd := p.hdr.FreeSpaceEnd - uint16(len(data))
	copy(p.raw[newEnd:], data)

	// Record the slot entry and update header fields.
	entry := slotEntry{Offset: newEnd, Length: uint16(len(data))}
	slotID := SlotID(len(p.slots))
	p.slots = append(p.slots, entry)
	p.hdr.NumSlots++
	p.hdr.FreeSpaceEnd = newEnd
	p.hdr.FreeSpaceOffset += SlotSize

	p.marshalSlot(slotID, entry)
	p.marshalHeader()
	return slotID, nil
}

// GetTuple retrieves a copy of the tuple stored at the given SlotID.
// The returned slice is independent of the page's internal buffer.
//
// Returns ErrInvalidSlot if slotID is out of range.
// Returns ErrDeletedSlot if the slot has been deleted.
func (p *Page) GetTuple(slotID SlotID) ([]byte, error) {
	if int(slotID) >= len(p.slots) {
		return nil, &ErrInvalidSlot{ID: slotID, NumSlots: len(p.slots)}
	}
	entry := p.slots[slotID]
	if entry.isDeleted() {
		return nil, &ErrDeletedSlot{ID: slotID}
	}
	result := make([]byte, entry.Length)
	copy(result, p.raw[entry.Offset:entry.Offset+entry.Length])
	return result, nil
}

// DeleteTuple marks the slot at slotID as deleted by writing a tombstone
// sentinel. The tuple's bytes remain on the page until Compact is called.
//
// Returns ErrInvalidSlot if slotID is out of range.
// Returns ErrDeletedSlot if the slot has already been deleted.
func (p *Page) DeleteTuple(slotID SlotID) error {
	if int(slotID) >= len(p.slots) {
		return &ErrInvalidSlot{ID: slotID, NumSlots: len(p.slots)}
	}
	if p.slots[slotID].isDeleted() {
		return &ErrDeletedSlot{ID: slotID}
	}
	tombstone := slotEntry{Offset: DeletedOffset, Length: DeletedLength}
	p.slots[slotID] = tombstone
	p.marshalSlot(slotID, tombstone)
	return nil
}

// Compact defragments the tuple region by rewriting all live tuples
// contiguously at the end of the page. Deleted slot entries are preserved
// in the slot array as tombstones; their slot IDs remain permanently invalid.
//
// After compaction all live slot IDs remain valid and return the same data.
// New tuples inserted after compaction will reuse the recovered free space.
func (p *Page) Compact() error {
	writeCursor := PageSize
	for i := range p.slots {
		entry := p.slots[i]
		if entry.isDeleted() {
			continue
		}
		// Make a local copy before moving (source and destination may overlap).
		tmp := make([]byte, entry.Length)
		copy(tmp, p.raw[entry.Offset:entry.Offset+entry.Length])

		newOffset := uint16(writeCursor) - entry.Length
		copy(p.raw[newOffset:], tmp)

		p.slots[i].Offset = newOffset
		p.marshalSlot(SlotID(i), p.slots[i])
		writeCursor = int(newOffset)
	}
	p.hdr.FreeSpaceEnd = uint16(writeCursor)
	p.marshalHeader()
	return nil
}

// Marshal serializes the page to a fresh 8192-byte slice suitable for writing
// to disk. The checksum is recomputed before serialization.
//
// The returned slice is independent of the page's internal buffer.
func (p *Page) Marshal() ([]byte, error) {
	p.hdr.Checksum = p.computeChecksum()
	p.marshalHeader()
	result := make([]byte, PageSize)
	copy(result, p.raw[:])
	return result, nil
}

// Unmarshal deserializes an 8192-byte slice into the page, validating the
// page structure and checksum before populating the in-memory state.
//
// Returns ErrInvalidPage if data has the wrong length, structural invariants
// are violated, or the checksum does not match.
func (p *Page) Unmarshal(data []byte) error {
	if len(data) != PageSize {
		return &ErrInvalidPage{Reason: fmt.Sprintf("data length %d != %d", len(data), PageSize)}
	}
	copy(p.raw[:], data)
	p.unmarshalHeader()

	// Structural validation.
	if p.hdr.FreeSpaceOffset < HeaderSize {
		return &ErrInvalidPage{Reason: "FreeSpaceOffset below HeaderSize"}
	}
	if p.hdr.FreeSpaceEnd > PageSize {
		return &ErrInvalidPage{Reason: "FreeSpaceEnd exceeds PageSize"}
	}
	if p.hdr.FreeSpaceOffset > p.hdr.FreeSpaceEnd {
		return &ErrInvalidPage{Reason: "FreeSpaceOffset > FreeSpaceEnd"}
	}
	slotArrayBytes := int(p.hdr.FreeSpaceOffset) - HeaderSize
	if slotArrayBytes != int(p.hdr.NumSlots)*SlotSize {
		return &ErrInvalidPage{Reason: "slot array size inconsistent with NumSlots"}
	}

	// Checksum verification: save stored value before computing.
	storedChecksum := p.hdr.Checksum
	computed := p.computeChecksum()
	if storedChecksum != computed {
		return &ErrInvalidPage{Reason: "checksum mismatch"}
	}

	// Parse and validate the slot array.
	p.slots = make([]slotEntry, p.hdr.NumSlots)
	for i := range p.slots {
		p.slots[i] = p.unmarshalSlot(SlotID(i))
		if !p.slots[i].isDeleted() {
			o := uint32(p.slots[i].Offset)
			l := uint32(p.slots[i].Length)
			if o+l > PageSize {
				return &ErrInvalidPage{Reason: fmt.Sprintf("slot %d tuple out of bounds", i)}
			}
		}
	}
	return nil
}

// Validate verifies the in-memory consistency of the page, checking that
// header fields, the slot array, and the checksum are all coherent.
func (p *Page) Validate() error {
	if p.hdr.FreeSpaceOffset < HeaderSize {
		return &ErrInvalidPage{Reason: "FreeSpaceOffset below HeaderSize"}
	}
	if p.hdr.FreeSpaceEnd > PageSize {
		return &ErrInvalidPage{Reason: "FreeSpaceEnd exceeds PageSize"}
	}
	if p.hdr.FreeSpaceOffset > p.hdr.FreeSpaceEnd {
		return &ErrInvalidPage{Reason: "FreeSpaceOffset > FreeSpaceEnd"}
	}
	if int(p.hdr.NumSlots) != len(p.slots) {
		return &ErrInvalidPage{Reason: "NumSlots inconsistent with slots slice length"}
	}
	if int(p.hdr.FreeSpaceOffset) != HeaderSize+len(p.slots)*SlotSize {
		return &ErrInvalidPage{Reason: "FreeSpaceOffset inconsistent with slot count"}
	}
	computed := p.computeChecksum()
	if computed != p.hdr.Checksum {
		return &ErrInvalidPage{Reason: "checksum mismatch"}
	}
	return nil
}

// ---- Internal helpers ---------------------------------------------------------

// marshalHeader writes the current hdr fields into raw[0:HeaderSize].
func (p *Page) marshalHeader() {
	binary.LittleEndian.PutUint64(p.raw[hdrOffPageID:], p.hdr.PageID)
	p.raw[hdrOffPageType] = uint8(p.hdr.PageType)
	binary.LittleEndian.PutUint16(p.raw[hdrOffNumSlots:], p.hdr.NumSlots)
	binary.LittleEndian.PutUint16(p.raw[hdrOffFreeSpaceOffset:], p.hdr.FreeSpaceOffset)
	binary.LittleEndian.PutUint16(p.raw[hdrOffFreeSpaceEnd:], p.hdr.FreeSpaceEnd)
	binary.LittleEndian.PutUint32(p.raw[hdrOffChecksum:], p.hdr.Checksum)
}

// unmarshalHeader reads raw[0:HeaderSize] into hdr.
func (p *Page) unmarshalHeader() {
	p.hdr.PageID = binary.LittleEndian.Uint64(p.raw[hdrOffPageID:])
	p.hdr.PageType = PageType(p.raw[hdrOffPageType])
	p.hdr.NumSlots = binary.LittleEndian.Uint16(p.raw[hdrOffNumSlots:])
	p.hdr.FreeSpaceOffset = binary.LittleEndian.Uint16(p.raw[hdrOffFreeSpaceOffset:])
	p.hdr.FreeSpaceEnd = binary.LittleEndian.Uint16(p.raw[hdrOffFreeSpaceEnd:])
	p.hdr.Checksum = binary.LittleEndian.Uint32(p.raw[hdrOffChecksum:])
}

// marshalSlot writes a single slot entry into the raw byte array at the
// position corresponding to the given SlotID.
func (p *Page) marshalSlot(id SlotID, entry slotEntry) {
	off := HeaderSize + int(id)*SlotSize
	binary.LittleEndian.PutUint16(p.raw[off:], entry.Offset)
	binary.LittleEndian.PutUint16(p.raw[off+2:], entry.Length)
}

// unmarshalSlot reads a single slot entry from the raw byte array.
func (p *Page) unmarshalSlot(id SlotID) slotEntry {
	off := HeaderSize + int(id)*SlotSize
	return slotEntry{
		Offset: binary.LittleEndian.Uint16(p.raw[off:]),
		Length: binary.LittleEndian.Uint16(p.raw[off+2:]),
	}
}

// computeChecksum returns the CRC32-IEEE checksum of the page, computed with
// the checksum field bytes [23..26] zeroed so the checksum does not cover itself.
func (p *Page) computeChecksum() uint32 {
	var tmp [PageSize]byte
	copy(tmp[:], p.raw[:])
	tmp[hdrOffChecksum] = 0
	tmp[hdrOffChecksum+1] = 0
	tmp[hdrOffChecksum+2] = 0
	tmp[hdrOffChecksum+3] = 0
	return crc32.ChecksumIEEE(tmp[:])
}
