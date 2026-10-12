package hdf5

// Translated from src/H5Gstab.c (read side only): "old style" groups whose
// links are stored in a symbol table (a v1 B-tree of symbol table nodes plus
// a local heap of names).
//
// The C functions take the group's object location and read the symbol
// table message (H5O_msg_read(oloc, H5O_STAB_ID, &stab)) themselves; here
// the caller passes the decoded message. Only increasing name order (the
// "native" order of this storage) is supported; callers wanting decreasing
// order can count with gStabCount and remap indices, or reverse a collected
// list (the C library does the latter with H5G__node_build_table and
// H5G__link_sort_table).

import "iter"

// gStabEntName is the name part of H5G__ent_to_link (src/H5Gent.c): the
// link name of a symbol table entry, from the group's local heap.
func gStabEntName(heap *h5hl, ent *h5gEntry) (string, error) {
	/* Get pointer to link's name in the heap */
	name, err := heap.stringAt(ent.nameOff)
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get symbol table link name")
	}
	if name == "" {
		return "", errorf(H5E_SYM, H5E_BADVALUE, "invalid link name")
	}
	return name, nil
}

// gStabIterateSkip is H5G__stab_iterate for H5_ITER_INC / H5_ITER_NATIVE
// order: call op(name, entry) for the links of the group, in name order,
// skipping the first skip links. op may return errBIterStop to stop early.
// It returns the number of entries passed through (*last_lnk).
func (f *File) gStabIterateSkip(stab *h5oStab, skip uint64, op func(name string, ent *h5gEntry) error) (lastLnk uint64, err error) {
	/* Pin the heap down in memory */
	heap, err := f.hlProtect(stab.heapAddr)
	if err != nil {
		return 0, wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table heap")
	}

	/* Build udata to pass through H5B_iterate() to H5G__node_iterate() */
	udata := &h5gBtItIt{
		heap:     heap,
		skip:     skip,
		finalEnt: &lastLnk,
		op: func(ent *h5gEntry) error {
			/* Convert the entry to a link */
			name, err := gStabEntName(heap, ent)
			if err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTCONVERT, "unable to convert symbol table entry to link")
			}
			e := *ent
			return op(name, &e)
		},
	}

	/* Iterate over the group members */
	if err := f.bIterate(h5bSnode, nil, stab.btreeAddr, func(_, _ any, addr haddr) error {
		return f.gNodeIterate(addr, udata)
	}); err != nil {
		return lastLnk, wrapf(err, H5E_SYM, H5E_CANTNEXT, "iteration operator failed")
	}

	/* Check for too high of a starting index (ex post facto :-) */
	/* (Skipping exactly as many entries as are in the group is currently an error) */
	if skip > 0 && skip >= lastLnk {
		return lastLnk, errorf(H5E_ARGS, H5E_BADVALUE, "invalid index specified")
	}
	return lastLnk, nil
}

// gStabIterate is H5G__stab_iterate (increasing order, no skip): call
// op(name, entry) for every link of the group in B-tree (sorted name)
// order. op may return errBIterStop to stop early without error. The entry
// passed to op is a copy and may be retained.
func (f *File) gStabIterate(stab *h5oStab, op func(name string, ent *h5gEntry) error) error {
	_, err := f.gStabIterateSkip(stab, 0, op)
	return err
}

// h5gStabLink is one link yielded by gStabLinks.
type h5gStabLink struct {
	name string
	ent  *h5gEntry
}

// gStabLinks is the iterator form of gStabIterate: it yields every link of
// the group in name order. On failure it yields a final (zero, err) pair.
func (f *File) gStabLinks(stab *h5oStab) iter.Seq2[h5gStabLink, error] {
	return func(yield func(h5gStabLink, error) bool) {
		err := f.gStabIterate(stab, func(name string, ent *h5gEntry) error {
			if !yield(h5gStabLink{name: name, ent: ent}, nil) {
				return errBIterStop
			}
			return nil
		})
		if err != nil {
			yield(h5gStabLink{}, err)
		}
	}
}

