package hdf5

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests of the public Dataset API (dataset.go, read.go) on the h5dump
// corpus; expected values are those of the .ddl files (h5dump output).

// h5dumpFile opens a file of tools/test/h5dump/testfiles.
func h5dumpFile(t *testing.T, name string, opts ...Option) *File {
	t.Helper()
	f, err := Open(filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles", name), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// openDS opens the dataset at path.
func openDS(t *testing.T, f *File, path string) *Dataset {
	t.Helper()
	d, err := f.OpenDataset(path)
	require.NoError(t, err, path)
	return d
}

// tdsetDset1 is /dset1 of tdset.h5 (10x20, element (r,c) = r+c).
func tdsetDset1(conv func(int) any) []any {
	out := make([]any, 0, 200)
	for r := range 10 {
		for c := range 20 {
			out = append(out, conv(r+c))
		}
	}
	return out
}

func TestReadIntegersAndFloats(t *testing.T) {
	f := h5dumpFile(t, "tdset.h5")
	d := openDS(t, f, "/dset1")

	assert.Equal(t, &h5.Datatype{Class: h5.ClassInteger, Version: 1, Size: 4, Order: h5.OrderBE, Precision: 32, Sign: h5.Sign2}, d.Datatype())
	assert.Equal(t, h5.Dataspace{Class: h5.SpaceSimple, Version: 1, Dims: []uint64{10, 20}}, d.Dataspace()) /* (no maximum dimensions stored) */

	vals, err := d.ReadValues()
	require.NoError(t, err)
	assert.Equal(t, tdsetDset1(func(v int) any { return int32(v) }), vals)

	check := func(name string, got any, err error, conv func(int) any) {
		t.Helper()
		require.NoError(t, err, name)
		want := tdsetDset1(conv)
		gotAny := make([]any, 0, len(want))
		switch g := got.(type) {
		case []int8:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []int16:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []int32:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []int64:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []uint8:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []uint16:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []uint32:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []uint64:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []float32:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []float64:
			for _, v := range g {
				gotAny = append(gotAny, v)
			}
		case []any:
			gotAny = g
		}
		assert.Equal(t, want, gotAny, name)
	}
	i8, err := Read[int8](d)
	check("int8", i8, err, func(v int) any { return int8(v) })
	i16, err := Read[int16](d)
	check("int16", i16, err, func(v int) any { return int16(v) })
	i32, err := Read[int32](d)
	check("int32", i32, err, func(v int) any { return int32(v) })
	i64, err := Read[int64](d)
	check("int64", i64, err, func(v int) any { return int64(v) })
	u8, err := Read[uint8](d)
	check("uint8", u8, err, func(v int) any { return uint8(v) })
	u16, err := Read[uint16](d)
	check("uint16", u16, err, func(v int) any { return uint16(v) })
	u32, err := Read[uint32](d)
	check("uint32", u32, err, func(v int) any { return uint32(v) })
	u64, err := Read[uint64](d)
	check("uint64", u64, err, func(v int) any { return uint64(v) })
	f32, err := Read[float32](d)
	check("float32", f32, err, func(v int) any { return float32(v) })
	f64, err := Read[float64](d)
	check("float64", f64, err, func(v int) any { return float64(v) })
	anys, err := Read[any](d)
	check("any", anys, err, func(v int) any { return int32(v) })

	/* ReadInto is the same conversion */
	var into []int64
	require.NoError(t, d.ReadInto(&into))
	assert.Equal(t, i64, into)

	/* A float dataset converts to floats */
	d2 := openDS(t, f, "/dset2")
	fl, err := Read[float64](d2)
	require.NoError(t, err)
	require.Len(t, fl, 600)
	assert.InDelta(t, 0.0, fl[0], 1e-12)
	assert.InDelta(t, 0.0002, fl[2], 1e-12) /* (0,2): 0.0002 in tdset-1.ddl */

	/* Layout, filters and fill value (h5dump -p) */
	l, err := d.Layout()
	require.NoError(t, err)
	assert.Equal(t, h5.LayoutContiguous, l.Class)
	assert.True(t, l.AddressDefined)
	assert.EqualValues(t, 2048, l.Address)
	assert.EqualValues(t, 800, l.Size)
	filters, err := d.Filters()
	require.NoError(t, err)
	assert.Nil(t, filters)
	fv, err := d.FillValue()
	require.NoError(t, err)
	assert.False(t, fv.Defined)
	assert.False(t, fv.Undefined)
	assert.Nil(t, fv.Value)
	assert.EqualValues(t, h5dAllocTimeLate, fv.AllocTime)
	assert.EqualValues(t, h5dFillTimeIfset, fv.FillTime)
}

func TestReadCompoundStruct(t *testing.T) {
	f := h5dumpFile(t, "tcompound.h5")
	d := openDS(t, f, "/dset1")

	type tagged struct {
		A int32   `hdf5:"a_name"`
		B float32 `hdf5:"b_name"`
		C float64 `hdf5:"c_name"`
		X string  `hdf5:"-"`
	}
	vals, err := Read[tagged](d)
	require.NoError(t, err)
	require.Len(t, vals, 5)
	for i, v := range vals {
		assert.Equal(t, int32(i), v.A)
		assert.Equal(t, float32(i*i), v.B)
		assert.InDelta(t, 1/float64(i+1), v.C, 1e-12)
		assert.Empty(t, v.X)
	}

	/* Case-insensitive name matching */
	type byName struct {
		A_NAME int64
		B_name float64
	}
	vals2, err := Read[byName](d)
	require.NoError(t, err)
	assert.Equal(t, byName{A_NAME: 3, B_name: 9}, vals2[3])

	/* h5.Compound identity */
	comps, err := Read[h5.Compound](d)
	require.NoError(t, err)
	assert.Equal(t, []string{"a_name", "b_name", "c_name"}, []string{comps[0].Fields[0].Name, comps[0].Fields[1].Name, comps[0].Fields[2].Name})
	assert.Equal(t, int32(2), comps[2].Fields[0].Value)
}

func TestReadArrays(t *testing.T) {
	f := h5dumpFile(t, "tarray1.h5")
	d := openDS(t, f, "/Dataset1")
	vals, err := Read[[]int32](d)
	require.NoError(t, err)
	assert.Equal(t, [][]int32{{0, 1, 2, 3}, {10, 11, 12, 13}, {20, 21, 22, 23}, {30, 31, 32, 33}}, vals)

	wide, err := Read[[]float64](d)
	require.NoError(t, err)
	assert.Equal(t, []float64{30, 31, 32, 33}, wide[3])
}

func TestReadStrings(t *testing.T) {
	f := h5dumpFile(t, "tvlstr.h5")
	d := openDS(t, f, "/Dataset1")
	assert.True(t, d.Datatype().IsVlenString())
	vals, err := Read[string](d)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"Four score and seven years ago our forefathers brought forth on this continent a new nation,",
		"conceived in liberty and dedicated to the proposition that all men are created equal.",
		"",
		"",
	}, vals)

	b, err := Read[[]byte](d)
	require.NoError(t, err)
	assert.Equal(t, []byte("conceived"), b[1][:9])

	/* A string is not a number */
	_, err = Read[int32](d)
	assert.Error(t, err)

	/* Fixed-size strings */
	f2 := h5dumpFile(t, "tattr2.h5")
	a, err := f2.Root().Attribute("string")
	require.NoError(t, err)
	strs, err := ReadAttribute[string](a)
	require.NoError(t, err)
	assert.Equal(t, []string{"ab", "de"}, strs)
}

