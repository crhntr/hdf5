package hdf5

// STUB — to be replaced by the translation of src/H5FAcache.c, src/H5FAhdr.c,
// src/H5FAdblock.c, src/H5FAdblkpage.c and src/H5FA.c.

// H5FA_cls_id_t
const (
	h5faClsChunk     = 0 /* Chunk index of dataset (unfiltered) */
	h5faClsFiltChunk = 1 /* Chunk index of dataset (filtered) */
	h5faClsTest      = 2
)

// h5faClass is H5FA_class_t (decode side).
type h5faClass struct {
	id     uint8
	decode func(f *File, ctx any, p *image) (any, error) /* decode one element of hdr.eltSize bytes */
	fill   func(ctx any) any                             /* value of a never-written element */
}

// h5faHdr is H5FA_hdr_t.
type h5faHdr struct {
	addr          haddr
	version       uint8
	clsID         uint8
	eltSize       uint8  /* Element size in file (in bytes) */
	maxNelmtsBits uint8  /* Log2(Max. # of elements in array) */
	nelmts        uint64 /* # of elements in the array */
	dblkAddr      haddr  /* Address of data block */
}

// h5fa is H5FA_t.
type h5fa struct {
	f   *File
	hdr *h5faHdr
	cls *h5faClass
	ctx any
}

// faOpen is H5FA_open.
func (f *File) faOpen(addr haddr, cls *h5faClass, ctx any) (*h5fa, error) {
	return nil, errorf(H5E_FARRAY, H5E_UNSUPPORTED, "fixed array not translated yet")
}

// get is H5FA_get: the decoded element at idx (cls.fill(ctx) when the data
// block/page has not been allocated).
func (fa *h5fa) get(idx uint64) (any, error) {
	return nil, errorf(H5E_FARRAY, H5E_UNSUPPORTED, "fixed array not translated yet")
}
