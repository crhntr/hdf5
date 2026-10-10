package hdf5

// Translated from src/H5Gcache.c (read side only): symbol table node
// ("SNOD") deserialization.

import "bytes"

// h5gNodeMagic is H5G_NODE_MAGIC.
var h5gNodeMagic = []byte("SNOD")

// h5gNodeVers is H5G_NODE_VERS: symbol table node version number.
const h5gNodeVers = 1

// h5gNodeSize is H5G_NODE_SIZE(f): the size of a symbol table node on disk.
func (f *File) h5gNodeSize() int {
	return /* General metadata fields */ h5SizeofMagic + /*magic number */
		1 + /* Version */
		1 + /* Reserved */
		2 + /* Number of symbols */
		/* Entries */
		int(2*f.symLeafK)*h5gSizeofEntry(int(f.sizeofAddr), int(f.sizeofSize))
}

// h5gNode is H5G_node_t: a symbol table node.
type h5gNode struct {
	nodeSize int        /* Size of node on disk              */
	nsyms    uint       /* Number of symbols                 */
	entry    []h5gEntry /* Array of symbol table entries     */
}

// gCacheNodeGetInitialLoadSize is H5G__cache_node_get_initial_load_size.
func (f *File) gCacheNodeGetInitialLoadSize() int { return f.h5gNodeSize() }

// gCacheNodeDeserialize is H5G__cache_node_deserialize: given a buffer
// containing the on disk image of a symbol table node, decode it.
func (f *File) gCacheNodeDeserialize(buf []byte) (*h5gNode, error) {
	p := f.newImage(buf)

	/* Allocate symbol table data structures */
	sym := &h5gNode{nodeSize: f.h5gNodeSize()}

	/* Magic */
	if p.overflow(h5SizeofMagic) {
		return nil, errorf(H5E_SYM, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), h5gNodeMagic) {
		return nil, errorf(H5E_SYM, H5E_BADVALUE, "bad symbol table node signature")
	}

	/* Version */
	if p.overflow(1) {
		return nil, errorf(H5E_SYM, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if h5gNodeVers != p.uint8() {
		return nil, errorf(H5E_SYM, H5E_VERSION, "bad symbol table node version")
	}

	/* Reserved */
	if p.overflow(1) {
		return nil, errorf(H5E_SYM, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	p.skip(1)

	/* Number of symbols */
	if p.overflow(2) {
		return nil, errorf(H5E_SYM, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	sym.nsyms = uint(p.uint16())

	/* The node holds at most 2*sym_leaf_k entries (the size of the
	 * sym->entry array in the C code) */
	if sym.nsyms > 2*f.symLeafK {
		return nil, errorf(H5E_SYM, H5E_BADVALUE, "number of symbols is greater than maximum")
	}

	/* Entries */
	ents, err := h5gEntDecodeVec(p, int(sym.nsyms))
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTLOAD, "unable to decode symbol table entries")
	}
	sym.entry = ents
	return sym, nil
}

// gNodeProtect is H5AC_protect(f, H5AC_SNODE, addr, f, H5AC__READ_ONLY_FLAG):
// load (through the cache) the symbol table node at addr.
func (f *File) gNodeProtect(addr haddr) (*h5gNode, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table node: undefined address")
	}
	v, err := f.cached("H5G_SNODE", addr, func() (any, error) {
		buf, err := f.readAt(addr, f.gCacheNodeGetInitialLoadSize())
		if err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTLOAD, "unable to read symbol table node")
		}
		return f.gCacheNodeDeserialize(buf)
	})
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTPROTECT, "unable to protect symbol table node")
	}
	return v.(*h5gNode), nil
}
