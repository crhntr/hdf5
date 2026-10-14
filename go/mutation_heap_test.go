package hdf5

// Tests for the heap code (h5hg_cache.go, h5hg.go, h5hl_cache.go, h5hl.go,
// h5hf_*.go) and the decoding helpers (h5checksum.go, h5decode.go)
// written from the mutants gremlins left alive. Most build files with
// h5Builder (mutation_h5o_test.go) holding heaps assembled byte by byte;
// two were written by libhdf5 (testdata/gen/gen_heap_sizes.c). The
// expectations are what libhdf5 1.14.6 reads from the same bytes, through
// the datasets and groups that refer to the heaps or, for heaps nothing
// refers to, through the library's own heap routines (H5HG_read,
// H5HL_protect / H5HL_offset_into, H5HF_open / H5HF_get_obj_len /
// H5HF_read, called from a program compiled against the private headers
// of ../src); set HDF5_MUTATION_DUMP to a directory to have the tests
// write the files there (the heap addresses and IDs are logged).

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dumpLogf logs when the test files are dumped (HDF5_MUTATION_DUMP).
func dumpLogf(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("HDF5_MUTATION_DUMP") != "" {
		t.Logf(format, args...)
	}
}

/* ------------------------------------------------------------------------
 * Global heap
 */

// gheapRef is a variable-length string element referring to object idx
// of a global heap collection, n characters long.
type gheapRef struct{ n, idx uint32 }

// gheapDataset opens the dataset "/obj" of variable-length strings whose
// elements refer to the global heap collection coll.
func gheapDataset(t *testing.T, coll []byte, refs ...gheapRef) *Dataset {
	t.Helper()
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		heap := b.put(coll)
		dumpLogf(t, "collection at %d", heap)
		var data []byte
		for _, r := range refs {
			data = binary.LittleEndian.AppendUint32(le64a(binary.LittleEndian.AppendUint32(data, r.n), heap), r.idx)
		}
		addr := b.put(data)
		l := le64a(le64a([]byte{3, 1}, addr), uint64(len(data)))
		return b.put(ohdr2(2, v2Msgs(spaceMsg(uint64(len(refs))), ohMsg{typ: h5oDtypeID, data: vlenStrType(16)}, layoutMsg(l))))
	})
	ds, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	return ds
}