func TestReadEnum(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "tools", "test", "h5diff", "testfiles", "h5diff_enum_invalid_values.h5"))
	require.NoError(t, err)
	defer f.Close()
	d := openDS(t, f, "/dset2")
	enums, err := Read[h5.Enum](d)
	require.NoError(t, err)
	assert.Equal(t, []h5.Enum{{Value: 9}, {Value: 9}, {Name: "YIN"}, {Name: "YIN"}, {Value: 9}, {Name: "YANG", Value: 1}}, enums)
	ints, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{9, 9, 0, 0, 9, 1}, ints)
	names, err := Read[string](d)
	require.NoError(t, err)
	assert.Equal(t, []string{"", "", "YIN", "YIN", "", "YANG"}, names)
	_, err = Read[bool](d) /* 9 is not a bool */
	assert.Error(t, err)
}

func TestReadObjectReferences(t *testing.T) {
	f := h5dumpFile(t, "trefer_obj.h5")
	target := openDS(t, f, "/Group1/Dataset1")
	info, err := target.Info()
	require.NoError(t, err)

	for _, path := range []string{"/Dataset3", "/Dataset4"} {
		refs, err := Read[h5.ObjectRef](openDS(t, f, path))
		require.NoError(t, err, path)
		require.Len(t, refs, 1, path)
		assert.Equal(t, h5.ObjectRef{Address: info.Address}, refs[0], path)

		/* Dereference: the Dataset methods work on an object opened by address */
		obj, err := f.OpenAddress(refs[0].Address)
		require.NoError(t, err)
		ds, ok := obj.(*Dataset)
		require.True(t, ok, "%T", obj)
		assert.Equal(t, "", ds.Path())
		vals, err := Read[uint32](ds)
		require.NoError(t, err)
		assert.Equal(t, []uint32{0}, vals)
		assert.Equal(t, h5.ClassInteger, ds.Datatype().Class)
		assert.Equal(t, []uint64{1}, ds.Dataspace().Dims)
		l, err := ds.Layout()
		require.NoError(t, err)
		want, err := target.Layout()
		require.NoError(t, err)
		assert.Equal(t, want, l)
	}
}

