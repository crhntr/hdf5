# HDF5 for Go

This directory is a Go module, `github.com/HDFGroup/hdf5/go`, with two
implementations of HDF5 file reading that share one API:

- `hdf5` — pure Go (no cgo), a translation of the C library in `../src`
  into Go. Reads every file format feature of the HDF5 1.x line (superblock
  versions 0–3, both object header versions, all datatype classes, compact /
  contiguous / chunked / virtual layouts, every chunk index, the built-in
  filters except szip, old-style and dense groups and attributes, shared
  messages, family and multi/split driver files).
- `chdf5` — cgo bindings to the official libhdf5 (1.14.x) exposing the same
  API, used both as an oracle for `hdf5` and as a full-featured alternative
  where linking the C library is acceptable.

`h5` holds the shared vocabulary (datatype, dataspace, link, layout and
value descriptions) and `conformance` reads every test file in the
repository with both packages and compares the results.

See [API.md](API.md) for the API, [TRANSLATION.md](TRANSLATION.md) for how
the C code maps onto the Go code.

## Using

```go
f, err := hdf5.Open("data.h5")
defer f.Close()
for link, err := range f.Root().Links() { ... }
ds, err := f.OpenDataset("/temperature")
vals, err := hdf5.Read[float32](ds)
```

## Building and testing

The pure Go packages need nothing beyond the Go toolchain:

    go test ./h5 ./hdf5

`chdf5` and `conformance` need libhdf5 1.14 and pkg-config:

    PKG_CONFIG_PATH=/path/to/hdf5/lib/pkgconfig go test ./chdf5 ./conformance

or `make test` from this directory (see the Makefile for the HDF5_DIR
variable). With `CGO_ENABLED=0` the cgo packages are excluded.
