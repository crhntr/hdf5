package hdf5

// Translated from src/H5Odtype.c (decode path only).

import (
	"bytes"
	"math/bits"

	"github.com/HDFGroup/hdf5/go/h5"
)

// Datatype message versions (H5Tpkg.h).
const (
	/* This is the version to create all datatypes which don't contain
	 * array datatypes (atomic types, compound datatypes without array fields,
	 * vlen sequences of objects which aren't arrays, etc.) */
	h5oDtypeVersion1 = 1

	/* This is the version to create all datatypes which contain H5T_ARRAY
	 * class objects (array definitely, potentially compound & vlen sequences
	 * also) */
	h5oDtypeVersion2 = 2

	/* This is the version to create all datatypes which contain packed
	 * encodings: no padding of names, smaller compound member offsets,
	 * no array permutations, VAX byte order */
	h5oDtypeVersion3 = 3

	/* This is the version that adds support for new reference types */
	h5oDtypeVersion4 = 4

	/* The latest version of the format.  Look through the 'encode helper'
	 * routine and 'size' callback for places to change when updating this. */
	h5oDtypeVersionLatest = h5oDtypeVersion4
)

// Input/output flags for decode functions (H5Opkg.h).
const (
	h5oDecodeioNochange   = 0x01 /* IN: do not modify values */
	h5oDecodeioDirty      = 0x02 /* OUT: message has been changed */
	h5oDecodeioRficUnubnt = 0x04 /* IN: Relax file integrity checks for unusual numbers of unused bits in numeric datatypes */
)

// H5T_OPAQUE_TAG_MAX
const h5tOpaqueTagMax = 256

// H5R_ENCODE_VERSION: version for encoding (revised) references.
const h5rEncodeVersion = 0x1

// H5R_type_t limits.
const (
	h5rBadtype = -1
	h5rMaxtype = 5
)

// dtypeLimitEncSize is H5VM_limit_enc_size: the number of bytes needed to
// encode values within a range from 0 to limit.
func dtypeLimitEncSize(limit uint64) int {
	/* H5VM_log2_gen(limit) / 8 + 1 */
	log2 := 0
	if limit > 0 {
		log2 = bits.Len64(limit) - 1
	}
	return log2/8 + 1
}

// h5oDtypeCheckVersion is the non-strict H5O_DTYPE_CHECK_VERSION macro: if
// the version is too low and we are allowed to change the message, upgrade
// it and mark the object header as dirty.
func h5oDtypeCheckVersion(dt *h5.Datatype, vers *uint8, minVers uint8, ioflags *uint) {
	if *vers < minVers && *ioflags&h5oDecodeioNochange == 0 {
		*vers = minVers
		h5tUpgradeVersion(dt, minVers)
		*ioflags |= h5oDecodeioDirty
	}
}

// h5tUpgradeVersion is H5T__upgrade_version: upgrade the version of dt and
// recursively of its compound members and parent datatypes (H5T__visit
// with H5T_VISIT_SIMPLE | H5T_VISIT_COMPLEX_LAST: the components first).
func h5tUpgradeVersion(dt *h5.Datatype, version uint8) {
	if dt == nil {
		return
	}
	switch dt.Class {
	case h5.ClassCompound:
		for i := range dt.Members {
			h5tUpgradeVersion(dt.Members[i].Type, version)
		}
	case h5.ClassArray, h5.ClassEnum, h5.ClassVlen:
		h5tUpgradeVersion(dt.Base, version)
	}
	h5tUpgradeVersionCb(dt, version)
}

// h5tUpgradeVersionCb is H5T__upgrade_version_cb.
func h5tUpgradeVersionCb(dt *h5.Datatype, version uint8) {
	/* Special behavior for each type of datatype */
	switch dt.Class {
	case h5.ClassCompound, h5.ClassArray, h5.ClassEnum:
		/* These types benefit from "upgrading" their version */
		if version > dt.Version {
			dt.Version = version
		}
	case h5.ClassVlen:
		if dt.Base != nil && dt.Base.Version > dt.Version {
			dt.Version = dt.Base.Version
		}
	}
}

