//go:build cgo

// Package conformance holds the tests that read every HDF5 file of the
// repository (and files generated with the chdf5 writing API) with both the
// pure Go reader (package hdf5) and the cgo wrapper around libhdf5 (package
// chdf5) and compare the results: superblock, link tree, object info,
// comments, attributes, dataset datatypes, dataspaces, layouts, filters,
// fill values and values. libhdf5 is the reference: what it cannot read the
// pure Go reader must refuse too.
//
// The package has no non-test code. Run it with
//
//	PKG_CONFIG_PATH=$HOME/.local/hdf5-1.14.6/lib/pkgconfig go test -v ./conformance
//
// or `make conformance`.
package conformance
