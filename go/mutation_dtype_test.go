package hdf5

// Tests for the datatype message decoder (h5o_dtype.go) and h5t.go
// written from the mutants gremlins left alive. Each test builds a file
// with h5Builder (mutation_h5o_test.go) holding the dataset /obj of the
// datatype under test, its elements stored in a compact layout, and
// expects what libhdf5 1.14.6 reads from the same bytes (set
// HDF5_MUTATION_DUMP to a directory to have the tests write the files
// there).

import (
	"encoding/binary"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dtypeObject puts a dataset object header (version 1 when v1 is set,
// version 2 otherwise) of n elements of the datatype message msg, whose
// elements are data (compact layout).
func dtypeObject(msg, data []byte, n uint64, v1 bool) func(b *h5Builder) uint64 {
	return func(b *h5Builder) uint64 {
		compact := binary.LittleEndian.AppendUint16([]byte{3, 0}, uint16(len(data)))
		msgs := []ohMsg{spaceMsg(n), {typ: h5oDtypeID, flags: 1, data: msg}, layoutMsg(append(compact, data...))}
		if v1 {
			return b.put(v1Hdr(uint16(len(msgs)), v1Msgs(msgs...)))
		}
		return b.put(ohdr2(2, v2Msgs(msgs...)))
	}
}

// readDtypeObject opens /obj built by dtypeObject and returns its
// datatype and values, or the error opening or reading it.
func readDtypeObject(t *testing.T, msg, data []byte, n uint64, v1 bool) (*h5.Datatype, []any, error) {
	t.Helper()
	f, _ := buildObject(t, dtypeObject(msg, data, n, v1))
	ds, err := f.OpenDataset("/obj")
	if err != nil {
		return nil, nil, err
	}
	vals, err := ds.ReadValues()
	return ds.Datatype(), vals, err
}

// openDtypeObject opens /obj built by dtypeObject and returns the error
// (libhdf5 decodes the datatype message when it opens the dataset).
func openDtypeObject(t *testing.T, msg, data []byte, n uint64, v1 bool) error {
	t.Helper()
	f, _ := buildObject(t, dtypeObject(msg, data, n, v1))
	_, err := f.OpenDataset("/obj")
	return err
}

// encFloat is a floating-point datatype message: flags holds the byte
// order, padding and normalization bits (bits 0-5).
func encFloat(version uint8, size uint32, flags uint32, sign, offset, prec uint16, epos, esize, mpos, msize uint8, bias uint32) []byte {
	return newEnc().hdr(h5.ClassFloat, version, flags|uint32(sign)<<8, size).u16(offset).u16(prec).u8(epos, esize, mpos, msize).u32(bias).b
}

// v1Member encodes a version 1 compound member: padded name, offset, the
// intrinsic array dimensions (ndims of dims) and the member type.
func v1Member(e *enc, name string, offset uint32, ndims uint8, dims [4]uint32, typ []byte) *enc {
	e.name(name, true).u32(offset).u8(ndims, 0, 0, 0).u32(0).u32(0)
	for _, d := range dims {
		e.u32(d)
	}
	return e.raw(typ)
}

// TestMutationDtypeCompoundOffsetSize: version 3 compound member offsets
// take the number of bytes H5VM_limit_enc_size gives for the compound
// size (h5o_dtype.go:62): one byte for a 200-byte compound.
func TestMutationDtypeCompoundOffsetSize(t *testing.T) {
	msg := newEnc().hdr(h5.ClassCompound, 3, 2, 200).
		name("a", false).u8(0).raw(encInt(1, 1, false, false, 0, 8)).
		name("b", false).u8(199).raw(encInt(1, 1, false, false, 0, 8)).b
	data := make([]byte, 200)
	data[0], data[199] = 7, 9
	dt, vals, err := readDtypeObject(t, msg, data, 1, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(199), dt.Members[1].Offset)
	assert.Equal(t, []any{h5.Compound{Fields: []h5.Field{{Name: "a", Value: uint8(7)}, {Name: "b", Value: uint8(9)}}}}, vals)
}

// TestMutationDtypeUnusualUnusedBits: in a version 1 (not checksummed)
// object header, libhdf5 rejects numeric types whose precision plus
// offset is less than half their size in bits, and only for sizes above
// one byte (h5o_dtype.go:121, 123).
func TestMutationDtypeUnusualUnusedBits(t *testing.T) {
	for _, c := range []struct {
		name       string
		size       uint32
		prec       uint16
		data       []byte
		want       any
		rejectedV1 bool
	}{
		{"1_byte_3_bits", 1, 3, []byte{0xfd}, uint8(5), false},
		{"4_bytes_16_bits", 4, 16, []byte{0x34, 0x12, 0xff, 0xff}, uint32(0x1234), false},
		{"4_bytes_15_bits", 4, 15, []byte{0x34, 0x12, 0, 0}, uint32(0x1234), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			msg := encInt(1, c.size, false, false, 0, c.prec)
			t.Run("v1", func(t *testing.T) {
				if c.rejectedV1 {
					require.Error(t, openDtypeObject(t, msg, c.data, 1, true))
					return
				}
				_, vals, err := readDtypeObject(t, msg, c.data, 1, true)
				require.NoError(t, err)
				assert.Equal(t, []any{c.want}, vals)
			})
			t.Run("v2", func(t *testing.T) {
				_, vals, err := readDtypeObject(t, msg, c.data, 1, false)
				require.NoError(t, err)
				assert.Equal(t, []any{c.want}, vals)
			})
		})
	}
}

// TestMutationDtypeEmptyMemberName: a compound member named "" decodes
// (h5o_dtype.go:139: the name is NUL terminated): libhdf5 opens the
// dataset, H5Tget_member_name gives "" and "b". (Its values cannot be
// read into a memory compound type: H5Tinsert refuses an empty name.)
func TestMutationDtypeEmptyMemberName(t *testing.T) {
	msg := newEnc().hdr(h5.ClassCompound, 3, 2, 2).
		name("", false).u8(0).raw(encInt(1, 1, false, false, 0, 8)).
		name("b", false).u8(1).raw(encInt(1, 1, false, false, 0, 8)).b
	dt, _, _ := readDtypeObject(t, msg, []byte{1, 2}, 1, false)
	require.NotNil(t, dt)
	require.Len(t, dt.Members, 2)
	assert.Equal(t, "", dt.Members[0].Name)
	assert.Equal(t, "b", dt.Members[1].Name)
	assert.Equal(t, uint64(1), dt.Members[1].Offset)
}

// TestMutationDtypeFloatFields: floating-point bit fields at the top of
// the element and one bit past it (h5o_dtype.go:264, 279).
func TestMutationDtypeFloatFields(t *testing.T) {
	/* Sign bit 0, exponent bits 1-8 (bias 127), mantissa bits 9-31. */
	t.Run("mantissa_at_top", func(t *testing.T) {
		top := encFloat(1, 4, 0x20, 0, 0, 32, 1, 8, 9, 23, 127)
		_, vals, err := readDtypeObject(t, top, binary.LittleEndian.AppendUint32(nil, 1|127<<1|1<<31), 1, false)
		require.NoError(t, err)
		assert.Equal(t, []any{-1.5}, vals)
	})

	for _, c := range []struct {
		name string
		msg  []byte
	}{
		/* Mantissa bits 9-32 */
		{"mantissa_past_end", encFloat(1, 4, 0x20, 0, 0, 32, 1, 8, 9, 24, 127)},
		/* Mantissa bits 1-20, exponent bits 21-32 */
		{"exponent_past_end", encFloat(1, 4, 0x20, 0, 0, 32, 21, 12, 1, 20, 127)},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Error(t, openDtypeObject(t, c.msg, make([]byte, 4), 1, false))
		})
	}
}