func TestReadRegionReferences(t *testing.T) {
	f := h5dumpFile(t, "tdatareg.h5")
	d2 := openDS(t, f, "/Dataset2")
	info, err := d2.Info()
	require.NoError(t, err)

	refs, err := Read[h5.RegionRef](openDS(t, f, "/Dataset1"))
	require.NoError(t, err)
	require.Len(t, refs, 4)
	assert.Equal(t, h5.RegionRef{Address: info.Address, Selection: h5.Selection{
		Type: h5.SelHyperslab, Rank: 2, Blocks: [][2][]uint64{{{2, 2}, {7, 7}}},
	}}, refs[0])
	assert.Equal(t, h5.RegionRef{Address: info.Address, Selection: h5.Selection{
		Type: h5.SelPoints, Rank: 2, Points: [][]uint64{
			{6, 9}, {2, 2}, {8, 4}, {1, 6}, {2, 8}, {3, 2}, {0, 4}, {9, 0}, {7, 1}, {3, 3},
		},
	}}, refs[1])
	assert.True(t, refs[2].Undefined)
	assert.True(t, refs[3].Undefined)

	/* Read the referenced block with ReadHyperslab: (2,2)-(7,7) */
	vals, err := Read[uint8](d2)
	require.NoError(t, err)
	blk, err := d2.ReadHyperslab(h5.Hyperslab{Start: []uint64{2, 2}, Count: []uint64{1, 1}, Block: []uint64{6, 6}})
	require.NoError(t, err)
	require.Len(t, blk, 36)
	for i, v := range blk {
		r, c := 2+i/6, 2+i%6
		assert.Equal(t, vals[r*10+c], v, "(%d,%d)", r, c)
	}
}

