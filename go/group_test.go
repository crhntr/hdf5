package hdf5

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

// grpTestFile returns the path of a corpus file relative to the repository
// root.
func grpTestFile(t *testing.T, rel string) string {
	t.Helper()
	path := rel
	if !filepath.IsAbs(rel) {
		path = filepath.Join(repoRoot(t), rel)
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("%s: %v", rel, err)
	}
	return path
}

// grpOpen opens a corpus file and closes it at the end of the test.
func grpOpen(t *testing.T, rel string, opts ...Option) *File {
	t.Helper()
	f, err := Open(grpTestFile(t, rel), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// grpWalkPaths returns the paths reported by Walk from the root group.
func grpWalkPaths(t *testing.T, f *File) []string {
	t.Helper()
	var paths []string
	for v, err := range f.Root().Walk() {
		require.NoError(t, err)
		paths = append(paths, v.Path)
	}
	return paths
}

// grpCollectLinks collects a link sequence.
func grpCollectLinks(seq func(func(h5.Link, error) bool)) ([]h5.Link, error) {
	var lnks []h5.Link
	for l, err := range seq {
		if err != nil {
			return lnks, err
		}
		lnks = append(lnks, l)
	}
	return lnks, nil
}

func grpLinkNames(lnks []h5.Link) []string {
	names := make([]string, len(lnks))
	for i, l := range lnks {
		names[i] = l.Name
	}
	return names
}

// h5lsEscape is print_string (tools/src/h5ls/h5ls.c) with escape_spaces:
// how h5ls prints link names.
func h5lsEscape(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case ' ':
			sb.WriteString(`\ `)
		default:
			if c >= 0x20 && c < 0x7f {
				sb.WriteByte(c)
			} else {
				fmt.Fprintf(&sb, `\%03o`, c)
			}
		}
	}
	return sb.String()
}

// h5lsPaths extracts the object paths of "h5ls -r" output: the lines
// starting with '/', the path being everything up to the first unescaped
// space (the root group's own line is skipped).
func h5lsPaths(out string) []string {
	var paths []string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		l := sc.Text()
		if !strings.HasPrefix(l, "/") {
			continue
		}
		end := len(l)
		for i := 0; i < len(l); i++ {
			if l[i] == '\\' {
				i++
				continue
			}
			if l[i] == ' ' {
				end = i
				break
			}
		}
		if p := l[:end]; p != "/" {
			paths = append(paths, p)
		}
	}
	return paths
}

// grpWalkEscaped returns the Walk paths as h5ls prints them.
func grpWalkEscaped(t *testing.T, path string) ([]string, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var paths []string
	for v, err := range f.Root().Walk() {
		if err != nil {
			return paths, err
		}
		parts := strings.Split(strings.TrimPrefix(v.Path, "/"), "/")
		for i := range parts {
			parts[i] = h5lsEscape(parts[i])
		}
		paths = append(paths, "/"+strings.Join(parts, "/"))
	}
	return paths, nil
}

// h5lsBinary returns the h5ls of the reference installation, or "".
func h5lsBinary() string {
	if p := filepath.Join(os.Getenv("HOME"), ".local/hdf5-1.14.6/bin/h5ls"); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath("h5ls"); err == nil {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// TestGroupWalkCorpus walks every file of the corpus that h5dump can open
// and checks every visited object and group.
func TestGroupWalkCorpus(t *testing.T) {
	root := repoRoot(t)
	files := corpusFiles(t)
	require.NotEmpty(t, files)

	var nfiles, nlinks, nobjects, ngroups, known int
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		if h5dumpFailures[rel] {
			// Files the C library cannot read: only check that walking
			// them does not panic.
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s: panic: %v\n%s", rel, r, debug.Stack())
					}
				}()
				f, err := Open(path)
				if err != nil {
					return
				}
				defer f.Close()
				for v, err := range f.Root().Walk() {
					if err != nil {
						break
					}
					if v.Object != nil {
						v.Object.Info()
					}
				}
			}()
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v\n%s", rel, r, debug.Stack())
				}
			}()
			f, err := Open(path)
			if err != nil {
				if !errors.Is(err, ErrNotHDF5) {
					t.Errorf("%s: open: %v", rel, err)
				}
				return
			}
			defer f.Close()
			nfiles++

			check := func(where string, g *Group) {
				ngroups++
				lnks, err := grpCollectLinks(g.Links())
				if !assert.NoError(t, err, "%s %s: links", rel, where) {
					return
				}
				n, err := g.NumLinks()
				assert.NoError(t, err, "%s %s", rel, where)
				assert.Equal(t, len(lnks), n, "%s %s: number of links", rel, where)
				names := grpLinkNames(lnks)
				assert.True(t, slices.IsSorted(names), "%s %s: links not in name order", rel, where)
				for i, l := range lnks {
					if i >= 100 {
						break
					}
					got, err := g.Link(l.Name)
					if assert.NoError(t, err, "%s %s: link %q", rel, where, l.Name) {
						assert.Equal(t, l, got, "%s %s: link %q", rel, where, l.Name)
					}
				}
				oh, err := g.header()
				require.NoError(t, err)
				linfo, ok, err := f.gObjGetLinfo(oh)
				require.NoError(t, err)
				corder, err := grpCollectLinks(g.LinksByCreationOrder())
				if ok && linfo.trackCorder {
					if assert.NoError(t, err, "%s %s: creation order", rel, where) {
						assert.ElementsMatch(t, names, grpLinkNames(corder), "%s %s", rel, where)
						assert.True(t, slices.IsSortedFunc(corder, func(a, b h5.Link) int {
							return int(a.CreationOrder - b.CreationOrder)
						}), "%s %s: links not in creation order", rel, where)
					}
				} else {
					assert.Error(t, err, "%s %s: creation order not tracked", rel, where)
				}
			}

			r := f.Root()
			if ohdrKnownFailures[rel] != "" {
				_, err := r.Info()
				assert.Error(t, err, rel)
				known++
				return
			}
			info, err := r.Info()
			if assert.NoError(t, err, rel) {
				assert.Equal(t, h5.ObjectGroup, info.Type, rel)
				assert.Equal(t, f.sblock.rootAddr, info.Address, rel)
			}
			check("/", r)
			for v, err := range r.Walk() {
				if !assert.NoError(t, err, rel) {
					break
				}
				nlinks++
				assert.True(t, strings.HasPrefix(v.Path, "/"), "%s: %s", rel, v.Path)
				assert.Equal(t, v.Link.Name, v.Path[strings.LastIndexByte(v.Path, '/')+1:], "%s: %s", rel, v.Path)
				if v.Link.Type != h5.LinkHard {
					assert.Nil(t, v.Object, "%s: %s", rel, v.Path)
					continue
				}
				if v.Object == nil {
					t.Logf("%s: %s: object of unknown type", rel, v.Path)
					continue
				}
				nobjects++
				assert.Equal(t, v.Path, v.Object.Path(), rel)
				oi, err := v.Object.Info()
				if !assert.NoError(t, err, "%s: %s", rel, v.Path) {
					continue
				}
				assert.Equal(t, v.Link.Address, oi.Address, "%s: %s", rel, v.Path)
				switch o := v.Object.(type) {
				case *Group:
					assert.Equal(t, h5.ObjectGroup, oi.Type, "%s: %s", rel, v.Path)
					check(v.Path, o)
				case *Dataset:
					assert.Equal(t, h5.ObjectDataset, oi.Type, "%s: %s", rel, v.Path)
				case *Datatype:
					assert.Equal(t, h5.ObjectNamedDatatype, oi.Type, "%s: %s", rel, v.Path)
					assert.NotNil(t, o.Datatype(), "%s: %s", rel, v.Path)
				}
				_, err = v.Object.Comment()
				assert.NoError(t, err, "%s: %s", rel, v.Path)
			}
		}()
	}
	t.Logf("corpus: %d files walked, %d links, %d objects, %d groups checked, %d known failures", nfiles, nlinks, nobjects, ngroups, known)
	assert.Greater(t, nfiles, 300)
}

