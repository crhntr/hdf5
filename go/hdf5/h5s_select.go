package hdf5

// Translated from src/H5Sselect.c (read side). The selection "class"
// method tables of the C library (H5S_sel_all, H5S_sel_none, H5S_sel_point,
// H5S_sel_hyper) become switches on the selection type that call the
// h5sAll*/h5sNone*/h5sPoint*/h5sHyper* functions.
//
// The selection iterators of the C library (H5S_sel_iter_t with its
// iter_init / iter_coords / iter_next / iter_get_seq_list methods) are
// replaced by Go iterators:
//
//	(*h5s).iterSeqs(elmtSize)  - H5S_select_iter_get_seq_list: byte runs
//	(*h5s).iterPoints()        - H5S_select_iter_coords + iter_next(1)
//	h5sSelectIterBlocks(space) - H5S_SELECT_ITER_BLOCK + iter_next_block
//
// Selections are immutable: the C functions that modify a dataspace's
// selection in place (H5S_select_offset, H5S_select_adjust_u, ...) return
// a new dataspace here and leave their argument untouched.

import (
	"iter"
	"math/bits"

	"github.com/HDFGroup/hdf5/go/h5"
)

// seq is one sequence of H5S_select_iter_get_seq_list: len bytes starting
// at byte offset off from the start of the row-major extent buffer.
type seq struct {
	off uint64 /* byte offset from the start of the row-major extent buffer */
	len uint64 /* bytes */
}

// selection returns the dataspace's selection, materializing the implicit
// "all" selection of a nil sel.
func (s *h5s) selection() *h5sSelection {
	if s.sel != nil {
		return s.sel
	}
	return &h5sSelection{typ: h5sSelAll, rank: s.extent.rank, unlimDim: -1}
}

// selOffset returns the selection offset in dimension u (0 when unset).
func (sel *h5sSelection) selOffset(u int) int64 {
	if sel.offset == nil || u >= len(sel.offset) {
		return 0
	}
	return sel.offset[u]
}

// h5sCopy is H5S_copy(src, share_selection=true, copy_max=true): a shallow
// copy of the dataspace that shares the (immutable) selection structures.
func h5sCopy(src *h5s) *h5s {
	dst := &h5s{extent: src.extent}
	dst.extent.size = append([]uint64(nil), src.extent.size...)
	if src.extent.max != nil {
		dst.extent.max = append([]uint64(nil), src.extent.max...)
	}
	if src.sel != nil {
		sel := *src.sel
		dst.sel = &sel
	}
	return dst
}

// h5sSelectOffset is H5S_select_offset: set the selection offset for a
// dataspace. A nil offset resets all dimensions to zero. It returns a new
// dataspace.
func h5sSelectOffset(space *h5s, offset []int64) (*h5s, error) {
	/* Check args */
	if space.extent.rank == 0 || space.extent.rank > h5sMaxRank {
		return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "invalid dataspace rank for selection offset")
	}
	if offset != nil && uint32(len(offset)) < space.extent.rank {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "offset shorter than dataspace rank")
	}
	ret := h5sCopy(space)
	sel := *space.selection()

	/* Copy the offset over. As a special case, when offset is NULL, we
	 * reset all dimensions to zero.
	 */
	sel.offset = make([]int64, space.extent.rank)
	if offset != nil {
		copy(sel.offset, offset)
	}

	/* Indicate that the offset was changed */
	sel.offsetChanged = true
	ret.sel = &sel
	return ret, nil
}

// h5sGetSelectNpoints is H5S_get_select_npoints: the number of elements in
// the current selection.
func h5sGetSelectNpoints(space *h5s) uint64 {
	sel := space.selection()
	if sel.typ == h5sSelAll {
		/* (H5S_select_all sets num_elem to the extent's number of elements) */
		return space.extent.nelem
	}
	return sel.numElem
}

