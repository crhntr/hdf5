package hdf5

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/HDFGroup/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

/*
 * Generated file: testdata/dset.h5 (see testdata/gen_dset.c)
 */

// testOpenGenerated opens a dataset of testdata/dset.h5 (refreshing the
// extent of virtual datasets, as H5Dget_space does).
func testOpenGenerated(t *testing.T, f *File, path string) *h5dShared {
	t.Helper()
	ds := testOpenDataset(t, f, path)
	if ds.virt != nil {
		_, err := f.h5dGetSpace(ds)
		require.NoError(t, err)
	}
	return ds
}

func i32s(vals ...int) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = int32(v)
	}
	return out
}

func i32Range(from, n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = int32(from + i)
	}
	return out
}

func TestDatasetGenerated(t *testing.T) {
	f, err := Open("testdata/dset.h5")
	require.NoError(t, err)
	defer f.Close()

	fillPartial := make([]any, 100)
	for r := 0; r < 10; r++ {
		for c := 0; c < 10; c++ {
			v := int16(-1)
			if r >= 2 && r <= 5 && c >= 2 && c <= 5 {
				v = int16(r*10 + c)
			}
			fillPartial[r*10+c] = v
		}
	}
	compact := make([]any, 12)
	for i := range compact {
		compact[i] = int16(i)
	}
	cases := []struct {
		path  string
		class h5.LayoutClass
		index h5.ChunkIndex
		dims  []uint64
		want  []any
	}{
		{"/single_unfilt", h5.LayoutChunked, h5.ChunkIndexSingle, []uint64{4, 5}, i32Range(0, 20)},
		{"/single_filt", h5.LayoutChunked, h5.ChunkIndexSingle, []uint64{4, 5}, i32Range(0, 20)},
		{"/single_unalloc", h5.LayoutChunked, h5.ChunkIndexSingle, []uint64{4, 5}, i32s(7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7)},
		{"/edge_fa", h5.LayoutChunked, h5.ChunkIndexFArray, []uint64{7, 9}, i32Range(0, 63)},
		{"/edge_ea", h5.LayoutChunked, h5.ChunkIndexEArray, []uint64{7, 9}, i32Range(0, 63)},
		{"/fill_partial", h5.LayoutChunked, h5.ChunkIndexFArray, []uint64{10, 10}, fillPartial},
		{"/contig_unalloc", h5.LayoutContiguous, 0, []uint64{6}, i32s(42, 42, 42, 42, 42, 42)},
		{"/compact", h5.LayoutCompact, 0, []uint64{3, 4}, compact},
		{"/external", h5.LayoutContiguous, 0, []uint64{10}, i32Range(100, 10)},
		{"/vds", h5.LayoutVirtual, 0, []uint64{12}, i32s(10, 11, 12, 13, 14, -9, 20, 21, 22, 23, 24, -9)},
		{"/vds_printf", h5.LayoutVirtual, 0, []uint64{6}, i32s(30, 31, 32, 40, 41, 42)},
		{"/vds_unlim", h5.LayoutVirtual, 0, []uint64{4}, i32Range(50, 4)},
		{"/vds_xfile", h5.LayoutVirtual, 0, []uint64{4}, i32Range(60, 4)},
		{"/vds_missing", h5.LayoutVirtual, 0, []uint64{4}, i32s(5, 5, 5, 5)},
	}
	bin := h5dumpBin()
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			ds := testOpenGenerated(t, f, c.path)
			l, err := ds.layoutInfo()
			require.NoError(t, err)
			assert.Equal(t, c.class, l.Class)
			if c.class == h5.LayoutChunked {
				assert.Equal(t, c.index, l.ChunkIndex)
			}
			assert.Equal(t, c.dims, ds.dataspace().Dims)
			vals, err := f.dReadValues(ds, nil)
			require.NoError(t, err)
			assert.Equal(t, c.want, vals)
			if bin != "" {
				raw, err := f.dRead(ds, nil)
				require.NoError(t, err)
				want, err := h5dumpRaw(t, bin, filepath.Join(f.dir, "dset.h5"), c.path)
				require.NoError(t, err)
				assert.Equal(t, want, raw)
			}
		})
	}

	t.Run("layout details", func(t *testing.T) {
		ds := testOpenGenerated(t, f, "/external")
		l, err := ds.layoutInfo()
		require.NoError(t, err)
		assert.Equal(t, []h5.ExternalFile{{Name: "dset_ext.raw", Offset: 16, Size: 12}, {Name: "dset_ext.raw", Offset: 100, Size: 28}}, l.External)
		assert.False(t, l.AddressDefined)

		ds = testOpenGenerated(t, f, "/single_filt")
		assert.Equal(t, []h5.Filter{
			{ID: h5.FilterShuffle, Name: "shuffle", Flags: 1, CDValues: []uint32{4}},
			{ID: h5.FilterDeflate, Name: "deflate", Flags: 1, CDValues: []uint32{6}},
		}, ds.filters())
		l, err = ds.layoutInfo()
		require.NoError(t, err)
		assert.Equal(t, []uint64{4, 5}, l.ChunkDims)
		assert.EqualValues(t, ds.chunkSt.singleNbytes, ds.storageSize(f))

		ds = testOpenGenerated(t, f, "/contig_unalloc")
		fv, err := ds.fillValue(f)
		require.NoError(t, err)
		assert.True(t, fv.Defined)
		assert.Equal(t, []byte{42, 0, 0, 0}, fv.Value)
		assert.EqualValues(t, 0, ds.storageSize(f))
		dec, err := ds.fillValueDecoded(f)
		require.NoError(t, err)
		assert.Equal(t, int32(42), dec)
	})

	t.Run("fill time never", func(t *testing.T) {
		ds := testOpenGenerated(t, f, "/fill_never")
		vals, err := f.dReadValues(ds, nil)
		require.NoError(t, err)
		for r := 0; r < 10; r++ {
			for c := 0; c < 10; c++ {
				switch {
				case r >= 2 && r <= 5 && c >= 2 && c <= 5:
					assert.Equal(t, int16(r*10+c), vals[r*10+c])
				case r >= 6 || c >= 6:
					/* chunks never written: skipped, the buffer keeps zeros */
					assert.Equal(t, int16(0), vals[r*10+c], "(%d,%d)", r, c)
				}
			}
		}
	})

	t.Run("undefined fill value", func(t *testing.T) {
		ds := testOpenGenerated(t, f, "/contig_undef")
		fv, err := ds.fillValue(f)
		require.NoError(t, err)
		assert.True(t, fv.Undefined)
		_, err = f.dReadValues(ds, nil)
		assert.ErrorContains(t, err, "dataset doesn't exist")
	})

	t.Run("vlen fill", func(t *testing.T) {
		ds := testOpenGenerated(t, f, "/vlen_fill")
		vals, err := f.dReadValues(ds, nil)
		require.NoError(t, err)
		assert.Equal(t, []any{i32s(1), i32s(2, 3), []any{}, []any{}, []any{}, []any{}}, vals)
	})

	t.Run("virtual hyperslab", func(t *testing.T) {
		ds := testOpenGenerated(t, f, "/vds")
		vals, err := f.dReadHyperslab(ds, h5.Hyperslab{Start: []uint64{3}, Stride: []uint64{2}, Count: []uint64{5}})
		require.NoError(t, err)
		assert.Equal(t, i32s(13, -9, 21, 23, -9), vals)
	})

	t.Run("virtual source lookup", func(t *testing.T) {
		/* Same file */
		src, ds, err := f.dVirtualOpenSource(".", "/compact")
		require.NoError(t, err)
		require.NotNil(t, ds)
		assert.Same(t, f, src)
		assert.Equal(t, []uint64{3, 4}, ds.dataspace().Dims)

		/* Missing dataset, missing file, not a dataset: no error, no dataset */
		for _, c := range [][2]string{{".", "/no_such_dataset"}, {"no_such_file.h5", "/x"}, {".", "/"}} {
			src, ds, err := f.dVirtualOpenSource(c[0], c[1])
			assert.NoError(t, err, c)
			assert.Nil(t, ds, c)
			assert.Nil(t, src, c)
		}
	})

	t.Run("chunk iteration", func(t *testing.T) {
		ds := testOpenGenerated(t, f, "/fill_partial")
		var offsets [][]uint64
		require.NoError(t, f.h5dChunkIter(ds, func(off []uint64, mask uint32, addr haddr, n uint64) error {
			offsets = append(offsets, off)
			assert.EqualValues(t, 18, n)
			return nil
		}))
		assert.Equal(t, [][]uint64{{0, 0}, {0, 3}, {3, 0}, {3, 3}}, offsets)
	})
}

