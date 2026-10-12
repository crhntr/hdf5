package hdf5

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shuffleEncode is the write direction of H5Z__filter_shuffle (only used to
// build test input).
func shuffleEncode(buf []byte, bytesoftype int) []byte {
	numofelements := len(buf) / bytesoftype
	if bytesoftype <= 1 || numofelements <= 1 {
		return append([]byte(nil), buf...)
	}
	out := make([]byte, 0, len(buf))
	for i := 0; i < bytesoftype; i++ {
		for j := 0; j < numofelements; j++ {
			out = append(out, buf[j*bytesoftype+i])
		}
	}
	return append(out, buf[numofelements*bytesoftype:]...)
}

func zlibEncode(t testing.TB, data []byte, level int) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := zlib.NewWriterLevel(&b, level)
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return b.Bytes()
}

func fletcherEncode(data []byte) []byte {
	out := append([]byte(nil), data...)
	return binary.LittleEndian.AppendUint32(out, checksumFletcher32(data))
}

func TestFilterDeflate(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 7, 100, 4096, 100000} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(rng.Intn(5)) // compressible
		}
		for _, level := range []int{1, 6, 9} {
			out, err := h5zFilterDeflateFunc(h5zFlagReverse, []uint32{uint32(level)}, zlibEncode(t, data, level))
			require.NoError(t, err)
			assert.Equal(t, data, out)
		}
	}

	good := zlibEncode(t, []byte("hello hello hello"), 6)

	t.Run("trailing garbage is ignored", func(t *testing.T) {
		out, err := h5zFilterDeflateFunc(h5zFlagReverse, []uint32{6}, append(append([]byte(nil), good...), 1, 2, 3))
		require.NoError(t, err)
		assert.Equal(t, "hello hello hello", string(out))
	})
	t.Run("bad level", func(t *testing.T) {
		_, err := h5zFilterDeflateFunc(h5zFlagReverse, []uint32{10}, good)
		assert.ErrorIs(t, err, &Error{Major: H5E_ARGS, Minor: H5E_BADVALUE})
		_, err = h5zFilterDeflateFunc(h5zFlagReverse, nil, good)
		assert.ErrorIs(t, err, &Error{Major: H5E_ARGS, Minor: H5E_BADVALUE})
	})
	t.Run("truncated", func(t *testing.T) {
		_, err := h5zFilterDeflateFunc(h5zFlagReverse, []uint32{6}, good[:len(good)-3])
		assert.ErrorIs(t, err, &Error{Major: H5E_PLINE, Minor: H5E_CANTINIT})
	})
	t.Run("corrupt adler", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[len(bad)-1] ^= 0xff
		_, err := h5zFilterDeflateFunc(h5zFlagReverse, []uint32{6}, bad)
		assert.Error(t, err)
	})
	t.Run("not zlib", func(t *testing.T) {
		_, err := h5zFilterDeflateFunc(h5zFlagReverse, []uint32{6}, []byte("this is not a zlib stream"))
		assert.Error(t, err)
	})
}

func TestFilterShuffle(t *testing.T) {
	rev := func(size uint32, in []byte) ([]byte, error) {
		return h5zFilterShuffleFunc(h5zFlagReverse, []uint32{size}, in)
	}

	t.Run("4-byte elements, no leftover", func(t *testing.T) {
		// elements 0x00010203 style: a0a1a2a3 b0b1b2b3 c0c1c2c3
		shuffled := []byte{
			0xa0, 0xb0, 0xc0,
			0xa1, 0xb1, 0xc1,
			0xa2, 0xb2, 0xc2,
			0xa3, 0xb3, 0xc3,
		}
		out, err := rev(4, shuffled)
		require.NoError(t, err)
		assert.Equal(t, []byte{
			0xa0, 0xa1, 0xa2, 0xa3,
			0xb0, 0xb1, 0xb2, 0xb3,
			0xc0, 0xc1, 0xc2, 0xc3,
		}, out)
	})

	t.Run("4-byte elements, 2 leftover bytes", func(t *testing.T) {
		shuffled := []byte{
			0xa0, 0xb0, 0xc0,
			0xa1, 0xb1, 0xc1,
			0xa2, 0xb2, 0xc2,
			0xa3, 0xb3, 0xc3,
			0x77, 0x88, // leftover, stored unshuffled at the end
		}
		out, err := rev(4, shuffled)
		require.NoError(t, err)
		assert.Equal(t, []byte{
			0xa0, 0xa1, 0xa2, 0xa3,
			0xb0, 0xb1, 0xb2, 0xb3,
			0xc0, 0xc1, 0xc2, 0xc3,
			0x77, 0x88,
		}, out)
	})

	t.Run("3-byte elements, 1 leftover byte", func(t *testing.T) {
		shuffled := []byte{1, 4, 2, 5, 3, 6, 9}
		out, err := rev(3, shuffled)
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3, 4, 5, 6, 9}, out)
	})

	t.Run("fewer than two elements is a no-op", func(t *testing.T) {
		in := []byte{1, 2, 3, 4, 5, 6, 7}
		out, err := rev(4, in)
		require.NoError(t, err)
		assert.Equal(t, in, out)
		out, err = rev(8, in)
		require.NoError(t, err)
		assert.Equal(t, in, out)
	})

	t.Run("1-byte elements is a no-op", func(t *testing.T) {
		in := []byte{9, 8, 7, 6}
		out, err := rev(1, in)
		require.NoError(t, err)
		assert.Equal(t, in, out)
	})

	t.Run("bad parameters", func(t *testing.T) {
		_, err := h5zFilterShuffleFunc(h5zFlagReverse, nil, []byte{1, 2})
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADVALUE})
		_, err = rev(0, []byte{1, 2})
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADVALUE})
		_, err = h5zFilterShuffleFunc(h5zFlagReverse, []uint32{4, 4}, []byte{1, 2})
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADVALUE})
	})

	t.Run("round trip", func(t *testing.T) {
		rng := rand.New(rand.NewSource(2))
		for size := 1; size <= 17; size++ {
			for n := 1; n < 70; n += 3 {
				data := make([]byte, n)
				rng.Read(data)
				out, err := rev(uint32(size), shuffleEncode(data, size))
				require.NoError(t, err)
				assert.Equal(t, data, out, "size %d len %d", size, n)
			}
		}
	})
}