// h5lsGoldenTests are the h5ls tests (tools/test/h5ls/CMakeTests.cmake)
// that list a whole file recursively without following symbolic links,
// with the file they list.
var h5lsGoldenTests = map[string]string{
	"tall-2":        "tall.h5",
	"tdset-1":       "tdset.h5",
	"tslink-1":      "tslink.h5",
	"textlink-1":    "textlink.h5",
	"textlinksrc-4": "textlinksrc.h5",
	"tudlink-1":     "tudlink.h5",
	"tcomp-1":       "tcompound.h5",
	"tnestcomp-1":   "tnestedcomp.h5",
	"tnestcomp-2":   "tnestedcomp.h5",
	"tnestcomp-3":   "tnestedcomp.h5",
	"tnestcomp-4":   "tnestedcomp.h5",
	"tloop-1":       "tloop.h5",
	"tstr-1":        "tstr.h5",
	"tsaf":          "tsaf.h5",
	"tvldtypes1":    "tvldtypes1.h5",
	"tarray1":       "tarray1.h5",
}

// h5lsTestFile locates a file used by the h5ls tests.
func h5lsTestFile(t *testing.T, name string) string {
	root := repoRoot(t)
	for _, dir := range []string{"tools/test/h5ls/testfiles", "tools/testfiles", "tools/test/h5dump/testfiles"} {
		if p := filepath.Join(root, dir, name); fileExists(p) {
			return p
		}
	}
	t.Fatalf("h5ls test file %s not found", name)
	return ""
}

// TestGroupWalkMatchesH5lsExpected compares Walk with the expected output
// of the recursive h5ls tests.
func TestGroupWalkMatchesH5lsExpected(t *testing.T) {
	root := repoRoot(t)
	var names []string
	for name := range h5lsGoldenTests {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(root, "tools/test/h5ls/expected", name+".ls"))
		require.NoError(t, err)
		want := h5lsPaths(string(b))
		require.NotEmpty(t, want, name)
		got, err := grpWalkEscaped(t, h5lsTestFile(t, h5lsGoldenTests[name]))
		require.NoError(t, err, name)
		assert.Equal(t, want, got, name)
	}
}

