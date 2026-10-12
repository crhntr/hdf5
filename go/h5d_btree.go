package hdf5

// Translated from src/H5Dbtree.c (read side): the version 1 B-tree chunk
// index (H5B_BTREE, the B-tree class whose keys describe chunks).
//
// Each key of the tree is {chunk size in the file, filter mask, chunk
// offset}; the offset is stored in elements (one per dataset dimension plus
// a trailing 0 for the datatype "dimension") and converted to scaled chunk
// coordinates (offset / chunk dimension) when it is decoded. Child i of a
// leaf node is the chunk described by key i.

// h5dBtreeKey is H5D_btree_key_t: the B-tree key.
type h5dBtreeKey struct {
	nbytes     uint32   /*size of stored data	*/
	scaled     []uint64 /*logical offset to start*/
	filterMask uint32   /*excluded filters	*/
}

// h5dBtreeShared is the H5B_shared_t "udata" of a chunk B-tree: the
// dataset's chunk layout (a copy of H5O_layout_chunk_t made by
// H5D__btree_shared_create).
type h5dBtreeShared struct {
	ndims uint32   /* number of chunk dimensions (rank + 1) */
	dim   []uint32 /* chunk dimensions (last = datatype size) */
}

// h5bBtree is H5B_BTREE: the v1 B-tree class for chunked dataset storage.
// Its context is a *h5dBtreeShared, its keys *h5dBtreeKey.
func h5bBtree() *h5bClass {
	return &h5bClass{
		id:         h5bChunkID,             /*id			*/
		sizeofRkey: h5dBtreeSizeofRkey,     /*sizeof_rkey		*/
		decodeKey:  h5dBtreeDecodeKeyClass, /*decode		*/
	}
}

// h5dBtreeSizeofRkey is the raw key size set up by H5D__btree_shared_create.
func h5dBtreeSizeofRkey(_ *File, ctx any) int {
	shared, ok := ctx.(*h5dBtreeShared)
	if !ok || shared == nil {
		return 0
	}
	/* Set the raw key size */
	return 4 + /*storage size		*/
		4 + /*filter mask		*/
		int(shared.ndims)*8 /*dimension indices	*/
}

// h5dBtreeCmp3 is H5D__btree_cmp3: compare the requested datum against the
// keys of a child: negative if the chunk is before lt_key, positive if it
// is at or after rt_key, zero if it falls between them.
func h5dBtreeCmp3(ltKey, rtKey *h5dBtreeKey, udata *h5dChunkUd, ndims uint32) int {
	/* Special case for faster checks on 1-D chunks */
	/* (Checking for ndims==2 because last dimension is the datatype size) */
	/* The additional checking for the right key is necessary due to the */
	/* slightly odd way the library initializes the right-most node in the */
	/* indexed storage B-tree... */
	/* (Dump the B-tree with h5debug to look at it) -QAK */
	if ndims == 2 {
		if udata.scaled[0] > rtKey.scaled[0] {
			return 1
		} else if udata.scaled[0] == rtKey.scaled[0] && udata.scaled[1] >= rtKey.scaled[1] {
			return 1
		} else if udata.scaled[0] < ltKey.scaled[0] {
			return -1
		}
		return 0
	}
	if h5vmVectorCmpU(ndims, udata.scaled, rtKey.scaled) >= 0 {
		return 1
	} else if h5vmVectorCmpU(ndims, udata.scaled, ltKey.scaled) < 0 {
		return -1
	}
	return 0
}

// h5vmVectorCmpU is H5VM_vector_cmp_u.
func h5vmVectorCmpU(n uint32, v1, v2 []uint64) int {
	for u := uint32(0); u < n; u++ {
		if v1[u] < v2[u] {
			return -1
		}
		if v1[u] > v2[u] {
			return 1
		}
	}
	return 0
}

// h5dBtreeFound is H5D__btree_found: called when the B-tree search engine
// has found the leaf entry that points to the chunk which would contain
// the requested one. It returns errBNotFound if the chunk is not the
// requested one (e.g. in a sparse dataset, the requested chunk might fall
// between two stored chunks).
func h5dBtreeFound(addr haddr, ltKey *h5dBtreeKey, udata *h5dChunkUd, ndims uint32) error {
	/* Is this *really* the requested chunk? */
	for u := uint32(0); u < ndims; u++ {
		if udata.scaled[u] >= ltKey.scaled[u]+1 {
			return errBNotFound
		}
	}

	/* Initialize return values */
	if ltKey.nbytes == 0 {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk B-tree key has a zero chunk size")
	}
	udata.chunkBlock.offset = addr
	udata.chunkBlock.length = uint64(ltKey.nbytes)
	udata.filterMask = ltKey.filterMask
	return nil
}

// h5dBtreeDecodeKey is H5D__btree_decode_key: decode a raw key into a
// native key.
func h5dBtreeDecodeKey(shared *h5dBtreeShared, p *image) (*h5dBtreeKey, error) {
	layout := shared
	if layout.ndims > h5oLayoutNdims || int(layout.ndims) > len(layout.dim) {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "bad number of dimensions")
	}
	key := &h5dBtreeKey{scaled: make([]uint64, layout.ndims)}

	key.nbytes = p.uint32()
	key.filterMask = p.uint32()
	for u := uint32(0); u < layout.ndims; u++ {
		if layout.dim[u] == 0 {
			return nil, errorf(H5E_DATASET, H5E_BADVALUE, "chunk size must be > 0, dim = %d ", u)
		}

		/* Retrieve coordinate offset */
		tmpOffset := p.uint64()
		if tmpOffset%uint64(layout.dim[u]) != 0 {
			return nil, errorf(H5E_DATASET, H5E_BADVALUE, "bad coordinate offset")
		}

		/* Convert to a scaled offset */
		key.scaled[u] = tmpOffset / uint64(layout.dim[u])
	}
	if err := p.done(); err != nil {
		return nil, err
	}
	return key, nil
}

