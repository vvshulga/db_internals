package storage

import (
	"errors"
	"math"
	"testing"
)

// ---- helpers -----------------------------------------------------------------

func mustSchema(t *testing.T, cols []Column) *Schema {
	t.Helper()
	s, err := NewSchema(cols)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return s
}

func mustEncode(t *testing.T, schema *Schema, row Row) []byte {
	t.Helper()
	data, err := Encode(schema, row)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return data
}

// rowsEqual compares two Rows element-by-element.
func rowsEqual(a, b Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- Value constructors and accessors ----------------------------------------

func TestValue_AllTypes_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		kind ValueKind
	}{
		{"null", NewNullValue(), KindNull},
		{"int", NewIntValue(-42), KindInt},
		{"bigint", NewBigIntValue(1<<40 + 7), KindBigInt},
		{"float", NewFloatValue(3.14), KindFloat},
		{"double", NewDoubleValue(math.Pi), KindDouble},
		{"boolean true", NewBooleanValue(true), KindBoolean},
		{"boolean false", NewBooleanValue(false), KindBoolean},
		{"datetime", NewDatetimeValue(1_700_000_000_000_000_000), KindDatetime},
		{"varchar", NewVarcharValue("hello"), KindVarchar},
		{"text", NewTextValue("world"), KindText},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.v.Kind() != tc.kind {
				t.Errorf("Kind: got %d, want %d", tc.v.Kind(), tc.kind)
			}
			switch tc.kind {
			case KindNull:
				if !tc.v.IsNull() {
					t.Error("IsNull should be true")
				}
			case KindInt:
				if tc.v.AsInt() != -42 {
					t.Errorf("AsInt: got %d, want -42", tc.v.AsInt())
				}
			case KindBigInt:
				if tc.v.AsBigInt() != 1<<40+7 {
					t.Errorf("AsBigInt: got %d", tc.v.AsBigInt())
				}
			case KindFloat:
				if math.Abs(float64(tc.v.AsFloat()-3.14)) > 1e-5 {
					t.Errorf("AsFloat: got %v", tc.v.AsFloat())
				}
			case KindDouble:
				if tc.v.AsDouble() != math.Pi {
					t.Errorf("AsDouble: got %v", tc.v.AsDouble())
				}
			case KindBoolean:
				// checked separately above
			case KindDatetime:
				if tc.v.AsDatetime() != 1_700_000_000_000_000_000 {
					t.Errorf("AsDatetime: got %d", tc.v.AsDatetime())
				}
			case KindVarchar:
				if tc.v.AsString() != "hello" {
					t.Errorf("AsString: got %q", tc.v.AsString())
				}
			case KindText:
				if tc.v.AsString() != "world" {
					t.Errorf("AsString: got %q", tc.v.AsString())
				}
			}
		})
	}
}

func TestValue_Boolean(t *testing.T) {
	if !NewBooleanValue(true).AsBoolean() {
		t.Error("NewBooleanValue(true).AsBoolean() should be true")
	}
	if NewBooleanValue(false).AsBoolean() {
		t.Error("NewBooleanValue(false).AsBoolean() should be false")
	}
}

func TestValue_IsNull(t *testing.T) {
	if !NewNullValue().IsNull() {
		t.Error("NullValue.IsNull() should be true")
	}
	if NewIntValue(0).IsNull() {
		t.Error("Int(0).IsNull() should be false")
	}
}

func TestValue_String(t *testing.T) {
	tests := []struct{ v Value; want string }{
		{NewNullValue(), "NULL"},
		{NewIntValue(42), "42"},
		{NewBooleanValue(true), "true"},
		{NewBooleanValue(false), "false"},
		{NewVarcharValue("hi"), "hi"},
		{NewTextValue("world"), "world"},
	}
	for _, tc := range tests {
		if got := tc.v.String(); got != tc.want {
			t.Errorf("Value.String() = %q, want %q", got, tc.want)
		}
	}
}

// ---- Encode / Decode round-trips ---------------------------------------------

func TestEncodeDecode_AllFixed(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "i", Type: TypeINT},
		{Name: "b", Type: TypeBIGINT},
		{Name: "f", Type: TypeFLOAT},
		{Name: "d", Type: TypeDOUBLE},
		{Name: "bool", Type: TypeBOOLEAN},
		{Name: "dt", Type: TypeDATETIME},
	})
	row := Row{
		NewIntValue(-1),
		NewBigIntValue(1 << 40),
		NewFloatValue(1.5),
		NewDoubleValue(math.E),
		NewBooleanValue(true),
		NewDatetimeValue(999),
	}
	data := mustEncode(t, schema, row)
	got, err := Decode(schema, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !rowsEqual(got, row) {
		t.Errorf("round-trip mismatch:\n got  %v\n want %v", got, row)
	}
}

func TestEncodeDecode_AllVarchar(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "a", Type: TypeVARCHAR, MaxLen: 32},
		{Name: "b", Type: TypeVARCHAR, MaxLen: 32},
	})
	row := Row{NewVarcharValue("hello"), NewVarcharValue("world")}
	data := mustEncode(t, schema, row)
	got, err := Decode(schema, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !rowsEqual(got, row) {
		t.Errorf("round-trip mismatch: got %v, want %v", got, row)
	}
}

func TestEncodeDecode_Mixed(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
		{Name: "age", Type: TypeINT},
		{Name: "bio", Type: TypeTEXT, Nullable: true},
		{Name: "active", Type: TypeBOOLEAN},
	})
	row := Row{
		NewIntValue(42),
		NewVarcharValue("Alice"),
		NewIntValue(30),
		NewTextValue("loves databases"),
		NewBooleanValue(true),
	}
	data := mustEncode(t, schema, row)
	got, err := Decode(schema, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !rowsEqual(got, row) {
		t.Errorf("round-trip mismatch: got %v, want %v", got, row)
	}
}