// TestGroupWalkMatchesH5ls compares the paths reported by Walk with the
// output of "h5ls -r" (same set, same order: depth first, name order) for
// the files of the h5ls tests and for every other corpus file h5dump can
// read, when the reference h5ls is installed.
func TestGroupWalkMatchesH5ls(t *testing.T) {
	h5ls := h5lsBinary()
	if h5ls == "" {
		t.Skip("h5ls not installed")
	}
	if quickMode() {
		t.Skip("quick mode: runs h5ls on the whole corpus")
	}
	root := repoRoot(t)

	// h5ls -r never finishes on this file (it keeps printing the padding
	// of an overlong name).
	h5lsHangs := map[string]bool{"tools/test/h5dump/testfiles/tlonglinks.h5": true}

	var files []string
	for _, name := range []string{
		"tdset_idx.h5", "tall.h5", "tarray1.h5", "tattr2.h5", "tattrreg.h5", "tcompound.h5",
		"tdatareg.h5", "tdset.h5", "tempty.h5", "textlink.h5", "textlinksrc.h5", "textlinktar.h5",
		"tfloat16.h5", "tfloat16_be.h5", "tgroup.h5", "tgrp_comments.h5", "tgrpnullspace.h5",
		"thlink.h5", "tloop.h5", "tnestedcomp.h5", "tsaf.h5", "tslink.h5", "tsoftlinks.h5",
		"tstr.h5", "tudfilter.h5", "tudlink.h5", "tvldtypes1.h5",
	} {
		files = append(files, h5lsTestFile(t, name))
	}
	h5lsFiles := len(files)
	files = append(files, corpusFiles(t)...)

	seen := map[string]bool{}
	var compared, matched, paths, skipped int
	for i, path := range files {
		rel, _ := filepath.Rel(root, path)
		if seen[rel] || h5dumpFailures[rel] || ohdrKnownFailures[rel] != "" || h5lsHangs[rel] {
			continue
		}
		seen[rel] = true
		got, err := grpWalkEscaped(t, path)
		if errors.Is(err, ErrNotHDF5) {
			continue
		}
		if !assert.NoError(t, err, rel) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		out, err := exec.CommandContext(ctx, h5ls, "-r", path).Output()
		timedOut := ctx.Err() == context.DeadlineExceeded
		cancel()
		if timedOut {
			t.Logf("%s: h5ls timed out", rel)
			skipped++
			continue
		}
		if err != nil && len(out) == 0 {
			t.Logf("%s: h5ls failed: %v", rel, err)
			skipped++
			continue
		}
		compared++
		want := h5lsPaths(string(out))
		if assert.Equal(t, want, got, rel) {
			matched++
			paths += len(want)
		}
		if i < h5lsFiles {
			assert.NotEmpty(t, want, rel)
		}
	}
	t.Logf("h5ls -r: %d files compared, %d identical (%d paths), %d skipped", compared, matched, paths, skipped)
	assert.Equal(t, compared, matched)
	assert.Greater(t, compared, 300)
}

// TestGroupCompactCreationOrder checks compact groups that track the
// creation order of their links against h5dump --sort_by=creation_order
// (tordergr1.ddl / tordergr3.ddl).
func TestGroupCompactCreationOrder(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tordergr.h5")

	g, err := f.OpenGroup("/1")
	require.NoError(t, err)
	assert.Equal(t, "/1", g.Path())

	byName, err := grpCollectLinks(g.Links())
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, grpLinkNames(byName))

	byCorder, err := grpCollectLinks(g.LinksByCreationOrder())
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "b", "a"}, grpLinkNames(byCorder))
	for i, l := range byCorder {
		assert.True(t, l.HasCreationOrder)
		assert.EqualValues(t, i, l.CreationOrder)
	}

	a2, err := g.OpenGroup("a/a2")
	require.NoError(t, err)
	assert.Equal(t, "/1/a/a2", a2.Path())
	byCorder, err = grpCollectLinks(a2.LinksByCreationOrder())
	require.NoError(t, err)
	assert.Equal(t, []string{"a22", "a21"}, grpLinkNames(byCorder))

	// Lookup by index in both orders (H5Lget_name_by_idx).
	oh, err := g.header()
	require.NoError(t, err)
	for _, tc := range []struct {
		idx   h5Index
		order h5IterOrder
		want  []string
	}{
		{h5IndexName, h5IterInc, []string{"a", "b", "c"}},
		{h5IndexName, h5IterDec, []string{"c", "b", "a"}},
		{h5IndexCrtOrder, h5IterInc, []string{"c", "b", "a"}},
		{h5IndexCrtOrder, h5IterDec, []string{"a", "b", "c"}},
	} {
		for n, want := range tc.want {
			name, err := f.gObjGetNameByIdx(oh, tc.idx, tc.order, uint64(n))
			require.NoError(t, err)
			assert.Equal(t, want, name)
			lnk, err := f.gObjLookupByIdx(oh, tc.idx, tc.order, uint64(n))
			require.NoError(t, err)
			assert.Equal(t, want, lnk.name)
		}
		_, err := f.gObjLookupByIdx(oh, tc.idx, tc.order, 3)
		assert.Error(t, err)
	}
	info, err := f.gObjInfo(oh)
	require.NoError(t, err)
	assert.Equal(t, h5gInfo{storageType: h5gStorageTypeCompact, nlinks: 3, maxCorder: 3}, info)

	// Skipping links (H5Literate2 with an index).
	var last uint64
	var names []string
	require.NoError(t, f.gObjIterate(oh, h5IndexCrtOrder, h5IterInc, 1, &last, func(l *h5oLink) error {
		names = append(names, l.name)
		return nil
	}))
	assert.Equal(t, []string{"b", "a"}, names)
	assert.EqualValues(t, 3, last)
	assert.Error(t, f.gObjIterate(oh, h5IndexName, h5IterInc, 3, nil, func(*h5oLink) error { return nil }))

	// Walk visits the groups depth first in name order.
	assert.Equal(t, []string{
		"/1", "/1/a", "/1/a/a1", "/1/a/a2", "/1/a/a2/a21", "/1/a/a2/a22", "/1/b", "/1/c",
		"/2", "/2/a", "/2/a/a1", "/2/a/a2", "/2/a/a2/a21", "/2/a/a2/a22", "/2/b", "/2/c",
	}, grpWalkPaths(t, f))
}