// testFindDataset returns the object header address of the dataset at path.
func testFindDataset(t testing.TB, f *File, path string) haddr {
	t.Helper()
	refs, _ := testWalkDatasets(t, f)
	for _, r := range refs {
		if r.path == path {
			return r.addr
		}
	}
	t.Fatalf("dataset %s not found", path)
	return haddrUndef
}

/*
 * Golden h5dump output (tools/test/h5dump/expected/*.ddl)
 */

// ddlDataset is the DATA block of one dataset of a .ddl file.
type ddlDataset struct {
	path string
	data []string
}

var ddlIndexRE = regexp.MustCompile(`\(\d+(,\d+)*\):`)

// parseDdl returns the datasets of an h5dump output with a DATA block.
func parseDdl(t *testing.T, name string) (file string, out []ddlDataset) {
	fh, err := os.Open(name)
	require.NoError(t, err)
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<24)
	var (
		groups  []string // group path per indent level
		cur     *ddlDataset
		curInd  = -1
		inData  bool
		headRE  = regexp.MustCompile(`^HDF5 "(.*)" \{$`)
		groupRE = regexp.MustCompile(`^(\s*)GROUP "(.*)" \{$`)
		dsetRE  = regexp.MustCompile(`^(\s*)DATASET "(.*)" \{$`)
	)
	for sc.Scan() {
		line := sc.Text()
		if m := headRE.FindStringSubmatch(line); m != nil {
			file = m[1]
			continue
		}
		indent := (len(line) - len(strings.TrimLeft(line, " "))) / 3
		if m := groupRE.FindStringSubmatch(line); m != nil {
			for len(groups) <= indent {
				groups = append(groups, "")
			}
			groups = groups[:indent+1]
			groups[indent] = m[2]
			continue
		}
		if m := dsetRE.FindStringSubmatch(line); m != nil {
			p := m[2]
			if !strings.HasPrefix(p, "/") {
				var parts []string
				for i := 0; i < indent && i < len(groups); i++ {
					if g := groups[i]; g != "" && g != "/" {
						parts = append(parts, strings.Trim(g, "/"))
					}
				}
				parts = append(parts, p)
				p = "/" + strings.Join(parts, "/")
			}
			out = append(out, ddlDataset{path: p})
			cur, curInd, inData = &out[len(out)-1], indent, false
			continue
		}
		if cur == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case !inData && indent == curInd+1 && trimmed == "DATA {":
			inData = true
		case inData && indent == curInd+1 && trimmed == "}":
			inData = false
			cur = nil
		case inData:
			cur.data = append(cur.data, trimmed)
		}
	}
	require.NoError(t, sc.Err())
	return file, out
}

