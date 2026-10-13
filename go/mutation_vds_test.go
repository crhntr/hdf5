package hdf5

// Tests for the virtual dataset code (h5d_virtual.go) written from the
// mutants gremlins left alive. Each test opens a small file assembled byte
// by byte (see mutation_h5o_test.go): int32 source datasets with
// contiguous storage in the root group and a virtual dataset "/vds" whose
// mappings refer to them (source file "."), and expects what libhdf5
// 1.14.6 reads from the same bytes (set HDF5_MUTATION_DUMP to a directory
// to have the tests write the files there for checking them with libhdf5).

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

/* Encoded selections (H5S_select_serialize) */

// encSelAll is an "all" selection.
func encSelAll() []byte { return []byte{3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0} }

// encSelNone is a "none" selection.
func encSelNone() []byte { return []byte{0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0} }

// encSelHyper is a regular hyperslab selection (version 3, 8-byte
// values; h5sUnlimited for an unlimited count or block). The arguments
// hold start, stride, count and block for each dimension.
func encSelHyper(dims ...[4]uint64) []byte {
	b := []byte{2, 0, 0, 0, 3, 0, 0, 0, 1, 8}
	b = binary.LittleEndian.AppendUint32(b, uint32(len(dims)))
	for _, d := range dims {
		for _, v := range d {
			b = le64a(b, v)
		}
	}
	return b
}

// encBlock is the hyperslab dimension selecting n elements from start.
func encBlock(start, n uint64) [4]uint64 { return [4]uint64{start, 1, 1, n} }

// vdsMap is a virtual dataset mapping.
type vdsMap struct {
	file, dset string
	src, virt  []byte /* encoded source and virtual selections */
}

// vdsLayout puts the global heap collection holding the mappings and
// returns the body of a version 4 virtual layout message referring to it.
func (b *h5Builder) vdsLayout(maps ...vdsMap) []byte {
	enc := le64a([]byte{0}, uint64(len(maps))) /* version, number of mappings */
	for _, m := range maps {
		enc = append(enc, m.file+"\x00"+m.dset+"\x00"...)
		enc = append(append(enc, m.src...), m.virt...)
	}
	enc = binary.LittleEndian.AppendUint32(enc, checksumLookup3(enc, 0))
	heap := b.put(gcol(enc))
	return binary.LittleEndian.AppendUint32(le64a([]byte{4, 3}, heap), 1)
}

// spaceMaxMsg is a version 2 dataspace message with maximum dimensions.
func spaceMaxMsg(dims, max []uint64) ohMsg {
	b := []byte{2, byte(len(dims)), 1, 1}
	for _, d := range dims {
		b = le64a(b, d)
	}
	for _, d := range max {
		b = le64a(b, d)
	}
	return ohMsg{typ: h5oSdspaceID, data: b}
}

// i32Contig puts an int32 dataset with contiguous storage holding vals and
// returns its object header address.
func (b *h5Builder) i32Contig(space ohMsg, vals ...int32) uint64 {
	var data []byte
	for _, v := range vals {
		data = binary.LittleEndian.AppendUint32(data, uint32(v))
	}
	addr := b.put(data)
	l := le64a(le64a([]byte{3, 1}, addr), uint64(len(data)))
	return b.put(ohdr2(2, v2Msgs(space, i32Type(), fillMsg(-1), layoutMsg(l))))
}

// i32Seq returns n values from first on.
func i32Seq(first int32, n int) []int32 {
	v := make([]int32, n)
	for i := range v {
		v[i] = first + int32(i)
	}
	return v
}

// vdsFile opens a file whose root group links "vds" to a virtual int32
// dataset with dataspace space, fill value -7 and the mappings build
// returns; build may link more objects (the source datasets) by
// appending to links.
func vdsFile(t *testing.T, space ohMsg, build func(b *h5Builder, links *[]ohMsg) []vdsMap) *File {
	t.Helper()
	b := newH5Builder()
	var links []ohMsg
	maps := build(b, &links)
	vds := b.put(ohdr2(2, v2Msgs(space, i32Type(), fillMsg(-7), layoutMsg(b.vdsLayout(maps...)))))
	root := b.rootGroup(append(links, hardLinkMsg("vds", vds))...)
	return builtFile(t, b.finish(root))
}

