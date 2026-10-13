package hdf5

// Tests for the object header code (h5o_cache.go, h5o_layout.go,
// h5o_link.go, h5o_linfo.go) written from the mutants gremlins left alive.
// Each test opens a small file assembled byte by byte below (a version 2
// superblock, a root group holding compact links, and the object headers
// under test) and expects what libhdf5 1.14.6 reads from the same bytes:
// set HDF5_MUTATION_DUMP to a directory to have the tests write the files
// there for checking them with libhdf5 (h5dump, h5debug or chdf5).

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// h5Builder assembles an HDF5 file image with 8-byte addresses and
// lengths: a version 2 superblock (written by finish) followed by the
// blocks put or allocated by the test, each aligned to 8 bytes.
type h5Builder struct {
	buf []byte
}

const h5BuilderSuperblockSize = 48

func newH5Builder() *h5Builder {
	return &h5Builder{buf: make([]byte, h5BuilderSuperblockSize)}
}

// alloc reserves n bytes and returns their address.
func (b *h5Builder) alloc(n int) uint64 {
	for len(b.buf)%8 != 0 {
		b.buf = append(b.buf, 0)
	}
	addr := uint64(len(b.buf))
	b.buf = append(b.buf, make([]byte, n)...)
	return addr
}

// put appends data and returns its address.
func (b *h5Builder) put(data []byte) uint64 {
	addr := b.alloc(len(data))
	copy(b.buf[addr:], data)
	return addr
}

// set writes data at addr (which must have been allocated).
func (b *h5Builder) set(addr uint64, data []byte) {
	copy(b.buf[addr:addr+uint64(len(data))], data)
}

// finish writes the superblock (end of file address: the end of the
// image) for the root group at root and returns the image.
func (b *h5Builder) finish(root uint64) []byte {
	sb := []byte("\x89HDF\r\n\x1a\n")
	sb = append(sb, 2, 8, 8, 0)
	sb = le64a(sb, 0)                  /* base address */
	sb = le64a(sb, haddrUndef)         /* superblock extension */
	sb = le64a(sb, uint64(len(b.buf))) /* end of file address */
	sb = le64a(sb, root)
	sb = binary.LittleEndian.AppendUint32(sb, checksumLookup3(sb, 0))
	copy(b.buf, sb)
	return b.buf
}

func le64a(b []byte, v uint64) []byte { return binary.LittleEndian.AppendUint64(b, v) }

// ohMsg is an object header message: type, flags and encoded body.
type ohMsg struct {
	typ   uint16
	flags uint8
	data  []byte
}

// v2Msgs encodes messages for a version 2 object header chunk (no
// creation order field).
func v2Msgs(msgs ...ohMsg) []byte {
	var b []byte
	for _, m := range msgs {
		b = append(b, byte(m.typ))
		b = binary.LittleEndian.AppendUint16(b, uint16(len(m.data)))
		b = append(b, m.flags)
		b = append(b, m.data...)
	}
	return b
}

// v1Msgs encodes messages for a version 1 object header chunk (8-byte
// message headers, bodies padded to multiples of 8).
func v1Msgs(msgs ...ohMsg) []byte {
	var b []byte
	for _, m := range msgs {
		data := append([]byte(nil), m.data...)
		for len(data)%8 != 0 {
			data = append(data, 0)
		}
		b = binary.LittleEndian.AppendUint16(b, m.typ)
		b = binary.LittleEndian.AppendUint16(b, uint16(len(data)))
		b = append(b, m.flags, 0, 0, 0)
		b = append(b, data...)
	}
	return b
}

// ohdr2 encodes a version 2 object header (flags: H5O_HDR_* bits, which
// select the width of the chunk 0 size) whose first chunk holds body.
func ohdr2(flags byte, body []byte) []byte {
	b := []byte("OHDR")
	b = append(b, 2, flags)
	n := len(body)
	switch flags & 3 {
	case 0:
		b = append(b, byte(n))
	case 1:
		b = binary.LittleEndian.AppendUint16(b, uint16(n))
	case 2:
		b = binary.LittleEndian.AppendUint32(b, uint32(n))
	default:
		b = le64a(b, uint64(n))
	}
	b = append(b, body...)
	return binary.LittleEndian.AppendUint32(b, checksumLookup3(b, 0))
}

