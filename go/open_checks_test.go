package hdf5

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

// h5clear_mdc_image.h5 was closed with a metadata cache image: the root
// group's object header is only in the image block (h5c_image.go). The
// expected values are those of h5dump: DSET[i][j] = i*j.
func TestCacheImage(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "tools", "test", "misc", "testfiles", "h5clear_mdc_image.h5"))
	require.NoError(t, err)
	defer f.Close()
	assert.NotEmpty(t, f.cacheImage)

	var names []string
	for l, err := range f.Root().Links() {
		require.NoError(t, err)
		names = append(names, l.Name)
	}
	assert.Equal(t, []string{"DSET"}, names)

	ds, err := f.OpenDataset("/DSET")
	require.NoError(t, err)
	assert.Equal(t, []uint64{50, 100}, ds.Dataspace().Dims)
	vals, err := Read[int32](ds)
	require.NoError(t, err)
	require.Len(t, vals, 5000)
	for i := range 50 {
		for j := range 100 {
			if vals[i*100+j] != int32(i*j) {
				t.Fatalf("DSET[%d][%d] = %d, want %d", i, j, vals[i*100+j], i*j)
			}
		}
	}
	part, err := ds.ReadHyperslab(h5.Hyperslab{Start: []uint64{49, 96}, Count: []uint64{1, 4}})
	require.NoError(t, err)
	assert.Equal(t, []any{int32(4704), int32(4753), int32(4802), int32(4851)}, part)
}

// The cache image block of cve_2020_10812.h5 lies beyond the end of the
// allocated address space: libhdf5 fails reading it ("addr overflow").
func TestCacheImageBeyondEOA(t *testing.T) {
	_, err := Open(filepath.Join(repoRoot(t), "test", "testfiles", "cve_2020_10812.h5"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "addr overflow")
}

// libhdf5 refuses files whose superblock records a larger end of file than
// the file has, and version 3 superblocks marked as open for writing.
func TestSuperblockOpenChecks(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools", "test", "misc", "testfiles")
	_, err := Open(filepath.Join(dir, "h5clear_fsm_persist_greater.h5"))
	require.Error(t, err)
	assert.ErrorIs(t, err, &Error{Minor: H5E_TRUNCATED})
	_, err = Open(filepath.Join(dir, "h5clear_fsm_persist_user_greater.h5")) // with a user block
	assert.ErrorIs(t, err, &Error{Minor: H5E_TRUNCATED})
	for _, name := range []string{"h5clear_sec2_v3.h5", "h5clear_status_noclose.h5", "latest_h5clear_log_v3.h5"} {
		_, err := Open(filepath.Join(dir, name))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "file is already open for write", name)
	}
	// Status flags of version 0 superblocks are not checked.
	f, err := Open(filepath.Join(dir, "h5clear_sec2_v0.h5"))
	require.NoError(t, err)
	f.Close()
}

// Reads beyond the stored end-of-address marker fail, as in H5FD_read: the
// root group's symbol table of h5clear_fsm_persist_less.h5 lies beyond it.
func TestReadBeyondEOA(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "tools", "test", "misc", "testfiles", "h5clear_fsm_persist_less.h5"))
	require.NoError(t, err)
	defer f.Close()
	_, err = f.Root().NumLinks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "addr overflow")
}

// A dataset whose messages libhdf5 cannot decode fails to open (H5Dopen),
// not on first use.
func TestDatasetOpenDecodesMessages(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "test", "testfiles", "th5s.h5"))
	require.NoError(t, err)
	defer f.Close()
	_, err = f.OpenDataset("/dset")
	require.Error(t, err)
	_, err = f.Open("/dset")
	require.Error(t, err)
}

// Relative external data file names are looked up in the current directory
// only (no prefix), as libhdf5 does.
func TestExternalFileCurrentDirectory(t *testing.T) {
	name, err := filepath.Abs(filepath.Join("testdata", "dset.h5"))
	require.NoError(t, err)
	t.Run("elsewhere", func(t *testing.T) {
		t.Chdir(t.TempDir())
		f, err := Open(name)
		require.NoError(t, err)
		defer f.Close()
		ds, err := f.OpenDataset("/external")
		require.NoError(t, err)
		_, err = ds.ReadValues()
		require.Error(t, err)
	})
	t.Run("prefix", func(t *testing.T) {
		t.Chdir(t.TempDir())
		f, err := Open(name, WithExternalFilePrefix("${ORIGIN}"))
		require.NoError(t, err)
		defer f.Close()
		ds, err := f.OpenDataset("/external")
		require.NoError(t, err)
		vals, err := Read[int32](ds)
		require.NoError(t, err)
		assert.Equal(t, []int32{100, 101, 102, 103, 104, 105, 106, 107, 108, 109}, vals)
	})
	t.Run("here", func(t *testing.T) {
		t.Chdir(filepath.Dir(name))
		f, err := Open(name)
		require.NoError(t, err)
		defer f.Close()
		ds, err := f.OpenDataset("/external")
		require.NoError(t, err)
		_, err = ds.ReadValues()
		require.NoError(t, err)
	})
}
