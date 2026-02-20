package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// overflowChunkSize is the maximum number of value bytes stored in a single
// overflow page. The page body is slot 0 of an OverflowPage:
//
//	[NextPageID uint64 LE][chunk bytes…]
//
// Available bytes = PageSize − HeaderSize − SlotSize − 8 (NextPageID) = 8161.
const overflowChunkSize = PageSize - HeaderSize - SlotSize - 8

// writeOverflowChain writes data to a contiguous run of freshly-allocated
// OverflowPages, returning the global page ID of the first page.
//
// The chain is singly-linked: each page's slot-0 body starts with the next
// page's ID (0 = last). The caller must hold h.mu.
func writeOverflowChain(h *HeapFile, data []byte) (uint64, error) {
	numPages := (len(data) + overflowChunkSize - 1) / overflowChunkSize
	if numPages == 0 {
		numPages = 1 // even an empty value occupies one page
	}
	firstPageID := h.meta.TotalPages

	// Write pages in reverse order (last → first) so each page knows the ID
	// of its successor before it is written.
	for i := numPages - 1; i >= 0; i-- {
		start := i * overflowChunkSize
		end := start + overflowChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]

		var nextPageID uint64
		if i < numPages-1 {
			nextPageID = firstPageID + uint64(i+1)
		}

		body := make([]byte, 8+len(chunk))
		binary.LittleEndian.PutUint64(body, nextPageID)
		copy(body[8:], chunk)

		pg := NewPage(firstPageID+uint64(i), OverflowPage)
		if _, err := pg.InsertTuple(body); err != nil {
			return 0, fmt.Errorf("writeOverflowChain page %d: %w", i, err)
		}
		if err := h.writePage(pg); err != nil {
			return 0, fmt.Errorf("writeOverflowChain write page %d: %w", i, err)
		}
	}

	// Commit the new page count atomically via the meta page.
	h.meta.TotalPages += uint64(numPages)
	if err := h.writeMeta(); err != nil {
		return 0, fmt.Errorf("writeOverflowChain writeMeta: %w", err)
	}
	return firstPageID, nil
}

// readOverflowChain follows the overflow chain starting at firstPageID and
// concatenates all chunk bytes into a single slice.
//
// The caller must hold h.mu.
func readOverflowChain(h *HeapFile, firstPageID uint64) ([]byte, error) {
	var result []byte
	visited := make(map[uint64]bool)
	pageID := firstPageID

	for pageID != 0 {
		// Cycle detection
		if visited[pageID] {
			return nil, &ErrCorruptRecord{
				Reason: fmt.Sprintf("overflow chain cycle detected at page %d", pageID),
			}
		}
		visited[pageID] = true

		// Safety: limit chain length to prevent memory exhaustion
		// 10K pages = ~80 MB TEXT (very generous upper bound)
		if len(visited) > 10000 {
			return nil, &ErrCorruptRecord{
				Reason: fmt.Sprintf("overflow chain too long (%d pages)", len(visited)),
			}
		}

		pg, err := h.readPage(pageID)
		if err != nil {
			return nil, fmt.Errorf("readOverflowChain page %d: %w", pageID, err)
		}
		body, err := pg.GetTuple(0)
		if err != nil {
			return nil, fmt.Errorf("readOverflowChain page %d slot 0: %w", pageID, err)
		}
		if len(body) < 8 {
			return nil, fmt.Errorf("readOverflowChain page %d: body too short (%d bytes)", pageID, len(body))
		}
		pageID = binary.LittleEndian.Uint64(body[:8])
		result = append(result, body[8:]...)
	}
	return result, nil
}

// encodeWithOverflow encodes row into a heap-ready []byte. If the fully inline
// encoding would exceed maxTupleSize, all non-null TEXT column values are moved
// to overflow page chains. VARCHAR columns are always inline.
//
// The caller must hold h.mu (overflow page writes need the lock).
func encodeWithOverflow(schema *Schema, row Row, h *HeapFile) ([]byte, error) {
	if err := validateRow(schema, row); err != nil {
		return nil, err
	}

	// Compute inline sizes for all var columns.
	varLengths := make([]int, len(schema.layout.varIndices))
	totalVarData := 0
	for vi, ci := range schema.layout.varIndices {
		val := row[ci]
		if val.IsNull() {
			continue
		}
		varLengths[vi] = len(val.strVal)
		totalVarData += len(val.strVal)
	}

	totalSize := schema.layout.varDataOffset + totalVarData
	maxTupleSize := PageSize - HeaderSize - SlotSize

	// Fast path: row fits inline — no overflow needed.
	if totalSize <= maxTupleSize {
		return encodeGeneral(schema, row, varLengths, totalVarData, nil)
	}

	// Slow path: spill non-null TEXT columns to overflow pages.
	overflowCols := make(map[int]uint64) // column index → first overflow page ID
	for vi, ci := range schema.layout.varIndices {
		col := schema.columns[ci]
		if col.Type != TypeTEXT {
			continue
		}
		val := row[ci]
		if val.IsNull() {
			continue
		}
		firstPageID, err := writeOverflowChain(h, []byte(val.strVal))
		if err != nil {
			return nil, fmt.Errorf("encodeWithOverflow column %q: %w", col.Name, err)
		}
		overflowCols[ci] = firstPageID
		// Remove this column's bytes from the inline total.
		totalVarData -= varLengths[vi]
		varLengths[vi] = 0

		totalSize = schema.layout.varDataOffset + totalVarData
		if totalSize <= maxTupleSize {
			break // row fits now
		}
	}

	if schema.layout.varDataOffset+totalVarData > maxTupleSize {
		return nil, &ErrRowTooLarge{
			Size:    schema.layout.varDataOffset + totalVarData,
			MaxSize: maxTupleSize,
		}
	}
	return encodeGeneral(schema, row, varLengths, totalVarData, overflowCols)
}

