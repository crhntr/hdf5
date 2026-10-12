package hdf5

// Translated from src/H5Shyper.c (read side).
//
// Hyperslab selections keep the C library's two forms: the regular
// "diminfo" form (start/stride/count/block per dimension, in the
// application and optimized variants) and the span tree
// (H5S_hyper_span_info_t / H5S_hyper_span_t, with the span linked lists as
// slices). Span trees are immutable once built and share their lower
// dimensions freely.
//
// Not translated: the selection combination machinery (H5S__fill_in_new_space,
// H5S__hyper_clip_spans, H5S__hyper_merge_spans, H5S__hyper_update_diminfo,
// H5S_combine_hyperslab, ...). The irregular forms of the serialized
// selection, which the C library decodes by OR-ing blocks one at a time
// into the selection, are decoded by building the span tree of the union of
// the blocks directly (h5sHyperSpansFromBlocks) and then attempting
// H5S__hyper_rebuild, which yields the same span tree and the same regular
// description. Likewise H5S__hyper_project_intersection is implemented by
// walking the source/intersect span trees to element index ranges and the
// destination span tree to the projected spans, instead of the C
// state-machine (H5S__hyper_proj_int_iterate / _build_proj).

import (
	"math/bits"
	"sort"

	"github.com/crhntr/hdf5/go/h5"
)

// H5S_HYPER_VERSION_*
const (
	h5sHyperVersion1      = 1
	h5sHyperVersion2      = 2
	h5sHyperVersion3      = 3
	h5sHyperVersionLatest = h5sHyperVersion3
)

// Flags for serialization of selections
const (
	h5sHyperRegular   = 0x01
	h5sSelectFlagBits = h5sHyperRegular
)

// H5S_UINT16_MAX / H5S_UINT32_MAX / H5S_UINT64_MAX
const (
	h5sUint16Max = 0x0000FFFF
	h5sUint32Max = 0xFFFFFFFF
	h5sUint64Max = ^uint64(0)
)

// h5sHyperMaxSpans bounds the number of span nodes built for one span tree
// (regular selections expanded to spans, unions of decoded blocks,
// projections), so that corrupt selections cannot exhaust memory.
const h5sHyperMaxSpans = 1 << 24

// h5sHyperMaxBlocks bounds the number of blocks of an irregular selection
// listed in h5sSelection.blocks.
const h5sHyperMaxBlocks = 1 << 22

/*
 * Span tree construction helpers
 */

// h5sHyperNewSpanInfo is H5S__hyper_new_span_info plus the bounds
// computation done by its callers: wrap a non-empty, sorted list of spans
// in a span info with the bounding box of the tree.
func h5sHyperNewSpanInfo(spans []h5sHyperSpan) *h5sHyperSpanInfo {
	depth := 1
	if spans[0].down != nil {
		depth += len(spans[0].down.lowBounds)
	}
	info := &h5sHyperSpanInfo{
		lowBounds:  make([]uint64, depth),
		highBounds: make([]uint64, depth),
		spans:      spans,
	}

	/* Set the low & high bounds for this dimension */
	info.lowBounds[0] = spans[0].low
	info.highBounds[0] = spans[len(spans)-1].high

	/* Copy bounds from lower dimensions */
	if depth > 1 {
		for u := 1; u < depth; u++ {
			info.lowBounds[u] = ^uint64(0)
		}
		var prev *h5sHyperSpanInfo
		for _, sp := range spans {
			if sp.down == nil || sp.down == prev {
				continue
			}
			prev = sp.down
			for u := 1; u < depth && u-1 < len(sp.down.lowBounds); u++ {
				info.lowBounds[u] = min(info.lowBounds[u], sp.down.lowBounds[u-1])
				info.highBounds[u] = max(info.highBounds[u], sp.down.highBounds[u-1])
			}
		}
	}
	return info
}

// h5sHyperSpanBuilder accumulates the spans of one dimension in increasing
// order (the span_tree argument of H5S__hyper_append_span).
type h5sHyperSpanBuilder struct {
	spans  []h5sHyperSpan
	budget *int // remaining span nodes (shared by a whole tree build)
}

// appendSpan is H5S__hyper_append_span: append the span low..high with the
// given down tree, extending the previous span instead when it ends just
// before low and has the same down tree.
func (b *h5sHyperSpanBuilder) appendSpan(low, high uint64, down *h5sHyperSpanInfo) error {
	/* Check if span can just extend the previous merged span */
	if n := len(b.spans); n > 0 {
		tail := &b.spans[n-1]
		if tail.high+1 == low && h5sHyperCmpSpans(down, tail.down) {
			/* Extend previous merged span to include new high bound */
			tail.high = high
			return nil
		}
		/* Share the previous span's down span tree if they are the same */
		if down != nil && tail.down != nil && down != tail.down && h5sHyperCmpSpans(down, tail.down) {
			down = tail.down
		}
	}
	if b.budget != nil {
		if *b.budget <= 0 {
			return errorf(H5E_DATASPACE, H5E_CANTALLOC, "too many hyperslab spans")
		}
		*b.budget--
	}

	/* Append to end of merged spans list */
	b.spans = append(b.spans, h5sHyperSpan{low: low, high: high, down: down})
	return nil
}

// finish returns the span info for the appended spans, or nil when there
// are none.
func (b *h5sHyperSpanBuilder) finish() *h5sHyperSpanInfo {
	if len(b.spans) == 0 {
		return nil
	}
	return h5sHyperNewSpanInfo(b.spans)
}

// h5sHyperCmpSpans is H5S__hyper_cmp_spans: whether two span trees describe
// the same selection.
func h5sHyperCmpSpans(spanInfo1, spanInfo2 *h5sHyperSpanInfo) bool {
	/* Check for redundant comparison (or both spans being NULL) */
	if spanInfo1 == spanInfo2 {
		return true
	}

	/* Check for one span being NULL */
	if spanInfo1 == nil || spanInfo2 == nil {
		return false
	}

	/* Compare low & high bounds for this span list */
	if spanInfo1.lowBounds[0] != spanInfo2.lowBounds[0] || spanInfo1.highBounds[0] != spanInfo2.highBounds[0] {
		return false
	}
	if len(spanInfo1.spans) != len(spanInfo2.spans) {
		return false
	}
	for i := range spanInfo1.spans {
		span1, span2 := &spanInfo1.spans[i], &spanInfo2.spans[i]

		/* Check if the actual low & high span information is the same */
		if span1.low != span2.low || span1.high != span2.high {
			return false
		}
		if span1.down != nil || span2.down != nil {
			if !h5sHyperCmpSpans(span1.down, span2.down) {
				return false
			}
		}
	}
	return true
}

// h5sHyperSpansNelem is H5S__hyper_spans_nelem: the number of elements in
// a span tree. ok is false when the count overflows.
func h5sHyperSpansNelem(spans *h5sHyperSpanInfo) (n uint64, ok bool) {
	memo := map[*h5sHyperSpanInfo]uint64{}
	return h5sHyperSpansNelemHelper(spans, memo)
}

// h5sHyperSpansNelemHelper is H5S__hyper_spans_nelem_helper (the C
// op_gen / op_info memoization of shared subtrees is a map here).
func h5sHyperSpansNelemHelper(spans *h5sHyperSpanInfo, memo map[*h5sHyperSpanInfo]uint64) (uint64, bool) {
	if n, found := memo[spans]; found {
		return n, true
	}
	var ret uint64
	for i := range spans.spans {
		span := &spans.spans[i]
		width := span.high - span.low + 1
		if span.down != nil {
			nelem, ok := h5sHyperSpansNelemHelper(span.down, memo)
			if !ok {
				return 0, false
			}
			hi, lo := bits.Mul64(width, nelem)
			if hi != 0 {
				return 0, false
			}
			width = lo
		}
		var carry uint64
		ret, carry = bits.Add64(ret, width, 0)
		if carry != 0 {
			return 0, false
		}
	}
	memo[spans] = ret
	return ret, true
}

// h5sHyperSpanNblocks is H5S__hyper_span_nblocks: the number of blocks in
// a span tree (saturating at the maximum uint64).
func h5sHyperSpanNblocks(spans *h5sHyperSpanInfo) uint64 {
	memo := map[*h5sHyperSpanInfo]uint64{}
	var helper func(*h5sHyperSpanInfo) uint64
	helper = func(spans *h5sHyperSpanInfo) uint64 {
		if n, found := memo[spans]; found {
			return n
		}
		var ret uint64
		for i := range spans.spans {
			span := &spans.spans[i]
			if span.down != nil {
				n := helper(span.down)
				ret += n
				if ret < n {
					ret = ^uint64(0)
				}
			} else {
				ret++
			}
		}
		memo[spans] = ret
		return ret
	}
	if spans == nil {
		return 0
	}
	return helper(spans)
}

// h5sHyperMakeSpans is H5S__hyper_make_spans: build the span tree of a
// regular hyperslab. Lower dimensions are shared between all spans of a
// dimension.
func h5sHyperMakeSpans(rank int, start, stride, count, block []uint64) (*h5sHyperSpanInfo, error) {
	var down *h5sHyperSpanInfo
	total := 0

	/* Start creating spans in fastest changing dimension */
	for i := rank - 1; i >= 0; i-- {
		/* Sanity check */
		if count[i] == 0 {
			return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "count == 0 is invalid")
		}
		if count[i] > h5sHyperMaxSpans || total+int(count[i]) > h5sHyperMaxSpans {
			return nil, errorf(H5E_DATASPACE, H5E_CANTALLOC, "too many hyperslab spans (count %d)", count[i])
		}
		if block[i] == 0 || block[i] == h5sUnlimited || count[i] == h5sUnlimited {
			return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "invalid hyperslab block")
		}
		total += int(count[i])

		/* Generate all the span segments for this dimension */
		spans := make([]h5sHyperSpan, count[i])
		currLow := start[i]
		for u := range spans {
			currHigh, ok := h5sMulAdd(1, block[i]-1, currLow)
			if !ok {
				return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "hyperslab selection out of range")
			}
			/* Set the span's basic information */
			spans[u] = h5sHyperSpan{low: currLow, high: currHigh, down: down}
			if u+1 < len(spans) {
				next, ok := h5sMulAdd(1, stride[i], currLow)
				if !ok {
					return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "hyperslab selection out of range")
				}
				currLow = next
			}
		}

		/* Keep the pointer to the next dimension down's completed list */
		down = h5sHyperNewSpanInfo(spans)
	}

	/* Success!  Return the head of the list in the slowest changing dimension */
	return down, nil
}

// h5sHyperSpansFromBlocks builds the span tree of the union of a list of
// blocks {start, end} (end inclusive) of the given rank. It replaces the
// sequence of H5S_select_hyperslab(H5S_SELECT_OR) calls the C library makes
// when deserializing an irregular hyperslab selection (and
// H5S_hyper_add_span_element when converting points), producing the same,
// fully merged, span tree. It returns nil for an empty list.
func h5sHyperSpansFromBlocks(rank int, blocks [][2][]uint64) (*h5sHyperSpanInfo, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	budget := h5sHyperMaxSpans
	idx := make([]int, len(blocks))
	for i := range idx {
		idx[i] = i
	}
	return h5sHyperSpansFromBlocksHelper(rank, 0, blocks, idx, &budget)
}

