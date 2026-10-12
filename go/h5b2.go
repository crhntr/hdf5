package hdf5

// Translated from src/H5B2.c (read side only): the v2 B-tree API routines
// H5B2_open, H5B2_iterate, H5B2_find, H5B2_index, H5B2_get_nrec and
// H5B2_neighbor.
//
// A v2 B-tree ("BTHD" header, "BTIN" internal nodes, "BTLF" leaves) stores
// fixed-size records of a client-defined class (fractal heap 'huge' objects,
// dense link / attribute name and creation order indexes, shared message
// indexes, chunk indexes). The generic code never interprets the records;
// the client class (h5b2Class) supplies:
//
//   - decode(f, ctx, p): decode one record from an image of exactly
//     hdr.rrecSize bytes into a native Go value (a pointer to a record
//     struct). It must be deterministic: decoded nodes are cached on the File
//     by (class id, depth, record count, address).
//   - compare(f, ctx, udata, rec): compare the search key udata (whatever
//     the caller passes to find/neighbor, usually a struct mirroring the C
//     H5*_bt2_ud_t user data) with the native record rec: <0, 0, >0.
//
// ctx is the client callback context (the C H5B2_class_t crt_context
// result). In the C library H5B2_open passes ctx_udata to the class's
// crt_context callback; here the caller builds the context itself (e.g.
// with h5dBt2CrtContext or h5smBt2CrtContext) and passes it to b2Open,
// which hands it back to every decode/compare call. Classes without a
// crt_context in C take a nil ctx.
//
// Iteration sentinels follow h5b.go: an iterate operator returns
// errBIterStop to stop early without error.

// H5B2_subid_t: v2 B-tree record types.
const (
	h5b2TestID               = 0  /* B-tree is for testing (do not use for actual data) */
	h5b2FheapHugeIndirID     = 1  /* B-tree is for fractal heap indirectly accessed, non-filtered 'huge' objects */
	h5b2FheapHugeFiltIndirID = 2  /* B-tree is for fractal heap indirectly accessed, filtered 'huge' objects */
	h5b2FheapHugeDirID       = 3  /* B-tree is for fractal heap directly accessed, non-filtered 'huge' objects */
	h5b2FheapHugeFiltDirID   = 4  /* B-tree is for fractal heap directly accessed, filtered 'huge' objects */
	h5b2GrpDenseNameID       = 5  /* B-tree is for indexing 'name' field for "dense" link storage in groups */
	h5b2GrpDenseCorderID     = 6  /* B-tree is for indexing 'creation order' field for "dense" link storage in groups */
	h5b2SohmIndexID          = 7  /* B-tree is an index for shared object header messages */
	h5b2AttrDenseNameID      = 8  /* B-tree is for indexing 'name' field for "dense" attribute storage on objects */
	h5b2AttrDenseCorderID    = 9  /* B-tree is for indexing 'creation order' field for "dense" attribute storage on objects */
	h5b2CdsetID              = 10 /* B-tree is for non-filtered chunked dataset storage w/implicit index */
	h5b2CdsetFiltID          = 11 /* B-tree is for filtered chunked dataset storage w/implicit index */
	h5b2TestID2              = 12 /* Another B-tree is for testing (do not use for actual data) */
	h5b2NumBtreeID           = 13 /* Number of B-tree key IDs (must be last) */
)

// H5B2_compare_t: comparisons for H5B2_neighbor() call.
const (
	h5b2CompareLess    = 0 /* Records with keys less than query value */
	h5b2CompareGreater = 1 /* Records with keys greater than query value */
)

// h5b2Class is H5B2_class_t (decode side). ctx is the client "context"
// (H5B2_class_t.crt_context) and is passed back to the callbacks.
type h5b2Class struct {
	id      uint8
	decode  func(f *File, ctx any, p *image) (any, error)           /* H5B2_class_t.decode: one raw record */
	compare func(f *File, ctx any, udata any, rec any) (int, error) /* H5B2_class_t.compare: udata vs rec (<0, 0, >0) */
}

// h5b2 is H5B2_t: an opened v2 B-tree.
type h5b2 struct {
	f   *File
	hdr *h5b2Hdr
	cls *h5b2Class
	ctx any
}

