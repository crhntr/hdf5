package hdf5

// Tests for the object header messages (h5o_attr.go, h5o_attribute.go,
// h5o_fill.go, h5o_pline.go, h5o_mtime.go, h5o_int.go, h5o_fsinfo.go,
// h5o_efl.go, h5o_shared.go, h5o_sdspace.go, h5o_name.go) written from the
// mutants gremlins left alive. The objects are assembled byte by byte
// (see mutation_h5o_test.go) or come from the corpus; the expectations
// are what libhdf5 1.14.6 reads from the same bytes (H5Oget_info3,
// H5Oget_native_info, H5Oget_comment, H5Aiterate2, H5Aget_info_by_name,
// H5Dopen2, H5Dread, H5Pget_filter2, H5Pfill_value_defined; set
// HDF5_MUTATION_DUMP to a directory to have the tests write the files
// there).

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pad8 pads b with zeros to a multiple of 8 bytes.
func pad8(b []byte) []byte {
	b = append([]byte(nil), b...)
	for len(b)%8 != 0 {
		b = append(b, 0)
	}
	return b
}

// attrMsgRaw is an attribute message of the given version whose name is
// stored as rawName with the stored length nameLen, with the datatype,
// dataspace and data messages given (version 1 pads the name, datatype
// and dataspace to multiples of 8 bytes).
func attrMsgRaw(version byte, rawName []byte, nameLen int, dt, ds, data []byte) ohMsg {
	b := []byte{version, 0}
	b = binary.LittleEndian.AppendUint16(b, uint16(nameLen))
	b = binary.LittleEndian.AppendUint16(b, uint16(len(dt)))
	b = binary.LittleEndian.AppendUint16(b, uint16(len(ds)))
	if version >= 3 {
		b = append(b, 0) /* character set */
	}
	for _, part := range [][]byte{rawName, dt, ds} {
		if version == 1 {
			part = pad8(part)
		}
		b = append(b, part...)
	}
	return ohMsg{typ: h5oAttrID, data: append(b, data...)}
}

// scalarSpace is a version 2 dataspace message for a scalar dataspace.
var scalarSpace = []byte{2, 0, 0, 0}

// i32 encodes int32 values little-endian.
func i32(vs ...int32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, uint32(v))
	}
	return b
}

// attrValues lists the attributes of the object at path as "name=value".
func attrValues(t *testing.T, f *File, path string) ([]string, error) {
	t.Helper()
	oh := ohAt(t, f, path)
	var got []string
	err := f.oAttrIterateReal(oh, h5aIndexName, h5aIterInc, 0, nil, func(attr *h5oAttr) error {
		v, err := f.aRead(attr)
		require.NoError(t, err)
		got = append(got, fmt.Sprintf("%s=%v", attr.name, v[0]))
		return nil
	})
	return got, err
}

// groupWith opens a file whose root group links "obj" to a version 2
// group header holding msgs after its link info message.
func groupWith(t *testing.T, msgs ...ohMsg) *File {
	t.Helper()
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		return b.put(ohdr2(2, v2Msgs(append([]ohMsg{linfoMsg()}, msgs...)...)))
	})
	return f
}

// TestMutationAttrName: attribute names whose stored length does not end
// at their first NUL byte (H5O__attr_decode copies name_len - 1 bytes with
// H5MM_strndup and compares the length of the copy). libhdf5 reads the
// attribute stored as "abc" with length 3 as "ab" (h5o_attr.go:105), and
// fails on the one stored as "\0bc" (h5o_attr.go:106).
func TestMutationAttrName(t *testing.T) {
	t.Run("unterminated", func(t *testing.T) {
		f := groupWith(t, attrMsgRaw(3, []byte("abc"), 3, i32Type().data, scalarSpace, i32(5)))
		got, err := attrValues(t, f, "/obj")
		require.NoError(t, err)
		assert.Equal(t, []string{"ab=5"}, got)
	})
	t.Run("leading_nul", func(t *testing.T) {
		f := groupWith(t, attrMsgRaw(3, []byte("\x00bc"), 3, i32Type().data, scalarSpace, i32(5)))
		_, err := attrValues(t, f, "/obj")
		require.Error(t, err)
	})
}

