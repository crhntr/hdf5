package hdf5

import (
	"encoding/binary"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HDFGroup/hdf5/go/h5"
)

// enc builds hand-encoded datatype messages following the "Datatype
// Message" section of the file format specification.
type enc struct{ b []byte }

func (e *enc) u8(v ...uint8) *enc { e.b = append(e.b, v...); return e }
func (e *enc) u16(v uint16) *enc  { e.b = binary.LittleEndian.AppendUint16(e.b, v); return e }
func (e *enc) u32(v uint32) *enc  { e.b = binary.LittleEndian.AppendUint32(e.b, v); return e }
func (e *enc) raw(b []byte) *enc  { e.b = append(e.b, b...); return e }

// hdr appends the 8-byte header: class and version, 24 bits of class
// flags, size.
func (e *enc) hdr(class h5.Class, version uint8, flags uint32, size uint32) *enc {
	return e.u32(uint32(class) | uint32(version)<<4 | flags<<8).u32(size)
}

// name appends a NUL-terminated name, padded to a multiple of 8 bytes when
// pad is set (datatype message versions 1 and 2).
func (e *enc) name(s string, pad bool) *enc {
	e.b = append(e.b, s...)
	e.b = append(e.b, 0)
	if pad {
		for (len(s)+1)%8 != 0 {
			e.b = append(e.b, 0)
			s += "x"
		}
	}
	return e
}

func newEnc() *enc { return &enc{} }

// Atomic member encodings used below.
func encInt(version uint8, size uint32, signed, be bool, offset, prec uint16) []byte {
	var flags uint32
	if be {
		flags |= 0x1
	}
	if signed {
		flags |= 0x8
	}
	return newEnc().hdr(h5.ClassInteger, version, flags, size).u16(offset).u16(prec).b
}

