package hdf5

// Tests for the dataset code (h5d_none.go, h5d_int.go, h5d_io.go,
// h5d_fill.go, h5d_single.go, h5d_farray.go) written from the mutants
// gremlins left alive. The files are assembled byte by byte (see
// mutation_h5o_test.go) and the expectations are what libhdf5 1.14.6
// reads from the same bytes (set HDF5_MUTATION_DUMP to a directory to have
// the tests write the files there for checking them with libhdf5).

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chunkRec is what H5Dchunk_iter reports for a chunk.
type chunkRec struct {
	offset []uint64
	addr   uint64
	size   uint64
}

// chunkRecs lists the chunks of the dataset /obj as H5Dchunk_iter does.
func chunkRecs(t *testing.T, f *File) []chunkRec {
	t.Helper()
	d, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	ds, err := d.dataset()
	require.NoError(t, err)
	var recs []chunkRec
	require.NoError(t, f.h5dChunkIter(ds, func(offset []uint64, _ uint32, addr haddr, n uint64) error {
		recs = append(recs, chunkRec{offset, addr, n})
		return nil
	}))
	return recs
}

// implicitLayout is a version 4 layout message for chunks of the given
// dimensions (with the element size as the last one) indexed by the
// implicit index at addr.
func implicitLayout(addr uint64, chunk ...uint64) []byte {
	l := []byte{4, 2, 0, byte(len(chunk)), 8}
	for _, d := range chunk {
		l = le64a(l, d)
	}
	return le64a(append(l, 2), addr)
}

// chunkData is n chunks of size int32 elements each, the elements of
// chunk k holding 100*k, 100*k+1, ...
func chunkData(n, size int) []byte {
	var b []byte
	for k := range n {
		for e := range size {
			b = binary.LittleEndian.AppendUint32(b, uint32(100*k+e))
		}
	}
	return b
}

// TestMutationImplicitIndex: the implicit chunk index (h5d_none.go): an
// int32 dataset {3, 5} with maximum dimensions {4, 7} in 2x2 chunks, so
// that the chunks are stored in the order of the 2x4 chunks of the
// maximum dimensions (eight chunks, the last row and column of chunks of
// the dataset partial). libhdf5 reads the values from the chunks at
// their addresses and lists the six chunks of the dataset's extent
// (H5Dchunk_iter, H5Dget_storage_size: 96 bytes).
func TestMutationImplicitIndex(t *testing.T) {
	var data uint64
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		data = b.put(chunkData(8, 4))
		return b.put(ohdr2(2, v2Msgs(spaceMaxMsg([]uint64{3, 5}, []uint64{4, 7}), i32Type(), fillMsg(-7), layoutMsg(implicitLayout(data, 2, 2, 4)))))
	})
	d, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	got, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{
		0, 1, 100, 101, 200,
		2, 3, 102, 103, 202,
		400, 401, 500, 501, 600,
	}, got)
	recs := chunkRecs(t, f)
	assert.Equal(t, []chunkRec{
		{[]uint64{0, 0}, data, 16}, {[]uint64{0, 2}, data + 16, 16}, {[]uint64{0, 4}, data + 32, 16},
		{[]uint64{2, 0}, data + 64, 16}, {[]uint64{2, 2}, data + 80, 16}, {[]uint64{2, 4}, data + 96, 16},
	}, recs)
	ds, err := d.dataset()
	require.NoError(t, err)
	assert.EqualValues(t, 96, ds.storageSize(f))
}

// TestMutationImplicitChunkBound: the bound hdf5 puts on the iteration
// over the chunks of an implicit index (h5d_none.go:77): the chunks of
// int32 datasets {n} with one-element chunks whose data would start at
// the end of the file. For n = eof/4 + 1 the chunks are listed as libhdf5
// lists them (H5Dchunk_iter: addresses past the end of the file); for
// n = eof/4 + 2, where libhdf5 lists them as well, hdf5 refuses to
// iterate (a corrupt extent must not make it loop over up to 2^64
// chunks).
func TestMutationImplicitChunkBound(t *testing.T) {
	image := func(n uint64) []byte {
		b := newH5Builder()
		obj := b.put(ohdr2(2, v2Msgs(spaceMsg(n), i32Type(), fillMsg(-7), layoutMsg(implicitLayout(uint64(len(b.buf))+256, 1, 4)))))
		return b.finish(b.rootGroup(hardLinkMsg("obj", obj)))
	}
	eof := uint64(len(image(1)))
	for _, c := range []struct {
		name string
		n    uint64
	}{{"max", eof/4 + 1}, {"over", eof/4 + 2}} {
		t.Run(c.name, func(t *testing.T) {
			f := builtFile(t, image(c.n))
			d, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			ds, err := d.dataset()
			require.NoError(t, err)
			var count uint64
			err = f.h5dChunkIter(ds, func(_ []uint64, _ uint32, addr haddr, _ uint64) error {
				assert.Equal(t, ds.chunkSt.idxAddr+4*count, addr)
				count++
				return nil
			})
			if c.name == "over" {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.n, count)
			assert.Equal(t, 4*c.n, ds.storageSize(f))
		})
	}
}

