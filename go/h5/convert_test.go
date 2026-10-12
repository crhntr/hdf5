package h5

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func conv[T any](t *testing.T, vals ...any) []T {
	t.Helper()
	out, err := ConvertValues[T](vals)
	require.NoError(t, err)
	return out
}

func convErr[T any](t *testing.T, vals ...any) {
	t.Helper()
	_, err := ConvertValues[T](vals)
	assert.Error(t, err)
}

func vals(v ...any) []any { return v }

func TestConvertEmpty(t *testing.T) {
	out, err := ConvertValues[int32](nil)
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Empty(t, out)
}

func TestConvertAllIntegerWidths(t *testing.T) {
	srcs := []any{int8(5), int16(5), int32(5), int64(5), uint8(5), uint16(5), uint32(5), uint64(5)}
	for _, s := range srcs {
		assert.Equal(t, []int8{5}, conv[int8](t, s))
		assert.Equal(t, []int16{5}, conv[int16](t, s))
		assert.Equal(t, []int32{5}, conv[int32](t, s))
		assert.Equal(t, []int64{5}, conv[int64](t, s))
		assert.Equal(t, []int{5}, conv[int](t, s))
		assert.Equal(t, []uint8{5}, conv[uint8](t, s))
		assert.Equal(t, []uint16{5}, conv[uint16](t, s))
		assert.Equal(t, []uint32{5}, conv[uint32](t, s))
		assert.Equal(t, []uint64{5}, conv[uint64](t, s))
		assert.Equal(t, []uint{5}, conv[uint](t, s))
	}
}

func TestConvertIntegerBounds(t *testing.T) {
	assert.Equal(t, []int8{math.MinInt8, math.MaxInt8}, conv[int8](t, int64(math.MinInt8), int64(math.MaxInt8)))
	assert.Equal(t, []int16{math.MinInt16, math.MaxInt16}, conv[int16](t, int64(math.MinInt16), int64(math.MaxInt16)))
	assert.Equal(t, []int32{math.MinInt32, math.MaxInt32}, conv[int32](t, int64(math.MinInt32), int64(math.MaxInt32)))
	assert.Equal(t, []int64{math.MinInt64, math.MaxInt64}, conv[int64](t, int64(math.MinInt64), uint64(math.MaxInt64)))
	assert.Equal(t, []uint8{math.MaxUint8}, conv[uint8](t, uint64(math.MaxUint8)))
	assert.Equal(t, []uint16{math.MaxUint16}, conv[uint16](t, uint64(math.MaxUint16)))
	assert.Equal(t, []uint32{math.MaxUint32}, conv[uint32](t, uint64(math.MaxUint32)))
	assert.Equal(t, []uint64{math.MaxUint64}, conv[uint64](t, uint64(math.MaxUint64)))
}

func TestConvertIntegerOverflow(t *testing.T) {
	convErr[int8](t, int64(128))
	convErr[int8](t, int64(-129))
	convErr[int8](t, uint8(200))
	convErr[int16](t, int32(32768))
	convErr[int16](t, int32(-32769))
	convErr[int32](t, int64(math.MaxInt32+1))
	convErr[int32](t, int64(math.MinInt32-1))
	convErr[int32](t, uint32(math.MaxUint32))
	convErr[int64](t, uint64(math.MaxInt64+1))
	convErr[int64](t, uint64(math.MaxUint64))
	convErr[uint8](t, int64(256))
	convErr[uint8](t, int16(-1))
	convErr[uint16](t, int64(65536))
	convErr[uint16](t, int8(-1))
	convErr[uint32](t, int64(math.MaxUint32+1))
	convErr[uint32](t, int64(-1))
	convErr[uint64](t, int64(-1))
	convErr[uint64](t, int8(math.MinInt8))
}

