package hdf5

// Translated from src/H5Gcompact.c (read side only): "compact" link
// storage, where the links of a new-style group are link messages in the
// group's object header.
//
// The C functions take the group's object location; here they take the
// group's loaded object header.

// gCompactBuildTable is H5G__compact_build_table: build a table containing
// a sorted list of the links of a group (the link messages of its object
// header).
func (f *File) gCompactBuildTable(oh *h5o, linfo *h5oLinfo, idxType h5Index, order h5IterOrder) (*h5gLinkTable, error) {
	ltable := &h5gLinkTable{}

	/* Allocate space for the table entries */
	if linfo.nlinks > 0 {
		/* Allocate the link table */
		/* (bounded by the number of messages in the header) */
		ltable.lnks = make([]*h5oLink, 0, min(linfo.nlinks, uint64(len(oh.mesgs))))

		/* Iterate through the link messages, adding them to the table */
		/* (H5G__compact_build_table_cb: copy link message into table) */
		if err := f.msgIterate(oh, h5oLinkID, func(_ *h5oMesg, native any) error {
			lnk, ok := native.(*h5oLink)
			if !ok {
				return errorf(H5E_SYM, H5E_CANTCOPY, "can't copy link message")
			}
			ltable.lnks = append(ltable.lnks, lnk)
			return nil
		}); err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "error iterating over link messages")
		}

		/* Sort link table in correct iteration order */
		h5gLinkSortTable(ltable, idxType, order)
	}
	return ltable, nil
}

// gCompactGetNameByIdx is H5G__compact_get_name_by_idx: the name of the
// idx-th link of the group in the given index order.
func (f *File) gCompactGetNameByIdx(oh *h5o, linfo *h5oLinfo, idxType h5Index, order h5IterOrder, idx uint64) (string, error) {
	/* Build table of all link messages */
	ltable, err := f.gCompactBuildTable(oh, linfo, idxType, order)
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_CANTINIT, "can't create link message table")
	}

	/* Check for going out of bounds */
	if idx >= uint64(len(ltable.lnks)) {
		return "", errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
	}

	/* Get the name */
	return ltable.lnks[idx].name, nil
}

// gCompactIterate is H5G__compact_iterate: iterate over the links in a
// group.
func (f *File) gCompactIterate(oh *h5o, linfo *h5oLinfo, idxType h5Index, order h5IterOrder, skip uint64, lastLnk *uint64, op h5gLibIterate) error {
	/* Build table of all link messages */
	ltable, err := f.gCompactBuildTable(oh, linfo, idxType, order)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTINIT, "can't create link message table")
	}

	/* Iterate over links in table */
	return h5gLinkIterateTable(ltable, skip, lastLnk, op)
}

// gCompactLookup is H5G__compact_lookup: look up an object relative to a
// group, using link messages. It returns found=false (and no error) when
// there is no link with that name.
func (f *File) gCompactLookup(oh *h5o, name string) (*h5oLink, bool, error) {
	var result *h5oLink

	/* Iterate through the link messages, looking for the name */
	/* (H5G__compact_lookup_cb) */
	if err := f.msgIterate(oh, h5oLinkID, func(_ *h5oMesg, native any) error {
		lnk, ok := native.(*h5oLink)
		if !ok {
			return errorf(H5E_SYM, H5E_CANTCOPY, "can't copy link message")
		}

		/* Check for name to get information */
		if lnk.name == name {
			/* Copy link information */
			result = lnk

			/* Stop iteration now */
			return errBIterStop
		}
		return nil
	}); err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "error iterating over link messages")
	}
	return result, result != nil, nil
}

// gCompactLookupByIdx is H5G__compact_lookup_by_idx: look up an object in a
// group using link messages, according to the order of an index.
func (f *File) gCompactLookupByIdx(oh *h5o, linfo *h5oLinfo, idxType h5Index, order h5IterOrder, n uint64) (*h5oLink, error) {
	/* Build table of all link messages, sorted according to desired order */
	ltable, err := f.gCompactBuildTable(oh, linfo, idxType, order)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTINIT, "can't create link message table")
	}

	/* Check for going out of bounds */
	if n >= uint64(len(ltable.lnks)) {
		return nil, errorf(H5E_ARGS, H5E_BADRANGE, "index out of bound")
	}

	/* Copy link information */
	return ltable.lnks[n], nil
}
