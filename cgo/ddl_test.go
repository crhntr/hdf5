//go:build cgo

package hdf5

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

// The expectations below come from the h5dump output files in
// tools/test/h5dump/expected (named in each test).

func h5dumpFile(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "tools", "test", "h5dump", "testfiles", name)
}

func openTest(t *testing.T, name string, opts ...Option) *File {
	t.Helper()
	path := h5dumpFile(t, name)
	t.Chdir(filepath.Dir(path))
	f, err := Open(path, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, f.Close()) })
	return f
}

func readDataset(t *testing.T, f *File, path string) (*Dataset, []any) {
	t.Helper()
	d, err := f.OpenDataset(path)
	require.NoError(t, err)
	vals, err := d.ReadValues()
	require.NoError(t, err)
	return d, vals
}

func addressOf(t *testing.T, f *File, path string) uint64 {
	t.Helper()
	o, err := f.Open(path)
	require.NoError(t, err)
	defer o.Close()
	info, err := o.Info()
	require.NoError(t, err)
	return info.Address
}

// tcomp-1.ddl
func TestCompound(t *testing.T) {
	f := openTest(t, "tcompound.h5")
	d, vals := readDataset(t, f, "/dset1")
	dt := d.Datatype()
	require.Equal(t, h5.ClassCompound, dt.Class)
	require.Len(t, dt.Members, 3)
	assert.Equal(t, []string{"a_name", "b_name", "c_name"}, []string{dt.Members[0].Name, dt.Members[1].Name, dt.Members[2].Name})
	assert.Equal(t, h5.OrderBE, dt.Members[0].Type.Order)
	assert.Equal(t, []uint64{0, 4, 8}, []uint64{dt.Members[0].Offset, dt.Members[1].Offset, dt.Members[2].Offset})
	require.Len(t, vals, 5)
	for i, v := range vals {
		assert.Equal(t, h5.Compound{Fields: []h5.Field{
			{Name: "a_name", Value: int32(i)},
			{Name: "b_name", Value: float32(i * i)},
			{Name: "c_name", Value: 1 / float64(i+1)},
		}}, v)
	}
	type rec struct {
		A int     `hdf5:"a_name"`
		B float64 `hdf5:"b_name"`
		C float32 `hdf5:"c_name"`
	}
	recs, err := Read[rec](d)
	require.NoError(t, err)
	assert.Equal(t, rec{A: 3, B: 9, C: 0.25}, recs[3])

	// Committed datatype /type1.
	ct, err := f.OpenDatatype("/type1")
	require.NoError(t, err)
	assert.Equal(t, h5.ClassCompound, ct.Datatype().Class)
	info, err := ct.Info()
	require.NoError(t, err)
	assert.Equal(t, h5.ObjectNamedDatatype, info.Type)
}

// tnestedcmpddt.ddl
func TestEnum(t *testing.T) {
	f := openTest(t, "tnestedcmpddt.h5")
	d, vals := readDataset(t, f, "/dset2")
	assert.Equal(t, []uint64{h5.Unlimited}, d.Dataspace().MaxDims)
	enum := d.Datatype().Members[2].Type
	require.Equal(t, h5.ClassEnum, enum.Class)
	assert.Equal(t, []string{"Red", "Green", "Blue", "White", "Black"}, enum.EnumNames)
	assert.Equal(t, []int64{0, 1, 2, 3, 4}, enum.EnumValues)
	assert.Equal(t, h5.ClassInteger, enum.Base.Class)
	require.Len(t, vals, 6)
	assert.Equal(t, h5.Compound{Fields: []h5.Field{
		{Name: "a_name", Value: int32(5)},
		{Name: "b_name", Value: float32(5.5)},
		{Name: "c_name", Value: h5.Enum{Name: "Green", Value: 1}},
	}}, vals[5])

	_, vals = readDataset(t, f, "/dset4")
	names, err := h5.ConvertValues[string](vals)
	require.NoError(t, err)
	assert.Equal(t, []string{"Red", "Green", "Blue", "Green", "White", "Blue"}, names)
}

