package hdf5

// Translated from src/H5Olinfo.c (decode only): the link info message.

/* This is the initial version of the message */
const h5oLinfoVersion = 0

/* Flags for link info index flag encoding */
const (
	h5oLinfoTrackCorder = 0x01
	h5oLinfoIndexCorder = 0x02
	h5oLinfoAllFlags    = h5oLinfoTrackCorder | h5oLinfoIndexCorder
)

// h5oLinfoDecode is H5O__linfo_decode: decode a link info message.
func h5oLinfoDecode(f *File, p *image) (*h5oLinfo, error) {
	addrSize := int(f.sizeofAddr)

	/* Check input buffer before decoding version and index flags */
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}

	/* Version of message */
	if p.uint8() != h5oLinfoVersion {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Allocate space for message */
	linfo := &h5oLinfo{}

	/* Get the index flags for the group */
	indexFlags := p.uint8()
	if indexFlags&^h5oLinfoAllFlags != 0 {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad flag value for message")
	}
	linfo.trackCorder = indexFlags&h5oLinfoTrackCorder != 0
	linfo.indexCorder = indexFlags&h5oLinfoIndexCorder != 0

	/* Set the number of links in the group to an invalid value, so we query it later */
	linfo.nlinks = ^uint64(0)

	/* Max. link creation order value for the group, if tracked */
	if linfo.trackCorder {
		if p.overflow(8) {
			return nil, errDecodeOverflow()
		}
		linfo.maxCorder = int64(p.uint64())
		if linfo.maxCorder < 0 {
			return nil, errorf(H5E_OHDR, H5E_BADVALUE, "invalid max creation order value for message: %d", linfo.maxCorder)
		}
	} else {
		linfo.maxCorder = 0
	}

	/* Check input buffer before decoding the next two addresses */
	if p.overflow(addrSize + addrSize) {
		return nil, errDecodeOverflow()
	}

	/* Address of fractal heap to store "dense" links */
	linfo.fheapAddr = p.addr()

	/* Address of v2 B-tree to index names of links (names are always indexed) */
	linfo.nameBt2Addr = p.addr()

	/* Address of v2 B-tree to index creation order of links, if there is one */
	if linfo.indexCorder {
		if p.overflow(addrSize) {
			return nil, errDecodeOverflow()
		}
		linfo.corderBt2Addr = p.addr()
	} else {
		linfo.corderBt2Addr = haddrUndef
	}
	return linfo, nil
}
