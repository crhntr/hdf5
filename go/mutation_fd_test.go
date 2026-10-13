package hdf5

// Tests for the family and multi/split drivers (h5fd_family.go,
// h5fd_multi.go) written from the mutants gremlins left alive. The member
// files are corpus files copied (and byte-patched) into temporary
// directories; set HDF5_MUTATION_DUMP to a directory to keep a copy of the
// patched sets there for checking them with libhdf5 (h5dump
// --filedriver=multi|family, or chdf5 with WithDriver).

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMembers writes the files of a member set (name -> contents) to dir
// and, when HDF5_MUTATION_DUMP is set, to a directory named after the test
// under it.
func writeMembers(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	dirs := []string{dir}
	if d := os.Getenv("HDF5_MUTATION_DUMP"); d != "" {
		d = filepath.Join(d, strings.ReplaceAll(t.Name(), "/", "_"))
		require.NoError(t, os.MkdirAll(d, 0o755))
		dirs = append(dirs, d)
	}
	for _, d := range dirs {
		for name, b := range files {
			require.NoError(t, os.WriteFile(filepath.Join(d, name), b, 0o644))
		}
	}
}

// fileDigest walks f and returns, for every object, its path and kind and,
// for datasets, the fingerprint of the values.
func fileDigest(t *testing.T, f *File) []string {
	t.Helper()
	var out []string
	for v, err := range f.Root().Walk() {
		require.NoError(t, err)
		line := v.Path + " " + linkTok(v.Link)
		if ds, ok := v.Object.(*Dataset); ok {
			vals, err := ds.ReadValues()
			require.NoError(t, err, v.Path)
			line += " " + h5.Fingerprint(vals)
		}
		out = append(out, line)
	}
	require.NotEmpty(t, out)
	return out
}

// tmultiDigest is the digest of tools/test/h5dump/testfiles/tmulti.
func tmultiDigest(t *testing.T) []string {
	t.Helper()
	f, err := openViaDrivers(t, filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles", "tmulti"))
	require.NoError(t, err)
	return fileDigest(t, f)
}

// tmultiMembers returns the members of tmulti keyed by their letter.
func tmultiMembers(t *testing.T) map[string][]byte {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles")
	m := map[string][]byte{}
	for _, l := range []string{"s", "b", "r", "g", "l", "o"} {
		m[l] = readFile(t, filepath.Join(dir, "tmulti-"+l+".h5"))
	}
	return m
}

// multiDriverInfo encodes the "NCSAmult" driver information of tmulti
// (the member addresses and EOAs of tmulti-s.h5) with the memory map mmap
// and the member name templates names (in member order).
func multiDriverInfo(t *testing.T, sMember []byte, mmap [6]byte, names []string) []byte {
	t.Helper()
	info := append(mmap[:], 0, 0)
	info = append(info, sMember[0x78:0x78+6*16]...)
	for _, n := range names {
		b := append([]byte(n), 0)
		for len(b)%8 != 0 {
			b = append(b, 0)
		}
		info = append(info, b...)
	}
	return info
}

// setMultiDriverInfo replaces the driver information block of a copy of
// tmulti-s.h5 (at 0x60; the member holds nothing else) with info.
func setMultiDriverInfo(sMember, info []byte) []byte {
	b := append([]byte(nil), sMember...)
	clear(b[0x70:])
	binary.LittleEndian.PutUint32(b[0x64:], uint32(len(info)))
	copy(b[0x70:], info)
	return b
}

// TestMutationMultiNameTemplates: tmulti with member name templates of
// 7 to 10 characters (stored NUL terminated and padded to multiples of 8:
// 8 or 16 bytes, H5FD_multi_sb_decode), and with the memory map entry of
// the superblock set to H5FD_MEM_DEFAULT (meaning the type itself, in
// open and read). libhdf5 (h5dump --filedriver=multi m) reads both like
// tmulti.
func TestMutationMultiNameTemplates(t *testing.T) {
	want := tmultiDigest(t)
	membs := tmultiMembers(t)
	letters := []string{"s", "bb", "rrr", "g", "llll", "o"}
	var names []string
	for _, l := range letters {
		names = append(names, "%s-"+l+".h5")
	}

	for _, tc := range []struct {
		name string
		mmap [6]byte
	}{
		{"templates", [6]byte{1, 2, 3, 4, 5, 6}},
		{"default super map", [6]byte{0, 2, 3, 4, 5, 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := multiDriverInfo(t, membs["s"], tc.mmap, names)
			files := map[string][]byte{}
			for _, l := range letters {
				b := membs[l[:1]]
				if l == "s" {
					b = setMultiDriverInfo(b, info)
				}
				files["m-"+l+".h5"] = b
			}
			dir := t.TempDir()
			writeMembers(t, dir, files)

			d, err := h5fdMultiSbDecode(info)
			require.NoError(t, err)
			for i, n := range names {
				assert.Equal(t, n, d.membName[i+1])
			}

			f, err := Open(filepath.Join(dir, "m-s.h5"))
			require.NoError(t, err)
			defer f.Close()
			md, ok := f.fd.(*multiDriver)
			require.True(t, ok, "driver is %T", f.fd)
			assert.Equal(t, filepath.Join(dir, "m"), md.name)
			assert.Equal(t, want, fileDigest(t, f))
		})
	}

	// A map entry beyond H5FD_MEM_OHDR is out of range.
	for _, v := range []byte{7, 8, 255} {
		_, err := h5fdMultiSbDecode(multiDriverInfo(t, membs["s"], [6]byte{1, 2, v, 4, 5, 6}, names))
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADRANGE}, "map entry %d", v)
	}
}

