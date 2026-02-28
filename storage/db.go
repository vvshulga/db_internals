package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const catalogFileName = "catalog.json"
const catalogTmpFileName = "catalog.json.tmp"

// columnRecord is the JSON-serializable form of Column.
type columnRecord struct {
	Name     string   `json:"name"`
	Type     DataType `json:"type"`
	MaxLen   uint16   `json:"max_len,omitempty"`
	Nullable bool     `json:"nullable,omitempty"`
}

// tableRecord is the JSON-serializable metadata for one table.
type tableRecord struct {
	Columns []columnRecord `json:"columns"`
}

// catalogFile is the root JSON structure persisted to catalog.json.
type catalogFile struct {
	Tables map[string]tableRecord `json:"tables"`
}

// tableEntry holds the schema and (optionally) an open handle for one table.
type tableEntry struct {
	schema *Schema
	handle *TableHandle // nil until OpenTable is called
}

// DB manages a directory of heap-file tables.
// It persists the table catalog (names + schemas) to catalog.json and opens
// HeapFiles lazily on the first OpenTable call.
//
// Typical usage:
//
//	db, err := storage.OpenDB("/var/db/mydb")
//	t, err  := db.CreateTable("users", schema)
//	rid, err := t.Insert(row)
type DB struct {
	mu     sync.Mutex
	dir    string
	tables map[string]*tableEntry
}

// OpenDB opens (or creates) the table catalog rooted at dir.
// All table schemas are loaded into memory; HeapFiles are opened lazily
// on the first OpenTable call.
func OpenDB(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("storage.OpenDB: mkdir %q: %w", dir, err)
	}
	// Remove any stale tmp file left by a crashed CreateTable/DropTable.
	// The rename never completed, so catalog.json is still authoritative.
	_ = os.Remove(filepath.Join(dir, catalogTmpFileName))
	db := &DB{dir: dir, tables: make(map[string]*tableEntry)}
	if err := db.loadCatalog(); err != nil {
		return nil, fmt.Errorf("storage.OpenDB: %w", err)
	}
	return db, nil
}

// Close syncs and closes all open table HeapFiles and their indexes.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	var firstErr error
	for _, e := range db.tables {
		if e.handle != nil {
			if err := e.handle.closeHeap(); err != nil && firstErr == nil {
				firstErr = err
			}
			e.handle = nil
		}
	}
	return firstErr
}

// Flush syncs all open tables to disk, ensuring all previous writes are durable.
func (db *DB) Flush() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	for _, e := range db.tables {
		if e.handle != nil {
			if err := e.handle.Flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateTable registers a new table with the given schema and returns an open
// TableHandle ready for use. Returns ErrTableExists if the name is taken.
func (db *DB) CreateTable(name string, schema *Schema) (*TableHandle, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.tables[name]; exists {
		return nil, &ErrTableExists{Name: name}
	}

	heap, err := OpenHeapFile(db.dir, name)
	if err != nil {
		return nil, fmt.Errorf("storage.CreateTable %q: %w", name, err)
	}
	handle := &TableHandle{name: name, schema: schema, heap: heap}
	db.tables[name] = &tableEntry{schema: schema, handle: handle}

	if err := db.persistCatalog(); err != nil {
		// Roll back in-memory state on persistence failure.
		_ = heap.Close()
		delete(db.tables, name)
		return nil, fmt.Errorf("storage.CreateTable %q: persist: %w", name, err)
	}
	return handle, nil
}

// DropTable closes the table (if open), removes all its segment files from
// disk, and removes it from the catalog. Returns ErrTableNotFound if unknown.
func (db *DB) DropTable(name string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	e, exists := db.tables[name]
	if !exists {
		return &ErrTableNotFound{Name: name}
	}
	if e.handle != nil {
		if err := e.handle.closeHeap(); err != nil {
			return fmt.Errorf("storage.DropTable %q: close: %w", name, err)
		}
		e.handle = nil
	}

	// Remove all segment files: <name>.0.heap, <name>.1.heap, ...
	pattern := filepath.Join(db.dir, name+".*.heap")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("storage.DropTable %q: glob: %w", name, err)
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("storage.DropTable %q: remove %s: %w", name, path, err)
		}
	}

	// Remove all index files: <name>.<colName>.idx
	idxPattern := filepath.Join(db.dir, name+".*.idx")
	idxMatches, err := filepath.Glob(idxPattern)
	if err != nil {
		return fmt.Errorf("storage.DropTable %q: glob idx: %w", name, err)
	}
	for _, path := range idxMatches {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("storage.DropTable %q: remove %s: %w", name, path, err)
		}
	}

	delete(db.tables, name)
	if err := db.persistCatalog(); err != nil {
		return fmt.Errorf("storage.DropTable %q: persist: %w", name, err)
	}
	return nil
}

