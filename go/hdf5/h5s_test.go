package hdf5

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HDFGroup/hdf5/go/h5"
)

// newSpace makes a simple dataspace with an "all" selection.
func newSpace(t *testing.T, dims ...uint64) *h5s {
	t.Helper()
	s, err := h5sCreateSimple(dims, nil)
	require.NoError(t, err)
	return s
}

// selectHyper selects a regular hyperslab in a copy of s.
func selectHyper(t *testing.T, s *h5s, start, stride, count, block []uint64) *h5s {
	t.Helper()
	r, err := h5sSelectHyperslabPublic(s, h5.Hyperslab{Start: start, Stride: stride, Count: count, Block: block})
	require.NoError(t, err)
	return r
}

// withSel attaches a decoded selection to a new dataspace of the given
// dims.
func withSel(t *testing.T, sel *h5sSelection, dims ...uint64) *h5s {
	t.Helper()
	s := newSpace(t, dims...)
	r, err := h5sNew(&s.extent, sel)
	require.NoError(t, err)
	return r
}

func collectSeqs(s *h5s, elmtSize uint64) [][2]uint64 {
	var out [][2]uint64
	for q := range s.iterSeqs(elmtSize) {
		out = append(out, [2]uint64{q.off, q.len})
	}
	return out
}

func collectPoints(s *h5s) [][]uint64 {
	var out [][]uint64
	for c := range s.iterPoints() {
		out = append(out, append([]uint64(nil), c...))
	}
	return out
}

// mergeSeqs merges adjacent sequences (the C iterator does not always
// return maximal runs).
func mergeSeqs(in [][2]uint64) [][2]uint64 {
	var out [][2]uint64
	for _, q := range in {
		if n := len(out); n > 0 && out[n-1][0]+out[n-1][1] == q[0] {
			out[n-1][1] += q[1]
			continue
		}
		out = append(out, q)
	}
	return out
}

func TestRegularIteration(t *testing.T) {
	// 1D: start 2, stride 5, count 3, block 2 in 20 elements.
	s := selectHyper(t, newSpace(t, 20), []uint64{2}, []uint64{5}, []uint64{3}, []uint64{2})
	assert.Equal(t, [][2]uint64{{2, 2}, {7, 2}, {12, 2}}, collectSeqs(s, 1))
	assert.Equal(t, [][2]uint64{{16, 16}, {56, 16}, {96, 16}}, collectSeqs(s, 8))
	assert.Equal(t, [][]uint64{{2}, {3}, {7}, {8}, {12}, {13}}, collectPoints(s))
	assert.Equal(t, uint64(6), h5sGetSelectNpoints(s))
	assert.True(t, h5sSelectIsRegular(s))
	assert.False(t, h5sSelectIsContiguous(s))

	// 1D stride == block merges into one run.
	s = selectHyper(t, newSpace(t, 20), []uint64{4}, []uint64{3}, []uint64{4}, []uint64{3})
	assert.Equal(t, [][2]uint64{{4, 12}}, collectSeqs(s, 1))
	assert.True(t, h5sSelectIsContiguous(s))
	assert.True(t, h5sSelectIsSingle(s))

	// 2D 6x8: rows 1,4 (stride 3, block 2 -> rows 1,2,4,5), columns 0..1 and 4..5.
	s = selectHyper(t, newSpace(t, 6, 8), []uint64{1, 0}, []uint64{3, 4}, []uint64{2, 2}, []uint64{2, 2})
	var want [][2]uint64
	for _, r := range []uint64{1, 2, 4, 5} {
		want = append(want, [2]uint64{r*8 + 0, 2}, [2]uint64{r*8 + 4, 2})
	}
	assert.Equal(t, want, collectSeqs(s, 1))
	assert.Equal(t, uint64(16), h5sGetSelectNpoints(s))
	start, end, err := h5sGetSelectBounds(s)
	require.NoError(t, err)
	assert.Equal(t, []uint64{1, 0}, start)
	assert.Equal(t, []uint64{5, 5}, end)
	off, err := h5sGetSelectOffset(s)
	require.NoError(t, err)
	assert.Equal(t, uint64(8), off)

	// 2D full rows flatten into one run per block of rows.
	s = selectHyper(t, newSpace(t, 6, 8), []uint64{1, 0}, []uint64{3, 1}, []uint64{2, 1}, []uint64{2, 8})
	assert.Equal(t, [][2]uint64{{8, 16}, {32, 16}}, collectSeqs(s, 1))

	// 3D 3x4x5 with stride and block in every dimension.
	s = selectHyper(t, newSpace(t, 3, 4, 5), []uint64{0, 1, 0}, []uint64{2, 2, 3}, []uint64{2, 2, 2}, []uint64{1, 1, 2})
	want = nil
	for _, i := range []uint64{0, 2} {
		for _, j := range []uint64{1, 3} {
			for _, k := range []uint64{0, 3} {
				want = append(want, [2]uint64{(i*4+j)*5 + k, 2})
			}
		}
	}
	assert.Equal(t, want, collectSeqs(s, 1))
	pts := collectPoints(s)
	assert.Len(t, pts, 16)
	assert.Equal(t, []uint64{0, 1, 0}, pts[0])
	assert.Equal(t, []uint64{2, 3, 4}, pts[15])

	// "all" and "none".
	all := newSpace(t, 3, 4)
	assert.Equal(t, [][2]uint64{{0, 48}}, collectSeqs(all, 4))
	assert.Len(t, collectPoints(all), 12)
	none := newSpace(t, 3, 4)
	h5sSelectNone(none)
	assert.Empty(t, collectSeqs(none, 4))
	assert.Empty(t, collectPoints(none))

	// Scalar.
	scalar := h5sCreate(h5.SpaceScalar)
	assert.Equal(t, [][2]uint64{{0, 8}}, collectSeqs(scalar, 8))
	assert.Equal(t, [][]uint64{nil}, collectPoints(scalar))

	// Zero count selects nothing.
	s = selectHyper(t, newSpace(t, 10), []uint64{1}, nil, []uint64{0}, nil)
	assert.Equal(t, h5sSelNone, h5sGetSelectType(s))

	// Overlapping blocks are rejected.
	_, err = h5sSelectHyperslabPublic(newSpace(t, 10), h5.Hyperslab{Start: []uint64{0}, Stride: []uint64{1}, Count: []uint64{2}, Block: []uint64{2}})
	assert.Error(t, err)

	// A selection outside the extent is not valid and yields nothing.
	s = selectHyper(t, newSpace(t, 10), []uint64{8}, nil, []uint64{4}, nil)
	assert.False(t, h5sSelectValid(s))
	assert.Empty(t, collectSeqs(s, 1))
}

