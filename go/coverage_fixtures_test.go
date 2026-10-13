package hdf5

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

// Tests on testdata/cover.h5 and testdata/cover_sohm.h5 (written by
// testdata/gen/gen_cover.c), the inputs for read paths `go test -cover`
// found unexercised. Both files are compared with libhdf5 by
// cgo/conformance; the expected values below are h5dump's (libhdf5).

func openCover(t *testing.T, name string) *File {
	t.Helper()
	f, err := Open("testdata/" + name)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// sparse returns n values of fill with the given indices set to their index.
func sparse(n int, fill int32, set ...int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = fill
	}
	for _, i := range set {
		out[i] = int32(i)
	}
	return out
}

func TestCoverSparseChunkIndexes(t *testing.T) {
	f := openCover(t, "cover.h5")
	for _, tc := range []struct {
		path string
		idx  h5.ChunkIndex
		want []int32
	}{
		/* extensible array: unallocated data blocks and super blocks */
		{"/ea_sparse", h5.ChunkIndexEArray, sparse(300, -7, 0, 1, 2, 3, 100, 101, 298, 299)},
		{"/ea_sparse_filt", h5.ChunkIndexEArray, sparse(300, -7, 0, 1, 2, 3, 100, 101, 298, 299)},
		/* paged fixed array: pages never written */
		{"/fa_paged", h5.ChunkIndexFArray, sparse(3000, -3, 0, 1500, 2999)},
		{"/fa_paged_filt", h5.ChunkIndexFArray, sparse(3000, -3, 0, 1500, 2999)},
	} {
		d := openDS(t, f, tc.path)
		l, err := d.Layout()
		require.NoError(t, err, tc.path)
		assert.Equal(t, tc.idx, l.ChunkIndex, tc.path)
		got, err := Read[int32](d)
		require.NoError(t, err, tc.path)
		assert.Equal(t, tc.want, got, tc.path)

		/* A hyperslab entirely in unwritten chunks, and one across */
		vals, err := d.ReadHyperslab(h5.Hyperslab{Start: []uint64{150}, Count: []uint64{4}})
		require.NoError(t, err, tc.path)
		w := tc.want[150:154]
		assert.Equal(t, []any{w[0], w[1], w[2], w[3]}, vals, tc.path)
		vals, err = d.ReadHyperslab(h5.Hyperslab{Start: []uint64{98}, Stride: []uint64{3}, Count: []uint64{2}})
		require.NoError(t, err, tc.path)
		assert.Equal(t, []any{tc.want[98], tc.want[101]}, vals, tc.path)
	}
}

// libhdf5 cannot read the chunks of a dataset with a variable-length fill
// value that were never written from a file opened read-only: it fills
// the chunk with H5D__fill_refill_vl, whose conversion back to the file's
// datatype writes to the global heap ("no write intent on file"). Chunks
// that exist read normally, and an unallocated contiguous dataset is
// filled through H5D__fill (no conversion back).
func TestCoverVlenFillValue(t *testing.T) {
	f := openCover(t, "cover.h5")

	d := openDS(t, f, "/vlen_str_fill")
	fv, err := d.FillValue()
	require.NoError(t, err)
	assert.True(t, fv.Defined)
	assert.Nil(t, fv.Value) /* (vlen fill values are not exposed as bytes) */
	_, err = d.ReadValues()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't refill fill value buffer")
	vals, err := d.ReadHyperslab(h5.Hyperslab{Start: []uint64{0}, Count: []uint64{2}})
	require.NoError(t, err)
	assert.Equal(t, []any{"a", "bc"}, vals)
	_, err = d.ReadHyperslab(h5.Hyperslab{Start: []uint64{1}, Count: []uint64{2}})
	require.Error(t, err)

	strs, err := Read[string](openDS(t, f, "/vlen_str_contig"))
	require.NoError(t, err)
	assert.Equal(t, []string{"abc", "abc", "abc"}, strs)

	i16, err := Read[int16](openDS(t, f, "/fixed_fill_contig"))
	require.NoError(t, err)
	assert.Equal(t, []int16{11, 11, 11, 11, 11, 11}, i16)
}

