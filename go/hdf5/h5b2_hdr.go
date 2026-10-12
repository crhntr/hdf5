package hdf5

// Translated from src/H5B2hdr.c (read side only): the v2 B-tree header
// routines H5B2__hdr_init, H5B2__hdr_alloc and H5B2__hdr_protect, plus the
// package types and size macros of src/H5B2pkg.h they rely on.

// H5B2_SIZEOF_RECORDS_PER_NODE: size of storage for number of records per
// node (on disk).
const h5b2SizeofRecordsPerNode = 2

// H5B2_SIZEOF_CHKSUM: size of checksum information (on disk).
const h5b2SizeofChksum = 4

// H5B2_METADATA_PREFIX_SIZE: format overhead for all v2 B-tree metadata in
// the file.
const h5b2MetadataPrefixSize = h5SizeofMagic + /* Signature */
	1 + /* Version */
	1 + /* Tree type */
	h5b2SizeofChksum /* Metadata checksum */

// H5B2_INT_PREFIX_SIZE: size of the v2 B-tree internal node prefix.
const h5b2IntPrefixSize = h5b2MetadataPrefixSize /* General metadata fields */
/* Header specific fields */ /* <none> */

// H5B2_LEAF_PREFIX_SIZE: size of the v2 B-tree leaf node prefix.
const h5b2LeafPrefixSize = h5b2MetadataPrefixSize /* General metadata fields */
/* Header specific fields */ /* <none> */

// h5b2TreePointerSize is H5B2_TREE_POINTER_SIZE: size of a "tree pointer"
// (on disk) (essentially, the largest internal pointer allowed).
func h5b2TreePointerSize(sizeofAddr, sizeofSize uint8) int {
	return int(sizeofAddr) + h5b2SizeofRecordsPerNode + int(sizeofSize)
}

// h5b2HeaderSize is H5B2_HEADER_SIZE: size of the v2 B-tree header on disk.
func h5b2HeaderSize(sizeofAddr, sizeofSize uint8) int {
	return /* General metadata fields */ h5b2MetadataPrefixSize +

		/* Header specific fields */
		4 + /* Node size, in bytes */
		2 + /* Record size, in bytes */
		2 + /* Depth of tree */
		1 + /* Split % of full (as integer, ie. "98" means 98%) */
		1 + /* Merge % of full (as integer, ie. "98" means 98%) */
		h5b2TreePointerSize(sizeofAddr, sizeofSize) /* Node pointer to root node in tree */
}

// h5b2HeaderSizeFile is H5B2_HEADER_SIZE_FILE: size of the v2 B-tree header
// on disk (via file pointer).
func h5b2HeaderSizeFile(f *File) int { return h5b2HeaderSize(f.sizeofAddr, f.sizeofSize) }

// h5b2HeaderSizeHdr is H5B2_HEADER_SIZE_HDR: size of the v2 B-tree header
// on disk (via v2 B-tree header).
func h5b2HeaderSizeHdr(h *h5b2Hdr) int { return h5b2HeaderSize(h.sizeofAddr, h.sizeofSize) }

// h5b2IntPointerSize is H5B2_INT_POINTER_SIZE: size of an internal node
// pointer (on disk) for an internal node at depth d (d >= 1).
func h5b2IntPointerSize(h *h5b2Hdr, d uint16) uint64 {
	return uint64(h.sizeofAddr) + /* Address of child node */
		uint64(h.maxNrecSize) + /* # of records in child node */
		uint64(h.nodeInfo[d-1].cumMaxNrecSize) /* Total # of records in child & below */
}