// h5sSelectValid is H5S_select_valid: whether the selection, with the
// current offset, fits within the extent. Unlimited selections are not
// valid. Iterating a selection requires a valid one.
func h5sSelectValid(space *h5s) bool {
	sel := space.selection()
	switch sel.typ {
	case h5sSelAll:
		return h5sAllIsValid(space)
	case h5sSelNone:
		return h5sNoneIsValid(space)
	case h5sSelPoints:
		return h5sPointIsValid(space)
	case h5sSelHyperslabs:
		return h5sHyperIsValid(space)
	}
	return false
}

// h5sSelectDeserialize is H5S_select_deserialize: decode a serialized
// selection (as stored in region references and virtual dataset mappings)
// from p, advancing p past it. The selection records the rank of the
// dataspace it was encoded for (0 for "all" and "none", which do not
// encode one); attach it to an extent with h5sNew.
func h5sSelectDeserialize(f *File, p *image) (*h5sSelection, error) {
	_ = f

	/* Selection-type specific coding is moved to the callbacks. */

	/* Decode selection type */
	if p.overflow(4) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection type")
	}
	selType := p.uint32()

	var (
		sel *h5sSelection
		err error
	)
	/* Make routine for selection type */
	switch selType {
	case h5sSelPoints: /* Sequence of points selected */
		sel, err = h5sPointDeserialize(p)
	case h5sSelHyperslabs: /* Hyperslab selection defined */
		sel, err = h5sHyperDeserialize(p)
	case h5sSelAll: /* Entire extent selected */
		sel, err = h5sAllDeserialize(p)
	case h5sSelNone: /* Nothing selected */
		sel, err = h5sNoneDeserialize(p)
	default:
		err = errorf(H5E_DATASPACE, H5E_BADVALUE, "unknown selection type %d", selType)
	}
	if err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTLOAD, "can't deserialize selection")
	}
	return sel, nil
}

// h5sGetSelectBounds is H5S_get_select_bounds: the bounding box of the
// selection (including the selection offset). It fails for "none".
func h5sGetSelectBounds(space *h5s) (start, end []uint64, err error) {
	switch space.selection().typ {
	case h5sSelAll:
		return h5sAllBounds(space)
	case h5sSelPoints:
		return h5sPointBounds(space)
	case h5sSelHyperslabs:
		return h5sHyperBounds(space)
	}
	return h5sNoneBounds(space)
}

// h5sGetSelectOffset is H5S_get_select_offset: the linear offset (in
// elements) of the first selected element, including the selection
// offset. It fails for "none".
func h5sGetSelectOffset(space *h5s) (uint64, error) {
	switch space.selection().typ {
	case h5sSelAll:
		return h5sAllOffset(space)
	case h5sSelPoints:
		return h5sPointOffset(space)
	case h5sSelHyperslabs:
		return h5sHyperOffset(space)
	}
	return h5sNoneOffset(space)
}

// h5sGetSelectUnlimDim is H5S_get_select_unlim_dim: the unlimited
// dimension of the selection, or -1.
func h5sGetSelectUnlimDim(space *h5s) int {
	if sel := space.selection(); sel.typ == h5sSelHyperslabs {
		return h5sHyperUnlimDim(space)
	}
	return -1
}

// h5sGetSelectNumElemNonUnlim is H5S_get_select_num_elem_non_unlim: the
// number of elements in a slice through the non-unlimited dimensions.
func h5sGetSelectNumElemNonUnlim(space *h5s) (uint64, error) {
	/* Check for selection callback */
	if space.selection().typ != h5sSelHyperslabs {
		return 0, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "selection type has no num_elem_non_unlim callback")
	}
	n, err := h5sHyperNumElemNonUnlim(space)
	if err != nil {
		return 0, wrapf(err, H5E_DATASPACE, H5E_CANTCOUNT, "can't get number of elements in non-unlimited dimension")
	}
	return n, nil
}

// h5sSelectIsContiguous is H5S_select_is_contiguous: whether the selected
// elements are contiguous in the dataspace.
func h5sSelectIsContiguous(space *h5s) bool {
	switch space.selection().typ {
	case h5sSelAll:
		return h5sAllIsContiguous(space)
	case h5sSelPoints:
		return h5sPointIsContiguous(space)
	case h5sSelHyperslabs:
		return h5sHyperIsContiguous(space)
	}
	return h5sNoneIsContiguous(space)
}