// h5sHyperSpansFromBlocksHelper builds dimension dim of the union of the
// blocks listed in idx: it cuts the dimension at every block boundary and
// builds the lower dimensions from the blocks covering each piece.
func h5sHyperSpansFromBlocksHelper(rank, dim int, blocks [][2][]uint64, idx []int, budget *int) (*h5sHyperSpanInfo, error) {
	/* Collect the boundaries of the blocks in this dimension: every block
	 * covers whole pieces between consecutive boundaries. */
	bounds := make([]uint64, 0, 2*len(idx))
	for _, i := range idx {
		bounds = append(bounds, blocks[i][0][dim], blocks[i][1][dim]+1)
	}
	sort.Slice(bounds, func(a, b int) bool { return bounds[a] < bounds[b] })
	uniq := bounds[:0]
	for _, v := range bounds {
		if len(uniq) == 0 || uniq[len(uniq)-1] != v {
			uniq = append(uniq, v)
		}
	}
	bounds = uniq

	/* Sweep the pieces in increasing order, tracking the covering blocks */
	byStart := append([]int(nil), idx...)
	sort.Slice(byStart, func(a, b int) bool { return blocks[byStart[a]][0][dim] < blocks[byStart[b]][0][dim] })
	var active []int
	next := 0
	b := h5sHyperSpanBuilder{budget: budget}
	for k := 0; k+1 < len(bounds); k++ {
		low, high := bounds[k], bounds[k+1]-1

		/* Drop blocks that ended, add blocks that start here */
		keep := active[:0]
		for _, i := range active {
			if blocks[i][1][dim] >= low {
				keep = append(keep, i)
			}
		}
		active = keep
		for next < len(byStart) && blocks[byStart[next]][0][dim] == low {
			active = append(active, byStart[next])
			next++
		}
		if len(active) == 0 {
			continue
		}

		/* Build the lower dimensions for this piece */
		var down *h5sHyperSpanInfo
		if dim+1 < rank {
			sub := append([]int(nil), active...)
			var err error
			if down, err = h5sHyperSpansFromBlocksHelper(rank, dim+1, blocks, sub, budget); err != nil {
				return nil, err
			}
		}
		if err := b.appendSpan(low, high, down); err != nil {
			return nil, err
		}
	}
	return b.finish(), nil
}

/*
 * Iteration
 */

// h5sHyperIterRuns walks a (valid, limited) hyperslab selection in
// row-major order and calls emit(off, n) with runs of contiguous elements
// (the work of H5S__hyper_iter_get_seq_list_opt for regular selections,
// flattening the trailing dimensions that are selected entirely, and of
// H5S__hyper_iter_get_seq_list_gen for span trees).
func h5sHyperIterRuns(space *h5s, emit func(off, n uint64) bool) bool {
	sel := space.selection()
	rank := int(space.extent.rank)
	if rank == 0 || sel.unlimDim >= 0 {
		return true
	}
	size := space.extent.size
	pitch := h5sPitches(&space.extent)

	if sel.regular {
		opt := sel.opt

		/* Check for any "contiguous" blocks that can be flattened */
		flat := rank - 1
		for flat > 0 && opt[flat].count == 1 && opt[flat].start == 0 && sel.selOffset(flat) == 0 && opt[flat].block == size[flat] {
			flat--
		}

		var walk func(d int, base uint64) bool
		walk = func(d int, base uint64) bool {
			od := &opt[d]
			off := sel.selOffset(d)
			for i := uint64(0); i < od.count; i++ {
				s := uint64(int64(od.start+i*od.stride) + off)
				if d == flat {
					if !emit(base+s*pitch[d], od.block*pitch[d]) {
						return false
					}
					continue
				}
				for j := uint64(0); j < od.block; j++ {
					if !walk(d+1, base+(s+j)*pitch[d]) {
						return false
					}
				}
			}
			return true
		}
		return walk(0, 0)
	}

	var walk func(spans *h5sHyperSpanInfo, d int, base uint64) bool
	walk = func(spans *h5sHyperSpanInfo, d int, base uint64) bool {
		off := sel.selOffset(d)
		for i := range spans.spans {
			span := &spans.spans[i]
			low := uint64(int64(span.low) + off)
			if span.down == nil {
				if !emit(base+low*pitch[d], (span.high-span.low+1)*pitch[d]) {
					return false
				}
				continue
			}
			for x := uint64(0); x <= span.high-span.low; x++ {
				if !walk(span.down, d+1, base+(low+x)*pitch[d]) {
					return false
				}
			}
		}
		return true
	}
	if sel.spanLst == nil {
		return true
	}
	return walk(sel.spanLst, 0, 0)
}

// h5sHyperIterPoints yields the coordinates of the selected elements in
// row-major order (H5S__hyper_iter_coords / H5S__hyper_iter_next).
func h5sHyperIterPoints(space *h5s, yield func([]uint64) bool) {
	sel := space.selection()
	rank := int(space.extent.rank)
	if rank == 0 || sel.unlimDim >= 0 {
		return
	}
	coords := make([]uint64, rank)

	if sel.regular {
		opt := sel.opt
		var walk func(d int) bool
		walk = func(d int) bool {
			od := &opt[d]
			off := sel.selOffset(d)
			for i := uint64(0); i < od.count; i++ {
				s := od.start + i*od.stride
				for j := uint64(0); j < od.block; j++ {
					coords[d] = uint64(int64(s+j) + off)
					if d+1 == rank {
						if !yield(coords) {
							return false
						}
					} else if !walk(d + 1) {
						return false
					}
				}
			}
			return true
		}
		walk(0)
		return
	}

	var walk func(spans *h5sHyperSpanInfo, d int) bool
	walk = func(spans *h5sHyperSpanInfo, d int) bool {
		off := sel.selOffset(d)
		for i := range spans.spans {
			span := &spans.spans[i]
			for x := span.low; ; x++ {
				coords[d] = uint64(int64(x) + off)
				if span.down == nil {
					if !yield(coords) {
						return false
					}
				} else if !walk(span.down, d+1) {
					return false
				}
				if x == span.high {
					break
				}
			}
		}
		return true
	}
	if sel.spanLst != nil {
		walk(sel.spanLst, 0)
	}
}

// h5sHyperIterBlocks yields the blocks of the selection the way the
// hyperslab iterator's iter_block / iter_next_block walk them (without
// flattening): the diminfo blocks in row-major order for a regular
// selection, the span tree blocks otherwise.
func h5sHyperIterBlocks(space *h5s, yield func([]uint64, []uint64) bool) {
	sel := space.selection()
	rank := int(space.extent.rank)
	if rank == 0 || sel.unlimDim >= 0 {
		return
	}
	if sel.regular {
		opt := sel.opt
		idx := make([]uint64, rank)
		for {
			start := make([]uint64, rank)
			end := make([]uint64, rank)
			for u := range rank {
				start[u] = opt[u].start + idx[u]*opt[u].stride
				end[u] = start[u] + opt[u].block - 1
			}
			if !yield(start, end) {
				return
			}
			d := rank - 1
			for ; d >= 0; d-- {
				idx[d]++
				if idx[d] < opt[d].count {
					break
				}
				idx[d] = 0
			}
			if d < 0 {
				return
			}
		}
	}
	if sel.spanLst != nil {
		start := make([]uint64, rank)
		end := make([]uint64, rank)
		h5sHyperSpanBlocklist(sel.spanLst, start, end, 0, func(s, e []uint64) bool {
			return yield(append([]uint64(nil), s...), append([]uint64(nil), e...))
		})
	}
}

/*
 * Serialization
 */

// h5sHyperDeserialize is H5S__hyper_deserialize: decode a hyperslab
// selection (versions 1, 2 and 3, after the selection type).
func h5sHyperDeserialize(p *image) (*h5sSelection, error) {
	var (
		flags   uint8
		encSize uint8
	)

	/* Decode version */
	if p.overflow(4) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection version")
	}
	version := p.uint32()

	if version < h5sHyperVersion1 || version > h5sHyperVersionLatest {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "bad version number for hyperslab selection")
	}

	if version >= h5sHyperVersion2 {
		/* Decode flags */
		if p.overflow(1) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection flags")
		}
		flags = p.uint8()

		if version >= h5sHyperVersion3 {
			/* decode size of offset info */
			if p.overflow(1) {
				return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection encoding size")
			}
			encSize = p.uint8()
		} else {
			/* Skip over the remainder of the header */
			if p.overflow(4) {
				return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection header")
			}
			p.skip(4)
			encSize = h5sSelectInfoEncSize8
		}

		/* Check for unknown flags */
		if flags&^h5sSelectFlagBits != 0 {
			return nil, errorf(H5E_DATASPACE, H5E_CANTLOAD, "unknown flag for selection")
		}
	} else {
		/* Skip over the remainder of the header */
		if p.overflow(8) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection header")
		}
		p.skip(8)
		encSize = h5sSelectInfoEncSize4
	}

	/* Check encoded */
	if encSize&^h5sSelectInfoEncSizeBits != 0 {
		return nil, errorf(H5E_DATASPACE, H5E_CANTLOAD, "unknown size of point/offset info for selection")
	}
	var n int
	switch encSize {
	case h5sSelectInfoEncSize2:
		n = 2
	case h5sSelectInfoEncSize4:
		n = 4
	case h5sSelectInfoEncSize8:
		n = 8
	default:
		return nil, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "unknown offset info size for hyperslab")
	}
	/* The "unlimited" marker for this encoding size */
	unlimMark := map[int]uint64{2: h5sUint16Max, 4: h5sUint32Max, 8: h5sUint64Max}[n]

	/* Decode the rank of the point selection */
	if p.overflow(4) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection rank")
	}
	rank := p.uint32()
	if rank == 0 || rank > h5sMaxRank {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "invalid rank (%d) for serialized hyperslab selection", rank)
	}

	if flags&h5sHyperRegular != 0 {
		start := make([]uint64, rank)
		stride := make([]uint64, rank)
		count := make([]uint64, rank)
		block := make([]uint64, rank)

		/* Decode start/stride/block/count */
		for u := range rank {
			if p.overflow(4 * n) {
				return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection ranks")
			}
			start[u] = p.uintVar(n)
			stride[u] = p.uintVar(n)
			count[u] = p.uintVar(n)
			if count[u] == unlimMark {
				count[u] = h5sUnlimited
			}
			block[u] = p.uintVar(n)
			if block[u] == unlimMark {
				block[u] = h5sUnlimited
			}
		}

		/* Select the hyperslab to the current selection */
		space := &h5s{extent: h5sExtent{typ: h5.SpaceSimple, rank: rank, size: make([]uint64, rank)}}
		if err := h5sSelectHyperslab(space, start, stride, count, block); err != nil {
			return nil, wrapf(err, H5E_DATASPACE, H5E_CANTSET, "can't change selection")
		}
		return space.sel, nil
	}

	/* Decode the number of blocks */
	if p.overflow(n) {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding number of selection blocks")
	}
	numElem := p.uintVar(n)

	/* Bound the allocation by the size of the image */
	perBlock := uint64(rank) * 2 * uint64(n)
	if numElem > uint64(p.remaining())/perBlock {
		p.overflow(p.remaining() + 1)
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection coordinates")
	}

	/* Retrieve the coordinates from the buffer */
	blocks := make([][2][]uint64, 0, numElem)
	flat := make([]uint64, numElem*2*uint64(rank))
	for u := uint64(0); u < numElem; u++ {
		/* Decode the starting and ending points */
		if p.overflow(int(perBlock)) {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding selection coordinates")
		}
		bs := flat[u*2*uint64(rank) : (u*2+1)*uint64(rank) : (u*2+1)*uint64(rank)]
		be := flat[(u*2+1)*uint64(rank) : (u*2+2)*uint64(rank) : (u*2+2)*uint64(rank)]
		for v := range bs {
			bs[v] = p.uintVar(n)
		}
		for v := range be {
			be[v] = p.uintVar(n)
		}

		/* Change the ending points into blocks; a zero-sized block leaves
		 * the selection unchanged (H5S_SELECT_OR) */
		empty := false
		for v := range bs {
			blk := be[v] - bs[v] + 1
			if blk == 0 {
				empty = true
			} else if be[v] < bs[v] || be[v] == h5sUnlimited {
				return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "hyperslab block end before start")
			}
		}
		if !empty {
			blocks = append(blocks, [2][]uint64{bs, be})
		}
	}

	/* Select or add the hyperslabs to the current selection */
	spans, err := h5sHyperSpansFromBlocks(int(rank), blocks)
	if err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTSET, "can't change selection")
	}
	sel, err := h5sHyperSelectionFromSpans(rank, spans)
	if err == nil {
		err = h5sHyperListBlocks(sel)
	}
	if err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTSET, "can't change selection")
	}
	return sel, nil
}

