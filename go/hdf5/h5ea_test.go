package hdf5

// Tests of the extensible array (H5EA) and fixed array (H5FA) modules and
// of the dataset chunk index glue (H5Dearray / H5Dfarray).
//
// Expected values come from h5debug (HDF5 1.14.6):
//
//	h5debug <file> <object header address>                          (layout message)
//	h5debug <file> <EA/FA header address> <object header address>
//	h5debug <file> <EA index block> <EA header> <object header>
//	h5debug <file> <EA super block> <EA header> <super block index> <object header>
//	h5debug <file> <EA/FA data block> <EA/FA header> [<# of elements>] <object header>

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testOpenFile opens a file of the repository by its path relative to the
// repository root.
func testOpenFile(t *testing.T, rel string, opts ...Option) *File {
	t.Helper()
	f, err := Open(filepath.Join(repoRoot(t), rel), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// testEaOpen opens the extensible array chunk index at addr with the class
// named by the id stored in its header; for the filtered class the chunk
// size length is derived from the element size (address + chunk size +
// 4-byte filter mask).
func testEaOpen(t *testing.T, f *File, addr haddr) *h5ea {
	t.Helper()
	img, err := f.readAt(addr, 8)
	require.NoError(t, err)
	require.Equal(t, h5eaHdrMagic, string(img[:4]))
	ctx := &h5dEarrayCtx{fileAddrLen: int(f.sizeofAddr)}
	cls := h5dEarrayClsChunk()
	if img[5] == h5eaClsFiltChunk {
		cls = h5dEarrayClsFiltChunk()
		ctx.chunkSizeLen = int(img[6]) - int(f.sizeofAddr) - 4
	}
	ea, err := f.eaOpen(addr, cls, ctx)
	require.NoError(t, err)
	return ea
}

// testFaOpen is testEaOpen for fixed arrays.
func testFaOpen(t *testing.T, f *File, addr haddr) *h5fa {
	t.Helper()
	img, err := f.readAt(addr, 8)
	require.NoError(t, err)
	require.Equal(t, h5faHdrMagic, string(img[:4]))
	ctx := &h5dFarrayCtx{fileAddrLen: int(f.sizeofAddr)}
	cls := h5dFarrayClsChunk()
	if img[5] == h5faClsFiltChunk {
		cls = h5dFarrayClsFiltChunk()
		ctx.chunkSizeLen = int(img[6]) - int(f.sizeofAddr) - 4
	}
	fa, err := f.faOpen(addr, cls, ctx)
	require.NoError(t, err)
	return fa
}

func testChunkedLayout(idxType uint8, idxAddr haddr, chunkDim ...uint64) *h5oLayout {
	return &h5oLayout{
		typ: h5dChunked, version: 4,
		chunkNdims: uint32(len(chunkDim)), chunkDim: chunkDim,
		chunkIdxType: idxType, chunkIdxAddr: idxAddr,
	}
}

func TestH5VMHelpers(t *testing.T) {
	assert.Equal(t, uint(0), h5vmLog2Gen(0))
	assert.Equal(t, uint(0), h5vmLog2Gen(1))
	assert.Equal(t, uint(6), h5vmLog2Gen(100))
	assert.Equal(t, uint(63), h5vmLog2Gen(1<<63))
	for i := uint(0); i < 32; i++ {
		assert.Equal(t, i, h5vmLog2Of2(uint32(1)<<i))
	}
	b := []byte{0x80, 0x01}
	assert.True(t, h5vmBitGet(b, 0))
	assert.False(t, h5vmBitGet(b, 1))
	assert.True(t, h5vmBitGet(b, 15))
	assert.False(t, h5vmBitGet(b, 16)) // past the end

	c := []uint64{10, 11, 12, 13}
	h5vmSwizzleCoords(c, 2)
	assert.Equal(t, []uint64{12, 10, 11, 13}, c)
	h5vmUnswizzleCoords(c, 2)
	assert.Equal(t, []uint64{10, 11, 12, 13}, c)

	down := make([]uint64, 3)
	h5vmArrayDown(3, []uint64{4, 5, 6}, down)
	assert.Equal(t, []uint64{30, 6, 1}, down)
	assert.Equal(t, uint64(1*30+2*6+3), h5vmArrayOffsetPre(3, down, []uint64{1, 2, 3}))
	assert.Equal(t, uint64(1*30+2*6+3), h5vmChunkIndex(3, []uint64{2, 5, 9}, []uint32{2, 2, 3}, down))
}

// TestEarrayHdrInit checks the super block geometry against the table in
// the H5EA__hdr_init comment.
func TestEarrayHdrInit(t *testing.T) {
	f := &File{sizeofAddr: 8, sizeofSize: 8}
	hdr := h5eaHdrAlloc(f)
	hdr.eltSize, hdr.maxNelmtsBits, hdr.idxBlkElmts = 8, 32, 4
	hdr.dataBlkMinElmts, hdr.supBlkMinDataPtrs, hdr.maxDblkPageNelmtsBits = 16, 4, 10
	require.NoError(t, h5eaHdrInit(hdr, h5dEarrayClsChunk(), nil))
	assert.Equal(t, uint64(29), hdr.nsblks)
	assert.Equal(t, uint64(1024), hdr.dblkPageNelmts)
	assert.Equal(t, uint8(4), hdr.arrOffSize)
	assert.Equal(t, uint64(72), hdr.size)
	want := []h5eaSblkInfo{
		{1, 16, 0, 0}, {1, 32, 16, 1}, {2, 32, 48, 2}, {2, 64, 112, 4},
		{4, 64, 240, 6}, {4, 128, 496, 10}, {8, 128, 1008, 14}, {8, 256, 2032, 22},
	}
	assert.Equal(t, want, hdr.sblkInfo[:len(want)])
	assert.Equal(t, uint64(16*(1<<13-1)), hdr.sblkInfo[13].startIdx)
	assert.Equal(t, uint64(2048), hdr.sblkInfo[13].dblkNelmts)

	// Element index -> super block index.
	for _, c := range []struct{ idx, sblk uint64 }{
		{4, 0}, {19, 0}, {20, 1}, {51, 1}, {52, 2}, {115, 2}, {116, 3}, {243, 3}, {244, 4}, {131060, 13},
	} {
		assert.Equal(t, c.sblk, h5eaDblockSblkIdx(hdr, c.idx), "idx %d", c.idx)
	}

	// Corrupt creation parameters are rejected.
	for _, mod := range []func(h *h5eaHdr){
		func(h *h5eaHdr) { h.maxNelmtsBits = 0 },
		func(h *h5eaHdr) { h.maxNelmtsBits = 65 },
		func(h *h5eaHdr) { h.dataBlkMinElmts = 0 },
		func(h *h5eaHdr) { h.dataBlkMinElmts = 12 },
		func(h *h5eaHdr) { h.supBlkMinDataPtrs = 0 },
		func(h *h5eaHdr) { h.supBlkMinDataPtrs = 1 },
		func(h *h5eaHdr) { h.eltSize = 0 },
		func(h *h5eaHdr) { h.maxNelmtsBits = 3 },
		func(h *h5eaHdr) { h.maxDblkPageNelmtsBits = 40 },
	} {
		h := h5eaHdrAlloc(f)
		h.eltSize, h.maxNelmtsBits, h.idxBlkElmts = 8, 32, 4
		h.dataBlkMinElmts, h.supBlkMinDataPtrs, h.maxDblkPageNelmtsBits = 16, 4, 10
		mod(h)
		assert.Error(t, h5eaHdrInit(h, h5dEarrayClsChunk(), nil))
	}
}

// TestEarrayErrLevel reads the extensible array of /DSET_ERR in
// h5fc_err_level.h5 (dims {1, 500}/{1, UNLIM}, chunk {1, 1}): 500 elements in
// the index block, 6 data blocks addressed by the index block and the 4
// data blocks of super block 4.
func TestEarrayErrLevel(t *testing.T) {
	f := testOpenFile(t, "tools/test/h5format_convert/testfiles/h5fc_err_level.h5")
	ea := testEaOpen(t, f, 528)

	hdr := ea.hdr
	assert.Equal(t, uint8(h5eaClsChunk), hdr.clsID)
	assert.Equal(t, uint8(8), hdr.eltSize)
	assert.Equal(t, uint8(32), hdr.maxNelmtsBits)
	assert.Equal(t, uint8(4), hdr.idxBlkElmts)
	assert.Equal(t, uint8(16), hdr.dataBlkMinElmts)
	assert.Equal(t, uint8(4), hdr.supBlkMinDataPtrs)
	assert.Equal(t, uint8(10), hdr.maxDblkPageNelmtsBits)
	assert.Equal(t, uint64(500), ea.getNelmts())
	assert.Equal(t, haddr(528), ea.getAddr())
	stats := ea.getStats()
	assert.Equal(t, uint64(1), stats.stored.nsuperBlks)
	assert.Equal(t, uint64(10), stats.stored.ndataBlks)
	assert.Equal(t, uint64(500), stats.stored.nelmts)
	assert.Equal(t, uint64(72), stats.computed.hdrSize)
	assert.Equal(t, uint64(298), stats.computed.indexBlkSize)
	assert.Equal(t, haddr(600), hdr.idxBlkAddr)

	iblock, err := h5eaIblockProtect(hdr)
	require.NoError(t, err)
	assert.Equal(t, uint64(298), iblock.size)
	assert.Equal(t, []haddr{898, 1048, 1326, 1604, 1882, 4624}, iblock.dblkAddrs)
	require.Len(t, iblock.sblkAddrs, 25)
	assert.Equal(t, haddr(2416), iblock.sblkAddrs[0])
	assert.Equal(t, haddrUndef, iblock.sblkAddrs[1])

	sblock, err := h5eaSblockProtect(hdr, 2416, 4)
	require.NoError(t, err)
	assert.Equal(t, uint64(54), sblock.size)
	assert.Equal(t, uint64(64), sblock.dblkNelmts)
	assert.Equal(t, []haddr{5158, 5692, 6226, 6760}, sblock.dblkAddrs)

	dblock, err := h5eaDblockProtect(hdr, 4624, 64)
	require.NoError(t, err)
	assert.Equal(t, uint64(534), dblock.size)
	assert.Equal(t, haddr(2756), dblock.elmts[0])

	// The chunks were allocated in order: chunk i is at 2576 + i.
	for i := uint64(0); i < 500; i++ {
		v, err := ea.get(i)
		require.NoError(t, err)
		require.Equal(t, haddr(2576+i), v, "element %d", i)
	}
	v, err := ea.get(500)
	require.NoError(t, err)
	assert.Equal(t, haddrUndef, v)
	v, err = ea.get(1 << 40)
	require.NoError(t, err)
	assert.Equal(t, haddrUndef, v)

	n := 0
	require.NoError(t, ea.iterate(func(idx uint64, elmt any) error {
		assert.Equal(t, haddr(2576+idx), elmt)
		n++
		return nil
	}))
	assert.Equal(t, 500, n)

	// The chunk index: the unlimited dimension is dimension 1, so the chunk
	// coordinates are swizzled.
	layout := testChunkedLayout(h5dChunkIdxEarray, 528, 1, 1, 1)
	maxDims := []uint64{1, h5sUnlimitedDim}
	for j := uint64(0); j < 500; j += 7 {
		addr, nbytes, mask, err := f.dEarrayIdxGetAddr(layout, nil, maxDims, []uint64{0, j})
		require.NoError(t, err)
		assert.Equal(t, haddr(2576+j), addr)
		assert.Equal(t, uint64(1), nbytes)
		assert.Equal(t, uint32(0), mask)
	}
	addr, nbytes, _, err := f.dEarrayIdxGetAddr(layout, nil, maxDims, []uint64{0, 600})
	require.NoError(t, err)
	assert.Equal(t, haddrUndef, addr)
	assert.Equal(t, uint64(0), nbytes)

	var got [][]uint64
	require.NoError(t, f.dEarrayIdxIterate(layout, nil, maxDims, func(scaled []uint64, addr haddr, nbytes uint64, mask uint32) error {
		assert.Equal(t, haddr(2576+scaled[1]), addr)
		got = append(got, scaled)
		return nil
	}))
	require.Len(t, got, 500)
	assert.Equal(t, []uint64{0, 0}, got[0])
	assert.Equal(t, []uint64{0, 499}, got[499])

	// Early stop.
	n = 0
	require.NoError(t, f.dEarrayIdxIterate(layout, nil, maxDims, func([]uint64, haddr, uint64, uint32) error {
		n++
		if n == 3 {
			return errBIterStop
		}
		return nil
	}))
	assert.Equal(t, 3, n)

	// No unlimited dimension: not an extensible array index.
	_, _, _, err = f.dEarrayIdxGetAddr(layout, nil, []uint64{1, 500}, []uint64{0, 0})
	assert.Error(t, err)
	// Wrong class.
	_, err = f.eaOpen(528, h5dEarrayClsFiltChunk(), &h5dEarrayCtx{fileAddrLen: 8, chunkSizeLen: 2})
	assert.Error(t, err)
}

// TestFarrayTdsetIdx reads the fixed arrays of /dset_fixed (unfiltered) and
// /dset_filter (deflate) in tdset_idx.h5 (dims {20, 10}, chunk {5, 5}).
func TestFarrayTdsetIdx(t *testing.T) {
	f := testOpenFile(t, "tools/test/h5ls/testfiles/tdset_idx.h5")

	fa := testFaOpen(t, f, 479)
	assert.Equal(t, uint64(8), fa.getNelmts())
	assert.Equal(t, haddr(507), fa.hdr.dblkAddr)
	assert.Equal(t, uint64(28), fa.getStats().hdrSize)
	assert.Equal(t, uint64(82), fa.getStats().dblkSize)
	for i := uint64(0); i < 8; i++ {
		v, err := fa.get(i)
		require.NoError(t, err)
		assert.Equal(t, haddr(2096+100*i), v)
	}
	_, err := fa.get(8)
	assert.Error(t, err)

	layout := testChunkedLayout(h5dChunkIdxFarray, 479, 5, 5, 4)
	for i := uint64(0); i < 4; i++ {
		for j := uint64(0); j < 2; j++ {
			addr, nbytes, mask, err := f.dFarrayIdxGetAddr(layout, nil, []uint64{20, 10}, []uint64{i, j})
			require.NoError(t, err)
			assert.Equal(t, haddr(2096+100*(i*2+j)), addr)
			assert.Equal(t, uint64(100), nbytes)
			assert.Equal(t, uint32(0), mask)
		}
	}

	filt := testFaOpen(t, f, 1185)
	assert.Equal(t, uint8(14), filt.hdr.eltSize)
	pline := &h5oPline{version: 2, nused: 1, filter: []h5zFilterInfo{{id: h5zFilterDeflate, cdValues: []uint32{9}}}}
	layout = testChunkedLayout(h5dChunkIdxFarray, 1185, 5, 5, 4)
	var scaled [][]uint64
	require.NoError(t, f.dFarrayIdxIterate(layout, pline, []uint64{20, 10}, func(s []uint64, addr haddr, nbytes uint64, mask uint32) error {
		k := uint64(len(scaled))
		assert.Equal(t, haddr(3696+25*k), addr)
		assert.Equal(t, uint64(25), nbytes)
		assert.Equal(t, uint32(0), mask)
		scaled = append(scaled, s)
		return nil
	}))
	assert.Equal(t, [][]uint64{{0, 0}, {0, 1}, {1, 0}, {1, 1}, {2, 0}, {2, 1}, {3, 0}, {3, 1}}, scaled)
	addr, nbytes, _, err := f.dFarrayIdxGetAddr(layout, pline, []uint64{20, 10}, []uint64{3, 1})
	require.NoError(t, err)
	assert.Equal(t, haddr(3871), addr)
	assert.Equal(t, uint64(25), nbytes)
}

// TestFarrayPaged reads the paged fixed array of /DSET_TWO in test_ld.h5
// (dims {4, 10}/{60, 100}, chunk {2, 2}: 1500 elements in 2 pages, the
// second one never initialized).
func TestFarrayPaged(t *testing.T) {
	for _, verify := range []bool{true, false} {
		f := testOpenFile(t, "hl/test/testfiles/test_ld.h5", WithChecksumVerification(verify))
		fa := testFaOpen(t, f, 2013)
		assert.Equal(t, uint64(1500), fa.getNelmts())
		assert.Equal(t, uint64(12027), fa.getStats().dblkSize)

		dblock, err := h5faDblockProtect(fa.hdr, 5130)
		require.NoError(t, err)
		assert.Equal(t, uint64(2), dblock.npages)
		assert.Equal(t, uint64(1024), dblock.dblkPageNelmts)
		assert.Equal(t, uint64(476), dblock.lastPageNelmts)
		assert.Equal(t, uint64(12027), dblock.size)

		want := map[uint64]haddr{}
		for i := uint64(0); i < 5; i++ {
			want[i] = 2656 + 16*i
			want[50+i] = 2736 + 16*i
		}
		require.NoError(t, fa.iterate(func(idx uint64, elmt any) error {
			if a, ok := want[idx]; ok {
				assert.Equal(t, a, elmt, "element %d", idx)
			} else {
				assert.Equal(t, haddrUndef, elmt, "element %d", idx)
			}
			return nil
		}))

		layout := testChunkedLayout(h5dChunkIdxFarray, 2013, 2, 2, 4)
		var got []haddr
		require.NoError(t, f.dFarrayIdxIterate(layout, nil, []uint64{60, 100}, func(s []uint64, addr haddr, nbytes uint64, mask uint32) error {
			a2, n2, _, err := f.dFarrayIdxGetAddr(layout, nil, []uint64{60, 100}, s)
			require.NoError(t, err)
			assert.Equal(t, addr, a2)
			assert.Equal(t, uint64(16), n2)
			assert.Less(t, s[0], uint64(2))
			assert.Less(t, s[1], uint64(5))
			got = append(got, addr)
			return nil
		}))
		assert.Len(t, got, 10)
	}
}

// TestEarrayCorrupt checks that damaged extensible / fixed array metadata
// produces errors (and no panics).
func TestEarrayCorrupt(t *testing.T) {
	src := filepath.Join(repoRoot(t), "tools/test/h5format_convert/testfiles/h5fc_err_level.h5")
	data := readFile(t, src)

	// Flip a byte in each of: the header, the index block, the super block,
	// a data block. With checksums verified each must fail; without, the
	// structural checks catch the damaged signatures.
	for _, off := range []int{528, 528 + 6, 600, 600 + 4, 2416, 2416 + 5, 4624, 4624 + 20} {
		buf := append([]byte(nil), data...)
		buf[off] ^= 0xff
		f, err := OpenReader(bytes.NewReader(buf), int64(len(buf)))
		require.NoError(t, err)
		ea, err := f.eaOpen(528, h5dEarrayClsChunk(), &h5dEarrayCtx{fileAddrLen: 8})
		if err == nil {
			err = ea.iterate(func(uint64, any) error { return nil })
		}
		assert.Error(t, err, "offset %d", off)
		f.Close()
	}

	// Every truncation of the array metadata fails cleanly.
	for _, n := range []int{530, 560, 620, 800, 2420, 2460, 4700, 5000} {
		f, err := OpenReader(bytes.NewReader(data[:n]), int64(n))
		if err != nil {
			continue
		}
		ea, err := f.eaOpen(528, h5dEarrayClsChunk(), &h5dEarrayCtx{fileAddrLen: 8})
		if err == nil {
			err = ea.iterate(func(uint64, any) error { return nil })
		}
		assert.Error(t, err, "truncated at %d", n)
		f.Close()
	}
}
