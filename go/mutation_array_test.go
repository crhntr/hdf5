package hdf5

// Tests for the extensible and fixed array code (h5ea*.go, h5fa*.go)
// written from the mutants gremlins left alive. Most build files with
// h5Builder (mutation_h5o_test.go): one-dimensional uint8 datasets in
// chunks of one element whose chunk index is an extensible or fixed array
// assembled block by block below, with creation parameters libhdf5 never
// picks for a dataset (small data blocks and pages, all super blocks in
// the index block, 64-bit array sizes, ...). The expectations are what
// libhdf5 1.14.6 reads from the same bytes (H5Dread, H5Dchunk_iter,
// H5Dget_storage_size and, for the fixed array statistics, the index
// size of H5Oget_native_info); set HDF5_MUTATION_DUMP to a directory to
// have the tests write the files there.

import (
	"encoding/binary"
	"math/bits"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// arrFill is the fill value of the array test datasets.
const arrFill = 0xee

// arrValue is the byte of chunk i of the array test datasets (never the
// fill value).
func arrValue(i uint64) byte { return byte(i%200 + 1) }

// u8FillMsg is a version 3 fill value message for a uint8 dataset: fill
// value v, written if set, allocated late.
func u8FillMsg(v byte) ohMsg {
	return ohMsg{typ: h5oFillNewID, data: []byte{3, 0x2a, 1, 0, 0, 0, v}}
}

// arrBlock appends the checksum of a metadata block.
func arrBlock(b []byte) []byte {
	return binary.LittleEndian.AppendUint32(b, checksumLookup3(b, 0))
}

// eaGeom is the creation parameters of an extensible array.
type eaGeom struct {
	bits, idxBlkElmts, dmin, minPtrs, pageBits uint8
}

// eaFixture describes an extensible array chunk index (unfiltered: the
// elements are chunk addresses) of a dataset of n chunks: the elements
// of set are defined, every block (index block, data block, super block,
// data block page) holding one of them is allocated.
type eaFixture struct {
	g         eaGeom
	n         uint64
	maxIdxSet uint64
	set       []uint64
	patchHdr  func(hdr []byte) /* changes the header before its checksum is computed */
	t         *testing.T
	hdrAddr   uint64 /* (set by put) */
}

// eaLoc is where the element of an extensible array is stored.
type eaLoc struct {
	sblk, dblk, page int /* super block, data block in it, page in it (-1: not paged) */
	off              uint64
}

// put assembles the array (the chunk of element i, one byte, at data+i)
// and returns the header address.
func (x *eaFixture) put(b *h5Builder, data uint64) uint64 {
	g := x.g
	l2 := func(v uint64) int { return bits.Len64(v) - 1 }
	nsblks := 1 + int(g.bits) - l2(uint64(g.dmin))
	ndblks := func(s int) uint64 { return 1 << (s / 2) }
	dn := func(s int) uint64 { return uint64(g.dmin) << ((s + 1) / 2) }
	start, startDblk := make([]uint64, nsblks), make([]uint64, nsblks)
	for s := 1; s < nsblks; s++ {
		start[s] = start[s-1] + ndblks(s-1)*dn(s-1)
		startDblk[s] = startDblk[s-1] + ndblks(s-1)
	}
	pageN := uint64(1) << g.pageBits
	ibSblks := 2 * l2(uint64(g.minPtrs))
	nDblkAddrs := 2 * (int(g.minPtrs) - 1)
	nSblkAddrs := nsblks - ibSblks
	arrOff := (int(g.bits) + 7) / 8
	undef := le64a(nil, haddrUndef)

	/* Where each element is stored */
	locate := func(i uint64) eaLoc {
		if i < uint64(g.idxBlkElmts) {
			return eaLoc{-1, -1, -1, i}
		}
		e := i - uint64(g.idxBlkElmts)
		s := l2(e/uint64(g.dmin) + 1)
		off := e - start[s]
		d := int(off / dn(s))
		off %= dn(s)
		if s >= ibSblks && dn(s) > pageN {
			return eaLoc{s, d, int(off / pageN), off % pageN}
		}
		return eaLoc{s, d, -1, off}
	}
	isSet, alloc := map[eaLoc]bool{}, map[eaLoc]bool{} /* elements; pages, data blocks, super blocks */
	for _, i := range x.set {
		l := locate(i)
		isSet[l] = true
		alloc[eaLoc{l.sblk, l.dblk, l.page, 0}] = true
		alloc[eaLoc{l.sblk, l.dblk, -2, 0}] = true
		alloc[eaLoc{l.sblk, -2, -2, 0}] = true
	}
	elmts := func(sblk, dblk, page int, n uint64, base uint64) []byte {
		var out []byte
		for o := range n {
			if isSet[eaLoc{sblk, dblk, page, o}] {
				out = le64a(out, data+base+o)
			} else {
				out = append(out, undef...)
			}
		}
		return out
	}
	/* (the index of the first element of a block, for its elements) */
	first := func(s, d int) uint64 { return uint64(g.idxBlkElmts) + start[s] + uint64(d)*dn(s) }

	hdr := b.alloc(72)
	prefix := func(magic string, off uint64, withOff bool) []byte {
		p := le64a(append([]byte(magic), 0, 0), hdr)
		if withOff {
			p = append(p, le64a(nil, off)[:arrOff]...)
		}
		return p
	}
	var nsuper, superSize, ndata, dataSize, realized uint64
	dblock := func(s, d int) uint64 {
		off := start[s] + uint64(d)*dn(s)
		ndata++
		if s < ibSblks || dn(s) <= pageN {
			blk := arrBlock(append(prefix("EADB", off, true), elmts(s, d, -1, dn(s), first(s, d))...))
			dataSize += uint64(len(blk))
			realized += dn(s)
			return b.put(blk)
		}
		npages := dn(s) / pageN
		blk := arrBlock(prefix("EADB", off, true))
		for p := range int(npages) {
			page := make([]byte, pageN*8+4)
			if alloc[eaLoc{s, d, p, 0}] {
				page = arrBlock(elmts(s, d, p, pageN, first(s, d)+uint64(p)*pageN))
			}
			blk = append(blk, page...)
		}
		dataSize += uint64(len(blk))
		realized += dn(s)
		return b.put(blk)
	}
	var ib []byte
	ibAddr := uint64(haddrUndef)
	if len(x.set) > 0 {
		ib = append(prefix("EAIB", 0, false), elmts(-1, -1, -1, uint64(g.idxBlkElmts), 0)...)
		realized += uint64(g.idxBlkElmts)
		for s := range ibSblks {
			for d := range int(ndblks(s)) {
				if alloc[eaLoc{s, d, -2, 0}] {
					ib = le64a(ib, dblock(s, d))
				} else {
					ib = append(ib, undef...)
				}
			}
		}
		require.Len(x.t, ib, 14+int(g.idxBlkElmts)*8+nDblkAddrs*8, "index block data block addresses")
		for s := ibSblks; s < nsblks; s++ {
			if !alloc[eaLoc{s, -2, -2, 0}] {
				ib = append(ib, undef...)
				continue
			}
			sb := prefix("EASB", start[s], true)
			var addrs []byte
			for d := range int(ndblks(s)) {
				if alloc[eaLoc{s, d, -2, 0}] {
					addrs = le64a(addrs, dblock(s, d))
				} else {
					addrs = append(addrs, undef...)
				}
			}
			if dn(s) > pageN {
				npages := dn(s) / pageN
				initSize := (npages + 7) / 8
				bitmap := make([]byte, ndblks(s)*initSize)
				for d := range int(ndblks(s)) {
					for p := range int(npages) {
						if bit := d*int(npages) + p; alloc[eaLoc{s, d, p, 0}] {
							bitmap[bit/8] |= 0x80 >> (bit % 8)
						}
					}
				}
				sb = append(sb, bitmap...)
			}
			blk := arrBlock(append(sb, addrs...))
			nsuper++
			superSize += uint64(len(blk))
			ib = le64a(ib, b.put(blk))
		}
		require.Len(x.t, ib, 14+int(g.idxBlkElmts)*8+(nDblkAddrs+nSblkAddrs)*8, "index block size")
		ibAddr = b.put(arrBlock(ib))
	}
	h := append([]byte("EAHD"), 0, 0, 8, g.bits, g.idxBlkElmts, g.dmin, g.minPtrs, g.pageBits)
	for _, v := range []uint64{nsuper, superSize, ndata, dataSize, x.maxIdxSet, realized, ibAddr} {
		h = le64a(h, v)
	}
	if x.patchHdr != nil {
		x.patchHdr(h)
	}
	b.set(hdr, arrBlock(h))
	x.hdrAddr = hdr
	return hdr
}

// arrDataset opens a file whose "/obj" is a uint8 dataset of n elements
// in chunks of one element (chunk i holds arrValue(i)) with maximum
// dimension maxDim, a version 4 layout with the chunk index index (type
// and creation parameters) at the address put returns.
func arrDataset(t *testing.T, n, maxDim uint64, index []byte, put func(b *h5Builder, data uint64) uint64) *Dataset {
	t.Helper()
	return arrDatasetPadded(t, n, maxDim, index, put, 0)
}

// arrDatasetPadded is arrDataset with the file padded to pad bytes.
func arrDatasetPadded(t *testing.T, n, maxDim uint64, index []byte, put func(b *h5Builder, data uint64) uint64, pad int) *Dataset {
	t.Helper()
	b := newH5Builder()
	data := make([]byte, n)
	for i := range n {
		data[i] = arrValue(i)
	}
	addr := put(b, b.put(data))
	obj := b.put(ohdr2(2, v2Msgs(spaceMaxMsg([]uint64{n}, []uint64{maxDim}), u8Type(), u8FillMsg(arrFill),
		v4Chunked(0, []uint64{1, 1}, 1, addr, index...))))
	root := b.rootGroup(hardLinkMsg("obj", obj))
	if pad > 0 {
		require.LessOrEqual(t, len(b.buf), pad)
		b.buf = append(b.buf, make([]byte, pad-len(b.buf))...)
	}
	f := builtFile(t, b.finish(root))
	ds, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	return ds
}

// eaDataset is arrDataset for an extensible array index (the layout
// message holds the creation parameters libhdf5 uses for datasets; the
// array header holds its own).
func eaDataset(t *testing.T, x *eaFixture) *Dataset {
	t.Helper()
	x.t = t
	return arrDataset(t, x.n, h5sUnlimitedDim, []byte{h5dChunkIdxEarray, 32, 4, 4, 16, 10}, x.put)
}

// checkArrDataset compares what the dataset reads (H5Dread), its chunks
// (H5Dchunk_iter) and its storage size (H5Dget_storage_size) with the
// chunks of set: chunk i holds arrValue(i), the others the fill value.
func checkArrDataset(t *testing.T, ds *Dataset, n uint64, set []uint64) {
	t.Helper()
	want := make([]uint8, n)
	for i := range want {
		want[i] = arrFill
	}
	var wantIter []uint64
	for _, i := range set {
		want[i] = arrValue(i)
		wantIter = append(wantIter, i)
	}
	slices.Sort(wantIter)
	got, err := Read[uint8](ds)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, wantIter, chunkScaled(t, ds))
	dset, err := ds.dataset()
	require.NoError(t, err)
	assert.Equal(t, uint64(len(set)), dset.storageSize(ds.f))
}

