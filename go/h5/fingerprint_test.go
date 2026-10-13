package h5

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprintFormat(t *testing.T) {
	fp := Fingerprint([]any{int32(1)})
	assert.Len(t, fp, FingerprintSize)
	assert.Regexp(t, "^[0-9a-f]{16}$", fp)
	assert.Equal(t, fp, Fingerprint([]any{int32(1)}), "deterministic")
}

// TestFingerprintStable pins a few fingerprints: when it fails, the
// canonical encoding changed and the golden file of the corpus
// (go/testdata/golden) must be regenerated (cd cgo && make golden).
func TestFingerprintStable(t *testing.T) {
	assert.Equal(t, "6e340b9cffb37a98", Fingerprint(nil))
	assert.Equal(t, "e8f9481096a6d1c9", Fingerprint([]any{int32(1), "a", 1.5}))
	assert.Equal(t, "90ee3fab6f5e65c5", Fingerprint(&Datatype{Class: ClassInteger, Size: 4, Precision: 32, Sign: Sign2}))
}

func TestFingerprintDistinguishes(t *testing.T) {
	nan1 := math.Float64frombits(0x7ff8000000000001)
	nan2 := math.Float64frombits(0x7ff8000000000002)
	rr := RegionRef{Address: 8, Selection: Selection{Type: SelPoints, Rank: 1, Points: [][]uint64{{1}}}}
	rrBlocks := RegionRef{Address: 8, Selection: Selection{Type: SelHyperslab, Rank: 1, Blocks: [][2][]uint64{{{0}, {1}}}}}
	rrUnlim := RegionRef{Address: 8, Selection: Selection{Type: SelHyperslab, Rank: 1,
		Unlimited: &Hyperslab{Start: []uint64{0}, Stride: []uint64{1}, Count: []uint64{Unlimited}, Block: []uint64{1}}}}
	i32 := &Datatype{Class: ClassInteger, Size: 4, Precision: 32, Sign: Sign2}
	pairs := []struct {
		name string
		a, b any
	}{
		{"int widths", []any{int32(1)}, []any{int64(1)}},
		{"signedness", []any{int8(1)}, []any{uint8(1)}},
		{"int vs float", []any{int64(0)}, []any{float64(0)}},
		{"float32 vs float64", []any{float32(1)}, []any{float64(1)}},
		{"nan payload", []any{nan1}, []any{nan2}},
		{"nan32 payload", []any{math.Float32frombits(0x7fc00001)}, []any{math.Float32frombits(0x7fc00002)}},
		{"negative zero", []any{0.0}, []any{math.Copysign(0, -1)}},
		{"string vs bytes", []any{"a"}, []any{[]byte("a")}},
		{"string concat", []any{"ab", "c"}, []any{"a", "bc"}},
		{"nested", []any{[]any{int8(1)}, int8(2)}, []any{[]any{int8(1), int8(2)}}},
		{"nil vs empty list", []any{nil}, []any{[]any{}}},
		{"bool", []any{true}, []any{false}},
		{"enum name", []any{Enum{Name: "A", Value: 1}}, []any{Enum{Name: "B", Value: 1}}},
		{"enum value", []any{Enum{Name: "A", Value: 1}}, []any{Enum{Name: "A", Value: 2}}},
		{"compound field order",
			[]any{Compound{Fields: []Field{{"a", int8(1)}, {"b", int8(2)}}}},
			[]any{Compound{Fields: []Field{{"b", int8(2)}, {"a", int8(1)}}}}},
		{"compound field name",
			[]any{Compound{Fields: []Field{{"a", int8(1)}}}},
			[]any{Compound{Fields: []Field{{"x", int8(1)}}}}},
		{"object ref", []any{ObjectRef{Address: 1}}, []any{ObjectRef{Address: 2}}},
		{"object ref undefined", []any{ObjectRef{}}, []any{ObjectRef{Undefined: true}}},
		{"region ref points", []any{rr}, []any{rrBlocks}},
		{"region ref unlimited", []any{rrBlocks}, []any{rrUnlim}},
		{"region ref vs object ref", []any{rr}, []any{ObjectRef{Address: 8}}},
		{"attr ref", []any{AttrRef{Address: 1, Name: "a"}}, []any{AttrRef{Address: 1, Name: "b"}}},
		{"selection nil points", Selection{Type: SelPoints}, Selection{Type: SelPoints, Points: [][]uint64{}}},
		{"datatype member type",
			&Datatype{Class: ClassCompound, Members: []Member{{Name: "a", Type: i32}}},
			&Datatype{Class: ClassCompound, Members: []Member{{Name: "a", Type: &Datatype{Class: ClassInteger, Size: 4, Precision: 32}}}}},
		{"datatype base", &Datatype{Class: ClassVlen, Base: i32}, &Datatype{Class: ClassVlen}},
		{"datatype float info", &Datatype{Class: ClassFloat, Float: FloatInfo{ExpBias: 127}}, &Datatype{Class: ClassFloat, Float: FloatInfo{ExpBias: 1023}}},
		{"datatype enum values", &Datatype{Class: ClassEnum, EnumNames: []string{"A"}, EnumValues: []int64{0}},
			&Datatype{Class: ClassEnum, EnumNames: []string{"A"}, EnumValues: []int64{1}}},
		{"dataspace max dims nil", Dataspace{Class: SpaceSimple, Dims: []uint64{1}}, Dataspace{Class: SpaceSimple, Dims: []uint64{1}, MaxDims: []uint64{1}}},
		{"dataspace dims vs max dims", Dataspace{Dims: []uint64{1}}, Dataspace{MaxDims: []uint64{1}}},
		{"link user data nil", Link{Type: 65}, Link{Type: 65, UserData: []byte{}}},
		{"link creation order", Link{Name: "a", CreationOrder: 1}, Link{Name: "a", CreationOrder: 2}},
		{"links nil", []Link(nil), []Link{}},
		{"links order", []Link{{Name: "a"}, {Name: "b"}}, []Link{{Name: "b"}, {Name: "a"}}},
		{"object info", ObjectInfo{RefCount: 1}, ObjectInfo{RefCount: 2}},
		{"object info times", ObjectInfo{MTime: 1}, ObjectInfo{CTime: 1}},
		{"superblock driver", SuperblockInfo{DriverName: "NCSAmult"}, SuperblockInfo{DriverName: "NCSAfami"}},
		{"layout chunk dims", Layout{Class: LayoutChunked, ChunkDims: []uint64{2, 3}}, Layout{Class: LayoutChunked, ChunkDims: []uint64{3, 2}}},
		{"layout external", Layout{External: []ExternalFile{{Name: "a", Size: 1}}}, Layout{External: []ExternalFile{{Name: "a", Size: 2}}}},
		{"layout virtual",
			Layout{Virtual: []VirtualMapping{{SourceFile: ".", SourceDataset: "a", SourceSelection: Selection{Type: SelAll}}}},
			Layout{Virtual: []VirtualMapping{{SourceFile: ".", SourceDataset: "a", VirtualSelection: Selection{Type: SelAll}}}}},
		{"filter cd values", Filter{ID: FilterDeflate, CDValues: []uint32{1}}, Filter{ID: FilterDeflate, CDValues: []uint32{2}}},
		{"filters nil", []Filter(nil), []Filter{}},
		{"fill value nil", FillValueInfo{Defined: true}, FillValueInfo{Defined: true, Value: []byte{}}},
		{"float64 slices", []float64{math.Copysign(0, -1)}, []float64{0}},
		{"strings", []string{"a", "b"}, []string{"ab"}},
		/* Slices of the same length whose contents differ (each slice
		 * kind is written behind a nil marker). */
		{"link user data", Link{Type: 65, UserData: []byte{1}}, Link{Type: 65, UserData: []byte{2}}},
		{"fill value bytes", FillValueInfo{Defined: true, Value: []byte{1}}, FillValueInfo{Defined: true, Value: []byte{2}}},
		{"filters", []Filter{{ID: FilterDeflate}}, []Filter{{ID: FilterShuffle}}},
		{"float32 slices", []float32{1}, []float32{2}},
		{"selection blocks",
			Selection{Type: SelHyperslab, Rank: 1, Blocks: [][2][]uint64{{{0}, {1}}}},
			Selection{Type: SelHyperslab, Rank: 1, Blocks: [][2][]uint64{{{0}, {2}}}}},
		{"selection points",
			Selection{Type: SelPoints, Rank: 1, Points: [][]uint64{{1}}},
			Selection{Type: SelPoints, Rank: 1, Points: [][]uint64{{2}}}},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			assert.NotEqual(t, Fingerprint(p.a), Fingerprint(p.b))
		})
	}
}

