package hdf5

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// smIndexRecords returns the records of a shared message index (a list or
// a v2 B-tree), in index order.
func smIndexRecords(f *File, header *h5smIndexHeader) ([]h5smSohm, error) {
	if header.numMessages == 0 && !addrDefined(header.indexAddr) {
		// (indexes are created when the first message is shared)
		return nil, nil
	}
	switch header.indexType {
	case h5smIndexList:
		list, err := f.smListProtect(header)
		if err != nil {
			return nil, err
		}
		return append([]h5smSohm(nil), list.messages[:header.numMessages]...), nil
	case h5smIndexBtree:
		bt2, err := f.b2Open(header.indexAddr, h5smIndex(), h5smBt2CrtContext(f))
		if err != nil {
			return nil, err
		}
		var recs []h5smSohm
		err = bt2.iterate(func(rec any) error {
			recs = append(recs, *rec.(*h5smSohm))
			return nil
		})
		return recs, err
	}
	return nil, fmt.Errorf("bad index type %d", header.indexType)
}

// smDecodeIndex reads and decodes every message of a shared message index,
// returning the number of messages by type.
func smDecodeIndex(f *File, header *h5smIndexHeader) (map[uint16]int, error) {
	recs, err := smIndexRecords(f, header)
	if err != nil {
		return nil, err
	}
	if uint64(len(recs)) != header.numMessages {
		return nil, fmt.Errorf("index has %d records, header says %d", len(recs), header.numMessages)
	}
	var fheap *h5hf
	if addrDefined(header.heapAddr) {
		if fheap, err = f.hfOpen(header.heapAddr); err != nil {
			return nil, err
		}
	}
	types := map[uint16]int{}
	for i := range recs {
		rec := &recs[i]
		raw, err := h5smReadMesg(f, rec, fheap, nil)
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
		var typeID uint16
		if rec.location == h5smInOh {
			typeID = uint16(rec.msgTypeID)
		} else {
			// Messages in the heap do not record their type: try the
			// types of the index.
			typeID = h5oNullID
			for _, id := range []uint16{h5oSdspaceID, h5oDtypeID, h5oFillNewID, h5oPlineID, h5oAttrID} {
				flag, _ := h5smTypeToFlag(id)
				if header.mesgTypes&flag == 0 {
					continue
				}
				if _, err := f.msgDecode(nil, id, raw); err == nil {
					typeID = id
					break
				}
			}
			if typeID == h5oNullID {
				return nil, fmt.Errorf("record %d: message decodes as none of the index's types", i)
			}
			// The same message read through smReadMessage.
			sh := h5smReconstitute(typeID, rec.heapLoc.fheapID)
			if _, err := f.smReadMessage(sh, typeID); err != nil {
				return nil, fmt.Errorf("record %d: %w", i, err)
			}
		}
		if _, err := f.msgDecode(nil, typeID, raw); err != nil {
			return nil, fmt.Errorf("record %d (type %#x): %w", i, typeID, err)
		}
		types[typeID]++
	}
	return types, nil
}