// seqU64 is from, from+1, ..., to-1.
func seqU64(from, to uint64) []uint64 {
	var s []uint64
	for i := from; i < to; i++ {
		s = append(s, i)
	}
	return s
}

// TestMutationEarrayGeometries: extensible arrays with creation
// parameters at the limits of H5EA__hdr_init and of the block sizing of
// the index block, super block and data block clients. libhdf5 reads
// every dataset.
func TestMutationEarrayGeometries(t *testing.T) {
	for _, c := range []struct {
		name string
		x    eaFixture
	}{
		/* Pages of 4 elements: the data blocks of super block 2 hold 4
		 * elements, as many as a page, and are not paged
		 * (h5ea_cache.go:376, 498, h5ea_dblock.go:24,
		 * h5ea_sblock.go:38); those of super block 3 hold 8 and are
		 * paged (h5ea_cache.go:528); 2 data block pointers per super
		 * block, the minimum (h5ea_hdr.go:66). Elements 8..15 are in
		 * super block 2, 16..31 in super block 3 (data block 0: pages
		 * 16..19 and 20..23). */
		{"page_as_large_as_data_block", eaFixture{
			g: eaGeom{bits: 8, idxBlkElmts: 2, dmin: 2, minPtrs: 2, pageBits: 2}, n: 40, maxIdxSet: 40,
			set: []uint64{0, 3, 9, 11, 13, 17, 26, 27, 36},
		}},
		/* 16 elements at most, 4 super blocks, all of whose data blocks
		 * are in the index block (h5ea_hdr.go:86, h5ea_iblock.go:21), and
		 * pages of 16 elements: the page size bits equal the array size
		 * bits (h5ea_hdr.go:72). */
		{"all_super_blocks_in_index_block", eaFixture{
			g: eaGeom{bits: 4, idxBlkElmts: 1, dmin: 2, minPtrs: 4, pageBits: 4}, n: 16, maxIdxSet: 16,
			set: []uint64{0, 1, 2, 5, 6, 15},
		}},
		/* 64-bit array sizes: 61 super blocks, 8-byte block offsets
		 * (h5ea_hdr.go:60, h5ea_cache.go:220). */
		{"max_nelmts_bits_64", eaFixture{
			g: eaGeom{bits: 64, idxBlkElmts: 4, dmin: 16, minPtrs: 4, pageBits: 10}, n: 300, maxIdxSet: 300,
			set: []uint64{0, 5, 20, 100, 299},
		}},
		/* A max. index set of 2^6, the most elements the array holds
		 * (h5ea_cache.go:220). */
		{"max_idx_set_full", eaFixture{
			g: eaGeom{bits: 6, idxBlkElmts: 4, dmin: 4, minPtrs: 2, pageBits: 3}, n: 64, maxIdxSet: 64,
			set: []uint64{1, 30, 63},
		}},
		/* Pages of 2 elements and data blocks of 32 elements in super
		 * block 9: 16 pages, 2 bytes of 'page init' bitmap per data
		 * block, 16 data blocks (h5ea_sblock.go:56,
		 * h5ea_cache.go:440). Super block 9 starts at element 512. */
		{"page_init_bitmaps", eaFixture{
			g: eaGeom{bits: 12, idxBlkElmts: 1, dmin: 1, minPtrs: 2, pageBits: 1}, n: 1024, maxIdxSet: 1024,
			set: []uint64{0, 7, 512, 512 + 5*32 + 3, 512 + 5*32 + 30, 512 + 15*32 + 31, 1000},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ds := eaDataset(t, &c.x)
			checkArrDataset(t, ds, c.x.n, c.x.set)
		})
	}
}