func TestSelectionOffsetAndAdjust(t *testing.T) {
	s := selectHyper(t, newSpace(t, 10, 10), []uint64{1, 1}, nil, []uint64{2, 3}, nil)
	o, err := h5sSelectOffset(s, []int64{2, -1})
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{30, 3}, {40, 3}}, collectSeqs(o, 1))
	start, end, err := h5sGetSelectBounds(o)
	require.NoError(t, err)
	assert.Equal(t, []uint64{3, 0}, start)
	assert.Equal(t, []uint64{4, 2}, end)
	// The original is unchanged.
	assert.Equal(t, [][2]uint64{{11, 3}, {21, 3}}, collectSeqs(s, 1))

	// Normalizing moves the selection by the offset; denormalizing restores it.
	n, old, ok, err := h5sHyperNormalizeOffset(o)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []int64{2, -1}, old)
	assert.Equal(t, [][2]uint64{{30, 3}, {40, 3}}, collectSeqs(n, 1))
	assert.Equal(t, []uint64{3, 0}, n.sel.start)
	d, err := h5sHyperDenormalizeOffset(n, old)
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{30, 3}, {40, 3}}, collectSeqs(d, 1))
	assert.Equal(t, []uint64{1, 1}, d.sel.start)

	// adjust_u on hyperslabs (regular and irregular) and points.
	a, err := h5sSelectAdjustU(s, []uint64{1, 1})
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{0, 3}, {10, 3}}, collectSeqs(a, 1))
	_, err = h5sSelectAdjustU(s, []uint64{2, 0})
	assert.Error(t, err)

	irr := withSel(t, mustBlocksSel(t, 2, [][2][]uint64{{{1, 1}, {1, 2}}, {{2, 4}, {3, 4}}}), 10, 10)
	require.False(t, irr.sel.regular)
	a, err = h5sSelectAdjustU(irr, []uint64{1, 1})
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{0, 2}, {13, 1}, {23, 1}}, collectSeqs(a, 1))
	assert.Equal(t, [][2][]uint64{{{0, 0}, {0, 1}}, {{1, 3}, {2, 3}}}, a.sel.blocks)

	pts := newSpace(t, 5, 5)
	h5sSelectElements(pts, [][]uint64{{4, 4}, {1, 2}})
	a, err = h5sSelectAdjustU(pts, []uint64{1, 2})
	require.NoError(t, err)
	assert.Equal(t, [][]uint64{{3, 2}, {0, 0}}, collectPoints(a))
	_, err = h5sSelectAdjustU(pts, []uint64{2, 0})
	assert.Error(t, err)
}

// mustBlocksSel builds an irregular selection from blocks, as the
// deserializer does.
func mustBlocksSel(t *testing.T, rank uint32, blocks [][2][]uint64) *h5sSelection {
	t.Helper()
	spans, err := h5sHyperSpansFromBlocks(int(rank), blocks)
	require.NoError(t, err)
	sel, err := h5sHyperSelectionFromSpans(rank, spans)
	require.NoError(t, err)
	require.NoError(t, h5sHyperListBlocks(sel))
	return sel
}

