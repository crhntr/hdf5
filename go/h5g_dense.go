package hdf5

// Translated from src/H5Gdense.c (read side only): "dense" link storage,
// where the links of a new-style group are link messages stored as objects
// in a fractal heap, indexed by a v2 B-tree on the hash of their names and,
// optionally, a v2 B-tree on their creation order.
//
// The fractal heap and the v2 B-trees are opened with hfOpen/b2Open; their
// handles hold nothing that needs closing.

// h5gDenseDecodeLink is the H5O_msg_decode(f, NULL, H5O_LINK_ID, ...) step
// of the dense storage callbacks: decode the link message stored in a
// fractal heap object. The result does not alias obj.
func h5gDenseDecodeLink(f *File, obj []byte) (*h5oLink, error) {
	native, err := f.msgDecode(nil, h5oLinkID, obj)
	if err != nil {
		return nil, err
	}
	lnk, ok := native.(*h5oLink)
	if !ok {
		return nil, errorf(H5E_SYM, H5E_CANTDECODE, "can't decode link")
	}
	return lnk, nil
}

// h5gDenseRecordID returns the fractal heap ID of a record of either dense
// link index (the C callbacks rely on the heap ID being the first field of
// both record structures).
func h5gDenseRecordID(rec any) ([]byte, error) {
	switch r := rec.(type) {
	case *h5gDenseBt2NameRec:
		return r.id[:], nil
	case *h5gDenseBt2CorderRec:
		return r.id[:], nil
	}
	return nil, errorf(H5E_SYM, H5E_BADTYPE, "unexpected dense link record type %T", rec)
}

// h5gDenseBt2Class returns the v2 B-tree class of the index at addr: the
// name index or the creation order index of the group.
func h5gDenseBt2Class(linfo *h5oLinfo, addr haddr) *h5b2Class {
	if addr == linfo.corderBt2Addr {
		return h5gBt2Corder()
	}
	return h5gBt2Name()
}

// gDenseLookup is H5G__dense_lookup: look up a link within a group that
// uses dense link storage. It returns found=false (and no error) when there
// is no link with that name.
func (f *File) gDenseLookup(linfo *h5oLinfo, name string) (*h5oLink, bool, error) {
	/* Open the fractal heap */
	fheap, err := f.hfOpen(linfo.fheapAddr)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open fractal heap")
	}

	/* Open the name index v2 B-tree */
	bt2Name, err := f.b2Open(linfo.nameBt2Addr, h5gBt2Name(), nil)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open v2 B-tree for name index")
	}

	/* Construct the user data for v2 B-tree callback */
	var result *h5oLink
	udata := &h5gBt2UdCommon{
		fheap:      fheap,
		name:       name,
		nameHash:   checksumLookup3([]byte(name), 0),
		decodeLink: h5gDenseDecodeLink,
		/* H5G__dense_lookup_cb: copy link information */
		foundOp: func(lnk *h5oLink) error {
			result = lnk
			return nil
		},
	}

	/* Find & copy the named link in the 'name' index */
	found, err := bt2Name.find(udata, nil)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_CANTINSERT, "unable to locate link in name index")
	}
	if found && result == nil {
		return nil, false, errorf(H5E_SYM, H5E_NOTFOUND, "link found in name index but not decoded")
	}
	return result, found, nil
}

// gDenseLookupByIdxBt2Cb is H5G__dense_lookup_by_idx_bt2_cb (with
// H5G__dense_lookup_by_idx_fh_cb): decode the link a dense index record
// points to.
func (f *File) gDenseLookupByIdxBt2Cb(fheap *h5hf, record any) (*h5oLink, error) {
	id, err := h5gDenseRecordID(record)
	if err != nil {
		return nil, err
	}

	/* Call fractal heap 'op' routine, to copy the link information */
	var lnk *h5oLink
	if err := fheap.op(id, func(obj []byte) error {
		/* Decode link information & keep a copy */
		l, err := h5gDenseDecodeLink(f, obj)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTDECODE, "can't decode link")
		}
		lnk = l
		return nil
	}); err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTOPERATE, "link found callback failed")
	}
	return lnk, nil
}