// TestMutationEarrayPagedDataBlock: the paged data block 0 of super block
// 3 of the array of "page_as_large_as_data_block" (8 elements in 2 pages
// of 4) protected on its own, as h5debug does: the data block prefix is
// the image, the block size counts the elements and the checksum of each
// page (h5ea.go:135, 136, h5ea_cache.go:528). h5debug <file> <data block>
// <header> 8 <object header> reports "Data Block size: 91" (and then
// crashes printing the elements, which a paged data block does not hold).
func TestMutationEarrayPagedDataBlock(t *testing.T) {
	x := eaFixture{
		g: eaGeom{bits: 8, idxBlkElmts: 2, dmin: 2, minPtrs: 2, pageBits: 2}, n: 40, maxIdxSet: 40,
		set: []uint64{17, 26},
	}
	ds := eaDataset(t, &x)
	ea := testEaOpen(t, ds.f, x.hdrAddr)
	iblock, err := h5eaIblockProtect(ea.hdr)
	require.NoError(t, err)
	require.Len(t, iblock.sblkAddrs, 6)
	sblock, err := h5eaSblockProtect(ea.hdr, iblock.sblkAddrs[1], 3)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), sblock.dblkNpages)
	dblock, err := h5eaDblockProtect(ea.hdr, sblock.dblkAddrs[0], 8)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), dblock.npages)
	assert.Equal(t, uint64(91), dblock.size)
	t.Logf("h5debug <file> %d %d 8 <object header>", sblock.dblkAddrs[0], x.hdrAddr)
}

