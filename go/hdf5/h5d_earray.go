package hdf5

// Translated from src/H5Dearray.c (read side only): the extensible array
// classes for dataset chunk indices (H5EA_CLS_CHUNK, H5EA_CLS_FILT_CHUNK),
// their context, fill and decode callbacks, and the index callbacks used to
// read a dataset (init, open, is_space_alloc, get_addr, resize, iterate).
//
// The chunk index callbacks take an h5dEarrayIdxInfo, the counterpart of
// H5D_chk_idx_info_t. The parts of H5O_layout_chunk_t they use that are not
// stored in the layout message (the # of chunks in each maximum dimension,
// the unlimited dimension, the swizzled sizes) are computed from the
// dataspace's maximum dimensions by h5dChunkIdxLayoutInit.
//
// Entry points for the dataset code:
//
//	(f *File) dEarrayIdxGetAddr(layout, pline, maxDims, scaled) (addr, nbytes, filterMask, err)
//	(f *File) dEarrayIdxIterate(layout, pline, maxDims, cb) error

import "errors"

/****************/
/* Local Macros */
/****************/

// h5sUnlimitedDim is H5S_UNLIMITED: the maximum dimension size of an
// unlimited dimension (HSIZE_UNDEF).
const h5sUnlimitedDim = ^uint64(0)

/******************/
/* Local Typedefs */
/******************/

// h5dEarrayCtxUd is H5D_earray_ctx_ud_t: extensible array create/open user
// data.
type h5dEarrayCtxUd struct {
	f         *File  /* Pointer to file info */
	chunkSize uint32 /* Size of chunk (bytes) */
}

// h5dEarrayCtx is H5D_earray_ctx_t: extensible array callback context.
type h5dEarrayCtx struct {
	fileAddrLen  int /* Size of addresses in the file (bytes) */
	chunkSizeLen int /* Size of chunk sizes in the file (bytes) */
}

// h5dEarrayFiltElmt is H5D_earray_filt_elmt_t: native extensible array
// element for chunks w/filters.
type h5dEarrayFiltElmt struct {
	addr       haddr  /* Address of chunk */
	nbytes     uint32 /* Size of chunk (in file) */
	filterMask uint32 /* Excluded filters for chunk */
}

// h5dChunkRec is H5D_chunk_rec_t: generic chunk record (as passed to a
// chunk index iteration callback).
type h5dChunkRec struct {
	scaled     []uint64 /* Logical offset to start (scaled by chunk dims) */
	nbytes     uint64   /* Size of stored data */
	filterMask uint32   /* Excluded filters */
	chunkAddr  haddr    /* Address of chunk in file */
}

// h5dChunkIdxLayout holds the parts of H5O_layout_chunk_t (src/H5Oprivate.h)
// used by the fixed and extensible array chunk index callbacks: the chunk
// dimensions and size from the layout message, and the values computed when
// the dataset is opened (H5D__chunk_set_info_real, H5D__earray_idx_init,
// H5D__earray_idx_resize).
type h5dChunkIdxLayout struct {
	ndims         int      /* Num dimensions in chunk (rank + 1) */
	dim           []uint32 /* Size of chunk in elements (ndims entries, last = datatype size) */
	size          uint32   /* Size of chunk in bytes */
	maxChunks     []uint64 /* # of chunks in each dataset's max. dimension */
	maxDownChunks []uint64 /* "down" size of number of chunks in each max dim */

	// u.earray
	unlimDim              int      /* Rank of unlimited dimension for dataset */
	swizzledDim           []uint32 /* swizzled chunk dimensions */
	swizzledMaxChunks     []uint64 /* swizzled max # of chunks in each dimension (local in H5D__earray_idx_resize) */
	swizzledMaxDownChunks []uint64 /* swizzled max "down" size of number of chunks in each dimension */
}

