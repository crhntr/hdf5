package hdf5

// Translated from src/H5B2cache.c (read side only): the
// get_initial_load_size, verify_chksum and deserialize callbacks of the v2
// B-tree header, internal node and leaf node cache clients. The
// h5b2Cache*Load functions play the role of H5C__load_entry for them: read
// the image, verify the checksum (unless disabled with
// WithChecksumVerification(false)) and deserialize it.

import "bytes"

// v2 B-tree signatures (H5B2_HDR_MAGIC, H5B2_INT_MAGIC, H5B2_LEAF_MAGIC in
// src/H5Fprivate.h).
const (
	h5b2HdrMagic  = "BTHD" /* Header */
	h5b2IntMagic  = "BTIN" /* Internal node */
	h5b2LeafMagic = "BTLF" /* Leaf node */
)

// B-tree format version #'s.
const (
	h5b2HdrVersion  = 0 /* Header */
	h5b2IntVersion  = 0 /* Internal node */
	h5b2LeafVersion = 0 /* Leaf node */
)

// h5b2Internal is H5B2_internal_t: B-tree internal node information.
type h5b2Internal struct {
	native   []any         /* Native records (nrec of them) */
	nodePtrs []h5b2NodePtr /* Node pointers (nrec+1 of them) */
	nrec     uint16        /* Number of records in node */
	depth    uint16        /* Depth of this node in the B-tree */
}

// h5b2Leaf is H5B2_leaf_t: B-tree leaf node information.
type h5b2Leaf struct {
	native []any  /* Native records (nrec of them) */
	nrec   uint16 /* Number of records in node */
}

// h5b2GetChecksums is H5F_get_checksums: the checksum stored in the last
// four bytes of buf and the one computed over the bytes before it.
func h5b2GetChecksums(buf []byte) (stored, computed uint32, err error) {
	/* Check for buffer size smaller than H5_SIZEOF_CHKSUM */
	if len(buf) < h5b2SizeofChksum {
		return 0, 0, errorf(H5E_IO, H5E_BADVALUE, "checksum buffer is smaller than expected")
	}

	/* Offset to the checksum in the buffer */
	n := len(buf) - h5b2SizeofChksum

	/* Return the stored checksum */
	/* Return the computed checksum for the buffer */
	return le32(buf[n:]), checksumMetadata(buf[:n], 0), nil
}

// h5b2CacheHdrGetInitialLoadSize is H5B2__cache_hdr_get_initial_load_size:
// compute the size of the data structure on disk.
func h5b2CacheHdrGetInitialLoadSize(f *File) int {
	/* Set the image length size */
	return h5b2HeaderSizeFile(f)
}

// h5b2CacheHdrVerifyChksum is H5B2__cache_hdr_verify_chksum: verify the
// computed checksum of the data structure is the same as the stored
// chksum.
func h5b2CacheHdrVerifyChksum(img []byte) (bool, error) {
	/* Get stored and computed checksums */
	stored, computed, err := h5b2GetChecksums(img)
	if err != nil {
		return false, wrapf(err, H5E_BTREE, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5b2CacheHdrDeserialize is H5B2__cache_hdr_deserialize: loads a B-tree
// header from the disk image.
func h5b2CacheHdrDeserialize(f *File, img []byte, addr haddr) (*h5b2Hdr, error) {
	/* Allocate new B-tree header and reset cache info */
	hdr := h5b2HdrAlloc(f)

	p := f.newImage(img)
	if p.overflow(hdr.hdrSize) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Magic number */
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5b2HdrMagic)) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "wrong B-tree header signature")
	}

	/* Version */
	if hdr.version = p.uint8(); hdr.version != h5b2HdrVersion {
		return nil, errorf(H5E_BTREE, H5E_BADRANGE, "wrong B-tree header version")
	}

	/* B-tree class */
	id := p.uint8()
	if id >= h5b2NumBtreeID {
		return nil, errorf(H5E_BTREE, H5E_BADTYPE, "incorrect B-tree type")
	}
	hdr.typ = id

	/* Node size (in bytes) */
	hdr.nodeSize = p.uint32()

	/* Raw key size (in bytes) */
	hdr.rrecSize = p.uint16()

	/* Depth of tree */
	depth := p.uint16()

	/* Split & merge %s */
	hdr.splitPercent = p.uint8()
	hdr.mergePercent = p.uint8()

	/* Root node pointer */
	hdr.rootAddr = p.addr()
	hdr.rootNrec = p.uint16()
	hdr.rootAllNrec = p.length()

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, err
	}

	/* Sanity check */
	if p.off != hdr.hdrSize {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree header size mismatch")
	}

	/* Initialize B-tree header info */
	if err := h5b2HdrInit(hdr, depth); err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTINIT, "can't initialize B-tree header info")
	}

	/* Set the B-tree header's address */
	hdr.addr = addr
	return hdr, nil
}

