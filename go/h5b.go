package hdf5

// Translated from src/H5B.c (read side only): version 1 B-trees.
//
// A v1 B-tree ("TREE" nodes) indexes either the symbol table nodes of an
// old-style group (class h5bSnode, see h5g_node.go) or the chunks of a
// chunked dataset (class H5B_BTREE in src/H5Dbtree.c). The code here is
// generic over the node type, as in the C library:
//
//   - h5bClass describes a tree type. Only the raw key size (sizeofRkey) and
//     the key decoder (decodeKey) are needed for reading; the C class's cmp3
//     and found callbacks are passed to bFind as closures that capture the
//     search key (the C "udata"), and the H5B_operator_t callback is passed
//     to bIterate.
//   - Keys are decoded into Go values (`any`) by decodeKey, which receives an
//     image of exactly sizeofRkey bytes. The generic code never interprets
//     them; it only hands them back to the closures. A node with N children
//     has N+1 keys: child[i] covers the keys between native[i] and
//     native[i+1].
//   - ctx is the per-tree client context (the C H5B_shared_t "udata" /
//     the udata given to get_shared, e.g. the dataset's dimensionality for
//     chunk B-trees). It is passed back to sizeofRkey and decodeKey.
//
// Decoded nodes are cached by (class id, sizeof_rkey, address); decodeKey
// must therefore produce the same value for the same bytes whenever
// sizeofRkey(f, ctx) is the same.
//
// Writing a new class (e.g. the chunk B-tree): define
//
//	var h5bBtree = &h5bClass{
//		id:         h5bChunkID,
//		sizeofRkey: func(f *File, ctx any) int { ... 4 + 4 + 8*ndims ... },
//		decodeKey:  func(f *File, ctx any, p *image) (any, error) { ... return &chunkKey{...}, p.done() },
//	}
//
// and call f.bIterate(h5bBtree, ctx, addr, op) / f.bFind(h5bBtree, ctx, addr,
// cmp3, found). The K value used for node sizing is f.btreeK[cls.id].

import (
	"errors"
	"fmt"
)

// h5bSizeofHdr is H5B_SIZEOF_HDR(F).
func h5bSizeofHdr(sizeofAddr int) int {
	return h5SizeofMagic + /*magic number			  */
		4 + /*type, level, num entries		  */
		2*sizeofAddr /*left and right sibling addresses	  */
}

// h5bUnknownNodelevel is H5B_UNKNOWN_NODELEVEL (from the develop branch of
// the C library, where the expected level of a node is checked to detect
// corrupted, cyclic trees).
const h5bUnknownNodelevel = -1

// errBIterStop may be returned by a bIterate operator (and by the operators
// of the iteration functions built on it, e.g. gStabIterate) to stop the
// iteration early without error. It is the Go spelling of an H5B_operator_t
// returning H5_ITER_STOP (a positive value).
var errBIterStop = errors.New("hdf5: stop iteration")

// errBNotFound is returned by a bFind leaf callback (the C class's "found"
// method) when the leaf child does not contain the wanted key, i.e. when the
// C callback would set *found = false.
var errBNotFound = errors.New("hdf5: not found in B-tree leaf")

// h5bClass is H5B_class_t: the per-node-type callbacks of a v1 B-tree.
// Keys are decoded into Go values by decodeKey; the generic code never
// interprets them.
type h5bClass struct {
	id         uint8                                         /* h5bSnodeID or h5bChunkID */
	sizeofRkey func(f *File, ctx any) int                    /* H5B_class_t.sizeof_rkey: encoded key size */
	decodeKey  func(f *File, ctx any, p *image) (any, error) /* H5B_class_t.decode */
}

// h5bShared is H5B_shared_t: the information shared by all nodes of a tree.
type h5bShared struct {
	typ         *h5bClass /* Type of tree */
	twoK        uint      /* 2*"K" value for tree's nodes */
	sizeofRkey  int       /* Size of raw (disk) key */
	sizeofRnode int       /* Size of raw (disk) node */
	sizeofAddr  int       /* Size of file address (in bytes) */
	sizeofLen   int       /* Size of file lengths (in bytes) */
}

