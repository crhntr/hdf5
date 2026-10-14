package hdf5

// Translated from src/H5Zdeflate.c (decompression direction only).

import (
	"bytes"
	"compress/zlib"
	"io"
)

// h5zDeflateMaxOutput bounds the size of a decompressed chunk. HDF5 chunks
// are limited to 4 GiB - 1 bytes.
const h5zDeflateMaxOutput = 1<<32 - 1

// h5zFilterDeflateFunc is H5Z__filter_deflate with H5Z_FLAG_REVERSE: inflate
// the zlib stream in buf. As in the C code no size hint for the output is
// available, so the output grows as needed.
func h5zFilterDeflateFunc(flags uint, cdValues []uint32, buf []byte) ([]byte, error) {
	/* Check arguments */
	if len(cdValues) != 1 || cdValues[0] > 9 {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid deflate aggression level")
	}
	if flags&h5zFlagReverse == 0 {
		return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED, "deflate is only supported in the read direction")
	}

	/* Input; uncompress */
	return h5zInflate(buf, h5zDeflateMaxOutput)
}

// h5zInflate is the decompression loop of H5Z__filter_deflate: inflate the
// zlib stream in buf, failing if it expands to more than maxOutput bytes
// (where the C code fails to allocate the output buffer).
func h5zInflate(buf []byte, maxOutput uint64) ([]byte, error) {
	/* Initialize the uncompression routines */
	zr, err := zlib.NewReader(bytes.NewReader(buf))
	if err != nil {
		return nil, wrapf(err, H5E_PLINE, H5E_CANTINIT, "inflateInit() failed")
	}
	defer zr.Close()

	/* Loop to uncompress the buffer (until Z_STREAM_END); the output buffer
	 * grows whenever it runs full. */
	out, err := io.ReadAll(io.LimitReader(zr, int64(maxOutput+1)))
	if err != nil {
		return nil, wrapf(err, H5E_PLINE, H5E_CANTINIT, "inflate() failed")
	}
	if uint64(len(out)) > maxOutput {
		return nil, errorf(H5E_RESOURCE, H5E_CANTALLOC, "memory allocation failed for deflate uncompression")
	}
	return out, nil
}
