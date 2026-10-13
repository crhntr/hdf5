package hdf5

// Tests for the group and link code (h5g_*.go, h5l*.go) written from the
// mutants gremlins left alive. They open the generated files of testdata
// (whose link orders libhdf5 lists the same way) and files assembled byte
// by byte with h5Builder (mutation_h5o_test.go); the expectations are
// what libhdf5 1.14.6 reports for the same files (H5Literate2,
// H5Lget_name_by_idx, H5Lvisit2, H5Lget_info2, H5Oopen); set
// HDF5_MUTATION_DUMP to a directory to have the tests write the
// assembled files there.

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkOrders lists the links of the group at addr in each index and
// order, as the names (with "/<creation order>" when it is valid) joined
// by spaces, or "FAILED", keyed "name inc", "name dec", "name native",
// "crt inc", "crt dec", "crt native".
func linkOrders(t *testing.T, f *File, addr uint64) map[string]string {
	t.Helper()
	oh, err := f.oProtect(addr)
	require.NoError(t, err)
	out := map[string]string{}
	for _, idx := range []struct {
		name string
		typ  h5Index
	}{{"name", h5IndexName}, {"crt", h5IndexCrtOrder}} {
		for _, ord := range []struct {
			name string
			typ  h5IterOrder
		}{{"inc", h5IterInc}, {"dec", h5IterDec}, {"native", h5IterNative}} {
			var names []string
			err := f.gObjIterate(oh, idx.typ, ord.typ, 0, nil, func(lnk *h5oLink) error {
				if lnk.corderValid {
					names = append(names, fmt.Sprintf("%s/%d", lnk.name, lnk.corder))
				} else {
					names = append(names, lnk.name)
				}
				return nil
			})
			s := strings.Join(names, " ")
			if err != nil {
				s = "FAILED"
			}
			out[idx.name+" "+ord.name] = s
		}
	}
	return out
}

// hardLinkCrtMsg is a link message for a hard link to addr with creation
// order corder.
func hardLinkCrtMsg(name string, addr uint64, corder int64) ohMsg {
	b := le64a([]byte{1, 0x04}, uint64(corder))
	b = append(append(b, byte(len(name))), name...)
	return ohMsg{typ: h5oLinkID, data: le64a(b, addr)}
}

// linfoCrtMsg is a link info message for a group with compact storage
// that tracks the creation order of its links.
func linfoCrtMsg(maxCorder int64) ohMsg {
	return ohMsg{typ: h5oLinfoID, data: le64a(le64a(le64a([]byte{0, 1}, uint64(maxCorder)), haddrUndef), haddrUndef)}
}

