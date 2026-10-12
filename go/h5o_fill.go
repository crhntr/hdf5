package hdf5

// Translated from src/H5Ofill.c (decode only): the old (H5O_FILL_ID) and
// new (H5O_FILL_NEW_ID) fill value messages. Both decode into h5oFill.

import "github.com/crhntr/hdf5/go/h5"

// Fill value message versions (H5Oprivate.h).
const (
	/* Initial version of the fill value message */
	h5oFillVersion1 = 1
	/* Revised version of the fill value message */
	h5oFillVersion2 = 2
	/* Version of the fill value message which only stores the fill value
	 * when it's defined, and packs the other information into a single byte. */
	h5oFillVersion3 = 3
	/* The latest version of the format. */
	h5oFillVersionLatest = h5oFillVersion3
)

/* Masks, shift values & flags for fill value message */
const (
	h5oFillMaskAllocTime      = 0x03
	h5oFillShiftAllocTime     = 0
	h5oFillMaskFillTime       = 0x03
	h5oFillShiftFillTime      = 2
	h5oFillFlagUndefinedValue = 0x10
	h5oFillFlagHaveValue      = 0x20
	h5oFillFlagsAll           = h5oFillMaskAllocTime | (h5oFillMaskFillTime << h5oFillShiftFillTime) | h5oFillFlagUndefinedValue | h5oFillFlagHaveValue
)

// h5oFillNewDecode is H5O__fill_new_decode: decode a new fill value message.
// The new fill value message is fill value plus space allocation time and
// fill value writing time and whether fill value is defined.
func h5oFillNewDecode(f *File, p *image) (*h5oFill, error) {
	fill := &h5oFill{}

	/* Version */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	fill.version = p.uint8()
	if fill.version < h5oFillVersion1 || fill.version > h5oFillVersionLatest {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for fill value message")
	}

	/* Decode each version */
	if fill.version < h5oFillVersion3 {
		/* Versions 1 & 2 */

		/* Buffer size check for the next three bytes */
		if p.overflow(3) {
			return nil, errDecodeOverflow()
		}

		/* Space allocation time */
		fill.allocTime = p.uint8()

		/* Fill value write time */
		fill.fillTime = p.uint8()

		/* Whether fill value is defined */
		fill.fillDefined = p.uint8() != 0

		/* Only decode fill value information if one is defined */
		if fill.fillDefined {
			if p.overflow(4) {
				return nil, errDecodeOverflow()
			}
			fill.size = int64(p.int32())
			if fill.size > 0 {
				if fill.size > int64(p.remaining()) || p.overflow(int(fill.size)) {
					return nil, errDecodeOverflow()
				}
				fill.buf = append([]byte(nil), p.bytes(int(fill.size))...)
			}
		} else {
			fill.size = -1
		}
	} else {
		/* Version 3 */

		/* Flags */
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		flags := p.uint8()

		/* Check for unknown flags */
		if flags&^h5oFillFlagsAll != 0 {
			return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "unknown flag for fill value message")
		}

		/* Space allocation time */
		fill.allocTime = (flags >> h5oFillShiftAllocTime) & h5oFillMaskAllocTime

		/* Fill value write time */
		fill.fillTime = (flags >> h5oFillShiftFillTime) & h5oFillMaskFillTime

		/* Check for undefined fill value */
		if flags&h5oFillFlagUndefinedValue != 0 {
			if flags&h5oFillFlagHaveValue != 0 {
				return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "have value and undefined value flags both set")
			}

			/* Set value for "undefined" fill value */
			fill.size = -1
		} else if flags&h5oFillFlagHaveValue != 0 {
			/* Fill value size */
			if p.overflow(4) {
				return nil, errDecodeOverflow()
			}
			fill.size = int64(p.uint32())

			/* Fill value */
			if fill.size > int64(p.remaining()) || p.overflow(int(fill.size)) {
				return nil, errDecodeOverflow()
			}
			fill.buf = append([]byte(nil), p.bytes(int(fill.size))...)

			/* Set the "defined" flag */
			fill.fillDefined = true
		} else {
			/* Set the "defined" flag */
			fill.fillDefined = true
		}
	}
	return fill, nil
}

// h5oFillOldDecode is H5O__fill_old_decode: decode an old fill value
// message. The size of the fill value is checked against the datatype
// message of the same object header, if there is one (oh may be nil when
// the message does not come from an object header).
func (f *File) h5oFillOldDecode(oh *h5o, p *image) (*h5oFill, error) {
	fill := &h5oFill{}

	/* Set non-zero default fields */
	fill.version = h5oFillVersion2
	fill.allocTime = h5dAllocTimeLate
	fill.fillTime = h5dFillTimeIfset

	/* Fill value size */
	if p.overflow(4) {
		return nil, errDecodeOverflow()
	}
	fill.size = int64(p.uint32())

	/* Only decode the fill value itself if there is one */
	if fill.size > 0 {
		/* Ensure that fill size doesn't exceed buffer size, due to possible data corruption */
		if fill.size > int64(p.remaining()) || p.overflow(int(fill.size)) {
			return nil, errDecodeOverflow()
		}

		/* Get the datatype message  */
		if oh != nil && f.msgExists(oh, h5oDtypeID) {
			v, err := f.msgRead(oh, h5oDtypeID)
			if err != nil {
				return nil, wrapf(err, H5E_SYM, H5E_CANTGET, "can't read DTYPE message")
			}
			dt := v.(*h5.Datatype)

			/* Verify size */
			if uint64(fill.size) != dt.Size {
				return nil, errorf(H5E_SYM, H5E_CANTGET, "inconsistent fill value size")
			}
		}

		fill.buf = append([]byte(nil), p.bytes(int(fill.size))...)
		fill.fillDefined = true
	} else {
		fill.size = -1
	}
	return fill, nil
}
