package hdf5

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChecksumLookup3(t *testing.T) {
	// Known values from Bob Jenkins' lookup3 ("hashlittle") test vectors.
	assert.Equal(t, uint32(0xdeadbeef), checksumLookup3([]byte{}, 0))
	assert.Equal(t, uint32(0x17770551), checksumLookup3([]byte("Four score and seven years ago"), 0))
	assert.Equal(t, uint32(0xcd628161), checksumLookup3([]byte("Four score and seven years ago"), 1))
}

func TestChecksumFletcher32(t *testing.T) {
	// Vectors from HDF5's test/tchecksum.c (buf of 2048 bytes 0..255 repeating)
	// are exercised through the filter tests; check the trivial cases here.
	assert.Equal(t, uint32(0x00010001), checksumFletcher32([]byte{0, 1}))
}

func TestSuperblockCorpus(t *testing.T) {
	files := corpusFiles(t)
	require.NotEmpty(t, files)
	var ok, notHDF5, other int
	for _, path := range files {
		f, err := Open(path, WithExternalLinks(false))
		if err != nil {
			if errors.Is(err, ErrNotHDF5) {
				notHDF5++
				continue
			}
			other++
			t.Logf("%s: %v", filepath.Base(path), err)
			continue
		}
		sb := f.Superblock()
		assert.Contains(t, []uint8{0, 1, 2, 3}, sb.Version, path)
		assert.NotEqual(t, ^uint64(0), sb.RootGroupAddress, path)
		f.Close()
		ok++
	}
	t.Logf("corpus: %d files, %d superblocks decoded, %d not HDF5, %d errors", len(files), ok, notHDF5, other)
	assert.Greater(t, ok, 400)
}
