package hdf5

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The selection operations of the "all" and "none" classes (H5Sall.c,
// H5Snone.c) and H5S_select_adjust_s, checked against libhdf5: the adjust
// results are those of H5Sselect_adjust in testdata/h5s_selections.json
// (written by testdata/gen/gen_sel.c); the class methods return constants
// (H5S__all_is_contiguous is true, H5S__none_is_single false, ...).

func TestSelectionAdjustFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/h5s_selections.json")
	require.NoError(t, err)
	var fx struct {
		Cases  []selFixture `json:"cases"`
		Adjust []struct {
			Case   int        `json:"case"`
			Offset []int64    `json:"offset"`
			Result selFixture `json:"result"`
		} `json:"adjust"`
	}
	require.NoError(t, json.Unmarshal(raw, &fx))
	require.NotEmpty(t, fx.Adjust)
	types := map[int]bool{}
	for _, a := range fx.Adjust {
		c := fx.Cases[a.Case]
		s := decodeFixture(t, c, c.EncLatest)
		got, err := h5sSelectAdjustS(s, a.Offset)
		require.NoError(t, err, c.Name)
		a.Result.Name = c.Name
		if c.Type == h5sSelHyperslabs {
			/* H5Sget_select_hyper_blocklist reports a regular
			 * hyperslab from diminfo.app, the application's
			 * description, which H5S__hyper_adjust_s does not move
			 * (only diminfo.opt): its blocks are those before the
			 * adjustment. The bounds and sequences are compared. */
			a.Result.Blocks = nil
		}
		checkFixture(t, a.Result, got, "adjust "+c.Name)
		types[c.Type] = true

		/* The adjusted selection has the same shape */
		same, err := h5sSelectShapeSame(s, got)
		require.NoError(t, err)
		assert.True(t, same, c.Name)
	}
	assert.Len(t, types, 3, "points, hyperslabs and all")

	_, err = h5sSelectAdjustS(newSpace(t, 3, 3), []int64{1})
	assert.Error(t, err, "offset shorter than the rank")
}

func TestSelectionAllAndNoneClasses(t *testing.T) {
	all := newSpace(t, 3, 4)
	none := newSpace(t, 3, 4)
	h5sSelectNone(none)
	pts := newSpace(t, 3, 4)
	h5sSelectElements(pts, [][]uint64{{1, 1}, {2, 3}})
	one := newSpace(t, 3, 4)
	h5sSelectElements(one, [][]uint64{{2, 1}})
	unlim, err := h5sCreateSimple([]uint64{10, 10}, []uint64{h5sUnlimited, 10})
	require.NoError(t, err)
	unlim = selectHyper(t, unlim, []uint64{1, 8}, []uint64{4, 1}, []uint64{h5sUnlimited, 1}, []uint64{2, 2})

	/* H5S_get_select_unlim_dim */
	assert.Equal(t, -1, h5sGetSelectUnlimDim(all))
	assert.Equal(t, -1, h5sGetSelectUnlimDim(none))
	assert.Equal(t, -1, h5sGetSelectUnlimDim(pts))
	assert.Equal(t, 0, h5sGetSelectUnlimDim(unlim))

	/* H5S_select_is_contiguous / is_single / is_regular */
	assert.True(t, h5sSelectIsContiguous(all))
	assert.True(t, h5sSelectIsSingle(all))
	assert.True(t, h5sSelectIsRegular(all))
	assert.False(t, h5sSelectIsContiguous(none))
	assert.False(t, h5sSelectIsSingle(none))
	assert.True(t, h5sSelectIsRegular(none))
	assert.False(t, h5sSelectIsContiguous(pts))
	assert.False(t, h5sSelectIsSingle(pts))
	assert.False(t, h5sSelectIsRegular(pts))
	assert.True(t, h5sSelectIsContiguous(one))
	assert.True(t, h5sSelectIsSingle(one))
	assert.True(t, h5sSelectIsRegular(one))

	/* H5S_get_select_offset */
	off, err := h5sGetSelectOffset(all)
	require.NoError(t, err)
	assert.Zero(t, off)
	_, err = h5sGetSelectOffset(none)
	assert.Error(t, err)
	off, err = h5sGetSelectOffset(pts)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), off)

	/* H5S_select_adjust_u: "all" and "none" do not move */
	for _, s := range []*h5s{all, none} {
		a, err := h5sSelectAdjustU(s, []uint64{1, 1})
		require.NoError(t, err)
		assert.Equal(t, h5sGetSelectType(s), h5sGetSelectType(a))
		assert.Equal(t, h5sGetSelectNpoints(s), h5sGetSelectNpoints(a))
		a2, err := h5sSelectAdjustS(s, []int64{-1, 2})
		require.NoError(t, err)
		assert.Equal(t, h5sGetSelectNpoints(s), h5sGetSelectNpoints(a2))
	}
	_, err = h5sSelectAdjustU(all, []uint64{1})
	assert.Error(t, err)

	/* H5S_select_project_scalar */
	off, err = h5sSelectProjectScalar(all)
	require.NoError(t, err)
	assert.Zero(t, off)
	_, err = h5sSelectProjectScalar(none)
	assert.Error(t, err)

	/* H5S_select_shape_same between the classes */
	for _, c := range []struct {
		a, b *h5s
		want bool
	}{
		{all, all, true},
		{none, none, true},
		{all, newSpace(t, 1, 3, 4), true},
		{newSpace(t, 2, 3, 4), all, false},
		{all, newSpace(t, 4, 3), false},
		{none, pts, false},
		{pts, pts, true},
	} {
		got, err := h5sSelectShapeSame(c.a, c.b)
		require.NoError(t, err)
		assert.Equal(t, c.want, got)
	}
	/* Two "none" selections of different ranks */
	none3 := newSpace(t, 2, 3, 4)
	h5sSelectNone(none3)
	got, err := h5sSelectShapeSame(none3, none)
	require.NoError(t, err)
	assert.True(t, got)
}
