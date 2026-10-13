package hdf5

import (
	"encoding/binary"
	"math"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

func convValues(t *testing.T, dt *h5.Datatype, buf []byte) []any {
	t.Helper()
	n := uint64(len(buf)) / dt.Size
	vals, err := h5tDecodeValues(testFile(), dt, buf, n)
	require.NoError(t, err)
	return vals
}

func convOne(t *testing.T, dt *h5.Datatype, elem []byte) any {
	t.Helper()
	return convValues(t, dt, elem)[0]
}

func intType(size uint64, signed bool, order h5.ByteOrder, offset, prec uint32) *h5.Datatype {
	dt := &h5.Datatype{Class: h5.ClassInteger, Size: size, Order: order, Offset: offset, Precision: prec}
	if signed {
		dt.Sign = h5.Sign2
	}
	return dt
}

func floatType(size uint64, order h5.ByteOrder, offset, prec uint32, fi h5.FloatInfo) *h5.Datatype {
	return &h5.Datatype{Class: h5.ClassFloat, Size: size, Order: order, Offset: offset, Precision: prec, Float: fi}
}

var (
	f16LE = floatType(2, h5.OrderLE, 0, 16, h5.FloatInfo{SignPos: 15, ExpPos: 10, ExpSize: 5, MantPos: 0, MantSize: 10, ExpBias: 15, Norm: h5.NormImplied})
	f32LE = floatType(4, h5.OrderLE, 0, 32, h5.FloatInfo{SignPos: 31, ExpPos: 23, ExpSize: 8, MantPos: 0, MantSize: 23, ExpBias: 127, Norm: h5.NormImplied})
	f32BE = floatType(4, h5.OrderBE, 0, 32, h5.FloatInfo{SignPos: 31, ExpPos: 23, ExpSize: 8, MantPos: 0, MantSize: 23, ExpBias: 127, Norm: h5.NormImplied})
	f64BE = floatType(8, h5.OrderBE, 0, 64, h5.FloatInfo{SignPos: 63, ExpPos: 52, ExpSize: 11, MantPos: 0, MantSize: 52, ExpBias: 1023, Norm: h5.NormImplied})
	// IEEE binary128.
	f128LE = floatType(16, h5.OrderLE, 0, 128, h5.FloatInfo{SignPos: 127, ExpPos: 112, ExpSize: 15, MantPos: 0, MantSize: 112, ExpBias: 16383, Norm: h5.NormImplied})
	// x87 80-bit extended precision in 16 bytes (no implied mantissa bit).
	f80LE = floatType(16, h5.OrderLE, 0, 80, h5.FloatInfo{SignPos: 79, ExpPos: 64, ExpSize: 15, MantPos: 0, MantSize: 64, ExpBias: 16383, Norm: h5.NormNone})
	// VAX F-float.
	vaxF = floatType(4, h5.OrderVAX, 0, 32, h5.FloatInfo{SignPos: 31, ExpPos: 23, ExpSize: 8, MantPos: 0, MantSize: 23, ExpBias: 129, Norm: h5.NormImplied})
)

func TestConvFloat16(t *testing.T) {
	le16b := func(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }
	cases := map[uint16]float64{
		0x3c00: 1,
		0xc000: -2,
		0x7bff: 65504,
		0x3555: 0.333251953125,
		0x0001: math.Ldexp(1, -24),        // smallest denormal
		0x03ff: 1023 * math.Ldexp(1, -24), // largest denormal
		0x0000: 0,
		0x7c00: math.Inf(1),
		0xfc00: math.Inf(-1),
	}
	for raw, want := range cases {
		assert.Equal(t, want, convOne(t, f16LE, le16b(raw)), "%#04x", raw)
	}
	v := convOne(t, f16LE, le16b(0x8000)).(float64)
	assert.True(t, v == 0 && math.Signbit(v), "-0")
	assert.True(t, math.IsNaN(convOne(t, f16LE, le16b(0x7e00)).(float64)))
	// Several elements, with the loop-carried state of H5T__conv_f_f.
	assert.Equal(t, []any{1.0, math.Ldexp(1, -24), -2.0}, convValues(t, f16LE, []byte{0x00, 0x3c, 0x01, 0x00, 0x00, 0xc0}))
}

func TestConvFloat32And64(t *testing.T) {
	assert.Equal(t, float32(1.5), convOne(t, f32LE, []byte{0, 0, 0xc0, 0x3f}))
	assert.Equal(t, float32(-0.1), convOne(t, f32BE, binary.BigEndian.AppendUint32(nil, math.Float32bits(-0.1))))
	assert.Equal(t, 0.1, convOne(t, f64BE, binary.BigEndian.AppendUint64(nil, math.Float64bits(0.1))))
	nan := convOne(t, f64BE, binary.BigEndian.AppendUint64(nil, 0x7ff8000000000001)).(float64)
	assert.True(t, math.IsNaN(nan))
}

func TestConvFloat128(t *testing.T) {
	quad := func(hi, lo uint64) []byte {
		return binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, lo), hi)
	}
	assert.Equal(t, 1.0, convOne(t, f128LE, quad(0x3fff000000000000, 0)))
	assert.Equal(t, -1.0/3, convOne(t, f128LE, quad(0xbffd555555555555, 0x5555555555555555)))
	assert.Equal(t, 2.0/3, convOne(t, f128LE, quad(0x3ffe555555555555, 0x5555555555555555)))
	// 1 + 2^-53: H5T__conv_f_f rounds a set first cut-off bit up.
	assert.Equal(t, 1+math.Ldexp(1, -52), convOne(t, f128LE, quad(0x3fff000000000000, 1<<59)))
	// Out of float64 range.
	assert.Equal(t, math.Inf(1), convOne(t, f128LE, quad(0x7ffe000000000000, 0)))
	assert.Equal(t, 0.0, convOne(t, f128LE, quad(0x0001000000000000, 0)))
	// Denormal float64 result: 2^-1070.
	assert.Equal(t, math.Ldexp(1, -1070), convOne(t, f128LE, quad(uint64(16383-1070)<<48, 0)))
	assert.Equal(t, math.Inf(-1), convOne(t, f128LE, quad(0xffff000000000000, 0)))
	assert.True(t, math.IsNaN(convOne(t, f128LE, quad(0x7fff800000000000, 0)).(float64)))
}

