package hdf5

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HDFGroup/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The expected values in these tests come from h5dump -H -p, h5ls -v and
// h5debug FILE ADDR (HDF5 1.14.6) run on the same files.

// openTestFile opens a corpus file given relative to the repository root.
func openTestFile(t *testing.T, rel string) *File {
	t.Helper()
	f, err := Open(filepath.Join(repoRoot(t), rel), WithExternalLinks(false))
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// lookupPath follows a slash-separated path of hard links from the root
// group through symbol tables and link messages, and returns the object
// header address.
func lookupPath(t *testing.T, f *File, path string) haddr {
	t.Helper()
	addr := f.sblock.rootAddr
	for _, name := range strings.Split(strings.Trim(path, "/"), "/") {
		if name == "" {
			continue
		}
		oh, err := f.oProtect(addr)
		require.NoError(t, err)
		found := false
		if f.msgExists(oh, h5oStabID) {
			v, err := f.msgRead(oh, h5oStabID)
			require.NoError(t, err)
			ent, ok, err := f.gStabLookup(v.(*h5oStab), name)
			require.NoError(t, err)
			if ok {
				addr, found = ent.header, true
			}
		}
		if !found {
			err := f.msgIterate(oh, h5oLinkID, func(_ *h5oMesg, native any) error {
				if l := native.(*h5oLink); l.name == name && l.typ == h5.LinkHard {
					addr, found = l.addr, true
					return errBIterStop
				}
				return nil
			})
			require.NoError(t, err)
		}
		require.True(t, found, "link %q not found in %q", name, path)
	}
	return addr
}

// readMsg loads the object header at addr and decodes its first message
// of the given type.
func readMsg[T any](t *testing.T, f *File, addr haddr, typeID uint16) T {
	t.Helper()
	oh, err := f.oProtect(addr)
	require.NoError(t, err)
	v, err := f.msgRead(oh, typeID)
	require.NoError(t, err)
	out, ok := v.(T)
	require.True(t, ok, "message %#x decoded as %T", typeID, v)
	return out
}

func TestLayoutMessages(t *testing.T) {
	f := openTestFile(t, "tools/test/h5dump/testfiles/tfilters.h5")

	// h5dump -H -p -d compact: COMPACT, SIZE 800.
	compact := lookupPath(t, f, "compact")
	assert.Equal(t, haddr(800), compact)
	lay := readMsg[*h5oLayout](t, f, compact, h5oLayoutID)
	assert.Equal(t, uint8(3), lay.version)
	assert.Equal(t, uint8(h5dCompact), lay.typ)
	assert.Equal(t, uint64(800), lay.compactSize)
	require.Len(t, lay.compactBuf, 800)

	// h5debug tfilters.h5 2200: version 3, contiguous, address 2472, size 800.
	contig := lookupPath(t, f, "contiguous")
	assert.Equal(t, haddr(2200), contig)
	lay = readMsg[*h5oLayout](t, f, contig, h5oLayoutID)
	assert.Equal(t, uint8(3), lay.version)
	assert.Equal(t, uint8(h5dContiguous), lay.typ)
	assert.Equal(t, haddr(2472), lay.contigAddr)
	assert.Equal(t, uint64(800), lay.contigSize)

	// h5dump: CHUNKED ( 10, 5 ); h5debug: {10, 5, 4}, v1 B-tree index at 29656.
	all := lookupPath(t, f, "all")
	lay = readMsg[*h5oLayout](t, f, all, h5oLayoutID)
	assert.Equal(t, uint8(h5dChunked), lay.typ)
	assert.Equal(t, uint32(3), lay.chunkNdims)
	assert.Equal(t, []uint64{10, 5, 4}, lay.chunkDim)
	assert.Equal(t, uint8(h5dChunkIdxBtree), lay.chunkIdxType)
	assert.Equal(t, haddr(29656), lay.chunkIdxAddr)

	// Version 1 layout message (fill_old.h5 /dset2, h5debug 1576): contiguous
	// at 2304; the message stores the dimensions plus the element size.
	g := openTestFile(t, "test/testfiles/fill_old.h5")
	lay = readMsg[*h5oLayout](t, g, lookupPath(t, g, "dset2"), h5oLayoutID)
	assert.Equal(t, uint8(1), lay.version)
	assert.Equal(t, uint8(h5dContiguous), lay.typ)
	assert.Equal(t, haddr(2304), lay.contigAddr)
	assert.Equal(t, uint32(3), lay.ndims)
	assert.Equal(t, []uint64{8, 8, 4}, lay.dim)
}

func TestLayoutVersion4Indexes(t *testing.T) {
	// h5ls -rv / h5debug test/testfiles/h5fc_ext1_f.h5: version 4 chunked
	// layouts {2, 3, 4} with an implicit index (DSET_NONE at 5846, index
	// 2384), a fixed array (DSET_FA at 4940, index 2062), an extensible
	// array (GROUP/DSET_NDATA_EA at 715, no index allocated) and a v2 B-tree (DSET_NDATA_BT2 at 1653, no
	// index allocated). h5debug does not print the index creation
	// parameters; the extensible and fixed array ones are the library
	// defaults (H5D_EARRAY_*, H5D_FARRAY_* in H5Dpkg.h).
	f := openTestFile(t, "test/testfiles/h5fc_ext1_f.h5")
	assert.Equal(t, haddr(5846), lookupPath(t, f, "DSET_NONE"))
	tests := []struct {
		name    string
		addr    haddr
		idx     uint8
		idxAddr haddr
	}{
		{"DSET_NONE", 5846, h5dChunkIdxNone, 2384},
		{"DSET_FA", 4940, h5dChunkIdxFarray, 2062},
		{"GROUP/DSET_NDATA_EA", 715, h5dChunkIdxEarray, haddrUndef},
		{"DSET_NDATA_BT2", 1653, h5dChunkIdxBt2, haddrUndef},
	}
	for _, tt := range tests {
		lay := readMsg[*h5oLayout](t, f, tt.addr, h5oLayoutID)
		assert.Equal(t, uint8(4), lay.version, tt.name)
		assert.Equal(t, uint8(h5dChunked), lay.typ, tt.name)
		assert.Equal(t, uint32(3), lay.chunkNdims, tt.name)
		assert.Equal(t, []uint64{2, 3, 4}, lay.chunkDim, tt.name)
		assert.Equal(t, tt.idx, lay.chunkIdxType, tt.name)
		assert.Equal(t, tt.idxAddr, lay.chunkIdxAddr, tt.name)
		switch tt.idx {
		case h5dChunkIdxBt2:
			assert.Equal(t, h5b2CreateParams{nodeSize: 512, splitPercent: 100, mergePercent: 40}, lay.bt2Cparam, tt.name)
		case h5dChunkIdxEarray:
			assert.Equal(t, h5eaCreateParams{maxNelmtsBits: 32, idxBlkElmts: 4, supBlkMinDataPtrs: 4, dataBlkMinElmts: 16, maxDblkPageNelmtsBits: 10}, lay.earrayCparam, tt.name)
		case h5dChunkIdxFarray:
			assert.Equal(t, uint8(10), lay.farrayMaxDblkPageNelmtsBits, tt.name)
		}
	}
}

func TestFilterPipelineMessage(t *testing.T) {
	f := openTestFile(t, "tools/test/h5dump/testfiles/tfilters.h5")

	// h5ls -v tfilters.h5/all:
	//   shuffle-2 OPT {4}, szip-4 OPT {141, 4, 32, 5}, deflate-1 OPT {5},
	//   fletcher32-3 {}, nbit-5 OPT {8, 1, 50, 1, 4, 0, 32, 0}
	pline := readMsg[*h5oPline](t, f, lookupPath(t, f, "all"), h5oPlineID)
	require.Equal(t, 5, pline.nused)
	require.Len(t, pline.filter, 5)
	type flt struct {
		id    int32
		flags uint16
		cd    []uint32
	}
	want := []flt{
		{h5zFilterShuffle, h5zFlagOptional, []uint32{4}},
		{h5zFilterSzip, h5zFlagOptional, []uint32{141, 4, 32, 5}},
		{h5zFilterDeflate, h5zFlagOptional, []uint32{5}},
		{h5zFilterFletcher32, h5zFlagMandatory, nil},
		{h5zFilterNbit, h5zFlagOptional, []uint32{8, 1, 50, 1, 4, 0, 32, 0}},
	}
	for i, w := range want {
		assert.Equal(t, w.id, pline.filter[i].id, i)
		assert.Equal(t, w.flags, pline.filter[i].flags, i)
		assert.Equal(t, w.cd, pline.filter[i].cdValues, i)
	}

	// A user-defined filter keeps its name: myfilter-405 {5, 6}.
	pline = readMsg[*h5oPline](t, f, lookupPath(t, f, "myfilter"), h5oPlineID)
	require.Len(t, pline.filter, 1)
	assert.Equal(t, int32(405), pline.filter[0].id)
	assert.Equal(t, "myfilter", pline.filter[0].name)
	assert.Equal(t, []uint32{5, 6}, pline.filter[0].cdValues)
}

func TestFillValueMessages(t *testing.T) {
	// h5debug tfilters.h5 29336: fill_new, alloc time incremental, fill
	// time "if set", default value (size 0).
	f := openTestFile(t, "tools/test/h5dump/testfiles/tfilters.h5")
	fill := readMsg[*h5oFill](t, f, lookupPath(t, f, "all"), h5oFillNewID)
	assert.Equal(t, uint8(h5dAllocTimeIncr), fill.allocTime)
	assert.Equal(t, uint8(h5dFillTimeIfset), fill.fillTime)
	assert.True(t, fill.fillDefined)
	assert.Empty(t, fill.buf)

	// h5dump -H -p fill_old.h5: dset2 has FILL_TIME IFSET, VALUE 4444,
	// ALLOCATION_TIME LATE, stored in an old fill value message (0x0004)
	// whose size is checked against the big-endian int datatype.
	g := openTestFile(t, "test/testfiles/fill_old.h5")
	old := readMsg[*h5oFill](t, g, lookupPath(t, g, "dset2"), h5oFillID)
	assert.Equal(t, uint8(h5oFillVersion2), old.version)
	assert.Equal(t, uint8(h5dAllocTimeLate), old.allocTime)
	assert.Equal(t, uint8(h5dFillTimeIfset), old.fillTime)
	assert.True(t, old.fillDefined)
	require.Equal(t, int64(4), old.size)
	assert.Equal(t, uint32(4444), binary.BigEndian.Uint32(old.buf))
}

func TestCommentAndMtimeMessages(t *testing.T) {
	// h5ls -v tfilters.h5: Comment: "This is a dataset with contiguous storage";
	// h5debug: mtime_new 2024-03-24 15:03:29 PDT.
	f := openTestFile(t, "tools/test/h5dump/testfiles/tfilters.h5")
	addr := lookupPath(t, f, "contiguous")
	oh, err := f.oProtect(addr)
	require.NoError(t, err)
	comment, ok, err := f.oGetComment(oh)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "This is a dataset with contiguous storage", comment)
	pdt := time.Date(2024, 3, 24, 22, 3, 29, 0, time.UTC).Unix()
	assert.Equal(t, h5oMtime(pdt), readMsg[h5oMtime](t, f, addr, h5oMtimeNewID))

	// Version 1 headers report the modification time message as the
	// change time.
	info, err := f.oGetInfo(oh)
	require.NoError(t, err)
	assert.Equal(t, pdt, info.CTime)
	assert.Zero(t, info.MTime)

	// Old-style mtime message (h5debug fill_old.h5 1576: 2002-02-28 07:24:32 PST).
	g := openTestFile(t, "test/testfiles/fill_old.h5")
	assert.Equal(t, h5oMtime(time.Date(2002, 2, 28, 15, 24, 32, 0, time.UTC).Unix()),
		readMsg[h5oMtime](t, g, lookupPath(t, g, "dset2"), h5oMtimeID))
}