// TestMutationEarrayMaxIdxSet: an array whose max. index set (9) ends in
// the middle of data block 0 of super block 2 (elements 8..11), all of
// whose elements up to 39 are stored. libhdf5 reads the chunks below 9
// and the fill value for the others (H5EA_get returns the fill value at
// and past the max. index set, h5ea.go:507), and H5Dchunk_iter reports
// the chunks below 9 (H5EA_iterate stops at the max. index set,
// h5ea.go:561, 581).
func TestMutationEarrayMaxIdxSet(t *testing.T) {
	x := eaFixture{
		g: eaGeom{bits: 8, idxBlkElmts: 2, dmin: 2, minPtrs: 2, pageBits: 2}, n: 40, maxIdxSet: 9,
		set: seqU64(0, 40),
	}
	ds := eaDataset(t, &x)
	checkArrDataset(t, ds, x.n, seqU64(0, 9))
}

// TestMutationEarrayHeaderStatistics: the array of
// "page_as_large_as_data_block" with header statistics libhdf5 never
// uses (the # of super blocks and data blocks, their sizes and the # of
// elements realized) larger than the file. libhdf5 reads the dataset.
func TestMutationEarrayHeaderStatistics(t *testing.T) {
	set := []uint64{0, 3, 9, 11, 13, 17, 26, 27, 36}
	for _, c := range []struct {
		name string
		off  int
		v    uint64
	}{
		{"super_blocks", 12, 1000},
		{"super_block_size", 20, 1 << 60},
		{"data_blocks", 28, 1 << 60},
		{"data_block_size", 36, 1 << 60},
		{"elements", 52, 1 << 62},
		{"elements_max", 52, 1<<64 - 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := eaFixture{
				g: eaGeom{bits: 8, idxBlkElmts: 2, dmin: 2, minPtrs: 2, pageBits: 2}, n: 40, maxIdxSet: 40, set: set,
				patchHdr: func(h []byte) { binary.LittleEndian.PutUint64(h[c.off:], c.v) },
			}
			checkArrDataset(t, eaDataset(t, &x), x.n, set)
		})
	}
}