// h5sSelectIsSingle is H5S_select_is_single: whether the selection is a
// single block.
func h5sSelectIsSingle(space *h5s) bool {
	switch space.selection().typ {
	case h5sSelAll:
		return h5sAllIsSingle(space)
	case h5sSelPoints:
		return h5sPointIsSingle(space)
	case h5sSelHyperslabs:
		return h5sHyperIsSingle(space)
	}
	return h5sNoneIsSingle(space)
}

// h5sSelectIsRegular is H5S_select_is_regular: whether the selection is a
// single block or a regular strided pattern.
func h5sSelectIsRegular(space *h5s) bool {
	switch space.selection().typ {
	case h5sSelAll:
		return h5sAllIsRegular(space)
	case h5sSelPoints:
		return h5sPointIsRegular(space)
	case h5sSelHyperslabs:
		return h5sHyperIsRegular(space)
	}
	return h5sNoneIsRegular(space)
}

// h5sSelectAdjustU is H5S_select_adjust_u: move the selection by
// subtracting an unsigned offset from it. It returns a new dataspace.
func h5sSelectAdjustU(space *h5s, offset []uint64) (*h5s, error) {
	if uint32(len(offset)) < space.extent.rank {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "offset shorter than dataspace rank")
	}
	switch space.selection().typ {
	case h5sSelPoints:
		return h5sPointAdjustU(space, offset)
	case h5sSelHyperslabs:
		return h5sHyperAdjustU(space, offset)
	case h5sSelAll:
		return h5sAllAdjustU(space, offset)
	}
	return h5sNoneAdjustU(space, offset)
}

// h5sSelectAdjustS is H5S_select_adjust_s: move the selection by
// subtracting a signed offset from it. It returns a new dataspace.
func h5sSelectAdjustS(space *h5s, offset []int64) (*h5s, error) {
	if uint32(len(offset)) < space.extent.rank {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "offset shorter than dataspace rank")
	}
	switch space.selection().typ {
	case h5sSelPoints:
		return h5sPointAdjustS(space, offset)
	case h5sSelHyperslabs:
		return h5sHyperAdjustS(space, offset)
	}
	/* "all" and "none" selections do not move */
	return h5sCopy(space), nil
}

// h5sSelectProjectScalar is H5S_select_project_scalar: the linear offset
// (in elements) of the single element selected, for projecting onto a
// scalar dataspace.
func h5sSelectProjectScalar(space *h5s) (uint64, error) {
	switch space.selection().typ {
	case h5sSelAll:
		return h5sAllProjectScalar(space)
	case h5sSelPoints:
		return h5sPointProjectScalar(space)
	case h5sSelHyperslabs:
		return h5sHyperProjectScalar(space)
	}
	return 0, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "can't project scalar from a \"none\" selection")
}

// h5sGetSelectType is H5S_get_select_type.
func h5sGetSelectType(space *h5s) int {
	return space.selection().typ
}