// gStabCount is H5G__stab_count: count the number of links in a group.
func (f *File) gStabCount(stab *h5oStab) (uint64, error) {
	/* Reset the number of objects in the group */
	var numObjs uint64

	/* Iterate over the group members */
	if err := f.bIterate(h5bSnode, nil, stab.btreeAddr, func(_, _ any, addr haddr) error {
		return f.gNodeSumup(addr, &numObjs)
	}); err != nil {
		return 0, wrapf(err, H5E_SYM, H5E_BADITER, "iteration operator failed")
	}
	return numObjs, nil
}

// gStabBhSize is H5G__stab_bh_size: retrieve storage for the B-tree (and
// symbol table nodes) and the local heap of a symbol table (as reported by
// H5Oget_info's meta_size.obj / h5stat).
func (f *File) gStabBhSize(stab *h5oStab) (indexSize, heapSize uint64, err error) {
	/* Set up user data for B-tree iteration */
	var snodeSize uint64

	/* Get the B-tree & symbol table node size info */
	btInfo, err := f.bGetInfo(h5bSnode, nil, stab.btreeAddr, func(_, _ any, _ haddr) error {
		f.gNodeIterateSize(&snodeSize)
		return nil
	})
	if err != nil {
		return 0, 0, wrapf(err, H5E_SYM, H5E_CANTGET, "iteration operator failed")
	}

	/* Add symbol table & B-tree node sizes to index info */
	indexSize = snodeSize + btInfo.size

	/* Get the size of the local heap for the group */
	if heapSize, err = f.hlHeapsize(stab.heapAddr); err != nil {
		return 0, 0, wrapf(err, H5E_SYM, H5E_CANTGET, "iteration operator failed")
	}
	return indexSize, heapSize, nil
}

// gStabGetNameByIdx is H5G__stab_get_name_by_idx (increasing order): the
// name of the n-th link of the group in name order.
func (f *File) gStabGetNameByIdx(stab *h5oStab, n uint64) (string, error) {
	/* Pin the heap down in memory */
	heap, err := f.hlProtect(stab.heapAddr)
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table heap")
	}

	/* Set iteration information */
	var name *string
	udata := &h5gBtItIdxCommon{
		idx:     n,
		numObjs: 0,
		/* H5G__stab_get_name_by_idx_cb */
		op: func(ent *h5gEntry) error {
			s, err := heap.stringAt(ent.nameOff)
			if err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get symbol table link name")
			}
			name = &s
			return nil
		},
	}

	/* Iterate over the group members */
	if err := f.bIterate(h5bSnode, nil, stab.btreeAddr, func(_, _ any, addr haddr) error {
		return f.gNodeByIdx(addr, udata)
	}); err != nil {
		return "", wrapf(err, H5E_SYM, H5E_BADITER, "iteration operator failed")
	}

	/* If we don't know the name now, we almost certainly went out of bounds */
	if name == nil {
		return "", errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
	}
	return *name, nil
}

// gStabLookup is H5G__stab_lookup: look up a link by name in a symbol
// table. It returns found=false (and no error) when the group has no link
// with that name. The returned entry is a copy.
func (f *File) gStabLookup(stab *h5oStab, name string) (*h5gEntry, bool, error) {
	if name == "" {
		return nil, false, errorf(H5E_ARGS, H5E_BADVALUE, "no name given")
	}

	/* Pin the heap down in memory */
	heap, err := f.hlProtect(stab.heapAddr)
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table heap")
	}

	/* Set up user data to pass to 'find' operation callback */
	var result *h5gEntry

	/* Set up the user data for actual B-tree find operation */
	btUdata := &h5gBtLkp{
		common: h5gBtCommon{
			name:      name,
			heap:      heap,
			blockSize: heap.heapGetSize(),
		},
		/* H5G__stab_lookup_cb */
		op: func(ent *h5gEntry) error {
			/* Convert the entry to a link */
			if _, err := gStabEntName(heap, ent); err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTCONVERT, "unable to convert symbol table entry to link")
			}
			e := *ent
			result = &e
			return nil
		},
	}

	/* Search the B-tree. As in the C code, a failing H5G__node_cmp3 (a key
	 * whose name offset is outside the heap) compares as "less than" and the
	 * failure itself is not reported. */
	cmp3 := func(lt, rt any) int {
		ltKey, ok1 := lt.(*h5gNodeKey)
		rtKey, ok2 := rt.(*h5gNodeKey)
		if !ok1 || !ok2 {
			return -1
		}
		c, _ := gNodeCmp3(ltKey, rtKey, &btUdata.common)
		return c
	}
	found, err := f.bFind(h5bSnode, nil, stab.btreeAddr, cmp3, func(_, _ any, addr haddr) error {
		return f.gNodeFound(addr, btUdata)
	})
	if err != nil {
		return nil, false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "not found")
	}
	if !found {
		return nil, false, nil
	}
	return result, true, nil
}