// tarray1.ddl
func TestArray(t *testing.T) {
	f := openTest(t, "tarray1.h5")
	d, vals := readDataset(t, f, "/Dataset1")
	assert.Equal(t, []uint64{4}, d.Datatype().ArrayDims)
	assert.Equal(t, uint64(16), d.Datatype().Size)
	assert.Equal(t, []any{int32(10), int32(11), int32(12), int32(13)}, vals[1])
	rows, err := Read[[]int](d)
	require.NoError(t, err)
	assert.Equal(t, [][]int{{0, 1, 2, 3}, {10, 11, 12, 13}, {20, 21, 22, 23}, {30, 31, 32, 33}}, rows)
}

// tvldtypes1.ddl, tvlstr.ddl
func TestVlen(t *testing.T) {
	f := openTest(t, "tvldtypes1.h5")
	d, vals := readDataset(t, f, "/Dataset1.0")
	assert.Equal(t, h5.ClassVlen, d.Datatype().Class)
	assert.Equal(t, uint64(16), d.Datatype().Size) // on-disk size: length + heap ID
	assert.Equal(t, []any{
		[]any{int32(0)},
		[]any{int32(10), int32(11)},
		[]any{int32(20), int32(21), int32(22)},
		[]any{int32(30), int32(31), int32(32), int32(33)},
	}, vals)

	f = openTest(t, "tvlstr.h5")
	d, vals = readDataset(t, f, "/Dataset1")
	assert.True(t, d.Datatype().IsVlenString())
	assert.Equal(t, []any{
		"Four score and seven years ago our forefathers brought forth on this continent a new nation,",
		"conceived in liberty and dedicated to the proposition that all men are created equal.",
		"", "",
	}, vals)
}

// tstr-2.ddl
func TestStringPadding(t *testing.T) {
	f := openTest(t, "tstr2.h5")
	d, vals := readDataset(t, f, "/g5/dset5")
	assert.Equal(t, h5.StrSpacePad, d.Datatype().StrPad)
	assert.Equal(t, "This is row 0 of type H5T_STR_SPACEPAD of", vals[0])
	d, vals = readDataset(t, f, "/g1/dset1")
	assert.Equal(t, h5.StrNullTerm, d.Datatype().StrPad)
	assert.Equal(t, uint64(50), d.Datatype().Size)
	for _, v := range vals {
		assert.NotContains(t, v.(string), "\x00")
	}
}

// tdatareg.ddl
func TestRegionReferences(t *testing.T) {
	f := openTest(t, "tdatareg.h5")
	d, vals := readDataset(t, f, "/Dataset1")
	assert.Equal(t, h5.RefRegion1, d.Datatype().RefType)
	assert.Equal(t, uint64(12), d.Datatype().Size)
	target := addressOf(t, f, "/Dataset2")
	require.Len(t, vals, 4)
	assert.Equal(t, h5.RegionRef{Address: target, Selection: h5.Selection{
		Type: h5.SelHyperslab, Rank: 2, Blocks: [][2][]uint64{{{2, 2}, {7, 7}}},
	}}, vals[0])
	assert.Equal(t, h5.RegionRef{Address: target, Selection: h5.Selection{
		Type: h5.SelPoints, Rank: 2, Points: [][]uint64{
			{6, 9}, {2, 2}, {8, 4}, {1, 6}, {2, 8}, {3, 2}, {0, 4}, {9, 0}, {7, 1}, {3, 3},
		},
	}}, vals[1])
	assert.Equal(t, h5.RegionRef{Undefined: true}, vals[2])
	assert.Equal(t, h5.RegionRef{Undefined: true}, vals[3])
}

// trefer_objR.ddl, trefer_attrR.ddl
func TestObjectReferences(t *testing.T) {
	f := openTest(t, "trefer_obj.h5")
	target := addressOf(t, f, "/Group1/Dataset1")
	d, vals := readDataset(t, f, "/Dataset4")
	assert.Equal(t, h5.RefObject1, d.Datatype().RefType)
	assert.Equal(t, []any{h5.ObjectRef{Address: target}}, vals)
	d, vals = readDataset(t, f, "/Dataset3")
	assert.Equal(t, h5.RefObject2, d.Datatype().RefType)
	assert.Equal(t, []any{h5.ObjectRef{Address: target}}, vals)

	f = openTest(t, "trefer_attr.h5")
	_, vals = readDataset(t, f, "/Dataset3")
	assert.Equal(t, h5.AttrRef{Address: addressOf(t, f, "/Group1/Dataset1"), Name: "Attr1"}, vals[0])
	assert.Equal(t, h5.AttrRef{Address: addressOf(t, f, "/Group1"), Name: "Attr2"}, vals[2])
}

