package hdf5

// Translated from src/H5Dnone.c (read side): the implicit chunk index
// ("none", layout message version 4) of unfiltered datasets with fixed
// dimensions whose chunks are allocated early: the chunks are stored back
// to back starting at the index address, in row-major order of the chunks
// of the maximum dimensions.

// h5dCopsNone is H5D_COPS_NONE: the implicit chunk index.
func h5dCopsNone() *h5dChunkOps {
	return &h5dChunkOps{
		idxType:      h5dChunkIdxNone,
		init:         h5dNoneIdxInit,
		isSpaceAlloc: h5dNoneIdxIsSpaceAlloc,
		getAddr:      h5dNoneIdxGetAddr,
		iterate:      h5dNoneIdxIterate,
	}
}

// h5dNoneIdxInit checks the preconditions the C callbacks assert: an
// implicit index never has filters.
func h5dNoneIdxInit(_ *File, dset *h5dShared) error {
	if dset.dcplCache.pline.nused != 0 {
		return errorf(H5E_DATASET, H5E_BADVALUE, "implicit chunk index with a filter pipeline")
	}
	return nil
}

// h5dNoneIdxIsSpaceAlloc is H5D__none_idx_is_space_alloc.
func h5dNoneIdxIsSpaceAlloc(sc *h5dChunkStorage) bool {
	return addrDefined(sc.idxAddr)
}

// h5dNoneIdxGetAddr is H5D__none_idx_get_addr: get the file address of a
// chunk.
func h5dNoneIdxGetAddr(f *File, dset *h5dShared, udata *h5dChunkUd) error {
	sc := &dset.chunkSt
	layout := &dset.chunk
	if !addrDefined(sc.idxAddr) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "implicit chunk index address is undefined")
	}
	ndims := int(layout.ndims) - 1
	if len(layout.maxDownChunks) < ndims {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk information not set up")
	}

	/* Calculate the index of this chunk */
	udata.chunkIdx = h5vmArrayOffsetPre(ndims, layout.maxDownChunks, udata.scaled)

	/* Calculate the address of the chunk */
	off, err := h5dMulSize(udata.chunkIdx, uint64(layout.size))
	if err != nil || sc.idxAddr+off < sc.idxAddr || udata.chunkIdx >= layout.maxNchunks {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk index %d out of range of the implicit index", udata.chunkIdx)
	}
	udata.chunkBlock.offset = sc.idxAddr + off

	/* Update the other (constant) information for the chunk */
	udata.chunkBlock.length = uint64(layout.size)
	udata.filterMask = 0
	return nil
}

// h5dNoneIdxIterate is H5D__none_idx_iterate: iterate over all the chunks
// in the dataset's dataspace.
func h5dNoneIdxIterate(f *File, dset *h5dShared, chunkCb func(rec *h5dChunkRec) error) error {
	sc := &dset.chunkSt
	layout := &dset.chunk
	if !addrDefined(sc.idxAddr) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "implicit chunk index address is undefined")
	}
	ndims := int(layout.ndims) - 1
	if ndims <= 0 || len(layout.chunks) < ndims {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk information not set up")
	}

	/* (Bound the iteration: every chunk must lie in the file) */
	if layout.size == 0 || layout.nchunks > f.eof()/uint64(layout.size)+1 {
		return errorf(H5E_DATASET, H5E_BADVALUE, "too many chunks (%d) for an implicit index", layout.nchunks)
	}

	/* Initialize generic chunk record */
	scaled := make([]uint64, ndims)

	/* Iterate over all the chunks in the dataset's dataspace */
	for u := uint64(0); u < layout.nchunks; u++ {
		/* Calculate the index of this chunk */
		idx := h5vmArrayOffsetPre(ndims, layout.maxDownChunks, scaled)

		/* Make "generic chunk" callback */
		chunkRec := &h5dChunkRec{
			scaled:     append([]uint64(nil), scaled...),
			nbytes:     layout.size,
			filterMask: 0,
			chunkAddr:  sc.idxAddr + idx*uint64(layout.size),
		}
		if err := chunkCb(chunkRec); err != nil {
			return err
		}

		/* Update coordinates of chunk in dataset */
		for currDim := ndims - 1; currDim >= 0; currDim-- {
			/* Increment coordinate in current dimension */
			scaled[currDim]++

			/* Check if we went off the end of the current dimension */
			if scaled[currDim] >= layout.chunks[currDim] {
				/* Reset coordinate & move to next faster dimension */
				scaled[currDim] = 0
			} else {
				break
			}
		}
	}
	return nil
}