func TestSharedMessagesGenerated(t *testing.T) {
	f := openTestdata(t, "attr_sohm.h5")

	info, err := f.smGetInfo()
	require.NoError(t, err)
	assert.Equal(t, uint32(2), info.nindexes)
	assert.Equal(t, []uint32{h5oShmesgAttrFlag, h5oShmesgDtypeFlag | h5oShmesgSdspaceFlag}, info.indexFlags)
	assert.Equal(t, []uint32{0, 0}, info.minsizes)
	assert.Equal(t, uint32(8), info.listMax)
	assert.Equal(t, uint32(6), info.btreeMin)
	assert.True(t, info.storeMsgCrtIdx)

	table, err := f.smTableProtect()
	require.NoError(t, err)
	require.Len(t, table.indexes, 2)
	// 32 distinct attribute messages: a v2 B-tree index.
	assert.Equal(t, h5smIndexBtree, table.indexes[0].indexType)
	assert.Equal(t, uint64(32), table.indexes[0].numMessages)
	// 3 datatypes and 3 dataspaces: a list index.
	assert.Equal(t, h5smIndexList, table.indexes[1].indexType)
	assert.Equal(t, uint64(6), table.indexes[1].numMessages)

	types, err := smDecodeIndex(f, &table.indexes[0])
	require.NoError(t, err)
	assert.Equal(t, map[uint16]int{h5oAttrID: 32}, types)
	types, err = smDecodeIndex(f, &table.indexes[1])
	require.NoError(t, err)
	assert.Equal(t, map[uint16]int{h5oDtypeID: 3, h5oSdspaceID: 3}, types)

	for _, id := range []uint16{h5oAttrID, h5oDtypeID, h5oSdspaceID, h5oFillID} {
		ok, err := f.smTypeShared(id)
		require.NoError(t, err)
		assert.Equal(t, id != h5oFillID, ok, "type %#x", id)
	}
	addr, err := f.smGetFheapAddr(h5oDtypeID)
	require.NoError(t, err)
	assert.Equal(t, table.indexes[1].heapAddr, addr)
	_, err = f.smGetFheapAddr(h5oPlineID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.smGetFheapAddr(h5oLinkID)
	assert.Error(t, err)

	// Dense attributes, all stored in the shared message heap.
	oh := ohAt(t, f, "/dense")
	byName, byCorder := genAttrNames(30)
	attrs, err := objectAttrs(f, oh)
	require.NoError(t, err)
	require.Len(t, attrs, 30)
	for i, a := range attrs {
		assert.Equal(t, byName[i], a.name)
		require.NotNil(t, a.shared, a.name)
		assert.Equal(t, uint8(h5oShareTypeSohm), a.shared.typ, a.name)
		assert.Equal(t, byCorder[a.crtIdx], a.name)
		checkGenAttr(t, f, a)
	}
	names, _ := iterNames(t, f, oh, h5aIndexCrtOrder, h5aIterNative)
	assert.Equal(t, byCorder, names)
	for _, name := range []string{byName[0], byName[29], "attr_17"} {
		a, err := f.aOpenByName(oh, name)
		require.NoError(t, err)
		assert.Equal(t, uint8(h5oShareTypeSohm), a.shared.typ)
		checkGenAttr(t, f, a)
	}
	_, err = f.aOpenByName(oh, "attr_30")
	assert.ErrorIs(t, err, ErrNotFound)

	// Compact attributes shared in the heap (the first three are the
	// same messages as attributes of /dense).
	oh = ohAt(t, f, "/compact")
	attrs, err = objectAttrs(f, oh)
	require.NoError(t, err)
	var got []string
	for _, a := range attrs {
		got = append(got, a.name)
		require.NotNil(t, a.shared, a.name)
		assert.Equal(t, uint8(h5oShareTypeSohm), a.shared.typ, a.name)
	}
	assert.Equal(t, []string{"attr_00", "attr_07", "attr_14", "cmpd", "vlstr"}, got)
	for _, a := range attrs[:3] {
		checkGenAttr(t, f, a)
	}
	vals, err := f.aRead(attrs[3])
	require.NoError(t, err)
	assert.Equal(t, []any{
		h5.Compound{Fields: []h5.Field{{Name: "a", Value: int32(1)}, {Name: "b", Value: 0.5}}},
		h5.Compound{Fields: []h5.Field{{Name: "a", Value: int32(2)}, {Name: "b", Value: 1.5}}},
	}, vals)
	assert.Equal(t, uint64(2), attrs[3].ds.nelem)
	vals, err = f.aRead(attrs[4])
	require.NoError(t, err)
	assert.Equal(t, []any{"shared vlen string"}, vals)
	assert.True(t, attrs[4].dt.IsVlenString())

	// Dataset with a shared datatype, dataspace and attribute.
	oh = ohAt(t, f, "/dset")
	dt, err := f.msgRead(oh, h5oDtypeID)
	require.NoError(t, err)
	assert.Equal(t, h5.ClassInteger, dt.(*h5.Datatype).Class)
	ds, err := f.msgRead(oh, h5oSdspaceID)
	require.NoError(t, err)
	assert.Equal(t, []uint64{4, 5}, ds.(*h5sExtent).size)
	a, err := f.aOpenByName(oh, "attr_00")
	require.NoError(t, err)
	checkGenAttr(t, f, a)

	// Bad shared message references fail cleanly.
	_, err = f.smReadMessage(&h5oShared{typ: h5oShareTypeCommitted}, h5oDtypeID)
	assert.Error(t, err)
	_, err = f.smReadMessage(&h5oShared{typ: h5oShareTypeSohm, heapID: []byte{1, 2}}, h5oDtypeID)
	assert.Error(t, err)
	_, err = f.smReadMessage(&h5oShared{typ: h5oShareTypeSohm, heapID: make([]byte, 8)}, h5oDtypeID)
	assert.Error(t, err)
	_, err = f.smReadMessage(&h5oShared{typ: h5oShareTypeSohm, heapID: []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}}, h5oDtypeID)
	assert.Error(t, err)
	_, err = f.smReadMessage(&h5oShared{typ: h5oShareTypeSohm, heapID: make([]byte, 8)}, h5oPlineID)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestSharedMessagesCorpus loads the shared message table of every corpus
// file that has one and reads and decodes every message of every index.
func TestSharedMessagesCorpus(t *testing.T) {
	root := repoRoot(t)
	var nfiles, nlist, nbtree, nmesgs int
	for _, path := range corpusFiles(t) {
		rel, _ := filepath.Rel(root, path)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v\n%s", rel, r, debug.Stack())
				}
			}()
			f, err := Open(path, WithExternalLinks(false))
			if err != nil {
				return
			}
			defer f.Close()
			if f.sohmTable() == nil {
				return
			}
			nfiles++
			info, err := f.smGetInfo()
			if !assert.NoError(t, err, rel) {
				return
			}
			table, err := f.smTableProtect()
			require.NoError(t, err)
			for i := range table.indexes {
				header := &table.indexes[i]
				switch header.indexType {
				case h5smIndexList:
					nlist++
				case h5smIndexBtree:
					nbtree++
				}
				types, err := smDecodeIndex(f, header)
				if !assert.NoError(t, err, "%s: index %d", rel, i) {
					continue
				}
				for _, n := range types {
					nmesgs += n
				}
				if len(types) > 0 {
					t.Logf("%s: index %d (%#x, type %d): %v", rel, i, info.indexFlags[i], header.indexType, types)
				}
			}
		}()
	}
	t.Logf("shared messages: %d files, %d list indexes, %d B-tree indexes, %d messages", nfiles, nlist, nbtree, nmesgs)
	assert.Positive(t, nfiles)
}

