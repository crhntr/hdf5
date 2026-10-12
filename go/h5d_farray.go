package hdf5

// Translated from src/H5Dfarray.c (read side only): the fixed array classes
// for dataset chunk indices (H5FA_CLS_CHUNK, H5FA_CLS_FILT_CHUNK), their
// context, fill and decode callbacks, and the index callbacks used to read a
// dataset (open, is_space_alloc, get_addr, iterate).
//
// Entry points for the dataset code:
//
//	(f *File) dFarrayIdxGetAddr(layout, pline, maxDims, scaled) (addr, nbytes, filterMask, err)
//	(f *File) dFarrayIdxIterate(layout, pline, maxDims, cb) error

import "errors"

/******************/
/* Local Typedefs */
/******************/

// h5dFarrayCtxUd is H5D_farray_ctx_ud_t: fixed array create/open user data.
type h5dFarrayCtxUd struct {
	f         *File  /* Pointer to file info */
	chunkSize uint32 /* Size of chunk (bytes) */
}

// h5dFarrayCtx is H5D_farray_ctx_t: fixed array callback context.
type h5dFarrayCtx struct {
	fileAddrLen  int /* Size of addresses in the file (bytes) */
	chunkSizeLen int /* Size of chunk sizes in the file (bytes) */
}

// h5dFarrayFiltElmt is H5D_farray_filt_elmt_t: native fixed array element
// for chunks w/filters.
type h5dFarrayFiltElmt struct {
	addr       haddr  /* Address of chunk */
	nbytes     uint32 /* Size of chunk (in file) */
	filterMask uint32 /* Excluded filters for chunk */
}

// h5dFarrayIdxInfo is H5D_chk_idx_info_t for a fixed array chunk index
// (with storage->u.farray folded in).
type h5dFarrayIdxInfo struct {
	f       *File
	pline   *h5oPline          /* I/O pipeline info */
	layout  *h5dChunkIdxLayout /* Chunk layout description */
	idxAddr haddr              /* storage->idx_addr: address of the fixed array header */
	fa      *h5fa              /* storage->u.farray.fa: the open fixed array */
}

/*****************************/
/* Library Private Variables */
/*****************************/

// h5dFarrayClsChunk is H5FA_CLS_CHUNK: fixed array class callbacks for
// dataset chunks w/o filters.
func h5dFarrayClsChunk() *h5faClass {
	return &h5faClass{
		id:     h5faClsChunk,        /* Type of fixed array */
		name:   "Chunk w/o filters", /* Name of fixed array class */
		fill:   h5dFarrayFill,       /* Fill block of missing elements callback */
		decode: h5dFarrayDecode,     /* Element decoding callback */
	}
}

// h5dFarrayClsFiltChunk is H5FA_CLS_FILT_CHUNK: fixed array class callbacks
// for dataset chunks w/filters.
func h5dFarrayClsFiltChunk() *h5faClass {
	return &h5faClass{
		id:     h5faClsFiltChunk,    /* Type of fixed array */
		name:   "Chunk w/filters",   /* Name of fixed array class */
		fill:   h5dFarrayFiltFill,   /* Fill block of missing elements callback */
		decode: h5dFarrayFiltDecode, /* Element decoding callback */
	}
}

// h5dFarrayCrtContext is H5D__farray_crt_context: create context for
// callbacks.
func h5dFarrayCrtContext(udata *h5dFarrayCtxUd) (*h5dFarrayCtx, error) {
	/* Sanity checks */
	if udata == nil || udata.f == nil || udata.chunkSize == 0 {
		return nil, errorf(H5E_DATASET, H5E_CANTCREATE, "can't create fixed array client callback context: chunk size is zero")
	}

	/* Allocate new context structure */
	ctx := &h5dFarrayCtx{}

	/* Initialize the context */
	ctx.fileAddrLen = int(udata.f.sizeofAddr)

	/* Compute the size required for encoding the size of a chunk, allowing
	 *      for an extra byte, in case the filter makes the chunk larger.
	 */
	ctx.chunkSizeLen = 1 + int((h5vmLog2Gen(uint64(udata.chunkSize))+8)/8)
	if ctx.chunkSizeLen > 8 {
		ctx.chunkSizeLen = 8
	}

	return ctx, nil
}

