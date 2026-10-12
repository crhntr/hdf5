package hdf5

// Translated from src/H5Sall.c (read side). The "all" selection iterator
// methods are folded into the Go iterators in h5s_select.go and
// h5sAllIterPoints below.

// H5S_ALL_VERSION_*
const (
	h5sAllVersion1      = 1
	h5sAllVersionLatest = h5sAllVersion1
)

// h5sAllIterPoints yields the coordinates of every element of the extent
// in row-major order (H5S__all_iter_coords / H5S__all_iter_next).
func h5sAllIterPoints(space *h5s, yield func([]uint64) bool) {
	if space.extent.nelem == 0 {
		return
	}
	rank := int(space.extent.rank)
	coords := make([]uint64, rank)
	for {
		if !yield(coords) {
			return
		}
		/* Advance the row-major position */
		d := rank - 1
		for ; d >= 0; d-- {
			coords[d]++
			if coords[d] < space.extent.size[d] {
				break
			}
			coords[d] = 0
		}
		if d < 0 {
			return
		}
	}
}

// h5sAllIsValid is H5S__all_is_valid: an "all" selection always fits.
func h5sAllIsValid(*h5s) bool { return true }

// h5sAllDeserialize is H5S__all_deserialize: decode an "all" selection
// (after the selection type).
func h5sAllDeserialize(p *image) (*h5sSelection, error) {
	/* Decode version */
	if p.overflow(4) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection version")
	}
	version := p.uint32()

	if version < h5sAllVersion1 || version > h5sAllVersionLatest {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "bad version number for all selection")
	}

	/* Skip over the remainder of the header */
	if p.overflow(8) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding header")
	}
	p.skip(8)

	/* Change to "all" selection */
	return &h5sSelection{typ: h5sSelAll, unlimDim: -1}, nil
}

// h5sAllBounds is H5S__all_bounds: the bounding box of an "all" selection
// is the whole extent.
func h5sAllBounds(space *h5s) (start, end []uint64, err error) {
	rank := space.extent.rank
	start = make([]uint64, rank)
	end = make([]uint64, rank)

	/* Just copy over the complete extent */
	for i := range rank {
		start[i] = 0
		end[i] = space.extent.size[i] - 1
	}
	return start, end, nil
}

// h5sAllOffset is H5S__all_offset: the first element is at offset 0.
func h5sAllOffset(*h5s) (uint64, error) { return 0, nil }

// h5sAllUnlimDim is H5S__all_unlim_dim.
func h5sAllUnlimDim(*h5s) int { return -1 }

// h5sAllIsContiguous is H5S__all_is_contiguous.
func h5sAllIsContiguous(*h5s) bool { return true }

// h5sAllIsSingle is H5S__all_is_single.
func h5sAllIsSingle(*h5s) bool { return true }

// h5sAllIsRegular is H5S__all_is_regular.
func h5sAllIsRegular(*h5s) bool { return true }

// h5sAllShapeSame is H5S__all_shape_same (space1 has the larger rank).
func h5sAllShapeSame(space1, space2 *h5s) bool {
	/* Initialize dataspace dims */
	space1Dim := int(space1.extent.rank) - 1
	space2Dim := int(space2.extent.rank) - 1

	/* Recall that space1_rank >= space2_rank.
	 *
	 * In the following while loop, we test to see if space1 and space2
	 * have identical size in all dimensions they have in common.
	 */
	for space2Dim >= 0 {
		if space1.extent.size[space1Dim] != space2.extent.size[space2Dim] {
			return false
		}
		space1Dim--
		space2Dim--
	}

	/* Since we are selecting the entire space, we must also verify that space1
	 * has size 1 in all dimensions that it does not share with space2.
	 */
	for space1Dim >= 0 {
		if space1.extent.size[space1Dim] != 1 {
			return false
		}
		space1Dim--
	}
	return true
}

// h5sAllIntersectBlock is H5S__all_intersect_block: any block within the
// extent intersects (the bounds were checked by the caller).
func h5sAllIntersectBlock(*h5s, []uint64, []uint64) bool { return true }

// h5sAllAdjustU is H5S__all_adjust_u: an "all" selection does not move.
func h5sAllAdjustU(space *h5s, _ []uint64) (*h5s, error) { return h5sCopy(space), nil }

// h5sAllProjectScalar is H5S__all_project_scalar.
func h5sAllProjectScalar(*h5s) (uint64, error) {
	/* Set offset of selection in projected buffer */
	return 0, nil
}

// h5sSelectAll is H5S_select_all: select the entire extent of a dataspace
// being built.
func h5sSelectAll(space *h5s) {
	/* Remove current selection first, set number of elements in selection
	 * and selection type */
	space.sel = &h5sSelection{typ: h5sSelAll, rank: space.extent.rank, numElem: space.extent.nelem, unlimDim: -1}
}
