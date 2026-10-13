package hdf5

// Translated from src/H5EAhdr.c (read side only: alloc, init, alloc_elmts,
// protect). Array header routines.

// H5EA_MAX_NELMTS_IDX_MAX: max. limit on the # of bits used to store the
// max. # of elements in the array.
const h5eaMaxNelmtsIdxMax = 64

// h5eaSblkDblkNelmts is H5EA_SBLK_DBLK_NELMTS: # of elements in a data
// block for a particular super block.
func h5eaSblkDblkNelmts(s uint64, m uint8) uint64 {
	return (uint64(1) << ((s + 1) / 2)) * uint64(m)
}

// h5eaHdrAlloc is H5EA__hdr_alloc: allocate shared extensible array header.
func h5eaHdrAlloc(f *File) *h5eaHdr {
	/* Allocate space for the shared information */
	hdr := &h5eaHdr{}

	/* Set non-zero internal fields */
	hdr.addr = haddrUndef

	/* Set the internal parameters for the array */
	hdr.f = f
	hdr.sizeofAddr = uint64(f.sizeofAddr)
	hdr.sizeofSize = uint64(f.sizeofSize)

	return hdr
}

// h5eaHdrInit is H5EA__hdr_init: compute useful information for extensible
// array, based on "creation" information.
//
// Notes: The equations for variables below are based on this information:
//
//	<sblk idx>  <# of dblks>  <size of dblks>       Range of elements in sblk
//	==========  ============  ===============       =========================
//	      0          1         1 * <dblk min elmts>   0 * <dblk min elmts> <->   1 * <dblk min elmts> - 1
//	      1          1         2 * <dblk min elmts>   1 * <dblk min elmts> <->   3 * <dblk min elmts> - 1
//	      2          2         2 * <dblk min elmts>   3 * <dblk min elmts> <->   7 * <dblk min elmts> - 1
//	      3          2         4 * <dblk min elmts>   7 * <dblk min elmts> <->  15 * <dblk min elmts> - 1
//	      4          4         4 * <dblk min elmts>  15 * <dblk min elmts> <->  31 * <dblk min elmts> - 1
//	      5          4         8 * <dblk min elmts>  31 * <dblk min elmts> <->  63 * <dblk min elmts> - 1
//	      6          8         8 * <dblk min elmts>  63 * <dblk min elmts> <-> 127 * <dblk min elmts> - 1
//	      7          8        16 * <dblk min elmts> 127 * <dblk min elmts> <-> 255 * <dblk min elmts> - 1
//	      .          .         . * <dblk min elmts>   . * <dblk min elmts> <->   . * <dblk min elmts> - 1
//
//	Therefore:
//		<sblk idx>(<elmt idx>) = lg2((<elmt idx> / <dblk min elmts>) + 1)
//		<# of dblks>(<sblk idx>) = 2 ^ (<sblk idx> / 2)
//		<size of dblk>(<sblk idx>) = 2 ^ ((<sblk idx> + 1) / 2)
//		<total # of sblks>(<max. # of elmts>) = 1 + (lg2(<max. # of elmts>) - lg2(<dblk min_elmts>))
//
// The class and the client callback context (the result of the class'
// crt_context callback in C) are stored in the header.
func h5eaHdrInit(hdr *h5eaHdr, cls *h5eaClass, ctx any) error {
	/* Sanity check */
	/* (the C code asserts these; a corrupt file must not get past them) */
	if hdr.maxNelmtsBits == 0 || hdr.maxNelmtsBits > h5eaMaxNelmtsIdxMax {
		return errorf(H5E_EARRAY, H5E_BADVALUE, "max. # of elements bits must be > 0 and <= %d (is %d)", h5eaMaxNelmtsIdxMax, hdr.maxNelmtsBits)
	}
	if !h5vmPowerOfTwo(uint64(hdr.dataBlkMinElmts)) {
		return errorf(H5E_EARRAY, H5E_BADVALUE, "min # of elements per data block must be power of two (is %d)", hdr.dataBlkMinElmts)
	}
	if hdr.supBlkMinDataPtrs < 2 || !h5vmPowerOfTwo(uint64(hdr.supBlkMinDataPtrs)) {
		return errorf(H5E_EARRAY, H5E_BADVALUE, "min # of data block pointers in super block must be a power of two >= two (is %d)", hdr.supBlkMinDataPtrs)
	}
	if hdr.eltSize == 0 {
		return errorf(H5E_EARRAY, H5E_BADVALUE, "element size must be greater than zero")
	}
	/* (the C code shifts 1 by the page size bits: undefined at 64 and
	 *  more. Larger pages than the array are not checked: no data block
	 *  is paged then) */
	if hdr.maxDblkPageNelmtsBits >= 64 {
		return errorf(H5E_EARRAY, H5E_BADVALUE, "max. # of elements per data block page bits must be < 64 (is %d)", hdr.maxDblkPageNelmtsBits)
	}
	if uint64(h5vmLog2Of2(uint32(hdr.dataBlkMinElmts))) > uint64(hdr.maxNelmtsBits) {
		return errorf(H5E_EARRAY, H5E_BADVALUE, "min # of elements per data block larger than the max. # of elements")
	}

	/* Compute general information */
	hdr.nsblks = 1 + (uint64(hdr.maxNelmtsBits) - uint64(h5vmLog2Of2(uint32(hdr.dataBlkMinElmts))))
	hdr.dblkPageNelmts = uint64(1) << hdr.maxDblkPageNelmtsBits
	hdr.arrOffSize = uint8(h5eaSizeofOffsetBits(uint64(hdr.maxNelmtsBits)))

	/* (the index block holds the data block addresses of the first super
	 *  blocks; there must be at least that many super blocks) */
	if h5eaSblkFirstIdx(hdr.supBlkMinDataPtrs) > hdr.nsblks {
		return errorf(H5E_EARRAY, H5E_BADVALUE, "min # of data block pointers in super block too large for the array size")
	}

	/* Allocate information for each super block */
	hdr.sblkInfo = make([]h5eaSblkInfo, hdr.nsblks)

	/* Compute information about each super block */
	var startIdx, startDblk uint64 /* First element / data block index for each super block */
	for u := uint64(0); u < hdr.nsblks; u++ {
		hdr.sblkInfo[u].ndblks = uint64(1) << (u / 2)
		hdr.sblkInfo[u].dblkNelmts = h5eaSblkDblkNelmts(u, hdr.dataBlkMinElmts)
		hdr.sblkInfo[u].startIdx = startIdx
		hdr.sblkInfo[u].startDblk = startDblk

		/* Advance starting indices for next super block */
		startIdx += hdr.sblkInfo[u].ndblks * hdr.sblkInfo[u].dblkNelmts
		startDblk += hdr.sblkInfo[u].ndblks
	}

	/* Set size of header on disk (locally and in statistics) */
	hdr.size = h5eaHeaderSizeHdr(hdr)
	hdr.hdrSize = hdr.size

	/* Create the callback context, if there's one */
	hdr.cls = cls
	hdr.cbCtx = ctx

	return nil
}

