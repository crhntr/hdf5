package hdf5

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testV1Stab finds the symbol table message in the version 1 object header
// at addr. It is a minimal test-only reader (object headers are translated
// elsewhere) that follows continuation messages. ok is false when the
// header has no symbol table message or is not a version 1 header.
func testV1Stab(f *File, addr haddr) (stab *h5oStab, ok bool, err error) {
	p, err := f.readImage(addr, 16)
	if err != nil {
		return nil, false, err
	}
	if p.uint8() != 1 {
		return nil, false, nil
	}
	p.skip(1)
	nmesgs := int(p.uint16())
	p.skip(4)
	chunkSize := int(p.uint32())
	type chunk struct {
		addr haddr
		size int
	}
	chunks := []chunk{{addr + 16, chunkSize}}
	seen := 0
	for len(chunks) > 0 && seen < nmesgs && seen < 1000 {
		c := chunks[0]
		chunks = chunks[1:]
		q, err := f.readImage(c.addr, c.size)
		if err != nil {
			return nil, false, err
		}
		for q.remaining() >= 8 && seen < nmesgs {
			typ := q.uint16()
			size := int(q.uint16())
			q.skip(4)
			data := q.sub(size)
			seen++
			switch typ {
			case h5oStabID:
				s := &h5oStab{btreeAddr: data.addr(), heapAddr: data.addr()}
				return s, true, data.done()
			case h5oContID:
				a := data.addr()
				l := data.length()
				if data.done() == nil && l < 1<<20 {
					chunks = append(chunks, chunk{a, int(l)})
				}
			}
			if q.done() != nil {
				return nil, false, q.done()
			}
		}
	}
	return nil, false, nil
}

// testRootStab returns the root group's symbol table: the stab message of
// the root object header when present, else the addresses cached in the
// superblock's root symbol table entry.
func testRootStab(t *testing.T, f *File) *h5oStab {
	t.Helper()
	require.NotNil(t, f.sblock.rootEnt, "superblock version 0/1 expected")
	if s, ok, err := testV1Stab(f, f.sblock.rootAddr); err == nil && ok {
		return s
	}
	ent := f.sblock.rootEnt
	require.Equal(t, h5gCachedStab, ent.typ)
	return &h5oStab{btreeAddr: ent.btreeAddr, heapAddr: ent.heapAddr}
}

// testWalk lists the links of a symbol table group recursively, in the
// format of `h5ls -r` paths, with soft link values appended as " -> value".
func testWalk(f *File, stab *h5oStab, path string, visited map[haddr]bool, out *[]string) error {
	return f.gStabIterate(stab, func(name string, ent *h5gEntry) error {
		full := path + "/" + name
		if ent.typ == h5gCachedSlink {
			v, err := f.gStabSoftValue(stab, ent)
			if err != nil {
				return err
			}
			*out = append(*out, full+" -> "+v)
			return nil
		}
		*out = append(*out, full)
		if visited[ent.header] {
			return nil
		}
		visited[ent.header] = true
		sub, ok, err := testV1Stab(f, ent.header)
		if err != nil || !ok {
			return err
		}
		return testWalk(f, sub, full, visited, out)
	})
}