// h5sSelectShapeSame is H5S_select_shape_same: whether two selections have
// the same shape (the same number of elements, in the same relative
// positions, possibly at a different offset and with extra leading
// dimensions of size 1 in the higher-rank one).
func h5sSelectShapeSame(space1, space2 *h5s) (bool, error) {
	/* Check for different number of elements selected */
	if h5sGetSelectNpoints(space1) != h5sGetSelectNpoints(space2) {
		return false, nil
	}

	/* Check special cases if both dataspaces aren't scalar */
	/* (If only one is, the number of selected points check is sufficient) */
	if space1.extent.rank == 0 || space2.extent.rank == 0 {
		return true, nil
	}

	/* To simplify logic, let space_a point to the element of the set
	 * {space1, space2} with the largest rank or space1 if the ranks
	 * are identical, and space_b to the other one.
	 */
	spaceA, spaceB := space1, space2
	if space1.extent.rank < space2.extent.rank {
		spaceA, spaceB = space2, space1
	}
	spaceARank := int(spaceA.extent.rank)
	spaceBRank := int(spaceB.extent.rank)

	/* Get selection type for both dataspaces */
	selAType := h5sGetSelectType(spaceA)
	selBType := h5sGetSelectType(spaceB)

	/* If selections aren't "none", compare their bounds */
	if selAType != h5sSelNone && selBType != h5sSelNone {
		/* Get low & high bounds for both dataspaces */
		lowA, highA, err := h5sGetSelectBounds(spaceA)
		if err != nil {
			return false, wrapf(err, H5E_DATASPACE, H5E_CANTGET, "can't get selection bounds for first dataspace")
		}
		lowB, highB, err := h5sGetSelectBounds(spaceB)
		if err != nil {
			return false, wrapf(err, H5E_DATASPACE, H5E_CANTGET, "can't get selection bounds for second dataspace")
		}

		/* Check that the range between the low & high bounds are the same */
		spaceADim := spaceARank - 1
		spaceBDim := spaceBRank - 1
		for spaceBDim >= 0 {
			/* Verify that the ranges are the same */
			if highA[spaceADim]-lowA[spaceADim] != highB[spaceBDim]-lowB[spaceBDim] {
				return false, nil
			}
			spaceADim--
			spaceBDim--
		}

		/* Check that the rest of the ranges in space a are "flat" */
		for spaceADim >= 0 {
			/* This range should be flat to be the same in a lower dimension */
			if lowA[spaceADim] != highA[spaceADim] {
				return false, nil
			}
			spaceADim--
		}

		/* Check for a single block in each selection */
		if h5sSelectIsSingle(spaceA) && h5sSelectIsSingle(spaceB) {
			/* If both selections are a single block and their bounds are
			 * the same, then the selections are the same, even if the
			 * selection types are different.
			 */
			return true, nil
		}
	}

	/* If the dataspaces have the same selection type, use the selection's
	 * shape_same operator.
	 */
	if selAType == selBType {
		switch selAType {
		case h5sSelAll:
			return h5sAllShapeSame(spaceA, spaceB), nil
		case h5sSelNone:
			return h5sNoneShapeSame(spaceA, spaceB), nil
		case h5sSelPoints:
			return h5sPointShapeSame(spaceA, spaceB), nil
		default:
			return h5sHyperShapeSame(spaceA, spaceB)
		}
	}

	/* Otherwise, iterate through all the blocks in the selection */
	offset := make([]int64, spaceARank) /* Offset of selection b blocks relative to selection a blocks */
	firstBlock := true

	nextA, stopA := iter.Pull2(h5sSelectIterBlocks(spaceA))
	defer stopA()
	nextB, stopB := iter.Pull2(h5sSelectIterBlocks(spaceB))
	defer stopB()

	startA, endA, okA := nextA()
	startB, endB, okB := nextB()
	for okA && okB {
		spaceADim := spaceARank - 1
		spaceBDim := spaceBRank - 1

		/* The first block only compares the sizes and sets the
		 * relative offsets for later blocks
		 */
		if firstBlock {
			/* If the block sizes in the common dimensions from
			 * each selection don't match, get out
			 */
			for spaceBDim >= 0 {
				if endA[spaceADim]-startA[spaceADim] != endB[spaceBDim]-startB[spaceBDim] {
					return false, nil
				}

				/* Set the relative locations of the selections */
				offset[spaceADim] = int64(startB[spaceBDim]) - int64(startA[spaceADim])

				spaceADim--
				spaceBDim--
			}

			/* Similarly, if the block size in any dimension that appears only
			 * in space_a is not equal to 1, get out.
			 */
			for spaceADim >= 0 {
				if startA[spaceADim] != endA[spaceADim] {
					return false, nil
				}
				spaceADim--
			}

			/* Reset "first block" flag */
			firstBlock = false
		} else {
			/* Check over the blocks for each selection */
			/* For dimensions that space_a and space_b have in common: */
			for spaceBDim >= 0 {
				/* Check if the blocks are in the same relative location */
				if uint64(int64(startA[spaceADim])+offset[spaceADim]) != startB[spaceBDim] {
					return false, nil
				}

				/* If the block sizes from each selection doesn't match, get out */
				if endA[spaceADim]-startA[spaceADim] != endB[spaceBDim]-startB[spaceBDim] {
					return false, nil
				}

				spaceADim--
				spaceBDim--
			}

			/* For dimensions that appear only in space_a: */
			for spaceADim >= 0 {
				/* If the block size isn't 1, get out */
				if startA[spaceADim] != endA[spaceADim] {
					return false, nil
				}
				spaceADim--
			}
		}

		/* Advance to next block in selection iterators */
		startA, endA, okA = nextA()
		startB, endB, okB = nextB()
	}

	/* Did we run out of blocks at the same time? */
	return okA == okB, nil
}