// TestGroupDenseCreationOrderIndex checks a dense group with an indexed
// creation order (testdata/bt2_corder.h5, see testdata/bt2_gen.c: soft links
// "l0000".."l0299" to "/t0000".., created in that order).
func TestGroupDenseCreationOrderIndex(t *testing.T) {
	f, err := Open("testdata/bt2_corder.h5")
	require.NoError(t, err)
	defer f.Close()

	g, err := f.OpenGroup("links")
	require.NoError(t, err)
	oh, err := g.header()
	require.NoError(t, err)
	info, err := f.gObjInfo(oh)
	require.NoError(t, err)
	assert.Equal(t, h5gStorageTypeDense, info.storageType)
	assert.EqualValues(t, 300, info.nlinks)

	byName, err := grpCollectLinks(g.Links())
	require.NoError(t, err)
	byCorder, err := grpCollectLinks(g.LinksByCreationOrder())
	require.NoError(t, err)
	require.Len(t, byName, 300)
	require.Len(t, byCorder, 300)
	for i := range 300 {
		name := fmt.Sprintf("l%04d", i)
		assert.Equal(t, name, byName[i].Name)
		assert.Equal(t, byName[i], byCorder[i])
		assert.Equal(t, h5.LinkSoft, byCorder[i].Type)
		assert.Equal(t, fmt.Sprintf("/t%04d", i), byCorder[i].SoftPath)
		assert.EqualValues(t, i, byCorder[i].CreationOrder)
		assert.True(t, byCorder[i].HasCreationOrder)
	}

	// The creation order index answers native-order lookups; the
	// increasing and decreasing orders build a table.
	for _, n := range []uint64{0, 1, 149, 298, 299} {
		for _, order := range []h5IterOrder{h5IterInc, h5IterNative} {
			lnk, err := f.gObjLookupByIdx(oh, h5IndexCrtOrder, order, n)
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("l%04d", n), lnk.name)
		}
		lnk, err := f.gObjLookupByIdx(oh, h5IndexCrtOrder, h5IterDec, n)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("l%04d", 299-n), lnk.name)
		name, err := f.gObjGetNameByIdx(oh, h5IndexName, h5IterDec, n)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("l%04d", 299-n), name)
	}
	for _, order := range []h5IterOrder{h5IterInc, h5IterDec, h5IterNative} {
		_, err := f.gObjLookupByIdx(oh, h5IndexCrtOrder, order, 300)
		assert.Error(t, err)
	}

	// The soft links are dangling.
	_, err = g.Open("l0000")
	assert.True(t, IsNotFound(err), "%v", err)
	ok, err := f.lExists(g.loc(), "l0000")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = gLocExists(g.loc(), "l0000")
	require.NoError(t, err)
	assert.False(t, ok)
	val, err := f.lGetVal(g.loc(), "l0123")
	require.NoError(t, err)
	assert.Equal(t, "/t0123", string(val))
}

// TestGroupDenseManyLinks iterates and looks up the 35001 links of the
// root group of h5stat_newgrat.h5 (dense storage, a multi-level name
// index).
func TestGroupDenseManyLinks(t *testing.T) {
	f := grpOpen(t, "tools/test/h5stat/testfiles/h5stat_newgrat.h5")
	r := f.Root()

	n, err := r.NumLinks()
	require.NoError(t, err)
	assert.Equal(t, 35001, n)

	lnks, err := grpCollectLinks(r.Links())
	require.NoError(t, err)
	require.Len(t, lnks, 35001)
	names := grpLinkNames(lnks)
	assert.True(t, slices.IsSorted(names))
	assert.Equal(t, "DATASET_NAME", names[0])
	assert.Equal(t, "GROUP1", names[1])
	assert.Equal(t, "GROUP9999", names[35000])

	// Every link points to a distinct object.
	addrs := map[uint64]bool{}
	for _, l := range lnks {
		assert.Equal(t, h5.LinkHard, l.Type)
		addrs[l.Address] = true
	}
	assert.Len(t, addrs, 35001)

	// Stopping early.
	count := 0
	for range r.Links() {
		count++
		if count == 10 {
			break
		}
	}
	assert.Equal(t, 10, count)

	// Lookup by name through the name index.
	for _, i := range []int{0, 1, 2, 17, 1000, 20000, 34999, 35000} {
		l, err := r.Link(names[i])
		require.NoError(t, err)
		assert.Equal(t, lnks[i], l)
	}
	for _, missing := range []string{"GROUP0", "GROUP35001", "group1", "GROUP1 "} {
		_, err = r.Link(missing)
		assert.True(t, IsNotFound(err), "%s: %v", missing, err)
	}
	g, err := r.OpenGroup("GROUP12345")
	require.NoError(t, err)
	assert.Equal(t, "/GROUP12345", g.Path())
	ds, err := f.OpenDataset("/DATASET_NAME")
	require.NoError(t, err)
	assert.Equal(t, "/DATASET_NAME", ds.Path())

	// Lookup by index at the boundaries.
	oh, err := r.header()
	require.NoError(t, err)
	for _, i := range []uint64{0, 1, 34999, 35000} {
		lnk, err := f.gObjLookupByIdx(oh, h5IndexName, h5IterInc, i)
		require.NoError(t, err)
		assert.Equal(t, names[i], lnk.name)
		lnk, err = f.gObjLookupByIdx(oh, h5IndexName, h5IterDec, i)
		require.NoError(t, err)
		assert.Equal(t, names[35000-i], lnk.name)
	}
	_, err = f.gObjLookupByIdx(oh, h5IndexName, h5IterInc, 35001)
	assert.Error(t, err)
	_, err = f.gObjLookupByIdx(oh, h5IndexName, h5IterNative, 35001)
	assert.Error(t, err)

	// Native (hash) order visits the same links.
	var native []string
	require.NoError(t, f.gObjIterate(oh, h5IndexName, h5IterNative, 0, nil, func(l *h5oLink) error {
		native = append(native, l.name)
		return nil
	}))
	assert.False(t, slices.IsSorted(native))
	slices.Sort(native)
	assert.Equal(t, names, native)
	first, err := f.gObjLookupByIdx(oh, h5IndexName, h5IterNative, 0)
	require.NoError(t, err)
	var firstNative string
	require.NoError(t, f.gObjIterate(oh, h5IndexName, h5IterNative, 0, nil, func(l *h5oLink) error {
		firstNative = l.name
		return errBIterStop
	}))
	assert.Equal(t, firstNative, first.name)

	// Creation order is not tracked.
	_, err = grpCollectLinks(r.LinksByCreationOrder())
	assert.Error(t, err)

	// Walk reports all links, and the objects are all openable.
	paths := grpWalkPaths(t, f)
	assert.Len(t, paths, 35001)
}