func TestIrregularSpans(t *testing.T) {
	// Overlapping blocks are merged into one span tree.
	sel := mustBlocksSel(t, 2, [][2][]uint64{
		{{0, 0}, {1, 1}},
		{{1, 1}, {3, 4}},
		{{5, 0}, {5, 9}},
	})
	assert.False(t, sel.regular)
	assert.Equal(t, uint64(4+3+8+10), sel.numElem)
	assert.Equal(t, [][2][]uint64{
		{{0, 0}, {0, 1}}, {{1, 0}, {1, 4}}, {{2, 1}, {3, 4}}, {{5, 0}, {5, 9}},
	}, sel.blocks)
	s := withSel(t, sel, 8, 10)
	assert.Equal(t, [][2]uint64{{0, 2}, {10, 5}, {21, 4}, {31, 4}, {50, 10}}, collectSeqs(s, 1))

	// Adjacent blocks that form a regular pattern are rebuilt.
	sel = mustBlocksSel(t, 2, [][2][]uint64{{{0, 4}, {1, 5}}, {{0, 0}, {1, 1}}})
	assert.True(t, sel.regular)
	assert.Equal(t, []uint64{0, 0}, sel.start)
	assert.Equal(t, []uint64{1, 4}, sel.stride)
	assert.Equal(t, []uint64{1, 2}, sel.count)
	assert.Equal(t, []uint64{2, 2}, sel.block)

	// Blocks that touch merge into one block.
	sel = mustBlocksSel(t, 1, [][2][]uint64{{{0}, {3}}, {{4}, {9}}})
	assert.True(t, sel.regular)
	assert.Equal(t, []uint64{10}, sel.block)

	// Intersections with blocks.
	s = withSel(t, mustBlocksSel(t, 2, [][2][]uint64{{{0, 0}, {0, 1}}, {{4, 4}, {5, 5}}}), 8, 8)
	for _, c := range []struct {
		start, end []uint64
		want       bool
	}{
		{[]uint64{0, 0}, []uint64{0, 0}, true},
		{[]uint64{1, 0}, []uint64{3, 7}, false},
		{[]uint64{0, 2}, []uint64{3, 7}, false},
		{[]uint64{3, 3}, []uint64{4, 4}, true},
		{[]uint64{5, 0}, []uint64{7, 4}, true},
		{[]uint64{6, 0}, []uint64{7, 7}, false},
		{[]uint64{1, 2}, []uint64{3, 3}, false},
	} {
		got, err := h5sSelectIntersectBlock(s, c.start, c.end)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "%v-%v", c.start, c.end)
	}
}

func TestProjectIntersection(t *testing.T) {
	// A 4x4 block of a 10x10 dataset mapped to a 16-element 1D buffer;
	// intersect with rows 3..4, columns 0..9: the second and third rows of
	// the block, i.e. buffer elements 4..11.
	src := selectHyper(t, newSpace(t, 10, 10), []uint64{2, 3}, nil, []uint64{1, 1}, []uint64{4, 4})
	dst := newSpace(t, 16)
	sis := selectHyper(t, newSpace(t, 10, 10), []uint64{3, 0}, nil, []uint64{1, 1}, []uint64{2, 10})
	p, err := h5sSelectProjectIntersection(src, dst, sis)
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{4, 8}}, collectSeqs(p, 1))
	assert.True(t, p.sel.regular)

	// Column intersection: every row of the block contributes its first
	// two elements.
	sis = selectHyper(t, newSpace(t, 10, 10), []uint64{0, 0}, nil, []uint64{1, 1}, []uint64{10, 5})
	p, err = h5sSelectProjectIntersection(src, dst, sis)
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{0, 2}, {4, 2}, {8, 2}, {12, 2}}, collectSeqs(p, 1))
	assert.True(t, p.sel.regular)
	assert.Equal(t, []uint64{4}, p.sel.stride)

	// No intersection selects none; "all" intersect copies dst.
	sis = selectHyper(t, newSpace(t, 10, 10), []uint64{8, 8}, nil, []uint64{1, 1}, []uint64{2, 2})
	p, err = h5sSelectProjectIntersection(src, dst, sis)
	require.NoError(t, err)
	assert.Equal(t, h5sSelNone, h5sGetSelectType(p))
	p, err = h5sSelectProjectIntersection(src, dst, newSpace(t, 10, 10))
	require.NoError(t, err)
	assert.Equal(t, h5sSelAll, h5sGetSelectType(p))

	// A point intersect selection in any order (the C library's
	// H5S_hyper_add_span_element mishandles points that are not in
	// increasing order; the union built here does not).
	pts := newSpace(t, 6, 6)
	h5sSelectElements(pts, [][]uint64{{0, 1}, {5, 5}, {2, 3}})
	p, err = h5sSelectProjectIntersection(newSpace(t, 6, 6), newSpace(t, 36), pts)
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{1, 1}, {15, 1}, {35, 1}}, collectSeqs(p, 1))

	// Different element counts are rejected.
	_, err = h5sSelectProjectIntersection(src, newSpace(t, 15), sis)
	assert.Error(t, err)

	// Scalar destination.
	one := selectHyper(t, newSpace(t, 10, 10), []uint64{3, 3}, nil, []uint64{1, 1}, nil)
	sis = selectHyper(t, newSpace(t, 10, 10), []uint64{0, 0}, nil, []uint64{1, 1}, []uint64{4, 4})
	p, err = h5sSelectProjectIntersection(one, h5sCreate(h5.SpaceScalar), sis)
	require.NoError(t, err)
	assert.Equal(t, h5sSelAll, h5sGetSelectType(p))
}