// tfill.ddl
func TestFillValues(t *testing.T) {
	f := openTest(t, "tfvalues.h5")
	d, err := f.OpenDataset("/fill_compound")
	require.NoError(t, err)
	fv, err := d.FillValue()
	require.NoError(t, err)
	assert.True(t, fv.Defined)
	assert.Equal(t, uint8(2), fv.FillTime)  // H5D_FILL_TIME_IFSET
	assert.Equal(t, uint8(2), fv.AllocTime) // H5D_ALLOC_TIME_LATE
	dt := d.Datatype()
	require.Len(t, fv.Value, int(dt.Size))
	assert.Equal(t, uint8(1), fv.Value[dt.Members[0].Offset])
	off := dt.Members[1].Offset
	assert.Equal(t, 2.0, math.Float64frombits(binary.LittleEndian.Uint64(fv.Value[off:off+8])))

	for name, want := range map[string]uint8{"/fill_time_alloc": 0, "/fill_time_never": 1, "/fill_time_ifset": 2} {
		d, err := f.OpenDataset(name)
		require.NoError(t, err)
		fv, err := d.FillValue()
		require.NoError(t, err)
		assert.Equal(t, want, fv.FillTime, name)
	}

	d, err = f.OpenDataset("/fill_array")
	require.NoError(t, err)
	fv, err = d.FillValue()
	require.NoError(t, err)
	assert.False(t, fv.Defined)
	assert.False(t, fv.Undefined) // H5D_FILL_VALUE_DEFAULT
}

// tdeflate.ddl, texternal.ddl
func TestFiltersAndExternal(t *testing.T) {
	f := openTest(t, "tfilters.h5")
	d, err := f.OpenDataset("/deflate")
	require.NoError(t, err)
	l, err := d.Layout()
	require.NoError(t, err)
	assert.Equal(t, h5.LayoutChunked, l.Class)
	assert.Equal(t, []uint64{10, 5}, l.ChunkDims)
	filters, err := d.Filters()
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, h5.FilterDeflate, filters[0].ID)
	assert.Equal(t, []uint32{9}, filters[0].CDValues)
	vals, err := Read[int32](d)
	require.NoError(t, err)
	assert.Len(t, vals, 200)
	part, err := d.ReadHyperslab(h5.Hyperslab{Start: []uint64{1, 2}, Count: []uint64{2, 3}})
	require.NoError(t, err)
	assert.Equal(t, []any{vals[12], vals[13], vals[14], vals[22], vals[23], vals[24]}, part)

	d, err = f.OpenDataset("/external")
	require.NoError(t, err)
	l, err = d.Layout()
	require.NoError(t, err)
	assert.Equal(t, h5.LayoutContiguous, l.Class)
	assert.Equal(t, []h5.ExternalFile{{Name: "ext1.bin", Size: 200}, {Name: "ext2.bin", Size: 200}}, l.External)
}

// vds/tvds-1.ddl, vds/tvds_layout-1.ddl
func TestVirtual(t *testing.T) {
	f := openTest(t, "vds/1_vds.h5")
	d, vals := readDataset(t, f, "/vds_dset")
	assert.Equal(t, []uint64{5, 18, 8}, d.Dataspace().Dims)
	assert.Equal(t, []uint64{h5.Unlimited, 18, 8}, d.Dataspace().MaxDims)
	l, err := d.Layout()
	require.NoError(t, err)
	assert.Equal(t, h5.LayoutVirtual, l.Class)
	require.Len(t, l.Virtual, 6)
	m := l.Virtual[1]
	assert.Equal(t, "1_b.h5", m.SourceFile)
	assert.Equal(t, "/source_dset", m.SourceDataset)
	assert.Equal(t, &h5.Hyperslab{
		Start: []uint64{0, 2, 0}, Stride: []uint64{1, 1, 1}, Count: []uint64{1, 1, 1}, Block: []uint64{h5.Unlimited, 4, 8},
	}, m.VirtualSelection.Unlimited)
	ints, err := h5.ConvertValues[int](vals)
	require.NoError(t, err)
	at := func(i, j, k int) int { return ints[(i*18+j)*8+k] }
	assert.Equal(t, 10, at(0, 0, 0))
	assert.Equal(t, 20, at(0, 2, 0))
	assert.Equal(t, 60, at(0, 17, 7))
	assert.Equal(t, 11, at(1, 0, 0))
	assert.Equal(t, 21, at(1, 5, 0))
}