func TestFingerprintEquivalences(t *testing.T) {
	// Value trees: nil and empty []any / []byte are equal (as the
	// conformance suite compares them).
	assert.Equal(t, Fingerprint([]any{[]byte(nil)}), Fingerprint([]any{[]byte{}}))
	assert.Equal(t, Fingerprint([]any(nil)), Fingerprint([]any{}))
	// Pointers and values of the same struct.
	dt := Datatype{Class: ClassString, Size: 3}
	assert.Equal(t, Fingerprint(dt), Fingerprint(&dt))
	h := Hyperslab{Start: []uint64{1}}
	assert.Equal(t, Fingerprint(h), Fingerprint(&h))
	sp := Dataspace{Class: SpaceScalar}
	assert.Equal(t, Fingerprint(sp), Fingerprint(&sp))
	assert.Equal(t, Fingerprint((*Datatype)(nil)), Fingerprint(nil))
	// Equal deep copies.
	mk := func() any {
		return []any{Compound{Fields: []Field{{"x", []any{RegionRef{Selection: Selection{Type: SelPoints, Points: [][]uint64{{1, 2}}}}}}}}}
	}
	assert.Equal(t, Fingerprint(mk()), Fingerprint(mk()))
}

func TestFingerprintEveryType(t *testing.T) {
	values := []any{
		nil, int8(1), int16(1), int32(1), int64(1), 1, uint8(1), uint16(1), uint32(1), uint64(1), uint(1),
		float32(1), 1.0, true, "s", []byte("b"), []any{}, Enum{}, Compound{}, ObjectRef{}, RegionRef{}, AttrRef{},
		Selection{}, &Selection{}, (*Selection)(nil), Hyperslab{}, &Hyperslab{}, Datatype{}, &Datatype{},
		Dataspace{}, &Dataspace{}, (*Dataspace)(nil), Link{}, []Link{}, ObjectInfo{}, SuperblockInfo{},
		Layout{}, VirtualMapping{}, ExternalFile{}, Filter{}, []Filter{}, FillValueInfo{},
		[]string{}, []uint64{}, []int64{}, []uint32{}, []float64{}, []float32{1},
	}
	seen := map[string]any{}
	for _, v := range values {
		var buf bytes.Buffer
		require.NoError(t, WriteCanonical(&buf, v), "%T", v)
		assert.NotZero(t, buf.Len())
		fp := Fingerprint(v)
		if v != nil && reflect.ValueOf(v).Kind() == reflect.Pointer && reflect.ValueOf(v).IsNil() {
			continue // nil pointers encode as nil
		}
		if prev, ok := seen[fp]; ok && reflect.TypeOf(prev) != reflect.TypeOf(v) {
			// Pointers and values of one struct share an encoding.
			if reflect.TypeOf(prev) != reflect.PointerTo(reflect.TypeOf(v)) && reflect.PointerTo(reflect.TypeOf(prev)) != reflect.TypeOf(v) {
				t.Errorf("%T and %T have the same fingerprint", prev, v)
			}
		}
		seen[fp] = v
	}
}

