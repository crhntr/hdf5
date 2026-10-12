package hdf5

// Translated from src/H5Gbtree2.c (read side only): the v2 B-tree record
// classes for indexing the links of "dense" (new-style) groups by name
// (H5G_BT2_NAME, B-tree type 5) and by creation order (H5G_BT2_CORDER,
// B-tree type 6). The record types and the B-tree user data come from
// src/H5Gpkg.h.
//
// Neither class has a callback context: open the B-trees with a nil ctx,
//
//	bt2, err := f.b2Open(linfo.nameBt2Addr, h5gBt2Name(), nil)
//
// and search them with a *h5gBt2UdCommon as udata. The name comparison
// needs the link object from the group's fractal heap: the caller sets
// fheap and decodeLink (the H5O_msg_decode(H5O_LINK_ID) step, i.e. the link
// message decoder), and optionally foundOp (the H5B2_found_t "found_op" that
// receives the decoded link when the names match).

import "strings"

// H5G_DENSE_FHEAP_ID_LEN: size of a heap ID of a link in the fractal heap of
// a dense group.
const h5gDenseFheapIDLen = 7

// h5gDenseBt2NameRec is H5G_dense_bt2_name_rec_t: native 'name' field index
// records in the v2 B-tree.
type h5gDenseBt2NameRec struct {
	id   [h5gDenseFheapIDLen]byte /* Heap ID for link */
	hash uint32                   /* Hash of 'name' field value */
}

// h5gDenseBt2CorderRec is H5G_dense_bt2_corder_rec_t: native 'creation
// order' field index records in the v2 B-tree.
type h5gDenseBt2CorderRec struct {
	id     [h5gDenseFheapIDLen]byte /* Heap ID for link */
	corder int64                    /* 'creation order' field value */
}

// h5gBt2UdCommon is H5G_bt2_ud_common_t: common data exchange structure for
// dense link storage. This structure is passed through the v2 B-tree layer
// (as udata of find/neighbor) to the methods for the objects to which the
// v2 B-tree points.
type h5gBt2UdCommon struct {
	/* downward */
	fheap    *h5hf  /* Fractal heap handle               */
	name     string /* Name of link to compare           */
	nameHash uint32 /* Hash of name of link to compare   */
	corder   int64  /* Creation order value of link to compare   */

	/* Link message decoder (H5O_msg_decode(f, NULL, H5O_LINK_ID, ...)) */
	decodeLink func(f *File, obj []byte) (*h5oLink, error)
	foundOp    func(lnk *h5oLink) error /* Callback when correct link is found */
}

// h5gFhUdCmp is H5G_fh_ud_cmp_t: data exchange structure for dense link
// storage. This structure is passed through the fractal heap layer to
// compare links.
type h5gFhUdCmp struct {
	/* downward */
	f          *File                                       /* Pointer to file that fractal heap is in */
	name       string                                      /* Name of link to compare           */
	decodeLink func(f *File, obj []byte) (*h5oLink, error) /* Link message decoder */
	foundOp    func(lnk *h5oLink) error                    /* Callback when correct link is found */

	/* upward */
	cmp int /* Comparison of two link names      */
}

// h5gBt2Name is H5G_BT2_NAME: v2 B-tree class for indexing 'name' field of
// links.
func h5gBt2Name() *h5b2Class {
	return &h5b2Class{
		id:      h5b2GrpDenseNameID,        /* Type of B-tree */
		compare: h5gDenseBtree2NameCompare, /* Record comparison callback */
		decode:  h5gDenseBtree2NameDecode,  /* Record decoding callback */
	}
}

// h5gBt2Corder is H5G_BT2_CORDER: v2 B-tree class for indexing 'creation
// order' field of links.
func h5gBt2Corder() *h5b2Class {
	return &h5b2Class{
		id:      h5b2GrpDenseCorderID,        /* Type of B-tree */
		compare: h5gDenseBtree2CorderCompare, /* Record comparison callback */
		decode:  h5gDenseBtree2CorderDecode,  /* Record decoding callback */
	}
}