// readVDS opens /vds and reads it (nil values: the dataset cannot be
// opened or read).
func readVDS(t *testing.T, f *File) (dims []uint64, vals []int32, err error) {
	t.Helper()
	ds, err := f.OpenDataset("/vds")
	if err != nil {
		return nil, nil, err
	}
	dims = ds.Dataspace().Dims
	vals, err = Read[int32](ds)
	return dims, vals, err
}

// checkVDS opens and reads /vds and expects the dimensions dims (nil:
// opening fails) and the values want (nil: reading fails).
func checkVDS(t *testing.T, f *File, dims []uint64, want []int32) {
	t.Helper()
	gotDims, vals, err := readVDS(t, f)
	if dims == nil {
		assert.Nil(t, gotDims, "dataset opened")
		assert.Error(t, err)
		return
	}
	require.NotNil(t, gotDims, "dataset not opened: %v", err)
	assert.Equal(t, dims, gotDims)
	if want == nil {
		assert.Error(t, err)
		return
	}
	require.NoError(t, err)
	assert.Equal(t, want, vals)
}

// unlimRows is the virtual selection of a "printf" mapping in a
// one-dimensional dataset: blocks of block elements every stride
// elements, without end.
func unlimRows(stride, block uint64) []byte {
	return encSelHyper([4]uint64{0, stride, h5sUnlimited, block})
}

// TestMutationVirtualSourceNames: source names parsed for "printf" block
// number specifiers (h5d_virtual.go:357, 369): a dataset name ending in
// '%', which libhdf5 rejects (invalid format specifier) when it decodes
// the layout, and a "printf" mapping whose dataset name starts with the
// specifier ("%b": source datasets "0" and "1", two elements each, in
// blocks of two every two elements of the virtual dataset).
func TestMutationVirtualSourceNames(t *testing.T) {
	t.Run("trailing_percent", func(t *testing.T) {
		f := vdsFile(t, spaceMsg(2), func(b *h5Builder, links *[]ohMsg) []vdsMap {
			*links = append(*links, hardLinkMsg("src%", b.i32Contig(spaceMsg(2), 1, 2)))
			return []vdsMap{{".", "src%", encSelAll(), encSelAll()}}
		})
		_, err := f.OpenDataset("/vds")
		assert.Error(t, err)
	})
	t.Run("leading_specifier", func(t *testing.T) {
		f := vdsFile(t, spaceMaxMsg([]uint64{1}, []uint64{h5sUnlimited}), func(b *h5Builder, links *[]ohMsg) []vdsMap {
			*links = append(*links,
				hardLinkMsg("0", b.i32Contig(spaceMsg(2), 10, 11)),
				hardLinkMsg("1", b.i32Contig(spaceMsg(2), 20, 21)))
			return []vdsMap{{".", "%b", encSelAll(), unlimRows(2, 2)}}
		})
		dims, vals, err := readVDS(t, f)
		require.NoError(t, err)
		assert.Equal(t, []uint64{4}, dims)
		assert.Equal(t, []int32{10, 11, 20, 21}, vals)
	})
}

// TestMutationVirtualMinDims: the minimum dimensions of a virtual dataset
// (the bounds of its limited virtual selections, h5d_virtual.go:295, 309,
// 311, 312, 323): elements 0..3 mapped from a four-element dataset and
// element 4 from a one-element dataset. libhdf5 opens the dataset with
// five elements and refuses it with four (H5D_virtual_check_min_dims).
func TestMutationVirtualMinDims(t *testing.T) {
	for _, n := range []uint64{4, 5} {
		t.Run(fmt.Sprintf("dims%d", n), func(t *testing.T) {
			f := vdsFile(t, spaceMsg(n), func(b *h5Builder, links *[]ohMsg) []vdsMap {
				*links = append(*links,
					hardLinkMsg("a", b.i32Contig(spaceMsg(4), 1, 2, 3, 4)),
					hardLinkMsg("b", b.i32Contig(spaceMsg(1), 5)))
				return []vdsMap{
					{".", "a", encSelAll(), encSelHyper(encBlock(0, 4))},
					{".", "b", encSelAll(), encSelHyper(encBlock(4, 1))},
				}
			})
			if n == 4 {
				checkVDS(t, f, nil, nil)
				return
			}
			checkVDS(t, f, []uint64{5}, []int32{1, 2, 3, 4, 5})
		})
	}
}