// h5sHyperSelectionFromSpans makes the selection of a span tree (none when
// spans is nil), with its element count, attempting to rebuild the regular
// form (H5S__hyper_rebuild) and listing the blocks of an irregular one.
func h5sHyperSelectionFromSpans(rank uint32, spans *h5sHyperSpanInfo) (*h5sSelection, error) {
	if spans == nil {
		return &h5sSelection{typ: h5sSelNone, rank: rank, unlimDim: -1}, nil
	}
	if uint32(len(spans.lowBounds)) != rank {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "span tree depth does not match rank")
	}
	nelem, ok := h5sHyperSpansNelem(spans)
	if !ok {
		return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "number of elements in hyperslab selection overflows")
	}
	sel := &h5sSelection{
		typ:      h5sSelHyperslabs,
		rank:     rank,
		numElem:  nelem,
		spanLst:  spans,
		unlimDim: -1,
	}

	/* Attempt to build "optimized" start/stride/count/block information
	 * from resulting hyperslab span tree.
	 */
	h5sHyperRebuild(sel)
	return sel, nil
}

// h5sHyperListBlocks fills in the block list (h5sSelection.blocks) of an
// irregular hyperslab selection, as H5Sget_select_hyper_blocklist would
// return it.
func h5sHyperListBlocks(sel *h5sSelection) error {
	if sel.typ != h5sSelHyperslabs || sel.regular || sel.spanLst == nil {
		return nil
	}
	nblocks := h5sHyperSpanNblocks(sel.spanLst)
	if nblocks > h5sHyperMaxBlocks {
		return errorf(H5E_DATASPACE, H5E_CANTALLOC, "too many blocks (%d) in hyperslab selection", nblocks)
	}
	sel.blocks = h5sGetSelectHyperBlocklist(sel, int(nblocks))
	return nil
}

// h5sHyperSpanBlocklist is H5S__hyper_span_blocklist: walk the blocks of a
// span tree, calling fn with each block's start and end (start and end
// are scratch arrays reused between calls).
func h5sHyperSpanBlocklist(spans *h5sHyperSpanInfo, start, end []uint64, rank int, fn func(start, end []uint64) bool) bool {
	/* Walk through the list of spans, recursing or outputting them */
	for i := range spans.spans {
		curr := &spans.spans[i]
		start[rank] = curr.low
		end[rank] = curr.high

		/* Recurse if this node has down spans */
		if curr.down != nil {
			/* Recurse down to the next dimension */
			if !h5sHyperSpanBlocklist(curr.down, start, end, rank+1, fn) {
				return false
			}
		} else if !fn(start, end) {
			return false
		}
	}
	return true
}

// h5sGetSelectHyperBlocklist is H5S__get_select_hyper_blocklist for a span
// tree selection: the list of blocks in span tree order.
func h5sGetSelectHyperBlocklist(sel *h5sSelection, nblocks int) [][2][]uint64 {
	rank := int(sel.rank)
	out := make([][2][]uint64, 0, nblocks)
	start := make([]uint64, rank)
	end := make([]uint64, rank)
	h5sHyperSpanBlocklist(sel.spanLst, start, end, 0, func(s, e []uint64) bool {
		out = append(out, [2][]uint64{append([]uint64(nil), s...), append([]uint64(nil), e...)})
		return true
	})
	return out
}

/*
 * Queries
 */

// h5sHyperIsValid is H5S__hyper_is_valid: whether the selection (plus the
// selection offset) lies within the extent.
func h5sHyperIsValid(space *h5s) bool {
	sel := space.selection()

	/* Check for unlimited selection */
	if sel.unlimDim >= 0 {
		return false
	}

	/* Check which set of low & high bounds we should be using */
	lowBounds, highBounds := h5sHyperBoundsArrays(sel)
	if lowBounds == nil {
		return false
	}

	/* Check each dimension */
	for u := range int(space.extent.rank) {
		/* Bounds check the selected point + offset against the extent */
		if int64(lowBounds[u])+sel.selOffset(u) < 0 {
			return false
		}
		if highBounds[u]+uint64(sel.selOffset(u)) >= space.extent.size[u] {
			return false
		}
	}
	return true
}

// h5sHyperBoundsArrays returns the low & high bounds of the regular form
// when it is valid, else those of the span tree.
func h5sHyperBoundsArrays(sel *h5sSelection) (low, high []uint64) {
	if sel.regular {
		return sel.lowBounds, sel.highBounds
	}
	if sel.spanLst == nil {
		return nil, nil
	}
	return sel.spanLst.lowBounds, sel.spanLst.highBounds
}

// h5sHyperBounds is H5S__hyper_bounds: the bounding box of the selection,
// including the selection offset.
func h5sHyperBounds(space *h5s) (start, end []uint64, err error) {
	sel := space.selection()
	rank := int(space.extent.rank)

	/* Check which set of low & high bounds we should be using */
	lowBounds, highBounds := h5sHyperBoundsArrays(sel)
	if lowBounds == nil || len(lowBounds) < rank {
		return nil, nil, errorf(H5E_DATASPACE, H5E_CANTGET, "hyperslab selection has no bounds")
	}
	start = make([]uint64, rank)
	end = make([]uint64, rank)

	/* Check for offset set */
	if sel.offsetChanged {
		/* Loop over dimensions */
		for u := range rank {
			/* Check for offset moving selection negative */
			if int64(lowBounds[u])+sel.selOffset(u) < 0 {
				return nil, nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
			}

			/* Set the low & high bounds in this dimension */
			start[u] = uint64(int64(lowBounds[u]) + sel.selOffset(u))
			if u == sel.unlimDim {
				end[u] = h5sUnlimited
			} else {
				end[u] = uint64(int64(highBounds[u]) + sel.selOffset(u))
			}
		}
	} else {
		/* Offset vector is still zeros, just copy low & high bounds */
		copy(start, lowBounds)
		copy(end, highBounds)
	}
	return start, end, nil
}

// h5sHyperOffset is H5S__hyper_offset: the linear offset of the first
// selected element (including the selection offset).
func h5sHyperOffset(space *h5s) (uint64, error) {
	sel := space.selection()
	rank := int(space.extent.rank)
	dimSize := space.extent.size

	/* Start at linear offset 0 */
	var offset uint64

	/* Check for a "regular" hyperslab selection */
	if sel.regular {
		diminfo := sel.opt

		/* Loop through starting coordinates, calculating the linear offset */
		accum := uint64(1)
		for i := rank - 1; i >= 0; i-- {
			hypOffset := int64(diminfo[i].start) + sel.selOffset(i) /* Hyperslab's offset in this dimension */

			/* Check for offset moving selection out of the dataspace */
			if hypOffset < 0 || uint64(hypOffset) >= dimSize[i] {
				return 0, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
			}

			/* Add the hyperslab's offset in this dimension to the total linear offset */
			offset += uint64(hypOffset) * accum

			/* Increase the accumulator */
			accum *= dimSize[i]
		}
		return offset, nil
	}

	/* Calculate the accumulator for each dimension */
	dimAccum := h5sPitches(&space.extent)

	/* Get information for the first span, in the slowest changing dimension */
	spans := sel.spanLst
	if spans == nil {
		return 0, errorf(H5E_DATASPACE, H5E_CANTGET, "hyperslab selection has no spans")
	}

	/* Work down the spans, computing the linear offset */
	for i := 0; spans != nil && i < rank; i++ {
		span := &spans.spans[0]
		hypOffset := int64(span.low) + sel.selOffset(i) /* Hyperslab's offset in this dimension */

		/* Check for offset moving selection out of the dataspace */
		if hypOffset < 0 || uint64(hypOffset) >= dimSize[i] {
			return 0, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
		}

		/* Add the hyperslab's offset in this dimension to the total linear offset */
		offset += uint64(hypOffset) * dimAccum[i]

		/* Advance to first span in "down" dimension */
		spans = span.down
	}
	return offset, nil
}

// h5sHyperUnlimDim is H5S__hyper_unlim_dim.
func h5sHyperUnlimDim(space *h5s) int {
	return space.selection().unlimDim
}

// h5sHyperNumElemNonUnlim is H5S__hyper_num_elem_non_unlim.
func h5sHyperNumElemNonUnlim(space *h5s) (uint64, error) {
	sel := space.selection()

	/* Get number of elements in the non-unlimited dimensions */
	if sel.unlimDim >= 0 {
		return sel.numElemNonUnlim, nil
	}
	return 0, errorf(H5E_DATASPACE, H5E_BADVALUE, "selection has no unlimited dimension")
}