// TestMutationGroupDuplicateCreationOrder: compact groups tracking the
// creation order of their links whose link messages repeat creation
// orders. libhdf5 sorts the link table with qsort (H5G__link_sort_table),
// which glibc implements as a stable merge sort: links with the same
// creation order keep the order of their messages in both directions
// (h5g_link.go:43, 56; with 30 links the unstable sort hdf5 used reordered
// them).
func TestMutationGroupDuplicateCreationOrder(t *testing.T) {
	for _, c := range []struct {
		name   string
		corder []int64
		want   map[string]string
	}{
		{"few", []int64{3, 1, 3, 2, 1}, map[string]string{
			"name inc":    "l00/3 l01/1 l02/3 l03/2 l04/1",
			"name dec":    "l04/1 l03/2 l02/3 l01/1 l00/3",
			"name native": "l00/3 l01/1 l02/3 l03/2 l04/1",
			"crt inc":     "l01/1 l04/1 l03/2 l00/3 l02/3",
			"crt dec":     "l00/3 l02/3 l03/2 l01/1 l04/1",
			"crt native":  "l00/3 l01/1 l02/3 l03/2 l04/1",
		}},
		{"many", []int64{4, 2, 0, 3, 1, 4, 2, 0, 3, 1, 4, 2, 0, 3, 1, 4, 2, 0, 3, 1, 4, 2, 0, 3, 1, 4, 2, 0, 3, 1}, map[string]string{
			"name inc":    "l00/4 l01/2 l02/0 l03/3 l04/1 l05/4 l06/2 l07/0 l08/3 l09/1 l10/4 l11/2 l12/0 l13/3 l14/1 l15/4 l16/2 l17/0 l18/3 l19/1 l20/4 l21/2 l22/0 l23/3 l24/1 l25/4 l26/2 l27/0 l28/3 l29/1",
			"name dec":    "l29/1 l28/3 l27/0 l26/2 l25/4 l24/1 l23/3 l22/0 l21/2 l20/4 l19/1 l18/3 l17/0 l16/2 l15/4 l14/1 l13/3 l12/0 l11/2 l10/4 l09/1 l08/3 l07/0 l06/2 l05/4 l04/1 l03/3 l02/0 l01/2 l00/4",
			"name native": "l00/4 l01/2 l02/0 l03/3 l04/1 l05/4 l06/2 l07/0 l08/3 l09/1 l10/4 l11/2 l12/0 l13/3 l14/1 l15/4 l16/2 l17/0 l18/3 l19/1 l20/4 l21/2 l22/0 l23/3 l24/1 l25/4 l26/2 l27/0 l28/3 l29/1",
			"crt inc":     "l02/0 l07/0 l12/0 l17/0 l22/0 l27/0 l04/1 l09/1 l14/1 l19/1 l24/1 l29/1 l01/2 l06/2 l11/2 l16/2 l21/2 l26/2 l03/3 l08/3 l13/3 l18/3 l23/3 l28/3 l00/4 l05/4 l10/4 l15/4 l20/4 l25/4",
			"crt dec":     "l00/4 l05/4 l10/4 l15/4 l20/4 l25/4 l03/3 l08/3 l13/3 l18/3 l23/3 l28/3 l01/2 l06/2 l11/2 l16/2 l21/2 l26/2 l04/1 l09/1 l14/1 l19/1 l24/1 l29/1 l02/0 l07/0 l12/0 l17/0 l22/0 l27/0",
			"crt native":  "l00/4 l01/2 l02/0 l03/3 l04/1 l05/4 l06/2 l07/0 l08/3 l09/1 l10/4 l11/2 l12/0 l13/3 l14/1 l15/4 l16/2 l17/0 l18/3 l19/1 l20/4 l21/2 l22/0 l23/3 l24/1 l25/4 l26/2 l27/0 l28/3 l29/1",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newH5Builder()
			target := b.put(ohdr2(2, v2Msgs(linfoMsg())))
			msgs := []ohMsg{linfoCrtMsg(10)}
			for i, co := range c.corder {
				msgs = append(msgs, hardLinkCrtMsg(fmt.Sprintf("l%02d", i), target, co))
			}
			root := b.put(ohdr2(2, v2Msgs(msgs...)))
			f := builtFile(t, b.finish(root))
			assert.Equal(t, c.want, linkOrders(t, f, root))
		})
	}
}

// iterSummary iterates over the links of the group at path in the given
// index and order from skip, stopping after stop links (0: never), as
// H5Literate2 does: the names visited and the index after the last link
// passed (H5Literate2's idx), or the error.
func iterSummary(t *testing.T, f *File, path string, idx h5Index, order h5IterOrder, skip uint64, stop int) ([]string, uint64, error) {
	t.Helper()
	g, err := f.OpenGroup(path)
	require.NoError(t, err)
	oh, err := g.header()
	require.NoError(t, err)
	var names []string
	var last uint64
	err = f.gObjIterate(oh, idx, order, skip, &last, func(lnk *h5oLink) error {
		names = append(names, lnk.name)
		if len(names) == stop {
			return errBIterStop
		}
		return nil
	})
	return names, last, err
}

