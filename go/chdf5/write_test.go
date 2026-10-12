//go:build cgo

package chdf5

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HDFGroup/hdf5/go/h5"
)

func roundTrip[T Element](t *testing.T, g *Group, name string, dims []uint64, data []T, opts ...DatasetOption) *Dataset {
	t.Helper()
	d, err := WriteDataset(g, name, dims, data, opts...)
	require.NoError(t, err)
	got, err := Read[T](d)
	require.NoError(t, err)
	assert.Equal(t, data, got, name)
	return d
}

func TestWriteRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []CreateOption
	}{
		{"default", nil},
		{"latest", []CreateOption{WithLatestFormat()}},
		{"userblock", []CreateOption{WithUserblock(512), WithSymK(8, 2), WithIstoreK(64)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "w.h5")
			f, err := Create(path, tc.opts...)
			require.NoError(t, err)
			root := f.Root()
			roundTrip(t, root, "i8", []uint64{4}, []int8{-128, -1, 0, 127})
			roundTrip(t, root, "u16", []uint64{2, 2}, []uint16{0, 1, 2, 65535})
			roundTrip(t, root, "i32", []uint64{3}, []int32{-5, 0, 5})
			roundTrip(t, root, "i64", []uint64{2}, []int64{-1 << 62, 1 << 62})
			roundTrip(t, root, "int", []uint64{2}, []int{-7, 7})
			roundTrip(t, root, "u64", []uint64{2}, []uint64{0, 1<<64 - 1})
			roundTrip(t, root, "f32", []uint64{2}, []float32{1.5, -2.25})
			roundTrip(t, root, "f64", nil, []float64{3.141592653589793})
			roundTrip(t, root, "bool", []uint64{3}, []bool{true, false, true})
			sd := roundTrip(t, root, "str", []uint64{3}, []string{"a", "hello", ""})
			assert.Equal(t, h5.ClassString, sd.Datatype().Class)
			assert.Equal(t, uint64(5), sd.Datatype().Size)
			vd := roundTrip(t, root, "vstr", []uint64{2}, []string{"variable", "länge"}, WithVariableLengthStrings())
			assert.True(t, vd.Datatype().IsVlenString())
			assert.Equal(t, h5.CsetUTF8, vd.Datatype().Cset)

			data := make([]float64, 100)
			for i := range data {
				data[i] = float64(i) / 3
			}
			cd := roundTrip(t, root, "chunked", []uint64{10, 10}, data,
				WithChunk(5, 5), WithShuffle(), WithDeflate(6), WithFletcher32(), WithMaxDims(h5.Unlimited, 10), WithFillValue(-1.0))
			l, err := cd.Layout()
			require.NoError(t, err)
			assert.Equal(t, h5.LayoutChunked, l.Class)
			assert.Equal(t, []uint64{5, 5}, l.ChunkDims)
			assert.Equal(t, []uint64{h5.Unlimited, 10}, cd.Dataspace().MaxDims)
			filters, err := cd.Filters()
			require.NoError(t, err)
			var ids []h5.FilterID
			for _, fl := range filters {
				ids = append(ids, fl.ID)
			}
			assert.Equal(t, []h5.FilterID{h5.FilterFletcher32, h5.FilterShuffle, h5.FilterDeflate}, ids)
			fv, err := cd.FillValue()
			require.NoError(t, err)
			assert.True(t, fv.Defined)
			assert.Len(t, fv.Value, 8)
			part, err := cd.ReadHyperslab(h5.Hyperslab{Start: []uint64{2, 3}, Stride: []uint64{2, 2}, Count: []uint64{2, 2}})
			require.NoError(t, err)
			assert.Equal(t, []any{data[23], data[25], data[43], data[45]}, part)

			cmp := roundTrip(t, root, "compact", []uint64{4}, []int16{1, 2, 3, 4}, WithCompact())
			l, err = cmp.Layout()
			require.NoError(t, err)
			assert.Equal(t, h5.LayoutCompact, l.Class)
			assert.Equal(t, uint64(8), l.CompactSize)
			ctg := roundTrip(t, root, "contig", []uint64{4}, []uint32{1, 2, 3, 4}, WithContiguous())
			l, err = ctg.Layout()
			require.NoError(t, err)
			assert.Equal(t, h5.LayoutContiguous, l.Class)
			assert.True(t, l.AddressDefined)
			assert.Equal(t, uint64(16), l.Size)

			g, err := f.CreateGroup("/a/b")
			require.NoError(t, err)
			require.NoError(t, WriteAttribute(g, "scalar", nil, []int32{42}))
			require.NoError(t, WriteAttribute(g, "names", []uint64{2}, []string{"x", "yz"}))
			require.NoError(t, WriteAttribute[float64](ctg, "scale", []uint64{1}, []float64{0.5}))
			require.NoError(t, SetComment(g, "a comment"))
			require.NoError(t, root.CreateSoftLink("soft", "/a/b"))
			require.NoError(t, root.CreateExternalLink("ext", "other.h5", "/x"))
			require.NoError(t, root.CreateHardLink("hard", "/a"))
			co, err := root.CreateGroup("ordered", WithCreationOrder())
			require.NoError(t, err)
			for _, n := range []string{"zeta", "alpha", "mid"} {
				_, err := co.CreateGroup(n)
				require.NoError(t, err)
			}
			require.NoError(t, f.Close())

			f, err = Open(path)
			require.NoError(t, err)
			defer f.Close()
			b, err := f.OpenGroup("/a/b")
			require.NoError(t, err)
			c, err := b.Comment()
			require.NoError(t, err)
			assert.Equal(t, "a comment", c)
			var attrNames []string
			for a, err := range b.Attributes() {
				require.NoError(t, err)
				attrNames = append(attrNames, a.Name())
			}
			assert.Equal(t, []string{"names", "scalar"}, attrNames)
			a, err := b.Attribute("names")
			require.NoError(t, err)
			names, err := ReadAttribute[string](a)
			require.NoError(t, err)
			assert.Equal(t, []string{"x", "yz"}, names)
			a, err = b.Attribute("scalar")
			require.NoError(t, err)
			assert.Equal(t, h5.SpaceScalar, a.Dataspace().Class)
			sv, err := a.ReadValues()
			require.NoError(t, err)
			assert.Equal(t, []any{int32(42)}, sv)

			l2, err := f.Root().Link("soft")
			require.NoError(t, err)
			assert.Equal(t, h5.Link{Name: "soft", Type: h5.LinkSoft, SoftPath: "/a/b"}, l2)
			l2, err = f.Root().Link("ext")
			require.NoError(t, err)
			assert.Equal(t, "other.h5", l2.ExternalFile)
			assert.Equal(t, "/x", l2.ExternalPath)
			o, err := f.Open("/soft")
			require.NoError(t, err)
			assert.Equal(t, "/soft", o.Path())
			_, ok := o.(*Group)
			assert.True(t, ok)

			co2, err := f.OpenGroup("/ordered")
			require.NoError(t, err)
			var order []string
			for l, err := range co2.LinksByCreationOrder() {
				require.NoError(t, err)
				assert.True(t, l.HasCreationOrder)
				order = append(order, l.Name)
			}
			assert.Equal(t, []string{"zeta", "alpha", "mid"}, order)
			order = nil
			for l, err := range co2.Links() {
				require.NoError(t, err)
				order = append(order, l.Name)
			}
			assert.Equal(t, []string{"alpha", "mid", "zeta"}, order)
			n, err := co2.NumLinks()
			require.NoError(t, err)
			assert.Equal(t, 3, n)
			for _, err := range f.Root().LinksByCreationOrder() {
				assert.Error(t, err) // not tracked in the root group
			}

			// Walk: /hard is a second hard link to /a, which must not be
			// descended into twice.
			var paths []string
			for v, err := range f.Root().Walk() {
				require.NoError(t, err)
				paths = append(paths, v.Path)
				if v.Link.Type == h5.LinkHard {
					assert.NotNil(t, v.Object, v.Path)
				} else {
					assert.Nil(t, v.Object, v.Path)
				}
			}
			assert.Contains(t, paths, "/a/b")
			assert.Contains(t, paths, "/hard")
			assert.NotContains(t, paths, "/hard/b")

			ainfo, err := f.Root().Info()
			require.NoError(t, err)
			obj, err := f.OpenAddress(ainfo.Address)
			require.NoError(t, err)
			assert.Equal(t, "/", obj.Path())

			sb := f.Superblock()
			assert.Equal(t, uint8(8), sb.SizeOfOffsets)
			assert.Equal(t, ainfo.Address, sb.RootGroupAddress)
			switch tc.name {
			case "latest":
				assert.Equal(t, uint8(3), sb.Version)
				assert.Equal(t, uint8(2), ainfo.HeaderVersion)
			case "userblock":
				assert.Equal(t, uint64(512), sb.BaseAddress)
				assert.Equal(t, uint(2), sb.SymLeafK)
				assert.Equal(t, uint(8), sb.SnodeBtreeK)
				assert.Equal(t, uint(64), sb.ChunkBtreeK)
				assert.Equal(t, uint8(1), sb.Version) // non-default istore K
			default:
				assert.Equal(t, uint8(0), sb.Version)
				assert.Equal(t, uint8(1), ainfo.HeaderVersion)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing.h5"))
	require.Error(t, err)
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Stack, "H5Fopen")

	f, err := Create(filepath.Join(t.TempDir(), "e.h5"))
	require.NoError(t, err)
	defer f.Close()
	_, err = f.OpenDataset("/nope")
	assert.Error(t, err)
	_, err = WriteDataset(f.Root(), "bad", []uint64{3}, []int32{1, 2})
	assert.Error(t, err)
	g, err := f.CreateGroup("g")
	require.NoError(t, err)
	_, err = f.OpenDataset("/g")
	assert.Error(t, err)
	_, err = g.Attribute("nope")
	assert.Error(t, err)
}

func TestExternalLinkOptions(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	tf, err := Create(filepath.Join(sub, "target.h5"))
	require.NoError(t, err)
	_, err = WriteDataset(tf.Root(), "x", []uint64{2}, []int32{7, 8})
	require.NoError(t, err)
	require.NoError(t, tf.Close())

	src := filepath.Join(dir, "src.h5")
	sf, err := Create(src)
	require.NoError(t, err)
	require.NoError(t, sf.Root().CreateExternalLink("ext", "target.h5", "/x"))
	require.NoError(t, sf.Close())

	t.Chdir(dir)
	f, err := Open(src)
	require.NoError(t, err)
	_, err = f.OpenDataset("/ext")
	assert.Error(t, err, "target.h5 is not next to src.h5")
	require.NoError(t, f.Close())

	f, err = Open(src, WithExternalLinkPrefix(sub))
	require.NoError(t, err)
	d, err := f.OpenDataset("/ext")
	require.NoError(t, err)
	vals, err := Read[int32](d)
	require.NoError(t, err)
	assert.Equal(t, []int32{7, 8}, vals)
	require.NoError(t, f.Close())

	f, err = Open(src, WithExternalLinkPrefix(sub), WithExternalLinks(false))
	require.NoError(t, err)
	_, err = f.OpenDataset("/ext")
	assert.Error(t, err)
	l, err := f.Root().Link("ext")
	require.NoError(t, err)
	assert.Equal(t, h5.LinkExternal, l.Type)
	require.NoError(t, f.Close())
}