// ddlTokens splits h5dump data lines into numbers and quoted strings.
var ddlTokenRE = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"|-?(?:\d+\.?\d*(?:e[+-]?\d+)?|inf|nan|INF|NAN|-nan)`)

func ddlTokens(lines []string) []string {
	var toks []string
	for _, l := range lines {
		l = ddlIndexRE.ReplaceAllString(l, "")
		for _, m := range ddlTokenRE.FindAllStringSubmatch(l, -1) {
			if strings.HasPrefix(m[0], `"`) {
				toks = append(toks, "s:"+m[1])
			} else {
				toks = append(toks, m[0])
			}
		}
	}
	return toks
}

// ddlFlatten flattens canonical values into the tokens h5dump prints, or
// returns false for values it does not print as numbers or strings.
func ddlFlatten(v any, out *[]string) bool {
	switch x := v.(type) {
	case int8, int16, int32, int64, uint8, uint16, uint32, uint64:
		*out = append(*out, fmt.Sprint(x))
	case float32:
		*out = append(*out, "f:"+strconv.FormatFloat(float64(x), 'g', -1, 32))
	case float64:
		*out = append(*out, "f:"+strconv.FormatFloat(x, 'g', -1, 64))
	case string:
		*out = append(*out, "s:"+x)
	case h5.Compound:
		for _, fl := range x.Fields {
			if !ddlFlatten(fl.Value, out) {
				return false
			}
		}
	case []any:
		for _, e := range x {
			if !ddlFlatten(e, out) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

// ddlEscape is how h5dump escapes the characters of a string.
func ddlEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r >= 0x7f {
				fmt.Fprintf(&b, `\%03o`, r)
			} else {
				b.WriteByte(r)
			}
		}
	}
	return b.String()
}

