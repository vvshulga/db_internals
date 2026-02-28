package storage

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/btree"
)

// indexDegree is the B-tree branching factor. 32 gives good cache behavior
// for typical workloads (each node holds up to 63 items).
const indexDegree = 32

// IndexEntry is a single item stored in the B-tree index.
// The total order is: (Value, PageID, SlotID) — Value first, then RID to
// allow multiple rows with the same value (non-unique indexes).
type IndexEntry struct {
	Value Value
	RID   RID
}

// NULL sorts before all non-NULL values. Different kinds sort by their
// ValueKind ordinal. Same kind uses type-natural comparison.
// CompareValues compares two Values for ordering.
// Returns -1 (a < b), 0 (a == b), or 1 (a > b).
// Used by both indexes and query execution.
func CompareValues(a, b Value) int {
	aN, bN := a.IsNull(), b.IsNull()
	if aN && bN {
		return 0
	}
	if aN {
		return -1
	}
	if bN {
		return 1
	}
	if a.Kind() != b.Kind() {
		return cmp.Compare(int(a.Kind()), int(b.Kind()))
	}
	switch a.Kind() {
	case KindInt:
		return cmp.Compare(a.AsInt(), b.AsInt())
	case KindBigInt:
		return cmp.Compare(a.AsBigInt(), b.AsBigInt())
	case KindFloat:
		return cmp.Compare(a.AsFloat(), b.AsFloat())
	case KindDouble:
		return cmp.Compare(a.AsDouble(), b.AsDouble())
	case KindBoolean:
		// false < true
		av, bv := a.AsBoolean(), b.AsBoolean()
		if av == bv {
			return 0
		}
		if !av {
			return -1
		}
		return 1
	case KindDatetime:
		return cmp.Compare(a.AsDatetime(), b.AsDatetime())
	case KindVarchar, KindText:
		return cmp.Compare(a.AsString(), b.AsString())
	}
	return 0
}

// compareIndexEntry is the less-function for the generic btree.
// Returns true if a sorts strictly before b.
func compareIndexEntry(a, b IndexEntry) bool {
	c := CompareValues(a.Value, b.Value)
	if c != 0 {
		return c < 0
	}
	if a.RID.PageID != b.RID.PageID {
		return a.RID.PageID < b.RID.PageID
	}
	return a.RID.SlotID < b.RID.SlotID
}

// Index is an in-memory B-tree index over a single column of a table.
//
// Persistence: the tree is saved to <dir>/<tableName>.<colName>.idx as a
// sorted flat file. The file is written atomically (tmp → rename) on
// Checkpoint and loaded in full on OpenIndex.
//
// Index is safe for concurrent use. A single RW-mutex guards the tree.
type Index struct {
	mu       sync.RWMutex
	dir      string
	basename string // "<tableName>.<colName>"
	table    string // table name (for error messages)
	col      string // column name
	colIndex int    // column position in the schema
	unique   bool
	tree     *btree.BTreeG[IndexEntry]
	dirty    bool
}

// indexPath returns the canonical path for this index file.
func (idx *Index) indexPath() string {
	return filepath.Join(idx.dir, idx.basename+".idx")
}

// OpenIndex opens an existing index file or creates a new empty index if none
// exists. The caller specifies whether the index should enforce uniqueness.
func OpenIndex(dir, tableName, colName string, colIndex int, unique bool) (*Index, error) {
	idx := &Index{
		dir:      dir,
		basename: tableName + "." + colName,
		table:    tableName,
		col:      colName,
		colIndex: colIndex,
		unique:   unique,
		tree:     btree.NewG(indexDegree, compareIndexEntry),
	}
	path := idx.indexPath()
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return idx, nil // brand-new index
	}
	if err != nil {
		return nil, fmt.Errorf("storage: OpenIndex %q.%q: stat: %w", tableName, colName, err)
	}
	if err := idx.load(); err != nil {
		return nil, fmt.Errorf("storage: OpenIndex %q.%q: load: %w", tableName, colName, err)
	}
	return idx, nil
}