func TestConvFloatX87(t *testing.T) {
	x87 := func(sign bool, exp uint16, mant uint64) []byte {
		b := binary.LittleEndian.AppendUint64(nil, mant)
		if sign {
			exp |= 0x8000
		}
		b = binary.LittleEndian.AppendUint16(b, exp)
		return append(b, make([]byte, 6)...)
	}
	assert.Equal(t, 1.0, convOne(t, f80LE, x87(false, 0x3fff, 0x8000000000000000)))
	assert.Equal(t, -2.5, convOne(t, f80LE, x87(true, 0x4000, 0xa000000000000000)))
	assert.Equal(t, 0.1, convOne(t, f80LE, x87(false, 0x3ffb, 0xcccccccccccccccd)))
	assert.Equal(t, math.Inf(1), convOne(t, f80LE, x87(false, 0x7fff, 0x8000000000000000)))
	assert.True(t, math.IsNaN(convOne(t, f80LE, x87(false, 0x7fff, 0xc000000000000000)).(float64)))
	assert.Equal(t, 0.0, convOne(t, f80LE, x87(false, 0, 0)))
}

func TestConvFloatCustomAndVAX(t *testing.T) {
	// A 3-byte float: 1 sign bit, 7 exponent bits (bias 63), 16 mantissa bits.
	f24 := floatType(3, h5.OrderLE, 0, 24, h5.FloatInfo{SignPos: 23, ExpPos: 16, ExpSize: 7, MantPos: 0, MantSize: 16, ExpBias: 63, Norm: h5.NormImplied})
	assert.Equal(t, []any{1.5, -0.75}, convValues(t, f24, []byte{0x00, 0x80, 0x3f, 0x00, 0x80, 0xbe}))

	// The same layout shifted into the upper 3 bytes of a 4-byte,
	// big-endian element with one bytes of padding: not an IEEE single,
	// so it converts to float64.
	f24in32 := floatType(4, h5.OrderBE, 8, 24, h5.FloatInfo{SignPos: 31, ExpPos: 24, ExpSize: 7, MantPos: 8, MantSize: 16, ExpBias: 63, Norm: h5.NormImplied})
	assert.Equal(t, 1.5, convOne(t, f24in32, []byte{0x3f, 0x80, 0x00, 0xaa}))

	// VAX F-float: 16-bit words in little-endian order, high word first.
	assert.Equal(t, []any{1.0, -0.75}, convValues(t, vaxF, []byte{0x80, 0x40, 0x00, 0x00, 0x40, 0xc0, 0x00, 0x00}))

	// Odd-sized VAX floats and MSB-set normalization are not supported.
	_, err := h5tDecodeValues(testFile(), floatType(3, h5.OrderVAX, 0, 24, f24.Float), make([]byte, 3), 1)
	assert.Error(t, err)
	msb := floatType(4, h5.OrderLE, 0, 32, h5.FloatInfo{SignPos: 31, ExpPos: 23, ExpSize: 8, MantPos: 0, MantSize: 23, ExpBias: 127, Norm: h5.NormMSBSet})
	_, err = h5tDecodeValues(testFile(), msb, []byte{0, 0, 0x80, 0x3f}, 1)
	assert.Error(t, err)
}

