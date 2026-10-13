package hdf5

// Translated from src/H5S.c (read side: dataspace creation in memory and
// extent queries). The dataspace message decoder (src/H5Osdspace.c) is in
// h5o_sdspace.go.

import (
	"math/bits"

	"github.com/crhntr/hdf5/go/h5"
)

// h5sCreate is H5S_create: create a new dataspace of the given class, with
// an "all" selection.
func h5sCreate(typ h5.SpaceClass) *h5s {
	/* Create a new dataspace */
	newDs := &h5s{}

	/* Initialize default dataspace state */
	newDs.extent.typ = typ
	if typ == h5.SpaceNull {
		newDs.extent.version = h5oSdspaceVersion2
	} else {
		newDs.extent.version = h5oSdspaceVersion1
	}
	newDs.extent.rank = 0
	newDs.extent.size, newDs.extent.max = nil, nil

	switch typ {
	case h5.SpaceScalar:
		newDs.extent.nelem = 1
	default:
		newDs.extent.nelem = 0
	}

	/* Start with "all" selection */
	h5sSelectAll(newDs)

	return newDs
}

// h5sGetSimpleExtentNpoints is H5S_get_simple_extent_npoints: the number of
// elements in the extent.
func h5sGetSimpleExtentNpoints(ds *h5s) uint64 {
	/* Get the number of elements in extent */
	return ds.extent.nelem
}

// h5sGetNpointsMax is H5S_get_npoints_max: the maximum number of elements
// the extent can hold (math.MaxUint64 when a dimension is unlimited or the
// product overflows).
func h5sGetNpointsMax(ds *h5s) uint64 {
	switch ds.extent.typ {
	case h5.SpaceNull:
		return 0
	case h5.SpaceScalar:
		return 1
	}
	dims := ds.extent.size
	if ds.extent.max != nil {
		dims = ds.extent.max
	}
	ret := uint64(1)
	for _, d := range dims {
		if d == h5sUnlimited {
			return ^uint64(0)
		}
		hi, lo := bits.Mul64(ret, d)
		if hi != 0 {
			return ^uint64(0)
		}
		ret = lo
	}
	return ret
}

// h5sGetSimpleExtentNdims is H5S_get_simple_extent_ndims.
func h5sGetSimpleExtentNdims(ds *h5s) int {
	return int(ds.extent.rank)
}

// h5sExtentGetDims is H5S_extent_get_dims: the current and maximum
// dimension sizes (nil for scalar and null extents).
func h5sExtentGetDims(ext *h5sExtent) (dims, maxDims []uint64) {
	if ext.typ != h5.SpaceSimple {
		return nil, nil
	}
	dims = append([]uint64(nil), ext.size...)
	if ext.max != nil {
		maxDims = append([]uint64(nil), ext.max...)
	} else {
		maxDims = append([]uint64(nil), ext.size...)
	}
	return dims, maxDims
}

// h5sGetSimpleExtentDims is H5S_get_simple_extent_dims.
func h5sGetSimpleExtentDims(ds *h5s) (dims, maxDims []uint64) {
	return h5sExtentGetDims(&ds.extent)
}

// h5sSetExtentSimple is H5S_set_extent_simple: set the extent of a
// dataspace being built in memory. A nil max means "same as dims". The
// selection offset is reset and an "all" selection is kept.
func h5sSetExtentSimple(space *h5s, rank uint32, dims, max []uint64) error {
	/* Check args */
	if rank > h5sMaxRank {
		return errorf(H5E_DATASPACE, H5E_BADRANGE, "dataspace rank too large: %d", rank)
	}
	if uint32(len(dims)) < rank || (max != nil && uint32(len(max)) < rank) {
		return errorf(H5E_DATASPACE, H5E_BADVALUE, "dimension arrays shorter than rank %d", rank)
	}

	/* shift out of the previous state to a "simple" dataspace.  */
	space.extent.size, space.extent.max = nil, nil

	if rank == 0 { /* scalar variable */
		space.extent.typ = h5.SpaceScalar
		space.extent.nelem = 1
		space.extent.rank = 0 /* set to scalar rank */
	} else {
		space.extent.typ = h5.SpaceSimple

		/* Set the rank and allocate space for the dims */
		space.extent.rank = rank
		space.extent.size = append([]uint64(nil), dims[:rank]...)

		/* Copy the dimensions & compute the number of elements in the extent */
		nelem := uint64(1)
		for _, d := range space.extent.size {
			hi, lo := bits.Mul64(nelem, d)
			if hi != 0 {
				return errorf(H5E_DATASPACE, H5E_OVERFLOW, "number of elements in dataspace overflows")
			}
			nelem = lo
		}
		space.extent.nelem = nelem

		/* Copy the maximum dimensions if specified. Otherwise, the maximal dimensions are the
		 * same as the dimension */
		if max != nil {
			space.extent.max = append([]uint64(nil), max[:rank]...)
		} else {
			space.extent.max = append([]uint64(nil), dims[:rank]...)
		}
	}

	/* Selection related cleanup */
	/* Set offset to zeros */
	if space.sel != nil {
		space.sel.offset = nil
		space.sel.offsetChanged = false
	}

	/* If the selection is 'all', update the number of elements selected */
	if h5sGetSelectType(space) == h5sSelAll {
		h5sSelectAll(space)
	}
	return nil
}