func testOpen(t *testing.T, rel string) *File {
	t.Helper()
	path := rel
	if !filepath.IsAbs(rel) && !bytes.HasPrefix([]byte(rel), []byte("testdata/")) {
		path = filepath.Join(repoRoot(t), rel)
	}
	f, err := Open(path, WithExternalLinks(false))
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

func TestStabCorpusListings(t *testing.T) {
	// Expected values are `h5ls -r` output of the C library (1.14.6).
	for _, tc := range []struct {
		file string
		want []string
	}{
		{"tools/test/h5dump/testfiles/tgroup.h5", []string{
			"/g1", "/g1/g1.1", "/g1/g1.2",
			"/g2", "/g2/g2.1", "/g2/g2.1/g2.1.1", "/g2/g2.1/g2.1.2", "/g2/g2.1/g2.1.3",
			"/g3", "/g3/g3.1", "/g3/g3.2", "/g3/g3.3", "/g3/g3.4",
		}},
		{"tools/test/h5dump/testfiles/tsoftlinks.h5", []string{
			"/dset1", "/dset2", "/dtype", "/group1",
			"/group1/soft_dangle -> not_yet",
			"/group1/soft_dset1 -> /dset1",
			"/group1/soft_dset2 -> /dset2",
			"/group1/soft_dtype -> /dtype",
			"/group1/soft_empty_grp -> /group_empty",
			"/group_empty",
			"/soft_dangle -> not_yet",
			"/soft_dset1 -> /dset1",
			"/soft_dtype -> /dtype",
			"/soft_empty_grp -> /group_empty",
			"/soft_group1 -> /group1",
		}},
		{"tools/test/h5dump/testfiles/tslink.h5", []string{"/slink1 -> somevalue", "/slink2 -> linkvalue"}},
		{"tools/test/h5dump/testfiles/tall.h5", nil},
		{"test/testfiles/group_old.h5", []string{"/old"}},
		{"tools/test/h5dump/testfiles/tlarge_objname.h5", nil},
		{"tools/test/h5dump/testfiles/thlink.h5", nil},
	} {
		t.Run(filepath.Base(tc.file), func(t *testing.T) {
			f := testOpen(t, tc.file)
			var got []string
			require.NoError(t, testWalk(f, testRootStab(t, f), "", map[haddr]bool{f.sblock.rootAddr: true}, &got))
			if tc.want != nil {
				assert.Equal(t, tc.want, got)
			} else {
				assert.NotEmpty(t, got)
			}
		})
	}
}

func TestStabRepackEarly(t *testing.T) {
	// h5repack_early.h5: an old-style root group with 100 named datatypes
	// "0" .. "99" (13 symbol table nodes).
	f := testOpen(t, "tools/test/h5repack/testfiles/h5repack_early.h5")
	stab := testRootStab(t, f)

	var want []string
	for i := 0; i < 100; i++ {
		want = append(want, fmt.Sprint(i))
	}
	sort.Strings(want) // strcmp order: "0", "1", "10", "11", ...

	var got []string
	require.NoError(t, f.gStabIterate(stab, func(name string, ent *h5gEntry) error {
		got = append(got, name)
		return nil
	}))
	assert.Equal(t, want, got)

	n, err := f.gStabCount(stab)
	require.NoError(t, err)
	assert.Equal(t, uint64(100), n)

	for i, name := range want {
		ent, found, err := f.gStabLookup(stab, name)
		require.NoError(t, err, name)
		require.True(t, found, name)
		gotName, ent2, err := f.gStabLookupByIdx(stab, uint64(i))
		require.NoError(t, err)
		assert.Equal(t, name, gotName)
		assert.Equal(t, ent.header, ent2.header)
	}
}

// testStabLinkName / testStabSoftValue describe the links of /big in the
// generated files (see testdata/gen_stab.c).
func testStabLinkName(j int) string { return fmt.Sprintf("link%04d", j) }

func testStabGenerated(t *testing.T, file string, nlinks int, wantLevel uint8, wantIndexSize, wantHeapSize uint64) {
	f := testOpen(t, file)
	root := testRootStab(t, f)

	// Root group: big, dset, empty.
	var rootNames []string
	require.NoError(t, f.gStabIterate(root, func(name string, ent *h5gEntry) error {
		rootNames = append(rootNames, name)
		return nil
	}))
	assert.Equal(t, []string{"big", "dset", "empty"}, rootNames)

	dset, found, err := f.gStabLookup(root, "dset")
	require.NoError(t, err)
	require.True(t, found)
	bigEnt, found, err := f.gStabLookup(root, "big")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, h5gCachedStab, bigEnt.typ, "the group entry caches its symbol table")
	big := &h5oStab{btreeAddr: bigEnt.btreeAddr, heapAddr: bigEnt.heapAddr}
	if s, ok, err := testV1Stab(f, bigEnt.header); assert.NoError(t, err) && assert.True(t, ok) {
		assert.Equal(t, big, s, "cached stab matches the stab message")
	}
	emptyEnt, found, err := f.gStabLookup(root, "empty")
	require.NoError(t, err)
	require.True(t, found)
	empty := &h5oStab{btreeAddr: emptyEnt.btreeAddr, heapAddr: emptyEnt.heapAddr}

	// B-tree depth.
	bt, err := f.bProtect(h5bSnode, nil, big.btreeAddr)
	require.NoError(t, err)
	assert.Equal(t, wantLevel, bt.level)

	// Counts.
	n, err := f.gStabCount(big)
	require.NoError(t, err)
	assert.Equal(t, uint64(nlinks), n)
	n, err = f.gStabCount(empty)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), n)

	// Iteration in name order, entry contents.
	i := 0
	require.NoError(t, f.gStabIterate(big, func(name string, ent *h5gEntry) error {
		require.Equal(t, testStabLinkName(i), name)
		if i%3 == 0 {
			assert.Equal(t, h5gNothingCached, ent.typ, name)
			assert.Equal(t, dset.header, ent.header, name)
		} else {
			require.Equal(t, h5gCachedSlink, ent.typ, name)
			v, err := f.gStabSoftValue(big, ent)
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("/target%d", i), v)
		}
		i++
		return nil
	}))
	assert.Equal(t, nlinks, i)

	// Early stop.
	i = 0
	require.NoError(t, f.gStabIterate(big, func(name string, ent *h5gEntry) error {
		i++
		if i == 7 {
			return errBIterStop
		}
		return nil
	}))
	assert.Equal(t, 7, i)

	// Operator errors propagate.
	boom := errors.New("boom")
	err = f.gStabIterate(big, func(string, *h5gEntry) error { return boom })
	assert.ErrorIs(t, err, boom)

	// Skip.
	last, err := f.gStabIterateSkip(big, uint64(nlinks-2), func(name string, ent *h5gEntry) error {
		assert.Contains(t, []string{testStabLinkName(nlinks - 2), testStabLinkName(nlinks - 1)}, name)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(nlinks), last)
	_, err = f.gStabIterateSkip(big, uint64(nlinks), func(string, *h5gEntry) error { return nil })
	assert.Error(t, err, "skipping all entries is an error")

	// iter.Seq2 form, with break.
	i = 0
	for l, err := range f.gStabLinks(big) {
		require.NoError(t, err)
		assert.Equal(t, testStabLinkName(i), l.name)
		i++
		if i == 10 {
			break
		}
	}
	assert.Equal(t, 10, i)

	// Lookup by name.
	for j := 0; j < nlinks; j++ {
		ent, found, err := f.gStabLookup(big, testStabLinkName(j))
		require.NoError(t, err)
		require.True(t, found, testStabLinkName(j))
		if j%3 == 0 {
			assert.Equal(t, dset.header, ent.header)
		} else {
			assert.Equal(t, h5gCachedSlink, ent.typ)
		}
	}
	for _, name := range []string{"link", "link00000", testStabLinkName(nlinks), "a", "zzz", "link0001x", "big"} {
		_, found, err := f.gStabLookup(big, name)
		require.NoError(t, err, name)
		assert.False(t, found, name)
	}
	_, found, err = f.gStabLookup(empty, "x")
	require.NoError(t, err)
	assert.False(t, found)

	// Lookup by index / name by index.
	for _, idx := range []int{0, 1, 2, 3, nlinks / 2, nlinks - 1} {
		name, ent, err := f.gStabLookupByIdx(big, uint64(idx))
		require.NoError(t, err)
		assert.Equal(t, testStabLinkName(idx), name)
		if idx%3 == 0 {
			assert.Equal(t, dset.header, ent.header)
		}
		name, err = f.gStabGetNameByIdx(big, uint64(idx))
		require.NoError(t, err)
		assert.Equal(t, testStabLinkName(idx), name)
	}
	_, _, err = f.gStabLookupByIdx(big, uint64(nlinks))
	assert.Error(t, err)
	_, err = f.gStabGetNameByIdx(big, uint64(nlinks))
	assert.Error(t, err)
	_, _, err = f.gStabLookupByIdx(empty, 0)
	assert.Error(t, err)

	// Storage, as reported by h5stat ("Groups: B-tree/List, Heap") for the
	// three groups of the file.
	var indexSize, heapSize uint64
	for _, s := range []*h5oStab{root, big, empty} {
		is, hs, err := f.gStabBhSize(s)
		require.NoError(t, err)
		indexSize += is
		heapSize += hs
	}
	assert.Equal(t, wantIndexSize, indexSize)
	assert.Equal(t, wantHeapSize, heapSize)
}

