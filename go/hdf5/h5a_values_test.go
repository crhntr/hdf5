package hdf5

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/HDFGroup/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rootAttrs opens a corpus file and returns the attributes of its root
// group in name order, with their values.
func rootAttrs(t *testing.T, rel string) (*File, []*h5oAttr, map[string][]any) {
	t.Helper()
	f := openTestFile(t, rel)
	oh, err := f.oProtect(f.sblock.rootAddr)
	require.NoError(t, err)
	attrs, err := objectAttrs(f, oh)
	require.NoError(t, err)
	vals := map[string][]any{}
	for _, a := range attrs {
		v, err := f.aRead(a)
		require.NoError(t, err, a.name)
		vals[a.name] = v
	}
	return f, attrs, vals
}

func attrNames(attrs []*h5oAttr) []string {
	var names []string
	for _, a := range attrs {
		names = append(names, a.name)
	}
	return names
}

// intSizeValues is the content of the attributes "D[SU]nnBITS" of
// tattrintsize.h5 / tscalarattrintsize.h5: 8 x bits elements, element
// (i, j) all ones shifted left by i+j, in the attribute's integer type.
func intSizeValues(signed bool, bitsN int) []any {
	var want []any
	for i := 0; i < 8; i++ {
		for j := 0; j < bitsN; j++ {
			v := ^uint64(0) << uint(i+j)
			if i+j >= 64 {
				v = 0
			}
			switch {
			case signed && bitsN == 8:
				want = append(want, int8(v))
			case signed && bitsN == 16:
				want = append(want, int16(v))
			case signed && bitsN == 32:
				want = append(want, int32(v))
			case signed:
				want = append(want, int64(v))
			case bitsN == 8:
				want = append(want, uint8(v))
			case bitsN == 16:
				want = append(want, uint16(v))
			case bitsN == 32:
				want = append(want, uint32(v))
			default:
				want = append(want, v)
			}
		}
	}
	return want
}

