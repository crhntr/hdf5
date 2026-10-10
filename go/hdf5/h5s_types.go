package hdf5

// In-core dataspace types (H5Spkg.h), shared between the dataspace module
// (h5s*.go) and its users.

import "github.com/HDFGroup/hdf5/go/h5"

// H5S_MAX_RANK
const h5sMaxRank = 32

// h5sExtent is H5S_extent_t.
type h5sExtent struct {
	typ     h5.SpaceClass /* Type of extent */
	version uint8
	nelem   uint64   /* Number of elements in extent */
	rank    uint32   /* Number of dimensions */
	size    []uint64 /* Current size of the dimensions */
	max     []uint64 /* Maximum size of the dimensions (nil: same as size) */
}

// public converts to the shared vocabulary.
func (e *h5sExtent) public() h5.Dataspace {
	ds := h5.Dataspace{Class: e.typ, Version: e.version}
	if e.typ == h5.SpaceSimple {
		ds.Dims = append([]uint64(nil), e.size...)
		if e.max != nil {
			ds.MaxDims = append([]uint64(nil), e.max...)
		}
	}
	return ds
}

// H5S_sel_type
const (
	h5sSelError      = -1
	h5sSelNone       = 0
	h5sSelPoints     = 1
	h5sSelHyperslabs = 2
	h5sSelAll        = 3
)

// h5sSelection is the selection part of H5S_t, in a form that is
// convenient for reading: a list of blocks for hyperslabs ("span" form is
// normalized into blocks), a list of points for point selections.
type h5sSelection struct {
	typ  int /* H5S_SEL_* */
	rank uint32
	// For H5S_SEL_HYPERSLABS: either a regular hyperslab (diminfo valid) ...
	regular bool
	start   []uint64
	stride  []uint64
	count   []uint64
	block   []uint64
	// ... or an irregular list of blocks {start, end} (end inclusive).
	blocks [][2][]uint64
	// For H5S_SEL_POINTS.
	points [][]uint64
	// Unlimited dimension for VDS selections (H5S_UNLIMITED-sized count/block).
	unlimDim int
}

// h5s is H5S_t: extent plus selection.
type h5s struct {
	extent h5sExtent
	sel    *h5sSelection // nil means "all"
}
