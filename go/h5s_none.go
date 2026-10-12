package hdf5

// Translated from src/H5Snone.c (read side). A "none" selection yields
// nothing from the iterators in h5s_select.go.

// H5S_NONE_VERSION_*
const (
	h5sNoneVersion1      = 1
	h5sNoneVersionLatest = h5sNoneVersion1
)

// h5sNoneIsValid is H5S__none_is_valid.
func h5sNoneIsValid(*h5s) bool { return true }

// h5sNoneDeserialize is H5S__none_deserialize: decode a "none" selection
// (after the selection type).
func h5sNoneDeserialize(p *image) (*h5sSelection, error) {
	/* Decode version */
	if p.overflow(4) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection version")
	}
	version := p.uint32()

	if version < h5sNoneVersion1 || version > h5sNoneVersionLatest {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "bad version number for none selection")
	}

	/* Skip over the remainder of the header */
	if p.overflow(8) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection header")
	}
	p.skip(8)

	/* Change to "none" selection */
	return &h5sSelection{typ: h5sSelNone, unlimDim: -1}, nil
}

// h5sNoneBounds is H5S__none_bounds: a "none" selection has no bounds.
func h5sNoneBounds(*h5s) (start, end []uint64, err error) {
	return nil, nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "can't get bounds of \"none\" selection")
}

// h5sNoneOffset is H5S__none_offset.
func h5sNoneOffset(*h5s) (uint64, error) {
	return 0, errorf(H5E_DATASPACE, H5E_BADVALUE, "can't get offset of \"none\" selection")
}

// h5sNoneUnlimDim is H5S__none_unlim_dim.
func h5sNoneUnlimDim(*h5s) int { return -1 }

// h5sNoneIsContiguous is H5S__none_is_contiguous.
func h5sNoneIsContiguous(*h5s) bool { return false }

// h5sNoneIsSingle is H5S__none_is_single.
func h5sNoneIsSingle(*h5s) bool { return false }

// h5sNoneIsRegular is H5S__none_is_regular: a "none" selection is always
// regular.
func h5sNoneIsRegular(*h5s) bool { return true }

// h5sNoneShapeSame is H5S__none_shape_same.
func h5sNoneShapeSame(*h5s, *h5s) bool { return true }

// h5sNoneIntersectBlock is H5S__none_intersect_block.
func h5sNoneIntersectBlock(*h5s, []uint64, []uint64) bool { return false }

// h5sNoneAdjustU is H5S__none_adjust_u.
func h5sNoneAdjustU(space *h5s, _ []uint64) (*h5s, error) { return h5sCopy(space), nil }

// h5sSelectNone is H5S_select_none: select nothing in a dataspace being
// built.
func h5sSelectNone(space *h5s) {
	/* Remove current selection first, set number of elements in selection
	 * and selection type */
	space.sel = &h5sSelection{typ: h5sSelNone, rank: space.extent.rank, numElem: 0, unlimDim: -1}
}