func TestConvInteger(t *testing.T) {
	assert.Equal(t, int8(-3), convOne(t, intType(1, true, h5.OrderLE, 0, 8), []byte{0xfd}))
	assert.Equal(t, uint8(253), convOne(t, intType(1, false, h5.OrderLE, 0, 8), []byte{0xfd}))
	assert.Equal(t, int16(-2), convOne(t, intType(2, true, h5.OrderBE, 0, 16), []byte{0xff, 0xfe}))
	assert.Equal(t, uint32(0x01020304), convOne(t, intType(4, false, h5.OrderBE, 0, 32), []byte{1, 2, 3, 4}))
	assert.Equal(t, int64(-5), convOne(t, intType(8, true, h5.OrderLE, 0, 64), []byte{0xfb, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}))
	assert.Equal(t, uint64(math.MaxUint64), convOne(t, intType(8, false, h5.OrderLE, 0, 64), []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}))

	// Odd sizes: int64 / uint64, sign extended.
	assert.Equal(t, int64(-2), convOne(t, intType(3, true, h5.OrderLE, 0, 24), []byte{0xfe, 0xff, 0xff}))
	assert.Equal(t, int64(0x7fffff), convOne(t, intType(3, true, h5.OrderLE, 0, 24), []byte{0xff, 0xff, 0x7f}))
	assert.Equal(t, uint64(0x010203), convOne(t, intType(3, false, h5.OrderBE, 0, 24), []byte{1, 2, 3}))
	assert.Equal(t, uint64(256), convOne(t, intType(5, false, h5.OrderBE, 0, 40), []byte{0, 0, 0, 1, 0}))
	assert.Equal(t, int64(-1), convOne(t, intType(7, true, h5.OrderLE, 0, 56), []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}))
	// Larger than 8 bytes: the low 64 bits.
	b16 := append(binary.LittleEndian.AppendUint64(nil, uint64(0xfffffffffffffffb)), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	assert.Equal(t, int64(-5), convOne(t, intType(16, true, h5.OrderLE, 0, 128), b16))

	// Precision and offset: 8 bits at offset 4 of a 2-byte integer.
	i8in16 := intType(2, true, h5.OrderLE, 4, 8)
	assert.Equal(t, []any{int16(-1), int16(127), int16(-128)}, convValues(t, i8in16, []byte{0xf0, 0x0f, 0xf0, 0xa7, 0x0a, 0xf8}))
	// 12-bit unsigned value with garbage in the padding.
	assert.Equal(t, uint16(0x123), convOne(t, intType(2, false, h5.OrderLE, 0, 12), []byte{0x23, 0xf1}))
	// 12-bit signed value, big endian.
	assert.Equal(t, int16(-2048), convOne(t, intType(2, true, h5.OrderBE, 0, 12), []byte{0x08, 0x00}))
}