// withUserblock moves the image written by an h5Builder behind a user
// block of n bytes (the superblock's base address set to n).
func withUserblock(img []byte, n int) []byte {
	out := append(make([]byte, n), img...)
	sb := out[n : n+h5BuilderSuperblockSize]
	binary.LittleEndian.PutUint64(sb[12:], uint64(n))
	binary.LittleEndian.PutUint64(sb[28:], uint64(len(out))) /* end of file address (absolute) */
	binary.LittleEndian.PutUint32(sb[44:], checksumLookup3(sb[:44], 0))
	return out
}

// TestMutationContiguousOffset: the offset of contiguous storage that is
// not allocated (H5D__get_offset, h5d_int.go:304), in an int32 dataset {4}
// with fill value -7, in a file without and with a 512-byte user block.
// libhdf5 adds the base address to HADDR_UNDEF: H5Dget_offset returns
// HADDR_UNDEF without a user block and 511 with one (and
// H5Dget_storage_size 0), which is what the cgo package reports as the
// layout.
func TestMutationContiguousOffset(t *testing.T) {
	for _, ub := range []int{0, 512} {
		t.Run(fmt.Sprintf("userblock%d", ub), func(t *testing.T) {
			b := newH5Builder()
			l := le64a(le64a([]byte{3, 1}, haddrUndef), 16)
			obj := b.put(ohdr2(2, v2Msgs(spaceMsg(4), i32Type(), fillMsg(-7), layoutMsg(l))))
			img := b.finish(b.rootGroup(hardLinkMsg("obj", obj)))
			if ub > 0 {
				img = withUserblock(img, ub)
			}
			f := builtFile(t, img)
			d, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			layout, err := d.Layout()
			require.NoError(t, err)
			if ub == 0 {
				assert.Equal(t, uint64(haddrUndef), layout.Address)
				assert.False(t, layout.AddressDefined)
				assert.EqualValues(t, 16, layout.Size)
			} else {
				assert.EqualValues(t, ub-1, layout.Address)
				assert.True(t, layout.AddressDefined)
				assert.EqualValues(t, 0, layout.Size)
			}
			got, err := Read[int32](d)
			require.NoError(t, err)
			assert.Equal(t, []int32{-7, -7, -7, -7}, got)
		})
	}
}

// patchOhdrChunk0 replaces the first occurrence of old by new in the first
// chunk of the version 2 object header at addr of the image b and
// recomputes the chunk's checksum.
func patchOhdrChunk0(t *testing.T, b []byte, addr uint64, old, new []byte) {
	t.Helper()
	require.Equal(t, "OHDR", string(b[addr:addr+4]))
	flags := b[addr+5]
	p := addr + 6
	if flags&0x20 != 0 { /* times */
		p += 16
	}
	if flags&0x10 != 0 { /* attribute phase change values */
		p += 4
	}
	w := uint64(1) << (flags & 3)
	size := le64(append(append([]byte(nil), b[p:p+w]...), make([]byte, 8)...))
	start := p + w
	i := bytes.Index(b[start:start+size], old)
	require.GreaterOrEqual(t, i, 0, "pattern not found")
	copy(b[start+uint64(i):], new)
	setChecksum(b, addr, start+size+4)
}

