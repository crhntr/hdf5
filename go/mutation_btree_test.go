package hdf5

// Tests for the B-tree code (h5b.go, h5b_cache.go, h5b2.go, h5b2_cache.go,
// h5b2_hdr.go, h5b2_int.go) written from the mutants gremlins left alive.
// Most build files with h5Builder (mutation_h5o_test.go): chunked int32
// datasets whose chunks of one element are indexed by a hand-assembled v2
// B-tree, and old-style groups whose symbol table B-tree is assembled
// node by node; the others patch v2 B-tree nodes of corpus files. The
// expectations are what libhdf5 1.14.6 reads from the same bytes (set
// HDF5_MUTATION_DUMP to a directory to have the tests write the files
// there).

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bt2Node is a node of a hand-assembled v2 B-tree chunk index (type 10,
// one-dimensional dataset): the scaled offsets of its records and, for
// internal nodes, its children.
type bt2Node struct {
	recs []uint64
	kids []*bt2Node
}

func bt2Leaf(recs ...uint64) *bt2Node { return &bt2Node{recs: recs} }

// count is the number of records in the subtree.
func (n *bt2Node) count() uint64 {
	c := uint64(len(n.recs))
	for _, k := range n.kids {
		c += k.count()
	}
	return c
}

// limitEncSize is H5VM_limit_enc_size: the bytes needed to encode n.
func limitEncSize(n uint64) int {
	if n == 0 {
		return 1
	}
	return (bits.Len64(n)-1)/8 + 1
}

// appendVar appends the low n bytes of v (little-endian).
func appendVar(b []byte, v uint64, n int) []byte { return append(b, le64a(nil, v)[:n]...) }

// bt2ChunkIndex assembles a v2 B-tree chunk index (unfiltered, 16-byte
// records: chunk address and scaled offset) whose nodes are nodeSize
// bytes; the chunk of scaled offset s is the int32 at data+4*s.
type bt2ChunkIndex struct {
	nodeSize   uint32
	depth      uint16
	root       *bt2Node /* nil: empty tree */
	data       uint64
	maxNrecLen int   /* bytes of a node pointer's record count */
	cumLen     []int /* bytes of a node pointer's total record count, by depth */
}

// sizes computes the sizes of the node pointer fields (H5B2__hdr_init).
func (x *bt2ChunkIndex) sizes() {
	const rrec, leafPrefix, intPrefix = 16, 10, 10
	maxNrec := (uint64(x.nodeSize) - leafPrefix) / rrec
	x.maxNrecLen = limitEncSize(maxNrec)
	x.cumLen = []int{0}
	cum := maxNrec
	for d := 1; d <= int(x.depth); d++ {
		ptr := uint64(8 + x.maxNrecLen + x.cumLen[d-1])
		n := (uint64(x.nodeSize) - (intPrefix + ptr)) / (rrec + ptr)
		cum = (n+1)*cum + n
		x.cumLen = append(x.cumLen, limitEncSize(cum))
	}
}

// node puts the node n at depth d (children first) and returns its
// address.
func (x *bt2ChunkIndex) node(b *h5Builder, n *bt2Node, d int) uint64 {
	var kids []uint64
	for _, k := range n.kids {
		kids = append(kids, x.node(b, k, d-1))
	}
	img := []byte("BTLF")
	if d > 0 {
		img = []byte("BTIN")
	}
	img = append(img, 0, h5b2CdsetID)
	for _, s := range n.recs {
		img = le64a(le64a(img, x.data+4*s), s)
	}
	for i, k := range n.kids {
		img = appendVar(le64a(img, kids[i]), uint64(len(k.recs)), x.maxNrecLen)
		if d > 1 {
			img = appendVar(img, k.count(), x.cumLen[d-1])
		}
	}
	img = binary.LittleEndian.AppendUint32(img, checksumLookup3(img, 0))
	addr := b.alloc(int(x.nodeSize))
	b.set(addr, img)
	return addr
}

// put puts the nodes and the header of the index and returns the header
// address.
func (x *bt2ChunkIndex) put(b *h5Builder) uint64 {
	x.sizes()
	root, nrec, all := uint64(haddrUndef), 0, uint64(0)
	if x.root != nil {
		root, nrec, all = x.node(b, x.root, int(x.depth)), len(x.root.recs), x.root.count()
	}
	h := append([]byte("BTHD"), 0, h5b2CdsetID)
	h = binary.LittleEndian.AppendUint32(h, x.nodeSize)
	h = binary.LittleEndian.AppendUint16(h, 16)
	h = binary.LittleEndian.AppendUint16(h, x.depth)
	h = append(h, 100, 40)
	h = binary.LittleEndian.AppendUint16(le64a(h, root), uint16(nrec))
	h = le64a(h, all)
	h = binary.LittleEndian.AppendUint32(h, checksumLookup3(h, 0))
	return b.put(h)
}

// bt2ChunkDataset opens a file whose "/obj" is an int32 dataset of n
// elements (fill value -7), in chunks of one element indexed by x; chunk s
// holds 100+s.
func bt2ChunkDataset(t *testing.T, n uint64, x *bt2ChunkIndex) *Dataset {
	t.Helper()
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		data := make([]byte, 0, 4*n)
		for s := range n {
			data = binary.LittleEndian.AppendUint32(data, uint32(100+s))
		}
		x.data = b.put(data)
		idx := x.put(b)
		/* Version 4 chunked layout: chunk dimensions {1, 4} (1-byte
		 * encoded), v2 B-tree index (node size 2048, split 100%, merge
		 * 40%: creation parameters only) at idx. */
		l := le64a([]byte{4, 2, 0, 2, 1, 1, 4, 5, 0, 8, 0, 0, 100, 40}, idx)
		return b.put(ohdr2(2, v2Msgs(spaceMsg(n), i32Type(), fillMsg(-7), layoutMsg(l))))
	})
	ds, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	return ds
}