// h5sHyperIsContiguous is H5S__hyper_is_contiguous: whether the selection
// is one block that is contiguous in the extent (a "large contiguous"
// block spanning all but the slowest dimension, or a "small contiguous"
// block of size 1 in all but the fastest dimension).
func h5sHyperIsContiguous(space *h5s) bool {
	sel := space.selection()
	rank := int(space.extent.rank)
	var largeContiguous, smallContiguous bool

	/* Check for a "regular" hyperslab selection */
	if sel.regular {
		diminfo := sel.opt

		/* Initialize flags */
		largeContiguous = true  /* assume true and reset if the dimensions don't match */
		smallContiguous = false /* assume false initially */

		/* Check for a "large contiguous" block */
		for u := range rank {
			if diminfo[u].count > 1 {
				largeContiguous = false
				break
			}
			if u > 0 && diminfo[u].block != space.extent.size[u] {
				largeContiguous = false
				break
			}
		}

		/* If we didn't find a large contiguous block, check for a small one */
		if !largeContiguous {
			smallContiguous = true
			for u := range rank {
				if diminfo[u].count > 1 {
					smallContiguous = false
					break
				}
				if u < rank-1 && diminfo[u].block != 1 {
					smallContiguous = false
					break
				}
			}
		}
		return largeContiguous || smallContiguous
	}
	if sel.spanLst == nil {
		return false
	}

	/* Initialize flags */
	largeContiguous = true  /* assume true and reset if the dimensions don't match */
	smallContiguous = false /* assume false initially */

	/* Get information for slowest changing information */
	spans := sel.spanLst
	span := &spans.spans[0]

	/* If there are multiple spans in the slowest changing dimension, the selection isn't contiguous */
	if len(spans.spans) > 1 {
		largeContiguous = false
	} else if span.down != nil {
		/* Now check the rest of the dimensions */
		u := 1 /* Current dimension working on */

		/* Get the span information for the next fastest dimension */
		spans = span.down

		/* Cycle down the spans until we run out of down spans or find a non-contiguous span */
		for spans != nil {
			span = &spans.spans[0]

			/* Check that this is the only span and it spans the entire dimension */
			if len(spans.spans) > 1 {
				largeContiguous = false
				break
			}
			/* If this span doesn't cover the entire dimension, then this selection isn't
			 * contiguous */
			if span.high-span.low+1 != space.extent.size[u] {
				largeContiguous = false
				break
			}
			/* Walk down to the next span */
			spans = span.down

			/* Increment dimension */
			u++
		}
	}

	/* If we didn't find a large contiguous block, check for a small one */
	if !largeContiguous {
		smallContiguous = true

		/* Get information for slowest changing information */
		spans = sel.spanLst

		/* Current dimension working on */
		u := 0

		/* Cycle down the spans until we run out of down spans or find a non-contiguous span */
		for spans != nil {
			span = &spans.spans[0]

			/* Check that this is the only span and it spans the entire dimension */
			if len(spans.spans) > 1 {
				smallContiguous = false
				break
			}
			/* If this span doesn't cover the entire dimension, then this selection isn't contiguous
			 */
			if u < rank-1 && span.high-span.low+1 != 1 {
				smallContiguous = false
				break
			}
			/* Walk down to the next span */
			spans = span.down

			/* Increment dimension */
			u++
		}
	}

	/* Indicate true if it's either a large or small contiguous block */
	return largeContiguous || smallContiguous
}

// h5sHyperIsSingle is H5S__hyper_is_single: whether the selection is a
// single block.
func h5sHyperIsSingle(space *h5s) bool {
	sel := space.selection()

	/* Check for a "single" hyperslab selection */
	if sel.regular {
		/*
		 * For a regular hyperslab to be single, it must have only one
		 * block (i.e. count==1 in all dimensions)
		 */
		for u := range int(space.extent.rank) {
			if sel.opt[u].count > 1 {
				return false
			}
		}
		return true
	}

	/*
	 * For a region to be single, it must have only one block
	 */
	/* Cycle down the spans until we run out of down spans or find a non-contiguous span */
	for spans := sel.spanLst; spans != nil; spans = spans.spans[0].down {
		/* Check that this is the only span and it spans the entire dimension */
		if len(spans.spans) > 1 {
			return false
		}
	}
	return true
}

// h5sHyperIsRegular is H5S__hyper_is_regular. (The span tree of an
// irregular selection was already checked with H5S__hyper_rebuild when it
// was built.)
func h5sHyperIsRegular(space *h5s) bool {
	/* Only simple check for regular hyperslabs for now... */
	return space.selection().regular
}

// h5sHyperSpansShapeSameHelper is H5S__hyper_spans_shape_same_helper:
// whether two span trees have the same shape, with the elements of the
// second offset by offset from those of the first.
func h5sHyperSpansShapeSameHelper(spanInfo1, spanInfo2 *h5sHyperSpanInfo, offset []int64, restZeros []bool) bool {
	/* Compare low & high bounds for this span list */
	if uint64(int64(spanInfo1.lowBounds[0])+offset[0]) != spanInfo2.lowBounds[0] {
		return false
	}
	if uint64(int64(spanInfo1.highBounds[0])+offset[0]) != spanInfo2.highBounds[0] {
		return false
	}
	if len(spanInfo1.spans) != len(spanInfo2.spans) {
		return false
	}
	for i := range spanInfo1.spans {
		span1, span2 := &spanInfo1.spans[i], &spanInfo2.spans[i]

		/* Check if the actual low & high span information is the same */
		if uint64(int64(span1.low)+offset[0]) != span2.low || uint64(int64(span1.high)+offset[0]) != span2.high {
			return false
		}

		/* Check for down tree for this span */
		if span1.down != nil || span2.down != nil {
			if span1.down == nil || span2.down == nil {
				return false
			}
			/* If the rest of the span trees have a zero offset, use the faster comparison routine */
			if restZeros[0] {
				if !h5sHyperCmpSpans(span1.down, span2.down) {
					return false
				}
			} else if !h5sHyperSpansShapeSameHelper(span1.down, span2.down, offset[1:], restZeros[1:]) {
				return false
			}
		}
	}
	return true
}

// h5sHyperSpansShapeSame is H5S__hyper_spans_shape_same.
func h5sHyperSpansShapeSame(spanInfo1, spanInfo2 *h5sHyperSpanInfo, ndims int) bool {
	/* Initialize arrays */
	offset := make([]int64, h5sMaxRank)
	restZeros := make([]bool, h5sMaxRank)

	/* Check for an offset between the two selections */
	span1 := &spanInfo1.spans[0]
	span2 := &spanInfo2.spans[0]
	zeroOffset := true
	for u := range ndims {
		/* Check for offset in this dimension */
		if span1.low != span2.low {
			offset[u] = int64(span2.low) - int64(span1.low)

			/* Indicate that the offset vector is not all zeros */
			zeroOffset = false
		}

		/* (Both span trees must have the same depth) */
		if (span1.down == nil) != (span2.down == nil) {
			return false
		}

		/* Advance to next dimension */
		if span1.down != nil {
			span1 = &span1.down.spans[0]
			span2 = &span2.down.spans[0]
		}
	}

	/* Check if there's a "tail" of all zeros in a non-zero offset vector */
	if !zeroOffset {
		/* Find first non-zero offset, from the fastest dimension up */
		for i := ndims - 1; i >= 0; i-- {
			if offset[i] != 0 {
				restZeros[i] = true
				break
			}
		}
	}

	/* If the offset vector is all zero, we can use the faster span tree
	 *  comparison routine.  Otherwise, use a generalized version of that
	 *  routine.
	 */
	if zeroOffset {
		return h5sHyperCmpSpans(spanInfo1, spanInfo2)
	}
	return h5sHyperSpansShapeSameHelper(spanInfo1, spanInfo2, offset, restZeros)
}

// h5sHyperShapeSame is H5S__hyper_shape_same (space1 has the larger rank).
func h5sHyperShapeSame(space1, space2 *h5s) (bool, error) {
	sel1, sel2 := space1.selection(), space2.selection()

	/* Get dataspace ranks */
	space1Rank := int(space1.extent.rank)
	space2Rank := int(space2.extent.rank)

	/* If both are regular hyperslabs, compare their diminfo values */
	if sel1.regular && sel2.regular {
		/* Initialize dimensions */
		space1Dim := space1Rank - 1
		space2Dim := space2Rank - 1

		/* Check that the shapes are the same in the common dimensions, and that
		 * block == 1 in all dimensions that appear only in space1.
		 */
		for space2Dim >= 0 {
			if sel1.opt[space1Dim].stride != sel2.opt[space2Dim].stride {
				return false, nil
			}
			if sel1.opt[space1Dim].count != sel2.opt[space2Dim].count {
				return false, nil
			}
			if sel1.opt[space1Dim].block != sel2.opt[space2Dim].block {
				return false, nil
			}
			space1Dim--
			space2Dim--
		}
		for space1Dim >= 0 {
			if sel1.opt[space1Dim].block != 1 {
				return false, nil
			}
			space1Dim--
		}
		return true, nil
	}

	/* If both aren't regular, use fast irregular comparison */
	/* Make certain that both selections have span trees */
	spans1, err := h5sHyperGenerateSpans(sel1)
	if err != nil {
		return false, wrapf(err, H5E_DATASPACE, H5E_UNINITIALIZED, "can't construct span tree for hyperslab selection")
	}
	spans2, err := h5sHyperGenerateSpans(sel2)
	if err != nil {
		return false, wrapf(err, H5E_DATASPACE, H5E_UNINITIALIZED, "can't construct span tree for hyperslab selection")
	}

	/* If rank of space A is different (guaranteed greater) than
	 *      rank of space B, walk down the span tree, verifying
	 *      that the block size is 1 on the way down.
	 */
	for diffRank := space1Rank - space2Rank; diffRank > 0; diffRank-- {
		/* Get pointer to first span in tree */
		span := &spans1.spans[0]

		/* Check for more spans in this dimension */
		if len(spans1.spans) > 1 {
			return false, nil
		}

		/* Check for span size > 1 element */
		if span.low != span.high {
			return false, nil
		}

		/* Walk down to the next dimension */
		spans1 = span.down
		if spans1 == nil {
			return false, nil
		}
	}

	/* Compare the span trees */
	return h5sHyperSpansShapeSame(spans1, spans2, space2Rank), nil
}

// h5sHyperGenerateSpans is H5S__hyper_generate_spans: the span tree of a
// hyperslab selection, built from the optimized regular form when the
// selection has none (the selection itself is not modified).
func h5sHyperGenerateSpans(sel *h5sSelection) (*h5sHyperSpanInfo, error) {
	if sel.spanLst != nil {
		return sel.spanLst, nil
	}
	if !sel.regular {
		return nil, errorf(H5E_DATASPACE, H5E_UNINITIALIZED, "dataspace does not have span tree")
	}
	rank := int(sel.rank)
	start := make([]uint64, rank)
	stride := make([]uint64, rank)
	count := make([]uint64, rank)
	block := make([]uint64, rank)

	/* Get the diminfo */
	for u := range rank {
		/* Check for unlimited dimension and return error */
		if sel.opt[u].count == h5sUnlimited {
			return nil, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "can't generate spans with unlimited count")
		}
		if sel.opt[u].block == h5sUnlimited {
			return nil, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "can't generate spans with unlimited block")
		}
		start[u] = sel.opt[u].start
		stride[u] = sel.opt[u].stride
		count[u] = sel.opt[u].count
		block[u] = sel.opt[u].block
	}

	/* Build the hyperslab information also */
	spans, err := h5sHyperMakeSpans(rank, start, stride, count, block)
	if err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTINSERT, "can't generate hyperslabs")
	}
	return spans, nil
}

