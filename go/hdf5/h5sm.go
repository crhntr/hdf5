package hdf5

// STUB — to be replaced by the translation of src/H5SM.c and
// src/H5SMcache.c (shared object header messages).

// smReadMessage reads the message described by sh (sh.typ ==
// h5oShareTypeSohm, sh.heapID set) from the file's shared message heap and
// decodes it as a message of class typeID (the role of the SOHM branch of
// H5O__shared_read: H5SM_get_fheap_addr, H5HF_get_obj_len/H5HF_read, then the
// class's decode callback with no message flags, e.g. f.msgDecode(nil,
// typeID, raw)). The shared message table message decoded from the
// superblock extension is in f.sohm (a *h5oShmesgTable, or nil).
func (f *File) smReadMessage(sh *h5oShared, typeID uint16) (any, error) {
	return nil, errorf(H5E_SOHM, H5E_UNSUPPORTED, "shared message table not translated yet")
}
