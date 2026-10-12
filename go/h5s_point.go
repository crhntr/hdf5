package hdf5

// Translated from src/H5Spoint.c (read side). The point list (a linked
// list of H5S_pnt_node_t in C) is the slice h5sSelection.points; the point
// iterator methods are folded into h5sPointIterRuns / h5sPointIterPoints.

import "math/bits"

// H5S_POINT_VERSION_*
const (
	h5sPointVersion1      = 1
	h5sPointVersion2      = 2
	h5sPointVersionLatest = h5sPointVersion2
)

// H5S_SELECT_INFO_ENC_SIZE_*
const (
	h5sSelectInfoEncSize2    = 0x02 /* 2 bytes: 16 bits */
	h5sSelectInfoEncSize4    = 0x04 /* 4 bytes: 32 bits */
	h5sSelectInfoEncSize8    = 0x08 /* 8 bytes: 64 bits */
	h5sSelectInfoEncSizeBits = h5sSelectInfoEncSize2 | h5sSelectInfoEncSize4 | h5sSelectInfoEncSize8
)

// h5sPointIterRuns walks the points in list order and calls emit with the
// linear element offset of each (H5S__point_iter_get_seq_list; the caller
// merges adjacent points into one sequence).
func h5sPointIterRuns(space *h5s, emit func(off, n uint64) bool) bool {
	sel := space.selection()
	ndims := int(space.extent.rank)
	for _, pnt := range sel.points {
		/* Compute the offset of each selected point in the buffer */
		var loc uint64
		acc := uint64(1)
		for i := ndims - 1; i >= 0; i-- {
			loc += uint64(int64(pnt[i])+sel.selOffset(i)) * acc
			acc *= space.extent.size[i]
		}
		if !emit(loc, 1) {
			return false
		}
	}
	return true
}

// h5sPointIterPoints yields each point (plus the selection offset) in list
// order (H5S__point_iter_coords / H5S__point_iter_next).
func h5sPointIterPoints(space *h5s, yield func([]uint64) bool) {
	sel := space.selection()
	coords := make([]uint64, space.extent.rank)
	for _, pnt := range sel.points {
		for u := range coords {
			coords[u] = uint64(int64(pnt[u]) + sel.selOffset(u))
		}
		if !yield(coords) {
			return
		}
	}
}

// h5sPointAdd is H5S__point_add with op == H5S_SELECT_APPEND: add points
// to the end of the selection's list, updating the bounding box.
func h5sPointAdd(sel *h5sSelection, coord [][]uint64) {
	for _, pnt := range coord {
		/* Copy over the coordinates */
		sel.points = append(sel.points, pnt)

		/* Update bound box */
		for dim := range sel.rank {
			sel.pntLowBounds[dim] = min(sel.pntLowBounds[dim], pnt[dim])
			sel.pntHighBounds[dim] = max(sel.pntHighBounds[dim], pnt[dim])
		}
	}

	/* Set the number of elements in the new selection */
	sel.numElem += uint64(len(coord))
}

// h5sPointIsValid is H5S__point_is_valid: whether all points (plus the
// selection offset) lie within the extent.
func h5sPointIsValid(space *h5s) bool {
	sel := space.selection()
	if len(sel.points) == 0 {
		return true
	}
	/* Check each dimension */
	for u := range int(space.extent.rank) {
		/* Bounds check the selected point + offset against the extent */
		/* (The C library compares with '>' here, which accepts a point one
		 *  past the end of the extent; '>=' is used so that iterating a
		 *  valid selection stays inside the extent.) */
		if sel.pntHighBounds[u]+uint64(sel.selOffset(u)) >= space.extent.size[u] {
			return false
		}
		if int64(sel.pntLowBounds[u])+sel.selOffset(u) < 0 {
			return false
		}
	}
	return true
}