// TestGroupOldStyle checks old-style (symbol table) groups (tall.h5).
func TestGroupOldStyle(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tall.h5")
	r := f.Root()
	oh, err := r.header()
	require.NoError(t, err)
	_, ok, err := f.gObjGetLinfo(oh)
	require.NoError(t, err)
	require.False(t, ok)
	info, err := f.gObjInfo(oh)
	require.NoError(t, err)
	assert.Equal(t, h5gInfo{storageType: h5gStorageTypeSymbolTable, nlinks: 2}, info)

	lnks, err := grpCollectLinks(r.Links())
	require.NoError(t, err)
	assert.Equal(t, []string{"g1", "g2"}, grpLinkNames(lnks))
	_, err = grpCollectLinks(r.LinksByCreationOrder())
	assert.ErrorContains(t, err, "no creation order index to query")

	g12, err := f.OpenGroup("/g1/g1.2")
	require.NoError(t, err)
	lnks, err = grpCollectLinks(g12.Links())
	require.NoError(t, err)
	assert.Equal(t, []string{"extlink", "g1.2.1"}, grpLinkNames(lnks))
	assert.Equal(t, h5.LinkExternal, lnks[0].Type)
	assert.Equal(t, "somefile", lnks[0].ExternalFile)
	assert.Equal(t, "somepath", lnks[0].ExternalPath)

	// Lookup by index in both orders.
	g1, err := f.OpenGroup("g1")
	require.NoError(t, err)
	g1oh, err := g1.header()
	require.NoError(t, err)
	for i, want := range []string{"g1.1", "g1.2"} {
		lnk, err := f.gObjLookupByIdx(g1oh, h5IndexName, h5IterInc, uint64(i))
		require.NoError(t, err)
		assert.Equal(t, want, lnk.name)
		lnk, err = f.gObjLookupByIdx(g1oh, h5IndexName, h5IterDec, uint64(1-i))
		require.NoError(t, err)
		assert.Equal(t, want, lnk.name)
	}
	var dec []string
	require.NoError(t, f.gObjIterate(g1oh, h5IndexName, h5IterDec, 0, nil, func(l *h5oLink) error {
		dec = append(dec, l.name)
		return nil
	}))
	assert.Equal(t, []string{"g1.2", "g1.1"}, dec)
	_, err = f.gObjLookupByIdx(g1oh, h5IndexName, h5IterInc, 2)
	assert.Error(t, err)
	_, err = f.gObjLookupByIdx(g1oh, h5IndexCrtOrder, h5IterInc, 0)
	assert.Error(t, err)

	// Find by index (H5Oopen_by_idx).
	loc, err := gLocFindByIdx(f.rootLoc(), "/g1", h5IndexName, h5IterDec, 0)
	require.NoError(t, err)
	assert.Equal(t, "/g1/g1.2", loc.path)
	assert.Equal(t, g12.addr, loc.addr)

	// Info against h5ls -v: /g1/g1.1/dset1.1.1 is at 5480, has two
	// attributes and one link.
	ds, err := f.OpenDataset("/g1/g1.1/dset1.1.1")
	require.NoError(t, err)
	oi, err := ds.Info()
	require.NoError(t, err)
	assert.EqualValues(t, 5480, oi.Address)
	assert.Equal(t, h5.ObjectDataset, oi.Type)
	assert.EqualValues(t, 1, oi.RefCount)
	assert.EqualValues(t, 1, oi.HeaderVersion)
	assert.EqualValues(t, 2, oi.NumAttrs)
	assert.EqualValues(t, 0, oi.ATime) // version 1 headers only have the modification time message
	byName, err := gLocInfo(f.rootLoc(), "g1/g1.1/dset1.1.1")
	require.NoError(t, err)
	assert.Equal(t, oi, byName)
	gi, err := gGetInfoByName(f.rootLoc(), "/g2")
	require.NoError(t, err)
	assert.EqualValues(t, 3, gi.nlinks)

	// Paths with "." components and repeated separators.
	obj, err := f.Open("//g1/./g1.1///dset1.1.1")
	require.NoError(t, err)
	assert.Equal(t, "/g1/g1.1/dset1.1.1", obj.Path())
	obj, err = g1.Open(".")
	require.NoError(t, err)
	assert.Equal(t, "/g1", obj.Path())
	obj, err = g1.Open("/g2")
	require.NoError(t, err)
	assert.Equal(t, "/g2", obj.Path())

	// Wrong object types.
	_, err = f.OpenGroup("/g1/g1.1/dset1.1.1")
	assert.ErrorContains(t, err, "not a group")
	_, err = f.OpenDataset("/g1")
	assert.ErrorContains(t, err, "not a dataset")
	_, err = f.OpenDatatype("/g1")
	assert.ErrorContains(t, err, "not a named datatype")
	_, err = f.Open("/g1/g1.1/dset1.1.1/x")
	assert.Error(t, err)
	_, err = f.Open("/nosuch/x")
	assert.True(t, IsNotFound(err), "%v", err)

	// The user-defined link /g2/udlink cannot be followed.
	l, err := f.Root().Link("g2/udlink")
	require.NoError(t, err)
	assert.Equal(t, h5.LinkType(187), l.Type)
	_, err = f.Open("/g2/udlink")
	assert.ErrorContains(t, err, "unable to get UD link class")
}

