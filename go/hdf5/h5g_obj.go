package hdf5

// Translated from src/H5Gobj.c (read side only): the functions that
// dispatch group operations to the storage used by the group: "dense"
// (h5g_dense.go) or "compact" (h5g_compact.go) link storage for new-style
// groups, which have a link info message, and symbol tables (h5g_stab.go)
// for old-style groups.
//
// The C functions take the group's object location; here they take the
// group's loaded object header.

// H5G_storage_type_t: types of link storage for groups.
type h5gStorageType int

const (
	h5gStorageTypeUnknown     h5gStorageType = -1 /* Unknown link storage type	*/
	h5gStorageTypeSymbolTable h5gStorageType = 0  /* Links in group are stored with a "symbol table" */
	h5gStorageTypeCompact     h5gStorageType = 1  /* Links are stored in object header */
	h5gStorageTypeDense       h5gStorageType = 2  /* Links are stored in fractal heap & indexed with v2 B-tree */
)

// h5gInfo is H5G_info_t: information about a group.
type h5gInfo struct {
	storageType h5gStorageType /* Type of storage for links in group */
	nlinks      uint64         /* Number of links in group */
	maxCorder   int64          /* Current max. creation order value for group */
	mounted     bool           /* Whether group has a file mounted on it */
}

// gObjGetLinfo is H5G__obj_get_linfo: retrieve the "link info" message for
// a group, with the number of links set (counted from the name index for
// dense storage, from the link messages for compact storage). exists is
// false when the group has no link info message (an old-style group). The
// returned message is a copy and may be modified.
func (f *File) gObjGetLinfo(grpOh *h5o) (linfo *h5oLinfo, exists bool, err error) {
	/* Check for the group having a link info message */
	if !f.msgExists(grpOh, h5oLinfoID) {
		return nil, false, nil
	}

	/* Retrieve the "link info" structure */
	native, err := f.msgRead(grpOh, h5oLinfoID)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_CANTGET, "link info message not present")
	}
	cached, ok := native.(*h5oLinfo)
	if !ok {
		return nil, false, errorf(H5E_SYM, H5E_CANTGET, "link info message not present")
	}
	l := *cached
	linfo = &l

	/* Check if we don't know how many links there are */
	if linfo.nlinks == ^uint64(0) {
		/* Check if we are using "dense" link storage */
		if addrDefined(linfo.fheapAddr) {
			/* Open the name index v2 B-tree */
			bt2Name, err := f.b2Open(linfo.nameBt2Addr, h5gBt2Name(), nil)
			if err != nil {
				return nil, false, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open v2 B-tree for name index")
			}

			/* Retrieve # of records in "name" B-tree */
			/* (should be same # of records in all indices) */
			linfo.nlinks = bt2Name.numRecords()
		} else {
			/* Retrieve # of links from object header */
			linfo.nlinks = uint64(f.msgCount(grpOh, h5oLinkID))
		}
	}
	return linfo, true, nil
}

// gObjStab reads the symbol table message of an old-style group.
func (f *File) gObjStab(grpOh *h5o) (*h5oStab, error) {
	native, err := f.msgRead(grpOh, h5oStabID)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_BADMESG, "unable to read symbol table message")
	}
	stab, ok := native.(*h5oStab)
	if !ok {
		return nil, errorf(H5E_SYM, H5E_BADMESG, "unable to read symbol table message")
	}
	return stab, nil
}

// gObjStabEntToLink is the H5G__ent_to_link step of the symbol table
// callbacks (H5G__stab_iterate, H5G__stab_lookup, ...): convert a symbol
// table entry of the group into a link message.
func (f *File) gObjStabEntToLink(stab *h5oStab, ent *h5gEntry) (*h5oLink, error) {
	heap, err := f.hlProtect(stab.heapAddr)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table heap")
	}
	lnk, err := gEntToLink(ent, heap)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTCONVERT, "unable to convert symbol table entry to link")
	}
	return lnk, nil
}

// gObjStabIterate is H5G__stab_iterate over the links of an old-style
// group as link messages, in increasing or native (name) order, or in
// decreasing order by building a table (H5G__node_build_table) and sorting
// it.
func (f *File) gObjStabIterate(grpOh *h5o, order h5IterOrder, skip uint64, lastLnk *uint64, op h5gLibIterate) error {
	stab, err := f.gObjStab(grpOh)
	if err != nil {
		return err
	}

	/* Check on iteration order */
	/* ("native" iteration order is increasing for this link storage mechanism) */
	if order != h5IterDec {
		/* Iterate over the group members */
		n, err := f.gStabIterateSkip(stab, skip, func(_ string, ent *h5gEntry) error {
			lnk, err := f.gObjStabEntToLink(stab, ent)
			if err != nil {
				return err
			}
			return op(lnk)
		})
		if lastLnk != nil {
			*lastLnk = n
		}
		return err
	}

	/* Build udata to pass through H5B_iterate() to H5G__node_build_table() */
	ltable := &h5gLinkTable{}
	if err := f.gStabIterate(stab, func(_ string, ent *h5gEntry) error {
		lnk, err := f.gObjStabEntToLink(stab, ent)
		if err != nil {
			return err
		}
		ltable.lnks = append(ltable.lnks, lnk)
		return nil
	}); err != nil {
		return wrapf(err, H5E_SYM, H5E_NOTFOUND, "unable to build link table")
	}

	/* Check for skipping out of bounds */
	if skip > 0 && skip >= uint64(len(ltable.lnks)) {
		return errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
	}

	/* Sort link table in correct iteration order */
	h5gLinkSortTable(ltable, h5IndexName, order)

	/* Iterate over links in table */
	return h5gLinkIterateTable(ltable, skip, lastLnk, op)
}