// h5sPointDeserialize is H5S__point_deserialize: decode a point selection
// (versions 1 and 2, after the selection type).
func h5sPointDeserialize(p *image) (*h5sSelection, error) {
	/* Decode version */
	if p.overflow(4) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection version")
	}
	version := p.uint32()

	if version < h5sPointVersion1 || version > h5sPointVersionLatest {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "bad version number for point selection")
	}

	var encSize uint8
	if version >= h5sPointVersion2 {
		/* Decode size of point info */
		if p.overflow(1) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding point info")
		}
		encSize = p.uint8()
	} else {
		/* Skip over the remainder of the header */
		if p.overflow(8) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection headers")
		}
		p.skip(8)
		encSize = h5sSelectInfoEncSize4
	}

	/* Check encoded size */
	if encSize&^h5sSelectInfoEncSizeBits != 0 {
		return nil, errorf(H5E_DATASPACE, H5E_CANTLOAD, "unknown size of point/offset info for selection")
	}

	/* Decode the rank of the point selection */
	if p.overflow(4) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection rank")
	}
	rank := p.uint32()

	if rank == 0 || rank > h5sMaxRank {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "invalid rank (%d) for serialized point selection", rank)
	}

	/* decode the number of points */
	var numElem uint64
	var encTypeSize int
	switch encSize {
	case h5sSelectInfoEncSize2:
		if p.overflow(2) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding number of points")
		}
		numElem = uint64(p.uint16())
		encTypeSize = 2
	case h5sSelectInfoEncSize4:
		if p.overflow(4) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding number of points")
		}
		numElem = uint64(p.uint32())
		encTypeSize = 4
	case h5sSelectInfoEncSize8:
		if p.overflow(8) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding number of points")
		}
		numElem = p.uint64()
		encTypeSize = 8
	default:
		return nil, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "unknown point info size")
	}

	/* Determine necessary size of buffer for coordinates */
	hi, need := bits.Mul64(numElem, uint64(rank)*uint64(encTypeSize))
	/* Check for overflow during multiplication */
	if hi != 0 || need > uint64(1<<62) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "size of point selection buffer overflowed")
	}

	/* Check for possible buffer overrun */
	if need > uint64(p.remaining()) {
		p.overflow(int(min(need, uint64(1<<31))))
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection coordinates")
	}

	/* Allocate space for the coordinates (bounded by the image size) */
	coord := make([][]uint64, numElem)
	flat := make([]uint64, numElem*uint64(rank))

	/* Retrieve the coordinates from the buffer */
	for i := range coord {
		pnt := flat[uint64(i)*uint64(rank) : uint64(i+1)*uint64(rank) : uint64(i+1)*uint64(rank)]
		for j := range pnt {
			pnt[j] = p.uintVar(encTypeSize)
		}
		coord[i] = pnt
	}
	if err := p.done(); err != nil {
		return nil, err
	}

	/* Select points */
	sel := h5sNewPointSelection(rank)
	h5sPointAdd(sel, coord)
	return sel, nil
}

// h5sNewPointSelection is the allocation part of H5S_select_elements: an
// empty point selection with the bounding box reset.
func h5sNewPointSelection(rank uint32) *h5sSelection {
	sel := &h5sSelection{typ: h5sSelPoints, rank: rank, unlimDim: -1}

	/* Set the bound box to the default value */
	sel.pntLowBounds = make([]uint64, rank)
	sel.pntHighBounds = make([]uint64, rank)
	for u := range sel.pntLowBounds {
		sel.pntLowBounds[u] = ^uint64(0)
	}
	return sel
}

// h5sSelectElements is H5S_select_elements with H5S_SELECT_APPEND on a
// dataspace being built: append points (copied by the caller) to the
// selection, converting a non-point selection to an empty point list
// first.
func h5sSelectElements(space *h5s, coord [][]uint64) {
	var sel *h5sSelection
	if old := space.sel; old != nil && old.typ == h5sSelPoints {
		cp := *old
		cp.points = append([][]uint64(nil), old.points...)
		cp.pntLowBounds = append([]uint64(nil), old.pntLowBounds...)
		cp.pntHighBounds = append([]uint64(nil), old.pntHighBounds...)
		sel = &cp
	} else {
		/* If we are setting a new selection, remove current selection first */
		sel = h5sNewPointSelection(space.extent.rank)
	}

	/* Add points to selection */
	h5sPointAdd(sel, coord)

	/* Set selection type */
	space.sel = sel
}

// h5sGetSelectElemPointlist is H5S__get_select_elem_pointlist: the points
// startpoint .. startpoint+numpoints-1 in selection order.
func h5sGetSelectElemPointlist(space *h5s, startpoint, numpoints uint64) [][]uint64 {
	sel := space.selection()
	n := uint64(len(sel.points))
	if startpoint >= n {
		return nil
	}
	end := n
	if numpoints < n-startpoint {
		end = startpoint + numpoints
	}
	return sel.points[startpoint:end]
}

