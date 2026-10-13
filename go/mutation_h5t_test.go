package hdf5

// Tests for the conversion of raw data to canonical values (h5t_conv.go,
// h5t_bit.go) written from the mutants gremlins left alive. The
// expectations are what libhdf5 1.14.6 reads: the floating-point
// conversions are compared with libhdf5's H5Tconvert results stored in
// testdata/conv_float.h5 (testdata/gen/gen_conv_float.c), the other files
// are built with h5Builder (mutation_h5o_test.go; set HDF5_MUTATION_DUMP
// to a directory to have the tests write them there).

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMutationConvFloatLibhdf5: every dataset /L_S of
// testdata/conv_float.h5 (custom floating-point layouts, see
// gen_conv_float.c) reads as the doubles of /L_S.expect, libhdf5's
// H5Tconvert of the same buffer, bit for bit (H5T__conv_f_f: special
// values, exponent clipping, denormals, rounding and its carry, the
// state carried from one element to the next, which starts over for each
// array element and vlen sequence).
func TestMutationConvFloatLibhdf5(t *testing.T) {
	f := openTestdata(t, "conv_float.h5")
	n := 0
	for l, err := range f.Root().Links() {
		require.NoError(t, err)
		if strings.HasSuffix(l.Name, ".expect") {
			continue
		}
		n++
		t.Run(l.Name, func(t *testing.T) {
			ds, err := f.OpenDataset("/" + l.Name)
			require.NoError(t, err)
			vals, err := ds.ReadValues()
			require.NoError(t, err)
			got := flattenValues(vals)
			exp, err := f.OpenDataset("/" + l.Name + ".expect")
			require.NoError(t, err)
			want, err := Read[float64](exp)
			require.NoError(t, err)
			require.Len(t, got, len(want))
			bad := 0
			for i := range want {
				g, ok := got[i].(float64)
				if !ok || math.Float64bits(g) != math.Float64bits(want[i]) {
					if bad < 10 {
						t.Errorf("element %d: got %v (%s), libhdf5 %v (%#016x)", i, got[i], bitsOf(got[i]), want[i], math.Float64bits(want[i]))
					}
					bad++
				}
			}
			if bad > 0 {
				t.Errorf("%d of %d elements differ", bad, len(want))
			}
		})
	}
	assert.Equal(t, 41, n)
}

// flattenValues flattens arrays and vlen sequences.
func flattenValues(vals []any) []any {
	var out []any
	for _, v := range vals {
		if s, ok := v.([]any); ok {
			out = append(out, flattenValues(s)...)
		} else {
			out = append(out, v)
		}
	}
	return out
}

func bitsOf(v any) string {
	if g, ok := v.(float64); ok {
		return fmt.Sprintf("%#016x", math.Float64bits(g))
	}
	return fmt.Sprintf("%T", v)
}

// TestMutationConvFloatExponentSize: H5T__conv_f_f converts exponent
// fields of up to 63 bits (testdata/conv_float.h5 /e63_*) and rejects
// wider ones (h5t_conv.go:233): libhdf5 fails to read a 16-byte float
// with a 64-bit exponent.
func TestMutationConvFloatExponentSize(t *testing.T) {
	/* sign bit 127, exponent bits 63-126, mantissa bits 0-62 */
	msg := encFloat(1, 16, 0x20, 127, 0, 128, 63, 64, 0, 63, 1023)
	_, _, err := readDtypeObject(t, msg, make([]byte, 16), 1, false)
	require.Error(t, err)
}

// heapRefObject puts a global heap collection holding blob as object 1
// followed by the dataset /obj of one element of datatype msg; elem builds
// the element from the collection's address. The dataset is at
// heapRefObjAddr.
func heapRefObject(msg []byte, blob []byte, elem func(heap uint64) []byte) func(b *h5Builder) uint64 {
	return func(b *h5Builder) uint64 {
		heap := b.put(gcol(blob))
		return dtypeObject(msg, elem(heap), 1, false)(b)
	}
}

// heapRefObjAddr is the address of /obj in the files of heapRefObject:
// after the superblock and the 4096-byte global heap collection.
const heapRefObjAddr = h5BuilderSuperblockSize + 4096

// readHeapRef opens the file of heapRefObject and reads /obj.
func readHeapRef(t *testing.T, msg []byte, blob []byte, elem func(heap uint64) []byte) ([]any, error) {
	t.Helper()
	f, obj := buildObject(t, heapRefObject(msg, blob, elem))
	require.Equal(t, uint64(heapRefObjAddr), obj)
	ds, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	return ds.ReadValues()
}

// TestMutationConvVlenStringNul: a variable-length string is a C string:
// the sequence "\x00abc" reads as "" (h5t_conv.go:776).
func TestMutationConvVlenStringNul(t *testing.T) {
	msg := newEnc().hdr(h5.ClassVlen, 1, 1, 16).raw(encInt(1, 1, false, false, 0, 8)).b
	vals, err := readHeapRef(t, msg, []byte("\x00abc"), func(heap uint64) []byte {
		return binary.LittleEndian.AppendUint32(le64a(binary.LittleEndian.AppendUint32(nil, 4), heap), 1)
	})
	require.NoError(t, err)
	assert.Equal(t, []any{""}, vals)
}

