package hdf5

// Translated from src/H5EAdblkpage.c (read side only: alloc, protect). Data
// block page routines for extensible arrays.

// h5eaDblkPageAlloc is H5EA__dblk_page_alloc: allocate extensible array data
// block page.
func h5eaDblkPageAlloc(hdr *h5eaHdr) (*h5eaDblkPage, error) {
	/* Allocate memory for the data block */
	dblkPage := &h5eaDblkPage{}

	/* Share common array information */
	dblkPage.hdr = hdr

	/* Allocate buffer for elements in data block page */
	elmts, err := h5eaHdrAllocElmts(hdr, hdr.dblkPageNelmts)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for data block page element buffer")
	}
	dblkPage.elmts = elmts

	return dblkPage, nil
}

// h5eaDblkPageProtect is H5EA__dblk_page_protect: convenience wrapper around
// protecting extensible array data block page.
func h5eaDblkPageProtect(hdr *h5eaHdr, dblkPageAddr haddr) (*h5eaDblkPage, error) {
	/* Sanity check */
	if !addrDefined(dblkPageAddr) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "undefined extensible array data block page address")
	}

	/* Set up user data */
	udata := h5eaDblkPageCacheUd{hdr: hdr, dblkPageAddr: dblkPageAddr}

	/* Protect the data block page */
	v, err := hdr.f.cached("H5EA_DBLK_PAGE", dblkPageAddr, func() (any, error) {
		return h5eaCacheDblkPageLoad(&udata)
	})
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array data block page, address = %d", dblkPageAddr)
	}
	dblkPage, ok := v.(*h5eaDblkPage)
	if !ok || dblkPage.hdr != hdr {
		return nil, errorf(H5E_EARRAY, H5E_CANTPROTECT, "cached object at address %d is not this extensible array's data block page", dblkPageAddr)
	}
	return dblkPage, nil
}
