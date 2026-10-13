//go:build cgo

package conformance

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chdf5 "github.com/crhntr/hdf5/cgo"
	"github.com/crhntr/hdf5/cgo/internal/fixtures"
	"github.com/crhntr/hdf5/cgo/internal/golden"
)

// TestGoldenUpToDate checks that the golden file of the pure Go module's
// tests (go/testdata/golden/corpus.txt.gz, read by go/golden_test.go)
// matches what libhdf5 reads from the corpus now. It fails when the
// corpus, the committed fixtures or the canonical encoding
// (go/h5/fingerprint.go) changed without regenerating the file with
// `make golden`.
func TestGoldenUpToDate(t *testing.T) {
	root := repoRoot(t)
	committed, err := os.ReadFile(filepath.Join(root, "go", "testdata", "golden", "corpus.txt.gz"))
	require.NoError(t, err, "missing golden file: run make golden")
	zr, err := gzip.NewReader(bytes.NewReader(committed))
	require.NoError(t, err)
	want, err := io.ReadAll(zr)
	require.NoError(t, err)

	var got bytes.Buffer
	require.NoError(t, golden.Generate(&got, root))
	if bytes.Equal(want, got.Bytes()) {
		return
	}
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(got.String(), "\n")
	file := ""
	for i := range max(len(wl), len(gl)) {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if strings.HasPrefix(g, "F\t") {
			file = g
		}
		if w != g {
			t.Fatalf("the golden file is stale (run make golden); first difference at line %d (block %s)\ncommitted: %s\nlibhdf5:   %s",
				i+1, file, w, g)
		}
	}
}

// TestFixturesUpToDate checks that the committed fixtures
// (go/testdata/fixtures, part of the golden file's corpus) are what
// package fixtures writes (run make fixtures after changing it).
func TestFixturesUpToDate(t *testing.T) {
	root := repoRoot(t)
	for _, ff := range fixtures.Formats {
		t.Run(ff.Name, func(t *testing.T) {
			if !ff.Reproducible() {
				_, err := os.Stat(filepath.Join(root, "go", "testdata", "fixtures", ff.Name))
				assert.True(t, os.IsNotExist(err), "fixtures of format %s are not reproducible and must not be committed", ff.Name)
				return
			}
			dir := t.TempDir()
			require.NoError(t, fixtures.Write(dir, ff, chdf5.WithoutTimes()))
			for _, name := range fixtures.Files {
				want, err := os.ReadFile(filepath.Join(root, "go", "testdata", "fixtures", ff.Name, name))
				require.NoError(t, err, "missing fixture: run make fixtures")
				got, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err)
				assert.True(t, bytes.Equal(want, got), "%s/%s is stale: run make fixtures", ff.Name, name)
			}
		})
	}
}
