package hdf5

// Tests for the dataspace and selection code (h5s.go, h5s_select.go,
// h5s_point.go, h5s_hyper.go) written from the mutants gremlins left
// alive. The expectations are libhdf5's: most come from
// testdata/h5s_selections.json, written by testdata/gen/gen_sel.c, which
// also calls the library-internal functions the Go code translates
// (H5S_get_select_offset, H5S_hyper_clip_unlim, H5S_extent_equal, ...;
// libhdf5 exports them); the others were checked with small h5cc programs
// calling the public API, as noted at each.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"runtime"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selMutFixtures are the sections of testdata/h5s_selections.json used
// here (see gen_sel.c).
type selMutFixtures struct {
	Cases   []selFixture `json:"cases"`
	Offsets []struct {
		Case       int         `json:"case"`
		Offset     []int64     `json:"offset"`
		Valid      bool        `json:"valid"`
		Low        []uint64    `json:"low"`
		High       []uint64    `json:"high"`
		ElemOffset *uint64     `json:"elem_offset"`
		Seqs       [][2]uint64 `json:"seqs"`
	} `json:"offsets"`
	Clips []struct {
		Rank          int         `json:"rank"`
		Start         []uint64    `json:"start"`
		Stride        []uint64    `json:"stride"`
		Count         []uint64    `json:"count"`
		Block         []uint64    `json:"block"`
		Clip          uint64      `json:"clip"`
		Type          int         `json:"type"`
		Npoints       uint64      `json:"npoints"`
		FirstIncBlock *uint64     `json:"first_inc_block"`
		Partial       bool        `json:"partial"`
		Low           []uint64    `json:"low"`
		High          []uint64    `json:"high"`
		Regular       *bool       `json:"regular"`
		Seqs          [][2]uint64 `json:"seqs"`
	} `json:"clips"`
	Extents []struct {
		Class      h5.SpaceClass `json:"class"`
		Dims       []uint64      `json:"dims"`
		Max        []uint64      `json:"max"`
		Version    uint8         `json:"version"`
		NpointsMax uint64        `json:"npoints_max"`
		Equal      []bool        `json:"equal"`
	} `json:"extents"`
	Decoded []selFixture `json:"decoded"`
}

func loadSelMutFixtures(t *testing.T) selMutFixtures {
	t.Helper()
	raw, err := os.ReadFile("testdata/h5s_selections.json")
	require.NoError(t, err)
	var fx selMutFixtures
	require.NoError(t, json.Unmarshal(raw, &fx))
	require.NotEmpty(t, fx.Offsets)
	require.NotEmpty(t, fx.Clips)
	require.NotEmpty(t, fx.Extents)
	require.NotEmpty(t, fx.Decoded)
	return fx
}

// seqsFromPoints turns the element iterator's coordinates into merged
// sequences of elmtSize-byte elements.
func seqsFromPoints(s *h5s, elmtSize uint64) [][2]uint64 {
	var out [][2]uint64
	pitch := h5sPitches(&s.extent)
	for c := range s.iterPoints() {
		var off uint64
		for u := range c {
			off += c[u] * pitch[u]
		}
		out = mergeSeqs(append(out, [2]uint64{off * elmtSize, elmtSize}))
	}
	return out
}