// TestMutationAttrVersion2Dataspace: a version 2 attribute message does
// not pad its parts (h5o_attr.go:200): an int32 attribute {2} whose
// 12-byte (version 2) dataspace message is followed by its values 7, 9.
func TestMutationAttrVersion2Dataspace(t *testing.T) {
	f := groupWith(t, attrMsgRaw(2, []byte("v\x00"), 2, i32Type().data, spaceMsg(2).data, i32(7, 9)))
	g, err := f.OpenGroup("/obj")
	require.NoError(t, err)
	a, err := g.Attribute("v")
	require.NoError(t, err)
	got, err := ReadAttribute[int32](a)
	require.NoError(t, err)
	assert.Equal(t, []int32{7, 9}, got)
}

// TestMutationAttrCreationOrder: the creation order of an attribute
// opened by name (H5O__attr_open_cb, h5o_attribute.go:47): the stored
// creation index in a header that tracks the creation order of its
// attributes (H5Aget_info_by_name: corder 3 for "b"), the position of
// the message otherwise (corder 1 for "b").
func TestMutationAttrCreationOrder(t *testing.T) {
	for _, c := range []struct {
		name string
		hdr  []byte
		want uint32
	}{
		{"tracked", ohdr2(2|h5oHdrAttrCrtOrderTracked, v2MsgsCrt([]ohMsg{linfoMsg(), attrMsg("a", 1), attrMsg("b", 2)}, []uint16{0, 7, 3})), 3},
		{"untracked", ohdr2(2, v2Msgs(linfoMsg(), attrMsg("a", 1), attrMsg("b", 2))), 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, obj := buildObject(t, func(b *h5Builder) uint64 { return b.put(c.hdr) })
			oh, err := f.oProtect(obj)
			require.NoError(t, err)
			attr, err := f.oAttrOpenByName(oh, "b")
			require.NoError(t, err)
			assert.Equal(t, c.want, attr.crtIdx)
		})
	}
}

// compactLayout is a version 3 layout message for compact storage of
// data.
func compactLayout(data []byte) ohMsg {
	l := binary.LittleEndian.AppendUint16([]byte{3, 0}, uint16(len(data)))
	return layoutMsg(append(l, data...))
}

// TestMutationAttrVersion1Ainfo: an attribute info message in a version 1
// object header is ignored (H5O__attr_open_by_name, H5O_attr_iterate_real,
// H5O__attr_exists only look for one in later versions;
// h5o_attribute.go:64, 156, 247): the compact attribute a = 5 of an int32
// dataset {2} whose version 1 header also holds an attribute info message
// naming dense storage at an address that holds no fractal heap. libhdf5
// opens, lists and finds a.
func TestMutationAttrVersion1Ainfo(t *testing.T) {
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		notHeap := b.alloc(64)
		ainfo := ohMsg{typ: h5oAinfoID, data: le64a(le64a([]byte{0, 0}, notHeap), notHeap)}
		msgs := v1Msgs(i32Type(), spaceMsg(2), compactLayout(i32(1, 2)), ainfo, attrMsg("a", 5))
		return b.put(v1Hdr(5, msgs))
	})
	d, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	a, err := d.Attribute("a")
	require.NoError(t, err)
	v, err := ReadAttribute[int32](a)
	require.NoError(t, err)
	assert.Equal(t, []int32{5}, v)
	got, err := attrValues(t, f, "/obj")
	require.NoError(t, err)
	assert.Equal(t, []string{"a=5"}, got)
	ok, err := f.oAttrExists(d.oh, "a")
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestMutationAttrOpenByIdxNone: opening the first attribute of an object
// without attributes fails with a not-found error (H5O__attr_open_by_idx
// finds no attribute; h5o_attribute.go:132).
func TestMutationAttrOpenByIdxNone(t *testing.T) {
	f := groupWith(t)
	_, err := f.oAttrOpenByIdx(ohAt(t, f, "/obj"), h5aIndexName, h5aIterInc, 0)
	require.Error(t, err)
	assert.True(t, IsNotFound(err), "%v", err)
}

