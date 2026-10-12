package hdf5

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Expected values in these tests come from h5debug (HDF5 1.14.6):
//
//	h5debug <file> <heap header address>                        (FRHP)
//	h5debug <file> <bt2 node address> <bt2 header address> ...  (dense name index records)
//
// The dense name index records hold the lookup3 hash of the link/attribute
// name and its heap ID, so a heap object read through an ID can be checked
// against the hash of the name decoded from it.

const newgratFile = "tools/test/h5stat/testfiles/h5stat_newgrat.h5"

func openHeap(t *testing.T, path string, addr haddr) (*File, *h5hf) {
	t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot(t), path)
	}
	f, err := Open(path, WithExternalLinks(false))
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	fh, err := f.hfOpen(addr)
	require.NoError(t, err)
	return f, fh
}

// hfWalkDblocks visits every direct block of the heap's managed space, in
// heap offset order (a test-only walk of the doubling table, independent of
// the offset -> block lookup used by the read path).
func hfWalkDblocks(fh *h5hf, fn func(db *h5hfDirect) error) error {
	hdr := fh.hdr
	dt := &hdr.manDtable
	if !addrDefined(dt.tableAddr) {
		return nil
	}
	if dt.curRootRows == 0 {
		db, err := h5hfManDblockProtect(hdr, dt.tableAddr, dt.startBlockSize, nil, 0)
		if err != nil {
			return err
		}
		return fn(db)
	}
	var walk func(addr haddr, nrows uint32, par *h5hfIndirect, parEntry uint32) error
	walk = func(addr haddr, nrows uint32, par *h5hfIndirect, parEntry uint32) error {
		ib, err := h5hfManIblockProtect(hdr, addr, nrows, par, parEntry)
		if err != nil {
			return err
		}
		for row := uint32(0); row < ib.nrows; row++ {
			for col := uint32(0); col < uint32(dt.width); col++ {
				entry := row*uint32(dt.width) + col
				child := ib.ents[entry]
				if !addrDefined(child) {
					continue
				}
				if row < dt.maxDirectRows {
					db, err := h5hfManDblockProtect(hdr, child, dt.rowBlockSize[row], ib, entry)
					if err != nil {
						return err
					}
					if want := ib.blockOff + dt.rowBlockOff[row] + uint64(col)*dt.rowBlockSize[row]; db.blockOff != want {
						return fmt.Errorf("direct block at %d has offset %d, want %d", child, db.blockOff, want)
					}
					if err := fn(db); err != nil {
						return err
					}
				} else {
					childRows := h5hfLog2Gen(dt.rowBlockSize[row]) - dt.firstRowBits + 1
					if err := walk(child, childRows, ib, entry); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return walk(dt.tableAddr, uint32(dt.curRootRows), nil, 0)
}

// manID encodes a managed heap ID (H5HF_MAN_ID_ENCODE).
func manID(hdr *h5hfHdr, off, length uint64) []byte {
	id := make([]byte, hdr.idLen)
	id[0] = h5hfIDVersCurr | h5hfIDTypeMan
	for i := 0; i < int(hdr.heapOffSize); i++ {
		id[1+i] = byte(off >> (8 * i))
	}
	for i := 0; i < int(hdr.heapLenSize); i++ {
		id[1+int(hdr.heapOffSize)+i] = byte(length >> (8 * i))
	}
	return id
}

// hugeID encodes an indirectly accessed 'huge' heap ID.
func hugeID(hdr *h5hfHdr, n uint64) []byte {
	id := make([]byte, hdr.idLen)
	id[0] = h5hfIDVersCurr | h5hfIDTypeHuge
	for i := 0; i < int(hdr.hugeIDSize); i++ {
		id[1+i] = byte(n >> (8 * i))
	}
	return id
}

// testLink is the part of an encoded link message (src/H5Olink.c) the
// tests look at.
type testLink struct {
	name  string
	typ   uint8
	value string // soft/external link value
	size  int    // encoded size
}

func parseLink(b []byte, sizeofAddr int) (testLink, error) {
	var l testLink
	p := &image{buf: b}
	if v := p.uint8(); v != 1 {
		return l, fmt.Errorf("bad link message version %d", v)
	}
	flags := p.uint8()
	if flags&0x08 != 0 {
		l.typ = p.uint8()
	}
	if flags&0x04 != 0 {
		p.skip(8)
	}
	if flags&0x10 != 0 {
		p.skip(1)
	}
	nameLen := p.uintVar(1 << (flags & 3))
	if p.overflow(int(nameLen)) || nameLen == 0 {
		return l, fmt.Errorf("bad link name length %d", nameLen)
	}
	l.name = string(p.bytes(int(nameLen)))
	switch l.typ {
	case 0:
		p.skip(sizeofAddr)
	case 1:
		n := int(p.uint16())
		l.value = string(p.bytes(n))
	default:
		if l.typ < 64 {
			return l, fmt.Errorf("bad link type %d", l.typ)
		}
		// external (64) and user-defined links: length + data
		n := int(p.uint16())
		l.value = string(p.bytes(n))
	}
	l.size = p.off
	return l, p.done()
}

// testAttr is the part of an encoded attribute message (src/H5Oattr.c) the
// tests look at.
type testAttr struct {
	name string
	data []byte
	size int // encoded size
}

func parseAttr(b []byte, sizeofSize int) (testAttr, error) {
	var a testAttr
	p := &image{buf: b}
	version := p.uint8()
	if version < 1 || version > 3 {
		return a, fmt.Errorf("bad attribute message version %d", version)
	}
	flags := p.uint8()
	if flags != 0 {
		return a, fmt.Errorf("shared attribute components not supported by the test")
	}
	nameLen := int(p.uint16())
	dtSize := int(p.uint16())
	dsSize := int(p.uint16())
	if version == 3 {
		p.skip(1)
	}
	pad := func(n int) int {
		if version == 1 {
			return int(h5AlignOld(uint64(n)))
		}
		return n
	}
	name := p.bytes(pad(nameLen))
	if len(name) < nameLen || nameLen == 0 {
		return a, fmt.Errorf("bad attribute name")
	}
	a.name = strings.TrimRight(string(name[:nameLen]), "\x00")
	dt := p.sub(pad(dtSize))
	ds := p.sub(pad(dsSize))
	if p.err != nil {
		return a, p.err
	}
	dt.skip(4)
	elemSize := uint64(dt.uint32())
	dsVersion := ds.uint8()
	ndims := int(ds.uint8())
	nelem := uint64(1)
	switch dsVersion {
	case 1:
		ds.skip(6)
	case 2:
		ds.skip(1)
		if typ := ds.uint8(); typ == 2 {
			nelem = 0
		}
	default:
		return a, fmt.Errorf("bad dataspace version %d", dsVersion)
	}
	for i := 0; i < ndims; i++ {
		nelem *= ds.uintVar(sizeofSize)
	}
	if dt.err != nil || ds.err != nil {
		return a, errors.Join(dt.err, ds.err)
	}
	a.data = p.bytes(int(nelem * elemSize))
	a.size = p.off
	return a, p.done()
}

func TestFractalHeapHeader(t *testing.T) {
	_, fh := openHeap(t, newgratFile, 1874)
	hdr := fh.hdr
	assert.Equal(t, haddr(1874), fh.heapAddr())
	assert.Equal(t, 7, fh.idLen())
	assert.EqualValues(t, 0, hdr.filterLen)
	assert.Nil(t, hdr.pline)
	assert.False(t, hdr.hugeIDsWrapped)
	assert.True(t, hdr.checksumDblocks)
	assert.EqualValues(t, 66220223, hdr.freeSpace)
	assert.EqualValues(t, 67108864, hdr.manSize)
	assert.EqualValues(t, 737280, hdr.manAllocSize)
	assert.EqualValues(t, 737280, hdr.manIterOff)
	assert.EqualValues(t, 35001, hdr.manNobjs)
	assert.Equal(t, haddr(2058), hdr.fsAddr)
	assert.EqualValues(t, 4096, hdr.maxManSize)
	assert.EqualValues(t, 0, hdr.hugeSize)
	assert.EqualValues(t, 0, hdr.hugeNobjs)
	assert.EqualValues(t, 0, hdr.nextManID)
	assert.Equal(t, haddrUndef, hdr.hugeBt2Addr)
	assert.EqualValues(t, 0, hdr.tinySize)
	assert.EqualValues(t, 0, hdr.tinyNobjs)
	dt := &hdr.manDtable
	assert.EqualValues(t, 4, dt.width)
	assert.EqualValues(t, 512, dt.startBlockSize)
	assert.EqualValues(t, 65536, dt.maxDirectSize)
	assert.EqualValues(t, 32, dt.maxIndexBits)
	assert.EqualValues(t, 1, dt.startRootRows)
	assert.Equal(t, haddr(6361522), dt.tableAddr)
	assert.EqualValues(t, 16, dt.curRootRows)
	assert.EqualValues(t, 22, dt.maxRootRows)
	assert.EqualValues(t, 9, dt.maxDirectRows)
	assert.EqualValues(t, 11, dt.firstRowBits)
	assert.EqualValues(t, 2048, dt.numIDFirstRow)
	assert.EqualValues(t, 4, hdr.heapOffSize)
	assert.EqualValues(t, 2, hdr.heapLenSize)
	assert.False(t, hdr.hugeIDsDirect)
	assert.EqualValues(t, 6, hdr.hugeIDSize)
	assert.EqualValues(t, 6, hdr.tinyMaxLen)
	assert.False(t, hdr.tinyLenExtended)

	// The attribute heap of /DATASET_NAME.
	_, fh2 := openHeap(t, newgratFile, 6304375)
	hdr2 := fh2.hdr
	assert.Equal(t, 8, fh2.idLen())
	assert.EqualValues(t, 416, hdr2.freeSpace)
	assert.EqualValues(t, 4096, hdr2.manSize)
	assert.EqualValues(t, 100, hdr2.manNobjs)
	assert.Equal(t, haddr(6304521), hdr2.fsAddr)
	assert.EqualValues(t, 1024, hdr2.manDtable.startBlockSize)
	assert.EqualValues(t, 40, hdr2.manDtable.maxIndexBits)
	assert.Equal(t, haddr(6311826), hdr2.manDtable.tableAddr)
	assert.EqualValues(t, 1, hdr2.manDtable.curRootRows)
	assert.EqualValues(t, 29, hdr2.manDtable.maxRootRows)
	assert.EqualValues(t, 8, hdr2.manDtable.maxDirectRows)
	assert.EqualValues(t, 12, hdr2.manDtable.firstRowBits)
	assert.EqualValues(t, 4096, hdr2.manDtable.numIDFirstRow)
	assert.EqualValues(t, 5, hdr2.heapOffSize)
	assert.EqualValues(t, 2, hdr2.heapLenSize)

	// Not a heap header.
	f := fh.f
	_, err := f.hfOpen(2020)
	assert.Error(t, err)
	_, err = f.hfOpen(haddrUndef)
	assert.Error(t, err)
}

// Heap IDs and name hashes of one leaf of the root group's dense name index
// (h5debug h5stat_newgrat.h5 2140 2020 38). Several of these objects live
// below child indirect blocks (heap offset >= 2^19).
var newgratLinkRecords = []struct {
	hash uint32
	id   string
}{
	{0x7ac5, "00b69a06001500"}, {0x403f8, "00446d07001500"}, {0x5f0a0, "0062e708001500"},
	{0x61ebf, "00303304001500"}, {0x809f8, "00cc7504001500"}, {0x10f055, "00239c08001500"},
	{0x11e8a0, "000d2c06001500"}, {0x145b44, "00297302001400"}, {0x158ec0, "00cb5708001500"},
	{0x17e09e, "001ac303001500"}, {0x1b80b8, "00ad0802001400"}, {0x1d32bf, "0089f601001400"},
	{0x1e4f43, "00c5e506001500"}, {0x1ee735, "00c5fa05001500"}, {0x1fa6fd, "00498d01001400"},
	{0x20a90f, "0039cc09001500"}, {0x216bd1, "008f5609001500"}, {0x225de2, "00349b05001500"},
	{0x22f9cb, "0084db07001500"}, {0x247aec, "00dd6d01001400"}, {0x249305, "00c1ab02001400"},
	{0x266358, "00ddb202001400"}, {0x286277, "00ce0107001500"}, {0x28c702, "00a86904001500"},
	{0x2911f0, "00955702001400"}, {0x2cbc6c, "00a1af05001500"}, {0x2dad0b, "006f3307001500"},
	{0x341bf6, "00436807001500"}, {0x369770, "0095c701001400"}, {0x378e09, "00f51001001400"},
	{0x392f06, "00930a08001500"}, {0x3b6542, "00feb207001500"}, {0x3b6b98, "00d19009001500"},
	{0x3b7f88, "004c4105001500"}, {0x3becf6, "003ace06001500"}, {0x3bf69b, "00b50503001400"},
	{0x3c2fee, "009d4a02001400"}, {0x3cd2d2, "0017ca0a001500"},
	// root node record of the index (h5debug ... 3630911 2020 1 3)
	{0x8579e3a6, "00c10303001400"},
}

func hexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b := make([]byte, len(s)/2)
	for i := range b {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		require.NoError(t, err)
		b[i] = byte(v)
	}
	return b
}

func TestFractalHeapReadLinksByID(t *testing.T) {
	f, fh := openHeap(t, newgratFile, 1874)
	for _, rec := range newgratLinkRecords {
		id := hexBytes(t, rec.id)
		obj, err := fh.read(id)
		require.NoError(t, err, rec.id)
		n, err := fh.objLen(id)
		require.NoError(t, err)
		assert.EqualValues(t, len(obj), n)
		l, err := parseLink(obj, int(f.sizeofAddr))
		require.NoError(t, err, rec.id)
		assert.Equal(t, rec.hash, checksumLookup3([]byte(l.name), 0), "%s: %q", rec.id, l.name)
		assert.True(t, strings.HasPrefix(l.name, "GROUP"), l.name)
		assert.Equal(t, len(obj), l.size)

		// op sees the same bytes without copying.
		require.NoError(t, fh.op(id, func(b []byte) error {
			assert.Equal(t, obj, b)
			return nil
		}))
		off, err := fh.objOff(id)
		require.NoError(t, err)
		assert.Equal(t, uintVarLen(id[1:], 4), off)
	}
}

// Heap IDs (printed by h5debug as a little-endian uint64) and name hashes of
// /DATASET_NAME's dense attribute name index (h5debug ... 6305115 1086 3 1
// and h5debug ... 6304603 1086 26).
var newgratAttrRecords = []struct {
	id   uint64
	hash uint32
}{
	{0x0024000000019900, 0x48d6c52b}, {0x0024000000048200, 0x7c4df009}, {0x002400000009c600, 0xc9d7164d},
	{0x0024000000015100, 0x07814e11}, {0x002400000008ee00, 0x0b4e697c}, {0x002400000004ee00, 0x0c0cc73e},
	{0x00240000000e5600, 0x0ce6ba03}, {0x002400000002dd00, 0x0e0e56f9}, {0x002300000000e800, 0x11929ff4},
	{0x002400000008ca00, 0x13f40ad7}, {0x002400000002b900, 0x16daa082}, {0x0024000000041600, 0x202cfe92},
	{0x00240000000a5600, 0x2234163b}, {0x00240000000a7a00, 0x24b5fb48}, {0x002400000008a600, 0x26a41283},
	{0x00240000000b7600, 0x27ac7fc5}, {0x002400000004a600, 0x2c641846}, {0x0024000000091200, 0x2ca48089},
	{0x00240000000b5200, 0x2d114dd5}, {0x00240000000dc600, 0x3121cec7}, {0x0024000000075200, 0x31c0b4a7},
	{0x0023000000001600, 0x320e0f3c}, {0x0024000000051200, 0x359dc1c7}, {0x0023000000010b00, 0x360ab90a},
	{0x002400000006e600, 0x39ff0926}, {0x002400000004ca00, 0x3b9b550e}, {0x0024000000081600, 0x3e2ae9c9},
	{0x00240000000d7e00, 0x460d8752}, {0x00240000000d5a00, 0x46ed5835},
}

func TestFractalHeapReadAttributesByID(t *testing.T) {
	f, fh := openHeap(t, newgratFile, 6304375)
	for _, rec := range newgratAttrRecords {
		id := binary.LittleEndian.AppendUint64(nil, rec.id)
		obj, err := fh.read(id)
		require.NoError(t, err, "%#x", rec.id)
		a, err := parseAttr(obj, int(f.sizeofSize))
		require.NoError(t, err)
		assert.Equal(t, rec.hash, checksumLookup3([]byte(a.name), 0), a.name)
		assert.True(t, strings.HasPrefix(a.name, "ATTR"), a.name)
		assert.Equal(t, []byte{0, 0, 0, 0}, a.data) // h5dump: (0): 0
		assert.Equal(t, len(obj), a.size)
	}
}

// walkObjects parses the objects packed in each direct block of a heap with
// parse (which returns the size of the object at the start of b) and reads
// every one back through a managed heap ID.
func walkObjects(t *testing.T, fh *h5hf, parse func(b []byte) (int, error), visit func(obj []byte)) int {
	t.Helper()
	count, err := walkObjectsErr(fh, parse, visit)
	require.NoError(t, err)
	return count
}

// walkObjectsErr is walkObjects returning the error.
func walkObjectsErr(fh *h5hf, parse func(b []byte) (int, error), visit func(obj []byte)) (int, error) {
	hdr := fh.hdr
	overhead := h5hfManAbsDirectOverhead(hdr)
	count := 0
	err := hfWalkDblocks(fh, func(db *h5hfDirect) error {
		pos := overhead
		for pos < db.size && db.blk[pos] != 0 {
			n, err := parse(db.blk[pos:])
			if err != nil {
				return fmt.Errorf("object at heap offset %d: %w", db.blockOff+pos, err)
			}
			id := manID(hdr, db.blockOff+pos, uint64(n))
			obj, err := fh.read(id)
			if err != nil {
				return err
			}
			if !bytes.Equal(obj, db.blk[pos:pos+uint64(n)]) {
				return fmt.Errorf("object at heap offset %d read back differently", db.blockOff+pos)
			}
			visit(obj)
			count++
			pos += uint64(n)
		}
		return nil
	})
	return count, err
}

func TestFractalHeapWalkLinks(t *testing.T) {
	f, fh := openHeap(t, newgratFile, 1874)
	names := map[string]bool{}
	n := walkObjects(t, fh, func(b []byte) (int, error) {
		l, err := parseLink(b, int(f.sizeofAddr))
		return l.size, err
	}, func(obj []byte) {
		l, err := parseLink(obj, int(f.sizeofAddr))
		require.NoError(t, err)
		names[l.name] = true
	})
	assert.EqualValues(t, fh.hdr.manNobjs, n)
	// h5ls -r: /DATASET_NAME and /GROUP1 .. /GROUP35000
	require.Len(t, names, 35001)
	assert.True(t, names["DATASET_NAME"])
	for i := 1; i <= 35000; i++ {
		if !names["GROUP"+strconv.Itoa(i)] {
			t.Fatalf("GROUP%d not found", i)
		}
	}
}

func TestFractalHeapWalkAttributes(t *testing.T) {
	f, fh := openHeap(t, newgratFile, 6304375)
	names := map[string]bool{}
	n := walkObjects(t, fh, func(b []byte) (int, error) {
		a, err := parseAttr(b, int(f.sizeofSize))
		return a.size, err
	}, func(obj []byte) {
		a, err := parseAttr(obj, int(f.sizeofSize))
		require.NoError(t, err)
		names[a.name] = true
	})
	assert.EqualValues(t, 100, n)
	for i := 1; i <= 100; i++ {
		assert.True(t, names["ATTR"+strconv.Itoa(i)], i)
	}
}

// TestFractalHeapCorpus opens every fractal heap in the corpus (located by
// signature), walks its direct blocks and reads back the link and attribute
// objects it can parse.
func TestFractalHeapCorpus(t *testing.T) {
	heaps, objects := 0, 0
	for _, path := range corpusFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		if !bytes.Contains(raw, []byte(h5hfHdrMagic)) {
			continue
		}
		f, err := Open(path, WithExternalLinks(false))
		if err != nil {
			continue
		}
		for i := 0; ; {
			j := bytes.Index(raw[i:], []byte(h5hfHdrMagic))
			if j < 0 {
				break
			}
			i += j + 1
			addr := haddr(i-1) - f.baseAddr
			fh, err := f.hfOpen(addr)
			if err != nil {
				t.Logf("%s @%d: %v", filepath.Base(path), addr, err)
				continue
			}
			heaps++
			var parse func(b []byte) (int, error)
			// Classify the heap by its first object: link messages,
			// attribute messages or something else (shared messages).
			var first []byte
			require.NoError(t, hfWalkDblocks(fh, func(db *h5hfDirect) error {
				if first == nil {
					first = db.blk[h5hfManAbsDirectOverhead(fh.hdr):]
				}
				return nil
			}), path)
			parseLinkSize := func(b []byte) (int, error) {
				l, err := parseLink(b, int(f.sizeofAddr))
				return l.size, err
			}
			parseAttrSize := func(b []byte) (int, error) {
				a, err := parseAttr(b, int(f.sizeofSize))
				return a.size, err
			}
			if _, err := parseLinkSize(first); err == nil && first[0] == 1 {
				parse = parseLinkSize
			} else if _, err := parseAttrSize(first); err == nil && first[0] == 3 {
				parse = parseAttrSize
			} else {
				t.Logf("%s @%d: %d objects (not links or attributes)", filepath.Base(path), addr, fh.hdr.manNobjs)
				continue
			}
			n, err := walkObjectsErr(fh, parse, func([]byte) {})
			assert.NoError(t, err, "%s @%d", path, addr)
			// (objects that were removed stay in their direct block, e.g.
			// the deleted link "mytype2" of tfcontents1.h5)
			assert.GreaterOrEqual(t, uint64(n), fh.hdr.manNobjs, "%s @%d", path, addr)
			objects += n
		}
		f.Close()
	}
	t.Logf("%d fractal heaps, %d link/attribute objects read", heaps, objects)
	assert.GreaterOrEqual(t, heaps, 14)
	assert.Greater(t, objects, 35000)
}

func TestFractalHeapHugeObjects(t *testing.T) {
	path, err := filepath.Abs("testdata/fheap_huge.h5")
	require.NoError(t, err)

	// Links of /dense: 200 managed soft links and two 'huge' ones.
	f, fh := openHeap(t, path, 258)
	hdr := fh.hdr
	assert.EqualValues(t, 200, hdr.manNobjs)
	assert.EqualValues(t, 2, hdr.hugeNobjs)
	assert.EqualValues(t, 11028, hdr.hugeSize)
	assert.EqualValues(t, 2, hdr.nextManID)
	assert.Equal(t, haddr(3596), hdr.hugeBt2Addr)
	assert.EqualValues(t, 4, hdr.manDtable.curRootRows)
	assert.False(t, hdr.hugeIDsDirect)
	links := map[string]string{}
	n := walkObjects(t, fh, func(b []byte) (int, error) {
		l, err := parseLink(b, int(f.sizeofAddr))
		return l.size, err
	}, func(obj []byte) {
		l, err := parseLink(obj, int(f.sizeofAddr))
		require.NoError(t, err)
		links[l.name] = l.value
	})
	assert.Equal(t, 200, n)
	for i := 0; i < 200; i++ {
		assert.Equal(t, fmt.Sprintf("/target%03d", i), links[fmt.Sprintf("link%03d", i)])
	}
	for _, huge := range []uint64{1, 2} {
		obj, err := fh.read(hugeID(hdr, huge))
		require.NoError(t, err)
		l, err := parseLink(obj, int(f.sizeofAddr))
		require.NoError(t, err)
		switch l.name {
		case "longvalue":
			assert.Equal(t, strings.Repeat("v", 6000), l.value)
		default:
			assert.Equal(t, strings.Repeat("n", 5000), l.name)
			assert.Equal(t, "/short", l.value)
		}
	}

	// Attributes of /dense: a00..a09 = i*i and 'huge' "big" = i%251.
	_, fh2 := openHeap(t, path, 10161)
	attrs := map[string][]byte{}
	n = walkObjects(t, fh2, func(b []byte) (int, error) {
		a, err := parseAttr(b, int(f.sizeofSize))
		return a.size, err
	}, func(obj []byte) {
		a, err := parseAttr(obj, int(f.sizeofSize))
		require.NoError(t, err)
		attrs[a.name] = a.data
	})
	assert.Equal(t, 10, n)
	for i := 0; i < 10; i++ {
		assert.Equal(t, binary.LittleEndian.AppendUint32(nil, uint32(i*i)), attrs[fmt.Sprintf("a%02d", i)])
	}
	obj, err := fh2.read(hugeID(fh2.hdr, 1))
	require.NoError(t, err)
	a, err := parseAttr(obj, int(f.sizeofSize))
	require.NoError(t, err)
	assert.Equal(t, "big", a.name)
	require.Len(t, a.data, 8000)
	for i, v := range a.data {
		if v != byte(i%251) {
			t.Fatalf("big[%d] = %d", i, v)
		}
	}

	// The 'huge' object lengths and offsets come from the v2 B-tree too.
	var total uint64
	for _, huge := range []uint64{1, 2} {
		n, err := fh.objLen(hugeID(hdr, huge))
		require.NoError(t, err)
		obj, err := fh.read(hugeID(hdr, huge))
		require.NoError(t, err)
		assert.EqualValues(t, len(obj), n)
		total += n
		off, err := fh.objOff(hugeID(hdr, huge))
		require.NoError(t, err)
		assert.NotZero(t, off)
	}
	assert.Equal(t, hdr.hugeSize, total)
	_, err = fh.read(hugeID(hdr, 3))
	assert.True(t, IsNotFound(err), "missing 'huge' object: %v", err)
}

func TestFractalHeapFiltered(t *testing.T) {
	path, err := filepath.Abs("testdata/fheap_filtered.h5")
	require.NoError(t, err)
	f, fh := openHeap(t, path, 290)
	hdr := fh.hdr

	// h5debug fheap_filtered.h5 290
	assert.NotZero(t, hdr.filterLen)
	require.NotNil(t, hdr.pline)
	assert.EqualValues(t, 1, hdr.pline.nused)
	require.Len(t, hdr.pline.filter, 1)
	assert.EqualValues(t, h5zFilterDeflate, hdr.pline.filter[0].id)
	assert.EqualValues(t, h5zFlagOptional, hdr.pline.filter[0].flags)
	assert.Equal(t, []uint32{6}, hdr.pline.filter[0].cdValues)
	assert.EqualValues(t, 300, hdr.manNobjs)
	assert.EqualValues(t, 1, hdr.hugeNobjs)
	assert.EqualValues(t, 9148, hdr.freeSpace)
	assert.EqualValues(t, 16384, hdr.manSize)
	assert.EqualValues(t, 8192, hdr.manAllocSize)
	assert.Equal(t, haddr(498), hdr.fsAddr)
	assert.Equal(t, haddr(5188), hdr.hugeBt2Addr)
	assert.Equal(t, haddr(9826), hdr.manDtable.tableAddr)
	assert.EqualValues(t, 4, hdr.manDtable.curRootRows)
	assert.False(t, hdr.hugeIDsDirect)

	// The root indirect block carries the filtered sizes of its direct
	// blocks.
	ib, err := h5hfManIblockProtect(hdr, hdr.manDtable.tableAddr, uint32(hdr.manDtable.curRootRows), nil, 0)
	require.NoError(t, err)
	require.Len(t, ib.filtEnts, 4*4)
	children := 0
	for u, addr := range ib.ents {
		if addrDefined(addr) {
			children++
			assert.NotZero(t, ib.filtEnts[u].size)
			assert.Less(t, ib.filtEnts[u].size, hdr.manDtable.rowBlockSize[u/4])
		}
	}
	assert.EqualValues(t, children, ib.nchildren)

	// The first object of the heap is the first link inserted: "link000" ->
	// "/target000" (version, flags, type, name length, name, value length,
	// value).
	first := manID(hdr, h5hfManAbsDirectOverhead(hdr), 1+1+1+1+7+2+10)
	obj, err := fh.read(first)
	if errors.Is(err, ErrUnsupported) {
		t.Logf("filtered direct block: %v (filters not translated yet)", err)
	} else {
		require.NoError(t, err)
		l, err := parseLink(obj, int(f.sizeofAddr))
		require.NoError(t, err)
		assert.Equal(t, "link000", l.name)
		assert.Equal(t, "/target000", l.value)
	}
	obj, err = fh.read(hugeID(hdr, 1))
	if errors.Is(err, ErrUnsupported) {
		t.Logf("filtered 'huge' object: %v (filters not translated yet)", err)
	} else {
		require.NoError(t, err)
		l, err := parseLink(obj, int(f.sizeofAddr))
		require.NoError(t, err)
		assert.Equal(t, strings.Repeat("n", 5000), l.name)
		assert.Equal(t, "/short", l.value)
	}
}

func TestFractalHeapTinyObjects(t *testing.T) {
	for _, idLen := range []uint16{8, 18, 19, 40} {
		hdr := &h5hfHdr{idLen: idLen}
		h5hfTinyInit(hdr)
		fh := &h5hf{hdr: hdr}
		switch idLen {
		case 8:
			assert.EqualValues(t, 7, hdr.tinyMaxLen)
			assert.False(t, hdr.tinyLenExtended)
		case 18:
			assert.EqualValues(t, h5hfTinyLenShort, hdr.tinyMaxLen)
			assert.False(t, hdr.tinyLenExtended)
		case 19, 40:
			assert.EqualValues(t, idLen-2, hdr.tinyMaxLen)
			assert.True(t, hdr.tinyLenExtended)
		}
		for n := 1; n <= int(hdr.tinyMaxLen); n++ {
			obj := make([]byte, n)
			for i := range obj {
				obj[i] = byte('a' + i)
			}
			// H5HF__tiny_insert
			id := make([]byte, idLen)
			if !hdr.tinyLenExtended {
				id[0] = h5hfIDVersCurr | h5hfIDTypeTiny | byte((n-1)&h5hfTinyMaskShort)
				copy(id[1:], obj)
			} else {
				id[0] = h5hfIDVersCurr | h5hfIDTypeTiny | byte(((n-1)&h5hfTinyMaskExt1)>>8)
				id[1] = byte((n - 1) & h5hfTinyMaskExt2)
				copy(id[2:], obj)
			}
			got, err := fh.read(id)
			require.NoError(t, err)
			assert.Equal(t, obj, got)
			l, err := fh.objLen(id)
			require.NoError(t, err)
			assert.EqualValues(t, n, l)
			off, err := fh.objOff(id)
			require.NoError(t, err)
			assert.Zero(t, off)
		}
		// A length that overruns the ID is an error, not a panic.
		id := make([]byte, idLen)
		id[0] = h5hfIDTypeTiny | 0x0f
		if hdr.tinyLenExtended {
			id[1] = 0xff
		}
		if int(idLen) < 17 || hdr.tinyLenExtended {
			_, err := fh.read(id)
			assert.Error(t, err)
		}
		_, err := fh.read(id[:1])
		assert.Error(t, err)
	}
}

func TestFractalHeapHugeDirect(t *testing.T) {
	// A heap whose IDs are long enough to hold 'huge' objects' addresses
	// and lengths directly (no public API creates one): point an ID at the
	// file signature of a corpus file.
	f, err := Open(filepath.Join(repoRoot(t), newgratFile), WithExternalLinks(false))
	require.NoError(t, err)
	defer f.Close()
	hdr := &h5hfHdr{f: f, idLen: 17, sizeofAddr: f.sizeofAddr, sizeofSize: f.sizeofSize}
	h5hfHugeInit(hdr)
	require.True(t, hdr.hugeIDsDirect)
	assert.EqualValues(t, 16, hdr.hugeIDSize)
	fh := &h5hf{f: f, hdr: hdr}
	id := []byte{h5hfIDTypeHuge}
	id = binary.LittleEndian.AppendUint64(id, 0)
	id = binary.LittleEndian.AppendUint64(id, 8)
	obj, err := fh.read(id)
	require.NoError(t, err)
	assert.Equal(t, h5fSignature, obj)
	n, err := fh.objLen(id)
	require.NoError(t, err)
	assert.EqualValues(t, 8, n)
	off, err := fh.objOff(id)
	require.NoError(t, err)
	assert.Zero(t, off)

	// Past the end of the file / truncated ID.
	binary.LittleEndian.PutUint64(id[9:], 1<<40)
	_, err = fh.read(id)
	assert.Error(t, err)
	_, err = fh.read(id[:5])
	assert.Error(t, err)

	// IDs too short for direct access use the v2 B-tree.
	hdr2 := &h5hfHdr{f: f, idLen: 8, sizeofAddr: f.sizeofAddr, sizeofSize: f.sizeofSize, hugeBt2Addr: haddrUndef}
	h5hfHugeInit(hdr2)
	assert.False(t, hdr2.hugeIDsDirect)
	assert.EqualValues(t, 7, hdr2.hugeIDSize)
	assert.EqualValues(t, uint64(1)<<56-1, hdr2.hugeMaxID)
	_, err = (&h5hf{f: f, hdr: hdr2}).read(hugeID(hdr2, 1))
	assert.Error(t, err)

	// Filtered heaps need room for the filter mask and de-filtered size.
	hdr3 := &h5hfHdr{f: f, idLen: 29, filterLen: 1, sizeofAddr: 8, sizeofSize: 8}
	h5hfHugeInit(hdr3)
	assert.True(t, hdr3.hugeIDsDirect)
	assert.EqualValues(t, 24, hdr3.hugeIDSize)
	hdr3.idLen = 28
	h5hfHugeInit(hdr3)
	assert.False(t, hdr3.hugeIDsDirect)
	assert.EqualValues(t, 8, hdr3.hugeIDSize)
	assert.Equal(t, ^uint64(0), hdr3.hugeMaxID)
}

func TestFractalHeapBadIDs(t *testing.T) {
	_, fh := openHeap(t, newgratFile, 1874)
	hdr := fh.hdr
	for name, id := range map[string][]byte{
		"empty":        {},
		"version":      {0x40, 1, 0, 0, 0, 1, 0},
		"reserved":     {0x30, 1, 0, 0, 0, 1, 0},
		"short":        {0x00, 1},
		"zero offset":  manID(hdr, 0, 10),
		"zero length":  manID(hdr, 100, 0),
		"past end":     manID(hdr, hdr.manSize+1, 10),
		"too long":     manID(hdr, 100, 5000),
		"in prefix":    manID(hdr, 1, 10),
		"past block":   manID(hdr, 500, 100),
		"unallocated":  manID(hdr, 1<<25, 10),
		"huge no tree": hugeID(hdr, 1),
	} {
		_, err := fh.read(id)
		assert.Error(t, err, name)
		_, _ = fh.objLen(id)
	}
}

func TestFractalHeapDtable(t *testing.T) {
	_, fh := openHeap(t, newgratFile, 1874)
	dt := &fh.hdr.manDtable
	assert.Equal(t, []uint64{512, 512, 1024, 2048, 4096}, dt.rowBlockSize[:5])
	assert.Equal(t, []uint64{0, 2048, 4096, 8192, 16384}, dt.rowBlockOff[:5])
	for _, tc := range []struct {
		off      uint64
		row, col uint32
	}{
		{0, 0, 0}, {511, 0, 0}, {512, 0, 1}, {2047, 0, 3}, {2048, 1, 0}, {2560, 1, 1},
		{4096, 2, 0}, {5120, 2, 1}, {1 << 19, 9, 0}, {3 << 19, 10, 2}, {5 << 18, 10, 1},
	} {
		row, col, err := h5hfDtableLookup(dt, tc.off)
		require.NoError(t, err)
		assert.Equal(t, [2]uint32{tc.row, tc.col}, [2]uint32{row, col}, "offset %d", tc.off)
	}
	_, _, err := h5hfDtableLookup(dt, 1<<40)
	assert.Error(t, err)
	assert.EqualValues(t, 0, h5hfDtableSizeToRow(dt, 512))
	assert.EqualValues(t, 2, h5hfDtableSizeToRow(dt, 1024))
	assert.EqualValues(t, 8, h5hfDtableSizeToRow(dt, 65536))
	assert.EqualValues(t, 1, h5hfDtableSizeToRows(dt, 2048))
	assert.EqualValues(t, 9, h5hfDtableSizeToRows(dt, 1<<19))
	assert.EqualValues(t, 512*3, h5hfDtableSpanSize(dt, 0, 1, 3))
	assert.EqualValues(t, 512*2+512*4+1024, h5hfDtableSpanSize(dt, 0, 2, 7))
	assert.EqualValues(t, 0, h5hfDtableSpanSize(dt, 0, 0, 0))
	assert.EqualValues(t, 0, h5hfDtableSpanSize(dt, 100, 0, 1))
	assert.EqualValues(t, 2, h5hfLimitEncSize(4096))
	assert.EqualValues(t, 1, h5hfLimitEncSize(255))
	assert.EqualValues(t, 16, h5hfLog2Of2(65536))
	assert.EqualValues(t, 63, h5hfLog2Gen(^uint64(0)))

	for name, d := range map[string]h5hfDtable{
		"zero width":  {width: 0, startBlockSize: 512, maxDirectSize: 65536, maxIndexBits: 32},
		"odd width":   {width: 3, startBlockSize: 512, maxDirectSize: 65536, maxIndexBits: 32},
		"odd start":   {width: 4, startBlockSize: 500, maxDirectSize: 65536, maxIndexBits: 32},
		"zero start":  {width: 4, startBlockSize: 0, maxDirectSize: 65536, maxIndexBits: 32},
		"odd max":     {width: 4, startBlockSize: 512, maxDirectSize: 65535, maxIndexBits: 32},
		"huge max":    {width: 4, startBlockSize: 512, maxDirectSize: 1 << 32, maxIndexBits: 32},
		"start > max": {width: 4, startBlockSize: 1 << 17, maxDirectSize: 65536, maxIndexBits: 32},
		"zero index":  {width: 4, startBlockSize: 512, maxDirectSize: 65536, maxIndexBits: 0},
		"small index": {width: 4, startBlockSize: 512, maxDirectSize: 65536, maxIndexBits: 10},
		"large index": {width: 4, startBlockSize: 512, maxDirectSize: 65536, maxIndexBits: 65},
		"zero max":    {width: 4, startBlockSize: 512, maxDirectSize: 0, maxIndexBits: 32},
	} {
		assert.Error(t, h5hfDtableInit(&d), name)
	}
}

func TestFractalHeapDecodePline(t *testing.T) {
	// Version 1: name padded to a multiple of 8, odd number of client data
	// values padded to an even number.
	v1 := []byte{1, 2, 0, 0, 0, 0, 0, 0}
	v1 = append(v1, 1, 0, 8, 0, 1, 0, 1, 0)
	v1 = append(v1, []byte("deflate\x00")...)
	v1 = append(v1, 6, 0, 0, 0, 0, 0, 0, 0)
	v1 = append(v1, 0x00, 0x01, 0, 0, 0, 0, 2, 0) // filter 256, no name, 2 values
	v1 = append(v1, 1, 0, 0, 0, 2, 0, 0, 0)
	pline, err := h5hfDecodePline(&image{buf: v1})
	require.NoError(t, err)
	assert.EqualValues(t, 1, pline.version)
	require.Len(t, pline.filter, 2)
	assert.Equal(t, h5zFilterInfo{id: 1, flags: 1, name: "deflate", cdValues: []uint32{6}}, pline.filter[0])
	assert.Equal(t, h5zFilterInfo{id: 256, cdValues: []uint32{1, 2}}, pline.filter[1])

	// Version 2: no names for library filters, no padding.
	v2 := []byte{2, 2, 2, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1, 0, 5, 0, 0, 0}
	pline, err = h5hfDecodePline(&image{buf: v2})
	require.NoError(t, err)
	assert.Equal(t, []h5zFilterInfo{{id: 2}, {id: 1, cdValues: []uint32{5}}}, pline.filter)

	for i := range v1 {
		_, err := h5hfDecodePline(&image{buf: v1[:i]})
		assert.Error(t, err, "truncated at %d", i)
	}
	for i := range v2 {
		_, err := h5hfDecodePline(&image{buf: v2[:i]})
		assert.Error(t, err, "truncated at %d", i)
	}
	for name, b := range map[string][]byte{
		"version 0":   {0, 0},
		"version 3":   {3, 0},
		"too many":    {2, 33},
		"odd name":    {1, 1, 0, 0, 0, 0, 0, 0, 1, 0, 3, 0, 0, 0, 0, 0, 'a', 'b', 0},
		"no NUL name": {2, 1, 0, 1, 8, 0, 0, 0, 0, 0, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'},
	} {
		_, err := h5hfDecodePline(&image{buf: b})
		assert.Error(t, err, name)
	}
}

func TestFractalHeapHugeBt2Records(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), newgratFile), WithExternalLinks(false))
	require.NoError(t, err)
	defer f.Close()
	ctx := h5hfHugeBt2CrtContext(f)
	assert.Equal(t, &h5hfHugeBt2Ctx{sizeofAddr: 8, sizeofSize: 8}, ctx)

	raw := func(vals ...any) *image {
		var b []byte
		for _, v := range vals {
			switch v := v.(type) {
			case uint64:
				b = binary.LittleEndian.AppendUint64(b, v)
			case uint32:
				b = binary.LittleEndian.AppendUint32(b, v)
			}
		}
		return f.newImage(b)
	}

	for id, cls := range map[uint8]*h5b2Class{
		h5b2FheapHugeIndirID:     h5hfHugeBt2Indir(),
		h5b2FheapHugeFiltIndirID: h5hfHugeBt2FiltIndir(),
		h5b2FheapHugeDirID:       h5hfHugeBt2Dir(),
		h5b2FheapHugeFiltDirID:   h5hfHugeBt2FiltDir(),
	} {
		assert.Equal(t, id, cls.id)
		assert.Equal(t, id, h5hfHugeBt2Class(id).id)
	}
	assert.Nil(t, h5hfHugeBt2Class(5))

	rec, err := h5hfHugeBt2Indir().decode(f, ctx, raw(uint64(1000), uint64(20), uint64(7)))
	require.NoError(t, err)
	assert.Equal(t, &h5hfHugeBt2IndirRec{addr: 1000, len: 20, id: 7}, rec)
	c, err := h5hfHugeBt2Indir().compare(f, ctx, &h5hfHugeBt2IndirRec{id: 8}, rec)
	require.NoError(t, err)
	assert.Equal(t, 1, c)
	var found h5hfHugeBt2IndirRec
	require.NoError(t, h5hfHugeBt2IndirFound(rec, &found))
	assert.EqualValues(t, 1000, found.addr)

	rec, err = h5hfHugeBt2FiltIndir().decode(f, ctx, raw(uint64(1000), uint64(20), uint32(3), uint64(50), uint64(9)))
	require.NoError(t, err)
	assert.Equal(t, &h5hfHugeBt2FiltIndirRec{addr: 1000, len: 20, filterMask: 3, objSize: 50, id: 9}, rec)
	c, err = h5hfHugeBt2FiltIndir().compare(f, ctx, &h5hfHugeBt2FiltIndirRec{id: 9}, rec)
	require.NoError(t, err)
	assert.Equal(t, 0, c)
	var foundFilt h5hfHugeBt2FiltIndirRec
	require.NoError(t, h5hfHugeBt2FiltIndirFound(rec, &foundFilt))
	assert.EqualValues(t, 50, foundFilt.objSize)

	rec, err = h5hfHugeBt2Dir().decode(f, nil, raw(^uint64(0), uint64(20)))
	require.NoError(t, err)
	assert.Equal(t, &h5hfHugeBt2DirRec{addr: haddrUndef, len: 20}, rec)
	c, err = h5hfHugeBt2Dir().compare(f, ctx, &h5hfHugeBt2DirRec{addr: 10, len: 30}, &h5hfHugeBt2DirRec{addr: 10, len: 20})
	require.NoError(t, err)
	assert.Equal(t, 1, c)
	c, err = h5hfHugeBt2Dir().compare(f, ctx, &h5hfHugeBt2DirRec{addr: 5, len: 30}, &h5hfHugeBt2DirRec{addr: 10, len: 20})
	require.NoError(t, err)
	assert.Equal(t, -1, c)

	rec, err = h5hfHugeBt2FiltDir().decode(f, ctx, raw(uint64(10), uint64(20), uint32(1), uint64(40)))
	require.NoError(t, err)
	assert.Equal(t, &h5hfHugeBt2FiltDirRec{addr: 10, len: 20, filterMask: 1, objSize: 40}, rec)
	c, err = h5hfHugeBt2FiltDir().compare(f, ctx, &h5hfHugeBt2FiltDirRec{addr: 10, len: 20}, rec)
	require.NoError(t, err)
	assert.Equal(t, 0, c)

	// Truncated records and wrong record types are errors.
	_, err = h5hfHugeBt2Indir().decode(f, ctx, raw(uint64(1000), uint64(20)))
	assert.Error(t, err)
	_, err = h5hfHugeBt2FiltDir().decode(f, ctx, raw(uint64(1000)))
	assert.Error(t, err)
	_, err = h5hfHugeBt2Indir().compare(f, ctx, &h5hfHugeBt2DirRec{}, rec)
	assert.Error(t, err)
	assert.Error(t, h5hfHugeBt2IndirFound(rec, &found))
}