// OpenTable returns a TableHandle for the named table, opening its HeapFile
// if not already open. Returns ErrTableNotFound if the table does not exist.
// Repeated calls return the same cached handle.
func (db *DB) OpenTable(name string) (*TableHandle, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	e, exists := db.tables[name]
	if !exists {
		return nil, &ErrTableNotFound{Name: name}
	}
	if e.handle != nil {
		return e.handle, nil
	}

	heap, err := OpenHeapFile(db.dir, name)
	if err != nil {
		return nil, fmt.Errorf("storage.OpenTable %q: %w", name, err)
	}
	handle := &TableHandle{name: name, schema: e.schema, heap: heap}

	// Auto-load any persisted index files for this table.
	idxPattern := filepath.Join(db.dir, name+".*.idx")
	idxMatches, err := filepath.Glob(idxPattern)
	if err != nil {
		_ = heap.Close()
		return nil, fmt.Errorf("storage.OpenTable %q: glob idx: %w", name, err)
	}
	for _, path := range idxMatches {
		base := filepath.Base(path)
		colName, ok := indexFileColName(name, base)
		if !ok {
			continue
		}
		ci, ok := e.schema.ColumnIndex(colName)
		if !ok {
			continue // column no longer in schema; skip stale file
		}
		idx, err := OpenIndex(db.dir, name, colName, ci, false)
		if err != nil {
			_ = heap.Close()
			return nil, fmt.Errorf("storage.OpenTable %q: open index %q: %w", name, colName, err)
		}
		handle.attachIndex(idx)
	}

	e.handle = handle
	return handle, nil
}

// ValidationIssue describes a single problem found during a Validate scan.
type ValidationIssue struct {
	Table   string // table name
	PageID  uint64 // 0 = meta/catalog-level problem; ≥1 = data page
	Problem string // human-readable description
}

// Validate scans every table in the catalog and checks that all pages can be
// read and pass their CRC32 checksum. It returns one ValidationIssue per
// problem found; a nil/empty slice means the database is healthy.
//
// Validate does not modify any data. It is safe to call on a live database.
func (db *DB) Validate() []ValidationIssue {
	db.mu.Lock()
	names := make([]string, 0, len(db.tables))
	for name := range db.tables {
		names = append(names, name)
	}
	db.mu.Unlock()

	var issues []ValidationIssue
	for _, name := range names {
		heap, err := OpenHeapFile(db.dir, name)
		if err != nil {
			issues = append(issues, ValidationIssue{
				Table:   name,
				PageID:  0,
				Problem: fmt.Sprintf("cannot open heap file: %v", err),
			})
			continue
		}
		total := heap.PageCount()
		for pageID := uint64(1); pageID < total; pageID++ {
			if _, err := heap.readPage(pageID); err != nil {
				issues = append(issues, ValidationIssue{
					Table:   name,
					PageID:  pageID,
					Problem: err.Error(),
				})
			}
		}
		_ = heap.Close()
	}
	return issues
}

// TableNames returns an alphabetically sorted list of all known table names.
func (db *DB) TableNames() []string {
	db.mu.Lock()
	defer db.mu.Unlock()
	names := make([]string, 0, len(db.tables))
	for name := range db.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Dir returns the database directory path.
func (db *DB) Dir() string {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.dir
}

// CreateDatabase creates a new database directory at filepath.Join(parentDir, name).
// Returns ErrDatabaseExists if the directory already exists.
func CreateDatabase(parentDir, name string) error {
	target := filepath.Join(parentDir, name)
	if _, err := os.Stat(target); err == nil {
		return &ErrDatabaseExists{Name: name}
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("storage.CreateDatabase %q: %w", name, err)
	}
	return nil
}

// DropDatabase removes the database directory at filepath.Join(parentDir, name)
// and all its contents. Returns ErrDatabaseNotFound if the directory does not exist.
func DropDatabase(parentDir, name string) error {
	target := filepath.Join(parentDir, name)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return &ErrDatabaseNotFound{Name: name}
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("storage.DropDatabase %q: %w", name, err)
	}
	return nil
}

// RenameDatabase renames the database directory from filepath.Join(parentDir, oldName)
// to filepath.Join(parentDir, newName).
// Returns ErrDatabaseNotFound if the source does not exist.
// Returns ErrDatabaseExists if the destination already exists.
func RenameDatabase(parentDir, oldName, newName string) error {
	src := filepath.Join(parentDir, oldName)
	dst := filepath.Join(parentDir, newName)
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return &ErrDatabaseNotFound{Name: oldName}
	}
	if _, err := os.Stat(dst); err == nil {
		return &ErrDatabaseExists{Name: newName}
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("storage.RenameDatabase %q -> %q: %w", oldName, newName, err)
	}
	return nil
}

// ---- persistence helpers --------------------------------------------------------

func (db *DB) loadCatalog() error {
	path := filepath.Join(db.dir, catalogFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // fresh directory, no catalog yet
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", catalogFileName, err)
	}
	var cf catalogFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return &ErrCorruptCatalog{Reason: err.Error()}
	}
	for name, tr := range cf.Tables {
		cols := make([]Column, len(tr.Columns))
		for i, cr := range tr.Columns {
			cols[i] = Column{Name: cr.Name, Type: cr.Type, MaxLen: cr.MaxLen, Nullable: cr.Nullable}
		}
		schema, err := NewSchema(cols)
		if err != nil {
			return &ErrCorruptCatalog{Reason: fmt.Sprintf("table %q: %v", name, err)}
		}
		db.tables[name] = &tableEntry{schema: schema}
	}
	return nil
}

func (db *DB) persistCatalog() error {
	cf := catalogFile{Tables: make(map[string]tableRecord, len(db.tables))}
	for name, e := range db.tables {
		cols := make([]columnRecord, e.schema.NumColumns())
		for i := range cols {
			c := e.schema.Column(i)
			cols[i] = columnRecord{Name: c.Name, Type: c.Type, MaxLen: c.MaxLen, Nullable: c.Nullable}
		}
		cf.Tables[name] = tableRecord{Columns: cols}
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmpPath := filepath.Join(db.dir, catalogTmpFileName)
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	finalPath := filepath.Join(db.dir, catalogFileName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
