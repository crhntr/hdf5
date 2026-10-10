// Package h5 holds the vocabulary shared by the two Go implementations of
// HDF5 in this module: the pure-Go reader (package hdf5) and the cgo wrapper
// around libhdf5 (package chdf5).
//
// It only contains plain data descriptions of what is found in an HDF5 file
// (datatypes, dataspaces, links, layouts, filters and decoded values). It has
// no dependencies outside the Go standard library and performs no I/O, so the
// conformance tests can compare what the two implementations report for the
// same file without either implementation depending on the other.
package h5