// TestMutationGroupIterateSkip: H5Literate2 from an index on the
// old-style groups of testdata/stab_default.h5 (/empty: no link; /big:
// 1000 links named link0000..link0999) and on the dense root group of
// testdata/fixtures/root_crt_order/main.h5 (19 links, name and creation
// order indexes). libhdf5 lists no link of /empty in increasing and in
// decreasing order (a sorted table, h5g_obj.go:143), fails to start /big
// at index 1000 in every order (h5g_obj.go:143), lists 999 links from
// index 1 in both orders (h5g_obj.go:143) and 998 links from link0002 in
// native order from index 2 (h5g_node.go:193), with the index 1000 after
// them; it stops the dense native iteration at the third link with the
// index 3 (h5g_dense.go:292) and lists 17 links from index 2.
func TestMutationGroupIterateSkip(t *testing.T) {
	f := openTestdata(t, "stab_default.h5")
	names, last, err := iterSummary(t, f, "/empty", h5IndexName, h5IterInc, 0, 0)
	require.NoError(t, err)
	assert.Empty(t, names)
	assert.Zero(t, last)

	names, last, err = iterSummary(t, f, "/empty", h5IndexName, h5IterDec, 0, 0)
	require.NoError(t, err)
	assert.Empty(t, names)
	assert.Zero(t, last)
	for _, order := range []h5IterOrder{h5IterInc, h5IterDec, h5IterNative} {
		_, _, err = iterSummary(t, f, "/big", h5IndexName, order, 1000, 0)
		assert.Error(t, err, "order %v", order)
	}

	for _, c := range []struct {
		order       h5IterOrder
		skip        uint64
		n           int
		first, last string
	}{
		{h5IterInc, 1, 999, "link0001", "link0999"},
		{h5IterNative, 2, 998, "link0002", "link0999"},
		{h5IterDec, 1, 999, "link0998", "link0000"},
		{h5IterDec, 999, 1, "link0000", "link0000"},
	} {
		names, last, err := iterSummary(t, f, "/big", h5IndexName, c.order, c.skip, 0)
		require.NoError(t, err)
		require.Len(t, names, c.n)
		assert.Equal(t, c.first, names[0])
		assert.Equal(t, c.last, names[len(names)-1])
		assert.Equal(t, uint64(1000), last)
	}

	f = openTestdata(t, "fixtures/root_crt_order/main.h5")
	names, last, err = iterSummary(t, f, "/", h5IndexName, h5IterNative, 0, 3)
	require.NoError(t, err)
	assert.Equal(t, []string{"soft_ints", "ext_group", "strings"}, names)
	assert.Equal(t, uint64(3), last)
	names, last, err = iterSummary(t, f, "/", h5IndexName, h5IterNative, 2, 0)
	require.NoError(t, err)
	require.Len(t, names, 17)
	assert.Equal(t, "strings", names[0])
	assert.Equal(t, "phase", names[16])
	assert.Equal(t, uint64(19), last)
}

// nameByIdx is gObjGetNameByIdx (H5Lget_name_by_idx) on the group at
// path.
func nameByIdx(t *testing.T, f *File, path string, idx h5Index, order h5IterOrder, n uint64) (string, error) {
	t.Helper()
	g, err := f.OpenGroup(path)
	require.NoError(t, err)
	oh, err := g.header()
	require.NoError(t, err)
	return f.gObjGetNameByIdx(oh, idx, order, n)
}

// TestMutationGroupNameByIdx: H5Lget_name_by_idx on the dense root group
// of testdata/fixtures/root_crt_order/main.h5 (19 links, name and
// creation order indexes) and on the compact group /ordered of
// testdata/fixtures/default/main.h5 (6 links). libhdf5 looks the native
// order up in the index of the field (h5g_dense.go:323): the fourth link
// is "ordered" by name hash and "layouts" by creation order; and it
// fails on the index past the last link in every index and order
// (h5g_dense.go:384, h5g_compact.go:51).
func TestMutationGroupNameByIdx(t *testing.T) {
	f := openTestdata(t, "fixtures/root_crt_order/main.h5")
	name, err := nameByIdx(t, f, "/", h5IndexName, h5IterNative, 3)
	require.NoError(t, err)
	assert.Equal(t, "ordered", name)
	name, err = nameByIdx(t, f, "/", h5IndexCrtOrder, h5IterNative, 3)
	require.NoError(t, err)
	assert.Equal(t, "layouts", name)
	g := openTestdata(t, "fixtures/default/main.h5")
	for _, idx := range []h5Index{h5IndexName, h5IndexCrtOrder} {
		for _, order := range []h5IterOrder{h5IterInc, h5IterDec, h5IterNative} {
			_, err := nameByIdx(t, f, "/", idx, order, 19)
			assert.Error(t, err, "%v %v", idx, order)
			_, err = nameByIdx(t, g, "/ordered", idx, order, 6)
			assert.Error(t, err, "%v %v", idx, order)
		}
	}
	name, err = nameByIdx(t, g, "/ordered", h5IndexName, h5IterDec, 5)
	require.NoError(t, err)
	assert.Equal(t, "alpha", name)
}

