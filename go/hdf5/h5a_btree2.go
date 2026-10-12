package hdf5

// Translated from src/H5Abtree2.c (read side only): the v2 B-tree record
// classes for indexing "dense" attribute storage by name (H5A_BT2_NAME,
// B-tree type 8) and by creation order (H5A_BT2_CORDER, B-tree type 9). The
// record types and the B-tree user data come from src/H5Apkg.h.
//
// Neither class has a callback context: open the B-trees with a nil ctx,
//
//	bt2, err := f.b2Open(ainfo.nameBt2Addr, h5aBt2Name(), nil)
//
// and search them with a *h5aBt2UdCommon as udata. The name comparison
// needs the attribute message from a fractal heap (the object's dense
// attribute heap, or the shared message heap for records flagged
// H5O_MSG_FLAG_SHARED): the caller sets fheap / sharedFheap and decodeAttr
// (the H5O_msg_decode(H5O_ATTR_ID) step, i.e. the attribute message
// decoder), and optionally foundOp (the H5A_bt2_found_t callback that
// receives the decoded attribute when the names match).

import "strings"

// h5aDenseBt2NameRec is H5A_dense_bt2_name_rec_t: native 'name' field index
// records in the v2 B-tree.
type h5aDenseBt2NameRec struct {
	id     [h5oFheapIDLen]byte /* Heap ID for attribute */
	flags  uint8               /* Object header message flags for attribute */
	corder uint32              /* 'creation order' field value */
	hash   uint32              /* Hash of 'name' field value */
}

// h5aDenseBt2CorderRec is H5A_dense_bt2_corder_rec_t: native 'creation
// order' field index records in the v2 B-tree.
type h5aDenseBt2CorderRec struct {
	id     [h5oFheapIDLen]byte /* Heap ID for attribute */
	flags  uint8               /* Object header message flags for attribute */
	corder uint32              /* 'creation order' field value */
}

// h5aBt2UdCommon is H5A_bt2_ud_common_t: common data exchange structure
// for dense attribute storage. This structure is passed through the v2
// B-tree layer (as udata of find) to the methods for the objects to which
// the v2 B-tree points.
type h5aBt2UdCommon struct {
	/* downward */
	fheap       *h5hf  /* Fractal heap handle               */
	sharedFheap *h5hf  /* Fractal heap handle for shared messages */
	name        string /* Name of attribute to compare      */
	nameHash    uint32 /* Hash of name of attribute to compare */
	flags       uint8  /* Flags for attribute storage location */
	corder      uint32 /* Creation order value of attribute to compare */

	/* Attribute message decoder (H5O_msg_decode(f, NULL, H5O_ATTR_ID, ...)) */
	decodeAttr func(f *File, obj []byte) (*h5oAttr, error)
	foundOp    func(attr *h5oAttr) error /* Callback when correct attribute is found */
}

// h5aFhUdCmp is H5A_fh_ud_cmp_t: data exchange structure for dense
// attribute storage. This structure is passed through the fractal heap
// layer to compare attributes.
type h5aFhUdCmp struct {
	/* downward */
	f          *File                                       /* Pointer to file that fractal heap is in */
	name       string                                      /* Name of attribute to compare      */
	record     *h5aDenseBt2NameRec                         /* v2 B-tree record for attribute */
	decodeAttr func(f *File, obj []byte) (*h5oAttr, error) /* Attribute message decoder */
	foundOp    func(attr *h5oAttr) error                   /* Callback when correct attribute is found */

	/* upward */
	cmp int /* Comparison of two attribute names */
}

// h5aBt2Name is H5A_BT2_NAME: v2 B-tree class for indexing 'name' field of
// attributes.
func h5aBt2Name() *h5b2Class {
	return &h5b2Class{
		id:      h5b2AttrDenseNameID,       /* Type of B-tree */
		compare: h5aDenseBtree2NameCompare, /* Record comparison callback */
		decode:  h5aDenseBtree2NameDecode,  /* Record decoding callback */
	}
}

// h5aBt2Corder is H5A_BT2_CORDER: v2 B-tree class for indexing 'creation
// order' field of attributes.
func h5aBt2Corder() *h5b2Class {
	return &h5b2Class{
		id:      h5b2AttrDenseCorderID,       /* Type of B-tree */
		compare: h5aDenseBtree2CorderCompare, /* Record comparison callback */
		decode:  h5aDenseBtree2CorderDecode,  /* Record decoding callback */
	}
}