// TestMutationEarrayLargePages: arrays whose data block pages are larger
// than the array (page size bits above the array size bits: no data
// block is paged). libhdf5 reads the datasets.
func TestMutationEarrayLargePages(t *testing.T) {
	for _, pageBits := range []uint8{5, 63} {
		t.Run(strconv.Itoa(int(pageBits)), func(t *testing.T) {
			x := eaFixture{
				g: eaGeom{bits: 4, idxBlkElmts: 1, dmin: 2, minPtrs: 4, pageBits: pageBits}, n: 16, maxIdxSet: 16,
				set: []uint64{0, 1, 2, 5, 6, 15},
			}
			checkArrDataset(t, eaDataset(t, &x), x.n, x.set)
		})
	}
}

// faFixture describes a fixed array chunk index (unfiltered) of nelmts
// elements: the elements of set are defined; the data block is allocated
// when set is not empty, with the pages holding one of them initialized.
// With partial, only the data block prefix and the pages up to the last
// initialized one are put (the rest of the data block lies past them).
type faFixture struct {
	pageBits uint8
	nelmts   uint64
	set      []uint64
	partial  bool
	patchHdr func(hdr []byte) /* changes the header before its checksum is computed */
	hdrAddr  uint64           /* (set by put) */
}

// put assembles the array (the chunk of element i at data+i) and
// returns the header address.
func (x *faFixture) put(b *h5Builder, data uint64) uint64 {
	pageN := uint64(1) << x.pageBits
	isSet := map[uint64]bool{}
	for _, i := range x.set {
		isSet[i] = true
	}
	elmts := func(from, n uint64) []byte {
		var out []byte
		for i := from; i < from+n; i++ {
			if isSet[i] {
				out = le64a(out, data+i)
			} else {
				out = le64a(out, haddrUndef)
			}
		}
		return out
	}
	hdr := b.alloc(28)
	dblk := uint64(haddrUndef)
	if len(x.set) > 0 {
		blk := le64a(append([]byte("FADB"), 0, 0), hdr)
		if x.nelmts <= pageN {
			blk = arrBlock(append(blk, elmts(0, x.nelmts)...))
		} else {
			npages := (x.nelmts + pageN - 1) / pageN
			bitmap := make([]byte, (npages+7)/8)
			last := uint64(0)
			for _, i := range x.set {
				bitmap[i/pageN/8] |= 0x80 >> (i / pageN % 8)
				last = max(last, i/pageN)
			}
			blk = arrBlock(append(blk, bitmap...))
			for p := range npages {
				if x.partial && p > last {
					break
				}
				n := min(pageN, x.nelmts-p*pageN)
				page := make([]byte, n*8+4)
				if bitmap[p/8]&(0x80>>(p%8)) != 0 {
					page = arrBlock(elmts(p*pageN, n))
				}
				blk = append(blk, page...)
			}
		}
		dblk = b.put(blk)
	}
	h := le64a(le64a(append([]byte("FAHD"), 0, 0, 8, x.pageBits), x.nelmts), dblk)
	if x.patchHdr != nil {
		x.patchHdr(h)
	}
	b.set(hdr, arrBlock(h))
	x.hdrAddr = hdr
	return hdr
}