// TestMutationGlobalHeapCollections: global heap collections (4096
// bytes, 8-byte lengths) read through variable-length strings
// (h5hg_cache.go:210, 236, 238, 239, 246, 248, 296; h5hg.go:53), checked
// with h5dump and H5HG_read:
//
//   - index_257: objects 1 and 257, past the 257 slots H5HG_NOBJS
//     allocates, read;
//   - index_1000: objects 1 and 1000, more than twice the slots, read;
//   - unused_slot: objects 1 and 1000, and object 600 (a slot added for
//     index 1000) refused ("bad heap pointer");
//   - next_unused: objects 1 and 256 (the last of the 257 slots), and
//     object 257 refused ("bad heap index");
//   - free_space_16: object 1 followed by a free space object of 16
//     bytes, its header only, read;
//   - empty_last_object: object 1 followed by an empty object 2 in the
//     last 16 bytes (with no free space), both read.
func TestMutationGlobalHeapCollections(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 4096-16-16-16)
	for _, c := range []struct {
		name string
		objs [][2]any
		refs []gheapRef
		want []any /* nil: libhdf5 cannot read the dataset */
	}{
		{"index_257", [][2]any{{1, []byte("a")}, {257, []byte("big index")}}, []gheapRef{{1, 1}, {9, 257}}, []any{"a", "big index"}},
		{"index_1000", [][2]any{{1, []byte("a")}, {1000, []byte("bigger")}}, []gheapRef{{1, 1}, {6, 1000}}, []any{"a", "bigger"}},
		{"unused_slot", [][2]any{{1, []byte("a")}, {1000, []byte("bigger")}}, []gheapRef{{1, 1}, {0, 600}}, nil},
		{"next_unused", [][2]any{{1, []byte("a")}, {256, []byte("last")}}, []gheapRef{{4, 256}, {0, 257}}, nil},
		{"free_space_16", [][2]any{{1, big}}, []gheapRef{{uint32(len(big)), 1}}, []any{string(big)}},
		{"empty_last_object", [][2]any{{1, big}, {2, []byte{}}}, []gheapRef{{0, 2}, {uint32(len(big)), 1}}, []any{"", string(big)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			coll := gcolImage(4096, c.objs)
			if c.name == "free_space_16" {
				require.Equal(t, le64a(make([]byte, 8), 16), coll[4096-16:])
			}
			ds := gheapDataset(t, coll, c.refs...)
			got, err := ds.ReadValues()
			if c.want == nil {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestMutationGlobalHeapSize2: a collection in a file with 2-byte lengths
// (testdata/heap_size2.h5), whose header (4+1+3+2 bytes) is aligned to 16
// bytes (h5hg_cache.go:36). h5dump reads "alpha", "be", "gamma-delta".
func TestMutationGlobalHeapSize2(t *testing.T) {
	f, err := Open("testdata/heap_size2.h5")
	require.NoError(t, err)
	defer f.Close()
	ds, err := f.OpenDataset("/vs")
	require.NoError(t, err)
	got, err := ds.ReadValues()
	require.NoError(t, err)
	assert.Equal(t, []any{"alpha", "be", "gamma-delta"}, got)
}

/* ------------------------------------------------------------------------
 * Local heap
 */

// lheapPrefix is a local heap prefix (8-byte lengths and addresses, 32
// bytes).
func lheapPrefix(dblkSize, freeHead, dblkAddr uint64) []byte {
	return le64a(le64a(le64a(append([]byte("HEAP"), 0, 0, 0, 0), dblkSize), freeHead), dblkAddr)
}

// lheapFile builds a file holding a local heap whose data block, data,
// follows the prefix, and returns it with the heap address.
func lheapFile(t *testing.T, data []byte, freeHead uint64) (*File, uint64) {
	t.Helper()
	b := newH5Builder()
	heap := b.alloc(32 + len(data))
	b.set(heap, append(lheapPrefix(uint64(len(data)), freeHead, heap+32), data...))
	dumpLogf(t, "local heap at %d", heap)
	return builtFile(t, b.finish(b.rootGroup())), heap
}

// TestMutationLocalHeapAddr2: old-style groups in a file with 2-byte
// addresses (testdata/heap_addr2.h5), whose local heap prefix (4+1+3+4+
// 4+2 bytes) is aligned to 24 bytes (h5hl_cache.go:37). h5dump lists /g1,
// /g1/d (5) and /g1/sub.
func TestMutationLocalHeapAddr2(t *testing.T) {
	f, err := Open("testdata/heap_addr2.h5")
	require.NoError(t, err)
	defer f.Close()
	var paths []string
	for v, err := range f.Root().Walk() {
		require.NoError(t, err)
		paths = append(paths, v.Path)
	}
	assert.Equal(t, []string{"/g1", "/g1/d", "/g1/sub"}, paths)
	ds, err := f.OpenDataset("/g1/d")
	require.NoError(t, err)
	got, err := Read[int32](ds)
	require.NoError(t, err)
	assert.Equal(t, []int32{5}, got)
}

// lheapNode stores a local heap free list node (next free block offset,
// size) at off in data.
func lheapNode(data []byte, off, next, size uint64) {
	binary.LittleEndian.PutUint64(data[off:], next)
	binary.LittleEndian.PutUint64(data[off+8:], size)
}

// TestMutationLocalHeaps: local heaps read through H5HL_protect and
// H5HL_offset_into (h5hl_cache.go:95, 123, 129, 145; h5hl.go:64, 75,
// 77):
//
//   - empty: a data block of 0 bytes, without free list, at an address
//     past the end of the file;
//   - overlapping_free_list: a free list of five blocks of a 64-byte data
//     block, each overlapping the next by 8 bytes, the last one empty
//     (more blocks than 16-byte nodes fit in the data block);
//   - contiguous_1024: a 1024-byte data block after the prefix (read with
//     the prefix in a second read: more than the 512 bytes of the
//     speculative read);
//
// which libhdf5 loads, and
//
//   - free_list_wraps: a free list whose second block is at offset
//     2^64-8: the sanity check free_block + 2 * sizeof_size > dblk_size
//     wraps around and libhdf5 decodes the 16 bytes before the data block
//     (here the end of the prefix, which it rejects: "bad heap free
//     list"); hdf5 fails;
//   - free_list_cycle: a free list whose second block points back to the
//     first, which libhdf5 walks without end; hdf5 fails.
func TestMutationLocalHeaps(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		b := newH5Builder()
		heap := b.put(lheapPrefix(0, h5hlFreeNull, 1<<40))
		dumpLogf(t, "local heap at %d", heap)
		f := builtFile(t, b.finish(b.rootGroup()))
		h, err := f.hlProtect(heap)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), h.heapGetSize())
		_, err = h.stringAt(0)
		assert.Error(t, err)
	})
	t.Run("overlapping_free_list", func(t *testing.T) {
		data := make([]byte, 64)
		copy(data, "abcdefg")
		for _, n := range [][3]uint64{{40, 32, 0}, {32, 24, 32}, {24, 16, 24}, {16, 8, 16}, {8, h5hlFreeNull, 8}} {
			lheapNode(data, n[0], n[1], n[2])
		}
		f, heap := lheapFile(t, data, 40)
		h, err := f.hlProtect(heap)
		require.NoError(t, err)
		assert.Equal(t, []h5hlFree{{40, 0}, {32, 32}, {24, 24}, {16, 16}, {8, 8}}, h.freelist)
		s, err := h.stringAt(0)
		require.NoError(t, err)
		assert.Equal(t, "abcdefg", s)
	})
	t.Run("contiguous_1024", func(t *testing.T) {
		data := make([]byte, 1024)
		copy(data[600:], "far")
		f, heap := lheapFile(t, data, h5hlFreeNull)
		h, err := f.hlProtect(heap)
		require.NoError(t, err)
		s, err := h.stringAt(600)
		require.NoError(t, err)
		assert.Equal(t, "far", s)
	})
	t.Run("free_list_wraps", func(t *testing.T) {
		data := make([]byte, 64)
		lheapNode(data, 8, 1<<64-8, 8)
		f, heap := lheapFile(t, data, 8)
		_, err := f.hlProtect(heap)
		assert.Error(t, err)
	})
	t.Run("free_list_cycle", func(t *testing.T) {
		data := make([]byte, 64)
		lheapNode(data, 8, 24, 16)
		lheapNode(data, 24, 8, 16)
		f, heap := lheapFile(t, data, 8)
		_, err := f.hlProtect(heap)
		assert.Error(t, err)
	})
}