// h5tIsNumericWithUnusualUnusedBits is H5T_is_numeric_with_unusual_unused_bits:
// detect if a datatype is a numeric datatype (int, float, or bitfield) with
// an unusual # of unused bits.  This means that the precision (i.e. the #
// of bits used) is less than the size of the datatype, at power-of-two
// boundaries.
func h5tIsNumericWithUnusualUnusedBits(dt *h5.Datatype) bool {
	/* Is the correct type? */
	if dt.Class == h5.ClassInteger || dt.Class == h5.ClassFloat || dt.Class == h5.ClassBitfield {
		/* Has unused bits? */
		if dt.Size > 1 && uint64(dt.Precision) < dt.Size*8 {
			/* Unused bits are unusually large? */
			return dt.Size*8 > 2*(uint64(dt.Precision)+uint64(dt.Offset))
		}
	}
	return false
}

// h5RangeOverlap is H5_RANGE_OVERLAP(L1, H1, L2, H2): whether the closed
// ranges [L1, H1] and [L2, H2] overlap.
func h5RangeOverlap(l1, h1, l2, h2 uint64) bool {
	return l1 <= h2 && l2 <= h1
}

// dtypeNameLength is the strnlen() of the field/enum name at the cursor,
// with the "not null terminated" check of the C code.
func dtypeNameLength(p *image) (int, bool) {
	n := bytes.IndexByte(p.rest(), 0)
	if n < 0 {
		return 0, false
	}
	return n, true
}