func TestExternalFileListMessage(t *testing.T) {
	// h5debug tfilters.h5 35160: heap 712, 2/2 slots, ext1.bin (offset 8)
	// and ext2.bin (offset 24), 200 bytes each at file offset 0.
	f := openTestFile(t, "tools/test/h5dump/testfiles/tfilters.h5")
	efl := readMsg[*h5oEfl](t, f, lookupPath(t, f, "external"), h5oEflID)
	assert.Equal(t, haddr(712), efl.heapAddr)
	assert.Equal(t, 2, efl.nalloc)
	assert.Equal(t, 2, efl.nused)
	require.Len(t, efl.slot, 2)
	assert.Equal(t, h5oEflEntry{nameOffset: 8, name: "ext1.bin", offset: 0, size: 200}, efl.slot[0])
	assert.Equal(t, h5oEflEntry{nameOffset: 24, name: "ext2.bin", offset: 0, size: 200}, efl.slot[1])
}

func TestDataspaceMessage(t *testing.T) {
	f := openTestFile(t, "tools/test/h5dump/testfiles/tfilters.h5")
	ds := readMsg[*h5sExtent](t, f, lookupPath(t, f, "external"), h5oSdspaceID)
	assert.Equal(t, h5.SpaceSimple, ds.typ)
	assert.Equal(t, uint32(1), ds.rank)
	assert.Equal(t, []uint64{100}, ds.size)
	assert.Equal(t, uint64(100), ds.nelem)

	// h5dump tnullspace.ddl: DATASPACE NULL (version 2 dataspace message).
	g := openTestFile(t, "tools/test/h5dump/testfiles/tnullspace.h5")
	ds = readMsg[*h5sExtent](t, g, lookupPath(t, g, "dset"), h5oSdspaceID)
	assert.Equal(t, h5.SpaceNull, ds.typ)
	assert.Equal(t, uint8(2), ds.version)
	assert.Zero(t, ds.nelem)
}

