// Package cliutil provides shared helpers for parsing command-line arguments
// and formatting storage rows for human-readable output. It is used by both
// the storage_cli command and the dbserver/dbctl daemon tools.
package cliutil

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vvshulga/db_internals/storage"
)

// ParseColSpec parses a column spec like "id:int", "name:varchar(64)", "score:double?".
// Append ? to the type to mark the column nullable.
func ParseColSpec(spec string) (storage.Column, error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return storage.Column{}, fmt.Errorf("invalid column spec %q: want name:type or name:type?", spec)
	}
	name := parts[0]
	typeSpec := parts[1]

	nullable := false
	if strings.HasSuffix(typeSpec, "?") {
		nullable = true
		typeSpec = typeSpec[:len(typeSpec)-1]
	}

	col := storage.Column{Name: name, Nullable: nullable}
	lower := strings.ToLower(typeSpec)

	switch {
	case lower == "int":
		col.Type = storage.TypeINT
	case lower == "bigint":
		col.Type = storage.TypeBIGINT
	case lower == "float":
		col.Type = storage.TypeFLOAT
	case lower == "double":
		col.Type = storage.TypeDOUBLE
	case lower == "boolean" || lower == "bool":
		col.Type = storage.TypeBOOLEAN
	case lower == "datetime":
		col.Type = storage.TypeDATETIME
	case lower == "text":
		col.Type = storage.TypeTEXT
	case strings.HasPrefix(lower, "varchar(") && strings.HasSuffix(lower, ")"):
		inner := lower[len("varchar(") : len(lower)-1]
		n, err := strconv.Atoi(inner)
		if err != nil || n <= 0 || n > 65535 {
			return storage.Column{}, fmt.Errorf("invalid varchar length in %q", spec)
		}
		col.Type = storage.TypeVARCHAR
		col.MaxLen = uint16(n)
	default:
		return storage.Column{}, fmt.Errorf("unknown type %q in %q (valid: int bigint float double boolean datetime varchar(N) text)", typeSpec, spec)
	}
	return col, nil
}

// ParseRID parses "pageID:slotID" into a storage.RID.
func ParseRID(s string) (storage.RID, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return storage.RID{}, fmt.Errorf("invalid RID %q: want pageID:slotID", s)
	}
	pageID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return storage.RID{}, fmt.Errorf("invalid RID %q: bad pageID: %v", s, err)
	}
	slotID, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return storage.RID{}, fmt.Errorf("invalid RID %q: bad slotID: %v", s, err)
	}
	return storage.RID{PageID: pageID, SlotID: storage.SlotID(slotID)}, nil
}

// ParseValues converts positional string arguments into a Row according to the
// column types defined in schema.
func ParseValues(schema *storage.Schema, args []string) (storage.Row, error) {
	if len(args) != schema.NumColumns() {
		return nil, fmt.Errorf("got %d values, schema has %d columns", len(args), schema.NumColumns())
	}
	row := make(storage.Row, schema.NumColumns())
	for i := 0; i < schema.NumColumns(); i++ {
		v, err := parseValue(schema.Column(i), args[i])
		if err != nil {
			return nil, fmt.Errorf("column %d (%s): %v", i, schema.Column(i).Name, err)
		}
		row[i] = v
	}
	return row, nil
}

func parseValue(col storage.Column, s string) (storage.Value, error) {
	if strings.EqualFold(s, "NULL") {
		if !col.Nullable {
			return storage.Value{}, fmt.Errorf("column is not nullable")
		}
		return storage.NewNullValue(), nil
	}
	switch col.Type {
	case storage.TypeINT:
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return storage.Value{}, fmt.Errorf("expected INT, got %q: %v", s, err)
		}
		return storage.NewIntValue(int32(n)), nil
	case storage.TypeBIGINT:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return storage.Value{}, fmt.Errorf("expected BIGINT, got %q: %v", s, err)
		}
		return storage.NewBigIntValue(n), nil
	case storage.TypeFLOAT:
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return storage.Value{}, fmt.Errorf("expected FLOAT, got %q: %v", s, err)
		}
		return storage.NewFloatValue(float32(f)), nil
	case storage.TypeDOUBLE:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return storage.Value{}, fmt.Errorf("expected DOUBLE, got %q: %v", s, err)
		}
		return storage.NewDoubleValue(f), nil
	case storage.TypeBOOLEAN:
		switch strings.ToLower(s) {
		case "true", "1":
			return storage.NewBooleanValue(true), nil
		case "false", "0":
			return storage.NewBooleanValue(false), nil
		default:
			return storage.Value{}, fmt.Errorf("expected BOOLEAN (true/false/1/0), got %q", s)
		}
	case storage.TypeDATETIME:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return storage.Value{}, fmt.Errorf("expected DATETIME (Unix nanoseconds int64), got %q: %v", s, err)
		}
		return storage.NewDatetimeValue(n), nil
	case storage.TypeVARCHAR:
		return storage.NewVarcharValue(s), nil
	case storage.TypeTEXT:
		return storage.NewTextValue(s), nil
	default:
		return storage.Value{}, fmt.Errorf("unsupported type %v", col.Type)
	}
}

// FormatRow returns "col=val  col=val  ..." for a row.
func FormatRow(schema *storage.Schema, row storage.Row) string {
	parts := make([]string, schema.NumColumns())
	for i := 0; i < schema.NumColumns(); i++ {
		parts[i] = schema.Column(i).Name + "=" + row[i].String()
	}
	return strings.Join(parts, "  ")
}

// FormatType returns the display string for a column type, e.g. "VARCHAR(64)".
func FormatType(col storage.Column) string {
	if col.Type == storage.TypeVARCHAR {
		return fmt.Sprintf("VARCHAR(%d)", col.MaxLen)
	}
	return col.Type.String()
}