// TestMutationVirtualMappingChecks: the checks of the mapping selections
// when the layout is decoded (H5D_virtual_check_mapping_pre,
// h5d_virtual.go:213) on a mapping of the source dataset a = {1, ..., 6}:
// unlimited virtual and source selections of rank 2 with the same number
// of elements in the limited dimension, which libhdf5 reads, and with
// different numbers (3 and 2), which it refuses to open; and a limited
// virtual selection mapped to an unlimited source selection, which
// libhdf5 opens (the counts are only compared when the source extent is
// known) but cannot read.
func TestMutationVirtualMappingChecks(t *testing.T) {
	unlim := h5sUnlimited
	for _, c := range []struct {
		name            string
		space, srcSpace ohMsg
		src, virt       []byte
		dims            []uint64 /* nil: libhdf5 cannot open the dataset */
		want            []int32  /* nil: libhdf5 cannot read the dataset */
	}{
		{"unlimited_both_rank2", spaceMaxMsg([]uint64{2, 3}, []uint64{unlim, 3}), spaceMaxMsg([]uint64{2, 3}, []uint64{unlim, 3}),
			encSelHyper(encBlock(0, unlim), encBlock(0, 3)), encSelHyper(encBlock(0, unlim), encBlock(0, 3)),
			[]uint64{2, 3}, []int32{1, 2, 3, 4, 5, 6}},
		{"unlimited_nonunlim_mismatch", spaceMaxMsg([]uint64{2, 3}, []uint64{unlim, 3}), spaceMaxMsg([]uint64{2, 3}, []uint64{unlim, 3}),
			encSelHyper(encBlock(0, unlim), encBlock(0, 2)), encSelHyper(encBlock(0, unlim), encBlock(0, 3)),
			nil, nil},
		{"limited_virtual_unlimited_source", spaceMsg(6), spaceMaxMsg([]uint64{6}, []uint64{unlim}),
			encSelHyper(encBlock(0, unlim)), encSelHyper(encBlock(0, 6)),
			[]uint64{6}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := vdsFile(t, c.space, func(b *h5Builder, links *[]ohMsg) []vdsMap {
				*links = append(*links, hardLinkMsg("a", b.i32Contig(c.srcSpace, i32Seq(1, 6)...)))
				return []vdsMap{{".", "a", c.src, c.virt}}
			})
			checkVDS(t, f, c.dims, c.want)
		})
	}
}

// TestMutationVirtualSelectionRanks: mapping selections whose rank
// differs from the dataspace they select in, in a virtual dataset {4}
// mapped from a (h5d_virtual.go:146, 506). libhdf5 reads an "all" source
// selection of a 2x2 source dataset and "none" selections (as the fill
// value, -7), and fails to read a "none" source selection mapped to an
// "all" virtual one and a virtual selection of rank 2. H5S_extent_copy
// gives a hyperslab source selection the source dataset's extent without
// checking the rank: libhdf5 then reads past the extent's dimensions
// (rank 2 selections of a rank 1 dataset read 1 2 1 2, 1 1 1 1 or 1 2 3
// 4) or crashes (SIGFPE, a rank 1 selection of a rank 2 dataset); hdf5
// refuses them.
func TestMutationVirtualSelectionRanks(t *testing.T) {
	for _, c := range []struct {
		name     string
		srcSpace ohMsg
		src      []byte
		virt     []byte
		want     []int32 /* nil: the dataset cannot be read */
	}{
		{"source_all_2d", spaceMsg(2, 2), encSelAll(), encSelHyper(encBlock(0, 4)), []int32{1, 2, 3, 4}},
		{"source_hyper_2d", spaceMsg(4), encSelHyper(encBlock(0, 2), encBlock(0, 2)), encSelHyper(encBlock(0, 4)), nil},
		{"source_hyper_2d_row", spaceMsg(4), encSelHyper(encBlock(0, 1), encBlock(0, 4)), encSelHyper(encBlock(0, 4)), nil},
		{"source_hyper_2d_col", spaceMsg(4), encSelHyper(encBlock(0, 4), encBlock(0, 1)), encSelHyper(encBlock(0, 4)), nil},
		{"source_hyper_1d", spaceMsg(2, 2), encSelHyper(encBlock(0, 4)), encSelHyper(encBlock(0, 4)), nil},
		{"none", spaceMsg(4), encSelNone(), encSelNone(), []int32{-7, -7, -7, -7}},
		{"source_none", spaceMsg(4), encSelNone(), encSelAll(), nil},
		{"virtual_none", spaceMsg(4), encSelAll(), encSelNone(), []int32{-7, -7, -7, -7}},
		{"virtual_hyper_2d", spaceMsg(4), encSelAll(), encSelHyper(encBlock(0, 2), encBlock(0, 2)), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := vdsFile(t, spaceMsg(4), func(b *h5Builder, links *[]ohMsg) []vdsMap {
				*links = append(*links, hardLinkMsg("a", b.i32Contig(c.srcSpace, 1, 2, 3, 4)))
				return []vdsMap{{".", "a", c.src, c.virt}}
			})
			_, vals, err := readVDS(t, f)
			if c.want == nil {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, vals)
		})
	}
}