// TestMutationMultiComputeNext: memb_next of compute_next, the address of
// the member that follows each member in the address space (HADDR_MAX for
// the last one, HADDR_UNDEF for the types that are not members), for the
// tmulti layout, for members whose addresses are not in memory type order
// and for the split layout.
func TestMutationMultiComputeNext(t *testing.T) {
	const hmax = haddrUndef - 1 /* HADDR_MAX */
	for _, tc := range []struct {
		name string
		mmap [h5fdMemNTypes]h5fdMem
		addr [h5fdMemNTypes]haddr
		want [h5fdMemNTypes]haddr
	}{
		{
			"tmulti",
			[h5fdMemNTypes]h5fdMem{0, 1, 2, 3, 4, 5, 6},
			[h5fdMemNTypes]haddr{haddrUndef, 0, 0x1999999999999999, 0x3333333333333332, 0x4ccccccccccccccb, 0x6666666666666664, 0x7ffffffffffffffd},
			[h5fdMemNTypes]haddr{haddrUndef, 0x1999999999999999, 0x3333333333333332, 0x4ccccccccccccccb, 0x6666666666666664, 0x7ffffffffffffffd, hmax},
		},
		{
			"unordered",
			[h5fdMemNTypes]h5fdMem{0, 1, 2, 3, 4, 5, 6},
			[h5fdMemNTypes]haddr{haddrUndef, 0, 0x3000, 0x1000, 0x2000, 0x5000, 0x4000},
			[h5fdMemNTypes]haddr{haddrUndef, 0x1000, 0x4000, 0x2000, 0x3000, hmax, 0x5000},
		},
		{
			"split",
			[h5fdMemNTypes]h5fdMem{0, 1, 1, 3, 1, 1, 1},
			[h5fdMemNTypes]haddr{haddrUndef, 0, haddrUndef, 0x7fffffffffffffff, haddrUndef, haddrUndef, haddrUndef},
			[h5fdMemNTypes]haddr{haddrUndef, 0x7fffffffffffffff, haddrUndef, hmax, haddrUndef, haddrUndef, haddrUndef},
		},
	} {
		d := &multiDriver{membMap: tc.mmap, membAddr: tc.addr}
		d.computeNext()
		assert.Equal(t, tc.want, d.membNext, tc.name)
	}
}

// memberFile creates a member file of n bytes (byte i is i%251) in dir.
func memberFile(t *testing.T, dir, name string, n int) *sec2Driver {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o644))
	m, err := h5fdSec2OpenMember(filepath.Join(dir, name))
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })
	return m
}