// ochk encodes a version 2 continuation chunk.
func ochk(body []byte) []byte {
	b := append([]byte("OCHK"), body...)
	return binary.LittleEndian.AppendUint32(b, checksumLookup3(b, 0))
}

// ohdr1 encodes a version 1 object header prefix (nmesgs messages, chunk
// 0 size chunk0Size) followed by body.
func ohdr1(nmesgs uint16, chunk0Size uint32, body []byte) []byte {
	b := []byte{1, 0}
	b = binary.LittleEndian.AppendUint16(b, nmesgs)
	b = binary.LittleEndian.AppendUint32(b, 1) /* link count */
	b = binary.LittleEndian.AppendUint32(b, chunk0Size)
	b = append(b, 0, 0, 0, 0)
	return append(b, body...)
}

// contMsg is a continuation message for the chunk at addr of size bytes.
func contMsg(addr, size uint64) ohMsg {
	return ohMsg{typ: h5oContID, data: le64a(le64a(nil, addr), size)}
}

// hardLinkMsg is a link message (with default type and character set)
// for a hard link to addr.
func hardLinkMsg(name string, addr uint64) ohMsg {
	b := append([]byte{1, 0, byte(len(name))}, name...)
	return ohMsg{typ: h5oLinkID, data: le64a(b, addr)}
}

// linfoMsg is a link info message for a group with compact storage.
func linfoMsg() ohMsg {
	return ohMsg{typ: h5oLinfoID, data: le64a(le64a([]byte{0, 0}, haddrUndef), haddrUndef)}
}

// rootGroup puts a version 2 root group header holding links and returns
// its address.
func (b *h5Builder) rootGroup(links ...ohMsg) uint64 {
	return b.put(ohdr2(2, v2Msgs(append([]ohMsg{linfoMsg()}, links...)...)))
}

// i32Type is a datatype message for H5T_STD_I32LE.
func i32Type() ohMsg {
	return ohMsg{typ: h5oDtypeID, data: []byte{0x10, 0x08, 0, 0, 4, 0, 0, 0, 0, 0, 32, 0}}
}

// spaceMsg is a version 2 dataspace message for a simple dataspace.
func spaceMsg(dims ...uint64) ohMsg {
	b := []byte{2, byte(len(dims)), 0, 1}
	for _, d := range dims {
		b = le64a(b, d)
	}
	return ohMsg{typ: h5oSdspaceID, data: b}
}

// fillMsg is a version 3 fill value message: fill value v (4 bytes),
// written if set, allocated late.
func fillMsg(v int32) ohMsg {
	return ohMsg{typ: h5oFillNewID, data: binary.LittleEndian.AppendUint32([]byte{3, 0x2a, 4, 0, 0, 0}, uint32(v))}
}

// layoutMsg is a layout message with body b.
func layoutMsg(b []byte) ohMsg { return ohMsg{typ: h5oLayoutID, data: b} }