// TestGroupSoftLinks follows soft links (tsoftlinks.h5, tslink.h5).
func TestGroupSoftLinks(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tsoftlinks.h5")

	dset1, err := f.OpenDataset("/dset1")
	require.NoError(t, err)

	obj, err := f.Open("/soft_dset1")
	require.NoError(t, err)
	ds, ok := obj.(*Dataset)
	require.True(t, ok, "%T", obj)
	assert.Equal(t, "/soft_dset1", ds.Path())
	assert.Equal(t, dset1.addr, ds.addr)

	// A soft link to a group holding soft links.
	ds, err = f.OpenDataset("/soft_group1/soft_dset1")
	require.NoError(t, err)
	assert.Equal(t, "/soft_group1/soft_dset1", ds.Path())
	assert.Equal(t, dset1.addr, ds.addr)

	// Committed datatypes through soft links.
	dt, err := f.OpenDatatype("/group1/soft_dtype")
	require.NoError(t, err)
	assert.Equal(t, h5.ClassInteger, dt.Datatype().Class)
	direct, err := f.OpenDatatype("dtype")
	require.NoError(t, err)
	assert.Equal(t, direct.addr, dt.addr)
	assert.Equal(t, direct.Datatype(), dt.Datatype())

	// Link does not follow the last link.
	l, err := f.Root().Link("soft_dset1")
	require.NoError(t, err)
	assert.Equal(t, h5.Link{Name: "soft_dset1", Type: h5.LinkSoft, SoftPath: "/dset1", Cset: h5.CsetASCII}, l)

	// Dangling soft links.
	l, err = f.Root().Link("soft_dangle")
	require.NoError(t, err)
	assert.Equal(t, "not_yet", l.SoftPath)
	_, err = f.Open("/soft_dangle")
	assert.True(t, IsNotFound(err), "%v", err)

	// Walk reports soft links without following them.
	var softs []string
	for v, err := range f.Root().Walk() {
		require.NoError(t, err)
		if v.Link.Type == h5.LinkSoft {
			assert.Nil(t, v.Object)
			softs = append(softs, v.Path+" -> "+v.Link.SoftPath)
		}
	}
	assert.Equal(t, []string{
		"/group1/soft_dangle -> not_yet",
		"/group1/soft_dset1 -> /dset1",
		"/group1/soft_dset2 -> /dset2",
		"/group1/soft_dtype -> /dtype",
		"/group1/soft_empty_grp -> /group_empty",
		"/soft_dangle -> not_yet",
		"/soft_dset1 -> /dset1",
		"/soft_dtype -> /dtype",
		"/soft_empty_grp -> /group_empty",
		"/soft_group1 -> /group1",
	}, softs)

	f2 := grpOpen(t, "tools/test/h5dump/testfiles/tslink.h5")
	lnks, err := grpCollectLinks(f2.Root().Links())
	require.NoError(t, err)
	assert.Equal(t, []h5.Link{
		{Name: "slink1", Type: h5.LinkSoft, SoftPath: "somevalue"},
		{Name: "slink2", Type: h5.LinkSoft, SoftPath: "linkvalue"},
	}, lnks)
}

// TestGroupSoftLinkLimit checks that soft link cycles end with "too many
// links" after H5L_NUM_LINKS links (testdata/bt2_corder.h5's dangling
// links, rewritten in memory to point at each other is not possible
// without a writer, so a cycle through the traversal context is
// simulated).
func TestGroupSoftLinkLimit(t *testing.T) {
	f, err := Open("testdata/bt2_corder.h5")
	require.NoError(t, err)
	defer f.Close()
	g, err := f.OpenGroup("/links")
	require.NoError(t, err)

	// With no links left to traverse, following a soft link fails.
	ctx := &gTravCtx{nlinks: 0}
	_, err = gLocFind(ctx, g.loc(), "l0001")
	assert.True(t, errors.Is(err, &Error{Minor: H5E_NLINKS}), "%v", err)

	// The count is restored after each traversal.
	ctx.nlinks = 1
	_, err = gLocFind(ctx, g.loc(), "l0001")
	assert.True(t, IsNotFound(err), "%v", err)
	assert.Equal(t, 1, ctx.nlinks)
}