func TestLinkInfoGroupInfoAndLinkMessages(t *testing.T) {
	// h5debug tordergr.h5 96: linfo (track creation order, max 2), ginfo
	// (defaults), hard links "2" -> 328 (corder 0) and "1" -> 6944 (corder 1).
	f := openTestFile(t, "tools/test/h5dump/testfiles/tordergr.h5")
	root := f.sblock.rootAddr
	linfo := readMsg[*h5oLinfo](t, f, root, h5oLinfoID)
	assert.True(t, linfo.trackCorder)
	assert.False(t, linfo.indexCorder)
	assert.Equal(t, int64(2), linfo.maxCorder)
	assert.Equal(t, haddrUndef, linfo.fheapAddr)
	assert.Equal(t, haddrUndef, linfo.nameBt2Addr)
	assert.Equal(t, haddrUndef, linfo.corderBt2Addr)

	ginfo := readMsg[*h5oGinfo](t, f, root, h5oGinfoID)
	assert.Equal(t, h5oGinfo{maxCompact: 8, minDense: 6, estNumEntries: 4, estNameLen: 8}, *ginfo)

	oh, err := f.oProtect(root)
	require.NoError(t, err)
	var links []h5oLink
	require.NoError(t, f.msgIterate(oh, h5oLinkID, func(_ *h5oMesg, native any) error {
		links = append(links, *native.(*h5oLink))
		return nil
	}))
	require.Len(t, links, 2)
	assert.Equal(t, h5oLink{typ: h5.LinkHard, corderValid: true, corder: 0, name: "2", addr: 328}, links[0])
	assert.Equal(t, h5oLink{typ: h5.LinkHard, corderValid: true, corder: 1, name: "1", addr: 6944}, links[1])
	assert.Equal(t, 2, f.msgCount(oh, h5oLinkID))
	n, err := f.oGetNlinks(root)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)

	info, err := f.oGetInfo(oh)
	require.NoError(t, err)
	assert.Equal(t, h5.ObjectGroup, info.Type)
}