// h5b2CacheHdrLoad reads, verifies and deserializes the v2 B-tree header at
// addr (the H5C__load_entry sequence for H5AC_BT2_HDR).
func h5b2CacheHdrLoad(f *File, addr haddr) (*h5b2Hdr, error) {
	img, err := f.readAt(addr, h5b2CacheHdrGetInitialLoadSize(f))
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTLOAD, "unable to read v2 B-tree header")
	}
	if f.opts.verifyChecksums {
		ok, err := h5b2CacheHdrVerifyChksum(img)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_BTREE, H5E_BADCHECKSUM, "incorrect metadata checksum for v2 B-tree header")
		}
	}
	return h5b2CacheHdrDeserialize(f, img, addr)
}

// h5b2CacheIntGetInitialLoadSize is H5B2__cache_int_get_initial_load_size:
// compute the size of the data structure on disk.
func h5b2CacheIntGetInitialLoadSize(hdr *h5b2Hdr) int {
	/* Set the image length size */
	return int(hdr.nodeSize)
}

// h5b2CacheIntChkSize is the "chk_size" computation shared by
// H5B2__cache_int_verify_chksum and H5B2__cache_int_deserialize: internal
// node prefix header + records + child pointer triplets, i.e. the size of
// the node with the checksum at the end.
func h5b2CacheIntChkSize(hdr *h5b2Hdr, nrec, depth uint16) uint64 {
	return h5b2IntPrefixSize + (uint64(nrec) * uint64(hdr.rrecSize)) +
		(uint64(nrec)+1)*h5b2IntPointerSize(hdr, depth)
}

// h5b2CacheIntVerifyChksum is H5B2__cache_int_verify_chksum: verify the
// computed checksum of the data structure is the same as the stored
// chksum.
func h5b2CacheIntVerifyChksum(img []byte, hdr *h5b2Hdr, nrec, depth uint16) (bool, error) {
	/* Internal node prefix header + records + child pointer triplets: size with checksum at the end */
	chkSize := h5b2CacheIntChkSize(hdr, nrec, depth)
	if chkSize > uint64(len(img)) {
		return false, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree internal node records overrun the node")
	}

	/* Get stored and computed checksums */
	stored, computed, err := h5b2GetChecksums(img[:chkSize])
	if err != nil {
		return false, wrapf(err, H5E_BTREE, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5b2CacheIntDeserialize is H5B2__cache_int_deserialize: deserialize a
// B-tree internal node from the disk image. nrec and depth come from the
// parent's node pointer (H5B2_internal_cache_ud_t).
func h5b2CacheIntDeserialize(bt *h5b2, img []byte, nrec, depth uint16) (*h5b2Internal, error) {
	hdr := bt.hdr
	f := bt.f
	p := f.newImage(img)

	/* Allocate new internal node and reset cache info */
	internal := &h5b2Internal{}

	/* Magic number */
	if p.overflow(h5SizeofMagic + 1 + 1) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5b2IntMagic)) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "wrong B-tree internal node signature")
	}

	/* Version */
	if p.uint8() != h5b2IntVersion {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "wrong B-tree internal node version")
	}

	/* B-tree type */
	if p.uint8() != bt.cls.id {
		return nil, errorf(H5E_BTREE, H5E_BADTYPE, "incorrect B-tree type")
	}

	/* (The node must hold the records and node pointers it claims to.) */
	if h5b2CacheIntChkSize(hdr, nrec, depth) > uint64(len(img)) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree internal node records overrun the node")
	}

	/* Allocate space for the native keys in memory */
	internal.native = make([]any, nrec)

	/* Allocate space for the node pointers in memory */
	internal.nodePtrs = make([]h5b2NodePtr, int(nrec)+1)

	/* Set the number of records in the leaf & it's depth */
	internal.nrec = nrec
	internal.depth = depth

	/* Deserialize records for internal node */
	for u := 0; u < int(internal.nrec); u++ {
		/* Decode record */
		rec, err := bt.cls.decode(f, bt.ctx, p.sub(int(hdr.rrecSize)))
		if err != nil {
			return nil, wrapf(err, H5E_BTREE, H5E_CANTDECODE, "unable to decode B-tree record")
		}
		internal.native[u] = rec

		/* Move to next record */
	}

	/* Deserialize node pointers for internal node */
	for u := 0; u < int(internal.nrec)+1; u++ {
		intNodePtr := &internal.nodePtrs[u]

		/* Decode node pointer */
		intNodePtr.addr = p.addr()
		nodeNrec := p.uintVar(int(hdr.maxNrecSize))
		if nodeNrec > 0xffff {
			return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree node record count out of range")
		}
		intNodePtr.nodeNrec = uint16(nodeNrec)
		if internal.depth > 1 {
			intNodePtr.allNrec = p.uintVar(int(hdr.nodeInfo[internal.depth-1].cumMaxNrecSize))
		} else {
			intNodePtr.allNrec = uint64(intNodePtr.nodeNrec)
		}

		/* Move to next node pointer */
	}

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()

	/* Sanity check parsing */
	if err := p.done(); err != nil {
		return nil, err
	}
	return internal, nil
}

