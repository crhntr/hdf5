package hdf5

// Translated from src/H5Oattr.c (decode only): the attribute message,
// decoded into h5oAttr (the parts of H5A_t / H5A_shared_t that the message
// describes).

import (
	"bytes"
	"math/bits"

	"github.com/HDFGroup/hdf5/go/h5"
)

// Attribute message versions (H5Apkg.h).
const (
	/* This is the initial version, which does not have support for shared datatypes */
	h5oAttrVersion1 = 1

	/* This version allows support for shared datatypes & dataspaces by adding a
	 *      'flag' byte indicating when those components are shared.  This version
	 *      also dropped the alignment on all the components.
	 */
	h5oAttrVersion2 = 2

	/* Add support for different character encodings of attribute names */
	h5oAttrVersion3 = 3

	/* The latest version of the format. */
	h5oAttrVersionLatest = h5oAttrVersion3
)

/* Flags for attribute flag encoding */
const (
	h5oAttrFlagTypeShared  = 0x01
	h5oAttrFlagSpaceShared = 0x02
	h5oAttrFlagAll         = 0x03
)

// h5oAttrDecode is H5O__attr_decode: decode an attribute message. The
// datatype and dataspace are decoded through their (shared) message
// decoders; when they are shared, dtShared / dsShared record the shared
// message information.
func (f *File) h5oAttrDecode(openOh *h5o, p *image) (*h5oAttr, error) {
	attr := &h5oAttr{}
	var flags uint8 /* Attribute flags */

	/* Version number */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	attr.version = p.uint8()
	if attr.version < h5oAttrVersion1 || attr.version > h5oAttrVersionLatest {
		return nil, errorf(H5E_ATTR, H5E_CANTLOAD, "bad version number for attribute message")
	}

	/* Get the flags byte if we have a later version of the attribute */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	if attr.version >= h5oAttrVersion2 {
		flags = p.uint8()

		/* Check for unknown flag */
		if flags&^h5oAttrFlagAll != 0 {
			return nil, errorf(H5E_ATTR, H5E_CANTLOAD, "unknown flag for attribute message")
		}
	} else {
		p.skip(1) /* Byte is unused when version < 2 */
	}

	/* Decode the sizes of the parts of the attribute.  The sizes stored in
	 * the file are exact but the parts are aligned on 8-byte boundaries.
	 */
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	nameLen := uint64(p.uint16()) /* Including null */
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	dtSize := uint64(p.uint16())
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	dsSize := uint64(p.uint16())

	/* Decode the character encoding for the name for versions 3 or later,
	 * as well as some reserved bytes.
	 */
	if attr.version >= h5oAttrVersion3 {
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		attr.cset = h5.Cset(p.uint8())
	}

	/* Decode and store the name */
	if p.overflow(int(nameLen)) {
		return nil, errDecodeOverflow()
	}
	if nameLen == 0 {
		/* (H5MM_strndup(p, name_len - 1) with name_len == 0) */
		return nil, errorf(H5E_ATTR, H5E_CANTDECODE, "attribute name has different length than stored length")
	}
	rawName := p.rest()[:nameLen-1]
	if i := bytes.IndexByte(rawName, 0); i >= 0 {
		rawName = rawName[:i]
	}
	attr.name = string(rawName)

	/* Make an attempt to detect corrupted name or name length - HDFFV-10588 */
	if nameLen != uint64(len(attr.name))+1 {
		return nil, errorf(H5E_ATTR, H5E_CANTDECODE, "attribute name has different length than stored length")
	}

	/* Determine pointer movement and check if it's valid */
	delta := nameLen
	if attr.version < h5oAttrVersion2 {
		delta = h5oAlignOld(nameLen)
	}
	if delta > uint64(p.remaining()) || p.overflow(int(delta)) {
		return nil, errDecodeOverflow()
	}
	p.skip(int(delta))

	/* Decode the attribute's datatype */
	if dtSize > uint64(p.remaining()) || p.overflow(int(dtSize)) {
		return nil, errDecodeOverflow()
	}
	{
		q := f.newImage(p.rest()[:dtSize])
		if flags&h5oAttrFlagTypeShared != 0 {
			sh, err := h5oSharedDecodeHeader(f, f.newImage(p.rest()[:dtSize]), h5oDtypeID)
			if err != nil {
				return nil, wrapf(err, H5E_ATTR, H5E_CANTDECODE, "can't decode attribute datatype")
			}
			attr.dtShared = sh
		}
		dtFlags := uint8(0)
		if flags&h5oAttrFlagTypeShared != 0 {
			dtFlags = h5oMsgFlagShared
		}
		v, err := f.h5oSharedDecodeTmpl(openOh, dtFlags, q, h5oDtypeID,
			func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oDtypeDecode(f, p) })
		if err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTDECODE, "can't decode attribute datatype")
		}
		dt, ok := v.(*h5.Datatype)
		if !ok || dt == nil {
			return nil, errorf(H5E_ATTR, H5E_CANTDECODE, "can't decode attribute datatype")
		}
		attr.dt = dt
	}

	/* Determine pointer movement and check if it's valid */
	delta = dtSize
	if attr.version < h5oAttrVersion2 {
		delta = h5oAlignOld(dtSize)
	}
	if delta > uint64(p.remaining()) || p.overflow(int(delta)) {
		return nil, errDecodeOverflow()
	}
	p.skip(int(delta))

	/* Decode the attribute dataspace.  It can be shared in versions >= 3
	 * What's actually shared, though, is only the extent.
	 */

	/* Decode attribute's dataspace extent */
	if dsSize > uint64(p.remaining()) || p.overflow(int(dsSize)) {
		return nil, errDecodeOverflow()
	}
	{
		q := f.newImage(p.rest()[:dsSize])
		dsFlags := uint8(0)
		if flags&h5oAttrFlagSpaceShared != 0 {
			sh, err := h5oSharedDecodeHeader(f, f.newImage(p.rest()[:dsSize]), h5oSdspaceID)
			if err != nil {
				return nil, wrapf(err, H5E_ATTR, H5E_CANTDECODE, "can't decode attribute dataspace")
			}
			attr.dsShared = sh
			dsFlags = h5oMsgFlagShared
		}
		v, err := f.h5oSharedDecodeTmpl(openOh, dsFlags, q, h5oSdspaceID,
			func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oSdspaceDecode(f, p) })
		if err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTDECODE, "can't decode attribute dataspace")
		}
		extent, ok := v.(*h5sExtent)
		if !ok || extent == nil {
			return nil, errorf(H5E_ATTR, H5E_CANTDECODE, "can't decode attribute dataspace")
		}
		/* Copy the extent information to the dataspace */
		/* (Default to entire dataspace being selected) */
		attr.ds = extent
	}

	/* Determine pointer movement and check if it's valid */
	delta = dsSize
	if attr.version < h5oAttrVersion2 {
		delta = h5oAlignOld(dsSize)
	}
	if delta > uint64(p.remaining()) || p.overflow(int(delta)) {
		return nil, errDecodeOverflow()
	}
	p.skip(int(delta))

	/* Get the datatype & dataspace sizes */
	dtElemSize := attr.dt.Size
	if 0 == dtElemSize {
		return nil, errorf(H5E_ATTR, H5E_CANTGET, "unable to get datatype size")
	}
	dsNelem := attr.ds.nelem

	/* Compute the size of the data */
	/* Check if multiplication has overflown */
	hi, lo := bits.Mul64(dsNelem, dtElemSize)
	if hi != 0 {
		return nil, errorf(H5E_RESOURCE, H5E_OVERFLOW, "data size exceeds addressable range")
	}
	attr.dataSize = lo

	/* Get the data */
	if attr.dataSize != 0 {
		/* Ensure that data size doesn't exceed buffer size, in case of
		 * it's being corrupted in the file
		 */
		if attr.dataSize > uint64(p.remaining()) || p.overflow(int(attr.dataSize)) {
			return nil, errDecodeOverflow()
		}
		attr.data = append([]byte(nil), p.bytes(int(attr.dataSize))...)
	}
	return attr, nil
}

// h5oAttrSetCrtIndex is H5O__attr_set_crt_index: set the creation index of
// an attribute message (returns a copy; decoded messages are not modified
// in place).
func h5oAttrSetCrtIndex(native any, crtIdx uint16) any {
	attr, ok := native.(*h5oAttr)
	if !ok {
		return native
	}
	a := *attr

	/* Set the creation index */
	a.crtIdx = crtIdx
	return &a
}
