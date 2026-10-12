package h5

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDatatypeString(t *testing.T) {
	i32 := &Datatype{Class: ClassInteger, Size: 4, Precision: 32, Sign: Sign2, Order: OrderLE}
	u16be := &Datatype{Class: ClassInteger, Size: 2, Precision: 16, Sign: SignNone, Order: OrderBE}
	tests := []struct {
		name string
		dt   *Datatype
		want string
	}{
		{"nil", nil, "<nil>"},
		{"int32 le", i32, "int32le"},
		{"uint16 be", u16be, "uint16be"},
		{"int vax", &Datatype{Class: ClassInteger, Precision: 8, Sign: Sign2, Order: OrderVAX}, "int8vax"},
		{"int no order", &Datatype{Class: ClassInteger, Precision: 8, Sign: Sign2, Order: OrderNone}, "int8"},
		{"odd precision", &Datatype{Class: ClassInteger, Size: 4, Precision: 24, Sign: SignNone, Order: OrderLE}, "uint24le"},
		{"float32 le", &Datatype{Class: ClassFloat, Size: 4, Order: OrderLE}, "float32le"},
		{"float64 be", &Datatype{Class: ClassFloat, Size: 8, Order: OrderBE}, "float64be"},
		{"fixed string", &Datatype{Class: ClassString, Size: 10}, "string[10]"},
		{"vlen string", &Datatype{Class: ClassVlen, VlenKind: VlenString}, "vlen string"},
		{"vlen sequence", &Datatype{Class: ClassVlen, VlenKind: VlenSequence, Base: i32}, "vlen(int32le)"},
		{"array", &Datatype{Class: ClassArray, ArrayDims: []uint64{2, 3}, Base: i32}, "array[2 3](int32le)"},
		{"compound", &Datatype{Class: ClassCompound, Size: 12, Members: []Member{{Name: "a", Type: i32}, {Name: "b", Offset: 4, Type: i32}}}, "compound{2 members, 12 bytes}"},
		{"enum", &Datatype{Class: ClassEnum, Base: i32, EnumNames: []string{"A", "B", "C"}, EnumValues: []int64{0, 1, 2}}, "enum(int32le){3}"},
		{"opaque", &Datatype{Class: ClassOpaque, Size: 5, Tag: "x"}, "opaque[5]"},
		{"reference", &Datatype{Class: ClassReference, Size: 8}, "reference[8]"},
		{"bitfield", &Datatype{Class: ClassBitfield, Size: 2}, "bitfield[2]"},
		{"time", &Datatype{Class: ClassTime, Size: 4}, "time[4]"},
		{"unknown class", &Datatype{Class: Class(42), Size: 1}, "class(42)[1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.dt.String())
		})
	}
}

func TestDatatypeNestedString(t *testing.T) {
	f := &Datatype{Class: ClassFloat, Size: 8, Order: OrderLE}
	dt := &Datatype{Class: ClassVlen, Base: &Datatype{Class: ClassArray, ArrayDims: []uint64{4}, Base: f}}
	assert.Equal(t, "vlen(array[4](float64le))", dt.String())
}

func TestDatatypeNumElems(t *testing.T) {
	assert.Equal(t, uint64(1), (&Datatype{Class: ClassInteger}).NumElems())
	assert.Equal(t, uint64(1), (&Datatype{Class: ClassArray}).NumElems())
	assert.Equal(t, uint64(5), (&Datatype{Class: ClassArray, ArrayDims: []uint64{5}}).NumElems())
	assert.Equal(t, uint64(24), (&Datatype{Class: ClassArray, ArrayDims: []uint64{2, 3, 4}}).NumElems())
	assert.Equal(t, uint64(0), (&Datatype{Class: ClassArray, ArrayDims: []uint64{3, 0}}).NumElems())
}

func TestDatatypeIsVlenString(t *testing.T) {
	assert.True(t, (&Datatype{Class: ClassVlen, VlenKind: VlenString}).IsVlenString())
	assert.False(t, (&Datatype{Class: ClassVlen, VlenKind: VlenSequence}).IsVlenString())
	assert.False(t, (&Datatype{Class: ClassString, VlenKind: VlenString}).IsVlenString())
	assert.False(t, (&Datatype{Class: ClassInteger}).IsVlenString())
}

func TestClassString(t *testing.T) {
	want := map[Class]string{
		ClassInteger: "integer", ClassFloat: "float", ClassTime: "time", ClassString: "string",
		ClassBitfield: "bitfield", ClassOpaque: "opaque", ClassCompound: "compound",
		ClassReference: "reference", ClassEnum: "enum", ClassVlen: "vlen", ClassArray: "array",
		Class(11): "class(11)", Class(255): "class(255)",
	}
	for c, s := range want {
		assert.Equal(t, s, c.String())
	}
}

func TestClassNumbering(t *testing.T) {
	// Numbers are fixed by the file format specification.
	assert.EqualValues(t, 0, ClassInteger)
	assert.EqualValues(t, 6, ClassCompound)
	assert.EqualValues(t, 10, ClassArray)
}

func TestDataspace(t *testing.T) {
	tests := []struct {
		name  string
		s     Dataspace
		elems uint64
		rank  int
	}{
		{"scalar", Dataspace{Class: SpaceScalar}, 1, 0},
		{"scalar ignores dims", Dataspace{Class: SpaceScalar, Dims: []uint64{9}}, 1, 1},
		{"null", Dataspace{Class: SpaceNull}, 0, 0},
		{"null ignores dims", Dataspace{Class: SpaceNull, Dims: []uint64{9}}, 0, 1},
		{"simple 1d", Dataspace{Class: SpaceSimple, Dims: []uint64{7}}, 7, 1},
		{"simple 3d", Dataspace{Class: SpaceSimple, Dims: []uint64{2, 3, 4}}, 24, 3},
		{"simple zero dim", Dataspace{Class: SpaceSimple, Dims: []uint64{2, 0}}, 0, 2},
		{"simple no dims", Dataspace{Class: SpaceSimple}, 1, 0},
		{"unlimited max dims", Dataspace{Class: SpaceSimple, Dims: []uint64{3}, MaxDims: []uint64{Unlimited}}, 3, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.elems, tt.s.NumElems())
			assert.Equal(t, tt.rank, tt.s.Rank())
		})
	}
	assert.Equal(t, ^uint64(0), Unlimited)
}

func TestLinkTypeString(t *testing.T) {
	assert.Equal(t, "hard", LinkHard.String())
	assert.Equal(t, "soft", LinkSoft.String())
	assert.Equal(t, "external", LinkExternal.String())
	assert.Equal(t, "user-defined", LinkType(65).String())
	assert.Equal(t, "user-defined", LinkType(255).String())
}

func TestObjectTypeString(t *testing.T) {
	assert.Equal(t, "group", ObjectGroup.String())
	assert.Equal(t, "dataset", ObjectDataset.String())
	assert.Equal(t, "datatype", ObjectNamedDatatype.String())
	assert.Equal(t, "unknown", ObjectUnknown.String())
	assert.Equal(t, "unknown", ObjectType(17).String())
}

func TestLayoutClassString(t *testing.T) {
	assert.Equal(t, "compact", LayoutCompact.String())
	assert.Equal(t, "contiguous", LayoutContiguous.String())
	assert.Equal(t, "chunked", LayoutChunked.String())
	assert.Equal(t, "virtual", LayoutVirtual.String())
	assert.Equal(t, "unknown", LayoutClass(9).String())
}
