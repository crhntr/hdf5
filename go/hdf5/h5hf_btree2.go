package hdf5

// Translated from src/H5HFbtree2.c (read side only): the v2 B-tree record
// classes used to track 'huge' fractal heap objects (B-tree types 1-4).

import "cmp"

// h5hfHugeBt2Ctx is H5HF_huge_bt2_ctx_t: the v2 B-tree callback context
// (the sizes of addresses & lengths in the file).
type h5hfHugeBt2Ctx struct {
	sizeofAddr uint8 /* Size of file addresses */
	sizeofSize uint8 /* Size of file sizes */
}

// h5hfHugeBt2IndirRec is H5HF_huge_bt2_indir_rec_t: indirectly accessed
// 'huge' object's record in the v2 B-tree.
type h5hfHugeBt2IndirRec struct {
	addr haddr  /* Address of the object in the file */
	len  uint64 /* Length of the object in the file */
	id   uint64 /* ID used for object (not used for 'huge' objects directly accessed) */
}

// h5hfHugeBt2FiltIndirRec is H5HF_huge_bt2_filt_indir_rec_t: indirectly
// accessed, filtered 'huge' object's record in the v2 B-tree.
type h5hfHugeBt2FiltIndirRec struct {
	addr       haddr  /* Address of the filtered object in the file */
	len        uint64 /* Length of the filtered object in the file */
	filterMask uint32 /* I/O pipeline filter mask for filtered object in the file */
	objSize    uint64 /* Size of the de-filtered object in memory */
	id         uint64 /* ID used for object (not used for 'huge' objects directly accessed) */
}

// h5hfHugeBt2DirRec is H5HF_huge_bt2_dir_rec_t: directly accessed 'huge'
// object's record in the v2 B-tree.
type h5hfHugeBt2DirRec struct {
	addr haddr  /* Address of the object in the file */
	len  uint64 /* Length of the object in the file */
}

// h5hfHugeBt2FiltDirRec is H5HF_huge_bt2_filt_dir_rec_t: directly accessed,
// filtered 'huge' object's record in the v2 B-tree.
type h5hfHugeBt2FiltDirRec struct {
	addr       haddr  /* Address of the filtered object in the file */
	len        uint64 /* Length of the filtered object in the file */
	filterMask uint32 /* I/O pipeline filter mask for filtered object in the file */
	objSize    uint64 /* Size of the de-filtered object in memory */
}

// h5hfHugeBt2Indir is H5HF_HUGE_BT2_INDIR: v2 B-tree class for indirectly
// accessed 'huge' objects.
func h5hfHugeBt2Indir() *h5b2Class {
	return &h5b2Class{
		id:      h5b2FheapHugeIndirID,    /* Type of B-tree */
		decode:  h5hfHugeBt2IndirDecode,  /* Record decoding callback */
		compare: h5hfHugeBt2IndirCompare, /* Record comparison callback */
	}
}

// h5hfHugeBt2FiltIndir is H5HF_HUGE_BT2_FILT_INDIR: v2 B-tree class for
// indirectly accessed, filtered 'huge' objects.
func h5hfHugeBt2FiltIndir() *h5b2Class {
	return &h5b2Class{
		id:      h5b2FheapHugeFiltIndirID,    /* Type of B-tree */
		decode:  h5hfHugeBt2FiltIndirDecode,  /* Record decoding callback */
		compare: h5hfHugeBt2FiltIndirCompare, /* Record comparison callback */
	}
}

// h5hfHugeBt2Dir is H5HF_HUGE_BT2_DIR: v2 B-tree class for directly
// accessed 'huge' objects.
func h5hfHugeBt2Dir() *h5b2Class {
	return &h5b2Class{
		id:      h5b2FheapHugeDirID,    /* Type of B-tree */
		decode:  h5hfHugeBt2DirDecode,  /* Record decoding callback */
		compare: h5hfHugeBt2DirCompare, /* Record comparison callback */
	}
}

