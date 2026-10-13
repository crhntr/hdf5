package golden

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Files returns the files the conformance suite compares and the golden
// file records, absolute and sorted: every *.h5 / *.hdf5 file of the
// repository rooted at root outside the Go module directories (go and
// cgo), plus go/testdata/*.h5, cgo/testdata/*.h5 and the committed
// fixtures go/testdata/fixtures/*/*.h5.
func Files(root string) []string {
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
	for _, pattern := range []string{"go/testdata/*.h5", "cgo/testdata/*.h5", "go/testdata/fixtures/*/*.h5"} {
		extra, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		files = append(files, extra...)
	}
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

// RepoRoot returns the root of the HDF5 repository checkout containing dir
// (found by src/H5public.h), or "" when there is none.
func RepoRoot(dir string) string {
	for d := dir; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "src", "H5public.h")); err == nil {
			return d
		}
	}
	return ""
}