func TestReadHyperslab(t *testing.T) {
	f := h5dumpFile(t, "tdset.h5")
	d := openDS(t, f, "/dset1")

	/* rows 1,3,5 × columns 2,5: element = r+c */
	vals, err := d.ReadHyperslab(h5.Hyperslab{Start: []uint64{1, 2}, Stride: []uint64{2, 3}, Count: []uint64{3, 2}})
	require.NoError(t, err)
	assert.Equal(t, []any{int32(3), int32(6), int32(5), int32(8), int32(7), int32(10)}, vals)

	/* blocks */
	vals, err = d.ReadHyperslab(h5.Hyperslab{Start: []uint64{8, 18}, Stride: []uint64{1, 1}, Count: []uint64{1, 1}, Block: []uint64{2, 2}})
	require.NoError(t, err)
	assert.Equal(t, []any{int32(26), int32(27), int32(27), int32(28)}, vals)

	/* empty selection */
	vals, err = d.ReadHyperslab(h5.Hyperslab{Start: []uint64{0, 0}, Count: []uint64{0, 0}})
	if err == nil {
		assert.Equal(t, []any{}, vals)
	}

	/* errors: rank mismatch, out of bounds */
	_, err = d.ReadHyperslab(h5.Hyperslab{Start: []uint64{0}, Count: []uint64{1}})
	assert.Error(t, err)
	_, err = d.ReadHyperslab(h5.Hyperslab{Start: []uint64{9, 0}, Count: []uint64{2, 1}})
	assert.Error(t, err)
	_, err = d.ReadHyperslab(h5.Hyperslab{Start: []uint64{0, 0}, Stride: []uint64{1}, Count: []uint64{1, 1}})
	assert.Error(t, err)

	/* A scalar dataspace has no hyperslab */
	f2 := h5dumpFile(t, "tvlstr.h5")
	a, err := f2.Root().Attribute("test_scalar")
	require.NoError(t, err)
	assert.Equal(t, h5.SpaceScalar, a.Dataspace().Class)
}

func TestReadIntoErrors(t *testing.T) {
	f := h5dumpFile(t, "tdset.h5")
	d := openDS(t, f, "/dset1")
	var s []int32
	assert.Error(t, d.ReadInto(s))           /* not a pointer */
	assert.Error(t, d.ReadInto(new(int32)))  /* not a slice */
	assert.Error(t, d.ReadInto(nil))         /* nil */
	assert.Error(t, d.ReadInto(&[]string{})) /* integers are not strings */
	var b []bool
	assert.Error(t, d.ReadInto(&b)) /* values > 1 */
	var small []struct{ X int8 }
	assert.Error(t, d.ReadInto(&small)) /* not a compound */
	require.NoError(t, d.ReadInto(&s))
	assert.Len(t, s, 200)

	/* uint8 overflows on a float dataset with large values */
	f2 := h5dumpFile(t, "tattr2.h5")
	a, err := f2.Root().Attribute("integer")
	require.NoError(t, err)
	var u []uint8
	require.NoError(t, a.ReadInto(&u))
	assert.Equal(t, []uint8{1, 2}, u)
}

func TestOpenDatasetNotADataset(t *testing.T) {
	f := h5dumpFile(t, "tcompound.h5")
	_, err := f.OpenDataset("/")
	assert.ErrorContains(t, err, "not a dataset")
	_, err = f.OpenDataset("/type1")
	assert.ErrorContains(t, err, "not a dataset")
	_, err = f.OpenDataset("/no_such_dataset")
	assert.True(t, IsNotFound(err), "%v", err)
	_, err = f.Root().OpenDataset("group1")
	if err != nil {
		assert.NotContains(t, err.Error(), "panic")
	}
}

func TestDatasetThroughExternalLink(t *testing.T) {
	f := h5dumpFile(t, "textlinksrc.h5")

	d := openDS(t, f, "/ext_link2")
	/* (the path in the target file, as for groups: see TestGroupExternalLinks) */
	assert.Equal(t, "/dset", d.Path())
	assert.Equal(t, "textlinktar.h5", filepath.Base(d.f.Name()))
	vals, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{1, 2, 3, 4, 5, 6}, vals)
	assert.Equal(t, []uint64{6}, d.Dataspace().Dims)
	l, err := d.Layout()
	require.NoError(t, err)
	assert.Equal(t, h5.LayoutContiguous, l.Class)
	hs, err := d.ReadHyperslab(h5.Hyperslab{Start: []uint64{1}, Stride: []uint64{2}, Count: []uint64{3}})
	require.NoError(t, err)
	assert.Equal(t, []any{int32(2), int32(4), int32(6)}, hs)

	/* external link to a soft link in the target file */
	d = openDS(t, f, "/ext2soft_link1")
	vals, err = Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{0, 0, 1, 1, 2, 2, 3, 3}, vals)
	assert.Equal(t, h5.OrderBE, d.Datatype().Order)

	/* through Open and Walk */
	obj, err := f.Open("/ext_link1/dset")
	require.NoError(t, err)
	ds, ok := obj.(*Dataset)
	require.True(t, ok)
	vals, err = Read[int32](ds)
	require.NoError(t, err)
	assert.Len(t, vals, 6)
}