func TestSharedMessageTableCorruption(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "attr_sohm.h5"))
	require.NoError(t, err)
	f := openTestdata(t, "attr_sohm.h5")
	sohm := f.sohmTable()
	require.NotNil(t, sohm)
	table, err := f.smTableProtect()
	require.NoError(t, err)
	listAddr := table.indexes[1].indexAddr

	// Every byte of the master table and of the list index, with and
	// without checksum verification.
	for _, region := range []struct {
		name string
		addr haddr
		size uint64
	}{
		{"table", sohm.addr, table.tableSize},
		{"list", listAddr, table.indexes[1].listSize},
	} {
		for off := uint64(0); off < region.size; off += uint64(quickStride(7)) {
			for _, verify := range []bool{false, true} {
				img := append([]byte(nil), data...)
				img[region.addr+off] ^= 0x5a
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("%s byte %d: panic: %v\n%s", region.name, off, r, debug.Stack())
						}
					}()
					g, err := OpenReader(bytes.NewReader(img), int64(len(img)), WithChecksumVerification(verify))
					if err != nil {
						return
					}
					defer g.Close()
					_, err = g.smGetInfo()
					if verify && region.name == "table" {
						assert.ErrorIs(t, err, ErrBadChecksum, "%s byte %d", region.name, off)
					}
					if verify && region.name == "list" && off < h5smListSize(g, table.indexes[1].numMessages) {
						_, err := g.smListProtect(&table.indexes[1])
						assert.ErrorIs(t, err, ErrBadChecksum, "%s byte %d", region.name, off)
					}
					exerciseAttributes(g)
					tbl, err := g.smTableProtect()
					if err == nil {
						for i := range tbl.indexes {
							_, _ = smDecodeIndex(g, &tbl.indexes[i])
						}
					}
				}()
			}
		}
	}

	// No table at all.
	g := openTestFile(t, "tools/test/h5dump/testfiles/tattr.h5")
	info, err := g.smGetInfo()
	require.NoError(t, err)
	assert.Zero(t, info.nindexes)
	ok, err := g.smTypeShared(h5oAttrID)
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = g.smTableProtect()
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrUnsupported))
}