func TestStabDeepBtree(t *testing.T) {
	// sym_k(2, 2): /big has 500 links under a B-tree of level 4
	// (h5debug testdata/stab_deep.h5 1088 1184).
	testStabGenerated(t, "testdata/stab_deep.h5", 500, 4, 32640, 22800)
}

func TestStabDefaultK(t *testing.T) {
	// Default K values: /big has 1000 links under a B-tree of level 1.
	testStabGenerated(t, "testdata/stab_default.h5", 1000, 1, 58576, 45328)
}

func TestStabLocalHeap(t *testing.T) {
	f := testOpen(t, "testdata/stab_deep.h5")
	ent := f.sblock.rootEnt
	heap, err := f.hlProtect(ent.heapAddr)
	require.NoError(t, err)
	// h5debug testdata/stab_deep.h5 232: the root group heap.
	assert.Equal(t, uint64(88), heap.dblkSize)
	assert.Equal(t, heap.dblkSize, uint64(len(heap.image)))
	assert.True(t, heap.singleCacheObj, "data block follows the prefix")
	assert.Equal(t, []h5hlFree{{offset: 32, size: 56}}, heap.freelist)
	assert.Equal(t, uint64(32+88), uint64(heap.prfxSize)+heap.dblkSize)

	// /big's heap was relocated when it grew (h5debug testdata/stab_deep.h5
	// 1184): separate data block.
	bigEnt, found, err := f.gStabLookup(&h5oStab{btreeAddr: ent.btreeAddr, heapAddr: ent.heapAddr}, "big")
	require.NoError(t, err)
	require.True(t, found)
	bigHeap, err := f.hlProtect(bigEnt.heapAddr)
	require.NoError(t, err)
	assert.False(t, bigHeap.singleCacheObj)
	assert.Equal(t, haddr(42744), bigHeap.dblkAddr)
	assert.Equal(t, uint64(22528), bigHeap.dblkSize)
	assert.Equal(t, []h5hlFree{{offset: 13336, size: 9192}}, bigHeap.freelist)
	s, err := heap.stringAt(0)
	require.NoError(t, err)
	assert.Equal(t, "", s)
	s, err = heap.stringAt(8)
	require.NoError(t, err)
	assert.Equal(t, "dset", s)
	_, err = heap.offsetInto(heap.dblkSize)
	assert.Error(t, err)
	_, err = heap.stringAt(1 << 40)
	assert.Error(t, err)

	// Cached: the same value comes back.
	heap2, err := f.hlProtect(ent.heapAddr)
	require.NoError(t, err)
	assert.Same(t, heap, heap2)

	// Not a heap.
	_, err = f.hlProtect(0)
	assert.Error(t, err)
	_, err = f.hlProtect(haddrUndef)
	assert.Error(t, err)
	_, err = f.hlProtect(f.eof() + 100)
	assert.Error(t, err)
}

