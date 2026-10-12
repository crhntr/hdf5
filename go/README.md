# HDF5 for Go

This directory is a Go module, `github.com/HDFGroup/hdf5/go`, with two
implementations of HDF5 file reading that share one API:

- `hdf5` — pure Go (no cgo), a translation of the C library in `../src`
  into Go. Reads every file format feature of the HDF5 1.x line (superblock
  versions 0–3, both object header versions, all datatype classes, compact /
  contiguous / chunked / virtual layouts, every chunk index, the built-in
  filters except szip, old-style and dense groups and attributes, shared
  messages, external links and external data files, family and multi/split
  driver files). Virtual datasets read their source datasets from the same
  file or from other files located like libhdf5 does (`WithVirtualPrefix`,
  `HDF5_VDS_PREFIX`, the directory of the file, the current directory).
- `chdf5` — cgo bindings to the official libhdf5 (1.14.x) exposing the same
  API, used both as an oracle for `hdf5` and as a full-featured alternative
  where linking the C library is acceptable. It also has a small writing API
  used to generate test files.

`h5` holds the shared vocabulary (datatype, dataspace, link, layout and
value descriptions, and the conversion of values to Go types) and
`conformance` reads every test file in the repository with both packages
and compares the results.

See [API.md](API.md) for the API (including the fields the conformance
suite ignores and the known differences between the packages),
[TRANSLATION.md](TRANSLATION.md) for how the C code maps onto the Go code.

## Using

```go
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

Replacing `hdf5` by `chdf5` in the code above gives the same results.

## Building and testing

The pure Go packages need nothing beyond the Go toolchain:

    go test ./h5 ./hdf5

`chdf5` and `conformance` need libhdf5 1.14 and pkg-config:

    PKG_CONFIG_PATH=/path/to/hdf5/lib/pkgconfig go test ./chdf5 ./conformance

or `make test` from this directory (see the Makefile for the HDF5_DIR
variable). With `CGO_ENABLED=0` the cgo packages are excluded. Some tests
of `hdf5` also compare raw data with the output of the reference `h5dump`
when it is installed in `~/.local/hdf5-1.14.6/bin`.
