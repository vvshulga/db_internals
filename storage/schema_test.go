package storage

import (
	"errors"
	"testing"
)

func makeSchema(t *testing.T, cols []Column) *Schema {
	t.Helper()
	s, err := NewSchema(cols)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return s
}

func TestNewSchema_Valid(t *testing.T) {
	t.Run("single fixed", func(t *testing.T) {
		s := makeSchema(t, []Column{{Name: "id", Type: TypeINT}})
		if s.NumColumns() != 1 {
			t.Errorf("NumColumns: got %d, want 1", s.NumColumns())
		}
	})

	t.Run("mixed types", func(t *testing.T) {
		cols := []Column{
			{Name: "id", Type: TypeINT},
			{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
			{Name: "score", Type: TypeDOUBLE},
			{Name: "bio", Type: TypeTEXT, Nullable: true},
		}
		s := makeSchema(t, cols)
		if s.NumColumns() != 4 {
			t.Errorf("NumColumns: got %d, want 4", s.NumColumns())
		}
	})

	t.Run("all variable", func(t *testing.T) {
		cols := []Column{
			{Name: "a", Type: TypeVARCHAR, MaxLen: 10},
			{Name: "b", Type: TypeTEXT},
		}
		s := makeSchema(t, cols)
		if s.FixedRegionSize() != 0 {
			t.Errorf("FixedRegionSize: got %d, want 0", s.FixedRegionSize())
		}
	})
}

func TestNewSchema_Errors(t *testing.T) {
	t.Run("empty columns", func(t *testing.T) {
		_, err := NewSchema(nil)
		var e *ErrInvalidSchema
		if !errors.As(err, &e) {
			t.Fatalf("expected ErrInvalidSchema, got %T: %v", err, err)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		_, err := NewSchema([]Column{
			{Name: "x", Type: TypeINT},
			{Name: "x", Type: TypeBIGINT},
		})
		var e *ErrInvalidSchema
		if !errors.As(err, &e) {
			t.Fatalf("expected ErrInvalidSchema, got %T: %v", err, err)
		}
	})

	t.Run("VARCHAR MaxLen zero", func(t *testing.T) {
		_, err := NewSchema([]Column{{Name: "v", Type: TypeVARCHAR, MaxLen: 0}})
		var e *ErrInvalidSchema
		if !errors.As(err, &e) {
			t.Fatalf("expected ErrInvalidSchema, got %T: %v", err, err)
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		_, err := NewSchema([]Column{{Name: "x", Type: DataType(99)}})
		var e *ErrInvalidSchema
		if !errors.As(err, &e) {
			t.Fatalf("expected ErrInvalidSchema, got %T: %v", err, err)
		}
	})

	t.Run("empty column name", func(t *testing.T) {
		_, err := NewSchema([]Column{{Name: "", Type: TypeINT}})
		var e *ErrInvalidSchema
		if !errors.As(err, &e) {
			t.Fatalf("expected ErrInvalidSchema, got %T: %v", err, err)
		}
	})
}

func TestSchema_NullBitmapSize(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{1, 1}, {7, 1}, {8, 1}, {9, 2}, {16, 2}, {17, 3},
	}
	for _, tc := range tests {
		cols := make([]Column, tc.n)
		for i := range cols {
			cols[i] = Column{Name: colName(i), Type: TypeINT}
		}
		s := makeSchema(t, cols)
		if got := s.NullBitmapSize(); got != tc.want {
			t.Errorf("NullBitmapSize for %d cols: got %d, want %d", tc.n, got, tc.want)
		}
	}
}

func TestSchema_FixedOffsets(t *testing.T) {
	// Schema: INT(4), INT(4), BOOLEAN(1)  → offsets 0, 4, 8
	s := makeSchema(t, []Column{
		{Name: "a", Type: TypeINT},
		{Name: "b", Type: TypeINT},
		{Name: "c", Type: TypeBOOLEAN},
	})
	wantOffsets := []int{0, 4, 8}
	for i, want := range wantOffsets {
		if got := s.FixedOffset(i); got != want {
			t.Errorf("FixedOffset(%d): got %d, want %d", i, got, want)
		}
	}
}

func TestSchema_VarDirOffset(t *testing.T) {
	// Schema: INT(4), VARCHAR — bitmap=1, fixed=4 → varDirOffset=5
	s := makeSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
	})
	if got := s.VarDirOffset(); got != 5 {
		t.Errorf("VarDirOffset: got %d, want 5", got)
	}
}

func TestSchema_VarDataOffset(t *testing.T) {
	// Schema: INT, VARCHAR, TEXT — bitmap=1, fixed=4, dir=2*12=24 → varDataOffset=29
	s := makeSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
		{Name: "bio", Type: TypeTEXT},
	})
	if got := s.VarDataOffset(); got != 29 {
		t.Errorf("VarDataOffset: got %d, want 29", got)
	}
}

func TestSchema_ColumnIndex(t *testing.T) {
	s := makeSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 32},
	})
	if i, ok := s.ColumnIndex("name"); !ok || i != 1 {
		t.Errorf("ColumnIndex(name): got (%d, %v), want (1, true)", i, ok)
	}
	if _, ok := s.ColumnIndex("missing"); ok {
		t.Error("ColumnIndex(missing): expected not found")
	}
}

func TestSchema_MinRecordSize(t *testing.T) {
	// Schema: INT, VARCHAR, TEXT — bitmap=1, fixed=4, dir=2*12=24 → min=29
	s := makeSchema(t, []Column{
		{Name: "id", Type: TypeINT},
		{Name: "name", Type: TypeVARCHAR, MaxLen: 64},
		{Name: "bio", Type: TypeTEXT},
	})
	if got := s.MinRecordSize(); got != 29 {
		t.Errorf("MinRecordSize: got %d, want 29", got)
	}
}

// colName generates a unique column name from an index.
func colName(i int) string {
	return string(rune('a'+i%26)) + string(rune('0'+i/26))
}
