package hdf5

// Translated from src/H5Dbtree2.c (read side only): the v2 B-tree record
// classes for chunked datasets indexed with a v2 B-tree (H5D_BT2, B-tree
// type 10, non-filtered; H5D_BT2_FILT, type 11, filtered), their callback
// context, and H5D__bt2_idx_open.
//
// Usage: h5dBt2IdxOpen(f, layout) opens the chunk index of a dataset whose
// layout message says H5D_CHUNK_IDX_BT2 (it builds the context exactly as
// H5D__bt2_idx_open does and picks the class from the B-tree header). The
// records are *h5dChunkRec values; to look up a chunk, call
//
//	found, err := bt2.find(&h5dBt2Ud{rec: h5dChunkRec{scaled: scaled}, ndims: ctx.ndims}, op)
//
// with the chunk's scaled coordinates (one per dataset dimension).

// h5dBt2CtxUd is H5D_bt2_ctx_ud_t: user data for creating the callback
// context (the file is passed to h5dBt2CrtContext directly).
type h5dBt2CtxUd struct {
	chunkSize uint32   /* Size of chunk (bytes; for filtered object) */
	ndims     uint32   /* Number of dimensions */
	dim       []uint32 /* Size of chunk in elements */
}

// h5dBt2Ctx is H5D_bt2_ctx_t: the callback context.
type h5dBt2Ctx struct {
	chunkSize    uint32   /* Size of chunk (bytes; constant for unfiltered object) */
	sizeofAddr   uint8    /* Size of file addresses in the file (bytes) */
	chunkSizeLen int      /* Size of chunk sizes in the file (bytes) */
	ndims        uint32   /* Number of dimensions in chunk */
	dim          []uint32 /* Size of chunk in elements */
}

// h5dChunkRec is H5D_chunk_rec_t (src/H5Dpkg.h): the native record of a
// chunk index.
type h5dChunkRec struct {
	scaled     []uint64 /* Logical offset to start (one per dimension) */
	nbytes     uint32   /* Size of stored data */
	filterMask uint32   /* Excluded filters */
	chunkAddr  haddr    /* Address of chunk in file */
}

// h5dBt2Ud is H5D_bt2_ud_t: user data for compare callback.
type h5dBt2Ud struct {
	rec   h5dChunkRec /* The record to search for */
	ndims uint32      /* Number of dimensions for the chunked dataset */
}

// h5dBt2 is H5D_BT2: v2 B-tree class for indexing non-filtered chunked
// datasets.
func h5dBt2() *h5b2Class {
	return &h5b2Class{
		id:      h5b2CdsetID,        /* Type of B-tree */
		compare: h5dBt2Compare,      /* Record comparison callback */
		decode:  h5dBt2UnfiltDecode, /* Record decoding callback */
	}
}

// h5dBt2Filt is H5D_BT2_FILT: v2 B-tree class for indexing filtered
// chunked datasets.
func h5dBt2Filt() *h5b2Class {
	return &h5b2Class{
		id:      h5b2CdsetFiltID,  /* Type of B-tree */
		compare: h5dBt2Compare,    /* Record comparison callback */
		decode:  h5dBt2FiltDecode, /* Record decoding callback */
	}
}

// h5dBt2CrtContext is H5D__bt2_crt_context: create client callback context.
func h5dBt2CrtContext(f *File, udata *h5dBt2CtxUd) (*h5dBt2Ctx, error) {
	/* Sanity check */
	if udata == nil || udata.ndims == 0 || udata.ndims >= h5oLayoutNdims {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "invalid chunk dimensionality for v2 B-tree chunk index")
	}

	/* Allocate callback context */
	ctx := &h5dBt2Ctx{}

	/* Determine the size of addresses and set the chunk size and # of dimensions for the dataset */
	ctx.sizeofAddr = f.sizeofAddr
	ctx.chunkSize = udata.chunkSize
	ctx.ndims = udata.ndims

	/* Set up the "local" information for this dataset's chunk dimension sizes */
	myDim := make([]uint32, h5oLayoutNdims)
	copy(myDim, udata.dim)
	ctx.dim = myDim

	/*
	 * Compute the size required for encoding the size of a chunk,
	 * allowing for an extra byte, in case the filter makes the chunk larger.
	 */
	ctx.chunkSizeLen = 1 + ((int(h5hfLog2Gen(uint64(udata.chunkSize))) + 8) / 8)
	if ctx.chunkSizeLen > 8 {
		ctx.chunkSizeLen = 8
	}
	return ctx, nil
}

// h5dBt2CtxOf returns the context passed to a record callback.
func h5dBt2CtxOf(ctx any) (*h5dBt2Ctx, error) {
	c, ok := ctx.(*h5dBt2Ctx)
	if !ok || c == nil {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "missing v2 B-tree chunk index context (%T)", ctx)
	}
	return c, nil
}