// TestMutationDtypeOpaqueTag: the tag of an opaque type is a C string,
// empty when its first byte is NUL (h5o_dtype.go:361).
func TestMutationDtypeOpaqueTag(t *testing.T) {
	msg := newEnc().hdr(h5.ClassOpaque, 1, 8, 2).raw([]byte("\x00tagtag\x00")).b
	dt, vals, err := readDtypeObject(t, msg, []byte{1, 2}, 1, false)
	require.NoError(t, err)
	assert.Equal(t, "", dt.Tag)
	assert.Equal(t, []any{[]byte{1, 2}}, vals)
}

// TestMutationDtypeV1ArrayMembers: intrinsic array members of version 1
// compounds: four dimensions are allowed (h5o_dtype.go:463), and every
// dimension must be positive (h5o_dtype.go:522).
func TestMutationDtypeV1ArrayMembers(t *testing.T) {
	u8 := encInt(1, 1, false, false, 0, 8)
	msg := v1Member(newEnc().hdr(h5.ClassCompound, 1, 1, 16), "a", 0, 4, [4]uint32{2, 2, 2, 2}, u8).b
	data := make([]byte, 16)
	for i := range data {
		data[i] = byte(i)
	}
	t.Run("4_dims", func(t *testing.T) {
		dt, vals, err := readDtypeObject(t, msg, data, 1, false)
		require.NoError(t, err)
		assert.Equal(t, []uint64{2, 2, 2, 2}, dt.Members[0].Type.ArrayDims)
		want := make([]any, 16)
		for i := range want {
			want[i] = uint8(i)
		}
		assert.Equal(t, []any{h5.Compound{Fields: []h5.Field{{Name: "a", Value: want}}}}, vals)
	})
	t.Run("zero_second_dim", func(t *testing.T) {
		msg := v1Member(newEnc().hdr(h5.ClassCompound, 1, 1, 16), "a", 0, 2, [4]uint32{2, 0, 0, 0}, u8).b
		require.Error(t, openDtypeObject(t, msg, data, 1, false))
	})
}