// faDataset is arrDataset for a fixed array index of x.nelmts chunks.
func faDataset(t *testing.T, x *faFixture) *Dataset {
	t.Helper()
	return arrDataset(t, x.nelmts, x.nelmts, []byte{h5dChunkIdxFarray, x.pageBits}, x.put)
}

// TestMutationFarrayGeometries: fixed arrays at the limits of the paging
// of H5FA__dblock_alloc and of the data block client. libhdf5 reads every
// dataset; the size of the data block in the header statistics is the
// index size H5Oget_native_info reports minus the 28 bytes of the header.
func TestMutationFarrayGeometries(t *testing.T) {
	for _, c := range []struct {
		name     string
		x        faFixture
		dblkSize uint64
	}{
		/* 16 elements in pages of 16: not paged (h5fa_dblock.go:23,
		 * h5fa_cache.go:184, 241) */
		{"page_as_large_as_data_block", faFixture{pageBits: 4, nelmts: 16, set: []uint64{0, 7, 15}}, 18 + 16*8},
		/* 32 elements in 8 full pages of 4: one byte of 'page init'
		 * bitmap, a full last page (h5fa_dblock.go:25, 48,
		 * h5fa_cache.go:185, 242) */
		{"eight_full_pages", faFixture{pageBits: 2, nelmts: 32, set: []uint64{1, 13, 28, 31}}, 19 + 8*(4*8+4)},
		/* 9 pages, the last one holding 1 element */
		{"partial_last_page", faFixture{pageBits: 2, nelmts: 33, set: []uint64{2, 32}}, 20 + 8*(4*8+4) + 8 + 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			ds := faDataset(t, &c.x)
			checkArrDataset(t, ds, c.x.nelmts, c.x.set)
			fa := testFaOpen(t, ds.f, c.x.hdrAddr)
			assert.Equal(t, c.dblkSize, fa.getStats().dblkSize)
		})
	}
}

// TestMutationFarrayElementsFillFile: a paged fixed array of 128 elements
// of 8 bytes in a file of 1024 bytes, whose data block holds its first
// two pages only (the others are not initialized and lie past the end of
// the file): the elements fill the file, the most a fixed array with a
// data block may hold here (h5fa_cache.go:177). libhdf5 reads the
// dataset.
func TestMutationFarrayElementsFillFile(t *testing.T) {
	x := faFixture{pageBits: 2, nelmts: 128, set: []uint64{0, 5}, partial: true}
	ds := arrDatasetPadded(t, x.nelmts, x.nelmts, []byte{h5dChunkIdxFarray, x.pageBits}, x.put, 1024)
	checkArrDataset(t, ds, x.nelmts, x.set)
}

// TestMutationFarrayPageBits64: a fixed array of 4 elements whose header
// says pages of 2^64 elements (h5fa_cache.go:169): the C code shifts 1 by
// 64 to get the page size, which is undefined. hdf5 refuses the header.
func TestMutationFarrayPageBits64(t *testing.T) {
	x := faFixture{pageBits: 6, nelmts: 4, set: []uint64{1, 2}, patchHdr: func(h []byte) { h[7] = 64 }}
	ds := faDataset(t, &x)
	_, err := Read[uint8](ds)
	assert.ErrorContains(t, err, "bad max. # of elements per data block page bits 64")
}

// TestMutationEarrayPageBits64: the array of
// "page_as_large_as_data_block" whose header says pages of 2^64 elements
// (h5ea_hdr.go:72): the C code shifts 1 by 64 to get the page size, which
// is undefined. hdf5 refuses the header.
func TestMutationEarrayPageBits64(t *testing.T) {
	x := eaFixture{
		g: eaGeom{bits: 8, idxBlkElmts: 2, dmin: 2, minPtrs: 2, pageBits: 2}, n: 40, maxIdxSet: 40,
		set:      []uint64{0, 3, 9, 11, 13, 17, 26, 27, 36},
		patchHdr: func(h []byte) { h[11] = 64 },
	}
	_, err := Read[uint8](eaDataset(t, &x))
	assert.ErrorContains(t, err, "max. # of elements per data block page bits must be < 64")
}
