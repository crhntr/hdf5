# HDF5 for Go, cgo bindings

`github.com/crhntr/hdf5/cgo` is `package hdf5` built on the official HDF5 C
library (libhdf5 1.14.x). It exposes the same reading API as the pure Go
reader [`github.com/crhntr/hdf5/go`](../go), with the types of
`github.com/crhntr/hdf5/go/h5` as the shared vocabulary, so a program
switches implementations by changing only the import path:

```go
import "github.com/crhntr/hdf5/cgo" // instead of "github.com/crhntr/hdf5/go"

f, err := hdf5.Open("data.h5")
```

Where both are imported, name this one `chdf5`:

```go
import (
    "github.com/crhntr/hdf5/go"
    chdf5 "github.com/crhntr/hdf5/cgo"
)
```

`conformance` (test-only) reads every HDF5 file of the repository with
both packages and compares the results.

## Building

libhdf5 is found with pkg-config (`hdf5.pc`):

    PKG_CONFIG_PATH=/path/to/hdf5/lib/pkgconfig go test ./...

or `make test` / `make conformance` / `make vet` / `make fmt` in this
directory, with `HDF5_DIR` pointing at the installation (default
`$(HOME)/.local/hdf5-1.14.6`). With `CGO_ENABLED=0` the packages are empty.

`make golden` regenerates `../go/testdata/golden/corpus.txt.gz`, the
fingerprints of what libhdf5 reads from the conformance corpus that the
pure Go module's `TestGolden` compares with (see `../go/README.md`), and
`make fixtures` rewrites the committed fixtures in `../go/testdata/fixtures`
first (`internal/fixtures`, written `WithoutTimes` so they are
reproducible). `make test` fails while either is stale.

In a checkout of the repository the `go.work` file at the root builds this
module against `../go`; elsewhere `github.com/crhntr/hdf5/go` comes from the
module proxy at the version go.mod requires.

## Writing

Beyond the shared reading API the package has a small writing API, used to
generate test files:

```go
f, err := hdf5.Create("out.h5", hdf5.WithLatestFormat())
g, err := f.CreateGroup("/data")
ds, err := hdf5.WriteDataset(g, "x", []uint64{3}, []float64{1, 2, 3},
    hdf5.WithChunk(2), hdf5.WithDeflate(4))
err = hdf5.WriteAttribute(ds, "units", nil, []string{"m"})
err = f.Close()
```

`Create` takes `WithLatestFormat`, `WithLibverBounds`, `WithUserblock`,
`WithSizes`, `WithSymK`, `WithIstoreK`, `WithRootCreationOrder`,
`WithFileSpaceStrategy`, `WithFileSpacePageSize` and `WithSharedMessages`;
groups have `CreateGroup` (with `WithCreationOrder`, `WithLinkPhaseChange`),
`CreateSoftLink`, `CreateHardLink` and `CreateExternalLink`;
`WriteDataset[T]` and `WriteAttribute[T]` take `WithChunk`, `WithMaxDims`,
`WithDeflate`, `WithShuffle`, `WithFletcher32`, `WithFillValue`,
`WithCompact`, `WithContiguous`, `WithAllocTime` and
`WithVariableLengthStrings`.

See [../go/README.md](../go/README.md) for an overview of both modules and
[../go/API.md](../go/API.md) for the API and the known differences between
the two implementations.
