package hdf5

// STUB — to be replaced by the translation of src/H5HGcache.c + src/H5HG.c.

// h5hgT is H5HG_t: a reference to an object in a global heap collection.
type h5hgT struct {
	addr haddr  /* address of collection */
	idx  uint32 /* object ID within collection */
}

// hgRead is H5HG_read: return a copy of the bytes of the global heap object
// hobj. Index 0 (the free space object) and unknown indices are errors.
func (f *File) hgRead(hobj h5hgT) ([]byte, error) {
	return nil, errorf(H5E_HEAP, H5E_UNSUPPORTED, "global heap not translated yet")
}
