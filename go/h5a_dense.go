package hdf5

// Translated from src/H5Adense.c (read side only): routines for operating
// on "dense" attribute storage, where the attribute messages of an object
// are stored in a fractal heap and indexed by name (and optionally creation
// order) with v2 B-trees. The record classes of the indexes are in
// h5a_btree2.go.
//
// An attribute that is itself shared through the shared object header
// message (SOHM) interface has its record flagged H5O_MSG_FLAG_SHARED: its
// heap ID is then an ID in the SOHM heap for attribute messages rather
// than in the object's dense attribute heap.

import "errors"

// h5aBt2UdIt is H5A_bt2_ud_it_t: data exchange structure to pass through
// the v2 B-tree layer for the H5B2_iterate function when iterating over
// densely stored attributes.
type h5aBt2UdIt struct {
	/* downward (internal) */
	f           *File  /* Pointer to file that fractal heap is in */
	fheap       *h5hf  /* Fractal heap handle               */
	sharedFheap *h5hf  /* Fractal heap handle for shared messages */
	count       uint64 /* # of attributes examined          */

	/* downward (from application) */
	skip   uint64                    /* Number of attributes to skip      */
	attrOp func(attr *h5oAttr) error /* Callback for each attribute       */
}

// h5aFhUdCp is H5A_fh_ud_cp_t: data exchange structure to pass through the
// fractal heap layer for the H5HF_op function when copying an attribute
// stored in densely stored attributes (or the shared message heap).
type h5aFhUdCp struct {
	/* downward (internal) */
	f      *File /* Pointer to file that fractal heap is in */
	id     []byte
	flags  uint8  /* v2 B-tree record for attribute: flags */
	corder uint32 /* v2 B-tree record for attribute: creation order */

	/* upward */
	attr *h5oAttr /* Copy of attribute                 */
}

// h5aDenseDecodeAttr is the H5O_msg_decode(f, NULL, H5O_ATTR_ID, obj_len,
// obj) step used on attribute messages stored in a fractal heap: decode
// the attribute message (resolving its shared datatype / dataspace).
func h5aDenseDecodeAttr(f *File, obj []byte) (*h5oAttr, error) {
	v, err := f.msgDecode(nil, h5oAttrID, obj)
	if err != nil {
		return nil, err
	}
	attr, ok := v.(*h5oAttr)
	if !ok || attr == nil {
		return nil, errorf(H5E_ATTR, H5E_CANTDECODE, "can't decode attribute")
	}
	return attr, nil
}

// h5aDenseSharedFheap is the block repeated in H5A__dense_open,
// H5A__dense_iterate and H5A__dense_exists that opens the shared message
// heap for attributes: nil when attributes are not shared in this file or
// no attribute has been shared yet.
func (f *File) h5aDenseSharedFheap() (*h5hf, error) {
	/* Check if attributes are shared in this file */
	attrSharable, err := f.smTypeShared(h5oAttrID)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't determine if attributes are shared")
	}

	/* Get handle for shared message heap, if attributes are shareable */
	if !attrSharable {
		return nil, nil
	}

	/* Retrieve the address of the shared message's fractal heap */
	sharedFheapAddr, err := f.smGetFheapAddr(h5oAttrID)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't get shared message heap address")
	}

	/* Check if there are any shared messages currently */
	if !addrDefined(sharedFheapAddr) {
		return nil, nil
	}

	/* Open the fractal heap for shared header messages */
	sharedFheap, err := f.hfOpen(sharedFheapAddr)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open fractal heap")
	}
	return sharedFheap, nil
}

// h5aDenseFndCb is H5A__dense_fnd_cb: callback when an attribute is
// located in an index. Takes over ownership of the (freshly decoded)
// attribute.
func h5aDenseFndCb(attr *h5oAttr, userAttr **h5oAttr) error {
	/* Take over attribute ownership */
	/* (an attribute found earlier, in an intermediate B-tree node, is
	 *  simply dropped) */
	*userAttr = attr
	return nil
}

