//go:build cgo

package conformance

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	chdf5 "github.com/crhntr/hdf5/cgo"
	"github.com/crhntr/hdf5/go/h5"
)

// Fixtures written with the chdf5 writing API, for the combinations of
// file format features the repository corpus lacks or has few of: the
// latest format (dense groups and attributes, the version 2 chunk
// indexes), every integer and float width, fixed and variable-length
// strings, the bool enum, creation order, shared messages, paged file
// space, user blocks, 4-byte addresses and external links between
// generated files. Big-endian data is covered by the corpus only (the
// writer stores native types).

// Values of the H5O_SHMESG_*_FLAG and H5F_fspace_strategy_t constants.
const (
	shmesgAll         = 1<<1 | 1<<3 | 1<<5 | 1<<11 | 1<<12 // SDSPACE | DTYPE | FILL | PLINE | ATTR
	fspaceStrategyPag = 1                                  // H5F_FSPACE_STRATEGY_PAGE
	allocTimeEarly    = 1                                  // H5D_ALLOC_TIME_EARLY
)

// fixtureFormats are the file creation settings each fixture is written
// with.
var fixtureFormats = []struct {
	name string
	opts []chdf5.CreateOption
}{
	{"default", nil},
	{"latest", []chdf5.CreateOption{chdf5.WithLatestFormat()}},
	{"v18", []chdf5.CreateOption{chdf5.WithLibverBounds(chdf5.LibverV18, chdf5.LibverLatest)}},
	{"v110", []chdf5.CreateOption{chdf5.WithLibverBounds(chdf5.LibverV110, chdf5.LibverLatest)}},
	{"userblock", []chdf5.CreateOption{chdf5.WithUserblock(1024), chdf5.WithSymK(8, 2), chdf5.WithIstoreK(64)}},
	{"sizes4", []chdf5.CreateOption{chdf5.WithSizes(4, 4)}},
	{"latest_sizes4", []chdf5.CreateOption{chdf5.WithLatestFormat(), chdf5.WithSizes(4, 4)}},
	{"root_crt_order", []chdf5.CreateOption{chdf5.WithLatestFormat(), chdf5.WithRootCreationOrder()}},
	{"shared_messages", []chdf5.CreateOption{chdf5.WithLatestFormat(), chdf5.WithSharedMessages(shmesgAll, 1)}},
	{"shared_messages_v18", []chdf5.CreateOption{chdf5.WithLibverBounds(chdf5.LibverV18, chdf5.LibverLatest), chdf5.WithSharedMessages(shmesgAll, 1)}},
	{"paged", []chdf5.CreateOption{chdf5.WithLatestFormat(), chdf5.WithFileSpaceStrategy(fspaceStrategyPag, true, 1), chdf5.WithFileSpacePageSize(4096)}},
}

// TestFixtures writes files with chdf5 and compares their reads.
func TestFixtures(t *testing.T) {
	all := &totals{s: newStats()}
	for _, ff := range fixtureFormats {
		t.Run(ff.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target.h5")
			writeTarget(t, target, ff.opts)
			main := filepath.Join(dir, "main.h5")
			writeFixture(t, main, ff.opts)
			for _, f := range []string{main, target} {
				t.Run(filepath.Base(f), func(t *testing.T) {
					all.add(compareFile(t, f, "fixtures/"+ff.name+"/"+filepath.Base(f), fileOptions{}))
				})
			}
		})
	}
	t.Log(all.s.table())
}

// writeTarget writes the file the external links of the fixture point to.
func writeTarget(t *testing.T, name string, opts []chdf5.CreateOption) {
	f, err := chdf5.Create(name, opts...)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	g, err := f.CreateGroup("/g")
	require.NoError(t, err)
	_, err = chdf5.WriteDataset(g, "x", []uint64{3}, []float64{1, 2, 3})
	require.NoError(t, err)
	require.NoError(t, chdf5.WriteAttribute(g, "a", nil, []int32{7}))
	require.NoError(t, g.CreateExternalLink("back", "main.h5", "/ints/i8"))
}