// h5b2NumIntRec is H5B2_NUM_INT_REC: number of records that fit into an
// internal node at depth d (accounts for extra node pointer by counting it
// in with the prefix bytes). ok is false when the node is too small to hold
// even the prefix and one node pointer (the unsigned C expression would
// wrap around).
func h5b2NumIntRec(h *h5b2Hdr, d uint16) (n uint64, ok bool) {
	ptrSize := h5b2IntPointerSize(h, d)
	if uint64(h.nodeSize) < h5b2IntPrefixSize+ptrSize {
		return 0, false
	}
	return (uint64(h.nodeSize) - (h5b2IntPrefixSize + ptrSize)) / (uint64(h.rrecSize) + ptrSize), true
}

// h5b2NumLeafRec is H5B2_NUM_LEAF_REC.
func h5b2NumLeafRec(n, r uint64) uint64 { return (n - h5b2LeafPrefixSize) / r }

// h5b2NodePtr is H5B2_node_ptr_t: a "node pointer" to another B-tree node.
type h5b2NodePtr struct {
	addr     haddr  /* Address of other node */
	nodeNrec uint16 /* Number of records used in node pointed to */
	allNrec  uint64 /* Number of records in node pointed to and all it's children */
}

// h5b2NodeInfo is H5B2_node_info_t: information about a node at a given
// depth. (The free-list factories of the C struct are not needed.)
type h5b2NodeInfo struct {
	maxNrec        uint64 /* Max. number of records in node */
	splitNrec      uint64 /* Number of records to split node at */
	mergeNrec      uint64 /* Number of records to merge node at */
	cumMaxNrec     uint64 /* Cumulative max. # of records below this node's depth */
	cumMaxNrecSize uint8  /* Size to store cumulative max. # of records for this node (in bytes) */
}

// h5b2Hdr is H5B2_hdr_t: the B-tree header information (the stored fields
// and those derived from them; the cache, SWMR and client fields of the C
// struct are not needed for reading).
type h5b2Hdr struct {
	addr                       haddr  /* Address of B-tree header in the file */
	version                    uint8  /* Version of the header ("BTHD") */
	typ                        uint8  /* Type of B-tree (H5B2_subid_t, as found in file) */
	nodeSize                   uint32 /* Size of B-tree nodes, in bytes             */
	rrecSize                   uint16 /* Size of "raw" (on disk) record, in bytes   */
	depth                      uint16 /* B-tree's overall depth                     */
	splitPercent, mergePercent uint8  /* Percent full at which to split/merge the node */

	/* Node pointer to root node in B-tree (H5B2_hdr_t.root) */
	rootAddr    haddr
	rootNrec    uint16
	rootAllNrec uint64 /* total number of records in the tree */

	/* Derived information from user's information (not stored) */
	maxNrecSize uint8 /* Size to store max. # of records in any node (in bytes) */

	/* Shared internal data structures (not stored) */
	hdrSize    int            /* Size of the B-tree header on disk */
	sizeofSize uint8          /* Size of file sizes */
	sizeofAddr uint8          /* Size of file addresses */
	nodeInfo   []h5b2NodeInfo /* Table of node info structs for current depth of B-tree */
}

// rootPtr returns the root node pointer (H5B2_hdr_t.root).
func (h *h5b2Hdr) rootPtr() h5b2NodePtr {
	return h5b2NodePtr{addr: h.rootAddr, nodeNrec: h.rootNrec, allNrec: h.rootAllNrec}
}

