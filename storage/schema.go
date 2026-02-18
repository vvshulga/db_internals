package storage

import "fmt"

// varDirEntrySize is the number of bytes occupied by one entry in the variable-
// length column directory within a serialized record. Each entry is 12 bytes:
//
//	Inline:   [uint32 flag=0][uint16 Offset][uint16 Length][uint32 padding]
//	Overflow: [uint32 flag=1][uint64 FirstOverflowPageID]
const varDirEntrySize = 12

// ---- DataType ----------------------------------------------------------------

// DataType identifies the SQL type of a column.
type DataType uint8

const (
	TypeINT      DataType = 1 // int32,  4 bytes, little-endian
	TypeBIGINT   DataType = 2 // int64,  8 bytes, little-endian
	TypeFLOAT    DataType = 3 // float32, 4 bytes, IEEE 754 bits LE
	TypeDOUBLE   DataType = 4 // float64, 8 bytes, IEEE 754 bits LE
	TypeBOOLEAN  DataType = 5 // uint8,  1 byte, 0x00=false 0x01=true
	TypeDATETIME DataType = 6 // int64 Unix nanoseconds, 8 bytes LE
	TypeVARCHAR  DataType = 7 // variable-length, up to Column.MaxLen bytes
	TypeTEXT     DataType = 8 // variable-length, up to 65535 bytes
)

// IsFixedLength reports whether the type occupies a known constant byte width.
func (dt DataType) IsFixedLength() bool {
	switch dt {
	case TypeINT, TypeBIGINT, TypeFLOAT, TypeDOUBLE, TypeBOOLEAN, TypeDATETIME:
		return true
	}
	return false
}

// FixedSize returns the byte width for fixed-length types, and 0 for variable-length.
func (dt DataType) FixedSize() int {
	switch dt {
	case TypeINT:
		return 4
	case TypeBIGINT:
		return 8
	case TypeFLOAT:
		return 4
	case TypeDOUBLE:
		return 8
	case TypeBOOLEAN:
		return 1
	case TypeDATETIME:
		return 8
	}
	return 0
}

// String returns the SQL type name.
func (dt DataType) String() string {
	switch dt {
	case TypeINT:
		return "INT"
	case TypeBIGINT:
		return "BIGINT"
	case TypeFLOAT:
		return "FLOAT"
	case TypeDOUBLE:
		return "DOUBLE"
	case TypeBOOLEAN:
		return "BOOLEAN"
	case TypeDATETIME:
		return "DATETIME"
	case TypeVARCHAR:
		return "VARCHAR"
	case TypeTEXT:
		return "TEXT"
	}
	return fmt.Sprintf("DataType(%d)", uint8(dt))
}

// ---- Column ------------------------------------------------------------------

// Column describes a single column in a table schema.
type Column struct {
	Name     string
	Type     DataType
	MaxLen   uint16 // meaningful only for TypeVARCHAR; must be > 0
	Nullable bool
}

// ---- Schema ------------------------------------------------------------------

// schemaLayout holds pre-computed byte-offset information derived from a
// column list. It is built once in NewSchema and never mutated.
type schemaLayout struct {
	nullBitmapSize int   // ceil(numCols / 8)
	fixedSize      int   // total bytes of the fixed-length column region
	fixedOffsets   []int // fixedOffsets[i] = byte offset within fixed region; -1 if var-length
	varIndices     []int // indices of var-length columns in column order
	varDirOffset   int   // byte offset to start of var directory within a record
	varDataOffset  int   // byte offset to start of var data blob within a record
}

// Schema describes the complete column set of a table and pre-computes the
// binary layout used by Encode and Decode.
type Schema struct {
	columns []Column
	layout  schemaLayout
}