func TestConvBitfieldOpaqueTime(t *testing.T) {
	bf := &h5.Datatype{Class: h5.ClassBitfield, Size: 2, Order: h5.OrderBE, Offset: 3, Precision: 5}
	assert.Equal(t, []any{uint64(31), uint64(1)}, convValues(t, bf, []byte{0x00, 0xf8, 0xff, 0x0f}))
	assert.Equal(t, uint64(0xfffe), convOne(t, &h5.Datatype{Class: h5.ClassBitfield, Size: 2, Precision: 16}, []byte{0xfe, 0xff}))

	buf := []byte{1, 2, 3, 4}
	vals := convValues(t, &h5.Datatype{Class: h5.ClassOpaque, Size: 2, Order: h5.OrderNone, Tag: "x"}, buf)
	assert.Equal(t, []any{[]byte{1, 2}, []byte{3, 4}}, vals)
	buf[0] = 9
	assert.Equal(t, []byte{1, 2}, vals[0], "copied")
	assert.Equal(t, []byte{1, 2, 3, 4}, convOne(t, &h5.Datatype{Class: h5.ClassTime, Size: 4, Precision: 32}, []byte{1, 2, 3, 4}))
}

func TestConvString(t *testing.T) {
	str := func(pad h5.StrPad, size uint64) *h5.Datatype {
		return &h5.Datatype{Class: h5.ClassString, Size: size, Order: h5.OrderNone, Precision: uint32(8 * size), StrPad: pad}
	}
	assert.Equal(t, []any{"ab", "abcdef"}, convValues(t, str(h5.StrNullTerm, 6), []byte("ab\x00cd\x00abcdef")))
	assert.Equal(t, []any{"ab\x00c", ""}, convValues(t, str(h5.StrNullPad, 6), []byte("ab\x00c\x00\x00\x00\x00\x00\x00\x00\x00")))
	assert.Equal(t, []any{"a b", ""}, convValues(t, str(h5.StrSpacePad, 5), []byte("a b       ")))
	/* A reserved padding value: the bytes as they are (libhdf5 reads the
	 * string without a conversion) */
	assert.Equal(t, []any{"a\x00"}, convValues(t, str(3, 2), []byte("a\x00")))
}

func TestConvEnum(t *testing.T) {
	dt := &h5.Datatype{
		Class: h5.ClassEnum, Size: 2, Order: h5.OrderNone,
		Base:       intType(2, true, h5.OrderBE, 0, 16),
		EnumNames:  []string{"MINUS_ONE", "ZERO", "THREE_HUNDRED"},
		EnumValues: []int64{-1, 0, 300},
	}
	assert.Equal(t, []any{
		h5.Enum{Name: "MINUS_ONE", Value: -1},
		h5.Enum{Name: "THREE_HUNDRED", Value: 300},
		h5.Enum{Name: "", Value: 5},
	}, convValues(t, dt, []byte{0xff, 0xff, 0x01, 0x2c, 0x00, 0x05}))

	// Decoded from a message: the values and the data use the same
	// representation.
	msg := newEnc().hdr(h5.ClassEnum, 3, 2, 1).raw(encInt(1, 1, false, false, 0, 8)).name("A", false).name("B", false).u8(0xff, 7).b
	dt = decodeDtype(t, msg)
	assert.Equal(t, []any{h5.Enum{Name: "B", Value: 7}, h5.Enum{Name: "A", Value: 255}}, convValues(t, dt, []byte{7, 0xff}))
}