// TestMutationAttrIterateSkipAll: iterating over the attributes after
// skipping all of them is an error ("invalid index specified"; H5Aiterate2
// fails with idx 2 on a group with two compact attributes and with idx 30
// on /tracked of testdata/attr_dense.h5, 30 dense attributes;
// h5o_attribute.go:167, 188).
func TestMutationAttrIterateSkipAll(t *testing.T) {
	op := func(*h5oAttr) error { return nil }
	f := groupWith(t, attrMsg("a", 1), attrMsg("b", 2))
	require.NoError(t, f.oAttrIterateReal(ohAt(t, f, "/obj"), h5aIndexName, h5aIterInc, 1, nil, op))
	require.Error(t, f.oAttrIterateReal(ohAt(t, f, "/obj"), h5aIndexName, h5aIterInc, 2, nil, op))

	g := openTestdata(t, "attr_dense.h5")
	require.NoError(t, g.oAttrIterateReal(ohAt(t, g, "/tracked"), h5aIndexName, h5aIterInc, 29, nil, op))
	require.Error(t, g.oAttrIterateReal(ohAt(t, g, "/tracked"), h5aIndexName, h5aIterInc, 30, nil, op))
}

// datasetWith opens a file whose root group links "obj" to a version 2
// header of an int32 dataset {2} stored compactly (1, 2) that also holds
// msgs.
func datasetWith(t *testing.T, msgs ...ohMsg) *File {
	t.Helper()
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		return b.put(ohdr2(2, v2Msgs(append([]ohMsg{i32Type(), spaceMsg(2), compactLayout(i32(1, 2))}, msgs...)...)))
	})
	return f
}

// TestMutationFillMessages: fill value messages without a value. libhdf5
// reports the fill value as undefined (H5Pfill_value_defined) for a
// version 2 new-style message whose "defined" byte is 0
// (h5o_fill.go:78) and for an old-style message of size 0
// (h5o_fill.go:150, 173), and fails to open the dataset whose old-style
// message holds 2 bytes for its 4-byte datatype ("inconsistent fill value
// size", h5o_fill.go:157).
func TestMutationFillMessages(t *testing.T) {
	for _, c := range []struct {
		name string
		fill ohMsg
	}{
		{"new_v2_undefined", ohMsg{typ: h5oFillNewID, data: []byte{2, 1, 2, 0}}},
		{"old_empty", ohMsg{typ: h5oFillID, data: []byte{0, 0, 0, 0}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := datasetWith(t, c.fill).OpenDataset("/obj")
			require.NoError(t, err)
			fv, err := d.FillValue()
			require.NoError(t, err)
			assert.True(t, fv.Undefined)
			assert.False(t, fv.Defined)
			got, err := Read[int32](d)
			require.NoError(t, err)
			assert.Equal(t, []int32{1, 2}, got)
		})
	}
	t.Run("old_wrong_size", func(t *testing.T) {
		_, err := datasetWith(t, ohMsg{typ: h5oFillID, data: []byte{2, 0, 0, 0, 7, 7}}).OpenDataset("/obj")
		require.Error(t, err)
	})
}

