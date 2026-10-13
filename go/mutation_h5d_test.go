package hdf5

// Tests for the dataset storage code (h5d_chunk.go, h5d_earray.go,
// h5d_efl.go) written from the mutants gremlins left alive. The expected
// values are what libhdf5 1.14.6 reads from the same files: the generated
// files of testdata (whose generators record what libhdf5 returns), and
// files assembled byte by byte with the builders of mutation_h5o_test.go
// (set HDF5_MUTATION_DUMP to a directory to have the tests write them
// there, with the external data files they use, for checking them with
// libhdf5).

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eaUnlimMid is the value of /ea_unlim_mid of testdata/chunkidx.h5 (int16
// {4,5,6}, chunk {2,2,3}) at (a, b, c), as testdata/gen_chunkidx.c writes
// it: chunk (i, j, k) holds the bytes v, v+1, ... (v = 100i+10j+k) in the
// row-major order of its part inside the extent; chunks {0,1,1} and
// {1,2,0} are not written (fill value 0).
func eaUnlimMid(a, b, c int) int16 {
	i, j, k := a/2, b/2, c/3
	if (i == 0 && j == 1 && k == 1) || (i == 1 && j == 2 && k == 0) {
		return 0
	}
	cb := min(2, 5-2*j) /* the edge chunks of dimension 1 hold one row */
	e := ((a-2*i)*cb+(b-2*j))*3 + (c - 3*k)
	v := 100*i + 10*j + k
	return int16(uint16(byte(v+2*e)) | uint16(byte(v+2*e+1))<<8)
}

// TestMutationChunkHyperslabRows: a hyperslab selection of a chunked
// dataset spanning three chunks in a dimension other than the fastest one
// (H5D__create_piece_file_map_hyper steps the end of the chunk block in
// that dimension, h5d_chunk.go:695).
func TestMutationChunkHyperslabRows(t *testing.T) {
	f := openTestdata(t, "chunkidx.h5")
	ds, err := f.OpenDataset("/ea_unlim_mid")
	require.NoError(t, err)
	for _, sel := range []h5.Hyperslab{
		{Start: []uint64{0, 0, 0}, Count: []uint64{4, 5, 6}},
		{Start: []uint64{1, 1, 2}, Count: []uint64{3, 4, 2}},
		/* rows 3 and 4 of dimension 1 are in its chunks 1 and 2: the
		 * selection starts after the end of chunk 0 */
		{Start: []uint64{0, 3, 0}, Count: []uint64{4, 2, 6}},
		/* row 4 is in chunk 2 of dimension 1: going to the next row of
		 * chunks in dimension 0 brings dimension 1 back to chunk 2
		 * (h5d_chunk.go:685) */
		{Start: []uint64{0, 4, 0}, Count: []uint64{4, 1, 6}},
	} {
		got, err := ds.ReadHyperslab(sel)
		require.NoError(t, err)
		var want []any
		for a := sel.Start[0]; a < sel.Start[0]+sel.Count[0]; a++ {
			for b := sel.Start[1]; b < sel.Start[1]+sel.Count[1]; b++ {
				for c := sel.Start[2]; c < sel.Start[2]+sel.Count[2]; c++ {
					want = append(want, eaUnlimMid(int(a), int(b), int(c)))
				}
			}
		}
		assert.Equal(t, want, got, "%v", sel)
	}
	all, err := Read[int16](ds)
	require.NoError(t, err)
	for n, v := range all {
		require.Equal(t, eaUnlimMid(n/30, n/6%5, n%6), v, "element %d", n)
	}
}

// TestMutationEarrayTwoUnlimitedDims: /ea_unlim_mid of
// testdata/chunkidx.h5 (maximum dimensions {4,UNLIM,6}, extensible array
// index) with the first maximum dimension changed to unlimited too.
// H5D__earray_idx_init finds the second unlimited dimension
// (h5d_earray.go:240) and libhdf5 fails to open the dataset.
func TestMutationEarrayTwoUnlimitedDims(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "chunkidx.h5"))
	require.NoError(t, err)
	f := builtFile(t, raw)
	ds, err := f.OpenDataset("/ea_unlim_mid")
	require.NoError(t, err)
	info, err := ds.Info()
	require.NoError(t, err)
	patchMessage(t, raw, info.Address, h5oSdspaceID, func(msg []byte) {
		require.Equal(t, []byte{2, 3, 1, 1}, msg[:4])
		require.Equal(t, uint64(4), binary.LittleEndian.Uint64(msg[28:]))
		binary.LittleEndian.PutUint64(msg[28:], h5sUnlimitedDim)
	})
	f = builtFile(t, raw)
	_, err = f.OpenDataset("/ea_unlim_mid")
	assert.ErrorContains(t, err, "already found unlimited dimension")
}