// h5sHyperIntersectBlockHelper is H5S__hyper_intersect_block_helper:
// whether a span tree intersects the block start..end.
func h5sHyperIntersectBlockHelper(spans *h5sHyperSpanInfo, rank int, start, end []uint64, visited map[*h5sHyperSpanInfo]bool) bool {
	/* Check if we've already visited this span tree */
	if visited[spans] {
		return false
	}

	/* Verify that there is a possibility of an overlap by checking the block
	 *  against the low & high bounds for the span tree.
	 */
	for u := range rank {
		if start[u] > spans.highBounds[u] || end[u] < spans.lowBounds[u] {
			return false
		}
	}

	/* Iterate over the spans in the tree */
	for i := range spans.spans {
		curr := &spans.spans[i]

		/* Check for span entirely before block */
		if curr.high < start[0] {
			/* Advance to next span in this dimension */
			continue
		}
		/* If this span is past the end of the block, then we're done in this dimension */
		if curr.low > end[0] {
			return false
		}

		/* block & span overlap */
		/* If this is the bottom dimension, then the span tree overlaps the block */
		if curr.down == nil {
			return true
		}

		/* Recursively check spans in next dimension down */
		/* If there is an intersection in the "down" dimensions,
		 * the span trees overlap.
		 */
		if h5sHyperIntersectBlockHelper(curr.down, rank-1, start[1:], end[1:], visited) {
			return true
		}
	}

	/* Set the tree's operation generation */
	visited[spans] = true
	return false
}

// h5sHyperIntersectBlock is H5S__hyper_intersect_block: whether the
// selection intersects the block start..end (the caller has checked the
// bounds).
func h5sHyperIntersectBlock(space *h5s, start, end []uint64) (bool, error) {
	sel := space.selection()
	rank := int(space.extent.rank)

	/* Check for regular hyperslab intersection */
	if sel.regular {
		/* Check for a single block */
		/* For a regular hyperslab to be single, it must have only one block
		 * (i.e. count == 1 in all dimensions).
		 */
		singleBlock := true
		for u := range rank {
			if sel.opt[u].count > 1 {
				singleBlock = false
			}
		}

		/* Single blocks have already been "compared" above, in the low / high
		 * bound checking, so just return true if we've reached here - they
		 * would have been rejected earlier, if they didn't intersect.
		 */
		if singleBlock {
			return true, nil
		}

		/* Loop over the dimensions, checking for an intersection */
		for u := range rank {
			opt := &sel.opt[u]
			/* If the block's start is <= the hyperslab start, they intersect */
			/* (So, if the start is > the hyperslab start, check more conditions) */
			if start[u] > opt.start {
				/* Adjust start coord for selection's 'start' offset */
				adjStart := start[u] - opt.start

				/* Compute # of strides into the selection */
				var nstride uint64
				if opt.count > 1 {
					nstride = adjStart / opt.stride
				}

				/* "Rebase" the adjusted start coord into the same range
				 *      range of values as the selections's first block.
				 */
				adjStart -= nstride * opt.stride

				/* If the adjusted start doesn't fall within the first hyperslab
				 *  span, check for the block overlapping with the next one.
				 */
				if adjStart >= opt.block {
					/* Adjust end coord for selection's 'start' offset */
					adjEnd := end[u] - opt.start

					/* "Rebase" the adjusted end coord into the same range
					 *      range of values as the selections's first block.
					 */
					adjEnd -= nstride * opt.stride

					/* If block doesn't extend over beginning of next span,
					 *  it doesn't intersect.
					 */
					if adjEnd < opt.stride {
						return false, nil
					}
				}
			}
		}

		/* If we've looped through all dimensions and none of them didn't
		 *  overlap, then all of them do, so we report true.
		 */
		return true, nil
	}
	if sel.spanLst == nil {
		return false, errorf(H5E_DATASPACE, H5E_UNINITIALIZED, "hyperslab selection has no span tree")
	}

	/* Perform the span-by-span intersection check */
	return h5sHyperIntersectBlockHelper(sel.spanLst, rank, start, end, map[*h5sHyperSpanInfo]bool{}), nil
}

// h5sHyperAdjustSHelper is H5S__hyper_adjust_s_helper: a copy of a span
// tree moved by subtracting offset (shared subtrees are copied once).
func h5sHyperAdjustSHelper(spans *h5sHyperSpanInfo, rank int, offset []int64, done map[*h5sHyperSpanInfo]*h5sHyperSpanInfo) (*h5sHyperSpanInfo, error) {
	/* Check if we've already set this span tree */
	if n, found := done[spans]; found {
		return n, nil
	}
	out := make([]h5sHyperSpan, len(spans.spans))

	/* Iterate over the spans in tree */
	for i := range spans.spans {
		span := &spans.spans[i]

		/* Adjust span offset */
		if int64(span.low)-offset[0] < 0 {
			return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
		}
		out[i].low = uint64(int64(span.low) - offset[0])
		out[i].high = uint64(int64(span.high) - offset[0])

		/* Recursively adjust spans in next dimension down */
		if span.down != nil {
			down, err := h5sHyperAdjustSHelper(span.down, rank-1, offset[1:], done)
			if err != nil {
				return nil, err
			}
			out[i].down = down
		}
	}

	/* Set the tree's operation generation */
	n := h5sHyperNewSpanInfo(out)
	done[spans] = n
	return n, nil
}

// h5sHyperAdjustU is H5S__hyper_adjust_u: move the selection by
// subtracting an offset. It returns a new dataspace.
func h5sHyperAdjustU(space *h5s, offset []uint64) (*h5s, error) {
	soff := make([]int64, len(offset))
	for u, o := range offset {
		if o > 1<<63-1 {
			return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset too large")
		}
		soff[u] = int64(o)
	}
	return h5sHyperAdjustS(space, soff)
}

// h5sHyperAdjustS is H5S__hyper_adjust_s: move the selection by
// subtracting a signed offset. It returns a new dataspace. Unlike the C
// library, the application form of a regular selection is moved along
// with the optimized one.
func h5sHyperAdjustS(space *h5s, offset []int64) (*h5s, error) {
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

	/* Subtract the offset from the "regular" coordinates, if they exist */
	if old.regular {
		sel.opt = append([]h5sHyperDim(nil), old.opt...)
		sel.start = append([]uint64(nil), old.start...)
		sel.lowBounds = append([]uint64(nil), old.lowBounds...)
		sel.highBounds = append([]uint64(nil), old.highBounds...)
		for u := range rank {
			if int64(sel.opt[u].start)-offset[u] < 0 || int64(sel.lowBounds[u])-offset[u] < 0 {
				return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "offset moves selection out of bounds")
			}
			sel.opt[u].start = uint64(int64(sel.opt[u].start) - offset[u])
			sel.start[u] = uint64(int64(sel.start[u]) - offset[u])

			/* Adjust the low & high bounds */
			sel.lowBounds[u] = uint64(int64(sel.lowBounds[u]) - offset[u])
			if u != sel.unlimDim {
				sel.highBounds[u] = uint64(int64(sel.highBounds[u]) - offset[u])
			}
		}
	}

	/* Subtract the offset from the span tree coordinates, if they exist */
	if old.spanLst != nil {
		spans, err := h5sHyperAdjustSHelper(old.spanLst, rank, offset, map[*h5sHyperSpanInfo]*h5sHyperSpanInfo{})
		if err != nil {
			return nil, err
		}
		sel.spanLst = spans
		if !sel.regular && old.blocks != nil {
			sel.blocks = h5sGetSelectHyperBlocklist(&sel, len(old.blocks))
		}
	}
	ret.sel = &sel
	return ret, nil
}

// h5sHyperProjectScalar is H5S__hyper_project_scalar: the linear offset of
// the single selected element.
func h5sHyperProjectScalar(space *h5s) (uint64, error) {
	sel := space.selection()
	rank := int(space.extent.rank)
	block := make([]uint64, rank) /* Block selected in base dataspace */

	/* Check for a "regular" hyperslab selection */
	if sel.regular {
		/* Build the table of the initial offset */
		for u := range rank {
			if sel.opt[u].count != 1 || sel.opt[u].block != 1 {
				return 0, errorf(H5E_DATASPACE, H5E_BADRANGE, "hyperslab selection of more than one element")
			}
			/* Keep the offset for later */
			block[u] = sel.opt[u].start
		}
	} else {
		/* Advance down selected spans */
		currDim := 0
		for spans := sel.spanLst; spans != nil; currDim++ {
			curr := &spans.spans[0]
			if len(spans.spans) != 1 || curr.low != curr.high || currDim >= rank {
				return 0, errorf(H5E_DATASPACE, H5E_BADRANGE, "hyperslab selection of more than one element")
			}

			/* Save the location of the selection in current dimension */
			block[currDim] = curr.low

			/* Advance down to next dimension */
			spans = curr.down
		}
	}

	/* Calculate offset of selection in projected buffer */
	return h5vmArrayOffset(space.extent.size, block), nil
}

// h5sHyperNormalizeOffset is H5S_hyper_normalize_offset: for a hyperslab
// selection with a changed offset, return a dataspace whose selection has
// been moved by the offset (and has a zero offset), the old offset and
// true. Otherwise return the dataspace unchanged and false.
func h5sHyperNormalizeOffset(space *h5s) (*h5s, []int64, bool, error) {
	sel := space.selection()

	/* Check for hyperslab selection & offset changed */
	if sel.typ != h5sSelHyperslabs || !sel.offsetChanged {
		return space, nil, false, nil
	}
	rank := int(space.extent.rank)

	/* Copy & invert the selection offset */
	oldOffset := make([]int64, rank)
	inv := make([]int64, rank)
	for u := range rank {
		oldOffset[u] = sel.selOffset(u)
		inv[u] = -oldOffset[u]
	}

	/* Call the 'adjust' routine */
	ret, err := h5sHyperAdjustS(space, inv)
	if err != nil {
		return nil, nil, false, wrapf(err, H5E_DATASPACE, H5E_CANTSET, "can't adjust selection")
	}

	/* Zero out the selection offset */
	ret.sel.offset = make([]int64, rank)

	/* Indicate that the offset was normalized */
	return ret, oldOffset, true, nil
}

// h5sHyperDenormalizeOffset is H5S_hyper_denormalize_offset: undo
// h5sHyperNormalizeOffset, returning a new dataspace.
func h5sHyperDenormalizeOffset(space *h5s, oldOffset []int64) (*h5s, error) {
	/* Call the 'adjust' routine */
	ret, err := h5sHyperAdjustS(space, oldOffset)
	if err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTSET, "can't adjust selection")
	}

	/* Copy the selection offset over */
	ret.sel.offset = append([]int64(nil), oldOffset...)
	return ret, nil
}

/*
 * Regular form
 */