// setVDSView gives the virtual dataset /vds the view and printf gap of a
// dataset access property list (H5Pset_virtual_view,
// H5Pset_virtual_printf_gap) and recomputes its extent (H5Dget_space),
// as TestVirtualViewAndGap does.
func setVDSView(t *testing.T, f *File, view int, gap uint64) *Dataset {
	t.Helper()
	d, err := f.OpenDataset("/vds")
	require.NoError(t, err)
	ds, err := d.dataset()
	require.NoError(t, err)
	ds.virt.view, ds.virt.printfGap, ds.virt.init = view, gap, false
	for i := range ds.virt.list {
		ent := &ds.virt.list[i]
		ent.unlimExtentSource, ent.unlimExtentVirtual = h5sUnlimited, h5sUnlimited
		ent.clipSizeSource, ent.clipSizeVirtual = h5sUnlimited, h5sUnlimited
	}
	_, err = f.h5dGetSpace(ds)
	require.NoError(t, err)
	return d
}

// TestMutationVirtualPrintf: the extent of "printf" virtual datasets
// (H5D__virtual_set_extent_unlim, h5d_virtual.go:776, 786) whose
// stored extent (two elements) differs from the one computed from the
// source datasets, read with both views: source datasets s0 = {10, 11}
// and s1 = {20, 21} (but no s2) in blocks of two elements every three
// elements, and no source datasets at all.
func TestMutationVirtualPrintf(t *testing.T) {
	for _, c := range []struct {
		name    string
		sources int
		view    int
		dims    []uint64
		want    []int32
	}{
		{"last_available", 2, h5dVdsLastAvailable, []uint64{5}, []int32{10, 11, -7, 20, 21}},
		{"first_missing", 2, h5dVdsFirstMissing, []uint64{6}, []int32{10, 11, -7, 20, 21, -7}},
		{"no_sources_last_available", 0, h5dVdsLastAvailable, []uint64{0}, []int32{}},
		{"no_sources_first_missing", 0, h5dVdsFirstMissing, []uint64{0}, []int32{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := vdsFile(t, spaceMaxMsg([]uint64{2}, []uint64{h5sUnlimited}), func(b *h5Builder, links *[]ohMsg) []vdsMap {
				for i := range c.sources {
					v := int32(10 * (i + 1))
					*links = append(*links, hardLinkMsg(fmt.Sprintf("s%d", i), b.i32Contig(spaceMsg(2), v, v+1)))
				}
				return []vdsMap{{".", "s%b", encSelAll(), unlimRows(3, 2)}}
			})
			d := setVDSView(t, f, c.view, 0)
			assert.Equal(t, c.dims, d.Dataspace().Dims)
			vals, err := Read[int32](d)
			require.NoError(t, err)
			assert.Equal(t, c.want, vals)
		})
	}
}