// gDenseLookupByIdx is H5G__dense_lookup_by_idx: look up a link within a
// group that uses dense link storage, according to the order of an index.
func (f *File) gDenseLookupByIdx(linfo *h5oLinfo, idxType h5Index, order h5IterOrder, n uint64) (*h5oLink, error) {
	/* Determine the address of the index to use */
	var bt2Addr haddr
	if idxType == h5IndexName {
		/* Since names are hashed, getting them in strictly increasing or
		 *      decreasing order requires building a table and sorting it.
		 *      If the order is native, use the B-tree for names.
		 */
		bt2Addr = haddrUndef
	} else {
		/* This address may not be defined if creation order is tracked, but
		 *      there's no index on it.  If there's no v2 B-tree that indexes
		 *      the links and the order is native, use the B-tree for names.
		 *      Otherwise, build a table.
		 */
		bt2Addr = linfo.corderBt2Addr
	}

	/* If the order is native and there's no B-tree for indexing the links,
	 * use the B-tree for names instead of building a table to speed up the
	 * process.
	 */
	if order == h5IterNative && !addrDefined(bt2Addr) {
		bt2Addr = linfo.nameBt2Addr
	}

	/* If there is an index defined for the field, use it */
	if addrDefined(bt2Addr) {
		/* Open the fractal heap */
		fheap, err := f.hfOpen(linfo.fheapAddr)
		if err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open fractal heap")
		}

		/* Open the index v2 B-tree */
		bt2, err := f.b2Open(bt2Addr, h5gDenseBt2Class(linfo, bt2Addr), nil)
		if err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open v2 B-tree for index")
		}

		/* Find & copy the link in the appropriate index */
		var lnk *h5oLink
		if err := bt2.indexOrder(order == h5IterDec, n, func(rec any) error {
			l, err := f.gDenseLookupByIdxBt2Cb(fheap, rec)
			lnk = l
			return err
		}); err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTINSERT, "unable to locate link in index")
		}
		return lnk, nil
	}

	/* Otherwise, we need to build a table of the links and sort it */
	/* Build the table of links for this group */
	ltable, err := f.gDenseBuildTable(linfo, idxType, order)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTGET, "error building table of links")
	}

	/* Check for going out of bounds */
	if n >= uint64(len(ltable.lnks)) {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
	}

	/* Copy link information */
	return ltable.lnks[n], nil
}

// gDenseBuildTable is H5G__dense_build_table: build a table containing a
// sorted list of the links of a group (used for iterating in an order that
// no index provides natively).
func (f *File) gDenseBuildTable(linfo *h5oLinfo, idxType h5Index, order h5IterOrder) (*h5gLinkTable, error) {
	ltable := &h5gLinkTable{}

	/* Allocate space for the table entries */
	if linfo.nlinks > 0 {
		/* Allocate the table to store the links */
		/* (the count comes from the file: only use it as a capacity hint
		 *  when it is plausible) */
		ltable.lnks = make([]*h5oLink, 0, min(linfo.nlinks, f.eof()/8+1, 1<<20))

		/* Iterate over the links in the group, building a table of the link messages */
		/* (H5G__dense_build_table_cb: copy link information) */
		if err := f.gDenseIterate(linfo, h5IndexName, h5IterNative, 0, nil, func(lnk *h5oLink) error {
			ltable.lnks = append(ltable.lnks, lnk)
			return nil
		}); err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTNEXT, "error iterating over links")
		}

		/* Sort link table in correct iteration order */
		h5gLinkSortTable(ltable, idxType, order)
	}
	return ltable, nil
}

