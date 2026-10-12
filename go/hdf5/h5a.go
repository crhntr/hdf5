package hdf5

// Stubs for the attribute module (src/H5A*.c and the attribute parts of
// src/H5Oattribute.c), which is translated separately; the agent
// translating it replaces this file. The public Object attribute methods
// (object.go) are built on these three functions.

// aIterate is H5O__attr_iterate_real with H5_INDEX_NAME / H5_ITER_INC:
// call op for every attribute of the object whose header is oh, in
// increasing name order. op may return errBIterStop to stop early.
func (f *File) aIterate(oh *h5o, op func(attr *h5oAttr) error) error {
	return errorf(H5E_ATTR, H5E_UNSUPPORTED, "attributes not translated yet")
}

// aOpenByName is H5O__attr_open_by_name: the attribute called name of the
// object whose header is oh.
func (f *File) aOpenByName(oh *h5o, name string) (*h5oAttr, error) {
	return nil, errorf(H5E_ATTR, H5E_UNSUPPORTED, "attributes not translated yet")
}

// aCount is H5O__attr_count_real: the number of attributes of the object
// whose header is oh.
func (f *File) aCount(oh *h5o) (uint64, error) {
	return 0, errorf(H5E_ATTR, H5E_UNSUPPORTED, "attributes not translated yet")
}
