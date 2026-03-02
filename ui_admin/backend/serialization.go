package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vvshulga/db_internals/storage"
)

// SchemaToJSON converts storage.Schema to JSON-friendly TableSchema (no index info).
func SchemaToJSON(name string, schema *storage.Schema) TableSchema {
	cols := make([]Column, schema.NumColumns())
	for i := 0; i < schema.NumColumns(); i++ {
		c := schema.Column(i)
		cols[i] = Column{
			Name:     c.Name,
			Type:     formatColumnType(c),
			Nullable: c.Nullable,
		}
	}
	return TableSchema{Name: name, Columns: cols}
}

// SchemaToJSONWithIndexes converts a TableHandle schema to TableSchema,
// populating Indexed and IndexUnique for every column that has an index.
func SchemaToJSONWithIndexes(tbl *storage.TableHandle) TableSchema {
	schema := tbl.Schema()
	name := tbl.Name()

	// Build a fast lookup: colName → IndexMeta
	infos := tbl.IndexInfos()
	idxMap := make(map[string]storage.IndexMeta, len(infos))
	for _, m := range infos {
		idxMap[m.Column] = m
	}

	cols := make([]Column, schema.NumColumns())
	for i := 0; i < schema.NumColumns(); i++ {
		c := schema.Column(i)
		col := Column{
			Name:     c.Name,
			Type:     formatColumnType(c),
			Nullable: c.Nullable,
		}
		if m, ok := idxMap[c.Name]; ok {
			col.Indexed = true
			col.IndexUnique = m.Unique
		}
		cols[i] = col
	}
	return TableSchema{Name: name, Columns: cols}
}

// formatColumnType returns user-friendly type name
func formatColumnType(col storage.Column) string {
	switch col.Type {
	case storage.TypeINT:
		return "INT"
	case storage.TypeBIGINT:
		return "BIGINT"
	case storage.TypeFLOAT:
		return "FLOAT"
	case storage.TypeDOUBLE:
		return "DOUBLE"
	case storage.TypeBOOLEAN:
		return "BOOLEAN"
	case storage.TypeDATETIME:
		return "DATETIME"
	case storage.TypeVARCHAR:
		return fmt.Sprintf("VARCHAR(%d)", col.MaxLen)
	case storage.TypeTEXT:
		return "TEXT"
	default:
		return "UNKNOWN"
	}
}

// RowToJSON converts storage.Row to JSON map
func RowToJSON(schema *storage.Schema, row storage.Row) map[string]interface{} {
	result := make(map[string]interface{})
	for i := 0; i < len(row); i++ {
		col := schema.Column(i)
		result[col.Name] = valueToJSON(row[i])
	}
	return result
}

// valueToJSON converts storage.Value to JSON-compatible type
func valueToJSON(val storage.Value) interface{} {
	if val.IsNull() {
		return nil
	}
	switch val.Kind() {
	case storage.KindInt:
		return val.AsInt()
	case storage.KindBigInt:
		return val.AsBigInt()
	case storage.KindFloat:
		return val.AsFloat()
	case storage.KindDouble:
		return val.AsDouble()
	case storage.KindBoolean:
		return val.AsBoolean()
	case storage.KindVarchar, storage.KindText:
		return val.AsString()
	case storage.KindDatetime:
		return time.Unix(0, val.AsDatetime()).Format(time.RFC3339)
	default:
		return nil
	}
}

// JSONToRow converts JSON map to storage.Row
func JSONToRow(schema *storage.Schema, values map[string]interface{}) (storage.Row, error) {
	row := make(storage.Row, schema.NumColumns())
	for i := 0; i < schema.NumColumns(); i++ {
		col := schema.Column(i)
		val, ok := values[col.Name]
		if !ok {
			if !col.Nullable {
				return nil, fmt.Errorf("missing required column %q", col.Name)
			}
			row[i] = storage.NewNullValue()
			continue
		}
		storageVal, err := jsonToValue(col, val)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", col.Name, err)
		}
		row[i] = storageVal
	}
	return row, nil
}