// h5oDtypeDecodeHelper is H5O__dtype_decode_helper: decodes a datatype.
// It returns true if we can upgrade the parent type's version even with
// strict format checks.
//
// The "skip" mode of the C function (decoding an unbounded buffer from
// H5Tdecode) is not needed: the buffer end is always known.
func h5oDtypeDecodeHelper(ioflags *uint, p *image, dt *h5.Datatype) (bool, error) {
	retValue := false

	/* Version, class & flags */
	if p.overflow(4) {
		return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	flags := p.uint32()
	version := uint8((flags >> 4) & 0x0f)
	if version < h5oDtypeVersion1 || version > h5oDtypeVersionLatest {
		return false, errorf(H5E_DATATYPE, H5E_CANTLOAD, "bad version number for datatype message")
	}
	dt.Version = version
	dt.Class = h5.Class(flags & 0x0f)
	flags >>= 8

	/* Size */
	if p.overflow(4) {
		return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	dt.Size = uint64(p.uint32())

	/* Check for invalid datatype size */
	if dt.Size == 0 {
		return false, errorf(H5E_OHDR, H5E_BADVALUE, "invalid datatype size")
	}

	switch dt.Class {
	case h5.ClassInteger:
		/*
		 * Integer types...
		 */
		dt.Order = h5.OrderLE
		if flags&0x1 != 0 {
			dt.Order = h5.OrderBE
		}
		dt.LSBPad = padFlag(flags & 0x2)
		dt.MSBPad = padFlag(flags & 0x4)
		dt.Sign = h5.SignNone
		if flags&0x8 != 0 {
			dt.Sign = h5.Sign2
		}
		if p.overflow(2 + 2) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Offset = uint32(p.uint16())
		dt.Precision = uint32(p.uint16())

		/* Sanity checks */
		if uint64(dt.Offset) >= dt.Size*8 {
			return false, errorf(H5E_DATATYPE, H5E_BADRANGE, "integer offset out of bounds")
		}
		if 0 == dt.Precision {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "precision is zero")
		}
		if (uint64(dt.Offset)+uint64(dt.Precision))-1 >= dt.Size*8 {
			return false, errorf(H5E_DATATYPE, H5E_BADRANGE, "integer offset+precision out of bounds")
		}

	case h5.ClassFloat:
		/*
		 * Floating-point types...
		 */
		dt.Order = h5.OrderLE
		if flags&0x1 != 0 {
			dt.Order = h5.OrderBE
		}
		if version >= h5oDtypeVersion3 {
			/* Unsupported byte order*/
			if flags&0x40 != 0 && flags&0x1 == 0 {
				return false, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "bad byte order for datatype message")
			}

			/* VAX order if both 1st and 6th bits are turned on*/
			if flags&0x40 != 0 {
				dt.Order = h5.OrderVAX
			}
		}
		dt.LSBPad = padFlag(flags & 0x2)
		dt.MSBPad = padFlag(flags & 0x4)
		dt.Float.IntPad = padFlag(flags & 0x8)
		switch (flags >> 4) & 0x03 {
		case 0:
			dt.Float.Norm = h5.NormNone
		case 1:
			dt.Float.Norm = h5.NormMSBSet
		case 2:
			dt.Float.Norm = h5.NormImplied
		default:
			return false, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "unknown floating-point normalization")
		}
		dt.Float.SignPos = (flags >> 8) & 0xff
		if uint64(dt.Float.SignPos) >= dt.Size*8 {
			return false, errorf(H5E_DATATYPE, H5E_BADRANGE, "sign bit position out of bounds")
		}

		if p.overflow(2 + 2) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Offset = uint32(p.uint16())
		dt.Precision = uint32(p.uint16())

		if p.overflow(1 + 1) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Float.ExpPos = uint32(p.uint8())
		dt.Float.ExpSize = uint32(p.uint8())
		if dt.Float.ExpSize == 0 {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "exponent size can't be zero")
		}
		if uint64(dt.Float.ExpPos) >= dt.Size*8 {
			return false, errorf(H5E_DATATYPE, H5E_BADRANGE, "exponent starting position out of bounds")
		}
		if (uint64(dt.Float.ExpPos)+uint64(dt.Float.ExpSize))-1 >= dt.Size*8 {
			return false, errorf(H5E_DATATYPE, H5E_BADRANGE, "exponent range out of bounds")
		}

		if p.overflow(1 + 1) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Float.MantPos = uint32(p.uint8())
		dt.Float.MantSize = uint32(p.uint8())
		if dt.Float.MantSize == 0 {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "mantissa size can't be zero")
		}
		if uint64(dt.Float.MantPos) >= dt.Size*8 {
			return false, errorf(H5E_DATATYPE, H5E_BADRANGE, "mantissa starting position out of bounds")
		}
		if (uint64(dt.Float.MantPos)+uint64(dt.Float.MantSize))-1 >= dt.Size*8 {
			return false, errorf(H5E_DATATYPE, H5E_BADRANGE, "mantissa range out of bounds")
		}

		if p.overflow(4) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Float.ExpBias = uint64(p.uint32())

		/* Sanity check bits don't overlap */
		sign := uint64(dt.Float.SignPos)
		elo, ehi := uint64(dt.Float.ExpPos), uint64(dt.Float.ExpPos)+uint64(dt.Float.ExpSize)-1
		mlo, mhi := uint64(dt.Float.MantPos), uint64(dt.Float.MantPos)+uint64(dt.Float.MantSize)-1
		if h5RangeOverlap(sign, sign, elo, ehi) {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "exponent and sign positions overlap")
		}
		if h5RangeOverlap(sign, sign, mlo, mhi) {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "mantissa and sign positions overlap")
		}
		if h5RangeOverlap(elo, ehi, mlo, mhi) {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "mantissa and exponent positions overlap")
		}

	case h5.ClassTime:
		/*
		 * Time datatypes...
		 */
		dt.Order = h5.OrderLE
		if flags&0x1 != 0 {
			dt.Order = h5.OrderBE
		}
		if p.overflow(2) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Precision = uint32(p.uint16())

	case h5.ClassString:
		/*
		 * Character string types...
		 */
		dt.Order = h5.OrderNone
		dt.Precision = uint32(8 * dt.Size)
		dt.Offset = 0
		dt.LSBPad = h5.PadZero
		dt.MSBPad = h5.PadZero

		dt.StrPad = h5.StrPad(flags & 0x0f)
		dt.Cset = h5.Cset((flags >> 4) & 0x0f)

	case h5.ClassBitfield:
		/*
		 * Bit fields...
		 */
		dt.Order = h5.OrderLE
		if flags&0x1 != 0 {
			dt.Order = h5.OrderBE
		}
		dt.LSBPad = padFlag(flags & 0x2)
		dt.MSBPad = padFlag(flags & 0x4)
		if p.overflow(2 + 2) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Offset = uint32(p.uint16())
		dt.Precision = uint32(p.uint16())

	case h5.ClassOpaque:
		/*
		 * Opaque types...
		 */
		dt.Order = h5.OrderNone

		/* The opaque tag flag field must be aligned */
		z := int(flags & (h5tOpaqueTagMax - 1))
		if 0 != (z & 0x7) {
			return false, errorf(H5E_OHDR, H5E_BADVALUE, "opaque flag field must be aligned")
		}

		if p.overflow(z) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		tag := p.bytes(z)
		/* The tag is used as a NUL-terminated C string */
		if i := bytes.IndexByte(tag, 0); i >= 0 {
			tag = tag[:i]
		}
		dt.Tag = string(tag)

	case h5.ClassCompound:
		var (
			maxMembPos uint64 /* Maximum member covered, so far */
			maxVersion uint8  /* Maximum member version */
			upgradeTo  uint8  /* Version number we can "soft" upgrade to */
			membSize   []uint64
		)
		dt.Order = h5.OrderNone

		/* Compute the # of bytes required to store a member offset */
		offsetNbytes := dtypeLimitEncSize(dt.Size)

		/*
		 * Compound datatypes...
		 */
		nmembs := int(flags & 0xffff)
		if nmembs == 0 {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "invalid number of members: %d", nmembs)
		}
		/* Each member needs at least a name terminator, an offset and an
		 * 8-byte datatype header: bound the allocation by the buffer. */
		if nmembs > p.remaining()/(1+1+8) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.Members = make([]h5.Member, 0, nmembs)
		membSize = make([]uint64, 0, nmembs)

		for len(dt.Members) < nmembs {
			cur := len(dt.Members)
			var (
				ndims uint32    /* Number of dimensions of the array field */
				dim   [4]uint64 /* Dimensions of the array */
				memb  h5.Member
			)

			/* Get the length of the field name */
			actualNameLength, ok := dtypeNameLength(p)
			if !ok {
				return false, errorf(H5E_OHDR, H5E_OVERFLOW, "field name not null terminated")
			}

			if p.overflow(actualNameLength) {
				return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}

			/* Check for duplicated field name */
			name := string(p.rest()[:actualNameLength])
			for membIdx := 0; membIdx < cur; membIdx++ {
				if name == dt.Members[membIdx].Name {
					return false, errorf(H5E_OHDR, H5E_BADVALUE,
						"duplicated compound field name '%s', for fields %d and %d", name, membIdx, cur)
				}
			}

			/* Decode the field name */
			memb.Name = name

			/* Version 3 of the datatype message eliminated the padding to multiple of 8 bytes */
			if version >= h5oDtypeVersion3 {
				/* Advance past name, including null terminator */
				if p.overflow(actualNameLength + 1) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(actualNameLength + 1)
			} else {
				/* Advance multiple of 8 w/ null terminator */
				if p.overflow(((actualNameLength + 8) / 8) * 8) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(((actualNameLength + 8) / 8) * 8)
			}

			/* Decode the field offset */
			/* (starting with version 3 of the datatype message, use the minimum # of bytes required) */
			if version >= h5oDtypeVersion3 {
				if p.overflow(offsetNbytes) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				memb.Offset = uint64(uint32(p.uintVar(offsetNbytes)))
			} else {
				if p.overflow(4) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				memb.Offset = uint64(p.uint32())
			}

			/* Older versions of the library allowed a field to have
			 * intrinsic 'arrayness'.  Newer versions of the library
			 * use the separate array datatypes. */
			if version == h5oDtypeVersion1 {
				/* Decode the number of dimensions */
				if p.overflow(1) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				ndims = uint32(p.uint8())

				/* Check that ndims is valid */
				if ndims > 4 {
					return false, errorf(H5E_DATATYPE, H5E_BADTYPE, "invalid number of dimensions for array")
				}

				/* Skip reserved bytes */
				if p.overflow(3) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(3)

				/* Skip dimension permutation */
				if p.overflow(4) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(4)

				/* Skip reserved bytes */
				if p.overflow(4) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(4)

				/* Decode array dimension sizes */
				if p.overflow(4 * 4) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				for i := 0; i < 4; i++ {
					dim[i] = uint64(p.uint32())
				}
			}

			/* Allocate space for the field's datatype */
			tempType := &h5.Datatype{}

			/* Decode the field's datatype information */
			canUpgrade, err := h5oDtypeDecodeHelper(ioflags, p, tempType)
			if err != nil {
				return false, wrapf(err, H5E_DATATYPE, H5E_CANTDECODE, "unable to decode member type")
			}
			if tempType.Size == 0 {
				return false, errorf(H5E_DATATYPE, H5E_CANTDECODE, "type size can't be zero")
			}
			if memb.Offset+tempType.Size > dt.Size {
				return false, errorf(H5E_DATATYPE, H5E_CANTDECODE, "member type extends outside its parent compound type")
			}

			/* Upgrade the version if we can and it is necessary */
			if canUpgrade && tempType.Version > version {
				upgradeTo = tempType.Version

				/* Pass "can_upgrade" flag down to parent type */
				retValue = true
			}

			/* Go create the array datatype now, for older versions of the datatype message */
			if version == h5oDtypeVersion1 {
				/* Check if this member is an array field */
				if ndims > 0 {
					/* Validate decoded dims */
					for u := uint32(0); u < ndims; u++ {
						if !(dim[u] > 0) {
							return false, errorf(H5E_DATATYPE, H5E_BADVALUE, "zero-sized dimension specified")
						}
					}

					/* Create the array datatype for the field */
					arrayDt, err := h5tArrayCreate(tempType, dim[:ndims])
					if err != nil {
						return false, wrapf(err, H5E_DATATYPE, H5E_CANTINIT, "unable to create array datatype")
					}

					/* Make the array type the type that is set for the field */
					tempType = arrayDt

					/* Reset array version if NOCHANGE is specified (i.e. h5debug) */
					if *ioflags&h5oDecodeioNochange != 0 {
						tempType.Version = h5oDtypeVersion1
					} else {
						/* Otherwise upgrade the compound version */
						if upgradeTo < tempType.Version {
							upgradeTo = tempType.Version
						}

						/* Set the return value to indicate that we should freely
						 * upgrade parent types */
						retValue = true
					}
				}
			}

			/* Keep track of the maximum member version found */
			if tempType.Version > maxVersion {
				maxVersion = tempType.Version
			}

			/* Set the field datatype (finally :-) */
			memb.Type = tempType

			/* Check if this field overlaps with a prior field
			 * (probably indicates that the file is corrupt)
			 */
			size := tempType.Size
			if cur > 0 && memb.Offset < maxMembPos {
				for u := 0; u < cur; u++ {
					uOff, uSize := dt.Members[u].Offset, membSize[u]
					if (memb.Offset >= uOff && memb.Offset < uOff+uSize) ||
						(memb.Offset < uOff && memb.Offset+size > uOff) {
						return false, errorf(H5E_DATATYPE, H5E_CANTDECODE, "member overlaps with previous member")
					}
				}
			}

			/* Update the maximum member position covered */
			maxMembPos = max(maxMembPos, memb.Offset+size)

			dt.Members = append(dt.Members, memb)
			membSize = append(membSize, size)
		}

		/* Upgrade the compound if requested */
		if version < upgradeTo {
			version = upgradeTo
			h5tUpgradeVersion(dt, upgradeTo)
			/* We won't mark the message dirty since there were no
			 * errors in the file, simply type versions that we will no
			 * longer encode. */
		}

		/* Check that no member of this compound has a version greater
		 * than the compound itself. */
		h5oDtypeCheckVersion(dt, &version, maxVersion, ioflags)

	case h5.ClassReference:
		/*
		 * Reference datatypes...
		 */
		dt.Order = h5.OrderNone
		dt.Precision = uint32(8 * dt.Size)
		dt.Offset = 0
		dt.LSBPad = h5.PadZero
		dt.MSBPad = h5.PadZero

		/* Set reference type */
		rtype := int(flags & 0x0f)
		if rtype <= h5rBadtype || rtype >= h5rMaxtype {
			return false, errorf(H5E_DATATYPE, H5E_CANTDECODE, "invalid reference type")
		}
		dt.RefType = h5.RefType(rtype)

		/* Set generic flag */
		if dt.RefType == h5.RefObject2 || dt.RefType == h5.RefRegion2 || dt.RefType == h5.RefAttr {
			if (flags>>4)&0x0f != h5rEncodeVersion {
				return false, errorf(H5E_DATATYPE, H5E_CANTDECODE, "reference version does not match")
			}
		}

	case h5.ClassEnum:
		/*
		 * Enumeration datatypes...
		 */
		dt.Order = h5.OrderNone
		nmembs := int(flags & 0xffff)
		dt.Base = &h5.Datatype{}
		if _, err := h5oDtypeDecodeHelper(ioflags, p, dt.Base); err != nil {
			return false, wrapf(err, H5E_DATATYPE, H5E_CANTDECODE, "unable to decode parent datatype")
		}
		if dt.Base.Size != dt.Size {
			return false, errorf(H5E_DATATYPE, H5E_BADSIZE, "ENUM datatype size does not match parent")
		}

		/* Check if the parent of this enum has a version greater than the
		 * enum itself. */
		h5oDtypeCheckVersion(dt, &version, dt.Base.Version, ioflags)

		/* Allocate name and value arrays (each member needs at least a
		 * 2-byte name and its value: bound the allocation by the buffer) */
		if uint64(nmembs) > uint64(p.remaining()) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.EnumNames = make([]string, 0, nmembs)

		/* Names */
		for len(dt.EnumNames) < nmembs {
			/* Get the length of the enum name */
			actualNameLength, ok := dtypeNameLength(p)
			if !ok {
				return false, errorf(H5E_OHDR, H5E_OVERFLOW, "enum name not null terminated")
			}
			if 0 == actualNameLength {
				return false, errorf(H5E_OHDR, H5E_BADSIZE, "0 length enum name")
			}

			if p.overflow(actualNameLength) {
				return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			dt.EnumNames = append(dt.EnumNames, string(p.rest()[:actualNameLength]))

			/* Version 3 of the datatype message eliminated the padding to multiple of 8 bytes */
			if version >= h5oDtypeVersion3 {
				/* Advance past name, including null terminator */
				if p.overflow(actualNameLength + 1) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(actualNameLength + 1)
			} else {
				/* Advance multiple of 8 w/ null terminator */
				if p.overflow(((actualNameLength + 8) / 8) * 8) {
					return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(((actualNameLength + 8) / 8) * 8)
			}
		}
		if len(dt.EnumNames) != nmembs {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "incorrect number of enum members decoded")
		}

		/* Values */
		baseSize := dt.Base.Size
		if baseSize != 0 && uint64(nmembs) > uint64(p.remaining())/baseSize {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		if p.overflow(nmembs * int(baseSize)) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.EnumValues = make([]int64, nmembs)
		for i := range dt.EnumValues {
			dt.EnumValues[i] = h5tEnumRawValue(dt.Base, p.bytes(int(baseSize)))
		}

	case h5.ClassVlen:
		/*
		 * Variable length datatypes...
		 */
		dt.Order = h5.OrderNone

		/* Set the type of VL information, either sequence or string */
		dt.VlenKind = h5.VlenKind(flags & 0x0f)
		if dt.VlenKind == h5.VlenString {
			dt.StrPad = h5.StrPad((flags >> 4) & 0x0f)
			dt.Cset = h5.Cset((flags >> 8) & 0x0f)
		} else if dt.VlenKind != h5.VlenSequence {
			return false, errorf(H5E_DATATYPE, H5E_CANTINIT, "invalid VL datatype type")
		}

		/* Decode base type of VL information */
		dt.Base = &h5.Datatype{}
		if _, err := h5oDtypeDecodeHelper(ioflags, p, dt.Base); err != nil {
			return false, wrapf(err, H5E_DATATYPE, H5E_CANTDECODE, "unable to decode VL parent type")
		}

		/* Check if the parent of this vlen has a version greater than the
		 * vlen itself. */
		h5oDtypeCheckVersion(dt, &version, dt.Base.Version, ioflags)

	case h5.ClassArray:
		/*
		 * Array datatypes...
		 */
		dt.Order = h5.OrderNone

		/* Decode the number of dimensions */
		if p.overflow(1) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		ndims := int(p.uint8())

		/* Double-check the number of dimensions */
		if ndims > h5sMaxRank {
			return false, errorf(H5E_DATATYPE, H5E_CANTLOAD, "too many dimensions for array datatype")
		}

		/* Skip reserved bytes, if version has them */
		if version < h5oDtypeVersion3 {
			if p.overflow(3) {
				return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			p.skip(3)
		}

		/* Decode array dimension sizes & compute number of elements */
		if p.overflow(ndims * 4) {
			return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		dt.ArrayDims = make([]uint64, ndims)
		for u := 0; u < ndims; u++ {
			dt.ArrayDims[u] = uint64(p.uint32())
		}

		/* Skip array dimension permutations, if version has them */
		if version < h5oDtypeVersion3 {
			if p.overflow(ndims * 4) {
				return false, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			p.skip(ndims * 4)
		}

		/* Decode base type of array */
		dt.Base = &h5.Datatype{}
		if _, err := h5oDtypeDecodeHelper(ioflags, p, dt.Base); err != nil {
			return false, wrapf(err, H5E_DATATYPE, H5E_CANTDECODE, "unable to decode array parent type")
		}

		/* Check if the parent of this array has a version greater than the
		 * array itself. */
		h5oDtypeCheckVersion(dt, &version, dt.Base.Version, ioflags)

		/* There should be no array datatypes with version < 2. */
		h5oDtypeCheckVersion(dt, &version, h5oDtypeVersion2, ioflags)

	default:
		return false, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "unknown datatype class found")
	}

	/* Check for numeric type w/unusual # of unused bits */
	if h5tIsNumericWithUnusualUnusedBits(dt) {
		/* Throw an error if the object header is not checksummed, unless the
		 * H5F_RFIC_UNUSUAL_NUM_UNUSED_NUMERIC_BITS flag is set with
		 * H5Pset_relax_file_integrity_checks() to suppress it.
		 */
		if *ioflags&h5oDecodeioRficUnubnt == 0 {
			return false, errorf(H5E_DATATYPE, H5E_BADVALUE,
				"datatype has unusually large # of unused bits (prec = %d bits, size = %d bytes), possibly "+
					"corrupted file. See documentation for H5Pset_relax_file_integrity_checks for details.",
				dt.Precision, dt.Size)
		}
	}

	if err := p.done(); err != nil {
		return false, err
	}
	return retValue, nil
}

// padFlag maps a datatype message padding flag bit to H5T_PAD_ONE/ZERO.
func padFlag(bit uint32) h5.Pad {
	if bit != 0 {
		return h5.PadOne
	}
	return h5.PadZero
}

// h5tArrayCreate is H5T__array_create: create an array datatype of base
// with the given dimensions (used for the intrinsic array members of
// version 1 compound datatypes).
func h5tArrayCreate(base *h5.Datatype, dim []uint64) (*h5.Datatype, error) {
	dt := &h5.Datatype{
		Class:     h5.ClassArray,
		Order:     h5.OrderNone,
		Base:      base,
		ArrayDims: append([]uint64(nil), dim...),
	}

	/* Set the array's size (number of elements * element datatype's size) */
	nelem := uint64(1)
	for _, d := range dim {
		hi, lo := bits.Mul64(nelem, d)
		if hi != 0 {
			return nil, errorf(H5E_DATATYPE, H5E_OVERFLOW, "array datatype size overflows")
		}
		nelem = lo
	}
	hi, size := bits.Mul64(base.Size, nelem)
	if hi != 0 {
		return nil, errorf(H5E_DATATYPE, H5E_OVERFLOW, "array datatype size overflows")
	}
	dt.Size = size

	/* Array datatypes need a later version of the datatype object header message */
	dt.Version = max(base.Version, h5oDtypeVersion2)
	return dt, nil
}

// h5oDtypeDecodeIO is H5O__dtype_decode with explicit decode I/O flags: the
// object header code passes 0 for unchecksummed (version 1) object headers
// and h5oDecodeioRficUnubnt for checksummed ones, as the C code does via
// H5O_SIZEOF_CHKSUM_OH(open_oh).
func h5oDtypeDecodeIO(f *File, p *image, ioflags uint) (*h5.Datatype, error) {
	/* Allocate datatype message */
	dt := &h5.Datatype{}

	/* Perform actual decode of message */
	if _, err := h5oDtypeDecodeHelper(&ioflags, p, dt); err != nil {
		return nil, wrapf(err, H5E_DATATYPE, H5E_CANTDECODE, "can't decode type")
	}
	return dt, nil
}

// h5oDtypeDecodeOh is H5O__dtype_decode for a message of the object header
// openOh: the "unusual number of unused bits" check applies unless the
// object header has a checksum (H5F_RFIC_UNUSUAL_NUM_UNUSED_NUMERIC_BITS is
// never set here). A nil openOh is a decode from a non-object header,
// assumed checksummed.
func h5oDtypeDecodeOh(f *File, openOh *h5o, p *image) (*h5.Datatype, error) {
	/* Indicate if the object header has a checksum, or if the
	 * H5F_RFIC_UNUSUAL_NUM_UNUSED_NUMERIC_BITS flag is set */
	var ioflags uint
	if openOh != nil {
		if h5oSizeofChksumOh(openOh) > 0 {
			ioflags |= h5oDecodeioRficUnubnt
		}
	} else {
		/* Decode operations from non-object headers are assumed to be checksummed */
		ioflags |= h5oDecodeioRficUnubnt
	}
	return h5oDtypeDecodeIO(f, p, ioflags)
}

// h5oDtypeDecode is H5O_dtype_decode: decode a datatype message. As with
// a NULL open_oh in the C code, the buffer is assumed to be checksummed (the
// "unusual number of unused bits" check is relaxed); use h5oDtypeDecodeIO
// to apply it for version 1 object headers.
func h5oDtypeDecode(f *File, p *image) (*h5.Datatype, error) {
	return h5oDtypeDecodeIO(f, p, h5oDecodeioRficUnubnt)
}

// h5tEnumRawValue converts the raw (file) representation of an enumeration
// member value, in the enum's base type, to the int64 stored in
// Datatype.EnumValues: the integer's precision bits, sign-extended for
// signed bases.
func h5tEnumRawValue(base *h5.Datatype, raw []byte) int64 {
	switch base.Class {
	case h5.ClassInteger, h5.ClassBitfield:
		return int64(h5tIntegerBits(base, raw))
	}
	/* Non-integer bases are not created by the library; keep the low bytes */
	return int64(uintVarLen(raw, len(raw)))
}