// TestMutationDtypeMemberOrder: compound members stored out of offset
// order are accepted, and a member overlapping a previous one, or
// starting at the same offset, is rejected (h5o_dtype.go:568, 569, 576).
func TestMutationDtypeMemberOrder(t *testing.T) {
	u16 := encInt(1, 2, false, false, 0, 16)
	u32 := encInt(1, 4, false, false, 0, 32)
	t.Run("out_of_order", func(t *testing.T) {
		msg := newEnc().hdr(h5.ClassCompound, 3, 2, 8).
			name("a", false).u8(4).raw(u32).
			name("b", false).u8(0).raw(u16).b
		_, vals, err := readDtypeObject(t, msg, []byte{1, 0, 0, 0, 2, 0, 0, 0}, 1, false)
		require.NoError(t, err)
		assert.Equal(t, []any{h5.Compound{Fields: []h5.Field{{Name: "a", Value: uint32(2)}, {Name: "b", Value: uint16(1)}}}}, vals)
	})
	t.Run("overlap", func(t *testing.T) {
		msg := newEnc().hdr(h5.ClassCompound, 3, 2, 12).
			name("a", false).u8(8).raw(u32).
			name("b", false).u8(6).raw(u32).b
		require.Error(t, openDtypeObject(t, msg, make([]byte, 12), 1, false))
	})
	t.Run("same_offset", func(t *testing.T) {
		msg := newEnc().hdr(h5.ClassCompound, 3, 2, 4).
			name("a", false).u8(0).raw(u32).
			name("b", false).u8(0).raw(u16).b
		require.Error(t, openDtypeObject(t, msg, make([]byte, 4), 1, false))
	})
}

// TestMutationDtypeEnumNoMembers: an enumeration with no members ending
// the message decodes (h5o_dtype.go:639).
func TestMutationDtypeEnumNoMembers(t *testing.T) {
	msg := newEnc().hdr(h5.ClassEnum, 3, 0, 1).raw(encInt(1, 1, false, false, 0, 8)).b
	dt, vals, err := readDtypeObject(t, msg, []byte{3}, 1, false)
	require.NoError(t, err)
	assert.Empty(t, dt.EnumNames)
	assert.Equal(t, []any{h5.Enum{Value: 3}}, vals)
}

// TestMutationDtypeArrayRank: an array datatype of H5S_MAX_RANK (32)
// dimensions (h5o_dtype.go:730).
func TestMutationDtypeArrayRank(t *testing.T) {
	e := newEnc().hdr(h5.ClassArray, 3, 0, 2).u8(32)
	for i := 0; i < 32; i++ {
		if i == 31 {
			e.u32(2)
		} else {
			e.u32(1)
		}
	}
	msg := e.raw(encInt(1, 1, false, false, 0, 8)).b
	t.Run("32", func(t *testing.T) {
		dt, vals, err := readDtypeObject(t, msg, []byte{5, 6}, 1, false)
		require.NoError(t, err)
		assert.Len(t, dt.ArrayDims, 32)
		assert.Equal(t, []any{[]any{uint8(5), uint8(6)}}, vals)
	})
	t.Run("33", func(t *testing.T) {
		e := newEnc().hdr(h5.ClassArray, 3, 0, 1).u8(33)
		for range 33 {
			e.u32(1)
		}
		require.Error(t, openDtypeObject(t, e.raw(encInt(1, 1, false, false, 0, 8)).b, []byte{5}, 1, false))
	})
}

// TestMutationDetectClass: H5T_detect_class looks for the class in the
// members of a compound (h5t.go:28): H5Tdetect_class(compound of an
// integer, H5T_VLEN) is false, H5Tdetect_class(compound of a vlen,
// H5T_VLEN) true.
func TestMutationDetectClass(t *testing.T) {
	cmpd := func(m *h5.Datatype) *h5.Datatype {
		return &h5.Datatype{Class: h5.ClassCompound, Members: []h5.Member{{Name: "m", Type: m}}}
	}
	assert.False(t, h5tDetectClass(cmpd(&h5.Datatype{Class: h5.ClassInteger}), h5.ClassVlen))
	assert.True(t, h5tDetectClass(cmpd(&h5.Datatype{Class: h5.ClassVlen, Base: &h5.Datatype{Class: h5.ClassInteger}}), h5.ClassVlen))
}
