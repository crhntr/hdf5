package hdf5

// Translated from src/H5Gnode.c (read side only): symbol table nodes and the
// H5B_SNODE v1 B-tree class that indexes them.

import "errors"

// h5gNodeKey is H5G_node_key_t: each key field of the B-link tree that
// points to symbol table nodes consists of this structure.
type h5gNodeKey struct {
	offset uint64 /*offset into heap for name          */
}

// h5bSnode is H5B_SNODE: the v1 B-tree class for symbol table nodes. Its
// keys are *h5gNodeKey; ctx is unused (nil).
var h5bSnode = &h5bClass{
	id:         h5bSnodeID,      /*id            */
	sizeofRkey: gNodeSizeofRkey, /*sizeof_rkey (H5G__node_init) */
	decodeKey:  gNodeDecodeKey,  /*decode        */
}

// gNodeSizeofRkey is the raw key size set up by H5G__node_init: the key is a
// heap offset (a file length).
func gNodeSizeofRkey(f *File, _ any) int {
	return int(f.sizeofSize) /*name offset */
}

// gNodeDecodeKey is H5G__node_decode_key: decodes a raw key into a native
// key.
func gNodeDecodeKey(f *File, _ any, p *image) (any, error) {
	key := &h5gNodeKey{}
	key.offset = p.length()
	if err := p.done(); err != nil {
		return nil, err
	}
	return key, nil
}

// h5gBtCommon is H5G_bt_common_t: common data exchange structure for symbol
// table nodes, passed through the B-link tree layer to the methods for the
// objects to which the B-link tree points.
type h5gBtCommon struct {
	/* downward */
	name      string /* Points to temporary memory */
	heap      *h5hl  /* Symbol table heap */
	blockSize uint64 /* Size of the heap block */
}

// h5gBtLkp is H5G_bt_lkp_t: data exchange structure for the 'find' method.
type h5gBtLkp struct {
	/* downward */
	common h5gBtCommon               /* Common info for B-tree user data (must be first) */
	op     func(ent *h5gEntry) error /* Operator to call when correct entry is found */
}

// h5gBtItIt is H5G_bt_it_it_t: data exchange structure to pass through the
// B-tree layer for the H5B_iterate function. The C operator receives an
// H5O_link_t built by H5G__ent_to_link; here it receives the entry (the
// heap is in the structure for the conversion).
type h5gBtItIt struct {
	/* downward */
	heap *h5hl                     /*symbol table heap                  */
	skip uint64                    /*initial entries to skip             */
	op   func(ent *h5gEntry) error /*iteration operator                 */

	/* upward */
	finalEnt *uint64 /*final entry looked at                      */
}

// h5gBtItIdxCommon is H5G_bt_it_idx_common_t: common information for
// "by index" lookups in symbol tables.
type h5gBtItIdxCommon struct {
	/* downward */
	idx     uint64                    /* Index of group member to be queried */
	numObjs uint64                    /* The number of objects having been traversed */
	op      func(ent *h5gEntry) error /* Callback for B-tree iteration */
}

// h5gStrncmp is strncmp(s, (const char *)b, n) with n == len(b), as used by
// the H5Gnode.c comparisons with udata->block_size - offset: s is a Go
// string treated as NUL-terminated, b is the heap data from the name offset
// to the end of the heap data block.
func h5gStrncmp(s string, b []byte) int {
	for i := 0; i < len(b); i++ {
		var c1 byte
		if i < len(s) {
			c1 = s[i]
		}
		c2 := b[i]
		if c1 != c2 {
			return int(c1) - int(c2)
		}
		if c1 == 0 {
			return 0
		}
	}
	return 0
}

// gNodeCmp3 is H5G__node_cmp3: compares two keys from a B-tree node (LT_KEY
// and RT_KEY) against another key (not necessarily the same type) pointed to
// by UDATA.
//
// Return: negative if the UDATA key is less than or equal to the LT_KEY;
// positive if the UDATA key is greater than the RT_KEY; zero if the UDATA key
// falls between the LT_KEY (exclusive) and the RT_KEY (inclusive). On
// failure the C function returns FAIL, which callers treat as "less than";
// here the error is returned alongside -1.
func gNodeCmp3(ltKey, rtKey *h5gNodeKey, udata *h5gBtCommon) (int, error) {
	/* left side */
	s, err := udata.heap.offsetInto(ltKey.offset)
	if err != nil {
		return -1, wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get key name")
	}
	if h5gStrncmp(udata.name, s) <= 0 {
		return -1, nil
	}

	/* right side */
	s, err = udata.heap.offsetInto(rtKey.offset)
	if err != nil {
		return -1, wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get key name")
	}
	if h5gStrncmp(udata.name, s) > 0 {
		return 1, nil
	}
	return 0, nil
}