func TestDatasetConcurrentReads(t *testing.T) {
	f := h5dumpFile(t, filepath.Join("vds", "1_vds.h5"))
	d := openDS(t, f, "/vds_dset")
	want, err := d.ReadValues()
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := d.ReadValues()
			assert.NoError(t, err)
			assert.Equal(t, want, got)
			_, err = d.Layout()
			assert.NoError(t, err)
			_ = d.Dataspace()
		}()
	}
	wg.Wait()
}

/*
 * Virtual datasets
 */

// vdsCases are the h5dump VDS data tests (tools/test/h5dump/CMakeTestsVDS.cmake)
// that use the default access properties.
var vdsCases = []struct{ file, ddl, layoutDdl string }{
	{"1_vds.h5", "tvds-1.ddl", "tvds_layout-1.ddl"},
	{"2_vds.h5", "tvds-2.ddl", "tvds_layout-2.ddl"},
	{"3_1_vds.h5", "tvds-3_1.ddl", "tvds_layout-3_1.ddl"},
	{"3_2_vds.h5", "tvds-3_2.ddl", "tvds_layout-3_2.ddl"},
	{"4_vds.h5", "tvds-4.ddl", "tvds_layout-4.ddl"},
	{"5_vds.h5", "tvds-5.ddl", "tvds_layout-5.ddl"},
}

// compareDdlData compares the values of a dataset with the DATA block of a
// .ddl file.
func compareDdlData(t *testing.T, vals []any, data []string, what string) {
	t.Helper()
	var got []string
	for _, v := range vals {
		require.True(t, ddlFlatten(v, &got), what)
	}
	want := ddlTokens(data)
	require.Equal(t, len(want), len(got), "%s: token count", what)
	for i := range got {
		if !ddlTokenMatch(got[i], want[i]) {
			assert.Failf(t, "value mismatch", "%s: token %d: got %q, want %q", what, i, got[i], want[i])
			return
		}
	}
}

func TestVirtualDdl(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tools", "test", "h5dump", "testfiles", "vds")
	exp := filepath.Join(root, "tools", "test", "h5dump", "expected", "vds")
	for _, c := range vdsCases {
		t.Run(c.file, func(t *testing.T) {
			file, dsets := parseDdl(t, filepath.Join(exp, c.ddl))
			require.Equal(t, c.file, file)
			require.NotEmpty(t, dsets)
			f, err := Open(filepath.Join(dir, c.file))
			require.NoError(t, err)
			defer f.Close()
			for _, dd := range dsets {
				d := openDS(t, f, dd.path)
				l, err := d.Layout()
				require.NoError(t, err)
				require.Equal(t, h5.LayoutVirtual, l.Class)
				vals, err := d.ReadValues()
				require.NoError(t, err, dd.path)
				assert.EqualValues(t, d.Dataspace().NumElems(), len(vals))
				compareDdlData(t, vals, dd.data, dd.path)
			}
		})
	}
}

