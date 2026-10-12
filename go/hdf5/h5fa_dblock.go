package hdf5

// Translated from src/H5FAdblock.c (read side only: alloc, protect). Data
// block routines for fixed arrays.

// h5faDblockAlloc is H5FA__dblock_alloc: allocate fixed array data block.
func h5faDblockAlloc(hdr *h5faHdr) (*h5faDblock, error) {
	/* Check arguments */
	if hdr.nelmts == 0 {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "fixed array data block with no elements")
	}

	/* Allocate memory for the data block */
	dblock := &h5faDblock{}

	/* Share common array information */
	dblock.hdr = hdr

	/* Set non-zero internal fields */
	dblock.dblkPageNelmts = uint64(1) << hdr.maxDblkPageNelmtsBits

	/* Check if this data block should be paged */
	if hdr.nelmts > dblock.dblkPageNelmts {
		/* Compute number of pages */
		npages := ((hdr.nelmts + dblock.dblkPageNelmts) - 1) / dblock.dblkPageNelmts

		/* Safely assign the number of pages */
		dblock.npages = npages

		/* Sanity check that we have at least 1 page */
		if dblock.npages == 0 {
			return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "fixed array data block with no pages")
		}

		/* Compute size of 'page init' flag array, in bytes */
		dblock.dblkPageInitSize = (dblock.npages + 7) / 8

		/* Allocate space for 'page init' flags */
		if dblock.dblkPageInitSize > hdr.f.eof() {
			return nil, errorf(H5E_FARRAY, H5E_CANTALLOC, "memory allocation failed for page init bitmask: larger than the file")
		}
		dblock.dblkPageInit = make([]byte, dblock.dblkPageInitSize)

		/* Compute data block page size */
		dblock.dblkPageSize = (dblock.dblkPageNelmts * uint64(hdr.eltSize)) + h5faSizeofChksum

		/* Compute the # of elements on last page */
		if hdr.nelmts%dblock.dblkPageNelmts == 0 {
			dblock.lastPageNelmts = dblock.dblkPageNelmts
		} else {
			dblock.lastPageNelmts = hdr.nelmts % dblock.dblkPageNelmts
		}
	} else {
		/* Allocate buffer for elements in data block */
		/* (bound the allocation by what the file can hold) */
		if hdr.nelmts > hdr.f.eof()/uint64(hdr.eltSize) {
			return nil, errorf(H5E_FARRAY, H5E_CANTALLOC, "memory allocation failed for data block element buffer: %d elements of %d bytes larger than the file", hdr.nelmts, hdr.eltSize)
		}
		dblock.elmts = make([]any, hdr.nelmts)
	}

	return dblock, nil
}

// h5faDblockProtect is H5FA__dblock_protect: convenience wrapper around
// protecting fixed array data block.
func h5faDblockProtect(hdr *h5faHdr, dblkAddr haddr) (*h5faDblock, error) {
	/* Sanity check */
	if !addrDefined(dblkAddr) {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "undefined fixed array data block address")
	}

	/* Set up user data */
	udata := h5faDblockCacheUd{hdr: hdr, dblkAddr: dblkAddr}

	/* Protect the data block */
	v, err := hdr.f.cached("H5FA_DBLOCK", dblkAddr, func() (any, error) {
		return h5faCacheDblockLoad(&udata)
	})
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTPROTECT, "unable to protect fixed array data block, address = %d", dblkAddr)
	}
	dblock, ok := v.(*h5faDblock)
	if !ok || dblock.hdr != hdr {
		return nil, errorf(H5E_FARRAY, H5E_CANTPROTECT, "cached object at address %d is not this fixed array's data block", dblkAddr)
	}
	return dblock, nil
}
