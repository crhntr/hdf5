package hdf5

// STUB — to be replaced by the translation of src/H5HFcache.c, src/H5HFhdr.c,
// src/H5HFdtable.c, src/H5HFiblock.c, src/H5HFdblock.c, src/H5HFman.c,
// src/H5HFtiny.c, src/H5HFhuge.c, src/H5HFbtree2.c and src/H5HF.c.

// h5hf is H5HF_t: an opened fractal heap.
type h5hf struct {
	f   *File
	hdr *h5hfHdr
}

// h5hfHdr is H5HF_hdr_t (decoded header fields).
type h5hfHdr struct {
	addr                                        haddr
	version                                     uint8
	heapOffSize                                 uint8
	heapLenSize                                 uint8
	idLen                                       uint16 /* Size of heap IDs (in bytes) */
	filterLen                                   uint16 /* Size of I/O filter information (in bytes) */
	flags                                       uint8
	maxManSize                                  uint32 /* Max. size of object to manage in doubling table */
	nextManID                                   uint64 /* Next ID for huge objects */
	hugeBt2Addr                                 haddr
	freeSpace                                   uint64
	fsAddr                                      haddr
	manDtable                                   h5hfDtable
	manSize, manAllocSize, manIterOff, manNobjs uint64
	hugeSize, hugeNobjs, tinySize, tinyNobjs    uint64
	pline                                       *h5oPline /* I/O filter pipeline for heap objects, if any */
	plineRootDirectSize                         uint64
	plineRootDirectFilterMask                   uint32
	checksumDblocks                             bool
	hugeIDsDirect                               bool
	hugeIDsWrapped                              bool
}

// h5hfDtable is H5HF_dtable_t: the doubling table parameters.
type h5hfDtable struct {
	width                                                         uint16 /* Number of columns in the table (must be power of 2) */
	startBits                                                     uint16 /* # of bits for starting block size (i.e. log2(start_block_size)) */
	maxDirectBits                                                 uint16 /* Max. # of bits for direct block size */
	maxIndexBits                                                  uint16 /* Max. # of bits for root indirect block (i.e. log2(max_root_iblock_size)) */
	startRootRows                                                 uint16 /* Starting number of rows for root indirect block */
	tableAddr                                                     haddr  /* Address of first block for table */
	curRootRows                                                   uint16 /* Current number of rows in the root indirect block */
	startBlockSize                                                uint64
	maxDirectSize                                                 uint64
	maxDirRows, maxDirectRows, numIDFirstRow                      uint32
	firstRowBits                                                  uint32
	rowBlockSize, rowBlockOff, rowTotDblockFree, rowMaxDblockFree []uint64
}

// hfOpen is H5HF_open: load the heap header at addr.
func (f *File) hfOpen(addr haddr) (*h5hf, error) {
	return nil, errorf(H5E_HEAP, H5E_UNSUPPORTED, "fractal heap not translated yet")
}

// idLen is H5HF_get_id_len.
func (fh *h5hf) idLen() int { return int(fh.hdr.idLen) }

// read is H5HF_read: return a copy of the object identified by the heap ID.
func (fh *h5hf) read(id []byte) ([]byte, error) {
	return nil, errorf(H5E_HEAP, H5E_UNSUPPORTED, "fractal heap not translated yet")
}