// h5sHyperRebuildHelper is H5S__hyper_rebuild_helper: rebuild the regular
// form of a span tree into spanSlabInfo, if the tree is one that a single
// H5S_SELECT_SET operation could have generated.
func h5sHyperRebuildHelper(spans *h5sHyperSpanInfo, spanSlabInfo []h5sHyperDim) bool {
	/* Initialization */
	span := &spans.spans[0]
	stride := uint64(1)
	var prevLow uint64

	/* Get "canonical" down span information */
	if span.down != nil {
		/* Go to the next down span and check whether the selection can be rebuilt */
		if len(spanSlabInfo) < 2 || !h5sHyperRebuildHelper(span.down, spanSlabInfo[1:]) {
			return false
		}
	}

	/* Assign the initial starting point & block size for this dimension */
	start := span.low
	block := span.high - span.low + 1

	/* Loop the spans */
	for spancount := range spans.spans {
		span = &spans.spans[spancount]
		if spancount > 0 {
			prevSpan := &spans.spans[spancount-1]

			/* Check that down spans match current slab info */
			/* (Can skip check if previous span's down pointer is same as current one) */
			if span.down != nil && prevSpan.down != span.down {
				if !h5sHyperCmpSpans(span.down, prevSpan.down) {
					return false
				}
			}

			/* Obtain values for stride and block */
			currStride := span.low - prevLow
			currBlock := span.high - span.low + 1

			/* Compare stride and block for this span.  To compare stride,
			 * three spans are needed.  Account for the first two spans.
			 */
			if currBlock != block {
				return false
			}
			if spancount > 1 {
				if stride != currStride {
					return false
				}
			} else {
				stride = currStride
			}
		}

		/* Keep current starting point */
		prevLow = span.low
	}

	/* Save the span information. */
	spanSlabInfo[0] = h5sHyperDim{start: start, count: uint64(len(spans.spans)), block: block, stride: stride}
	return true
}

// h5sHyperRebuild is H5S__hyper_rebuild: set the regular form of a span
// tree selection (being built), if it has one.
func h5sHyperRebuild(sel *h5sSelection) {
	rebuilt := make([]h5sHyperDim, sel.rank)

	/* Check whether the slab can be rebuilt */
	/* (Only regular selection can be rebuilt. If yes, fill in correct values) */
	if sel.spanLst == nil || !h5sHyperRebuildHelper(sel.spanLst, rebuilt) {
		sel.regular = false
		return
	}

	/* Set the dimension info & bounds for the dataspace, from the rebuilt info */
	sel.opt = rebuilt
	sel.start = make([]uint64, sel.rank)
	sel.stride = make([]uint64, sel.rank)
	sel.count = make([]uint64, sel.rank)
	sel.block = make([]uint64, sel.rank)
	for u, d := range rebuilt {
		sel.start[u], sel.stride[u], sel.count[u], sel.block[u] = d.start, d.stride, d.count, d.block
	}
	sel.lowBounds = append([]uint64(nil), sel.spanLst.lowBounds...)
	sel.highBounds = append([]uint64(nil), sel.spanLst.highBounds...)
	sel.regular = true
	sel.blocks = nil
}

// h5sHyperOnes returns a slice of n ones (H5S_hyper_ones_g).
func h5sHyperOnes(n int) []uint64 {
	s := make([]uint64, n)
	for i := range s {
		s[i] = 1
	}
	return s
}

// h5sSelectHyperslab is H5S_select_hyperslab with op == H5S_SELECT_SET on
// a dataspace being built: select a regular hyperslab. nil stride or
// block mean 1 in every dimension. A zero count or block selects nothing.
// count or block (not both) may be H5S_UNLIMITED in one dimension.
func h5sSelectHyperslab(space *h5s, start, stride, count, block []uint64) error {
	rank := int(space.extent.rank)
	if len(start) < rank || len(count) < rank {
		return errorf(H5E_ARGS, H5E_BADVALUE, "hyperslab arrays shorter than dataspace rank")
	}

	/* Point to the correct stride values */
	defaultStride := stride == nil
	if stride == nil {
		stride = h5sHyperOnes(rank)
	}

	/* Point to the correct block values */
	defaultBlock := block == nil
	if block == nil {
		block = h5sHyperOnes(rank)
	}
	if len(stride) < rank || len(block) < rank {
		return errorf(H5E_ARGS, H5E_BADVALUE, "hyperslab arrays shorter than dataspace rank")
	}

	/* Check new selection */
	unlimDim := -1
	for u := range rank {
		/* Check for overlapping hyperslab blocks in new selection. */
		if count[u] > 1 && stride[u] < block[u] {
			return errorf(H5E_ARGS, H5E_BADVALUE, "hyperslab blocks overlap")
		}

		/* Detect zero-sized hyperslabs in new selection */
		if count[u] == 0 || block[u] == 0 {
			/* Convert to "none" selection */
			h5sSelectNone(space)
			return nil
		}

		/* Check for unlimited dimension */
		if count[u] == h5sUnlimited || block[u] == h5sUnlimited {
			if unlimDim >= 0 {
				return errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "cannot have more than one unlimited dimension in selection")
			}
			if count[u] == block[u] { /* Both are H5S_UNLIMITED */
				return errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "count and block cannot both be unlimited")
			}
			unlimDim = u
		}
	}

	/* Optimize hyperslab parameters to merge contiguous blocks, etc. */
	optStride := make([]uint64, rank)
	optCount := make([]uint64, rank)
	optBlock := make([]uint64, rank)
	if defaultStride && defaultBlock {
		/* Point to existing arrays */
		for u := range rank {
			optStride[u], optCount[u], optBlock[u] = 1, 1, count[u]
		}
	} else {
		for u := range rank {
			/* contiguous hyperslabs have the block size equal to the stride */
			if stride[u] == block[u] && count[u] != h5sUnlimited {
				optCount[u] = 1
				optStride[u] = 1
				if block[u] == 1 {
					optBlock[u] = count[u]
				} else {
					hi, lo := bits.Mul64(block[u], count[u])
					if hi != 0 {
						return errorf(H5E_DATASPACE, H5E_BADRANGE, "hyperslab selection out of range")
					}
					optBlock[u] = lo
				}
			} else {
				if count[u] == 1 {
					optStride[u] = 1
				} else {
					optStride[u] = stride[u]
				}
				optCount[u] = count[u]
				optBlock[u] = block[u]
			}
		}
	}

	/* Set selection to regular hyperslab */
	if err := h5sHyperSetRegularHyperslab(space, start, stride, count, block, optStride, optCount, optBlock); err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_CANTSET, "can't set regular hyperslab selection")
	}
	return nil
}

// h5sHyperSetRegularHyperslab is H5S__set_regular_hyperslab.
func h5sHyperSetRegularHyperslab(space *h5s, start, appStride, appCount, appBlock, optStride, optCount, optBlock []uint64) error {
	rank := int(space.extent.rank)

	/* If we are setting a new selection, remove current selection first */
	sel := &h5sSelection{
		typ:        h5sSelHyperslabs,
		rank:       uint32(rank),
		regular:    true,
		start:      append([]uint64(nil), start[:rank]...),
		stride:     append([]uint64(nil), appStride[:rank]...),
		count:      append([]uint64(nil), appCount[:rank]...),
		block:      append([]uint64(nil), appBlock[:rank]...),
		opt:        make([]h5sHyperDim, rank),
		lowBounds:  make([]uint64, rank),
		highBounds: make([]uint64, rank),
		unlimDim:   -1,
	}

	/* Set the diminfo */
	numElem := uint64(1)
	numElemOK := true
	for u := range rank {
		/* Set application and optimized hyperslab info */
		sel.opt[u] = h5sHyperDim{start: start[u], stride: optStride[u], count: optCount[u], block: optBlock[u]}

		/* Set low bound of bounding box for the hyperslab selection */
		sel.lowBounds[u] = start[u]

		/* Check for unlimited dimension & set high bound */
		if appCount[u] == h5sUnlimited || appBlock[u] == h5sUnlimited {
			sel.unlimDim = u
			sel.highBounds[u] = h5sUnlimited
		} else {
			/* Update # of elements selected */
			hi, lo := bits.Mul64(optCount[u], optBlock[u])
			if hi != 0 {
				numElemOK = false
			}
			hi, numElem = bits.Mul64(numElem, lo)
			if hi != 0 {
				numElemOK = false
			}
			hb, ok := h5sMulAdd(optStride[u], optCount[u]-1, optBlock[u]-1)
			if ok {
				hb, ok = h5sMulAdd(1, hb, start[u])
			}
			if !ok || hb == h5sUnlimited {
				return errorf(H5E_DATASPACE, H5E_BADRANGE, "hyperslab selection out of range")
			}
			sel.highBounds[u] = hb
		}
	}
	if !numElemOK {
		return errorf(H5E_DATASPACE, H5E_OVERFLOW, "number of elements in hyperslab selection overflows")
	}
	sel.numElem = numElem

	/* Handle unlimited selections */
	if sel.unlimDim >= 0 {
		/* Calculate num_elem_non_unlim */
		sel.numElemNonUnlim = numElem

		/* Update num_elem */
		sel.numElem = h5sUnlimited
	}

	/* Set selection type */
	space.sel = sel
	return nil
}

// h5sSelectHyperslabPublic selects the regular hyperslab hs in a copy of
// space (H5Sselect_hyperslab(space, H5S_SELECT_SET, ...)) and returns the
// copy.
func h5sSelectHyperslabPublic(space *h5s, hs h5.Hyperslab) (*h5s, error) {
	rank := int(space.extent.rank)
	if len(hs.Start) != rank || len(hs.Count) != rank || (hs.Stride != nil && len(hs.Stride) != rank) || (hs.Block != nil && len(hs.Block) != rank) {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "hyperslab rank does not match dataspace rank %d", rank)
	}
	ret := h5sCopy(space)
	if err := h5sSelectHyperslab(ret, hs.Start, hs.Stride, hs.Count, hs.Block); err != nil {
		return nil, err
	}
	if ret.sel != nil && ret.sel.typ == h5sSelHyperslabs {
		ret.sel.offset = nil
		ret.sel.offsetChanged = false
	}
	return ret, nil
}

// h5sHyperFromPoints converts a point selection into a hyperslab selection
// of the same elements (discarding their order), as
// H5S_select_project_intersection does with H5S_hyper_add_span_element.
func h5sHyperFromPoints(space *h5s) (*h5s, error) {
	sel := space.selection()
	blocks := make([][2][]uint64, len(sel.points))
	for i, pnt := range sel.points {
		blocks[i] = [2][]uint64{pnt, pnt}
	}
	spans, err := h5sHyperSpansFromBlocks(int(space.extent.rank), blocks)
	if err != nil {
		return nil, err
	}
	hsel, err := h5sHyperSelectionFromSpans(space.extent.rank, spans)
	if err != nil {
		return nil, err
	}
	ret := h5sCopy(space)
	ret.sel = hsel
	return ret, nil
}

/*
 * Projection
 */

// h5sIdxRange is an inclusive range of element indices (positions in
// selection order).
type h5sIdxRange struct{ lo, hi uint64 }

// h5sAppendIdxRange appends lo..hi to a sorted list of ranges, merging it
// with the last range when they touch.
func h5sAppendIdxRange(out []h5sIdxRange, lo, hi uint64) []h5sIdxRange {
	if n := len(out); n > 0 && out[n-1].hi+1 == lo {
		out[n-1].hi = hi
		return out
	}
	return append(out, h5sIdxRange{lo, hi})
}

