package hdf5

// STUB — to be replaced by the translation of src/H5HLcache.c + src/H5HL.c.

// h5hl is H5HL_t: a local heap (collection of NUL-terminated strings and
// other small objects referenced by offset).
type h5hl struct {
	addr     haddr
	dblkAddr haddr
	dblkSize uint64
	freeHead uint64
	image    []byte /* the heap's data block */
}

// hlProtect is H5HL_protect: load (through the cache) the local heap at addr.
func (f *File) hlProtect(addr haddr) (*h5hl, error) {
	return nil, errorf(H5E_HEAP, H5E_UNSUPPORTED, "local heap not translated yet")
}

// offsetInto is H5HL_offset_into: the heap bytes starting at offset, or an
// error when the offset is outside the heap.
func (h *h5hl) offsetInto(offset uint64) ([]byte, error) {
	return nil, errorf(H5E_HEAP, H5E_UNSUPPORTED, "local heap not translated yet")
}

// stringAt returns the NUL-terminated string at offset (the common use of
// H5HL_offset_into for link names and soft link values).
func (h *h5hl) stringAt(offset uint64) (string, error) {
	return "", errorf(H5E_HEAP, H5E_UNSUPPORTED, "local heap not translated yet")
}
