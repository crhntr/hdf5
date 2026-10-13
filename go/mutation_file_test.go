package hdf5

// Tests for the file layer (h5f.go, h5f_super.go, h5f_efc.go, h5vm.go)
// written from the mutants gremlins left alive. The expected results are
// what libhdf5 1.14.6 does with the same files: set HDF5_MUTATION_DUMP to a
// directory to have the tests write the files they build there for
// checking them with libhdf5 (h5dump, h5debug or chdf5).

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appendLen appends v as an n-byte little endian integer (n up to 32;
// bytes beyond the eighth are 0, or 0xff for HADDR_UNDEF).
func appendLen(b []byte, v uint64, n int) []byte {
	for i := range n {
		switch {
		case v == haddrUndef:
			b = append(b, 0xff)
		case i < 8:
			b = append(b, byte(v>>(8*i)))
		default:
			b = append(b, 0)
		}
	}
	return b
}

// wideFile builds a file with a version 0 or 1 superblock whose addresses
// are sizeofAddr bytes and lengths sizeofSize bytes wide and an empty root
// group: a version 1 object header holding a link info message (no
// fractal heap, no name index) and a group info message.
func wideFile(version byte, sizeofAddr, sizeofSize int) []byte {
	sb := append([]byte(nil), h5fSignature...)
	sb = append(sb, version, 0, 0, 0, 0, byte(sizeofAddr), byte(sizeofSize), 0)
	sb = binary.LittleEndian.AppendUint16(sb, 4)  /* symbol table leaf K */
	sb = binary.LittleEndian.AppendUint16(sb, 16) /* B-tree internal K */
	sb = binary.LittleEndian.AppendUint32(sb, 0)  /* status flags */
	if version == 1 {
		sb = binary.LittleEndian.AppendUint16(sb, 32) /* indexed storage K */
		sb = append(sb, 0, 0)
	}
	eofAt := len(sb) + 2*sizeofAddr
	sb = appendLen(sb, 0, sizeofAddr)          /* base address */
	sb = appendLen(sb, haddrUndef, sizeofAddr) /* free space info */
	sb = appendLen(sb, 0, sizeofAddr)          /* end of file address (set below) */
	sb = appendLen(sb, haddrUndef, sizeofAddr) /* driver information block */
	rootAt := len(sb) + sizeofSize
	sb = appendLen(sb, 0, sizeofSize) /* root entry: link name offset */
	sb = appendLen(sb, 0, sizeofAddr) /* object header address (set below) */
	sb = append(sb, make([]byte, 4+4+16)...)
	for len(sb)%8 != 0 {
		sb = append(sb, 0)
	}
	root := uint64(len(sb))

	linfo := append([]byte{0, 0}, appendLen(appendLen(nil, haddrUndef, sizeofAddr), haddrUndef, sizeofAddr)...)
	body := v1Msgs(ohMsg{typ: h5oLinfoID, data: linfo}, ohMsg{typ: h5oGinfoID, data: []byte{0, 0}})
	b := append(sb, ohdr1(2, uint32(len(body)), body)...)
	copy(b[eofAt:], appendLen(nil, uint64(len(b)), sizeofAddr))
	copy(b[rootAt:], appendLen(nil, root, sizeofAddr))
	return b
}

// TestMutationWideSuperblock: superblocks with 32-byte and 16-byte
// addresses (H5F_addr_decode takes any width; libhdf5 decodes lengths of
// 2, 4 and 8 bytes only), a version 1 superblock with 32-byte addresses
// being the largest libhdf5 reads. libhdf5 opens the files (h5dump shows
// an empty root group).
func TestMutationWideSuperblock(t *testing.T) {
	for _, c := range []struct {
		version            byte
		sizeofAddr, sizeof int
	}{{1, 32, 8}, {0, 32, 8}, {1, 16, 4}, {0, 16, 2}, {1, 2, 8}, {0, 2, 2}} {
		t.Run(fmt.Sprintf("v%d_%d_%d", c.version, c.sizeofAddr, c.sizeof), func(t *testing.T) {
			img := wideFile(c.version, c.sizeofAddr, c.sizeof)
			f := builtFile(t, img)
			sb := f.Superblock()
			assert.EqualValues(t, c.version, sb.Version)
			assert.EqualValues(t, c.sizeofAddr, sb.SizeOfOffsets)
			assert.EqualValues(t, c.sizeof, sb.SizeOfLengths)
			assert.EqualValues(t, len(img), sb.EndOfFileAddress)
			assert.Equal(t, uint64(haddrUndef), sb.DriverInfoAddress)
			n, err := f.Root().NumLinks()
			require.NoError(t, err)
			assert.Zero(t, n)
		})
	}
}

// tarray1 is tools/test/h5dump/testfiles/tarray1.h5: a version 0
// superblock (sizes at offsets 13 and 14) and an array dataset.
func tarray1(t *testing.T) []byte {
	t.Helper()
	return readFile(t, filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles", "tarray1.h5"))
}

// imageDigest opens img and returns fileDigest of it.
func imageDigest(t *testing.T, img []byte) []string {
	t.Helper()
	return fileDigest(t, builtFile(t, img))
}

// TestMutationSuperblockSizeBytes: H5F__superblock_prefix_decode accepts
// 2, 4, 8, 16 and 32 as the sizes of addresses and lengths and nothing
// else, in version 0 and version 2 superblocks. libhdf5 refuses the
// patched files with "bad byte number in an address" / "bad byte number
// for object size".
func TestMutationSuperblockSizeBytes(t *testing.T) {
	b := newH5Builder()
	v2 := b.finish(b.rootGroup())
	for _, c := range []struct {
		name       string
		img        []byte
		addr, size int
	}{
		{"v0", tarray1(t), 13, 14},
		{"v2", v2, 9, 10},
	} {
		for _, v := range []byte{0, 1, 3, 6, 12, 33, 64} {
			for _, field := range []int{c.addr, c.size} {
				t.Run(fmt.Sprintf("%s_byte%d_%d", c.name, field, v), func(t *testing.T) {
					img := append([]byte(nil), c.img...)
					img[field] = v
					if c.name == "v2" {
						setChecksum(img, 0, h5BuilderSuperblockSize)
					}
					dumpFile(t, img)
					_, err := OpenReader(bytes.NewReader(img), int64(len(img)))
					require.Error(t, err)
					want := "bad byte number in an address"
					if field == c.size {
						want = "bad byte number for object size"
					}
					assert.Contains(t, err.Error(), want)
				})
			}
		}
	}
}

// TestMutationSuperblockChecksum: version 2 superblocks have a checksum
// (H5F__cache_superblock_verify_chksum). libhdf5 refuses the file with
// the last byte of the checksum changed.
func TestMutationSuperblockChecksum(t *testing.T) {
	b := newH5Builder()
	img := b.finish(b.rootGroup())
	require.EqualValues(t, 2, img[8])
	img[h5BuilderSuperblockSize-1] ^= 1
	dumpFile(t, img)
	_, err := OpenReader(bytes.NewReader(img), int64(len(img)))
	assert.ErrorIs(t, err, ErrBadChecksum)
	f, err := OpenReader(bytes.NewReader(img), int64(len(img)), WithChecksumVerification(false))
	require.NoError(t, err)
	f.Close()
}

// TestMutationSuperblockMoved: tarray1.h5 behind 512 or 2048 bytes added
// in front of it (as cat would, the stored base address stays 0): the
// signature is found at the power of two, the base address becomes that
// offset and the stored end of file address moves with it (H5F__super_read:
// "the superblock moved in the file"), so that the whole file stays
// readable. libhdf5 (h5dump) reads the files like tarray1.h5.
func TestMutationSuperblockMoved(t *testing.T) {
	orig := tarray1(t)
	want := imageDigest(t, orig)
	for _, n := range []int{512, 2048} {
		img := append(make([]byte, n), orig...)
		f := builtFile(t, img)
		assert.EqualValues(t, n, f.baseAddr)
		assert.EqualValues(t, len(orig)+n, f.eoa)
		assert.Equal(t, want, fileDigest(t, f), "user block %d", n)
	}
}

// sparseReader is a file image of size bytes whose first bytes are b and
// the rest zeros.
type sparseReader struct {
	b    []byte
	size int64
}

func (r *sparseReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.size {
		return 0, io.EOF
	}
	clear(p)
	if off < int64(len(r.b)) {
		copy(p, r.b[off:])
	}
	return len(p), nil
}

// TestMutationSuperblockReadSize: the superblock is read with a buffer of
// the largest superblock size, whatever the size of the file: a file image
// of 2^50 bytes holding tarray1.h5 opens and reads like tarray1.h5.
func TestMutationSuperblockReadSize(t *testing.T) {
	orig := tarray1(t)
	f, err := OpenReader(&sparseReader{b: orig, size: 1 << 50}, 1<<50)
	require.NoError(t, err)
	defer f.Close()
	assert.Equal(t, imageDigest(t, orig), fileDigest(t, f))
}

// TestMutationFileClose: File.Close reports the failure to close the
// file or a file opened through it (the source file of a virtual
// dataset).
func TestMutationFileClose(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles", "tarray1.h5"))
	require.NoError(t, err)
	require.NoError(t, f.fd.Close())
	assert.ErrorIs(t, f.Close(), os.ErrClosed)
	assert.NoError(t, f.Close(), "closed already")

	t.Setenv("HDF5_VDS_PREFIX", "")
	dir, name := prefixLayout(t)
	f, err = Open(name, WithVirtualPrefix(filepath.Join(dir, "b")))
	require.NoError(t, err)
	got, err := readI32(t, f, "/vds_xfile")
	require.NoError(t, err)
	assert.Equal(t, []int32{60, 61, 62, 63}, got)
	require.Len(t, f.extFiles, 1)
	for _, ef := range f.extFiles {
		require.NoError(t, ef.fd.Close())
	}
	assert.ErrorIs(t, f.Close(), os.ErrClosed)
}

// TestMutationReadEmpty: reads of zero bytes succeed (H5FD_read returns
// before checking the EOA).
func TestMutationReadEmpty(t *testing.T) {
	f := builtFile(t, tarray1(t))
	for _, addr := range []haddr{0, 100, haddr(f.eof())} {
		b, err := f.readAt(addr, 0)
		require.NoError(t, err, addr)
		assert.Empty(t, b)
		require.NoError(t, f.readInto(addr, nil), addr)
	}
}

// eofReader is a file image that returns io.EOF with the reads that end
// at the end of the image (which io.ReaderAt allows).
type eofReader struct{ r *bytes.Reader }

func (r eofReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.r.ReadAt(p, off)
	if err == nil && off+int64(n) == r.r.Size() {
		err = io.EOF
	}
	return n, err
}

// failReader is a file image whose reads overlapping [from, to) fail.
type failReader struct {
	r        *bytes.Reader
	from, to int64
}

var errFailReader = errors.New("read failed")

func (r failReader) ReadAt(p []byte, off int64) (int, error) {
	if off < r.to && off+int64(len(p)) > r.from {
		return 0, errFailReader
	}
	return r.r.ReadAt(p, off)
}

// TestMutationReadIntoErrors: raw data reads (readInto), and the reads of
// the signature and the superblock, tolerate io.EOF from a reader that
// filled the buffer and report any other read error.
// The data of /Dataset1 are the last 64 bytes of tarray1.h5.
func TestMutationReadIntoErrors(t *testing.T) {
	img := tarray1(t)
	want := imageDigest(t, img)

	f, err := OpenReader(eofReader{bytes.NewReader(img)}, int64(len(img)))
	require.NoError(t, err)
	defer f.Close()
	assert.Equal(t, want, fileDigest(t, f))

	/* A file smaller than the superblock read size: the superblock read
	 * ends at the end of the image. */
	b := newH5Builder()
	small := b.finish(b.rootGroup())
	require.Less(t, len(small), h5fSuperblockFixedSize+h5fSuperblockVarlenSize(1, 32, 32))
	f2, err := OpenReader(eofReader{bytes.NewReader(small)}, int64(len(small)))
	require.NoError(t, err)
	defer f2.Close()
	n, err := f2.Root().NumLinks()
	require.NoError(t, err)
	assert.Zero(t, n)
	/* ... and an 8-byte image whose signature read does. */
	_, err = OpenReader(eofReader{bytes.NewReader(small[:8])}, 8)
	assert.ErrorIs(t, err, &Error{Minor: H5E_OVERFLOW})
	/* A short read is an error. */
	_, err = OpenReader(eofReader{bytes.NewReader(small)}, int64(len(small))+8)
	assert.Error(t, err)

	ds, err := builtFile(t, img).OpenDataset("/Dataset1")
	require.NoError(t, err)
	layout, err := ds.Layout()
	require.NoError(t, err)
	require.Equal(t, h5.LayoutContiguous, layout.Class)
	require.EqualValues(t, 64, layout.Size)

	f, err = OpenReader(failReader{bytes.NewReader(img), int64(layout.Address), int64(layout.Address + layout.Size)}, int64(len(img)))
	require.NoError(t, err)
	defer f.Close()
	ds, err = f.OpenDataset("/Dataset1")
	require.NoError(t, err)
	_, err = ds.ReadValues()
	assert.ErrorIs(t, err, errFailReader)
}

// TestMutationPower2up: H5VM_power2up rounds up to a power of two, 0 from
// 2^63 + 1 (and from 2^63, where the C code returns 0 as well).
func TestMutationPower2up(t *testing.T) {
	for n, want := range map[uint64]uint64{
		0: 1, 1: 1, 2: 2, 3: 4, 4: 4, 5: 8, 1000: 1024, 1024: 1024,
		1 << 62: 1 << 62, 1<<62 + 1: 1 << 63, 1 << 63: 0, 1<<63 + 1: 0, haddrUndef: 0,
	} {
		assert.Equal(t, want, h5vmPower2up(n), "%d", n)
	}
}

// vdsLayout is prefixLayout with the copy of dset.h5 in dir/a also reached
// through a symbolic link dir/link/dset.h5, and copies of dset_src.h5 in
// the directories srcDirs (relative to dir; "b" holds one already).
func vdsLayout(t *testing.T, srcDirs ...string) (dir, name, link string) {
	t.Helper()
	dir, name = prefixLayout(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "link"), 0o755))
	link = filepath.Join(dir, "link", "dset.h5")
	require.NoError(t, os.Symlink(name, link))
	src := readFile(t, filepath.Join(dir, "b", "dset_src.h5"))
	for _, d := range srcDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, d, "dset_src.h5"), src, 0o644))
	}
	return dir, name, link
}