// h5dChunkIdxLayoutInit computes an h5dChunkIdxLayout from a chunked layout
// message and the dataspace's maximum dimensions (the max_chunks /
// max_down_chunks part of H5D__chunk_set_info_real in src/H5Dchunk.c, plus
// layout->u.chunk.size as H5O__layout_decode computes it).
func h5dChunkIdxLayoutInit(layout *h5oLayout, maxDims []uint64) (*h5dChunkIdxLayout, error) {
	if layout == nil || layout.typ != h5dChunked {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "not a chunked layout")
	}
	ndims := int(layout.chunkNdims)
	if ndims < 2 || ndims > h5oLayoutNdims || len(layout.chunkDim) < ndims {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "bad # of chunk dimensions %d", layout.chunkNdims)
	}
	rank := ndims - 1

	/* Can happen when corrupt files are parsed */
	if len(maxDims) != rank {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "dataspace rank %d does not match chunk rank %d", len(maxDims), rank)
	}

	l := &h5dChunkIdxLayout{
		ndims:         ndims,
		dim:           make([]uint32, ndims),
		maxChunks:     make([]uint64, rank),
		maxDownChunks: make([]uint64, rank),
	}
	l.size = 1
	for u := 0; u < ndims; u++ {
		if layout.chunkDim[u] == 0 || layout.chunkDim[u] > 0xffffffff {
			return nil, errorf(H5E_DATASET, H5E_BADVALUE, "chunk dimension %d has bad size %d", u, layout.chunkDim[u])
		}
		l.dim[u] = uint32(layout.chunkDim[u])
		l.size *= l.dim[u]
	}

	/* Compute the # of chunks in dataset dimensions */
	for u := 0; u < rank; u++ {
		/* Round up to the next integer # of chunks, to accommodate partial chunks */
		if maxDims[u] == h5sUnlimitedDim {
			l.maxChunks[u] = h5sUnlimitedDim
		} else {
			l.maxChunks[u] = maxDims[u] / uint64(l.dim[u])
			if maxDims[u]%uint64(l.dim[u]) != 0 {
				l.maxChunks[u]++
			}
		}
	}

	/* Get the "down" sizes for each dimension */
	h5vmArrayDown(rank, l.maxChunks, l.maxDownChunks)

	return l, nil
}

// h5dEarrayIdxInfo is H5D_chk_idx_info_t for an extensible array chunk
// index (with storage->u.earray folded in).
type h5dEarrayIdxInfo struct {
	f       *File
	pline   *h5oPline          /* I/O pipeline info */
	layout  *h5dChunkIdxLayout /* Chunk layout description */
	idxAddr haddr              /* storage->idx_addr: address of the extensible array header */
	ea      *h5ea              /* storage->u.earray.ea: the open extensible array */
}

/*****************************/
/* Library Private Variables */
/*****************************/

// h5dEarrayClsChunk is H5EA_CLS_CHUNK: extensible array class callbacks for
// dataset chunks w/o filters.
func h5dEarrayClsChunk() *h5eaClass {
	return &h5eaClass{
		id:     h5eaClsChunk,        /* Type of extensible array */
		name:   "Chunk w/o filters", /* Name of extensible array class */
		fill:   h5dEarrayFill,       /* Fill block of missing elements callback */
		decode: h5dEarrayDecode,     /* Element decoding callback */
	}
}

// h5dEarrayClsFiltChunk is H5EA_CLS_FILT_CHUNK: extensible array class
// callbacks for dataset chunks w/filters.
func h5dEarrayClsFiltChunk() *h5eaClass {
	return &h5eaClass{
		id:     h5eaClsFiltChunk,    /* Type of extensible array */
		name:   "Chunk w/filters",   /* Name of extensible array class */
		fill:   h5dEarrayFiltFill,   /* Fill block of missing elements callback */
		decode: h5dEarrayFiltDecode, /* Element decoding callback */
	}
}