// h5aDenseOpen is H5A__dense_open: open an attribute in dense storage
// structures for an object.
func (f *File) h5aDenseOpen(ainfo *h5oAinfo, name string) (*h5oAttr, error) {
	var ret *h5oAttr

	/* Open the fractal heap */
	fheap, err := f.hfOpen(ainfo.fheapAddr)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open fractal heap")
	}

	/* Get handle for shared message heap, if attributes are shareable */
	sharedFheap, err := f.h5aDenseSharedFheap()
	if err != nil {
		return nil, err
	}

	/* Open the name index v2 B-tree */
	bt2Name, err := f.b2Open(ainfo.nameBt2Addr, h5aBt2Name(), nil)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open v2 B-tree for name index")
	}

	/* Create the "udata" information for v2 B-tree record find */
	udata := &h5aBt2UdCommon{
		fheap:       fheap,
		sharedFheap: sharedFheap,
		name:        name,
		nameHash:    checksumLookup3([]byte(name), 0),
		flags:       0,
		corder:      0,
		decodeAttr:  h5aDenseDecodeAttr,
		foundOp:     func(attr *h5oAttr) error { return h5aDenseFndCb(attr, &ret) }, /* v2 B-tree comparison callback */
	}

	/* Find & copy the attribute in the 'name' index */
	attrExists, err := bt2Name.find(udata, nil)
	if err != nil {
		/* (H5E_NOTFOUND in the C library; a failed search is not reported
		 *  as a missing attribute here) */
		return nil, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't search for attribute in name index")
	}
	if !attrExists || ret == nil {
		return nil, errorf(H5E_ATTR, H5E_NOTFOUND, "can't locate attribute in name index")
	}
	return ret, nil
}

// h5aDenseCopyFhCb is H5A__dense_copy_fh_cb: callback for fractal heap
// operator, to make copy of attribute for calling routine.
func h5aDenseCopyFhCb(obj []byte, udata *h5aFhUdCp) error {
	/* Decode attribute information & keep a copy */
	/* (we make a copy instead of calling the user/library callback directly in
	 *  this routine because this fractal heap 'op' callback routine is called
	 *  with the direct block protected and if the callback routine invokes an
	 *  HDF5 routine, it could attempt to re-protect that direct block for the
	 *  heap, causing the HDF5 routine called to fail)
	 */
	attr, err := h5aDenseDecodeAttr(udata.f, obj)
	if err != nil {
		return wrapf(err, H5E_ATTR, H5E_CANTDECODE, "can't decode attribute")
	}

	/* Set the creation order index for the attribute */
	attr.crtIdx = udata.corder

	/* Check whether we should "reconstitute" the shared message info */
	if udata.flags&h5oMsgFlagShared != 0 {
		var id [h5oFheapIDLen]byte
		copy(id[:], udata.id)
		attr.shared = h5smReconstitute(h5oAttrID, id)
	}
	udata.attr = attr
	return nil
}

// h5aDenseIterateBt2Cb is H5A__dense_iterate_bt2_cb: v2 B-tree callback for
// dense attribute storage iterator. The record is a
// *h5aDenseBt2NameRec (name index) or a *h5aDenseBt2CorderRec (creation
// order index).
func h5aDenseIterateBt2Cb(rec any, bt2Udata *h5aBt2UdIt) error {
	var (
		id     []byte
		flags  uint8
		corder uint32
	)
	switch r := rec.(type) {
	case *h5aDenseBt2NameRec:
		id, flags, corder = r.id[:], r.flags, r.corder
	case *h5aDenseBt2CorderRec:
		id, flags, corder = r.id[:], r.flags, r.corder
	default:
		return errorf(H5E_ATTR, H5E_BADTYPE, "unexpected dense attribute record type %T", rec)
	}

	var err error

	/* Check for skipping attributes */
	if bt2Udata.skip > 0 {
		bt2Udata.skip--
	} else {
		/* Check for iterating over shared attribute */
		fheap := bt2Udata.fheap
		if flags&h5oMsgFlagShared != 0 {
			fheap = bt2Udata.sharedFheap
		}
		if fheap == nil {
			return errorf(H5E_ATTR, H5E_CANTOPERATE, "no fractal heap for shared attribute")
		}

		/* Prepare user data for callback */
		/* down */
		fhUdata := h5aFhUdCp{f: bt2Udata.f, id: id, flags: flags, corder: corder}

		/* Call fractal heap 'op' routine, to copy the attribute information */
		if err := fheap.op(id, func(obj []byte) error { return h5aDenseCopyFhCb(obj, &fhUdata) }); err != nil {
			return wrapf(err, H5E_ATTR, H5E_CANTOPERATE, "heap op callback failed")
		}

		/* Call the library's callback */
		err = bt2Udata.attrOp(fhUdata.attr)
	}

	/* Increment the number of attributes passed through */
	/* (whether we skipped them or not) */
	bt2Udata.count++

	/* Check for callback failure and pass along return value */
	if err != nil && !errors.Is(err, errBIterStop) {
		return wrapf(err, H5E_ATTR, H5E_CANTNEXT, "iteration operator failed")
	}
	return err
}

