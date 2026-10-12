package hdf5

// Typed reads (see API.md, "Supported Go types for Read[T] / ReadInto").
// The conversion from canonical values lives in package h5 so that this
// package and the cgo package (github.com/crhntr/hdf5/cgo) convert
// identically.

import "github.com/crhntr/hdf5/go/h5"

// Read reads the whole dataset as a []T, in row-major order. T is one of
// the types of the table in API.md: integers (range checked), floats,
// string, []byte, bool, a struct for compound values (fields matched by
// `hdf5:"name"` tag, exact name, then case-insensitively), a slice for array
// and variable-length sequence values, h5.ObjectRef, h5.RegionRef,
// h5.AttrRef, h5.Enum, h5.Compound, or any.
func Read[T any](d *Dataset) ([]T, error) {
	vals, err := d.ReadValues()
	if err != nil {
		return nil, err
	}
	return h5.ConvertValues[T](vals)
}

// ReadAttribute reads the attribute as a []T (see Read for the supported
// types).
func ReadAttribute[T any](a *Attribute) ([]T, error) {
	vals, err := a.ReadValues()
	if err != nil {
		return nil, err
	}
	return h5.ConvertValues[T](vals)
}
