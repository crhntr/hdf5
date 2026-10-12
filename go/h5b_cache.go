package hdf5

// Translated from src/H5Bcache.c (read side only): v1 B-tree node
// deserialization.

import "bytes"

// h5bMagic is H5B_MAGIC.
var h5bMagic = []byte("TREE")

// bCacheGetInitialLoadSize is H5B__cache_get_initial_load_size: the size of
// the on-disk node image.
func bCacheGetInitialLoadSize(shared *h5bShared) int { return shared.sizeofRnode }

// bCacheDeserialize is H5B__cache_deserialize: decode one B-tree node from
// its on-disk image. Keys are decoded by the class's decodeKey callback from
// a sub-image of exactly sizeof_rkey bytes; the decoded values are stored in
// native as `any`.
func (f *File) bCacheDeserialize(shared *h5bShared, ctx any, buf []byte) (*h5bT, error) {
	p := f.newImage(buf)
	cls := shared.typ

	/* Allocate the B-tree node in memory */
	bt := &h5bT{}

	/* Magic number */
	if p.overflow(h5SizeofMagic) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), h5bMagic) {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "wrong B-tree signature")
	}

	/* Node type and level */
	if p.overflow(2) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if p.uint8() != cls.id {
		return nil, errorf(H5E_BTREE, H5E_CANTLOAD, "incorrect B-tree node type")
	}
	bt.level = p.uint8()

	/* Entries used */
	if p.overflow(2) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	bt.nchildren = p.uint16()

	/* Check if bt->nchildren is greater than two_k */
	if uint(bt.nchildren) > shared.twoK {
		return nil, errorf(H5E_BTREE, H5E_BADVALUE, "number of children is greater than maximum")
	}

	/* Sibling pointers */
	if p.overflow(int(f.sizeofAddr)) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	bt.left = p.addr()

	if p.overflow(int(f.sizeofAddr)) {
		return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	bt.right = p.addr()

	/* Allocate space for the native keys and child addresses */
	bt.native = make([]any, 0, int(bt.nchildren)+1)
	bt.child = make([]haddr, 0, bt.nchildren)

	/* Child/key pairs */
	for u := 0; u < int(bt.nchildren); u++ {
		/* Decode native key value */
		if p.overflow(shared.sizeofRkey) {
			return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		key, err := cls.decodeKey(f, ctx, p.sub(shared.sizeofRkey))
		if err != nil {
			return nil, wrapf(err, H5E_BTREE, H5E_CANTDECODE, "unable to decode key")
		}
		bt.native = append(bt.native, key)

		/* Decode address value */
		if p.overflow(int(f.sizeofAddr)) {
			return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		bt.child = append(bt.child, p.addr())
	}

	/* Final key */
	if bt.nchildren > 0 {
		/* Decode native key value */
		if p.overflow(shared.sizeofRkey) {
			return nil, errorf(H5E_BTREE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		key, err := cls.decodeKey(f, ctx, p.sub(shared.sizeofRkey))
		if err != nil {
			return nil, wrapf(err, H5E_BTREE, H5E_CANTDECODE, "unable to decode key")
		}
		bt.native = append(bt.native, key)
	}

	if err := p.done(); err != nil {
		return nil, err
	}
	return bt, nil
}