func checkIJ(t *testing.T, f *File) {
	t.Helper()
	d, err := f.OpenDataset("/dset1")
	require.NoError(t, err)
	vals, err := Read[int32](d)
	require.NoError(t, err)
	require.Len(t, vals, 150)
	for i := 0; i < 10; i++ {
		for j := 0; j < 15; j++ {
			assert.Equal(t, int32(i+j), vals[i*15+j])
		}
	}
}

// tfamily.ddl, tmulti.ddl, tsplit_file.ddl
func TestDrivers(t *testing.T) {
	t.Run("family", func(t *testing.T) {
		f := openTest(t, "tfamily%05d.h5", WithDriver("family"))
		checkIJ(t, f)
	})
	t.Run("multi", func(t *testing.T) {
		f := openTest(t, "tmulti", WithDriver("multi"))
		checkIJ(t, f)
		assert.Equal(t, "NCSAmult", f.Superblock().DriverName)
	})
	t.Run("split", func(t *testing.T) {
		f := openTest(t, "tsplit_file", WithDriver("split"))
		checkIJ(t, f)
		a, err := f.Root().Attribute("Metadata")
		require.NoError(t, err)
		s, err := ReadAttribute[string](a)
		require.NoError(t, err)
		assert.Equal(t, []string{"this is some metadata on this file"}, s)
	})
	for _, tc := range []struct{ path, driver string }{
		{"test/testfiles/multi_file_v16", "multi"},
		{"test/testfiles/family_v16-%06d.h5", "family"},
		{"tools/test/misc/testfiles/family_file%05d.h5", "family"},
	} {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			f, err := Open(filepath.Join(repoRoot(t), tc.path), WithDriver(tc.driver))
			require.NoError(t, err)
			defer f.Close()
			n := 0
			for v, err := range f.Root().Walk() {
				require.NoError(t, err)
				if d, ok := v.Object.(*Dataset); ok {
					_, err := d.ReadValues()
					assert.NoError(t, err, v.Path)
					n++
				}
			}
			assert.NotZero(t, n)
		})
	}
}

func TestLinks(t *testing.T) {
	f := openTest(t, "tall.h5")
	g, err := f.OpenGroup("/g1/g1.2/g1.2.1")
	require.NoError(t, err)
	l, err := g.Link("slink")
	require.NoError(t, err)
	assert.Equal(t, h5.LinkSoft, l.Type)
	assert.Equal(t, "somevalue", l.SoftPath)

	f = openTest(t, "textlink.h5")
	l, err = f.Root().Link("extlink1")
	require.NoError(t, err)
	assert.Equal(t, h5.LinkExternal, l.Type)
	assert.Equal(t, "filename", l.ExternalFile)
	assert.Equal(t, "objname", l.ExternalPath)

	var paths []string
	for v, err := range openTest(t, "tall.h5").Root().Walk() {
		require.NoError(t, err)
		paths = append(paths, v.Path)
	}
	assert.Equal(t, []string{
		"/g1", "/g1/g1.1", "/g1/g1.1/dset1.1.1", "/g1/g1.1/dset1.1.2",
		"/g1/g1.2", "/g1/g1.2/extlink", "/g1/g1.2/g1.2.1", "/g1/g1.2/g1.2.1/slink",
		"/g2", "/g2/dset2.1", "/g2/dset2.2", "/g2/udlink",
	}, paths)
}
