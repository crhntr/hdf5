// Package hdf5 is a pure Go (no cgo) reader for the HDF5 file format,
// implemented as a translation of the HDF5 C library (this repository's src/
// directory) into Go.
//
// Internal files are named after the C source files they were translated from
// (h5o_dtype.go <- src/H5Odtype.c, h5b2_cache.go <- src/H5B2cache.c, ...) and
// each translated function carries a comment naming its C origin so the two can
// be read side by side. The public API (file.go, group.go, dataset.go,
// attribute.go, read.go) is idiomatic Go built on top of that translation and
// uses iterators (iter.Seq2) and generics for typed reads.
//
// Only the standard library is used; github.com/stretchr/testify is used in
// tests.
package hdf5
