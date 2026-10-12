package hdf5

// Translated from src/H5Olink.c (decode only): the link message.

import "github.com/HDFGroup/hdf5/go/h5"

/* Current version of link information */
const h5oLinkVersion = 1

/* Flags for link flag encoding */
const (
	h5oLinkNameSize      = 0x03 /* 2-bit field for size of name length */
	h5oLinkStoreCorder   = 0x04 /* Whether to store creation index */
	h5oLinkStoreLinkType = 0x08 /* Whether to store non-default link type */
	h5oLinkStoreNameCset = 0x10 /* Whether to store non-default name character set */
	h5oLinkAllFlags      = h5oLinkNameSize | h5oLinkStoreCorder | h5oLinkStoreLinkType | h5oLinkStoreNameCset
)

// H5L_type_t limits (H5Lpublic.h).
const (
	h5lTypeUdMin = 64  /* H5L_TYPE_UD_MIN: first user-defined link type */
	h5lTypeMax   = 255 /* H5L_TYPE_MAX: maximum link type id */
)

// h5oLinkDecode is H5O__link_decode: decode a link message.
func h5oLinkDecode(f *File, p *image) (*h5oLink, error) {
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	if p.uint8() != h5oLinkVersion {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Allocate space for message */
	lnk := &h5oLink{}

	/* Get the encoding flags for the link */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	linkFlags := p.uint8()
	if linkFlags&^h5oLinkAllFlags != 0 {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad flag value for message")
	}

	/* Check for non-default link type */
	if linkFlags&h5oLinkStoreLinkType != 0 {
		/* Get the type of the link */
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		lnk.typ = h5.LinkType(p.uint8())
		if lnk.typ < h5.LinkHard || lnk.typ > h5lTypeMax {
			return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad link type")
		}
	} else {
		lnk.typ = h5.LinkHard
	}

	/* Get the link creation time from the file */
	if linkFlags&h5oLinkStoreCorder != 0 {
		if p.overflow(8) {
			return nil, errDecodeOverflow()
		}
		lnk.corder = int64(p.uint64())
		lnk.corderValid = true
	} else {
		lnk.corder = 0
		lnk.corderValid = false
	}

	/* Check for non-default name character set */
	if linkFlags&h5oLinkStoreNameCset != 0 {
		/* Get the link name's character set */
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		lnk.cset = h5.Cset(p.uint8())
		if lnk.cset < h5.CsetASCII || lnk.cset > h5.CsetUTF8 {
			return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad cset type")
		}
	} else {
		lnk.cset = h5.CsetASCII
	}

	/* Get the length of the link's name */
	var n uint64
	switch linkFlags & h5oLinkNameSize {
	case 0: /* 1 byte size */
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		n = uint64(p.uint8())
	case 1: /* 2 byte size */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		n = uint64(p.uint16())
	case 2: /* 4 byte size */
		if p.overflow(4) {
			return nil, errDecodeOverflow()
		}
		n = uint64(p.uint32())
	case 3: /* 8 byte size */
		if p.overflow(8) {
			return nil, errDecodeOverflow()
		}
		n = p.uint64()
	default:
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "no appropriate size for name length")
	}
	if n == 0 {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "invalid name length")
	}

	/* Get the link's name */
	if n > uint64(p.remaining()) || p.overflow(int(n)) {
		return nil, errDecodeOverflow()
	}
	lnk.name = string(p.bytes(int(n)))

	/* Get the appropriate information for each type of link */
	switch lnk.typ {
	case h5.LinkHard:
		/* Get the address of the object the link points to */
		if p.overflow(int(f.sizeofAddr)) {
			return nil, errDecodeOverflow()
		}
		lnk.addr = p.addr()

	case h5.LinkSoft:
		/* Get the link value */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		n := int(p.uint16())
		if n == 0 {
			return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "invalid link length")
		}
		if p.overflow(n) {
			return nil, errDecodeOverflow()
		}
		lnk.softName = string(p.bytes(n))

	/* User-defined links */
	default:
		if lnk.typ < h5lTypeUdMin || lnk.typ > h5lTypeMax {
			return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "unknown link type")
		}

		/* A UD link.  Get the user-supplied data */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		n := int(p.uint16())
		if lnk.typ == h5.LinkExternal && n < 3 {
			return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "external link information length < 3")
		}
		lnk.udSize = n
		if n > 0 {
			if p.overflow(n) {
				return nil, errDecodeOverflow()
			}
			lnk.udData = append([]byte(nil), p.bytes(n)...)
		} else {
			lnk.udData = nil
		}
	}
	return lnk, nil
}
