package hdf5

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openViaDrivers opens name the way Open does when openDriver is wired to
// openMemberDriver: names that need the member-set aware drivers (patterns,
// multi base names, family members without driver information) go through
// openMemberDriver, anything else is a flat file whose driver is replaced by
// sbLoad.
func openViaDrivers(t *testing.T, name string, opts ...Option) (*File, error) {
	t.Helper()
	o := defaultOpenOptions()
	for _, opt := range opts {
		opt(&o)
	}
	fd, err := openMemberDriver(name, &o)
	if err != nil {
		return nil, err
	}
	if fd == nil {
		fd, err = openDriver(name, &o)
		if err != nil {
			return nil, err
		}
	}
	f, err := openWithDriver(name, fd, o)
	if err != nil {
		fd.Close()
		return nil, err
	}
	t.Cleanup(func() { f.Close() })
	return f, nil
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	require.NoError(t, err)
	return b
}

// familyBytes reads the members name(0), name(1), ... of a family and
// returns their concatenation with every member but the last padded to
// membSize.
func familyBytes(t *testing.T, pattern string, membSize int) []byte {
	t.Helper()
	var all []byte
	for i := 0; ; i++ {
		b, err := os.ReadFile(fmt.Sprintf(pattern, i))
		if err != nil {
			break
		}
		if len(all) > 0 && len(all)%membSize != 0 {
			t.Fatalf("member %d follows a short member", i)
		}
		all = append(all, b...)
	}
	return all
}

func TestFamilyV16(t *testing.T) {
	// Created by the 1.6 library: no driver information block, member size
	// 5120 (the size of the first member), 3 full members and a short last
	// one.
	dir := filepath.Join(repoRoot(t), "test", "testfiles")
	pattern := filepath.Join(dir, "family_v16-%06d.h5")
	want := familyBytes(t, pattern, 5120)
	require.Len(t, want, 3*5120+4048)

	for _, name := range []string{pattern, fmt.Sprintf(pattern, 0)} {
		t.Run(name, func(t *testing.T) {
			f, err := openViaDrivers(t, name)
			require.NoError(t, err)

			fam, ok := f.fd.(*familyDriver)
			require.True(t, ok, "driver is %T", f.fd)
			assert.Len(t, fam.memb, 4)
			assert.EqualValues(t, 5120, fam.membSize)
			assert.EqualValues(t, len(want), f.fd.EOF())

			sb := f.Superblock()
			assert.EqualValues(t, 0, sb.Version)
			assert.EqualValues(t, 8, sb.SizeOfOffsets)
			assert.EqualValues(t, 8, sb.SizeOfLengths)
			assert.EqualValues(t, 4, sb.SymLeafK)
			assert.EqualValues(t, 16, sb.SnodeBtreeK)
			assert.EqualValues(t, 32, sb.ChunkBtreeK)
			assert.EqualValues(t, 0x3a0, sb.RootGroupAddress)
			assert.EqualValues(t, len(want), sb.EndOfFileAddress)

			// A read in the last member, one that spans members and one
			// that spans three.
			for _, r := range []struct{ addr, n int }{
				{3*5120 + 100, 64}, {5120 - 10, 40}, {5000, 2*5120 + 200}, {0, len(want)}, {len(want) - 1, 1},
			} {
				got, err := f.readAt(haddr(r.addr), r.n)
				require.NoError(t, err)
				assert.Equal(t, want[r.addr:r.addr+r.n], got, "addr %d", r.addr)
			}

			// Reads beyond the end of the family fail.
			_, err = f.readAt(haddr(len(want)-1), 2)
			assert.Error(t, err)
		})
	}

	t.Run("member size override", func(t *testing.T) {
		f, err := openViaDrivers(t, pattern, func(o *openOptions) { o.familyMemberSize = 5120 })
		require.NoError(t, err)
		assert.EqualValues(t, len(want), f.fd.EOF())
	})
}