// The fill refill only fails for fill values with non-null
// variable-length components.
func TestFillVlWritesHeap(t *testing.T) {
	vstr := &h5.Datatype{Class: h5.ClassVlen, VlenKind: h5.VlenString, Size: 16}
	vseq := &h5.Datatype{Class: h5.ClassVlen, VlenKind: h5.VlenSequence, Size: 16, Base: &h5.Datatype{Class: h5.ClassInteger, Size: 4}}
	null := make([]byte, 16)
	empty := []byte{0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0}    /* length 0, heap object 1 */
	nonEmpty := []byte{2, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0} /* length 2 */
	assert.False(t, h5dFillVlWritesHeap(vstr, null, 8))
	assert.True(t, h5dFillVlWritesHeap(vstr, empty, 8))
	assert.False(t, h5dFillVlWritesHeap(vseq, empty, 8))
	assert.True(t, h5dFillVlWritesHeap(vseq, nonEmpty, 8))
	cmpd := &h5.Datatype{Class: h5.ClassCompound, Size: 20, Members: []h5.Member{
		{Name: "i", Offset: 0, Type: &h5.Datatype{Class: h5.ClassInteger, Size: 4}},
		{Name: "s", Offset: 4, Type: vstr},
	}}
	assert.False(t, h5dFillVlWritesHeap(cmpd, append([]byte{5, 0, 0, 0}, null...), 8))
	assert.True(t, h5dFillVlWritesHeap(cmpd, append([]byte{5, 0, 0, 0}, empty...), 8))
	arr := &h5.Datatype{Class: h5.ClassArray, Size: 32, ArrayDims: []uint64{2}, Base: vseq}
	assert.True(t, h5dFillVlWritesHeap(arr, append(append([]byte{}, null...), nonEmpty...), 8))
	assert.False(t, h5dFillVlWritesHeap(arr, make([]byte, 32), 8))
	assert.False(t, h5dFillVlWritesHeap(vstr, null[:4], 8))

	assert.True(t, h5tDetectClass(arr, h5.ClassVlen))
	assert.True(t, h5tDetectClass(cmpd, h5.ClassVlen))
	assert.True(t, h5tDetectClass(&h5.Datatype{Class: h5.ClassCompound, Members: []h5.Member{{Type: cmpd}}}, h5.ClassVlen))
	assert.False(t, h5tDetectClass(&h5.Datatype{Class: h5.ClassEnum, Base: &h5.Datatype{Class: h5.ClassInteger}}, h5.ClassVlen))
	assert.False(t, h5tDetectClass(nil, h5.ClassVlen))
}

func TestCoverNullAndEmptyDataspaces(t *testing.T) {
	f := openCover(t, "cover.h5")
	d := openDS(t, f, "/null_space")
	assert.Equal(t, h5.SpaceNull, d.Dataspace().Class)
	vals, err := d.ReadValues()
	require.NoError(t, err)
	assert.Empty(t, vals)
	var i32 []int32
	require.NoError(t, d.ReadInto(&i32))
	assert.Empty(t, i32)

	n, err := d.NumAttributes()
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	var names []string
	for a, err := range d.Attributes() {
		require.NoError(t, err)
		names = append(names, a.Name())
	}
	assert.Equal(t, []string{"null_attr", "scalar_attr"}, names)
	a, err := d.Attribute("null_attr")
	require.NoError(t, err)
	assert.Equal(t, h5.SpaceNull, a.Dataspace().Class)
	av, err := a.ReadValues()
	require.NoError(t, err)
	assert.Empty(t, av)
	a, err = d.Attribute("scalar_attr")
	require.NoError(t, err)
	assert.Equal(t, h5.SpaceScalar, a.Dataspace().Class)
	got, err := ReadAttribute[int32](a)
	require.NoError(t, err)
	assert.Equal(t, []int32{5}, got)
	require.NoError(t, a.ReadInto(&i32))
	assert.Equal(t, []int32{5}, i32)
	var str []string
	assert.Error(t, a.ReadInto(&str))
	_, err = ReadAttribute[string](a)
	assert.Error(t, err)

	e := openDS(t, f, "/empty_2d")
	assert.Equal(t, []uint64{0, 3}, e.Dataspace().Dims)
	vals, err = e.ReadValues()
	require.NoError(t, err)
	assert.Empty(t, vals)
	vals, err = e.ReadHyperslab(h5.Hyperslab{Start: []uint64{0, 0}, Count: []uint64{0, 3}})
	require.NoError(t, err)
	assert.Empty(t, vals)
	_, err = e.ReadHyperslab(h5.Hyperslab{Start: []uint64{0, 0}, Count: []uint64{1, 3}})
	assert.Error(t, err)
}

