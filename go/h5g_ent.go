package hdf5

// Translated from src/H5Gent.c: symbol table entries.

import "github.com/crhntr/hdf5/go/h5"

// H5G_cache_type_t
type h5gCacheType int32

const (
	h5gCachedError   h5gCacheType = -1 /* Bad cache type (should never happen) */
	h5gNothingCached h5gCacheType = 0  /* Nothing is cached */
	h5gCachedStab    h5gCacheType = 1  /* Symbol table, `stab' */
	h5gCachedSlink   h5gCacheType = 2  /* Symbolic link */
	h5gNCached       h5gCacheType = 3  /* Number of cached types */
)

// h5gEntry is H5G_entry_t.
type h5gEntry struct {
	typ     h5gCacheType /* type of information cached */
	nameOff uint64       /* offset of name within name heap */
	header  haddr        /* file address of object header */
	// cache.stab
	btreeAddr haddr /* file address of symbol table B-tree */
	heapAddr  haddr /* file address of stab name heap */
	// cache.slink
	lvalOffset uint32 /* link value offset */
}

// h5gSizeofScratch is H5G_SIZEOF_SCRATCH.
const h5gSizeofScratch = 16

// h5gSizeofEntry is H5G_SIZEOF_ENTRY(sizeof_size, sizeof_addr).
func h5gSizeofEntry(sizeofAddr, sizeofSize int) int {
	return sizeofSize + /*offset of name into heap */
		sizeofAddr + /*address of object header */
		4 + /*entry type */
		4 + /*reserved */
		h5gSizeofScratch /*scratch pad space */
}

// h5gEntDecode is H5G_ent_decode.
func h5gEntDecode(p *image) (*h5gEntry, error) {
	ent := &h5gEntry{}
	start := p.off

	if p.overflow(int(p.sizeofSize)) {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "image pointer is out of bounds")
	}
	/* decode header */
	ent.nameOff = p.length()

	if p.overflow(int(p.sizeofAddr) + 4) {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "image pointer is out of bounds")
	}
	ent.header = p.addr()
	tmp := p.uint32()
	p.skip(4) /*reserved*/

	ent.typ = h5gCacheType(tmp)

	/* decode scratch-pad */
	switch ent.typ {
	case h5gNothingCached:
	case h5gCachedStab:
		if p.overflow(int(p.sizeofAddr) * 2) {
			return nil, errorf(H5E_FILE, H5E_OVERFLOW, "image pointer is out of bounds")
		}
		ent.btreeAddr = p.addr()
		ent.heapAddr = p.addr()
	case h5gCachedSlink:
		if p.overflow(4) {
			return nil, errorf(H5E_FILE, H5E_OVERFLOW, "image pointer is out of bounds")
		}
		ent.lvalOffset = p.uint32()
	default:
		return nil, errorf(H5E_SYM, H5E_BADVALUE, "unknown symbol table entry cache type")
	}

	end := start + h5gSizeofEntry(int(p.sizeofAddr), int(p.sizeofSize))
	if end > len(p.buf) {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "image pointer is out of bounds")
	}
	p.off = end
	return ent, nil
}

// h5gEntDecodeVec is H5G__ent_decode_vec: decode n consecutive entries.
func h5gEntDecodeVec(p *image, n int) ([]h5gEntry, error) {
	ents := make([]h5gEntry, 0, n)
	for i := 0; i < n; i++ {
		e, err := h5gEntDecode(p)
		if err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTDECODE, "can't decode")
		}
		ents = append(ents, *e)
	}
	return ents, nil
}

// gEntToLink is H5G__ent_to_link: convert a symbol table entry (plus the
// local heap holding its name) into a link message.
func gEntToLink(ent *h5gEntry, heap *h5hl) (*h5oLink, error) {
	lnk := &h5oLink{cset: h5.CsetASCII}

	/* Set (default) common info for link */
	name, err := heap.stringAt(ent.nameOff)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get symbol table link name")
	}
	if name == "" {
		return nil, errorf(H5E_SYM, H5E_BADVALUE, "invalid link name")
	}
	lnk.name = name

	/* Object is a symbolic or hard link */
	if ent.typ == h5gCachedSlink {
		s, err := heap.stringAt(uint64(ent.lvalOffset))
		if err != nil {
			return nil, wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get symbolic link name")
		}
		/* Set link value */
		lnk.softName = s
		/* Set link type */
		lnk.typ = h5.LinkSoft
	} else {
		/* Set address of object */
		lnk.addr = ent.header
		/* Set link type */
		lnk.typ = h5.LinkHard
	}
	return lnk, nil
}