/* ------------------------------------------------------------------------
 * Fractal heap
 */

// fhGeom describes a fractal heap assembled by the tests (8-byte
// addresses and lengths): the header fields that matter here.
type fhGeom struct {
	idLen         uint16
	flags         uint8 /* h5hfHdrFlags* */
	maxMan        uint32
	width         uint16
	start         uint64
	maxDirect     uint64
	maxIndex      uint16
	manSize       uint64
	hugeBt2       uint64 /* 0: undefined */
	pline         []byte /* encoded I/O filter pipeline; nil: none */
	rootDirSize   uint64 /* filtered root direct block: size on disk */
	rootDirMask   uint32 /* filtered root direct block: filter mask */
	startRootRows uint16
}

// fhDefault is the geometry of the heaps of dense groups and attributes,
// with 8-byte heap IDs and checksummed direct blocks.
func fhDefault() fhGeom {
	return fhGeom{idLen: 8, flags: h5hfHdrFlagsChecksumDblocks, maxMan: 4096, width: 4, start: 512,
		maxDirect: 65536, maxIndex: 32, manSize: 512, startRootRows: 1}
}

func log2u(n uint64) int { return int(h5hfLog2Gen(n)) }

// offSize, lenSize: the sizes of heap offsets and object lengths in
// managed heap IDs (H5HF__hdr_finish_init_phase1).
func (g *fhGeom) offSize() int { return (int(g.maxIndex) + 7) / 8 }
func (g *fhGeom) lenSize() int {
	return min((log2u(g.maxDirect)+7)/8, limitEncSize(uint64(g.maxMan)))
}

// maxDirectRows is the number of direct block rows of an indirect block.
func (g *fhGeom) maxDirectRows() int { return log2u(g.maxDirect) - log2u(g.start) + 2 }

// dblkPrefix is the size of a direct block's prefix.
func (g *fhGeom) dblkPrefix() int {
	n := 5 + 8 + g.offSize()
	if g.flags&h5hfHdrFlagsChecksumDblocks != 0 {
		n += 4
	}
	return n
}

// header encodes the heap header for a root block at root with rows
// rows (0: a root direct block).
func (g *fhGeom) header(root uint64, rows uint16) []byte {
	b := append([]byte("FRHP"), 0)
	b = binary.LittleEndian.AppendUint16(b, g.idLen)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(g.pline)))
	b = append(b, g.flags)
	b = binary.LittleEndian.AppendUint32(b, g.maxMan)
	hugeBt2 := uint64(haddrUndef)
	if g.hugeBt2 != 0 {
		hugeBt2 = g.hugeBt2
	}
	b = le64a(le64a(b, 1), hugeBt2)     /* next huge ID, huge object B-tree */
	b = le64a(le64a(b, 0), haddrUndef)  /* free space, free space manager */
	b = le64a(b, g.manSize)             /* managed space */
	b = le64a(le64a(b, g.manSize), 0)   /* allocated managed space, iterator offset */
	b = append(b, make([]byte, 5*8)...) /* object counts and sizes */
	b = binary.LittleEndian.AppendUint16(b, g.width)
	b = le64a(le64a(b, g.start), g.maxDirect)
	b = binary.LittleEndian.AppendUint16(b, g.maxIndex)
	b = binary.LittleEndian.AppendUint16(b, g.startRootRows)
	b = binary.LittleEndian.AppendUint16(le64a(b, root), rows)
	if g.pline != nil {
		b = binary.LittleEndian.AppendUint32(le64a(b, g.rootDirSize), g.rootDirMask)
		b = append(b, g.pline...)
	}
	return binary.LittleEndian.AppendUint32(b, checksumLookup3(b, 0))
}

