package hdf5

// STUB — to be replaced by the translation of src/H5S.c,
// src/H5Sselect.c, src/H5Sall.c, src/H5Spoint.c, src/H5Shyper.c (deserialize
// and iteration only) and src/H5Snone.c.

// h5sSelectDeserialize is H5S_select_deserialize: decode a serialized
// selection (as stored in region references and virtual dataset mappings).
// It returns the selection and the extent rank it was encoded for.
func h5sSelectDeserialize(f *File, p *image) (*h5sSelection, error) {
	return nil, errorf(H5E_DATASPACE, H5E_UNSUPPORTED, "selection not translated yet")
}