func TestSoftExternalAndUserDefinedLinks(t *testing.T) {
	links := func(f *File) map[string]*h5oLink {
		oh, err := f.oProtect(f.sblock.rootAddr)
		require.NoError(t, err)
		m := map[string]*h5oLink{}
		require.NoError(t, f.msgIterate(oh, h5oLinkID, func(_ *h5oMesg, native any) error {
			l := native.(*h5oLink)
			m[l.name] = l
			return nil
		}))
		return m
	}

	// textlink.ddl: extlink1 -> filename:objname, extlink2 -> anotherfile:anotherobj.
	ext := links(openTestFile(t, "tools/test/h5dump/testfiles/textlink.h5"))
	require.Contains(t, ext, "extlink1")
	l := ext["extlink1"]
	assert.Equal(t, h5.LinkExternal, l.typ)
	assert.Equal(t, len(l.udData), l.udSize)
	assert.Equal(t, "\x00filename\x00objname\x00", string(l.udData))
	assert.Equal(t, "\x00anotherfile\x00anotherobj\x00", string(ext["extlink2"].udData))

	// tudlink-1.ddl: udlink1 and udlink2 of link class 187; h5debug shows
	// user data sizes 0 and 4.
	ud := links(openTestFile(t, "tools/test/h5dump/testfiles/tudlink.h5"))
	require.Contains(t, ud, "udlink1")
	assert.Equal(t, h5.LinkType(187), ud["udlink1"].typ)
	assert.Equal(t, 0, ud["udlink1"].udSize)
	assert.Equal(t, 4, ud["udlink2"].udSize)

}

