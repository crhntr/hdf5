package hdf5

// Translated from src/H5Zfletcher32.c (read direction only).

import "encoding/binary"

// h5zFletcherLen is FLETCHER_LEN.
const h5zFletcherLen = 4

// h5zFilterFletcher32Func is H5Z__filter_fletcher32 with H5Z_FLAG_REVERSE:
// verify the checksum stored in the last four bytes of buf (unless
// H5Z_FLAG_SKIP_EDC is set) and return the data without it.
func h5zFilterFletcher32Func(flags uint, cdValues []uint32, buf []byte) ([]byte, error) {
	if flags&h5zFlagReverse == 0 {
		return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED, "fletcher32 is only supported in the read direction")
	}
	if len(buf) < h5zFletcherLen {
		return nil, errorf(H5E_STORAGE, H5E_READERROR, "data error detected by Fletcher32 checksum")
	}

	/* Read */

	/* Do checksum if it's enabled for read; otherwise skip it
	 * to save performance. */
	srcNBytes := len(buf) - h5zFletcherLen
	if flags&h5zFlagSkipEdc == 0 {
		/* Get the stored checksum */
		storedFletcher := binary.LittleEndian.Uint32(buf[srcNBytes:])

		/* Compute checksum (can't fail) */
		fletcher := checksumFletcher32(buf[:srcNBytes])

		/* The reversed checksum.  There was a bug in the calculating code of
		 * the Fletcher32 checksum in the library before v1.6.3.  The checksum
		 * value wasn't consistent between big-endian and little-endian systems.
		 * This bug was fixed in Release 1.6.3.  However, after fixing the bug,
		 * the checksum value is no longer the same as before on little-endian
		 * system.  We'll check both the correct checksum and the wrong
		 * checksum to be consistent with Release 1.6.2 and before.
		 *
		 * (The C code swaps the two bytes within each 16-bit half of the
		 * value as laid out in memory; with the little-endian memory layout
		 * of UINT32DECODE'd values that is a byte swap of both halves.)
		 */
		reversedFletcher := (fletcher&0x00ff00ff)<<8 | (fletcher>>8)&0x00ff00ff

		/* Verify computed checksum matches stored checksum */
		if storedFletcher != fletcher && storedFletcher != reversedFletcher {
			return nil, errorf(H5E_STORAGE, H5E_READERROR, "data error detected by Fletcher32 checksum")
		}
	}

	/* Set return values */
	/* (Reuse the input buffer, just note that the size is smaller by the size of the checksum) */
	return buf[:srcNBytes], nil
}
