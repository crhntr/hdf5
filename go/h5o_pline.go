package hdf5

// Translated from src/H5Opline.c (decode only): the filter pipeline
// message.

import "bytes"

// Filter pipeline message versions (H5Oprivate.h).
const (
	/* Initial version of the I/O pipeline message */
	h5oPlineVersion1 = 1

	/* Version of the I/O pipeline message that doesn't store the filter
	 * name for library-defined filters and packs the filter information */
	h5oPlineVersion2 = 2

	/* The latest version of the format. */
	h5oPlineVersionLatest = h5oPlineVersion2
)

// h5zMaxNfilters is H5Z_MAX_NFILTERS: the maximum number of filters allowed
// in a pipeline.
const h5zMaxNfilters = 32

// h5oPlineDecode is H5O__pline_decode: decode a filter pipeline message.
func h5oPlineDecode(f *File, p *image) (*h5oPline, error) {
	/* Allocate space for I/O pipeline message */
	pline := &h5oPline{}

	/* Version */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	pline.version = p.uint8()
	if pline.version < h5oPlineVersion1 || pline.version > h5oPlineVersionLatest {
		return nil, errorf(H5E_PLINE, H5E_CANTLOAD, "bad version number for filter pipeline message")
	}

	/* Number of filters */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	pline.nused = int(p.uint8())
	if pline.nused > h5zMaxNfilters {
		/* Reset the number of filters used to avoid array traversal in error
		 * handling code.
		 */
		pline.nused = 0
		return nil, errorf(H5E_PLINE, H5E_CANTLOAD, "filter pipeline message has too many filters")
	}

	/* Reserved */
	if pline.version == h5oPlineVersion1 {
		if p.overflow(6) {
			return nil, errDecodeOverflow()
		}
		p.skip(6)
	}

	/* Allocate array for filters */
	pline.filter = make([]h5zFilterInfo, pline.nused)

	/* Decode filters */
	for i := range pline.filter {
		filter := &pline.filter[i]
		var nameLength int /* Length of filter name */

		/* Filter ID */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		filter.id = int32(p.uint16())

		/* Length of filter name */
		if pline.version > h5oPlineVersion1 && filter.id < h5zFilterReserved {
			nameLength = 0
		} else {
			if p.overflow(2) {
				return nil, errDecodeOverflow()
			}
			nameLength = int(p.uint16())
			if pline.version == h5oPlineVersion1 && nameLength%8 != 0 {
				return nil, errorf(H5E_PLINE, H5E_CANTLOAD, "filter name length is not a multiple of eight")
			}
		}

		/* Filter flags */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		filter.flags = p.uint16()

		/* Number of filter parameters ("client data elements") */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		cdNelmts := int(p.uint16())

		/* Filter name, if there is one */
		if nameLength != 0 {
			/* Determine actual name length (without padding, but with null terminator) */
			rest := p.rest()
			actualNameLength := bytes.IndexByte(rest, 0)
			if actualNameLength < 0 {
				return nil, errorf(H5E_OHDR, H5E_NOSPACE, "filter name not null terminated")
			}
			filter.name = string(rest[:actualNameLength])
			if p.overflow(nameLength) {
				return nil, errDecodeOverflow()
			}
			p.skip(nameLength)
		}

		/* Filter parameters */
		if cdNelmts != 0 {
			/* Allocate space for the client data elements */
			if p.overflow(4 * cdNelmts) {
				return nil, errDecodeOverflow()
			}
			filter.cdValues = make([]uint32, cdNelmts)

			/* Read the client data values and the padding */
			for j := range filter.cdValues {
				if p.overflow(4) {
					return nil, errDecodeOverflow()
				}
				filter.cdValues[j] = p.uint32()
			}
			if pline.version == h5oPlineVersion1 {
				if cdNelmts%2 != 0 {
					if p.overflow(4) {
						return nil, errDecodeOverflow()
					}
					p.skip(4) /* padding */
				}
			}
		}
	}
	return pline, nil
}
