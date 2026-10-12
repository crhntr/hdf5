package hdf5

// Translated from src/H5HFdtable.c ("doubling table" routines for fractal
// heaps). The H5VM helpers it uses (src/H5VMprivate.h) are translated at the
// bottom of the file with an h5hf prefix so they cannot collide with a
// translation of H5VM.

import "math/bits"

// h5hfDtableInit is H5HF__dtable_init: initialize values for doubling table.
func h5hfDtableInit(dtable *h5hfDtable) error {
	/* Validate the creation parameters (checked at creation time in the C
	 * library, see H5HF__hdr_create; checked here because the values come
	 * from the file and drive the table computations) */
	if dtable.width == 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "width must be greater than zero")
	}
	if dtable.width&(dtable.width-1) != 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "width not power of two")
	}
	if dtable.startBlockSize == 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "starting block size must be greater than zero")
	}
	if dtable.startBlockSize&(dtable.startBlockSize-1) != 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "starting block size not power of two")
	}
	if dtable.maxDirectSize == 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "max. direct block size must be greater than zero")
	}
	if dtable.maxDirectSize > h5hfMaxDirectSizeLimit {
		return errorf(H5E_HEAP, H5E_BADVALUE, "max. direct block size too large")
	}
	if dtable.maxDirectSize&(dtable.maxDirectSize-1) != 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "max. direct block size not power of two")
	}
	if dtable.startBlockSize > dtable.maxDirectSize {
		return errorf(H5E_HEAP, H5E_BADVALUE, "starting block size larger than max. direct block size")
	}
	if dtable.maxIndexBits == 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "max. heap size must be greater than zero")
	}
	if dtable.maxIndexBits > 64 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "max. heap size too large")
	}

	/* Compute/cache some values */
	dtable.startBits = uint16(h5hfLog2Of2(uint32(dtable.startBlockSize)))
	dtable.firstRowBits = uint32(dtable.startBits) + h5hfLog2Of2(uint32(dtable.width))
	if dtable.firstRowBits > uint32(dtable.maxIndexBits) {
		return errorf(H5E_HEAP, H5E_BADVALUE, "max. heap size smaller than first row of doubling table")
	}
	dtable.maxRootRows = (uint32(dtable.maxIndexBits) - dtable.firstRowBits) + 1
	dtable.maxDirectBits = uint16(h5hfLog2Of2(uint32(dtable.maxDirectSize)))
	dtable.maxDirectRows = (uint32(dtable.maxDirectBits) - uint32(dtable.startBits)) + 2
	dtable.maxDirRows = dtable.maxDirectRows
	dtable.numIDFirstRow = dtable.startBlockSize * uint64(dtable.width)
	dtable.maxDirBlkOffSize = h5hfSizeofOffsetLen(dtable.maxDirectSize)

	/* Build table of block sizes for each row */
	dtable.rowBlockSize = make([]uint64, dtable.maxRootRows)
	dtable.rowBlockOff = make([]uint64, dtable.maxRootRows)
	dtable.rowTotDblockFree = make([]uint64, dtable.maxRootRows)
	dtable.rowMaxDblockFree = make([]uint64, dtable.maxRootRows)
	tmpBlockSize := dtable.startBlockSize
	accBlockOff := dtable.startBlockSize * uint64(dtable.width)
	dtable.rowBlockSize[0] = dtable.startBlockSize
	dtable.rowBlockOff[0] = 0
	for u := uint32(1); u < dtable.maxRootRows; u++ {
		dtable.rowBlockSize[u] = tmpBlockSize
		dtable.rowBlockOff[u] = accBlockOff
		tmpBlockSize *= 2
		accBlockOff *= 2
	}
	return nil
}

// h5hfDtableLookup is H5HF__dtable_lookup: compute the row & col of an
// offset in a doubling-table.
func h5hfDtableLookup(dtable *h5hfDtable, off uint64) (row, col uint32, err error) {
	/* Check for offset in first row */
	if off < dtable.numIDFirstRow {
		row = 0
		col = uint32(off / dtable.startBlockSize)
	} else {
		highBit := h5hfLog2Gen(off)     /* Determine the high bit in the offset */
		offMask := uint64(1) << highBit /* Compute mask for determining column */

		row = (highBit - dtable.firstRowBits) + 1
		if row >= uint32(len(dtable.rowBlockSize)) {
			return 0, 0, errorf(H5E_HEAP, H5E_BADRANGE, "offset %d outside of doubling table", off)
		}
		col = uint32((off - offMask) / dtable.rowBlockSize[row])
	}
	return row, col, nil
}

// h5hfDtableSizeToRow is H5HF__dtable_size_to_row: compute row that can
// hold block of a certain size.
func h5hfDtableSizeToRow(dtable *h5hfDtable, blockSize uint64) uint32 {
	if blockSize == dtable.startBlockSize {
		return 0
	}
	return (h5hfLog2Of2(uint32(blockSize)) - h5hfLog2Of2(uint32(dtable.startBlockSize))) + 1
}

// h5hfDtableSizeToRows is H5HF__dtable_size_to_rows: compute # of rows of
// indirect block of a given size.
func h5hfDtableSizeToRows(dtable *h5hfDtable, size uint64) uint32 {
	return (h5hfLog2Gen(size) - dtable.firstRowBits) + 1
}

// h5hfDtableSpanSize is H5HF__dtable_span_size: compute the size covered by
// a span of entries.
func h5hfDtableSpanSize(dtable *h5hfDtable, startRow, startCol, numEntries uint32) uint64 {
	width := uint32(dtable.width)
	if width == 0 {
		return 0
	}

	/* Compute starting entry */
	startEntry := (startRow * width) + startCol

	/* Compute ending entry, column & row */
	endEntry := (startEntry + numEntries) - 1
	endRow := endEntry / width
	endCol := endEntry % width

	/* (zero on failure: an empty span or one past the end of the table) */
	if numEntries == 0 || endEntry < startEntry || endRow >= uint32(len(dtable.rowBlockSize)) {
		return 0
	}

	/* Initialize accumulated span size */
	var accSpanSize uint64

	/* Compute span size covered */

	/* Check for multi-row span */
	if startRow != endRow {
		/* Accommodate partial starting row */
		if startCol > 0 {
			accSpanSize = dtable.rowBlockSize[startRow] * uint64(width-startCol)
			startRow++
		}

		/* Accumulate full rows */
		for startRow < endRow {
			accSpanSize += dtable.rowBlockSize[startRow] * uint64(width)
			startRow++
		}

		/* Accommodate partial ending row */
		accSpanSize += dtable.rowBlockSize[startRow] * uint64(endCol+1)
	} else {
		/* Span is in same row */
		accSpanSize = dtable.rowBlockSize[startRow] * uint64((endCol-startCol)+1)
	}
	return accSpanSize
}

// h5hfLog2Gen is H5VM_log2_gen: the log base two of n (i.e. the highest bit
// set in n; 0 for n == 0).
func h5hfLog2Gen(n uint64) uint32 {
	if n == 0 {
		return 0
	}
	return uint32(bits.Len64(n) - 1)
}

// h5hfLog2Of2 is H5VM_log2_of2: the log base two of n, which must be a
// power of two (limited to 32-bit quantities).
func h5hfLog2Of2(n uint32) uint32 {
	return uint32(bits.TrailingZeros32(n)) & 31
}

// h5hfLimitEncSize is H5VM_limit_enc_size: the # of bytes needed to encode
// values within a range from 0 to a given limit.
func h5hfLimitEncSize(limit uint64) uint32 {
	return (h5hfLog2Gen(limit) / 8) + 1
}