// builtFile opens the image (and writes it to
// $HDF5_MUTATION_DUMP/<test name>.h5 when that variable is set).
func builtFile(t *testing.T, img []byte) *File {
	t.Helper()
	if dir := os.Getenv("HDF5_MUTATION_DUMP"); dir != "" {
		fn := strings.ReplaceAll(t.Name(), "/", "_") + ".h5"
		require.NoError(t, os.WriteFile(filepath.Join(dir, fn), img, 0o644))
	}
	f, err := OpenReader(bytes.NewReader(img), int64(len(img)))
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// buildObject opens a file whose root group links "obj" to the object
// header build puts, and returns the header address.
func buildObject(t *testing.T, build func(b *h5Builder) uint64) (*File, uint64) {
	t.Helper()
	b := newH5Builder()
	obj := build(b)
	root := b.rootGroup(hardLinkMsg("obj", obj))
	return builtFile(t, b.finish(root)), obj
}

// hdrWant is what H5Oget_native_info (H5O_NATIVE_INFO_HDR) returns.
type hdrWant struct {
	version         uint8
	nmesgs, nchunks int
	total, free     uint64
}

// checkHeader loads the object header at addr and compares its header
// information with want (nil: libhdf5 cannot load it).
func checkHeader(t *testing.T, f *File, addr uint64, want *hdrWant) {
	t.Helper()
	oh, err := f.oProtect(addr)
	if want == nil {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)
	hi := h5oGetHdrInfoReal(oh)
	assert.Equal(t, *want, hdrWant{hi.version, hi.nmesgs, hi.nchunks, hi.space.total, hi.space.free})
}

// v1Hdr is a version 1 object header holding body in its first chunk.
func v1Hdr(nmesgs uint16, body []byte) []byte { return ohdr1(nmesgs, uint32(len(body)), body) }

// TestMutationObjectHeaderPrefix: the chunk 0 size checks of
// H5O__prefix_deserialize (h5o_cache.go:332, 378, 379). libhdf5 loads the
// headers without messages (H5Oopen then fails with "unable to determine
// object type") and the headers whose first chunk is one message header,
// and rejects the others with "bad object header chunk size".
func TestMutationObjectHeaderPrefix(t *testing.T) {
	null0 := ohMsg{typ: h5oNullID}
	for _, c := range []struct {
		name string
		hdr  []byte
		want *hdrWant
	}{
		{"v2_empty", ohdr2(0, nil), &hdrWant{2, 0, 1, 11, 0}},
		{"v2_one_message_header", ohdr2(0, v2Msgs(null0)), &hdrWant{2, 1, 1, 15, 4}},
		{"v2_short_chunk", ohdr2(0, []byte{0, 0, 0}), nil},
		{"v1_empty", ohdr1(0, 0, nil), &hdrWant{1, 0, 1, 16, 0}},
		{"v1_one_message_header", v1Hdr(1, v1Msgs(null0)), &hdrWant{1, 1, 1, 24, 8}},
		{"v1_no_messages_nonempty_chunk", v1Hdr(0, v1Msgs(null0)), nil},
		{"v1_messages_empty_chunk", ohdr1(1, 0, nil), nil},
		{"v1_short_chunk", ohdr1(1, 4, make([]byte, 4)), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, obj := buildObject(t, func(b *h5Builder) uint64 { return b.put(c.hdr) })
			checkHeader(t, f, obj, c.want)
			_, err := f.Open("/obj")
			assert.Error(t, err, "no message gives the object a class")
		})
	}
}

// TestMutationUnknownMessageID: a message with the type ID of the
// "unknown" class itself (0x19) is an unknown message for
// H5O__chunk_deserialize (id >= H5O_UNKNOWN_ID, h5o_cache.go:553), so the
// "fail if unknown always" flag makes libhdf5 reject the header.
func TestMutationUnknownMessageID(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		f, obj := buildObject(t, func(b *h5Builder) uint64 {
			return b.put(ohdr2(0, v2Msgs(linfoMsg(), ohMsg{typ: h5oUnknownID, data: make([]byte, 8)})))
		})
		checkHeader(t, f, obj, &hdrWant{2, 2, 1, 45, 0})
		oh, err := f.oProtect(obj)
		require.NoError(t, err)
		assert.Equal(t, uint16(h5oUnknownID), oh.mesgs[1].typeID)
		assert.Equal(t, h5oUnknown(h5oUnknownID), oh.mesgs[1].native)
	})
	t.Run("fail_if_unknown", func(t *testing.T) {
		f, obj := buildObject(t, func(b *h5Builder) uint64 {
			return b.put(ohdr2(0, v2Msgs(linfoMsg(), ohMsg{typ: h5oUnknownID, flags: h5oMsgFlagFailIfUnknownAlways, data: make([]byte, 8)})))
		})
		checkHeader(t, f, obj, nil)
	})
}

