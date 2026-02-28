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
	TotalPages   uint64 // bytes 0-7: global page count including page 0; always >= 2
	FreeListHead uint64 // bytes 8-15: first free overflow page (0 = empty list)
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
	mu        sync.Mutex
	dir       string
	basename  string               // table name prefix
	segments  map[uint32]*os.File  // lazily opened segment files
	meta      heapMeta
	metaDirty bool                 // true if meta has unpersisted changes
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

	// Flush dirty meta before closing
	if h.metaDirty {
		if err := h.writeMeta(); err != nil {
			return err  // Don't proceed if meta flush fails
		}
		h.metaDirty = false
	}

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

// Flush syncs all open segment files to disk, ensuring all previous writes
// are durable. This is called automatically by Close(), but can be called
// explicitly when durability is required before closing.
func (h *HeapFile) Flush() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Flush dirty meta first
	if h.metaDirty {
		if err := h.writeMeta(); err != nil {
			return err
		}
		h.metaDirty = false
	}

	// Then flush all segments
	for id, f := range h.segments {
		if err := f.Sync(); err != nil {
			return fmt.Errorf("flush segment %d: %w", id, err)
		}
	}
	return nil
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
// TEXT column values too large to fit inline are automatically spilled to
// overflow pages (see overflow.go). The insert is not atomic with respect to
// overflow page writes: a crash between overflow writes and the main tuple
// write leaves orphaned overflow pages (space leak, no data corruption).
//
// Strategy: try the last data page; if full, compact and retry; if still full,
// allocate a new page (which may start a new segment file).
func (h *HeapFile) Insert(schema *Schema, row Row) (RID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Encoding happens inside the lock because encodeWithOverflow may write
	// overflow pages, which requires exclusive access to the heap file state.
	data, err := encodeWithOverflow(schema, row, h)
	if err != nil {
		return RID{}, err
	}
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
	return decodeWithOverflow(schema, data, h)
}

// Delete marks the row identified by rid as deleted (tombstone) and frees any
// overflow pages belonging to the row. Space on the data page is not reclaimed
// until the page is compacted on a future insert.
func (h *HeapFile) Delete(schema *Schema, rid RID) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.validateRID(rid); err != nil {
		return err
	}
	pg, err := h.readPage(rid.PageID)
	if err != nil {
		return err
	}
	data, err := pg.GetTuple(rid.SlotID)
	if err != nil {
		return err
	}
	if err := freeRowOverflow(schema, data, h); err != nil {
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
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.validateRID(rid); err != nil {
		return RID{}, err
	}

	// Read the old row first so its overflow pages can be freed before the
	// new row is encoded. This lets writeOverflowChain reuse the just-freed
	// pages, keeping the heap file from growing unnecessarily.
	pg, err := h.readPage(rid.PageID)
	if err != nil {
		return RID{}, err
	}
	oldData, err := pg.GetTuple(rid.SlotID)
	if err != nil {
		return RID{}, err
	}
	if err := freeRowOverflow(schema, oldData, h); err != nil {
		return RID{}, err
	}

	// Encode new row (may reuse overflow pages freed above).
	newData, err := encodeWithOverflow(schema, newRow, h)
	if err != nil {
		return RID{}, err
	}

	// Tombstone the old slot.
	if err := pg.DeleteTuple(rid.SlotID); err != nil {
		return RID{}, err
	}
	if err := h.writePage(pg); err != nil {
		return RID{}, err
	}

	// Insert new row.
	return h.insertEncoded(newData)
}

// Scan iterates over all live tuples in the heap in page order, calling fn for
// each. If fn returns false the scan stops. If fn returns an error the scan
// stops and that error is returned.
//
// Overflow pages (PageType == OverflowPage) are skipped automatically; they
// are not independent rows and must not be presented to the caller.
func (h *HeapFile) Scan(schema *Schema, fn func(RID, Row) (bool, error)) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for pageID := uint64(1); pageID < h.meta.TotalPages; pageID++ {
		pg, err := h.readPage(pageID)
		if err != nil {
			return err
		}
		// Skip overflow pages — they are part of a large TEXT value chain,
		// not independent data rows.
		if isOverflowPage(pg) {
			continue
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
			row, err := decodeWithOverflow(schema, data, h)
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
	h.metaDirty = true
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
		if len(data) >= 16 {
			h.meta.FreeListHead = binary.LittleEndian.Uint64(data[8:])
		}
		return nil
	}
	return fmt.Errorf("readMeta: no live meta tuple found on page 0")
}

// writeMeta serializes h.meta into the meta tuple on page 0.
//
// A fresh MetaPage is constructed on every call so that tombstone slot entries
// do not accumulate. Each call leaves page 0 with exactly one live slot (slot 0).
func (h *HeapFile) writeMeta() error {
	pg := NewPage(0, MetaPage)
	var buf [metaTupleSize]byte
	binary.LittleEndian.PutUint64(buf[0:], h.meta.TotalPages)
	binary.LittleEndian.PutUint64(buf[8:], h.meta.FreeListHead)
	if _, err := pg.InsertTuple(buf[:]); err != nil {
		return fmt.Errorf("writeMeta: insert meta: %w", err)
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