func TestUnlimitedSelection(t *testing.T) {
	// VDS-style unlimited count: rows 1,2 then every 4th pair, columns 8..9.
	s := newSpace(t, 10, 10)
	require.NoError(t, h5sSelectHyperslab(s, []uint64{1, 8}, []uint64{4, 1}, []uint64{h5sUnlimited, 1}, []uint64{2, 2}))
	assert.Equal(t, 0, h5sGetSelectUnlimDim(s))
	assert.Equal(t, h5sUnlimited, h5sGetSelectNpoints(s))
	n, err := h5sGetSelectNumElemNonUnlim(s)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)
	assert.False(t, h5sSelectValid(s))
	assert.Empty(t, collectSeqs(s, 1))

	// Clip to 7 rows: blocks at rows 1-2 and 5-6.
	c, err := h5sHyperClipUnlim(s, 7)
	require.NoError(t, err)
	assert.Equal(t, uint64(8), h5sGetSelectNpoints(c))
	assert.Equal(t, [][2]uint64{{18, 2}, {28, 2}, {58, 2}, {68, 2}}, collectSeqs(c, 1))
	// Clip to 6 rows: the last block is partial.
	c, err = h5sHyperClipUnlim(s, 6)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), h5sGetSelectNpoints(c))
	assert.Equal(t, [][2]uint64{{18, 2}, {28, 2}, {58, 2}}, collectSeqs(c, 1))
	// Clip before the start: none.
	c, err = h5sHyperClipUnlim(s, 1)
	require.NoError(t, err)
	assert.Equal(t, h5sSelNone, h5sGetSelectType(c))

	// Clip extents.
	m := selectHyper(t, newSpace(t, 100), []uint64{0}, nil, []uint64{1}, []uint64{6})
	e, err := h5sHyperGetClipExtent(s, m, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), e) // 3 slices: rows 1, 2 and 5
	first, partial, err := h5sHyperGetFirstIncBlock(s, 6)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), first)
	assert.True(t, partial)
	blk, err := h5sHyperGetUnlimBlock(s, 1)
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{58, 2}, {68, 2}}, collectSeqs(blk, 1))

	// Unlimited block.
	u := newSpace(t, 10, 10)
	require.NoError(t, h5sSelectHyperslab(u, []uint64{3, 0}, nil, []uint64{1, 1}, []uint64{h5sUnlimited, 10}))
	c, err = h5sHyperClipUnlim(u, 5)
	require.NoError(t, err)
	assert.Equal(t, [][2]uint64{{30, 20}}, collectSeqs(c, 1))
	e, err = h5sHyperGetClipExtentMatch(u, u, 5, true)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), e)
}

// selFixtures is testdata/h5s_selections.json, written by
// testdata/gen/gen_sel.c with the C library.
type selFixture struct {
	Name        string       `json:"name"`
	Dims        []uint64     `json:"dims"`
	Type        int          `json:"type"`
	Npoints     uint64       `json:"npoints"`
	EncEarliest string       `json:"enc_earliest"`
	EncLatest   string       `json:"enc_latest"`
	Low         []uint64     `json:"low"`
	High        []uint64     `json:"high"`
	Regular     *bool        `json:"regular"`
	Blocks      [][][]uint64 `json:"blocks"`
	Points      [][]uint64   `json:"points"`
	Seqs        [][2]uint64  `json:"seqs"`
	// What the C library reports after H5Sdecode of the encodings.
	DecEarliest *selFixture `json:"dec_earliest"`
	DecLatest   *selFixture `json:"dec_latest"`
}

type selFixtures struct {
	Cases       []selFixture `json:"cases"`
	Projections []struct {
		Name   string     `json:"name"`
		Src    selFixture `json:"src"`
		Dst    selFixture `json:"dst"`
		Sis    selFixture `json:"sis"`
		Result selFixture `json:"result"`
	} `json:"projections"`
	ShapeSame      [][3]any      `json:"shape_same"`
	IntersectBlock [][3]any      `json:"intersect_block"`
	Blocks         [][2][]uint64 `json:"blocks"`
}