func TestEncodeDecode_NullFixedCol(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "val", Type: TypeINT, Nullable: true},
	})
	row := Row{NewIntValue(1), NewNullValue()}
	data := mustEncode(t, schema, row)
	got, err := Decode(schema, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got[1] != NewNullValue() {
		t.Errorf("expected NULL for col 1, got %v", got[1])
	}
}

func TestEncodeDecode_NullVarCol(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 32, Nullable: true},
	})
	row := Row{NewIntValue(5), NewNullValue()}
	data := mustEncode(t, schema, row)
	got, err := Decode(schema, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !got[1].IsNull() {
		t.Errorf("expected NULL for name col, got %v", got[1])
	}
}

func TestEncodeDecode_AllNullNullable(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "a", Type: TypeINT, Nullable: true},
		{Name: "b", Type: TypeVARCHAR, MaxLen: 10, Nullable: true},
		{Name: "c", Type: TypeTEXT, Nullable: true},
	})
	row := Row{NewNullValue(), NewNullValue(), NewNullValue()}
	data := mustEncode(t, schema, row)
	got, err := Decode(schema, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i, v := range got {
		if !v.IsNull() {
			t.Errorf("col %d: expected NULL, got %v", i, v)
		}
	}
}

// ---- Encode error paths ------------------------------------------------------

func TestEncode_NonNullableRejectsNull(t *testing.T) {
	schema := mustSchema(t, []Column{{Name: "id", Type: TypeINT}})
	_, err := Encode(schema, Row{NewNullValue()})
	var e *ErrNullConstraint
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrNullConstraint, got %T: %v", err, err)
	}
	if e.ColumnIndex != 0 {
		t.Errorf("ColumnIndex: got %d, want 0", e.ColumnIndex)
	}
}

func TestEncode_VarcharTooLong(t *testing.T) {
	schema := mustSchema(t, []Column{{Name: "v", Type: TypeVARCHAR, MaxLen: 4}})
	_, err := Encode(schema, Row{NewVarcharValue("hello!")}) // 6 chars > MaxLen 4
	var e *ErrVarcharTooLong
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrVarcharTooLong, got %T: %v", err, err)
	}
	if e.MaxLen != 4 || e.ActualLen != 6 {
		t.Errorf("ErrVarcharTooLong: MaxLen=%d ActualLen=%d", e.MaxLen, e.ActualLen)
	}
}

func TestEncode_RowTooLarge(t *testing.T) {
	// A TEXT value larger than a page cannot ever fit.
	schema := mustSchema(t, []Column{{Name: "t", Type: TypeTEXT}})
	big := make([]byte, PageSize)
	_, err := Encode(schema, Row{NewTextValue(string(big))})
	var e *ErrRowTooLarge
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrRowTooLarge, got %T: %v", err, err)
	}
}

func TestEncode_SchemaMismatch(t *testing.T) {
	schema := mustSchema(t, []Column{{Name: "id", Type: TypeINT}})
	_, err := Encode(schema, Row{NewIntValue(1), NewIntValue(2)})
	var e *ErrSchemaMismatch
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrSchemaMismatch, got %T: %v", err, err)
	}
}

// ---- DecodeColumn ------------------------------------------------------------

func TestDecodeColumn_Fixed(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "a", Type: TypeINT},
		{Name: "b", Type: TypeBIGINT},
	})
	row := Row{NewIntValue(7), NewBigIntValue(42)}
	data := mustEncode(t, schema, row)

	v, err := DecodeColumn(schema, data, 0)
	if err != nil || v.AsInt() != 7 {
		t.Errorf("DecodeColumn(0): err=%v val=%v", err, v)
	}
	v, err = DecodeColumn(schema, data, 1)
	if err != nil || v.AsBigInt() != 42 {
		t.Errorf("DecodeColumn(1): err=%v val=%v", err, v)
	}
}

func TestDecodeColumn_Varchar(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 32},
	})
	row := Row{NewIntValue(1), NewVarcharValue("Bob")}
	data := mustEncode(t, schema, row)

	v, err := DecodeColumn(schema, data, 1)
	if err != nil || v.AsString() != "Bob" {
		t.Errorf("DecodeColumn(name): err=%v val=%v", err, v)
	}
}

func TestDecodeColumn_NullVarchar(t *testing.T) {
	schema := mustSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 32, Nullable: true},
	})
	row := Row{NewIntValue(1), NewNullValue()}
	data := mustEncode(t, schema, row)

	v, err := DecodeColumn(schema, data, 1)
	if err != nil || !v.IsNull() {
		t.Errorf("DecodeColumn(nullable name): err=%v val=%v", err, v)
	}
}

// ---- Decode error paths ------------------------------------------------------

func TestDecode_CorruptShortBuffer(t *testing.T) {
	schema := mustSchema(t, []Column{{Name: "id", Type: TypeINT}})
	_, err := Decode(schema, []byte{})
	var e *ErrCorruptRecord
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrCorruptRecord, got %T: %v", err, err)
	}
}

func TestDecode_CorruptVarOffset(t *testing.T) {
	schema := mustSchema(t, []Column{{Name: "t", Type: TypeTEXT}})
	row := Row{NewTextValue("hi")}
	data := mustEncode(t, schema, row)
	// Corrupt the var-directory offset to point beyond the buffer.
	data[schema.VarDirOffset()] = 0xFF
	data[schema.VarDirOffset()+1] = 0xFF
	_, err := Decode(schema, data)
	var e *ErrCorruptRecord
	if !errors.As(err, &e) {
		t.Fatalf("expected ErrCorruptRecord, got %T: %v", err, err)
	}
}