// gStabLookupByIdx is H5G__stab_lookup_by_idx (increasing order): the name
// and entry of the idx-th link of the group in name order. The returned
// entry is a copy.
func (f *File) gStabLookupByIdx(stab *h5oStab, idx uint64) (string, *h5gEntry, error) {
	/* Pin the heap down in memory */
	heap, err := f.hlProtect(stab.heapAddr)
	if err != nil {
		return "", nil, wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table heap")
	}

	/* Set iteration information */
	var (
		name   string
		result *h5gEntry
	)
	udata := &h5gBtItIdxCommon{
		idx:     idx,
		numObjs: 0,
		/* H5G__stab_lookup_by_idx_cb */
		op: func(ent *h5gEntry) error {
			/* Convert the entry to a link */
			n, err := gStabEntName(heap, ent)
			if err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTCONVERT, "unable to convert symbol table entry to link")
			}
			e := *ent
			name, result = n, &e
			return nil
		},
	}

	/* Iterate over the group members */
	if err := f.bIterate(h5bSnode, nil, stab.btreeAddr, func(_, _ any, addr haddr) error {
		return f.gNodeByIdx(addr, udata)
	}); err != nil {
		return "", nil, wrapf(err, H5E_SYM, H5E_BADITER, "iteration operator failed")
	}

	/* If we didn't find the link, we almost certainly went out of bounds */
	if result == nil {
		return "", nil, errorf(H5E_ARGS, H5E_BADVALUE, "index out of bound")
	}
	return name, result, nil
}

// gStabSoftValue returns the value of a soft link stored in a symbol table
// entry (ent.typ == h5gCachedSlink): the heap string at ent.lvalOffset. This
// is the soft-link part of H5G__ent_to_link (src/H5Gent.c).
func (f *File) gStabSoftValue(stab *h5oStab, ent *h5gEntry) (string, error) {
	if ent.typ != h5gCachedSlink {
		return "", errorf(H5E_SYM, H5E_BADTYPE, "symbol table entry is not a soft link")
	}
	heap, err := f.hlProtect(stab.heapAddr)
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table heap")
	}
	s, err := heap.stringAt(uint64(ent.lvalOffset))
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get symbolic link name")
	}
	return s, nil
}

// gStabValid is H5G__stab_valid (read-only): verify that a group's symbol
// table message is valid. The addresses in alt (the stab addresses cached in
// the group's symbol table entry, e.g. the superblock's root entry) are
// tried if the addresses in stab are invalid. The corrected message is
// returned; unlike the C function it is not written back to the file.
func (f *File) gStabValid(stab *h5oStab, alt *h5oStab) (*h5oStab, error) {
	out := *stab

	/* Check if the symbol table message's b-tree address is valid */
	if err := f.bValid(h5bSnode, nil, out.btreeAddr); err != nil {
		/* Address is invalid, try the b-tree address in the alternate symbol table message */
		if alt == nil {
			return nil, wrapf(err, H5E_SYM, H5E_BADVALUE, "unable to locate b-tree")
		}
		if err := f.bValid(h5bSnode, nil, alt.btreeAddr); err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_BADVALUE, "unable to locate b-tree")
		}
		/* The alternate symbol table's b-tree address is valid.  Adjust the
		 * symbol table message in the group. */
		out.btreeAddr = alt.btreeAddr
	}

	/* Check if the symbol table message's heap address is valid */
	if _, err := f.hlProtect(out.heapAddr); err != nil {
		/* Address is invalid, try the heap address in the alternate symbol table message */
		if alt == nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to locate heap")
		}
		if _, err := f.hlProtect(alt.heapAddr); err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to locate heap")
		}
		/* The alternate symbol table's heap address is valid.  Adjust the
		 * symbol table message in the group. */
		out.heapAddr = alt.heapAddr
	}
	return &out, nil
}