func TestStabCorruptStabMsg(t *testing.T) {
	// The root group's symbol table message has B-tree and heap addresses
	// of 0 (h5debug corrupt_stab_msg.h5 928). Reading through it fails, as
	// in the C library opened read-only (h5ls lists nothing); H5G__stab_valid
	// repairs it from the root symbol table entry.
	f := testOpen(t, "test/testfiles/corrupt_stab_msg.h5")
	bad, ok, err := testV1Stab(f, f.sblock.rootAddr)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, &h5oStab{btreeAddr: 0, heapAddr: 0}, bad)

	assert.Error(t, f.gStabIterate(bad, func(string, *h5gEntry) error { return nil }))
	_, err = f.gStabCount(bad)
	assert.Error(t, err)
	_, _, err = f.gStabLookup(bad, "DS1")
	assert.Error(t, err)
	_, _, err = f.gStabLookupByIdx(bad, 0)
	assert.Error(t, err)
	_, err = f.gStabGetNameByIdx(bad, 0)
	assert.Error(t, err)
	_, err = f.gStabValid(bad, nil)
	assert.Error(t, err)

	ent := f.sblock.rootEnt
	alt := &h5oStab{btreeAddr: ent.btreeAddr, heapAddr: ent.heapAddr}
	fixed, err := f.gStabValid(bad, alt)
	require.NoError(t, err)
	assert.Equal(t, alt, fixed)
	_, found, err := f.gStabLookup(fixed, "DS1")
	require.NoError(t, err)
	assert.True(t, found)
}

