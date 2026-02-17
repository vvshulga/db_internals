package storage

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ---- ValueKind ---------------------------------------------------------------

// ValueKind identifies the Go representation of a Value.
type ValueKind uint8

const (
	KindNull     ValueKind = 0
	KindInt      ValueKind = 1
	KindBigInt   ValueKind = 2
	KindFloat    ValueKind = 3
	KindDouble   ValueKind = 4
	KindBoolean  ValueKind = 5
	KindDatetime ValueKind = 6
	KindVarchar  ValueKind = 7
	KindText     ValueKind = 8
)

// ---- Value -------------------------------------------------------------------

// Value is a tagged union that holds a single column value of any supported type.
//
// Fixed-width types (INT, BIGINT, FLOAT, DOUBLE, BOOLEAN, DATETIME) are stored
// in the numeric field — no heap allocation. String types (VARCHAR, TEXT) use
// strVal. The zero Value is a NULL.
//
// Value is comparable with == because both uint64 and string are comparable in Go.
type Value struct {
	kind    ValueKind
	numeric uint64 // holds the bit pattern of int32/int64/float32/float64/bool/datetime
	strVal  string // holds VARCHAR and TEXT content
}

// -- Constructors --

func NewNullValue() Value                  { return Value{kind: KindNull} }
func NewIntValue(v int32) Value            { return Value{kind: KindInt, numeric: uint64(uint32(v))} }
func NewBigIntValue(v int64) Value         { return Value{kind: KindBigInt, numeric: uint64(v)} }
func NewFloatValue(v float32) Value        { return Value{kind: KindFloat, numeric: uint64(math.Float32bits(v))} }
func NewDoubleValue(v float64) Value       { return Value{kind: KindDouble, numeric: math.Float64bits(v)} }
func NewBooleanValue(v bool) Value {
	var n uint64
	if v {
		n = 1
	}
	return Value{kind: KindBoolean, numeric: n}
}
func NewDatetimeValue(v int64) Value   { return Value{kind: KindDatetime, numeric: uint64(v)} }
func NewVarcharValue(v string) Value   { return Value{kind: KindVarchar, strVal: v} }
func NewTextValue(v string) Value      { return Value{kind: KindText, strVal: v} }

// -- Accessors --

// IsNull reports whether this Value is NULL.
func (v Value) IsNull() bool { return v.kind == KindNull }

// Kind returns the ValueKind of this Value.
func (v Value) Kind() ValueKind { return v.kind }

// AsInt returns the int32 value. Panics if Kind != KindInt.
func (v Value) AsInt() int32 {
	if v.kind != KindInt {
		panic(fmt.Sprintf("storage: Value.AsInt called on Kind %d", v.kind))
	}
	return int32(v.numeric)
}

// AsBigInt returns the int64 value. Panics if Kind != KindBigInt.
func (v Value) AsBigInt() int64 {
	if v.kind != KindBigInt {
		panic(fmt.Sprintf("storage: Value.AsBigInt called on Kind %d", v.kind))
	}
	return int64(v.numeric)
}

// AsFloat returns the float32 value. Panics if Kind != KindFloat.
func (v Value) AsFloat() float32 {
	if v.kind != KindFloat {
		panic(fmt.Sprintf("storage: Value.AsFloat called on Kind %d", v.kind))
	}
	return math.Float32frombits(uint32(v.numeric))
}

// AsDouble returns the float64 value. Panics if Kind != KindDouble.
func (v Value) AsDouble() float64 {
	if v.kind != KindDouble {
		panic(fmt.Sprintf("storage: Value.AsDouble called on Kind %d", v.kind))
	}
	return math.Float64frombits(v.numeric)
}

// AsBoolean returns the bool value. Panics if Kind != KindBoolean.
func (v Value) AsBoolean() bool {
	if v.kind != KindBoolean {
		panic(fmt.Sprintf("storage: Value.AsBoolean called on Kind %d", v.kind))
	}
	return v.numeric != 0
}

