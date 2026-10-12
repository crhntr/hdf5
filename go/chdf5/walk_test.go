//go:build cgo

package chdf5

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HDFGroup/hdf5/go/h5"
)

// maxReadBytes bounds the size of the datasets the corpus test reads.
const maxReadBytes = 64 << 20

// h5dumpPath returns the h5dump of the installed libhdf5, "" if absent.
func h5dumpPath() string {
	if p := os.Getenv("H5DUMP"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "hdf5-1.14.6", "bin", "h5dump")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if p, err := exec.LookPath("h5dump"); err == nil {
		return p
	}
	return ""
}

// h5dumpCanOpen reports whether `h5dump -H` reads the file's header
// without error (the corpus contains deliberately broken files).
func h5dumpCanOpen(t testing.TB, h5dump, file string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h5dump, "-H", file)
	cmd.Dir = filepath.Dir(file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return !strings.Contains(string(out), "h5dump error")
}

// readAll walks every object of f and reads every attribute and dataset
// (within maxReadBytes), returning the number of datasets read.
func readAll(t *testing.T, f *File) (datasets, attrs int) {
	t.Helper()
	readAttrs := func(o Object) {
		_, err := o.Info()
		assert.NoError(t, err, "Info %s", o.Path())
		_, err = o.Comment()
		assert.NoError(t, err, "Comment %s", o.Path())
		for a, err := range o.Attributes() {
			if !assert.NoError(t, err, "attributes of %s", o.Path()) {
				return
			}
			_, err := a.ReadValues()
			assert.NoError(t, err, "attribute %s@%s", o.Path(), a.Name())
			a.Close()
			attrs++
		}
	}
	readAttrs(f.Root())
	for v, err := range f.Root().Walk() {
		if !assert.NoError(t, err, "walk at %s", v.Path) {
			continue
		}
		if v.Object == nil {
			continue
		}
		readAttrs(v.Object)
		if d, ok := v.Object.(*Dataset); ok {
			_, err := d.Layout()
			assert.NoError(t, err, "Layout %s", v.Path)
			filters, err := d.Filters()
			assert.NoError(t, err, "Filters %s", v.Path)
			_, err = d.FillValue()
			assert.NoError(t, err, "FillValue %s", v.Path)
			skip := d.Dataspace().NumElems()*d.Datatype().Size > maxReadBytes
			if l, err := d.Layout(); err == nil {
				for _, x := range l.External {
					if _, err := os.Stat(x.Name); err != nil {
						skip = true // external data file missing from the repository
					}
				}
			}
			for _, fl := range filters {
				if fl.ID == h5.FilterSzip || !FilterAvailable(fl.ID) {
					skip = true
				}
			}
			if !skip {
				_, err := d.ReadValues()
				assert.NoError(t, err, "ReadValues %s", v.Path)
				datasets++
			}
		}
		v.Object.Close()
	}
	return datasets, attrs
}

// TestCorpus opens every file of the repository that h5dump can open,
// walks it and reads every attribute and dataset.
func TestCorpus(t *testing.T) {
	h5dump := h5dumpPath()
	files := corpusFiles(t)
	var opened, skipped int
	for _, file := range files {
		rel, _ := filepath.Rel(repoRoot(t), file)
		if h5dump != "" && !h5dumpCanOpen(t, h5dump, file) {
			skipped++
			continue
		}
		t.Run(rel, func(t *testing.T) {
			// External data files, external links and external references
			// are resolved relative to the current directory.
			t.Chdir(filepath.Dir(file))
			f, err := Open(file)
			if h5dump == "" && err != nil {
				t.Skipf("cannot open: %v", err)
			}
			require.NoError(t, err)
			defer f.Close()
			sb := f.Superblock()
			assert.NotZero(t, sb.SizeOfOffsets)
			readAll(t, f)
			opened++
		})
	}
	t.Logf("%d corpus files, %d opened and read, %d not readable by h5dump", len(files), opened, skipped)
}