// TestMutationChunkIterate:H5D__chunk_iter on /ea_paged of
// testdata/chunkidx.h5 (an unfiltered dataset with an extensible array
// index: H5D__earray_idx_iterate reads plain addresses, h5d_earray.go:501),
// as is and behind a 512-byte user block (the chunk addresses are
// absolute: base address + chunk address, h5d_chunk.go:1055). libhdf5
// (H5Dchunk_iter) reports the chunks of chunkidx_expected.txt, at
// addresses 512 higher in the second case, and H5Dget_storage_size 13.
func TestMutationChunkIterate(t *testing.T) {
	_, iters := readChunkidxExpected(t)
	want := iters["ea_paged"]
	require.Len(t, want, 13)
	raw, err := os.ReadFile(filepath.Join("testdata", "chunkidx.h5"))
	require.NoError(t, err)
	for _, ub := range []int{0, 512} {
		img := append(make([]byte, ub), raw...)
		f, err := OpenReader(bytes.NewReader(img), int64(len(img)))
		require.NoError(t, err)
		t.Cleanup(func() { f.Close() })
		ds := testOpenDataset(t, f, "/ea_paged")
		var got []chunkidxLine
		require.NoError(t, f.h5dChunkIter(ds, func(off []uint64, mask uint32, addr haddr, n uint64) error {
			got = append(got, chunkidxLine{scaled: off, addr: addr, nbytes: n, mask: mask})
			return nil
		}))
		require.Len(t, got, len(want), "user block %d", ub)
		for i, w := range want {
			w.addr += uint64(ub)
			assert.Equal(t, w, got[i], "user block %d", ub)
		}
		assert.Equal(t, uint64(13), ds.storageSize(f), "user block %d", ub)
	}
}

// TestMutationChunkIterateIndices: H5D__chunk_iter on the datasets of
// test/testfiles/h5fc_ext_none.h5 (int32 {4,6}, chunk {2,3}) with a v2
// B-tree index (h5d_chunk.go:1159) and an implicit index (the number of
// chunks of a dataset whose dimensions are multiples of the chunk's,
// h5d_chunk.go:156), as H5Dchunk_iter reports them.
func TestMutationChunkIterateIndices(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "test", "testfiles", "h5fc_ext_none.h5"))
	require.NoError(t, err)
	defer f.Close()
	offsets := [][]uint64{{0, 0}, {0, 3}, {2, 0}, {2, 3}}
	for path, addr := range map[string]haddr{"/GROUP/DSET_BT2": 2192, "/DSET_NONE": 2384} {
		ds := testOpenDataset(t, f, path)
		var got []chunkidxLine
		require.NoError(t, f.h5dChunkIter(ds, func(off []uint64, mask uint32, a haddr, n uint64) error {
			got = append(got, chunkidxLine{scaled: off, addr: a, nbytes: n, mask: mask})
			return nil
		}))
		var want []chunkidxLine
		for i, off := range offsets {
			want = append(want, chunkidxLine{scaled: off, addr: addr + 24*uint64(i), nbytes: 24})
		}
		assert.Equal(t, want, got, path)
		assert.Equal(t, uint64(96), ds.storageSize(f), path)
	}
}

// v4Chunked is a version 4 layout message for a chunked dataset: flags,
// the chunk dimensions (the last one the element size) encoded in enc
// bytes, the index type and its parameters, and the index address.
func v4Chunked(flags byte, dims []uint64, enc int, idxAddr uint64, index ...byte) ohMsg {
	l := []byte{4, 2, flags, byte(len(dims)), byte(enc)}
	for _, d := range dims {
		l = append(l, le64a(nil, d)[:enc]...)
	}
	return layoutMsg(le64a(append(l, index...), idxAddr))
}

