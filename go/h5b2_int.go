package hdf5

// Translated from src/H5B2int.c (read side only): internal routines for
// v2 B-trees (H5B2__locate_record, H5B2__iterate_node).

import "errors"

// h5b2LocateRecord is H5B2__locate_record: performs a binary search to
// locate a record in a sorted array of records.
//
// Sets idx to location of record greater than or equal to record to
// locate.
//
// Returns the comparison value for insertion location: negative for record
// to locate being less than value in idx, zero for record to locate equal
// to value in idx, positive for record to locate being greater than value
// in idx (which should only happen when record to locate is greater than
// all records to search).
func h5b2LocateRecord(bt *h5b2, native []any, udata any) (idx int, cmp int, err error) {
	if bt.cls.compare == nil {
		return 0, 0, errorf(H5E_BTREE, H5E_CANTCOMPARE, "v2 B-tree class has no compare callback")
	}

	lo, hi := 0, len(native) /* Low & high index values */
	myIdx := 0               /* Final index value */

	cmp = -1

	for lo < hi && cmp != 0 {
		myIdx = (lo + hi) / 2
		if cmp, err = bt.cls.compare(bt.f, bt.ctx, udata, native[myIdx]); err != nil {
			return 0, 0, wrapf(err, H5E_BTREE, H5E_CANTCOMPARE, "can't compare btree2 records")
		}
		if cmp < 0 {
			hi = myIdx
		} else {
			lo = myIdx + 1
		}
	}

	return myIdx, cmp, nil
}

// h5b2IterateNode is H5B2__iterate_node: iterate over all the records from
// a B-tree node, in "in-order" order, making a callback for each record.
//
// If the callback returns errBIterStop, the iteration breaks out without
// finishing all the records and stop=true is returned. visited guards
// against corrupt trees in which a node is reachable more than once.
func h5b2IterateNode(bt *h5b2, depth uint16, currNode *h5b2NodePtr, visited map[haddr]bool, op func(rec any) error) (stop bool, err error) {
	if visited[currNode.addr] {
		return false, errorf(H5E_BTREE, H5E_BADVALUE, "cyclic v2 B-tree detected")
	}
	visited[currNode.addr] = true

	/* Protect current node & set up variables */
	var native []any           /* Pointers to node's native records */
	var nodePtrs []h5b2NodePtr /* Pointers to node's node pointers */
	if depth > 0 {
		/* Lock the current B-tree node */
		internal, err := h5b2ProtectInternal(bt, currNode, depth)
		if err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree internal node")
		}

		/* Set up information about current node */
		native = internal.native
		nodePtrs = internal.nodePtrs
	} else {
		/* Lock the current B-tree node */
		leaf, err := h5b2ProtectLeaf(bt, currNode)
		if err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree leaf node")
		}

		/* Set up information about current node */
		native = leaf.native
	}

	/* (The C routine copies the native records and node pointers before
	 *  releasing the node; cached nodes are immutable here.) */

	/* Iterate through records, in order */
	var u int
	for u = 0; u < int(currNode.nodeNrec); u++ {
		/* Descend into child node, if current node is an internal node */
		if depth > 0 {
			if stop, err = h5b2IterateNode(bt, depth-1, &nodePtrs[u], visited, op); err != nil {
				return false, wrapf(err, H5E_BTREE, H5E_BADITER, "node iteration failed")
			}
			if stop {
				return true, nil
			}
		}

		/* Make callback for current record */
		if err = op(native[u]); err != nil {
			if errors.Is(err, errBIterStop) {
				return true, nil
			}
			return false, wrapf(err, H5E_BTREE, H5E_BADITER, "iterator function failed")
		}
	}

	/* Descend into last child node, if current node is an internal node */
	if depth > 0 {
		if stop, err = h5b2IterateNode(bt, depth-1, &nodePtrs[u], visited, op); err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_BADITER, "node iteration failed")
		}
	}
	return stop, nil
}