// h5dBtreeDecodeKeyClass adapts h5dBtreeDecodeKey to h5bClass.decodeKey.
func h5dBtreeDecodeKeyClass(_ *File, ctx any, p *image) (any, error) {
	shared, ok := ctx.(*h5dBtreeShared)
	if !ok || shared == nil {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "missing chunk B-tree shared info")
	}
	return h5dBtreeDecodeKey(shared, p)
}

// h5dBtreeSharedCreate is H5D__btree_shared_create: set up the shared
// information of the dataset's chunk B-tree.
func h5dBtreeSharedCreate(sc *h5dChunkStorage, layout *h5dChunkLayout) {
	/* Set up the "local" information for this dataset's chunks */
	sc.btreeShared = &h5dBtreeShared{ndims: layout.ndims, dim: append([]uint32(nil), layout.dim...)}
}

// h5dCopsBtree is H5D_COPS_BTREE: the v1 B-tree chunk index.
func h5dCopsBtree() *h5dChunkOps {
	return &h5dChunkOps{
		idxType:      h5dChunkIdxBtree,
		init:         h5dBtreeIdxInit,
		isSpaceAlloc: h5dBtreeIdxIsSpaceAlloc,
		getAddr:      h5dBtreeIdxGetAddr,
		iterate:      h5dBtreeIdxIterate,
	}
}

// h5dBtreeIdxInit is H5D__btree_idx_init: initialize the indexing
// information for a dataset.
func h5dBtreeIdxInit(_ *File, dset *h5dShared) error {
	/* Allocate the shared structure */
	h5dBtreeSharedCreate(&dset.chunkSt, &dset.chunk)
	return nil
}

// h5dBtreeIdxIsSpaceAlloc is H5D__btree_idx_is_space_alloc.
func h5dBtreeIdxIsSpaceAlloc(sc *h5dChunkStorage) bool {
	return addrDefined(sc.idxAddr)
}

// h5dBtreeIdxGetAddr is H5D__btree_idx_get_addr: get the file address of a
// chunk if file space has been assigned.
func h5dBtreeIdxGetAddr(f *File, dset *h5dShared, udata *h5dChunkUd) error {
	sc := &dset.chunkSt
	shared := sc.btreeShared
	if shared == nil || shared.ndims == 0 {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk B-tree not initialized")
	}
	if !addrDefined(sc.idxAddr) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk B-tree address is undefined")
	}
	if uint32(len(udata.scaled)) < shared.ndims {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk coordinates shorter than the chunk rank")
	}
	ndims := shared.ndims

	/* Go get the chunk information from the B-tree */
	cmp3 := func(lt, rt any) int {
		ltKey, ok1 := lt.(*h5dBtreeKey)
		rtKey, ok2 := rt.(*h5dBtreeKey)
		if !ok1 || !ok2 {
			/* (FAIL is treated as "less than") */
			return -1
		}
		return h5dBtreeCmp3(ltKey, rtKey, udata, ndims)
	}
	found := func(lt, _ any, addr haddr) error {
		ltKey, ok := lt.(*h5dBtreeKey)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected chunk B-tree key %T", lt)
		}
		return h5dBtreeFound(addr, ltKey, udata, ndims)
	}
	if _, err := f.bFind(h5bBtree(), shared, sc.idxAddr, cmp3, found); err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTFIND, "can't check for chunk in B-tree")
	}
	return nil
}

// h5dBtreeIdxIterate is H5D__btree_idx_iterate (with
// H5D__btree_idx_iterate_cb): iterate over the chunks in the index, making
// a callback for each one.
func h5dBtreeIdxIterate(f *File, dset *h5dShared, chunkCb func(rec *h5dChunkRec) error) error {
	sc := &dset.chunkSt
	if sc.btreeShared == nil {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk B-tree not initialized")
	}

	/* Iterate over existing chunks */
	err := f.bIterate(h5bBtree(), sc.btreeShared, sc.idxAddr, func(lt, _ any, addr haddr) error {
		ltKey, ok := lt.(*h5dBtreeKey)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected chunk B-tree key %T", lt)
		}

		/* Compose generic chunk record for callback */
		chunkRec := &h5dChunkRec{
			scaled:     append([]uint64(nil), ltKey.scaled...),
			nbytes:     ltKey.nbytes,
			filterMask: ltKey.filterMask,
			chunkAddr:  addr,
		}

		/* Make "generic chunk" callback */
		return chunkCb(chunkRec)
	})
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_BADITER, "unable to iterate over chunk B-tree")
	}
	return nil
}

// h5dBtreeIdxSize is H5D__btree_idx_size: the size of the B-tree nodes.
func h5dBtreeIdxSize(f *File, dset *h5dShared) (uint64, error) {
	sc := &dset.chunkSt
	/* Get metadata information for B-tree */
	info, err := f.bGetInfo(h5bBtree(), sc.btreeShared, sc.idxAddr, nil)
	if err != nil {
		return 0, wrapf(err, H5E_BTREE, H5E_CANTINIT, "unable to iterate over chunk B-tree")
	}
	/* Set the size of the B-tree */
	return info.size, nil
}
