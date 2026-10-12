package hdf5

// Translated from src/H5FAhdr.c (read side only: alloc, init, protect).
// Array header routines for Fixed Arrays.

// h5faHdrAlloc is H5FA__hdr_alloc: allocate shared Fixed Array header.
func h5faHdrAlloc(f *File) *h5faHdr {
	/* Allocate space for the shared information */
	hdr := &h5faHdr{}

	/* Set non-zero internal fields */
	hdr.addr = haddrUndef

	/* Set the internal parameters for the array */
	hdr.f = f
	hdr.sizeofAddr = uint64(f.sizeofAddr)
	hdr.sizeofSize = uint64(f.sizeofSize)

	return hdr
}

// h5faHdrInit is H5FA__hdr_init: initialize shared fixed array header. The
// class and the client callback context (the result of the class'
// crt_context callback in C) are stored in the header.
func h5faHdrInit(hdr *h5faHdr, cls *h5faClass, ctx any) error {
	/* Set size of header on disk (locally and in statistics) */
	hdr.size = h5faHeaderSizeHdr(hdr)
	hdr.hdrSize = hdr.size

	/* Set number of elements for Fixed Array in statistics */
	/* (hdr.nelmts holds both cparam.nelmts and stats.nelmts) */

	/* Create the callback context, if there's one */
	hdr.cls = cls
	hdr.cbCtx = ctx

	return nil
}

// h5faHdrProtect is H5FA__hdr_protect: convenience wrapper around
// protecting fixed array header.
func h5faHdrProtect(f *File, faAddr haddr, cls *h5faClass, ctx any) (*h5faHdr, error) {
	/* Sanity check */
	if !addrDefined(faAddr) {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "undefined fixed array header address")
	}

	/* Protect the header */
	v, err := f.cached("H5FA_HDR", faAddr, func() (any, error) {
		return h5faCacheHdrLoad(f, faAddr, cls, ctx)
	})
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTPROTECT, "unable to protect fixed array header, address = %d", faAddr)
	}
	hdr, ok := v.(*h5faHdr)
	if !ok {
		return nil, errorf(H5E_FARRAY, H5E_CANTPROTECT, "cached object at address %d is not a fixed array header", faAddr)
	}

	/* (the class is looked up from the id in the file by the C library;
	 *  here the caller supplies it, so check that it matches) */
	if cls == nil || hdr.clsID != cls.id {
		return nil, errorf(H5E_FARRAY, H5E_BADTYPE, "incorrect fixed array class")
	}
	return hdr, nil
}
