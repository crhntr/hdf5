package hdf5

// The public attribute API (see API.md), built on the attribute module
// (h5a.go, h5a_int.go, h5o_attribute.go).

import "github.com/crhntr/hdf5/go/h5"

// Datatype describes the attribute's datatype (H5Aget_type).
func (a *Attribute) Datatype() *h5.Datatype { return a.attr.dt }

// Dataspace describes the attribute's dataspace (H5Aget_space).
func (a *Attribute) Dataspace() h5.Dataspace {
	if a.attr.ds == nil {
		return h5.Dataspace{}
	}
	return a.attr.ds.public()
}

// ReadValues reads the attribute as canonical values (h5/value.go) in
// row-major order: one value per element, one for a scalar dataspace, none
// for a null dataspace.
func (a *Attribute) ReadValues() ([]any, error) {
	vals, err := a.f.aRead(a.attr)
	if err != nil {
		return nil, err
	}
	if vals == nil {
		vals = []any{}
	}
	return vals, nil
}

// ReadInto reads the attribute into dst, a pointer to a slice of a
// supported Go type (see Read and h5.ConvertInto).
func (a *Attribute) ReadInto(dst any) error {
	vals, err := a.ReadValues()
	if err != nil {
		return err
	}
	return h5.ConvertInto(vals, dst)
}