// H5Soffset_simple: the selection offset moves every kind of selection
// in the validity check, the bounds, the linear offset of the first
// element and the iterators (point lists, regular and irregular
// hyperslabs; "all" ignores it), up to the edges of the extent and one
// past them.
func TestMutationSelectionOffsets(t *testing.T) {
	fx := loadSelMutFixtures(t)
	types := map[int]bool{}
	for _, o := range fx.Offsets {
		c := fx.Cases[o.Case]
		what := fmt.Sprintf("%s offset %v", c.Name, o.Offset)
		s := decodeFixture(t, c, c.EncLatest)
		m, err := h5sSelectOffset(s, o.Offset)
		require.NoError(t, err, what)
		types[c.Type] = true

		valid := h5sSelectValid(m)
		edge := false
		if c.Type == h5sSelPoints {
			for u, h := range o.High {
				edge = edge || h == c.Dims[u]
			}
		}
		if edge {
			/* H5S__point_is_valid compares the highest point plus the
			 * offset with the extent using '>', accepting a point just
			 * past the end; h5sPointIsValid does not (see there). */
			assert.True(t, o.Valid, what)
			assert.False(t, valid, what+": valid")
		} else {
			assert.Equal(t, o.Valid, valid, what+": valid")
		}

		low, high, err := h5sGetSelectBounds(m)
		if o.Low != nil {
			require.NoError(t, err, what)
			assert.Equal(t, o.Low, low, what+": low")
			assert.Equal(t, o.High, high, what+": high")
		} else {
			assert.Error(t, err, what+": bounds")
		}

		off, err := h5sGetSelectOffset(m)
		if o.ElemOffset != nil {
			require.NoError(t, err, what)
			assert.Equal(t, *o.ElemOffset, off, what+": element offset")
		} else {
			assert.Error(t, err, what+": element offset")
		}

		if o.Seqs != nil && !edge {
			got := collectSeqs(m, 4)
			assert.Equal(t, mergeSeqs(o.Seqs), got, what+": sequences")
			if c.Type != h5sSelAll {
				/* (the element iterator applies the offset too) */
				assert.Equal(t, got, seqsFromPoints(m, 4), what+": elements")
			}
		}
	}
	assert.Len(t, types, 3, "points, hyperslabs and all")

	/* H5Soffset_simple accepts the largest rank and a NULL offset, which
	 * resets the offset (h5cc: both return 0) */
	dims := make([]uint64, h5sMaxRank)
	for u := range dims {
		dims[u] = 2
	}
	big, err := h5sCreateSimple(dims, nil)
	require.NoError(t, err)
	offset := make([]int64, h5sMaxRank)
	offset[h5sMaxRank-1] = 1
	_, err = h5sSelectOffset(big, offset)
	assert.NoError(t, err)
	s := selectHyper(t, newSpace(t, 10), []uint64{2}, nil, []uint64{3}, nil)
	m, err := h5sSelectOffset(s, []int64{4})
	require.NoError(t, err)
	r, err := h5sSelectOffset(m, nil)
	require.NoError(t, err)
	assert.True(t, r.sel.offsetChanged)
	assert.Equal(t, []int64{0}, r.sel.offset)
	assert.Equal(t, [][2]uint64{{2, 3}}, collectSeqs(r, 1))
}

// H5S_hyper_clip_unlim and H5S_hyper_get_first_inc_block on unlimited
// counts and blocks, including clip sizes near 2^64 where the count
// computations must not overflow, and a last block cut by the clip.
func TestMutationSelectionClips(t *testing.T) {
	fx := loadSelMutFixtures(t)
	for _, c := range fx.Clips {
		what := fmt.Sprintf("%v/%v/%v/%v clip %d", c.Start, c.Stride, c.Count, c.Block, c.Clip)
		s, err := h5sCreateSimple([]uint64{10, 10}[:c.Rank], []uint64{h5sUnlimited, 10}[:c.Rank])
		require.NoError(t, err)
		require.NoError(t, h5sSelectHyperslab(s, c.Start, c.Stride, c.Count, c.Block))

		got, err := h5sHyperClipUnlim(s, c.Clip)
		require.NoError(t, err, what)
		assert.Equal(t, c.Type, h5sGetSelectType(got), what+": type")
		assert.Equal(t, c.Npoints, h5sGetSelectNpoints(got), what+": npoints")
		if c.Low != nil {
			low, high, err := h5sGetSelectBounds(got)
			require.NoError(t, err, what)
			assert.Equal(t, c.Low, low, what+": low")
			assert.Equal(t, c.High, high, what+": high")
			assert.Equal(t, *c.Regular, h5sSelectIsRegular(got), what+": regular")
		}
		inExtent := c.Seqs != nil
		for u, h := range c.High {
			inExtent = inExtent && h < []uint64{10, 10}[u]
		}
		if inExtent {
			assert.Equal(t, mergeSeqs(c.Seqs), collectSeqs(got, 1), what+": sequences")
		}

		if c.FirstIncBlock != nil {
			first, partial, err := h5sHyperGetFirstIncBlock(s, c.Clip)
			require.NoError(t, err, what)
			assert.Equal(t, *c.FirstIncBlock, first, what+": first incomplete block")
			assert.Equal(t, c.Partial, partial, what+": partial")
		}
	}
}

// H5S_create's extent version, H5S_get_npoints_max and H5S_extent_equal.
func TestMutationExtents(t *testing.T) {
	fx := loadSelMutFixtures(t)
	var spaces []*h5s
	for i, e := range fx.Extents {
		var s *h5s
		if e.Class == h5.SpaceSimple {
			var err error
			s, err = h5sCreateSimple(e.Dims, e.Max)
			require.NoError(t, err)
		} else {
			s = h5sCreate(e.Class)
		}
		spaces = append(spaces, s)
		what := fmt.Sprintf("class %d dims %v max %v", e.Class, e.Dims, e.Max)
		assert.Equal(t, e.Version, s.extent.version, what+": version")
		assert.Equal(t, e.NpointsMax, h5sGetNpointsMax(s), what+": npoints max")
		for j := range i + 1 {
			assert.Equal(t, e.Equal[j], h5sExtentEqual(s, spaces[j]), "%s equal to %d", what, j)
		}
	}
}

