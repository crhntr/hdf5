package hdf5

// STUB — to be replaced by the translation of src/H5Bcache.c + src/H5B.c.

// h5bClass is H5B_class_t: the per-node-type callbacks of a v1 B-tree.
// Keys are decoded into Go values by decodeKey; the generic code never
// interprets them.
type h5bClass struct {
	id         uint8                                         /* h5bSnodeID or h5bChunkID */
	sizeofRkey func(f *File, ctx any) int                    /* H5B_class_t.sizeof_rkey: encoded key size */
	decodeKey  func(f *File, ctx any, p *image) (any, error) /* H5B_class_t.decode */
}

// h5bT is H5B_t: one decoded B-tree node.
type h5bT struct {
	level       uint8
	nchildren   uint16
	left, right haddr
	native      []any   /* nchildren+1 decoded keys */
	child       []haddr /* nchildren child addresses */
}

// bIterate is H5B_iterate: visit every leaf-level child of the B-tree rooted
// at addr, in order, calling op(leftKey, rightKey, childAddr).
func (f *File) bIterate(cls *h5bClass, ctx any, addr haddr, op func(leftKey, rightKey any, childAddr haddr) error) error {
	return errorf(H5E_BTREE, H5E_UNSUPPORTED, "v1 B-tree not translated yet")
}

// bFind is H5B_find: descend from addr using cmp3(leftKey, rightKey), which
// returns <0 / 0 / >0 for "the wanted key is before / within / after the
// range [leftKey, rightKey)", then calls found(leftKey, rightKey, childAddr)
// on the leaf child. Returns found=false (and no error) when absent.
func (f *File) bFind(cls *h5bClass, ctx any, addr haddr, cmp3 func(leftKey, rightKey any) int, found func(leftKey, rightKey any, childAddr haddr) error) (bool, error) {
	return false, errorf(H5E_BTREE, H5E_UNSUPPORTED, "v1 B-tree not translated yet")
}

// bProtect is H5B_protect / H5B__cache_deserialize: load one node.
func (f *File) bProtect(cls *h5bClass, ctx any, addr haddr) (*h5bT, error) {
	return nil, errorf(H5E_BTREE, H5E_UNSUPPORTED, "v1 B-tree not translated yet")
}