// AsDatetime returns the Unix-nanosecond timestamp. Panics if Kind != KindDatetime.
func (v Value) AsDatetime() int64 {
	if v.kind != KindDatetime {
		panic(fmt.Sprintf("storage: Value.AsDatetime called on Kind %d", v.kind))
	}
	return int64(v.numeric)
}

// AsString returns the string content for VARCHAR or TEXT values.
// Panics if Kind is neither KindVarchar nor KindText.
func (v Value) AsString() string {
	if v.kind != KindVarchar && v.kind != KindText {
		panic(fmt.Sprintf("storage: Value.AsString called on Kind %d", v.kind))
	}
	return v.strVal
}

// String returns a human-readable representation of the value.
func (v Value) String() string {
	switch v.kind {
	case KindNull:
		return "NULL"
	case KindInt:
		return fmt.Sprintf("%d", int32(v.numeric))
	case KindBigInt:
		return fmt.Sprintf("%d", int64(v.numeric))
	case KindFloat:
		return fmt.Sprintf("%g", math.Float32frombits(uint32(v.numeric)))
	case KindDouble:
		return fmt.Sprintf("%g", math.Float64frombits(v.numeric))
	case KindBoolean:
		if v.numeric != 0 {
			return "true"
		}
		return "false"
	case KindDatetime:
		return fmt.Sprintf("%d", int64(v.numeric))
	case KindVarchar, KindText:
		return v.strVal
	}
	return fmt.Sprintf("Value(kind=%d)", v.kind)
}

// ---- Row ---------------------------------------------------------------------

// Row is an ordered slice of Values, one per schema column.
type Row []Value

// ---- Encode ------------------------------------------------------------------

// Encode serializes row into a compact []byte according to schema.
// The returned slice is ready to pass to Page.InsertTuple.
//
// Binary format:
//
//	[NullBitmap: ceil(N/8) bytes, LSB-first, bit i=1 → col i is NULL]
//	[Fixed region: fixed-length columns in schema order, always present]
//	[Var directory: 4 bytes per var-length column — Offset uint16 LE + Length uint16 LE]
//	[Var data: concatenated var-length values, no padding]
//
// Errors: ErrSchemaMismatch, ErrNullConstraint, ErrVarcharTooLong, ErrRowTooLarge.
func Encode(schema *Schema, row Row) ([]byte, error) {
	if len(row) != schema.NumColumns() {
		return nil, &ErrSchemaMismatch{Got: len(row), Want: schema.NumColumns()}
	}

	// Validate and measure variable-length data.
	varLengths := make([]int, len(schema.layout.varIndices))
	totalVarData := 0
	for vi, ci := range schema.layout.varIndices {
		col := schema.columns[ci]
		val := row[ci]
		if val.IsNull() {
			if !col.Nullable {
				return nil, &ErrNullConstraint{ColumnIndex: ci, ColumnName: col.Name}
			}
			varLengths[vi] = 0
			continue
		}
		s := val.strVal
		n := len(s)
		if col.Type == TypeVARCHAR && n > int(col.MaxLen) {
			return nil, &ErrVarcharTooLong{
				ColumnIndex: ci, ColumnName: col.Name,
				MaxLen: col.MaxLen, ActualLen: n,
			}
		}
		varLengths[vi] = n
		totalVarData += n
	}

	// Validate fixed-length nullable columns.
	for i, col := range schema.columns {
		if col.Type.IsFixedLength() && row[i].IsNull() && !col.Nullable {
			return nil, &ErrNullConstraint{ColumnIndex: i, ColumnName: col.Name}
		}
	}

	totalSize := schema.layout.varDataOffset + totalVarData
	maxTupleSize := PageSize - HeaderSize - SlotSize
	if totalSize > maxTupleSize {
		return nil, &ErrRowTooLarge{Size: totalSize, MaxSize: maxTupleSize}
	}

	buf := make([]byte, totalSize)

	// Fill null bitmap.
	for i, val := range row {
		if val.IsNull() {
			buf[i/8] |= 1 << (i % 8)
		}
	}

	// Fill fixed region.
	fixedBase := schema.layout.nullBitmapSize
	for i, col := range schema.columns {
		if !col.Type.IsFixedLength() {
			continue
		}
		off := fixedBase + schema.layout.fixedOffsets[i]
		val := row[i]
		if val.IsNull() {
			// Leave zeros — null bitmap is authoritative.
			continue
		}
		if err := encodeFixed(buf, off, val, col.Type); err != nil {
			return nil, err
		}
	}

	// Fill var directory and var data.
	dirBase := schema.layout.varDirOffset
	dataBase := schema.layout.varDataOffset
	dataOff := 0
	for vi, ci := range schema.layout.varIndices {
		dirOff := dirBase + vi*4
		val := row[ci]
		if val.IsNull() {
			// Leave offset=0, length=0 (null bitmap is authoritative).
			continue
		}
		s := val.strVal
		n := len(s)
		binary.LittleEndian.PutUint16(buf[dirOff:], uint16(dataOff))
		binary.LittleEndian.PutUint16(buf[dirOff+2:], uint16(n))
		copy(buf[dataBase+dataOff:], s)
		dataOff += n
	}

	return buf, nil
}

