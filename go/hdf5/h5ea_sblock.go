package hdf5

// Translated from src/H5EAsblock.c (read side only: alloc, protect). Super
// block routines for extensible arrays.

// h5eaSblockAlloc is H5EA__sblock_alloc: allocate extensible array super
// block.
func h5eaSblockAlloc(hdr *h5eaHdr, sblkIdx uint64) (*h5eaSblock, error) {
	/* Check arguments */
	if sblkIdx >= hdr.nsblks {
		return nil, errorf(H5E_EARRAY, H5E_BADRANGE, "extensible array super block index %d out of range", sblkIdx)
	}

	/* Allocate memory for the index block */
	sblock := &h5eaSblock{}

	/* Share common array information */
	sblock.hdr = hdr

	/* Set non-zero internal fields */
	sblock.addr = haddrUndef

	/* Compute/cache information */
	sblock.idx = sblkIdx
	sblock.ndblks = hdr.sblkInfo[sblkIdx].ndblks
	sblock.dblkNelmts = hdr.sblkInfo[sblkIdx].dblkNelmts

	/* (bound the allocations below by the file size: the super block must
	 *  fit in the file) */
	if sblock.ndblks > hdr.f.eof()/hdr.sizeofAddr {
		return nil, errorf(H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for super block data block addresses: %d addresses larger than the file", sblock.ndblks)
	}

	/* Allocate buffer for data block addresses in super block */
	sblock.dblkAddrs = make([]haddr, sblock.ndblks)

	/* Check if # of elements in data blocks requires paging */
	if sblock.dblkNelmts > hdr.dblkPageNelmts {
		/* Compute # of pages in each data block from this super block */
		sblock.dblkNpages = sblock.dblkNelmts / hdr.dblkPageNelmts

		/* Sanity check that we have at least 2 pages in data block */
		/* Sanity check for integer truncation */
		/* (both are powers of two, so these hold for a valid header) */
		if sblock.dblkNpages < 2 || sblock.dblkNpages*hdr.dblkPageNelmts != sblock.dblkNelmts {
			return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "bad # of pages in extensible array data block")
		}

		/* Compute size of buffer for each data block's 'page init' bitmask */
		sblock.dblkPageInitSize = (sblock.dblkNpages + 7) / 8

		/* Allocate buffer for all 'page init' bitmasks in super block */
		if sblock.dblkPageInitSize > hdr.f.eof()/sblock.ndblks {
			return nil, errorf(H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for super block page init bitmask: larger than the file")
		}
		sblock.pageInit = make([]byte, sblock.ndblks*sblock.dblkPageInitSize)

		/* Compute data block page size */
		sblock.dblkPageSize = (hdr.dblkPageNelmts * uint64(hdr.eltSize)) + h5eaSizeofChksum
	}

	return sblock, nil
}

// h5eaSblockProtect is H5EA__sblock_protect: convenience wrapper around
// protecting extensible array super block.
func h5eaSblockProtect(hdr *h5eaHdr, sblkAddr haddr, sblkIdx uint64) (*h5eaSblock, error) {
	/* Sanity check */
	if !addrDefined(sblkAddr) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "undefined extensible array super block address")
	}
	if sblkIdx == 0 || sblkIdx >= hdr.nsblks {
		return nil, errorf(H5E_EARRAY, H5E_BADRANGE, "extensible array super block index %d out of range", sblkIdx)
	}

	/* Set up user data */
	udata := h5eaSblockCacheUd{hdr: hdr, sblkIdx: sblkIdx, sblkAddr: sblkAddr}

	/* Protect the super block */
	v, err := hdr.f.cached("H5EA_SBLOCK", sblkAddr, func() (any, error) {
		return h5eaCacheSblockLoad(&udata)
	})
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array super block, address = %d", sblkAddr)
	}
	sblock, ok := v.(*h5eaSblock)
	if !ok || sblock.hdr != hdr || sblock.idx != sblkIdx {
		return nil, errorf(H5E_EARRAY, H5E_CANTPROTECT, "cached object at address %d is not this extensible array's super block %d", sblkAddr, sblkIdx)
	}
	return sblock, nil
}