// gNodeFound is H5G__node_found: the B-tree search engine has found the
// symbol table node which contains the requested symbol if the symbol
// exists. This function examines that node for the symbol and calls the
// udata operator on it. It returns errBNotFound when the symbol is absent
// (the C code's *found = false).
func (f *File) gNodeFound(addr haddr, udata *h5gBtLkp) error {
	/*
	 * Load the symbol table node for exclusive access.
	 */
	sn, err := f.gNodeProtect(addr)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table node")
	}

	/*
	 * Binary search.
	 */
	var lt, idx uint
	rt := sn.nsyms
	cmp := 1
	for lt < rt && cmp != 0 {
		idx = (lt + rt) / 2

		s, err := udata.common.heap.offsetInto(sn.entry[idx].nameOff)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get symbol table name")
		}
		cmp = h5gStrncmp(udata.common.name, s)

		if cmp < 0 {
			rt = idx
		} else {
			lt = idx + 1
		}
	}

	if cmp != 0 {
		return errBNotFound
	}

	/* Call user's callback operator */
	if err := udata.op(&sn.entry[idx]); err != nil {
		return wrapf(err, H5E_SYM, H5E_BADITER, "iterator callback failed")
	}
	return nil
}

// gNodeIterate is H5G__node_iterate: called for each symbol table node
// during a group iterate operation. Returns errBIterStop when the operator
// asked to stop.
func (f *File) gNodeIterate(addr haddr, udata *h5gBtItIt) error {
	/* Protect the symbol table node & local heap while we iterate over entries */
	sn, err := f.gNodeProtect(addr)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to load symbol table node")
	}

	/*
	 * Iterate over the symbol table node entries.
	 */
	stop := false
	for u := uint(0); u < sn.nsyms && !stop; u++ {
		if udata.skip > 0 {
			udata.skip--
		} else {
			/* Make the callback */
			if err := udata.op(&sn.entry[u]); err != nil {
				if !errors.Is(err, errBIterStop) {
					return wrapf(err, H5E_SYM, H5E_CANTNEXT, "iteration operator failed")
				}
				stop = true
			}
		}

		/* Increment the number of entries passed through */
		/* (whether we skipped them or not) */
		if udata.finalEnt != nil {
			*udata.finalEnt++
		}
	}
	if stop {
		return errBIterStop
	}
	return nil
}

// gNodeSumup is H5G__node_sumup: called for each symbol table node during a
// group iterate operation to return total number of members in the group.
func (f *File) gNodeSumup(addr haddr, numObjs *uint64) error {
	/* Find the object node and add the number of symbol entries. */
	sn, err := f.gNodeProtect(addr)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to load symbol table node")
	}
	*numObjs += uint64(sn.nsyms)
	return nil
}

// gNodeByIdx is H5G__node_by_idx: called for each symbol table node during a
// group iterate operation to locate the entry with index udata.idx. Returns
// errBIterStop once the entry has been found (and udata.op called on it).
func (f *File) gNodeByIdx(addr haddr, udata *h5gBtItIdxCommon) error {
	/* Get a pointer to the symbol table node */
	sn, err := f.gNodeProtect(addr)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to load symbol table node")
	}

	/* Find the node, locate the object symbol table entry and retrieve the name */
	if udata.idx >= udata.numObjs && udata.idx < udata.numObjs+uint64(sn.nsyms) {
		/* Compute index of entry */
		entIdx := udata.idx - udata.numObjs

		/* Call 'by index' callback */
		if err := udata.op(&sn.entry[entIdx]); err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTGET, "'by index' callback failed")
		}

		/* Indicate that we found the entry we are interested in */
		return errBIterStop
	}
	udata.numObjs += uint64(sn.nsyms)
	return nil
}

// gNodeIterateSize is H5G__node_iterate_size: called for each symbol table
// node to gather storage info for SNODs.
func (f *File) gNodeIterateSize(stabSize *uint64) {
	*stabSize += uint64(f.h5gNodeSize())
}