// h5eaHdrAllocElmts is H5EA__hdr_alloc_elmts: allocate extensible array data
// block elements.
func h5eaHdrAllocElmts(hdr *h5eaHdr, nelmts uint64) ([]any, error) {
	/* Check arguments */
	if nelmts == 0 {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "no elements to allocate")
	}

	/* (bound the allocation by what the file can hold) */
	if nelmts > hdr.f.eof()/uint64(hdr.eltSize) {
		return nil, errorf(H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for data block data element buffer: %d elements of %d bytes larger than the file", nelmts, hdr.eltSize)
	}

	/* Allocate buffer for elements in index block */
	return make([]any, nelmts), nil
}

// h5eaHdrProtect is H5EA__hdr_protect: convenience wrapper around
// protecting extensible array header.
func h5eaHdrProtect(f *File, eaAddr haddr, cls *h5eaClass, ctx any) (*h5eaHdr, error) {
	/* Sanity check */
	if !addrDefined(eaAddr) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "undefined extensible array header address")
	}

	/* Protect the header */
	v, err := f.cached("H5EA_HDR", eaAddr, func() (any, error) {
		return h5eaCacheHdrLoad(f, eaAddr, cls, ctx)
	})
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array header, address = %d", eaAddr)
	}
	hdr, ok := v.(*h5eaHdr)
	if !ok {
		return nil, errorf(H5E_EARRAY, H5E_CANTPROTECT, "cached object at address %d is not an extensible array header", eaAddr)
	}

	/* (the class is looked up from the id in the file by the C library;
	 *  here the caller supplies it, so check that it matches) */
	if cls == nil || hdr.clsID != cls.id {
		return nil, errorf(H5E_EARRAY, H5E_BADTYPE, "incorrect extensible array class")
	}
	return hdr, nil
}
