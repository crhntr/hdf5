//go:build cgo

// Package conformance holds the tests that read every HDF5 file of the
// repository (and files generated with the writing API of the cgo package)
// with both the pure Go reader (github.com/crhntr/hdf5/go, imported as
// hdf5) and the cgo wrapper around libhdf5 (github.com/crhntr/hdf5/cgo,
// imported as chdf5) and compare the results: superblock, link tree, object
// info, comments, attributes, dataset datatypes, dataspaces, layouts,
// filters, fill values and values. libhdf5 is the reference: what it cannot
// read the pure Go reader must refuse too.
//
// The package has no non-test code. Run it with
//
//	PKG_CONFIG_PATH=$HOME/.local/hdf5-1.14.6/lib/pkgconfig go test -v ./conformance
//
// from the cgo module directory, or `make conformance`.
package conformance