// TestMutationMultiEOF: H5FD_multi_get_eof is the largest end over the
// members of member address + member size, where an empty member ends at
// 0 (not at its address) and the sum is limited to HADDR_MAX.
func TestMutationMultiEOF(t *testing.T) {
	dir := t.TempDir()
	d := &multiDriver{membMap: [h5fdMemNTypes]h5fdMem{0, 1, 2, 3, 4, 5, 6}}
	d.membAddr = [h5fdMemNTypes]haddr{haddrUndef, 0, 0x100, 0x10000, haddrUndef, haddrUndef, haddrUndef}
	d.memb[h5fdMemSuper] = memberFile(t, dir, "s", 100)
	d.memb[h5fdMemBtree] = memberFile(t, dir, "b", 0x200)
	d.memb[h5fdMemDraw] = memberFile(t, dir, "r", 0) /* empty: ends at 0 */
	assert.EqualValues(t, 0x300, d.EOF())

	d.membAddr[h5fdMemDraw] = haddrUndef - 0x100
	d.memb[h5fdMemDraw] = memberFile(t, dir, "r2", 0x200)
	assert.EqualValues(t, haddrUndef-1, d.EOF())
}

// TestMutationMultiReadClose: a read through the multi driver reports the
// number of bytes read (io.ReaderAt), and closing reports the failure to
// close a member (H5FD_multi_close: "error closing member files").
func TestMutationMultiReadClose(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles")
	f, err := openViaDrivers(t, filepath.Join(dir, "tmulti"))
	require.NoError(t, err)
	md := f.fd.(*multiDriver)
	buf := make([]byte, 64)
	off := uint64(0x7ffffffffffffffd + 16) /* tmulti-o.h5 offset 16 */
	n, err := md.ReadAt(buf, int64(off))
	require.NoError(t, err)
	assert.Equal(t, len(buf), n)
	assert.Equal(t, readFile(t, filepath.Join(dir, "tmulti-o.h5"))[16:80], buf)
	/* An empty read succeeds (H5FD_read returns before the EOA check). */
	n, err = md.ReadAt(nil, int64(off))
	require.NoError(t, err)
	assert.Zero(t, n)

	require.NoError(t, md.memb[h5fdMemBtree].Close())
	assert.Error(t, md.Close())
	assert.Equal(t, [h5fdMemNTypes]*sec2Driver{}, md.memb)
	assert.NoError(t, md.Close())
}

// TestMutationMultiBaseName: the base name of a multi file opened by the
// name of its superblock member is that name without the suffix of the
// member's name template, unless nothing would remain: libhdf5 opens no
// file by an empty name (H5Fopen: "invalid file name").
func TestMutationMultiBaseName(t *testing.T) {
	d := &multiDriver{membMap: [h5fdMemNTypes]h5fdMem{0, 1, 2, 3, 4, 5, 6}}
	for mt := h5fdMemSuper; mt < h5fdMemNTypes; mt++ {
		d.membName[mt] = "%s-" + string("sbrglo"[mt-1]) + ".h5"
	}
	assert.Equal(t, "dir/x", d.baseFromMemberName("dir/x-s.h5"))
	assert.Equal(t, "x", d.baseFromMemberName("x-o.h5"))
	assert.Equal(t, "-s.h5", d.baseFromMemberName("-s.h5"))
	assert.Equal(t, "x.h5", d.baseFromMemberName("x.h5"))
}

// TestMutationMultiExpandName: the snprintf of a member name template in
// open_members: "%s" is the base name, "%%" a percent sign, and a name of
// H5FD_MULT_MAX_FILE_NAME_LEN (1024) characters or more would be
// truncated ("filename is too long and would be truncated").
func TestMutationMultiExpandName(t *testing.T) {
	got, err := h5fdMultiExpandName("%s-%%.h5", "x")
	require.NoError(t, err)
	assert.Equal(t, "x-%.h5", got)
	for _, tmpl := range []string{"%s%", "%", "%s-%d.h5"} {
		_, err := h5fdMultiExpandName(tmpl, "x")
		assert.Error(t, err, tmpl)
	}
	long := strings.Repeat("a", 1023-len("-s.h5"))
	got, err = h5fdMultiExpandName("%s-s.h5", long)
	require.NoError(t, err)
	assert.Len(t, got, 1023)
	_, err = h5fdMultiExpandName("%s-s.h5", long+"a")
	assert.Error(t, err)
}