// TestMutationExternalSlots: an external file list that allocates more
// slots than it uses (h5d_int.go:374): /external of testdata/dset.h5 (two
// segments) with three allocated slots. libhdf5 lists the two segments
// (H5Pget_external_count, H5Pget_external).
func TestMutationExternalSlots(t *testing.T) {
	f := patchedFile(t, "testdata/dset.h5", func(orig *File, b []byte) {
		d, err := orig.OpenDataset("/external")
		require.NoError(t, err)
		/* (version 1, reserved, 2 allocated slots, 2 used) */
		patchOhdrChunk0(t, b, d.addr, []byte{1, 0, 0, 0, 2, 0, 2, 0}, []byte{1, 0, 0, 0, 3, 0, 2, 0})
	})
	d, err := f.OpenDataset("/external")
	require.NoError(t, err)
	layout, err := d.Layout()
	require.NoError(t, err)
	assert.Equal(t, []h5.ExternalFile{{Name: "dset_ext.raw", Offset: 16, Size: 12}, {Name: "dset_ext.raw", Offset: 100, Size: 28}}, layout.External)
}

// TestMutationDatasetDims: dataspace dimensions at the limits
// H5D__cache_dataspace_info puts on them (h5d_int.go:75, 86): a chunked
// int32 dataset without chunks of rank 32 (the maximum) opens, one
// whose first dimension is 2^63 - 1 opens and one whose first dimension is
// 2^63 does not (H5VM_power2up fails: "unable to get the next power of
// 2").
func TestMutationDatasetDims(t *testing.T) {
	for _, c := range []struct {
		name string
		dims []uint64
		ok   bool
	}{
		{"rank32", ones(32, 1), true},
		{"dim_2^63-1", []uint64{1<<63 - 1, 1}, true},
		{"dim_2^63", []uint64{1 << 63, 1}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := buildObject(t, func(b *h5Builder) uint64 {
				/* (version 3 chunked layout, no chunks: chunks of one element) */
				l := le64a([]byte{3, 2, byte(len(c.dims) + 1)}, haddrUndef)
				for range c.dims {
					l = append(l, 1, 0, 0, 0)
				}
				l = append(l, 4, 0, 0, 0)
				return b.put(ohdr2(2, v2Msgs(spaceMsg(c.dims...), i32Type(), fillMsg(-7), layoutMsg(l))))
			})
			d, err := f.OpenDataset("/obj")
			if !c.ok {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.dims, d.Dataspace().Dims)
		})
	}
}

// TestMutationReadTooLarge: reading datasets too large for a Go slice
// (h5d_io.go:89): int32 datasets of 2^50 and 2^61 - 1 elements stored in
// chunks of one element, none written. libhdf5 opens them (and the cgo
// package fails to allocate the buffer); hdf5 reports an error instead of
// panicking in make.
func TestMutationReadTooLarge(t *testing.T) {
	for _, n := range []uint64{1 << 50, 1<<61 - 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f, _ := buildObject(t, func(b *h5Builder) uint64 {
				l := append(le64a([]byte{3, 2, 2}, haddrUndef), 1, 0, 0, 0, 4, 0, 0, 0)
				return b.put(ohdr2(2, v2Msgs(spaceMsg(n), i32Type(), fillMsg(-7), layoutMsg(l))))
			})
			d, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			assert.Equal(t, []uint64{n}, d.Dataspace().Dims)
			_, err = Read[int32](d)
			assert.Error(t, err)
		})
	}
}

// TestMutationUndefinedFill: reading an int32 dataset {4} whose contiguous
// storage is not allocated and whose fill value is undefined (H5D__read,
// h5d_io.go:174), with each fill time. libhdf5 fails ("dataset doesn't
// exist, no data can be read") when the fill value would be written at
// allocation or if set, and leaves the buffer as it was when it is never
// written (hdf5 returns zeros).
func TestMutationUndefinedFill(t *testing.T) {
	for _, c := range []struct {
		name     string
		fillTime byte
		ok       bool
	}{
		{"alloc", h5dFillTimeAlloc, false},
		{"never", h5dFillTimeNever, true},
		{"ifset", h5dFillTimeIfset, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := buildObject(t, func(b *h5Builder) uint64 {
				/* (version 3 fill value message: allocation late, the fill
				 *  time, fill value undefined) */
				fill := ohMsg{typ: h5oFillNewID, data: []byte{3, h5dAllocTimeLate | c.fillTime<<h5oFillShiftFillTime | h5oFillFlagUndefinedValue}}
				l := le64a(le64a([]byte{3, 1}, haddrUndef), 16)
				return b.put(ohdr2(2, v2Msgs(spaceMsg(4), i32Type(), fill, layoutMsg(l))))
			})
			d, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			got, err := Read[int32](d)
			if !c.ok {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []int32{0, 0, 0, 0}, got)
		})
	}
}