// h5gDenseFhNameCmp is H5G__dense_fh_name_cmp: compares the name of a link
// in a fractal heap to another name.
func h5gDenseFhNameCmp(obj []byte, udata *h5gFhUdCmp) error {
	if udata.decodeLink == nil {
		return errorf(H5E_SYM, H5E_CANTDECODE, "no link decoder for dense link storage")
	}

	/* Decode link information */
	lnk, err := udata.decodeLink(udata.f, obj)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTDECODE, "can't decode link")
	}

	/* Compare the string values */
	udata.cmp = strings.Compare(udata.name, lnk.name)

	/* Check for correct link & callback to make */
	if udata.cmp == 0 && udata.foundOp != nil {
		if err := udata.foundOp(lnk); err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTOPERATE, "link found callback failed")
		}
	}
	return nil
}

// h5gDenseBtree2NameCompare is H5G__dense_btree2_name_compare: compare two
// native information records, according to some key. bt2Udata is a
// *h5gBt2UdCommon, bt2Rec a *h5gDenseBt2NameRec.
func h5gDenseBtree2NameCompare(f *File, ctx any, bt2Udata any, bt2Rec any) (int, error) {
	udata, ok1 := bt2Udata.(*h5gBt2UdCommon)
	rec, ok2 := bt2Rec.(*h5gDenseBt2NameRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_SYM, H5E_BADTYPE, "unexpected dense link name record types %T, %T", bt2Udata, bt2Rec)
	}

	/* Check hash value */
	if udata.nameHash < rec.hash {
		return -1, nil
	} else if udata.nameHash > rec.hash {
		return 1, nil
	}

	/* Prepare user data for callback */
	/* down */
	fhUdata := h5gFhUdCmp{
		f:          f,
		name:       udata.name,
		decodeLink: udata.decodeLink,
		foundOp:    udata.foundOp,

		/* up */
		cmp: 0,
	}
	if udata.fheap == nil {
		return 0, errorf(H5E_HEAP, H5E_CANTCOMPARE, "no fractal heap for dense link storage")
	}

	/* Check if the user's link and the B-tree's link have the same name */
	if err := udata.fheap.op(rec.id[:], func(obj []byte) error { return h5gDenseFhNameCmp(obj, &fhUdata) }); err != nil {
		return 0, wrapf(err, H5E_HEAP, H5E_CANTCOMPARE, "can't compare btree2 records")
	}

	/* Callback will set comparison value */
	return fhUdata.cmp, nil
}

// h5gDenseBtree2NameDecode is H5G__dense_btree2_name_decode: decode raw
// disk form of record into native form.
func h5gDenseBtree2NameDecode(f *File, ctx any, p *image) (any, error) {
	nrecord := &h5gDenseBt2NameRec{}

	/* Decode the record's fields */
	nrecord.hash = p.uint32()
	copy(nrecord.id[:], p.bytes(h5gDenseFheapIDLen))
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}

// h5gDenseBtree2CorderCompare is H5G__dense_btree2_corder_compare: compare
// two native information records, according to some key. bt2Udata is a
// *h5gBt2UdCommon, bt2Rec a *h5gDenseBt2CorderRec.
func h5gDenseBtree2CorderCompare(f *File, ctx any, bt2Udata any, bt2Rec any) (int, error) {
	udata, ok1 := bt2Udata.(*h5gBt2UdCommon)
	rec, ok2 := bt2Rec.(*h5gDenseBt2CorderRec)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_SYM, H5E_BADTYPE, "unexpected dense link creation order record types %T, %T", bt2Udata, bt2Rec)
	}

	/* Check creation order value */
	if udata.corder < rec.corder {
		return -1, nil
	} else if udata.corder > rec.corder {
		return 1, nil
	}
	return 0, nil
}

// h5gDenseBtree2CorderDecode is H5G__dense_btree2_corder_decode: decode raw
// disk form of record into native form.
func h5gDenseBtree2CorderDecode(f *File, ctx any, p *image) (any, error) {
	nrecord := &h5gDenseBt2CorderRec{}

	/* Decode the record's fields */
	nrecord.corder = int64(p.uint64())
	copy(nrecord.id[:], p.bytes(h5gDenseFheapIDLen))
	if err := p.done(); err != nil {
		return nil, err
	}
	return nrecord, nil
}