// decodeWithOverflow decodes data into a Row, reading overflow page chains for
// any TEXT column whose directory entry has flag = varDirOverflowFlag.
//
// The caller must hold h.mu.
func decodeWithOverflow(schema *Schema, data []byte, h *HeapFile) (Row, error) {
	minSize := schema.layout.varDataOffset
	if len(data) < minSize {
		return nil, &ErrCorruptRecord{
			Reason: fmt.Sprintf("data length %d < minimum record size %d", len(data), minSize),
		}
	}

	row := make(Row, schema.NumColumns())
	varDataSize := len(data) - schema.layout.varDataOffset

	// Decode fixed-length columns.
	for i, col := range schema.columns {
		isNull := (data[i/8]>>(i%8))&1 == 1
		if isNull {
			row[i] = NewNullValue()
			continue
		}
		if col.Type.IsFixedLength() {
			off := schema.layout.nullBitmapSize + schema.layout.fixedOffsets[i]
			v, err := decodeFixed(data, off, col.Type)
			if err != nil {
				return nil, &ErrCorruptRecord{Reason: err.Error()}
			}
			row[i] = v
		}
	}

	// Decode variable-length columns (inline or overflow).
	for vi, ci := range schema.layout.varIndices {
		col := schema.columns[ci]
		isNull := (data[ci/8]>>(ci%8))&1 == 1
		if isNull {
			row[ci] = NewNullValue()
			continue
		}

		dirOff := schema.layout.varDirOffset + vi*varDirEntrySize
		if dirOff+varDirEntrySize > len(data) {
			return nil, &ErrCorruptRecord{
				Reason: fmt.Sprintf("var directory entry %d out of bounds", vi),
			}
		}

		flag := binary.LittleEndian.Uint32(data[dirOff:])

		if flag == varDirOverflowFlag {
			// Overflow: follow the page chain to reconstruct the value.
			firstPageID := binary.LittleEndian.Uint64(data[dirOff+4:])
			raw, err := readOverflowChain(h, firstPageID)
			if err != nil {
				return nil, fmt.Errorf("decodeWithOverflow column %q: %w", col.Name, err)
			}
			// Only TEXT columns can overflow.
			row[ci] = NewTextValue(string(raw))
			continue
		}

		// Inline value.
		relOffset := int(binary.LittleEndian.Uint16(data[dirOff+4:]))
		length := int(binary.LittleEndian.Uint16(data[dirOff+6:]))
		if relOffset+length > varDataSize {
			return nil, &ErrCorruptRecord{
				Reason: fmt.Sprintf("var column %d data [%d:%d] out of bounds (var region %d bytes)",
					ci, relOffset, relOffset+length, varDataSize),
			}
		}
		abs := schema.layout.varDataOffset + relOffset
		s := string(data[abs : abs+length])
		if col.Type == TypeVARCHAR {
			row[ci] = NewVarcharValue(s)
		} else {
			row[ci] = NewTextValue(s)
		}
	}

	return row, nil
}

// hasOverflowColumn returns true if any var-directory entry in data carries
// the overflow flag. Used by heap.go to decide which decode path to take.
func hasOverflowColumn(schema *Schema, data []byte) bool {
	for vi := range schema.layout.varIndices {
		dirOff := schema.layout.varDirOffset + vi*varDirEntrySize
		if dirOff+4 > len(data) {
			break
		}
		if binary.LittleEndian.Uint32(data[dirOff:]) == varDirOverflowFlag {
			return true
		}
	}
	return false
}

// scanOverflowPageIDs returns the first-page IDs of all overflow columns in
// data. Used by heap.go when deleting rows to free overflow chains in the
// future (currently a no-op stub; a freelist is needed for reclamation).
func scanOverflowPageIDs(schema *Schema, data []byte) []uint64 {
	var ids []uint64
	for vi := range schema.layout.varIndices {
		dirOff := schema.layout.varDirOffset + vi*varDirEntrySize
		if dirOff+varDirEntrySize > len(data) {
			break
		}
		if binary.LittleEndian.Uint32(data[dirOff:]) == varDirOverflowFlag {
			ids = append(ids, binary.LittleEndian.Uint64(data[dirOff+4:]))
		}
	}
	return ids
}

// freeOverflowChain is a stub that marks overflow pages as freed.
// A proper implementation requires a freelist page; until then, overflow pages
// are leaked when their owning row is deleted or updated.
func freeOverflowChain(_ *HeapFile, _ uint64) error {
	// TODO: implement freelist-based reclamation.
	return nil
}

// freeRowOverflow frees all overflow chains referenced by data.
// The caller must hold h.mu.
func freeRowOverflow(schema *Schema, data []byte, h *HeapFile) error {
	for _, pageID := range scanOverflowPageIDs(schema, data) {
		if err := freeOverflowChain(h, pageID); err != nil {
			return err
		}
	}
	return nil
}

// isOverflowPage reports whether pg is an OverflowPage. Used during Scan to
// skip overflow pages (they are not independent rows).
func isOverflowPage(pg *Page) bool {
	return pg.Type() == OverflowPage
}

// Ensure errors package is used.
var _ = errors.New