func loadSelFixtures(t *testing.T) selFixtures {
	t.Helper()
	raw, err := os.ReadFile("testdata/h5s_selections.json")
	require.NoError(t, err)
	var fx selFixtures
	require.NoError(t, json.Unmarshal(raw, &fx))
	require.NotEmpty(t, fx.Cases)
	return fx
}

// selectionBytes returns the serialized selection of an H5Sencode2 buffer
// (type, version, sizeof(size), extent size, extent message, selection).
func selectionBytes(t *testing.T, enc string) []byte {
	t.Helper()
	b, err := hex.DecodeString(enc)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(b), 7)
	extSize := int(le32(b[3:]))
	return b[7+extSize:]
}

// decodeFixture decodes a fixture's encoding into a dataspace.
func decodeFixture(t *testing.T, fx selFixture, enc string) *h5s {
	t.Helper()
	buf := selectionBytes(t, enc)
	p := &image{buf: buf, sizeofAddr: 8, sizeofSize: 8}
	sel, err := h5sSelectDeserialize(nil, p)
	require.NoError(t, err, fx.Name)
	assert.Zero(t, p.remaining(), fx.Name)
	return withSel(t, sel, fx.Dims...)
}

func checkFixture(t *testing.T, fx selFixture, s *h5s, what string) {
	t.Helper()
	assert.Equal(t, fx.Type, h5sGetSelectType(s), what)
	assert.Equal(t, fx.Npoints, h5sGetSelectNpoints(s), what)
	if fx.Low != nil {
		low, high, err := h5sGetSelectBounds(s)
		require.NoError(t, err, what)
		assert.Equal(t, fx.Low, low, what)
		assert.Equal(t, fx.High, high, what)
	}
	if fx.Regular != nil {
		assert.Equal(t, *fx.Regular, h5sSelectIsRegular(s), what)
	}
	sel := *s.selection()
	require.NoError(t, h5sHyperListBlocks(&sel), what)
	pub, err := h5rSelectionPublic(&sel, s.extent.rank)
	require.NoError(t, err, what)
	if fx.Blocks != nil {
		var want [][2][]uint64
		for _, b := range fx.Blocks {
			want = append(want, [2][]uint64{b[0], b[1]})
		}
		assert.Equal(t, want, pub.Blocks, what)
	}
	if fx.Points != nil {
		assert.Equal(t, fx.Points, pub.Points, what)
		assert.Equal(t, fx.Points, collectPoints(s), what)
	}
	if fx.Seqs != nil || (fx.Type != h5sSelNone && fx.Npoints != h5sUnlimited) {
		got := collectSeqs(s, 4)
		assert.Equal(t, mergeSeqs(fx.Seqs), got, what)

		// The element iterator agrees with the sequences.
		var fromPoints [][2]uint64
		pitch := h5sPitches(&s.extent)
		for _, c := range collectPoints(s) {
			var off uint64
			for u := range c {
				off += c[u] * pitch[u]
			}
			fromPoints = mergeSeqs(append(fromPoints, [2]uint64{off * 4, 4}))
		}
		assert.Equal(t, got, fromPoints, what)
	}
}

func TestSelectionFixtures(t *testing.T) {
	fx := loadSelFixtures(t)
	var spaces []*h5s
	for _, c := range fx.Cases {
		for _, enc := range []struct {
			name, hex string
			want      *selFixture
		}{{"earliest", c.EncEarliest, c.DecEarliest}, {"latest", c.EncLatest, c.DecLatest}} {
			require.NotNil(t, enc.want)
			s := decodeFixture(t, c, enc.hex)
			checkFixture(t, *enc.want, s, c.Name+"/"+enc.name)
			if enc.name == "latest" {
				spaces = append(spaces, s)
			}
		}
	}

	// The encodings cover every serialization version.
	versions := map[string]bool{}
	for _, c := range fx.Cases {
		for _, enc := range []string{c.EncEarliest, c.EncLatest} {
			b := selectionBytes(t, enc)
			versions[strconv.Itoa(int(le32(b)))+"/"+strconv.Itoa(int(le32(b[4:])))] = true
		}
	}
	for _, v := range []string{"1/1", "1/2", "2/1", "2/2", "2/3", "3/1", "0/1"} {
		assert.True(t, versions[v], "selection type/version %s not covered", v)
	}

	for _, ss := range fx.ShapeSame {
		i, j, want := int(ss[0].(float64)), int(ss[1].(float64)), ss[2].(bool)
		got, err := h5sSelectShapeSame(spaces[i], spaces[j])
		require.NoError(t, err)
		assert.Equal(t, want, got, "shape_same(%s, %s)", fx.Cases[i].Name, fx.Cases[j].Name)
	}

	for _, ib := range fx.IntersectBlock {
		i, k, want := int(ib[0].(float64)), int(ib[1].(float64)), ib[2].(bool)
		s := spaces[i]
		rank := int(s.extent.rank)
		got, err := h5sSelectIntersectBlock(s, fx.Blocks[k][0][:rank], fx.Blocks[k][1][:rank])
		require.NoError(t, err)
		assert.Equal(t, want, got, "intersect_block(%s, %v)", fx.Cases[i].Name, fx.Blocks[k])
	}

	for _, pr := range fx.Projections {
		src := decodeFixture(t, pr.Src, pr.Src.EncLatest)
		dst := decodeFixture(t, pr.Dst, pr.Dst.EncLatest)
		sis := decodeFixture(t, pr.Sis, pr.Sis.EncLatest)
		got, err := h5sSelectProjectIntersection(src, dst, sis)
		require.NoError(t, err, pr.Name)
		checkFixture(t, pr.Result, got, "projection "+pr.Name)
	}
}