func encF32LE(version uint8) []byte {
	return newEnc().hdr(h5.ClassFloat, version, 0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(127).b
}

// testFile is a File with the given address and length sizes, for decoding
// buffers that are not backed by a file.
func testFile() *File { return &File{sizeofAddr: 8, sizeofSize: 8} }

func decodeDtype(t *testing.T, msg []byte) *h5.Datatype {
	t.Helper()
	f := testFile()
	p := f.newImage(msg)
	dt, err := h5oDtypeDecode(f, p)
	require.NoError(t, err)
	assert.Equal(t, len(msg), p.off, "whole message consumed")
	return dt
}

func decodeDtypeErr(t *testing.T, msg []byte) error {
	t.Helper()
	f := testFile()
	_, err := h5oDtypeDecode(f, f.newImage(msg))
	require.Error(t, err)
	return err
}

func TestDtypeDecodeInteger(t *testing.T) {
	// Big-endian, LSB pad one, signed, offset 2, precision 12, 2 bytes.
	msg := newEnc().hdr(h5.ClassInteger, 1, 0x1|0x2|0x8, 2).u16(2).u16(12).b
	dt := decodeDtype(t, msg)
	assert.Equal(t, &h5.Datatype{
		Class: h5.ClassInteger, Version: 1, Size: 2, Order: h5.OrderBE,
		Precision: 12, Offset: 2, LSBPad: h5.PadOne, MSBPad: h5.PadZero, Sign: h5.Sign2,
	}, dt)

	dt = decodeDtype(t, encInt(1, 8, false, false, 0, 64))
	assert.Equal(t, h5.OrderLE, dt.Order)
	assert.Equal(t, h5.SignNone, dt.Sign)
	assert.Equal(t, uint32(64), dt.Precision)

	// Sanity checks.
	decodeDtypeErr(t, encInt(1, 2, true, false, 16, 1))                       // offset out of bounds
	decodeDtypeErr(t, encInt(1, 2, true, false, 0, 0))                        // precision is zero
	decodeDtypeErr(t, encInt(1, 2, true, false, 8, 9))                        // offset+precision out of bounds
	decodeDtypeErr(t, newEnc().hdr(h5.ClassInteger, 1, 0, 0).u16(0).u16(8).b) // size zero
}

func TestDtypeDecodeUnusualUnusedBits(t *testing.T) {
	// 4-byte integer using 8 bits: rejected unless the object header is
	// checksummed (or the check is relaxed).
	msg := encInt(1, 4, true, false, 0, 8)
	f := testFile()
	_, err := h5oDtypeDecodeIO(f, f.newImage(msg), 0)
	require.Error(t, err)
	dt, err := h5oDtypeDecodeIO(f, f.newImage(msg), h5oDecodeioRficUnubnt)
	require.NoError(t, err)
	assert.Equal(t, uint32(8), dt.Precision)
	// h5oDtypeDecode relaxes the check (no object header, as in the C code).
	decodeDtype(t, msg)
	// 2-byte integer using 12 bits is not unusual.
	_, err = h5oDtypeDecodeIO(f, f.newImage(encInt(1, 2, true, false, 0, 12)), 0)
	require.NoError(t, err)
}

func TestDtypeDecodeFloat(t *testing.T) {
	// IEEE double, little endian.
	msg := newEnc().hdr(h5.ClassFloat, 1, 0x20|63<<8, 8).u16(0).u16(64).u8(52, 11, 0, 52).u32(1023).b
	dt := decodeDtype(t, msg)
	assert.Equal(t, &h5.Datatype{
		Class: h5.ClassFloat, Version: 1, Size: 8, Order: h5.OrderLE, Precision: 64,
		Float: h5.FloatInfo{SignPos: 63, ExpPos: 52, ExpSize: 11, MantPos: 0, MantSize: 52, ExpBias: 1023, Norm: h5.NormImplied},
	}, dt)

	// All the bit-field flags: BE, LSB pad 1, MSB pad 1, internal pad 1,
	// MSB-set normalization.
	msg = newEnc().hdr(h5.ClassFloat, 1, 0x1|0x2|0x4|0x8|0x10|30<<8, 4).u16(1).u16(30).u8(22, 8, 1, 21).u32(100).b
	dt = decodeDtype(t, msg)
	assert.Equal(t, h5.OrderBE, dt.Order)
	assert.Equal(t, h5.PadOne, dt.LSBPad)
	assert.Equal(t, h5.PadOne, dt.MSBPad)
	assert.Equal(t, h5.PadOne, dt.Float.IntPad)
	assert.Equal(t, h5.NormMSBSet, dt.Float.Norm)
	assert.Equal(t, h5.FloatInfo{SignPos: 30, ExpPos: 22, ExpSize: 8, MantPos: 1, MantSize: 21, ExpBias: 100, Norm: h5.NormMSBSet, IntPad: h5.PadOne}, dt.Float)
	assert.Equal(t, uint32(1), dt.Offset)
	assert.Equal(t, uint32(30), dt.Precision)

	// No normalization.
	msg = newEnc().hdr(h5.ClassFloat, 1, 79<<8, 16).u16(0).u16(80).u8(64, 15, 0, 64).u32(16383).b
	assert.Equal(t, h5.NormNone, decodeDtype(t, msg).Float.Norm)

	// VAX order (version 3, bits 0 and 6).
	msg = newEnc().hdr(h5.ClassFloat, 3, 0x41|0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(129).b
	dt = decodeDtype(t, msg)
	assert.Equal(t, h5.OrderVAX, dt.Order)
	assert.Equal(t, uint8(3), dt.Version)

	// Bit 6 without bit 0 is unsupported in version 3, ignored before.
	decodeDtypeErr(t, newEnc().hdr(h5.ClassFloat, 3, 0x40|0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(127).b)
	dt = decodeDtype(t, newEnc().hdr(h5.ClassFloat, 2, 0x41|0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(127).b)
	assert.Equal(t, h5.OrderBE, dt.Order)

	// Invalid layouts.
	bad := [][]byte{
		newEnc().hdr(h5.ClassFloat, 1, 0x30|31<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(127).b,  // normalization 3
		newEnc().hdr(h5.ClassFloat, 1, 0x20|32<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(127).b,  // sign out of bounds
		newEnc().hdr(h5.ClassFloat, 1, 0x20|31<<8, 4).u16(0).u16(32).u8(23, 0, 0, 23).u32(127).b,  // exponent size 0
		newEnc().hdr(h5.ClassFloat, 1, 0x20|31<<8, 4).u16(0).u16(32).u8(32, 1, 0, 23).u32(127).b,  // exponent position
		newEnc().hdr(h5.ClassFloat, 1, 0x20|31<<8, 4).u16(0).u16(32).u8(25, 8, 0, 23).u32(127).b,  // exponent range
		newEnc().hdr(h5.ClassFloat, 1, 0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 0, 0).u32(127).b,   // mantissa size 0
		newEnc().hdr(h5.ClassFloat, 1, 0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 32, 1).u32(127).b,  // mantissa position
		newEnc().hdr(h5.ClassFloat, 1, 0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 10, 23).u32(127).b, // mantissa range
		newEnc().hdr(h5.ClassFloat, 1, 0x20|30<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(127).b,  // sign/exponent overlap
		newEnc().hdr(h5.ClassFloat, 1, 0x20|22<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(127).b,  // sign/mantissa overlap
		newEnc().hdr(h5.ClassFloat, 1, 0x20|31<<8, 4).u16(0).u16(32).u8(22, 8, 0, 23).u32(127).b,  // exponent/mantissa overlap
	}
	for i, msg := range bad {
		f := testFile()
		_, err := h5oDtypeDecode(f, f.newImage(msg))
		assert.Error(t, err, "case %d", i)
	}
}

func TestDtypeDecodeTimeStringBitfieldOpaque(t *testing.T) {
	dt := decodeDtype(t, newEnc().hdr(h5.ClassTime, 1, 0x1, 4).u16(32).b)
	assert.Equal(t, &h5.Datatype{Class: h5.ClassTime, Version: 1, Size: 4, Order: h5.OrderBE, Precision: 32}, dt)

	// Space padded UTF-8 string.
	dt = decodeDtype(t, newEnc().hdr(h5.ClassString, 1, 0x2|0x1<<4, 10).b)
	assert.Equal(t, &h5.Datatype{
		Class: h5.ClassString, Version: 1, Size: 10, Order: h5.OrderNone, Precision: 80,
		StrPad: h5.StrSpacePad, Cset: h5.CsetUTF8,
	}, dt)

	dt = decodeDtype(t, newEnc().hdr(h5.ClassBitfield, 1, 0x1|0x4, 2).u16(3).u16(5).b)
	assert.Equal(t, &h5.Datatype{
		Class: h5.ClassBitfield, Version: 1, Size: 2, Order: h5.OrderBE, Precision: 5, Offset: 3, MSBPad: h5.PadOne,
	}, dt)

	// Opaque: the tag is NUL padded to a multiple of 8 bytes.
	dt = decodeDtype(t, newEnc().hdr(h5.ClassOpaque, 1, 16, 3).raw([]byte("hello world\x00\x00\x00\x00\x00")).b)
	assert.Equal(t, &h5.Datatype{Class: h5.ClassOpaque, Version: 1, Size: 3, Order: h5.OrderNone, Tag: "hello world"}, dt)
	dt = decodeDtype(t, newEnc().hdr(h5.ClassOpaque, 1, 0, 3).b)
	assert.Equal(t, "", dt.Tag)
	decodeDtypeErr(t, newEnc().hdr(h5.ClassOpaque, 1, 12, 3).raw(make([]byte, 12)).b) // not aligned
}

func TestDtypeDecodeCompoundV1(t *testing.T) {
	// Version 1 compound with an intrinsic array member: the array member
	// becomes an H5T_ARRAY type (version 2) and the compound is upgraded.
	msg := newEnc().hdr(h5.ClassCompound, 1, 2, 16).
		name("a", true).u32(0).u8(0, 0, 0, 0).u32(0).u32(0).u32(0).u32(0).u32(0).u32(0).raw(encInt(1, 4, true, false, 0, 32)).
		name("arr_member", true).u32(4).u8(2, 0, 0, 0).u32(0).u32(0).u32(2).u32(3).u32(0).u32(0).raw(encInt(1, 2, false, true, 0, 16)).b
	dt := decodeDtype(t, msg)
	require.Len(t, dt.Members, 2)
	assert.Equal(t, uint8(2), dt.Version)
	assert.Equal(t, h5.OrderNone, dt.Order)
	assert.Equal(t, "a", dt.Members[0].Name)
	assert.Equal(t, uint64(0), dt.Members[0].Offset)
	assert.Equal(t, h5.ClassInteger, dt.Members[0].Type.Class)
	assert.Equal(t, uint8(2), dt.Members[0].Type.Version, "upgraded with the compound")
	m := dt.Members[1]
	assert.Equal(t, "arr_member", m.Name)
	assert.Equal(t, uint64(4), m.Offset)
	assert.Equal(t, h5.ClassArray, m.Type.Class)
	assert.Equal(t, []uint64{2, 3}, m.Type.ArrayDims)
	assert.Equal(t, uint64(12), m.Type.Size)
	assert.Equal(t, uint8(2), m.Type.Version)
	assert.Equal(t, h5.OrderBE, m.Type.Base.Order)

	// Zero-sized dimension, too many dimensions.
	decodeDtypeErr(t, newEnc().hdr(h5.ClassCompound, 1, 1, 16).
		name("a", true).u32(0).u8(1, 0, 0, 0).u32(0).u32(0).u32(0).u32(0).u32(0).u32(0).raw(encInt(1, 4, true, false, 0, 32)).b)
	decodeDtypeErr(t, newEnc().hdr(h5.ClassCompound, 1, 1, 16).
		name("a", true).u32(0).u8(5, 0, 0, 0).u32(0).u32(0).u32(1).u32(1).u32(1).u32(1).raw(encInt(1, 4, true, false, 0, 32)).b)
}

func TestDtypeDecodeCompoundV2V3(t *testing.T) {
	arr := newEnc().hdr(h5.ClassArray, 2, 0, 8).u8(1, 0, 0, 0).u32(2).u32(0).raw(encF32LE(1)).b
	msg := newEnc().hdr(h5.ClassCompound, 2, 2, 12).
		name("count", true).u32(0).raw(encInt(1, 4, false, false, 0, 32)).
		name("values", true).u32(4).raw(arr).b
	dt := decodeDtype(t, msg)
	assert.Equal(t, uint8(2), dt.Version)
	require.Len(t, dt.Members, 2)
	assert.Equal(t, "values", dt.Members[1].Name)
	assert.Equal(t, []uint64{2}, dt.Members[1].Type.ArrayDims)
	assert.Equal(t, h5.ClassFloat, dt.Members[1].Type.Base.Class)

	// Version 3: unpadded names and offsets in the minimum number of bytes
	// (2 bytes for a 300-byte compound).
	msg = newEnc().hdr(h5.ClassCompound, 3, 2, 300).
		name("x", false).u8(0, 0).raw(encInt(1, 1, true, false, 0, 8)).
		name("yy", false).u16(296).raw(encInt(3, 4, true, true, 0, 32)).b
	dt = decodeDtype(t, msg)
	assert.Equal(t, uint8(3), dt.Version)
	require.Len(t, dt.Members, 2)
	assert.Equal(t, h5.Member{Name: "x", Offset: 0, Type: &h5.Datatype{Class: h5.ClassInteger, Version: 1, Size: 1, Order: h5.OrderLE, Precision: 8, Sign: h5.Sign2}}, dt.Members[0])
	assert.Equal(t, "yy", dt.Members[1].Name)
	assert.Equal(t, uint64(296), dt.Members[1].Offset)

	// Member version greater than the compound's: the compound is upgraded.
	msg = newEnc().hdr(h5.ClassCompound, 1, 1, 4).name("a", true).u32(0).u8(0, 0, 0, 0).u32(0).u32(0).u32(0).u32(0).u32(0).u32(0).raw(encInt(3, 4, true, false, 0, 32)).b
	assert.Equal(t, uint8(3), decodeDtype(t, msg).Version)

	// Invalid compounds.
	bad := [][]byte{
		newEnc().hdr(h5.ClassCompound, 3, 0, 4).b,                                                              // no members
		newEnc().hdr(h5.ClassCompound, 3, 1, 4).name("a", false).u8(1).raw(encInt(1, 4, true, false, 0, 32)).b, // extends outside
		newEnc().hdr(h5.ClassCompound, 3, 2, 8).name("a", false).u8(0).raw(encInt(1, 4, true, false, 0, 32)).name("a", false).u8(4).raw(encInt(1, 4, true, false, 0, 32)).b, // duplicate
		newEnc().hdr(h5.ClassCompound, 3, 2, 8).name("a", false).u8(0).raw(encInt(1, 4, true, false, 0, 32)).name("b", false).u8(2).raw(encInt(1, 4, true, false, 0, 32)).b, // overlap
		newEnc().hdr(h5.ClassCompound, 3, 0xffff, 8).name("a", false).u8(0).raw(encInt(1, 4, true, false, 0, 32)).b,                                                         // nmembs > buffer
		newEnc().hdr(h5.ClassCompound, 3, 1, 8).raw([]byte("abc")).b,                                                                                                        // name not terminated
	}
	for i, msg := range bad {
		f := testFile()
		_, err := h5oDtypeDecode(f, f.newImage(msg))
		assert.Error(t, err, "case %d", i)
	}
}

func TestDtypeDecodeReference(t *testing.T) {
	dt := decodeDtype(t, newEnc().hdr(h5.ClassReference, 1, 0, 8).b)
	assert.Equal(t, &h5.Datatype{Class: h5.ClassReference, Version: 1, Size: 8, Order: h5.OrderNone, Precision: 64, RefType: h5.RefObject1}, dt)
	assert.Equal(t, h5.RefRegion1, decodeDtype(t, newEnc().hdr(h5.ClassReference, 1, 1, 12).b).RefType)

	// Version 4: revised references carry the encoding version in bits 4-7.
	assert.Equal(t, h5.RefObject2, decodeDtype(t, newEnc().hdr(h5.ClassReference, 4, 2|1<<4, 18).b).RefType)
	assert.Equal(t, h5.RefRegion2, decodeDtype(t, newEnc().hdr(h5.ClassReference, 4, 3|1<<4, 18).b).RefType)
	assert.Equal(t, h5.RefAttr, decodeDtype(t, newEnc().hdr(h5.ClassReference, 4, 4|1<<4, 18).b).RefType)
	decodeDtypeErr(t, newEnc().hdr(h5.ClassReference, 4, 2|2<<4, 18).b) // reference version mismatch
	decodeDtypeErr(t, newEnc().hdr(h5.ClassReference, 4, 2, 18).b)      // reference version 0
	decodeDtypeErr(t, newEnc().hdr(h5.ClassReference, 4, 5|1<<4, 18).b) // H5R_MAXTYPE
}

func TestDtypeDecodeEnum(t *testing.T) {
	values := []byte{0xff, 0xff, 0x00, 0x00, 0x01, 0x2c} // -1, 0, 300 as big-endian int16
	msg := newEnc().hdr(h5.ClassEnum, 1, 3, 2).raw(encInt(1, 2, true, true, 0, 16)).
		name("MINUS_ONE", true).name("ZERO", true).name("THREE_HUNDRED", true).raw(values).b
	dt := decodeDtype(t, msg)
	assert.Equal(t, []string{"MINUS_ONE", "ZERO", "THREE_HUNDRED"}, dt.EnumNames)
	assert.Equal(t, []int64{-1, 0, 300}, dt.EnumValues)
	assert.Equal(t, h5.OrderNone, dt.Order)
	assert.Equal(t, h5.OrderBE, dt.Base.Order)

	// Version 3: names are not padded; unsigned base.
	msg = newEnc().hdr(h5.ClassEnum, 3, 2, 1).raw(encInt(1, 1, false, false, 0, 8)).
		name("A", false).name("B", false).u8(0xff, 7).b
	dt = decodeDtype(t, msg)
	assert.Equal(t, uint8(3), dt.Version)
	assert.Equal(t, []string{"A", "B"}, dt.EnumNames)
	assert.Equal(t, []int64{255, 7}, dt.EnumValues)

	decodeDtypeErr(t, newEnc().hdr(h5.ClassEnum, 3, 1, 2).raw(encInt(1, 1, false, false, 0, 8)).name("A", false).u8(0).b) // size mismatch
	decodeDtypeErr(t, newEnc().hdr(h5.ClassEnum, 3, 1, 1).raw(encInt(1, 1, false, false, 0, 8)).name("", false).u8(0).b)  // empty name
	decodeDtypeErr(t, newEnc().hdr(h5.ClassEnum, 3, 2, 1).raw(encInt(1, 1, false, false, 0, 8)).name("A", false).u8(0).b) // too few names
	decodeDtypeErr(t, newEnc().hdr(h5.ClassEnum, 3, 1, 1).raw(encInt(1, 1, false, false, 0, 8)).name("A", false).b)       // missing value
}

func TestDtypeDecodeVlen(t *testing.T) {
	dt := decodeDtype(t, newEnc().hdr(h5.ClassVlen, 1, 0, 16).raw(encInt(1, 1, false, false, 0, 8)).b)
	assert.Equal(t, h5.VlenSequence, dt.VlenKind)
	assert.Equal(t, h5.ClassInteger, dt.Base.Class)

	// Null-padded UTF-8 vlen string.
	dt = decodeDtype(t, newEnc().hdr(h5.ClassVlen, 1, 1|1<<4|1<<8, 16).raw(encInt(1, 1, false, false, 0, 8)).b)
	assert.True(t, dt.IsVlenString())
	assert.Equal(t, h5.StrNullPad, dt.StrPad)
	assert.Equal(t, h5.CsetUTF8, dt.Cset)

	// Base type version greater than the vlen's: upgraded.
	assert.Equal(t, uint8(3), decodeDtype(t, newEnc().hdr(h5.ClassVlen, 1, 0, 16).raw(encInt(3, 1, false, false, 0, 8)).b).Version)

	decodeDtypeErr(t, newEnc().hdr(h5.ClassVlen, 1, 2, 16).raw(encInt(1, 1, false, false, 0, 8)).b) // invalid vlen type
	decodeDtypeErr(t, newEnc().hdr(h5.ClassVlen, 1, 0, 16).b)                                       // missing base
}

func TestDtypeDecodeArray(t *testing.T) {
	// Version 2: reserved bytes and permutations.
	msg := newEnc().hdr(h5.ClassArray, 2, 0, 24).u8(2, 0, 0, 0).u32(2).u32(3).u32(0).u32(1).raw(encF32LE(1)).b
	dt := decodeDtype(t, msg)
	assert.Equal(t, []uint64{2, 3}, dt.ArrayDims)
	assert.Equal(t, uint64(6), dt.NumElems())
	assert.Equal(t, uint8(2), dt.Version)

	// Version 3: neither.
	msg = newEnc().hdr(h5.ClassArray, 3, 0, 32).u8(1).u32(4).raw(encInt(1, 8, true, false, 0, 64)).b
	dt = decodeDtype(t, msg)
	assert.Equal(t, []uint64{4}, dt.ArrayDims)
	assert.Equal(t, uint8(3), dt.Version)

	// There should be no array datatypes with version < 2: upgraded.
	msg = newEnc().hdr(h5.ClassArray, 1, 0, 4).u8(1, 0, 0, 0).u32(1).u32(0).raw(encInt(1, 4, true, false, 0, 32)).b
	dt = decodeDtype(t, msg)
	assert.Equal(t, uint8(2), dt.Version)
	assert.Equal(t, uint8(2), dt.Base.Version)

	decodeDtypeErr(t, newEnc().hdr(h5.ClassArray, 3, 0, 4).u8(33).b) // too many dimensions
}

func TestDtypeDecodeBadHeader(t *testing.T) {
	decodeDtypeErr(t, newEnc().hdr(h5.ClassInteger, 0, 0, 4).u16(0).u16(32).b) // version 0
	decodeDtypeErr(t, newEnc().hdr(h5.ClassInteger, 5, 0, 4).u16(0).u16(32).b) // version 5
	decodeDtypeErr(t, newEnc().hdr(11, 1, 0, 4).b)                             // unknown class
	decodeDtypeErr(t, []byte{0x10, 0, 0})                                      // truncated header
}

// validDtypeMessages returns one hand-encoded message per class/version.
func validDtypeMessages() [][]byte {
	arr2 := newEnc().hdr(h5.ClassArray, 2, 0, 24).u8(2, 0, 0, 0).u32(2).u32(3).u32(0).u32(1).raw(encF32LE(1)).b
	return [][]byte{
		encInt(1, 4, true, false, 0, 32),
		newEnc().hdr(h5.ClassFloat, 3, 0x41|0x20|31<<8, 4).u16(0).u16(32).u8(23, 8, 0, 23).u32(129).b,
		newEnc().hdr(h5.ClassTime, 1, 0x1, 4).u16(32).b,
		newEnc().hdr(h5.ClassBitfield, 1, 0x1, 2).u16(3).u16(5).b,
		newEnc().hdr(h5.ClassOpaque, 1, 16, 3).raw([]byte("hello world\x00\x00\x00\x00\x00")).b,
		newEnc().hdr(h5.ClassCompound, 1, 2, 16).
			name("a", true).u32(0).u8(0, 0, 0, 0).u32(0).u32(0).u32(0).u32(0).u32(0).u32(0).raw(encInt(1, 4, true, false, 0, 32)).
			name("arr_member", true).u32(4).u8(2, 0, 0, 0).u32(0).u32(0).u32(2).u32(3).u32(0).u32(0).raw(encInt(1, 2, false, true, 0, 16)).b,
		newEnc().hdr(h5.ClassCompound, 2, 2, 28).name("count", true).u32(0).raw(encInt(1, 4, false, false, 0, 32)).name("values", true).u32(4).raw(arr2).b,
		newEnc().hdr(h5.ClassCompound, 3, 2, 300).name("x", false).u8(0, 0).raw(encInt(1, 1, true, false, 0, 8)).name("yy", false).u16(296).raw(encInt(3, 4, true, true, 0, 32)).b,
		newEnc().hdr(h5.ClassReference, 4, 4|1<<4, 18).b,
		newEnc().hdr(h5.ClassEnum, 1, 3, 2).raw(encInt(1, 2, true, true, 0, 16)).name("MINUS_ONE", true).name("ZERO", true).name("THREE_HUNDRED", true).raw([]byte{0xff, 0xff, 0, 0, 1, 0x2c}).b,
		newEnc().hdr(h5.ClassEnum, 3, 2, 1).raw(encInt(1, 1, false, false, 0, 8)).name("A", false).name("B", false).u8(0xff, 7).b,
		newEnc().hdr(h5.ClassVlen, 1, 1|1<<4|1<<8, 16).raw(encInt(1, 1, false, false, 0, 8)).b,
		arr2,
		newEnc().hdr(h5.ClassArray, 3, 0, 32).u8(1).u32(4).raw(encInt(1, 8, true, false, 0, 64)).b,
	}
}

// Every proper prefix of a valid message must fail to decode, and random
// corruption must never panic.
func TestDtypeDecodeCorrupt(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i, msg := range validDtypeMessages() {
		decodeDtype(t, msg)
		for n := 0; n < len(msg); n++ {
			f := testFile()
			_, err := h5oDtypeDecode(f, f.newImage(msg[:n]))
			assert.Error(t, err, "message %d truncated to %d bytes", i, n)
		}
		for k := 0; k < 2000; k++ {
			b := append([]byte(nil), msg...)
			for j := 0; j < 1+rng.Intn(4); j++ {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
			f := testFile()
			assert.NotPanics(t, func() { _, _ = h5oDtypeDecode(f, f.newImage(b)) })
		}
	}
}

// corpusDtype decodes the datatype message at addr (n bytes) of a file in
// tools/test/h5dump/testfiles. The message locations were obtained with
// h5debug.
func corpusDtype(t *testing.T, name string, addr uint64, n int) (*File, *h5.Datatype) {
	t.Helper()
	f, err := Open(filepath.Join(repoRoot(t), "tools/test/h5dump/testfiles", name))
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	raw, err := f.readAt(addr, n)
	require.NoError(t, err)
	dt, err := h5oDtypeDecode(f, f.newImage(raw))
	require.NoError(t, err)
	return f, dt
}

func TestDtypeDecodeCorpus(t *testing.T) {
	// tcompound.h5 /dset1: H5T_STD_I32BE "a_name", H5T_IEEE_F32BE "b_name",
	// H5T_IEEE_F64BE "c_name".
	_, dt := corpusDtype(t, "tcompound.h5", 768, 184)
	require.Len(t, dt.Members, 3)
	assert.Equal(t, []string{"a_name", "b_name", "c_name"}, []string{dt.Members[0].Name, dt.Members[1].Name, dt.Members[2].Name})
	assert.Equal(t, []uint64{0, 4, 8}, []uint64{dt.Members[0].Offset, dt.Members[1].Offset, dt.Members[2].Offset})
	assert.Equal(t, h5.OrderBE, dt.Members[2].Type.Order)
	assert.Equal(t, uint64(16), dt.Size)

	// tcmpdintsize.h5: compound of 2-D array members (version 2).
	_, dt = corpusDtype(t, "tcmpdintsize.h5", 856, 560)
	assert.Equal(t, uint8(2), dt.Version)
	require.Len(t, dt.Members, 9)
	assert.Equal(t, "DU08BITS", dt.Members[0].Name)
	assert.Equal(t, []uint64{8, 8}, dt.Members[0].Type.ArrayDims)

	// tvms.h5: VAX double (version 3).
	_, dt = corpusDtype(t, "tvms.h5", 832, 24)
	assert.Equal(t, h5.OrderVAX, dt.Order)
	assert.Equal(t, uint64(1025), dt.Float.ExpBias)

	// tldouble.h5: x87 80-bit long double.
	_, dt = corpusDtype(t, "tldouble.h5", 856, 24)
	assert.Equal(t, h5.FloatInfo{SignPos: 79, ExpPos: 64, ExpSize: 15, MantPos: 0, MantSize: 64, ExpBias: 16383, Norm: h5.NormNone}, dt.Float)
	assert.Equal(t, uint32(80), dt.Precision)

	// tnestedcmpddt.h5 /enumtype.
	_, dt = corpusDtype(t, "tnestedcmpddt.h5", 5568, 80)
	assert.Equal(t, []string{"Red", "Green", "Blue", "White", "Black"}, dt.EnumNames)
	assert.Equal(t, []int64{0, 1, 2, 3, 4}, dt.EnumValues)

	// tbitnopaque.h5 /opaquetypetests/opaque_1.
	_, dt = corpusDtype(t, "tbitnopaque.h5", 6056, 32)
	assert.Equal(t, "1-byte opaque type", dt.Tag)

	// trefer_attr.h5 /Dataset3: H5T_STD_REF.
	_, dt = corpusDtype(t, "trefer_attr.h5", 3144, 8)
	assert.Equal(t, h5.RefObject2, dt.RefType)
	assert.Equal(t, uint64(18), dt.Size)

	// tvlstr.h5 /Dataset1: null-terminated vlen string.
	_, dt = corpusDtype(t, "tvlstr.h5", 840, 24)
	assert.True(t, dt.IsVlenString())
	assert.Equal(t, h5.StrNullTerm, dt.StrPad)
}