// h5sPointBounds is H5S__point_bounds: the bounding box of the points,
// including the selection offset.
func h5sPointBounds(space *h5s) (start, end []uint64, err error) {
	sel := space.selection()
	rank := int(space.extent.rank)
	if len(sel.points) == 0 {
		return nil, nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "point selection has no points")
	}
	start = make([]uint64, rank)
	end = make([]uint64, rank)

	/* Loop over dimensions */
	for u := range rank {
		/* Check for offset moving selection negative */
		if int64(sel.pntLowBounds[u])+sel.selOffset(u) < 0 {
			return nil, nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
		}

		/* Set the low & high bounds in this dimension */
		start[u] = uint64(int64(sel.pntLowBounds[u]) + sel.selOffset(u))
		end[u] = uint64(int64(sel.pntHighBounds[u]) + sel.selOffset(u))
	}
	return start, end, nil
}

// h5sPointOffset is H5S__point_offset: the linear offset of the first
// point (including the selection offset).
func h5sPointOffset(space *h5s) (uint64, error) {
	sel := space.selection()
	if len(sel.points) == 0 {
		return 0, errorf(H5E_DATASPACE, H5E_BADVALUE, "point selection has no points")
	}

	/* Start at linear offset 0 */
	var offset uint64

	/* Set up pointers to arrays of values */
	pnt := sel.points[0]
	dimSize := space.extent.size

	/* Loop through coordinates, calculating the linear offset */
	accum := uint64(1)
	for i := int(space.extent.rank) - 1; i >= 0; i-- {
		pntOffset := int64(pnt[i]) + sel.selOffset(i) /* Point's offset in this dimension */

		/* Check for offset moving selection out of the dataspace */
		if pntOffset < 0 || uint64(pntOffset) >= dimSize[i] {
			return 0, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
		}

		/* Add the point's offset in this dimension to the total linear offset */
		offset += uint64(pntOffset) * accum

		/* Increase the accumulator */
		accum *= dimSize[i]
	}
	return offset, nil
}

// h5sPointUnlimDim is H5S__point_unlim_dim.
func h5sPointUnlimDim(*h5s) int { return -1 }

// h5sPointIsContiguous is H5S__point_is_contiguous: one point is
// definitely contiguous; more than one might be, but that is not checked.
func h5sPointIsContiguous(space *h5s) bool {
	return space.selection().numElem == 1
}

// h5sPointIsSingle is H5S__point_is_single.
func h5sPointIsSingle(space *h5s) bool {
	/* One point is definitely 'single' :-) */
	return space.selection().numElem == 1
}

// h5sPointIsRegular is H5S__point_is_regular.
func h5sPointIsRegular(space *h5s) bool {
	/* Only simple check for regular points for now... */
	return space.selection().numElem == 1
}

// h5sPointShapeSame is H5S__point_shape_same (space1 has the larger rank).
func h5sPointShapeSame(space1, space2 *h5s) bool {
	/* Get dataspace ranks */
	space1Rank := int(space1.extent.rank)
	space2Rank := int(space2.extent.rank)
	pnts1 := space1.selection().points
	pnts2 := space2.selection().points
	if len(pnts1) == 0 || len(pnts2) == 0 {
		return len(pnts1) == len(pnts2)
	}

	offset := make([]int64, space1Rank) /* Offset between the selections */

	/* Initialize dimensions */
	space1Dim := space1Rank - 1
	space2Dim := space2Rank - 1

	/* Look at first point in each selection to compute the offset for common
	 *  dimensions.
	 */
	pnt1 := pnts1[0]
	pnt2 := pnts2[0]
	for space2Dim >= 0 {
		/* Set the relative locations of the selections */
		offset[space1Dim] = int64(pnt2[space2Dim]) - int64(pnt1[space1Dim])

		space1Dim--
		space2Dim--
	}

	/* For dimensions that appear only in space1: */
	for space1Dim >= 0 {
		/* Set the absolute offset of the remaining dimensions */
		offset[space1Dim] = int64(pnt1[space1Dim])

		space1Dim--
	}

	/* Loop over remaining points */
	for i := 1; i < len(pnts1) && i < len(pnts2); i++ {
		pnt1, pnt2 = pnts1[i], pnts2[i]

		/* Initialize dimensions */
		space1Dim = space1Rank - 1
		space2Dim = space2Rank - 1

		/* Compare locations in common dimensions, including relative offset */
		for space2Dim >= 0 {
			if uint64(int64(pnt1[space1Dim])+offset[space1Dim]) != pnt2[space2Dim] {
				return false
			}

			space1Dim--
			space2Dim--
		}

		/* For dimensions that appear only in space1: */
		for space1Dim >= 0 {
			/* Compare the absolute offset in the remaining dimensions */
			if int64(pnt1[space1Dim]) != offset[space1Dim] {
				return false
			}

			space1Dim--
		}
	}
	return true
}