// TestMutationPrefixSearchOrder: the places H5F_prefix_open_file looks
// for the source file of /vds_xfile in dset.h5 ("dset_src.h5", values
// 60..63; 0 when it is not found) besides the prefixes: the directory of
// the file's name as opened (a symbolic link's directory), the current
// directory and the directory of the file's resolved name. chdf5 reads
// the same layouts the same way.
func TestMutationPrefixSearchOrder(t *testing.T) {
	t.Setenv("HDF5_VDS_PREFIX", "")
	src := []int32{60, 61, 62, 63}
	read := func(t *testing.T, name string) []int32 {
		t.Helper()
		f, err := Open(name)
		require.NoError(t, err)
		defer f.Close()
		got, err := readI32(t, f, "/vds_xfile")
		require.NoError(t, err)
		return got
	}

	t.Run("directory of the link", func(t *testing.T) {
		_, name, link := vdsLayout(t, "link")
		assert.Equal(t, src, read(t, link))
		assert.Equal(t, []int32{0, 0, 0, 0}, read(t, name))
	})
	t.Run("directory of the resolved name", func(t *testing.T) {
		_, _, link := vdsLayout(t, "a")
		assert.Equal(t, src, read(t, link))
	})
	t.Run("current directory", func(t *testing.T) {
		dir, name, _ := vdsLayout(t)
		t.Chdir(filepath.Join(dir, "b"))
		assert.Equal(t, src, read(t, name))
	})
}

// TestMutationEfcOpen: the external file cache returns the file itself for
// its own name, refuses directories and reports why a file was not found.
func TestMutationEfcOpen(t *testing.T) {
	t.Setenv("HDF5_EXT_PREFIX", "")
	dir, name := prefixLayout(t)
	f, err := Open(name)
	require.NoError(t, err)
	defer f.Close()

	self, err := f.efcOpen(name)
	require.NoError(t, err)
	assert.Same(t, f, self)
	wd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(wd, name)
	require.NoError(t, err)
	self, err = f.efcOpen(rel)
	require.NoError(t, err)
	assert.Same(t, f, self)
	assert.Empty(t, f.extFiles)

	_, err = f.efcOpen(filepath.Join(dir, "b"))
	assert.ErrorIs(t, err, fs.ErrNotExist, "a directory")

	_, err = f.prefixOpenFile(h5fPrefixElink, "", "no-such-file.h5")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.ErrorIs(t, err, &Error{Minor: H5E_CANTOPENFILE})

}