// chunkValues is 100+s for each s in [from, to), with -7 for the chunks in
// missing.
func chunkValues(from, to uint64, missing ...uint64) []int32 {
	var v []int32
	for s := from; s < to; s++ {
		x := int32(100 + s)
		for _, m := range missing {
			if m == s {
				x = -7
			}
		}
		v = append(v, x)
	}
	return v
}

// chunkScaled returns the scaled offsets of the chunks h5dChunkIter (the
// H5Dchunk_iter of the C library) visits.
func chunkScaled(t *testing.T, ds *Dataset) []uint64 {
	t.Helper()
	dset, err := ds.dataset()
	require.NoError(t, err)
	var got []uint64
	require.NoError(t, ds.f.h5dChunkIter(dset, func(offset []uint64, _ uint32, _ haddr, _ uint64) error {
		got = append(got, offset[0])
		return nil
	}))
	return got
}

// TestMutationBt2ChunkIndex: v2 B-trees at the limits of the node sizing
// (h5b2_hdr.go:142, 163; h5b2_cache.go:186, 228, 261, 318, 360), an empty
// tree (h5b2.go:114) and an internal node without records
// (h5b2_int.go:27). libhdf5 reads every dataset (H5Dread, H5Dchunk_iter).
func TestMutationBt2ChunkIndex(t *testing.T) {
	seq := func(from, to uint64) []uint64 {
		var s []uint64
		for i := from; i < to; i++ {
			s = append(s, i)
		}
		return s
	}
	for _, c := range []struct {
		name    string
		n       uint64
		index   bt2ChunkIndex
		missing []uint64
	}{
		/* No records: no root node. */
		{"empty", 4, bt2ChunkIndex{nodeSize: 512}, []uint64{0, 1, 2, 3}},
		/* A leaf holds exactly one record (node size: leaf prefix and
		 * checksum, 10 bytes, plus one record). */
		{"one_record_nodes", 3, bt2ChunkIndex{nodeSize: 26, root: bt2Leaf(1)}, []uint64{0, 2}},
		/* 256 records per leaf: record counts take two bytes. */
		{"256_record_leaf", 256, bt2ChunkIndex{nodeSize: 10 + 256*16, root: bt2Leaf(seq(0, 256)...)}, nil},
		/* Internal and leaf nodes filled to the last byte. */
		{"full_nodes", 5, bt2ChunkIndex{nodeSize: 44, depth: 1, root: &bt2Node{
			recs: []uint64{2}, kids: []*bt2Node{bt2Leaf(0, 1), bt2Leaf(3, 4)}}}, nil},
		/* An internal node with no record (one child). */
		{"internal_node_without_records", 6, bt2ChunkIndex{nodeSize: 512, depth: 2, root: &bt2Node{
			recs: []uint64{1},
			kids: []*bt2Node{
				{kids: []*bt2Node{bt2Leaf(0)}},
				{recs: []uint64{4}, kids: []*bt2Node{bt2Leaf(2, 3), bt2Leaf(5)}},
			}}}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			ds := bt2ChunkDataset(t, c.n, &c.index)
			got, err := Read[int32](ds)
			require.NoError(t, err)
			assert.Equal(t, chunkValues(0, c.n, c.missing...), got)
			var want []uint64
			for s := range c.n {
				if !containsU64(c.missing, s) {
					want = append(want, s)
				}
			}
			assert.Equal(t, want, chunkScaled(t, ds))
		})
	}
}