// Close checkpoints the index if dirty and releases resources.
func (idx *Index) Close() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.dirty {
		return idx.save()
	}
	return nil
}

// Checkpoint flushes the index to disk if it has been modified since the
// last checkpoint. It is a no-op if the index is clean.
func (idx *Index) Checkpoint() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if !idx.dirty {
		return nil
	}
	return idx.save()
}

// Insert adds (val, rid) to the index. If unique=true and another RID already
// exists for val, it returns ErrUniqueViolation without modifying the tree.
func (idx *Index) Insert(val Value, rid RID) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.unique {
		// Check for an existing entry with this value and a different RID.
		pivot := IndexEntry{Value: val, RID: RID{PageID: 0, SlotID: 0}}
		var violation bool
		idx.tree.AscendGreaterOrEqual(pivot, func(e IndexEntry) bool {
			if CompareValues(e.Value, val) != 0 {
				return false
			}
			if e.RID != rid {
				violation = true
			}
			return false
		})
		if violation {
			return &ErrUniqueViolation{Table: idx.table, Column: idx.col, Value: val}
		}
	}

	idx.tree.ReplaceOrInsert(IndexEntry{Value: val, RID: rid})
	idx.dirty = true
	return nil
}

// Delete removes the (val, rid) pair from the index. It is a no-op if the
// pair is not present.
func (idx *Index) Delete(val Value, rid RID) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.tree.Delete(IndexEntry{Value: val, RID: rid})
	idx.dirty = true
}

// Lookup returns all RIDs whose indexed value equals val, in RID order.
func (idx *Index) Lookup(val Value) []RID {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	pivot := IndexEntry{Value: val, RID: RID{PageID: 0, SlotID: 0}}
	var rids []RID
	idx.tree.AscendGreaterOrEqual(pivot, func(e IndexEntry) bool {
		if CompareValues(e.Value, val) != 0 {
			return false
		}
		rids = append(rids, e.RID)
		return true
	})
	return rids
}

// RangeScan calls fn for every index entry whose value falls in [lo, hi].
// A nil lo means "from the beginning"; a nil hi means "to the end".
// fn receives the IndexEntry; return false to stop iteration.
func (idx *Index) RangeScan(lo, hi *Value, fn func(IndexEntry) bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	iter := func(e IndexEntry) bool {
		if hi != nil && CompareValues(e.Value, *hi) > 0 {
			return false
		}
		return fn(e)
	}

	if lo == nil {
		idx.tree.Ascend(iter)
	} else {
		pivot := IndexEntry{Value: *lo, RID: RID{PageID: 0, SlotID: 0}}
		idx.tree.AscendGreaterOrEqual(pivot, iter)
	}
}

// Len returns the number of entries in the index.
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.tree.Len()
}

// ---- Persistence helpers -------------------------------------------------------

// save serializes the tree to disk atomically via a tmp file.
// File format v2: [version:1] [unique:1] [checksum:4] [reserved:2] [count:8] [entries...]
// The checksum (CRC32-IEEE) covers all entry data.
// Caller must hold idx.mu (write lock).
func (idx *Index) save() error {
	tmpPath := idx.indexPath() + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}

	n := idx.tree.Len()

	// Write entries to buffer FIRST to compute checksum
	var entriesBuf bytes.Buffer
	var writeErr error
	idx.tree.Ascend(func(e IndexEntry) bool {
		if err := writeEntry(&entriesBuf, e); err != nil {
			writeErr = err
			return false
		}
		return true
	})
	if writeErr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write entry: %w", writeErr)
	}

	// Compute checksum of entries
	checksum := crc32.ChecksumIEEE(entriesBuf.Bytes())

	// Build header
	var hdr [16]byte
	hdr[0] = 2 // version 2
	if idx.unique {
		hdr[1] = 1
	} else {
		hdr[1] = 0
	}
	binary.LittleEndian.PutUint32(hdr[2:], checksum) // NEW: checksum bytes
	// hdr[6:8] reserved for future use
	binary.LittleEndian.PutUint64(hdr[8:], uint64(n)) // entry count

	// Write header + entries
	if _, err := f.Write(hdr[:]); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := f.Write(entriesBuf.Bytes()); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write entries: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpPath, idx.indexPath()); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	idx.dirty = false
	return nil
}