// u8Type is a datatype message for H5T_STD_U8LE.
func u8Type() ohMsg {
	return ohMsg{typ: h5oDtypeID, data: []byte{0x10, 0, 0, 0, 1, 0, 0, 0, 0, 0, 8, 0}}
}

// TestMutationChunkLayouts: chunked datasets at the limits of
// H5D__chunk_init, H5D__chunk_set_sizes, H5D__chunk_set_info_real and the
// chunk mapping and reading code (h5d_chunk.go). libhdf5 opens all of
// them, reads the values given (H5Dread), and reports the chunks given
// (H5Dchunk_iter: offsets in elements, relative addresses) and the
// storage size (H5Dget_storage_size).
func TestMutationChunkLayouts(t *testing.T) {
	const (
		idxSingle   = 1
		idxImplicit = 2
	)
	/* An int32 dataset of dims with an implicit index (all chunks
	 * allocated, in row-major order, at the index address) whose element
	 * (r, c) is 10r+c (999 in the parts of the edge chunks outside the
	 * extent) */
	implicit := func(b *h5Builder, dims, chunk [2]uint64) (ohMsg, []int32, int) {
		nc := [2]uint64{(dims[0] + chunk[0] - 1) / chunk[0], (dims[1] + chunk[1] - 1) / chunk[1]}
		size := chunk[0] * chunk[1] * 4
		raw := make([]byte, nc[0]*nc[1]*size)
		for i := range nc[0] {
			for j := range nc[1] {
				for a := range chunk[0] {
					for c := range chunk[1] {
						v := int32(999)
						if r, s := i*chunk[0]+a, j*chunk[1]+c; r < dims[0] && s < dims[1] {
							v = int32(10*r + s)
						}
						off := (i*nc[1]+j)*size + (a*chunk[1]+c)*4
						binary.LittleEndian.PutUint32(raw[off:], uint32(v))
					}
				}
			}
		}
		var want []int32
		for r := range dims[0] {
			for c := range dims[1] {
				want = append(want, int32(10*r+c))
			}
		}
		return v4Chunked(0, []uint64{chunk[0], chunk[1], 4}, 1, b.put(raw), idxImplicit), want, int(nc[0] * nc[1])
	}
	type result struct {
		values  []any
		nchunks int
		storage uint64
	}
	i32any := func(v []int32) []any {
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out
	}
	for _, c := range []struct {
		name  string
		build func(b *h5Builder) (msgs []ohMsg, want result)
	}{
		/* Edge chunks in both dimensions: 3x3 chunks of 24 bytes (the
		 * number of chunks and of maximum chunks, rounded up,
		 * h5d_chunk.go:156, 160) */
		{"implicit_5x7", func(b *h5Builder) ([]ohMsg, result) {
			l, want, n := implicit(b, [2]uint64{5, 7}, [2]uint64{2, 3})
			return []ohMsg{spaceMsg(5, 7), i32Type(), fillMsg(-7), l}, result{i32any(want), n, uint64(n) * 24}
		}},
		/* A dimension smaller than the chunk by one: every chunk is an
		 * edge chunk in that dimension (h5d_chunk.go:509) */
		{"implicit_4x2", func(b *h5Builder) ([]ohMsg, result) {
			l, want, n := implicit(b, [2]uint64{4, 2}, [2]uint64{2, 3})
			return []ohMsg{spaceMsg(4, 2), i32Type(), fillMsg(-7), l}, result{i32any(want), n, uint64(n) * 24}
		}},
		/* The largest chunk dimension and chunk size, 2^32-1 bytes
		 * (h5d_chunk.go:263, 268); the chunk is not allocated */
		{"chunk_dim_4GiB", func(b *h5Builder) ([]ohMsg, result) {
			l := v4Chunked(0, []uint64{0xffffffff, 1}, 4, haddrUndef, idxSingle)
			return []ohMsg{spaceMsg(4), u8Type(), l}, result{nil, 0, 0}
		}},
		/* The largest datatype, 2^32-1 bytes, in chunks of one element
		 * (h5d_chunk.go:204, 222); the chunk is not allocated */
		{"type_4GiB", func(b *h5Builder) ([]ohMsg, result) {
			opaque := ohMsg{typ: h5oDtypeID, data: append([]byte{0x15, 8, 0, 0, 0xff, 0xff, 0xff, 0xff}, "tag\x00\x00\x00\x00\x00"...)}
			l := v4Chunked(0, []uint64{1, 1}, 1, haddrUndef, idxSingle)
			return []ohMsg{spaceMsg(1), opaque, l}, result{nil, 0, 0}
		}},
		/* A deflated single chunk covering the dataset exactly, with
		 * partial edge chunks not filtered: the chunk is not a partial
		 * edge chunk (h5d_chunk.go:933) */
		{"filtered_whole_chunk", func(b *h5Builder) ([]ohMsg, result) {
			var data []byte
			for _, v := range []uint32{1, 2, 3, 4} {
				data = binary.LittleEndian.AppendUint32(data, v)
			}
			z := zlibEncode(t, data, 6)
			pline := ohMsg{typ: h5oPlineID, data: []byte{2, 1, 1, 0, 0, 0, 1, 0, 6, 0, 0, 0}}
			params := binary.LittleEndian.AppendUint32(le64a([]byte{idxSingle}, uint64(len(z))), 0)
			l := v4Chunked(h5oLayoutChunkDontFilterPartialBoundChunks|h5oLayoutChunkSingleIndexWithFilter,
				[]uint64{4, 4}, 1, b.put(z), params...)
			return []ohMsg{spaceMsg(4), i32Type(), pline, l}, result{i32any([]int32{1, 2, 3, 4}), 1, uint64(len(z))}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newH5Builder()
			msgs, want := c.build(b)
			obj := b.put(ohdr2(2, v2Msgs(msgs...)))
			img := b.finish(b.rootGroup(hardLinkMsg("obj", obj)))
			f := builtFile(t, img)
			ds, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			sh := testOpenDataset(t, f, "/obj")
			n := 0
			require.NoError(t, f.h5dChunkIter(sh, func([]uint64, uint32, haddr, uint64) error { n++; return nil }))
			assert.Equal(t, want.nchunks, n)
			assert.Equal(t, want.storage, sh.storageSize(f))
			if want.values == nil {
				return
			}
			got, err := ds.ReadValues()
			require.NoError(t, err)
			assert.Equal(t, want.values, got)
		})
	}
}

