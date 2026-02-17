package storage

import (
	"encoding/binary"
	"fmt"
)

// RID (Row Identifier) is the stable, persistent reference to a single row.
// It combines the global page number and the slot within that page.
//
// PageID is a global page number across all segment files of a heap file.
// The heap layer maps global PageID to a specific segment file transparently.
//
// RID is directly usable as a map key because both fields are comparable types.
type RID struct {
	PageID uint64
	SlotID SlotID // SlotID = uint16, defined in page.go
}

// String returns a human-readable representation: "PageID:SlotID".
func (r RID) String() string {
	return fmt.Sprintf("%d:%d", r.PageID, r.SlotID)
}

// IsZero returns true for the zero-value RID. PageID == 0 is the metadata page
// and is never a valid data row location, making the zero value a safe sentinel.
func (r RID) IsZero() bool {
	return r.PageID == 0 && r.SlotID == 0
}

// RIDEncodedSize is the fixed byte size of a serialized RID.
// Used by index pages to store row pointers compactly.
const RIDEncodedSize = 10 // uint64(8) + uint16(2)

// Encode serializes r into a fixed 10-byte little-endian array.
func (r RID) Encode() [RIDEncodedSize]byte {
	var b [RIDEncodedSize]byte
	binary.LittleEndian.PutUint64(b[0:], r.PageID)
	binary.LittleEndian.PutUint16(b[8:], uint16(r.SlotID))
	return b
}

// DecodeRID deserializes a RID from a 10-byte little-endian array.
func DecodeRID(b [RIDEncodedSize]byte) RID {
	return RID{
		PageID: binary.LittleEndian.Uint64(b[0:]),
		SlotID: SlotID(binary.LittleEndian.Uint16(b[8:])),
	}
}
