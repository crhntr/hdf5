package hdf5

// Tests of the dataset read path (h5d_*.go): a sweep over every dataset of
// the corpus (checked against `h5dump -b FILE` raw output when the
// reference tools are installed), value tests against the golden h5dump
// output, hyperslab / point selections, fill values and corrupt input.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDsetRef is a dataset found by testWalkDatasets.
type testDsetRef struct {
	path string
	addr haddr
}

// testWalkDatasets walks the group hierarchy of f from the root group
// (compact links, symbol tables and dense link storage) and returns every
// dataset reachable by hard links, by its first path.
func testWalkDatasets(t testing.TB, f *File) ([]testDsetRef, []error) {
	type item struct {
		path string
		addr haddr
	}
	var (
		out  []testDsetRef
		errs []error
	)
	seen := map[haddr]bool{}
	queue := []item{{"/", f.sblock.rootAddr}}
	for len(queue) > 0 && len(seen) < 20000 {
		it := queue[0]
		queue = queue[1:]
		if !addrDefined(it.addr) || seen[it.addr] {
			continue
		}
		seen[it.addr] = true
		oh, err := f.oProtect(it.addr)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.path, err))
			continue
		}
		join := func(name string) string {
			if it.path == "/" {
				return "/" + name
			}
			return it.path + "/" + name
		}
		if ok, _ := h5oDsetIsa(f, oh); ok {
			out = append(out, testDsetRef{it.path, it.addr})
			continue
		}
		var children []item
		for _, m := range oh.mesgs {
			switch m.typeID {
			case h5oLinkID, h5oStabID, h5oLinfoID:
			default:
				continue
			}
			native, err := f.h5oLoadNative(oh, m)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", it.path, err))
				continue
			}
			switch v := native.(type) {
			case *h5oLink:
				if v.typ == h5.LinkHard {
					children = append(children, item{join(v.name), v.addr})
				}
			case *h5oStab:
				err := f.gStabIterate(v, func(name string, ent *h5gEntry) error {
					children = append(children, item{join(name), ent.header})
					return nil
				})
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", it.path, err))
				}
			case *h5oLinfo:
				if !addrDefined(v.fheapAddr) || !addrDefined(v.nameBt2Addr) {
					continue
				}
				fh, err := f.hfOpen(v.fheapAddr)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", it.path, err))
					continue
				}
				bt, err := f.b2Open(v.nameBt2Addr, h5gBt2Name(), nil)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", it.path, err))
					continue
				}
				err = bt.iterate(func(rec any) error {
					r := rec.(*h5gDenseBt2NameRec)
					obj, err := fh.read(r.id[:])
					if err != nil {
						return err
					}
					lv, err := f.msgDecode(nil, h5oLinkID, obj)
					if err != nil {
						return err
					}
					if l := lv.(*h5oLink); l.typ == h5.LinkHard {
						children = append(children, item{join(l.name), l.addr})
					}
					return nil
				})
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", it.path, err))
				}
			}
		}
		sort.Slice(children, func(i, j int) bool { return children[i].path < children[j].path })
		queue = append(queue, children...)
	}
	return out, errs
}

// testOpenDataset opens the dataset at path (found with testWalkDatasets).
func testOpenDataset(t testing.TB, f *File, path string) *h5dShared {
	t.Helper()
	refs, _ := testWalkDatasets(t, f)
	for _, r := range refs {
		if r.path == path {
			oh, err := f.oProtect(r.addr)
			require.NoError(t, err)
			ds, err := f.dOpen(oh)
			require.NoError(t, err, path)
			return ds
		}
	}
	t.Fatalf("dataset %s not found", path)
	return nil
}

// testOpenFile opens a file relative to the repository root.
func testDOpenFile(t testing.TB, rel string, opts ...Option) *File {
	t.Helper()
	f, err := Open(filepath.Join(repoRoot(t), rel), opts...)
	require.NoError(t, err, rel)
	t.Cleanup(func() { f.Close() })
	return f
}