// h5bT is H5B_t: one decoded B-tree node.
type h5bT struct {
	level       uint8
	nchildren   uint16
	left, right haddr
	native      []any   /* nchildren+1 decoded keys */
	child       []haddr /* nchildren child addresses */
}

// h5bInfo is H5B_info_t: information about a B-tree.
type h5bInfo struct {
	size     uint64 /* Size of B-tree nodes */
	numNodes uint64 /* Number of B-tree nodes */
}

// bFind is H5B_find: descend from addr using cmp3(leftKey, rightKey), which
// returns <0 / 0 / >0 for "the wanted key is before / within / after the
// range of the child between leftKey and rightKey" (the class decides which
// bound is inclusive: (left, right] for symbol tables, [left, right) for
// chunks), then calls found(leftKey, rightKey, childAddr) on the leaf child.
// Returns found=false (and no error) when absent. The C cmp3 callback
// receives the search key as udata; here cmp3 is a closure capturing it.
//
// Note: this function does not follow the left/right sibling pointers since
// it assumes that all nodes can be reached from the parent node.
//
// In the C library the leaf callback (type->found) reports through *found
// whether the item exists in the leaf child. Here found returns
// errBNotFound for "the child that would hold the key does not contain it",
// which makes bFind return (false, nil); any other error is returned
// wrapped, and nil means found.
func (f *File) bFind(cls *h5bClass, ctx any, addr haddr, cmp3 func(leftKey, rightKey any) int, found func(leftKey, rightKey any, childAddr haddr) error) (bool, error) {
	return f.bFindHelper(cls, ctx, addr, h5bUnknownNodelevel, cmp3, found)
}

// bFindHelper is H5B_find_helper (develop branch; the 1.14.6 H5B_find body
// with the expected node level checked to detect corrupted trees).
func (f *File) bFindHelper(cls *h5bClass, ctx any, addr haddr, expLevel int, cmp3 func(leftKey, rightKey any) int, found func(leftKey, rightKey any, childAddr haddr) error) (bool, error) {
	/*
	 * Perform a binary search to locate the child which contains
	 * the thing for which we're searching.
	 */
	bt, err := f.bProtectLevel(cls, ctx, addr, expLevel)
	if err != nil {
		return false, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to load B-tree node")
	}

	var idx, lt int
	rt := int(bt.nchildren)
	cmp := 1
	for lt < rt && cmp != 0 {
		idx = (lt + rt) / 2
		/* compare */
		if cmp = cmp3(bt.native[idx], bt.native[idx+1]); cmp < 0 {
			rt = idx
		} else {
			lt = idx + 1
		}
	}

	/* Check if not found */
	if cmp != 0 {
		return false, nil
	}

	/*
	 * Follow the link to the subtree or to the data node.
	 */
	if bt.level > 0 {
		/* Sanity check to catch the case where the current node points to
		 * itself. */
		if bt.child[idx] == addr {
			return false, errorf(H5E_BTREE, H5E_BADVALUE, "cyclic B-tree detected")
		}
		ok, err := f.bFindHelper(cls, ctx, bt.child[idx], int(bt.level)-1, cmp3, found)
		if err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_NOTFOUND, "can't lookup key in subtree")
		}
		return ok, nil
	}
	if err := found(bt.native[idx], bt.native[idx+1], bt.child[idx]); err != nil {
		if errors.Is(err, errBNotFound) {
			return false, nil
		}
		return false, wrapf(err, H5E_BTREE, H5E_NOTFOUND, "can't lookup key in leaf node")
	}
	return true, nil
}