// h5hfHugeBt2FiltDir is H5HF_HUGE_BT2_FILT_DIR: v2 B-tree class for
// directly accessed, filtered 'huge' objects.
func h5hfHugeBt2FiltDir() *h5b2Class {
	return &h5b2Class{
		id:      h5b2FheapHugeFiltDirID,    /* Type of B-tree */
		decode:  h5hfHugeBt2FiltDirDecode,  /* Record decoding callback */
		compare: h5hfHugeBt2FiltDirCompare, /* Record comparison callback */
	}
}

// h5hfHugeBt2Class returns the class for v2 B-tree type id (1-4), or nil.
func h5hfHugeBt2Class(id uint8) *h5b2Class {
	switch id {
	case h5b2FheapHugeIndirID:
		return h5hfHugeBt2Indir()
	case h5b2FheapHugeFiltIndirID:
		return h5hfHugeBt2FiltIndir()
	case h5b2FheapHugeDirID:
		return h5hfHugeBt2Dir()
	case h5b2FheapHugeFiltDirID:
		return h5hfHugeBt2FiltDir()
	}
	return nil
}

// h5hfHugeBt2CrtContext is H5HF__huge_bt2_crt_context: create client
// callback context (common to all 'huge' v2 B-tree clients).
func h5hfHugeBt2CrtContext(f *File) *h5hfHugeBt2Ctx {
	/* Determine the size of addresses & lengths in the file */
	return &h5hfHugeBt2Ctx{sizeofAddr: f.sizeofAddr, sizeofSize: f.sizeofSize}
}

// h5hfHugeBt2CtxOf returns the context passed to a record callback, or a
// context built from f when the B-tree code passes none.
func h5hfHugeBt2CtxOf(f *File, ctx any) *h5hfHugeBt2Ctx {
	if c, ok := ctx.(*h5hfHugeBt2Ctx); ok && c != nil {
		return c
	}
	return h5hfHugeBt2CrtContext(f)
}

// h5hfHugeBt2IndirFound is H5HF__huge_bt2_indir_found: retrieve record for
// indirectly accessed 'huge' object, when it's found in the v2 B-tree.
func h5hfHugeBt2IndirFound(nrecord any, opData *h5hfHugeBt2IndirRec) error {
	rec, ok := nrecord.(*h5hfHugeBt2IndirRec)
	if !ok {
		return errorf(H5E_HEAP, H5E_BADTYPE, "unexpected 'huge' object record type %T", nrecord)
	}
	*opData = *rec
	return nil
}

// h5hfHugeBt2IndirCompare is H5HF__huge_bt2_indir_compare: compare the
// search record udata with the native record rec, by ID.
func h5hfHugeBt2IndirCompare(f *File, ctx any, udata any, rec any) (int, error) {
	r1, ok1 := udata.(*h5hfHugeBt2IndirRec)
	r2, ok2 := rec.(*h5hfHugeBt2IndirRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_HEAP, H5E_BADTYPE, "unexpected 'huge' object record types %T, %T", udata, rec)
	}
	/* (the C routine returns the difference of the IDs cast to int) */
	return cmp.Compare(r1.id, r2.id), nil
}

// h5hfHugeBt2IndirDecode is H5HF__huge_bt2_indir_decode: decode raw disk
// form of record into native form.
func h5hfHugeBt2IndirDecode(f *File, ctx any, p *image) (any, error) {
	c := h5hfHugeBt2CtxOf(f, ctx)
	nrecord := &h5hfHugeBt2IndirRec{}

	/* Decode the record's fields */
	nrecord.addr = p.addrLen(int(c.sizeofAddr))
	nrecord.len = p.uintVar(int(c.sizeofSize))
	nrecord.id = p.uintVar(int(c.sizeofSize))
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}

// h5hfHugeBt2FiltIndirFound is H5HF__huge_bt2_filt_indir_found: retrieve
// record for indirectly accessed, filtered 'huge' object, when it's found in
// the v2 B-tree.
func h5hfHugeBt2FiltIndirFound(nrecord any, opData *h5hfHugeBt2FiltIndirRec) error {
	rec, ok := nrecord.(*h5hfHugeBt2FiltIndirRec)
	if !ok {
		return errorf(H5E_HEAP, H5E_BADTYPE, "unexpected 'huge' object record type %T", nrecord)
	}
	*opData = *rec
	return nil
}