func containsU64(s []uint64, v uint64) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestMutationBt2FullLeaf: a leaf of 65535 records (the largest count a
// node pointer holds, h5b2_cache.go:261). libhdf5 reads the chunks on
// both sides of the leaf's end.
func TestMutationBt2FullLeaf(t *testing.T) {
	const n = 0xffff + 2
	recs := make([]uint64, 0xffff)
	for i := range recs {
		recs[i] = uint64(i)
	}
	ds := bt2ChunkDataset(t, n, &bt2ChunkIndex{nodeSize: 10 + 0xffff*16, depth: 1, root: &bt2Node{
		recs: []uint64{0xffff}, kids: []*bt2Node{bt2Leaf(recs...), bt2Leaf(0x10000)}}})
	got, err := ds.ReadHyperslab(h5.Hyperslab{Start: []uint64{0xfffd}, Count: []uint64{4}})
	require.NoError(t, err)
	assert.Equal(t, []any{int32(100 + 0xfffd), int32(100 + 0xfffe), int32(100 + 0xffff), int32(100 + 0x10000)}, got)
}

// TestMutationBt2NeighborLeaf: H5B2_neighbor on a tree whose root is a
// leaf (h5b2.go:343). (No read path of the C library calls
// H5B2_neighbor; the neighbors are those of the records' order.)
func TestMutationBt2NeighborLeaf(t *testing.T) {
	ds := bt2ChunkDataset(t, 6, &bt2ChunkIndex{nodeSize: 512, root: bt2Leaf(0, 2, 4)})
	dset, err := ds.dataset()
	require.NoError(t, err)
	bt, _, err := h5dBt2IdxOpen(ds.f, &dset.layout)
	require.NoError(t, err)
	neighbor := func(rng int, s uint64) (uint64, error) {
		var got uint64
		err := bt.neighbor(rng, &h5dBt2Ud{rec: h5dChunkRec{scaled: []uint64{s}}, ndims: 1}, func(rec any) error {
			got = rec.(*h5dChunkRec).scaled[0]
			return nil
		})
		return got, err
	}
	for _, c := range []struct {
		rng       int
		s, want   uint64
		wantError bool
	}{
		{h5b2CompareLess, 3, 2, false},
		{h5b2CompareLess, 2, 0, false},
		{h5b2CompareLess, 0, 0, true},
		{h5b2CompareGreater, 3, 4, false},
		{h5b2CompareGreater, 0, 2, false},
		{h5b2CompareGreater, 4, 0, true},
	} {
		got, err := neighbor(c.rng, c.s)
		if c.wantError {
			assert.Error(t, err, "%d %d", c.rng, c.s)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "%d %d", c.rng, c.s)
	}
}

// treeNode encodes a version 1 B-tree node ("TREE") of type typ (0:
// group, 1: chunks) at level: keys[i] and children[i] alternate, keys
// holding one more key than there are children (for a node without
// children, keys[0] is what follows the header). The node is padded to
// size bytes.
func treeNode(typ, level byte, left, right uint64, keys [][]byte, children []uint64, size int) []byte {
	b := append([]byte("TREE"), typ, level)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(children)))
	b = le64a(le64a(b, left), right)
	for i, k := range keys {
		b = append(b, k...)
		if i < len(children) {
			b = le64a(b, children[i])
		}
	}
	return append(b, make([]byte, size-len(b))...)
}

