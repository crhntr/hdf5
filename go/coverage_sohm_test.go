package hdf5

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Searching the v2 B-tree indexes of shared messages (H5SM__message_compare
// and its callbacks) in testdata/cover_sohm.h5, whose indexes are B-trees
// from the first message (testdata/gen/gen_cover.c): every message of an
// index is found by its encoding (as H5SM_find_in_list / H5B2_find look
// for a message being shared), by its heap ID, and a changed encoding with
// the same hash is not found.
func TestSharedMessageIndexSearch(t *testing.T) {
	f := openTestdata(t, "cover_sohm.h5")
	table, err := f.smTableProtect()
	require.NoError(t, err)
	require.Len(t, table.indexes, 2)

	var searched int
	for i := range table.indexes {
		header := &table.indexes[i]
		require.Equal(t, h5smIndexBtree, header.indexType, "index %d", i)
		recs, err := smIndexRecords(f, header)
		require.NoError(t, err)
		require.NotEmpty(t, recs)
		fheap, err := f.hfOpen(header.heapAddr)
		require.NoError(t, err)
		bt2, err := f.b2Open(header.indexAddr, h5smIndex(), h5smBt2CrtContext(f))
		require.NoError(t, err)

		for j := range recs {
			rec := &recs[j]
			require.Equal(t, h5smInHeap, rec.location)
			raw, err := h5smReadMesg(f, rec, fheap, nil)
			require.NoError(t, err)

			/* By encoding: hash, then the bytes in the heap */
			key := &h5smMesgKey{file: f, fheap: fheap, encoding: raw,
				message: h5smSohm{location: h5smInHeap, hash: rec.hash}}
			var got *h5smSohm
			found, err := bt2.find(key, func(r any) error {
				got = r.(*h5smSohm)
				return nil
			})
			require.NoError(t, err)
			require.True(t, found, "index %d record %d", i, j)
			assert.Equal(t, rec.heapLoc.fheapID, got.heapLoc.fheapID)

			/* By heap ID: no comparison of the encodings */
			byID := &h5smMesgKey{message: h5smSohm{location: h5smInHeap, hash: rec.hash, heapLoc: rec.heapLoc}}
			found, err = bt2.find(byID, nil)
			require.NoError(t, err)
			assert.True(t, found)

			/* Same hash, other bytes (longer, shorter, different) */
			for _, enc := range [][]byte{append(append([]byte(nil), raw...), 0), raw[:len(raw)-1], flipped(raw)} {
				other := &h5smMesgKey{file: f, fheap: fheap, encoding: enc,
					message: h5smSohm{location: h5smInHeap, hash: rec.hash}}
				found, err = bt2.find(other, nil)
				require.NoError(t, err)
				assert.False(t, found)
			}
			searched++
		}

		/* A hash larger and smaller than all */
		for _, hash := range []uint32{0, ^uint32(0)} {
			found, err := bt2.find(&h5smMesgKey{file: f, fheap: fheap, encoding: []byte{1},
				message: h5smSohm{location: h5smInHeap, hash: hash}}, nil)
			require.NoError(t, err)
			assert.False(t, found)
		}
	}
	assert.Equal(t, 1+12+12, searched, "the datatype, 12 dataspaces and 12 attributes")

	/* The comparison of a message in an object header (H5SM__compare_iter_op) */
	d, err := f.OpenDataset("/d03")
	require.NoError(t, err)
	oh, err := objHeader(d.f, d.oh, d.addr)
	require.NoError(t, err)
	var attrRaw []byte
	var attrIdx uint32
	for _, m := range oh.mesgs {
		if m.typeID == h5oAttrID {
			attrRaw = m.raw
			break
		}
	}
	require.NotNil(t, attrRaw)
	inOh := h5smSohm{location: h5smInOh, hash: 7, msgTypeID: h5oAttrID, mesgLoc: h5oMesgLoc{index: attrIdx, ohAddr: oh.chunks[0].addr}}
	cmp, err := h5smMessageCompare(&h5smMesgKey{file: f, encoding: attrRaw, message: h5smSohm{location: h5smInHeap, hash: 7}}, &inOh)
	require.NoError(t, err)
	assert.Zero(t, cmp)
	cmp, err = h5smMessageCompare(&h5smMesgKey{file: f, encoding: attrRaw[:1], message: h5smSohm{location: h5smInHeap, hash: 7}}, &inOh)
	require.NoError(t, err)
	assert.Equal(t, -1, cmp)
	cmp, err = h5smMessageCompare(&h5smMesgKey{message: inOh}, &inOh)
	require.NoError(t, err)
	assert.Zero(t, cmp, "same object header location")
	_, err = h5smMessageCompare(&h5smMesgKey{message: h5smSohm{hash: 7}}, &inOh)
	assert.Error(t, err, "no encoding to compare")
	inHeap := h5smSohm{location: h5smInHeap, hash: 7}
	inHeap.heapLoc.fheapID[1] = 1
	_, err = h5smMessageCompare(&h5smMesgKey{encoding: attrRaw, message: h5smSohm{hash: 7}}, &inHeap)
	assert.Error(t, err, "no heap")
}

func flipped(b []byte) []byte {
	out := append([]byte(nil), b...)
	out[len(out)/2] ^= 0xff
	return out
}
