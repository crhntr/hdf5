// Package hdf5 is a pure Go (no cgo) reader for the HDF5 file format,
// implemented as a translation of the HDF5 C library (this repository's src/
// directory) into Go.
//
// # Reading
//
// Open a file with Open (or OpenReader for an in-memory image), navigate
// from File.Root or open objects by path (File.Open, File.OpenGroup,
// File.OpenDataset, File.OpenDatatype; soft and external links are
// followed) or by object header address (File.OpenAddress, for object
// references). Group.Links lists a group's links in name order and
// Group.Walk visits a whole tree.
//
// A Dataset describes its datatype, dataspace, storage layout, filter
// pipeline and fill value with the shared vocabulary of package h5, and
// reads its elements as canonical values (ReadValues, ReadHyperslab: see
// h5/value.go) or as Go values (Read[T], ReadInto) converted by
// h5.ConvertValues. Attributes (Object.Attributes, Object.Attribute) read
// the same way (ReadAttribute[T]).
//
// Every layout is supported, including virtual datasets: their source
// datasets are looked up in the same file or in files located like the C
// library does (WithVirtualPrefix / HDF5_VDS_PREFIX, the directory of the
// file, the current directory) and kept open until the file is closed. A
// missing source reads as the fill value. External links and external data
// files are resolved the same way (WithExternalLinkPrefix,
// WithExternalFilePrefix).
//
// The API is shared with package chdf5 (cgo bindings to libhdf5) so that
// both can be compared; see API.md in the module root for the API, the
// canonical values and the known differences.
//
// # Translation
//
// Internal files are named after the C source files they were translated from
// (h5o_dtype.go <- src/H5Odtype.c, h5b2_cache.go <- src/H5B2cache.c, ...) and
// each translated function carries a comment naming its C origin so the two can
// be read side by side. The public API (file.go, group.go, object.go,
// dataset.go, attribute.go, read.go) is idiomatic Go built on top of that
// translation and uses iterators (iter.Seq2) and generics for typed reads.
//
// Only the standard library is used; github.com/stretchr/testify is used in
// tests.
package hdf5