// u8Type is the datatype message body of H5T_STD_U8LE.
func u8Type() []byte { return []byte{0x10, 0, 0, 0, 1, 0, 0, 0, 0, 0, 8, 0} }

// vlenStrType is the datatype message body of a variable-length ASCII
// string of the given (stored) size.
func vlenStrType(size byte) []byte {
	return append([]byte{0x19, 1, 0, 0, size, 0, 0, 0}, u8Type()...)
}

// TestMutationVlenFill: chunked datasets {4} with missing chunks whose user
// fill value has a variable-length string component, all read as the fill
// value (H5D__fill_refill_vl, h5d_fill.go:134, 154): a compound {s:
// vlen string at 0, x: int32 at 16} whose fill value has a null string
// and x = 5 (libhdf5 reads empty strings and 5), and a variable-length
// string whose datatype stores the size 8 instead of 16 (libhdf5 cannot
// read it).
func TestMutationVlenFill(t *testing.T) {
	/* (version 3 chunked layout: chunks of two elements indexed by a
	 *  version 1 B-tree without entries, so that every chunk is missing) */
	chunked := func(b *h5Builder, size uint32) []byte {
		key := le64a(le64a(binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, 2*size), 0), 0), 0)
		root := b.put(treeNode(1, 0, haddrUndef, haddrUndef, [][]byte{key}, nil, 24+64*8+65*24))
		return binary.LittleEndian.AppendUint32(append(le64a([]byte{3, 2, 2}, root), 2, 0, 0, 0), size)
	}
	fillMsg := func(v []byte) ohMsg {
		return ohMsg{typ: h5oFillNewID, data: append(binary.LittleEndian.AppendUint32([]byte{3, 0x2a}, uint32(len(v))), v...)}
	}
	t.Run("compound", func(t *testing.T) {
		f, _ := buildObject(t, func(b *h5Builder) uint64 {
			typ := []byte{0x36, 2, 0, 0, 20, 0, 0, 0}
			typ = append(append(typ, "s\x00\x00"...), vlenStrType(16)...)
			typ = append(append(typ, "x\x00\x10"...), i32Type().data...)
			fill := binary.LittleEndian.AppendUint32(make([]byte, 16), 5)
			l := chunked(b, 20)
			return b.put(ohdr2(2, v2Msgs(spaceMsg(4), ohMsg{typ: h5oDtypeID, data: typ}, fillMsg(fill), layoutMsg(l))))
		})
		d, err := f.OpenDataset("/obj")
		require.NoError(t, err)
		got, err := d.ReadValues()
		require.NoError(t, err)
		require.Len(t, got, 4)
		for _, v := range got {
			assert.Equal(t, h5.Compound{Fields: []h5.Field{{Name: "s", Value: ""}, {Name: "x", Value: int32(5)}}}, v)
		}
	})
	t.Run("vlen_size8", func(t *testing.T) {
		f, _ := buildObject(t, func(b *h5Builder) uint64 {
			fill := []byte{1, 0, 0, 0, 1, 0, 0, 0}
			l := chunked(b, 8)
			return b.put(ohdr2(2, v2Msgs(spaceMsg(4), ohMsg{typ: h5oDtypeID, data: vlenStrType(8)}, fillMsg(fill), layoutMsg(l))))
		})
		d, err := f.OpenDataset("/obj")
		if err == nil {
			_, err = d.ReadValues()
		}
		assert.Error(t, err)
	})
}

// TestMutationSingleChunkIndex: a single chunk index whose chunk (two
// elements: 0, 1) is smaller than the int32 dataset {4} (H5D__single_idx_
// get_addr, h5d_single.go:47). libhdf5 only asserts that the dataset has
// one chunk: it reads every chunk of the dataspace from the one chunk, 0 1
// 0 1, and lists that chunk (H5Dchunk_iter).
func TestMutationSingleChunkIndex(t *testing.T) {
	var data uint64
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		data = b.put(chunkData(1, 2))
		/* (version 4 chunked layout, chunk {2, 4}, single chunk index) */
		l := le64a(append([]byte{4, 2, 0, 2, 8}, append(append(le64a(nil, 2), le64a(nil, 4)...), 1)...), data)
		return b.put(ohdr2(2, v2Msgs(spaceMsg(4), i32Type(), fillMsg(-7), layoutMsg(l))))
	})
	d, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	got, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{0, 1, 0, 1}, got)
	assert.Equal(t, []chunkRec{{[]uint64{0}, data, 8}}, chunkRecs(t, f))
}