// Hand-modified encodings: an irregular hyperslab with a zero-sized
// block, which H5S__hyper_deserialize ORs into the selection as a no-op.
func TestMutationSelectionDecoded(t *testing.T) {
	fx := loadSelMutFixtures(t)
	for _, c := range fx.Decoded {
		require.NotNil(t, c.DecLatest, c.Name)
		s := decodeFixture(t, c, c.EncLatest)
		checkFixture(t, *c.DecLatest, s, c.Name)
	}
}

// The decoded irregular hyperslabs list their blocks for the region
// reference values (h5rSelectionPublic reads h5sSelection.blocks), as
// H5Sget_select_hyper_blocklist does.
func TestMutationSelectionDecodedBlocks(t *testing.T) {
	fx := loadSelMutFixtures(t)
	n := 0
	for _, c := range fx.Cases {
		if c.DecLatest == nil || c.DecLatest.Regular == nil || *c.DecLatest.Regular || c.DecLatest.Blocks == nil {
			continue
		}
		buf := selectionBytes(t, c.EncLatest)
		sel, err := h5sSelectDeserialize(nil, &image{buf: buf, sizeofAddr: 8, sizeofSize: 8})
		require.NoError(t, err, c.Name)
		pub, err := h5rSelectionPublic(sel, uint32(len(c.Dims)))
		require.NoError(t, err, c.Name)
		var want [][2][]uint64
		for _, b := range c.DecLatest.Blocks {
			want = append(want, [2][]uint64{b[0], b[1]})
		}
		assert.Equal(t, want, pub.Blocks, c.Name)
		n++
	}
	assert.Greater(t, n, 3)
}

// H5S_select_shape_same iterates over the blocks of selections of
// different types; a "none" selection has no block (H5S__none_iter_block
// fails), so comparing it with a non-"none" selection of no elements (an
// "all" selection of an empty extent) fails in libhdf5 (h5cc:
// H5Sselect_shape_same returns -1 for both orders and both rank orders).
func TestMutationShapeSameNoBlocks(t *testing.T) {
	none2 := newSpace(t, 2, 3)
	h5sSelectNone(none2)
	none1 := newSpace(t, 0)
	h5sSelectNone(none1)
	all0 := newSpace(t, 0)
	all20 := newSpace(t, 2, 0)
	for _, c := range [][2]*h5s{{none2, all0}, {all0, none2}, {all20, none1}, {none1, all20}} {
		_, err := h5sSelectShapeSame(c[0], c[1])
		assert.Error(t, err)
	}
	/* Two empty "all" selections compare their extents (h5cc: 0) */
	same, err := h5sSelectShapeSame(all20, all0)
	require.NoError(t, err)
	assert.False(t, same)
}

// The span count bound of the span tree builders (a Go addition: the C
// library has no bound) counts every span appended, in the union of
// decoded blocks and in a projection.
func TestMutationSpanBudgets(t *testing.T) {
	/* Three separate blocks: three spans */
	blocks := [][2][]uint64{{{0}, {0}}, {{2}, {2}}, {{4}, {4}}}
	idx := []int{0, 1, 2}
	budget := 3
	spans, err := h5sHyperSpansFromBlocksHelper(1, 0, blocks, idx, &budget)
	require.NoError(t, err)
	assert.Len(t, spans.spans, 3)
	assert.Zero(t, budget)
	budget = 2
	_, err = h5sHyperSpansFromBlocksHelper(1, 0, blocks, idx, &budget)
	assert.Error(t, err)

	/* Projecting rows 0..1 of a 2x4 extent intersected with columns 0
	 * and 2: two ranges in each of two rows */
	ss, err := h5sHyperSpansFromBlocks(2, [][2][]uint64{{{0, 0}, {1, 3}}})
	require.NoError(t, err)
	sis, err := h5sHyperSpansFromBlocks(2, [][2][]uint64{{{0, 0}, {1, 0}}, {{0, 2}, {1, 2}}})
	require.NoError(t, err)
	newState := func(budget int) *h5sHyperProjIntState {
		return &h5sHyperProjIntState{
			nelem:  map[*h5sHyperSpanInfo]uint64{},
			ranges: map[[2]*h5sHyperSpanInfo][]h5sIdxRange{},
			budget: budget,
		}
	}
	ranges, err := h5sHyperProjIntRanges(ss, sis, newState(4))
	require.NoError(t, err)
	assert.Equal(t, []h5sIdxRange{{0, 0}, {2, 2}, {4, 4}, {6, 6}}, ranges)
	_, err = h5sHyperProjIntRanges(ss, sis, newState(3))
	assert.Error(t, err)
}