func TestFilterFletcher32(t *testing.T) {
	// vectors from test/tchecksum.c
	vectors := []struct {
		data []byte
		sum  uint32
	}{
		{[]byte{23}, 0x17001700},
		{[]byte{23, 187}, 0x17bb17bb},
		{[]byte{23, 187, 98}, 0x917679bb},
		{[]byte{23, 187, 98, 217}, 0x924f7a94},
	}
	large := make([]byte, 3093)
	for u := range large {
		large[u] = byte(u * 3)
	}
	vectors = append(vectors, struct {
		data []byte
		sum  uint32
	}{large, 0x85b4e2a})

	for _, v := range vectors {
		assert.Equal(t, v.sum, checksumFletcher32(v.data))

		stored := binary.LittleEndian.AppendUint32(append([]byte(nil), v.data...), v.sum)
		out, err := h5zFilterFletcher32Func(h5zFlagReverse, nil, stored)
		require.NoError(t, err)
		assert.Equal(t, v.data, out)

		// the byte-swapped checksum of HDF5 < 1.6.3 is accepted too
		sw := (v.sum&0x00ff00ff)<<8 | (v.sum>>8)&0x00ff00ff
		stored = binary.LittleEndian.AppendUint32(append([]byte(nil), v.data...), sw)
		out, err = h5zFilterFletcher32Func(h5zFlagReverse, nil, stored)
		require.NoError(t, err)
		assert.Equal(t, v.data, out)

		// a wrong checksum is detected ...
		stored = binary.LittleEndian.AppendUint32(append([]byte(nil), v.data...), v.sum^0x100)
		_, err = h5zFilterFletcher32Func(h5zFlagReverse, nil, stored)
		assert.ErrorIs(t, err, &Error{Major: H5E_STORAGE, Minor: H5E_READERROR})

		// ... unless H5Z_FLAG_SKIP_EDC is set
		out, err = h5zFilterFletcher32Func(h5zFlagReverse|h5zFlagSkipEdc, nil, stored)
		require.NoError(t, err)
		assert.Equal(t, v.data, out)
	}

	// the reference swapped value for 0x924f7a94 is 0x4f92947a: the C code
	// swaps the two bytes inside each 16-bit half.
	data := []byte{23, 187, 98, 217}
	stored := binary.LittleEndian.AppendUint32(append([]byte(nil), data...), 0x4f92947a)
	_, err := h5zFilterFletcher32Func(h5zFlagReverse, nil, stored)
	require.NoError(t, err)

	// buffers shorter than the checksum
	_, err = h5zFilterFletcher32Func(h5zFlagReverse, nil, []byte{1, 2, 3})
	assert.Error(t, err)
}

func TestFilterSzipUnsupported(t *testing.T) {
	buf := []byte{0x10, 0, 0, 0, 1, 2, 3, 4}
	_, err := h5zFilterSzipFunc(h5zFlagReverse, []uint32{1 | 4, 16, 8, 32}, buf)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupported)
	assert.Contains(t, err.Error(), "szip filter not supported")

	_, err = h5zFilterSzipFunc(h5zFlagReverse, []uint32{1, 2, 3}, buf)
	assert.ErrorIs(t, err, &Error{Minor: H5E_BADVALUE})

	_, err = h5zFilterSzipFunc(h5zFlagReverse, []uint32{1, 2, 3, 4}, []byte{1})
	assert.Error(t, err)

	// through the pipeline
	pline := &h5oPline{nused: 1, filter: []h5zFilterInfo{{id: h5zFilterSzip, cdValues: []uint32{5, 16, 8, 32}}}}
	_, err = h5zPipeline(pline, 0, buf)
	assert.ErrorIs(t, err, ErrUnsupported)
}