// load reads all entries from the index file into the tree.
// Supports both v1 (no checksum) and v2 (with checksum) formats.
// Caller must hold idx.mu or be in single-threaded init.
func (idx *Index) load() error {
	f, err := os.Open(idx.indexPath())
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	var hdr [16]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	version := hdr[0]

	// Backward compatibility: support v1 (no checksum)
	if version == 1 {
		return idx.loadV1(f, hdr)
	}

	if version != 2 {
		return fmt.Errorf("unsupported index format version %d", version)
	}

	// Version 2: verify checksum
	idx.unique = (hdr[1] == 1)
	storedChecksum := binary.LittleEndian.Uint32(hdr[2:])
	n := binary.LittleEndian.Uint64(hdr[8:])

	// Read all entries into buffer
	var entriesBuf bytes.Buffer
	if _, err := io.Copy(&entriesBuf, f); err != nil {
		return fmt.Errorf("read entries: %w", err)
	}

	// Verify checksum
	computedChecksum := crc32.ChecksumIEEE(entriesBuf.Bytes())
	if storedChecksum != computedChecksum {
		return fmt.Errorf("index checksum mismatch: stored=0x%08x computed=0x%08x (file may be corrupted)",
			storedChecksum, computedChecksum)
	}

	// Parse entries
	reader := bytes.NewReader(entriesBuf.Bytes())
	for i := uint64(0); i < n; i++ {
		e, err := readEntry(reader)
		if err != nil {
			return fmt.Errorf("read entry %d: %w", i, err)
		}
		idx.tree.ReplaceOrInsert(e)
	}
	return nil
}

// loadV1 handles backward compatibility with version 1 index files (no checksum).
func (idx *Index) loadV1(f *os.File, hdr [16]byte) error {
	idx.unique = (hdr[1] == 1)
	n := binary.LittleEndian.Uint64(hdr[8:])

	for i := uint64(0); i < n; i++ {
		e, err := readEntry(f)
		if err != nil {
			return fmt.Errorf("read entry %d: %w", i, err)
		}
		idx.tree.ReplaceOrInsert(e)
	}
	return nil
}

// writeEntry serializes one IndexEntry to w.
func writeEntry(w io.Writer, e IndexEntry) error {
	// RID: 10 bytes
	enc := e.RID.Encode()
	if _, err := w.Write(enc[:]); err != nil {
		return err
	}
	// ValueKind: 1 byte
	if _, err := w.Write([]byte{byte(e.Value.Kind())}); err != nil {
		return err
	}
	// ValueData: variable
	return writeValueData(w, e.Value)
}

// readEntry deserializes one IndexEntry from r.
func readEntry(r io.Reader) (IndexEntry, error) {
	var ridBytes [RIDEncodedSize]byte
	if _, err := io.ReadFull(r, ridBytes[:]); err != nil {
		return IndexEntry{}, fmt.Errorf("read RID: %w", err)
	}
	rid := DecodeRID(ridBytes)

	var kindBuf [1]byte
	if _, err := io.ReadFull(r, kindBuf[:]); err != nil {
		return IndexEntry{}, fmt.Errorf("read kind: %w", err)
	}
	kind := ValueKind(kindBuf[0])

	val, err := readValueData(r, kind)
	if err != nil {
		return IndexEntry{}, err
	}
	return IndexEntry{Value: val, RID: rid}, nil
}