func TestConvertOverflowReportsElementIndex(t *testing.T) {
	_, err := ConvertValues[int8](vals(int64(1), int64(2), int64(1000)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "element 2")
	assert.Contains(t, err.Error(), "overflows")
}

func TestConvertIntegerFromEnumAndBool(t *testing.T) {
	assert.Equal(t, []int32{3}, conv[int32](t, Enum{Name: "X", Value: 3}))
	assert.Equal(t, []uint8{3}, conv[uint8](t, Enum{Name: "X", Value: 3}))
	convErr[uint8](t, Enum{Name: "X", Value: -3})
	convErr[int8](t, Enum{Value: 300})
	assert.Equal(t, []int32{1, 0}, conv[int32](t, true, false))
}

func TestConvertIntegerFromBadTypes(t *testing.T) {
	convErr[int32](t, "12")
	convErr[int32](t, []byte{1})
	convErr[int32](t, nil)
	convErr[uint32](t, "12")
	convErr[int32](t, ObjectRef{})
	convErr[int32](t, Compound{})
	convErr[int32](t, []any{int32(1)})
}

func TestConvertFloats(t *testing.T) {
	for _, s := range []any{int8(-3), int16(-3), int32(-3), int64(-3)} {
		assert.Equal(t, []float64{-3}, conv[float64](t, s))
		assert.Equal(t, []float32{-3}, conv[float32](t, s))
	}
	for _, s := range []any{uint8(3), uint16(3), uint32(3), uint64(3)} {
		assert.Equal(t, []float64{3}, conv[float64](t, s))
		assert.Equal(t, []float32{3}, conv[float32](t, s))
	}
	assert.Equal(t, []float64{math.MaxUint64}, conv[float64](t, uint64(math.MaxUint64)))
	assert.Equal(t, []float64{1.5}, conv[float64](t, float32(1.5)))
	assert.Equal(t, []float64{2.25}, conv[float64](t, 2.25))
	assert.Equal(t, []float32{1.5}, conv[float32](t, float32(1.5)))
	assert.Equal(t, []float32{2.25}, conv[float32](t, 2.25))
	assert.Equal(t, []float64{7}, conv[float64](t, Enum{Value: 7}))
	convErr[float64](t, "1.5")
	convErr[float32](t, []byte{1})
	convErr[float64](t, nil)
}

func TestConvertFloatSpecials(t *testing.T) {
	out := conv[float64](t, math.Inf(1), math.Inf(-1), math.NaN())
	assert.True(t, math.IsInf(out[0], 1))
	assert.True(t, math.IsInf(out[1], -1))
	assert.True(t, math.IsNaN(out[2]))
	f32 := conv[float32](t, float32(math.NaN()))
	assert.True(t, math.IsNaN(float64(f32[0])))
}

func TestConvertString(t *testing.T) {
	assert.Equal(t, []string{"a", ""}, conv[string](t, "a", ""))
	assert.Equal(t, []string{"hello"}, conv[string](t, []byte("hello")))
	assert.Equal(t, []string{"RED"}, conv[string](t, Enum{Name: "RED", Value: 1}))
	convErr[string](t, int32(5))
	convErr[string](t, 1.5)
	convErr[string](t, nil)
	convErr[string](t, []any{"a"})
}

func TestConvertBytes(t *testing.T) {
	src := []byte{1, 2, 3}
	out := conv[[]byte](t, src)
	assert.Equal(t, [][]byte{{1, 2, 3}}, out)
	assert.Equal(t, [][]byte{[]byte("ab")}, conv[[]byte](t, "ab"))
	assert.Equal(t, [][]byte{{}}, conv[[]byte](t, []byte{}))
	// An array of uint8 (canonical []any) also converts to []byte.
	assert.Equal(t, [][]byte{{4, 5}}, conv[[]byte](t, []any{uint8(4), uint8(5)}))
	convErr[[]byte](t, int32(1))
}

func TestConvertBool(t *testing.T) {
	assert.Equal(t, []bool{false, true}, conv[bool](t, uint8(0), uint8(1)))
	assert.Equal(t, []bool{false, true}, conv[bool](t, int32(0), int64(1)))
	assert.Equal(t, []bool{true, false}, conv[bool](t, true, false))
	assert.Equal(t, []bool{true}, conv[bool](t, Enum{Name: "T", Value: 1}))
	convErr[bool](t, uint8(2))
	convErr[bool](t, int32(-1))
	convErr[bool](t, uint64(1<<40))
	convErr[bool](t, "true")
}

type point struct {
	X int32
	Y float64
}

func TestConvertStructByName(t *testing.T) {
	c := Compound{Fields: []Field{{"X", int64(3)}, {"Y", float32(1.5)}}}
	assert.Equal(t, []point{{3, 1.5}}, conv[point](t, c))
}

func TestConvertStructCaseInsensitive(t *testing.T) {
	c := Compound{Fields: []Field{{"x", int8(3)}, {"y", int8(4)}}}
	assert.Equal(t, []point{{3, 4}}, conv[point](t, c))
}

func TestConvertStructExactBeatsFold(t *testing.T) {
	// Exact match wins even when a case-insensitive match appears earlier.
	c := Compound{Fields: []Field{{"x", int32(1)}, {"X", int32(2)}}}
	assert.Equal(t, []point{{X: 2}}, conv[point](t, c))
}

func TestConvertStructTagPrecedence(t *testing.T) {
	type tagged struct {
		A int32   `hdf5:"b"`
		B int32   `hdf5:"a"`
		C float64 `hdf5:"Gamma"`
	}
	c := Compound{Fields: []Field{{"a", int32(10)}, {"b", int32(20)}, {"gamma", float64(0.5)}}}
	assert.Equal(t, []tagged{{A: 20, B: 10, C: 0.5}}, conv[tagged](t, c))
}

func TestConvertStructSkips(t *testing.T) {
	type s struct {
		Keep    int32
		Skip    int32 `hdf5:"-"`
		Missing int32
		private int32
	}
	c := Compound{Fields: []Field{{"Keep", int32(1)}, {"Skip", int32(2)}, {"private", int32(3)}, {"Extra", int32(4)}}}
	got := conv[s](t, c)
	assert.Equal(t, int32(1), got[0].Keep)
	assert.Equal(t, int32(0), got[0].Skip)
	assert.Equal(t, int32(0), got[0].Missing)
	assert.Equal(t, int32(0), got[0].private)
}

func TestConvertStructNested(t *testing.T) {
	type inner struct{ V int16 }
	type outer struct {
		Name  string
		In    inner
		List  []int32
		Ref   ObjectRef
		Any   any
		Color string
	}
	c := Compound{Fields: []Field{
		{"Name", "n"},
		{"In", Compound{Fields: []Field{{"V", int64(7)}}}},
		{"List", []any{int32(1), int32(2)}},
		{"Ref", ObjectRef{Address: 9}},
		{"Any", uint8(3)},
		{"Color", Enum{Name: "RED", Value: 1}},
	}}
	got := conv[outer](t, c)
	assert.Equal(t, outer{"n", inner{7}, []int32{1, 2}, ObjectRef{Address: 9}, uint8(3), "RED"}, got[0])
}

func TestConvertStructErrors(t *testing.T) {
	_, err := ConvertValues[point](vals(Compound{Fields: []Field{{"X", int64(1 << 40)}}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field X")
	convErr[point](t, int32(1))
	convErr[point](t, []any{int32(1)})
	convErr[point](t, "x")
	convErr[point](t, nil)
}

func TestConvertSlices(t *testing.T) {
	assert.Equal(t, [][]int32{{1, 2, 3}, {}}, conv[[]int32](t, []any{int8(1), uint16(2), int64(3)}, []any{}))
	assert.Equal(t, [][]float64{{1, 2.5}}, conv[[]float64](t, []any{int32(1), float32(2.5)}))
	assert.Equal(t, [][]string{{"a", "b"}}, conv[[]string](t, []any{"a", "b"}))
	nested := []any{[]any{int32(1), int32(2)}, []any{int32(3)}}
	assert.Equal(t, [][][]int64{{{1, 2}, {3}}}, conv[[][]int64](t, nested))
	cs := []any{Compound{Fields: []Field{{"X", int32(1)}}}, Compound{Fields: []Field{{"X", int32(2)}}}}
	assert.Equal(t, [][]point{{{X: 1}, {X: 2}}}, conv[[]point](t, cs))
	assert.Equal(t, [][][]byte{{[]byte("ab"), []byte("c")}}, conv[[][]byte](t, []any{"ab", []byte("c")}))
}

func TestConvertSliceErrors(t *testing.T) {
	convErr[[]int32](t, int32(1))
	convErr[[]int32](t, "abc")
	convErr[[]int32](t, nil)
	convErr[[]int32](t, []any{int32(1), "x"})
	convErr[[]int8](t, []any{int64(1), int64(1000)})
	convErr[[]string](t, []any{int32(1)})
}

func TestConvertIdentity(t *testing.T) {
	or := ObjectRef{Address: 1, Undefined: true}
	rr := RegionRef{Address: 2, Selection: Selection{Type: SelHyperslab, Rank: 1, Blocks: [][2][]uint64{{{0}, {3}}}}}
	ar := AttrRef{Address: 3, Name: "attr"}
	en := Enum{Name: "A", Value: 4}
	co := Compound{Fields: []Field{{"f", int32(1)}, {"g", "s"}}}
	assert.Equal(t, []ObjectRef{or}, conv[ObjectRef](t, or))
	assert.Equal(t, []RegionRef{rr}, conv[RegionRef](t, rr))
	assert.Equal(t, []AttrRef{ar}, conv[AttrRef](t, ar))
	assert.Equal(t, []Enum{en}, conv[Enum](t, en))
	assert.Equal(t, []Compound{co}, conv[Compound](t, co))
	convErr[ObjectRef](t, RegionRef{})
	convErr[RegionRef](t, ObjectRef{})
	convErr[AttrRef](t, ObjectRef{})
	convErr[Enum](t, int32(1))
	convErr[ObjectRef](t, nil)
}

func TestConvertAny(t *testing.T) {
	in := []any{int8(1), uint64(2), float32(3), "s", []byte{1}, Enum{Name: "E"}, ObjectRef{Address: 1},
		Compound{Fields: []Field{{"a", int32(1)}}}, []any{int32(1), []any{"x"}}, nil}
	out := conv[any](t, in...)
	assert.Equal(t, in, out)
	assert.IsType(t, int8(0), out[0])
	assert.IsType(t, uint64(0), out[1])
	assert.IsType(t, float32(0), out[2])
}

func TestConvertSameTypeIdentity(t *testing.T) {
	assert.Equal(t, []int32{5}, conv[int32](t, int32(5)))
	assert.Equal(t, []string{"s"}, conv[string](t, "s"))
	assert.Equal(t, []float64{1.5}, conv[float64](t, float64(1.5)))
}

func TestConvertNamedTypes(t *testing.T) {
	type myInt int16
	type myStr string
	type myF float32
	type myBool bool
	assert.Equal(t, []myInt{3}, conv[myInt](t, int64(3)))
	assert.Equal(t, []myStr{"a", "RED"}, conv[myStr](t, "a", Enum{Name: "RED"}))
	assert.Equal(t, []myF{1.5}, conv[myF](t, float64(1.5)))
	assert.Equal(t, []myBool{true}, conv[myBool](t, uint8(1)))
	convErr[myInt](t, int64(1<<20))
}

func TestConvertInto(t *testing.T) {
	var ints []int32
	require.NoError(t, ConvertInto(vals(int8(1), uint64(2), int64(3)), &ints))
	assert.Equal(t, []int32{1, 2, 3}, ints)

	var fs []float64
	require.NoError(t, ConvertInto(vals(int32(1), float32(2.5)), &fs))
	assert.Equal(t, []float64{1, 2.5}, fs)

	var ss []string
	require.NoError(t, ConvertInto(vals("a", []byte("b")), &ss))
	assert.Equal(t, []string{"a", "b"}, ss)

	var pts []point
	require.NoError(t, ConvertInto(vals(Compound{Fields: []Field{{"x", int8(1)}, {"Y", 2.0}}}), &pts))
	assert.Equal(t, []point{{1, 2}}, pts)

	var nested [][]int16
	require.NoError(t, ConvertInto(vals([]any{int32(1)}, []any{int32(2), int32(3)}), &nested))
	assert.Equal(t, [][]int16{{1}, {2, 3}}, nested)

	var anys []any
	require.NoError(t, ConvertInto(vals(int8(1), "x"), &anys))
	assert.Equal(t, []any{int8(1), "x"}, anys)
}

func TestConvertIntoReplacesDestination(t *testing.T) {
	dst := []int32{9, 9, 9, 9}
	require.NoError(t, ConvertInto(vals(int32(1)), &dst))
	assert.Equal(t, []int32{1}, dst)

	require.NoError(t, ConvertInto(nil, &dst))
	assert.NotNil(t, dst)
	assert.Empty(t, dst)
}

func TestConvertIntoErrorLeavesDestination(t *testing.T) {
	dst := []int8{7, 8}
	err := ConvertInto(vals(int64(1), int64(1000)), &dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "element 1")
	assert.Equal(t, []int8{7, 8}, dst)
}

func TestConvertIntoBadDestination(t *testing.T) {
	v := vals(int32(1))
	var n int32
	var s []int32
	var arr [1]int32
	var pp *[]int32
	pps := &s
	assert.Error(t, ConvertInto(v, nil))
	assert.Error(t, ConvertInto(v, s))
	assert.Error(t, ConvertInto(v, n))
	assert.Error(t, ConvertInto(v, &n))
	assert.Error(t, ConvertInto(v, &arr))
	assert.Error(t, ConvertInto(v, pp))
	assert.Error(t, ConvertInto(v, &pps))
	assert.Error(t, ConvertInto(v, "x"))
	assert.Error(t, ConvertInto(v, map[string]int{}))
	err := ConvertInto(v, &n)
	assert.Contains(t, err.Error(), "pointer to a slice")
}

func TestConvertIntoWrongElementKind(t *testing.T) {
	var ms []map[string]int
	assert.Error(t, ConvertInto(vals(int32(1)), &ms))
	var chs []chan int
	assert.Error(t, ConvertInto(vals(int32(1)), &chs))
	var ps []*int32
	assert.Error(t, ConvertInto(vals(int32(1)), &ps))
	var ss []string
	assert.Error(t, ConvertInto(vals(int32(1)), &ss))
	var ob []ObjectRef
	assert.Error(t, ConvertInto(vals("x"), &ob))
	var fn []func()
	assert.Error(t, ConvertInto(vals(int32(1)), &fn))
}

func TestConvertIntoNamedSlice(t *testing.T) {
	type ints []int16
	var d ints
	require.NoError(t, ConvertInto(vals(int32(4)), &d))
	assert.Equal(t, ints{4}, d)
}

func TestConvertIntoBytes(t *testing.T) {
	var b []byte
	require.NoError(t, ConvertInto(vals(uint8(1), int32(2)), &b))
	assert.Equal(t, []byte{1, 2}, b)
	assert.Error(t, ConvertInto(vals(int32(256)), &b))
}