// ---- Decode ------------------------------------------------------------------

// Decode deserializes data (obtained from Page.GetTuple) into a Row according
// to schema. Returns ErrCorruptRecord if data is too short or directory
// pointers are out of bounds.
func Decode(schema *Schema, data []byte) (Row, error) {
	minSize := schema.layout.varDataOffset
	if len(data) < minSize {
		return nil, &ErrCorruptRecord{
			Reason: fmt.Sprintf("data length %d < minimum record size %d", len(data), minSize),
		}
	}

	row := make(Row, schema.NumColumns())
	varDataSize := len(data) - schema.layout.varDataOffset

	for i, col := range schema.columns {
		isNull := (data[i/8]>>(i%8))&1 == 1
		if isNull {
			row[i] = NewNullValue()
			continue
		}
		if col.Type.IsFixedLength() {
			off := schema.layout.nullBitmapSize + schema.layout.fixedOffsets[i]
			v, err := decodeFixed(data, off, col.Type)
			if err != nil {
				return nil, &ErrCorruptRecord{Reason: err.Error()}
			}
			row[i] = v
		}
	}

	// Decode variable-length columns.
	for vi, ci := range schema.layout.varIndices {
		col := schema.columns[ci]
		isNull := (data[ci/8]>>(ci%8))&1 == 1
		if isNull {
			row[ci] = NewNullValue()
			continue
		}
		dirOff := schema.layout.varDirOffset + vi*4
		if dirOff+4 > len(data) {
			return nil, &ErrCorruptRecord{
				Reason: fmt.Sprintf("var directory entry %d out of bounds", vi),
			}
		}
		relOffset := int(binary.LittleEndian.Uint16(data[dirOff:]))
		length := int(binary.LittleEndian.Uint16(data[dirOff+2:]))
		if relOffset+length > varDataSize {
			return nil, &ErrCorruptRecord{
				Reason: fmt.Sprintf("var column %d data [%d:%d] out of bounds (var region size %d)",
					ci, relOffset, relOffset+length, varDataSize),
			}
		}
		abs := schema.layout.varDataOffset + relOffset
		s := string(data[abs : abs+length])
		if col.Type == TypeVARCHAR {
			row[ci] = NewVarcharValue(s)
		} else {
			row[ci] = NewTextValue(s)
		}
	}

	return row, nil
}