// gObjIterate is H5G__obj_iterate: iterate over the links of a group in
// the given index and order, skipping the first skip links. op may return
// errBIterStop to stop early (gObjIterate then returns nil). lastLnk, if not
// nil, receives the number of links passed through.
func (f *File) gObjIterate(grpOh *h5o, idxType h5Index, order h5IterOrder, skip uint64, lastLnk *uint64, op h5gLibIterate) error {
	/* Attempt to get the link info for this group */
	linfo, linfoExists, err := f.gObjGetLinfo(grpOh)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTGET, "can't check for link info message")
	}
	if linfoExists {
		/* Check for going out of bounds */
		if skip > 0 && skip >= linfo.nlinks {
			return errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
		}

		/* Check for creation order tracking, if creation order index lookup requested */
		if idxType == h5IndexCrtOrder {
			/* Check if creation order is tracked */
			if !linfo.trackCorder {
				return errorf(H5E_SYM, H5E_NOTFOUND, "creation order not tracked for links in group")
			}
		}

		if addrDefined(linfo.fheapAddr) {
			/* Iterate over the links in the group, building a table of the link messages */
			if err := f.gDenseIterate(linfo, idxType, order, skip, lastLnk, op); err != nil {
				return wrapf(err, H5E_SYM, H5E_BADITER, "can't iterate over dense links")
			}
		} else {
			/* Get the object's name from the link messages */
			if err := f.gCompactIterate(grpOh, linfo, idxType, order, skip, lastLnk, op); err != nil {
				return wrapf(err, H5E_SYM, H5E_BADITER, "can't iterate over compact links")
			}
		}
	} else {
		/* Can only perform name lookups on groups with symbol tables */
		if idxType != h5IndexName {
			return errorf(H5E_SYM, H5E_BADVALUE, "no creation order index to query")
		}

		/* Iterate over symbol table */
		if err := f.gObjStabIterate(grpOh, order, skip, lastLnk, op); err != nil {
			return wrapf(err, H5E_SYM, H5E_BADITER, "can't iterate over symbol table")
		}
	}
	return nil
}

// gObjInfo is H5G__obj_info: retrieve information about a group.
func (f *File) gObjInfo(grpOh *h5o) (h5gInfo, error) {
	var grpInfo h5gInfo

	/* Get information from the group */
	/* (mounting files is not supported) */
	grpInfo.mounted = false

	/* Attempt to get the link info for this group */
	linfo, linfoExists, err := f.gObjGetLinfo(grpOh)
	if err != nil {
		return h5gInfo{}, wrapf(err, H5E_SYM, H5E_CANTGET, "can't check for link info message")
	}
	if linfoExists {
		/* Retrieve the information about the links */
		grpInfo.nlinks = linfo.nlinks
		grpInfo.maxCorder = linfo.maxCorder

		/* Check if the group is using compact or dense storage for its links */
		if addrDefined(linfo.fheapAddr) {
			grpInfo.storageType = h5gStorageTypeDense
		} else {
			grpInfo.storageType = h5gStorageTypeCompact
		}
	} else {
		/* Get the number of objects in this group by iterating over symbol table */
		stab, err := f.gObjStab(grpOh)
		if err != nil {
			return h5gInfo{}, err
		}
		n, err := f.gStabCount(stab)
		if err != nil {
			return h5gInfo{}, wrapf(err, H5E_SYM, H5E_CANTCOUNT, "can't count objects")
		}
		grpInfo.nlinks = n

		/* Set the other information about the group */
		grpInfo.storageType = h5gStorageTypeSymbolTable
		grpInfo.maxCorder = 0
	}
	return grpInfo, nil
}

