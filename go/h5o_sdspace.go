package hdf5

// Translated from src/H5Osdspace.c (decode only): the dataspace message.

import "github.com/crhntr/hdf5/go/h5"

// Dataspace message versions and flags (H5Spkg.h).
const (
	/* Initial version of the dataspace information */
	h5oSdspaceVersion1 = 1

	/* This version adds support for "null" dataspaces, encodes the type of the
	 *      dataspace in the message and eliminated the rest of the "reserved"
	 *      bytes.
	 */
	h5oSdspaceVersion2 = 2

	/* The latest version of the format. */
	h5oSdspaceVersionLatest = h5oSdspaceVersion2

	/* Flags to indicate special dataspace features are active */
	h5sValidMax  = 0x01
	h5sValidPerm = 0x02
)

// h5oSdspaceDecode is H5O__sdspace_decode: decode a dataspace message
// (versions 1 and 2) and compute the number of elements in the extent.
func h5oSdspaceDecode(f *File, p *image) (*h5sExtent, error) {
	sdim := &h5sExtent{}

	/* Check version */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	version := p.uint8()
	if version < h5oSdspaceVersion1 || version > h5oSdspaceVersion2 {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "wrong version number in dataspace message")
	}
	sdim.version = version

	/* Get rank */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	sdim.rank = uint32(p.uint8())
	if sdim.rank > h5sMaxRank {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "simple dataspace dimensionality is too large")
	}

	/* Get dataspace flags for later */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	flags := p.uint8()

	/* Get or determine the type of the extent */
	if version >= h5oSdspaceVersion2 {
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		sdim.typ = h5.SpaceClass(p.uint8())
		if sdim.typ != h5.SpaceSimple && sdim.rank > 0 {
			return nil, errorf(H5E_OHDR, H5E_BADVALUE, "invalid rank for scalar or NULL dataspace")
		}
	} else {
		/* Set the dataspace type to be simple or scalar as appropriate
		 * (version 1 does not allow H5S_NULL)
		 */
		if sdim.rank > 0 {
			sdim.typ = h5.SpaceSimple
		} else {
			sdim.typ = h5.SpaceScalar
		}

		/* Increment past reserved byte */
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		p.skip(1)
	}

	/* Version 1 has 4 reserved bytes */
	if version == h5oSdspaceVersion1 {
		if p.overflow(4) {
			return nil, errDecodeOverflow()
		}
		p.skip(4)
	}

	/* Decode dimension sizes */
	if sdim.rank > 0 {
		/* Check that we have space to decode sdim->rank values */
		if p.overflow(int(f.sizeofSize) * int(sdim.rank)) {
			return nil, errDecodeOverflow()
		}

		/* Sizes */
		sdim.size = make([]uint64, sdim.rank)
		for i := range sdim.size {
			sdim.size[i] = p.length()
		}

		if flags&h5sValidMax != 0 {
			/* Check that we have space to decode sdim->rank values */
			if p.overflow(int(f.sizeofSize) * int(sdim.rank)) {
				return nil, errDecodeOverflow()
			}

			/* Max sizes */
			sdim.max = make([]uint64, sdim.rank)
			for i := range sdim.max {
				sdim.max[i] = p.length()
				if sdim.size[i] > sdim.max[i] {
					return nil, errorf(H5E_OHDR, H5E_BADVALUE, "dataspace dim %d size of %d is greater than maxdim size of %d", i, sdim.size[i], sdim.max[i])
				}
			}
		}

		/* NOTE: The version 1 permutation indexes were never implemented so
		 *       there is nothing to decode.
		 */
	}

	/* Compute the number of elements in the extent */
	if sdim.typ == h5.SpaceNull {
		sdim.nelem = 0
	} else {
		sdim.nelem = 1
		for _, d := range sdim.size {
			sdim.nelem *= d
		}
	}
	return sdim, nil
}
