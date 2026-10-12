//go:build cgo

package conformance

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/crhntr/hdf5/go"
)

// repoRoot returns the root of the HDF5 repository checkout this module
// lives in (found by src/H5public.h).
func repoRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := wd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "src", "H5public.h")); err == nil {
			return d
		}
	}
	t.Skip("not inside an HDF5 repository checkout")
	return ""
}

// corpusFiles returns every *.h5 / *.hdf5 test file in the repository
// outside the Go module directories (go and cgo).
func corpusFiles(t testing.TB) []string {
	t.Helper()
	root := repoRoot(t)
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || isModuleDir(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".h5") || strings.HasSuffix(path, ".hdf5") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// isModuleDir reports whether path is one of the top-level Go module
// directories of the repository rooted at root (go and cgo); their test
// files are not part of the repository corpus.
func isModuleDir(root, path string) bool {
	if filepath.Dir(path) != root {
		return false
	}
	name := filepath.Base(path)
	return name == "go" || name == "cgo"
}

var dirLocks sync.Map // directory -> *sync.Mutex

// lockDir serializes the comparisons of the files of one directory and
// returns the unlock function. libhdf5 shares open files process wide: a
// file held open as the target of an external link or as a virtual
// dataset source (with the default file close degree) cannot be opened
// with chdf5's H5F_CLOSE_STRONG at the same time ("file close degree
// doesn't match"), and such files live in the same directory.
func lockDir(dir string) func() {
	m, _ := dirLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// TestCorpus compares every HDF5 file of the repository read with hdf5 and
// with chdf5 (libhdf5). Run with -v for the per-file summaries and the
// final table.
//
// The files are compared in parallel from the package directory: both
// packages look for relative external data files (EFL) in the current
// directory only, so the datasets stored in external files fail to read in
// both here; TestExternalFilesCurrentDirectory compares them from the
// directory of each file. (HDF5_EXTFILE_PREFIX cannot be used instead:
// libhdf5 reads it once, when the library is initialized, and the
// WithExternalFilePrefix access property breaks libhdf5's internal opens of
// the same dataset with the default property list, e.g. H5Oopen_by_token.)
func TestCorpus(t *testing.T) {
	root, files := filesOf(t)
	all := &totals{s: newStats()}
	t.Run("files", func(t *testing.T) {
		for _, file := range files {
			rel, _ := filepath.Rel(root, file)
			t.Run(rel, func(t *testing.T) {
				t.Parallel()
				defer lockDir(filepath.Dir(file))()
				all.add(compareFile(t, file, rel, fileOptions{}))
			})
		}
	})
	t.Log(all.s.table())
}

// TestExternalFilesCurrentDirectory compares the files with datasets
// stored in external data files (EFL) from the directory of each file:
// without a prefix both packages look for relative external file names in
// the current directory only (TestCorpus covers the other side).
func TestExternalFilesCurrentDirectory(t *testing.T) {
	root, all := filesOf(t)
	var files []string
	for _, f := range all {
		if hasExternalData(f) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Skip("no external data files in the corpus")
	}
	t.Logf("%d files with external data files", len(files))
	tot := &totals{s: newStats()}
	for _, file := range files {
		rel, _ := filepath.Rel(root, file)
		t.Run(rel, func(t *testing.T) {
			t.Chdir(filepath.Dir(file))
			tot.add(compareFile(t, file, rel, fileOptions{}))
		})
	}
	t.Log(tot.s.table())
}

// hasExternalData reports whether a dataset of the file stores its data in
// external files.
func hasExternalData(file string) bool {
	f, err := hdf5.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	for v, err := range f.Root().Walk() {
		if err != nil {
			return false
		}
		if d, ok := v.Object.(*hdf5.Dataset); ok {
			if l, err := d.Layout(); err == nil && len(l.External) > 0 {
				return true
			}
		}
	}
	return false
}