// TestMutationContinuationChunks: continuation chunks next to and
// overlapping other chunks, repeated, empty, and at the end of the file.
// The metadata cache of libhdf5 keys entries by address only: it loads
// chunks that overlap (at different addresses), gets a chunk loaded
// already back from the cache for a second continuation message naming
// its address (adding nothing to the header), and rejects a continuation
// message naming the header's own address (an entry of another type).
func TestMutationContinuationChunks(t *testing.T) {
	null := func(n int) ohMsg { return ohMsg{typ: h5oNullID, data: make([]byte, n)} }
	for _, c := range []struct {
		name  string
		build func(b *h5Builder) uint64
		want  *hdrWant
	}{
		{"v1_chunk_before_chunk0", func(b *h5Builder) uint64 {
			c1 := v1Msgs(null(8))
			a1 := b.put(c1)
			return b.put(v1Hdr(3, v1Msgs(linfoMsg(), contMsg(a1, uint64(len(c1))))))
		}, &hdrWant{1, 3, 2, 88, 16}},
		{"v2_chunk_before_chunk0", func(b *h5Builder) uint64 {
			c1 := ochk(v2Msgs(null(4))) /* 16 bytes: ends where chunk 0 starts */
			a1 := b.put(c1)
			return b.put(ohdr2(0, v2Msgs(linfoMsg(), contMsg(a1, uint64(len(c1))))))
		}, &hdrWant{2, 3, 2, 69, 8}},
		{"v1_chunk_inside_chunk", func(b *h5Builder) uint64 {
			c1 := v1Msgs(null(0), null(8))
			a1 := b.put(c1)
			return b.put(v1Hdr(6, v1Msgs(linfoMsg(), contMsg(a1, uint64(len(c1))), contMsg(a1+8, uint64(len(c1))-8))))
		}, &hdrWant{1, 6, 3, 136, 40}},
		{"v1_chunk_overlapping_start", func(b *h5Builder) uint64 {
			a := b.put(v1Msgs(null(0), null(0), null(8)))
			return b.put(v1Hdr(7, v1Msgs(linfoMsg(), contMsg(a+8, 24), contMsg(a, 16))))
		}, &hdrWant{1, 7, 3, 136, 40}},
		{"v1_chunk_twice", func(b *h5Builder) uint64 {
			c1 := v1Msgs(null(8))
			a1 := b.put(c1)
			return b.put(v1Hdr(4, v1Msgs(linfoMsg(), contMsg(a1, uint64(len(c1))), contMsg(a1, uint64(len(c1))))))
		}, &hdrWant{1, 4, 2, 112, 16}},
		{"v1_chunk_continued_by_itself", func(b *h5Builder) uint64 {
			a1 := b.alloc(24)
			b.set(a1, v1Msgs(contMsg(a1, 24)))
			return b.put(v1Hdr(3, v1Msgs(linfoMsg(), contMsg(a1, 24))))
		}, &hdrWant{1, 3, 2, 96, 0}},
		{"v1_continued_by_chunk0", func(b *h5Builder) uint64 {
			obj := b.alloc(16 + 32 + 24)
			b.set(obj, v1Hdr(2, v1Msgs(linfoMsg(), contMsg(obj, 72))))
			return obj
		}, nil},
		{"v1_empty_chunk", func(b *h5Builder) uint64 {
			a1 := b.put(make([]byte, 8))
			return b.put(v1Hdr(2, v1Msgs(linfoMsg(), contMsg(a1, 0))))
		}, &hdrWant{1, 2, 2, 72, 0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, obj := buildObject(t, c.build)
			checkHeader(t, f, obj, c.want)
		})
	}

	/* Empty chunks at and beyond the end of the file: H5FD_read rejects
	 * reads that end beyond the end of the file, but returns at once
	 * for empty ones. */
	t.Run("v1_empty_chunk_at_eof", func(t *testing.T) {
		b := newH5Builder()
		obj := b.alloc(16 + 32 + 24)
		root := b.rootGroup(hardLinkMsg("obj", obj))
		b.set(obj, v1Hdr(2, v1Msgs(linfoMsg(), contMsg(uint64(len(b.buf)), 0))))
		f := builtFile(t, b.finish(root))
		checkHeader(t, f, obj, &hdrWant{1, 2, 2, 72, 0})
	})
	t.Run("v1_empty_chunk_past_eof", func(t *testing.T) {
		b := newH5Builder()
		obj := b.alloc(16 + 32 + 24)
		root := b.rootGroup(hardLinkMsg("obj", obj))
		b.set(obj, v1Hdr(2, v1Msgs(linfoMsg(), contMsg(uint64(len(b.buf))+8, 0))))
		f := builtFile(t, b.finish(root))
		checkHeader(t, f, obj, &hdrWant{1, 2, 2, 72, 0})
	})
	t.Run("v1_chunk_past_eof", func(t *testing.T) {
		b := newH5Builder()
		obj := b.alloc(16 + 32 + 24)
		root := b.rootGroup(hardLinkMsg("obj", obj))
		b.set(obj, v1Hdr(2, v1Msgs(linfoMsg(), contMsg(uint64(len(b.buf))-8, 16))))
		f := builtFile(t, b.finish(root))
		checkHeader(t, f, obj, nil)
	})
}