// bIterateHelper is H5B__iterate_helper: calls op for each leaf child of
// the (sub)tree at addr. visited guards against corrupt trees in which a node
// is reachable more than once. It returns stop=true when op returned
// errBIterStop.
func (f *File) bIterateHelper(cls *h5bClass, ctx any, addr haddr, expLevel int, visited map[haddr]bool, op func(leftKey, rightKey any, childAddr haddr) error) (stop bool, err error) {
	if visited[addr] {
		return false, errorf(H5E_BTREE, H5E_BADVALUE, "cyclic B-tree detected")
	}
	visited[addr] = true

	/* Protect the initial/current node */
	bt, err := f.bProtectLevel(cls, ctx, addr, expLevel)
	if err != nil {
		return false, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to load B-tree node")
	}

	/* Iterate over node's children */
	for u := 0; u < int(bt.nchildren); u++ {
		if bt.level > 0 {
			stop, err = f.bIterateHelper(cls, ctx, bt.child[u], int(bt.level)-1, visited, op)
		} else {
			if err = op(bt.native[u], bt.native[u+1], bt.child[u]); errors.Is(err, errBIterStop) {
				stop, err = true, nil
			}
		}
		if err != nil {
			return false, wrapf(err, H5E_BTREE, H5E_BADITER, "B-tree iteration failed")
		}
		if stop {
			return true, nil
		}
	}
	return false, nil
}

// bIterate is H5B_iterate: visit every leaf-level child of the B-tree rooted
// at addr, in order, calling op(leftKey, rightKey, childAddr). op may return
// errBIterStop to end the iteration early (bIterate then returns nil).
func (f *File) bIterate(cls *h5bClass, ctx any, addr haddr, op func(leftKey, rightKey any, childAddr haddr) error) error {
	if !addrDefined(addr) {
		return errorf(H5E_BTREE, H5E_BADVALUE, "address is undefined")
	}

	/* Iterate over the B-tree records */
	if _, err := f.bIterateHelper(cls, ctx, addr, h5bUnknownNodelevel, map[haddr]bool{}, op); err != nil {
		return wrapf(err, H5E_BTREE, H5E_BADITER, "B-tree iteration failed")
	}
	return nil
}

// bSharedNew is H5B_shared_new (with the class's sizeof_rkey, as computed by
// the get_shared callbacks H5G__node_init / H5D__btree_shared_create):
// compute the sizes shared by all nodes of a tree.
func (f *File) bSharedNew(cls *h5bClass, ctx any) (*h5bShared, error) {
	if cls == nil || int(cls.id) >= len(f.btreeK) {
		return nil, errorf(H5E_BTREE, H5E_BADTYPE, "invalid B-tree class")
	}

	/* Set up the "global" information for this file's groups */
	shared := &h5bShared{
		typ:        cls,
		twoK:       2 * f.btreeK[cls.id],
		sizeofAddr: int(f.sizeofAddr),
		sizeofLen:  int(f.sizeofSize),
		sizeofRkey: cls.sizeofRkey(f, ctx),
	}
	if shared.sizeofRkey <= 0 {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "invalid B-tree raw key size")
	}
	if shared.twoK == 0 {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "invalid B-tree 'K' value")
	}
	shared.sizeofRnode = h5bSizeofHdr(shared.sizeofAddr) + /*node header	*/
		int(shared.twoK)*shared.sizeofAddr + /*child pointers */
		int(shared.twoK+1)*shared.sizeofRkey /*keys		*/
	return shared, nil
}

// bProtect is H5B_protect / H5B__cache_deserialize: load one node.
func (f *File) bProtect(cls *h5bClass, ctx any, addr haddr) (*h5bT, error) {
	return f.bProtectLevel(cls, ctx, addr, h5bUnknownNodelevel)
}

// bProtectLevel is H5AC_protect(f, H5AC_BT, addr, &cache_udata, ...) with
// cache_udata.exp_level set (develop branch): load one node through the
// cache and check its level when the expected level is known.
func (f *File) bProtectLevel(cls *h5bClass, ctx any, addr haddr, expLevel int) (*h5bT, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "address is undefined")
	}

	/* Get shared info for B-tree */
	shared, err := f.bSharedNew(cls, ctx)
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTGET, "can't retrieve B-tree's shared ref. count object")
	}

	kind := fmt.Sprintf("H5B/%d/%d", cls.id, shared.sizeofRkey)
	v, err := f.cached(kind, addr, func() (any, error) {
		buf, err := f.readAt(addr, bCacheGetInitialLoadSize(shared))
		if err != nil {
			return nil, wrapf(err, H5E_BTREE, H5E_CANTLOAD, "unable to read B-tree node")
		}
		return f.bCacheDeserialize(shared, ctx, buf)
	})
	if err != nil {
		return nil, err
	}
	bt := v.(*h5bT)

	/* Check in case of level is corrupted, if expected level is known */
	if expLevel != h5bUnknownNodelevel && int(bt.level) != expLevel {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "level is not as expected, possibly corrupted")
	}
	return bt, nil
}