// h5dEarrayCrtContext is H5D__earray_crt_context: create context for
// callbacks.
func h5dEarrayCrtContext(udata *h5dEarrayCtxUd) (*h5dEarrayCtx, error) {
	/* Sanity checks */
	if udata == nil || udata.f == nil || udata.chunkSize == 0 {
		return nil, errorf(H5E_DATASET, H5E_CANTCREATE, "can't create extensible array client callback context: chunk size is zero")
	}

	/* Allocate new context structure */
	ctx := &h5dEarrayCtx{}

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

// h5dEarrayFill is H5D__earray_fill: the "missing element" value (an
// undefined chunk address).
func h5dEarrayFill(ctx any) any {
	return haddr(haddrUndef) /* H5D_EARRAY_FILL */
}

// h5dEarrayDecode is H5D__earray_decode: decode an element from "raw" to
// "native" form.
func h5dEarrayDecode(f *File, ctx any, p *image) (any, error) {
	c, ok := ctx.(*h5dEarrayCtx)
	if !ok {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "bad extensible array callback context")
	}

	/* Decode element */
	/* (advances 'raw' pointer) */
	elmt := p.addrLen(c.fileAddrLen)
	return elmt, p.done()
}

// h5dEarrayFiltFill is H5D__earray_filt_fill: the "missing element" value
// for filtered chunks.
func h5dEarrayFiltFill(ctx any) any {
	return h5dEarrayFiltElmt{addr: haddrUndef, nbytes: 0, filterMask: 0} /* H5D_EARRAY_FILT_FILL */
}

// h5dEarrayFiltDecode is H5D__earray_filt_decode: decode an element from
// "raw" to "native" form.
func h5dEarrayFiltDecode(f *File, ctx any, p *image) (any, error) {
	c, ok := ctx.(*h5dEarrayCtx)
	if !ok {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "bad extensible array callback context")
	}

	/* Decode element */
	/* (advances 'raw' pointer) */
	var elmt h5dEarrayFiltElmt
	elmt.addr = p.addrLen(c.fileAddrLen)
	elmt.nbytes = uint32(p.uintVar(c.chunkSizeLen))
	elmt.filterMask = p.uint32()
	return elmt, p.done()
}

// h5dEarrayIdxInit is H5D__earray_idx_init: initialize the indexing
// information for a dataset (find the unlimited dimension from the
// dataspace's maximum dimensions).
func h5dEarrayIdxInit(layout *h5dChunkIdxLayout, maxDims []uint64) error {
	/* Find the rank of the unlimited dimension */
	unlimDim := -1
	for u := range maxDims {
		/* Check for unlimited dimension */
		if maxDims[u] == h5sUnlimitedDim {
			/* Check if we've already found an unlimited dimension */
			if unlimDim >= 0 {
				return errorf(H5E_DATASET, H5E_ALREADYINIT, "already found unlimited dimension")
			}

			/* Set the unlimited dimension */
			unlimDim = u
		}
	}

	/* Check if we didn't find an unlimited dimension */
	if unlimDim < 0 {
		return errorf(H5E_DATASET, H5E_UNINITIALIZED, "didn't find unlimited dimension")
	}

	/* Set the unlimited dimension for the layout's future use */
	layout.unlimDim = unlimDim

	return nil
}

// h5dEarrayIdxOpen is H5D__earray_idx_open: opens an existing extensible
// array.
func h5dEarrayIdxOpen(idxInfo *h5dEarrayIdxInfo) error {
	/* Check args */
	if !addrDefined(idxInfo.idxAddr) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "extensible array chunk index address is undefined")
	}

	/* Set up the user data */
	udata := h5dEarrayCtxUd{f: idxInfo.f, chunkSize: idxInfo.layout.size}

	/* (H5EA__hdr_init calls the class' crt_context with the user data) */
	ctx, err := h5dEarrayCrtContext(&udata)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't open extensible array")
	}

	/* (the C library picks the class from the id in the array header; the
	 *  array was created with the filtered class iff the dataset has
	 *  filters) */
	cls := h5dEarrayClsChunk()
	if idxInfo.pline != nil && idxInfo.pline.nused > 0 {
		cls = h5dEarrayClsFiltChunk()
	}

	/* Open the extensible array for the chunk index */
	ea, err := idxInfo.f.eaOpen(idxInfo.idxAddr, cls, ctx)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't open extensible array")
	}
	idxInfo.ea = ea

	return nil
}