// Every truncation and many single-byte corruptions of the fixture
// encodings must fail cleanly.
func TestSelectionDecodeCorrupt(t *testing.T) {
	fx := loadSelFixtures(t)
	for _, c := range fx.Cases {
		buf := selectionBytes(t, c.EncLatest)
		for n := 0; n < len(buf); n++ {
			p := &image{buf: buf[:n], sizeofAddr: 8, sizeofSize: 8}
			assert.NotPanics(t, func() {
				_, err := h5sSelectDeserialize(nil, p)
				assert.Error(t, err, "%s truncated to %d", c.Name, n)
			})
		}
		for i := range buf {
			for _, v := range []byte{0x00, 0x7f, 0xff} {
				b := bytes.Clone(buf)
				b[i] = v
				assert.NotPanics(t, func() {
					sel, err := h5sSelectDeserialize(nil, &image{buf: b, sizeofAddr: 8, sizeofSize: 8})
					if err != nil {
						return
					}
					// Whatever decodes must be usable.
					if s, err := h5sNew(&newSpace(t, c.Dims...).extent, sel); err == nil {
						n := 0
						for range s.iterSeqs(1) {
							if n++; n > 1000 {
								break
							}
						}
						_, _, _ = h5sGetSelectBounds(s)
					}
				})
			}
		}
	}

	// Huge counts are bounded by the input size.
	huge := []byte{
		2, 0, 0, 0, // hyperslab
		3, 0, 0, 0, // version 3
		0, 8, // flags, enc size 8
		1, 0, 0, 0, // rank 1
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x0f, // number of blocks
	}
	_, err := h5sSelectDeserialize(nil, &image{buf: huge})
	assert.Error(t, err)
	huge = []byte{
		1, 0, 0, 0, // points
		2, 0, 0, 0, // version 2
		8,          // enc size 8
		2, 0, 0, 0, // rank 2
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x0f, // number of points
	}
	_, err = h5sSelectDeserialize(nil, &image{buf: huge})
	assert.Error(t, err)

	// A regular selection with an element count that overflows.
	over := []byte{2, 0, 0, 0, 3, 0, 0, 0, 1, 8, 2, 0, 0, 0}
	for range 2 {
		over = append(over, make([]byte, 8)...)                             // start
		over = append(over, 1, 0, 0, 0, 0, 0, 0, 0)                         // stride
		over = append(over, 0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x0f) // count
		over = append(over, 1, 0, 0, 0, 0, 0, 0, 0)                         // block
	}
	_, err = h5sSelectDeserialize(nil, &image{buf: over})
	assert.Error(t, err)

	// Unknown selection type, bad versions.
	for _, b := range [][]byte{
		{9, 0, 0, 0, 1, 0, 0, 0},
		{3, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{2, 0, 0, 0, 4, 0, 0, 0},
		{1, 0, 0, 0, 3, 0, 0, 0},
		{2, 0, 0, 0, 3, 0, 0, 0, 2, 4}, // unknown flag
		{2, 0, 0, 0, 3, 0, 0, 0, 0, 3}, // bad enc size
	} {
		_, err := h5sSelectDeserialize(nil, &image{buf: b})
		assert.Error(t, err, "%x", b)
	}
}

// Region references in the h5dump corpus (selections shown in the .ddl
// files).
func TestSelectionCorpusRegionRefs(t *testing.T) {
	block := h5.Selection{Type: h5.SelHyperslab, Rank: 2, Blocks: [][2][]uint64{{{2, 2}, {7, 7}}}}
	pointList := h5.Selection{Type: h5.SelPoints, Rank: 2, Points: [][]uint64{
		{6, 9}, {2, 2}, {8, 4}, {1, 6}, {2, 8}, {3, 2}, {0, 4}, {9, 0}, {7, 1}, {3, 3},
	}}

	// tdatareg.h5 /Dataset1: old-style region references.
	_, vals := corpusValues(t, "tdatareg.h5", 1308, 8, 1396, 4)
	assert.Equal(t, block, vals[0].(h5.RegionRef).Selection)
	assert.Equal(t, pointList, vals[1].(h5.RegionRef).Selection)

	// trefer_reg.h5 /Dataset1: revised region references; the third and
	// fourth are unlimited selections (no block list).
	_, vals = corpusValues(t, "trefer_reg.h5", 531, 8, 2148, 4)
	assert.Equal(t, block, vals[0].(h5.RegionRef).Selection)
	assert.Equal(t, pointList, vals[1].(h5.RegionRef).Selection)
	assert.Equal(t, h5.Selection{Type: h5.SelHyperslab, Rank: 2}, vals[2].(h5.RegionRef).Selection)

	// trefer_reg_1d.h5 /Dataset1: a strided 1D hyperslab and points.
	_, vals = corpusValues(t, "trefer_reg_1d.h5", 531, 8, 2148, 2)
	var blocks [][2][]uint64
	for s := uint64(2); s <= 72; s += 5 {
		blocks = append(blocks, [2][]uint64{{s}, {s + 1}})
	}
	assert.Equal(t, h5.Selection{Type: h5.SelHyperslab, Rank: 1, Blocks: blocks}, vals[0].(h5.RegionRef).Selection)
	assert.Equal(t, h5.Selection{Type: h5.SelPoints, Rank: 1, Points: [][]uint64{
		{16}, {22}, {38}, {41}, {52}, {63}, {70}, {89}, {97}, {3},
	}}, vals[1].(h5.RegionRef).Selection)
}

// vdsMapping is one mapping of a virtual dataset as h5dump -p shows it.
type vdsMapping struct {
	file, dset      string
	virtual, source string // "ALL" or "start/stride/count/block"
}

var ddlTuple = regexp.MustCompile(`\(([^)]*)\)`)

// parseVDSDDL extracts the mappings from an h5dump -p .ddl file.
func parseVDSDDL(t *testing.T, path string) []vdsMapping {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var out []vdsMapping
	var cur *vdsMapping
	var side *string
	var hs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "MAPPING "):
			out = append(out, vdsMapping{})
			cur = &out[len(out)-1]
		case cur == nil:
		case line == "VIRTUAL {":
			side = &cur.virtual
		case line == "SOURCE {":
			side = &cur.source
		case strings.HasPrefix(line, "FILE "):
			cur.file = strings.Trim(strings.TrimPrefix(line, "FILE "), `"`)
		case strings.HasPrefix(line, "DATASET "):
			cur.dset = strings.Trim(strings.TrimPrefix(line, "DATASET "), `"`)
		case line == "SELECTION ALL":
			*side = "ALL"
		case strings.HasPrefix(line, "START ") || strings.HasPrefix(line, "STRIDE ") || strings.HasPrefix(line, "COUNT "):
			hs = append(hs, ddlTuple.FindStringSubmatch(line)[1])
		case strings.HasPrefix(line, "BLOCK "):
			hs = append(hs, ddlTuple.FindStringSubmatch(line)[1])
			*side = strings.Join(hs, "/")
			hs = nil
		}
	}
	require.NoError(t, sc.Err())
	return out
}

