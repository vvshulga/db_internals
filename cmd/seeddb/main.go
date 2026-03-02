package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/vvshulga/db_internals/parser"
	"github.com/vvshulga/db_internals/storage"
)

// stripComments removes SQL comments (-- style) from the input
// This is needed because the parser doesn't support comments
func stripComments(sql string) string {
	lines := strings.Split(sql, "\n")
	var cleaned []string

	for _, line := range lines {
		// Find comment marker
		if idx := strings.Index(line, "--"); idx >= 0 {
			// Keep everything before the comment
			line = line[:idx]
		}
		// Keep line if it's not empty after trimming
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, line)
		}
	}

	return strings.Join(cleaned, "\n")
}

func main() {
	// Parse command-line flags
	dbDir := flag.String("db", "./data", "Database directory")
	sqlFile := flag.String("sql", "seed.sql", "SQL file to execute")
	flag.Parse()

	// Open database
	db, err := storage.OpenDB(*dbDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Read SQL file
	content, err := os.ReadFile(*sqlFile)
	if err != nil {
		log.Fatalf("Failed to read SQL file: %v", err)
	}

	// Strip comments (parser doesn't support -- style comments)
	cleanedSQL := stripComments(string(content))

	// Parse SQL
	stmts, err := parser.ParseString(cleanedSQL)
	if err != nil {
		log.Fatalf("Failed to parse SQL: %v", err)
	}

	log.Printf("Parsed %d statements from %s", len(stmts), *sqlFile)

	// Execute each statement
	for i, stmt := range stmts {
		if err := executeStmt(db, stmt); err != nil {
			log.Fatalf("Statement %d failed: %v", i+1, err)
		}
	}

	log.Printf("Successfully executed %d statements", len(stmts))
}

// executeStmt dispatches to the appropriate execution function
func executeStmt(db *storage.DB, stmt parser.AstNode) error {
	switch s := stmt.(type) {
	case *parser.CreateTableStmt:
		return executeCreateTable(db, s)
	case *parser.InsertStmt:
		return executeInsert(db, s)
	case *parser.CreateIndexStmt:
		return executeCreateIndex(db, s)
	case *parser.SelectStmt:
		return fmt.Errorf("SELECT not supported in seeddb (read-only tool)")
	case *parser.UpdateStmt:
		return fmt.Errorf("UPDATE not supported in seeddb")
	case *parser.DeleteStmt:
		return fmt.Errorf("DELETE not supported in seeddb")
	default:
		return fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

// executeCreateTable executes a CREATE TABLE statement
func executeCreateTable(db *storage.DB, stmt *parser.CreateTableStmt) error {
	// Convert parser.ColumnDef to storage.Column
	cols := make([]storage.Column, len(stmt.Columns))
	for i, cd := range stmt.Columns {
		col, err := parseColumnDef(cd)
		if err != nil {
			return fmt.Errorf("column %q: %w", cd.Name, err)
		}
		cols[i] = col
	}

	// Create schema
	schema, err := storage.NewSchema(cols)
	if err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	// Create table
	_, err = db.CreateTable(stmt.TableName, schema)
	if err != nil {
		return fmt.Errorf("create table %q: %w", stmt.TableName, err)
	}

	log.Printf("Created table: %s (%d columns)", stmt.TableName, len(cols))
	return nil
}

// parseColumnDef parses a parser.ColumnDef into a storage.Column
// Handles type strings like "INT", "VARCHAR(64)", "TEXT?"
func parseColumnDef(cd parser.ColumnDef) (storage.Column, error) {
	// Use parsed fields from AST
	var dataType storage.DataType
	var maxLen uint16

	switch cd.Type {
	case "INT":
		dataType = storage.TypeINT
	case "BIGINT":
		dataType = storage.TypeBIGINT
	case "FLOAT":
		dataType = storage.TypeFLOAT
	case "DOUBLE":
		dataType = storage.TypeDOUBLE
	case "BOOLEAN":
		dataType = storage.TypeBOOLEAN
	case "DATETIME":
		dataType = storage.TypeDATETIME
	case "VARCHAR":
		if cd.TypeLen == nil {
			return storage.Column{}, fmt.Errorf("VARCHAR requires length parameter")
		}
		dataType = storage.TypeVARCHAR
		maxLen = *cd.TypeLen
	case "TEXT":
		dataType = storage.TypeTEXT
	default:
		return storage.Column{}, fmt.Errorf("unsupported type: %s", cd.Type)
	}

	return storage.Column{
		Name:     cd.Name,
		Type:     dataType,
		MaxLen:   maxLen,
		Nullable: cd.Nullable,
	}, nil
}

// executeCreateIndex creates an index on a table column.
func executeCreateIndex(db *storage.DB, stmt *parser.CreateIndexStmt) error {
	tbl, err := db.OpenTable(stmt.TableName)
	if err != nil {
		return fmt.Errorf("open table %q: %w", stmt.TableName, err)
	}
	if err := tbl.CreateIndex(stmt.ColumnName, stmt.Unique); err != nil {
		return fmt.Errorf("create index on %s.%s: %w", stmt.TableName, stmt.ColumnName, err)
	}
	log.Printf("Created index on %s.%s (unique=%v)", stmt.TableName, stmt.ColumnName, stmt.Unique)
	return nil
}

// executeInsert executes an INSERT statement
func executeInsert(db *storage.DB, stmt *parser.InsertStmt) error {
	// Open table
	table, err := db.OpenTable(stmt.TableName)
	if err != nil {
		return fmt.Errorf("open table %q: %w", stmt.TableName, err)
	}

	// Get schema
	schema := table.Schema()

	// Convert Expr values to storage.Row
	row, err := convertValuesToRow(schema, stmt.Values)
	if err != nil {
		return fmt.Errorf("convert values: %w", err)
	}

	// Insert row
	log.Printf("DEBUG: About to insert row: %v", row)
	for i, val := range row {
		log.Printf("DEBUG:   col %d: kind=%v, string=%q", i, val.Kind(), val.String())
	}
	rid, err := table.Insert(row)
	if err != nil {
		return fmt.Errorf("insert row: %w", err)
	}

	// Flush to ensure data is persisted
	if err := table.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}

	// Verify the data was inserted correctly
	readRow, ok, err := table.Get(rid)
	if err != nil {
		return fmt.Errorf("verify get: %w", err)
	}
	if !ok {
		return fmt.Errorf("verify: row not found after insert")
	}
	log.Printf("DEBUG: Read back row: %v", readRow)
	for i, val := range readRow {
		log.Printf("DEBUG:   col %d: kind=%v, string=%q", i, val.Kind(), val.String())
	}

	log.Printf("Inserted row into %s: RID=%s", stmt.TableName, rid)
	return nil
}

// convertValuesToRow converts an array of Expr to a storage.Row
func convertValuesToRow(schema *storage.Schema, exprs []parser.Expr) (storage.Row, error) {
	if len(exprs) != schema.NumColumns() {
		return nil, fmt.Errorf("column count mismatch: got %d values, expected %d", len(exprs), schema.NumColumns())
	}

	row := make(storage.Row, schema.NumColumns())

	for i, expr := range exprs {
		col := schema.Column(i)
		val, err := exprToValue(col, expr)
		if err != nil {
			return nil, fmt.Errorf("column %d (%s): %w", i, col.Name, err)
		}
		row[i] = val
	}

	return row, nil
}

// exprToValue converts a parser.Expr to a storage.Value based on the target column type
func exprToValue(col storage.Column, expr parser.Expr) (storage.Value, error) {
	// Handle NULL (parser uses nil or special marker)
	if expr == nil {
		if !col.Nullable {
			return storage.Value{}, fmt.Errorf("NULL not allowed")
		}
		return storage.NewNullValue(), nil
	}

	switch e := expr.(type) {
	case *parser.LiteralInt:
		// Convert based on target column type
		switch col.Type {
		case storage.TypeINT:
			if e.Value > 2147483647 {
				return storage.Value{}, fmt.Errorf("value %d out of range for INT (max 2147483647)", e.Value)
			}
			return storage.NewIntValue(int32(e.Value)), nil

		case storage.TypeBIGINT:
			return storage.NewBigIntValue(int64(e.Value)), nil

		case storage.TypeFLOAT:
			// Parser limitation: treat integer as float
			// For prices in cents, divide by 100: 2999 → 29.99
			return storage.NewFloatValue(float32(e.Value) / 100.0), nil

		case storage.TypeDOUBLE:
			// Parser limitation: treat integer as double
			// For prices in cents, divide by 100: 2999 → 29.99
			return storage.NewDoubleValue(float64(e.Value) / 100.0), nil

		default:
			return storage.Value{}, fmt.Errorf("cannot assign int literal to %v", col.Type)
		}

	case *parser.LiteralString:
		log.Printf("DEBUG: Converting string %q to column type %v", e.Value, col.Type)
		switch col.Type {
		case storage.TypeVARCHAR:
			if len(e.Value) > int(col.MaxLen) {
				return storage.Value{}, fmt.Errorf("string too long (max %d): %q", col.MaxLen, e.Value)
			}
			return storage.NewVarcharValue(e.Value), nil

		case storage.TypeTEXT:
			if len(e.Value) > 65535 {
				return storage.Value{}, fmt.Errorf("TEXT too long (max 65535 bytes)")
			}
			log.Printf("DEBUG: Creating TEXT value from %q", e.Value)
			val := storage.NewTextValue(e.Value)
			log.Printf("DEBUG: TEXT value created: kind=%v, String()=%q, AsString()=%q", val.Kind(), val.String(), val.AsString())
			return val, nil

		case storage.TypeDATETIME:
			// Parse RFC3339 datetime
			t, err := time.Parse(time.RFC3339, e.Value)
			if err != nil {
				return storage.Value{}, fmt.Errorf("invalid datetime (expected RFC3339): %w", err)
			}
			return storage.NewDatetimeValue(t.UnixNano()), nil

		default:
			return storage.Value{}, fmt.Errorf("cannot assign string literal to %v", col.Type)
		}

	case *parser.ColumnRef:
		// Special handling for boolean: "true" / "false"
		if col.Type == storage.TypeBOOLEAN {
			switch e.Name {
			case "true":
				return storage.NewBooleanValue(true), nil
			case "false":
				return storage.NewBooleanValue(false), nil
			default:
				return storage.Value{}, fmt.Errorf("invalid boolean: %q (expected 'true' or 'false')", e.Name)
			}
		}

		// Column references not supported in VALUES clause
		return storage.Value{}, fmt.Errorf("column references not supported in VALUES: %q", e.Name)

	default:
		return storage.Value{}, fmt.Errorf("unsupported expression type: %T", expr)
	}
}
