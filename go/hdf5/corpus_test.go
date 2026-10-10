package hdf5

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repoRoot returns the root of the HDFGroup/hdf5 checkout this module lives in.
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

// corpusFiles returns every *.h5 / *.hdf5 test file in the repository.
func corpusFiles(t testing.TB) []string {
	t.Helper()
	root := repoRoot(t)
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "go" {
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