// vdsSelPublicString renders a public selection like parseVDSDDL, or
// returns the expanded block list of the ddl's regular hyperslab.
func vdsExpandBlocks(t *testing.T, s string) [][2][]uint64 {
	t.Helper()
	parts := strings.Split(s, "/")
	require.Len(t, parts, 4, s)
	nums := make([][]uint64, 4)
	for i, p := range parts {
		for _, x := range strings.Split(p, ",") {
			n, err := strconv.ParseUint(strings.TrimSpace(x), 10, 64)
			require.NoError(t, err, s)
			nums[i] = append(nums[i], n)
		}
	}
	sel := &h5sSelection{typ: h5sSelHyperslabs, rank: uint32(len(nums[0])), regular: true, start: nums[0], stride: nums[1], count: nums[2], block: nums[3]}
	pub, err := h5rSelectionPublic(sel, sel.rank)
	require.NoError(t, err)
	return pub.Blocks
}

func TestVirtualLayout(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tools", "test", "h5dump", "testfiles", "vds")
	exp := filepath.Join(root, "tools", "test", "h5dump", "expected", "vds")
	cases := append([]struct{ file, ddl, layoutDdl string }{}, vdsCases...)
	cases = append(cases,
		struct{ file, ddl, layoutDdl string }{"vds-eiger.h5", "", "vds_layout-eiger.ddl"},
		struct{ file, ddl, layoutDdl string }{"vds-percival-unlim-maxmin.h5", "", "vds_layout-maxmin.ddl"})
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			want := parseVDSDDL(t, filepath.Join(exp, c.layoutDdl))
			f, err := Open(filepath.Join(dir, c.file))
			require.NoError(t, err)
			defer f.Close()
			var got []h5.VirtualMapping
			for v, err := range f.Root().Walk() {
				require.NoError(t, err)
				d, ok := v.Object.(*Dataset)
				if !ok {
					continue
				}
				l, err := d.Layout()
				require.NoError(t, err)
				if l.Class == h5.LayoutVirtual {
					got = append(got, l.Virtual...)
				}
			}
			require.Len(t, got, len(want))
			rank := func(m h5.VirtualMapping) int { return m.VirtualSelection.Rank }
			for i, w := range want {
				g := got[i]
				assert.Equal(t, w.file, g.SourceFile, "mapping %d", i)
				assert.Equal(t, w.dset, g.SourceDataset, "mapping %d", i)
				for _, side := range []struct {
					want string
					sel  h5.Selection
				}{{w.virtual, g.VirtualSelection}, {w.source, g.SourceSelection}} {
					switch {
					case side.want == "ALL":
						assert.Equal(t, h5.SelAll, side.sel.Type, "mapping %d", i)
					case strings.Contains(side.want, "H5S_UNLIMITED"):
						require.NotNil(t, side.sel.Unlimited, "mapping %d", i)
						hs := side.sel.Unlimited
						f := func(v []uint64) string {
							s := make([]string, len(v))
							for i, x := range v {
								if x == h5.Unlimited {
									s[i] = "H5S_UNLIMITED"
								} else {
									s[i] = strconv.FormatUint(x, 10)
								}
							}
							return strings.Join(s, ",")
						}
						assert.Equal(t, side.want, f(hs.Start)+"/"+f(hs.Stride)+"/"+f(hs.Count)+"/"+f(hs.Block), "mapping %d", i)
						assert.Nil(t, side.sel.Blocks)
					default:
						assert.Equal(t, h5.SelHyperslab, side.sel.Type, "mapping %d", i)
						assert.Nil(t, side.sel.Unlimited)
						assert.Equal(t, vdsExpandBlocks(t, side.want), side.sel.Blocks, "mapping %d", i)
					}
				}
				assert.NotZero(t, rank(g))
			}
		})
	}
}