// TestMutationChunkAtAddressZero: an uint8 dataset whose single,
// unfiltered chunk is the whole file (address 0, as many bytes as the
// file): the chunk size equals the end of the file (h5d_chunk.go:970).
// libhdf5 reads the bytes of the file.
func TestMutationChunkAtAddressZero(t *testing.T) {
	const size = 1024
	b := newH5Builder()
	obj := b.put(ohdr2(2, v2Msgs(spaceMsg(size), u8Type(), v4Chunked(0, []uint64{size, 1}, 2, 0, 1))))
	root := b.rootGroup(hardLinkMsg("obj", obj))
	b.buf = append(b.buf, make([]byte, size-len(b.buf))...) /* pad the file to size bytes */
	img := b.finish(root)
	require.Len(t, img, size)
	f := builtFile(t, img)
	ds, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	got, err := Read[uint8](ds)
	require.NoError(t, err)
	assert.Equal(t, img, got)
}

// TestMutationEarrayLayoutLimits: the chunk lookup of /ea_paged of
// testdata/chunkidx.h5 (an extensible array of 1-element chunks of 1
// byte) through layouts with more dimensions that add nothing to the
// index (every other dimension holds a single chunk): 32 dimensions, the
// most a layout has (h5d_earray.go:82), and a chunk dimension of 2^32-1
// (h5d_earray.go:100). H5D__earray_idx_get_addr computes the same
// element index, so the chunks are those H5Dget_chunk_storage_size and
// H5Dread_chunk found (chunkidx_expected.txt).
func TestMutationEarrayLayoutLimits(t *testing.T) {
	f := openTestdata(t, "chunkidx.h5")
	chunks, _ := readChunkidxExpected(t)
	for _, c := range []struct {
		name     string
		chunkDim []uint64
		maxDims  []uint64
	}{
		{"33_layout_dims", ones(33, 1), append([]uint64{h5sUnlimitedDim}, ones(31, 1)...)},
		{"chunk_dim_4GiB", []uint64{1, 0xffffffff, 1}, []uint64{h5sUnlimitedDim, 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			layout := testChunkedLayout(h5dChunkIdxEarray, 479, c.chunkDim...)
			for _, ch := range chunks["ea_paged"][:30] {
				scaled := make([]uint64, len(c.maxDims))
				scaled[0] = ch.scaled[0]
				addr, _, _, err := f.dEarrayIdxGetAddr(layout, nil, c.maxDims, scaled)
				require.NoError(t, err)
				if ch.undef {
					assert.Equal(t, haddrUndef, addr, "chunk %v", ch.scaled)
					continue
				}
				raw, err := f.readAt(addr, 1)
				require.NoError(t, err)
				assert.Equal(t, ch.raw, raw, "chunk %v", ch.scaled)
			}
		})
	}
}

