// Package hdf5 is a cgo wrapper around the HDF5 C library (libhdf5 1.14.x,
// found with pkg-config) exposing the same reading API as the pure Go
// reader github.com/crhntr/hdf5/go, so that a program can switch
// implementations by changing only the import path. Types from
// github.com/crhntr/hdf5/go/h5 are the shared vocabulary. The package also
// has a small writing API (Create, File.CreateGroup, WriteDataset[T],
// WriteAttribute[T], ...) used to generate test files.
//
// Every file but this one needs cgo: built with CGO_ENABLED=0 the package
// is empty. See README.md in this module and API.md in the go module.
package hdf5