func TestConvCompoundArray(t *testing.T) {
	cmp := &h5.Datatype{Class: h5.ClassCompound, Size: 12, Order: h5.OrderNone, Members: []h5.Member{
		{Name: "a", Offset: 8, Type: intType(4, true, h5.OrderLE, 0, 32)},
		{Name: "b", Offset: 0, Type: f64BE},
	}}
	elem := append(binary.BigEndian.AppendUint64(nil, math.Float64bits(2.5)), 0xff, 0xff, 0xff, 0xff)
	assert.Equal(t, h5.Compound{Fields: []h5.Field{{Name: "a", Value: int32(-1)}, {Name: "b", Value: 2.5}}}, convOne(t, cmp, elem))

	arr := &h5.Datatype{Class: h5.ClassArray, Size: 8, Order: h5.OrderNone, ArrayDims: []uint64{2, 2}, Base: intType(2, false, h5.OrderBE, 0, 16)}
	assert.Equal(t, []any{uint16(1), uint16(2), uint16(3), uint16(4)}, convOne(t, arr, []byte{0, 1, 0, 2, 0, 3, 0, 4}))

	// Array of compounds.
	arrCmp := &h5.Datatype{Class: h5.ClassArray, Size: 24, Order: h5.OrderNone, ArrayDims: []uint64{2}, Base: cmp}
	got := convOne(t, arrCmp, append(append([]byte{}, elem...), elem...)).([]any)
	assert.Len(t, got, 2)

	// Corrupt: elements exceed the array size; member outside compound.
	bad := &h5.Datatype{Class: h5.ClassArray, Size: 8, ArrayDims: []uint64{5}, Base: intType(2, false, h5.OrderLE, 0, 16)}
	_, err := h5tDecodeValues(testFile(), bad, make([]byte, 8), 1)
	assert.Error(t, err)
	badCmp := &h5.Datatype{Class: h5.ClassCompound, Size: 4, Members: []h5.Member{{Name: "x", Offset: 2, Type: intType(4, false, h5.OrderLE, 0, 32)}}}
	_, err = h5tDecodeValues(testFile(), badCmp, make([]byte, 4), 1)
	assert.Error(t, err)
}

func TestConvErrors(t *testing.T) {
	// Buffer too small.
	_, err := h5tDecodeValues(testFile(), intType(4, true, h5.OrderLE, 0, 32), make([]byte, 7), 2)
	assert.Error(t, err)
	// Vlen element smaller than a disk sequence.
	vl := &h5.Datatype{Class: h5.ClassVlen, Size: 8, Base: intType(1, false, h5.OrderLE, 0, 8)}
	_, err = h5tDecodeValues(testFile(), vl, make([]byte, 8), 1)
	assert.Error(t, err)
	// Unknown class.
	_, err = h5tDecodeValues(testFile(), &h5.Datatype{Class: 11, Size: 1}, make([]byte, 1), 1)
	assert.Error(t, err)
	// Null vlen sequences and strings need no heap.
	vl.Size = 16
	assert.Equal(t, []any{}, convOne(t, vl, make([]byte, 16)))
	vl.VlenKind = h5.VlenString
	assert.Equal(t, "", convOne(t, vl, make([]byte, 16)))
}

func TestConvReferencesNoHeap(t *testing.T) {
	obj1 := &h5.Datatype{Class: h5.ClassReference, Size: 8, Order: h5.OrderNone, RefType: h5.RefObject1}
	assert.Equal(t, []any{
		h5.ObjectRef{Undefined: true},
		h5.ObjectRef{Address: 0x10},
		h5.ObjectRef{Address: haddrUndef, Undefined: true},
	}, convValues(t, obj1, []byte{
		0, 0, 0, 0, 0, 0, 0, 0,
		0x10, 0, 0, 0, 0, 0, 0, 0,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	}))

	// Revised object reference to an object in the same file: stored
	// directly (type, flags, token size, token).
	ref := &h5.Datatype{Class: h5.ClassReference, Size: 18, Order: h5.OrderNone, RefType: h5.RefObject2}
	direct := append([]byte{2, 0, 8}, binary.LittleEndian.AppendUint64(nil, 0x728)...)
	direct = append(direct, make([]byte, 18-len(direct))...)
	assert.Equal(t, []any{h5.ObjectRef{Address: 0x728}, h5.ObjectRef{Undefined: true}}, convValues(t, ref, append(direct, make([]byte, 18)...)))

	// Null references of each revised kind.
	ref.RefType = h5.RefAttr
	assert.Equal(t, h5.AttrRef{Undefined: true}, convOne(t, ref, make([]byte, 18)))
	ref.RefType = h5.RefRegion2
	assert.Equal(t, h5.RegionRef{Undefined: true}, convOne(t, ref, make([]byte, 18)))
	reg1 := &h5.Datatype{Class: h5.ClassReference, Size: 12, Order: h5.OrderNone, RefType: h5.RefRegion1}
	assert.Equal(t, h5.RegionRef{Undefined: true}, convOne(t, reg1, make([]byte, 12)))

	// Invalid encoded reference type.
	bad := append([]byte{7, 0, 8}, make([]byte, 15)...)
	_, err := h5tDecodeValues(testFile(), ref, bad, 1)
	assert.Error(t, err)
	// Token size larger than H5O_token_t.
	bad = append([]byte{2, 0, 17}, make([]byte, 15)...)
	_, err = h5tDecodeValues(testFile(), ref, bad, 1)
	assert.Error(t, err)
}

