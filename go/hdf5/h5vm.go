package hdf5

// Translated from src/H5VM.c and the inline helpers / macros of
// src/H5VMprivate.h (only the vector helpers needed by the chunk index
// code: log2 helpers, bit get, "down" sizes, array offsets, chunk index and
// the swizzle/unswizzle of the unlimited dimension).

import "math/bits"

// h5vmLog2Gen is H5VM_log2_gen: determines the log base two of a number
// (i.e. the highest bit set in a number). log2(0) is 0, as in the C lookup
// table version.
func h5vmLog2Gen(n uint64) uint {
	if n == 0 {
		return 0
	}
	return uint(bits.Len64(n) - 1)
}

// h5vmMultiplyDeBruijnBitPosition is MultiplyDeBruijnBitPosition: lookup
// table for the specialized log2(n) of power of two routine.
func h5vmMultiplyDeBruijnBitPosition(i uint32) uint {
	tbl := [32]uint{0, 1, 28, 2, 29, 14, 24, 3, 30, 22, 20,
		15, 25, 17, 4, 8, 31, 27, 13, 23, 21, 19,
		16, 7, 26, 12, 18, 6, 11, 5, 10, 9}
	return tbl[i&31]
}

// h5vmLog2Of2 is H5VM_log2_of2: determines the log base two of a number
// (i.e. the highest bit set in a number).
//
// Note: N must be a power of two and is limited to 32-bit quantities (for
// other values the result is meaningless but always < 32, as in C).
func h5vmLog2Of2(n uint32) uint {
	return h5vmMultiplyDeBruijnBitPosition((n * 0x077CB531) >> 27)
}

// h5vmPowerOfTwo is the POWER_OF_TWO macro (H5private.h).
func h5vmPowerOfTwo(n uint64) bool {
	return n&(n-1) == 0 && n != 0
}

// h5vmBitGet is H5VM_bit_get: determine the value of the n'th bit in a
// buffer. Bits are sequentially stored in the buffer, starting with bit
// offset 0 in the first byte's high-bit position. An offset past the end of
// the buffer reads as false (the C routine does no range checking).
func h5vmBitGet(buf []byte, offset uint64) bool {
	if offset/8 >= uint64(len(buf)) {
		return false
	}
	return buf[offset/8]&(0x80>>(offset%8)) != 0
}

// h5vmArrayDown is H5VM_array_down: given a set of dimension sizes,
// calculate the size of each "down" slice. This is the size of the
// dimensions for all the dimensions below the current one, which is used for
// indexing offsets in this dimension.
func h5vmArrayDown(n int, totalSize []uint64, down []uint64) {
	/* Build the sizes of each dimension in the array
	 * (From fastest to slowest)
	 */
	acc := uint64(1)
	for i := n - 1; i >= 0; i-- {
		down[i] = acc
		acc *= totalSize[i]
	}
}

// h5vmArrayOffsetPre is H5VM_array_offset_pre: given a coordinate
// description of a location in an array, this function returns the offset
// of the coordinate, using the precomputed "down" sizes in acc.
func h5vmArrayOffsetPre(n int, acc []uint64, offset []uint64) uint64 {
	/* Compute offset in array */
	var ret uint64
	for u := 0; u < n; u++ {
		ret += acc[u] * offset[u]
	}
	return ret
}

// h5vmChunkIndex is H5VM_chunk_index: given a coordinate offset (coord), the
// size of each chunk (chunk), the number of dimensions (ndims) and the
// "down" number of chunks in each dimension (downNchunks), compute the
// linear chunk index. The chunk sizes must be non-zero.
func h5vmChunkIndex(ndims int, coord []uint64, chunk []uint32, downNchunks []uint64) uint64 {
	scaledCoord := make([]uint64, ndims) /* Scaled, coordinates, in terms of chunks */

	/* Defer to H5VM_chunk_index_scaled */
	return h5vmChunkIndexScaled(ndims, coord, chunk, downNchunks, scaledCoord)
}

// h5vmChunkIndexScaled is H5VM_chunk_index_scaled: as h5vmChunkIndex, also
// returning the scaled coordinates in scaled.
func h5vmChunkIndexScaled(ndims int, coord []uint64, chunk []uint32, downNchunks []uint64, scaled []uint64) uint64 {
	/* Compute the scaled coordinates for actual coordinates */
	/* (Note that the 'scaled' array is an 'OUT' parameter) */
	for u := 0; u < ndims; u++ {
		scaled[u] = coord[u] / uint64(chunk[u])
	}

	/* Compute the chunk index */
	return h5vmArrayOffsetPre(ndims, downNchunks, scaled)
}

// h5vmSwizzleCoords is the H5VM_swizzle_coords macro: move the value at
// position unlimDim to position 0, shifting the values before it up by one.
// Nothing to do when the unlimited dimension is at position 0.
func h5vmSwizzleCoords[T any](coords []T, unlimDim int) {
	if unlimDim != 0 {
		tmp := coords[unlimDim]
		copy(coords[1:unlimDim+1], coords[0:unlimDim])
		coords[0] = tmp
	}
}

// h5vmUnswizzleCoords is the H5VM_unswizzle_coords macro: the inverse of
// h5vmSwizzleCoords.
func h5vmUnswizzleCoords[T any](coords []T, unlimDim int) {
	if unlimDim != 0 {
		tmp := coords[0]
		copy(coords[0:unlimDim], coords[1:unlimDim+1])
		coords[unlimDim] = tmp
	}
}