// h5sSelectIntersectBlock is H5S_select_intersect_block: whether the
// selection intersects the block start..end (inclusive).
func h5sSelectIntersectBlock(space *h5s, start, end []uint64) (bool, error) {
	rank := int(space.extent.rank)
	if len(start) < rank || len(end) < rank {
		return false, errorf(H5E_DATASPACE, H5E_BADVALUE, "block coordinates shorter than dataspace rank")
	}

	/* If selections aren't "none", compare their bounds */
	typ := h5sGetSelectType(space)
	if typ != h5sSelNone {
		/* Get low & high bounds for dataspace selection */
		low, high, err := h5sGetSelectBounds(space)
		if err != nil {
			return false, wrapf(err, H5E_DATASPACE, H5E_CANTGET, "can't get selection bounds for dataspace")
		}

		/* Loop over selection bounds and block, checking for overlap */
		for u := range rank {
			/* If selection bounds & block don't overlap, can leave now */
			if !h5RangeOverlap(low[u], high[u], start[u], end[u]) {
				return false, nil
			}
		}
	}

	/* Call selection type's intersect routine */
	switch typ {
	case h5sSelAll:
		return h5sAllIntersectBlock(space, start, end), nil
	case h5sSelPoints:
		return h5sPointIntersectBlock(space, start, end), nil
	case h5sSelHyperslabs:
		ret, err := h5sHyperIntersectBlock(space, start, end)
		if err != nil {
			return false, wrapf(err, H5E_DATASPACE, H5E_CANTCOMPARE, "can't intersect block with selection")
		}
		return ret, nil
	}
	return h5sNoneIntersectBlock(space, start, end), nil
}