func TestRegionSelectionPublic(t *testing.T) {
	sel := &h5sSelection{typ: h5sSelHyperslabs, rank: 2, regular: true,
		start: []uint64{1, 0}, stride: []uint64{4, 3}, count: []uint64{2, 2}, block: []uint64{2, 1}}
	got, err := h5rSelectionPublic(sel, 0)
	require.NoError(t, err)
	assert.Equal(t, h5.Selection{Type: h5.SelHyperslab, Rank: 2, Blocks: [][2][]uint64{
		{{1, 0}, {2, 0}}, {{1, 3}, {2, 3}}, {{5, 0}, {6, 0}}, {{5, 3}, {6, 3}},
	}}, got)

	got, err = h5rSelectionPublic(&h5sSelection{typ: h5sSelPoints, rank: 1, points: [][]uint64{{3}, {1}}}, 0)
	require.NoError(t, err)
	assert.Equal(t, h5.Selection{Type: h5.SelPoints, Rank: 1, Points: [][]uint64{{3}, {1}}}, got)

	got, err = h5rSelectionPublic(&h5sSelection{typ: h5sSelAll}, 3)
	require.NoError(t, err)
	assert.Equal(t, h5.Selection{Type: h5.SelAll, Rank: 3}, got)

	_, err = h5rSelectionPublic(&h5sSelection{typ: h5sSelHyperslabs, regular: true, start: []uint64{0}, count: []uint64{1 << 40}}, 1)
	assert.Error(t, err)
}

// corpusValues decodes n elements stored contiguously at dataAddr with the
// datatype message at dtAddr (locations obtained with h5debug).
func corpusValues(t *testing.T, name string, dtAddr uint64, dtLen int, dataAddr, n uint64) (*h5.Datatype, []any) {
	t.Helper()
	f, dt := corpusDtype(t, name, dtAddr, dtLen)
	data, err := f.readAt(dataAddr, int(n*dt.Size))
	require.NoError(t, err)
	vals, err := h5tDecodeValues(f, dt, data, n)
	require.NoError(t, err)
	return dt, vals
}