// h5dEarrayIdxIsSpaceAlloc is H5D__earray_idx_is_space_alloc: query if
// space is allocated for index method.
func h5dEarrayIdxIsSpaceAlloc(idxAddr haddr) bool {
	return addrDefined(idxAddr)
}

// h5dEarrayIdxGetAddr is H5D__earray_idx_get_addr: get the file address of
// a chunk if file space has been assigned. Returns the address (HADDR_UNDEF
// if the chunk is not allocated), the chunk size in the file and the filter
// mask, plus the chunk's index in the array.
func h5dEarrayIdxGetAddr(idxInfo *h5dEarrayIdxInfo, scaled []uint64) (addr haddr, length uint64, filterMask uint32, chunkIdx uint64, err error) {
	/* Check if the extensible array is open yet */
	if idxInfo.ea == nil {
		/* Open the extensible array in file */
		if err := h5dEarrayIdxOpen(idxInfo); err != nil {
			return haddrUndef, 0, 0, 0, wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open extensible array")
		}
	}

	/* Set convenience pointer to extensible array structure */
	ea := idxInfo.ea
	layout := idxInfo.layout
	ndims := layout.ndims - 1 /* Number of dimensions */
	if len(scaled) < ndims {
		return haddrUndef, 0, 0, 0, errorf(H5E_DATASET, H5E_BADVALUE, "chunk coordinates have rank %d, want %d", len(scaled), ndims)
	}

	/* Check for unlimited dim. not being the slowest-changing dim. */
	var idx uint64 /* Array index of chunk */
	if layout.unlimDim > 0 {
		swizzledCoords := make([]uint64, ndims) /* swizzled chunk coordinates */

		/* Compute coordinate offset from scaled offset */
		for u := 0; u < ndims; u++ {
			swizzledCoords[u] = scaled[u] * uint64(layout.dim[u])
		}

		h5vmSwizzleCoords(swizzledCoords, layout.unlimDim)

		/* Calculate the index of this chunk */
		idx = h5vmChunkIndex(ndims, swizzledCoords, layout.swizzledDim, layout.swizzledMaxDownChunks)
	} else {
		/* Calculate the index of this chunk */
		idx = h5vmArrayOffsetPre(ndims, layout.maxDownChunks, scaled)
	}

	chunkIdx = idx

	/* Check for filters on chunks */
	if idxInfo.pline != nil && idxInfo.pline.nused > 0 {
		/* Get the information for the chunk */
		v, err := ea.get(idx)
		if err != nil {
			return haddrUndef, 0, 0, chunkIdx, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get chunk info")
		}
		elmt, ok := v.(h5dEarrayFiltElmt)
		if !ok {
			return haddrUndef, 0, 0, chunkIdx, errorf(H5E_DATASET, H5E_CANTGET, "can't get chunk info: unexpected element type %T", v)
		}

		/* Set the info for the chunk */
		addr = elmt.addr
		length = uint64(elmt.nbytes)
		filterMask = elmt.filterMask
	} else {
		/* Get the address for the chunk */
		v, err := ea.get(idx)
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

// h5dEarrayIdxResize is H5D__earray_idx_resize: calculate/setup the
// swizzled down chunk array, used for chunk index calculations. (The
// swizzled "down" sizes of the current # of chunks are not computed: they
// are not used when reading.)
func h5dEarrayIdxResize(layout *h5dChunkIdxLayout) {
	/* "Swizzle" constant dimensions for this dataset */
	if layout.unlimDim > 0 {
		ndims := layout.ndims - 1

		/* Get the swizzled chunk dimensions */
		layout.swizzledDim = make([]uint32, ndims)
		copy(layout.swizzledDim, layout.dim[:ndims])
		h5vmSwizzleCoords(layout.swizzledDim, layout.unlimDim)

		/* Get the swizzled max number of chunks in each dimension */
		layout.swizzledMaxChunks = make([]uint64, ndims)
		copy(layout.swizzledMaxChunks, layout.maxChunks)
		h5vmSwizzleCoords(layout.swizzledMaxChunks, layout.unlimDim)

		/* Get the swizzled max "down" sizes for each dimension */
		layout.swizzledMaxDownChunks = make([]uint64, ndims)
		h5vmArrayDown(ndims, layout.swizzledMaxChunks, layout.swizzledMaxDownChunks)
	}
}

// h5dEarrayItUd is H5D_earray_it_ud_t: extensible Array callback info for
// iteration over chunks.
type h5dEarrayItUd struct {
	layout   *h5dChunkIdxLayout           /* common.layout */
	chunkRec h5dChunkRec                  /* Generic chunk record for callback */
	filtered bool                         /* Whether the chunks are filtered */
	cb       func(rec *h5dChunkRec) error /* Chunk callback routine */
}

// h5dEarrayIdxIterateCb is H5D__earray_idx_iterate_cb: callback routine for
// extensible array element iteration.
func h5dEarrayIdxIterateCb(elmt any, udata *h5dEarrayItUd) error {
	/* Compose generic chunk record for callback */
	if udata.filtered {
		filtElmt, ok := elmt.(h5dEarrayFiltElmt)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected extensible array element type %T", elmt)
		}
		udata.chunkRec.chunkAddr = filtElmt.addr
		udata.chunkRec.nbytes = uint64(filtElmt.nbytes)
		udata.chunkRec.filterMask = filtElmt.filterMask
	} else {
		a, ok := elmt.(haddr)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected extensible array element type %T", elmt)
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
	scaled := udata.chunkRec.scaled
	maxChunks := udata.layout.maxChunks
	if udata.layout.unlimDim > 0 {
		/* (Deviation from the C code, which steps the unswizzled
		 *  coordinates: the array is ordered by the swizzled coordinates
		 *  when the unlimited dimension is not the slowest-changing one, so
		 *  step those and unswizzle the result. With the C stepping the
		 *  coordinates of all but the first chunks are wrong.) */
		h5vmSwizzleCoords(scaled, udata.layout.unlimDim)
		maxChunks = udata.layout.swizzledMaxChunks
	}
	currDim := ndims - 1
	for currDim >= 0 {
		/* Increment coordinate in current dimension */
		scaled[currDim]++

		/* Check if we went off the end of the current dimension */
		if scaled[currDim] >= maxChunks[currDim] {
			/* Reset coordinate & move to next faster dimension */
			scaled[currDim] = 0
			currDim--
		} else {
			break
		}
	}
	if udata.layout.unlimDim > 0 {
		h5vmUnswizzleCoords(scaled, udata.layout.unlimDim)
	}

	return nil
}

// h5dEarrayIdxIterate is H5D__earray_idx_iterate: iterate over the chunks in
// an index, making a callback for each one (for allocated chunks only).
// The callback may return errBIterStop to stop early without error.
func h5dEarrayIdxIterate(idxInfo *h5dEarrayIdxInfo, chunkCb func(rec *h5dChunkRec) error) error {
	/* Check if the extensible array is open yet */
	if idxInfo.ea == nil {
		/* Open the extensible array in file */
		if err := h5dEarrayIdxOpen(idxInfo); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open extensible array")
		}
	}

	/* Set convenience pointer to extensible array structure */
	ea := idxInfo.ea

	/* Get the extensible array statistics */
	eaStat := ea.getStats()

	if eaStat.stored.maxIdxSet > 0 {
		/* Initialize userdata */
		udata := h5dEarrayItUd{layout: idxInfo.layout}
		udata.chunkRec.scaled = make([]uint64, idxInfo.layout.ndims-1)
		udata.filtered = idxInfo.pline != nil && idxInfo.pline.nused > 0
		if !udata.filtered {
			udata.chunkRec.nbytes = uint64(idxInfo.layout.size)
			udata.chunkRec.filterMask = 0
		}
		udata.cb = chunkCb

		/* Iterate over the extensible array elements */
		if err := ea.iterate(func(_ uint64, elmt any) error {
			return h5dEarrayIdxIterateCb(elmt, &udata)
		}); err != nil {
			return wrapf(err, H5E_DATASET, H5E_BADITER, "unable to iterate over fixed array chunk index")
		}
	}

	return nil
}

// dEarrayIdxSetup builds the index info for a dataset whose chunks are
// indexed by an extensible array: the layout message, the I/O pipeline
// (nil or empty when unfiltered) and the dataspace's maximum dimensions
// (H5D__chunk_set_info_real + H5D__earray_idx_init + H5D__earray_idx_resize).
func (f *File) dEarrayIdxSetup(layout *h5oLayout, pline *h5oPline, maxDims []uint64) (*h5dEarrayIdxInfo, error) {
	if layout == nil || layout.typ != h5dChunked || layout.chunkIdxType != h5dChunkIdxEarray {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "layout does not use an extensible array chunk index")
	}
	l, err := h5dChunkIdxLayoutInit(layout, maxDims)
	if err != nil {
		return nil, err
	}
	if err := h5dEarrayIdxInit(l, maxDims); err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't initialize extensible array chunk index")
	}
	h5dEarrayIdxResize(l)
	return &h5dEarrayIdxInfo{f: f, pline: pline, layout: l, idxAddr: layout.chunkIdxAddr}, nil
}