// An unlimited selection (here unlimited in the first dimension) has no
// elements to iterate.
func TestMutationUnlimitedIteration(t *testing.T) {
	s, err := h5sCreateSimple([]uint64{10, 10}, []uint64{h5sUnlimited, 10})
	require.NoError(t, err)
	require.NoError(t, h5sSelectHyperslab(s, []uint64{1, 8}, []uint64{4, 1}, []uint64{h5sUnlimited, 1}, []uint64{2, 2}))
	require.Equal(t, 0, h5sGetSelectUnlimDim(s))
	first := func(seq iter.Seq[[]uint64]) int {
		n := 0
		for range seq {
			if n++; n == 100 {
				break
			}
		}
		return n
	}
	assert.Zero(t, first(s.iterPoints()))
	n := 0
	for range h5sSelectIterBlocks(s) {
		if n++; n == 100 {
			break
		}
	}
	assert.Zero(t, n)
}

// Decoding bounds: the claimed number of blocks of an irregular hyperslab
// is checked against the bytes left before anything is allocated, and
// the failure is recorded in the image; a point selection of the largest
// rank decodes (H5S__point_deserialize accepts rank <= H5S_MAX_RANK;
// libhdf5 itself then overflows its point node allocation, a free list of
// at most H5S_MAX_RANK coordinates for rank + 1, so it cannot be
// compared).
func TestMutationSelectionDecodeBounds(t *testing.T) {
	/* Version 3, irregular, 8-byte values, rank 32, 2^18 blocks claimed,
	 * one present */
	b := binary.LittleEndian.AppendUint32(nil, h5sSelHyperslabs)
	b = binary.LittleEndian.AppendUint32(b, 3)
	b = append(b, 0, 8)
	b = binary.LittleEndian.AppendUint32(b, 32)
	b = binary.LittleEndian.AppendUint64(b, 1<<18)
	for range 2 * 32 {
		b = binary.LittleEndian.AppendUint64(b, 1)
	}
	p := &image{buf: b, sizeofAddr: 8, sizeofSize: 8}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := h5sSelectDeserialize(nil, p)
	runtime.ReadMemStats(&after)
	assert.Error(t, err)
	assert.Error(t, p.done())
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20), "allocated before checking the block count")

	/* Points, version 2, 2-byte values, rank 32, two points */
	b = binary.LittleEndian.AppendUint32(nil, h5sSelPoints)
	b = binary.LittleEndian.AppendUint32(b, 2)
	b = append(b, 2)
	b = binary.LittleEndian.AppendUint32(b, 32)
	b = binary.LittleEndian.AppendUint16(b, 2)
	for i := range 2 * 32 {
		b = binary.LittleEndian.AppendUint16(b, uint16(i%3))
	}
	p = &image{buf: b, sizeofAddr: 8, sizeofSize: 8}
	sel, err := h5sSelectDeserialize(nil, p)
	require.NoError(t, err)
	assert.Zero(t, p.remaining())
	assert.Equal(t, uint32(32), sel.rank)
	assert.Equal(t, uint64(2), sel.numElem)
	assert.Equal(t, uint64(2), sel.points[1][0])
}