// TestMutationFamilyNames: H5FD__family_get_default_printf_filename finds
// ".h5" and "." anywhere in the name, the first character included; the
// snprintf of the member names supports %d/%i/%u with a 0 flag and a width
// (at most 255 here) and nothing else; the member name generator derived
// from a member name covers every trailing digit.
func TestMutationFamilyNames(t *testing.T) {
	for in, want := range map[string]string{
		".h5.x": "-%06d.h5",
		".x":    "-%06d.x",
		".h5":   "-%06d.h5",
	} {
		got, err := h5fdFamilyGetDefaultPrintfFilename(in)
		require.NoError(t, err)
		assert.Equal(t, want, got, in)
	}

	for _, c := range []struct {
		pattern string
		idx     uint
		want    string
	}{
		{"f%10d", 3, "f         3"},
		{"f%9d", 3, "f        3"},
		{"f%010d", 42, "f0000000042"},
		{"f%d", 12345, "f12345"},
		{"f%2i.h5", 123, "f123.h5"},
		{"f%255u", 7, "f" + strings.Repeat(" ", 254) + "7"},
	} {
		got, verbs, ok := h5fdFamilyMemberName(c.pattern, c.idx)
		require.True(t, ok, c.pattern)
		assert.Equal(t, 1, verbs)
		assert.Equal(t, c.want, got, c.pattern)
	}
	for _, p := range []string{"f%5", "f%05", "f%0", "f%256d", "f%9x"} {
		_, _, ok := h5fdFamilyMemberName(p, 0)
		assert.False(t, ok, p)
	}

	for member, want := range map[string]string{
		"x19.h5": "x%02d.h5",
		"x9":     "x%01d",
		"a90.h5": "a%02d.h5",
	} {
		got, ok := h5fdFamilyPatternFromMember(member)
		require.True(t, ok, member)
		assert.Equal(t, want, got, member)
	}
}

// TestMutationFamilySec2Read: H5FD__sec2_read fills the part of a read
// beyond the end of the file with zeros.
func TestMutationFamilySec2Read(t *testing.T) {
	m := memberFile(t, t.TempDir(), "m", 10)
	buf := make([]byte, 20)
	for i := range buf {
		buf[i] = 0xee
	}
	require.NoError(t, h5fdSec2Read(m, buf, 5))
	assert.Equal(t, append([]byte{5, 6, 7, 8, 9}, make([]byte, 15)...), buf)
	require.NoError(t, h5fdSec2Read(m, buf, 10))
	assert.Equal(t, make([]byte, 20), buf)
}

// TestMutationFamilyOpenEmptyFirst: a family whose first member is empty
// keeps the member size of the access properties (H5FD__family_open
// updates it from the first member's size only when that is not 0), the
// default H5FD_FAM_DEF_MEM_SIZE when none is given; reads beyond the last
// member fail.
func TestMutationFamilyOpenEmptyFirst(t *testing.T) {
	dir := t.TempDir()
	memberFile(t, dir, "e00000.h5", 0)
	memberFile(t, dir, "e00001.h5", 100)
	for _, c := range []struct{ pmem, want uint64 }{{0, h5fdFamDefMemSize}, {512, 512}} {
		d, err := h5fdFamilyOpen(filepath.Join(dir, "e%05d.h5"), c.pmem)
		require.NoError(t, err)
		assert.Equal(t, c.want, d.membSize)
		assert.Len(t, d.memb, 2)
		assert.Equal(t, c.want+100, d.EOF())

		buf := make([]byte, 8)
		n, err := d.ReadAt(buf, int64(c.want)+4)
		require.NoError(t, err)
		assert.Equal(t, 8, n)
		assert.Equal(t, []byte{4, 5, 6, 7, 8, 9, 10, 11}, buf)
		_, err = d.ReadAt(buf, int64(2*c.want))
		assert.Error(t, err, "beyond the last member")
		_, err = d.ReadAt(buf, int64(2*c.want)-4)
		assert.Error(t, err, "into the member after the last")
		require.NoError(t, d.Close())
	}
}