// b2Open is H5B2_open: opens an existing v2 B-tree in the file.
//
// cls must be the class of the records stored in the tree (its id must
// match the type stored in the header); ctx is the class's callback context.
func (f *File) b2Open(addr haddr, cls *h5b2Class, ctx any) (*h5b2, error) {
	/* Check arguments. */
	if !addrDefined(addr) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree address is undefined")
	}
	if cls == nil || cls.decode == nil {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree class is missing")
	}

	/* Look up the B-tree header */
	hdr, err := h5b2HdrProtect(f, addr)
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect v2 B-tree header")
	}

	/* (The C library selects the class from the type stored in the header,
	 *  H5B2_client_class_g[id]; here the caller supplies it, so make sure
	 *  they agree.) */
	if hdr.typ != cls.id {
		return nil, errorf(H5E_BTREE, H5E_BADTYPE, "incorrect B-tree type %d (expected %d)", hdr.typ, cls.id)
	}

	/* Create v2 B-tree info */
	/* Point v2 B-tree wrapper at header */
	/* Set file pointer for this v2 B-tree open context */
	return &h5b2{f: f, hdr: hdr, cls: cls, ctx: ctx}, nil
}

// iterate is H5B2_iterate: iterate over all the records in the B-tree, in
// "in-order" order, making a callback for each record.
//
// If the callback returns errBIterStop, the iteration breaks out without
// finishing all the records (and iterate returns nil); any other error ends
// the iteration and is returned wrapped.
func (bt *h5b2) iterate(op func(rec any) error) error {
	/* Get the v2 B-tree header */
	hdr := bt.hdr

	/* Iterate through records */
	if hdr.rootNrec > 0 {
		/* Iterate through nodes */
		root := hdr.rootPtr()
		if _, err := h5b2IterateNode(bt, hdr.depth, &root, map[haddr]bool{}, op); err != nil {
			return wrapf(err, H5E_BTREE, H5E_BADITER, "node iteration failed")
		}
	}
	return nil
}

// find is H5B2_find: locate the specified information in a B-tree and
// return that information by calling the provided op routine. The udata
// parameter is passed to the key comparison function.
//
// op is called with the record found, to allow the caller to return
// information about the record. If op is nil, find just reports whether a
// record is present in the B-tree. Returns found=false (no error) when
// absent.
//
// (The C routine also caches the minimum and maximum records of the tree in
// the shared header to short-cut later searches; decoded structures are
// immutable here, so that optimization is omitted.)
func (bt *h5b2) find(udata any, op func(rec any) error) (bool, error) {
	/* Get the v2 B-tree header */
	hdr := bt.hdr

	/* Make copy of the root node pointer to start search with */
	currNodePtr := hdr.rootPtr()

	/* Check for empty tree */
	if currNodePtr.nodeNrec == 0 {
		return false, nil
	}

	/* Current depth of the tree */
	depth := hdr.depth

	/* Walk down B-tree to find record or leaf node where record is located */
	for depth > 0 {
		/* Lock B-tree current node */
		internal, err := h5b2ProtectInternal(bt, &currNodePtr, depth)
		if err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to load B-tree internal node")
		}

		/* Locate node pointer for child */
		idx, cmp, err := h5b2LocateRecord(bt, internal.native, udata)
		if err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_CANTCOMPARE, "can't compare btree2 records")
		}

		if cmp > 0 {
			idx++
		}
		if cmp != 0 {
			/* Get node pointer for next node to search */
			/* Set pointer to next node to load */
			currNodePtr = internal.nodePtrs[idx]
		} else {
			/* Make callback for current record */
			if op != nil {
				if err := op(internal.native[idx]); err != nil {
					return false, wrapf(err, H5E_BTREE, H5E_NOTFOUND, "'found' callback failed for B-tree find operation")
				}
			}

			/* Indicate record found */
			return true, nil
		}

		/* Decrement depth we're at in B-tree */
		depth--
	}

	/* Lock B-tree leaf node */
	leaf, err := h5b2ProtectLeaf(bt, &currNodePtr)
	if err != nil {
		return false, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree leaf node")
	}

	/* Locate record */
	idx, cmp, err := h5b2LocateRecord(bt, leaf.native, udata)
	if err != nil {
		return false, wrapf(err, H5E_BTREE, H5E_CANTCOMPARE, "can't compare btree2 records")
	}

	if cmp != 0 {
		/* Record not found */
		return false, nil
	}

	/* Make callback for current record */
	if op != nil {
		if err := op(leaf.native[idx]); err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_NOTFOUND, "'found' callback failed for B-tree find operation")
		}
	}

	/* Indicate record found */
	return true, nil
}

// index is H5B2_index with H5_ITER_INC order: call op on the idx-th record
// (0-based, in the ordering used by the B-tree).
func (bt *h5b2) index(idx uint64, op func(rec any) error) error {
	return bt.indexOrder(false, idx, op)
}

