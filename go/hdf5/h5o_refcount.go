package hdf5

// Translated from src/H5Orefcount.c (decode only): the object reference
// count message.

// h5oRefcountVersion is H5O_REFCOUNT_VERSION: the current version of the
// message.
const h5oRefcountVersion = 0

// h5oRefcountDecode is H5O__refcount_decode: decode a reference count
// message.
func h5oRefcountDecode(f *File, p *image) (h5oRefcount, error) {
	/* Version of message */
	if p.overflow(1) {
		return 0, errDecodeOverflow()
	}
	if p.uint8() != h5oRefcountVersion {
		return 0, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Get reference count for object */
	if p.overflow(4) {
		return 0, errDecodeOverflow()
	}
	return h5oRefcount(p.uint32()), nil
}
