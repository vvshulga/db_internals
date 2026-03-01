# db_internals

Education project for the DB Internals CS osvita course. Implements an OLTP row-store database engine from scratch in Go: SQL lexer + parser, a slotted-page storage layer, and a table-manager API.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Workload Assumptions](#workload-assumptions)
- [Storage Architecture](#storage-architecture)
- [On-Disk Layout](#on-disk-layout)
- [Table Manager API](#table-manager-api)
- [storage\_cli](#storage_cli)
- [dbserver / dbctl — Daemon Mode](#dbserver--dbctl--daemon-mode)
- [Web Admin (ui_admin)](#web-admin-ui_admin)
- [Demo](#demo)
- [Running and Recovery](#running-and-recovery)
- [SQL Frontend](#sql-frontend)
- [Testing](#testing)

---

## Quick Start

**Prerequisites**: Go 1.25+

```bash
git clone https://github.com/vvshulga/db_internals.git
cd db_internals
go test -race ./...            # run all tests
make build                     # build all CLI/daemon binaries
./db_internals /tmp/mydb "SELECT * FROM users WHERE id = 1"
```

---

## Workload Assumptions

The storage engine is designed for **OLTP** (Online Transaction Processing):

| Assumption | Design choice |
|---|---|
| Short transactions (single-row reads/writes) | Direct page I/O, no buffer pool |
| Point lookups dominate | Stable `RID` (page+slot) for O(1) fetch |
| Occasional full-table scans | Sequential `Scan()` walks pages in order |
| Row sizes usually fit in one page | Max inline row ≈ 8 KiB; large TEXT values spill to linked overflow pages |
| Single-writer, no crash recovery | Coarse mutex per `HeapFile`; no WAL/redo log |
| Tables grow via append | Freed overflow pages are tracked in a singly-linked freelist and reused on the next write; inline tombstone space reclaimed by compaction on insert |

The engine does **not** implement: buffer pool, WAL, multi-version concurrency control, or cross-table joins.
Implemented so far: B-tree column indexes, large-TEXT overflow pages with a freelist, and a SQL execution engine (`query.Engine`).

---

## Storage Architecture

```
Application
     │  Row ([]Value)
     ▼
storage.DB / TableHandle          ← table registry + per-table CRUD
     │  Schema + encoded bytes
     ▼
storage.HeapFile                  ← append-only multi-segment file
     │  Page (read/write by global page ID)
     ▼
storage.Page  (8 KiB slotted)     ← insert / fetch / delete / compact
     │  raw [8192]byte
     ▼
OS file  (ReadAt / WriteAt)       ← no seek, no buffer pool
```

### Key types (all in `storage/`)

| Type | Role |
|---|---|
| `DB` | Catalog: maps table names → schemas, manages `HeapFile` lifetimes, persists `catalog.json` |
| `TableHandle` | Open table: bundles `HeapFile` + `Schema`, exposes `Insert/Get/Update/Delete/Scan` |
| `Scanner` | Pull iterator: `Next() / RID() / Row() / Err()` |
| `HeapFile` | Multi-segment data file for one table |
| `Page` | 8 KiB slotted page; tuple insert / fetch / delete / compact |
| `Schema` | Pre-computes column byte offsets once at construction |
| `Value` / `Row` | Tagged-union value type; `Row = []Value` |
| `RID` | `{PageID uint64, SlotID uint16}` — stable 10-byte row address |

### Insert flow

```
tbl.Insert(row)
      │
      ▼
1. Encode row → []byte
   NullBitmap | Fixed columns | Var directory | Var data
      │
      ▼
2. Read last data page  (PageID = TotalPages - 1)
      │
      ├── free space ≥ len(bytes) + 4 ──▶ 3a. Write tuple (grows ←)
      │                                        Add slot entry (grows →)
      │                                        Write page to disk
      │                                        Return RID{PageID, SlotID}
      │
      └── full (ErrPageFull)
            │
            ▼
         Compact()  ← reclaim tombstone space, repack tuples at end
            │
            ├── fits now ──▶ 3a (same as above)
            │
            └── still full
                  │
                  ▼
               appendPage()  ← create new DataPage, increment TotalPages,
                                update MetaPage on disk, write new page
                  │
                  ▼
               3a on the new page
```

### Page state: insert, delete, compaction

A page grows the **slot array rightward** and **tuple data leftward**.
`FSO` = FreeSpaceOffset (end of slot array), `FSE` = FreeSpaceEnd (start of tuple data).

**After inserting two tuples T0 and T1:**

```
byte:  0        19      27      31                      8152  8172  8192
       ┌────────┬───────┬───────┬──────────────────────┬─────┬─────┐
       │ Header │  S0   │  S1   │   · free space ·      │ T1  │ T0  │
       └────────┴───────┴───────┴──────────────────────┴─────┴─────┘
                         ↑ FSO=31                  FSE=8152 ↑
S0 = {Offset:8172, Length:20}  → RID{PageID, SlotID:0}
S1 = {Offset:8152, Length:20}  → RID{PageID, SlotID:1}
```

**After deleting T0 (SlotID 0):**

```
byte:  0        19      27      31                      8152  8172  8192
       ┌────────┬───────┬───────┬──────────────────────┬─────┬─────┐
       │ Header │ S0✗   │  S1   │   · free space ·      │ T1  │ T0  │
       └────────┴───────┴───────┴──────────────────────┴─────┴─────┘
                         ↑ FSO=31 (unchanged)       FSE=8152 ↑
S0✗ = {0xFFFF, 0xFFFF}  ← tombstone sentinel
T0 bytes still occupy the page; space is NOT reclaimed yet.
Get(RID{PageID,0}) returns (nil, false, nil) — not found.
```

**After Compact() (triggered lazily when the next insert finds the page full):**

```
byte:  0        19      27      31                      8172  8192
       ┌────────┬───────┬───────┬──────────────────────┬─────┐
       │ Header │ S0✗   │  S1'  │   · free space ·      │ T1  │
       └────────┴───────┴───────┴──────────────────────┴─────┘
                         ↑ FSO=31               FSE=8172 ↑
S1' = {Offset:8172, Length:20}  (T1 shifted; S1 SlotID unchanged)
S0✗ tombstone remains — SlotIDs are never reused or renumbered.
T0 bytes are gone; 20 bytes of free space reclaimed.
```

**Key invariants**

- A `RID{PageID, SlotID}` is stable for the lifetime of a live tuple — `Get` uses it for O(1) lookup.
- `Delete` is O(1): it writes the tombstone sentinel `{0xFFFF, 0xFFFF}` into the slot entry and writes the page back. Tuple bytes are left in place.
- `Compact` is O(slots + tuples): it repacks all live tuples at the end of the page without reordering slot IDs.
- `Update` = `Delete` old RID + `Insert` new row → the new row may land on a different page, producing a new RID.

### Operation characteristics

#### Well-optimized

| Operation | Cost | Why |
|---|---|---|
| **Get by RID** | O(1), 1 page read | `PageID` maps directly to a file offset; `SlotID` is an array index into the slot table. No scanning. |
| **Insert** | Amortized O(1), 1–2 page writes | Appends to the last data page. A new page is allocated only when the current one is full (rare relative to total inserts). |
| **Delete** | O(1), 1 page read + 1 page write | Overwrites one slot entry with the tombstone sentinel. No data movement, no cascading updates. |
| **Full-table scan** | O(pages), sequential I/O | Pages are read in order with `ReadAt`; no random access per row. Sequential I/O is cache-friendly and easy for the OS to prefetch. |

#### With a column index (`CreateIndex`)

When a B-tree index exists on a column, point lookups and range queries skip
the full-table scan entirely. The index is persisted to `<table>.<col>.idx`
and auto-loaded on `OpenTable`.

| Operation | Complexity | Notes |
|---|---|---|
| **`LookupExact`** | O(log n) | B-tree point lookup; no page I/O for the search itself |
| **`RangeScan`** | O(log n + k) | k = matching rows; each hit requires one heap page read |
| **`Insert` overhead** | O(log n) | One B-tree insert per indexed column after the heap write |
| **`Delete` overhead** | O(log n) | One heap fetch (to read old value) + one B-tree delete |
| **Index open / rebuild** | O(n) | Flat-file replay on `OpenTable`; n = index entry count |

**Benchmark comparison — 1 000-row table, `id INT` index (Intel i7-9750H):**

| Operation | Time/op | Allocs/op | Notes |
|---|---|---|---|
| Full-table scan (find by value) | 122 520 ns | 2 024 | Reads all pages, materialises all rows |
| Index lookup (`LookupExact`) | 179 ns | 1 | **~684× faster** than full scan |
| Index insert | 12 404 ns | 7 | Heap write + B-tree insert |
| Range scan (~10% of rows) | 539 067 ns | 505 | 100-row range, 1 heap fetch per hit |

#### Not optimized (without an index)

| Operation | Cost | Reason |
|---|---|---|
| **Lookup by field value** | O(rows), full scan | Without an index, finding a row by any column requires scanning every live tuple. |
| **Range queries** | O(rows), full scan | Rows are stored in insertion order within pages, not sorted by any key. Range predicates cannot skip pages. |
| **Update** | 2× single-row cost | Always a delete-then-reinsert. Two page writes minimum; the new row may land on a different page, making the new RID unpredictable for callers. |
| **Write-heavy workloads with many deletes** | Scan degrades over time | Tombstone slots accumulate in pages and are only reclaimed by `Compact`, which fires lazily on the next insert into a full page. A page with many tombstones wastes read bandwidth during scans because all slot entries are visited. |
| **Concurrent writes** | Serialised per table | A single `sync.Mutex` guards the entire `HeapFile`. Multiple goroutines writing to the same table are fully serialised — no per-page or per-row latching. |

---

## On-Disk Layout

### Directory structure

```
<data-dir>/
  catalog.json          ← JSON table registry (names + column definitions)
  catalog.json.tmp      ← atomic write scratch file (rename → catalog.json)
  users.0.heap          ← segment 0 of table "users"  (up to 1 GiB)
  users.1.heap          ← segment 1 of table "users"  (created when seg 0 fills)
  orders.0.heap         ← segment 0 of table "orders"
  ...
```

Each table's data spans one or more segment files named `<table>.N.heap`.
A **global page ID** is used in every `RID`; the segment is derived transparently:

```
segmentID  = PageID / 131072
localPage  = PageID % 131072
fileOffset = localPage × 8192
```

### Segment file layout

```
Segment file (<table>.N.heap)
┌──────────────────────────────────┐  ← offset 0 (only in segment 0)
│  Page 0 — MetaPage               │
│  slot 0: TotalPages    uint64 LE   bytes  0–7   │
│           FreeListHead  uint64 LE   bytes  8–15  │  46 bytes reserved
├──────────────────────────────────┤  ← offset 8192
│  Page 1 — DataPage  (first data) │
├──────────────────────────────────┤  ← offset 16384
│  Page 2 — DataPage               │
│  ...                             │
└──────────────────────────────────┘
```

### Page layout (8192 bytes)

```
┌─────────────────────────────────────────────────────────────────┐
│  Header  (19 bytes, little-endian)                               │
│    PageID          [0..7]   uint64                               │
│    PageType        [8]      uint8   (1=Data 2=Overflow 3=Meta)   │
│    NumSlots        [9..10]  uint16                               │
│    FreeSpaceOffset [11..12] uint16  ← next free byte (grows →)  │
│    FreeSpaceEnd    [13..14] uint16  ← tuple area start (grows ←)│
│    Checksum        [15..18] uint32  CRC32-IEEE                   │
├─────────────────────────────────────────────────────────────────┤
│  Slot array  (4 bytes × NumSlots, grows →)                       │
│    Each slot: Offset uint16 + Length uint16                      │
│    Deleted sentinel: Offset=0xFFFF, Length=0xFFFF               │
├─────────────────────────────────────────────────────────────────┤
│  Free space                                                      │
├─────────────────────────────────────────────────────────────────┤
│  Tuple data  (grows ←, packed at end of page)                    │
└─────────────────────────────────────────────────────────────────┘
```

### Record (tuple) binary format

```
┌────────────────────────────────────────────────────────────────┐
│ NullBitmap  ceil(N/8) bytes                                     │
│   bit i = 1 → column i is NULL  (LSB-first within each byte)   │
├────────────────────────────────────────────────────────────────┤
│ Fixed region  (one entry per fixed-length column, in order)     │
│   INT       → 4 bytes  (int32 LE)                              │
│   BIGINT    → 8 bytes  (int64 LE)                              │
│   FLOAT     → 4 bytes  (IEEE 754 LE)                           │
│   DOUBLE    → 8 bytes  (IEEE 754 LE)                           │
│   BOOLEAN   → 1 byte   (0x00 / 0x01)                           │
│   DATETIME  → 8 bytes  (int64 Unix nanoseconds LE)             │
│   (slot always present even if NULL — null bitmap is authority) │
├────────────────────────────────────────────────────────────────┤
│ Var directory  (4 bytes per variable-length column, in order)   │
│   Offset uint16 LE  — byte offset from start of var-data region │
│   Length uint16 LE  — byte length; both 0 if NULL              │
├────────────────────────────────────────────────────────────────┤
│ Var data  (VARCHAR / TEXT values, concatenated, no padding)     │
└────────────────────────────────────────────────────────────────┘
Total = ceil(N/8) + fixedSize + 4×numVarCols + Σ(varDataLengths)
```

### catalog.json format

```json
{
  "tables": {
    "users": {
      "columns": [
        { "name": "id",    "type": 1 },
        { "name": "name",  "type": 7, "max_len": 64 },
        { "name": "score", "type": 4, "nullable": true }
      ]
    }
  }
}
```

Column `type` values: 1=INT, 2=BIGINT, 3=FLOAT, 4=DOUBLE, 5=BOOLEAN, 6=DATETIME, 7=VARCHAR, 8=TEXT.

---

## Table Manager API

```go
// Open or create a database directory.
db, err := storage.OpenDB("/var/db/mydb")
defer db.Close()

// Define a schema.
schema, _ := storage.NewSchema([]storage.Column{
    {Name: "id",   Type: storage.TypeINT},
    {Name: "name", Type: storage.TypeVARCHAR, MaxLen: 128},
})

// create_table — returns an open TableHandle.
tbl, err := db.CreateTable("users", schema)

// open_table — reopens an existing table (returns cached handle if already open).
tbl, err = db.OpenTable("users")

// insert — returns a stable RID.
rid, err := tbl.Insert(storage.Row{
    storage.NewIntValue(1),
    storage.NewVarcharValue("Alice"),
})

// get — (row, true, nil) found; (nil, false, nil) deleted/missing; (nil, false, err) I/O error.
row, ok, err := tbl.Get(rid)

// update — delete-then-reinsert; returns new RID.
newRID, ok, err := tbl.Update(rid, storage.Row{
    storage.NewIntValue(1),
    storage.NewVarcharValue("Alice (updated)"),
})

// delete — (true, nil) deleted; (false, nil) already gone; (false, err) I/O error.
ok, err = tbl.Delete(rid)

// scan — pull iterator over all live rows in page order.
s := tbl.Scan()
for s.Next() {
    rid, row := s.RID(), s.Row()
    _ = rid; _ = row
}
if err := s.Err(); err != nil { ... }

// drop_table — closes, removes heap files, removes from catalog.
err = db.DropTable("users")
```

---

## storage_cli

`storage_cli` is a command-line tool that exposes the full table management API over the shell. Build it once; every invocation opens the database directory, runs the command, and closes cleanly — demonstrating that data persists across process restarts.

```bash
go build -o storage_cli ./cmd/storage_cli/
```

**Global flag**

```
storage_cli [--dir <path>] <command> [args...]
    --dir   database directory (default: ./data)
```

**Command reference**

| Command | Syntax | Example |
|---|---|---|
| `list-tables` | `list-tables` | `./storage_cli --dir ./data list-tables` |
| `describe` | `describe <table>` | `./storage_cli --dir ./data describe users` |
| `create-table` | `create-table <table> <col:type> ...` | see below |
| `drop-table` | `drop-table <table>` | `./storage_cli --dir ./data drop-table users` |
| `insert` | `insert <table> <val> ...` | `./storage_cli --dir ./data insert users 1 Alice` |
| `get` | `get <table> <pageID:slotID>` | `./storage_cli --dir ./data get users 1:0` |
| `update` | `update <table> <pageID:slotID> <val> ...` | `./storage_cli --dir ./data update users 1:0 1 Bob` |
| `delete` | `delete <table> <pageID:slotID>` | `./storage_cli --dir ./data delete users 1:0` |
| `scan` | `scan <table>` | `./storage_cli --dir ./data scan users` |

**Column types for `create-table`**: `int`, `bigint`, `float`, `double`, `boolean`, `datetime`, `varchar(N)`, `text`. Append `?` to any type to mark it nullable (e.g. `score:double?`).

Specs that contain shell-special characters (`(`, `)`, `?`) must be quoted:

```bash
./storage_cli --dir ./data create-table users \
    id:int \
    'name:varchar(64)' \
    'score:double?'
```

**Output conventions**

- `insert` prints `inserted <pageID:slotID>`
- `update` prints `updated <pageID:slotID>` (new RID) — update is delete-then-reinsert
- `delete` prints `deleted` or `not found`
- `get` and `scan` print `<pageID:slotID>  col=val  col=val  ...`

---

## dbserver / dbctl — Daemon Mode

`dbserver` is a long-running daemon that keeps a single `storage.DB` instance open.
`dbctl` is the control and client CLI: it manages the daemon's lifecycle and forwards
storage commands to it over a Unix socket.

The key advantage over `storage_cli` is that the daemon holds the entire B-tree index in
memory between calls. With `storage_cli` the index is cold-loaded from disk on every
invocation; with the daemon it stays hot, giving O(log n) lookups with zero startup cost.

### Build

```bash
go build -o dbserver ./cmd/dbserver/
go build -o dbctl    ./cmd/dbctl/
```

Both binaries must live in the same directory. `dbctl start` locates `dbserver` by looking
next to itself first, then searching `$PATH`.

### Daemon lifecycle

```bash
# Start the daemon in the background.
# Creates <dir>/dbserver.sock and <dir>/dbserver.pid.
# Daemon output is written to <dir>/dbserver.log.
./dbctl --dir ./data start
# → dbserver started (pid 1234, log: ./data/dbserver.log)

# Check whether it is running.
./dbctl --dir ./data status
# → dbserver is running (pid 1234)

# Restart (stop + start).
./dbctl --dir ./data restart

# Graceful shutdown (SIGTERM — waits for in-flight requests to finish).
./dbctl --dir ./data stop
# → dbserver stopped
```

**Global flag** — every `dbctl` subcommand accepts:

```
--dir <path>    database directory (default: ./data)
```

### Runtime files

| File | Created by | Purpose |
|---|---|---|
| `<dir>/dbserver.pid` | `dbserver` | Process ID — used by `dbctl stop` to send SIGTERM |
| `<dir>/dbserver.sock` | `dbserver` | Unix domain socket — used by all storage commands |
| `<dir>/dbserver.log` | `dbctl start` | Daemon stdout + stderr |

### Storage commands

Once the daemon is running, all storage commands are forwarded over the socket:

| Command | Syntax | Example |
|---|---|---|
| `list-tables` | `list-tables` | `./dbctl list-tables` |
| `describe` | `describe <table>` | `./dbctl describe users` |
| `create-table` | `create-table <table> <col:type> ...` | see below |
| `drop-table` | `drop-table <table>` | `./dbctl drop-table users` |
| `insert` | `insert <table> <val> ...` | `./dbctl insert users 1 Alice` |
| `get` | `get <table> <pageID:slotID>` | `./dbctl get users 1:0` |
| `update` | `update <table> <pageID:slotID> <val> ...` | `./dbctl update users 1:0 1 Bob` |
| `delete` | `delete <table> <pageID:slotID>` | `./dbctl delete users 1:0` |
| `scan` | `scan <table>` | `./dbctl scan users` |

Column types and nullable syntax are identical to `storage_cli`. Quote arguments that
contain shell-special characters:

```bash
./dbctl --dir ./data create-table employees \
    id:int \
    'name:varchar(64)' \
    dept:int \
    'salary:double?'
```

### End-to-end example

```bash
# Build and start.
go build -o dbserver ./cmd/dbserver/
go build -o dbctl    ./cmd/dbctl/
./dbctl --dir /tmp/mydb start

# DDL.
./dbctl --dir /tmp/mydb create-table users id:int 'name:varchar(64)'

# DML.
./dbctl --dir /tmp/mydb insert users 1 Alice
./dbctl --dir /tmp/mydb insert users 2 Bob
./dbctl --dir /tmp/mydb scan users
# → 1:0  id=1  name=Alice
# → 1:1  id=2  name=Bob

./dbctl --dir /tmp/mydb update users 1:0 99 Alice-Updated
./dbctl --dir /tmp/mydb delete users 1:1
./dbctl --dir /tmp/mydb scan users
# → 1:2  id=99  name=Alice-Updated

# Restart — data survives; daemon re-opens the heap files and index files from disk.
./dbctl --dir /tmp/mydb restart
./dbctl --dir /tmp/mydb scan users
# → 1:2  id=99  name=Alice-Updated

./dbctl --dir /tmp/mydb stop
```

### Wire protocol

`dbctl` and `dbserver` communicate via newline-delimited JSON on a Unix domain socket.
Each client connection carries exactly one request/response pair and is then closed.

```
→ {"cmd":"insert","args":["users","1","Alice"]}\n
← {"ok":true,"output":"inserted 1:0"}\n

→ {"cmd":"scan","args":["users"]}\n
← {"ok":true,"output":"1:0  id=1  name=Alice"}\n

→ {"cmd":"insert","args":["missing","1"]}\n
← {"ok":false,"error":"storage: table \"missing\" not found"}\n
```

The server accepts concurrent connections; each is handled in its own goroutine.
`Shutdown` closes the listener and waits for all in-flight handlers before the
process exits.

---

## Web Admin (ui_admin)

`ui_admin` consists of two parts that run together:

- **Backend** (`ui_admin/backend/`) — Go HTTP server exposing a REST API backed by the `storage` package. Builds into `ui_admin/ui_admin_server`. **Go only, no Node.js required.**
- **Frontend** (`ui_admin/frontend/`) — React 18 + TypeScript single-page app. Runs as a separate Vite dev server that proxies `/api/*` to the backend. **Requires Node.js 18+ and npm.**

### Build (backend only — no Node.js required)

```bash
cd ui_admin && make backend && cd ..
# Produces ./ui_admin/ui_admin_server
```

### Build (full stack — requires Node.js)

```bash
cd ui_admin && make all && cd ..
# Produces ./ui_admin/ui_admin_server  +  frontend/dist/ (static files)
```

Or from the project root:

```bash
make build-all
```

### Run (API server)

```bash
DB_DIR=/path/to/database ./ui_admin/ui_admin_server
# Server starting on :8080, database: /path/to/database
```

The `PORT` environment variable overrides the default port.

### Run with React UI (requires Node.js)

```bash
# Terminal 1 — API backend
DB_DIR=/path/to/database ./ui_admin/ui_admin_server

# Terminal 2 — React frontend (proxies /api/* to :8080)
cd ui_admin && make dev-frontend
```

Navigate to http://localhost:5173.

### API endpoints

Accessible directly at http://localhost:8080 without the frontend:

### API endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/info` | Database metadata (directory, table count) |
| `GET` | `/api/tables` | List all tables |
| `POST` | `/api/tables` | Create table |
| `GET` | `/api/tables/:name` | Table schema |
| `DELETE` | `/api/tables/:name` | Drop table |
| `GET` | `/api/tables/:name/rows[?page=N&page_size=M]` | Scan rows (paginated) |
| `POST` | `/api/tables/:name/rows` | Insert row |
| `GET` | `/api/tables/:name/rows/:rid` | Get row by RID |
| `PUT` | `/api/tables/:name/rows/:rid` | Update row |
| `DELETE` | `/api/tables/:name/rows/:rid` | Delete row |

---

## Demo

`demo.sh` is an end-to-end script that walks through the full employee lifecycle using every `storage_cli` command. It serves as both a quick sanity check and a self-documenting example.

```bash
go build -o storage_cli ./cmd/storage_cli/
bash demo.sh
```

The script creates a temporary database directory (cleaned up on exit) and runs 14 steps:

1. `create-table` — creates an `employees` table with five columns
2. `list-tables` — verifies the table appears in the catalog
3. `describe` — shows the schema
4. `insert` × 5 — inserts Alice, Bob, Carol, Dave, Eve; captures their RIDs
5. `scan` — prints all five rows
6. `get` — fetches Alice by RID
7. `update` — promotes Alice (new department, higher salary); prints her new RID
8. `scan` — shows Alice at her new RID, old slot is a tombstone
9. `delete` — removes Bob
10. `get` — tries Bob's old RID → `not found`
11. `scan` — four remaining employees, Bob's slot skipped
12. persistence — re-runs `list-tables` and `scan` to confirm data survives process restarts
13. `drop-table` — removes the table and its heap files
14. `list-tables` — confirms the catalog is empty

Expected final line: `✓  demo complete`

### Daemon + Web Admin demo

An interactive end-to-end walkthrough using the daemon and the web admin UI.

**Step 1 — Build all binaries:**

```bash
make build                            # Go binaries (dbserver, dbctl, seeddb, …)
cd ui_admin && make backend && cd ..  # REST API server (no Node.js required)
```

> To also get the React UI, replace `make backend` with `make all` (requires Node.js 18+).
> Steps 2–4 (daemon + CLI) work with `make build` alone.

**Step 2 — Seed the database with sample data:**

`seeddb` writes **directly to the storage layer** (not through the daemon), so it must
run **before** the daemon starts. The bundled `ui_admin/seed.sql` creates five
e-commerce tables (users, categories, products, orders, order_items) with ~75 rows.

```bash
./seeddb -db /tmp/demo -sql ui_admin/seed.sql
```

**Step 3 — Start the database daemon:**

```bash
./dbctl --dir /tmp/demo start
# → dbserver started (pid XXXX, log: /tmp/demo/dbserver.log)
```

The daemon loads the catalog from disk at startup, so it will see all tables seeded above.

**Step 4 — Verify via CLI:**

```bash
./dbctl --dir /tmp/demo list-tables
./dbctl --dir /tmp/demo scan users
```

**Step 5 — Start the web admin:**

`ui_admin_server` opens the database directory directly, so stop the daemon first
(both hold the heap files open; they cannot share a directory simultaneously).

```bash
./dbctl --dir /tmp/demo stop
DB_DIR=/tmp/demo ./ui_admin/ui_admin_server
# → Server starting on :8080, database: /tmp/demo
```

**Step 6 — Open the browser:**

Navigate to http://localhost:8080. The UI lets you browse tables, view and edit rows
(with pagination), and create or drop tables.

**Step 7 — Shut down:**

Press `Ctrl-C` to stop `ui_admin_server`.

---

## Running and Recovery

### Lifecycle

The storage engine can be used in two modes:

**Embedded / stateless** (`storage_cli`) — no persistent background process:

```
1. OpenDB(dir)       ← read catalog.json, validate directory
2. OpenTable / CRUD  ← HeapFile opened lazily on first use; pages read/written directly
3. Close()           ← fsync + close all open segment files
```

Each `storage_cli` invocation runs steps 1–3, exits, then the next call repeats from step 1.

**Daemon** (`dbserver` / `dbctl`) — a single long-running process:

```
dbserver start  → OpenDB once, listen on Unix socket
dbctl <cmd>     → send JSON request → receive JSON response (no startup cost)
dbctl stop      → SIGTERM → Close() → remove socket and PID files
```

The daemon keeps heap files and B-tree indexes open between requests, so repeated index
lookups pay no cold-load cost. Data is flushed to disk when indexes are checkpointed or
when the daemon is stopped cleanly.

Both modes ensure data consistency on clean shutdown: `Close()` calls `Sync()` on all
files before exiting. For explicit durability guarantees before shutdown, call `Flush()`.
Note: A process crash may lose recent writes that were not explicitly flushed (OS page
cache behavior). Future work will add Write-Ahead Logging (WAL) for crash recovery.

### Normal restart

When a process opens the database after a clean shutdown:

| Step | Code path | Effect |
|---|---|---|
| `OpenDB(dir)` | `os.MkdirAll` + remove stale `catalog.json.tmp` + `loadCatalog` | Table names and schemas loaded into memory |
| `OpenTable(name)` | `OpenHeapFile` → `readMeta` (page 0) | `TotalPages` restored; heap ready |
| First `Insert/Get/Scan` | `readPage(pageID)` → `Unmarshal` → CRC32 check | Every read validates its checksum |

All rows that were written before the previous `Close()` are immediately visible.
No replay or recovery step is needed.

### Crash scenarios

A crash (power loss, SIGKILL, panic) while a write is in progress can leave the database in a partially updated state.

| In-flight operation | On-disk state after crash | Effect on restart |
|---|---|---|
| `Insert` (writing a data page) | Page may have bad checksum or be partially written | `readPage` returns a checksum error for that page; other pages unaffected |
| `appendPage` (writing new data page before `writeMeta`) | New page exists on disk; `TotalPages` not updated | Orphaned page (wastes space); no data loss for any existing row |
| `writeMeta` (rewriting page 0) | Meta page may have bad checksum | Table inaccessible until page 0 is repaired or table is dropped and recreated |
| `persistCatalog` (write to `catalog.json.tmp`) | `catalog.json.tmp` exists; rename never ran | `OpenDB` removes the tmp file; `catalog.json` remains authoritative — no data loss |
| `persistCatalog` (rename atomically) | Either old or new catalog visible, never both | Correct state either way |

### Recovery steps

| Symptom | Diagnosis | Action |
|---|---|---|
| `Validate()` reports a CRC error on a data page | Partial write during insert | Affected rows on that page are inaccessible; other pages are fine. Scan remaining pages, export live data, drop and recreate the table. |
| `Validate()` reports a CRC error on page 0 (meta page) | Crash during `writeMeta` | Table cannot be opened. Drop the table (removes heap files) and recreate it from a backup. |
| `OpenDB` fails with corrupt catalog | Crash during catalog rename — extremely rare due to atomic rename | Restore `catalog.json` from a backup or reconstruct it by inspecting `*.heap` files. |
| Extra `*.heap` files with no matching catalog entry | Orphaned segments from a crashed `appendPage` | Safe to delete manually. |

### Using `DB.Validate()`

`Validate` reads every page in every table and returns one `ValidationIssue` per bad page:

```go
db, err := storage.OpenDB("/var/db/mydb")
if err != nil { log.Fatal(err) }
defer db.Close()

issues := db.Validate()
if len(issues) == 0 {
    fmt.Println("database is healthy")
} else {
    for _, iss := range issues {
        fmt.Printf("table=%s page=%d: %s\n", iss.Table, iss.PageID, iss.Problem)
    }
}
```

`Validate` is read-only and safe to call on a live database. It leverages the CRC32 checksum
that is already validated on every `Unmarshal`, so corrupt pages are detected at no extra cost.

---

## SQL Frontend

The `lexer` and `parser` packages tokenize SQL and build an AST.
The `query` package (`planner` → `optimizer` → `physical operators`) translates that
AST into storage operations. `query.Engine` ties it all together: one `Execute(sql)` call.

**Supported statements**: `SELECT`, `INSERT INTO`, `CREATE TABLE`, `UPDATE`, `DELETE`

```bash
# Usage: db_internals <database-dir> <sql>
./db_internals /tmp/mydb "CREATE TABLE users (id INT, name VARCHAR(64))"
./db_internals /tmp/mydb "INSERT INTO users VALUES (1, 'Alice')"
./db_internals /tmp/mydb "SELECT * FROM users WHERE id = 1"
./db_internals /tmp/mydb "UPDATE users SET name = 'Bob' WHERE id = 1"
./db_internals /tmp/mydb "DELETE FROM users WHERE id = 1"
./db_internals /tmp/mydb "SELECT name, COUNT(*) FROM orders GROUP BY name"
```

**WHERE operators**: `=  !=  <  >  <=  >=`
**Logical operators**: `AND  OR`
**Aggregate functions** (with `GROUP BY`): `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`

---

## Testing

```bash
go test -v -race ./...                                  # all packages
go test -v -race ./storage/...                          # storage only
go test -v -race -run TestTableHandle ./storage/...     # single test by prefix
```

The project uses GitHub Actions to run the full test suite on every push (`.github/workflows/tests.yml`).

### Benchmarks

```bash
go test -bench=. -benchmem ./storage/...
```

Results on Intel Core i7-9750H (darwin/amd64):

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `BenchmarkInsert` | 10,785 | 36,956 | 7 |
| `BenchmarkGet` | 4,146 | 17,744 | 5 |
| `BenchmarkUpdate` | 24,855 | 82,937 | 12 |
| `BenchmarkDelete` | 14,102 | 38,373 | 5 |
| `BenchmarkScan/rows=10` | 6,221 | 19,732 | 29 |
| `BenchmarkScan/rows=100` | 16,675 | 36,773 | 212 |
| `BenchmarkScan/rows=1000` | 170,811 | 214,951 | 2,021 |
