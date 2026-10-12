package hdf5

// Translated from src/H5B2leaf.c (read side only): routines for managing
// v2 B-tree leaf nodes (H5B2__protect_leaf, H5B2__neighbor_leaf).

import "fmt"

// h5b2ProtectLeaf is H5B2__protect_leaf: "protect" a leaf node in the
// metadata cache (read-only). The node's record count comes from nodePtr.
func h5b2ProtectLeaf(bt *h5b2, nodePtr *h5b2NodePtr) (*h5b2Leaf, error) {
	/* Check arguments. */
	if !addrDefined(nodePtr.addr) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree leaf node address is undefined")
	}

	/* Set up user data for callback */
	nrec := nodePtr.nodeNrec

	/* Protect the leaf node */
	kind := fmt.Sprintf("H5B2/leaf/%d/%d", bt.cls.id, nrec)
	v, err := bt.f.cached(kind, nodePtr.addr, func() (any, error) {
		return h5b2CacheLeafLoad(bt, nodePtr.addr, nrec)
	})
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree leaf node, address = %d", nodePtr.addr)
	}
	return v.(*h5b2Leaf), nil
}

// h5b2NeighborLeaf is H5B2__neighbor_leaf: locate a record relative to the
// specified information in a B-tree leaf node and call op with it.
// neighborLoc is the best candidate found in the levels above (nil if
// none).
func h5b2NeighborLeaf(bt *h5b2, currNodePtr *h5b2NodePtr, neighborLoc any, comp int, udata any, op func(rec any) error) error {
	/* Lock current B-tree node */
	leaf, err := h5b2ProtectLeaf(bt, currNodePtr)
	if err != nil {
		return wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree leaf node")
	}

	/* Locate node pointer for child */
	idx, cmp, err := h5b2LocateRecord(bt, leaf.native, udata)
	if err != nil {
		return wrapf(err, H5E_BTREE, H5E_CANTCOMPARE, "can't compare btree2 records")
	}
	if cmp > 0 {
		idx++
	} else if cmp == 0 && comp == h5b2CompareGreater {
		idx++
	}

	/* Set the neighbor location, if appropriate */
	if comp == h5b2CompareLess {
		if idx > 0 {
			neighborLoc = leaf.native[idx-1]
		}
	} else {
		if idx < int(leaf.nrec) {
			neighborLoc = leaf.native[idx]
		}
	}

	/* Make callback if neighbor record has been found */
	if neighborLoc == nil {
		return errorf(H5E_BTREE, H5E_NOTFOUND, "unable to find neighbor record in B-tree")
	}

	/* Make callback for current record */
	if err := op(neighborLoc); err != nil {
		return wrapf(err, H5E_BTREE, H5E_NOTFOUND, "'found' callback failed for B-tree neighbor operation")
	}
	return nil
}
