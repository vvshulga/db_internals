package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PagesPerSegment is the maximum number of pages in a single segment file.
// At PageSize=8192 bytes, one segment holds up to 1 GiB of data.
const PagesPerSegment = 131072

// metaTupleSize is the fixed size of the heap metadata tuple stored in slot 0
// of global page 0. Extra bytes are reserved for future fields.
const metaTupleSize = 64

// heapMeta is the in-memory cache of the metadata stored on the meta page.
type heapMeta struct {
	TotalPages uint64 // global page count including page 0; always >= 2
}

// HeapFile manages a table's data as a sequence of segment files.
//
// File naming: <dir>/<tableName>.N.heap where N is the zero-based segment index.
// Global page numbering: page N lives in segment N/PagesPerSegment at local
// offset (N%PagesPerSegment)*PageSize within that file.
//
// Page 0 (segment 0, local offset 0) is a MetaPage that stores heap metadata
// in its first slot. Data rows occupy pages 1 onwards.
//
// HeapFile is safe for concurrent use. A single mutex serialises all I/O so
// that the in-memory meta cache and file state stay consistent. A future buffer
// pool with per-page latches would replace this coarse lock.
type HeapFile struct {
	mu       sync.Mutex
	dir      string
	basename string               // table name prefix
	segments map[uint32]*os.File  // lazily opened segment files
	meta     heapMeta
}

// OpenHeapFile opens or creates the heap file for a table.
//
//   - dir: directory that holds segment files
//   - tableName: used as the file name prefix (e.g., "users" → users.0.heap)
//
// If no segment files exist yet, OpenHeapFile initialises the heap with a
// meta page (page 0) and one empty data page (page 1).
func OpenHeapFile(dir, tableName string) (*HeapFile, error) {
	h := &HeapFile{
		dir:      dir,
		basename: tableName,
		segments: make(map[uint32]*os.File),
	}

	seg0Path := h.segmentPath(0)
	info, err := os.Stat(seg0Path)

	if os.IsNotExist(err) {
		// New table: initialise from scratch.
		if err := h.initNew(); err != nil {
			return nil, fmt.Errorf("storage: OpenHeapFile %q: %w", tableName, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("storage: OpenHeapFile %q: stat: %w", tableName, err)
	} else if info.Size() == 0 {
		// Segment 0 exists but is empty (e.g., created by a previous failed init).
		if err := h.initNew(); err != nil {
			return nil, fmt.Errorf("storage: OpenHeapFile %q: %w", tableName, err)
		}
	} else {
		// Existing table: read meta page.
		if err := h.readMeta(); err != nil {
			return nil, fmt.Errorf("storage: OpenHeapFile %q: read meta: %w", tableName, err)
		}
	}
	return h, nil
}

// Close syncs and closes all open segment files. After Close all methods
// return an error.
func (h *HeapFile) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var firstErr error
	for id, f := range h.segments {
		if err := f.Sync(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(h.segments, id)
	}
	return firstErr
}

// PageCount returns the total number of pages in the heap (including page 0).
func (h *HeapFile) PageCount() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.meta.TotalPages
}

// Insert encodes row according to schema and stores it in the heap.
// Returns the stable RID of the new row.
//
// Strategy: try the last data page; if full, compact and retry; if still full,
// allocate a new page (which may start a new segment file).
func (h *HeapFile) Insert(schema *Schema, row Row) (RID, error) {
	data, err := Encode(schema, row)
	if err != nil {
		return RID{}, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	return h.insertEncoded(data)
}

// Fetch reads and decodes the row identified by rid.
// Returns ErrInvalidRID if rid is not a valid data page reference.
// Returns ErrDeletedSlot (from the page layer) if the slot is a tombstone.
func (h *HeapFile) Fetch(schema *Schema, rid RID) (Row, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.validateRID(rid); err != nil {
		return nil, err
	}
	pg, err := h.readPage(rid.PageID)
	if err != nil {
		return nil, err
	}
	data, err := pg.GetTuple(rid.SlotID)
	if err != nil {
		return nil, err
	}
	return Decode(schema, data)
}

// Delete marks the row identified by rid as deleted (tombstone).
// Space is not reclaimed until the page is compacted on a future insert.
func (h *HeapFile) Delete(rid RID) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.validateRID(rid); err != nil {
		return err
	}
	pg, err := h.readPage(rid.PageID)
	if err != nil {
		return err
	}
	if err := pg.DeleteTuple(rid.SlotID); err != nil {
		return err
	}
	return h.writePage(pg)
}

// Update performs a delete-then-reinsert. It is not atomic: a crash between
// the two operations leaves the old row deleted without the new row present.
// Returns the new RID of the updated row.
func (h *HeapFile) Update(schema *Schema, rid RID, newRow Row) (RID, error) {
	data, err := Encode(schema, newRow)
	if err != nil {
		return RID{}, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.validateRID(rid); err != nil {
		return RID{}, err
	}

	// Delete old row.
	pg, err := h.readPage(rid.PageID)
	if err != nil {
		return RID{}, err
	}
	if err := pg.DeleteTuple(rid.SlotID); err != nil {
		return RID{}, err
	}
	if err := h.writePage(pg); err != nil {
		return RID{}, err
	}

	// Insert new row.
	return h.insertEncoded(data)
}

// Scan iterates over all live tuples in the heap in page order, calling fn for
// each. If fn returns false the scan stops. If fn returns an error the scan
// stops and that error is returned.
func (h *HeapFile) Scan(schema *Schema, fn func(RID, Row) (bool, error)) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for pageID := uint64(1); pageID < h.meta.TotalPages; pageID++ {
		pg, err := h.readPage(pageID)
		if err != nil {
			return err
		}
		for s := 0; s < pg.NumSlots(); s++ {
			slotID := SlotID(s)
			data, err := pg.GetTuple(slotID)
			if err != nil {
				var eds *ErrDeletedSlot
				if errors.As(err, &eds) {
					continue
				}
				return err
			}
			row, err := Decode(schema, data)
			if err != nil {
				return err
			}
			cont, err := fn(RID{PageID: pageID, SlotID: slotID}, row)
			if err != nil {
				return err
			}
			if !cont {
				return nil
			}
		}
	}
	return nil
}

// ---- Internal helpers --------------------------------------------------------

// segmentPath returns the file path for segment segID.
func (h *HeapFile) segmentPath(segID uint32) string {
	return filepath.Join(h.dir, fmt.Sprintf("%s.%d.heap", h.basename, segID))
}

// openSegment returns the *os.File for segment segID, opening (or creating) it
// if not already cached.
func (h *HeapFile) openSegment(segID uint32) (*os.File, error) {
	if f, ok := h.segments[segID]; ok {
		return f, nil
	}
	path := h.segmentPath(segID)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open segment %d (%s): %w", segID, path, err)
	}
	h.segments[segID] = f
	return f, nil
}