// TestMutationVirtualUnlimited: the extent of virtual datasets with
// unlimited (not "printf") mappings whose stored extent (two elements)
// differs from the one computed from the source datasets, read with both
// views: one mapping of all of a = {1, 2, 3, 4} (maximum dimension
// unlimited), and two interleaved mappings, of a = {1, 2, 3} to the even
// elements and of b = {10} to the odd ones (the first missing element is
// b's second, element 3).
func TestMutationVirtualUnlimited(t *testing.T) {
	unlimSpace := func(vals ...int32) ohMsg { return spaceMaxMsg([]uint64{uint64(len(vals))}, []uint64{h5sUnlimited}) }
	all := encSelHyper(encBlock(0, h5sUnlimited))
	every := func(start uint64) []byte { return encSelHyper([4]uint64{start, 2, h5sUnlimited, 1}) }
	one := func(b *h5Builder, links *[]ohMsg) []vdsMap {
		a := []int32{1, 2, 3, 4}
		*links = append(*links, hardLinkMsg("a", b.i32Contig(unlimSpace(a...), a...)))
		return []vdsMap{{".", "a", all, all}}
	}
	two := func(b *h5Builder, links *[]ohMsg) []vdsMap {
		a, bb := []int32{1, 2, 3}, []int32{10}
		*links = append(*links,
			hardLinkMsg("a", b.i32Contig(unlimSpace(a...), a...)),
			hardLinkMsg("b", b.i32Contig(unlimSpace(bb...), bb...)))
		return []vdsMap{{".", "a", all, every(0)}, {".", "b", all, every(1)}}
	}
	for _, c := range []struct {
		name  string
		build func(b *h5Builder, links *[]ohMsg) []vdsMap
		view  int
		dims  []uint64
		want  []int32
	}{
		{"one_last_available", one, h5dVdsLastAvailable, []uint64{4}, []int32{1, 2, 3, 4}},
		{"one_first_missing", one, h5dVdsFirstMissing, []uint64{4}, []int32{1, 2, 3, 4}},
		{"two_last_available", two, h5dVdsLastAvailable, []uint64{5}, []int32{1, 10, 2, -7, 3}},
		{"two_first_missing", two, h5dVdsFirstMissing, []uint64{3}, []int32{1, 10, 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := vdsFile(t, spaceMaxMsg([]uint64{2}, []uint64{h5sUnlimited}), c.build)
			d := setVDSView(t, f, c.view, 0)
			assert.Equal(t, c.dims, d.Dataspace().Dims)
			vals, err := Read[int32](d)
			require.NoError(t, err)
			assert.Equal(t, c.want, vals)
		})
	}
}

// TestMutationVirtualPrintfMinDims: a "printf" mapping in a virtual
// dataset whose extent is set by a limited mapping (element 11 from
// c = {99}): the source datasets s0 = {10, 11}, s1 = {20, 21} and
// s3 = {40, 41} (but no s2) in blocks of two elements every three
// elements. The stored extent (13 elements) shrinks to 12, the minimum;
// with the default printf gap (0) the search for source datasets stops
// at s2, so the block of s3 reads as the fill value although it lies
// within the extent (h5d_virtual.go:852: the mapping is not initialized
// again from the extent before reading).
func TestMutationVirtualPrintfMinDims(t *testing.T) {
	f := vdsFile(t, spaceMaxMsg([]uint64{13}, []uint64{h5sUnlimited}), func(b *h5Builder, links *[]ohMsg) []vdsMap {
		for _, i := range []int{0, 1, 3} {
			v := int32(10 * (i + 1))
			*links = append(*links, hardLinkMsg(fmt.Sprintf("s%d", i), b.i32Contig(spaceMsg(2), v, v+1)))
		}
		*links = append(*links, hardLinkMsg("c", b.i32Contig(spaceMsg(1), 99)))
		return []vdsMap{
			{".", "c", encSelAll(), encSelHyper(encBlock(11, 1))},
			{".", "s%b", encSelAll(), unlimRows(3, 2)},
		}
	})
	checkVDS(t, f, []uint64{12}, []int32{10, 11, -7, 20, 21, -7, -7, -7, -7, -7, -7, 99})
}

// writeImage writes img to dir/name (and to $HDF5_MUTATION_DUMP/name when
// that variable is set).
func writeImage(t *testing.T, dir, name string, img []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), img, 0o644))
	if dump := os.Getenv("HDF5_MUTATION_DUMP"); dump != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dump, name), img, 0o644))
	}
}