// TestMutationPlineFilterNames: the names of filters with IDs from 256 on
// in a version 2 pipeline message, whose lengths need not be multiples of
// 8 (h5o_pline.go:75, 82): filter 256 named "abc" (name length 4) and
// filter 257 with an empty name, both skipped by the chunk's filter mask.
// libhdf5 lists them (H5Pget_filter2) and reads the stored values.
func TestMutationPlineFilterNames(t *testing.T) {
	d := filteredDataset(t, i32Type().data, 4, []uint64{2}, i32(4, 5), 3,
		plFilter{id: 256, name: []byte("abc\x00"), cd: []uint32{1}},
		plFilter{id: 257, flags: 1, name: []byte{0, 0, 0, 0}})
	filters, err := d.Filters()
	require.NoError(t, err)
	assert.Equal(t, []h5.Filter{{ID: 256, Name: "abc", CDValues: []uint32{1}}, {ID: 257, Flags: 1}}, filters)
	got, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{4, 5}, got)
}

// v1DatasetWith opens a file whose root group links "obj" to a version 1
// header of an int32 dataset {2} stored compactly (1, 2) that also holds
// msgs.
func v1DatasetWith(t *testing.T, msgs ...ohMsg) *File {
	t.Helper()
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		all := append([]ohMsg{i32Type(), spaceMsg(2), compactLayout(i32(1, 2))}, msgs...)
		return b.put(v1Hdr(uint16(len(all)), v1Msgs(all...)))
	})
	return f
}

// TestMutationOldMtime: the old-style modification time message
// (H5O__mtime_decode), which libhdf5 reports as the change time of a
// version 1 header (H5Oget_info3): "19871231235959" is 567993599, and a
// message with a character that is not a digit makes H5Oget_info3 fail
// (h5o_mtime.go:47, 55).
func TestMutationOldMtime(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		d, err := v1DatasetWith(t, ohMsg{typ: h5oMtimeID, data: []byte("19871231235959\x00\x00")}).OpenDataset("/obj")
		require.NoError(t, err)
		info, err := d.Info()
		require.NoError(t, err)
		assert.Equal(t, int64(567993599), info.CTime)
	})
	t.Run("not_a_digit", func(t *testing.T) {
		d, err := v1DatasetWith(t, ohMsg{typ: h5oMtimeID, data: []byte("1987123123595x\x00\x00")}).OpenDataset("/obj")
		require.NoError(t, err)
		_, err = d.Info()
		require.Error(t, err)
	})
}

// TestMutationEmptyComment: a comment message of one (NUL) byte is the
// empty comment (H5O__name_decode; h5o_name.go:10): libhdf5's
// H5Oget_comment returns "".
func TestMutationEmptyComment(t *testing.T) {
	f := groupWith(t, ohMsg{typ: h5oNameID, data: []byte{0}})
	comment, ok, err := f.oGetComment(ohAt(t, f, "/obj"))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, comment)
}