// NewSchema creates a Schema from the given columns, validates column
// definitions, and pre-computes the binary layout. Returns ErrInvalidSchema
// if any column is malformed or a name is duplicated.
func NewSchema(columns []Column) (*Schema, error) {
	if len(columns) == 0 {
		return nil, &ErrInvalidSchema{Reason: "schema must have at least one column"}
	}
	names := make(map[string]struct{}, len(columns))
	for i, col := range columns {
		if col.Name == "" {
			return nil, &ErrInvalidSchema{Reason: fmt.Sprintf("column %d has empty name", i)}
		}
		if _, dup := names[col.Name]; dup {
			return nil, &ErrInvalidSchema{Reason: fmt.Sprintf("duplicate column name %q", col.Name)}
		}
		names[col.Name] = struct{}{}
		if col.Type == TypeVARCHAR && col.MaxLen == 0 {
			return nil, &ErrInvalidSchema{
				Reason: fmt.Sprintf("column %q (VARCHAR) has MaxLen 0; must be > 0", col.Name),
			}
		}
		if col.Type != TypeVARCHAR && col.Type != TypeTEXT &&
			col.Type != TypeINT && col.Type != TypeBIGINT &&
			col.Type != TypeFLOAT && col.Type != TypeDOUBLE &&
			col.Type != TypeBOOLEAN && col.Type != TypeDATETIME {
			return nil, &ErrInvalidSchema{
				Reason: fmt.Sprintf("column %q has unknown type %d", col.Name, col.Type),
			}
		}
	}

	s := &Schema{columns: columns}
	s.layout = computeLayout(columns)
	return s, nil
}

func computeLayout(columns []Column) schemaLayout {
	n := len(columns)
	layout := schemaLayout{
		nullBitmapSize: (n + 7) / 8,
		fixedOffsets:   make([]int, n),
	}
	fixedCursor := 0
	for i, col := range columns {
		if col.Type.IsFixedLength() {
			layout.fixedOffsets[i] = fixedCursor
			fixedCursor += col.Type.FixedSize()
		} else {
			layout.fixedOffsets[i] = -1
			layout.varIndices = append(layout.varIndices, i)
		}
	}
	layout.fixedSize = fixedCursor
	layout.varDirOffset = layout.nullBitmapSize + layout.fixedSize
	layout.varDataOffset = layout.varDirOffset + varDirEntrySize*len(layout.varIndices)
	return layout
}

// NumColumns returns the number of columns in the schema.
func (s *Schema) NumColumns() int { return len(s.columns) }

// Column returns the column definition at index i. Panics if out of range.
func (s *Schema) Column(i int) Column { return s.columns[i] }

// ColumnIndex returns the index of the first column with the given name.
// Returns -1, false if no such column exists.
func (s *Schema) ColumnIndex(name string) (int, bool) {
	for i, col := range s.columns {
		if col.Name == name {
			return i, true
		}
	}
	return -1, false
}

// NullBitmapSize returns ceil(NumColumns() / 8).
func (s *Schema) NullBitmapSize() int { return s.layout.nullBitmapSize }

// FixedRegionSize returns the total byte size of the fixed-length column region.
func (s *Schema) FixedRegionSize() int { return s.layout.fixedSize }

// FixedOffset returns the byte offset of column i within the fixed region.
// Panics if i is out of range or if column i is variable-length.
func (s *Schema) FixedOffset(i int) int {
	off := s.layout.fixedOffsets[i]
	if off == -1 {
		panic(fmt.Sprintf("storage: column %d (%s) is variable-length", i, s.columns[i].Name))
	}
	return off
}

// VarDirOffset returns the byte offset of the variable-length directory within
// a serialized record.
func (s *Schema) VarDirOffset() int { return s.layout.varDirOffset }

// VarDataOffset returns the byte offset of the variable-length data blob within
// a serialized record (before any variable data).
func (s *Schema) VarDataOffset() int { return s.layout.varDataOffset }

// MinRecordSize returns the minimum serialized record size: null bitmap +
// fixed region + var directory (with zero variable data).
func (s *Schema) MinRecordSize() int { return s.layout.varDataOffset }