func TestAttributeMessages(t *testing.T) {
	// tattr-*.ddl / h5debug tattr.h5 928: version 1 attributes "/attr1"
	// (H5T_STD_I8BE {24}), "attr2" (H5T_STD_I32BE {10}, 1..10), "attr3"
	// (H5T_IEEE_F64BE {10}), "attr4" (H5T_STD_I32BE scalar, 100).
	f := openTestFile(t, "tools/test/h5dump/testfiles/tattr.h5")
	oh, err := f.oProtect(f.sblock.rootAddr)
	require.NoError(t, err)
	attrs := map[string]*h5oAttr{}
	require.NoError(t, f.msgIterate(oh, h5oAttrID, func(_ *h5oMesg, native any) error {
		a := native.(*h5oAttr)
		attrs[a.name] = a
		return nil
	}))
	require.Contains(t, attrs, "/attr1")
	a1 := attrs["/attr1"]
	assert.Equal(t, uint8(1), a1.version)
	assert.Equal(t, h5.ClassInteger, a1.dt.Class)
	assert.Equal(t, uint64(1), a1.dt.Size)
	assert.Equal(t, []uint64{24}, a1.ds.size)
	assert.Equal(t, "attribute of root group\x00", string(a1.data))

	a2 := attrs["attr2"]
	require.NotNil(t, a2)
	assert.Equal(t, h5.OrderBE, a2.dt.Order)
	assert.Equal(t, uint64(4), a2.dt.Size)
	assert.Equal(t, uint64(40), a2.dataSize)
	for i := 0; i < 10; i++ {
		assert.Equal(t, uint32(i+1), binary.BigEndian.Uint32(a2.data[4*i:]))
	}

	a3 := attrs["attr3"]
	require.NotNil(t, a3)
	assert.Equal(t, h5.ClassFloat, a3.dt.Class)
	assert.Equal(t, uint64(80), a3.dataSize)

	a4 := attrs["attr4"]
	require.NotNil(t, a4)
	assert.Equal(t, h5.SpaceScalar, a4.ds.typ)
	assert.Equal(t, uint32(100), binary.BigEndian.Uint32(a4.data))

	info, err := f.oGetInfo(oh)
	require.NoError(t, err)
	assert.Equal(t, uint64(len(attrs)), info.NumAttrs)
}

func TestSharedCommittedDatatype(t *testing.T) {
	// h5ls -rv tnamed_dtype_attr.h5: /Datatype at 800 (5 links); /Dataset at
	// 1256 and its scalar attribute "Attribute" use the committed datatype
	// (shared-1:800, native int); h5debug 1256: mtime_new 2008-10-15
	// 14:11:21 PDT.
	f := openTestFile(t, "tools/test/h5dump/testfiles/tnamed_dtype_attr.h5")
	dsetAddr := lookupPath(t, f, "Dataset")
	assert.Equal(t, haddr(1256), dsetAddr)
	dt := readMsg[*h5.Datatype](t, f, dsetAddr, h5oDtypeID)
	assert.Equal(t, h5.ClassInteger, dt.Class)
	assert.Equal(t, uint64(4), dt.Size)

	oh, err := f.oProtect(dsetAddr)
	require.NoError(t, err)
	flags, err := f.msgGetFlags(oh, h5oDtypeID)
	require.NoError(t, err)
	assert.NotZero(t, flags&h5oMsgFlagShared)

	a := readMsg[*h5oAttr](t, f, dsetAddr, h5oAttrID)
	assert.Equal(t, "Attribute", a.name)
	require.NotNil(t, a.dtShared)
	assert.Equal(t, uint8(h5oShareTypeCommitted), a.dtShared.typ)
	assert.Equal(t, haddr(800), a.dtShared.oh)
	assert.Equal(t, uint64(4), a.dt.Size)

	typ, err := f.oObjType(800)
	require.NoError(t, err)
	assert.Equal(t, h5.ObjectNamedDatatype, typ)
	rc, typ, err := f.oGetRcAndType(800)
	require.NoError(t, err)
	assert.Equal(t, uint32(5), rc)
	assert.Equal(t, h5.ObjectNamedDatatype, typ)

	info, err := f.oGetInfoByAddr(dsetAddr)
	require.NoError(t, err)
	ctime := time.Date(2008, 10, 15, 21, 11, 21, 0, time.UTC).Unix()
	assert.Equal(t, h5.ObjectInfo{Address: 1256, Type: h5.ObjectDataset, RefCount: 1, HeaderVersion: 1, NumAttrs: 1, CTime: ctime}, info)
}