func TestFingerprintUnsupported(t *testing.T) {
	err := WriteCanonical(&bytes.Buffer{}, []any{struct{}{}})
	assert.ErrorContains(t, err, "no canonical encoding for struct {}")
	assert.Panics(t, func() { Fingerprint(map[string]int{}) })
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestWriteCanonicalWriteError(t *testing.T) {
	assert.EqualError(t, WriteCanonical(failWriter{}, "x"), "boom")
	big := make([]any, 10000)
	for i := range big {
		big[i] = "0123456789"
	}
	assert.EqualError(t, WriteCanonical(failWriter{}, big), "boom")
}

// TestFingerprintCoversFields fails when a field is added to a type of the
// shared vocabulary: the canonical encoding (and the golden file) must
// then be extended.
func TestFingerprintCoversFields(t *testing.T) {
	want := map[reflect.Type]int{}
	for _, c := range []struct {
		v any
		n int
	}{
		{Datatype{}, 20}, {FloatInfo{}, 8}, {Member{}, 3}, {Dataspace{}, 4}, {Link{}, 10}, {ObjectInfo{}, 9},
		{SuperblockInfo{}, 13}, {Layout{}, 10}, {VirtualMapping{}, 4}, {ExternalFile{}, 3}, {Filter{}, 4},
		{FillValueInfo{}, 6}, {Selection{}, 5}, {Hyperslab{}, 4}, {Enum{}, 2}, {Compound{}, 1}, {Field{}, 2},
		{ObjectRef{}, 2}, {RegionRef{}, 3}, {AttrRef{}, 3},
	} {
		want[reflect.TypeOf(c.v)] = c.n
	}
	for typ, n := range want {
		assert.Equal(t, n, typ.NumField(), "%v", typ)
	}
}
