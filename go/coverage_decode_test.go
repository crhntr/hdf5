package hdf5

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

// The version, flag and length checks of the message decoders, on crafted
// messages (8-byte addresses and lengths). The expected outcomes follow the
// checks of the C decoders (H5O__btreek_decode, H5O__ginfo_decode, ...);
// the same decoders are compared with libhdf5 on corrupted messages of real
// files by TestMessageMutations in cgo/conformance.

type decodeCase struct {
	name string
	raw  []byte
	want any // nil: an error is expected
}

func le16b(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }
func le64b(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// checkDecoder runs the cases through msgDecode for the message type id,
// and every strict prefix of the valid cases, which must fail cleanly.
func checkDecoder(t *testing.T, f *File, id uint16, cases []decodeCase) {
	t.Helper()
	for _, c := range cases {
		got, err := f.msgDecode(nil, id, c.raw)
		if c.want == nil {
			assert.Error(t, err, c.name)
			continue
		}
		require.NoError(t, err, c.name)
		assert.Equal(t, c.want, got, c.name)
		for n := range len(c.raw) {
			_, err := f.msgDecode(nil, id, c.raw[:n])
			assert.Error(t, err, "%s truncated to %d bytes", c.name, n)
		}
	}
}

func TestDecodeSuperblockExtensionMessages(t *testing.T) {
	f := openTestdata(t, "cover.h5")

	checkDecoder(t, f, h5oBtreekID, []decodeCase{
		{"btreek", cat([]byte{0}, le16b(64), le16b(32), le16b(8)), &h5oBtreek{btreeK: [2]uint{32, 64}, symLeafK: 8}},
		{"btreek version 1", cat([]byte{1}, le16b(64), le16b(32), le16b(8)), nil},
	})
	assert.Equal(t, 0, h5bSnodeID)

	checkDecoder(t, f, h5oDrvinfoID, []decodeCase{
		{"drvinfo", cat([]byte{0}, []byte("NCSAfami"), le16b(8), le64b(2048)), &h5oDrvinfo{name: "NCSAfami", buf: le64b(2048)}},
		{"drvinfo version 1", cat([]byte{1}, []byte("NCSAfami"), le16b(8), le64b(2048)), nil},
		{"drvinfo empty", cat([]byte{0}, []byte("NCSAfami"), le16b(0)), nil},
	})

	checkDecoder(t, f, h5oShmesgID, []decodeCase{
		/* (H5O__shmesg_decode does not check the version) */
		{"shmesg", cat([]byte{0}, le64b(4096), []byte{2}), &h5oShmesgTable{addr: 4096, version: 0, nindexes: 2}},
		{"shmesg version 9", cat([]byte{9}, le64b(4096), []byte{2}), &h5oShmesgTable{addr: 4096, version: 9, nindexes: 2}},
	})

	checkDecoder(t, f, h5oMdciMsgID, []decodeCase{
		{"mdci", cat([]byte{0}, le64b(512), le64b(100)), &h5oMdci{addr: 512, size: 100}},
		{"mdci version 1", cat([]byte{1}, le64b(512), le64b(100)), nil},
	})
}

func TestDecodeGroupMessages(t *testing.T) {
	f := openTestdata(t, "cover.h5")

	checkDecoder(t, f, h5oGinfoID, []decodeCase{
		/* defaults: H5G_CRT_GINFO_MAX_COMPACT etc. */
		{"ginfo", []byte{0, 0}, &h5oGinfo{maxCompact: 8, minDense: 6, estNumEntries: 4, estNameLen: 8}},
		{"ginfo phase change", cat([]byte{0, 1}, le16b(12), le16b(9)),
			&h5oGinfo{storeLinkPhaseChange: true, maxCompact: 12, minDense: 9, estNumEntries: 4, estNameLen: 8}},
		{"ginfo entry info", cat([]byte{0, 2}, le16b(100), le16b(17)),
			&h5oGinfo{maxCompact: 8, minDense: 6, storeEstEntryInfo: true, estNumEntries: 100, estNameLen: 17}},
		{"ginfo both", cat([]byte{0, 3}, le16b(12), le16b(9), le16b(100), le16b(17)),
			&h5oGinfo{storeLinkPhaseChange: true, maxCompact: 12, minDense: 9, storeEstEntryInfo: true, estNumEntries: 100, estNameLen: 17}},
		{"ginfo version 1", []byte{1, 0}, nil},
		{"ginfo bad flags", []byte{0, 4}, nil},
	})

	checkDecoder(t, f, h5oStabID, []decodeCase{
		{"stab", cat(le64b(136), le64b(680)), &h5oStab{btreeAddr: 136, heapAddr: 680}},
	})

	name := []byte("child")
	checkDecoder(t, f, h5oLinkID, []decodeCase{
		{"hard", cat([]byte{1, 0, 5}, name, le64b(800)), &h5oLink{typ: h5.LinkHard, cset: h5.CsetASCII, name: "child", addr: 800}},
		{"hard, 2-byte name length, creation order, utf-8", cat([]byte{1, 0x15}, le64b(7), []byte{1}, le16b(5), name, le64b(800)),
			&h5oLink{typ: h5.LinkHard, corderValid: true, corder: 7, cset: h5.CsetUTF8, name: "child", addr: 800}},
		{"hard, 4-byte name length", cat([]byte{1, 2}, binary.LittleEndian.AppendUint32(nil, 5), name, le64b(800)),
			&h5oLink{typ: h5.LinkHard, cset: h5.CsetASCII, name: "child", addr: 800}},
		{"hard, 8-byte name length", cat([]byte{1, 3}, le64b(5), name, le64b(800)),
			&h5oLink{typ: h5.LinkHard, cset: h5.CsetASCII, name: "child", addr: 800}},
		{"soft", cat([]byte{1, 8, 1, 5}, name, le16b(3), []byte("/ab")),
			&h5oLink{typ: h5.LinkSoft, cset: h5.CsetASCII, name: "child", softName: "/ab"}},
		{"external", cat([]byte{1, 8, 64, 5}, name, le16b(5), []byte{0, 'f', 0, '/', 0}),
			&h5oLink{typ: h5.LinkExternal, cset: h5.CsetASCII, name: "child", udSize: 5, udData: []byte{0, 'f', 0, '/', 0}}},
		{"user-defined, no data", cat([]byte{1, 8, 100, 5}, name, le16b(0)),
			&h5oLink{typ: 100, cset: h5.CsetASCII, name: "child"}},
		{"version 0", cat([]byte{0, 0, 5}, name, le64b(800)), nil},
		{"bad flags", cat([]byte{1, 0x20, 5}, name, le64b(800)), nil},
		{"link type 2", cat([]byte{1, 8, 2, 5}, name, le64b(800)), nil},
		{"bad cset", cat([]byte{1, 0x10, 2, 5}, name, le64b(800)), nil},
		{"empty name", cat([]byte{1, 0, 0}, le64b(800)), nil},
		{"empty soft link value", cat([]byte{1, 8, 1, 5}, name, le16b(0)), nil},
		{"short external link value", cat([]byte{1, 8, 64, 5}, name, le16b(2), []byte{0, 'f'}), nil},
		{"name longer than the message", cat([]byte{1, 3}, le64b(1<<62), name), nil},
	})
}

func TestDecodeExternalFileList(t *testing.T) {
	f := openTestdata(t, "dset.h5")
	ds, err := f.OpenDataset("/external")
	require.NoError(t, err)
	oh, err := objHeader(ds.f, ds.oh, ds.addr)
	require.NoError(t, err)
	var raw []byte
	for _, m := range oh.mesgs {
		if m.typeID == h5oEflID {
			raw = m.raw
		}
	}
	require.NotNil(t, raw)
	v, err := f.msgDecode(nil, h5oEflID, raw)
	require.NoError(t, err)
	efl := v.(*h5oEfl)
	/* gen_dset.c: dset_ext.raw in two segments, offset 16 (12 bytes) and
	 * offset 100 (28 bytes) */
	require.Equal(t, 2, efl.nused)
	assert.Equal(t, "dset_ext.raw", efl.slot[0].name)
	assert.Equal(t, int64(16), efl.slot[0].offset)
	assert.Equal(t, uint64(12), efl.slot[0].size)
	assert.Equal(t, int64(100), efl.slot[1].offset)
	assert.Equal(t, uint64(28), efl.slot[1].size)

	for n := range len(raw) {
		_, err := f.msgDecode(nil, h5oEflID, raw[:n])
		assert.Error(t, err, "truncated to %d bytes", n)
	}
	change := func(off int, b ...byte) []byte {
		out := append([]byte(nil), raw...)
		copy(out[off:], b)
		return out
	}
	for name, bad := range map[string][]byte{
		"version 2":            change(0, 2),
		"no slots":             change(4, 0, 0),
		"more used than slots": change(6, 0xff, 0),
		"undefined heap":       change(8, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff),
		"heap not a heap":      change(8, 0, 0, 0, 0, 0, 0, 0, 0),
		"name at offset 0":     change(16, 0, 0, 0, 0, 0, 0, 0, 0),
		"name past the heap":   change(16, 0xff, 0xff, 0, 0, 0, 0, 0, 0),
	} {
		_, err := f.msgDecode(nil, h5oEflID, bad)
		assert.Error(t, err, name)
	}
}

// The filter names H5P__get_filter reports for filters stored without a
// name: the name of the library's filter class (H5Z_DEFLATE[0].name etc.),
// "Unknown library filter" for other ids below 256, "" above.
func TestFilterNames(t *testing.T) {
	for id, want := range map[int32]string{
		h5zFilterDeflate: "deflate", h5zFilterShuffle: "shuffle", h5zFilterFletcher32: "fletcher32",
		h5zFilterSzip: "szip", h5zFilterNbit: "nbit", h5zFilterScaleoffset: "scaleoffset",
		7: "Unknown library filter", 255: "Unknown library filter", 256: "", 32000: "",
	} {
		assert.Equal(t, want, h5dFilterName(&h5zFilterInfo{id: id}), "filter %d", id)
	}
	assert.Equal(t, "mine", h5dFilterName(&h5zFilterInfo{id: h5zFilterDeflate, name: "mine"}))
}