// h5aDenseIterate is H5A__dense_iterate: iterate over attributes in dense
// storage structures for an object. The operator may return errBIterStop
// to stop early (returned to the caller when the iteration goes through a
// table, swallowed when it walks a v2 B-tree).
func (f *File) h5aDenseIterate(ainfo *h5oAinfo, idxType h5aIndex, order h5aIterOrder, skip uint64, lastAttr *uint64, attrOp func(attr *h5oAttr) error) error {
	/* Check arguments */
	if !addrDefined(ainfo.fheapAddr) || !addrDefined(ainfo.nameBt2Addr) {
		return errorf(H5E_ATTR, H5E_BADVALUE, "no dense attribute storage")
	}

	/* Determine the address of the index to use */
	var bt2Addr haddr
	var cls *h5b2Class
	if idxType == h5aIndexName {
		/* Check if "native" order is OK - since names are hashed, getting them
		 *      in strictly increasing or decreasing order requires building a
		 *      table and sorting it.
		 */
		if order == h5aIterNative {
			bt2Addr = ainfo.nameBt2Addr
			cls = h5aBt2Name()
		} else {
			bt2Addr = haddrUndef
		}
	} else {
		if idxType != h5aIndexCrtOrder {
			return errorf(H5E_ARGS, H5E_BADVALUE, "unknown index type %d", idxType)
		}

		/* This address may not be defined if creation order is tracked, but
		 *      there's no index on it.  If there's no v2 B-tree that indexes
		 *      the links, a table will be built.
		 */
		bt2Addr = ainfo.corderBt2Addr
		cls = h5aBt2Corder()
	}

	/* Check on iteration order */
	if order == h5aIterNative && addrDefined(bt2Addr) {
		/* Open the fractal heap */
		fheap, err := f.hfOpen(ainfo.fheapAddr)
		if err != nil {
			return wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open fractal heap")
		}

		/* Get handle for shared message heap, if attributes are shareable */
		sharedFheap, err := f.h5aDenseSharedFheap()
		if err != nil {
			return err
		}

		/* Open the index v2 B-tree */
		bt2, err := f.b2Open(bt2Addr, cls, nil)
		if err != nil {
			return wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open v2 B-tree for index")
		}

		/* Construct the user data for v2 B-tree iterator callback */
		udata := &h5aBt2UdIt{
			f:           f,
			fheap:       fheap,
			sharedFheap: sharedFheap,
			skip:        skip,
			count:       0,
			attrOp:      attrOp,
		}

		/* Iterate over the records in the v2 B-tree's "native" order */
		/* (by hash of name) */
		err = bt2.iterate(func(rec any) error { return h5aDenseIterateBt2Cb(rec, udata) })

		/* Update the last attribute examined, if requested */
		if lastAttr != nil {
			*lastAttr = udata.count
		}
		if err != nil {
			return wrapf(err, H5E_ATTR, H5E_BADITER, "attribute iteration failed")
		}
		return nil
	}

	/* Build the table of attributes for this object */
	/* (build table using the name index, but sort according to idx_type) */
	atable, err := f.h5aDenseBuildTable(ainfo, idxType, order)
	if err != nil {
		return wrapf(err, H5E_ATTR, H5E_CANTGET, "error building table of attributes")
	}

	/* Iterate over attributes in table */
	if err := h5aAttrIterateTable(atable, skip, lastAttr, attrOp); err != nil {
		if errors.Is(err, errBIterStop) {
			return err
		}
		return wrapf(err, H5E_ATTR, H5E_CANTNEXT, "iteration operator failed")
	}
	return nil
}

// h5aDenseExists is H5A__dense_exists: check if an attribute exists in
// dense storage structures for an object.
func (f *File) h5aDenseExists(ainfo *h5oAinfo, name string) (bool, error) {
	/* Open the fractal heap */
	fheap, err := f.hfOpen(ainfo.fheapAddr)
	if err != nil {
		return false, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open fractal heap")
	}

	/* Get handle for shared message heap, if attributes are shareable */
	sharedFheap, err := f.h5aDenseSharedFheap()
	if err != nil {
		return false, err
	}

	/* Open the name index v2 B-tree */
	bt2Name, err := f.b2Open(ainfo.nameBt2Addr, h5aBt2Name(), nil)
	if err != nil {
		return false, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open v2 B-tree for name index")
	}

	/* Create the "udata" information for v2 B-tree record 'find' */
	udata := &h5aBt2UdCommon{
		fheap:       fheap,
		sharedFheap: sharedFheap,
		name:        name,
		nameHash:    checksumLookup3([]byte(name), 0),
		flags:       0,
		corder:      0,
		decodeAttr:  h5aDenseDecodeAttr,
		foundOp:     nil, /* v2 B-tree comparison callback */
	}

	/* Find the attribute in the 'name' index */
	attrExists, err := bt2Name.find(udata, nil)
	if err != nil {
		return false, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't search for attribute in name index")
	}
	return attrExists, nil
}