// ddlTokenMatch compares one decoded token with one h5dump token.
func ddlTokenMatch(got, want string) bool {
	switch {
	case strings.HasPrefix(got, "s:"):
		/* (h5dump shows the padding of space- and null-padded strings) */
		trim := func(s string) string {
			for {
				t := strings.TrimRight(s, " ")
				t = strings.TrimSuffix(t, `\000`)
				if t == s {
					return s
				}
				s = t
			}
		}
		return strings.HasPrefix(want, "s:") && trim(ddlEscape(got[2:])) == trim(want[2:])
	case strings.HasPrefix(got, "f:"):
		g, err1 := strconv.ParseFloat(got[2:], 64)
		w, err2 := strconv.ParseFloat(strings.TrimPrefix(strings.ToLower(want), "-nan"), 64)
		if strings.Contains(strings.ToLower(want), "nan") {
			return math.IsNaN(g)
		}
		if err1 != nil || err2 != nil {
			return false
		}
		if g == w {
			return true
		}
		return math.Abs(g-w) <= 1e-5*math.Max(math.Abs(g), math.Abs(w))
	}
	return got == want
}

func TestDatasetDdlValues(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tools", "test", "h5dump", "testfiles")
	cases := []struct {
		ddl  string
		open string // file to open, relative to dir ("" = the ddl's file)
	}{
		{"tdset-1", ""}, {"tcmpdints", ""}, {"tcomp-1", ""}, {"tcompound_complex2", ""},
		{"tnestcomp-1", ""}, {"tstr-1", ""}, {"tsaf", ""}, {"tvldtypes1", ""}, {"tvldtypes2", ""},
		{"tvldtypes3", ""}, {"tvldtypes4", ""}, {"tvldtypes5", ""}, {"tarray1", ""}, {"tarray2", ""},
		{"tarray3", ""}, {"tarray4", ""}, {"tarray5", ""}, {"tarray6", ""}, {"tarray7", ""},
		{"tarray8", ""}, {"tempty", ""}, {"tfamily", "tfamily%05d.h5"}, {"tmulti", "tmulti"},
		{"tfill", ""}, {"treadintfilter", ""}, {"thyperslab", ""}, {"tnullspace", ""},
		{"zerodim", ""}, {"tfloat16", ""}, {"tvms", ""}, {"tintsattrs", ""},
		{"tldouble", ""}, {"tall-1", ""}, {"tgroup-1", ""}, {"tattr-1", ""},
	}
	compared := 0
	for _, c := range cases {
		t.Run(c.ddl, func(t *testing.T) {
			file, dsets := parseDdl(t, filepath.Join(root, "tools", "test", "h5dump", "expected", c.ddl+".ddl"))
			name := c.open
			if name == "" {
				name = file
			}
			f, err := Open(filepath.Join(dir, name))
			require.NoError(t, err)
			defer f.Close()
			n := 0
			for _, d := range dsets {
				if len(d.data) == 0 {
					continue
				}
				oh, err := f.oProtect(testFindDataset(t, f, d.path))
				require.NoError(t, err)
				ds, err := f.dOpen(oh)
				require.NoError(t, err, d.path)
				vals, err := f.dReadValues(ds, nil)
				require.NoError(t, err, d.path)
				var got []string
				ok := true
				for _, v := range vals {
					if !ddlFlatten(v, &got) {
						ok = false
						break
					}
				}
				if !ok {
					continue
				}
				want := ddlTokens(d.data)
				if !assert.Equal(t, len(want), len(got), "%s: token count\nwant %v\ngot  %v", d.path, want, got) {
					continue
				}
				for i := range got {
					if !ddlTokenMatch(got[i], want[i]) {
						assert.Failf(t, "value mismatch", "%s: token %d: got %q, want %q", d.path, i, got[i], want[i])
						break
					}
				}
				compared++
				n++
			}
			t.Logf("%s: %d datasets compared", c.ddl, n)
		})
	}
	t.Logf("compared %d datasets with the golden h5dump output", compared)
}

