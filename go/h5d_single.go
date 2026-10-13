package hdf5

// Translated from src/H5Dsingle.c (read side): the "single chunk" index of
// a dataset with one chunk (layout message version 4). The chunk's address
// is the index address; for a filtered chunk its size and filter mask are
// stored in the layout message.

// h5dCopsSingle is H5D_COPS_SINGLE: the single chunk index.
func h5dCopsSingle() *h5dChunkOps {
	return &h5dChunkOps{
		idxType:      h5dChunkIdxSingle,
		init:         h5dSingleIdxInit,
		isSpaceAlloc: h5dSingleIdxIsSpaceAlloc,
		getAddr:      h5dSingleIdxGetAddr,
		iterate:      h5dSingleIdxIterate,
	}
}

// h5dSingleIdxInit is H5D__single_idx_init: initialize the indexing
// information for a dataset.
func h5dSingleIdxInit(_ *File, dset *h5dShared) error {
	sc := &dset.chunkSt
	if dset.dcplCache.pline.nused > 0 {
		dset.chunk.flags |= h5oLayoutChunkSingleIndexWithFilter

		if !addrDefined(sc.idxAddr) {
			sc.singleNbytes = 0
			sc.singleFilterMask = 0
		}
	} else {
		dset.chunk.flags = 0
	}
	return nil
}

// h5dSingleIdxIsSpaceAlloc is H5D__single_idx_is_space_alloc.
func h5dSingleIdxIsSpaceAlloc(sc *h5dChunkStorage) bool {
	return addrDefined(sc.idxAddr)
}

// h5dSingleIdxGetAddr is H5D__single_idx_get_addr: get the file address of
// the chunk. (The C library only asserts that the dataset has a single
// chunk: when the dataspace is larger than the chunk, every chunk of the
// dataspace is read from the one chunk.)
func h5dSingleIdxGetAddr(_ *File, dset *h5dShared, udata *h5dChunkUd) error {
	sc := &dset.chunkSt

	udata.chunkBlock.offset = sc.idxAddr
	if dset.chunk.flags&h5oLayoutChunkSingleIndexWithFilter != 0 {
		udata.chunkBlock.length = uint64(sc.singleNbytes)
		udata.filterMask = sc.singleFilterMask
	} else {
		udata.chunkBlock.length = uint64(dset.chunk.size)
		udata.filterMask = 0
	}
	if !addrDefined(udata.chunkBlock.offset) {
		udata.chunkBlock.length = 0
	}
	return nil
}

// h5dSingleIdxIterate is H5D__single_idx_iterate: make a callback for the
// chunk.
func h5dSingleIdxIterate(_ *File, dset *h5dShared, chunkCb func(rec *h5dChunkRec) error) error {
	sc := &dset.chunkSt
	if !addrDefined(sc.idxAddr) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "single chunk index address is undefined")
	}

	/* Initialize generic chunk record */
	chunkRec := &h5dChunkRec{scaled: make([]uint64, dset.ndims), chunkAddr: sc.idxAddr}
	if dset.chunk.flags&h5oLayoutChunkSingleIndexWithFilter != 0 {
		chunkRec.nbytes = sc.singleNbytes
		chunkRec.filterMask = sc.singleFilterMask
	} else {
		chunkRec.nbytes = dset.chunk.size
		chunkRec.filterMask = 0
	}

	/* Make "generic chunk" callback */
	return chunkCb(chunkRec)
}