// h5sPointIntersectBlock is H5S__point_intersect_block: whether any point
// lies in the block start..end.
func h5sPointIntersectBlock(space *h5s, start, end []uint64) bool {
	/* Loop over points */
	for _, pnt := range space.selection().points {
		/* Verify that the point is within the block */
		u := 0
		for ; u < int(space.extent.rank); u++ {
			if pnt[u] < start[u] || pnt[u] > end[u] {
				break
			}
		}

		/* Check if point was within block for all dimensions */
		if u == int(space.extent.rank) {
			return true
		}
	}
	return false
}

// h5sPointAdjustU is H5S__point_adjust_u: move the points by subtracting
// an offset. It returns a new dataspace.
func h5sPointAdjustU(space *h5s, offset []uint64) (*h5s, error) {
	soff := make([]int64, len(offset))
	for u, o := range offset {
		soff[u] = int64(o)
	}
	return h5sPointAdjustS(space, soff)
}

// h5sPointAdjustS is H5S__point_adjust_s: move the points by subtracting
// a signed offset. It returns a new dataspace.
func h5sPointAdjustS(space *h5s, offset []int64) (*h5s, error) {
	rank := int(space.extent.rank)
	ret := h5sCopy(space)

	/* Check for an all-zero offset vector */
	nonZeroOffset := false
	for u := range rank {
		if offset[u] != 0 {
			nonZeroOffset = true
			break
		}
	}

	/* Only perform operation if the offset is non-zero */
	if !nonZeroOffset {
		return ret, nil
	}
	old := space.selection()
	sel := *old
	sel.points = make([][]uint64, len(old.points))
	flat := make([]uint64, len(old.points)*rank)

	/* Iterate through the nodes, checking the bounds on each element */
	for i, pnt := range old.points {
		np := flat[i*rank : (i+1)*rank : (i+1)*rank]
		/* Adjust each coordinate for point node */
		for u := range rank {
			/* Check for offset moving selection negative */
			if int64(pnt[u])-offset[u] < 0 {
				return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
			}

			/* Adjust node's coordinate location */
			np[u] = uint64(int64(pnt[u]) - offset[u])
		}
		sel.points[i] = np
	}

	/* update the bound box of the selection */
	sel.pntLowBounds = make([]uint64, rank)
	sel.pntHighBounds = make([]uint64, rank)
	for u := range rank {
		sel.pntLowBounds[u] = uint64(int64(old.pntLowBounds[u]) - offset[u])
		sel.pntHighBounds[u] = uint64(int64(old.pntHighBounds[u]) - offset[u])
	}
	ret.sel = &sel
	return ret, nil
}

// h5sPointProjectScalar is H5S__point_project_scalar: the linear offset of
// the single selected point.
func h5sPointProjectScalar(space *h5s) (uint64, error) {
	sel := space.selection()

	/* Check for more than one point selected */
	if len(sel.points) != 1 {
		return 0, errorf(H5E_DATASPACE, H5E_BADRANGE, "point selection of one element has more than one node!")
	}

	/* Calculate offset of selection in projected buffer */
	return h5vmArrayOffset(space.extent.size, sel.points[0]), nil
}

// h5vmArrayOffset is H5VM_array_offset: the row-major linear offset of
// coordinates in an array of the given dimensions.
func h5vmArrayOffset(size, coord []uint64) uint64 {
	var off uint64
	acc := uint64(1)
	for i := len(size) - 1; i >= 0; i-- {
		off += coord[i] * acc
		acc *= size[i]
	}
	return off
}