func TestCoverReferences(t *testing.T) {
	f := openCover(t, "cover.h5")
	info, err := openDS(t, f, "/target").Info()
	require.NoError(t, err)
	dt, err := f.OpenDatatype("/dtype")
	require.NoError(t, err)
	dtInfo, err := dt.Info()
	require.NoError(t, err)
	nullSpace, err := openDS(t, f, "/null_space").Info()
	require.NoError(t, err)

	/* The regions h5dump prints: points, the blocks of a strided
	 * hyperslab, none, all, a single point */
	regions := []h5.Selection{
		{Type: h5.SelPoints, Rank: 2, Points: [][]uint64{{1, 2}, {3, 4}, {5, 7}}},
		{Type: h5.SelHyperslab, Rank: 2, Blocks: [][2][]uint64{{{1, 1}, {1, 1}}, {{1, 4}, {1, 4}}, {{3, 1}, {3, 1}}, {{3, 4}, {3, 4}}}},
		{Type: h5.SelNone, Rank: 2},
		{Type: h5.SelAll, Rank: 2},
		{Type: h5.SelPoints, Rank: 2, Points: [][]uint64{{0, 0}}},
	}
	old, err := Read[h5.RegionRef](openDS(t, f, "/regref_old"))
	require.NoError(t, err)
	require.Len(t, old, len(regions))
	for i, r := range old {
		assert.Equal(t, h5.RegionRef{Address: info.Address, Selection: regions[i]}, r, "region %d", i)
	}

	vals, err := openDS(t, f, "/regref_new").ReadValues()
	require.NoError(t, err)
	require.Len(t, vals, 7)
	for i := range regions {
		assert.Equal(t, h5.RegionRef{Address: info.Address, Selection: regions[i]}, vals[i], "region %d", i)
	}
	assert.Equal(t, h5.ObjectRef{Address: info.Address}, vals[5])
	assert.Equal(t, h5.AttrRef{Address: nullSpace.Address, Name: "scalar_attr"}, vals[6])

	objs, err := Read[h5.ObjectRef](openDS(t, f, "/objref_old"))
	require.NoError(t, err)
	assert.Equal(t, []h5.ObjectRef{{Address: info.Address}, {Address: dtInfo.Address}}, objs)
	obj, err := f.OpenAddress(objs[1].Address)
	require.NoError(t, err)
	assert.IsType(t, &Datatype{}, obj)
}

func TestCoverCommittedDatatype(t *testing.T) {
	f := openCover(t, "cover.h5")
	want := &h5.Datatype{Class: h5.ClassCompound, Version: 3, Size: 12, Members: []h5.Member{
		{Name: "a", Offset: 0, Type: &h5.Datatype{Class: h5.ClassInteger, Version: 1, Size: 4, Precision: 32, Sign: h5.Sign2}},
		{Name: "b", Offset: 4, Type: &h5.Datatype{Class: h5.ClassFloat, Version: 1, Size: 8, Precision: 64, Sign: h5.Sign2,
			Float: h5.FloatInfo{SignPos: 63, ExpPos: 52, ExpSize: 11, MantSize: 52, ExpBias: 1023, Norm: h5.NormImplied}}},
	}}

	for _, open := range []func() (*Datatype, error){
		func() (*Datatype, error) { return f.OpenDatatype("/dtype") },
		func() (*Datatype, error) { return f.Root().OpenDatatype("dtype") },
	} {
		dt, err := open()
		require.NoError(t, err)
		assert.Equal(t, want.Members[0].Name, dt.Datatype().Members[0].Name)
		assert.Equal(t, want.Size, dt.Datatype().Size)
		n, err := dt.NumAttributes()
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		for a, err := range dt.Attributes() {
			require.NoError(t, err)
			got, err := ReadAttribute[string](a)
			require.NoError(t, err)
			assert.Equal(t, []string{"committed"}, got)
		}
		a, err := dt.Attribute("note")
		require.NoError(t, err)
		assert.Equal(t, "note", a.Name())
		c, err := dt.Comment()
		require.NoError(t, err)
		assert.Equal(t, "", c)
	}
	_, err := f.Root().OpenDatatype("typed")
	assert.Error(t, err)
	_, err = f.Root().OpenDatatype("nothing")
	assert.Error(t, err)

	d := openDS(t, f, "/typed")
	assert.Equal(t, h5.ClassCompound, d.Datatype().Class)
	type rec struct {
		A int32
		B float64
	}
	got, err := Read[rec](d)
	require.NoError(t, err)
	assert.Equal(t, []rec{{1, 0.5}, {2, 1.5}}, got)
}