func TestPipeline(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	raw := make([]byte, 4*250+3) // 250 4-byte elements and 3 leftover bytes
	for i := range raw {
		raw[i] = byte(rng.Intn(4))
	}

	pline := &h5oPline{
		version: 2,
		nused:   3,
		filter: []h5zFilterInfo{
			{id: h5zFilterShuffle, cdValues: []uint32{4}},
			{id: h5zFilterDeflate, cdValues: []uint32{6}},
			{id: h5zFilterFletcher32},
		},
	}

	// write order: shuffle, deflate, fletcher32
	stored := fletcherEncode(zlibEncode(t, shuffleEncode(raw, 4), 6))

	t.Run("all filters", func(t *testing.T) {
		out, err := h5zPipeline(pline, 0, append([]byte(nil), stored...))
		require.NoError(t, err)
		assert.Equal(t, raw, out)
	})

	t.Run("corrupt checksum", func(t *testing.T) {
		bad := append([]byte(nil), stored...)
		bad[3] ^= 1
		_, err := h5zPipeline(pline, 0, bad)
		assert.ErrorIs(t, err, &Error{Major: H5E_PLINE, Minor: H5E_READERROR})
		assert.ErrorIs(t, err, &Error{Major: H5E_STORAGE, Minor: H5E_READERROR})

		// with EDC disabled the checksum is not examined and the (still
		// valid) deflate stream decodes
		bad = append([]byte(nil), stored...)
		bad[len(bad)-1] ^= 1
		out, err := h5zPipelineEDC(pline, 0, h5zDisableEDC, bad)
		require.NoError(t, err)
		assert.Equal(t, raw, out)
	})

	t.Run("filter mask skips filters", func(t *testing.T) {
		// the writer skipped fletcher32 (bit 2) for this chunk
		noSum := zlibEncode(t, shuffleEncode(raw, 4), 6)
		out, err := h5zPipeline(pline, 1<<2, append([]byte(nil), noSum...))
		require.NoError(t, err)
		assert.Equal(t, raw, out)

		// the writer skipped shuffle (bit 0) and deflate (bit 1)
		out, err = h5zPipeline(pline, 1<<0|1<<1, fletcherEncode(raw))
		require.NoError(t, err)
		assert.Equal(t, raw, out)

		// everything skipped: data is untouched
		out, err = h5zPipeline(pline, 7, append([]byte(nil), raw...))
		require.NoError(t, err)
		assert.Equal(t, raw, out)
	})

	t.Run("reverse order is observable", func(t *testing.T) {
		// two shuffles with different element sizes do not commute
		p := &h5oPline{nused: 2, filter: []h5zFilterInfo{
			{id: h5zFilterShuffle, cdValues: []uint32{2}},
			{id: h5zFilterShuffle, cdValues: []uint32{4}},
		}}
		data := make([]byte, 32)
		rng.Read(data)
		w := shuffleEncode(shuffleEncode(data, 2), 4)
		out, err := h5zPipeline(p, 0, w)
		require.NoError(t, err)
		assert.Equal(t, data, out)
	})

	t.Run("unknown filter", func(t *testing.T) {
		// even an optional unknown filter is an error on the read side
		for _, flags := range []uint16{h5zFlagMandatory, h5zFlagOptional} {
			p := &h5oPline{nused: 1, filter: []h5zFilterInfo{{id: 32000, flags: flags, name: "mine"}}}
			_, err := h5zPipeline(p, 0, []byte{1, 2, 3})
			require.Error(t, err)
			assert.ErrorIs(t, err, &Error{Major: H5E_PLINE, Minor: H5E_READERROR})
			assert.Contains(t, err.Error(), "'mine'")
		}
		p := &h5oPline{nused: 1, filter: []h5zFilterInfo{{id: 32000}}}
		_, err := h5zPipeline(p, 0, []byte{1, 2, 3})
		assert.ErrorContains(t, err, "name unavailable")

		// but a skipped unknown filter (mask bit set) is fine
		out, err := h5zPipeline(p, 1, []byte{1, 2, 3})
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, out)
	})

	t.Run("no pipeline", func(t *testing.T) {
		out, err := h5zPipeline(nil, 0, []byte{1})
		require.NoError(t, err)
		assert.Equal(t, []byte{1}, out)
	})
}