// TestMutationVirtualPrintfGrowth: a "printf" mapping whose source files
// src_<n>.h5 (dataset /x, two elements) appear after the virtual dataset
// was opened, in blocks of two elements every three elements
// (h5d_virtual.go:776): libhdf5 reports the extent 2 while only src_0.h5
// exists, and 5 from H5Dget_space once src_1.h5 is there.
func TestMutationVirtualPrintfGrowth(t *testing.T) {
	dir := t.TempDir()
	src := func(v int32) []byte {
		b := newH5Builder()
		x := b.i32Contig(spaceMsg(2), v, v+1)
		return b.finish(b.rootGroup(hardLinkMsg("x", x)))
	}
	b := newH5Builder()
	layout := b.vdsLayout(vdsMap{"src_%b.h5", "/x", encSelAll(), unlimRows(3, 2)})
	vds := b.put(ohdr2(2, v2Msgs(spaceMaxMsg([]uint64{1}, []uint64{h5sUnlimited}), i32Type(), fillMsg(-7), layoutMsg(layout))))
	writeImage(t, dir, "growth_vds.h5", b.finish(b.rootGroup(hardLinkMsg("vds", vds))))
	writeImage(t, dir, "src_0.h5", src(10))

	f, err := Open(filepath.Join(dir, "growth_vds.h5"))
	require.NoError(t, err)
	defer f.Close()
	d, err := f.OpenDataset("/vds")
	require.NoError(t, err)
	assert.Equal(t, []uint64{2}, d.Dataspace().Dims)

	writeImage(t, dir, "src_1.h5", src(20))
	ds, err := d.dataset()
	require.NoError(t, err)
	space, err := f.h5dGetSpace(ds)
	require.NoError(t, err)
	assert.Equal(t, []uint64{5}, space.extent.size)
	vals, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{10, 11, -7, 20, 21}, vals)
}

// TestMutationVirtualPrintfPartialRead: a hyperslab read of elements 0..2
// of the "printf" virtual dataset of TestMutationVirtualSourceNames
// ("%b": 0 = {10, 11}, 1 = {20, 21} in blocks of two), which ends inside
// the block of source dataset 1 (H5D__virtual_pre_io, h5d_virtual.go:1283:
// the partial block is read). libhdf5 reads 10 11 20.
func TestMutationVirtualPrintfPartialRead(t *testing.T) {
	f := vdsFile(t, spaceMaxMsg([]uint64{1}, []uint64{h5sUnlimited}), func(b *h5Builder, links *[]ohMsg) []vdsMap {
		*links = append(*links,
			hardLinkMsg("0", b.i32Contig(spaceMsg(2), 10, 11)),
			hardLinkMsg("1", b.i32Contig(spaceMsg(2), 20, 21)))
		return []vdsMap{{".", "%b", encSelAll(), unlimRows(2, 2)}}
	})
	d, err := f.OpenDataset("/vds")
	require.NoError(t, err)
	got, err := d.ReadHyperslab(h5.Hyperslab{Start: []uint64{0}, Count: []uint64{3}})
	require.NoError(t, err)
	assert.Equal(t, []any{int32(10), int32(11), int32(20)}, got)
}

// i16Type is a datatype message for a 16-bit signed integer, big-endian
// when be is set.
func i16Type(be bool) ohMsg {
	flags := byte(0x08)
	if be {
		flags |= 1
	}
	return ohMsg{typ: h5oDtypeID, data: []byte{0x10, flags, 0, 0, 2, 0, 0, 0, 0, 0, 16, 0}}
}

// TestMutationVirtualByteOrder: virtual datasets whose elements differ
// from their source's only by byte order, the one conversion hdf5 makes
// (h5d_virtual.go:1547, 1555): a big-endian int16 virtual dataset mapped
// from a little-endian one {1, -2} and the reverse. libhdf5 converts: both
// read 1 -2.
func TestMutationVirtualByteOrder(t *testing.T) {
	for _, vdsBE := range []bool{true, false} {
		t.Run(fmt.Sprintf("vds_be=%v", vdsBE), func(t *testing.T) {
			b := newH5Builder()
			raw := []byte{0, 1, 0xff, 0xfe} /* big-endian 1, -2 */
			if vdsBE {
				raw = []byte{1, 0, 0xfe, 0xff}
			}
			data := b.put(raw)
			src := b.put(ohdr2(2, v2Msgs(spaceMsg(2), i16Type(!vdsBE), layoutMsg(le64a(le64a([]byte{3, 1}, data), 4)))))
			l := b.vdsLayout(vdsMap{".", "a", encSelAll(), encSelAll()})
			vds := b.put(ohdr2(2, v2Msgs(spaceMsg(2), i16Type(vdsBE), layoutMsg(l))))
			f := builtFile(t, b.finish(b.rootGroup(hardLinkMsg("a", src), hardLinkMsg("vds", vds))))
			d, err := f.OpenDataset("/vds")
			require.NoError(t, err)
			got, err := Read[int16](d)
			require.NoError(t, err)
			assert.Equal(t, []int16{1, -2}, got)
		})
	}
}