// TestMutationOverlappingChunksBound: the chunks of an object header
// may overlap (see TestMutationContinuationChunks), but hdf5 refuses to
// load more chunk bytes for one header than the file holds, which bounds
// the work done on corrupt files (libhdf5 has no such bound and loads the
// second file too).
func TestMutationOverlappingChunksBound(t *testing.T) {
	for name, extra := range map[string]uint64{"file_size": 0, "larger": 8} {
		t.Run(name, func(t *testing.T) {
			b := newH5Builder()
			/* A region of empty null messages: every 8-byte aligned range
			 * of it is a valid chunk. */
			region := b.put(v1Msgs(make([]ohMsg, 32)...))
			obj := b.alloc(16 + 32 + 3*24)
			root := b.rootGroup(hardLinkMsg("obj", obj))
			b.alloc(0) /* pad the file to a multiple of 8 bytes */
			chunk0 := uint64(16 + 32 + 3*24)
			/* Three overlapping chunks that, with chunk 0, add up to the
			 * size of the file (plus extra). */
			rest := uint64(len(b.buf)) - chunk0 + extra
			s1, s2 := uint64(160), uint64(152)
			s3 := rest - s1 - s2
			require.True(t, s3 <= 240 && s3%8 == 0, "%d", s3)
			b.set(obj, v1Hdr(4, v1Msgs(linfoMsg(), contMsg(region, s1), contMsg(region+8, s2), contMsg(region+16, s3))))
			f := builtFile(t, b.finish(root))
			if extra == 0 {
				checkHeader(t, f, obj, &hdrWant{1, int(4 + rest/8), 4, chunk0 + rest, rest})
			} else {
				checkHeader(t, f, obj, nil)
			}
		})
	}
}

// ones returns n dimensions of size v.
func ones(n int, v uint64) []uint64 {
	d := make([]uint64, n)
	for i := range d {
		d[i] = v
	}
	return d
}

