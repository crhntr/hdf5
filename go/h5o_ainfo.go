package hdf5

// Translated from src/H5Oainfo.c (decode only): the attribute info message.

/* This is the initial version of the message */
const h5oAinfoVersion = 0

/* Flags for attribute info index flag encoding */
const (
	h5oAinfoTrackCorder = 0x01
	h5oAinfoIndexCorder = 0x02
	h5oAinfoAllFlags    = h5oAinfoTrackCorder | h5oAinfoIndexCorder
)

// h5oAinfoDecode is H5O__ainfo_decode: decode an attribute info message.
func h5oAinfoDecode(f *File, p *image) (*h5oAinfo, error) {
	sizeofAddr := int(f.sizeofAddr)

	/* Version of message */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	if p.uint8() != h5oAinfoVersion {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Allocate space for message */
	ainfo := &h5oAinfo{}

	/* Get the flags for the message */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	flags := p.uint8()
	if flags&^h5oAinfoAllFlags != 0 {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad flag value for message")
	}
	ainfo.trackCorder = flags&h5oAinfoTrackCorder != 0
	ainfo.indexCorder = flags&h5oAinfoIndexCorder != 0

	/* Set the number of attributes on the object to an invalid value, so we query it later */
	ainfo.nattrs = ^uint64(0)

	/* Max. creation order value for the object */
	if ainfo.trackCorder {
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		ainfo.maxCrtIdx = p.uint16()
	} else {
		ainfo.maxCrtIdx = h5oMaxCrtOrderIdx
	}

	/* Address of fractal heap to store "dense" attributes */
	if p.overflow(sizeofAddr) {
		return nil, errDecodeOverflow()
	}
	ainfo.fheapAddr = p.addr()

	/* Address of v2 B-tree to index names of attributes (names are always indexed) */
	if p.overflow(sizeofAddr) {
		return nil, errDecodeOverflow()
	}
	ainfo.nameBt2Addr = p.addr()

	/* Address of v2 B-tree to index creation order of links, if there is one */
	if ainfo.indexCorder {
		if p.overflow(sizeofAddr) {
			return nil, errDecodeOverflow()
		}
		ainfo.corderBt2Addr = p.addr()
	} else {
		ainfo.corderBt2Addr = haddrUndef
	}
	return ainfo, nil
}