func writeValueData(w io.Writer, v Value) error {
	var buf8 [8]byte
	switch v.Kind() {
	case KindNull:
		return nil
	case KindInt:
		binary.LittleEndian.PutUint32(buf8[:4], uint32(int32(v.AsInt())))
		_, err := w.Write(buf8[:4])
		return err
	case KindBigInt:
		binary.LittleEndian.PutUint64(buf8[:], uint64(v.AsBigInt()))
		_, err := w.Write(buf8[:8])
		return err
	case KindFloat:
		binary.LittleEndian.PutUint32(buf8[:4], math.Float32bits(v.AsFloat()))
		_, err := w.Write(buf8[:4])
		return err
	case KindDouble:
		binary.LittleEndian.PutUint64(buf8[:], math.Float64bits(v.AsDouble()))
		_, err := w.Write(buf8[:8])
		return err
	case KindBoolean:
		b := byte(0)
		if v.AsBoolean() {
			b = 1
		}
		_, err := w.Write([]byte{b})
		return err
	case KindDatetime:
		binary.LittleEndian.PutUint64(buf8[:], uint64(v.AsDatetime()))
		_, err := w.Write(buf8[:8])
		return err
	case KindVarchar, KindText:
		s := v.AsString()
		binary.LittleEndian.PutUint16(buf8[:2], uint16(len(s)))
		if _, err := w.Write(buf8[:2]); err != nil {
			return err
		}
		_, err := w.Write([]byte(s))
		return err
	}
	return fmt.Errorf("unknown ValueKind %d", v.Kind())
}

func readValueData(r io.Reader, kind ValueKind) (Value, error) {
	var buf8 [8]byte
	switch kind {
	case KindNull:
		return NewNullValue(), nil
	case KindInt:
		if _, err := io.ReadFull(r, buf8[:4]); err != nil {
			return Value{}, err
		}
		return NewIntValue(int32(binary.LittleEndian.Uint32(buf8[:4]))), nil
	case KindBigInt:
		if _, err := io.ReadFull(r, buf8[:8]); err != nil {
			return Value{}, err
		}
		return NewBigIntValue(int64(binary.LittleEndian.Uint64(buf8[:8]))), nil
	case KindFloat:
		if _, err := io.ReadFull(r, buf8[:4]); err != nil {
			return Value{}, err
		}
		return NewFloatValue(math.Float32frombits(binary.LittleEndian.Uint32(buf8[:4]))), nil
	case KindDouble:
		if _, err := io.ReadFull(r, buf8[:8]); err != nil {
			return Value{}, err
		}
		return NewDoubleValue(math.Float64frombits(binary.LittleEndian.Uint64(buf8[:8]))), nil
	case KindBoolean:
		if _, err := io.ReadFull(r, buf8[:1]); err != nil {
			return Value{}, err
		}
		return NewBooleanValue(buf8[0] != 0), nil
	case KindDatetime:
		if _, err := io.ReadFull(r, buf8[:8]); err != nil {
			return Value{}, err
		}
		return NewDatetimeValue(int64(binary.LittleEndian.Uint64(buf8[:8]))), nil
	case KindVarchar, KindText:
		if _, err := io.ReadFull(r, buf8[:2]); err != nil {
			return Value{}, err
		}
		length := int(binary.LittleEndian.Uint16(buf8[:2]))
		strBuf := make([]byte, length)
		if _, err := io.ReadFull(r, strBuf); err != nil {
			return Value{}, err
		}
		s := string(strBuf)
		if kind == KindVarchar {
			return NewVarcharValue(s), nil
		}
		return NewTextValue(s), nil
	}
	return Value{}, fmt.Errorf("unknown ValueKind %d", kind)
}

// indexFileColName extracts the column name from an index filename of the form
// "<tableName>.<colName>.idx". Returns ("", false) if the pattern doesn't match.
func indexFileColName(tableName, filename string) (string, bool) {
	prefix := tableName + "."
	if !strings.HasPrefix(filename, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(filename, prefix)
	if !strings.HasSuffix(rest, ".idx") {
		return "", false
	}
	col := strings.TrimSuffix(rest, ".idx")
	if col == "" {
		return "", false
	}
	return col, true
}