// eflSlot is an entry of an external file list: name, offset in the file
// and size of the segment.
type eflSlot struct {
	name   string
	offset int64
	size   uint64
}

// localHeap puts a local heap whose data segment is data (padded to a
// multiple of 8) and returns the heap's address.
func (b *h5Builder) localHeap(data []byte) uint64 {
	for len(data)%8 != 0 {
		data = append(data, 0)
	}
	dblk := b.put(data)
	hdr := append([]byte("HEAP"), 0, 0, 0, 0)
	hdr = le64a(hdr, uint64(len(data)))
	hdr = le64a(hdr, 1) /* H5HL_FREE_NULL: no free block */
	hdr = le64a(hdr, dblk)
	return b.put(hdr)
}

// eflMsg puts the local heap holding the names of slots and returns an
// external file list message with nalloc slots, of which slots are used.
func (b *h5Builder) eflMsg(nalloc int, slots []eflSlot) ohMsg {
	var names []byte
	var offs []uint64
	for _, s := range slots {
		offs = append(offs, uint64(len(names)))
		names = append(append(names, s.name...), 0)
	}
	if len(names) == 0 {
		names = []byte{0}
	}
	m := []byte{1, 0, 0, 0}
	m = binary.LittleEndian.AppendUint16(m, uint16(nalloc))
	m = binary.LittleEndian.AppendUint16(m, uint16(len(slots)))
	m = le64a(m, b.localHeap(names))
	for i, s := range slots {
		m = le64a(le64a(le64a(m, offs[i]), uint64(s.offset)), s.size)
	}
	return ohMsg{typ: h5oEflID, data: m}
}

// eflRawFiles writes the external files the EFL tests read, a.raw (int32
// 0..15) and b.raw (int32 100..115), to $HDF5_MUTATION_DUMP or a
// temporary directory, and returns the directory.
func eflRawFiles(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("HDF5_MUTATION_DUMP")
	if dir == "" {
		dir = t.TempDir()
	}
	for name, base := range map[string]uint32{"a.raw": 0, "b.raw": 100} {
		var raw []byte
		for i := range uint32(16) {
			raw = binary.LittleEndian.AppendUint32(raw, base+i)
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0o644))
	}
	return dir
}

// eflRead is a read of an EFL dataset: the whole dataset (start nil) or a
// one-dimensional hyperslab, and the values libhdf5 reads (nil: the read
// fails).
type eflRead struct {
	start, count uint64
	whole        bool
	want         []int32
}