// h5dFarrayFill is H5D__farray_fill: the "missing element" value (an
// undefined chunk address).
func h5dFarrayFill(ctx any) any {
	return haddr(haddrUndef) /* H5D_FARRAY_FILL */
}

// h5dFarrayDecode is H5D__farray_decode: decode an element from "raw" to
// "native" form.
func h5dFarrayDecode(f *File, ctx any, p *image) (any, error) {
	c, ok := ctx.(*h5dFarrayCtx)
	if !ok {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "bad fixed array callback context")
	}

	/* Decode element */
	/* (advances 'raw' pointer) */
	elmt := p.addrLen(c.fileAddrLen)
	return elmt, p.done()
}

// h5dFarrayFiltFill is H5D__farray_filt_fill: the "missing element" value
// for filtered chunks.
func h5dFarrayFiltFill(ctx any) any {
	return h5dFarrayFiltElmt{addr: haddrUndef, nbytes: 0, filterMask: 0} /* H5D_FARRAY_FILT_FILL */
}

// h5dFarrayFiltDecode is H5D__farray_filt_decode: decode an element from
// "raw" to "native" form.
func h5dFarrayFiltDecode(f *File, ctx any, p *image) (any, error) {
	c, ok := ctx.(*h5dFarrayCtx)
	if !ok {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "bad fixed array callback context")
	}

	/* Decode element */
	/* (advances 'raw' pointer) */
	var elmt h5dFarrayFiltElmt
	elmt.addr = p.addrLen(c.fileAddrLen)
	elmt.nbytes = uint32(p.uintVar(c.chunkSizeLen))
	elmt.filterMask = p.uint32()
	return elmt, p.done()
}

// h5dFarrayIdxOpen is H5D__farray_idx_open: opens an existing fixed array.
func h5dFarrayIdxOpen(idxInfo *h5dFarrayIdxInfo) error {
	/* Check args */
	if !addrDefined(idxInfo.idxAddr) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "fixed array chunk index address is undefined")
	}

	/* Set up the user data */
	udata := h5dFarrayCtxUd{f: idxInfo.f, chunkSize: idxInfo.layout.size}

	/* (H5FA__hdr_init calls the class' crt_context with the user data) */
	ctx, err := h5dFarrayCrtContext(&udata)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't open fixed array")
	}

	/* (the C library picks the class from the id in the array header; the
	 *  array was created with the filtered class iff the dataset has
	 *  filters) */
	cls := h5dFarrayClsChunk()
	if idxInfo.pline != nil && idxInfo.pline.nused > 0 {
		cls = h5dFarrayClsFiltChunk()
	}

	/* Open the fixed array for the chunk index */
	fa, err := idxInfo.f.faOpen(idxInfo.idxAddr, cls, ctx)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't open fixed array")
	}
	idxInfo.fa = fa

	return nil
}

// h5dFarrayIdxIsSpaceAlloc is H5D__farray_idx_is_space_alloc: query if
// space is allocated for index method.
func h5dFarrayIdxIsSpaceAlloc(idxAddr haddr) bool {
	return addrDefined(idxAddr)
}