// h5b2HdrInit is H5B2__hdr_init: initialize B-tree header info (node
// sizing for every depth) from the stored parameters, which the caller has
// already set in hdr (node size, record size, split/merge percents).
func h5b2HdrInit(hdr *h5b2Hdr, depth uint16) error {
	/*
	 * Check arguments.
	 */
	if hdr.nodeSize == 0 {
		return errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree node size is zero")
	}
	if hdr.rrecSize == 0 {
		return errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree record size is zero")
	}
	if uint64(hdr.nodeSize) < h5b2LeafPrefixSize+uint64(hdr.rrecSize) {
		return errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree node size %d too small for record size %d", hdr.nodeSize, hdr.rrecSize)
	}

	/* Assign dynamic information */
	hdr.depth = depth

	/* Allocate array of node info structs */
	hdr.nodeInfo = make([]h5b2NodeInfo, int(hdr.depth)+1)

	/* Initialize leaf node info */
	szMaxNrec := h5b2NumLeafRec(uint64(hdr.nodeSize), uint64(hdr.rrecSize))
	hdr.nodeInfo[0].maxNrec = szMaxNrec
	hdr.nodeInfo[0].splitNrec = (hdr.nodeInfo[0].maxNrec * uint64(hdr.splitPercent)) / 100
	hdr.nodeInfo[0].mergeNrec = (hdr.nodeInfo[0].maxNrec * uint64(hdr.mergePercent)) / 100
	hdr.nodeInfo[0].cumMaxNrec = hdr.nodeInfo[0].maxNrec
	hdr.nodeInfo[0].cumMaxNrecSize = 0

	/* Compute size to store # of records in each node */
	/* (uses leaf # of records because its the largest) */
	uMaxNrecSize := h5hfLimitEncSize(hdr.nodeInfo[0].maxNrec)
	if uMaxNrecSize > h5b2SizeofRecordsPerNode {
		return errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree leaf nodes hold too many records (%d)", hdr.nodeInfo[0].maxNrec)
	}
	hdr.maxNrecSize = uint8(uMaxNrecSize)

	/* Initialize internal node info */
	if depth > 0 {
		for u := uint16(1); u < depth+1; u++ {
			szMaxNrec, ok := h5b2NumIntRec(hdr, u)
			if !ok || szMaxNrec == 0 {
				return errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree node size %d too small for internal nodes at depth %d", hdr.nodeSize, u)
			}
			hdr.nodeInfo[u].maxNrec = szMaxNrec
			if hdr.nodeInfo[u].maxNrec > hdr.nodeInfo[u-1].maxNrec {
				return errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree internal node capacity exceeds leaf capacity")
			}

			hdr.nodeInfo[u].splitNrec = (hdr.nodeInfo[u].maxNrec * uint64(hdr.splitPercent)) / 100
			hdr.nodeInfo[u].mergeNrec = (hdr.nodeInfo[u].maxNrec * uint64(hdr.mergePercent)) / 100

			hdr.nodeInfo[u].cumMaxNrec = ((hdr.nodeInfo[u].maxNrec + 1) * hdr.nodeInfo[u-1].cumMaxNrec) +
				hdr.nodeInfo[u].maxNrec
			uMaxNrecSize = h5hfLimitEncSize(hdr.nodeInfo[u].cumMaxNrec)
			hdr.nodeInfo[u].cumMaxNrecSize = uint8(uMaxNrecSize)
		}
	}
	return nil
}

// h5b2HdrAlloc is H5B2__hdr_alloc: allocate B-tree header.
func h5b2HdrAlloc(f *File) *h5b2Hdr {
	/* Allocate space for the shared information */
	hdr := &h5b2Hdr{}

	/* Assign non-zero information */
	hdr.sizeofAddr = f.sizeofAddr
	hdr.sizeofSize = f.sizeofSize
	hdr.hdrSize = h5b2HeaderSizeHdr(hdr)
	hdr.rootAddr = haddrUndef
	return hdr
}

// h5b2HdrProtect is H5B2__hdr_protect (read-only): load a v2 B-tree header
// through the metadata cache.
func h5b2HdrProtect(f *File, hdrAddr haddr) (*h5b2Hdr, error) {
	/* Check arguments. */
	if !addrDefined(hdrAddr) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree header address is undefined")
	}

	/* Protect the header */
	v, err := f.cached("H5B2/hdr", hdrAddr, func() (any, error) {
		return h5b2CacheHdrLoad(f, hdrAddr)
	})
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTPROTECT, "unable to load v2 B-tree header, address = %d", hdrAddr)
	}
	return v.(*h5b2Hdr), nil
}