// Smaller translations checked against the C code paths.
func TestMutationSelectionMisc(t *testing.T) {
	/* H5S__hyper_span_nblocks of no span tree */
	assert.Zero(t, h5sHyperSpanNblocks(nil))

	/* H5S__hyper_make_spans does not compute the start of a block past
	 * the last one (which would overflow here) */
	spans, err := h5sHyperMakeSpans(1, []uint64{^uint64(0) - 1}, []uint64{4}, []uint64{1}, []uint64{1})
	require.NoError(t, err)
	assert.Equal(t, []h5sHyperSpan{{low: ^uint64(0) - 1, high: ^uint64(0) - 1}}, spans.spans)

	/* H5Sselect_hyperslab: at most one unlimited dimension, whichever
	 * comes first (h5cc: -1 for both) */
	s, err := h5sCreateSimple([]uint64{10, 10}, []uint64{h5sUnlimited, h5sUnlimited})
	require.NoError(t, err)
	assert.Error(t, h5sSelectHyperslab(s, []uint64{0, 0}, []uint64{2, 2}, []uint64{h5sUnlimited, 1}, []uint64{1, h5sUnlimited}))
	assert.Error(t, h5sSelectHyperslab(s, []uint64{0, 0}, []uint64{2, 2}, []uint64{1, h5sUnlimited}, []uint64{h5sUnlimited, 1}))

	/* H5Sselect_adjust of an irregular selection: moving it before the
	 * start of the extent fails, moving it back is fine (h5cc: -1, then
	 * 0 with bounds (1,0)-(4,4)) */
	irr := withSel(t, mustBlocksSel(t, 2, [][2][]uint64{{{0, 0}, {1, 1}}, {{1, 1}, {3, 4}}}), 8, 10)
	require.False(t, irr.sel.regular)
	_, err = h5sSelectAdjustS(irr, []int64{0, 1})
	assert.Error(t, err)
	a, err := h5sSelectAdjustS(irr, []int64{-1, 0})
	require.NoError(t, err)
	low, high, err := h5sGetSelectBounds(a)
	require.NoError(t, err)
	assert.Equal(t, []uint64{1, 0}, low)
	assert.Equal(t, []uint64{4, 4}, high)

	/* H5S__get_select_elem_pointlist: numpoints points from startpoint,
	 * stopping at the end of the list (h5cc H5Sget_select_elem_pointlist
	 * (0, 2): (1,1) (2,2); (1, 2): (2,2) (3,3)) */
	pts := newSpace(t, 5, 5)
	h5sSelectElements(pts, [][]uint64{{1, 1}, {2, 2}, {3, 3}})
	assert.Equal(t, [][]uint64{{1, 1}, {2, 2}}, h5sGetSelectElemPointlist(pts, 0, 2))
	assert.Equal(t, [][]uint64{{2, 2}, {3, 3}}, h5sGetSelectElemPointlist(pts, 1, 2))
	assert.Equal(t, [][]uint64{{2, 2}, {3, 3}}, h5sGetSelectElemPointlist(pts, 1, 3))
}

// The span count bound of h5sHyperMakeSpans (a Go addition) admits
// h5sHyperMaxSpans spans in all: a dimension of that many blocks passes
// it (and fails on its zero block size instead), one more span in another
// dimension does not.
func TestMutationMakeSpansBound(t *testing.T) {
	_, err := h5sHyperMakeSpans(1, []uint64{0}, []uint64{1}, []uint64{h5sHyperMaxSpans}, []uint64{0})
	assert.ErrorContains(t, err, "invalid hyperslab block")
	_, err = h5sHyperMakeSpans(2, []uint64{0, 0}, []uint64{1, 1}, []uint64{h5sHyperMaxSpans, 1}, []uint64{0, 1})
	assert.ErrorContains(t, err, "too many hyperslab spans")
}

// The block list bound of h5sHyperListBlocks (a Go addition) admits
// h5sHyperMaxBlocks blocks: 2048 rows sharing one row of 2048 blocks.
func TestMutationListBlocksBound(t *testing.T) {
	const n = 2048
	require.Equal(t, n*n, h5sHyperMaxBlocks)
	row := make([]h5sHyperSpan, n)
	for i := range row {
		row[i] = h5sHyperSpan{low: 2 * uint64(i), high: 2 * uint64(i)}
	}
	down := h5sHyperNewSpanInfo(row)
	rows := make([]h5sHyperSpan, n)
	for i := range rows {
		rows[i] = h5sHyperSpan{low: 2 * uint64(i), high: 2 * uint64(i), down: down}
	}
	sel := &h5sSelection{typ: h5sSelHyperslabs, rank: 2, spanLst: h5sHyperNewSpanInfo(rows), unlimDim: -1}
	require.NoError(t, h5sHyperListBlocks(sel))
	assert.Len(t, sel.blocks, h5sHyperMaxBlocks)
	assert.Equal(t, [2][]uint64{{2*n - 2, 2*n - 2}, {2*n - 2, 2*n - 2}}, sel.blocks[n*n-1])

	/* one more row is too many */
	sel.spanLst = h5sHyperNewSpanInfo(append(rows, h5sHyperSpan{low: 2 * n, high: 2 * n, down: down}))
	sel.blocks = nil
	assert.Error(t, h5sHyperListBlocks(sel))
	assert.Nil(t, sel.blocks)
}