// Expected values are those printed by h5dump (tools/test/h5dump/expected).
func TestConvCorpus(t *testing.T) {
	_, vals := corpusValues(t, "tvlstr.h5", 840, 24, 2048, 4)
	assert.Equal(t, []any{
		"Four score and seven years ago our forefathers brought forth on this continent a new nation,",
		"conceived in liberty and dedicated to the proposition that all men are created equal.",
		"", "", // "" and NULL
	}, vals)

	_, vals = corpusValues(t, "tvldtypes1.h5", 1000, 24, 2048, 4)
	assert.Equal(t, []any{
		[]any{int32(0)}, []any{int32(10), int32(11)}, []any{int32(20), int32(21), int32(22)},
		[]any{int32(30), int32(31), int32(32), int32(33)},
	}, vals)
	_, vals = corpusValues(t, "tvldtypes1.h5", 1600, 32, 6208, 4)
	assert.Equal(t, []any{float32(30), float32(30.1), float32(30.2), float32(30.3)}, vals[3])
	_, vals = corpusValues(t, "tvldtypes2.h5", 1000, 32, 2048, 1)
	assert.Equal(t, []any{[]any{[]any{uint32(0)}}}, vals)
	_, vals = corpusValues(t, "tarray6.h5", 1000, 40, 2048, 1)
	assert.Equal(t, []any{[]any{uint32(0)}, []any{uint32(10), uint32(11)}, []any{uint32(20), uint32(21), uint32(22)}, []any{uint32(30), uint32(31), uint32(32), uint32(33)}}, vals[0])

	_, vals = corpusValues(t, "tcompound.h5", 768, 184, 1016, 5)
	assert.Equal(t, h5.Compound{Fields: []h5.Field{{Name: "a_name", Value: int32(2)}, {Name: "b_name", Value: float32(4)}, {Name: "c_name", Value: 1.0 / 3}}}, vals[2])

	_, vals = corpusValues(t, "tarray1.h5", 1000, 32, 2048, 4)
	assert.Equal(t, []any{int32(30), int32(31), int32(32), int32(33)}, vals[3])

	for _, name := range []string{"tfloat16.h5", "tfloat16_be.h5"} {
		_, vals = corpusValues(t, name, 872, 24, 2048, 8)
		assert.Equal(t, []any{16.0, 0.5, 1.0, 1.5, 2.0, 2.5, 3.0, 3.5}, vals, name)
	}

	_, vals = corpusValues(t, "tldouble.h5", 856, 24, 2144, 3)
	assert.Equal(t, []any{1.0, 2.0, 3.0}, vals)

	_, vals = corpusValues(t, "t128bit_float.h5", 872, 24, 2144, 1)
	assert.InDelta(t, 1.123456789012345, vals[0], 1e-15)

	_, vals = corpusValues(t, "tvms.h5", 832, 24, 2048, 12)
	assert.Equal(t, []any{0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0}, vals)

	_, vals = corpusValues(t, "tfpformat.h5", 1440, 24, 2096, 2)
	assert.Equal(t, []any{float32(-0.1234567), float32(0.1234567)}, vals)

	_, vals = corpusValues(t, "tcmpdints.h5", 856, 560, 4192, 2)
	assert.Equal(t, h5.Compound{Fields: []h5.Field{
		{Name: "DU08BITS", Value: uint8(0xfe)}, {Name: "DU16BITS", Value: uint16(0xfffe)},
		{Name: "DU32BITS", Value: uint32(0xfffffffe)}, {Name: "DU64BITS", Value: uint64(0xfffffffffffffffe)},
		{Name: "DS08BITS", Value: int8(-2)}, {Name: "DS16BITS", Value: int16(-2)},
		{Name: "DS32BITS", Value: int32(-2)}, {Name: "DS64BITS", Value: int64(-2)},
		{Name: "DummyDBL", Value: 1.0001},
	}}, vals[1])

	_, vals = corpusValues(t, "tbitnopaque.h5", 1888, 16, 2432, 4)
	assert.Equal(t, []any{uint64(0xff), uint64(0xfe), uint64(0xfd), uint64(0xfc)}, vals)
	_, vals = corpusValues(t, "tbitnopaque.h5", 6056, 32, 2912, 2)
	assert.Equal(t, []any{[]byte{0xff}, []byte{0xfe}}, vals)
}