// indexOrder is H5B2_index: locate the idx'th record in a B-tree according
// to the ordering used by the B-tree (reversed when decreasing is true,
// i.e. H5_ITER_DEC). The idx values are 0-based.
//
// The op routine is called with the record found, to allow the caller to
// return information about the record.
func (bt *h5b2) indexOrder(decreasing bool, idx uint64, op func(rec any) error) error {
	/* Get the v2 B-tree header */
	hdr := bt.hdr

	/* Make copy of the root node pointer to start search with */
	currNodePtr := hdr.rootPtr()

	/* Check for empty tree */
	if currNodePtr.nodeNrec == 0 {
		return errorf(H5E_BTREE, H5E_NOTFOUND, "B-tree has no records")
	}

	/* Check for index greater than the number of records in the tree */
	if idx >= currNodePtr.allNrec {
		return errorf(H5E_BTREE, H5E_NOTFOUND, "B-tree doesn't have that many records")
	}

	/* Current depth of the tree */
	depth := hdr.depth

	/* Check for reverse indexing and map requested index to appropriate forward index */
	if decreasing {
		idx = currNodePtr.allNrec - (idx + 1)
	}

	/* Walk down B-tree to find record or leaf node where record is located */
	for depth > 0 {
		/* Lock B-tree current node */
		internal, err := h5b2ProtectInternal(bt, &currNodePtr, depth)
		if err != nil {
			return wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to load B-tree internal node")
		}

		/* Search for record with correct index */
		var u int
		descended := false
		for u = 0; u < int(internal.nrec); u++ {
			/* Check if record is in child node */
			if internal.nodePtrs[u].allNrec > idx {
				/* Set pointer to next node to load */
				currNodePtr = internal.nodePtrs[u]
				descended = true

				/* Break out of for loop */
				break
			}

			/* Check if record is in this node */
			if internal.nodePtrs[u].allNrec == idx {
				/* Make callback for current record */
				if err := op(internal.native[u]); err != nil {
					return wrapf(err, H5E_BTREE, H5E_NOTFOUND, "'found' callback failed for B-tree find operation")
				}
				return nil
			}

			/* Decrement index we are looking for to account for the node we
			 * just advanced past.
			 */
			idx -= internal.nodePtrs[u].allNrec + 1
		}

		/* Check last node pointer */
		if !descended {
			/* Check if record is in child node */
			if internal.nodePtrs[u].allNrec > idx {
				/* Set pointer to next node to load */
				currNodePtr = internal.nodePtrs[u]
			} else {
				/* Index that is greater than the number of records in the tree? */
				return errorf(H5E_BTREE, H5E_BADVALUE, "index off end of tree, B-tree record counts are corrupted")
			}
		}

		/* Decrement depth we're at in B-tree */
		depth--
	}

	/* Lock B-tree leaf node */
	leaf, err := h5b2ProtectLeaf(bt, &currNodePtr)
	if err != nil {
		return wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree leaf node")
	}

	/* Sanity check index */
	if idx >= uint64(leaf.nrec) {
		return errorf(H5E_BTREE, H5E_BADVALUE, "index off end of leaf node, B-tree record counts are corrupted")
	}

	/* Make callback for correct record */
	if err := op(leaf.native[idx]); err != nil {
		return wrapf(err, H5E_BTREE, H5E_NOTFOUND, "'found' callback failed for B-tree find operation")
	}
	return nil
}

// numRecords is H5B2_get_nrec: retrieves the number of records in a B-tree.
func (bt *h5b2) numRecords() uint64 { return bt.hdr.rootAllNrec }

// neighbor is H5B2_neighbor: locate a record relative to the specified
// information in a B-tree and call op with it. rng (h5b2CompareLess or
// h5b2CompareGreater) indicates whether to search for the record with the
// largest key less than, or the smallest key greater than, the information
// passed in with udata.
func (bt *h5b2) neighbor(rng int, udata any, op func(rec any) error) error {
	/* Get the v2 B-tree header */
	hdr := bt.hdr

	/* Check for empty tree */
	if !addrDefined(hdr.rootAddr) {
		return errorf(H5E_BTREE, H5E_NOTFOUND, "B-tree has no records")
	}

	/* Attempt to find neighbor record in B-tree */
	root := hdr.rootPtr()
	if hdr.depth > 0 {
		if err := h5b2NeighborInternal(bt, hdr.depth, &root, nil, rng, udata, op); err != nil {
			return wrapf(err, H5E_BTREE, H5E_NOTFOUND, "unable to find neighbor record in B-tree internal node")
		}
	} else {
		if err := h5b2NeighborLeaf(bt, &root, nil, rng, udata, op); err != nil {
			return wrapf(err, H5E_BTREE, H5E_NOTFOUND, "unable to find neighbor record in B-tree leaf node")
		}
	}
	return nil
}
