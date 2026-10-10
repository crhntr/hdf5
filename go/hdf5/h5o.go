package hdf5

// STUB — to be replaced by the translation of src/H5Ocache.c, src/H5Oint.c,
// src/H5Omessage.c, src/H5Oshared.c and the per-message H5O*.c files.

// oProtect is H5O_protect: load (through the cache) the object header at
// addr, including continuation chunks. Messages are decoded lazily by
// msgRead.
func (f *File) oProtect(addr haddr) (*h5o, error) {
	return nil, errorf(H5E_OHDR, H5E_UNSUPPORTED, "object headers not translated yet")
}

// msgExists is H5O_msg_exists_oh.
func (f *File) msgExists(oh *h5o, typeID uint16) bool { return false }

// msgCount is H5O_msg_count.
func (f *File) msgCount(oh *h5o, typeID uint16) int { return 0 }

// msgRead is H5O_msg_read_oh: decode (resolving shared messages) and return
// the first message of the given type as one of the h5o* structs in
// h5o_types.go. Returns ErrNotFound when the header has no such message.
func (f *File) msgRead(oh *h5o, typeID uint16) (any, error) {
	return nil, errorf(H5E_OHDR, H5E_UNSUPPORTED, "object headers not translated yet")
}

// msgIterate is H5O_msg_iterate: decode and visit every message of the given
// type, in header order.
func (f *File) msgIterate(oh *h5o, typeID uint16, op func(msg *h5oMesg, native any) error) error {
	return errorf(H5E_OHDR, H5E_UNSUPPORTED, "object headers not translated yet")
}