// gDenseIterate is H5G__dense_iterate: iterate over the objects in a group
// using dense link storage.
func (f *File) gDenseIterate(linfo *h5oLinfo, idxType h5Index, order h5IterOrder, skip uint64, lastLnk *uint64, op h5gLibIterate) error {
	/* Determine the address of the index to use */
	var bt2Addr haddr
	if idxType == h5IndexName {
		/* Since names are hashed, getting them in strictly increasing or
		 * decreasing order requires building a table and sorting it. If
		 * the order is native, use the B-tree for names.
		 */
		bt2Addr = haddrUndef
	} else {
		/* This address may not be defined if creation order is tracked, but
		 *      there's no index on it.  If there's no v2 B-tree that indexes
		 *      the links and the order is native, use the B-tree for names.
		 *      Otherwise, build a table.
		 */
		bt2Addr = linfo.corderBt2Addr
	}

	/* If the order is native and there's no B-tree for indexing the links,
	 * use the B-tree for names instead of building a table to speed up the
	 * process.
	 */
	if order == h5IterNative && !addrDefined(bt2Addr) {
		bt2Addr = linfo.nameBt2Addr
	}

	/* Check on iteration order */
	if order == h5IterNative {
		/* Open the fractal heap */
		fheap, err := f.hfOpen(linfo.fheapAddr)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open fractal heap")
		}

		/* Open the index v2 B-tree */
		bt2, err := f.b2Open(bt2Addr, h5gDenseBt2Class(linfo, bt2Addr), nil)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open v2 B-tree for index")
		}

		/* Construct the user data for v2 B-tree iterator callback */
		var count uint64

		/* Iterate over the records in the v2 B-tree's "native" order */
		/* (by hash of name) */
		/* (H5G__dense_iterate_bt2_cb) */
		err = bt2.iterate(func(rec any) error {
			var cbErr error

			/* Check for skipping links */
			if skip > 0 {
				skip--
			} else {
				id, err := h5gDenseRecordID(rec)
				if err != nil {
					return err
				}

				/* Call fractal heap 'op' routine, to copy the link information */
				/* (H5G__dense_iterate_fh_cb: decode link information & keep a copy) */
				var lnk *h5oLink
				if err := fheap.op(id, func(obj []byte) error {
					l, err := h5gDenseDecodeLink(f, obj)
					if err != nil {
						return wrapf(err, H5E_SYM, H5E_CANTDECODE, "can't decode link")
					}
					lnk = l
					return nil
				}); err != nil {
					return wrapf(err, H5E_SYM, H5E_CANTOPERATE, "heap op callback failed")
				}

				/* Make the callback */
				cbErr = op(lnk)
			}

			/* Increment the number of entries passed through */
			/* (whether we skipped them or not) */
			count++

			/* Check for callback failure and pass along return value */
			return cbErr
		})

		/* Update the last link examined, if requested */
		if lastLnk != nil {
			*lastLnk = count
		}
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_BADITER, "link iteration failed")
		}
		return nil
	}

	/* Build the table of links for this group */
	ltable, err := f.gDenseBuildTable(linfo, idxType, order)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTGET, "error building table of links")
	}

	/* Iterate over links in table */
	return h5gLinkIterateTable(ltable, skip, lastLnk, op)
}

// gDenseGetNameByIdx is H5G__dense_get_name_by_idx: the name of the n-th
// link of a group with dense link storage, in the given index order.
func (f *File) gDenseGetNameByIdx(linfo *h5oLinfo, idxType h5Index, order h5IterOrder, n uint64) (string, error) {
	/* Determine the address of the index to use */
	var bt2Addr haddr
	if idxType == h5IndexName {
		/* Since names are hashed, getting them in strictly increasing or
		 * decreasing order requires building a table and sorting it.  If
		 * the order is native, use the B-tree for names.
		 */
		bt2Addr = haddrUndef
	} else {
		/* This address may not be defined if creation order is tracked, but
		 *      there's no index on it.  If there's no v2 B-tree that indexes
		 *      the links and the order is native, use the B-tree for names.
		 *      Otherwise, build a table.
		 */
		bt2Addr = linfo.corderBt2Addr
	}

	/* If the order is native and there's no B-tree for indexing the links,
	 * use the B-tree for names instead of building a table to speed up the
	 * process.
	 */
	if order == h5IterNative && !addrDefined(bt2Addr) {
		bt2Addr = linfo.nameBt2Addr
	}

	/* If there is an index defined for the field, use it */
	if addrDefined(bt2Addr) {
		/* Open the fractal heap */
		fheap, err := f.hfOpen(linfo.fheapAddr)
		if err != nil {
			return "", wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open fractal heap")
		}

		/* Open the index v2 B-tree */
		bt2, err := f.b2Open(bt2Addr, h5gDenseBt2Class(linfo, bt2Addr), nil)
		if err != nil {
			return "", wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open v2 B-tree for index")
		}

		/* Retrieve the name according to the v2 B-tree's index order */
		/* (H5G__dense_get_name_by_idx_bt2_cb / _fh_cb) */
		var name string
		if err := bt2.indexOrder(order == h5IterDec, n, func(rec any) error {
			lnk, err := f.gDenseLookupByIdxBt2Cb(fheap, rec)
			if err != nil {
				return err
			}
			name = lnk.name
			return nil
		}); err != nil {
			return "", wrapf(err, H5E_SYM, H5E_CANTLIST, "can't locate object in v2 B-tree")
		}
		return name, nil
	}

	/* Otherwise, we need to build a table of the links and sort it */
	/* Build the table of links for this group */
	ltable, err := f.gDenseBuildTable(linfo, idxType, order)
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_CANTGET, "error building table of links")
	}

	/* Check for going out of bounds */
	if n >= uint64(len(ltable.lnks)) {
		return "", errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
	}

	/* Get the name */
	return ltable.lnks[n].name, nil
}