func TestVirtualSourceFiles(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tools", "test", "h5dump", "testfiles", "vds")
	exp := filepath.Join(root, "tools", "test", "h5dump", "expected", "vds")
	_, dsets := parseDdl(t, filepath.Join(exp, "tvds-2.ddl"))
	require.Len(t, dsets, 1)

	/* A copy of the virtual file alone: the source files are missing and
	 * the elements read as the fill value */
	tmp := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(dir, "2_vds.h5"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "2_vds.h5"), raw, 0o644))
	t.Chdir(tmp)

	f, err := Open(filepath.Join(tmp, "2_vds.h5"))
	require.NoError(t, err)
	d := openDS(t, f, dsets[0].path)
	vals, err := d.ReadValues()
	require.NoError(t, err)
	fv, err := d.FillValue()
	require.NoError(t, err)
	var fill any = int32(0)
	if fv.Defined {
		dec, err := h5tDecodeValues(f, d.Datatype(), fv.Value, 1)
		require.NoError(t, err)
		fill = dec[0]
	}
	for i, v := range vals {
		if !assert.Equal(t, fill, v, "element %d", i) {
			break
		}
	}
	assert.Empty(t, f.extFiles)
	require.NoError(t, f.Close())

	/* WithVirtualPrefix locates the source files */
	f, err = Open(filepath.Join(tmp, "2_vds.h5"), WithVirtualPrefix(dir))
	require.NoError(t, err)
	d = openDS(t, f, dsets[0].path)
	vals, err = d.ReadValues()
	require.NoError(t, err)
	compareDdlData(t, vals, dsets[0].data, dsets[0].path)

	/* The source files are cached with the virtual file and closed with it */
	require.NotEmpty(t, f.extFiles)
	ext := make([]*File, 0, len(f.extFiles))
	for _, ef := range f.extFiles {
		ext = append(ext, ef)
		assert.Equal(t, dir, filepath.Dir(ef.name))
	}
	require.NoError(t, f.Close())
	for _, ef := range ext {
		assert.True(t, ef.closed, ef.name)
	}

	/* "${ORIGIN}" is the directory of the virtual file */
	require.NoError(t, os.Symlink(dir, filepath.Join(tmp, "src")))
	f, err = Open(filepath.Join(tmp, "2_vds.h5"), WithVirtualPrefix("${ORIGIN}/src"))
	require.NoError(t, err)
	defer f.Close()
	vals, err = openDS(t, f, dsets[0].path).ReadValues()
	require.NoError(t, err)
	compareDdlData(t, vals, dsets[0].data, dsets[0].path)
}

// The h5dump tests with --vds-view-first-missing / --vds-gap-size need
// dataset access properties this API does not expose; they are checked
// with the internal view and printf gap settings.
func TestVirtualViewAndGap(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tools", "test", "h5dump", "testfiles", "vds")
	exp := filepath.Join(root, "tools", "test", "h5dump", "expected", "vds")
	cases := []struct {
		ddl, file string
		view      int
		gap       uint64
	}{
		{"vds-first.ddl", "vds-percival-unlim-maxmin.h5", h5dVdsFirstMissing, 0},
		{"vds-gap1.ddl", "vds-eiger.h5", h5dVdsLastAvailable, 1},
		{"vds-gap2.ddl", "vds-eiger.h5", h5dVdsLastAvailable, 2},
	}
	for _, c := range cases {
		t.Run(c.ddl, func(t *testing.T) {
			_, dsets := parseDdl(t, filepath.Join(exp, c.ddl))
			require.NotEmpty(t, dsets)
			f, err := Open(filepath.Join(dir, c.file))
			require.NoError(t, err)
			defer f.Close()
			for _, dd := range dsets {
				d := openDS(t, f, dd.path)
				ds, err := d.dataset()
				require.NoError(t, err)
				/* (H5Pset_virtual_view / H5Pset_virtual_printf_gap on the
				 *  access property list, then H5Dget_space) */
				ds.virt.view, ds.virt.printfGap, ds.virt.init = c.view, c.gap, false
				for i := range ds.virt.list {
					ent := &ds.virt.list[i]
					ent.unlimExtentSource, ent.unlimExtentVirtual = h5sUnlimited, h5sUnlimited
					ent.clipSizeSource, ent.clipSizeVirtual = h5sUnlimited, h5sUnlimited
				}
				_, err = f.h5dGetSpace(ds)
				require.NoError(t, err)
				vals, err := d.ReadValues()
				require.NoError(t, err)
				compareDdlData(t, vals, dd.data, dd.path)
			}
		})
	}
}