// readPage reads and unmarshals the page with the given global page ID.
func (h *HeapFile) readPage(pageID uint64) (*Page, error) {
	segID := uint32(pageID / PagesPerSegment)
	localPage := pageID % PagesPerSegment
	offset := int64(localPage) * PageSize

	f, err := h.openSegment(segID)
	if err != nil {
		return nil, fmt.Errorf("readPage %d: %w", pageID, err)
	}
	var buf [PageSize]byte
	if _, err := f.ReadAt(buf[:], offset); err != nil {
		return nil, fmt.Errorf("readPage %d: %w", pageID, err)
	}
	pg := &Page{}
	if err := pg.Unmarshal(buf[:]); err != nil {
		return nil, fmt.Errorf("readPage %d: %w", pageID, err)
	}
	return pg, nil
}

// writePage marshals and writes the page to its canonical position.
func (h *HeapFile) writePage(pg *Page) error {
	pageID := pg.PageID()
	segID := uint32(pageID / PagesPerSegment)
	localPage := pageID % PagesPerSegment
	offset := int64(localPage) * PageSize

	f, err := h.openSegment(segID)
	if err != nil {
		return fmt.Errorf("writePage %d: %w", pageID, err)
	}
	raw, err := pg.Marshal()
	if err != nil {
		return fmt.Errorf("writePage %d: marshal: %w", pageID, err)
	}
	if _, err := f.WriteAt(raw, offset); err != nil {
		return fmt.Errorf("writePage %d: %w", pageID, err)
	}
	return nil
}

// appendPage allocates the next global page, writes it to disk, increments
// TotalPages, and persists the updated meta. Creates a new segment file if
// the new page falls in a new segment.
func (h *HeapFile) appendPage() (*Page, error) {
	newID := h.meta.TotalPages
	pg := NewPage(newID, DataPage)
	if err := h.writePage(pg); err != nil {
		return nil, fmt.Errorf("appendPage %d: %w", newID, err)
	}
	h.meta.TotalPages++
	if err := h.writeMeta(); err != nil {
		return nil, fmt.Errorf("appendPage %d: writeMeta: %w", newID, err)
	}
	return pg, nil
}

