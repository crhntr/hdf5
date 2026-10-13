# HDF5 for Go

Two Go modules in this repository implement HDF5 file reading with one
shared API:

- `github.com/crhntr/hdf5/go` (this directory, `package hdf5`): pure Go (no
  cgo), a translation of the C library in `../src` into Go. Reads every file
  format feature of the HDF5 1.x line (superblock versions 0–3, both object
  header versions, all datatype classes, compact / contiguous / chunked /
  virtual layouts, every chunk index, the built-in filters except szip,
  old-style and dense groups and attributes, shared messages, external links
  and external data files, family and multi/split driver files). Virtual
  datasets read their source datasets from the same file or from other files
  located like libhdf5 does (`WithVirtualPrefix`, `HDF5_VDS_PREFIX`, the
  directory of the file, the current directory).
- `github.com/crhntr/hdf5/cgo` (`../cgo`, also `package hdf5`): cgo bindings
  to the official libhdf5 (1.14.x) exposing the same API, used both as an
  oracle for the pure Go reader and as a full-featured alternative where
  linking the C library is acceptable. It also has a small writing API used
  to generate test files. See [../cgo/README.md](../cgo/README.md).

`github.com/crhntr/hdf5/go/h5` (`h5`) holds the shared vocabulary
(datatype, dataspace, link, layout and value descriptions, and the
conversion of values to Go types) and `github.com/crhntr/hdf5/cgo/conformance`
(test-only) reads every test file in the repository with both packages and
compares the results. Where both packages are imported, the cgo one is
named `chdf5`:

```go
import (
    "github.com/crhntr/hdf5/go"            // package hdf5, pure Go
    chdf5 "github.com/crhntr/hdf5/cgo"     // package hdf5, libhdf5
    "github.com/crhntr/hdf5/go/h5"
)
```

The rest of the documentation calls them `hdf5` and `chdf5`. See
[API.md](API.md) for the API (including the fields the conformance suite
ignores and the known differences between the packages),
[TRANSLATION.md](TRANSLATION.md) for how the C code maps onto the Go code.

## Using

```go
import (
    "github.com/crhntr/hdf5/go"
    "github.com/crhntr/hdf5/go/h5"
)

f, err := hdf5.Open("data.h5")
if err != nil { ... }
defer f.Close()

for link, err := range f.Root().Links() { ... }
for v, err := range f.Root().Walk() { ... } // recursive

ds, err := f.OpenDataset("/temperature")
layout, err := ds.Layout()
vals, err := hdf5.Read[float32](ds)                       // whole dataset, row-major
part, err := ds.ReadHyperslab(h5.Hyperslab{               // canonical values
    Start: []uint64{0, 10}, Count: []uint64{4, 2},
})

type Point struct {
    X float64 `hdf5:"x"`
    Y float64 `hdf5:"y"`
}
pts, err := hdf5.Read[Point](ds2)                         // compound → struct

a, err := ds.Attribute("units")
units, err := hdf5.ReadAttribute[string](a)
```

Replacing the import `github.com/crhntr/hdf5/go` by
`github.com/crhntr/hdf5/cgo` in the code above gives the same results.

## Building and testing

The two modules live in `./go` and `./cgo` of the repository; the
`go.work` file at the repository root puts both in one workspace, so the
cgo module builds against the checked-out go module (no `replace`
directive is needed). Outside a checkout, `github.com/crhntr/hdf5/cgo`
fetches `github.com/crhntr/hdf5/go` from the module proxy at the version
its go.mod requires.

The pure Go module needs nothing beyond the Go toolchain:

    cd go && go test ./...

(or `make test` in this directory). `make test-quick` (or `HDF5_QUICK_TESTS=1 go test .`, or `-short`) runs the package tests in about 4 seconds instead of 20: it skips the comparisons that run the reference tools (h5dump, h5debug, h5ls) and shortens the seeded corruption loops, keeping every check against hard-coded values, `.ddl` files and fixtures; it is meant for mutation testing (gremlins).
pkg-config:

    cd cgo && PKG_CONFIG_PATH=/path/to/hdf5/lib/pkgconfig go test ./...

