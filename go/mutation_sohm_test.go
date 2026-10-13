package hdf5

// Tests for the shared object header message code (h5sm*.go) written from
// the mutants gremlins left alive, on testdata/sohm_edge.h5
// (testdata/gen_sohm_edge.c: a shared message table of 8 indexes, a full
// list index). The expectations are what libhdf5 1.14.6 reads from the
// file (h5dump, and h5debug for the table and the list).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMutationSharedMessageEdgeTable: the table of testdata/sohm_edge.h5
// has 8 indexes, the most there may be (h5sm.go:308, h5sm_cache.go:70),
// and its attribute index is a list of 4 messages, as many as it holds
// (h5sm_cache.go:183, 203, 243). h5debug sohm_edge.h5 88 and
// h5debug sohm_edge.h5 4324 88 report the indexes and the list below.
func TestMutationSharedMessageEdgeTable(t *testing.T) {
	f := openTestdata(t, "sohm_edge.h5")
	info, err := f.smGetInfo()
	require.NoError(t, err)
	assert.Equal(t, uint32(8), info.nindexes)
	assert.Equal(t, []uint32{h5oShmesgDtypeFlag, h5oShmesgSdspaceFlag, h5oShmesgAttrFlag, 0, 0, 0, 0, 0}, info.indexFlags)
	assert.Equal(t, uint32(4), info.listMax)
	assert.Equal(t, uint32(2), info.btreeMin)

	table, err := f.smTableProtect()
	require.NoError(t, err)
	require.Len(t, table.indexes, 8)
	header := &table.indexes[2]
	assert.Equal(t, h5smIndexList, header.indexType)
	assert.Equal(t, haddr(4324), header.indexAddr)
	assert.Equal(t, uint64(4), header.numMessages)
	list, err := f.smListProtect(header)
	require.NoError(t, err)
	require.Len(t, list.messages, 4)
	for i, want := range []struct {
		hash   uint32
		heapID uint64
	}{{408598346, 0x23000000001600}, {2592820264, 0x23000000003900}, {4142874035, 0x23000000005c00}, {4171113783, 0x23000000007f00}} {
		m := list.messages[i]
		assert.Equal(t, h5smInHeap, m.location, "message %d", i)
		assert.Equal(t, want.hash, m.hash, "message %d", i)
		assert.Equal(t, want.heapID, le64(m.heapLoc.fheapID[:]), "message %d", i)
		assert.Equal(t, uint64(1), m.heapLoc.refCount, "message %d", i)
	}

	ds, err := f.OpenDataset("/d3")
	require.NoError(t, err)
	vals, err := Read[int64](ds)
	require.NoError(t, err)
	assert.Equal(t, []int64{31, 32, 33, 34}, vals)
	d0, err := f.OpenDataset("/d0")
	require.NoError(t, err)
	n := 0
	for a, err := range d0.Attributes() {
		require.NoError(t, err)
		v, err := ReadAttribute[int32](a)
		require.NoError(t, err)
		assert.Equal(t, []int32{0}, v)
		n++
	}
	assert.Equal(t, 4, n)
}

// TestMutationSharedMessageCompare: H5SM__compare_cb orders a search key
// against a message in the shared message heap by encoded size first
// (h5sm_btree2.go:104): a shorter key is "less" (-1), a longer one
// "greater" (1), and keys of the same size compare as memcmp. (The read
// path of libhdf5 never searches an index; this is the comparison its
// write path uses.)
func TestMutationSharedMessageCompare(t *testing.T) {
	for _, c := range []struct {
		key, obj []byte
		want     int
	}{
		{[]byte{9}, []byte{1, 2}, -1},
		{[]byte{1, 2}, []byte{9}, 1},
		{[]byte{1, 2}, []byte{1, 3}, -1},
		{[]byte{1, 2}, []byte{1, 2}, 0},
	} {
		udata := h5smCompareUdata{key: &h5smMesgKey{encoding: c.key}}
		require.NoError(t, h5smCompareCb(c.obj, &udata))
		assert.Equal(t, c.want, udata.ret, "%v %v", c.key, c.obj)
	}
}