// dblock encodes a direct block of size bytes at heap offset off whose
// objects are objs (in-block offset -> bytes).
func (g *fhGeom) dblock(heap, off uint64, size int, objs map[int][]byte) []byte {
	b := append([]byte("FHDB"), 0)
	b = append(le64a(b, heap), le64a(nil, off)[:g.offSize()]...)
	sum := len(b)
	if g.flags&h5hfHdrFlagsChecksumDblocks != 0 {
		b = append(b, 0, 0, 0, 0)
	}
	if len(b) < size {
		b = append(b, make([]byte, size-len(b))...)
	}
	for at, o := range objs {
		copy(b[at:], o)
	}
	if g.flags&h5hfHdrFlagsChecksumDblocks != 0 && sum+4 <= len(b) {
		binary.LittleEndian.PutUint32(b[sum:], checksumLookup3(b, 0))
	}
	return b
}

// fhEnt is an indirect block entry: a child block address and, for the
// direct block entries of a filtered heap, its size on disk and filter
// mask.
type fhEnt struct {
	addr, size uint64
	mask       uint32
}

// iblock encodes an indirect block at heap offset off with rows rows and
// the entries ents (the others undefined).
func (g *fhGeom) iblock(heap, off uint64, rows int, ents map[int]fhEnt) []byte {
	b := append([]byte("FHIB"), 0)
	b = append(le64a(b, heap), le64a(nil, off)[:g.offSize()]...)
	for i := 0; i < rows*int(g.width); i++ {
		e, ok := ents[i]
		if !ok {
			e = fhEnt{addr: haddrUndef}
		}
		b = le64a(b, e.addr)
		if g.pline != nil && i < g.maxDirectRows()*int(g.width) {
			b = binary.LittleEndian.AppendUint32(le64a(b, e.size), e.mask)
		}
	}
	return binary.LittleEndian.AppendUint32(b, checksumLookup3(b, 0))
}

// manID is the managed heap ID (H5HF_MAN_ID_ENCODE) of the object of n
// bytes at heap offset off, padded to the heap ID length.
func (g *fhGeom) manID(off, n uint64) []byte {
	id := append([]byte{0}, le64a(nil, off)[:g.offSize()]...)
	id = append(id, le64a(nil, n)[:g.lenSize()]...)
	if len(id) < int(g.idLen) {
		id = append(id, make([]byte, int(g.idLen)-len(id))...)
	}
	return id
}

// fhFile builds a file holding a fractal heap (which nothing refers to)
// whose blocks build puts, returning the root block address and rows, and
// opens the heap.
func fhFile(t *testing.T, g *fhGeom, build func(b *h5Builder, heap uint64) (root uint64, rows uint16)) (*File, *h5hf, error) {
	t.Helper()
	b := newH5Builder()
	heap := b.alloc(len(g.header(0, 0)))
	root, rows := build(b, heap)
	b.set(heap, g.header(root, rows))
	dumpLogf(t, "fractal heap at %d", heap)
	f := builtFile(t, b.finish(b.rootGroup()))
	fh, err := f.hfOpen(heap)
	return f, fh, err
}

// fhRootDirect builds a file holding a heap whose root direct block (of
// the starting block size) holds objs, and opens the heap.
func fhRootDirect(t *testing.T, g fhGeom, objs map[int][]byte) *h5hf {
	t.Helper()
	_, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
		return b.put(g.dblock(heap, 0, int(g.start), objs)), 0
	})
	require.NoError(t, err)
	return fh
}

// fhRead reads the object id and logs the ID (for checking with
// H5HF_read).
func fhRead(t *testing.T, fh *h5hf, id []byte) ([]byte, error) {
	t.Helper()
	dumpLogf(t, "heap ID %s", hex.EncodeToString(id))
	return fh.read(id)
}

