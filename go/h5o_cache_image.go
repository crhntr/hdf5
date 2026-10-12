package hdf5

// Translated from src/H5Ocache_image.c (decode only): the metadata cache
// image message (superblock extension).

// h5oMdciVersion0 is H5O_MDCI_VERSION_0.
const h5oMdciVersion0 = 0

// h5oMdciDecode is H5O__mdci_decode: decode a metadata cache image message.
func h5oMdciDecode(f *File, p *image) (*h5oMdci, error) {
	/* Version of message */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	if p.uint8() != h5oMdciVersion0 {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Allocate space for message */
	mesg := &h5oMdci{}

	if p.overflow(int(f.sizeofAddr)) {
		return nil, errDecodeOverflow()
	}
	mesg.addr = p.addr()
	if p.overflow(int(f.sizeofSize)) {
		return nil, errDecodeOverflow()
	}
	mesg.size = p.length()
	return mesg, nil
}
