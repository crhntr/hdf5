package hdf5

// Translated from src/H5Zshuffle.c (unshuffle direction only).

// H5Z_SHUFFLE_* parameter layout.
const (
	h5zShuffleParmSize    = 0 /* "Local" parameter for shuffling size */
	h5zShuffleTotalNParms = 1 /* Number of parameters stored in the file */
)

// h5zFilterShuffleFunc is H5Z__filter_shuffle with H5Z_FLAG_REVERSE: it
// "re-interlaces" a block of data that was shuffled by putting all the bytes
// of one byte position of each element together. For 4-byte elements stored
// shuffled as 000111222333 it restores 012301230123.
func h5zFilterShuffleFunc(flags uint, cdValues []uint32, buf []byte) ([]byte, error) {
	/* Check arguments */
	if len(cdValues) != h5zShuffleTotalNParms || cdValues[h5zShuffleParmSize] == 0 {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid shuffle parameters")
	}
	if flags&h5zFlagReverse == 0 {
		return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED, "shuffle is only supported in the read direction")
	}

	/* Get the number of bytes per element from the parameter block */
	bytesoftype := uint64(cdValues[h5zShuffleParmSize])
	nbytes := uint64(len(buf))

	/* Compute the number of elements in buffer */
	numofelements := nbytes / bytesoftype

	/* Don't do anything for 1-byte elements, or "fractional" elements */
	if bytesoftype > 1 && numofelements > 1 {
		/* Compute the leftover bytes if there are any */
		leftover := nbytes % bytesoftype

		/* Allocate the destination buffer */
		dest := make([]byte, nbytes)

		/* Input; unshuffle */
		src := buf
		for i := uint64(0); i < bytesoftype; i++ {
			d := i
			for j := uint64(0); j < numofelements; j++ {
				dest[d] = src[0]
				src = src[1:]
				d += bytesoftype
			}
		}

		/* Add leftover to the end of data */
		if leftover > 0 {
			copy(dest[nbytes-leftover:], src[:leftover])
		}
		return dest, nil
	}

	return buf, nil
}
