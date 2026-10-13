//go:build cgo

package conformance

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/cgo/internal/fixtures"
)

// TestFixtures writes the files of package fixtures with chdf5 (with
// object times, unlike the committed copies in go/testdata/fixtures that
// TestCorpus compares) and compares their reads.
func TestFixtures(t *testing.T) {
	all := &totals{s: newStats()}
	for _, ff := range fixtures.Formats {
		t.Run(ff.Name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, fixtures.Write(dir, ff))
			for _, name := range fixtures.Files {
				f := filepath.Join(dir, name)
				t.Run(name, func(t *testing.T) {
					all.add(compareFile(t, f, "fixtures/"+ff.Name+"/"+name, fileOptions{}))
				})
			}
		})
	}
	t.Log(all.s.table())
}