// vdsSelString renders a selection like parseVDSDDL.
func vdsSelString(sel *h5sSelection) string {
	if sel.typ == h5sSelAll {
		return "ALL"
	}
	if sel.typ != h5sSelHyperslabs || !sel.regular {
		return "?"
	}
	f := func(v []uint64) string {
		s := make([]string, len(v))
		for i, x := range v {
			if x == h5sUnlimited {
				s[i] = "H5S_UNLIMITED"
			} else {
				s[i] = strconv.FormatUint(x, 10)
			}
		}
		return strings.Join(s, ",")
	}
	return f(sel.start) + "/" + f(sel.stride) + "/" + f(sel.count) + "/" + f(sel.block)
}

// Virtual dataset mappings: the global heap object of the layout holds
// the source and virtual selections of every mapping.
func TestSelectionCorpusVDS(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tools/test/h5dump/testfiles/vds")
	ddl := filepath.Join(repoRoot(t), "tools/test/h5dump/expected/vds")
	cases := map[string]string{
		"1_vds.h5":                     "tvds_layout-1.ddl",
		"2_vds.h5":                     "tvds_layout-2.ddl",
		"3_1_vds.h5":                   "tvds_layout-3_1.ddl",
		"3_2_vds.h5":                   "tvds_layout-3_2.ddl",
		"4_vds.h5":                     "tvds_layout-4.ddl",
		"5_vds.h5":                     "tvds_layout-5.ddl",
		"vds-eiger.h5":                 "vds_layout-eiger.ddl",
		"vds-percival-unlim-maxmin.h5": "vds_layout-maxmin.ddl",
	}
	for name, ddlName := range cases {
		want := parseVDSDDL(t, filepath.Join(ddl, ddlName))
		require.NotEmpty(t, want, name)

		raw, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		f, err := Open(filepath.Join(dir, name))
		require.NoError(t, err)

		var got []vdsMapping
		for off := 0; ; {
			i := bytes.Index(raw[off:], []byte("GCOL"))
			if i < 0 {
				break
			}
			addr := uint64(off + i)
			off += i + 4
			heap, err := f.hgProtect(addr)
			if err != nil {
				continue
			}
			for idx := uint32(1); uint64(idx) < heap.nused; idx++ {
				if heap.obj[idx].begin < 0 {
					continue
				}
				b, err := f.hgRead(h5hgT{addr: heap.addr, idx: idx})
				require.NoError(t, err)
				got = append(got, decodeVDSBlock(t, f, b)...)
			}
		}
		f.Close()
		assert.Equal(t, want, got, name)
	}
}

