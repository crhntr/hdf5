package hdf5

// Translated from src/H5EAiblock.c (read side only: alloc, protect). Index
// block routines for extensible arrays.

// h5eaIblockAlloc is H5EA__iblock_alloc: allocate extensible array index
// block.
func h5eaIblockAlloc(hdr *h5eaHdr) (*h5eaIblock, error) {
	/* Allocate memory for the index block */
	iblock := &h5eaIblock{}

	/* Share common array information */
	iblock.hdr = hdr

	/* Set non-zero internal fields */
	iblock.addr = haddrUndef

	/* Compute information */
	iblock.nsblks = h5eaSblkFirstIdx(hdr.supBlkMinDataPtrs)
	iblock.ndblkAddrs = 2 * (uint64(hdr.supBlkMinDataPtrs) - 1)
	if iblock.nsblks > hdr.nsblks {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "extensible array index block holds more super blocks than the array")
	}
	iblock.nsblkAddrs = hdr.nsblks - iblock.nsblks

	/* Allocate buffer for elements in index block */
	if hdr.idxBlkElmts > 0 {
		iblock.elmts = make([]any, hdr.idxBlkElmts)
	}

	/* Allocate buffer for data block addresses in index block */
	if iblock.ndblkAddrs > 0 {
		iblock.dblkAddrs = make([]haddr, iblock.ndblkAddrs)
	}

	/* Allocate buffer for super block addresses in index block */
	if iblock.nsblkAddrs > 0 {
		iblock.sblkAddrs = make([]haddr, iblock.nsblkAddrs)
	}

	return iblock, nil
}

// h5eaIblockProtect is H5EA__iblock_protect: convenience wrapper around
// protecting extensible array index block.
func h5eaIblockProtect(hdr *h5eaHdr) (*h5eaIblock, error) {
	/* Protect the index block */
	v, err := hdr.f.cached("H5EA_IBLOCK", hdr.idxBlkAddr, func() (any, error) {
		return h5eaCacheIblockLoad(hdr)
	})
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array index block, address = %d", hdr.idxBlkAddr)
	}
	iblock, ok := v.(*h5eaIblock)
	if !ok || iblock.hdr != hdr {
		return nil, errorf(H5E_EARRAY, H5E_CANTPROTECT, "cached object at address %d is not this extensible array's index block", hdr.idxBlkAddr)
	}
	return iblock, nil
}