// h5hfHugeBt2FiltIndirCompare is H5HF__huge_bt2_filt_indir_compare: compare
// the search record udata with the native record rec, by ID.
func h5hfHugeBt2FiltIndirCompare(f *File, ctx any, udata any, rec any) (int, error) {
	r1, ok1 := udata.(*h5hfHugeBt2FiltIndirRec)
	r2, ok2 := rec.(*h5hfHugeBt2FiltIndirRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_HEAP, H5E_BADTYPE, "unexpected 'huge' object record types %T, %T", udata, rec)
	}
	/* (the C routine returns the difference of the IDs cast to int) */
	return cmp.Compare(r1.id, r2.id), nil
}

// h5hfHugeBt2FiltIndirDecode is H5HF__huge_bt2_filt_indir_decode: decode
// raw disk form of record into native form.
func h5hfHugeBt2FiltIndirDecode(f *File, ctx any, p *image) (any, error) {
	c := h5hfHugeBt2CtxOf(f, ctx)
	nrecord := &h5hfHugeBt2FiltIndirRec{}

	/* Decode the record's fields */
	nrecord.addr = p.addrLen(int(c.sizeofAddr))
	nrecord.len = p.uintVar(int(c.sizeofSize))
	nrecord.filterMask = p.uint32()
	nrecord.objSize = p.uintVar(int(c.sizeofSize))
	nrecord.id = p.uintVar(int(c.sizeofSize))
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}

// h5hfHugeBt2DirCompare is H5HF__huge_bt2_dir_compare: compare the search
// record udata with the native record rec, by address and then length.
func h5hfHugeBt2DirCompare(f *File, ctx any, udata any, rec any) (int, error) {
	rec1, ok1 := udata.(*h5hfHugeBt2DirRec)
	rec2, ok2 := rec.(*h5hfHugeBt2DirRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_HEAP, H5E_BADTYPE, "unexpected 'huge' object record types %T, %T", udata, rec)
	}
	if rec1.addr < rec2.addr {
		return -1, nil
	} else if rec1.addr > rec2.addr {
		return 1, nil
	} else if rec1.len < rec2.len {
		return -1, nil
	} else if rec1.len > rec2.len {
		return 1, nil
	}
	return 0, nil
}

// h5hfHugeBt2DirDecode is H5HF__huge_bt2_dir_decode: decode raw disk form
// of record into native form.
func h5hfHugeBt2DirDecode(f *File, ctx any, p *image) (any, error) {
	c := h5hfHugeBt2CtxOf(f, ctx)
	nrecord := &h5hfHugeBt2DirRec{}

	/* Decode the record's fields */
	nrecord.addr = p.addrLen(int(c.sizeofAddr))
	nrecord.len = p.uintVar(int(c.sizeofSize))
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}

// h5hfHugeBt2FiltDirCompare is H5HF__huge_bt2_filt_dir_compare: compare the
// search record udata with the native record rec, by address and then
// length.
func h5hfHugeBt2FiltDirCompare(f *File, ctx any, udata any, rec any) (int, error) {
	rec1, ok1 := udata.(*h5hfHugeBt2FiltDirRec)
	rec2, ok2 := rec.(*h5hfHugeBt2FiltDirRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_HEAP, H5E_BADTYPE, "unexpected 'huge' object record types %T, %T", udata, rec)
	}
	if rec1.addr < rec2.addr {
		return -1, nil
	} else if rec1.addr > rec2.addr {
		return 1, nil
	} else if rec1.len < rec2.len {
		return -1, nil
	} else if rec1.len > rec2.len {
		return 1, nil
	}
	return 0, nil
}

// h5hfHugeBt2FiltDirDecode is H5HF__huge_bt2_filt_dir_decode: decode raw
// disk form of record into native form.
func h5hfHugeBt2FiltDirDecode(f *File, ctx any, p *image) (any, error) {
	c := h5hfHugeBt2CtxOf(f, ctx)
	nrecord := &h5hfHugeBt2FiltDirRec{}

	/* Decode the record's fields */
	nrecord.addr = p.addrLen(int(c.sizeofAddr))
	nrecord.len = p.uintVar(int(c.sizeofSize))
	nrecord.filterMask = p.uint32()
	nrecord.objSize = p.uintVar(int(c.sizeofSize))
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}