func TestContinuationChunks(t *testing.T) {
	// h5debug h5fc_ext_none.h5 48: version 2 header with 4 chunks
	// (48: 120 bytes of messages after the 27-byte prefix, 1923: 50,
	// 1848: 37, 7618: 52), 11 messages, timestamps 2017-01-27 22:30:06 PST.
	f := openTestFile(t, "tools/test/h5format_convert/testfiles/h5fc_ext_none.h5")
	oh, err := f.oProtect(48)
	require.NoError(t, err)
	assert.Equal(t, uint8(2), oh.version)
	assert.Equal(t, []h5oChunk{{addr: 48, size: 120 + 27}, {addr: 1923, size: 50}, {addr: 1848, size: 37}, {addr: 7618, size: 52}}, oh.chunks)
	require.Len(t, oh.mesgs, 11)
	wantTypes := []uint16{h5oLinfoID, h5oGinfoID, h5oContID, h5oLinkID, h5oLinkID, h5oContID, h5oContID, h5oLinkID, h5oLinkID, h5oLinkID, h5oLinkID}
	wantChunk := []int{0, 0, 0, 0, 0, 0, 1, 1, 2, 3, 3}
	for i, m := range oh.mesgs {
		assert.Equal(t, wantTypes[i], m.typeID, i)
		assert.Equal(t, wantChunk[i], m.chunkno, i)
	}
	// Raw message data (offset, size) in chunk: linfo (27, 18), link (8, 25) in chunk 2.
	assert.Equal(t, uint64(48+27), oh.mesgs[0].rawOff)
	assert.Len(t, oh.mesgs[0].raw, 18)
	assert.Equal(t, uint64(1848+8), oh.mesgs[8].rawOff)
	assert.Len(t, oh.mesgs[8].raw, 25)
	cont := readMsg[*h5oCont](t, f, 48, h5oContID)
	assert.Equal(t, h5oCont{addr: 1923, size: 50}, *cont)

	var addrs []haddr
	require.NoError(t, f.msgIterate(oh, h5oLinkID, func(_ *h5oMesg, native any) error {
		addrs = append(addrs, native.(*h5oLink).addr)
		return nil
	}))
	assert.Equal(t, []haddr{342, 910, 6428, 1564, 195}, addrs[:5])

	pst := time.Date(2017, 1, 28, 6, 30, 6, 0, time.UTC).Unix()
	info, err := f.oGetInfo(oh)
	require.NoError(t, err)
	assert.Equal(t, h5.ObjectInfo{Address: 48, Type: h5.ObjectGroup, RefCount: 1, HeaderVersion: 2, ATime: pst, MTime: pst, CTime: pst, BTime: pst}, info)

	hdr := h5oGetHdrInfoReal(oh)
	assert.Equal(t, 4, hdr.nchunks)
	assert.Equal(t, 11, hdr.nmesgs)
	assert.Equal(t, uint64(147+50+37+52), hdr.space.total)
	assert.Equal(t, hdr.space.total, hdr.space.free+hdr.space.meta+hdr.space.mesg)

	// Version 1 header with continuation chunks (h5debug tall.h5 3568:
	// chunks 3568 (24 bytes after the 16-byte prefix), 6168 (56), 8544 (88)).
	g := openTestFile(t, "tools/test/h5dump/testfiles/tall.h5")
	oh, err = g.oProtect(3568)
	require.NoError(t, err)
	assert.Equal(t, uint8(1), oh.version)
	assert.Equal(t, []h5oChunk{{addr: 3568, size: 40}, {addr: 6168, size: 56}, {addr: 8544, size: 88}}, oh.chunks)
	hdr = h5oGetHdrInfoReal(oh)
	assert.Equal(t, hdr.space.total, hdr.space.free+hdr.space.meta+hdr.space.mesg)
}

func TestSuperblockExtension(t *testing.T) {
	// h5debug h5fc_ext3_isf.h5: indexed storage internal node 1/2 rank 64
	// (from the v1 B-tree 'K' message), shared message table at 97 with 4
	// indexes, file space info message.
	f := openTestFile(t, "tools/test/h5format_convert/testfiles/h5fc_ext3_isf.h5")
	assert.Equal(t, uint(64), f.btreeK[h5bChunkID])
	assert.Equal(t, uint(16), f.btreeK[h5bSnodeID])
	assert.Equal(t, uint(4), f.symLeafK)
	sb := f.Superblock()
	assert.Equal(t, uint(64), sb.ChunkBtreeK)
	require.IsType(t, &h5oShmesgTable{}, f.sohm)
	assert.Equal(t, h5oShmesgTable{addr: 97, version: 0, nindexes: 4}, *f.sohm.(*h5oShmesgTable))

	ext, err := f.oProtect(f.sblock.extAddr)
	require.NoError(t, err)
	btreek := readMsg[*h5oBtreek](t, f, f.sblock.extAddr, h5oBtreekID)
	assert.Equal(t, h5oBtreek{btreeK: [2]uint{16, 64}, symLeafK: 4}, *btreek)
	assert.True(t, f.msgExists(ext, h5oFsinfoID))

	// file_space.ddl: FILE_SPACE_STRATEGY H5F_FSPACE_STRATEGY_NONE,
	// FREE_SPACE_PERSIST FALSE, FREE_SPACE_SECTION_THRESHOLD 1,
	// FILE_SPACE_PAGE_SIZE 8192.
	g := openTestFile(t, "tools/test/h5dump/testfiles/file_space.h5")
	fs := readMsg[*h5oFsinfo](t, g, g.sblock.extAddr, h5oFsinfoID)
	assert.Equal(t, uint8(h5oFsinfoVersion1), fs.version)
	assert.Equal(t, uint8(h5fFspaceStrategyNone), fs.strategy)
	assert.False(t, fs.persist)
	assert.Equal(t, uint64(1), fs.threshold)
	assert.Equal(t, uint64(8192), fs.pageSize)
}

