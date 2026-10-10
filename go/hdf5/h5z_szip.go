package hdf5

// Translated from src/H5Zszip.c (read direction; the libsz decoder itself
// cannot be translated).

import "encoding/binary"

// H5Z_SZIP_* parameter layout (H5Zpublic.h).
const (
	h5zSzipTotalNParms = 4
	h5zSzipParmMask    = 0
	h5zSzipParmPPB     = 1
	h5zSzipParmBPP     = 2
	h5zSzipParmPPS     = 3
)

// h5zFilterSzipFunc is H5Z__filter_szip with H5Z_FLAG_REVERSE. The szip
// (CCSDS rice/entropy coding) decoder lives in libsz and is not translated:
// after checking the parameters and decoding the 4-byte header that holds the
// size of the uncompressed block, the function fails with H5E_UNSUPPORTED.
func h5zFilterSzipFunc(flags uint, cdValues []uint32, buf []byte) ([]byte, error) {
	/* Check arguments */
	if len(cdValues) != h5zSzipTotalNParms {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid number of filter parameters")
	}
	if flags&h5zFlagReverse == 0 {
		return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED, "szip filter not supported")
	}

	/* Get the size of the uncompressed buffer */
	if len(buf) < 4 {
		return nil, errorf(H5E_PLINE, H5E_BADSIZE, "szip chunk too small for its header")
	}
	storedNalloc := binary.LittleEndian.Uint32(buf)

	return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED,
		"szip filter not supported (options_mask=0x%x bits_per_pixel=%d pixels_per_block=%d pixels_per_scanline=%d; %d compressed bytes expanding to %d)",
		cdValues[h5zSzipParmMask], cdValues[h5zSzipParmBPP], cdValues[h5zSzipParmPPB], cdValues[h5zSzipParmPPS],
		len(buf)-4, storedNalloc)
}
