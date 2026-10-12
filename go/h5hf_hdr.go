package hdf5

// Translated from src/H5HFhdr.c (read side only: finishing the
// initialization of a decoded header and protecting it through the cache).

// h5hfHdrComputeFreeSpace is H5HF__hdr_compute_free_space: compute direct
// block free space, for indirect blocks of different sizes.
func h5hfHdrComputeFreeSpace(hdr *h5hfHdr, iblockRow uint32) {
	dt := &hdr.manDtable

	/* Set the free space in direct blocks */
	var accHeapSize, accDblockFree, maxDblockFree uint64
	iblockSize := dt.rowBlockSize[iblockRow]
	currRow := uint32(0)
	for accHeapSize < iblockSize && currRow < uint32(len(dt.rowBlockSize)) {
		accHeapSize += dt.rowBlockSize[currRow] * uint64(dt.width)
		accDblockFree += dt.rowTotDblockFree[currRow] * uint64(dt.width)
		if dt.rowMaxDblockFree[currRow] > maxDblockFree {
			maxDblockFree = dt.rowMaxDblockFree[currRow]
		}
		currRow++
	}

	/* Set direct block free space values for indirect block */
	dt.rowTotDblockFree[iblockRow] = accDblockFree
	dt.rowMaxDblockFree[iblockRow] = maxDblockFree
}

// h5hfHdrFinishInitPhase1 is H5HF__hdr_finish_init_phase1: first phase to
// finish initializing info in shared heap header.
func h5hfHdrFinishInitPhase1(hdr *h5hfHdr) error {
	/* Compute/cache some values */
	hdr.heapOffSize = uint8(h5hfSizeofOffsetBits(uint32(hdr.manDtable.maxIndexBits)))
	if err := h5hfDtableInit(&hdr.manDtable); err != nil {
		return wrapf(err, H5E_HEAP, H5E_CANTINIT, "can't initialize doubling table info")
	}

	/* Set the size of heap IDs */
	hdr.heapLenSize = uint8(min(hdr.manDtable.maxDirBlkOffSize, h5hfLimitEncSize(uint64(hdr.maxManSize))))
	return nil
}

// h5hfHdrFinishInitPhase2 is H5HF__hdr_finish_init_phase2: second phase to
// finish initializing info in shared heap header.
func h5hfHdrFinishInitPhase2(hdr *h5hfHdr) error {
	dt := &hdr.manDtable

	/* Set the free space in direct blocks */
	overhead := h5hfManAbsDirectOverhead(hdr)
	for u := uint32(0); u < dt.maxRootRows; u++ {
		if u < dt.maxDirectRows {
			if dt.rowBlockSize[u] > overhead {
				dt.rowTotDblockFree[u] = dt.rowBlockSize[u] - overhead
			}
			dt.rowMaxDblockFree[u] = dt.rowTotDblockFree[u]
		} else {
			h5hfHdrComputeFreeSpace(hdr, u)
		}
	}

	/* (H5HF__man_iter_init: the block iterator for searching for free space
	 * is only used when inserting objects) */

	/* Initialize the information for tracking 'huge' objects */
	h5hfHugeInit(hdr)

	/* Initialize the information for tracking 'tiny' objects */
	h5hfTinyInit(hdr)
	return nil
}

// h5hfHdrFinishInit is H5HF__hdr_finish_init: finish initializing info in
// shared heap header.
func h5hfHdrFinishInit(hdr *h5hfHdr) error {
	/* First phase of header final initialization */
	if err := h5hfHdrFinishInitPhase1(hdr); err != nil {
		return wrapf(err, H5E_HEAP, H5E_CANTINIT, "can't finish phase #1 of header final initialization")
	}

	/* Second phase of header final initialization */
	if err := h5hfHdrFinishInitPhase2(hdr); err != nil {
		return wrapf(err, H5E_HEAP, H5E_CANTINIT, "can't finish phase #2 of header final initialization")
	}
	return nil
}

// h5hfHdrProtect is H5HF__hdr_protect: load (through the metadata cache)
// the fractal heap header at addr.
func h5hfHdrProtect(f *File, addr haddr) (*h5hfHdr, error) {
	/* Lock the heap header into memory */
	v, err := f.cached("H5HF_HDR", addr, func() (any, error) {
		return h5hfCacheHdrLoad(f, addr)
	})
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap header")
	}
	hdr, ok := v.(*h5hfHdr)
	if !ok {
		return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "cached object at address %d is not a fractal heap header", addr)
	}
	return hdr, nil
}