func TestConvCorpusReferences(t *testing.T) {
	// trefer_obj.h5: references to /Group1/Dataset1 (object header 0x728)
	// of each kind.
	_, vals := corpusValues(t, "trefer_obj.h5", 5008, 8, 2436, 1) // H5T_STD_REF object
	assert.Equal(t, []any{h5.ObjectRef{Address: 0x728}}, vals)
	_, vals = corpusValues(t, "trefer_obj.h5", 5280, 8, 2454, 1) // H5T_STD_REF_OBJ
	assert.Equal(t, []any{h5.ObjectRef{Address: 0x728}}, vals)
	_, vals = corpusValues(t, "trefer_obj.h5", 5552, 8, 2462, 1) // H5T_STD_REF region
	require.IsType(t, h5.RegionRef{}, vals[0])
	assert.Equal(t, uint64(0x728), vals[0].(h5.RegionRef).Address)
	_, vals = corpusValues(t, "trefer_obj.h5", 5824, 8, 2480, 1) // H5T_STD_REF_DSETREG
	require.IsType(t, h5.RegionRef{}, vals[0])
	assert.Equal(t, uint64(0x728), vals[0].(h5.RegionRef).Address)

	// trefer_attr.h5 /Dataset3: attribute references.
	_, vals = corpusValues(t, "trefer_attr.h5", 3144, 8, 3360, 4)
	assert.Equal(t, []any{
		h5.AttrRef{Address: 0x790, Name: "Attr1"}, h5.AttrRef{Address: 0x9e8, Name: "Attr1"},
		h5.AttrRef{Address: 0x320, Name: "Attr2"}, h5.AttrRef{Address: 0xaf8, Name: "Attr3"},
	}, vals)

	// trefer_compat.h5: old-style object and region references, the last
	// one null.
	_, vals = corpusValues(t, "trefer_compat.h5", 2960, 8, 3176, 4)
	assert.Equal(t, []any{h5.ObjectRef{Address: 0x728}, h5.ObjectRef{Address: 0x980}, h5.ObjectRef{Address: 0x320}, h5.ObjectRef{Address: 0xa90}}, vals)
	_, vals = corpusValues(t, "trefer_compat.h5", 5280, 8, 3208, 3)
	assert.Equal(t, uint64(0x728), vals[0].(h5.RegionRef).Address)
	assert.Equal(t, uint64(0x980), vals[1].(h5.RegionRef).Address)
	assert.True(t, vals[2].(h5.RegionRef).Undefined)

	// tdatareg.h5 /Dataset1: region references to /Dataset2 (744).
	_, vals = corpusValues(t, "tdatareg.h5", 1308, 8, 1396, 2)
	for _, v := range vals {
		assert.Equal(t, uint64(744), v.(h5.RegionRef).Address)
	}

	// trefer_reg.h5 /Dataset1: revised region references to /Dataset2.
	_, vals = corpusValues(t, "trefer_reg.h5", 531, 8, 2148, 4)
	for _, v := range vals {
		assert.Equal(t, uint64(195), v.(h5.RegionRef).Address)
	}
}

// Converting arbitrary bytes with any decodable datatype must never panic
// (vlen and reference lookups go to a real file's global heap).
func TestConvNoPanic(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "tools/test/h5dump/testfiles/tvlstr.h5"))
	require.NoError(t, err)
	defer f.Close()
	rng := rand.New(rand.NewSource(2))
	msgs := validDtypeMessages()
	msgs = append(msgs,
		newEnc().hdr(h5.ClassReference, 1, 0, 8).b,
		newEnc().hdr(h5.ClassReference, 1, 1, 12).b,
		newEnc().hdr(h5.ClassReference, 4, 2|1<<4, 18).b,
		newEnc().hdr(h5.ClassReference, 4, 3|1<<4, 18).b,
		newEnc().hdr(h5.ClassVlen, 1, 0, 16).raw(encInt(1, 2, false, false, 0, 16)).b,
		newEnc().hdr(h5.ClassFloat, 1, 0x20|79<<8, 16).u16(0).u16(80).u8(64, 15, 0, 64).u32(16383).b,
		newEnc().hdr(h5.ClassFloat, 1, 79<<8, 16).u16(0).u16(80).u8(64, 15, 0, 64).u32(16383).b,
	)
	for _, msg := range msgs {
		for k := 0; k < 300; k++ {
			b := append([]byte(nil), msg...)
			if k > 0 {
				for j := 0; j < 1+rng.Intn(3); j++ {
					b[rng.Intn(len(b))] = byte(rng.Intn(256))
				}
			}
			dt, err := h5oDtypeDecode(f, f.newImage(b))
			if err != nil || dt.Size > 1<<16 {
				continue
			}
			data := make([]byte, 4*dt.Size)
			for i := range data {
				switch rng.Intn(3) {
				case 0:
					data[i] = byte(rng.Intn(256))
				case 1:
					data[i] = 0xff
				}
			}
			assert.NotPanics(t, func() { _, _ = h5tDecodeValues(f, dt, data, 4) }, "%x", b)
		}
	}
}