// TestMutationFractalHeapGeometry: heaps with creation parameters at the
// limits H5HF__hdr_create accepts, and objects at the limits
// H5HF__man_op_real accepts (h5hf_dtable.go:30, 36, 42, 49;
// h5hf_cache.go:218; h5hf_man.go:54, 63, 114; h5hf_iblock.go:27;
// h5hf_dblock.go:177). H5HF_read reads every object:
//
//   - max_direct_2GiB: direct blocks of up to 2 GiB
//     (H5HF_MAX_DIRECT_SIZE_LIMIT);
//   - start_is_max_direct: a starting block size equal to the maximum
//     direct block size;
//   - max_index_64: a 64-bit heap address space (8-byte offsets);
//   - max_index_first_row: an address space as large as the first row of
//     the doubling table (a single row);
//   - id_len_4097: heap IDs of H5HF_MAX_ID_LEN bytes;
//   - offset_is_man_size: an object at the heap offset that is the
//     managed space size of the header;
//   - length_is_max_man: an object as large as the largest managed object;
//   - object_ends_block: an object ending at the end of its direct block;
//   - root_rows_max: a root indirect block with all the rows of the
//     doubling table;
//   - child_block_offset: a child indirect block whose stored heap offset
//     is that of the object (40 bytes into its first direct block): the
//     lookup of the object's offset relative to it lands on that block.
func TestMutationFractalHeapGeometry(t *testing.T) {
	obj := []byte("a fractal heap object")
	n := uint64(len(obj))
	rootDirect := func(name string, g fhGeom, at int) {
		t.Run(name, func(t *testing.T) {
			fh := fhRootDirect(t, g, map[int][]byte{at: obj})
			got, err := fhRead(t, fh, g.manID(uint64(at), n))
			require.NoError(t, err)
			assert.Equal(t, obj, got)
		})
	}
	g := fhDefault()
	g.maxDirect = h5hfMaxDirectSizeLimit
	rootDirect("max_direct_2GiB", g, g.dblkPrefix())
	g = fhDefault()
	g.maxDirect, g.maxMan = 512, 256
	rootDirect("start_is_max_direct", g, g.dblkPrefix())
	g = fhDefault()
	g.maxIndex, g.idLen = 64, 11
	rootDirect("max_index_64", g, g.dblkPrefix())
	g = fhDefault()
	g.maxIndex = 11 /* log2(512 * 4) */
	rootDirect("max_index_first_row", g, g.dblkPrefix())
	g = fhDefault()
	g.idLen = h5hfMaxIDLen
	rootDirect("id_len_4097", g, g.dblkPrefix())
	g = fhDefault()
	g.manSize = 100
	rootDirect("offset_is_man_size", g, 100)
	g = fhDefault()
	g.maxMan = uint32(n)
	rootDirect("length_is_max_man", g, g.dblkPrefix())
	g = fhDefault()
	rootDirect("object_ends_block", g, 512-len(obj))

	t.Run("root_rows_max", func(t *testing.T) {
		g := fhDefault()
		g.maxIndex, g.manSize = 13, 8192 /* rows of 4 blocks of 512, 512, 1024 bytes */
		_, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
			d := b.put(g.dblock(heap, 0, 512, map[int][]byte{g.dblkPrefix(): obj}))
			return b.put(g.iblock(heap, 0, 3, map[int]fhEnt{0: {addr: d}})), 3
		})
		require.NoError(t, err)
		got, err := fhRead(t, fh, g.manID(uint64(g.dblkPrefix()), n))
		require.NoError(t, err)
		assert.Equal(t, obj, got)
	})
	t.Run("child_block_offset", func(t *testing.T) {
		/* Width 2, blocks of 512 bytes: the root indirect block has two
		 * direct rows (heap offsets 0 and 1024) and an indirect row
		 * (offset 2048: two child indirect blocks of one row). */
		g := fhDefault()
		g.width, g.maxDirect, g.maxMan, g.maxIndex, g.manSize = 2, 512, 256, 16, 4096
		const off = 2048 + 40
		_, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
			d := b.put(g.dblock(heap, 2048, 512, map[int][]byte{40: obj}))
			child := b.put(g.iblock(heap, off, 1, map[int]fhEnt{0: {addr: d}}))
			return b.put(g.iblock(heap, 0, 3, map[int]fhEnt{4: {addr: child}})), 3
		})
		require.NoError(t, err)
		got, err := fhRead(t, fh, g.manID(off, n))
		require.NoError(t, err)
		assert.Equal(t, obj, got)
	})
}