// h5aDenseFhNameCmp is H5A__dense_fh_name_cmp: compares the name of an
// attribute in a fractal heap to another name.
func h5aDenseFhNameCmp(obj []byte, udata *h5aFhUdCmp) error {
	if udata.decodeAttr == nil {
		return errorf(H5E_OHDR, H5E_CANTDECODE, "no attribute decoder for dense attribute storage")
	}

	/* Decode attribute information */
	attr, err := udata.decodeAttr(udata.f, obj)
	if err != nil {
		return wrapf(err, H5E_OHDR, H5E_CANTDECODE, "can't decode attribute")
	}

	/* Compare the string values */
	udata.cmp = strings.Compare(udata.name, attr.name)

	/* Check for correct attribute & callback to make */
	if udata.cmp == 0 && udata.foundOp != nil {
		/* Check whether we should "reconstitute" the shared message info */
		if udata.record.flags&h5oMsgFlagShared != 0 {
			/* (H5SM_reconstitute(&attr->sh_loc, f, H5O_ATTR_ID, record->id)) */
			attr.shared = &h5oShared{
				typ:       h5oShareTypeSohm,
				heapID:    append([]byte(nil), udata.record.id[:]...),
				oh:        haddrUndef,
				msgTypeID: h5oAttrID,
			}
		}

		/* Set the creation order index for the attribute */
		/* (h5oAttr.crtIdx is 16 bits wide; H5O_msg_crt_idx_t is 32) */
		attr.crtIdx = uint16(udata.record.corder)

		/* Make callback */
		if err := udata.foundOp(attr); err != nil {
			return wrapf(err, H5E_OHDR, H5E_CANTOPERATE, "attribute found callback failed")
		}
	}
	return nil
}

// h5aDenseBtree2NameCompare is H5A__dense_btree2_name_compare: compare two
// native information records, according to some key. bt2Udata is a
// *h5aBt2UdCommon, bt2Rec a *h5aDenseBt2NameRec.
func h5aDenseBtree2NameCompare(f *File, ctx any, bt2Udata any, bt2Rec any) (int, error) {
	udata, ok1 := bt2Udata.(*h5aBt2UdCommon)
	rec, ok2 := bt2Rec.(*h5aDenseBt2NameRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_ATTR, H5E_BADTYPE, "unexpected dense attribute name record types %T, %T", bt2Udata, bt2Rec)
	}

	/* Check hash value */
	if udata.nameHash < rec.hash {
		return -1, nil
	} else if udata.nameHash > rec.hash {
		return 1, nil
	}

	/* Prepare user data for callback */
	/* down */
	fhUdata := h5aFhUdCmp{
		f:          f,
		name:       udata.name,
		record:     rec,
		decodeAttr: udata.decodeAttr,
		foundOp:    udata.foundOp,

		/* up */
		cmp: 0,
	}

	/* Check for attribute in shared storage */
	fheap := udata.fheap
	if rec.flags&h5oMsgFlagShared != 0 {
		fheap = udata.sharedFheap
	}
	if fheap == nil {
		return 0, errorf(H5E_HEAP, H5E_CANTCOMPARE, "no fractal heap for dense attribute storage")
	}

	/* Check if the user's attribute and the B-tree's attribute have the same name */
	if err := fheap.op(rec.id[:], func(obj []byte) error { return h5aDenseFhNameCmp(obj, &fhUdata) }); err != nil {
		return 0, wrapf(err, H5E_HEAP, H5E_CANTCOMPARE, "can't compare btree2 records")
	}

	/* Callback will set comparison value */
	return fhUdata.cmp, nil
}

// h5aDenseBtree2NameDecode is H5A__dense_btree2_name_decode: decode raw
// disk form of record into native form.
func h5aDenseBtree2NameDecode(f *File, ctx any, p *image) (any, error) {
	nrecord := &h5aDenseBt2NameRec{}

	/* Decode the record's fields */
	copy(nrecord.id[:], p.bytes(h5oFheapIDLen))
	nrecord.flags = p.uint8()
	nrecord.corder = p.uint32()
	nrecord.hash = p.uint32()
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}

// h5aDenseBtree2CorderCompare is H5A__dense_btree2_corder_compare: compare
// two native information records, according to some key. bt2Udata is a
// *h5aBt2UdCommon, bt2Rec a *h5aDenseBt2CorderRec.
func h5aDenseBtree2CorderCompare(f *File, ctx any, bt2Udata any, bt2Rec any) (int, error) {
	udata, ok1 := bt2Udata.(*h5aBt2UdCommon)
	rec, ok2 := bt2Rec.(*h5aDenseBt2CorderRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_ATTR, H5E_BADTYPE, "unexpected dense attribute creation order record types %T, %T", bt2Udata, bt2Rec)
	}

	/* Check creation order value */
	if udata.corder < rec.corder {
		return -1, nil
	} else if udata.corder > rec.corder {
		return 1, nil
	}
	return 0, nil
}

// h5aDenseBtree2CorderDecode is H5A__dense_btree2_corder_decode: decode raw
// disk form of record into native form.
func h5aDenseBtree2CorderDecode(f *File, ctx any, p *image) (any, error) {
	nrecord := &h5aDenseBt2CorderRec{}

	/* Decode the record's fields */
	copy(nrecord.id[:], p.bytes(h5oFheapIDLen))
	nrecord.flags = p.uint8()
	nrecord.corder = p.uint32()
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}
