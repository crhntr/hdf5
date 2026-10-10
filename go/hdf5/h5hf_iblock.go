package hdf5

// Translated from src/H5HFiblock.c (read side only: protecting indirect
// blocks through the metadata cache and reading their entries).

// h5hfManIblockProtect is H5HF__man_iblock_protect: load (through the
// metadata cache) the indirect block with iblockNrows rows at iblockAddr.
// parIblock and parEntry describe its parent (nil for the root indirect
// block).
//
// The C routine also maintains the pinned root/child indirect block
// pointers used while modifying the heap; reads only need the cache.
func h5hfManIblockProtect(hdr *h5hfHdr, iblockAddr haddr, iblockNrows uint32, parIblock *h5hfIndirect, parEntry uint32) (*h5hfIndirect, error) {
	dt := &hdr.manDtable

	/*
	 * Check arguments.
	 */
	if !addrDefined(iblockAddr) {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "undefined fractal heap indirect block address")
	}
	if iblockNrows == 0 {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "fractal heap indirect block has no rows")
	}
	/* (an indirect block never has more rows than the root indirect block
	 *  can have; this bounds the size of the block's image) */
	if iblockNrows > dt.maxRootRows {
		return nil, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap indirect block has too many rows (%d > %d)", iblockNrows, dt.maxRootRows)
	}
	if parIblock != nil && parEntry < dt.maxDirectRows*uint32(dt.width) {
		return nil, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap indirect block entry %d is a direct block entry", parEntry)
	}

	/* Set up parent info */
	parInfo := h5hfParent{hdr: hdr, iblock: parIblock, entry: parEntry}

	/* Protect the indirect block */
	v, err := hdr.f.cached("H5HF_IBLOCK", iblockAddr, func() (any, error) {
		/* (H5C__load_entry: get_initial_load_size, read, verify_chksum,
		 *  deserialize) */
		size := h5hfCacheIblockGetInitialLoadSize(hdr, iblockNrows)
		if size > hdr.f.eof() {
			return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "fractal heap indirect block size %d larger than the file", size)
		}
		img, err := hdr.f.readAt(iblockAddr, int(size))
		if err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTLOAD, "unable to read fractal heap indirect block")
		}
		if hdr.f.opts.verifyChecksums {
			ok, err := h5hfCacheIblockVerifyChksum(img)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errorf(H5E_HEAP, H5E_BADCHECKSUM, "incorrect metadata checksum for fractal heap indirect block")
			}
		}
		iblock, err := h5hfCacheIblockDeserialize(img, &parInfo, iblockNrows)
		if err != nil {
			return nil, err
		}

		/* Set the indirect block's address */
		iblock.addr = iblockAddr
		return iblock, nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap indirect block")
	}
	iblock, ok := v.(*h5hfIndirect)
	if !ok {
		return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "cached object at address %d is not a fractal heap indirect block", iblockAddr)
	}

	/* (a block cached for another heap, or with another shape, means the
	 *  file is corrupt) */
	if iblock.heapAddr != hdr.addr || iblock.nrows != iblockNrows {
		return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "fractal heap indirect block at address %d does not match its parent", iblockAddr)
	}
	return iblock, nil
}

// h5hfManIblockEntryAddr is H5HF__man_iblock_entry_addr: retrieve the
// address of an indirect block's child.
func h5hfManIblockEntryAddr(iblock *h5hfIndirect, entry uint32) (haddr, error) {
	if entry >= uint32(len(iblock.ents)) {
		return haddrUndef, errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap indirect block entry %d out of range", entry)
	}

	/* Retrieve address of entry */
	return iblock.ents[entry], nil
}
