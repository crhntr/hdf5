package hdf5

// Translated from src/H5HFdblock.c (read side only: protecting direct
// blocks through the metadata cache and locating the direct block that
// holds a heap offset).

// h5hfManDblockProtect is H5HF__man_dblock_protect: load (through the
// metadata cache) the direct block of dblockSize bytes at dblockAddr.
// parIblock and parEntry describe its parent (nil for a root direct block).
// The returned block's data is already passed back through the heap's I/O
// filters.
func h5hfManDblockProtect(hdr *h5hfHdr, dblockAddr haddr, dblockSize uint64, parIblock *h5hfIndirect, parEntry uint32) (*h5hfDirect, error) {
	/*
	 * Check arguments.
	 */
	if !addrDefined(dblockAddr) {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "undefined fractal heap direct block address")
	}
	if dblockSize == 0 {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "fractal heap direct block size is zero")
	}

	/* Set up parent info */
	var udata h5hfDblockCacheUd
	udata.parInfo.hdr = hdr
	udata.parInfo.iblock = parIblock
	udata.parInfo.entry = parEntry

	/* set up the direct block size */
	udata.dblockSize = dblockSize

	/* compute the on disk image size -- observe that odi_size and
	 * dblock_size will be identical if there is no filtering.
	 */
	if hdr.filterLen > 0 {
		if parIblock == nil {
			udata.odiSize = hdr.plineRootDirectSize
			udata.filterMask = hdr.plineRootDirectFilterMask
		} else {
			/* Sanity check */
			if parEntry >= uint32(len(parIblock.filtEnts)) || parIblock.ents[parEntry] != dblockAddr {
				return nil, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap direct block entry %d out of range", parEntry)
			}

			/* Set up parameters to read filtered direct block */
			udata.odiSize = parIblock.filtEnts[parEntry].size
			udata.filterMask = parIblock.filtEnts[parEntry].filterMask
		}
	} else {
		udata.odiSize = dblockSize
		udata.filterMask = 0
	}

	/* Protect the direct block */
	v, err := hdr.f.cached("H5HF_DBLOCK", dblockAddr, func() (any, error) {
		/* (H5C__load_entry: get_initial_load_size, read, verify_chksum,
		 *  deserialize) */
		size, err := h5hfCacheDblockGetInitialLoadSize(&udata)
		if err != nil {
			return nil, err
		}
		if size > hdr.f.eof() {
			return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "fractal heap direct block size %d larger than the file", size)
		}
		img, err := hdr.f.readAt(dblockAddr, int(size))
		if err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTLOAD, "unable to read fractal heap direct block")
		}

		/* Push direct block data through I/O filter pipeline (done in the
		 * verify_chksum or deserialize callback in the C library) */
		blk := img
		if hdr.filterLen > 0 {
			if blk, err = h5zPipeline(hdr.pline, udata.filterMask, img); err != nil {
				return nil, wrapf(err, H5E_HEAP, H5E_CANTFILTER, "output pipeline failed")
			}
			if uint64(len(blk)) != udata.dblockSize {
				return nil, errorf(H5E_HEAP, H5E_BADSIZE, "filtered fractal heap direct block decoded to %d bytes, expected %d", len(blk), udata.dblockSize)
			}
		}

		if hdr.f.opts.verifyChecksums {
			ok, err := h5hfCacheDblockVerifyChksum(hdr, blk)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errorf(H5E_HEAP, H5E_BADCHECKSUM, "incorrect metadata checksum for fractal heap direct block")
			}
		}
		return h5hfCacheDblockDeserialize(blk, &udata)
	})
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap direct block")
	}
	dblock, ok := v.(*h5hfDirect)
	if !ok {
		return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "cached object at address %d is not a fractal heap direct block", dblockAddr)
	}

	/* (a block cached for another heap, or with another size, means the
	 *  file is corrupt) */
	if dblock.heapAddr != hdr.addr || dblock.size != dblockSize {
		return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "fractal heap direct block at address %d does not match its parent", dblockAddr)
	}
	return dblock, nil
}

// h5hfManDblockLocate is H5HF__man_dblock_locate: locate the direct block
// holding heap offset objOff in a managed heap with a root indirect block.
// It returns the indirect block that points to the direct block and the
// entry of the direct block in it.
func h5hfManDblockLocate(hdr *h5hfHdr, objOff uint64) (*h5hfIndirect, uint32, error) {
	dt := &hdr.manDtable

	/*
	 * Check arguments.
	 */
	if dt.curRootRows == 0 {
		return nil, 0, errorf(H5E_HEAP, H5E_BADVALUE, "fractal heap has no root indirect block")
	}

	/* Look up row & column for object */
	row, col, err := h5hfDtableLookup(dt, objOff)
	if err != nil {
		return nil, 0, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't compute row & column of object")
	}

	/* Set initial indirect block info */
	iblockAddr := dt.tableAddr

	/* Lock root indirect block */
	iblock, err := h5hfManIblockProtect(hdr, iblockAddr, uint32(dt.curRootRows), nil, 0)
	if err != nil {
		return nil, 0, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap indirect block")
	}

	/* Check for indirect block row */
	for row >= dt.maxDirectRows {
		/* (the row must be within the indirect block) */
		if row >= iblock.nrows {
			return nil, 0, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap offset %d outside of indirect block", objOff)
		}

		/* Compute # of rows in child indirect block */
		nrows := (h5hfLog2Gen(dt.rowBlockSize[row]) - dt.firstRowBits) + 1
		if nrows >= iblock.nrows {
			/* child must be smaller than parent */
			return nil, 0, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap child indirect block not smaller than its parent")
		}

		/* Compute indirect block's entry */
		entry := (row * uint32(dt.width)) + col

		/* Locate child indirect block */
		iblockAddr, err = h5hfManIblockEntryAddr(iblock, entry)
		if err != nil {
			return nil, 0, err
		}

		/* Check if we need to (re-)create the child indirect block */
		if !addrDefined(iblockAddr) {
			/* (only possible when writing) */
			return nil, 0, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap ID not in allocated indirect block")
		}

		/* Lock child indirect block */
		newIblock, err := h5hfManIblockProtect(hdr, iblockAddr, nrows, iblock, entry)
		if err != nil {
			return nil, 0, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap indirect block")
		}

		/* Switch variables to use new indirect block */
		iblock = newIblock

		/* Look up row & column in new indirect block for object */
		if objOff < iblock.blockOff {
			return nil, 0, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap offset %d before indirect block", objOff)
		}
		if row, col, err = h5hfDtableLookup(dt, objOff-iblock.blockOff); err != nil {
			return nil, 0, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't compute row & column of object")
		}
		if row >= iblock.nrows {
			/* child must be smaller than parent */
			return nil, 0, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap offset %d outside of indirect block", objOff)
		}
	}

	/* Set return parameters */
	entry := (row * uint32(dt.width)) + col
	if entry >= uint32(len(iblock.ents)) {
		return nil, 0, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap offset %d outside of indirect block", objOff)
	}
	return iblock, entry, nil
}
