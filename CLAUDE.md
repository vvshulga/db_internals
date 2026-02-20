# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run all tests (always use -race)
go test -v -race ./...

# Run a single package
go test -v -race ./storage/...

# Run a single test by name
go test -v -race -run TestHeapFile_Insert ./storage/...

# Build the CLI
go build -o db_internals .
```

## Architecture

This is an educational OLTP database engine built bottom-up. The layers from lowest to highest:

```
SQL string
    ↓ lexer.Tokenize()
Token stream
    ↓ parser.ParseString()
AST (SelectStmt / InsertStmt / CreateTableStmt)
    ↓ [not yet implemented]
catalog.DB / TableHandle          ← next layer to build
    ↓
storage.HeapFile + storage.Schema ← implemented
    ↓
storage.Page (8 KiB slotted page) ← implemented
    ↓
OS file (pread/pwrite, no buffer pool)
```

### `storage` package — the core, fully implemented

**Page layout** (`page.go`): Fixed 8 KiB, header (19 bytes) + slot array growing forward + tuple data growing backward. Slot deleted sentinel: offset=0xFFFF, length=0xFFFF. CRC32-IEEE checksum covers the whole page with the checksum field zeroed.

**Record format** (`record.go` + `schema.go`): Binary layout is `[null bitmap: ceil(N/8) bytes] [fixed region] [var directory: 4 bytes/var-col] [var data]`. All integers little-endian. The `Schema` pre-computes all byte offsets once in `NewSchema`; encoding and decoding use these cached offsets directly.

**HeapFile** (`heap.go`): A table spans multiple segment files named `<tableName>.N.heap` (each capped at `PagesPerSegment=131072` pages = 1 GiB). `RID.PageID` is a global page number; the segment is derived as `PageID/PagesPerSegment`. Page 0 is the MetaPage (stores `TotalPages` in slot 0). Insert strategy: try last page → `Compact()` + retry → allocate new page.

**RID** (`rid.go`): `{PageID uint64, SlotID uint16}` — 10 bytes, comparable, usable as map key. `PageID==0` is always the MetaPage (reserved, never a valid data row).

### `lexer` / `parser` packages — SQL frontend, complete but not connected to storage

Lexer produces typed tokens; parser builds a recursive-descent AST for `SELECT`, `INSERT`, and `CREATE TABLE`. No execution engine connects these to storage yet.

## Durability Model

Write operations are NOT automatically synced to disk. Data is buffered by the OS page cache.

**To ensure durability:**
- Call `db.Flush()` or `table.Flush()` explicitly
- Call `db.Close()` which syncs all files before closing
- In daemon mode, `dbctl stop` triggers `Close()`, ensuring clean shutdown

**What this means:**
- Normal operation: writes are fast (OS buffered)
- Process crash: last few writes MAY be lost (OS dependent)
- Clean shutdown: all data is synced before exit

Future work: Implement Write-Ahead Log (WAL) for crash recovery.

## Conventions

- All binary encoding is **little-endian**.
- Each test file uses `t.TempDir()` for isolation; no shared state between tests.
- Error types are pointer receivers and checked via `errors.As`, never string matching.
- The storage package has zero external dependencies (stdlib only).
