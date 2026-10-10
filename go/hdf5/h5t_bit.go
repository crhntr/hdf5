package hdf5

// Translated from src/H5Tbit.c (the operations used by the datatype
// conversion code). Bit vectors are little-endian: bit 0 is the least
// significant bit of buf[0]. The C functions process whole bytes where
// possible; these process one bit at a time, which has the same result.
// Callers guarantee that the bit ranges are within buf.

// H5T_sdir_t
const (
	h5tBitLSB = 0 /* search from the least significant end */
	h5tBitMSB = 1 /* search from the most significant end */
)

func bitAt(buf []byte, i uint64) bool { return buf[i/8]>>(i%8)&1 != 0 }

func setBitAt(buf []byte, i uint64, v bool) {
	if v {
		buf[i/8] |= 1 << (i % 8)
	} else {
		buf[i/8] &^= 1 << (i % 8)
	}
}

// h5tBitCopy is H5T__bit_copy: copy size bits from src starting at bit
// srcOffset to dst starting at bit dstOffset.
func h5tBitCopy(dst []byte, dstOffset uint64, src []byte, srcOffset uint64, size uint64) {
	for i := uint64(0); i < size; i++ {
		setBitAt(dst, dstOffset+i, bitAt(src, srcOffset+i))
	}
}

// h5tBitGetD is H5T__bit_get_d: return a small bit sequence as a number.
// The bit vector starts at offset and is size bits long (size <= 64).
func h5tBitGetD(buf []byte, offset, size uint64) uint64 {
	var val uint64
	for i := uint64(0); i < size && i < 64; i++ {
		if bitAt(buf, offset+i) {
			val |= 1 << i
		}
	}
	return val
}

// h5tBitSetD is H5T__bit_set_d: set part of a bit vector to the specified
// unsigned value (size <= 64).
func h5tBitSetD(buf []byte, offset, size uint64, val uint64) {
	for i := uint64(0); i < size; i++ {
		setBitAt(buf, offset+i, i < 64 && val>>i&1 != 0)
	}
}

// h5tBitSet is H5T__bit_set: set or clear bits in a contiguous region of a
// vector beginning at bit offset and continuing for size bits.
func h5tBitSet(buf []byte, offset, size uint64, value bool) {
	for i := uint64(0); i < size; i++ {
		setBitAt(buf, offset+i, value)
	}
}

// h5tBitFind is H5T__bit_find: find the first bit with the specified value
// within a region of a bit vector. The region begins at offset and
// continues for size bits, and can be searched from the least significant
// end toward the most significant end (h5tBitLSB) or the other way
// (h5tBitMSB). It returns the position of the bit found, relative to the
// offset, or -1.
func h5tBitFind(buf []byte, offset, size uint64, direction int, value bool) int64 {
	switch direction {
	case h5tBitLSB:
		for i := uint64(0); i < size; i++ {
			if bitAt(buf, offset+i) == value {
				return int64(i)
			}
		}
	case h5tBitMSB:
		for i := size; i > 0; i-- {
			if bitAt(buf, offset+i-1) == value {
				return int64(i - 1)
			}
		}
	}
	return -1
}

// h5tBitInc is H5T__bit_inc: increment part of a bit field by adding 1.
// The bit field starts with bit position start and is size bits long. It
// returns the carry-out value: true if the field overflows.
func h5tBitInc(buf []byte, start, size uint64) bool {
	carry := true
	for i := uint64(0); i < size && carry; i++ {
		b := bitAt(buf, start+i)
		setBitAt(buf, start+i, !b)
		carry = b
	}
	return carry
}