/*
 * Hyperslab and point selections
 */

func TestDatasetHyperslab(t *testing.T) {
	bin := h5dumpBin()
	root := repoRoot(t)
	cases := []struct {
		file, path string
		hs         h5.Hyperslab
	}{
		/* 2-D chunked (5x5 chunks, deflate), crossing chunk boundaries */
		{"tools/test/h5ls/testfiles/tdset_idx.h5", "/dset_filter", h5.Hyperslab{Start: []uint64{1, 2}, Stride: []uint64{3, 2}, Count: []uint64{6, 4}, Block: []uint64{2, 1}}},
		{"tools/test/h5ls/testfiles/tdset_idx.h5", "/dset_btree", h5.Hyperslab{Start: []uint64{3, 3}, Count: []uint64{10, 6}}},
		{"tools/test/h5ls/testfiles/tdset_idx.h5", "/dset_fixed", h5.Hyperslab{Start: []uint64{0, 1}, Stride: []uint64{4, 4}, Count: []uint64{5, 2}, Block: []uint64{3, 2}}},
		/* contiguous */
		{"tools/test/h5dump/testfiles/tdset.h5", "/dset1", h5.Hyperslab{Start: []uint64{1, 1}, Stride: []uint64{2, 3}, Count: []uint64{4, 6}, Block: []uint64{1, 2}}},
		{"tools/test/h5dump/testfiles/tdset.h5", "/dset2", h5.Hyperslab{Start: []uint64{5, 0}, Count: []uint64{3, 20}}},
		/* chunked with partial edge chunks (fill values) */
		{"go/hdf5/testdata/dset.h5", "/fill_partial", h5.Hyperslab{Start: []uint64{1, 1}, Stride: []uint64{2, 2}, Count: []uint64{4, 5}, Block: []uint64{2, 1}}},
		{"go/hdf5/testdata/dset.h5", "/edge_ea", h5.Hyperslab{Start: []uint64{2, 3}, Count: []uint64{5, 6}}},
	}
	for _, c := range cases {
		t.Run(c.file+c.path, func(t *testing.T) {
			f, err := Open(filepath.Join(root, c.file))
			require.NoError(t, err)
			defer f.Close()
			oh, err := f.oProtect(testFindDataset(t, f, c.path))
			require.NoError(t, err)
			ds, err := f.dOpen(oh)
			require.NoError(t, err)

			/* Expected values: pick the elements from a whole read */
			all, err := f.dReadValues(ds, nil)
			require.NoError(t, err)
			dims := ds.currDims
			var want []any
			stride := c.hs.Stride
			if stride == nil {
				stride = []uint64{1, 1}
			}
			block := c.hs.Block
			if block == nil {
				block = []uint64{1, 1}
			}
			for i := uint64(0); i < c.hs.Count[0]; i++ {
				for bi := uint64(0); bi < block[0]; bi++ {
					r := c.hs.Start[0] + i*stride[0] + bi
					for j := uint64(0); j < c.hs.Count[1]; j++ {
						for bj := uint64(0); bj < block[1]; bj++ {
							col := c.hs.Start[1] + j*stride[1] + bj
							want = append(want, all[r*dims[1]+col])
						}
					}
				}
			}
			got, err := f.dReadHyperslab(ds, c.hs)
			require.NoError(t, err)
			assert.Equal(t, want, got)

			/* h5dump -s START -S STRIDE -c COUNT -k BLOCK */
			if bin != "" {
				raw, err := f.dRead(ds, testHyperslabSpace(t, ds, c.hs))
				require.NoError(t, err)
				join := func(v []uint64) string {
					s := make([]string, len(v))
					for i, x := range v {
						s[i] = strconv.FormatUint(x, 10)
					}
					return strings.Join(s, ",")
				}
				args := []string{"-s", join(c.hs.Start), "-c", join(c.hs.Count), "-S", join(stride), "-k", join(block)}
				exp, err := h5dumpRaw(t, bin, filepath.Join(root, c.file), c.path, args...)
				require.NoError(t, err)
				assert.Equal(t, exp, raw)
			}
		})
	}
}

