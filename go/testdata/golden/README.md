# Golden fingerprints of the corpus

`corpus.txt.gz` records what **libhdf5 1.14.6** reads from every HDF5 file
the conformance suite compares (the `.h5`/`.hdf5` files of the repository
outside `go/` and `cgo/`, `go/testdata/*.h5` and the committed fixtures in
`go/testdata/fixtures/`), as fingerprints: the first 8 bytes of the SHA-256
of the canonical encoding of each value (`h5.Fingerprint`,
`go/h5/fingerprint.go`). It was produced through the cgo package
(`github.com/crhntr/hdf5/cgo`) by `cgo/cmd/hdf5-golden`; the format is
documented in `cgo/internal/golden/golden.go`.

`TestGolden` (`go/golden_test.go`) reads the same files with the pure Go
package, records them the same way and compares, entry by entry. It needs
neither cgo nor libhdf5, so coverage and mutation testing of the pure Go
module run against the libhdf5 oracle.

## Regenerating

    cd cgo && make golden      # this file
    cd cgo && make fixtures    # go/testdata/fixtures, then this file

Regenerate whenever the corpus changes (files added, removed or modified),
the fixture writer (`cgo/internal/fixtures`) changes, the canonical
encoding (`go/h5/fingerprint.go`) or the shared types in `go/h5` change, or
what the conformance suite compares changes. `make test` in `cgo` fails
while the committed file is stale (`TestGoldenUpToDate`,
`TestFixturesUpToDate`). Generation is deterministic: the same libhdf5 and
corpus give the same bytes.