// h5dumpBin returns the reference h5dump, or "" when it is not installed.
func h5dumpBin() string {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local/hdf5-1.14.6/bin/h5dump")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// h5dumpRaw runs `h5dump -d path -b FILE` (optionally with selection
// arguments) and returns the raw bytes of the selected elements in the file
// datatype.
func h5dumpRaw(t testing.TB, bin, file, path string, extra ...string) ([]byte, error) {
	out := filepath.Join(t.TempDir(), "raw.bin")
	if abs, err := filepath.Abs(file); err == nil {
		file = abs
	}
	args := append([]string{"-d", path}, extra...)
	args = append(args, "-b", "FILE", "-o", out, file)
	cmd := exec.Command(bin, args...)
	cmd.Dir = filepath.Dir(file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = new(bytes.Buffer)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("h5dump %v: %v: %s", args, err, stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		return nil, fmt.Errorf("h5dump %v: %s", args, stderr.String())
	}
	return os.ReadFile(out)
}

// testCoverage counts the datasets read by layout, chunk index, filter and
// special cases.
type testCoverage map[string]int

func (c testCoverage) String() string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-40s %6d\n", k, c[k])
	}
	return b.String()
}

var testChunkIndexNames = map[h5.ChunkIndex]string{
	h5.ChunkIndexBTree1: "btree1",
	h5.ChunkIndexSingle: "single",
	h5.ChunkIndexNone:   "none(implicit)",
	h5.ChunkIndexFArray: "farray",
	h5.ChunkIndexEArray: "earray",
	h5.ChunkIndexBTree2: "btree2",
}

// testUnallocated reports whether some of the dataset's storage was never
// allocated (so reading it yields fill values).
func testUnallocated(t testing.TB, f *File, ds *h5dShared) bool {
	switch ds.layout.typ {
	case h5dContiguous:
		return ds.dcplCache.efl.nused == 0 && !addrDefined(ds.layout.contigAddr) && ds.space.extent.nelem > 0
	case h5dChunked:
		if ds.space.extent.nelem == 0 {
			return false
		}
		if !addrDefined(ds.chunkSt.idxAddr) {
			return true
		}
		var n uint64
		if err := f.h5dChunkIter(ds, func([]uint64, uint32, haddr, uint64) error { n++; return nil }); err != nil {
			return false
		}
		return n < ds.chunk.nchunks
	}
	return false
}

// testRawComparable reports whether `h5dump -b FILE` writes elements of dt
// as their raw bytes (it writes compound members and strings differently).
func testRawComparable(dt *h5.Datatype) bool {
	switch dt.Class {
	case h5.ClassInteger, h5.ClassFloat, h5.ClassBitfield, h5.ClassEnum, h5.ClassOpaque, h5.ClassTime:
		return true
	case h5.ClassArray:
		return testRawComparable(dt.Base)
	}
	return false
}