// TestMutationFamilySetEOA: H5FD__family_set_eoa gives every member an
// end-of-address marker: the member size up to the member holding the
// EOA, the rest of the EOA there and 0 after it (reads there fail).
func TestMutationFamilySetEOA(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		memberFile(t, dir, fmt.Sprintf("s%05d.h5", i), 100)
	}
	d, err := h5fdFamilyOpen(filepath.Join(dir, "s%05d.h5"), 0)
	require.NoError(t, err)
	defer d.Close()
	require.EqualValues(t, 100, d.membSize)

	for _, c := range []struct {
		eoa  uint64
		want []uint64
	}{
		{250, []uint64{100, 100, 50}},
		{100, []uint64{100, 0, 0}},
		{150, []uint64{100, 50, 0}},
		{1000, []uint64{100, 100, 100}},
		{0, []uint64{0, 0, 0}},
	} {
		d.setEOA(c.eoa)
		assert.Equal(t, c.want, d.membEOA, "eoa %d", c.eoa)
	}

	d.setEOA(150)
	buf := make([]byte, 10)
	_, err = d.ReadAt(buf, 140)
	require.NoError(t, err)
	assert.Equal(t, []byte{40, 41, 42, 43, 44, 45, 46, 47, 48, 49}, buf)
	_, err = d.ReadAt(buf, 145)
	assert.Error(t, err)
	_, err = d.ReadAt(buf, 200)
	assert.Error(t, err)
	d.setEOA(0)
	_, err = d.ReadAt(buf, 0)
	assert.Error(t, err)
}

// TestMutationFamilyClose: closing a family closes every member and
// reports the failure to close one (H5FD__family_close).
func TestMutationFamilyClose(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		memberFile(t, dir, fmt.Sprintf("c%05d.h5", i), 100)
	}
	d, err := h5fdFamilyOpen(filepath.Join(dir, "c%05d.h5"), 0)
	require.NoError(t, err)
	membs := d.memb
	require.NoError(t, d.Close())
	for _, m := range membs {
		_, err := m.f.Stat()
		assert.ErrorIs(t, err, os.ErrClosed)
	}

	d, err = h5fdFamilyOpen(filepath.Join(dir, "c%05d.h5"), 0)
	require.NoError(t, err)
	require.NoError(t, d.memb[1].Close())
	assert.ErrorIs(t, d.Close(), os.ErrClosed)
}

// TestMutationFamilyEOF: H5FD__family_get_eof is the size of the last
// non-empty member plus the size of the members before it: a family of a
// single member (tarray1.h5 as one%05d.h5) and family_v16 with an empty
// fifth member open and read like the original files. libhdf5
// (h5dump --filedriver=family) reads both like the original files.
func TestMutationFamilyEOF(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()

	orig, err := Open(filepath.Join(root, "tools", "test", "h5dump", "testfiles", "tarray1.h5"))
	require.NoError(t, err)
	defer orig.Close()
	one := readFile(t, filepath.Join(root, "tools", "test", "h5dump", "testfiles", "tarray1.h5"))
	writeMembers(t, dir, map[string][]byte{"one00000.h5": one})
	f, err := Open(filepath.Join(dir, "one%05d.h5"))
	require.NoError(t, err)
	defer f.Close()
	require.IsType(t, &familyDriver{}, f.fd)
	assert.EqualValues(t, len(one), f.fd.EOF())
	assert.Equal(t, fileDigest(t, orig), fileDigest(t, f))

	v16 := filepath.Join(root, "test", "testfiles", "family_v16-%06d.h5")
	orig2, err := Open(v16)
	require.NoError(t, err)
	defer orig2.Close()
	files := map[string][]byte{"v16-000004.h5": {}}
	for i := range 4 {
		files[fmt.Sprintf("v16-%06d.h5", i)] = readFile(t, fmt.Sprintf(v16, i))
	}
	writeMembers(t, dir, files)
	f2, err := Open(filepath.Join(dir, "v16-%06d.h5"))
	require.NoError(t, err)
	defer f2.Close()
	require.Len(t, f2.fd.(*familyDriver).memb, 5)
	assert.EqualValues(t, 3*5120+4048, f2.fd.EOF())
	assert.Equal(t, fileDigest(t, orig2), fileDigest(t, f2))
}