// gObjGetNameByIdx is H5G_obj_get_name_by_idx: the name of the n-th link
// of a group in the given index order.
func (f *File) gObjGetNameByIdx(grpOh *h5o, idxType h5Index, order h5IterOrder, n uint64) (string, error) {
	/* Attempt to get the link info for this group */
	linfo, linfoExists, err := f.gObjGetLinfo(grpOh)
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_CANTGET, "can't check for link info message")
	}
	if linfoExists {
		/* Check for creation order tracking, if creation order index lookup requested */
		if idxType == h5IndexCrtOrder {
			/* Check if creation order is tracked */
			if !linfo.trackCorder {
				return "", errorf(H5E_SYM, H5E_NOTFOUND, "creation order not tracked for links in group")
			}
		}

		/* Check for dense link storage */
		if addrDefined(linfo.fheapAddr) {
			/* Get the object's name from the dense link storage */
			name, err := f.gDenseGetNameByIdx(linfo, idxType, order, n)
			if err != nil {
				return "", wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate name")
			}
			return name, nil
		}
		/* Get the object's name from the link messages */
		name, err := f.gCompactGetNameByIdx(grpOh, linfo, idxType, order, n)
		if err != nil {
			return "", wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate name")
		}
		return name, nil
	}

	/* Can only perform name lookups on groups with symbol tables */
	if idxType != h5IndexName {
		return "", errorf(H5E_SYM, H5E_BADVALUE, "no creation order index to query")
	}

	/* Get the object's name from the symbol table */
	lnk, err := f.gObjStabLookupByIdx(grpOh, order, n)
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate name")
	}
	return lnk.name, nil
}

// gObjStabLookupByIdx is H5G__stab_lookup_by_idx for any order: the n-th
// link of an old-style group in name order (decreasing order is mapped to
// the increasing index, as H5G__stab_lookup_by_idx does).
func (f *File) gObjStabLookupByIdx(grpOh *h5o, order h5IterOrder, n uint64) (*h5oLink, error) {
	stab, err := f.gObjStab(grpOh)
	if err != nil {
		return nil, err
	}

	/* Remap index for decreasing iteration order */
	if order == h5IterDec {
		/* Iterate over the symbol table nodes, to count the links */
		nlinks, err := f.gStabCount(stab)
		if err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTCOUNT, "unable to count links")
		}

		/* Check for going out of bounds */
		if n >= nlinks {
			return nil, errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
		}

		/* Map decreasing iteration order index to increasing iteration order index */
		n = nlinks - (n + 1)
	}

	_, ent, err := f.gStabLookupByIdx(stab, n)
	if err != nil {
		return nil, err
	}
	return f.gObjStabEntToLink(stab, ent)
}

// gObjLookup is H5G__obj_lookup: look up a link in a group, using the name
// as the key. It returns found=false (and no error) when the group has no
// link with that name.
func (f *File) gObjLookup(grpOh *h5o, name string) (*h5oLink, bool, error) {
	/* Attempt to get the link info message for this group */
	linfo, linfoExists, err := f.gObjGetLinfo(grpOh)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_CANTGET, "can't check for link info message")
	}
	if linfoExists {
		/* Check for dense link storage */
		if addrDefined(linfo.fheapAddr) {
			/* Get the object's info from the dense link storage */
			lnk, found, err := f.gDenseLookup(linfo, name)
			if err != nil {
				return nil, false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
			}
			return lnk, found, nil
		}
		/* Get the object's info from the link messages */
		lnk, found, err := f.gCompactLookup(grpOh, name)
		if err != nil {
			return nil, false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
		}
		return lnk, found, nil
	}

	/* Get the object's info from the symbol table */
	/* (H5G__stab_lookup) */
	stab, err := f.gObjStab(grpOh)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
	}
	ent, found, err := f.gStabLookup(stab, name)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
	}
	if !found {
		return nil, false, nil
	}
	lnk, err := f.gObjStabEntToLink(stab, ent)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
	}
	return lnk, true, nil
}

// gObjLookupByIdx is H5G_obj_lookup_by_idx: look up a link in a group,
// according to an order within an index.
func (f *File) gObjLookupByIdx(grpOh *h5o, idxType h5Index, order h5IterOrder, n uint64) (*h5oLink, error) {
	/* Attempt to get the link info message for this group */
	linfo, linfoExists, err := f.gObjGetLinfo(grpOh)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTGET, "can't check for link info message")
	}
	if linfoExists {
		/* Check for creation order tracking, if creation order index lookup requested */
		if idxType == h5IndexCrtOrder {
			/* Check if creation order is tracked */
			if !linfo.trackCorder {
				return nil, errorf(H5E_SYM, H5E_NOTFOUND, "creation order not tracked for links in group")
			}
		}

		/* Check for dense link storage */
		if addrDefined(linfo.fheapAddr) {
			/* Get the link from the dense storage */
			lnk, err := f.gDenseLookupByIdx(linfo, idxType, order, n)
			if err != nil {
				return nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
			}
			return lnk, nil
		}
		/* Get the link from the link messages */
		lnk, err := f.gCompactLookupByIdx(grpOh, linfo, idxType, order, n)
		if err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
		}
		return lnk, nil
	}

	/* Can only perform name lookups on groups with symbol tables */
	if idxType != h5IndexName {
		return nil, errorf(H5E_SYM, H5E_BADVALUE, "no creation order index to query")
	}

	/* Get the object's info from the symbol table */
	lnk, err := f.gObjStabLookupByIdx(grpOh, order, n)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't locate object")
	}
	return lnk, nil
}