// TestMutationLayoutMessages: data layout messages of every version at
// the limits H5O__layout_decode checks (h5o_layout.go:57, 67, 107, 139,
// 197, 249, 258, 343), in int32 datasets with fill value -7 (the chunked
// ones have no chunks: reading them gives the fill value). libhdf5 opens
// and reads the datasets that have a "want", and fails to open the others.
func TestMutationLayoutMessages(t *testing.T) {
	data := []byte{1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, 4, 0, 0, 0}
	dims4 := func(l []byte, n int) []byte {
		for range n {
			l = append(l, 4, 0, 0, 0)
		}
		return l
	}
	/* Version 1 and 2 contiguous: ndims (dataspace rank + 1), the data
	 * address and ndims 32-bit dimensions. */
	contig := func(b *h5Builder, version byte, ndims, ndimsStored int) []byte {
		return dims4(le64a([]byte{version, byte(ndims), 1, 0, 0, 0, 0, 0}, b.put(data)), ndimsStored)
	}
	/* Version 3 chunked: ndims, index address, ndims 32-bit dimensions
	 * (the last one the datatype size). */
	v3chunked := func(ndims int) []byte {
		l := le64a([]byte{3, 2, byte(ndims)}, haddrUndef)
		for range ndims - 1 {
			l = append(l, 1, 0, 0, 0)
		}
		return append(l, 4, 0, 0, 0)
	}
	/* Version 4 chunked: flags, ndims, bytes per dimension, the
	 * dimensions, the index type and its parameters, the index address. */
	v4chunked := func(dims []uint64, enc int, index ...byte) []byte {
		l := []byte{4, 2, 0, byte(len(dims)), byte(enc)}
		for _, d := range dims {
			l = append(l, le64a(nil, d)[:enc]...)
		}
		return le64a(append(l, index...), haddrUndef)
	}
	single := []byte{1}
	bt2 := func(split, merge byte) []byte { return []byte{5, 0, 8, 0, 0, split, merge} }
	for _, c := range []struct {
		name   string
		dims   []uint64
		layout func(b *h5Builder) []byte
		class  h5.LayoutClass
		chunk  []uint64
		want   []int32 /* nil: libhdf5 cannot open the dataset */
	}{
		{"v1_contiguous", []uint64{4}, func(b *h5Builder) []byte { return contig(b, 1, 2, 2) },
			h5.LayoutContiguous, nil, []int32{1, 2, 3, 4}},
		{"v1_contiguous_short", []uint64{4}, func(b *h5Builder) []byte { return contig(b, 1, 2, 1) },
			0, nil, nil},
		{"v1_contiguous_33_dims", []uint64{4}, func(b *h5Builder) []byte { return contig(b, 1, 33, 33) },
			h5.LayoutContiguous, nil, []int32{1, 2, 3, 4}},
		{"v1_contiguous_34_dims", []uint64{4}, func(b *h5Builder) []byte { return contig(b, 1, 34, 34) },
			0, nil, nil},
		{"v2_compact", []uint64{4}, func(b *h5Builder) []byte {
			return append(dims4([]byte{2, 2, 0, 0, 0, 0, 0, 0}, 2), append([]byte{16, 0, 0, 0}, data...)...)
		}, h5.LayoutCompact, nil, []int32{1, 2, 3, 4}},
		{"v2_layout_class_3", []uint64{4}, func(b *h5Builder) []byte {
			return append(dims4([]byte{2, 2, 3, 0, 0, 0, 0, 0}, 2), 0, 0, 0, 0)
		}, 0, nil, nil},
		{"v3_chunked_33_dims", ones(32, 1), func(*h5Builder) []byte { return v3chunked(33) },
			h5.LayoutChunked, ones(32, 1), []int32{-7}},
		{"v3_chunked_34_dims", ones(32, 1), func(*h5Builder) []byte { return v3chunked(34) },
			0, nil, nil},
		{"v4_chunked_33_dims", ones(32, 1), func(*h5Builder) []byte { return v4chunked(ones(33, 1), 1, single...) },
			h5.LayoutChunked, ones(32, 1), []int32{-7}},
		{"v4_chunked_34_dims", ones(32, 1), func(*h5Builder) []byte { return v4chunked(ones(34, 1), 1, single...) },
			0, nil, nil},
		{"v4_8_byte_dims", []uint64{4}, func(*h5Builder) []byte { return v4chunked([]uint64{2, 4}, 8, 3, 10) },
			h5.LayoutChunked, []uint64{2}, []int32{-7, -7, -7, -7}},
		{"v4_9_byte_dims", []uint64{4}, func(*h5Builder) []byte {
			l := []byte{4, 2, 0, 2, 9, 2, 0, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 3, 10}
			return le64a(l, haddrUndef)
		}, 0, nil, nil},
		{"v4_bt2_merge_100", []uint64{4}, func(*h5Builder) []byte { return v4chunked([]uint64{2, 4}, 1, bt2(100, 100)...) },
			h5.LayoutChunked, []uint64{2}, []int32{-7, -7, -7, -7}},
		{"v4_bt2_merge_101", []uint64{4}, func(*h5Builder) []byte { return v4chunked([]uint64{2, 4}, 1, bt2(100, 101)...) },
			0, nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := buildObject(t, func(b *h5Builder) uint64 {
				l := c.layout(b)
				return b.put(ohdr2(2, v2Msgs(spaceMsg(c.dims...), i32Type(), fillMsg(-7), layoutMsg(l))))
			})
			ds, err := f.OpenDataset("/obj")
			if c.want == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			layout, err := ds.Layout()
			require.NoError(t, err)
			assert.Equal(t, c.class, layout.Class)
			assert.Equal(t, c.chunk, layout.ChunkDims)
			got, err := Read[int32](ds)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// gcol is a global heap collection (4096 bytes) holding obj as object 1.
func gcol(obj []byte) []byte {
	b := append([]byte("GCOL"), 1, 0, 0, 0)
	b = le64a(b, 4096)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = append(b, 0, 0, 0, 0, 0, 0)
	b = le64a(b, uint64(len(obj)))
	b = append(b, obj...)
	for len(b)%8 != 0 {
		b = append(b, 0)
	}
	free := uint64(4096 - len(b)) /* free space: object 0 */
	b = le64a(append(b, make([]byte, 8)...), free)
	return append(b, make([]byte, 4096-len(b))...)
}

// TestMutationVirtualEmptyNames: virtual dataset mappings with an empty
// source file name or source dataset name, as H5Pset_virtual stores
// them (h5o_layout.go:435, 446). libhdf5 decodes both; it reads the fill
// value from the mapping to dataset "" of the same file (which does not
// exist).
func TestMutationVirtualEmptyNames(t *testing.T) {
	all := []byte{3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0} /* "all" selection */
	for _, c := range []struct {
		name       string
		file, dset string
		read       bool
	}{
		{"no_file_name", "", "/src", false},
		{"no_dataset_name", ".", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := buildObject(t, func(b *h5Builder) uint64 {
				enc := le64a([]byte{0}, 1) /* version, number of mappings */
				enc = append(enc, c.file+"\x00"+c.dset+"\x00"...)
				enc = append(append(enc, all...), all...)
				enc = binary.LittleEndian.AppendUint32(enc, checksumLookup3(enc, 0))
				heap := b.put(gcol(enc))
				l := binary.LittleEndian.AppendUint32(le64a([]byte{4, 3}, heap), 1)
				return b.put(ohdr2(2, v2Msgs(spaceMsg(4), i32Type(), fillMsg(-7), layoutMsg(l))))
			})
			ds, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			layout, err := ds.Layout()
			require.NoError(t, err)
			require.Len(t, layout.Virtual, 1)
			assert.Equal(t, c.file, layout.Virtual[0].SourceFile)
			assert.Equal(t, c.dset, layout.Virtual[0].SourceDataset)
			if c.read {
				got, err := Read[int32](ds)
				require.NoError(t, err)
				assert.Equal(t, []int32{-7, -7, -7, -7}, got)
			}
		})
	}
}

// TestMutationLinkMessages: link messages that store the default link
// type (hard) or name character set (ASCII) explicitly, a link of the
// last user-defined class (255) and an external link whose file and
// object names are empty (the shortest value, 3 bytes) (h5o_link.go:53,
// 79, 147, 156), and a link info message cut after its flags
// (h5o_linfo.go:57). libhdf5 lists the links of the first group and
// cannot iterate over the links of the second.
func TestMutationLinkMessages(t *testing.T) {
	b := newH5Builder()
	obj := b.put(ohdr2(2, v2Msgs(linfoMsg())))
	cut := b.put(ohdr2(2, v2Msgs(ohMsg{typ: h5oLinfoID, data: []byte{0, 0}}, hardLinkMsg("x", obj))))
	link := func(data []byte) ohMsg { return ohMsg{typ: h5oLinkID, data: data} }
	root := b.rootGroup(
		link(le64a([]byte{1, 0x08, 0, 5, 't', 'y', 'p', 'e', '0'}, obj)),
		link(le64a([]byte{1, 0x10, 0, 5, 'c', 's', 'e', 't', '0'}, obj)),
		link([]byte{1, 0x08, 255, 5, 'u', 'd', '2', '5', '5', 2, 0, 'x', 'y'}),
		link([]byte{1, 0x08, 64, 5, 'e', 'l', 'i', 'n', 'k', 3, 0, 0, 0, 0}),
		hardLinkMsg("cut", cut),
	)
	f := builtFile(t, b.finish(root))

	var links []h5.Link
	for l, err := range f.Root().Links() {
		require.NoError(t, err)
		links = append(links, l)
	}
	assert.Equal(t, []h5.Link{
		{Name: "cset0", Type: h5.LinkHard, Address: obj},
		{Name: "cut", Type: h5.LinkHard, Address: cut},
		{Name: "elink", Type: h5.LinkExternal, UserData: []byte{0, 0, 0}},
		{Name: "type0", Type: h5.LinkHard, Address: obj},
		{Name: "ud255", Type: 255, UserData: []byte("xy")},
	}, links)
	g, err := f.OpenGroup("/type0")
	require.NoError(t, err)
	n, err := g.NumLinks()
	require.NoError(t, err)
	assert.Zero(t, n)

	g, err = f.OpenGroup("/cut")
	require.NoError(t, err)
	_, err = g.NumLinks()
	assert.Error(t, err)
	for _, err := range g.Links() {
		assert.Error(t, err)
	}
}
