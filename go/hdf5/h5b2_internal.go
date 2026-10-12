package hdf5

// Translated from src/H5B2internal.c (read side only): routines for
// managing v2 B-tree internal nodes (H5B2__protect_internal,
// H5B2__neighbor_internal).

import "fmt"

// h5b2ProtectInternal is H5B2__protect_internal: "protect" an internal node
// in the metadata cache (read-only). The node's record count comes from
// nodePtr; depth is the depth of the node in the tree (> 0).
func h5b2ProtectInternal(bt *h5b2, nodePtr *h5b2NodePtr, depth uint16) (*h5b2Internal, error) {
	/* Check arguments. */
	if !addrDefined(nodePtr.addr) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree internal node address is undefined")
	}
	if depth == 0 || depth > bt.hdr.depth {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "invalid v2 B-tree internal node depth %d", depth)
	}

	/* Set up user data for callback */
	nrec := nodePtr.nodeNrec

	/* Protect the internal node */
	kind := fmt.Sprintf("H5B2/int/%d/%d/%d", bt.cls.id, depth, nrec)
	v, err := bt.f.cached(kind, nodePtr.addr, func() (any, error) {
		return h5b2CacheIntLoad(bt, nodePtr.addr, nrec, depth)
	})
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree internal node, address = %d", nodePtr.addr)
	}
	return v.(*h5b2Internal), nil
}

// h5b2NeighborInternal is H5B2__neighbor_internal: locate a record
// relative to the specified information in a B-tree internal node and call
// op with it. neighborLoc is the best candidate found so far in the levels
// above (nil if none).
func h5b2NeighborInternal(bt *h5b2, depth uint16, currNodePtr *h5b2NodePtr, neighborLoc any, comp int, udata any, op func(rec any) error) error {
	/* Lock current B-tree node */
	internal, err := h5b2ProtectInternal(bt, currNodePtr, depth)
	if err != nil {
		return wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree internal node")
	}

	/* Locate node pointer for child */
	idx, cmp, err := h5b2LocateRecord(bt, internal.native, udata)
	if err != nil {
		return wrapf(err, H5E_BTREE, H5E_CANTCOMPARE, "can't compare btree2 records")
	}
	if cmp > 0 {
		idx++
	}

	/* Set the neighbor location, if appropriate */
	if comp == h5b2CompareLess {
		if idx > 0 {
			neighborLoc = internal.native[idx-1]
		}
	} else {
		if idx < int(internal.nrec) {
			neighborLoc = internal.native[idx]
		}
	}

	/* Attempt to find neighboring record */
	if depth > 1 {
		if err := h5b2NeighborInternal(bt, depth-1, &internal.nodePtrs[idx], neighborLoc, comp, udata, op); err != nil {
			return wrapf(err, H5E_BTREE, H5E_NOTFOUND, "unable to find neighbor record in B-tree internal node")
		}
	} else {
		if err := h5b2NeighborLeaf(bt, &internal.nodePtrs[idx], neighborLoc, comp, udata, op); err != nil {
			return wrapf(err, H5E_BTREE, H5E_NOTFOUND, "unable to find neighbor record in B-tree leaf node")
		}
	}
	return nil
}