// TestMutationFractalHeapCorrupt: managed objects that libhdf5 looks for
// outside of the tables it allocated (h5hf_dblock.go:191;
// h5hf_dtable.go:89; h5hf_cache.go:446); hdf5 fails:
//
//   - offset_past_root_rows: the heap offset 2048 (the managed space size)
//     in a heap whose root indirect block has one row of four 512-byte
//     blocks: H5HF__man_dblock_locate returns entry 4 of an indirect block
//     with four entries, whose address libhdf5 reads past the entry table
//     (H5HF_read fails, and the program then dies of a floating point
//     exception);
//   - offset_past_table: the heap offset 2^30 in a heap with a 30-bit
//     address space: H5HF__dtable_lookup computes row 20 of a doubling
//     table of 20 rows, and libhdf5 reads its block size past the row
//     table (and crashes);
//   - block_smaller_than_prefix: a root direct block of 16 bytes (the
//     starting block size) in a heap whose direct blocks have a 21-byte
//     prefix ending with a checksum, which libhdf5 reads past the block
//     (and finds wrong).
func TestMutationFractalHeapCorrupt(t *testing.T) {
	obj := []byte("a fractal heap object")
	n := uint64(len(obj))
	rootIblock := func(t *testing.T, g fhGeom) (*h5hf, error) {
		_, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
			d := b.put(g.dblock(heap, 0, 512, map[int][]byte{g.dblkPrefix(): obj}))
			return b.put(g.iblock(heap, 0, 1, map[int]fhEnt{0: {addr: d}})), 1
		})
		return fh, err
	}
	t.Run("offset_past_root_rows", func(t *testing.T) {
		g := fhDefault()
		g.manSize = 2048
		fh, err := rootIblock(t, g)
		require.NoError(t, err)
		got, err := fhRead(t, fh, g.manID(uint64(g.dblkPrefix()), n))
		require.NoError(t, err)
		assert.Equal(t, obj, got)
		_, err = fhRead(t, fh, g.manID(2048, n))
		assert.Error(t, err)
	})
	t.Run("offset_past_table", func(t *testing.T) {
		g := fhDefault()
		g.maxIndex, g.manSize = 30, 1<<30
		fh, err := rootIblock(t, g)
		require.NoError(t, err)
		_, err = fhRead(t, fh, g.manID(1<<30, n))
		assert.Error(t, err)
	})
	t.Run("block_smaller_than_prefix", func(t *testing.T) {
		g := fhDefault()
		g.start, g.maxDirect, g.maxMan, g.manSize = 16, 16, 8, 16
		fh := fhRootDirect(t, g, nil)
		_, err := fhRead(t, fh, g.manID(1, 1))
		assert.Error(t, err)
	})
}

// deflatePline is a version 2 filter pipeline message holding the
// deflate filter (level 6).
var deflatePline = []byte{2, 1, 1, 0, 0, 0, 1, 0, 6, 0, 0, 0}

// zlibBytes compresses b with zlib.
func zlibBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, err := w.Write(b)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// TestMutationFractalHeapFiltered: heaps with I/O filters
// (h5hf_dblock.go:36; h5hf_cache.go:353, 563, 619), whose objects
// H5HF_read reads:
//
//   - root_direct: a deflated root direct block;
//   - indirect_rows: a root indirect block with three direct rows (with
//     the filtered block sizes and masks) and an indirect row (addresses
//     only), and a deflated direct block in its first entry;
//   - filters_32: a pipeline of 32 deflate filters (H5Z_MAX_NFILTERS),
//     all skipped by the root direct block's filter mask;
//   - empty_filter_name: a version 1 pipeline whose deflate filter has
//     an 8-byte name field holding the empty name.
func TestMutationFractalHeapFiltered(t *testing.T) {
	obj := []byte("a fractal heap object")
	n := uint64(len(obj))
	deflatedRoot := func(name string, g fhGeom) {
		t.Run(name, func(t *testing.T) {
			_, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
				z := zlibBytes(t, g.dblock(heap, 0, 512, map[int][]byte{g.dblkPrefix(): obj}))
				g.rootDirSize = uint64(len(z))
				return b.put(z), 0
			})
			require.NoError(t, err)
			got, err := fhRead(t, fh, g.manID(uint64(g.dblkPrefix()), n))
			require.NoError(t, err)
			assert.Equal(t, obj, got)
		})
	}
	g := fhDefault()
	g.pline = deflatePline
	deflatedRoot("root_direct", g)

	t.Run("indirect_rows", func(t *testing.T) {
		g := fhDefault()
		g.pline = deflatePline
		g.width, g.maxDirect, g.maxMan, g.maxIndex, g.manSize = 2, 1024, 512, 16, 4096
		require.Equal(t, 3, g.maxDirectRows())
		_, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
			z := zlibBytes(t, g.dblock(heap, 0, 512, map[int][]byte{g.dblkPrefix(): obj}))
			d := b.put(z)
			return b.put(g.iblock(heap, 0, 4, map[int]fhEnt{0: {addr: d, size: uint64(len(z))}})), 4
		})
		require.NoError(t, err)
		got, err := fhRead(t, fh, g.manID(uint64(g.dblkPrefix()), n))
		require.NoError(t, err)
		assert.Equal(t, obj, got)
	})
	t.Run("filters_32", func(t *testing.T) {
		g := fhDefault()
		g.pline = []byte{2, h5hfZMaxNfilters}
		for range h5hfZMaxNfilters {
			g.pline = append(g.pline, deflatePline[2:]...)
		}
		g.rootDirSize, g.rootDirMask = 512, 0xffffffff
		fh := fhRootDirect(t, g, map[int][]byte{g.dblkPrefix(): obj})
		got, err := fhRead(t, fh, g.manID(uint64(g.dblkPrefix()), n))
		require.NoError(t, err)
		assert.Equal(t, obj, got)
	})
	g = fhDefault()
	g.pline = []byte{1, 1, 0, 0, 0, 0, 0, 0, /* version 1, 1 filter */
		1, 0, 8, 0, 0, 0, 1, 0, /* deflate, name length 8, flags 0, 1 parameter */
		0, 0, 0, 0, 0, 0, 0, 0, /* name: "" */
		6, 0, 0, 0, 0, 0, 0, 0} /* level 6, padding */
	deflatedRoot("empty_filter_name", g)
}

