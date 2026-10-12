package hdf5

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScaleoffsetDecompressBigEndianMemoryOrder(t *testing.T) {
	// The BE branch of H5Z__scaleoffset_decompress_one_atomic (used by the C
	// library on big-endian hosts) must produce the byte-reversed image of
	// the little-endian result.
	rng := rand.New(rand.NewSource(24))
	for _, size := range []int{1, 2, 4, 8} {
		for minbits := uint32(1); minbits < uint32(size*8); minbits++ {
			n := 1 + rng.Intn(50)
			packed := make([]byte, (n*int(minbits)+7)/8+1)
			rng.Read(packed)
			le := make([]byte, n*size)
			be := make([]byte, n*size)
			p := &h5zScaleoffsetParmsAtomic{size: uint32(size), minbits: minbits, memOrder: h5zScaleoffsetOrderLE}
			require.NoError(t, h5zScaleoffsetDecompress(le, uint32(n), packed, p))
			p.memOrder = h5zScaleoffsetOrderBE
			require.NoError(t, h5zScaleoffsetDecompress(be, uint32(n), packed, p))
			h5zScaleoffsetConvert(be, uint32(n), uint32(size))
			require.Equal(t, le, be, "size %d minbits %d", size, minbits)
		}
	}
}