// h5sHyperProjIntState carries the memoization for a projection.
type h5sHyperProjIntState struct {
	nelem  map[*h5sHyperSpanInfo]uint64
	ranges map[[2]*h5sHyperSpanInfo][]h5sIdxRange
	budget int
}

func (st *h5sHyperProjIntState) n(spans *h5sHyperSpanInfo) (uint64, error) {
	if spans == nil {
		return 1, nil
	}
	n, ok := h5sHyperSpansNelemHelper(spans, st.nelem)
	if !ok {
		return 0, errorf(H5E_DATASPACE, H5E_OVERFLOW, "number of elements in hyperslab selection overflows")
	}
	return n, nil
}

// h5sHyperProjIntRanges computes, for the source span tree ss and the
// source intersect span tree sis (of the same depth), the ranges of
// element indices (in ss selection order, relative to ss's first element)
// of the elements of ss that are also in sis. (This is the iteration over
// the source and intersect selections of H5S__hyper_proj_int_iterate.)
func h5sHyperProjIntRanges(ss, sis *h5sHyperSpanInfo, st *h5sHyperProjIntState) ([]h5sIdxRange, error) {
	key := [2]*h5sHyperSpanInfo{ss, sis}
	if r, found := st.ranges[key]; found {
		return r, nil
	}
	var out []h5sIdxRange
	var base uint64
	j := 0
	for i := range ss.spans {
		s := &ss.spans[i]
		rowN, err := st.n(s.down)
		if err != nil {
			return nil, err
		}

		/* Skip intersect spans entirely before this span */
		for j < len(sis.spans) && sis.spans[j].high < s.low {
			j++
		}
		for k := j; k < len(sis.spans) && sis.spans[k].low <= s.high; k++ {
			t := &sis.spans[k]
			a, b := max(s.low, t.low), min(s.high, t.high)
			if s.down == nil {
				out = h5sAppendIdxRange(out, base+(a-s.low), base+(b-s.low))
				continue
			}
			if t.down == nil {
				return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "span trees of different depths")
			}
			rel, err := h5sHyperProjIntRanges(s.down, t.down, st)
			if err != nil {
				return nil, err
			}
			if len(rel) == 0 {
				continue
			}
			if len(rel) == 1 && rel[0].lo == 0 && rel[0].hi == rowN-1 {
				/* Whole rows intersect */
				out = h5sAppendIdxRange(out, base+(a-s.low)*rowN, base+(b-s.low+1)*rowN-1)
				continue
			}
			for x := a; ; x++ {
				rb := base + (x-s.low)*rowN
				for _, r := range rel {
					if st.budget--; st.budget < 0 {
						return nil, errorf(H5E_DATASPACE, H5E_CANTALLOC, "too many sequences in projected selection")
					}
					out = h5sAppendIdxRange(out, rb+r.lo, rb+r.hi)
				}
				if x == b {
					break
				}
			}
		}
		base += (s.high - s.low + 1) * rowN
	}
	st.ranges[key] = out
	return out, nil
}

// h5sHyperProjBuild builds the span tree of the elements of the
// destination span tree ds whose indices (in ds selection order, relative
// to ds's first element) are in the sorted ranges. (This is the
// construction of the projected span tree of H5S__hyper_proj_int_build_proj.)
func h5sHyperProjBuild(ds *h5sHyperSpanInfo, ranges []h5sIdxRange, st *h5sHyperProjIntState) (*h5sHyperSpanInfo, error) {
	b := h5sHyperSpanBuilder{budget: &st.budget}
	var base uint64
	ri := 0
	for i := range ds.spans {
		s := &ds.spans[i]
		rowN, err := st.n(s.down)
		if err != nil {
			return nil, err
		}
		sb := base
		se := base + (s.high-s.low+1)*rowN - 1
		base = se + 1

		pos := sb
		for pos <= se && ri < len(ranges) {
			r := ranges[ri]
			if r.hi < pos {
				ri++
				continue
			}
			lo := max(r.lo, pos)
			if lo > se {
				break
			}
			hi := min(r.hi, se)
			if s.down == nil {
				if err := b.appendSpan(s.low+(lo-sb), s.low+(hi-sb), nil); err != nil {
					return nil, err
				}
				pos = hi + 1
				continue
			}
			x := (lo - sb) / rowN
			rowStart := sb + x*rowN
			rowEnd := rowStart + rowN - 1
			if lo == rowStart && hi >= rowEnd {
				/* Whole rows */
				x2 := (hi-sb+1)/rowN - 1
				if err := b.appendSpan(s.low+x, s.low+x2, s.down); err != nil {
					return nil, err
				}
				pos = sb + (x2+1)*rowN
				continue
			}

			/* Partial row: project the ranges within it onto the lower dimensions */
			var sub []h5sIdxRange
			for k := ri; k < len(ranges) && ranges[k].lo <= rowEnd; k++ {
				a, bb := max(ranges[k].lo, lo), min(ranges[k].hi, rowEnd)
				if a <= bb {
					sub = append(sub, h5sIdxRange{a - rowStart, bb - rowStart})
				}
			}
			down, err := h5sHyperProjBuild(s.down, sub, st)
			if err != nil {
				return nil, err
			}
			if down != nil {
				if err := b.appendSpan(s.low+x, s.low+x, down); err != nil {
					return nil, err
				}
			}
			pos = rowEnd + 1
		}
	}
	return b.finish(), nil
}

// h5sHyperProjectIntersection is H5S__hyper_project_intersection: set the
// selection of projSpace (which has the destination extent) to the
// projection onto dstSpace's selection of the elements of srcSpace's
// selection that are in srcIntersectSpace's. srcSpace and dstSpace have
// "all" or hyperslab selections, srcIntersectSpace a hyperslab selection.
func h5sHyperProjectIntersection(srcSpace, dstSpace, srcIntersectSpace, projSpace *h5s) error {
	/* Set up ss_span_info */
	ssSpanInfo, err := h5sHyperSpansOf(srcSpace)
	if err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_UNINITIALIZED, "can't construct span tree for source hyperslab selection")
	}

	/* Set up ds_span_info */
	dsSpanInfo, err := h5sHyperSpansOf(dstSpace)
	if err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_UNINITIALIZED, "can't construct span tree for destination hyperslab selection")
	}

	/* Make certain the source intersect selection has a span tree */
	sisSpanInfo, err := h5sHyperSpansOf(srcIntersectSpace)
	if err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_UNINITIALIZED, "can't construct span tree for source intersect hyperslab selection")
	}
	if len(ssSpanInfo.lowBounds) != len(sisSpanInfo.lowBounds) {
		return errorf(H5E_DATASPACE, H5E_BADVALUE, "source and source intersect selections have different ranks")
	}

	/* Iterate over selections and build projected span tree */
	st := &h5sHyperProjIntState{
		nelem:  map[*h5sHyperSpanInfo]uint64{},
		ranges: map[[2]*h5sHyperSpanInfo][]h5sIdxRange{},
		budget: h5sHyperMaxSpans,
	}
	ranges, err := h5sHyperProjIntRanges(ssSpanInfo, sisSpanInfo, st)
	if err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_CANTCOMPARE, "selection iteration failed")
	}
	var psSpanInfo *h5sHyperSpanInfo
	if len(ranges) > 0 {
		if psSpanInfo, err = h5sHyperProjBuild(dsSpanInfo, ranges, st); err != nil {
			return wrapf(err, H5E_DATASPACE, H5E_CANTCOMPARE, "selection iteration failed")
		}
	}

	/* Check for elements in projected space */
	if psSpanInfo == nil {
		/* If we did not add anything to proj_space, select none instead */
		h5sSelectNone(projSpace)
		return nil
	}

	/* Set span tree, the number of elements in current selection and
	 * attempt to build "optimized" start/stride/count/block information
	 * from resulting hyperslab span tree.
	 */
	sel, err := h5sHyperSelectionFromSpans(projSpace.extent.rank, psSpanInfo)
	if err != nil {
		return err
	}
	projSpace.sel = sel
	return nil
}

// h5sHyperSpansOf returns the span tree of an "all" or hyperslab
// selection (making one for "all" from the extent, as
// H5S__hyper_project_intersection does).
func h5sHyperSpansOf(space *h5s) (*h5sHyperSpanInfo, error) {
	sel := space.selection()
	if sel.typ == h5sSelHyperslabs {
		/* Make certain the selection has a span tree */
		return h5sHyperGenerateSpans(sel)
	}
	if sel.typ != h5sSelAll {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "unexpected selection type %d", sel.typ)
	}

	/* Create temporary span tree from all selection */
	rank := int(space.extent.rank)
	zeros := make([]uint64, rank)
	return h5sHyperMakeSpans(rank, zeros, zeros, h5sHyperOnes(rank), space.extent.size)
}

/*
 * Unlimited selections (virtual datasets)
 */

// h5sHyperGetClipDiminfo is H5S__hyper_get_clip_diminfo: the count and
// block required to clip an unlimited dimension to include clipSize.
func h5sHyperGetClipDiminfo(start, stride, count, block, clipSize uint64) (uint64, uint64) {
	/* Check for selection outside clip size */
	if start >= clipSize {
		if block == h5sUnlimited {
			block = 0
		} else {
			count = 0
		}
	} else if block == h5sUnlimited || block == stride {
		/* Check for single block in unlimited dimension */
		/* Calculate actual block size for this clip size */
		block = clipSize - start
		count = 1
	} else {
		/* Calculate initial count (last block may be partial) */
		count = (clipSize - start + stride - 1) / stride
	}
	return count, block
}