// bGetInfoHelper is H5B__get_info_helper: walk the B-tree nodes level by
// level (following right-sibling pointers), accumulating node count and
// size.
func (f *File) bGetInfoHelper(cls *h5bClass, ctx any, addr haddr, info *h5bInfo) error {
	/* Get shared info for B-tree */
	shared, err := f.bSharedNew(cls, ctx)
	if err != nil {
		return wrapf(err, H5E_BTREE, H5E_CANTGET, "can't retrieve B-tree's shared ref. count object")
	}

	/* Get the raw node size for iteration */
	sizeofRnode := uint64(shared.sizeofRnode)

	/* Bound the walk: no valid tree has more nodes than fit in the file. */
	maxNodes := f.eof()/sizeofRnode + 1

	for depth := 0; ; depth++ {
		if depth > 256 {
			return errorf(H5E_BTREE, H5E_BADVALUE, "B-tree too deep, possibly corrupted")
		}

		/* Protect the initial/current node */
		bt, err := f.bProtect(cls, ctx, addr)
		if err != nil {
			return wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to load B-tree node")
		}

		/* Cache information from this node */
		var leftChild haddr = haddrUndef
		if bt.nchildren > 0 {
			leftChild = bt.child[0]
		}
		nextAddr := bt.right
		level := bt.level

		/* Update B-tree info */
		info.size += sizeofRnode
		info.numNodes++

		/*
		 * Follow the right-sibling pointer from node to node until we've
		 *      processed all nodes.
		 */
		for addrDefined(nextAddr) {
			if info.numNodes > maxNodes {
				return errorf(H5E_BTREE, H5E_BADVALUE, "too many B-tree nodes, possibly corrupted")
			}

			/* Protect the next node to the right */
			addr = nextAddr
			bt, err := f.bProtect(cls, ctx, addr)
			if err != nil {
				return wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "B-tree node")
			}

			/* Cache information from this node */
			nextAddr = bt.right

			/* Update B-tree info */
			info.size += sizeofRnode
			info.numNodes++
		}

		/* Check for another "row" of B-tree nodes to iterate over */
		if level == 0 {
			return nil
		}
		/* Keep following the left-most child until we reach a leaf node. */
		if !addrDefined(leftChild) {
			return errorf(H5E_BTREE, H5E_BADITER, "unable to list B-tree node")
		}
		addr = leftChild
	}
}

// bGetInfo is H5B_get_info: return the amount of storage used for the
// B-tree, then (if op is not nil) iterate over the leaf children as
// bIterate does.
func (f *File) bGetInfo(cls *h5bClass, ctx any, addr haddr, op func(leftKey, rightKey any, childAddr haddr) error) (h5bInfo, error) {
	var info h5bInfo

	/* Iterate over the B-tree nodes */
	if err := f.bGetInfoHelper(cls, ctx, addr, &info); err != nil {
		return info, wrapf(err, H5E_BTREE, H5E_BADITER, "B-tree iteration failed")
	}

	/* Iterate over the B-tree records, making any "leaf" callbacks */
	/* (Only if operator defined) */
	if op != nil {
		if err := f.bIterate(cls, ctx, addr, op); err != nil {
			return info, err
		}
	}
	return info, nil
}

// bValid is H5B_valid: attempt to load a B-tree node.
func (f *File) bValid(cls *h5bClass, ctx any, addr haddr) error {
	if !addrDefined(addr) {
		return errorf(H5E_BTREE, H5E_BADVALUE, "address is undefined")
	}

	/*
	 * Load the tree node.
	 */
	if _, err := f.bProtect(cls, ctx, addr); err != nil {
		return wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to protect B-tree node")
	}
	return nil
}