// TestGroupExternalLinks follows external links (textlinksrc.h5 ->
// textlinktar.h5, tsoftlinks.h5), compare tools/test/h5ls/expected/
// textlinksrc-1.ls.
func TestGroupExternalLinks(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/textlinksrc.h5")

	l, err := f.Root().Link("ext_link1")
	require.NoError(t, err)
	assert.Equal(t, h5.LinkExternal, l.Type)
	assert.Equal(t, "textlinktar.h5", l.ExternalFile)
	assert.Equal(t, "group", l.ExternalPath) // h5ls: {textlinktar.h5//group}
	assert.Equal(t, append([]byte{0}, "textlinktar.h5\x00group\x00"...), l.UserData)

	obj, err := f.Open("/ext_link1")
	require.NoError(t, err)
	g, ok := obj.(*Group)
	require.True(t, ok, "%T", obj)
	assert.NotSame(t, f, g.f)
	assert.Equal(t, "textlinktar.h5", filepath.Base(g.f.Name()))
	assert.Equal(t, "/group", g.Path())
	info, err := g.Info()
	require.NoError(t, err)
	assert.EqualValues(t, 800, info.Address)
	assert.EqualValues(t, 2, info.RefCount)

	ds, err := f.OpenDataset("/ext_link1/dset")
	require.NoError(t, err)
	assert.Equal(t, g.f, ds.f)
	info, err = ds.Info()
	require.NoError(t, err)
	assert.EqualValues(t, 2536, info.Address) // h5ls -v textlinktar.h5/group

	dt, err := f.OpenDatatype("/ext_link3")
	require.NoError(t, err)
	assert.Equal(t, h5.ClassInteger, dt.Datatype().Class)

	// An external link to a soft link in a third file.
	ds, err = f.OpenDataset("ext2soft_link1")
	require.NoError(t, err)
	assert.Equal(t, "tsoftlinks.h5", filepath.Base(ds.f.Name()))
	// The external file's own dangling soft link.
	_, err = f.Open("ext2softdangle_link1")
	assert.True(t, IsNotFound(err), "%v", err)

	// The target files are opened once and closed with the file.
	f.mu.Lock()
	nExt := len(f.extFiles)
	exts := make([]*File, 0, nExt)
	for _, ef := range f.extFiles {
		exts = append(exts, ef)
	}
	f.mu.Unlock()
	assert.Equal(t, 2, nExt)

	// ext_link4 -> textlinktar.h5//group/elink_t2 -> textlinksrc.h5//ext_link4:
	// an external link cycle, cut by the link traversal limit.
	_, err = f.Open("/ext_link4")
	assert.True(t, errors.Is(err, &Error{Minor: H5E_NLINKS}), "%v", err)

	require.NoError(t, f.Close())
	for _, ef := range exts {
		assert.True(t, ef.closed)
	}

	// With external links disabled, the links are still listed but cannot
	// be followed.
	f2 := grpOpen(t, "tools/test/h5dump/testfiles/textlinksrc.h5", WithExternalLinks(false))
	_, err = f2.Open("/ext_link1")
	assert.ErrorContains(t, err, "following external links is disabled")
	_, err = f2.OpenDataset("/ext_link1/dset")
	assert.Error(t, err)
	l2, err := f2.Root().Link("ext_link1")
	require.NoError(t, err)
	assert.Equal(t, l, l2)
	ok, err = gLocExists(f2.rootLoc(), "ext_link1")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, f2.extFiles)
	assert.Equal(t, []string{
		"/ext2soft_link1", "/ext2softdangle_link1", "/ext_link1", "/ext_link2",
		"/ext_link3", "/ext_link4", "/ext_link5",
	}, grpWalkPaths(t, f2))

	// The external link prefix locates target files elsewhere.
	dir := t.TempDir()
	src, err := os.ReadFile(grpTestFile(t, "tools/test/h5dump/testfiles/textlinksrc.h5"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "textlinksrc.h5"), src, 0o644))
	f3 := grpOpen(t, filepath.Join(dir, "textlinksrc.h5"), WithExternalLinkPrefix(filepath.Join(repoRoot(t), "tools/test/h5dump/testfiles")))
	_, err = f3.OpenDataset("/ext_link2")
	require.NoError(t, err)
	f4, err := Open(filepath.Join(dir, "textlinksrc.h5"), WithExternalLinkPrefix(dir))
	require.NoError(t, err)
	defer f4.Close()
	if os.Getenv("HDF5_EXT_PREFIX") == "" {
		_, err = f4.OpenDataset("/ext_link2")
		assert.ErrorContains(t, err, "unable to open external file")
	}
}

// TestGroupUserDefinedLinks checks user-defined links of an unregistered
// class (tudlink.h5).
func TestGroupUserDefinedLinks(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tudlink.h5")
	var visits []Visit
	for v, err := range f.Root().Walk() {
		require.NoError(t, err)
		visits = append(visits, v)
	}
	require.Len(t, visits, 2)
	for i, v := range visits {
		assert.Equal(t, fmt.Sprintf("/udlink%d", i+1), v.Path)
		assert.Equal(t, h5.LinkType(187), v.Link.Type)
		assert.Nil(t, v.Object)
		_, err := f.Open(v.Path)
		assert.ErrorContains(t, err, "unable to get UD link class")
		val, err := f.lGetVal(f.rootLoc(), v.Path)
		require.NoError(t, err)
		assert.Nil(t, val)
	}
}