or `make test` in `../cgo` (see its Makefile for the HDF5_DIR variable).
With `CGO_ENABLED=0` the cgo packages are empty. Some tests of `hdf5` also
compare raw data with the output of the reference `h5dump` when it is
installed in `~/.local/hdf5-1.14.6/bin`.

### Conformance suite

`conformance` (`../cgo/conformance`, test-only, cgo) proves that `hdf5`
reads what libhdf5 reads, identically. For every `.h5`/`.hdf5` file of the
repository (plus `go/testdata` and the committed fixtures in
`go/testdata/fixtures`) it opens the file with both packages and
compares the superblock, the whole link tree (`Walk`: paths and links), and
for every object its info, comment, links (by name and by creation order),
attributes (names, datatypes, dataspaces, values, `ReadAttribute[T]`),
committed datatypes and, for datasets, the datatype, dataspace, layout,
filters, fill value, the values (datasets up to 64 MiB), a strided
hyperslab, an out-of-bounds hyperslab and `Read[T]`; soft and external
link targets and object references (`OpenAddress`) are followed too.
Values are compared element by element, floats by bit pattern. Whatever
libhdf5 refuses (corrupt files, missing filters or external files) `hdf5`
must refuse as well. `TestFixtures` does the same for files written with
the `chdf5` writing API in every file format variant (latest format with
dense groups and attributes and the version 2 chunk indexes, shared
messages, paged file space, user blocks, 4-byte addresses, external links
between the generated files, ...). The few known, justified differences
are on the allow-list in `../cgo/conformance/allow_test.go` (see also
API.md).

    cd ../cgo && make conformance

runs it verbosely: each file logs a summary line, and each test ends with
a table of what was compared, what both packages refuse (by reason) and
which allowed differences were met. The whole suite takes about ten
seconds.

### Golden file: the libhdf5 oracle without cgo

The pure Go module's own tests cannot use cgo, so tools that only run them
(coverage, mutation testing with gremlins) would not see the conformance
suite. `testdata/golden/corpus.txt.gz` brings the libhdf5 results into
them: for every file of the conformance corpus it records, as fingerprints
(`h5.Fingerprint`, a SHA-256 of a canonical encoding of the `h5` values;
floats by bit pattern), what libhdf5 1.14.6 reads through `chdf5`, walked
the way the conformance suite walks it: whether the file opens, the
superblock, every `Walk` visit and link, every object's info (without
times), path, comment, attributes (datatype, dataspace, values,
`ReadAttribute[T]`) and links (by name and by creation order), committed
datatypes, every dataset's datatype, dataspace, layout (without
`Version` / `ChunkIndex`), filters, fill value (without `Version`),
values (up to 64 MiB), `Read[T]`, the suite's strided hyperslab and
out-of-bounds hyperslab, soft and external link targets and the objects
of object references (`OpenAddress`). Where libhdf5 fails (corrupt files,
missing filter plugins, szip, missing external files) the record says so
and the pure Go reader must fail too.

`TestGolden` (`golden_test.go`) reads every recorded file with this
package, records it the same way and compares entry by entry, applying the
allowed differences of the conformance suite's allow-list; it reports the
first differing entry of each file with its path and the differing fields
(e.g. `entry 9 (V "/dset1") differs in values, Read[T], hyperslab`). It
fails as well when a corpus file is missing from the checkout or missing
from the golden file. It takes about 4 seconds and needs neither cgo nor
libhdf5. The format is documented in `../cgo/internal/golden`.

Regenerate the golden file with

    cd ../cgo && make golden

whenever the corpus, the fixtures, the canonical encoding
(`h5/fingerprint.go`) or the `h5` types change (`make fixtures` rewrites
the fixtures in `testdata/fixtures` first). `make test` in `../cgo` fails
while it is stale (`TestGoldenUpToDate` compares it with live libhdf5
output).