// heapIDsAndRegions returns the managed heap IDs of the objects of the heap
// at heapAddr in the file image orig (parsed with parse) and the byte ranges
// of its header, indirect blocks and direct block prefixes.
func heapIDsAndRegions(t *testing.T, orig []byte, heapAddr haddr, parse func(b []byte, f *File) (int, error)) ([][]byte, [][2]int) {
	t.Helper()
	f, err := OpenReader(bytes.NewReader(orig), int64(len(orig)))
	require.NoError(t, err)
	defer f.Close()
	fh, err := f.hfOpen(heapAddr)
	require.NoError(t, err)
	hdr := fh.hdr
	regions := [][2]int{{int(heapAddr), int(heapAddr + hdr.heapSize)}}
	if hdr.manDtable.curRootRows > 0 {
		root := int(hdr.manDtable.tableAddr)
		regions = append(regions, [2]int{root, root + int(h5hfManIndirectSize(hdr, uint32(hdr.manDtable.curRootRows)))})
	}
	var ids [][]byte
	overhead := h5hfManAbsDirectOverhead(hdr)
	require.NoError(t, hfWalkDblocks(fh, func(db *h5hfDirect) error {
		if len(regions) < 4 {
			addr := 0
			if hdr.manDtable.curRootRows == 0 {
				addr = int(hdr.manDtable.tableAddr)
			} else {
				ib, err := h5hfManIblockProtect(hdr, hdr.manDtable.tableAddr, uint32(hdr.manDtable.curRootRows), nil, 0)
				require.NoError(t, err)
				addr = int(ib.ents[db.parEntry])
			}
			regions = append(regions, [2]int{addr, addr + int(overhead) + 32})
		}
		for pos := overhead; pos < db.size && db.blk[pos] != 0; {
			n, err := parse(db.blk[pos:], f)
			if err != nil {
				return err
			}
			ids = append(ids, manID(hdr, db.blockOff+pos, uint64(n)))
			pos += uint64(n)
		}
		return nil
	}))
	return ids, regions
}