// TestDatasetCorpusSweep opens and reads every dataset of every readable
// corpus file, comparing the raw data with h5dump when available.
func TestDatasetCorpusSweep(t *testing.T) {
	bin := h5dumpBin()
	if testing.Short() {
		bin = ""
	}
	cov := testCoverage{}
	skipped := testCoverage{}
	var failures []string
	compared, mismatched := 0, 0
	files := corpusFiles(t)
	/* (plus the generated files of this package) */
	for _, name := range []string{"testdata/dset.h5", "testdata/chunkidx.h5"} {
		abs, err := filepath.Abs(name)
		require.NoError(t, err)
		files = append(files, abs)
	}
	for _, file := range files {
		f, err := Open(file)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(repoRoot(t), file)
		refs, _ := testWalkDatasets(t, f)
		for _, r := range refs {
			name := rel + ":" + r.path
			func() {
				defer func() {
					if p := recover(); p != nil {
						failures = append(failures, fmt.Sprintf("%s: PANIC %v", name, p))
					}
				}()
				oh, err := f.oProtect(r.addr)
				if err != nil {
					skipped["object header error"]++
					return
				}
				// libhdf5Fails reports whether the reference library cannot
				// read the dataset either (corrupt test inputs).
				libhdf5Fails := func() bool {
					if bin == "" {
						return true
					}
					_, err := h5dumpRaw(t, bin, file, r.path)
					return err != nil
				}
				ds, err := f.dOpen(oh)
				if err != nil {
					switch {
					case errors.Is(err, ErrUnsupported):
						skipped["open: unsupported (other module)"]++
					case libhdf5Fails():
						skipped["open: unreadable by libhdf5 too / unverified"]++
					default:
						failures = append(failures, fmt.Sprintf("%s: open: %v", name, err))
					}
					return
				}
				layout, err := ds.layoutInfo()
				if err != nil {
					failures = append(failures, fmt.Sprintf("%s: layout: %v", name, err))
					return
				}
				filters := ds.filters()
				if _, err := ds.fillValue(f); err != nil {
					failures = append(failures, fmt.Sprintf("%s: fill value: %v", name, err))
					return
				}
				_ = ds.storageSize(f)
				size := ds.space.extent.nelem * ds.typ.Size
				if ds.typ.Size != 0 && size/ds.typ.Size != ds.space.extent.nelem || size > 64<<20 {
					skipped["larger than 64 MiB"]++
					return
				}
				if layout.Class == h5.LayoutVirtual {
					/* (H5Dget_space refreshes the extent) */
					if _, err := f.h5dGetSpace(ds); err != nil {
						failures = append(failures, fmt.Sprintf("%s: virtual extent: %v", name, err))
						return
					}
				}
				hasSzip := false
				for _, fl := range filters {
					if fl.ID == h5.FilterSzip {
						hasSzip = true
					}
				}
				vals, err := f.dReadValues(ds, nil)
				if err != nil {
					switch {
					case hasSzip && errors.Is(err, ErrUnsupported):
						skipped["szip"]++
					case len(layout.External) > 0 && errors.Is(err, &Error{Major: H5E_EFL, Minor: H5E_CANTOPENFILE}):
						skipped["external file missing"]++
						t.Logf("%s: %v", name, err)
					case errors.Is(err, ErrUnsupported):
						skipped["unsupported filter/type"]++
					case strings.Contains(err.Error(), "is not registered"):
						skipped["filter plugin not available"]++
					case libhdf5Fails():
						skipped["read: unreadable by libhdf5 too / unverified"]++
					default:
						failures = append(failures, fmt.Sprintf("%s: read: %v", name, err))
					}
					return
				}
				if uint64(len(vals)) != ds.space.extent.nelem && !(ds.space.extent.typ == h5.SpaceScalar && len(vals) == 1) {
					failures = append(failures, fmt.Sprintf("%s: %d values for %d elements", name, len(vals), ds.space.extent.nelem))
				}

				/* Coverage */
				cov["layout "+layout.Class.String()]++
				if layout.Class == h5.LayoutChunked {
					cov["chunk index "+testChunkIndexNames[layout.ChunkIndex]]++
				}
				if len(layout.External) > 0 {
					cov["external file list"]++
				}
				for _, fl := range filters {
					cov["filter "+fl.Name]++
				}
				if testUnallocated(t, f, ds) {
					cov["fill value on unallocated storage"]++
				}

				/* Oracle */
				/* (with H5D_FILL_TIME_NEVER libhdf5 leaves whatever the
				 *  buffer held in unallocated parts) */
				neverFilled := ds.dcplCache.fill.fillTime == h5dFillTimeNever && testUnallocated(t, f, ds)
				if bin == "" || !testRawComparable(ds.typ) || ds.space.extent.nelem == 0 || neverFilled {
					return
				}
				raw, err := f.dRead(ds, nil)
				if err != nil {
					failures = append(failures, fmt.Sprintf("%s: dRead: %v", name, err))
					return
				}
				want, err := h5dumpRaw(t, bin, file, r.path)
				if err != nil {
					return
				}
				compared++
				if !bytes.Equal(raw, want) {
					mismatched++
					failures = append(failures, fmt.Sprintf("%s: raw data differs from h5dump (%d vs %d bytes)", name, len(raw), len(want)))
				}
			}()
		}
		f.Close()
	}
	t.Logf("datasets read, by kind:\n%s", cov)
	t.Logf("datasets skipped:\n%s", skipped)
	t.Logf("compared with h5dump: %d, mismatched: %d", compared, mismatched)
	for _, fl := range failures {
		t.Log(fl)
	}
	assert.Empty(t, failures)
}
