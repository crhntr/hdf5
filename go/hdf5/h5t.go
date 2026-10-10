package hdf5

// STUB — to be replaced by the translation of src/H5Odtype.c (decode) and
// the parts of src/H5T.c / src/H5Tconv*.c / src/H5Tvlen.c / src/H5Tref.c
// needed to convert file data to Go values.

import "github.com/HDFGroup/hdf5/go/h5"

// h5oDtypeDecode is H5O_dtype_decode: decode a datatype message.
func h5oDtypeDecode(f *File, p *image) (*h5.Datatype, error) {
	return nil, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "datatype not translated yet")
}

// h5tDecodeValues converts n elements of raw file data (in the file datatype
// dt, dt.Size bytes each) into the canonical Go values described in
// h5/value.go. Variable-length data and old-style references are resolved
// through f's global heap. ptype is the "property" context needed to decode
// region references' selections (unused for other types).
func h5tDecodeValues(f *File, dt *h5.Datatype, buf []byte, n uint64) ([]any, error) {
	return nil, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "datatype conversion not translated yet")
}
