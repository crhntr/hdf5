package hdf5

// In-core dataspace types (H5Spkg.h), shared between the dataspace module
// (h5s*.go) and its users.

import "github.com/HDFGroup/hdf5/go/h5"

// H5S_MAX_RANK
const h5sMaxRank = 32

// H5S_UNLIMITED (also HSIZE_UNDEF)
const h5sUnlimited = ^uint64(0)

// H5S_MAX_SIZE: maximum dimension size (highest value that is not a special
// value e.g. H5S_UNLIMITED)
const h5sMaxSize = ^uint64(0) - 1

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

// h5sHyperDim is H5S_hyper_dim_t: regular hyperslab information for one
// dimension.
type h5sHyperDim struct {
	start  uint64
	stride uint64
	count  uint64
	block  uint64
}

// h5sHyperSpan is H5S_hyper_span_t: the elements low..high (inclusive) of
// one dimension, with the spans of the next dimension down selected for
// each of them. The C "next" pointer is the position in the owning
// h5sHyperSpanInfo.spans slice.
type h5sHyperSpan struct {
	low, high uint64            /* Low & high bounds of elements selected for span, inclusive */
	down      *h5sHyperSpanInfo /* Spans in next dimension down (nil in the fastest dimension) */
}

// h5sHyperSpanInfo is H5S_hyper_span_info_t: the (sorted, disjoint,
// non-empty) list of spans in one dimension. Span infos may be shared
// between spans (the C reference count) and are never modified once built.
type h5sHyperSpanInfo struct {
	/* The bounding box of this set of spans and all lower dimensions.
	 * (NOTE: The bounds arrays are _relative_ to the depth of the span_info
	 *        node in the span tree, so index 0 is "this" dimension.)
	 */
	lowBounds  []uint64 /* The smallest element selected in each dimension */
	highBounds []uint64 /* The largest element selected in each dimension */

	spans []h5sHyperSpan /* head .. tail */
}

// h5sSelection is the selection part of H5S_t (H5S_select_t together with
// the per-type H5S_hyper_sel_t / H5S_pnt_list_t). A selection is immutable
// once built: the operations that modify a selection in C return a new one
// here.
type h5sSelection struct {
	typ  int    /* H5S_SEL_* */
	rank uint32 /* rank of the dataspace the selection was made for (0 for all/none decoded without a dataspace) */

	offset        []int64 /* Offset within the extent (nil: all zero) */
	offsetChanged bool    /* Indicate that the offset for the selection has been changed */

	numElem uint64 /* Number of elements in selection (H5S_UNLIMITED for unlimited selections; unused for "all") */

	// For H5S_SEL_HYPERSLABS: either a regular hyperslab (diminfo_valid ==
	// H5S_DIMINFO_VALID_YES) ...
	regular bool
	// ... with the "application" (app) form of the diminfo, as set by
	// H5Sselect_hyperslab or rebuilt from the span tree ...
	start  []uint64
	stride []uint64
	count  []uint64
	block  []uint64
	// ... and the optimized (opt) form with its bounding box.
	opt        []h5sHyperDim
	lowBounds  []uint64
	highBounds []uint64
	// The span tree (H5S_hyper_sel_t.span_lst): always set for irregular
	// selections; may be nil for regular ones (generated on demand).
	spanLst *h5sHyperSpanInfo
	// For irregular hyperslabs decoded with h5sSelectDeserialize: the list
	// of blocks {start, end} (end inclusive) in span tree order, as
	// H5Sget_select_hyper_blocklist returns them (nil for selections built
	// otherwise; see h5sHyperListBlocks).
	blocks [][2][]uint64
	// Unlimited dimension for VDS selections (H5S_UNLIMITED-sized
	// count/block), or -1.
	unlimDim        int
	numElemNonUnlim uint64 /* # of elements in a "slice" excluding the unlimited dimension */

	// For H5S_SEL_POINTS: the points in selection order, and their
	// bounding box.
	points        [][]uint64
	pntLowBounds  []uint64
	pntHighBounds []uint64
}

// h5s is H5S_t: extent plus selection.
type h5s struct {
	extent h5sExtent
	sel    *h5sSelection // nil means "all"
}