// snodeTreeSize is the size of a group B-tree node with the default K
// (16): header, 32 children, 33 keys.
const snodeTreeSize = 24 + 32*8 + 33*8

// snodeTree encodes a group B-tree node at level whose children are
// children (keys: heap offset 0).
func snodeTree(level byte, left, right uint64, children ...uint64) []byte {
	keys := make([][]byte, len(children)+1)
	for i := range keys {
		keys[i] = le64a(nil, 0)
	}
	return treeNode(0, level, left, right, keys, children, snodeTreeSize)
}

// stabGroup puts a local heap (a 16-byte data segment) and the object
// header of an old-style group whose B-tree is at btree, and returns the
// header and heap addresses.
func stabGroup(b *h5Builder, btree uint64) (obj, heap uint64) {
	heap = b.alloc(32 + 16)
	b.set(heap, le64a(le64a(le64a(append([]byte("HEAP"), 0, 0, 0, 0), 16), 1), heap+32))
	stab := ohMsg{typ: h5oStabID, data: le64a(le64a(nil, btree), heap)}
	return b.put(v1Hdr(1, v1Msgs(stab))), heap
}

// TestMutationStabBtreeSize: the B-tree and heap sizes of an old-style
// group (H5G__stab_bh_size, the meta_size.obj of H5Oget_native_info) when
// the walk along the levels of the B-tree reads every node but one child
// of the root is not a B-tree node (h5b.go:381). libhdf5 reports the sizes
// of the first group (3 B-tree nodes, 3 symbol table nodes of 328 bytes,
// a heap of 48 bytes) and fails on the second.
func TestMutationStabBtreeSize(t *testing.T) {
	for _, c := range []struct {
		name string
		bad  bool
	}{{"valid", false}, {"bad_second_child", true}} {
		t.Run(c.name, func(t *testing.T) {
			b := newH5Builder()
			snods := b.alloc(8)
			l0a, l0b, root := b.alloc(snodeTreeSize), b.alloc(snodeTreeSize), b.alloc(snodeTreeSize)
			obj, heap := stabGroup(b, root)
			b.set(l0a, snodeTree(0, haddrUndef, l0b, snods, snods))
			b.set(l0b, snodeTree(0, l0a, haddrUndef, snods))
			second := l0b
			if c.bad {
				second = heap
			}
			b.set(root, snodeTree(1, haddrUndef, haddrUndef, l0a, second))
			f := builtFile(t, b.finish(b.rootGroup(hardLinkMsg("obj", obj))))
			indexSize, heapSize, err := f.gStabBhSize(&h5oStab{btreeAddr: root, heapAddr: heap})
			if c.bad {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, uint64(3*snodeTreeSize+3*328), indexSize)
			assert.Equal(t, uint64(48), heapSize)
		})
	}
}