// TestMutationVirtualNested: a virtual dataset {5} whose mappings are, in
// this order, element 4 from c = {9}, elements 0..1 from b = {1, 2} and
// elements 2..3 from the virtual dataset v {2} (fill value -3), which maps
// its element 0 from a = {5} and leaves element 1 unmapped
// (H5D__virtual_read of v into a part of the buffer, h5d_virtual.go:1666:
// only the unmapped elements of that part get v's fill value). libhdf5
// reads 1 2 5 -3 9.
func TestMutationVirtualNested(t *testing.T) {
	f := vdsFile(t, spaceMsg(5), func(b *h5Builder, links *[]ohMsg) []vdsMap {
		vl := b.vdsLayout(vdsMap{".", "a", encSelAll(), encSelHyper(encBlock(0, 1))})
		v := b.put(ohdr2(2, v2Msgs(spaceMsg(2), i32Type(), fillMsg(-3), layoutMsg(vl))))
		*links = append(*links,
			hardLinkMsg("a", b.i32Contig(spaceMsg(1), 5)),
			hardLinkMsg("b", b.i32Contig(spaceMsg(2), 1, 2)),
			hardLinkMsg("c", b.i32Contig(spaceMsg(1), 9)),
			hardLinkMsg("v", v))
		return []vdsMap{
			{".", "c", encSelAll(), encSelHyper(encBlock(4, 1))},
			{".", "b", encSelAll(), encSelHyper(encBlock(0, 2))},
			{".", "v", encSelAll(), encSelHyper(encBlock(2, 2))},
		}
	})
	checkVDS(t, f, []uint64{5}, []int32{1, 2, 5, -3, 9})
}

// TestMutationVirtualShortFill: a virtual dataset {2} whose fill value
// message holds two bytes for its int32 elements, with element 0 mapped
// from a = {1} (h5d_virtual.go:1601: the failure to fill element 1 is
// reported). libhdf5 reads element 1 from the two bytes of the fill
// value and the two bytes that follow them in memory (it checks the size
// only when it converts the fill value); hdf5 fails to read it.
func TestMutationVirtualShortFill(t *testing.T) {
	b := newH5Builder()
	a := b.i32Contig(spaceMsg(1), 1)
	l := b.vdsLayout(vdsMap{".", "a", encSelAll(), encSelHyper(encBlock(0, 1))})
	fill := ohMsg{typ: h5oFillNewID, data: []byte{3, 0x2a, 2, 0, 0, 0, 7, 0}}
	vds := b.put(ohdr2(2, v2Msgs(spaceMsg(2), i32Type(), fill, layoutMsg(l))))
	f := builtFile(t, b.finish(b.rootGroup(hardLinkMsg("a", a), hardLinkMsg("vds", vds))))
	_, _, err := readVDS(t, f)
	assert.Error(t, err)
}

// TestMutationVirtualUnlimitedViews: unlimited mappings read with both
// views (H5D__virtual_set_extent_unlim, h5d_virtual.go:490, 859): a = {1,
// 2, 3, 4} mapped to the even elements and the missing dataset z to the
// odd ones (libhdf5: 7 elements 1 -7 2 -7 3 -7 4 with the last available
// view, none with the first missing one); and a mapped to the unlimited
// second dimension of a {1, n} virtual dataset. For that one libhdf5 reads
// 1 2 3 4 with the last available view but 1 -7 -7 -7 with the first
// missing one: when it clips the virtual selection to the new extent it
// takes the size of the dimension that is unlimited in the source
// selection (0), not in the virtual one.
func TestMutationVirtualUnlimitedViews(t *testing.T) {
	all := encSelHyper(encBlock(0, h5sUnlimited))
	every := func(start uint64) []byte { return encSelHyper([4]uint64{start, 2, h5sUnlimited, 1}) }
	a := func(b *h5Builder, links *[]ohMsg) {
		*links = append(*links, hardLinkMsg("a", b.i32Contig(spaceMaxMsg([]uint64{4}, []uint64{h5sUnlimited}), 1, 2, 3, 4)))
	}
	missing := func(b *h5Builder, links *[]ohMsg) []vdsMap {
		a(b, links)
		return []vdsMap{{".", "a", all, every(0)}, {".", "z", all, every(1)}}
	}
	rank2 := func(b *h5Builder, links *[]ohMsg) []vdsMap {
		a(b, links)
		return []vdsMap{{".", "a", all, encSelHyper(encBlock(0, 1), encBlock(0, h5sUnlimited))}}
	}
	space1 := spaceMaxMsg([]uint64{2}, []uint64{h5sUnlimited})
	space2 := spaceMaxMsg([]uint64{1, 2}, []uint64{1, h5sUnlimited})
	for _, c := range []struct {
		name  string
		space ohMsg
		build func(b *h5Builder, links *[]ohMsg) []vdsMap
		view  int
		dims  []uint64
		want  []int32
	}{
		{"missing_last_available", space1, missing, h5dVdsLastAvailable, []uint64{7}, []int32{1, -7, 2, -7, 3, -7, 4}},
		{"missing_first_missing", space1, missing, h5dVdsFirstMissing, []uint64{0}, []int32{}},
		{"rank2_last_available", space2, rank2, h5dVdsLastAvailable, []uint64{1, 4}, []int32{1, 2, 3, 4}},
		{"rank2_first_missing", space2, rank2, h5dVdsFirstMissing, []uint64{1, 4}, []int32{1, -7, -7, -7}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := vdsFile(t, c.space, c.build)
			d := setVDSView(t, f, c.view, 0)
			assert.Equal(t, c.dims, d.Dataspace().Dims)
			vals, err := Read[int32](d)
			require.NoError(t, err)
			assert.Equal(t, c.want, vals)
		})
	}
}