// DecodeColumn deserializes a single column value from data without decoding
// the full row. O(1) for fixed-length columns; O(numVarCols) for variable-length.
func DecodeColumn(schema *Schema, data []byte, colIndex int) (Value, error) {
	if colIndex < 0 || colIndex >= schema.NumColumns() {
		return Value{}, fmt.Errorf("storage: column index %d out of range [0, %d)",
			colIndex, schema.NumColumns())
	}
	if len(data) < schema.layout.varDataOffset {
		return Value{}, &ErrCorruptRecord{
			Reason: fmt.Sprintf("data length %d < minimum record size %d",
				len(data), schema.layout.varDataOffset),
		}
	}

	col := schema.columns[colIndex]
	isNull := (data[colIndex/8]>>(colIndex%8))&1 == 1
	if isNull {
		return NewNullValue(), nil
	}

	if col.Type.IsFixedLength() {
		off := schema.layout.nullBitmapSize + schema.layout.fixedOffsets[colIndex]
		return decodeFixed(data, off, col.Type)
	}

	// Find the var-index for this column.
	vi := -1
	for j, ci := range schema.layout.varIndices {
		if ci == colIndex {
			vi = j
			break
		}
	}
	if vi == -1 {
		return Value{}, &ErrCorruptRecord{Reason: fmt.Sprintf("column %d not in var index", colIndex)}
	}

	dirOff := schema.layout.varDirOffset + vi*4
	if dirOff+4 > len(data) {
		return Value{}, &ErrCorruptRecord{Reason: fmt.Sprintf("var directory entry %d out of bounds", vi)}
	}
	relOffset := int(binary.LittleEndian.Uint16(data[dirOff:]))
	length := int(binary.LittleEndian.Uint16(data[dirOff+2:]))
	varDataSize := len(data) - schema.layout.varDataOffset
	if relOffset+length > varDataSize {
		return Value{}, &ErrCorruptRecord{
			Reason: fmt.Sprintf("var column %d data out of bounds", colIndex),
		}
	}
	abs := schema.layout.varDataOffset + relOffset
	s := string(data[abs : abs+length])
	if col.Type == TypeVARCHAR {
		return NewVarcharValue(s), nil
	}
	return NewTextValue(s), nil
}

// ---- internal helpers --------------------------------------------------------

func encodeFixed(buf []byte, offset int, v Value, dt DataType) error {
	switch dt {
	case TypeINT:
		binary.LittleEndian.PutUint32(buf[offset:], uint32(v.numeric))
	case TypeBIGINT:
		binary.LittleEndian.PutUint64(buf[offset:], v.numeric)
	case TypeFLOAT:
		binary.LittleEndian.PutUint32(buf[offset:], uint32(v.numeric))
	case TypeDOUBLE:
		binary.LittleEndian.PutUint64(buf[offset:], v.numeric)
	case TypeBOOLEAN:
		if v.numeric != 0 {
			buf[offset] = 0x01
		} else {
			buf[offset] = 0x00
		}
	case TypeDATETIME:
		binary.LittleEndian.PutUint64(buf[offset:], v.numeric)
	default:
		return fmt.Errorf("storage: encodeFixed: unknown type %d", dt)
	}
	return nil
}

func decodeFixed(buf []byte, offset int, dt DataType) (Value, error) {
	switch dt {
	case TypeINT:
		return NewIntValue(int32(binary.LittleEndian.Uint32(buf[offset:]))), nil
	case TypeBIGINT:
		return NewBigIntValue(int64(binary.LittleEndian.Uint64(buf[offset:]))), nil
	case TypeFLOAT:
		return NewFloatValue(math.Float32frombits(binary.LittleEndian.Uint32(buf[offset:]))), nil
	case TypeDOUBLE:
		return NewDoubleValue(math.Float64frombits(binary.LittleEndian.Uint64(buf[offset:]))), nil
	case TypeBOOLEAN:
		return NewBooleanValue(buf[offset] != 0), nil
	case TypeDATETIME:
		return NewDatetimeValue(int64(binary.LittleEndian.Uint64(buf[offset:]))), nil
	}
	return Value{}, fmt.Errorf("storage: decodeFixed: unknown type %d", dt)
}