// h5dBt2Compare is H5D__bt2_compare: compare two native information
// records, according to some key. udata is a *h5dBt2Ud, rec2 a
// *h5dChunkRec.
func h5dBt2Compare(f *File, ctx any, udata any, rec2 any) (int, error) {
	ud, ok1 := udata.(*h5dBt2Ud)
	r2, ok2 := rec2.(*h5dChunkRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_DATASET, H5E_BADTYPE, "unexpected chunk record types %T, %T", udata, rec2)
	}
	rec1 := &ud.rec /* The search record */

	/* Compare the offsets but ignore the other fields */
	if int(ud.ndims) > len(rec1.scaled) || int(ud.ndims) > len(r2.scaled) {
		return 0, errorf(H5E_DATASET, H5E_BADVALUE, "chunk record has fewer than %d scaled offsets", ud.ndims)
	}
	/* (H5VM_vector_cmp_u(udata->ndims, rec1->scaled, rec2->scaled)) */
	for u := uint32(0); u < ud.ndims; u++ {
		if rec1.scaled[u] < r2.scaled[u] {
			return -1, nil
		}
		if rec1.scaled[u] > r2.scaled[u] {
			return 1, nil
		}
	}
	return 0, nil
}

// h5dBt2UnfiltDecode is H5D__bt2_unfilt_decode: decode raw disk form of
// record into native form (non-filtered).
func h5dBt2UnfiltDecode(f *File, ctx any, p *image) (any, error) {
	c, err := h5dBt2CtxOf(ctx)
	if err != nil {
		return nil, err
	}
	record := &h5dChunkRec{}

	/* Decode the record's fields */
	record.chunkAddr = p.addrLen(int(c.sizeofAddr))
	record.nbytes = c.chunkSize
	record.filterMask = 0
	record.scaled = make([]uint64, c.ndims)
	for u := range record.scaled {
		record.scaled[u] = p.uint64()
	}
	if err := p.done(); err != nil {
		return nil, err
	}
	return record, nil
}

// h5dBt2FiltDecode is H5D__bt2_filt_decode: decode raw disk form of record
// into native form (filtered).
func h5dBt2FiltDecode(f *File, ctx any, p *image) (any, error) {
	c, err := h5dBt2CtxOf(ctx)
	if err != nil {
		return nil, err
	}
	record := &h5dChunkRec{}

	/* Decode the record's fields */
	record.chunkAddr = p.addrLen(int(c.sizeofAddr))
	record.nbytes = uint32(p.uintVar(c.chunkSizeLen))
	record.filterMask = p.uint32()
	record.scaled = make([]uint64, c.ndims)
	for u := range record.scaled {
		record.scaled[u] = p.uint64()
	}
	if err := p.done(); err != nil {
		return nil, err
	}

	/* Sanity checks */
	if !addrDefined(record.chunkAddr) {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "filtered chunk record has an undefined address")
	}
	if record.nbytes == 0 {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "filtered chunk record has a zero size")
	}
	return record, nil
}

// h5dBt2IdxOpen is H5D__bt2_idx_open: opens an existing v2 B-tree chunk
// index. It returns the opened tree and the callback context (whose ndims
// is the number of scaled offsets in each record).
//
// The C library selects the record class from the type stored in the
// B-tree header (H5B2_client_class_g); this does the same.
func h5dBt2IdxOpen(f *File, layout *h5oLayout) (*h5b2, *h5dBt2Ctx, error) {
	/* Check args */
	if layout == nil || layout.chunkIdxType != h5dChunkIdxBt2 {
		return nil, nil, errorf(H5E_DATASET, H5E_BADVALUE, "layout does not use a v2 B-tree chunk index")
	}
	if !addrDefined(layout.chunkIdxAddr) {
		return nil, nil, errorf(H5E_DATASET, H5E_BADVALUE, "v2 B-tree chunk index address is undefined")
	}
	if layout.chunkNdims < 2 || int(layout.chunkNdims) > len(layout.chunkDim) {
		return nil, nil, errorf(H5E_DATASET, H5E_BADVALUE, "invalid chunk dimensionality %d", layout.chunkNdims)
	}

	/* Set up the user data */
	/* (layout->u.chunk.size: product of the chunk dimensions, as computed
	 *  in uint32_t by H5O__layout_decode) */
	var uCtx h5dBt2CtxUd
	uCtx.ndims = layout.chunkNdims - 1
	uCtx.chunkSize = 1
	uCtx.dim = make([]uint32, layout.chunkNdims)
	for u := uint32(0); u < layout.chunkNdims; u++ {
		uCtx.dim[u] = uint32(layout.chunkDim[u])
		uCtx.chunkSize *= uCtx.dim[u]
	}
	ctx, err := h5dBt2CrtContext(f, &uCtx)
	if err != nil {
		return nil, nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to create v2 B-tree client callback context")
	}

	/* Open v2 B-tree for the chunk index */
	hdr, err := h5b2HdrProtect(f, layout.chunkIdxAddr)
	if err != nil {
		return nil, nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't open v2 B-tree for tracking chunked dataset")
	}
	var cls *h5b2Class
	switch hdr.typ {
	case h5b2CdsetID:
		cls = h5dBt2()
	case h5b2CdsetFiltID:
		cls = h5dBt2Filt()
	default:
		return nil, nil, errorf(H5E_DATASET, H5E_BADTYPE, "v2 B-tree type %d is not a chunk index", hdr.typ)
	}
	bt2, err := f.b2Open(layout.chunkIdxAddr, cls, ctx)
	if err != nil {
		return nil, nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't open v2 B-tree for tracking chunked dataset")
	}
	return bt2, ctx, nil
}