// TestMutationExternalNoSlots: an external file list message that
// allocates no slot is refused ("bad number of allocated slots when
// parsing efl msg", h5o_efl.go:34): libhdf5 fails to open the empty
// int32 dataset {0} whose list allocates no slot, and opens the one
// whose list allocates one (unused) slot.
func TestMutationExternalNoSlots(t *testing.T) {
	for _, nalloc := range []int{0, 1} {
		t.Run(fmt.Sprint(nalloc), func(t *testing.T) {
			f, _ := buildObject(t, func(b *h5Builder) uint64 {
				contig := layoutMsg(le64a(le64a([]byte{3, 1}, haddrUndef), 0))
				return b.put(ohdr2(2, v2Msgs(i32Type(), spaceMsg(0), b.eflMsg(nalloc, nil), contig)))
			})
			_, err := f.OpenDataset("/obj")
			if nalloc == 0 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestMutationDataspaceTruncated: dataspace messages too short for their
// dimension sizes (h5o_sdspace.go:93) or maximum dimension sizes
// (h5o_sdspace.go:105) are refused: libhdf5 opens the empty int32
// dataset {2, 0} (maximum dimensions {2, 0}) and fails to open it when
// its dataspace message lacks the last dimension size or the last
// maximum dimension size.
func TestMutationDataspaceTruncated(t *testing.T) {
	for _, c := range []struct {
		name  string
		space []byte
		ok    bool
	}{
		{"complete", le64a(le64a(le64a(le64a([]byte{2, 2, 1, 1}, 2), 0), 2), 0), true},
		{"dims", le64a([]byte{2, 2, 0, 1}, 2), false},
		{"max_dims", le64a(le64a(le64a([]byte{2, 2, 1, 1}, 2), 0), 2), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := buildObject(t, func(b *h5Builder) uint64 {
				return b.put(ohdr2(2, v2Msgs(i32Type(), ohMsg{typ: h5oSdspaceID, data: c.space}, compactLayout(nil))))
			})
			_, err := f.OpenDataset("/obj")
			if c.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// sharedDtype is a datatype message stored as a version 3 shared message
// of the given sharing type referring to the object header at addr.
func sharedDtype(typ byte, addr uint64) ohMsg {
	return ohMsg{typ: h5oDtypeID, flags: h5oMsgFlagShared, data: le64a([]byte{3, typ}, addr)}
}

// sharedDataset opens a file whose root group links "obj" to an int32
// dataset {2} stored compactly (1, 2), whose datatype message is the one
// dtype returns for the header of a committed int32 datatype at addr
// (put first; build may put more headers).
func sharedDataset(t *testing.T, dtype func(b *h5Builder, committed uint64) ohMsg) (*File, uint64) {
	t.Helper()
	return buildObject(t, func(b *h5Builder) uint64 {
		committed := b.put(ohdr2(2, v2Msgs(i32Type())))
		return b.put(ohdr2(2, v2Msgs(dtype(b, committed), spaceMsg(2), compactLayout(i32(1, 2)))))
	})
}

// TestMutationSharedDatatype: datasets whose datatype message is shared,
// referring to the header of a committed int32 datatype. libhdf5 reads
// the values 1, 2 of the datasets whose shared message
//   - is a committed one: H5Oget_native_info reports the datatype
//     message as shared (h5o_int.go:268);
//   - has the sharing type 0 or 3 (not stored in the shared message
//     heap: H5O__shared_read reads every such message from the object
//     header it names);
//   - refers to a committed datatype whose own datatype message is
//     shared, referring to the int32 committed datatype (H5O__shared_read
//     decodes the message it reads, which resolves the next shared
//     message; h5o_shared.go:63);
//
// and fails to open the datasets whose shared messages form a cycle.
func TestMutationSharedDatatype(t *testing.T) {
	for _, c := range []struct {
		name  string
		dtype func(b *h5Builder, committed uint64) ohMsg
	}{
		{"committed", func(b *h5Builder, committed uint64) ohMsg { return sharedDtype(h5oShareTypeCommitted, committed) }},
		{"type_unshared", func(b *h5Builder, committed uint64) ohMsg { return sharedDtype(h5oShareTypeUnshared, committed) }},
		{"type_here", func(b *h5Builder, committed uint64) ohMsg { return sharedDtype(h5oShareTypeHere, committed) }},
		{"chain", func(b *h5Builder, committed uint64) ohMsg {
			t1 := b.put(ohdr2(2, v2Msgs(sharedDtype(h5oShareTypeCommitted, committed))))
			t2 := b.put(ohdr2(2, v2Msgs(sharedDtype(h5oShareTypeCommitted, t1))))
			return sharedDtype(h5oShareTypeCommitted, t2)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, obj := sharedDataset(t, c.dtype)
			d, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			got, err := Read[int32](d)
			require.NoError(t, err)
			assert.Equal(t, []int32{1, 2}, got)
			oh, err := f.oProtect(obj)
			require.NoError(t, err)
			hi := h5oGetHdrInfoReal(oh)
			assert.Equal(t, uint64(1)<<h5oDtypeID, hi.mesg.shared)
		})
	}
	t.Run("cycle", func(t *testing.T) {
		f, _ := buildObject(t, func(b *h5Builder) uint64 {
			t1 := b.alloc(len(ohdr2(2, v2Msgs(sharedDtype(h5oShareTypeCommitted, 0)))))
			t2 := b.put(ohdr2(2, v2Msgs(sharedDtype(h5oShareTypeCommitted, t1))))
			b.set(t1, ohdr2(2, v2Msgs(sharedDtype(h5oShareTypeCommitted, t2))))
			return b.put(ohdr2(2, v2Msgs(sharedDtype(h5oShareTypeCommitted, t1), spaceMsg(2), compactLayout(i32(1, 2)))))
		})
		_, err := f.OpenDataset("/obj")
		require.Error(t, err)
	})
}

// TestMutationSharedVersion2Heap: a version 2 shared message of the
// shared message heap type (which the C library never writes, and
// H5O__shared_decode only asserts against) is decoded with its heap ID
// (h5o_shared.go:118): /e08 of testdata/cover_sohm.h5 (int32 {9}, its
// dataspace in the shared message heap) with the version of its shared
// dataspace message changed from 3 to 2. libhdf5 opens it and reads the
// values of the original.
func TestMutationSharedVersion2Heap(t *testing.T) {
	want := func(f *File) []int32 {
		d, err := f.OpenDataset("/e08")
		require.NoError(t, err)
		assert.Equal(t, []uint64{9}, d.Dataspace().Dims)
		v, err := Read[int32](d)
		require.NoError(t, err)
		return v
	}(openTestdata(t, "cover_sohm.h5"))
	f := patchedFile(t, "testdata/cover_sohm.h5", func(orig *File, b []byte) {
		d, err := orig.OpenDataset("/e08")
		require.NoError(t, err)
		/* (version 3, heap type, heap ID) */
		patchOhdrChunk0(t, b, d.addr, []byte{3, 1, 0, 0xc2, 0, 0, 0, 0, 0x14, 0}, []byte{2})
	})
	d, err := f.OpenDataset("/e08")
	require.NoError(t, err)
	assert.Equal(t, []uint64{9}, d.Dataspace().Dims)
	got, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestMutationFsinfoAddresses: the free-space manager addresses of file
// space info messages (h5o_fsinfo.go:54, 91, 135), as libhdf5 sets them
// from the message when it opens the file (f->shared->fs_addr and the
// other fields H5F__super_read copies from it, read through the library's
// private headers): a version 0 message with the H5F_FILE_SPACE_ALL_PERSIST
// strategy, a version 1 message of a file with persistent free space, and
// one of a file without (no address).
func TestMutationFsinfoAddresses(t *testing.T) {
	undef := func(set map[int]haddr) [12]haddr {
		var a [12]haddr
		for i := range a {
			a[i] = haddrUndef
		}
		for i, v := range set {
			a[i] = v
		}
		return a
	}
	for _, c := range []struct {
		file string
		want h5oFsinfo
	}{
		{"test/testfiles/h5fc_ext1_f.h5", h5oFsinfo{version: h5oFsinfoVersion1, strategy: h5fFspaceStrategyFsmAggr, persist: true, threshold: 1, pageSize: 4096,
			eoa: 6760, fsAddr: undef(map[int]haddr{0: 6526, 2: 6643})}},
		{"tools/test/h5repack/testfiles/h5repack_fsm_aggr_persist.h5", h5oFsinfo{version: h5oFsinfoVersion1, strategy: h5fFspaceStrategyFsmAggr, persist: true, threshold: 1, pageSize: 4096,
			eoa: 2448, fsAddr: undef(map[int]haddr{0: 2448})}},
		{"tools/test/h5repack/testfiles/h5repack_none.h5", h5oFsinfo{version: h5oFsinfoVersion1, strategy: h5fFspaceStrategyNone, threshold: 1, pageSize: 4096,
			eoa: haddrUndef, fsAddr: undef(nil)}},
	} {
		t.Run(c.file, func(t *testing.T) {
			f := openTestFile(t, c.file)
			fs := readMsg[*h5oFsinfo](t, f, f.sblock.extAddr, h5oFsinfoID)
			assert.Equal(t, c.want, *fs)
		})
	}
}