func TestSmallMessageDecoders(t *testing.T) {
	f := &File{sizeofAddr: 8, sizeofSize: 8}

	// Reference count message: version 0, count.
	rc, err := h5oRefcountDecode(f, f.newImage([]byte{0, 7, 0, 0, 0}))
	require.NoError(t, err)
	assert.Equal(t, h5oRefcount(7), rc)
	_, err = h5oRefcountDecode(f, f.newImage([]byte{1, 7, 0, 0, 0}))
	assert.Error(t, err)
	_, err = h5oRefcountDecode(f, f.newImage([]byte{0, 7}))
	assert.ErrorIs(t, err, ErrOverflow)

	// Driver info message: version 0, 8-byte name, 2-byte length, data.
	di, err := h5oDrvinfoDecode(f, f.newImage(append([]byte("\x00NCSAmult\x03\x00"), 1, 2, 3)))
	require.NoError(t, err)
	assert.Equal(t, h5oDrvinfo{name: "NCSAmult", buf: []byte{1, 2, 3}}, *di)
	_, err = h5oDrvinfoDecode(f, f.newImage([]byte("\x00NCSAmult\x00\x00")))
	assert.Error(t, err)

	// Metadata cache image message.
	mdci, err := h5oMdciDecode(f, f.newImage([]byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0}))
	require.NoError(t, err)
	assert.Equal(t, h5oMdci{addr: 1, size: 2}, *mdci)

	// Bogus message.
	b, err := h5oBogusDecode(f, f.newImage([]byte{0xef, 0xbe, 0xad, 0xde}))
	require.NoError(t, err)
	assert.Equal(t, uint32(h5oBogusValue), b.u)

	// Comment message.
	name, err := h5oNameDecode(f, f.newImage([]byte("hello\x00")))
	require.NoError(t, err)
	assert.Equal(t, h5oName("hello"), name)
	_, err = h5oNameDecode(f, f.newImage(nil))
	assert.Error(t, err)

	// Shared message headers: version 3 SOHM and committed, version 1.
	sh, err := h5oSharedDecodeHeader(f, f.newImage([]byte{3, 1, 1, 2, 3, 4, 5, 6, 7, 8}), h5oDtypeID)
	require.NoError(t, err)
	assert.Equal(t, uint8(h5oShareTypeSohm), sh.typ)
	assert.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7, 8}, sh.heapID)
	sh, err = h5oSharedDecodeHeader(f, f.newImage([]byte{1, 0, 0, 0, 0, 0, 0, 0, 9, 9, 9, 9, 9, 9, 9, 9, 0x20, 3, 0, 0, 0, 0, 0, 0}), h5oDtypeID)
	require.NoError(t, err)
	assert.Equal(t, uint8(h5oShareTypeCommitted), sh.typ)
	assert.Equal(t, haddr(800), sh.oh)

	// Soft link message (no soft links are stored in link messages in the
	// corpus): version 1, flags "store link type" + 1-byte name length.
	l, err := h5oLinkDecode(f, f.newImage([]byte{1, 0x08, 1, 3, 'a', 'b', 'c', 4, 0, '/', 'x', '/', 'y'}))
	require.NoError(t, err)
	assert.Equal(t, h5oLink{typ: h5.LinkSoft, name: "abc", softName: "/x/y"}, *l)
	_, err = h5oLinkDecode(f, f.newImage([]byte{1, 0x08, 1, 3, 'a', 'b', 'c', 9, 0, '/'}))
	assert.ErrorIs(t, err, ErrOverflow)

	// A SOHM-shared message goes to the shared message module.
	_, err = f.h5oSharedRead(nil, &h5oShared{typ: h5oShareTypeSohm, heapID: make([]byte, 8)}, h5oDtypeID)
	assert.ErrorIs(t, err, ErrUnsupported)
}