// TestMutationFamilyExtDriverInfo: cover_fam (version 3 superblock, the
// family driver information a message of the superblock extension, 2048
// byte members) joined into a single member file whose driver information
// says 4096-byte members, opened by the member name: the superblock is
// read with the flat driver, so the EOA (0xcb0) is set before the family
// takes over, which splits it over the members with the member size of
// the time (the size of the only member) before the driver information
// sets the member size (H5F__super_read, H5FD__family_set_eoa,
// H5FD__family_sb_decode). The member's EOA bounds the reads through the
// driver. libhdf5 (h5dump --filedriver=family j%05d.h5) reads the file
// like cover_fam.
func TestMutationFamilyExtDriverInfo(t *testing.T) {
	orig, err := Open("testdata/cover_fam%05d.h5")
	require.NoError(t, err)
	defer orig.Close()

	b := append(readFile(t, "testdata/cover_fam00000.h5"), readFile(t, "testdata/cover_fam00001.h5")...)
	require.Equal(t, 0xcb0, len(b))
	/* the driver info message (type 0x14) of the extension's object header at 0x30 */
	require.Equal(t, byte(0x14), b[0x47])
	require.Equal(t, "NCSAfami", string(b[0x4c:0x54]))
	require.EqualValues(t, 2048, binary.LittleEndian.Uint64(b[0x56:]))
	binary.LittleEndian.PutUint64(b[0x56:], 4096)
	setChecksum(b, 0x30, 0x62)

	dir := t.TempDir()
	writeMembers(t, dir, map[string][]byte{"j00000.h5": b})
	f, err := Open(filepath.Join(dir, "j00000.h5"))
	require.NoError(t, err)
	defer f.Close()
	fam, ok := f.fd.(*familyDriver)
	require.True(t, ok, "driver is %T", f.fd)
	assert.EqualValues(t, 4096, fam.membSize)
	assert.Equal(t, []uint64{0xcb0}, fam.membEOA)
	assert.Equal(t, fileDigest(t, orig), fileDigest(t, f))

	buf := make([]byte, 16)
	_, err = fam.ReadAt(buf, 0xcb0-16)
	require.NoError(t, err)
	_, err = fam.ReadAt(buf, 0xcb0-8)
	assert.Error(t, err, "beyond the member's EOA")
}

// TestMutationFamilyTwoMembersNoDriverInfo: the first two members of
// family_v16 (written by HDF5 1.6 without driver information) with the
// superblock's end of file address set to their total size, 10240: the
// first member opened by name is recognised as a member of a two-member
// family. libhdf5 opens it with the family driver (H5Pset_fapl_family with
// H5F_FAMILY_DEFAULT: 5120-byte members) and lists the root group; the
// dataset does not open (H5D__contig_init: its data lies beyond the EOA).
func TestMutationFamilyTwoMembersNoDriverInfo(t *testing.T) {
	v16 := filepath.Join(repoRoot(t), "test", "testfiles", "family_v16-%06d.h5")
	m0 := readFile(t, fmt.Sprintf(v16, 0))
	require.EqualValues(t, 3*5120+4048, binary.LittleEndian.Uint64(m0[0x28:]))
	binary.LittleEndian.PutUint64(m0[0x28:], 2*5120)
	dir := t.TempDir()
	writeMembers(t, dir, map[string][]byte{"two-000000.h5": m0, "two-000001.h5": readFile(t, fmt.Sprintf(v16, 1))})

	f, err := Open(filepath.Join(dir, "two-000000.h5"))
	require.NoError(t, err)
	defer f.Close()
	fam, ok := f.fd.(*familyDriver)
	require.True(t, ok, "driver is %T", f.fd)
	assert.Len(t, fam.memb, 2)
	assert.EqualValues(t, 2*5120, fam.EOF())
	l, err := f.Root().Link("dataset")
	require.NoError(t, err)
	assert.Equal(t, h5.LinkHard, l.Type)
	_, err = f.OpenDataset("/dataset")
	assert.Error(t, err, "H5D__contig_init: the storage extends beyond the EOA")
}
