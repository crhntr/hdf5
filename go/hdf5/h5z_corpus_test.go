package hdf5

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real chunks from tools/test/h5repack/testfiles. All five files hold a
// 40x20 int32 dataset with element (r,c) == r*20+c, chunked 20x10, so there
// are four chunks. The chunk file addresses, sizes and filter masks come from
// the version 1 chunk B-tree (checked with `h5debug`/`h5dump -p`), and the
// filter parameters from the filter pipeline message (`h5debug <file>
// <ohdr addr>`).

type corpusChunk struct {
	addr, size int
	mask       uint32
	row, col   int // first element of the chunk
}

func (c corpusChunk) expected() []byte {
	out := make([]byte, 0, 200*4)
	for r := 0; r < 20; r++ {
		for cc := 0; cc < 10; cc++ {
			v := uint32((c.row+r)*20 + c.col + cc)
			out = append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		}
	}
	return out
}

func corpusFile(t *testing.T, name string) []byte {
	t.Helper()
	file, err := os.ReadFile(filepath.Join(repoRoot(t), "tools", "test", "h5repack", "testfiles", name))
	require.NoError(t, err)
	return file
}

func readCorpusChunks(t *testing.T, name string, chunks []corpusChunk, pline *h5oPline) {
	t.Helper()
	file := corpusFile(t, name)
	for _, c := range chunks {
		raw := append([]byte(nil), file[c.addr:c.addr+c.size]...)
		out, err := h5zPipeline(pline, c.mask, raw)
		require.NoError(t, err, "%s chunk at %d", name, c.addr)
		assert.Equal(t, c.expected(), out, "%s chunk at %d", name, c.addr)
	}
}

// chunks4 lists the four chunks of a dataset whose chunk data starts at file
// address first; sizes are the stored chunk sizes.
func chunks4(first int, sizes ...int) []corpusChunk {
	offs := [][2]int{{0, 0}, {0, 10}, {20, 0}, {20, 10}}
	var out []corpusChunk
	addr := first
	for i, o := range offs {
		sz := sizes[0]
		if len(sizes) > 1 {
			sz = sizes[i]
		}
		out = append(out, corpusChunk{addr: addr, size: sz, row: o[0], col: o[1]})
		addr += sz
	}
	return out
}

func TestCorpusChunkNbit(t *testing.T) {
	// dset_nbit: int32 with 31 bits of precision
	pline := &h5oPline{version: 1, nused: 1, filter: []h5zFilterInfo{{
		id: h5zFilterNbit, flags: h5zFlagOptional, name: "nbit",
		cdValues: []uint32{8, 0, 200, 1, 4, 0, 31, 0},
	}}}
	readCorpusChunks(t, "h5repack_nbit.h5", chunks4(4016, 776), pline)
}

func TestCorpusChunkScaleoffset(t *testing.T) {
	// dset_scaleoffset: int32 LE, scale type H5Z_SO_INT, 31 bits requested,
	// fill value defined (0): the fill value elements are stored as the
	// all-ones marker.
	cd := make([]uint32, 20)
	copy(cd, []uint32{2, 31, 200, 0, 4, 1, 0, 1})
	pline := &h5oPline{version: 1, nused: 1, filter: []h5zFilterInfo{{
		id: h5zFilterScaleoffset, flags: h5zFlagOptional, name: "scaleoffset", cdValues: cd,
	}}}
	readCorpusChunks(t, "h5repack_soffset.h5", chunks4(4016, 797), pline)

	// the first chunk header: minbits 31, sizeof(minval) 8, minval 1 (the
	// minimum of the non-fill elements)
	file := corpusFile(t, "h5repack_soffset.h5")
	assert.Equal(t, []byte{31, 0, 0, 0, 8, 1, 0, 0, 0, 0, 0, 0, 0}, file[4016:4016+13])
}

func TestCorpusChunkDeflate(t *testing.T) {
	pline := &h5oPline{version: 1, nused: 1, filter: []h5zFilterInfo{{
		id: h5zFilterDeflate, flags: h5zFlagOptional, name: "deflate", cdValues: []uint32{9},
	}}}
	readCorpusChunks(t, "h5repack_deflate.h5", chunks4(4016, 327, 317, 323, 323), pline)
}

func TestCorpusChunkShuffle(t *testing.T) {
	pline := &h5oPline{version: 1, nused: 1, filter: []h5zFilterInfo{{
		id: h5zFilterShuffle, flags: h5zFlagOptional, name: "shuffle", cdValues: []uint32{4},
	}}}
	readCorpusChunks(t, "h5repack_shuffle.h5", chunks4(4016, 800), pline)
}

func TestCorpusChunkFletcher32(t *testing.T) {
	pline := &h5oPline{version: 1, nused: 1, filter: []h5zFilterInfo{{
		id: h5zFilterFletcher32, flags: 0, name: "fletcher32",
	}}}
	readCorpusChunks(t, "h5repack_fletcher.h5", chunks4(4016, 804), pline)

	// flipping a data bit is detected
	file := corpusFile(t, "h5repack_fletcher.h5")
	raw := append([]byte(nil), file[4016:4016+804]...)
	raw[10] ^= 0x04
	_, err := h5zPipeline(pline, 0, raw)
	assert.ErrorIs(t, err, &Error{Major: H5E_STORAGE, Minor: H5E_READERROR})
}