// TestMutationGroupVisitCreationOrder: H5Lvisit2 in creation order from
// the root groups of testdata/fixtures/default/main.h5 and
// testdata/fixtures/latest/main.h5 (groups that do not track the creation
// order of their links, old-style subgroups) and of
// testdata/stab_default.h5 (an old-style root group): libhdf5 visits the
// groups that do not track creation orders in name order (h5g_int.go:189,
// 198, 264, 273) and /many and /ordered, which track them, in creation
// order; 98 links in all in the first two files, 1003 in the third.
func TestMutationGroupVisitCreationOrder(t *testing.T) {
	for _, name := range []string{"default", "latest"} {
		t.Run(name, func(t *testing.T) {
			f := openTestdata(t, "fixtures/"+name+"/main.h5")
			var paths, tracked []string
			require.NoError(t, gVisit(f.rootLoc(), ".", h5IndexCrtOrder, h5IterInc, func(_ *gLoc, path string, _ *h5oLink) error {
				paths = append(paths, path)
				if strings.HasPrefix(path, "ordered") || strings.HasPrefix(path, "many/g0") {
					tracked = append(tracked, path)
				}
				return nil
			}))
			assert.Len(t, paths, 98)
			assert.Equal(t, []string{"a", "a/b", "a/b/c", "commented", "dangling"}, paths[:5])
			assert.Equal(t, strings.Fields("many/g00 many/g07 many/g02 many/g09 many/g04 many/g06 many/g01 many/g08 many/g03 many/g05 "+
				"ordered ordered/zeta ordered/alpha ordered/mid ordered/beta ordered/data ordered/hard_f64"), tracked)
		})
	}
	t.Run("old_style_root", func(t *testing.T) {
		f := openTestdata(t, "stab_default.h5")
		var paths []string
		require.NoError(t, gVisit(f.rootLoc(), ".", h5IndexCrtOrder, h5IterInc, func(_ *gLoc, path string, _ *h5oLink) error {
			paths = append(paths, path)
			return nil
		}))
		require.Len(t, paths, 1003)
		assert.Equal(t, []string{"big", "big/link0000", "big/link0001"}, paths[:3])
		assert.Equal(t, "empty", paths[1002])
	})
}

// emptyGroupHdr is the object header of an empty new-style group.
func emptyGroupHdr() []byte { return ohdr2(2, v2Msgs(linfoMsg())) }

// udLinkMsg is a link message for a link of type typ (soft: 1,
// external: 64) whose value is val.
func udLinkMsg(name string, typ byte, val []byte) ohMsg {
	b := append([]byte{1, 0x08, typ, byte(len(name))}, name...)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(val)))
	return ohMsg{typ: h5oLinkID, data: append(b, val...)}
}

// elinkVal is the value of an external link to path in file (version 0,
// no flags).
func elinkVal(file, path string) []byte {
	return append(append(append([]byte{0}, file...), 0), append([]byte(path), 0)...)
}

// TestMutationGroupDenseEmpty: a group with dense link storage whose name
// index holds no record and whose fractal heap address points at bytes
// that are not a heap: H5G__dense_build_table opens nothing when the
// group has no link (h5g_dense.go:191), and libhdf5 lists no link.
func TestMutationGroupDenseEmpty(t *testing.T) {
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		h := append([]byte("BTHD"), 0, h5b2GrpDenseNameID)
		h = binary.LittleEndian.AppendUint32(h, 512)
		h = binary.LittleEndian.AppendUint16(h, 11)
		h = binary.LittleEndian.AppendUint16(h, 0)
		h = append(h, 100, 40)
		h = le64a(binary.LittleEndian.AppendUint16(le64a(h, haddrUndef), 0), 0)
		name := b.put(arrBlock(h))
		notHeap := b.alloc(64)
		linfo := ohMsg{typ: h5oLinfoID, data: le64a(le64a([]byte{0, 0}, notHeap), name)}
		return b.put(ohdr2(2, v2Msgs(linfo)))
	})
	g, err := f.OpenGroup("/obj")
	require.NoError(t, err)
	n, err := g.NumLinks()
	require.NoError(t, err)
	assert.Zero(t, n)
	for _, err := range g.Links() {
		require.NoError(t, err)
		t.Error("a link")
	}
}