// decodeVDSBlock decodes the global heap block of a virtual layout
// (H5O__layout_decode): version, number of entries, then per entry the
// source file and dataset names and the source and virtual selections.
func decodeVDSBlock(t *testing.T, f *File, b []byte) []vdsMapping {
	t.Helper()
	p := f.newImage(b)
	require.Equal(t, uint8(0), p.uint8())
	n := p.length()
	var out []vdsMapping
	for range n {
		var m vdsMapping
		m.file = p.cstring()
		m.dset = p.cstring()
		src, err := h5sSelectDeserialize(f, p)
		require.NoError(t, err)
		virt, err := h5sSelectDeserialize(f, p)
		require.NoError(t, err)
		m.source, m.virtual = vdsSelString(src), vdsSelString(virt)
		out = append(out, m)
	}
	require.NoError(t, p.done())
	assert.Equal(t, 4, p.remaining()) // checksum
	return out
}

func TestShapeSameAndQueries(t *testing.T) {
	// A 1x4 row in 2D has the same shape as a 4-element 1D selection.
	a := selectHyper(t, newSpace(t, 5, 5), []uint64{2, 1}, nil, []uint64{1, 1}, []uint64{1, 4})
	b := newSpace(t, 4)
	same, err := h5sSelectShapeSame(a, b)
	require.NoError(t, err)
	assert.True(t, same)
	same, err = h5sSelectShapeSame(b, a)
	require.NoError(t, err)
	assert.True(t, same)
	c := selectHyper(t, newSpace(t, 5, 5), []uint64{1, 1}, nil, []uint64{1, 1}, []uint64{2, 2})
	same, err = h5sSelectShapeSame(a, c)
	require.NoError(t, err)
	assert.False(t, same)

	// Extent helpers.
	s, err := h5sCreateSimple([]uint64{3, 4}, []uint64{h5sUnlimited, 4})
	require.NoError(t, err)
	assert.Equal(t, uint64(12), h5sGetSimpleExtentNpoints(s))
	assert.Equal(t, ^uint64(0), h5sGetNpointsMax(s))
	assert.Equal(t, 2, h5sGetSimpleExtentNdims(s))
	dims, maxDims := h5sGetSimpleExtentDims(s)
	assert.Equal(t, []uint64{3, 4}, dims)
	assert.Equal(t, []uint64{h5sUnlimited, 4}, maxDims)
	s2, err := h5sCreateSimple([]uint64{3, 4}, nil)
	require.NoError(t, err)
	assert.False(t, h5sExtentEqual(s, s2))
	s3, err := h5sCreateSimple([]uint64{3, 4}, []uint64{h5sUnlimited, 4})
	require.NoError(t, err)
	assert.True(t, h5sExtentEqual(s, s3))
	assert.Equal(t, h5.SpaceSimple, h5sGetSimpleExtentType(s))
	null := h5sCreate(h5.SpaceNull)
	assert.Zero(t, h5sGetSelectNpoints(null))
	assert.Empty(t, collectSeqs(null, 4))
	_, err = h5sCreateSimple(make([]uint64, 33), nil)
	assert.Error(t, err)

	// Rank mismatch when attaching a selection.
	sel := mustBlocksSel(t, 2, [][2][]uint64{{{0, 0}, {1, 1}}})
	_, err = h5sNew(&newSpace(t, 4).extent, sel)
	assert.Error(t, err)

	// Scalar projection.
	one := selectHyper(t, newSpace(t, 4, 5), []uint64{2, 3}, nil, []uint64{1, 1}, nil)
	off, err := h5sSelectProjectScalar(one)
	require.NoError(t, err)
	assert.Equal(t, uint64(13), off)
	pts := newSpace(t, 4, 5)
	h5sSelectElements(pts, [][]uint64{{3, 1}})
	off, err = h5sSelectProjectScalar(pts)
	require.NoError(t, err)
	assert.Equal(t, uint64(16), off)
	assert.True(t, slices.Equal([]uint64{3, 1}, h5sGetSelectElemPointlist(pts, 0, 1)[0]))
}