// h5sSelectProjectIntersection is H5S_select_project_intersection: project
// the intersection of the selections of srcSpace and srcIntersectSpace
// (within the selection of srcSpace) onto the selection of dstSpace. The
// result is a new dataspace with dstSpace's extent. srcSpace and dstSpace
// must select the same number of elements; the n-th selected element of
// srcSpace corresponds to the n-th selected element of dstSpace.
func h5sSelectProjectIntersection(srcSpace, dstSpace, srcIntersectSpace *h5s) (*h5s, error) {
	/* Sanity checks */
	if h5sGetSelectNpoints(srcSpace) != h5sGetSelectNpoints(dstSpace) {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "source and destination selections have different numbers of elements")
	}
	if srcSpace.extent.rank != srcIntersectSpace.extent.rank {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "source and source intersect spaces have different ranks")
	}

	/* Create new space, using dst extent.  Start with "all" selection. */
	newSpace := &h5s{extent: dstSpace.extent}
	newSpace.extent.size = append([]uint64(nil), dstSpace.extent.size...)
	if dstSpace.extent.max != nil {
		newSpace.extent.max = append([]uint64(nil), dstSpace.extent.max...)
	}

	/* If the intersecting space is "all", the intersection must be equal to the
	 * source space and the projection must be equal to the destination space */
	if h5sGetSelectType(srcIntersectSpace) == h5sSelAll {
		/* Copy the destination selection. */
		if dstSpace.sel != nil {
			sel := *dstSpace.sel
			newSpace.sel = &sel
		}
		return newSpace, nil
	}

	/* If any of the selections contain no elements, the projection must be
	 * "none" */
	if h5sGetSelectNpoints(srcIntersectSpace) == 0 || h5sGetSelectNpoints(srcSpace) == 0 || h5sGetSelectNpoints(dstSpace) == 0 {
		/* Change to "none" selection */
		h5sSelectNone(newSpace)
		return newSpace, nil
	}

	/* Handle scalar dataspaces.  It should not be possible for the source
	 * intersect space or the source space to be scalar since scalar spaces
	 * only support all or none selections, and both of those cases are
	 * covered above, and the source intersect space must have the same
	 * rank, so it also cannot be scalar, as scalar dataspaces have a rank
	 * of 0. */
	if srcSpace.extent.typ == h5.SpaceScalar || srcIntersectSpace.extent.typ == h5.SpaceScalar {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "unexpected scalar source dataspace")
	}

	/* Check for scalar dst_space.  In this case we simply check if the
	 * (single) point selected in src_space intersects src_intersect_space,
	 * if so select all in new_space, otherwise select none. */
	if dstSpace.extent.typ == h5.SpaceScalar {
		/* Get source space bounds.  Should be a single point. */
		coordsStart, coordsEnd, err := h5sGetSelectBounds(srcSpace)
		if err != nil {
			return nil, wrapf(err, H5E_DATASPACE, H5E_CANTGET, "can't get source space bounds")
		}

		/* Check for intersection */
		intersect, err := h5sSelectIntersectBlock(srcIntersectSpace, coordsStart, coordsEnd)
		if err != nil {
			return nil, wrapf(err, H5E_DATASPACE, H5E_CANTCOMPARE, "can't check for intersection")
		}

		/* Select all or none as appropriate */
		if intersect {
			h5sSelectAll(newSpace)
		} else {
			h5sSelectNone(newSpace)
		}
		return newSpace, nil
	}

	/* If the source intersect space is a point selection, convert it to a
	 * hyperslab (discarding ordering).  We can get away with this because
	 * the order does not matter for the source intersect space */
	if h5sGetSelectType(srcIntersectSpace) == h5sSelPoints {
		tmp, err := h5sHyperFromPoints(srcIntersectSpace)
		if err != nil {
			return nil, wrapf(err, H5E_DATASPACE, H5E_CANTSELECT, "can't add point to temporary dataspace selection")
		}

		/* Redirect local src_intersect_space pointer (will not affect
		 * calling function) */
		srcIntersectSpace = tmp
	}

	/* By this point, src_intersect_space must be a hyperslab selection */
	if h5sGetSelectType(srcIntersectSpace) != h5sSelHyperslabs {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "unexpected source intersect selection type")
	}

	/* If either the source space or the destination space is a point
	 * selection, iterate element by element */
	if h5sGetSelectType(srcSpace) == h5sSelPoints || h5sGetSelectType(dstSpace) == h5sSelPoints {
		/* Start with "none" selection */
		h5sSelectNone(newSpace)

		/* Initialize iterators */
		nextDs, stopDs := iter.Pull(dstSpace.iterPoints())
		defer stopDs()

		/* Iterate over points */
		var points [][]uint64
		for coords := range srcSpace.iterPoints() {
			/* Check for intersection */
			intersect, err := h5sSelectIntersectBlock(srcIntersectSpace, coords, coords)
			if err != nil {
				return nil, wrapf(err, H5E_DATASPACE, H5E_CANTCOMPARE, "can't check for intersection")
			}

			/* Get DS coords (advancing the destination iterator) */
			dsCoords, ok := nextDs()
			if !ok {
				return nil, errorf(H5E_DATASPACE, H5E_CANTNEXT, "can't advance destination selection iterator")
			}

			/* Add point if it intersects */
			if intersect {
				points = append(points, append([]uint64(nil), dsCoords...))
			}
		}
		if len(points) > 0 {
			/* Add points to new_space (H5S_SELECT_APPEND) */
			h5sSelectElements(newSpace, points)
		}
		return newSpace, nil
	}

	/* Source and destination selections are all or hyperslab,
	 * intersecting selection is hyperslab.  Call the hyperslab routine
	 * to project to another hyperslab selection. */
	if err := h5sHyperProjectIntersection(srcSpace, dstSpace, srcIntersectSpace, newSpace); err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTCLIP, "can't project hyperslab onto destination selection")
	}
	return newSpace, nil
}

