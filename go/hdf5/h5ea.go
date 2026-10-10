package hdf5

// STUB — to be replaced by the translation of src/H5EAcache.c, src/H5EAhdr.c,
// src/H5EAiblock.c, src/H5EAsblock.c, src/H5EAdblock.c, src/H5EAdblkpage.c,
// src/H5EAdbg.c (table computations) and src/H5EA.c.

// H5EA_cls_id_t
const (
	h5eaClsChunk     = 0
	h5eaClsFiltChunk = 1
	h5eaClsTest      = 2
)

// h5eaClass is H5EA_class_t (decode side).
type h5eaClass struct {
	id     uint8
	decode func(f *File, ctx any, p *image) (any, error)
	fill   func(ctx any) any
}

// h5eaHdr is H5EA_hdr_t.
type h5eaHdr struct {
	addr    haddr
	version uint8
	clsID   uint8
	eltSize uint8
	// H5EA_create_t cparam
	maxNelmtsBits         uint8
	idxBlkElmts           uint8
	supBlkMinDataPtrs     uint8
	dataBlkMinElmts       uint8
	maxDblkPageNelmtsBits uint8
	// stats
	nsuperBlks, ndataBlks, nelmts, maxIdxSet uint64
	idxBlkAddr                               haddr
}

// h5ea is H5EA_t.
type h5ea struct {
	f   *File
	hdr *h5eaHdr
	cls *h5eaClass
	ctx any
}

// eaOpen is H5EA_open.
func (f *File) eaOpen(addr haddr, cls *h5eaClass, ctx any) (*h5ea, error) {
	return nil, errorf(H5E_EARRAY, H5E_UNSUPPORTED, "extensible array not translated yet")
}

// get is H5EA_get: the decoded element at idx (cls.fill(ctx) when the
// containing block has not been allocated).
func (ea *h5ea) get(idx uint64) (any, error) {
	return nil, errorf(H5E_EARRAY, H5E_UNSUPPORTED, "extensible array not translated yet")
}
