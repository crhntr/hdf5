package hdf5

// Tests for the metadata cache image loader (h5c_image.go) written from
// the mutants gremlins left alive. Each test opens a file assembled byte
// by byte (with the builders of mutation_h5o_test.go and
// mutation_h5d_test.go) whose superblock extension has a metadata cache
// image message, and expects what libhdf5 1.14.6 reads from the same
// bytes (set HDF5_MUTATION_DUMP to a directory to have the tests write the
// files and the external data files there).

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mdciEntry is an entry of a metadata cache image block: the cache client
// type (H5AC_*_ID), the entry's address, its flush dependency parents and
// its image.
type mdciEntry struct {
	typ     byte
	addr    uint64
	parents []uint64
	image   []byte
}

// mdciBlock encodes a cache image block (H5C__encode_cache_image_header,
// H5C__encode_cache_image_entry; without the trailing checksum, which
// libhdf5 does not read): every entry is clean, in the user ring, not in
// the LRU and no flush dependency parent.
func mdciBlock(entries ...mdciEntry) []byte {
	var body []byte
	for _, e := range entries {
		body = append(body, e.typ, 0, 1, 0)
		body = binary.LittleEndian.AppendUint16(body, 0)
		body = binary.LittleEndian.AppendUint16(body, 0)
		body = binary.LittleEndian.AppendUint16(body, uint16(len(e.parents)))
		body = binary.LittleEndian.AppendUint32(body, 0xffffffff) /* LRU rank -1 */
		body = le64a(le64a(body, e.addr), uint64(len(e.image)))
		for _, p := range e.parents {
			body = le64a(body, p)
		}
		body = append(body, e.image...)
	}
	b := append([]byte(h5cMdciBlockSignature), h5cMdciBlockVersion0, 0)
	b = le64a(b, uint64(18+len(body)))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(entries)))
	return append(b, body...)
}

// finishWithImage puts the cache image block and a superblock extension
// whose metadata cache image message points to it, and returns the file
// image (finish with the superblock's extension address set).
func (b *h5Builder) finishWithImage(root uint64, block []byte) []byte {
	blk := b.put(block)
	mdci := ohMsg{typ: h5oMdciMsgID, data: le64a(le64a([]byte{h5oMdciVersion0}, blk), uint64(len(block)))}
	ext := b.put(ohdr2(2, v2Msgs(mdci)))
	img := b.finish(root)
	binary.LittleEndian.PutUint64(img[20:], ext)
	binary.LittleEndian.PutUint32(img[44:], checksumLookup3(img[:44], 0))
	return img
}

// dumpFile writes img to $HDF5_MUTATION_DUMP/<test name>.h5 when that
// variable is set.
func dumpFile(t *testing.T, img []byte) {
	t.Helper()
	if dir := os.Getenv("HDF5_MUTATION_DUMP"); dir != "" {
		fn := strings.ReplaceAll(t.Name(), "/", "_") + ".h5"
		require.NoError(t, os.WriteFile(filepath.Join(dir, fn), img, 0o644))
	}
}

// TestMutationCacheImage: an int32 dataset {2} stored in the external file
// a.raw (int32 0, 1, ...), whose external file list names it in a local
// heap with its data block at 1 MiB, far beyond the end of the file: the
// data block is only in the cache image block. libhdf5 takes prefetched
// entries from the cache image without checking them against the end of
// the file and reads {0, 1}.
func TestMutationCacheImage(t *testing.T) {
	dir := eflRawFiles(t)
	t.Setenv("HDF5_EXTFILE_PREFIX", "")
	const dblk = 1 << 20
	names := []byte("a.raw\x00\x00\x00")
	efl := func(heap uint64) ohMsg {
		m := le64a([]byte{1, 0, 0, 0, 1, 0, 1, 0}, heap)
		return ohMsg{typ: h5oEflID, data: le64a(le64a(le64a(m, 0), 0), 8)}
	}
	heapPrefix := func(d uint64) []byte {
		hdr := append([]byte("HEAP"), 0, 0, 0, 0)
		return le64a(le64a(le64a(hdr, uint64(len(names))), 1), d)
	}
	garbage := []byte("XXXXXXXX")
	const lheapDblkID = 3 /* H5AC_LHEAP_DBLK_ID */
	for _, c := range []struct {
		name       string
		dataInFile bool                       /* the heap's data block is in the file too */
		entries    func(d uint64) []mdciEntry /* d: address of the data block */
		ok         bool
	}{
		/* One entry: the block has room for exactly one entry header
		 * (h5c_image.go:173) and the entry's image ends the block
		 * (h5c_image.go:280); the data block is read from it
		 * (cacheImageAt, cacheImageEntry) */
		{"data_block", false, func(d uint64) []mdciEntry {
			return []mdciEntry{{typ: lheapDblkID, addr: d, image: names}}
		}, true},
		/* The data block entry is a flush dependency child of an entry
		 * that comes before it (h5c_image.go:266) */
		{"flush_dependency", false, func(d uint64) []mdciEntry {
			return []mdciEntry{
				{typ: lheapDblkID, addr: 2 * d, image: garbage},
				{typ: lheapDblkID, addr: d, parents: []uint64{2 * d}, image: names},
			}
		}, true},
		/* A parent that is not in the cache yet: "fd parent not in
		 * cache?!?" */
		{"parent_after_child", false, func(d uint64) []mdciEntry {
			return []mdciEntry{
				{typ: lheapDblkID, addr: d, parents: []uint64{2 * d}, image: names},
				{typ: lheapDblkID, addr: 2 * d, image: garbage},
			}
		}, false},
		/* An entry at the end of the address space: libhdf5 checks only
		 * that the address is defined */
		{"entry_at_end_of_address_space", false, func(d uint64) []mdciEntry {
			return []mdciEntry{
				{typ: lheapDblkID, addr: d, image: names},
				{typ: lheapDblkID, addr: haddrUndef - 4, image: make([]byte, 16)},
			}
		}, true},
		/* Entries that overlap the data block without starting at its
		 * address: the metadata cache finds entries by address only, so
		 * libhdf5 reads the data block from the file */
		{"entry_overlapping_data_block", true, func(d uint64) []mdciEntry {
			return []mdciEntry{{typ: lheapDblkID, addr: d - 4, image: garbage}}
		}, true},
		{"entry_inside_data_block", true, func(d uint64) []mdciEntry {
			return []mdciEntry{{typ: lheapDblkID, addr: d + 2, image: garbage[:4]}}
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newH5Builder()
			d := uint64(dblk)
			if c.dataInFile {
				d = b.put(names)
			}
			prfx := b.put(heapPrefix(d))
			layout := le64a(le64a([]byte{3, 1}, haddrUndef), 8)
			obj := b.put(ohdr2(2, v2Msgs(spaceMsg(2), i32Type(), fillMsg(-7), layoutMsg(layout), efl(prfx))))
			root := b.rootGroup(hardLinkMsg("obj", obj))
			img := b.finishWithImage(root, mdciBlock(c.entries(d)...))
			dumpFile(t, img)
			f, err := OpenReader(bytes.NewReader(img), int64(len(img)), WithExternalFilePrefix(dir))
			if !c.ok {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			defer f.Close()
			ds, err := f.OpenDataset("/obj")
			require.NoError(t, err)
			got, err := Read[int32](ds)
			require.NoError(t, err)
			assert.Equal(t, []int32{0, 1}, got)
		})
	}
}