// TestMutationExternalFiles: int32 datasets stored in external files
// (H5D__efl_init, H5O_efl_total_size, H5D__efl_read: h5d_efl.go). The
// expected values are those libhdf5 reads with the external files in the
// current directory; nil reads fail in libhdf5 too, and a dataset without
// reads cannot be opened by libhdf5.
func TestMutationExternalFiles(t *testing.T) {
	dir := eflRawFiles(t)
	t.Setenv("HDF5_EXTFILE_PREFIX", "")
	const unlimited = h5oEflUnlimited
	whole := func(want ...int32) eflRead { return eflRead{whole: true, want: want} }
	part := func(start, count uint64, want ...int32) eflRead {
		return eflRead{start: start, count: count, want: want}
	}
	failWhole := eflRead{whole: true}
	for _, c := range []struct {
		name   string
		dims   uint64
		nalloc int
		slots  []eflSlot
		reads  []eflRead
	}{
		/* An unlimited last segment makes the storage unlimited: the
		 * sizes are not added up (h5d_efl.go:30) */
		{"unlimited_last", 4, 2, []eflSlot{{"a.raw", 0, 8}, {"b.raw", 0, unlimited}},
			[]eflRead{whole(0, 1, 100, 101), part(3, 1, 101)}},
		/* 16 bytes of data, 12 bytes of storage (h5d_efl.go:30, 58, 67) */
		{"storage_too_small", 4, 1, []eflSlot{{"a.raw", 0, 12}}, nil},
		{"storage_exact", 4, 1, []eflSlot{{"a.raw", 4, 16}}, []eflRead{whole(1, 2, 3, 4)}},
		/* No slot in use: the external storage has size 0 (h5d_efl.go:30) */
		{"no_slots", 1, 1, nil, nil},
		/* Segments of size 0 add nothing (h5d_efl.go:36) and are skipped */
		{"empty_segment", 4, 3, []eflSlot{{"a.raw", 0, 8}, {"b.raw", 0, 0}, {"b.raw", 8, 8}},
			[]eflRead{whole(0, 1, 102, 103), part(2, 2, 102, 103)}},
		/* A read starting at the end of a segment starts with the next one:
		 * the first file is not opened (h5d_efl.go:99) */
		{"read_from_second_segment", 4, 2, []eflSlot{{"missing.raw", 0, 8}, {"b.raw", 0, 8}},
			[]eflRead{failWhole, part(2, 2, 100, 101), part(1, 2, nil...)}},
		/* A read starting inside a segment reads the rest of that segment
		 * only (h5d_efl.go:124) */
		{"read_across_segments", 4, 2, []eflSlot{{"a.raw", 0, 8}, {"b.raw", 4, 8}},
			[]eflRead{whole(0, 1, 101, 102), part(1, 2, 1, 101), part(1, 3, 1, 101, 102)}},
		/* A segment extending past the end of its file: the missing bytes
		 * read as zeros (h5d_efl.go:129) */
		{"short_file", 4, 1, []eflSlot{{"a.raw", 56, 16}}, []eflRead{whole(14, 15, 0, 0)}},
		/* H5F_OVERFLOW_HSIZET2OFFT(offset + skip): only the sum of the
		 * segment offset and the position in the segment must be a valid
		 * file offset; libhdf5 reads a.raw from offset 0 for elements 1..3
		 * (h5d_efl.go:116) */
		{"negative_offset", 4, 1, []eflSlot{{"a.raw", -4, 16}},
			[]eflRead{failWhole, part(1, 3, 0, 1, 2), part(0, 1, nil...)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newH5Builder()
			efl := b.eflMsg(c.nalloc, c.slots)
			layout := le64a(le64a([]byte{3, 1}, haddrUndef), c.dims*4)
			obj := b.put(ohdr2(2, v2Msgs(spaceMsg(c.dims), i32Type(), fillMsg(-7), layoutMsg(layout), efl)))
			img := b.finish(b.rootGroup(hardLinkMsg("obj", obj)))
			builtFile(t, img)
			f, err := OpenReader(bytes.NewReader(img), int64(len(img)), WithExternalFilePrefix(dir))
			require.NoError(t, err)
			defer f.Close()
			ds, err := f.OpenDataset("/obj")
			if c.reads == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			for _, r := range c.reads {
				var got []any
				if r.whole {
					got, err = ds.ReadValues()
				} else {
					got, err = ds.ReadHyperslab(h5.Hyperslab{Start: []uint64{r.start}, Count: []uint64{r.count}})
				}
				if r.want == nil {
					assert.Error(t, err, "%+v", r)
					continue
				}
				require.NoError(t, err, "%+v", r)
				want := make([]any, len(r.want))
				for i, v := range r.want {
					want[i] = v
				}
				assert.Equal(t, want, got, "%+v", r)
			}
		})
	}
}