// The expected values below are those of the h5dump golden output
// (tools/test/h5dump/expected/*.ddl).
func TestAttributeValuesDDL(t *testing.T) {
	t.Run("tattr", func(t *testing.T) { // tattr-1.ddl
		_, attrs, vals := rootAttrs(t, "tools/test/h5dump/testfiles/tattr.h5")
		assert.Equal(t, []string{"/attr1", "attr2", "attr3", "attr4", "attr5"}, attrNames(attrs))
		var want []any
		for _, c := range []byte("attribute of root group\x00") {
			want = append(want, int8(c))
		}
		assert.Equal(t, want, vals["/attr1"])
		assert.Equal(t, []any{int32(1), int32(2), int32(3), int32(4), int32(5), int32(6), int32(7), int32(8), int32(9), int32(10)}, vals["attr2"])
		require.Len(t, vals["attr3"], 10)
		for i, v := range vals["attr3"] {
			assert.Equal(t, 0.1*float64(i), v)
		}
		assert.Equal(t, []any{int32(100)}, vals["attr4"])
		assert.Equal(t, h5.SpaceScalar, attrs[3].ds.typ)
		assert.Equal(t, []any{"string attribute"}, vals["attr5"])
		assert.Equal(t, uint64(17), attrs[4].dt.Size)
		assert.Equal(t, h5.OrderBE, attrs[1].dt.Order)
	})
	t.Run("tattr4_be", func(t *testing.T) { // tattr-4_be.ddl
		_, attrs, vals := rootAttrs(t, "tools/test/h5dump/testfiles/tattr4_be.h5")
		require.Len(t, attrs, 9)
		for i, a := range attrs {
			assert.Equal(t, fmt.Sprintf("attr%d", i), a.name)
			assert.Equal(t, []any{"1234"}, vals[a.name])
		}
	})
	t.Run("tvlstr", func(t *testing.T) { // tvlstr.ddl
		_, attrs, vals := rootAttrs(t, "tools/test/h5dump/testfiles/tvlstr.h5")
		require.Len(t, attrs, 1)
		a := attrs[0]
		assert.Equal(t, "test_scalar", a.name)
		// The datatype is the committed datatype "/vl_string_type".
		require.NotNil(t, a.dtShared)
		assert.Equal(t, uint8(h5oShareTypeCommitted), a.dtShared.typ)
		assert.True(t, a.dt.IsVlenString())
		assert.Equal(t, []any{"This is the string for the attribute"}, vals["test_scalar"])
	})
	t.Run("tnullspace", func(t *testing.T) { // tnullspace.ddl
		f, attrs, vals := rootAttrs(t, "tools/test/h5dump/testfiles/tnullspace.h5")
		require.Len(t, attrs, 1)
		assert.Equal(t, "attr", attrs[0].name)
		assert.Equal(t, h5.SpaceNull, attrs[0].ds.typ)
		assert.Equal(t, h5.ClassInteger, attrs[0].dt.Class)
		assert.Equal(t, uint64(4), attrs[0].dt.Size)
		assert.Equal(t, []any{}, vals["attr"])
		assert.Empty(t, attrs[0].data)
		// A null dataspace has no data; data present anyway is an error.
		bad := h5aCopy(attrs[0])
		bad.data = []byte{1, 2, 3, 4}
		_, err := f.aRead(bad)
		assert.Error(t, err)
	})
	for _, tc := range []struct {
		file   string
		scalar bool // one element of type ARRAY [8][B] instead of an 8 x B dataspace
	}{
		{"tools/test/h5dump/testfiles/tattrintsize.h5", false},      // tattrintsize.ddl
		{"tools/test/h5dump/testfiles/tscalarattrintsize.h5", true}, // tscalarattrintsize.ddl
	} {
		t.Run(filepath.Base(tc.file), func(t *testing.T) {
			_, attrs, vals := rootAttrs(t, tc.file)
			assert.Equal(t, []string{"DS08BITS", "DS16BITS", "DS32BITS", "DS64BITS", "DU08BITS", "DU16BITS", "DU32BITS", "DU64BITS", "DummyDBL"}, attrNames(attrs))
			for _, a := range attrs[:8] {
				var bitsN int
				_, err := fmt.Sscanf(a.name[2:], "%02dBITS", &bitsN)
				require.NoError(t, err)
				want := intSizeValues(a.name[1] == 'S', bitsN)
				if tc.scalar {
					assert.Equal(t, []any{want}, vals[a.name], a.name)
					assert.Equal(t, h5.ClassArray, a.dt.Class)
				} else {
					assert.Equal(t, want, vals[a.name], a.name)
					assert.Equal(t, []uint64{8, uint64(bitsN)}, a.ds.size)
				}
			}
		})
	}
	t.Run("tattr2", func(t *testing.T) { // treference.ddl
		f, attrs, vals := rootAttrs(t, "tools/test/h5dump/testfiles/tattr2.h5")
		assert.Len(t, attrs, 27)
		assert.Equal(t, []any{[]any{int32(1), int32(2), int32(3)}, []any{int32(4), int32(5), int32(6)}}, vals["array"])
		assert.Equal(t, []any{uint64(1), uint64(2)}, vals["bitfield"])
		assert.Equal(t, []any{
			h5.Compound{Fields: []h5.Field{{Name: "a", Value: int8(1)}, {Name: "b", Value: float64(2)}}},
			h5.Compound{Fields: []h5.Field{{Name: "a", Value: int8(3)}, {Name: "b", Value: float64(4)}}},
		}, vals["compound"])
		assert.Equal(t, []any{h5.Enum{Name: "RED", Value: 0}, h5.Enum{Name: "RED", Value: 0}}, vals["enum"])
		assert.Equal(t, []any{float32(1), float32(2)}, vals["float"])
		assert.Equal(t, []any{int32(1), int32(2)}, vals["integer"])
		assert.Equal(t, []any{[]byte{1}, []byte{2}}, vals["opaque"])
		assert.Equal(t, []any{"ab", "de"}, vals["string"])
		assert.Equal(t, []any{[]any{int32(1)}, []any{int32(2), int32(3)}}, vals["vlen"])
		require.Len(t, vals["integer3D"], 24)
		for i, v := range vals["integer3D"] {
			assert.Equal(t, int32(i+1), v)
		}

		// The dataset's reference attributes point at the dataset itself.
		dset := lookupPath(t, f, "/dset")
		oh, err := f.oProtect(dset)
		require.NoError(t, err)
		a, err := f.aOpenByName(oh, "reference")
		require.NoError(t, err)
		refs, err := f.aRead(a)
		require.NoError(t, err)
		assert.Equal(t, []any{h5.ObjectRef{Address: dset}, h5.ObjectRef{Address: dset}}, refs)
	})
}

// TestAttributeVersion1Header checks attributes in version 1 object
// headers (always compact; aCount counts the attribute messages).
func TestAttributeVersion1Header(t *testing.T) {
	f := openTestFile(t, "tools/test/h5dump/testfiles/tattr.h5")
	oh, err := f.oProtect(f.sblock.rootAddr)
	require.NoError(t, err)
	require.Equal(t, uint8(h5oVersion1), oh.version)
	n, err := f.aCount(oh)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), n)
	// Creation order: the position in the header.
	names, crt := iterNames(t, f, oh, h5aIndexCrtOrder, h5aIterInc)
	assert.Equal(t, []string{"/attr1", "attr2", "attr3", "attr4", "attr5"}, names)
	assert.Equal(t, []uint32{0, 1, 2, 3, 4}, crt)
	a, err := f.aOpenByName(oh, "attr3")
	require.NoError(t, err)
	assert.Equal(t, uint32(2), a.crtIdx)

	// The returned attributes are copies: changing one does not change
	// the cached message or another copy.
	a.data[0] ^= 0xff
	a.dt.Size = 99
	a.ds.size[0] = 99
	b, err := f.aOpenByName(oh, "attr3")
	require.NoError(t, err)
	assert.NotEqual(t, a.data[0], b.data[0])
	assert.Equal(t, uint64(8), b.dt.Size)
	assert.Equal(t, uint64(10), b.ds.size[0])
}