// vlenStrSource puts a global heap collection holding "ab" and a dataset
// {1} of variable-length strings referring to it, and returns the
// dataset's object header address.
func (b *h5Builder) vlenStrSource() uint64 {
	heap := b.put(gcol([]byte("ab")))
	elem := binary.LittleEndian.AppendUint32(le64a(binary.LittleEndian.AppendUint32(nil, 2), heap), 1)
	data := b.put(elem)
	l := le64a(le64a([]byte{3, 1}, data), 16)
	return b.put(ohdr2(2, v2Msgs(spaceMsg(1), ohMsg{typ: h5oDtypeID, data: vlenStrType(16)}, layoutMsg(l))))
}

// TestMutationVirtualVlenSource: a virtual dataset of variable-length
// strings mapped from the dataset {"ab"} (h5d_virtual.go:1499). From the
// virtual dataset's own file libhdf5 and hdf5 read "ab"; from another file
// (whose heap holding "ab" lies where the virtual dataset's file has a heap
// holding "zz") libhdf5 reads "ab" too, but hdf5 refuses (it does not
// convert the heap references of another file, see the deviations in
// h5d_virtual.go).
func TestMutationVirtualVlenSource(t *testing.T) {
	vds := func(b *h5Builder, file string) uint64 {
		l := b.vdsLayout(vdsMap{file, "/s", encSelAll(), encSelAll()})
		return b.put(ohdr2(2, v2Msgs(spaceMsg(1), ohMsg{typ: h5oDtypeID, data: vlenStrType(16)}, layoutMsg(l))))
	}
	t.Run("same_file", func(t *testing.T) {
		b := newH5Builder()
		s := b.vlenStrSource()
		v := vds(b, ".")
		f := builtFile(t, b.finish(b.rootGroup(hardLinkMsg("s", s), hardLinkMsg("vds", v))))
		d, err := f.OpenDataset("/vds")
		require.NoError(t, err)
		got, err := d.ReadValues()
		require.NoError(t, err)
		assert.Equal(t, []any{"ab"}, got)
	})
	t.Run("other_file", func(t *testing.T) {
		dir := t.TempDir()
		b := newH5Builder()
		s := b.vlenStrSource()
		writeImage(t, dir, "vlen_src.h5", b.finish(b.rootGroup(hardLinkMsg("s", s))))
		b = newH5Builder()
		/* (a heap holding "zz" where the source file holds "ab") */
		require.Equal(t, uint64(48), b.put(gcol([]byte("zz"))))
		v := vds(b, "vlen_src.h5")
		writeImage(t, dir, "vlen_vds.h5", b.finish(b.rootGroup(hardLinkMsg("vds", v))))
		f, err := Open(filepath.Join(dir, "vlen_vds.h5"))
		require.NoError(t, err)
		defer f.Close()
		d, err := f.OpenDataset("/vds")
		require.NoError(t, err)
		_, err = d.ReadValues()
		assert.Error(t, err)
	})
}