// TestMutationFractalHeapTiny: 'tiny' objects (stored in the heap ID)
// (h5hf_tiny.go:26, 45, 50). H5HF_get_obj_len and H5HF_read return:
//
//   - id_len_17: in a heap with 17-byte IDs, the 16-byte object of an ID
//     whose length (15) is in the first byte (16 bytes of ID left: no
//     extra length byte);
//   - one_byte_id: the length (1) of the object of a 1-byte ID, which
//     H5HF__tiny_get_obj_len decodes from the first byte;
//   - extended_id: in a heap with 19-byte IDs (an extra length byte),
//     the length decoded from the second byte only: libhdf5 masks the
//     first byte with 0x0F00 before shifting it, so the length 0x103 of an
//     ID whose first byte is 0x21 decodes as 3 + 1 (and H5HF_read returns
//     the first four bytes of the ID's data).
func TestMutationFractalHeapTiny(t *testing.T) {
	data := []byte("0123456789abcdef")
	t.Run("id_len_17", func(t *testing.T) {
		g := fhDefault()
		g.idLen = 17
		fh := fhRootDirect(t, g, nil)
		id := append([]byte{h5hfIDTypeTiny | 15}, data...)
		n, err := fh.objLen(id)
		require.NoError(t, err)
		assert.Equal(t, uint64(16), n)
		got, err := fhRead(t, fh, id)
		require.NoError(t, err)
		assert.Equal(t, data, got)
	})
	t.Run("one_byte_id", func(t *testing.T) {
		fh := fhRootDirect(t, fhDefault(), nil)
		n, err := fh.objLen([]byte{h5hfIDTypeTiny})
		require.NoError(t, err)
		assert.Equal(t, uint64(1), n)
	})
	t.Run("extended_id", func(t *testing.T) {
		g := fhDefault()
		g.idLen = 19
		fh := fhRootDirect(t, g, nil)
		n, err := fh.objLen([]byte{h5hfIDTypeTiny | 1, 3})
		require.NoError(t, err)
		assert.Equal(t, uint64(4), n)
		id := append([]byte{h5hfIDTypeTiny | 1, 3}, "0123456789abcdefg"...)
		got, err := fhRead(t, fh, id)
		require.NoError(t, err)
		assert.Equal(t, data[:4], got)
	})
}

// TestMutationFractalHeapHuge: 'huge' objects (stored in the file)
// (h5hf_huge.go:31, 269). H5HF_read reads:
//
//   - id_len_16: in a heap with 16-byte IDs (one byte short of an
//     address and a length), the object found through the v2 B-tree of
//     huge objects by its 8-byte ID;
//   - whole_file: in a heap with 17-byte IDs (holding the address and
//     length), the object that is the whole file (address 0).
func TestMutationFractalHeapHuge(t *testing.T) {
	data := []byte("a huge object, stored in the file")
	t.Run("id_len_16", func(t *testing.T) {
		g := fhDefault()
		g.idLen = 16
		_, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
			obj := b.put(data)
			rec := le64a(le64a(le64a(nil, obj), uint64(len(data))), 1) /* address, length, ID */
			leaf := append([]byte("BTLF"), 0, h5b2FheapHugeIndirID)
			leaf = append(leaf, rec...)
			leaf = binary.LittleEndian.AppendUint32(leaf, checksumLookup3(leaf, 0))
			node := b.alloc(512)
			b.set(node, leaf)
			h := append([]byte("BTHD"), 0, h5b2FheapHugeIndirID)
			h = binary.LittleEndian.AppendUint32(h, 512)
			h = binary.LittleEndian.AppendUint16(h, uint16(len(rec)))
			h = binary.LittleEndian.AppendUint16(h, 0)
			h = append(h, 100, 40)
			h = le64a(binary.LittleEndian.AppendUint16(le64a(h, node), 1), 1)
			h = binary.LittleEndian.AppendUint32(h, checksumLookup3(h, 0))
			g.hugeBt2 = b.put(h)
			return b.put(g.dblock(heap, 0, 512, nil)), 0
		})
		require.NoError(t, err)
		id := append(le64a([]byte{h5hfIDTypeHuge}, 1), make([]byte, 7)...)
		got, err := fhRead(t, fh, id)
		require.NoError(t, err)
		assert.Equal(t, data, got)
	})
	t.Run("whole_file", func(t *testing.T) {
		g := fhDefault()
		g.idLen = 17
		f, fh, err := fhFile(t, &g, func(b *h5Builder, heap uint64) (uint64, uint16) {
			return b.put(g.dblock(heap, 0, 512, nil)), 0
		})
		require.NoError(t, err)
		eof := f.eof()
		got, err := fhRead(t, fh, le64a(le64a([]byte{h5hfIDTypeHuge}, 0), eof))
		require.NoError(t, err)
		require.Len(t, got, int(eof))
		assert.Equal(t, []byte("\x89HDF\r\n\x1a\n"), got[:8])
	})
}

