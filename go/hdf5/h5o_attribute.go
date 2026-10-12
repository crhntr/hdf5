package hdf5

// Partial translation of src/H5Oattribute.c: only H5O__attr_count_real,
// which H5O_get_info needs. The attribute module (src/H5Aint.c,
// src/H5Adense.c, the rest of src/H5Oattribute.c) is translated separately
// and replaces this file.

// oAttrCount is H5O__attr_count_real: the number of attributes on the
// object whose header is oh.
//
// TODO(attributes): for version 2 headers with an attribute info message the
// C code uses H5A__get_ainfo, which counts the attributes in dense storage
// (the number of records in the name index v2 B-tree at ainfo.nameBt2Addr)
// when ainfo.fheapAddr is defined. Until the attribute module replaces this
// function only the attribute messages stored in the header (compact
// storage) are counted.
func (f *File) oAttrCount(oh *h5o) (uint64, error) {
	/* Loop over all messages, counting the attributes */
	return uint64(f.msgCount(oh, h5oAttrID)), nil
}