// h5sHyperClipUnlim is H5S_hyper_clip_unlim: clip the unlimited dimension
// of an unlimited hyperslab selection to clipSize, returning a new
// dataspace. The application form of the clipped dimension is set to the
// clipped values.
func h5sHyperClipUnlim(space *h5s, clipSize uint64) (*h5s, error) {
	old := space.selection()
	if old.typ != h5sSelHyperslabs || old.unlimDim < 0 || !old.regular {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "selection is not an unlimited hyperslab")
	}
	ret := h5sCopy(space)
	sel := *old
	sel.opt = append([]h5sHyperDim(nil), old.opt...)
	sel.count = append([]uint64(nil), old.count...)
	sel.block = append([]uint64(nil), old.block...)
	sel.highBounds = append([]uint64(nil), old.highBounds...)

	/* Save original unlimited dimension */
	origUnlimDim := old.unlimDim

	/* Set up convenience pointer */
	diminfo := &sel.opt[origUnlimDim]

	/* Save original count in unlimited dimension */
	origCount := diminfo.count

	/* Get initial diminfo */
	diminfo.count, diminfo.block = h5sHyperGetClipDiminfo(diminfo.start, diminfo.stride, diminfo.count, diminfo.block, clipSize)
	sel.count[origUnlimDim], sel.block[origUnlimDim] = diminfo.count, diminfo.block

	/* Selection is no longer unlimited */
	sel.unlimDim = -1

	/* Check for nothing returned */
	if diminfo.block == 0 || diminfo.count == 0 {
		/* Convert to "none" selection */
		h5sSelectNone(ret)
		return ret, nil
	}

	var err error
	switch {
	case origCount == 1:
		/* Check for single block in unlimited dimension */
		/* Calculate number of elements */
		sel.numElem, err = h5sHyperMul(diminfo.block, old.numElemNonUnlim)
	default:
		/* Calculate number of elements */
		var n uint64
		if n, err = h5sHyperMul(diminfo.count, diminfo.block); err == nil {
			sel.numElem, err = h5sHyperMul(n, old.numElemNonUnlim)
		}
	}
	if err != nil {
		return nil, err
	}

	/* Update the upper bound */
	sel.highBounds[origUnlimDim] = diminfo.start + diminfo.stride*(diminfo.count-1) + (diminfo.block - 1)

	/* Check if last block is partial.  If superset is set, just keep the
	 * last block complete to speed computation. */
	if origCount != 1 && diminfo.stride*(diminfo.count-1)+diminfo.block > clipSize-diminfo.start {
		/* Last block is partial, need to construct compound selection */
		/* Generate span tree in selection */
		spans, err := h5sHyperGenerateSpans(&sel)
		if err != nil {
			return nil, wrapf(err, H5E_DATASPACE, H5E_CANTINIT, "unable to generate span tree")
		}

		/* "And" selection with calculated block to perform clip operation */
		/* (block is clip_size in the unlimited dimension, H5S_MAX_SIZE in
		 *  the others, so only the unlimited dimension is clipped) */
		spans = h5sHyperClipSpans(spans, origUnlimDim, clipSize)
		cl, err := h5sHyperSelectionFromSpans(sel.rank, spans)
		if err != nil {
			return nil, wrapf(err, H5E_DATASPACE, H5E_CANTINSERT, "can't generate hyperslabs")
		}
		cl.offset, cl.offsetChanged = old.offset, old.offsetChanged
		ret.sel = cl
		return ret, nil
	}

	/* Last block is complete, simply mark that diminfo.opt is valid */
	sel.regular = true
	sel.spanLst = nil
	ret.sel = &sel
	return ret, nil
}

// h5sHyperMul multiplies with an overflow check.
func h5sHyperMul(a, b uint64) (uint64, error) {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return 0, errorf(H5E_DATASPACE, H5E_OVERFLOW, "number of elements in hyperslab selection overflows")
	}
	return lo, nil
}

// h5sHyperClipSpans returns the span tree with dimension dim clipped to
// 0..clipSize-1 (the H5S_SELECT_AND of H5S_hyper_clip_unlim), or nil when
// nothing remains.
func h5sHyperClipSpans(spans *h5sHyperSpanInfo, dim int, clipSize uint64) *h5sHyperSpanInfo {
	memo := map[*h5sHyperSpanInfo]*h5sHyperSpanInfo{}
	var clip func(spans *h5sHyperSpanInfo, d int) *h5sHyperSpanInfo
	clip = func(spans *h5sHyperSpanInfo, d int) *h5sHyperSpanInfo {
		if n, found := memo[spans]; found {
			return n
		}
		b := h5sHyperSpanBuilder{}
		for i := range spans.spans {
			span := spans.spans[i]
			if d == dim {
				if span.low >= clipSize {
					break
				}
				span.high = min(span.high, clipSize-1)
			} else if d < dim && span.down != nil {
				span.down = clip(span.down, d+1)
				if span.down == nil {
					continue
				}
			}
			_ = b.appendSpan(span.low, span.high, span.down)
		}
		n := b.finish()
		memo[spans] = n
		return n
	}
	return clip(spans, 0)
}

// h5sHyperGetClipExtentReal is H5S__hyper_get_clip_extent_real: the
// extent the unlimited selection clipSpace must be clipped to so that it
// contains numSlices slices in the unlimited dimension.
func h5sHyperGetClipExtentReal(clipSpace *h5s, numSlices uint64, inclTrail bool) uint64 {
	sel := clipSpace.selection()
	diminfo := &sel.opt[sel.unlimDim]

	switch {
	case numSlices == 0:
		if inclTrail {
			return diminfo.start
		}
		return 0
	case diminfo.block == h5sUnlimited || diminfo.block == diminfo.stride:
		/* Unlimited block, just set the extent large enough for the block size
		 * to match num_slices */
		return diminfo.start + numSlices
	}

	/* Unlimited count, need to match extent so a block (possibly) gets cut
	 * off so the number of slices matches num_slices */
	/* Calculate number of complete blocks in clip_space */
	count := numSlices / diminfo.block

	/* Calculate slices remaining */
	remSlices := numSlices - count*diminfo.block

	if remSlices > 0 {
		/* Must end extent in middle of partial block (or beginning of empty
		 * block if include_trailing_space and rem_slices == 0) */
		return diminfo.start + count*diminfo.stride + remSlices
	}
	if inclTrail {
		/* End extent just before first missing block */
		return diminfo.start + count*diminfo.stride
	}
	/* End extent at end of last block */
	return diminfo.start + (count-1)*diminfo.stride + diminfo.block
}

// h5sHyperGetClipExtent is H5S_hyper_get_clip_extent: the extent the
// unlimited selection clipSpace must be clipped to so that it contains as
// many elements as matchSpace.
func h5sHyperGetClipExtent(clipSpace, matchSpace *h5s, inclTrail bool) (uint64, error) {
	csel := clipSpace.selection()
	if csel.typ != h5sSelHyperslabs || csel.unlimDim < 0 || csel.numElemNonUnlim == 0 {
		return 0, errorf(H5E_DATASPACE, H5E_BADVALUE, "selection is not an unlimited hyperslab")
	}

	/* Check for "none" match space */
	var numSlices uint64
	if h5sGetSelectType(matchSpace) != h5sSelNone {
		/* Calculate number of slices */
		numSlices = h5sGetSelectNpoints(matchSpace) / csel.numElemNonUnlim
	}

	/* Call "real" get_clip_extent function */
	return h5sHyperGetClipExtentReal(clipSpace, numSlices, inclTrail), nil
}

// h5sHyperGetClipExtentMatch is H5S_hyper_get_clip_extent_match: the
// extent the unlimited selection clipSpace must be clipped to so that it
// contains as many elements as the unlimited selection matchSpace would
// after being clipped to matchClipSize.
func h5sHyperGetClipExtentMatch(clipSpace, matchSpace *h5s, matchClipSize uint64, inclTrail bool) (uint64, error) {
	csel, msel := clipSpace.selection(), matchSpace.selection()
	if csel.typ != h5sSelHyperslabs || csel.unlimDim < 0 || msel.typ != h5sSelHyperslabs || msel.unlimDim < 0 {
		return 0, errorf(H5E_DATASPACE, H5E_BADVALUE, "selection is not an unlimited hyperslab")
	}
	if csel.numElemNonUnlim != msel.numElemNonUnlim {
		return 0, errorf(H5E_DATASPACE, H5E_BADVALUE, "unlimited selections have different slice sizes")
	}
	matchDiminfo := &msel.opt[msel.unlimDim]

	/* Get initial count and block */
	count, block := h5sHyperGetClipDiminfo(matchDiminfo.start, matchDiminfo.stride, matchDiminfo.count, matchDiminfo.block, matchClipSize)

	/* Calculate number of slices */
	var numSlices uint64
	switch {
	case block == 0 || count == 0:
		/* Check for nothing returned */
		numSlices = 0
	case count == 1:
		/* Check for single block in unlimited dimension */
		numSlices = block
	default:
		/* Calculate initial num_slices */
		numSlices = block * count

		/* Check for partial last block */
		if matchClipSize < matchDiminfo.start {
			return 0, errorf(H5E_DATASPACE, H5E_BADVALUE, "clip size before start of selection")
		}
		if matchDiminfo.stride*(count-1)+block > matchClipSize-matchDiminfo.start {
			/* Subtract slices missing from last block */
			numSlices -= matchDiminfo.stride*(count-1) + block - (matchClipSize - matchDiminfo.start)
		}
	}

	/* Call "real" get_clip_extent function */
	return h5sHyperGetClipExtentReal(clipSpace, numSlices, inclTrail), nil
}

// h5sHyperGetUnlimBlock is H5S_hyper_get_unlim_block: a dataspace (with
// space's extent) selecting only the blockIndex'th block of the unlimited
// dimension of space's selection (whose count is unlimited).
func h5sHyperGetUnlimBlock(space *h5s, blockIndex uint64) (*h5s, error) {
	sel := space.selection()
	if sel.typ != h5sSelHyperslabs || sel.unlimDim < 0 || sel.opt[sel.unlimDim].count != h5sUnlimited {
		return nil, errorf(H5E_DATASPACE, H5E_BADVALUE, "selection does not have an unlimited count")
	}
	rank := int(space.extent.rank)
	start := make([]uint64, rank)
	stride := make([]uint64, rank)
	count := make([]uint64, rank)
	block := make([]uint64, rank)

	/* Set start to select block_indexth block in unlimited dimension and set
	 * count to 1 in that dimension to only select that block.  Copy all other
	 * diminfo parameters. */
	for u := range rank {
		if u == sel.unlimDim {
			s, ok := h5sMulAdd(blockIndex, sel.opt[u].stride, sel.opt[u].start)
			if !ok {
				return nil, errorf(H5E_DATASPACE, H5E_BADRANGE, "block index out of range")
			}
			start[u] = s
			count[u] = 1
		} else {
			start[u] = sel.opt[u].start
			count[u] = sel.opt[u].count
		}
		stride[u] = sel.opt[u].stride
		block[u] = sel.opt[u].block
	}

	/* Create output space, copy extent */
	spaceOut := h5sCopy(space)

	/* Select block as defined by start/stride/count/block computed above */
	if err := h5sSelectHyperslab(spaceOut, start, stride, count, block); err != nil {
		return nil, wrapf(err, H5E_DATASPACE, H5E_CANTINIT, "can't select hyperslab")
	}
	return spaceOut, nil
}

// h5sHyperGetFirstIncBlock is H5S_hyper_get_first_inc_block: the index of
// the first block in the unlimited dimension of space that would be
// incomplete or missing when space is clipped to clipSize, and whether
// that block is partial (rather than missing).
func h5sHyperGetFirstIncBlock(space *h5s, clipSize uint64) (uint64, bool, error) {
	sel := space.selection()
	if sel.typ != h5sSelHyperslabs || sel.unlimDim < 0 || sel.opt[sel.unlimDim].count != h5sUnlimited {
		return 0, false, errorf(H5E_DATASPACE, H5E_BADVALUE, "selection does not have an unlimited count")
	}
	diminfo := &sel.opt[sel.unlimDim]

	/* Check for selection outside of clip_size */
	if diminfo.start >= clipSize {
		return 0, false, nil
	}

	/* Calculate index of first incomplete block */
	ret := (clipSize - diminfo.start + diminfo.stride - diminfo.block) / diminfo.stride

	/* Check for partial block */
	partial := diminfo.stride*ret < clipSize-diminfo.start
	return ret, partial, nil
}