// h5dFarrayIdxGetAddr is H5D__farray_idx_get_addr: get the file address of
// a chunk if file space has been assigned. Returns the address (HADDR_UNDEF
// if the chunk is not allocated), the chunk size in the file and the filter
// mask, plus the chunk's index in the array.
func h5dFarrayIdxGetAddr(idxInfo *h5dFarrayIdxInfo, scaled []uint64) (addr haddr, length uint64, filterMask uint32, chunkIdx uint64, err error) {
	/* Check if the fixed array is open yet */
	if idxInfo.fa == nil {
		/* Open the fixed array in file */
		if err := h5dFarrayIdxOpen(idxInfo); err != nil {
			return haddrUndef, 0, 0, 0, wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open fixed array")
		}
	}

	/* Set convenience pointer to fixed array structure */
	fa := idxInfo.fa
	layout := idxInfo.layout
	if len(scaled) < layout.ndims-1 {
		return haddrUndef, 0, 0, 0, errorf(H5E_DATASET, H5E_BADVALUE, "chunk coordinates have rank %d, want %d", len(scaled), layout.ndims-1)
	}

	/* Calculate the index of this chunk */
	idx := h5vmArrayOffsetPre(layout.ndims-1, layout.maxDownChunks, scaled)

	chunkIdx = idx

	/* Check for filters on chunks */
	if idxInfo.pline != nil && idxInfo.pline.nused > 0 {
		/* Get the information for the chunk */
		v, err := fa.get(idx)
		if err != nil {
			return haddrUndef, 0, 0, chunkIdx, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get chunk info")
		}
		elmt, ok := v.(h5dFarrayFiltElmt)
		if !ok {
			return haddrUndef, 0, 0, chunkIdx, errorf(H5E_DATASET, H5E_CANTGET, "can't get chunk info: unexpected element type %T", v)
		}

		/* Set the info for the chunk */
		addr = elmt.addr
		length = uint64(elmt.nbytes)
		filterMask = elmt.filterMask
	} else {
		/* Get the address for the chunk */
		v, err := fa.get(idx)
		if err != nil {
			return haddrUndef, 0, 0, chunkIdx, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get chunk address")
		}
		a, ok := v.(haddr)
		if !ok {
			return haddrUndef, 0, 0, chunkIdx, errorf(H5E_DATASET, H5E_CANTGET, "can't get chunk address: unexpected element type %T", v)
		}
		addr = a

		/* Update the other (constant) information for the chunk */
		length = uint64(layout.size)
		filterMask = 0
	}

	if !addrDefined(addr) {
		length = 0
	}

	return addr, length, filterMask, chunkIdx, nil
}

// h5dFarrayItUd is H5D_farray_it_ud_t: fixed Array callback info for
// iteration over chunks.
type h5dFarrayItUd struct {
	layout   *h5dChunkIdxLayout           /* common.layout */
	chunkRec h5dChunkRec                  /* Generic chunk record for callback */
	filtered bool                         /* Whether the chunks are filtered */
	cb       func(rec *h5dChunkRec) error /* Chunk callback routine */
}

// h5dFarrayIdxIterateCb is H5D__farray_idx_iterate_cb: callback routine for
// fixed array element iteration.
func h5dFarrayIdxIterateCb(elmt any, udata *h5dFarrayItUd) error {
	/* Compose generic chunk record for callback */
	if udata.filtered {
		filtElmt, ok := elmt.(h5dFarrayFiltElmt)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected fixed array element type %T", elmt)
		}
		udata.chunkRec.chunkAddr = filtElmt.addr
		udata.chunkRec.nbytes = filtElmt.nbytes
		udata.chunkRec.filterMask = filtElmt.filterMask
	} else {
		a, ok := elmt.(haddr)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected fixed array element type %T", elmt)
		}
		udata.chunkRec.chunkAddr = a
	}

	/* Make "generic chunk" callback */
	if addrDefined(udata.chunkRec.chunkAddr) {
		if err := udata.cb(&udata.chunkRec); err != nil {
			if errors.Is(err, errBIterStop) {
				return err
			}
			return wrapf(err, H5E_DATASET, H5E_CALLBACK, "failure in generic chunk iterator callback")
		}
	}

	/* Update coordinates of chunk in dataset */
	ndims := udata.layout.ndims - 1
	currDim := ndims - 1
	for currDim >= 0 {
		/* Increment coordinate in current dimension */
		udata.chunkRec.scaled[currDim]++

		/* Check if we went off the end of the current dimension */
		if udata.chunkRec.scaled[currDim] >= udata.layout.maxChunks[currDim] {
			/* Reset coordinate & move to next faster dimension */
			udata.chunkRec.scaled[currDim] = 0
			currDim--
		} else {
			break
		}
	}

	return nil
}

