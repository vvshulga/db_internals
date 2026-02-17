package storage

import (
	"testing"
)

func TestRID_Comparable(t *testing.T) {
	r1 := RID{PageID: 1, SlotID: 2}
	r2 := RID{PageID: 1, SlotID: 2}
	r3 := RID{PageID: 1, SlotID: 3}
	if r1 != r2 {
		t.Error("equal RIDs should compare equal")
	}
	if r1 == r3 {
		t.Error("different RIDs should not compare equal")
	}
}

func TestRID_MapKey(t *testing.T) {
	m := make(map[RID]int)
	r := RID{PageID: 42, SlotID: 7}
	m[r] = 99
	if m[r] != 99 {
		t.Errorf("map lookup: got %d, want 99", m[r])
	}
}

func TestRID_String(t *testing.T) {
	tests := []struct {
		rid  RID
		want string
	}{
		{RID{1, 0}, "1:0"},
		{RID{42, 7}, "42:7"},
		{RID{0, 0}, "0:0"},
	}
	for _, tc := range tests {
		if got := tc.rid.String(); got != tc.want {
			t.Errorf("RID%v.String() = %q, want %q", tc.rid, got, tc.want)
		}
	}
}

func TestRID_IsZero(t *testing.T) {
	if !(RID{}).IsZero() {
		t.Error("zero-value RID should be zero")
	}
	if (RID{PageID: 1}).IsZero() {
		t.Error("RID with PageID=1 should not be zero")
	}
	if (RID{SlotID: 1}).IsZero() {
		t.Error("RID with SlotID=1 should not be zero")
	}
}

func TestRID_EncodeDecodeRoundTrip(t *testing.T) {
	r := RID{PageID: 0xDEADBEEFCAFEBABE, SlotID: 0x1234}
	enc := r.Encode()
	dec := DecodeRID(enc)
	if dec != r {
		t.Errorf("round-trip: got %v, want %v", dec, r)
	}
}

func TestRID_EncodeIsLittleEndian(t *testing.T) {
	r := RID{PageID: 1, SlotID: 2}
	enc := r.Encode()
	// PageID=1 in LE: [0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
	if enc[0] != 0x01 || enc[1] != 0x00 {
		t.Errorf("PageID not little-endian: %v", enc[:8])
	}
	// SlotID=2 in LE: [0x02, 0x00]
	if enc[8] != 0x02 || enc[9] != 0x00 {
		t.Errorf("SlotID not little-endian: %v", enc[8:])
	}
}