// h5b2CacheIntLoad reads, verifies and deserializes a v2 B-tree internal
// node (the H5C__load_entry sequence for H5AC_BT2_INT).
func h5b2CacheIntLoad(bt *h5b2, addr haddr, nrec, depth uint16) (*h5b2Internal, error) {
	img, err := bt.f.readAt(addr, h5b2CacheIntGetInitialLoadSize(bt.hdr))
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTLOAD, "unable to read v2 B-tree internal node")
	}
	if bt.f.opts.verifyChecksums {
		ok, err := h5b2CacheIntVerifyChksum(img, bt.hdr, nrec, depth)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_BTREE, H5E_BADCHECKSUM, "incorrect metadata checksum for v2 B-tree internal node")
		}
	}
	return h5b2CacheIntDeserialize(bt, img, nrec, depth)
}

// h5b2CacheLeafGetInitialLoadSize is H5B2__cache_leaf_get_initial_load_size:
// compute the size of the data structure on disk.
func h5b2CacheLeafGetInitialLoadSize(hdr *h5b2Hdr) int {
	/* Set the image length size */
	return int(hdr.nodeSize)
}

// h5b2CacheLeafVerifyChksum is H5B2__cache_leaf_verify_chksum: verify the
// computed checksum of the data structure is the same as the stored
// chksum.
func h5b2CacheLeafVerifyChksum(img []byte, hdr *h5b2Hdr, nrec uint16) (bool, error) {
	/* Leaf node prefix header + records: size with checksum at the end */
	chkSize := h5b2LeafPrefixSize + (uint64(nrec) * uint64(hdr.rrecSize))
	if chkSize > uint64(len(img)) {
		return false, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree leaf node records overrun the node")
	}

	/* Get stored and computed checksums */
	stored, computed, err := h5b2GetChecksums(img[:chkSize])
	if err != nil {
		return false, wrapf(err, H5E_BTREE, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5b2CacheLeafDeserialize is H5B2__cache_leaf_deserialize: deserialize a
// B-tree leaf from the disk image. nrec comes from the parent's node pointer
// (H5B2_leaf_cache_ud_t).
func h5b2CacheLeafDeserialize(bt *h5b2, img []byte, nrec uint16) (*h5b2Leaf, error) {
	hdr := bt.hdr
	f := bt.f
	p := f.newImage(img)

	/* Allocate new leaf node and reset cache info */
	leaf := &h5b2Leaf{}

	/* Magic number */
	if p.overflow(h5SizeofMagic + 1 + 1) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5b2LeafMagic)) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "wrong B-tree leaf node signature")
	}

	/* Version */
	if p.uint8() != h5b2LeafVersion {
		return nil, errorf(H5E_BTREE, H5E_BADRANGE, "wrong B-tree leaf node version")
	}

	/* B-tree type */
	if p.uint8() != bt.cls.id {
		return nil, errorf(H5E_BTREE, H5E_BADTYPE, "incorrect B-tree type")
	}

	/* (The node must hold the records it claims to.) */
	if h5b2LeafPrefixSize+uint64(nrec)*uint64(hdr.rrecSize) > uint64(len(img)) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "v2 B-tree leaf node records overrun the node")
	}

	/* Allocate space for the native keys in memory */
	leaf.native = make([]any, nrec)

	/* Set the number of records in the leaf */
	leaf.nrec = nrec

	/* Deserialize records for leaf node */
	for u := 0; u < int(leaf.nrec); u++ {
		/* Decode record */
		rec, err := bt.cls.decode(f, bt.ctx, p.sub(int(hdr.rrecSize)))
		if err != nil {
			return nil, wrapf(err, H5E_BTREE, H5E_CANTDECODE, "unable to decode B-tree record")
		}
		leaf.native[u] = rec

		/* Move to next record */
	}

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()

	/* Sanity check parsing */
	if err := p.done(); err != nil {
		return nil, err
	}
	return leaf, nil
}

// h5b2CacheLeafLoad reads, verifies and deserializes a v2 B-tree leaf node
// (the H5C__load_entry sequence for H5AC_BT2_LEAF).
func h5b2CacheLeafLoad(bt *h5b2, addr haddr, nrec uint16) (*h5b2Leaf, error) {
	img, err := bt.f.readAt(addr, h5b2CacheLeafGetInitialLoadSize(bt.hdr))
	if err != nil {
		return nil, wrapf(err, H5E_BTREE, H5E_CANTLOAD, "unable to read v2 B-tree leaf node")
	}
	if bt.f.opts.verifyChecksums {
		ok, err := h5b2CacheLeafVerifyChksum(img, bt.hdr, nrec)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_BTREE, H5E_BADCHECKSUM, "incorrect metadata checksum for v2 B-tree leaf node")
		}
	}
	return h5b2CacheLeafDeserialize(bt, img, nrec)
}