// bGetInfoTimeout runs bGetInfoHelper on the group B-tree at addr,
// failing the test if it does not return within 10 seconds.
func bGetInfoTimeout(t *testing.T, f *File, addr uint64) (h5bInfo, error) {
	t.Helper()
	type result struct {
		info h5bInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var info h5bInfo
		err := f.bGetInfoHelper(h5bSnode, nil, addr, &info)
		done <- result{info, err}
	}()
	select {
	case r := <-done:
		return r.info, r.err
	case <-time.After(10 * time.Second):
		t.Fatal("bGetInfoHelper did not return")
		return h5bInfo{}, nil
	}
}

// TestMutationStabBtreeCycles: group B-trees in which a node is its own
// right sibling or its own leftmost child. libhdf5 walks them without end
// (H5B__get_info_helper follows right siblings without a bound, and
// H5Oget_native_info does not return; it recurses on the leftmost child
// until the stack overflows); hdf5 stops once it has counted more nodes than the file can hold
// (h5b.go:306, 336) or more levels than a B-tree can have (h5b.go:308,
// 309).
func TestMutationStabBtreeCycles(t *testing.T) {
	t.Run("right_sibling", func(t *testing.T) {
		b := newH5Builder()
		snods := b.alloc(8)
		root := b.alloc(snodeTreeSize)
		b.set(root, snodeTree(0, haddrUndef, root, snods))
		obj, _ := stabGroup(b, root)
		f := builtFile(t, b.finish(b.rootGroup(hardLinkMsg("obj", obj))))
		info, err := bGetInfoTimeout(t, f, root)
		require.Error(t, err)
		/* It fails when about to read a node beyond file size / node
		 * size + 1 nodes. */
		assert.Equal(t, f.eof()/snodeTreeSize+2, info.numNodes)
	})
	t.Run("leftmost_child", func(t *testing.T) {
		b := newH5Builder()
		root := b.alloc(snodeTreeSize)
		b.set(root, snodeTree(1, haddrUndef, haddrUndef, root))
		obj, _ := stabGroup(b, root)
		f := builtFile(t, b.finish(b.rootGroup(hardLinkMsg("obj", obj))))
		info, err := bGetInfoTimeout(t, f, root)
		require.Error(t, err)
		/* It fails when about to read a 258th level. */
		assert.Equal(t, uint64(257), info.numNodes)
	})
}

// TestMutationChunkBtreeEmptyRoot: the root of a chunk B-tree without
// children (as H5D__btree_idx_create writes it), followed by bytes that
// do not decode as a key (an offset that is not a multiple of the chunk
// size): H5B__cache_deserialize decodes the final key only for nodes with
// children (h5b_cache.go:89), and libhdf5 reads the fill value.
func TestMutationChunkBtreeEmptyRoot(t *testing.T) {
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		key := le64a(le64a(binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, 4), 0), 1), 0)
		root := b.put(treeNode(1, 0, haddrUndef, haddrUndef, [][]byte{key}, nil, 24+64*8+65*24))
		l := append(le64a([]byte{3, 2, 2}, root), 2, 0, 0, 0, 4, 0, 0, 0)
		return b.put(ohdr2(2, v2Msgs(spaceMsg(4), i32Type(), fillMsg(-7), layoutMsg(l))))
	})
	ds, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	got, err := Read[int32](ds)
	require.NoError(t, err)
	assert.Equal(t, []int32{-7, -7, -7, -7}, got)
}

