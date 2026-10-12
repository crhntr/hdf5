package hdf5

// Translated from src/H5FAdblkpage.c (read side only: alloc, protect). Data
// block page routines for fixed arrays.

// h5faDblkPageAlloc is H5FA__dblk_page_alloc: allocate fixed array data
// block page.
func h5faDblkPageAlloc(hdr *h5faHdr, nelmts uint64) (*h5faDblkPage, error) {
	/* Allocate memory for the data block */
	dblkPage := &h5faDblkPage{}

	/* Share common array information */
	dblkPage.hdr = hdr

	/* Set non-zero internal fields */
	dblkPage.nelmts = nelmts

	/* Allocate buffer for elements in data block page */
	/* (bound the allocation by what the file can hold) */
	if nelmts > hdr.f.eof()/uint64(hdr.eltSize) {
		return nil, errorf(H5E_FARRAY, H5E_CANTALLOC, "memory allocation failed for data block page element buffer: %d elements larger than the file", nelmts)
	}
	dblkPage.elmts = make([]any, nelmts)

	return dblkPage, nil
}

// h5faDblkPageProtect is H5FA__dblk_page_protect: convenience wrapper around
// protecting fixed array data block page.
func h5faDblkPageProtect(hdr *h5faHdr, dblkPageAddr haddr, dblkPageNelmts uint64) (*h5faDblkPage, error) {
	/* Sanity check */
	if !addrDefined(dblkPageAddr) {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "undefined fixed array data block page address")
	}

	/* Set up user data */
	udata := h5faDblkPageCacheUd{hdr: hdr, nelmts: dblkPageNelmts, dblkPageAddr: dblkPageAddr}

	/* Protect the data block page */
	v, err := hdr.f.cached("H5FA_DBLK_PAGE", dblkPageAddr, func() (any, error) {
		return h5faCacheDblkPageLoad(&udata)
	})
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTPROTECT, "unable to protect fixed array data block page, address = %d", dblkPageAddr)
	}
	dblkPage, ok := v.(*h5faDblkPage)
	if !ok || dblkPage.hdr != hdr || dblkPage.nelmts != dblkPageNelmts {
		return nil, errorf(H5E_FARRAY, H5E_CANTPROTECT, "cached object at address %d is not this fixed array's data block page of %d elements", dblkPageAddr, dblkPageNelmts)
	}
	return dblkPage, nil
}
