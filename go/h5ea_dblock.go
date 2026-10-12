package hdf5

// Translated from src/H5EAdblock.c (read side only: alloc, sblk_idx,
// protect). Data block routines for extensible arrays.

// h5eaDblockAlloc is H5EA__dblock_alloc: allocate extensible array data
// block.
func h5eaDblockAlloc(hdr *h5eaHdr, nelmts uint64) (*h5eaDblock, error) {
	/* Check arguments */
	if nelmts == 0 {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "extensible array data block with no elements")
	}

	/* Allocate memory for the data block */
	dblock := &h5eaDblock{}

	/* Share common array information */
	dblock.hdr = hdr

	/* Set non-zero internal fields */
	dblock.nelmts = nelmts

	/* Check if the data block is not going to be paged */
	if nelmts > hdr.dblkPageNelmts {
		/* Set the # of pages in the direct block */
		dblock.npages = nelmts / hdr.dblkPageNelmts
		if nelmts != dblock.npages*hdr.dblkPageNelmts {
			return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "extensible array data block size is not a multiple of the page size")
		}
	} else {
		/* Allocate buffer for elements in data block */
		elmts, err := h5eaHdrAllocElmts(hdr, nelmts)
		if err != nil {
			return nil, wrapf(err, H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for data block element buffer")
		}
		dblock.elmts = elmts
	}

	return dblock, nil
}

// h5eaDblockSblkIdx is H5EA__dblock_sblk_idx: compute the index of the super
// block where the element is located. idx must be >= the # of elements in
// the index block.
func h5eaDblockSblkIdx(hdr *h5eaHdr, idx uint64) uint64 {
	/* Adjust index for elements in index block */
	idx -= uint64(hdr.idxBlkElmts)

	/* Determine the superblock information for the index */
	return uint64(h5vmLog2Gen((idx / uint64(hdr.dataBlkMinElmts)) + 1))
}

// h5eaDblockProtect is H5EA__dblock_protect: convenience wrapper around
// protecting extensible array data block.
func h5eaDblockProtect(hdr *h5eaHdr, dblkAddr haddr, dblkNelmts uint64) (*h5eaDblock, error) {
	/* Sanity check */
	if !addrDefined(dblkAddr) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "undefined extensible array data block address")
	}
	if dblkNelmts == 0 {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "extensible array data block with no elements")
	}

	/* Set up user data */
	udata := h5eaDblockCacheUd{hdr: hdr, nelmts: dblkNelmts, dblkAddr: dblkAddr}

	/* Protect the data block */
	v, err := hdr.f.cached("H5EA_DBLOCK", dblkAddr, func() (any, error) {
		return h5eaCacheDblockLoad(&udata)
	})
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array data block, address = %d", dblkAddr)
	}
	dblock, ok := v.(*h5eaDblock)
	if !ok || dblock.hdr != hdr || dblock.nelmts != dblkNelmts {
		return nil, errorf(H5E_EARRAY, H5E_CANTPROTECT, "cached object at address %d is not this extensible array's data block of %d elements", dblkAddr, dblkNelmts)
	}
	return dblock, nil
}