// dEarrayIdxGetAddr looks up a chunk of a dataset whose chunks are indexed
// by an extensible array (H5D__earray_idx_get_addr). scaled are the chunk's
// coordinates divided by the chunk dimensions (rank entries); maxDims are
// the dataspace's maximum dimensions (H5S_UNLIMITED = ^uint64(0)). An
// unallocated chunk yields addr == HADDR_UNDEF and nbytes == 0. For an
// unfiltered dataset nbytes is the full chunk size and filterMask is 0.
func (f *File) dEarrayIdxGetAddr(layout *h5oLayout, pline *h5oPline, maxDims []uint64, scaled []uint64) (addr haddr, nbytes uint64, filterMask uint32, err error) {
	idxInfo, err := f.dEarrayIdxSetup(layout, pline, maxDims)
	if err != nil {
		return haddrUndef, 0, 0, err
	}
	addr, nbytes, filterMask, _, err = h5dEarrayIdxGetAddr(idxInfo, scaled)
	return addr, nbytes, filterMask, err
}

// dEarrayIdxIterate calls cb for every allocated chunk of a dataset whose
// chunks are indexed by an extensible array (H5D__earray_idx_iterate), in
// index order, with the chunk's scaled coordinates (a fresh slice), address,
// size in the file and filter mask. cb may return errBIterStop to stop
// early without error.
func (f *File) dEarrayIdxIterate(layout *h5oLayout, pline *h5oPline, maxDims []uint64, cb func(scaled []uint64, addr haddr, nbytes uint64, filterMask uint32) error) error {
	idxInfo, err := f.dEarrayIdxSetup(layout, pline, maxDims)
	if err != nil {
		return err
	}
	err = h5dEarrayIdxIterate(idxInfo, func(rec *h5dChunkRec) error {
		return cb(append([]uint64(nil), rec.scaled...), rec.chunkAddr, rec.nbytes, rec.filterMask)
	})
	if errors.Is(err, errBIterStop) {
		return nil
	}
	return err
}