// TestGroupHardLinkCycles checks that Walk cuts hard-link cycles like
// h5ls -r (tools/test/h5ls/expected/tloop-1.ls, h5repack_hlink.h5).
func TestGroupHardLinkCycles(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tloop.h5")
	var visits []Visit
	for v, err := range f.Root().Walk() {
		require.NoError(t, err)
		visits = append(visits, v)
	}
	require.Len(t, visits, 4)
	assert.Equal(t, []string{"/g1", "/g1/g1.1", "/g1/g1.1/g2.1", "/g2"}, []string{visits[0].Path, visits[1].Path, visits[2].Path, visits[3].Path})
	// /g1/g1.1/g2.1 is /g1, /g2 is /g1/g1.1: reported, with the object.
	assert.Equal(t, visits[0].Link.Address, visits[2].Link.Address)
	assert.Equal(t, visits[1].Link.Address, visits[3].Link.Address)
	g, ok := visits[2].Object.(*Group)
	require.True(t, ok)
	assert.Equal(t, "/g1/g1.1/g2.1", g.Path())

	// Deep paths through the cycle resolve.
	obj, err := f.Open("/g1/g1.1/g2.1/g1.1/g2.1/g1.1")
	require.NoError(t, err)
	assert.Equal(t, visits[1].Link.Address, obj.(*Group).addr)

	// Walking a subgroup.
	sub, err := f.OpenGroup("/g2")
	require.NoError(t, err)
	var paths []string
	for v, err := range sub.Walk() {
		require.NoError(t, err)
		paths = append(paths, v.Path)
	}
	assert.Equal(t, []string{"/g2/g2.1", "/g2/g2.1/g1.1"}, paths)

	// Stopping a walk inside a nested group.
	paths = nil
	for v, err := range f.Root().Walk() {
		require.NoError(t, err)
		paths = append(paths, v.Path)
		if v.Path == "/g1/g1.1" {
			break
		}
	}
	assert.Equal(t, []string{"/g1", "/g1/g1.1"}, paths)

	f2 := grpOpen(t, "tools/test/h5repack/testfiles/h5repack_hlink.h5")
	paths = grpWalkPaths(t, f2)
	assert.Contains(t, paths, "/g1/g2/link1 to g1")
	assert.Contains(t, paths, "/g1/g2/g3/link1 to g2")
}

// TestGroupOpenAddress opens objects by address.
func TestGroupOpenAddress(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tall.h5")
	obj, err := f.OpenAddress(5480)
	require.NoError(t, err)
	ds, ok := obj.(*Dataset)
	require.True(t, ok, "%T", obj)
	assert.Equal(t, "", ds.Path())
	info, err := ds.Info()
	require.NoError(t, err)
	assert.EqualValues(t, 5480, info.Address)

	obj, err = f.OpenAddress(f.Superblock().RootGroupAddress)
	require.NoError(t, err)
	g, ok := obj.(*Group)
	require.True(t, ok, "%T", obj)
	assert.Equal(t, "", g.Path())
	lnks, err := grpCollectLinks(g.Links())
	require.NoError(t, err)
	assert.Equal(t, []string{"g1", "g2"}, grpLinkNames(lnks))
	var paths []string
	for v, err := range g.Walk() {
		require.NoError(t, err)
		paths = append(paths, v.Path)
	}
	assert.Contains(t, paths, "g1/g1.1/dset1.1.1")
	sub, err := g.OpenGroup("g1")
	require.NoError(t, err)
	assert.Equal(t, "", sub.Path())

	_, err = f.OpenAddress(1)
	assert.Error(t, err)
	_, err = f.OpenAddress(1 << 40)
	assert.Error(t, err)
}

// TestGroupComments reads object comments (tgrp_comments.h5, compare
// tools/test/h5ls/expected/tgrp_comments.ls).
func TestGroupComments(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tgrp_comments.h5")
	g, err := f.OpenGroup("/glongcomment")
	require.NoError(t, err)
	c, err := g.Comment()
	require.NoError(t, err)
	assert.Equal(t, "Comment for group /glongcomment with a really, really, really long, long, long comment", c)
	info, err := g.Info()
	require.NoError(t, err)
	assert.EqualValues(t, 12424, info.Address)
	assert.EqualValues(t, 1, info.RefCount)
	c2, err := gLocGetComment(f.rootLoc(), "glongcomment")
	require.NoError(t, err)
	assert.Equal(t, c, c2)

	obj, err := f.Open("/g2/g2.1/g2.1.1")
	require.NoError(t, err)
	c, err = obj.Comment()
	require.NoError(t, err)
	assert.Equal(t, "Comment for group /g2/g2.1/g2.1.1", c)
	c, err = f.Root().Comment()
	require.NoError(t, err)
	assert.Equal(t, "", c)

	f2 := grpOpen(t, "tools/test/h5dump/testfiles/tall.h5")
	c, err = f2.Root().Comment()
	require.NoError(t, err)
	assert.Equal(t, "", c)
}

// TestGroupAttributeStubs checks that the attribute methods report the
// attribute module as not translated (until it replaces h5a.go).
func TestGroupAttributeStubs(t *testing.T) {
	f := grpOpen(t, "tools/test/h5dump/testfiles/tall.h5")
	ds, err := f.OpenDataset("/g1/g1.1/dset1.1.1")
	require.NoError(t, err)
	if _, err := f.aCount(ds.oh); err == nil {
		t.Skip("attribute module translated")
	}
	_, err = ds.NumAttributes()
	assert.ErrorIs(t, err, ErrUnsupported)
	_, err = ds.Attribute("attr1")
	assert.ErrorIs(t, err, ErrUnsupported)
	for _, err := range ds.Attributes() {
		assert.ErrorIs(t, err, ErrUnsupported)
	}
}

// TestGroupElinkValue checks H5Lunpack_elink_val.
func TestGroupElinkValue(t *testing.T) {
	flags, file, path, err := h5lUnpackElinkVal([]byte("\x00file.h5\x00/a/b\x00"))
	require.NoError(t, err)
	assert.Equal(t, uint8(0), flags)
	assert.Equal(t, "file.h5", file)
	assert.Equal(t, "/a/b", path)
	for _, bad := range []string{"", "\x00", "\x00f", "\x10f\x00p\x00", "\x02f\x00p\x00", "\x00f\x00p", "\x00file\x00"} {
		_, _, _, err := h5lUnpackElinkVal([]byte(bad))
		assert.Error(t, err, "%q", bad)
	}
	_, err = h5lExternQuery("x", []byte("\x10f\x00p\x00"))
	assert.Error(t, err)
}