// revisedRefType is the datatype message of revised references of type
// rtype (H5T_STD_REF: 18 bytes on disk with 8-byte addresses).
func revisedRefType(rtype h5.RefType) []byte {
	return newEnc().hdr(h5.ClassReference, 4, uint32(rtype)|h5rEncodeVersion<<4, 18).b
}

// blobRef is the element of a revised reference of type rtype whose
// encoding (blob) is object 1 of the global heap collection at heap.
func blobRef(rtype h5.RefType, blob []byte) func(heap uint64) []byte {
	return func(heap uint64) []byte {
		b := binary.LittleEndian.AppendUint32([]byte{byte(rtype), 0}, uint32(len(blob)))
		return binary.LittleEndian.AppendUint32(le64a(b, heap), 1)
	}
}

// refToken encodes an object token of n bytes holding the address addr.
func refToken(n int, addr uint64) []byte {
	tok := make([]byte, max(n, 8))
	binary.LittleEndian.PutUint64(tok, addr)
	return append([]byte{byte(n)}, tok[:n]...)
}

// TestMutationConvRevisedRefs: revised references (H5R__decode and its
// helpers) at the limits of their buffers. libhdf5 reads the ones that
// have a "want" (H5Dread into H5T_STD_REF, H5Rget_type,
// H5Rget_attr_name, H5Ropen_region), and fails to read the others
// (h5t_conv.go:1074, 1077, 1098, 1116, 1121, 1131). The selection of a
// region reference whose encoded extent rank (32) is not its dataset's
// (1) has the dataset's rank: H5Ropen_region copies it onto the
// dataset's dataspace (H5R__get_region).
func TestMutationConvRevisedRefs(t *testing.T) {
	/* The references point at the dataset itself */
	const addr = heapRefObjAddr
	all := []byte{3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0} /* "all" selection */
	region := func(size, rank uint32, sel []byte) []byte {
		b := binary.LittleEndian.AppendUint32(refToken(8, addr), size)
		return append(binary.LittleEndian.AppendUint32(b, rank), sel...)
	}
	for _, c := range []struct {
		name  string
		rtype h5.RefType
		blob  []byte
		elem  []byte /* (instead of a blob) */
		want  any    /* nil: libhdf5 fails */
	}{
		{name: "object_token_15_bytes", rtype: h5.RefObject2,
			elem: append([]byte{byte(h5.RefObject2), 0}, refToken(15, addr)...),
			want: h5.ObjectRef{Address: addr}},
		{name: "attribute_token_16_bytes", rtype: h5.RefAttr,
			blob: append(refToken(16, addr), 1, 0, 'x'),
			want: h5.AttrRef{Address: addr, Name: "x"}},
		{name: "attribute_empty_name", rtype: h5.RefAttr,
			blob: append(refToken(8, addr), 0, 0),
			want: h5.AttrRef{Address: addr, Name: ""}},
		{name: "region_rank_32", rtype: h5.RefRegion2,
			blob: region(16, 32, all),
			want: h5.RegionRef{Address: addr, Selection: h5.Selection{Type: h5.SelAll, Rank: 1}}},
		{name: "region_rank_33", rtype: h5.RefRegion2,
			blob: region(16, 33, all)},
		{name: "region_6_bytes", rtype: h5.RefRegion2,
			blob: append(refToken(8, addr), 16, 0, 0, 0, 1, 0)},
		{name: "region_size_past_end", rtype: h5.RefRegion2,
			blob: region(20, 1, all)},
	} {
		t.Run(c.name, func(t *testing.T) {
			elem := blobRef(c.rtype, c.blob)
			blob := c.blob
			if c.elem != nil {
				elem = func(uint64) []byte { return append(c.elem, make([]byte, 18-len(c.elem))...) }
				blob = []byte{0}
			}
			vals, err := readHeapRef(t, revisedRefType(c.rtype), blob, elem)
			if c.want == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []any{c.want}, vals)
		})
	}
}

// TestMutationConvRevisedRefsPastEnd: an object token or a string that
// extends past the end of the encoded reference is an error
// (h5t_conv.go:1077, 1104). (libhdf5's H5R__decode_obj_token and
// H5R__decode_string do not check the length and copy bytes from past the
// end of the buffer.)
func TestMutationConvRevisedRefsPastEnd(t *testing.T) {
	t.Run("object_token", func(t *testing.T) {
		elem := append([]byte{byte(h5.RefObject2), 0}, refToken(16, heapRefObjAddr)[:16]...)
		_, err := readHeapRef(t, revisedRefType(h5.RefObject2), []byte{0}, func(uint64) []byte { return elem })
		require.Error(t, err)
	})
	t.Run("attribute_name", func(t *testing.T) {
		blob := append(refToken(8, heapRefObjAddr), 5, 0, 'a', 'b')
		_, err := readHeapRef(t, revisedRefType(h5.RefAttr), blob, blobRef(h5.RefAttr, blob))
		require.Error(t, err)
	})
}
