package hdf5

import (
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests of the public Attribute API (attribute.go, read.go) on
// tools/test/h5dump/testfiles/tattr2.h5; the expected values are those
// h5dump -A prints.

// attr returns the attribute name of obj.
func attr(t *testing.T, obj Object, name string) *Attribute {
	t.Helper()
	a, err := obj.Attribute(name)
	require.NoError(t, err, name)
	return a
}

func TestAttributeRead(t *testing.T) {
	f := h5dumpFile(t, "tattr2.h5")
	root := f.Root()
	dset := openDS(t, f, "/dset")

	for _, obj := range []Object{root, dset} {
		a := attr(t, obj, "integer")
		assert.Equal(t, "integer", a.Name())
		assert.Equal(t, h5.ClassInteger, a.Datatype().Class)
		assert.Equal(t, []uint64{2}, a.Dataspace().Dims)
		ints, err := ReadAttribute[int32](a)
		require.NoError(t, err)
		assert.Equal(t, []int32{1, 2}, ints)
		vals, err := a.ReadValues()
		require.NoError(t, err)
		assert.Equal(t, []any{int32(1), int32(2)}, vals)

		/* 3-D: row-major */
		ints, err = ReadAttribute[int32](attr(t, obj, "integer3D"))
		require.NoError(t, err)
		require.Len(t, ints, 24)
		for i, v := range ints {
			assert.Equal(t, int32(i+1), v)
		}

		floats, err := ReadAttribute[float32](attr(t, obj, "float"))
		require.NoError(t, err)
		assert.Equal(t, []float32{1, 2}, floats)
		f64, err := ReadAttribute[float64](attr(t, obj, "float2D"))
		require.NoError(t, err)
		assert.Len(t, f64, 6)

		arrays, err := ReadAttribute[[]int32](attr(t, obj, "array"))
		require.NoError(t, err)
		assert.Equal(t, [][]int32{{1, 2, 3}, {4, 5, 6}}, arrays)

		bits, err := ReadAttribute[uint8](attr(t, obj, "bitfield"))
		require.NoError(t, err)
		assert.Equal(t, []uint8{1, 2}, bits)

		type ab struct {
			A int8    `hdf5:"a"`
			B float64 `hdf5:"b"`
		}
		comps, err := ReadAttribute[ab](attr(t, obj, "compound"))
		require.NoError(t, err)
		assert.Equal(t, []ab{{1, 2}, {3, 4}}, comps)

		enums, err := ReadAttribute[h5.Enum](attr(t, obj, "enum"))
		require.NoError(t, err)
		assert.Equal(t, []h5.Enum{{Name: "RED"}, {Name: "RED"}}, enums)
		bools, err := ReadAttribute[bool](attr(t, obj, "enum"))
		require.NoError(t, err)
		assert.Equal(t, []bool{false, false}, bools)

		opaque, err := ReadAttribute[[]byte](attr(t, obj, "opaque"))
		require.NoError(t, err)
		assert.Equal(t, [][]byte{{1}, {2}}, opaque)
		assert.Equal(t, "1-byte opaque type", attr(t, obj, "opaque").Datatype().Tag)

		strs, err := ReadAttribute[string](attr(t, obj, "string"))
		require.NoError(t, err)
		assert.Equal(t, []string{"ab", "de"}, strs)

		vlens, err := ReadAttribute[[]int32](attr(t, obj, "vlen"))
		require.NoError(t, err)
		assert.Equal(t, [][]int32{{1}, {2, 3}}, vlens)
		var anyVals []any
		require.NoError(t, attr(t, obj, "vlen").ReadInto(&anyVals))
		assert.Equal(t, []any{[]any{int32(1)}, []any{int32(2), int32(3)}}, anyVals)
	}

	/* Object references (attributes of /dset point at /dset) */
	info, err := dset.Info()
	require.NoError(t, err)
	refs, err := ReadAttribute[h5.ObjectRef](attr(t, dset, "reference"))
	require.NoError(t, err)
	assert.Equal(t, []h5.ObjectRef{{Address: info.Address}, {Address: info.Address}}, refs)
}

func TestAttributeScalarAndErrors(t *testing.T) {
	f := h5dumpFile(t, "tvlstr.h5")
	a := attr(t, f.Root(), "test_scalar")
	assert.Equal(t, h5.Dataspace{Class: h5.SpaceScalar, Version: a.Dataspace().Version}, a.Dataspace())
	assert.True(t, a.Datatype().IsVlenString())
	s, err := ReadAttribute[string](a)
	require.NoError(t, err)
	assert.Equal(t, []string{"This is the string for the attribute"}, s)

	var n []int32
	assert.Error(t, a.ReadInto(&n))
	assert.Error(t, a.ReadInto(n))
	_, err = ReadAttribute[float64](a)
	assert.Error(t, err)

	_, err = f.Root().Attribute("no_such_attribute")
	assert.True(t, IsNotFound(err), "%v", err)

	/* Attributes of a committed datatype */
	dt, err := f.OpenDatatype("/vl_string_type")
	require.NoError(t, err)
	assert.True(t, dt.Datatype().IsVlenString())
	n2, err := dt.NumAttributes()
	require.NoError(t, err)
	assert.Equal(t, 0, n2)
}