func TestCoverVirtualAllSelections(t *testing.T) {
	f := openCover(t, "cover.h5")
	target, err := Read[int32](openDS(t, f, "/target"))
	require.NoError(t, err)

	/* all <- all */
	got, err := Read[int32](openDS(t, f, "/vds_all"))
	require.NoError(t, err)
	assert.Equal(t, target, got)

	/* all <- rows 3..4 */
	got, err = Read[int32](openDS(t, f, "/vds_all_src"))
	require.NoError(t, err)
	assert.Equal(t, target[24:40], got)

	/* rows 1..2 <- all, fill -1 */
	got, err = Read[int32](openDS(t, f, "/vds_src_all"))
	require.NoError(t, err)
	want := make([]int32, 32)
	for i := range want {
		want[i] = -1
		if i >= 8 && i < 24 {
			want[i] = int32(100 + i - 8)
		}
	}
	assert.Equal(t, want, got)
	vals, err := openDS(t, f, "/vds_src_all").ReadHyperslab(h5.Hyperslab{Start: []uint64{0, 6}, Count: []uint64{4, 1}})
	require.NoError(t, err)
	assert.Equal(t, []any{int32(-1), int32(106), int32(114), int32(-1)}, vals)

	l, err := openDS(t, f, "/vds_all").Layout()
	require.NoError(t, err)
	require.Len(t, l.Virtual, 1)
	assert.Equal(t, h5.SelAll, l.Virtual[0].VirtualSelection.Type)
	assert.Equal(t, h5.SelAll, l.Virtual[0].SourceSelection.Type)
	assert.Equal(t, ".", l.Virtual[0].SourceFile)
	assert.Equal(t, "/target", l.Virtual[0].SourceDataset)
}

func TestCoverGroupAttributes(t *testing.T) {
	f := openCover(t, "cover.h5")
	g, err := f.OpenGroup("/group_attrs")
	require.NoError(t, err)
	n, err := g.NumAttributes()
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	var got []string
	for a, err := range g.Attributes() {
		require.NoError(t, err)
		got = append(got, a.Name())
		vals, err := a.ReadValues()
		require.NoError(t, err)
		switch a.Name() {
		case "x":
			assert.Equal(t, []any{int32(1), int32(2)}, vals)
		case "y":
			assert.Equal(t, []any{2.5}, vals)
		}
	}
	assert.Equal(t, []string{"x", "y"}, got)

	/* Stopping the iteration early */
	for a, err := range g.Attributes() {
		require.NoError(t, err)
		assert.Equal(t, "x", a.Name())
		break
	}
	for a, err := range openDS(t, f, "/null_space").Attributes() {
		require.NoError(t, err)
		assert.Equal(t, "null_attr", a.Name())
		break
	}
}

func TestCoverSharedMessagesBtree(t *testing.T) {
	f := openCover(t, "cover_sohm.h5")
	for i := range 12 {
		for _, prefix := range []string{"d", "e"} {
			name := prefix + string(rune('0'+i/10)) + string(rune('0'+i%10))
			d := openDS(t, f, "/"+name)
			got, err := Read[int32](d)
			require.NoError(t, err, name)
			want := make([]int32, i+1)
			for j := range want {
				want[j] = int32(i*10 + j)
			}
			assert.Equal(t, want, got, name)
			assert.Equal(t, h5.ClassInteger, d.Datatype().Class)
			if prefix == "d" {
				a, err := d.Attribute("a")
				require.NoError(t, err, name)
				av, err := ReadAttribute[int32](a)
				require.NoError(t, err, name)
				assert.Equal(t, []int32{int32(i), -int32(i)}, av, name)
			}
		}
	}
}
