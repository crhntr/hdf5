package hdf5

// Entry points of the attribute module used by the public API (the roles
// of H5Aiterate2, H5Aopen_by_name, H5Oget_info's num_attrs and H5Aread).
// The translations are in h5o_attribute.go (src/H5Oattribute.c),
// h5a_int.go (src/H5Aint.c) and h5a_dense.go (src/H5Adense.c).

import "errors"

// aIterate is H5O__attr_iterate_real with H5_INDEX_NAME, H5_ITER_INC: call
// op on every attribute of the object whose header is oh, in increasing
// strcmp order of the names. op may return errBIterStop to stop early
// (aIterate then returns nil). The attributes passed to op are fully
// decoded copies (datatype, dataspace and data present) that op may keep.
func (f *File) aIterate(oh *h5o, op func(attr *h5oAttr) error) error {
	err := f.oAttrIterateReal(oh, h5aIndexName, h5aIterInc, 0, nil, op)
	if err != nil && errors.Is(err, errBIterStop) {
		return nil
	}
	return err
}

// aOpenByName is H5O__attr_open_by_name: the attribute called name on the
// object whose header is oh. A missing attribute is an error that matches
// ErrNotFound. The returned attribute is a fully decoded copy.
func (f *File) aOpenByName(oh *h5o, name string) (*h5oAttr, error) {
	return f.oAttrOpenByName(oh, name)
}

// aCount is H5O__attr_count_real: the number of attributes on the object
// whose header is oh.
func (f *File) aCount(oh *h5o) (uint64, error) {
	return f.oAttrCount(oh)
}

// aRead is H5A__read: the attribute's elements as canonical values
// (h5/value.go); nil-free and empty for a null dataspace, one element for a
// scalar dataspace.
func (f *File) aRead(attr *h5oAttr) ([]any, error) {
	return f.aReadReal(attr)
}