// mutateHeap sets each byte of regions of the file image orig to a few
// values and checks that opening the heap at heapAddr and reading ids fails
// cleanly or succeeds, never panics.
func mutateHeap(t *testing.T, orig []byte, heapAddr haddr, ids [][]byte, regions [][2]int) {
	t.Helper()
	buf := make([]byte, len(orig))
	try := func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()
		f, err := OpenReader(bytes.NewReader(buf), int64(len(buf)), WithChecksumVerification(false))
		if err != nil {
			return
		}
		defer f.Close()
		fh, err := f.hfOpen(heapAddr)
		if err != nil {
			return
		}
		for _, id := range ids {
			_, _ = fh.read(id)
			_, _ = fh.objLen(id)
		}
		_ = hfWalkDblocks(fh, func(*h5hfDirect) error { return nil })
	}
	for _, r := range regions {
		for i := r[0]; i < r[1] && i < len(orig); i++ {
			for _, v := range []byte{0x00, 0xff, orig[i] ^ 0x01, orig[i] ^ 0x80, 0x40} {
				copy(buf, orig)
				buf[i] = v
				try()
			}
		}
	}
}

// TestFractalHeapCorrupt mutates every byte of a small file's heap header and
// direct block and checks that opening the heap and reading its objects
// fails cleanly or succeeds, never panics.
func TestFractalHeapCorrupt(t *testing.T) {
	path := filepath.Join(repoRoot(t), "tools/test/h5dump/testfiles/tattr4_be.h5")
	orig, err := os.ReadFile(path)
	require.NoError(t, err)

	ids, regions := heapIDsAndRegions(t, orig, 754, func(b []byte, f *File) (int, error) {
		a, err := parseAttr(b, int(f.sizeofSize))
		return a.size, err
	})
	require.Len(t, ids, 9)
	mutateHeap(t, orig, 754, ids, regions)

	// A heap with a root indirect block (links of /dense in fheap_huge.h5).
	hugeOrig, err := os.ReadFile("testdata/fheap_huge.h5")
	require.NoError(t, err)
	hugeIDs, hugeRegions := heapIDsAndRegions(t, hugeOrig, 258, func(b []byte, f *File) (int, error) {
		l, err := parseLink(b, int(f.sizeofAddr))
		return l.size, err
	})
	require.Len(t, hugeIDs, 200)
	mutateHeap(t, hugeOrig, 258, hugeIDs[:20], hugeRegions)

	f, err := OpenReader(bytes.NewReader(orig), int64(len(orig)))
	require.NoError(t, err)
	fh, err := f.hfOpen(754)
	require.NoError(t, err)
	dblockAddr := int(fh.hdr.manDtable.tableAddr)
	dblockSize := int(fh.hdr.manDtable.startBlockSize)
	f.Close()
	buf := make([]byte, len(orig))

	// With checksums verified, a flipped bit in the header or block is
	// reported as a checksum error.
	copy(buf, orig)
	buf[dblockAddr+dblockSize-1] ^= 0x01
	f, err = OpenReader(bytes.NewReader(buf), int64(len(buf)))
	require.NoError(t, err)
	defer f.Close()
	fh, err = f.hfOpen(754)
	require.NoError(t, err)
	_, err = fh.read(ids[0])
	assert.ErrorIs(t, err, ErrBadChecksum)
	copy(buf, orig)
	buf[754+20] ^= 0x01
	f2, err := OpenReader(bytes.NewReader(buf), int64(len(buf)))
	require.NoError(t, err)
	defer f2.Close()
	_, err = f2.hfOpen(754)
	assert.ErrorIs(t, err, ErrBadChecksum)
}