func TestObjectHeaderCorruption(t *testing.T) {
	f := openTestFile(t, "tools/test/h5dump/testfiles/tfilters.h5")
	// Addresses that are not object headers must fail cleanly.
	for _, addr := range []haddr{0, 1, 2472, f.eof() - 1, f.eof() + 100, haddrUndef} {
		_, err := f.oProtect(addr)
		assert.Error(t, err, addr)
	}

	// A refcount message in a version 1 header and unknown messages with
	// the "fail if unknown always" flag are rejected.
	img := func(msgs ...[]byte) []byte {
		var body []byte
		for _, m := range msgs {
			body = append(body, m...)
		}
		hdr := []byte{1, 0, byte(len(msgs)), 0, 1, 0, 0, 0}
		hdr = binary.LittleEndian.AppendUint32(hdr, uint32(len(body)))
		hdr = append(hdr, 0, 0, 0, 0)
		return append(hdr, body...)
	}
	msg := func(id uint16, flags uint8, data []byte) []byte {
		b := binary.LittleEndian.AppendUint16(nil, id)
		b = binary.LittleEndian.AppendUint16(b, uint16(len(data)))
		b = append(b, flags, 0, 0, 0)
		return append(b, data...)
	}
	g := &File{sizeofAddr: 8, sizeofSize: 8}
	decode := func(image []byte) (*h5o, error) {
		udata := &h5oCacheUd{common: h5oCommonCacheUd{f: g, contMsgInfo: &h5oContMsgs{}}}
		return g.h5oCacheDeserialize(image, udata)
	}

	oh, err := decode(img(msg(0x30, 0, make([]byte, 8)), msg(h5oNullID, 0, make([]byte, 8))))
	require.NoError(t, err)
	assert.Equal(t, uint16(h5oUnknownID), oh.mesgs[0].typeID)
	assert.Equal(t, h5oUnknown(0x30), oh.mesgs[0].native)

	_, err = decode(img(msg(0x30, h5oMsgFlagFailIfUnknownAlways, make([]byte, 8))))
	assert.Error(t, err)
	_, err = decode(img(msg(h5oRefcountID, 0, []byte{0, 2, 0, 0, 0, 0, 0, 0})))
	assert.Error(t, err)
	_, err = decode(img(msg(h5oStabID, h5oMsgFlagShareable, make([]byte, 16))))
	assert.Error(t, err, "unshareable class flagged shareable")
	_, err = decode(img(msg(h5oNullID, 0, make([]byte, 7))))
	assert.Error(t, err, "unaligned message")
	_, err = decode(img(msg(h5oNullID, 0, make([]byte, 8)))[:20])
	assert.Error(t, err, "truncated")
}

// TestObjectHeaderMutations decodes object headers of corrupted copies of
// corpus files (random byte changes inside the headers) and checks that
// corrupt input produces errors, never panics.
func TestObjectHeaderMutations(t *testing.T) {
	cases := []struct {
		file    string
		regions [][2]int // byte ranges holding object header chunks
	}{
		// h5debug: v2 header with 4 chunks.
		{"tools/test/h5format_convert/testfiles/h5fc_ext_none.h5", [][2]int{{48, 48 + 147}, {1923, 1923 + 50}, {1848, 1848 + 37}, {7618, 7618 + 52}}},
		// tfilters.h5: v1 headers of the "all", "external" and "compact" datasets.
		{"tools/test/h5dump/testfiles/tfilters.h5", [][2]int{{29336, 29336 + 336}, {35160, 35160 + 256}, {800, 800 + 936}}},
		// tnamed_dtype_attr.h5: shared datatypes and attributes.
		{"tools/test/h5dump/testfiles/tnamed_dtype_attr.h5", [][2]int{{800, 800 + 200}, {1256, 1256 + 272}}},
	}
	rng := rand.New(rand.NewSource(1))
	var loaded, failed, decoded, decodeErrs int
	for _, c := range cases {
		orig, err := os.ReadFile(filepath.Join(repoRoot(t), c.file))
		require.NoError(t, err)
		for iter := 0; iter < 2000; iter++ {
			buf := append([]byte(nil), orig...)
			for n := 1 + rng.Intn(4); n > 0; n-- {
				r := c.regions[rng.Intn(len(c.regions))]
				if r[1] > len(buf) {
					continue
				}
				i := r[0] + rng.Intn(r[1]-r[0])
				switch rng.Intn(3) {
				case 0:
					buf[i] = byte(rng.Intn(256))
				case 1:
					buf[i] ^= 1 << uint(rng.Intn(8))
				default:
					buf[i] = 0xff
				}
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s iteration %d: panic: %v", c.file, iter, r)
					}
				}()
				// Without checksum verification most changes to version 2
				// headers reach the message decoders.
				f, err := OpenReader(bytes.NewReader(buf), int64(len(buf)), WithExternalLinks(false), WithChecksumVerification(iter%2 == 0))
				if err != nil {
					return
				}
				defer f.Close()
				for _, r := range c.regions {
					oh, err := f.oProtect(haddr(r[0]))
					if err != nil {
						failed++
						continue
					}
					loaded++
					for _, m := range oh.mesgs {
						if _, err := f.h5oLoadNative(oh, m); err != nil {
							decodeErrs++
						} else {
							decoded++
						}
					}
					_, _ = f.oGetInfo(oh)
					_ = h5oGetHdrInfoReal(oh)
				}
			}()
		}
	}
	t.Logf("headers loaded %d, rejected %d; messages decoded %d, rejected %d", loaded, failed, decoded, decodeErrs)
	assert.NotZero(t, loaded)
	assert.NotZero(t, decodeErrs)
}