func writeFixture(t *testing.T, name string, opts []chdf5.CreateOption) {
	f, err := chdf5.Create(name, opts...)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	root := f.Root()
	must := func(err error) {
		t.Helper()
		require.NoError(t, err)
	}
	group := func(path string, gopts ...chdf5.GroupOption) *chdf5.Group {
		t.Helper()
		g, err := root.CreateGroup(strings.TrimPrefix(path, "/"), gopts...)
		require.NoError(t, err)
		return g
	}

	// Every integer width, with the extreme values.
	ints := group("/ints")
	_, err = chdf5.WriteDataset(ints, "i8", []uint64{5}, []int8{math.MinInt8, -1, 0, 1, math.MaxInt8})
	must(err)
	_, err = chdf5.WriteDataset(ints, "i16", []uint64{5}, []int16{math.MinInt16, -1, 0, 1, math.MaxInt16})
	must(err)
	_, err = chdf5.WriteDataset(ints, "i32", []uint64{5}, []int32{math.MinInt32, -1, 0, 1, math.MaxInt32})
	must(err)
	_, err = chdf5.WriteDataset(ints, "i64", []uint64{5}, []int64{math.MinInt64, -1, 0, 1, math.MaxInt64})
	must(err)
	_, err = chdf5.WriteDataset(ints, "u8", []uint64{3}, []uint8{0, 1, math.MaxUint8})
	must(err)
	_, err = chdf5.WriteDataset(ints, "u16", []uint64{3}, []uint16{0, 1, math.MaxUint16})
	must(err)
	_, err = chdf5.WriteDataset(ints, "u32", []uint64{3}, []uint32{0, 1, math.MaxUint32})
	must(err)
	_, err = chdf5.WriteDataset(ints, "u64", []uint64{3}, []uint64{0, 1, math.MaxUint64})
	must(err)
	_, err = chdf5.WriteDataset(ints, "scalar", nil, []int32{42})
	must(err)

	// Floats, with special values and NaN payloads.
	floats := group("/floats")
	f32 := []float32{0, float32(math.Copysign(0, -1)), 1.5, -2.25, math.MaxFloat32, math.SmallestNonzeroFloat32,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc00001), math.Float32frombits(0xffa00000)}
	_, err = chdf5.WriteDataset(floats, "f32", []uint64{uint64(len(f32))}, f32)
	must(err)
	f64 := []float64{0, math.Copysign(0, -1), 1.0 / 3, math.MaxFloat64, math.SmallestNonzeroFloat64,
		math.Inf(1), math.Inf(-1), math.Float64frombits(0x7ff8000000000123), math.Float64frombits(0xfff4000000000000)}
	_, err = chdf5.WriteDataset(floats, "f64", []uint64{uint64(len(f64))}, f64)
	must(err)

	// Strings and the bool enum.
	strs := group("/strings")
	_, err = chdf5.WriteDataset(strs, "fixed", []uint64{4}, []string{"a", "hello", "", "trailing  "})
	must(err)
	_, err = chdf5.WriteDataset(strs, "fixed_utf8", []uint64{2}, []string{"größe", "ok"})
	must(err)
	_, err = chdf5.WriteDataset(strs, "vlen", []uint64{2, 2}, []string{"variable", "", "länge", "x"}, chdf5.WithVariableLengthStrings())
	must(err)
	_, err = chdf5.WriteDataset(strs, "vlen_chunked", []uint64{3}, []string{"a", "bb", "ccc"},
		chdf5.WithVariableLengthStrings(), chdf5.WithChunk(2), chdf5.WithDeflate(4))
	must(err)
	_, err = chdf5.WriteDataset(strs, "bool", []uint64{4}, []bool{true, false, false, true})
	must(err)

	// Layouts, chunk indexes and filters.
	lay := group("/layouts")
	grid := func(n int) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = float64(i)*0.5 - 7
		}
		return out
	}
	_, err = chdf5.WriteDataset(lay, "compact", []uint64{2, 3}, []int16{1, 2, 3, 4, 5, 6}, chdf5.WithCompact())
	must(err)
	_, err = chdf5.WriteDataset(lay, "contiguous", []uint64{4, 5}, grid(20), chdf5.WithContiguous())
	must(err)
	_, err = chdf5.WriteDataset(lay, "contiguous_early", []uint64{6}, []uint32{1, 2, 3, 4, 5, 6},
		chdf5.WithContiguous(), chdf5.WithAllocTime(allocTimeEarly))
	must(err)
	_, err = chdf5.WriteDataset(lay, "chunk_single", []uint64{4, 5}, grid(20), chdf5.WithChunk(4, 5))
	must(err)
	_, err = chdf5.WriteDataset(lay, "chunk_implicit", []uint64{6, 6}, grid(36), chdf5.WithChunk(3, 3),
		chdf5.WithAllocTime(allocTimeEarly))
	must(err)
	_, err = chdf5.WriteDataset(lay, "chunk_fixed", []uint64{10, 10}, grid(100), chdf5.WithChunk(3, 4),
		chdf5.WithDeflate(6))
	must(err)
	_, err = chdf5.WriteDataset(lay, "chunk_extensible", []uint64{10, 10}, grid(100), chdf5.WithChunk(3, 4),
		chdf5.WithMaxDims(h5.Unlimited, 10), chdf5.WithShuffle(), chdf5.WithFletcher32())
	must(err)
	_, err = chdf5.WriteDataset(lay, "chunk_btree2", []uint64{10, 10}, grid(100), chdf5.WithChunk(3, 4),
		chdf5.WithMaxDims(h5.Unlimited, h5.Unlimited), chdf5.WithShuffle(), chdf5.WithDeflate(9),
		chdf5.WithFletcher32(), chdf5.WithFillValue(-1.0))
	must(err)
	cube := make([]uint16, 4*5*6)
	for i := range cube {
		cube[i] = uint16(i * 7)
	}
	_, err = chdf5.WriteDataset(lay, "chunk_3d", []uint64{4, 5, 6}, cube, chdf5.WithChunk(2, 3, 4), chdf5.WithShuffle())
	must(err)
	_, err = chdf5.WriteDataset(lay, "empty_unlimited", []uint64{0}, []int32{}, chdf5.WithChunk(8),
		chdf5.WithMaxDims(h5.Unlimited), chdf5.WithFillValue(int32(-5)))
	must(err)
	_, err = chdf5.WriteDataset(lay, "fill_int", []uint64{8}, []int64{1, 2, 3, 4, 5, 6, 7, 8}, chdf5.WithChunk(3),
		chdf5.WithMaxDims(20), chdf5.WithFillValue(int64(-9)))
	must(err)
	_, err = chdf5.WriteDataset(lay, "fill_string", []uint64{2}, []string{"ab", "c"}, chdf5.WithChunk(1),
		chdf5.WithFillValue("zz"))
	must(err)

	// Attributes: on the root, on a dataset, many on one group (dense
	// storage with the latest format).
	must(chdf5.WriteAttribute(root, "title", nil, []string{"conformance fixture"}))
	must(chdf5.WriteAttribute(root, "vlen_names", []uint64{3}, []string{"x", "", "zeta"}, chdf5.WithVariableLengthStrings()))
	must(chdf5.WriteAttribute(root, "scale", []uint64{2, 2}, []float64{1, -1, math.Inf(1), math.NaN()}))
	must(chdf5.WriteAttribute(root, "flags", []uint64{3}, []bool{true, true, false}))
	must(chdf5.WriteAttribute(root, "u64", nil, []uint64{math.MaxUint64}))
	d, err := chdf5.WriteDataset(root, "with_attrs", []uint64{2}, []int8{1, 2})
	must(err)
	must(chdf5.WriteAttribute(d, "units", nil, []string{"m/s"}))
	must(chdf5.WriteAttribute(d, "range", []uint64{2}, []float32{-1, 1}))
	must(chdf5.SetComment(d, "a dataset comment"))
	dense := group("/dense_attrs")
	for i := range 24 {
		must(chdf5.WriteAttribute(dense, fmt.Sprintf("attr%02d", 23-i), []uint64{2}, []int32{int32(i), -int32(i)}))
	}

	// Groups: nested, creation order, many links (dense with the latest
	// format), early phase change, comments, soft/hard/external links.
	group("/a/b/c")
	must(chdf5.SetComment(group("/commented"), "a group comment"))
	ordered := group("/ordered", chdf5.WithCreationOrder())
	for _, n := range []string{"zeta", "alpha", "mid", "beta"} {
		_, err := ordered.CreateGroup(n)
		must(err)
	}
	_, err = chdf5.WriteDataset(ordered, "data", []uint64{1}, []int32{1})
	must(err)
	many := group("/many", chdf5.WithCreationOrder())
	for i := range 40 {
		_, err := many.CreateGroup(fmt.Sprintf("g%02d", (i*7)%40))
		must(err)
	}
	phase := group("/phase", chdf5.WithLinkPhaseChange(2, 1))
	for _, n := range []string{"one", "two", "three"} {
		_, err := phase.CreateGroup(n)
		must(err)
	}
	must(root.CreateSoftLink("soft_ints", "/ints"))
	must(root.CreateSoftLink("soft_dataset", "/ints/i32"))
	must(root.CreateSoftLink("dangling", "/no/such/object"))
	must(root.CreateHardLink("hard_floats", "/floats"))
	must(ordered.CreateHardLink("hard_f64", "/floats/f64"))
	must(root.CreateExternalLink("ext_group", "target.h5", "/g"))
	must(root.CreateExternalLink("ext_dataset", "target.h5", "/g/x"))
	must(root.CreateExternalLink("ext_missing_file", "no_such_file.h5", "/x"))
	must(root.CreateExternalLink("ext_missing_object", "target.h5", "/nothing"))
}