// patchedFile opens a copy of the file rel (relative to the repository
// root, or under testdata) in which patch has changed bytes; patch gets
// the file opened from the original bytes to look up addresses.
func patchedFile(t *testing.T, rel string, patch func(orig *File, b []byte)) *File {
	t.Helper()
	path := rel
	if !strings.HasPrefix(rel, "testdata/") {
		path = filepath.Join(repoRoot(t), rel)
	}
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	orig, err := OpenReader(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	defer orig.Close()
	b = append([]byte(nil), b...)
	patch(orig, b)
	return builtFile(t, b)
}

// setChecksum stores the checksum of b[start:end-4] in b[end-4:end].
func setChecksum(b []byte, start, end uint64) {
	binary.LittleEndian.PutUint32(b[end-4:], checksumLookup3(b[start:end-4], 0))
}

// rootNameByIdx is the name of the n-th link of the root group in the
// native order of its name index (H5Lget_name_by_idx with H5_INDEX_NAME,
// H5_ITER_NATIVE).
func rootNameByIdx(t *testing.T, f *File, n uint64) (string, error) {
	t.Helper()
	oh, err := f.oProtect(f.sblock.rootAddr)
	require.NoError(t, err)
	return f.gObjGetNameByIdx(oh, h5IndexName, h5IterNative, n)
}

// TestMutationBt2IndexCounts: H5B2_index on name indexes of dense groups
// whose record counts disagree with the records (h5b2.go:241, 293, 313):
// the root group of testdata/dset.h5 (22 links, a leaf) with the total
// record count of the B-tree header lowered to 21 or raised to 23, and
// the root group of h5stat_newgrat.h5 (35001 links, depth 3) with the
// record count of the last child of the root node lowered by one. libhdf5
// looks up the links within the counts and fails on the links past them
// (H5Lget_name_by_idx; past the records of the root leaf, H5B2_index only
// asserts the index in a release build and fails on the record it reads
// beyond them).
func TestMutationBt2IndexCounts(t *testing.T) {
	const dsetNameBt2 = 6380 /* h5debug testdata/dset.h5 6380 */
	allNrec := func(v uint64) func(*File, []byte) {
		return func(_ *File, b []byte) {
			binary.LittleEndian.PutUint64(b[dsetNameBt2+26:], v)
			setChecksum(b, dsetNameBt2, dsetNameBt2+38)
		}
	}
	t.Run("total_count_low", func(t *testing.T) {
		f := patchedFile(t, "testdata/dset.h5", allNrec(21))
		name, err := rootNameByIdx(t, f, 20)
		require.NoError(t, err)
		assert.Equal(t, "external", name)
		_, err = rootNameByIdx(t, f, 21)
		assert.Error(t, err)
	})
	t.Run("total_count_high", func(t *testing.T) {
		f := patchedFile(t, "testdata/dset.h5", allNrec(23))
		_, err := rootNameByIdx(t, f, 22)
		assert.Error(t, err)
	})
	t.Run("child_count_low", func(t *testing.T) {
		f := patchedFile(t, "tools/test/h5stat/testfiles/h5stat_newgrat.h5", func(orig *File, b []byte) {
			bt, err := orig.b2Open(newgratLinkNameBt2, h5gBt2Name(), nil)
			require.NoError(t, err)
			hdr := bt.hdr
			require.EqualValues(t, 3, hdr.depth)
			/* The total record count of the last node pointer of the
			 * root node. */
			ptr := h5b2IntPointerSize(hdr, hdr.depth)
			n := uint64(hdr.rootNrec)
			off := hdr.rootAddr + h5b2IntPrefixSize - 4 + n*uint64(hdr.rrecSize) + n*ptr + uint64(hdr.sizeofAddr) + uint64(hdr.maxNrecSize)
			w := int(hdr.nodeInfo[hdr.depth-1].cumMaxNrecSize)
			v := make([]byte, 8)
			copy(v, b[off:off+uint64(w)])
			copy(b[off:off+uint64(w)], le64a(nil, le64(v)-1)[:w])
			setChecksum(b, hdr.rootAddr, hdr.rootAddr+h5b2CacheIntChkSize(hdr, hdr.rootNrec, hdr.depth))
		})
		name, err := rootNameByIdx(t, f, newgratNumLinks-2)
		require.NoError(t, err)
		assert.Equal(t, "GROUP12845", name)
		_, err = rootNameByIdx(t, f, newgratNumLinks-1)
		assert.Error(t, err)
	})
}