// h5dFarrayIdxIterate is H5D__farray_idx_iterate: iterate over the chunks in
// an index, making a callback for each one (for allocated chunks only).
// The callback may return errBIterStop to stop early without error.
func h5dFarrayIdxIterate(idxInfo *h5dFarrayIdxInfo, chunkCb func(rec *h5dChunkRec) error) error {
	/* Check if the fixed array is open yet */
	if idxInfo.fa == nil {
		/* Open the fixed array in file */
		if err := h5dFarrayIdxOpen(idxInfo); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open fixed array")
		}
	}

	/* Set convenience pointer to fixed array structure */
	fa := idxInfo.fa

	/* Get the fixed array statistics */
	faStat := fa.getStats()

	/* Check if there are any array elements */
	/* (without a data block every element is the fill value, i.e. an
	 *  undefined chunk address: nothing to visit, and nelmts is not bounded
	 *  by the file size) */
	if faStat.nelmts > 0 && addrDefined(fa.hdr.dblkAddr) {
		/* Initialize userdata */
		udata := h5dFarrayItUd{layout: idxInfo.layout}
		udata.chunkRec.scaled = make([]uint64, idxInfo.layout.ndims-1)
		udata.filtered = idxInfo.pline != nil && idxInfo.pline.nused > 0
		if !udata.filtered {
			udata.chunkRec.nbytes = uint32(idxInfo.layout.size)
			udata.chunkRec.filterMask = 0
		}
		udata.cb = chunkCb

		/* Iterate over the fixed array elements */
		if err := fa.iterate(func(_ uint64, elmt any) error {
			return h5dFarrayIdxIterateCb(elmt, &udata)
		}); err != nil {
			return wrapf(err, H5E_DATASET, H5E_BADITER, "unable to iterate over fixed array chunk index")
		}
	}

	return nil
}

// dFarrayIdxSetup builds the index info for a dataset whose chunks are
// indexed by a fixed array: the layout message, the I/O pipeline (nil or
// empty when unfiltered) and the dataspace's maximum dimensions (the
// max_chunks / max_down_chunks part of H5D__chunk_set_info_real).
func (f *File) dFarrayIdxSetup(layout *h5oLayout, pline *h5oPline, maxDims []uint64) (*h5dFarrayIdxInfo, error) {
	if layout == nil || layout.typ != h5dChunked || layout.chunkIdxType != h5dChunkIdxFarray {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "layout does not use a fixed array chunk index")
	}
	l, err := h5dChunkIdxLayoutInit(layout, maxDims)
	if err != nil {
		return nil, err
	}
	return &h5dFarrayIdxInfo{f: f, pline: pline, layout: l, idxAddr: layout.chunkIdxAddr}, nil
}

// dFarrayIdxGetAddr looks up a chunk of a dataset whose chunks are indexed
// by a fixed array (H5D__farray_idx_get_addr). scaled are the chunk's
// coordinates divided by the chunk dimensions (rank entries); maxDims are
// the dataspace's maximum dimensions (the current dimensions when the
// dataspace has no maximum). An unallocated chunk yields addr ==
// HADDR_UNDEF and nbytes == 0. For an unfiltered dataset nbytes is the full
// chunk size and filterMask is 0.
func (f *File) dFarrayIdxGetAddr(layout *h5oLayout, pline *h5oPline, maxDims []uint64, scaled []uint64) (addr haddr, nbytes uint64, filterMask uint32, err error) {
	idxInfo, err := f.dFarrayIdxSetup(layout, pline, maxDims)
	if err != nil {
		return haddrUndef, 0, 0, err
	}
	addr, nbytes, filterMask, _, err = h5dFarrayIdxGetAddr(idxInfo, scaled)
	return addr, nbytes, filterMask, err
}

// dFarrayIdxIterate calls cb for every allocated chunk of a dataset whose
// chunks are indexed by a fixed array (H5D__farray_idx_iterate), in index
// order, with the chunk's scaled coordinates (a fresh slice), address, size
// in the file and filter mask. cb may return errBIterStop to stop early
// without error.
func (f *File) dFarrayIdxIterate(layout *h5oLayout, pline *h5oPline, maxDims []uint64, cb func(scaled []uint64, addr haddr, nbytes uint64, filterMask uint32) error) error {
	idxInfo, err := f.dFarrayIdxSetup(layout, pline, maxDims)
	if err != nil {
		return err
	}
	err = h5dFarrayIdxIterate(idxInfo, func(rec *h5dChunkRec) error {
		return cb(append([]uint64(nil), rec.scaled...), rec.chunkAddr, uint64(rec.nbytes), rec.filterMask)
	})
	if errors.Is(err, errBIterStop) {
		return nil
	}
	return err
}