// jsonToValue converts JSON value to storage.Value based on column type
func jsonToValue(col storage.Column, val interface{}) (storage.Value, error) {
	if val == nil {
		if !col.Nullable {
			return storage.Value{}, fmt.Errorf("NULL not allowed")
		}
		return storage.NewNullValue(), nil
	}

	switch col.Type {
	case storage.TypeINT:
		// Handle both float64 (JSON number) and string input
		switch v := val.(type) {
		case float64:
			return storage.NewIntValue(int32(v)), nil
		case string:
			i, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return storage.Value{}, fmt.Errorf("invalid INT value %q: %w", v, err)
			}
			return storage.NewIntValue(int32(i)), nil
		default:
			return storage.Value{}, fmt.Errorf("expected number or string, got %T", val)
		}

	case storage.TypeBIGINT:
		switch v := val.(type) {
		case float64:
			return storage.NewBigIntValue(int64(v)), nil
		case string:
			i, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return storage.Value{}, fmt.Errorf("invalid BIGINT value %q: %w", v, err)
			}
			return storage.NewBigIntValue(i), nil
		default:
			return storage.Value{}, fmt.Errorf("expected number or string, got %T", val)
		}

	case storage.TypeFLOAT:
		switch v := val.(type) {
		case float64:
			return storage.NewFloatValue(float32(v)), nil
		case string:
			f, err := strconv.ParseFloat(v, 32)
			if err != nil {
				return storage.Value{}, fmt.Errorf("invalid FLOAT value %q: %w", v, err)
			}
			return storage.NewFloatValue(float32(f)), nil
		default:
			return storage.Value{}, fmt.Errorf("expected number or string, got %T", val)
		}

	case storage.TypeDOUBLE:
		switch v := val.(type) {
		case float64:
			return storage.NewDoubleValue(v), nil
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return storage.Value{}, fmt.Errorf("invalid DOUBLE value %q: %w", v, err)
			}
			return storage.NewDoubleValue(f), nil
		default:
			return storage.Value{}, fmt.Errorf("expected number or string, got %T", val)
		}

	case storage.TypeBOOLEAN:
		switch v := val.(type) {
		case bool:
			return storage.NewBooleanValue(v), nil
		case string:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return storage.Value{}, fmt.Errorf("invalid BOOLEAN value %q: %w", v, err)
			}
			return storage.NewBooleanValue(b), nil
		default:
			return storage.Value{}, fmt.Errorf("expected boolean or string, got %T", val)
		}

	case storage.TypeVARCHAR:
		s, ok := val.(string)
		if !ok {
			return storage.Value{}, fmt.Errorf("expected string, got %T", val)
		}
		if len(s) > int(col.MaxLen) {
			return storage.Value{}, fmt.Errorf("string too long (max %d, got %d)", col.MaxLen, len(s))
		}
		return storage.NewVarcharValue(s), nil

	case storage.TypeTEXT:
		s, ok := val.(string)
		if !ok {
			return storage.Value{}, fmt.Errorf("expected string, got %T", val)
		}
		return storage.NewTextValue(s), nil

	case storage.TypeDATETIME:
		s, ok := val.(string)
		if !ok {
			return storage.Value{}, fmt.Errorf("expected string (RFC3339 datetime), got %T", val)
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return storage.Value{}, fmt.Errorf("invalid DATETIME value %q: %w", s, err)
		}
		return storage.NewDatetimeValue(t.UnixNano()), nil

	default:
		return storage.Value{}, fmt.Errorf("unsupported type %v", col.Type)
	}
}

// parseColumnType converts "INT", "VARCHAR(64)" etc. to storage.ColumnDef
func parseColumnType(typeStr string) (storage.DataType, uint16, error) {
	typeStr = strings.ToUpper(strings.TrimSpace(typeStr))

	// Handle VARCHAR(N) and TEXT
	if strings.HasPrefix(typeStr, "VARCHAR(") {
		re := regexp.MustCompile(`^VARCHAR\((\d+)\)$`)
		matches := re.FindStringSubmatch(typeStr)
		if matches == nil {
			return 0, 0, fmt.Errorf("invalid VARCHAR format, expected VARCHAR(N)")
		}
		maxLen, err := strconv.ParseUint(matches[1], 10, 16)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid VARCHAR length: %w", err)
		}
		return storage.TypeVARCHAR, uint16(maxLen), nil
	}

	// Handle simple types
	switch typeStr {
	case "INT":
		return storage.TypeINT, 0, nil
	case "BIGINT":
		return storage.TypeBIGINT, 0, nil
	case "FLOAT":
		return storage.TypeFLOAT, 0, nil
	case "DOUBLE":
		return storage.TypeDOUBLE, 0, nil
	case "BOOLEAN":
		return storage.TypeBOOLEAN, 0, nil
	case "DATETIME":
		return storage.TypeDATETIME, 0, nil
	case "TEXT":
		return storage.TypeTEXT, 0, nil
	default:
		return 0, 0, fmt.Errorf("unsupported type %q", typeStr)
	}
}