// readMeta reads the meta tuple from page 0 and populates h.meta.
// It scans all slots for the first live tuple to handle pages where earlier
// slots have been tombstoned by previous writeMeta calls.
func (h *HeapFile) readMeta() error {
	pg, err := h.readPage(0)
	if err != nil {
		return err
	}
	for i := 0; i < pg.NumSlots(); i++ {
		data, err := pg.GetTuple(SlotID(i))
		if err != nil {
			continue // tombstone or invalid; try next slot
		}
		if len(data) < 8 {
			return fmt.Errorf("readMeta: meta tuple too short (%d bytes)", len(data))
		}
		h.meta.TotalPages = binary.LittleEndian.Uint64(data[0:])
		return nil
	}
	return fmt.Errorf("readMeta: no live meta tuple found on page 0")
}

// writeMeta serializes h.meta into the meta tuple on page 0.
// It scans for the current live meta slot before replacing it, because
// prior writeMeta calls may have tombstoned earlier slots.
func (h *HeapFile) writeMeta() error {
	pg, err := h.readPage(0)
	if err != nil {
		return err
	}
	// Find the current live meta slot.
	liveSlot := SlotID(0)
	found := false
	for i := 0; i < pg.NumSlots(); i++ {
		if _, err := pg.GetTuple(SlotID(i)); err == nil {
			liveSlot = SlotID(i)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("writeMeta: no live meta tuple on page 0")
	}
	if err := pg.DeleteTuple(liveSlot); err != nil {
		return fmt.Errorf("writeMeta: delete old meta: %w", err)
	}
	pg.Compact()

	var buf [metaTupleSize]byte
	binary.LittleEndian.PutUint64(buf[0:], h.meta.TotalPages)
	if _, err := pg.InsertTuple(buf[:]); err != nil {
		return fmt.Errorf("writeMeta: insert new meta: %w", err)
	}
	return h.writePage(pg)
}

// initNew creates the heap from scratch: writes page 0 (MetaPage) and page 1
// (first empty DataPage) and sets h.meta.TotalPages = 2.
func (h *HeapFile) initNew() error {
	h.meta.TotalPages = 2

	// Write page 0: MetaPage with the meta tuple.
	meta0 := NewPage(0, MetaPage)
	var buf [metaTupleSize]byte
	binary.LittleEndian.PutUint64(buf[0:], h.meta.TotalPages)
	if _, err := meta0.InsertTuple(buf[:]); err != nil {
		return fmt.Errorf("initNew: insert meta tuple: %w", err)
	}
	if err := h.writePage(meta0); err != nil {
		return fmt.Errorf("initNew: write meta page: %w", err)
	}

	// Write page 1: first empty DataPage.
	data1 := NewPage(1, DataPage)
	if err := h.writePage(data1); err != nil {
		return fmt.Errorf("initNew: write first data page: %w", err)
	}
	return nil
}

// lastDataPageID returns the global ID of the last data page. Since
// TotalPages is always >= 2 after initNew, this is TotalPages-1.
func (h *HeapFile) lastDataPageID() uint64 {
	return h.meta.TotalPages - 1
}

// validateRID checks that rid refers to a valid data page (not the meta page
// and within the current TotalPages range).
func (h *HeapFile) validateRID(rid RID) error {
	if rid.PageID == 0 {
		return &ErrInvalidRID{RID: rid, TotalPages: h.meta.TotalPages,
			Reason: "PageID 0 is the metadata page"}
	}
	if rid.PageID >= h.meta.TotalPages {
		return &ErrInvalidRID{RID: rid, TotalPages: h.meta.TotalPages,
			Reason: "PageID out of range"}
	}
	return nil
}

// insertEncoded is the internal, mutex-held implementation of Insert.
// It accepts an already-encoded row byte slice.
func (h *HeapFile) insertEncoded(data []byte) (RID, error) {
	pg, err := h.readPage(h.lastDataPageID())
	if err != nil {
		return RID{}, err
	}

	slotID, err := pg.InsertTuple(data)
	if err == nil {
		if werr := h.writePage(pg); werr != nil {
			return RID{}, werr
		}
		return RID{PageID: pg.PageID(), SlotID: slotID}, nil
	}

	var ef *ErrPageFull
	if !errors.As(err, &ef) {
		return RID{}, err
	}

	// Try compaction first.
	pg.Compact()
	slotID, err = pg.InsertTuple(data)
	if err == nil {
		if werr := h.writePage(pg); werr != nil {
			return RID{}, werr
		}
		return RID{PageID: pg.PageID(), SlotID: slotID}, nil
	}
	if !errors.As(err, &ef) {
		return RID{}, err
	}

	// Allocate a new page.
	pg, err = h.appendPage()
	if err != nil {
		return RID{}, err
	}
	slotID, err = pg.InsertTuple(data)
	if err != nil {
		return RID{}, fmt.Errorf("insert on fresh page failed: %w", err)
	}
	if werr := h.writePage(pg); werr != nil {
		return RID{}, werr
	}
	return RID{PageID: pg.PageID(), SlotID: slotID}, nil
}