// h5sCreateSimple is H5S_create_simple: create a simple dataspace with an
// "all" selection. maxdims may be nil.
func h5sCreateSimple(dims, maxdims []uint64) (*h5s, error) {
	/* Check arguments */
	if len(dims) > h5sMaxRank {
		return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "dataspace rank too large: %d", len(dims))
	}
	if maxdims != nil && len(maxdims) != len(dims) {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "maxdims rank does not match dims")
	}

	/* Create the space and set the extent */
	ret := h5sCreate(h5.SpaceSimple)
	if err := h5sSetExtentSimple(ret, uint32(len(dims)), dims, maxdims); err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTINIT, "can't set dimensions")
	}
	return ret, nil
}

// h5sFromExtent builds an in-memory dataspace with an "all" selection from
// a decoded extent (H5S_read: a dataspace message read from an object
// header gets the default "all" selection). The extent is copied.
func h5sFromExtent(ext *h5sExtent) (*h5s, error) {
	switch ext.typ {
	case h5.SpaceScalar, h5.SpaceNull:
		ds := h5sCreate(ext.typ)
		ds.extent.version = ext.version
		return ds, nil
	case h5.SpaceSimple:
		ds, err := h5sCreateSimple(ext.size, ext.max)
		if err != nil {
			return nil, err
		}
		ds.extent.version = ext.version
		/* (the decoded extent is kept as is (H5S_read): no maximum
		 *  dimensions when the message stores none) */
		if ext.max == nil {
			ds.extent.max = nil
		}
		if ext.rank != uint32(len(ext.size)) {
			return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "extent rank %d does not match %d dimensions", ext.rank, len(ext.size))
		}
		return ds, nil
	}
	return nil, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "unknown dataspace class %d", ext.typ)
}

// h5sNew attaches a selection (for example one decoded with
// h5sSelectDeserialize) to a copy of a decoded extent, verifying that the
// selection was made for a dataspace of the extent's rank (the check
// H5S_select_deserialize makes when it is given a dataspace).
func h5sNew(ext *h5sExtent, sel *h5sSelection) (*h5s, error) {
	ds, err := h5sFromExtent(ext)
	if err != nil {
		return nil, err
	}
	if sel == nil {
		return ds, nil
	}
	switch sel.typ {
	case h5sSelAll, h5sSelNone:
		cp := *sel
		cp.rank = ds.extent.rank
		ds.sel = &cp
	case h5sSelPoints, h5sSelHyperslabs:
		if sel.rank != ds.extent.rank {
			return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "rank of serialized selection does not match dataspace")
		}
		ds.sel = sel
	default:
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "invalid selection type %d", sel.typ)
	}
	return ds, nil
}

// h5sGetSimpleExtentType is H5S_get_simple_extent_type.
func h5sGetSimpleExtentType(space *h5s) h5.SpaceClass { return space.extent.typ }

// h5sExtentEqual is H5S_extent_equal: whether two dataspaces have the same
// extent (class, rank, current and maximum dimensions).
func h5sExtentEqual(ds1, ds2 *h5s) bool {
	/* Make certain the dataspaces are the same type */
	if ds1.extent.typ != ds2.extent.typ {
		return false
	}

	/* Make certain the dataspaces are the same rank */
	if ds1.extent.rank != ds2.extent.rank {
		return false
	}

	/* Make certain the dataspaces' current dimensions are the same size */
	if ds1.extent.rank > 0 {
		for u := range ds1.extent.rank {
			if ds1.extent.size[u] != ds2.extent.size[u] {
				return false
			}
		}
	}

	/* Make certain the dataspaces' maximum dimensions are the same size */
	if ds1.extent.rank > 0 {
		/* Check for no maximum dimensions on dataspaces */
		if ds1.extent.max != nil && ds2.extent.max != nil {
			for u := range ds1.extent.rank {
				if ds1.extent.max[u] != ds2.extent.max[u] {
					return false
				}
			}
		} else if (ds1.extent.max == nil) != (ds2.extent.max == nil) {
			return false
		}
	}
	return true
}

// h5sExtentNelem is H5S_extent_nelem.
func h5sExtentNelem(ext *h5sExtent) uint64 {
	/* Return the number of elements in extent */
	return ext.nelem
}