// TestMutationFractalHeapSpanSize: H5HF__dtable_span_size (not used by
// the read path) on the doubling table of dense group heaps (width 4,
// blocks of 512 bytes up to 64 KiB, 32-bit offsets: 22 rows)
// (h5hf_dtable.go:129, 153). The values are those of the C function,
// called on the same table; past the last row, where the C function
// reads past the row table, hdf5 returns 0.
func TestMutationFractalHeapSpanSize(t *testing.T) {
	dt := h5hfDtable{width: 4, startBlockSize: 512, maxDirectSize: 65536, maxIndexBits: 32}
	require.NoError(t, h5hfDtableInit(&dt))
	for _, c := range [][4]uint64{
		/* start row, start column, entries, span */
		{0, 1, 1, 512},
		{0, 1, 6, 3072},
		{2, 0, 9, 16384},
		{3, 2, 1, 2048},
		{21, 0, 5, 0},
	} {
		assert.Equal(t, c[3], h5hfDtableSpanSize(&dt, uint32(c[0]), uint32(c[1]), uint32(c[2])), "span %v", c[:3])
	}
}

// TestMutationHugeBt2DirCompare: the record comparison of the v2 B-trees
// of directly accessed 'huge' objects (H5HF__huge_bt2_dir_compare: by
// address, then by length), not used by the read path (h5hf_btree2.go:211,
// 213, 215).
func TestMutationHugeBt2DirCompare(t *testing.T) {
	f := testFile()
	cls := h5hfHugeBt2Dir()
	for _, c := range []struct {
		a, b h5hfHugeBt2DirRec
		want int
	}{
		{h5hfHugeBt2DirRec{addr: 10, len: 20}, h5hfHugeBt2DirRec{addr: 10, len: 20}, 0},
		{h5hfHugeBt2DirRec{addr: 11, len: 20}, h5hfHugeBt2DirRec{addr: 10, len: 30}, 1},
		{h5hfHugeBt2DirRec{addr: 10, len: 19}, h5hfHugeBt2DirRec{addr: 10, len: 20}, -1},
		{h5hfHugeBt2DirRec{addr: 10, len: 21}, h5hfHugeBt2DirRec{addr: 10, len: 20}, 1},
	} {
		got, err := cls.compare(f, nil, &c.a, &c.b)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "%+v %+v", c.a, c.b)
	}
}

// TestMutationFletcher32: Fletcher32 checksums whose sums need the
// reductions to 16 bits after the odd last byte and at the end
// (h5checksum.go:161, 166, 167), as H5_checksum_fletcher32 computes them.
func TestMutationFletcher32(t *testing.T) {
	for _, c := range []struct {
		data string
		want uint32
	}{
		{"ffab6a", 0x695869ac},
		{"c0007fff", 0x00014000},
		{"5affffffa501", 0xb5ff0001},
	} {
		b, err := hex.DecodeString(c.data)
		require.NoError(t, err)
		assert.Equal(t, c.want, checksumFletcher32(b), c.data)
	}
}

// TestMutationDecodeHelpers: the decoding helpers on short input
// (h5decode.go:195, 229): a string without a NUL fails (records an
// overflow) and a variable-length integer longer than its bytes decodes
// the bytes there are.
func TestMutationDecodeHelpers(t *testing.T) {
	p := &image{buf: []byte("ab")}
	assert.Equal(t, "", p.cstring())
	assert.Error(t, p.done())
	p = &image{buf: []byte("ab\x00c")}
	assert.Equal(t, "ab", p.cstring())
	assert.NoError(t, p.done())
	assert.Equal(t, 3, p.off)
	assert.Equal(t, uint64(0x0201), uintVarLen([]byte{1, 2}, 4))
}