func TestStabCorruptFiles(t *testing.T) {
	// Deliberately corrupt corpus files: errors, never panics.
	for _, tc := range []struct{ file, msg string }{
		{"test/testfiles/bad_offset.h5", "unable to offset into local heap data block"},
		{"tools/test/h5dump/testfiles/3790_infinite_loop.h5", "bad local heap signature"},
	} {
		f := testOpen(t, tc.file)
		var walked []string
		err := testWalk(f, testRootStab(t, f), "", map[haddr]bool{}, &walked)
		require.Error(t, err, tc.file)
		assert.Contains(t, err.Error(), tc.msg, tc.file)
	}
}

// TestStabCorpusNoPanic walks every old-style group reachable from the root
// of every corpus file with a version 0/1 superblock.
func TestStabCorpusNoPanic(t *testing.T) {
	var ok, failed int
	for _, path := range corpusFiles(t) {
		f, err := Open(path, WithExternalLinks(false))
		if err != nil {
			continue
		}
		if f.sblock.rootEnt != nil {
			stab, isStab, err := testV1Stab(f, f.sblock.rootAddr)
			if err != nil || !isStab {
				if e := f.sblock.rootEnt; e.typ == h5gCachedStab {
					stab = &h5oStab{btreeAddr: e.btreeAddr, heapAddr: e.heapAddr}
				}
			}
			if stab != nil {
				var walked []string
				if err := testWalk(f, stab, "", map[haddr]bool{f.sblock.rootAddr: true}, &walked); err != nil {
					failed++
					t.Logf("%s: %v", filepath.Base(path), err)
				} else {
					ok++
				}
			}
		}
		f.Close()
	}
	t.Logf("old-style root groups: %d walked, %d failed", ok, failed)
	assert.Greater(t, ok, 200)
}

// TestStabMutations flips bytes of valid files and reads them: decoding must
// fail cleanly or succeed, never panic or loop.
func TestStabMutations(t *testing.T) {
	for _, file := range []string{"testdata/stab_deep.h5", "testdata/stab_default.h5"} {
		orig, err := os.ReadFile(file)
		require.NoError(t, err)
		rng := rand.New(rand.NewSource(1))
		for iter := 0; iter < quickCount(300, 20); iter++ {
			buf := append([]byte(nil), orig...)
			for k := 0; k < 1+rng.Intn(8); k++ {
				off := rng.Intn(len(buf))
				switch rng.Intn(3) {
				case 0:
					buf[off] ^= byte(1 << rng.Intn(8))
				case 1:
					buf[off] = 0xff
				default:
					buf[off] = byte(rng.Intn(256))
				}
			}
			f, err := OpenReader(bytes.NewReader(buf), int64(len(buf)))
			if err != nil || f.sblock.rootEnt == nil || f.sblock.rootEnt.typ != h5gCachedStab {
				continue
			}
			e := f.sblock.rootEnt
			root := &h5oStab{btreeAddr: e.btreeAddr, heapAddr: e.heapAddr}
			var walked []string
			_ = testWalk(f, root, "", map[haddr]bool{}, &walked)
			if bigEnt, found, err := f.gStabLookup(root, "big"); err == nil && found && bigEnt.typ == h5gCachedStab {
				big := &h5oStab{btreeAddr: bigEnt.btreeAddr, heapAddr: bigEnt.heapAddr}
				_, _ = f.gStabCount(big)
				_, _, _ = f.gStabLookup(big, "link0042")
				_, _, _ = f.gStabLookupByIdx(big, 42)
				_, _, _ = f.gStabBhSize(big)
				_ = f.gStabIterate(big, func(string, *h5gEntry) error { return nil })
			}
			f.Close()
		}
	}
}
