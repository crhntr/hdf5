package hdf5

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Locating the files a file refers to: virtual dataset sources
// (HDF5_VDS_PREFIX, WithVirtualPrefix), external data files
// (HDF5_EXTFILE_PREFIX, WithExternalFilePrefix) and external link targets
// (HDF5_EXT_PREFIX), as H5F_prefix_open_file and H5D__build_file_prefix
// do: the environment variable is a ':' separated list tried first, then
// the access property, in which "${ORIGIN}" is the directory of the file.
//
// testdata/dset.h5 has /vds_xfile, a virtual dataset whose source is
// "dset_src.h5" /src (60..63), and /external, stored in the external file
// "dset_ext.raw" (100..109), see testdata/gen_dset.c.

// prefixLayout copies dset.h5 to dir/a, dset_src.h5 to dir/b and
// dset_ext.raw to dir/c, and returns the path of the copy of dset.h5.
func prefixLayout(t *testing.T) (dir, name string) {
	t.Helper()
	dir = t.TempDir()
	for _, c := range [][2]string{{"dset.h5", "a"}, {"dset_src.h5", "b"}, {"dset_ext.raw", "c"}} {
		b, err := os.ReadFile(filepath.Join("testdata", c[0]))
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, c[1]), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, c[1], c[0]), b, 0o644))
	}
	return dir, filepath.Join(dir, "a", "dset.h5")
}

func readI32(t *testing.T, f *File, path string) ([]int32, error) {
	t.Helper()
	d, err := f.OpenDataset(path)
	require.NoError(t, err)
	return Read[int32](d)
}

func TestVirtualSourcePrefixes(t *testing.T) {
	dir, name := prefixLayout(t)
	src := []int32{60, 61, 62, 63}
	missing := []int32{0, 0, 0, 0} /* the fill value: the source is not found */

	for _, tc := range []struct {
		name string
		env  string
		opts []Option
		want []int32
	}{
		{"not found", "", nil, missing},
		{"env", filepath.Join(dir, "b"), nil, src},
		/* the list is tried in order; empty entries are skipped */
		{"env list", filepath.Join(dir, "nothing") + "::" + filepath.Join(dir, "b"), nil, src},
		{"env trailing separator", filepath.Join(dir, "b") + "/", nil, src},
		{"env not found", filepath.Join(dir, "c"), nil, missing},
		{"property", "", []Option{WithVirtualPrefix(filepath.Join(dir, "b"))}, src},
		{"property origin", "", []Option{WithVirtualPrefix("${ORIGIN}/../b")}, src},
		{"property dot", "", []Option{WithVirtualPrefix(".")}, missing},
		/* H5D__build_file_prefix: when the environment variable is set
		 * the property is not used at all */
		{"env hides property", filepath.Join(dir, "c"), []Option{WithVirtualPrefix(filepath.Join(dir, "b"))}, missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HDF5_VDS_PREFIX", tc.env)
			f, err := Open(name, tc.opts...)
			require.NoError(t, err)
			defer f.Close()
			got, err := readI32(t, f, "/vds_xfile")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExternalFilePrefixes(t *testing.T) {
	dir, name := prefixLayout(t)
	want := make([]int32, 10)
	for i := range want {
		want[i] = int32(100 + i)
	}

	/* A second copy of the raw file with different values: whichever
	 * prefix wins is visible in the data. */
	raw, err := os.ReadFile(filepath.Join("testdata", "dset_ext.raw"))
	require.NoError(t, err)
	other := append([]byte(nil), raw...)
	for _, off := range []int{16, 20, 24} {
		binary.LittleEndian.PutUint32(other[off:], 7)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "d", "dset_ext.raw"), other, 0o644))
	wantOther := append([]int32{7, 7, 7}, want[3:]...)

	for _, tc := range []struct {
		name string
		env  string
		opts []Option
		want []int32
	}{
		{"env", filepath.Join(dir, "c"), nil, want},
		{"env origin", "${ORIGIN}/../c", nil, want},
		{"property", "", []Option{WithExternalFilePrefix(filepath.Join(dir, "d"))}, wantOther},
		{"property origin", "", []Option{WithExternalFilePrefix("${ORIGIN}/../d")}, wantOther},
		/* H5D__build_file_prefix: the environment variable wins over the
		 * property */
		{"env wins", filepath.Join(dir, "c"), []Option{WithExternalFilePrefix(filepath.Join(dir, "d"))}, want},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HDF5_EXTFILE_PREFIX", tc.env)
			f, err := Open(name, tc.opts...)
			require.NoError(t, err)
			defer f.Close()
			got, err := readI32(t, f, "/external")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	/* Without a prefix the name is relative to the current directory */
	t.Setenv("HDF5_EXTFILE_PREFIX", "")
	f, err := Open(name, WithExternalFilePrefix("."))
	require.NoError(t, err)
	defer f.Close()
	_, err = readI32(t, f, "/external")
	assert.Error(t, err)
}

// H5F_prefix_open_file with an absolute name that does not exist: the
// last component of the name is looked up with the prefixes.
func TestPrefixOpenFileAbsolute(t *testing.T) {
	dir, name := prefixLayout(t)
	f, err := Open(name)
	require.NoError(t, err)
	defer f.Close()

	abs := filepath.Join(dir, "b", "dset_src.h5")
	gone := filepath.Join(dir, "gone", "dset_src.h5")

	t.Setenv("HDF5_EXT_PREFIX", "")
	src, err := f.prefixOpenFile(h5fPrefixElink, "", abs)
	require.NoError(t, err)
	assert.Equal(t, abs, src.name)

	_, err = f.prefixOpenFile(h5fPrefixElink, "", gone)
	assert.Error(t, err)

	t.Setenv("HDF5_EXT_PREFIX", filepath.Join(dir, "nothing")+":"+filepath.Join(dir, "b"))
	src, err = f.prefixOpenFile(h5fPrefixElink, "", gone)
	require.NoError(t, err)
	assert.Equal(t, abs, src.name)

	t.Setenv("HDF5_EXT_PREFIX", "")
	src, err = f.prefixOpenFile(h5fPrefixElink, filepath.Join(dir, "b"), gone)
	require.NoError(t, err)
	assert.Equal(t, abs, src.name)

	/* The same file is returned from the external file cache */
	again, err := f.prefixOpenFile(h5fPrefixVDS, filepath.Join(dir, "b"), "dset_src.h5")
	require.NoError(t, err)
	assert.Same(t, src, again)

	_, err = f.prefixOpenFile(h5fPrefixEfile, "", "dset_src.h5")
	assert.Error(t, err, "external data files are not opened as HDF5 files")

	assert.Equal(t, "p/x.h5", h5fBuildName("p/", "x.h5"))
	assert.Equal(t, "x.h5", h5fBuildName("", "x.h5"))
	assert.Equal(t, []string{"a", "", "b"}, h5fGetenvPrefixNames("a::b"))
}