// TestMutationGroupUnterminatedName: an old-style group /g whose only
// link, "abc", has its name at the end of the local heap's data segment
// ("\0abc") without a terminating NUL. libhdf5 compares names with
// strncmp up to the end of the data segment (H5G__node_cmp3,
// H5G__node_found) and opens /g/abc (h5g_node.go:84).
func TestMutationGroupUnterminatedName(t *testing.T) {
	b := newH5Builder()
	target := b.put(emptyGroupHdr())
	heap := b.alloc(32 + 8)
	b.set(heap, le64a(le64a(le64a(append([]byte("HEAP"), 0, 0, 0, 0), 4), 1), heap+32))
	b.set(heap+32, []byte("\x00abc"))
	snod := append([]byte("SNOD"), 1, 0, 1, 0)
	snod = append(le64a(le64a(snod, 1), target), make([]byte, 24)...)
	snod = append(snod, make([]byte, 7*40)...)
	snodAddr := b.put(snod)
	btree := b.put(treeNode(0, 0, haddrUndef, haddrUndef, [][]byte{le64a(nil, 0), le64a(nil, 1)}, []uint64{snodAddr}, snodeTreeSize))
	grp := b.put(v1Hdr(1, v1Msgs(ohMsg{typ: h5oStabID, data: le64a(le64a(nil, btree), heap)})))
	f := builtFile(t, b.finish(b.rootGroup(hardLinkMsg("g", grp))))
	obj, err := f.Open("/g/abc")
	require.NoError(t, err)
	info, err := obj.Info()
	require.NoError(t, err)
	assert.Equal(t, target, info.Address)
}

// TestMutationLinkValues: soft and external link values. libhdf5
// (H5Lget_val, H5Lexists, H5Lget_info2, H5Literate2, H5Oopen) reads the
// value of the external link /ext_group of
// testdata/fixtures/default/main.h5 (h5l.go:57), finds no link
// "no_such" (h5l.go:107); in a file whose root group holds a soft link
// to "" and an external link of version 1, it opens the soft link as the
// root group (h5g_traverse.go:213) and fails to get the value or the
// information of the external link, so iterating over the root group
// fails (h5l.go:63, h5g_link.go:100).
func TestMutationLinkValues(t *testing.T) {
	f := openTestdata(t, "fixtures/default/main.h5")
	val, err := f.lGetVal(f.rootLoc(), "ext_group")
	require.NoError(t, err)
	assert.Equal(t, []byte("\x00target.h5\x00/g\x00"), val)
	ok, err := f.lExists(f.rootLoc(), "no_such")
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = f.lExists(f.rootLoc(), "/")
	require.NoError(t, err)
	assert.True(t, ok)

	b := newH5Builder()
	bad := elinkVal("other.h5", "/x")
	bad[0] = 0x10
	root := b.rootGroup(udLinkMsg("empty", 1, []byte{0}), udLinkMsg("bad_ext", 64, bad))
	f = builtFile(t, b.finish(root))
	obj, err := f.Open("/empty")
	require.NoError(t, err)
	info, err := obj.Info()
	require.NoError(t, err)
	assert.Equal(t, root, info.Address)
	_, err = f.lGetVal(f.rootLoc(), "bad_ext")
	assert.ErrorContains(t, err, "bad version number for external link")
	_, err = f.Root().Link("bad_ext")
	assert.ErrorContains(t, err, "bad version number for external link")
	var lerr error
	for _, err := range f.Root().Links() {
		if err != nil {
			lerr = err
		}
	}
	assert.ErrorContains(t, lerr, "bad version number for external link")
}

// TestMutationExternalLinkChain: external links that lead to external
// links in the same file: /e01 -> /e02 -> ... -> /e17 -> /target, and
// /loop -> /loop. libhdf5 traverses 16 user-defined links at most
// (H5L_NUM_LINKS): it opens /e02 (16 links) but not /e01 (17 links,
// h5g_traverse.go:181) nor /loop (h5g_traverse.go:184).
func TestMutationExternalLinkChain(t *testing.T) {
	b := newH5Builder()
	target := b.put(emptyGroupHdr())
	links := []ohMsg{hardLinkMsg("target", target), udLinkMsg("loop", 64, elinkVal("elink_chain.h5", "/loop"))}
	for i := 1; i <= 17; i++ {
		to := fmt.Sprintf("/e%02d", i+1)
		if i == 17 {
			to = "/target"
		}
		links = append(links, udLinkMsg(fmt.Sprintf("e%02d", i), 64, elinkVal("elink_chain.h5", to)))
	}
	img := b.finish(b.put(ohdr2(2, v2Msgs(append([]ohMsg{linfoMsg()}, links...)...))))
	dir := t.TempDir()
	writeImage(t, dir, "elink_chain.h5", img)
	f, err := Open(filepath.Join(dir, "elink_chain.h5"))
	require.NoError(t, err)
	defer f.Close()
	obj, err := f.Open("/e02")
	require.NoError(t, err)
	info, err := obj.Info()
	require.NoError(t, err)
	assert.Equal(t, target, info.Address)
	_, err = f.Open("/e01")
	assert.ErrorContains(t, err, "too many links")
	_, err = f.Open("/loop")
	assert.ErrorContains(t, err, "too many links")
}