func TestFamilyTfamily(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles")
	pattern := filepath.Join(dir, "tfamily%05d.h5")
	want := familyBytes(t, pattern, 256)
	require.Len(t, want, 10*256+88)

	for _, name := range []string{pattern, fmt.Sprintf(pattern, 0)} {
		f, err := openViaDrivers(t, name)
		require.NoError(t, err, name)
		fam, ok := f.fd.(*familyDriver)
		require.True(t, ok, "driver is %T", f.fd)
		assert.Len(t, fam.memb, 11)
		assert.EqualValues(t, 256, fam.membSize)
		assert.EqualValues(t, len(want), f.fd.EOF())
		sb := f.Superblock()
		assert.EqualValues(t, 0x3a0, sb.RootGroupAddress)
		assert.EqualValues(t, 0xa58, sb.EndOfFileAddress)

		got, err := f.readAt(250, 2600-250)
		require.NoError(t, err)
		assert.Equal(t, want[250:2600], got)
	}
}

func TestFamilyWithDriverInfo(t *testing.T) {
	// The superblock holds the driver information block: Open(member name)
	// switches to the family driver by itself.
	dir := filepath.Join(repoRoot(t), "tools", "test", "misc", "testfiles")
	pattern := filepath.Join(dir, "family_file%05d.h5")
	want := familyBytes(t, pattern, 1024)
	require.Len(t, want, 18*1024)

	for _, name := range []string{fmt.Sprintf(pattern, 0), pattern} {
		var f *File
		var err error
		if name == pattern {
			f, err = openViaDrivers(t, name)
		} else {
			f, err = Open(name)
			if err == nil {
				t.Cleanup(func() { f.Close() })
			}
		}
		require.NoError(t, err, name)
		assert.Equal(t, "NCSAfami", f.Superblock().DriverName)
		fam, ok := f.fd.(*familyDriver)
		require.True(t, ok, "driver is %T", f.fd)
		assert.Len(t, fam.memb, 18)
		assert.EqualValues(t, 1024, fam.membSize)
		assert.EqualValues(t, len(want), f.fd.EOF())
		assert.EqualValues(t, len(want), f.Superblock().EndOfFileAddress)

		for _, addr := range []int{0, 1000, 1024*7 + 5, 1024*17 + 1000} {
			n := 24
			if addr+n > len(want) {
				n = len(want) - addr
			}
			got, err := f.readAt(haddr(addr), n)
			require.NoError(t, err)
			assert.Equal(t, want[addr:addr+n], got)
		}
	}

	t.Run("member size from property", func(t *testing.T) {
		name := fmt.Sprintf(pattern, 0)
		f, err := Open(name, func(o *openOptions) { o.familyMemberSize = 1024 })
		require.NoError(t, err)
		f.Close()

		// The size in the superblock wins: a different size is an error
		// ("Family member size should be ...").
		_, err = Open(name, func(o *openOptions) { o.familyMemberSize = 2048 })
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADVALUE})
	})
}

