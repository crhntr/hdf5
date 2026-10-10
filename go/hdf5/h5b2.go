package hdf5

// STUB — to be replaced by the translation of src/H5B2cache.c, src/H5B2hdr.c,
// src/H5B2int.c, src/H5B2leaf.c, src/H5B2internal.c and src/H5B2.c.

// H5B2_subid_t: v2 B-tree record types.
const (
	h5b2TestID               = 0  /* B-tree is for testing (do not use for actual data) */
	h5b2FheapHugeIndirID     = 1  /* B-tree is for fractal heap indirectly accessed, non-filtered 'huge' objects */
	h5b2FheapHugeFiltIndirID = 2  /* B-tree is for fractal heap indirectly accessed, filtered 'huge' objects */
	h5b2FheapHugeDirID       = 3  /* B-tree is for fractal heap directly accessed, non-filtered 'huge' objects */
	h5b2FheapHugeFiltDirID   = 4  /* B-tree is for fractal heap directly accessed, filtered 'huge' objects */
	h5b2GrpDenseNameID       = 5  /* B-tree is for indexing 'name' field for "dense" link storage in groups */
	h5b2GrpDenseCorderID     = 6  /* B-tree is for indexing 'creation order' field for "dense" link storage in groups */
	h5b2SohmIndexID          = 7  /* B-tree is an index for shared object header messages */
	h5b2AttrDenseNameID      = 8  /* B-tree is for indexing 'name' field for "dense" attribute storage on objects */
	h5b2AttrDenseCorderID    = 9  /* B-tree is for indexing 'creation order' field for "dense" attribute storage on objects */
	h5b2CdsetID              = 10 /* B-tree is for non-filtered chunked dataset storage w/implicit index */
	h5b2CdsetFiltID          = 11 /* B-tree is for filtered chunked dataset storage w/implicit index */
	h5b2TestID2              = 12 /* Another B-tree is for testing (do not use for actual data) */
	h5b2NumBtreeID           = 13 /* Number of B-tree key IDs (must be last) */
)

// h5b2Class is H5B2_class_t (decode side). ctx is the client "context"
// (H5B2_class_t.crt_context) and is passed back to the callbacks.
type h5b2Class struct {
	id      uint8
	decode  func(f *File, ctx any, p *image) (any, error)           /* H5B2_class_t.decode: one raw record */
	compare func(f *File, ctx any, udata any, rec any) (int, error) /* H5B2_class_t.compare: udata vs rec (<0, 0, >0) */
}

// h5b2Hdr is H5B2_hdr_t (decoded header fields).
type h5b2Hdr struct {
	addr                       haddr
	version                    uint8
	typ                        uint8
	nodeSize                   uint32
	rrecSize                   uint16 /* size of raw (disk) record */
	depth                      uint16
	splitPercent, mergePercent uint8
	rootAddr                   haddr
	rootNrec                   uint16
	rootAllNrec                uint64 /* total number of records in the tree */
}

// h5b2 is H5B2_t: an opened v2 B-tree.
type h5b2 struct {
	f   *File
	hdr *h5b2Hdr
	cls *h5b2Class
	ctx any
}

// b2Open is H5B2_open: load the header at addr.
func (f *File) b2Open(addr haddr, cls *h5b2Class, ctx any) (*h5b2, error) {
	return nil, errorf(H5E_BTREE, H5E_UNSUPPORTED, "v2 B-tree not translated yet")
}

// iterate is H5B2_iterate: call op for every record, in order.
func (bt *h5b2) iterate(op func(rec any) error) error {
	return errorf(H5E_BTREE, H5E_UNSUPPORTED, "v2 B-tree not translated yet")
}

// find is H5B2_find: locate the record matching udata (via cls.compare) and
// call op on it. Returns found=false (no error) when absent.
func (bt *h5b2) find(udata any, op func(rec any) error) (bool, error) {
	return false, errorf(H5E_BTREE, H5E_UNSUPPORTED, "v2 B-tree not translated yet")
}

// index is H5B2_index: call op on the idx-th record (0-based, in order).
func (bt *h5b2) index(idx uint64, op func(rec any) error) error {
	return errorf(H5E_BTREE, H5E_UNSUPPORTED, "v2 B-tree not translated yet")
}

// numRecords is H5B2_get_nrec.
func (bt *h5b2) numRecords() uint64 { return bt.hdr.rootAllNrec }
