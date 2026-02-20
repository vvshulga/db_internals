// storage_cli is a command-line interface for the storage package.
// It exposes the full table management API (create/drop/open table,
// insert/get/update/delete/scan rows) through simple shell commands.
//
// Usage:
//
//	storage_cli [--dir <dir>] <command> [args...]
//
// Run storage_cli without arguments to see the full help.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/vvshulga/db_internals/internal/cliutil"
	"github.com/vvshulga/db_internals/storage"
)

func usage() {
	fmt.Fprint(os.Stderr, `storage_cli — table storage command-line tool

Usage: storage_cli [--dir <dir>] <command> [args...]

Global flags:
  --dir <path>    database directory (default: ./data)

Commands:
  list-tables
      List all tables in the database.

  describe <table>
      Show the schema (column names and types) of a table.

  create-table <table> <col:type> [<col:type> ...]
      Create a new table. Append ? to a type to mark it nullable.
      Types: int  bigint  float  double  boolean  datetime  varchar(N)  text
      Example: create-table users id:int name:varchar(64) score:double?

  drop-table <table>
      Drop a table and delete all its data files.

  insert <table> <val> [<val> ...]
      Insert a row. Values are positional and must match the schema.
      Use NULL (case-insensitive) for nullable columns.
      Prints the RID of the inserted row.

  get <table> <pageID:slotID>
      Fetch a row by RID. Prints "not found" if deleted or invalid.

  update <table> <pageID:slotID> <val> [<val> ...]
      Replace a row. Prints the new RID or "not found".

  delete <table> <pageID:slotID>
      Delete a row. Prints "deleted" or "not found".

  scan <table>
      Print all live rows in page order.
`)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	dir := flag.String("dir", "./data", "database directory")
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		usage()
		os.Exit(1)
	}
	cmd, rest := args[0], args[1:]

	db, err := storage.OpenDB(*dir)
	if err != nil {
		fatal("open db %q: %v", *dir, err)
	}
	defer db.Close()

	switch cmd {
	case "list-tables":
		runListTables(db)
	case "describe":
		runDescribe(db, rest)
	case "create-table":
		runCreateTable(db, rest)
	case "drop-table":
		runDropTable(db, rest)
	case "insert":
		runInsert(db, rest)
	case "get":
		runGet(db, rest)
	case "update":
		runUpdate(db, rest)
	case "delete":
		runDelete(db, rest)
	case "scan":
		runScan(db, rest)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

// ---- list-tables ------------------------------------------------------------

func runListTables(db *storage.DB) {
	for _, name := range db.TableNames() {
		fmt.Println(name)
	}
}

// ---- describe ---------------------------------------------------------------

func runDescribe(db *storage.DB, args []string) {
	if len(args) != 1 {
		fatal("describe: usage: describe <table>")
	}
	tbl, err := db.OpenTable(args[0])
	if err != nil {
		fatal("describe: %v", err)
	}
	schema := tbl.Schema()
	fmt.Printf("%-20s  %-16s  %s\n", "Column", "Type", "Nullable")
	fmt.Printf("%s  %s  %s\n",
		strings.Repeat("-", 20), strings.Repeat("-", 16), strings.Repeat("-", 8))
	for i := 0; i < schema.NumColumns(); i++ {
		col := schema.Column(i)
		fmt.Printf("%-20s  %-16s  %v\n", col.Name, cliutil.FormatType(col), col.Nullable)
	}
}

// ---- create-table -----------------------------------------------------------

func runCreateTable(db *storage.DB, args []string) {
	if len(args) < 2 {
		fatal("create-table: usage: create-table <table> <col:type> [<col:type> ...]")
	}
	name, specs := args[0], args[1:]
	cols := make([]storage.Column, len(specs))
	for i, spec := range specs {
		col, err := cliutil.ParseColSpec(spec)
		if err != nil {
			fatal("create-table: %v", err)
		}
		cols[i] = col
	}
	schema, err := storage.NewSchema(cols)
	if err != nil {
		fatal("create-table: invalid schema: %v", err)
	}
	if _, err := db.CreateTable(name, schema); err != nil {
		fatal("create-table: %v", err)
	}
	fmt.Printf("table %q created\n", name)
}

// ---- drop-table -------------------------------------------------------------

func runDropTable(db *storage.DB, args []string) {
	if len(args) != 1 {
		fatal("drop-table: usage: drop-table <table>")
	}
	if err := db.DropTable(args[0]); err != nil {
		fatal("drop-table: %v", err)
	}
	fmt.Printf("table %q dropped\n", args[0])
}

// ---- insert -----------------------------------------------------------------

func runInsert(db *storage.DB, args []string) {
	if len(args) < 1 {
		fatal("insert: usage: insert <table> <val> [<val> ...]")
	}
	tbl, err := db.OpenTable(args[0])
	if err != nil {
		fatal("insert: %v", err)
	}
	row, err := cliutil.ParseValues(tbl.Schema(), args[1:])
	if err != nil {
		fatal("insert: %v", err)
	}
	rid, err := tbl.Insert(row)
	if err != nil {
		fatal("insert: %v", err)
	}
	fmt.Println("inserted", rid)
}

// ---- get --------------------------------------------------------------------

func runGet(db *storage.DB, args []string) {
	if len(args) != 2 {
		fatal("get: usage: get <table> <pageID:slotID>")
	}
	tbl, err := db.OpenTable(args[0])
	if err != nil {
		fatal("get: %v", err)
	}
	rid, err := cliutil.ParseRID(args[1])
	if err != nil {
		fatal("get: %v", err)
	}
	row, ok, err := tbl.Get(rid)
	if err != nil {
		fatal("get: %v", err)
	}
	if !ok {
		fmt.Println("not found")
		return
	}
	fmt.Printf("%s  %s\n", rid, cliutil.FormatRow(tbl.Schema(), row))
}

// ---- update -----------------------------------------------------------------

func runUpdate(db *storage.DB, args []string) {
	if len(args) < 2 {
		fatal("update: usage: update <table> <pageID:slotID> <val> [<val> ...]")
	}
	tbl, err := db.OpenTable(args[0])
	if err != nil {
		fatal("update: %v", err)
	}
	rid, err := cliutil.ParseRID(args[1])
	if err != nil {
		fatal("update: %v", err)
	}
	row, err := cliutil.ParseValues(tbl.Schema(), args[2:])
	if err != nil {
		fatal("update: %v", err)
	}
	newRID, ok, err := tbl.Update(rid, row)
	if err != nil {
		fatal("update: %v", err)
	}
	if !ok {
		fmt.Println("not found")
		return
	}
	fmt.Println("updated", newRID)
}

// ---- delete -----------------------------------------------------------------

func runDelete(db *storage.DB, args []string) {
	if len(args) != 2 {
		fatal("delete: usage: delete <table> <pageID:slotID>")
	}
	tbl, err := db.OpenTable(args[0])
	if err != nil {
		fatal("delete: %v", err)
	}
	rid, err := cliutil.ParseRID(args[1])
	if err != nil {
		fatal("delete: %v", err)
	}
	ok, err := tbl.Delete(rid)
	if err != nil {
		fatal("delete: %v", err)
	}
	if !ok {
		fmt.Println("not found")
		return
	}
	fmt.Println("deleted")
}

// ---- scan -------------------------------------------------------------------

func runScan(db *storage.DB, args []string) {
	if len(args) != 1 {
		fatal("scan: usage: scan <table>")
	}
	tbl, err := db.OpenTable(args[0])
	if err != nil {
		fatal("scan: %v", err)
	}
	s := tbl.Scan()
	for s.Next() {
		fmt.Printf("%s  %s\n", s.RID(), cliutil.FormatRow(tbl.Schema(), s.Row()))
	}
	if err := s.Err(); err != nil {
		fatal("scan: %v", err)
	}
}