func TestFamilyNames(t *testing.T) {
	for _, c := range []struct {
		member, pattern string
	}{
		{"test/testfiles/family_v16-000000.h5", "test/testfiles/family_v16-%06d.h5"},
		{"tfamily00000.h5", "tfamily%05d.h5"},
		{"a/b.c/fam12.hdf5", "a/b.c/fam%02d.hdf5"},
		{"x7", "x%01d"},
		{"100%-000.h5", "100%%-%03d.h5"},
	} {
		got, ok := h5fdFamilyPatternFromMember(c.member)
		assert.True(t, ok, c.member)
		assert.Equal(t, c.pattern, got)
	}
	for _, name := range []string{"plain.h5", "dir1/name.h5", "a1b.h5", ""} {
		_, ok := h5fdFamilyPatternFromMember(name)
		assert.False(t, ok, name)
	}

	n, verbs, ok := h5fdFamilyMemberName("f-%06d.h5", 12)
	assert.Equal(t, "f-000012.h5", n)
	assert.Equal(t, 1, verbs)
	assert.True(t, ok)
	n, _, _ = h5fdFamilyMemberName("f%5d%%.h5", 3)
	assert.Equal(t, "f    3%.h5", n)
	_, _, ok = h5fdFamilyMemberName("f%s.h5", 0)
	assert.False(t, ok)
	_, _, ok = h5fdFamilyMemberName("f%d%d.h5", 0)
	assert.False(t, ok)
	_, _, ok = h5fdFamilyMemberName("f%", 0)
	assert.False(t, ok)
	assert.True(t, h5fdFamilyIsPattern("x%d"))
	assert.False(t, h5fdFamilyIsPattern("x.h5"))

	for in, want := range map[string]string{
		"file1.h5":   "file1-%06d.h5",
		"file1.hdf":  "file1-%06d.hdf",
		"file1":      "file1-%06d",
		"a.b/c.h5.x": "a.b/c-%06d.h5",
	} {
		got, err := h5fdFamilyGetDefaultPrintfFilename(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := h5fdFamilyGetDefaultPrintfFilename("")
	assert.Error(t, err)
}

func TestFamilySbDecode(t *testing.T) {
	d := &familyDriver{}
	assert.Error(t, d.sbDecode([]byte{1, 2, 3}))
	assert.Error(t, d.sbDecode(make([]byte, 8)), "member size 0")

	require.NoError(t, d.sbDecode([]byte{0, 4, 0, 0, 0, 0, 0, 0}))
	assert.EqualValues(t, 1024, d.membSize)
	assert.EqualValues(t, 1024, d.pmemSize)
	assert.Error(t, d.sbDecode([]byte{0, 8, 0, 0, 0, 0, 0, 0}), "differs from the property")
}

func TestFamilyReadAtOutOfRange(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools", "test", "misc", "testfiles")
	f, err := openViaDrivers(t, filepath.Join(dir, "family_file%05d.h5"))
	require.NoError(t, err)
	_, err = f.fd.ReadAt(make([]byte, 4), -1)
	assert.Error(t, err)
	_, err = f.fd.ReadAt(make([]byte, 4), 18*1024+1000)
	assert.Error(t, err)
}

func TestMultiSbDecodeCorrupt(t *testing.T) {
	good := readFile(t, filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles", "tmulti-s.h5"))[0x70:]
	// The driver information block is 0x98 bytes long.
	good = good[:0x98]

	d, err := h5fdMultiSbDecode(good)
	require.NoError(t, err)
	assert.Equal(t, [h5fdMemNTypes]h5fdMem{0, 1, 2, 3, 4, 5, 6}, d.membMap)
	assert.Equal(t, []h5fdMem{1, 2, 3, 4, 5, 6}, h5fdMultiUniqueMembers(&d.membMap))
	assert.EqualValues(t, 0, d.membAddr[h5fdMemSuper])
	assert.EqualValues(t, 0x1999999999999999, d.membAddr[h5fdMemBtree])
	assert.EqualValues(t, 0x3333333333333332, d.membAddr[h5fdMemDraw])
	assert.EqualValues(t, 0x4ccccccccccccccb, d.membAddr[h5fdMemGheap])
	assert.EqualValues(t, 0x6666666666666664, d.membAddr[h5fdMemLheap])
	assert.EqualValues(t, 0x7ffffffffffffffd, d.membAddr[h5fdMemOhdr])
	for mt := h5fdMemSuper; mt < h5fdMemNTypes; mt++ {
		if mt == h5fdMemGheap {
			assert.EqualValues(t, 0, d.membEOA[mt]) // tmulti-g.h5 is empty
		} else {
			assert.EqualValues(t, 0x800, d.membEOA[mt])
		}
		assert.Equal(t, "%s-"+string("sbrglo"[mt-1])+".h5", d.membName[mt])
	}

	// Every truncation of the block is an error, never a panic.
	for n := 0; n < len(good)-4; n++ {
		_, err := h5fdMultiSbDecode(good[:n])
		if n < 8+6*16+6*8-6 {
			assert.Error(t, err, "length %d", n)
		}
	}
	// A map entry out of range.
	bad := append([]byte(nil), good...)
	bad[2] = 9
	_, err = h5fdMultiSbDecode(bad)
	assert.Error(t, err)
}

func TestMultiTmulti(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles")
	type memb struct {
		letter string
		addr   haddr
	}
	membs := []memb{
		{"s", 0}, {"b", 0x1999999999999999}, {"r", 0x3333333333333332},
		{"g", 0x4ccccccccccccccb}, {"l", 0x6666666666666664}, {"o", 0x7ffffffffffffffd},
	}

	// tmulti-g.h5 is empty; the others are 2048 bytes long.
	for _, name := range []string{filepath.Join(dir, "tmulti"), filepath.Join(dir, "tmulti-s.h5")} {
		t.Run(filepath.Base(name), func(t *testing.T) {
			var f *File
			var err error
			if filepath.Base(name) == "tmulti" {
				f, err = openViaDrivers(t, name)
			} else {
				f, err = Open(name) // plain Open: the driver information block does the work
				if err == nil {
					t.Cleanup(func() { f.Close() })
				}
			}
			require.NoError(t, err)

			md, ok := f.fd.(*multiDriver)
			require.True(t, ok, "driver is %T", f.fd)
			assert.Equal(t, filepath.Join(dir, "tmulti"), md.name)

			// h5dump -B --filedriver=multi tmulti
			sb := f.Superblock()
			assert.EqualValues(t, 0, sb.Version)
			assert.EqualValues(t, 8, sb.SizeOfOffsets)
			assert.EqualValues(t, 8, sb.SizeOfLengths)
			assert.EqualValues(t, 4, sb.SymLeafK)
			assert.EqualValues(t, 16, sb.SnodeBtreeK)
			assert.EqualValues(t, 32, sb.ChunkBtreeK)
			assert.EqualValues(t, 0, sb.BaseAddress)
			assert.Equal(t, "NCSAmult", sb.DriverName)
			assert.EqualValues(t, 0x7ffffffffffffffd, sb.RootGroupAddress)
			assert.EqualValues(t, 0x800, sb.EndOfFileAddress)

			// EOF is the end of the highest member that has data.
			assert.Equal(t, uint64(0x7ffffffffffffffd+2048), f.fd.EOF())

			for _, m := range membs {
				b := readFile(t, filepath.Join(dir, "tmulti-"+m.letter+".h5"))
				if len(b) == 0 {
					_, err := f.readAt(m.addr, 8)
					assert.Error(t, err, "member %s is empty", m.letter)
					continue
				}
				for _, r := range []struct{ off, n int }{{0, 8}, {16, 64}, {1000, 1048}} {
					got, err := f.readAt(m.addr+haddr(r.off), r.n)
					require.NoError(t, err, "member %s", m.letter)
					assert.Equal(t, b[r.off:r.off+r.n], got, "member %s offset %d", m.letter, r.off)
				}
				// Past the EOA of the member.
				_, err := f.readAt(m.addr+2040, 16)
				assert.Error(t, err)
			}
			// An address in the gap between members is not backed by a file.
			_, err = f.readAt(0x1999999999999999+0x900, 8)
			assert.Error(t, err)
		})
	}
}

func TestMultiSplit(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles")
	meta := readFile(t, filepath.Join(dir, "tsplit_file-m.h5"))
	raw := readFile(t, filepath.Join(dir, "tsplit_file-r.h5"))
	const rawAddr = 0x7fffffffffffffff // HADDR_MAX/2

	for _, name := range []string{filepath.Join(dir, "tsplit_file"), filepath.Join(dir, "tsplit_file-m.h5")} {
		f, err := openViaDrivers(t, name)
		require.NoError(t, err, name)
		md, ok := f.fd.(*multiDriver)
		require.True(t, ok, "driver is %T", f.fd)
		assert.Equal(t, filepath.Join(dir, "tsplit_file"), md.name)
		assert.Len(t, h5fdMultiUniqueMembers(&md.membMap), 2)

		// h5dump -B --filedriver=split tsplit_file
		sb := f.Superblock()
		assert.EqualValues(t, 0, sb.Version)
		assert.EqualValues(t, 4, sb.SymLeafK)
		assert.EqualValues(t, 16, sb.SnodeBtreeK)
		assert.EqualValues(t, 32, sb.ChunkBtreeK)
		assert.Equal(t, "NCSAmult", sb.DriverName)
		assert.EqualValues(t, 0xa8, sb.RootGroupAddress)

		assert.EqualValues(t, uint64(rawAddr)+uint64(len(raw)), f.fd.EOF())
		got, err := f.readAt(100, 200)
		require.NoError(t, err)
		assert.Equal(t, meta[100:300], got)
		got, err = f.readAt(rawAddr+300, 50)
		require.NoError(t, err)
		assert.Equal(t, raw[300:350], got)
	}
}

func TestMultiFileV16(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "test", "testfiles")
	super := readFile(t, filepath.Join(dir, "multi_file_v16-s.h5"))
	raw := readFile(t, filepath.Join(dir, "multi_file_v16-r.h5"))
	require.Len(t, raw, 65536)

	f, err := openViaDrivers(t, filepath.Join(dir, "multi_file_v16"))
	require.NoError(t, err)
	_, ok := f.fd.(*multiDriver)
	require.True(t, ok, "driver is %T", f.fd)
	sb := f.Superblock()
	assert.EqualValues(t, 0, sb.Version)
	assert.Equal(t, "NCSAmult", sb.DriverName)
	assert.EqualValues(t, 0x3e8, sb.RootGroupAddress)

	got, err := f.readAt(0x3e8, 32)
	require.NoError(t, err)
	assert.Equal(t, super[0x3e8:0x3e8+32], got)
	const rawAddr = 0x7fffffffffffffff
	got, err = f.readAt(rawAddr+4096, 4096)
	require.NoError(t, err)
	assert.Equal(t, raw[4096:8192], got)
	// A read that runs off the end of the raw data file.
	_, err = f.readAt(rawAddr+65536-8, 16)
	assert.Error(t, err)
}

func TestMultiOpenMissing(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles")
	// Neither a file nor the base name of a multi file.
	_, err := Open(filepath.Join(dir, "no-such-file"))
	assert.Error(t, err)

	// A relaxed multi file works when members other than the one holding
	// the superblock are missing; reading their range fails.
	tmp := t.TempDir()
	for _, l := range []string{"s", "o"} {
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "m-"+l+".h5"),
			readFile(t, filepath.Join(dir, "tmulti-"+l+".h5")), 0o600))
	}
	f, err := Open(filepath.Join(tmp, "m-s.h5"))
	require.NoError(t, err)
	defer f.Close()
	_, err = f.readAt(0x1999999999999999, 8)
	assert.Error(t, err)
	got, err := f.readAt(0x7ffffffffffffffd, 8)
	require.NoError(t, err)
	assert.Equal(t, readFile(t, filepath.Join(tmp, "m-o.h5"))[:8], got)

	// Without the superblock member there is nothing to open.
	require.NoError(t, os.Remove(filepath.Join(tmp, "m-s.h5")))
	_, err = openViaDrivers(t, filepath.Join(tmp, "m"))
	assert.Error(t, err)
}

func TestFlatDriversUnchanged(t *testing.T) {
	// Files without (or with a flat-file) driver information are left alone.
	f, err := Open(filepath.Join(repoRoot(t), "test", "testfiles", "tarrold.h5"))
	if err != nil {
		t.Skip("tarrold.h5 not readable:", err)
	}
	defer f.Close()
	_, ok := f.fd.(*sec2Driver)
	assert.True(t, ok)
	assert.NoError(t, f.sbLoad(&drvinfo{name: "NCSAsec2"}))
	assert.NoError(t, f.sbLoad(&drvinfo{name: "NCSAcore"}))
	assert.NoError(t, f.sbLoad(&drvinfo{name: "NCSAlog"}))
	assert.NoError(t, f.sbLoad(&drvinfo{name: "NCSAstdi"}))
	_, ok = f.fd.(*sec2Driver)
	assert.True(t, ok)
}