func testHyperslabSpace(t *testing.T, ds *h5dShared, hs h5.Hyperslab) *h5s {
	space, err := h5sSelectHyperslabPublic(ds.space, hs)
	require.NoError(t, err)
	return space
}

func TestDatasetPointSelection(t *testing.T) {
	root := repoRoot(t)
	for _, c := range []struct{ file, path string }{
		{"tools/test/h5ls/testfiles/tdset_idx.h5", "/dset_filter"},
		{"tools/test/h5dump/testfiles/tdset.h5", "/dset1"},
		{"go/hdf5/testdata/dset.h5", "/fill_partial"},
		{"go/hdf5/testdata/dset.h5", "/compact"},
	} {
		t.Run(c.file+c.path, func(t *testing.T) {
			f, err := Open(filepath.Join(root, c.file))
			require.NoError(t, err)
			defer f.Close()
			oh, err := f.oProtect(testFindDataset(t, f, c.path))
			require.NoError(t, err)
			ds, err := f.dOpen(oh)
			require.NoError(t, err)
			all, err := f.dReadValues(ds, nil)
			require.NoError(t, err)
			dims := ds.currDims

			/* Points in an arbitrary order, crossing chunks, with repeats */
			pts := [][]uint64{{dims[0] - 1, dims[1] - 1}, {0, 0}, {dims[0] / 2, 1}, {0, 0}, {1, dims[1] - 1}, {dims[0] - 1, 0}}
			var want []any
			for _, p := range pts {
				want = append(want, all[p[0]*dims[1]+p[1]])
			}
			space := h5sCopy(ds.space)
			var cp [][]uint64
			for _, p := range pts {
				cp = append(cp, append([]uint64(nil), p...))
			}
			h5sSelectElements(space, cp)
			got, err := f.dReadValues(ds, space)
			require.NoError(t, err)
			assert.Equal(t, want, got)

			/* A single element (the "single piece" path) */
			one := h5sCopy(ds.space)
			h5sSelectElements(one, [][]uint64{{dims[0] - 1, 1}})
			got, err = f.dReadValues(ds, one)
			require.NoError(t, err)
			assert.Equal(t, []any{all[(dims[0]-1)*dims[1]+1]}, got)

			/* An empty selection */
			none := h5sCopy(ds.space)
			h5sSelectNone(none)
			got, err = f.dReadValues(ds, none)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

/*
 * Corrupt input
 */

// testMutatedFile opens an in-memory copy of a file modified by mutate,
// without checksum verification (so that the corruption reaches the
// dataset code).
func testMutatedFile(t *testing.T, data []byte, mutate func(b []byte) []byte) *File {
	b := mutate(append([]byte(nil), data...))
	f, err := OpenReader(bytes.NewReader(b), int64(len(b)), WithChecksumVerification(false))
	require.NoError(t, err)
	return f
}

func TestDatasetCorrupt(t *testing.T) {
	data, err := os.ReadFile("testdata/dset.h5")
	require.NoError(t, err)
	orig, err := Open("testdata/dset.h5")
	require.NoError(t, err)
	defer orig.Close()

	/* The single filtered chunk: its address, size and filter mask are in
	 * the layout message. */
	ds := testOpenDataset(t, orig, "/single_filt")
	chunkAddr := ds.chunkSt.idxAddr
	nbytes := ds.chunkSt.singleNbytes
	var layoutOff uint64
	for _, m := range ds.oh.mesgs {
		if m.typeID == h5oLayoutID {
			layoutOff = m.rawOff
		}
	}
	require.NotZero(t, layoutOff)
	/* version, class, flags, ndims, enc bytes, 3 dims of 1 byte, index type */
	nbytesOff := layoutOff + 5 + 3*1 + 1
	require.Equal(t, uint64(nbytes), binary.LittleEndian.Uint64(data[nbytesOff:]), "layout message layout")
	maskOff := nbytesOff + 8

	read := func(f *File, path string) error {
		defer f.Close()
		refs, _ := testWalkDatasets(t, f)
		addr := haddrUndef
		for _, r := range refs {
			if r.path == path {
				addr = r.addr
			}
		}
		if !addrDefined(addr) {
			return errors.New("dataset not found")
		}
		oh, err := f.oProtect(addr)
		if err != nil {
			return err
		}
		ds, err := f.dOpen(oh)
		if err != nil {
			return err
		}
		_, err = f.dReadValues(ds, nil)
		return err
	}

	t.Run("chunk size past end of file", func(t *testing.T) {
		f := testMutatedFile(t, data, func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[nbytesOff:], uint64(len(b))*2)
			return b
		})
		err := read(f, "/single_filt")
		assert.Error(t, err)
		assert.NotContains(t, fmt.Sprint(err), "dataset not found")
		t.Log(err)
	})
	t.Run("bad filter mask", func(t *testing.T) {
		f := testMutatedFile(t, data, func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[maskOff:], 0x2) /* skip deflate */
			return b
		})
		err := read(f, "/single_filt")
		assert.Error(t, err)
		assert.NotContains(t, fmt.Sprint(err), "dataset not found")
		t.Log(err)
	})
	t.Run("truncated chunk", func(t *testing.T) {
		f := testMutatedFile(t, data, func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[nbytesOff:], uint64(nbytes-5))
			return b
		})
		err := read(f, "/single_filt")
		assert.Error(t, err)
		assert.NotContains(t, fmt.Sprint(err), "dataset not found")
		t.Log(err)
	})
	t.Run("corrupt chunk data", func(t *testing.T) {
		f := testMutatedFile(t, data, func(b []byte) []byte {
			for i := uint64(0); i < uint64(nbytes); i++ {
				b[chunkAddr+i] ^= 0x5a
			}
			return b
		})
		err := read(f, "/single_filt")
		assert.Error(t, err)
		assert.NotContains(t, fmt.Sprint(err), "dataset not found")
		t.Log(err)
	})
	t.Run("file truncated before the chunk", func(t *testing.T) {
		b := append([]byte(nil), data[:chunkAddr+uint64(nbytes)/2]...)
		f, err := OpenReader(bytes.NewReader(b), int64(len(b)), WithChecksumVerification(false))
		if err != nil {
			return
		}
		err = read(f, "/single_filt")
		assert.Error(t, err)
	})

	/* A version 1 B-tree chunk index: the first key of the (leaf) root
	 * node claims a chunk larger than the file */
	t.Run("v1 B-tree chunk size past end of file", func(t *testing.T) {
		name := filepath.Join(repoRoot(t), "tools/test/h5repack/testfiles/h5repack_filters.h5")
		bdata, err := os.ReadFile(name)
		require.NoError(t, err)
		bf, err := Open(name)
		require.NoError(t, err)
		ds := testOpenDataset(t, bf, "/dset_deflate")
		l, err := ds.layoutInfo()
		require.NoError(t, err)
		require.Equal(t, h5.ChunkIndexBTree1, l.ChunkIndex)
		/* the key of chunk (0,0): nbytes, filter mask 0, offsets 0,0,0 */
		var nb uint64
		require.NoError(t, bf.h5dChunkIter(ds, func(off []uint64, _ uint32, _ haddr, n uint64) error {
			if off[0] == 0 && off[1] == 0 {
				nb = n
			}
			return nil
		}))
		bf.Close()
		pat := make([]byte, 4+4+3*8)
		binary.LittleEndian.PutUint32(pat, uint32(nb))
		key0 := bytes.Index(bdata, pat)
		require.Positive(t, key0)
		f := testMutatedFile(t, bdata, func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[key0:], uint32(len(b))*2)
			return b
		})
		err = read(f, "/dset_deflate")
		assert.ErrorContains(t, err, "invalid chunk size")
	})

	/* No panics on random single-byte corruption of any dataset of the
	 * file */
	t.Run("random corruption", func(t *testing.T) {
		refs, _ := testWalkDatasets(t, orig)
		seed := uint64(1)
		for i := 0; i < 2000; i++ {
			seed = seed*6364136223846793005 + 1442695040888963407
			pos := int(seed>>33) % len(data)
			val := byte(seed >> 17)
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Errorf("panic with byte %d set to %#x: %v", pos, val, p)
					}
				}()
				b := append([]byte(nil), data...)
				b[pos] = val
				f, err := OpenReader(bytes.NewReader(b), int64(len(b)), WithChecksumVerification(false))
				if err != nil {
					return
				}
				defer f.Close()
				for _, r := range refs {
					oh, err := f.oProtect(r.addr)
					if err != nil {
						continue
					}
					ds, err := f.dOpen(oh)
					if err != nil {
						continue
					}
					_, _ = ds.layoutInfo()
					_, _ = ds.fillValue(f)
					_ = ds.storageSize(f)
					if ds.space.extent.nelem*ds.typ.Size < 1<<24 {
						_, _ = f.dReadValues(ds, nil)
					}
				}
			}()
		}
	})
}

/*
 * Family / multi drivers
 */

func TestDatasetDrivers(t *testing.T) {
	dir := repoRoot(t)
	for _, name := range []string{"tools/test/h5dump/testfiles/tfamily%05d.h5", "tools/test/misc/testfiles/family_file%05d.h5", "tools/test/h5dump/testfiles/tmulti", "tools/test/h5dump/testfiles/tsplit_file"} {
		t.Run(name, func(t *testing.T) {
			f, err := Open(filepath.Join(dir, name))
			if err != nil && errors.Is(err, ErrNotFound) {
				t.Skip(err)
			}
			require.NoError(t, err)
			defer f.Close()
			refs, errs := testWalkDatasets(t, f)
			assert.Empty(t, errs)
			for _, r := range refs {
				oh, err := f.oProtect(r.addr)
				require.NoError(t, err)
				ds, err := f.dOpen(oh)
				require.NoError(t, err, r.path)
				vals, err := f.dReadValues(ds, nil)
				require.NoError(t, err, r.path)
				assert.Len(t, vals, int(max(ds.space.extent.nelem, 1)))
			}
		})
	}
}