// iterSeqs is the Go form of H5S_select_iter_init + repeated
// H5S_select_iter_get_seq_list: it yields the selection as maximal runs of
// contiguous bytes (offsets from the start of the row-major extent buffer
// with elements of elmtSize bytes), in selection order. For "all" and
// hyperslab selections this is increasing offset order; for point
// selections it is the order of the point list (adjacent points are merged
// into one run). The selection offset is applied.
//
// The selection must be valid for the extent (h5sSelectValid); for an
// invalid or unlimited selection nothing is yielded.
func (s *h5s) iterSeqs(elmtSize uint64) iter.Seq[seq] {
	return func(yield func(seq) bool) {
		if !h5sSelectValid(s) {
			return
		}
		var cur seq
		have := false
		emit := func(off, n uint64) bool {
			o, l := off*elmtSize, n*elmtSize
			if have && cur.off+cur.len == o {
				cur.len += l
				return true
			}
			if have && !yield(cur) {
				return false
			}
			cur, have = seq{off: o, len: l}, true
			return true
		}
		if h5sSelectIterRuns(s, emit) && have {
			yield(cur)
		}
	}
}

// h5sSelectIterRuns calls emit(off, n) for each run of n contiguous
// elements starting at linear element offset off, in selection order. It
// returns false when emit stopped the iteration.
func h5sSelectIterRuns(s *h5s, emit func(off, n uint64) bool) bool {
	sel := s.selection()
	switch sel.typ {
	case h5sSelAll:
		if s.extent.nelem > 0 {
			return emit(0, s.extent.nelem)
		}
		return true
	case h5sSelPoints:
		return h5sPointIterRuns(s, emit)
	case h5sSelHyperslabs:
		return h5sHyperIterRuns(s, emit)
	}
	return true
}

// iterPoints is the Go form of H5S_select_iter_coords / iter_next(1): it
// yields the coordinates of each selected element in selection order
// (row-major for "all" and hyperslab selections, list order for point
// selections), with the selection offset applied. The yielded slice is
// reused between iterations; copy it to keep it.
//
// Unlimited hyperslab selections yield nothing.
func (s *h5s) iterPoints() iter.Seq[[]uint64] {
	return func(yield func([]uint64) bool) {
		sel := s.selection()
		switch sel.typ {
		case h5sSelAll:
			h5sAllIterPoints(s, yield)
		case h5sSelPoints:
			h5sPointIterPoints(s, yield)
		case h5sSelHyperslabs:
			h5sHyperIterPoints(s, yield)
		}
	}
}

// h5sSelectIterBlocks yields the blocks (start, end inclusive) of the
// selection the way H5S_SELECT_ITER_BLOCK / H5S_SELECT_ITER_NEXT_BLOCK walk
// them (without flattening and without the selection offset): the whole
// extent for "all", one block per point, the diminfo blocks of a regular
// hyperslab and the span tree blocks of an irregular one.
func h5sSelectIterBlocks(space *h5s) iter.Seq2[[]uint64, []uint64] {
	return func(yield func([]uint64, []uint64) bool) {
		sel := space.selection()
		switch sel.typ {
		case h5sSelAll:
			start := make([]uint64, space.extent.rank)
			end := make([]uint64, space.extent.rank)
			for u := range end {
				end[u] = space.extent.size[u] - 1
			}
			yield(start, end)
		case h5sSelPoints:
			for _, pnt := range sel.points {
				if !yield(pnt, pnt) {
					return
				}
			}
		case h5sSelHyperslabs:
			h5sHyperIterBlocks(space, yield)
		}
	}
}

// h5sMulAdd returns a*b + c, and false on overflow.
func h5sMulAdd(a, b, c uint64) (uint64, bool) {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return 0, false
	}
	s, carry := bits.Add64(lo, c, 0)
	return s, carry == 0
}

// h5sPitches returns the row-major element pitch of each dimension of the
// extent.
func h5sPitches(ext *h5sExtent) []uint64 {
	pitch := make([]uint64, ext.rank)
	acc := uint64(1)
	for i := int(ext.rank) - 1; i >= 0; i-- {
		pitch[i] = acc
		acc *= ext.size[i]
	}
	return pitch
}
