package hdf5

// Translated from src/H5Aint.c (read side only): opening and reading
// attributes, the attribute tables used to iterate over attributes in a
// requested order, and H5A__get_ainfo. The H5A_t of the C library is the
// h5oAttr decoded from the attribute message (h5o_attr.go); attribute
// creation, writing, renaming, deleting and copying are not translated.

import (
	"errors"
	"math/bits"
	"sort"

	"github.com/crhntr/hdf5/go/h5"
)

// H5_index_t: the index used for iterating over attributes (H5public.h).
type h5aIndex int

const (
	h5aIndexName     h5aIndex = 0 /* Index on names (H5_INDEX_NAME) */
	h5aIndexCrtOrder h5aIndex = 1 /* Index on creation order (H5_INDEX_CRT_ORDER) */
)

// H5_iter_order_t: the order of an iteration (H5public.h).
type h5aIterOrder int

const (
	h5aIterInc    h5aIterOrder = 0 /* Increasing order (H5_ITER_INC) */
	h5aIterDec    h5aIterOrder = 1 /* Decreasing order (H5_ITER_DEC) */
	h5aIterNative h5aIterOrder = 2 /* No particular order, whatever is fastest (H5_ITER_NATIVE) */
)

// h5aAttrTable is H5A_attr_table_t: a table of attributes, used for
// iterating over the attributes of an object in a particular order.
type h5aAttrTable struct {
	attrs []*h5oAttr /* Pointer to array of attribute pointers */
}

// h5aInfo is H5A_info_t: information about an attribute.
type h5aInfo struct {
	corderValid bool    /* Indicate if creation order is valid */
	corder      uint32  /* Creation order */
	cset        h5.Cset /* Character set of attribute name */
	dataSize    uint64  /* Size of raw data */
}

// h5aOpenCommon is H5A__open_common: finishes initializing an attribute
// that was opened. The C routine records the object location of the
// attribute; here it checks that the attribute is complete: its datatype
// and dataspace have been decoded (resolving shared messages) and the
// raw data matches their sizes.
func h5aOpenCommon(attr *h5oAttr) error {
	/* check args */
	if attr == nil {
		return errorf(H5E_ATTR, H5E_CANTOPENOBJ, "no attribute")
	}
	if attr.dt == nil {
		return errorf(H5E_ATTR, H5E_CANTOPENOBJ, "attribute '%s' has no datatype", attr.name)
	}
	if attr.ds == nil {
		return errorf(H5E_ATTR, H5E_CANTOPENOBJ, "attribute '%s' has no dataspace", attr.name)
	}
	if uint64(len(attr.data)) != attr.dataSize {
		return errorf(H5E_ATTR, H5E_CANTOPENOBJ, "attribute '%s' has %d bytes of data, expected %d", attr.name, len(attr.data), attr.dataSize)
	}
	return nil
}

// aReadReal is H5A__read: read the complete attribute and convert its
// elements (stored in the file datatype) to canonical values.
func (f *File) aReadReal(attr *h5oAttr) ([]any, error) {
	if err := h5aOpenCommon(attr); err != nil {
		return nil, err
	}

	/* Create buffer for data to store on disk */
	nelmts := h5sExtentNelem(attr.ds)

	if nelmts == 0 {
		return []any{}, nil
	}

	/* Get the memory and file datatype sizes */
	srcTypeSize := attr.dt.Size
	hi, size := bits.Mul64(nelmts, srcTypeSize)
	if hi != 0 {
		return nil, errorf(H5E_ATTR, H5E_OVERFLOW, "attribute data size exceeds addressable range")
	}

	/* Check if the attribute has any data yet */
	/* (an attribute read from the file always has its data; make sure it
	 *  matches the dataspace and datatype) */
	if size != attr.dataSize || uint64(len(attr.data)) != size {
		return nil, errorf(H5E_ATTR, H5E_BADSIZE, "attribute '%s' has %d bytes of data, expected %d elements of %d bytes", attr.name, len(attr.data), nelmts, srcTypeSize)
	}

	/* Convert the disk buffer into memory values */
	values, err := h5tDecodeValues(f, attr.dt, attr.data, nelmts)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTCONVERT, "datatype conversion failed")
	}
	return values, nil
}

// h5aGetInfo is H5A__get_info: retrieve information about an attribute.
func h5aGetInfo(attr *h5oAttr) h5aInfo {
	/* Set info for attribute */
	ainfo := h5aInfo{cset: attr.cset, dataSize: attr.dataSize}
	if attr.crtIdx == h5oMaxCrtOrderIdx {
		ainfo.corderValid = false
		ainfo.corder = 0
	} else {
		ainfo.corderValid = true
		ainfo.corder = attr.crtIdx
	}
	return ainfo
}

// h5aCopy is H5A__copy: copy an attribute. The C routine shares the
// attribute's "shared" part (name, datatype, dataspace, data) between the
// copies; here every part is copied, so that the returned attribute never
// aliases a decoded message that lives in the metadata cache.
func h5aCopy(oldAttr *h5oAttr) *h5oAttr {
	if oldAttr == nil {
		return nil
	}
	newAttr := *oldAttr
	newAttr.dt = h5aCopyDatatype(oldAttr.dt)
	if oldAttr.ds != nil {
		ds := *oldAttr.ds
		ds.size = append([]uint64(nil), oldAttr.ds.size...)
		if oldAttr.ds.max != nil {
			ds.max = append([]uint64(nil), oldAttr.ds.max...)
		}
		newAttr.ds = &ds
	}
	if oldAttr.data != nil {
		newAttr.data = append([]byte(nil), oldAttr.data...)
	}
	newAttr.dtShared = h5aCopyShared(oldAttr.dtShared)
	newAttr.dsShared = h5aCopyShared(oldAttr.dsShared)
	newAttr.shared = h5aCopyShared(oldAttr.shared)
	return &newAttr
}

// h5aCopyShared copies a shared message header (part of H5A__copy).
func h5aCopyShared(sh *h5oShared) *h5oShared {
	if sh == nil {
		return nil
	}
	c := *sh
	if sh.heapID != nil {
		c.heapID = append([]byte(nil), sh.heapID...)
	}
	return &c
}

// h5aCopyDatatype is the H5T_copy(H5T_COPY_ALL) step of H5A__copy: a deep
// copy of a datatype tree.
func h5aCopyDatatype(dt *h5.Datatype) *h5.Datatype {
	if dt == nil {
		return nil
	}
	c := *dt
	if dt.Members != nil {
		c.Members = make([]h5.Member, len(dt.Members))
		for i, m := range dt.Members {
			c.Members[i] = h5.Member{Name: m.Name, Offset: m.Offset, Type: h5aCopyDatatype(m.Type)}
		}
	}
	if dt.EnumNames != nil {
		c.EnumNames = append([]string(nil), dt.EnumNames...)
	}
	if dt.EnumValues != nil {
		c.EnumValues = append([]int64(nil), dt.EnumValues...)
	}
	if dt.ArrayDims != nil {
		c.ArrayDims = append([]uint64(nil), dt.ArrayDims...)
	}
	c.Base = h5aCopyDatatype(dt.Base)
	return &c
}

// h5aCompactBtUd is H5A_compact_bt_ud_t: data exchange structure to pass
// through the object header layer for H5O__msg_iterate_real when building
// a table of compact attributes.
type h5aCompactBtUd struct {
	f           *File         /* Pointer to file that object header is in */
	atable      *h5aAttrTable /* Pointer to attribute table to build */
	bogusCrtIdx bool          /* Whether bogus creation index values need to be set */
}

// h5aCompactBuildTableCb is H5A__compact_build_table_cb: object header
// iterator callback routine to copy attribute into table.
func h5aCompactBuildTableCb(native any, sequence uint32, udata *h5aCompactBtUd) error {
	attr, ok := native.(*h5oAttr)
	if !ok || attr == nil {
		return errorf(H5E_ATTR, H5E_BADTYPE, "attribute message decoded as %T", native)
	}

	/* Copy attribute into table */
	c := h5aCopy(attr)

	/* Assign [somewhat arbitrary] creation order value, if requested */
	if udata.bogusCrtIdx {
		c.crtIdx = sequence
	}

	/* Increment attribute count */
	udata.atable.attrs = append(udata.atable.attrs, c)
	return nil
}

// h5aCompactBuildTable is H5A__compact_build_table: builds a table
// containing a sorted list of attributes for an object.
//
// Note: Used for building table of attributes in non-native iteration
// order for an index.
func (f *File) h5aCompactBuildTable(oh *h5o, idxType h5aIndex, order h5aIterOrder) (*h5aAttrTable, error) {
	/* Initialize table */
	atable := &h5aAttrTable{}

	/* Set up user data for iteration */
	udata := h5aCompactBtUd{
		f:           f,
		atable:      atable,
		bogusCrtIdx: oh.version == h5oVersion1 || oh.flags&h5oHdrAttrCrtOrderTracked == 0,
	}

	/* Iterate over existing attributes, checking for attribute with same name */
	var sequence uint32
	err := f.msgIterate(oh, h5oAttrID, func(_ *h5oMesg, native any) error {
		err := h5aCompactBuildTableCb(native, sequence, &udata)
		sequence++
		return err
	})
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_BADITER, "error building attribute table")
	}

	/* Don't sort an empty table. */
	if len(atable.attrs) > 0 {
		/* Sort attribute table in correct iteration order */
		if err := h5aAttrSortTable(atable, idxType, order); err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTSORT, "error sorting attribute table")
		}
	}
	return atable, nil
}

// h5aDenseBuildTableCb is H5A__dense_build_table_cb: callback routine for
// building table of attributes from dense attribute storage.
func h5aDenseBuildTableCb(attr *h5oAttr, atable *h5aAttrTable) error {
	/* Copy attribute information.  Share the attribute object in copying. */
	/* (the attribute was freshly decoded from the heap; it is not shared
	 *  with anything else) */
	atable.attrs = append(atable.attrs, attr)
	return nil
}

// h5aDenseBuildTableMaxPrealloc bounds the table capacity allocated up
// front from the (untrusted) record count of the name index.
const h5aDenseBuildTableMaxPrealloc = 1 << 16

// h5aDenseBuildTable is H5A__dense_build_table: builds a table containing
// a sorted list of attributes for an object.
//
// Note: Used for building table of attributes in non-native iteration
// order for an index.  Uses the "name" index to retrieve records, but the
// 'idx_type' index for sorting them.
func (f *File) h5aDenseBuildTable(ainfo *h5oAinfo, idxType h5aIndex, order h5aIterOrder) (*h5aAttrTable, error) {
	/* Sanity check */
	if !addrDefined(ainfo.fheapAddr) || !addrDefined(ainfo.nameBt2Addr) {
		return nil, errorf(H5E_ATTR, H5E_BADVALUE, "no dense attribute storage")
	}

	/* Open the name index v2 B-tree */
	bt2Name, err := f.b2Open(ainfo.nameBt2Addr, h5aBt2Name(), nil)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open v2 B-tree for name index")
	}

	/* Retrieve # of records in "name" B-tree */
	/* (should be same # of records in all indices) */
	nrec := bt2Name.numRecords()

	atable := &h5aAttrTable{}

	/* Allocate space for the table entries */
	if nrec > 0 {
		/* Allocate the table to store the attributes */
		atable.attrs = make([]*h5oAttr, 0, min(nrec, h5aDenseBuildTableMaxPrealloc))

		/* Iterate over the links in the group, building a table of the link messages */
		err := f.h5aDenseIterate(ainfo, h5aIndexName, h5aIterNative, 0, nil, func(attr *h5oAttr) error {
			return h5aDenseBuildTableCb(attr, atable)
		})
		if err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTINIT, "error building attribute table")
		}

		/* Sort attribute table in correct iteration order */
		if err := h5aAttrSortTable(atable, idxType, order); err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTSORT, "error sorting attribute table")
		}
	}
	return atable, nil
}

// h5aAttrCmpNameInc is H5A__attr_cmp_name_inc: comparing two attribute
// names, in increasing alphabetic order (same as strcmp()).
func h5aAttrCmpNameInc(attr1, attr2 *h5oAttr) int {
	return h5aStrcmp(attr1.name, attr2.name)
}

// h5aAttrCmpNameDec is H5A__attr_cmp_name_dec: comparing two attribute
// names, in decreasing alphabetic order (opposite of strcmp()).
func h5aAttrCmpNameDec(attr1, attr2 *h5oAttr) int {
	return h5aStrcmp(attr2.name, attr1.name)
}

// h5aStrcmp is strcmp(): byte-wise comparison of two names (Go strings
// compare as unsigned bytes, like strcmp).
func h5aStrcmp(s1, s2 string) int {
	switch {
	case s1 < s2:
		return -1
	case s1 > s2:
		return 1
	}
	return 0
}

// h5aAttrCmpCorderInc is H5A__attr_cmp_corder_inc: comparing two
// attributes, in increasing creation order.
func h5aAttrCmpCorderInc(attr1, attr2 *h5oAttr) int {
	if attr1.crtIdx < attr2.crtIdx {
		return -1
	} else if attr1.crtIdx > attr2.crtIdx {
		return 1
	}
	return 0
}

// h5aAttrCmpCorderDec is H5A__attr_cmp_corder_dec: comparing two
// attributes, in decreasing creation order.
func h5aAttrCmpCorderDec(attr1, attr2 *h5oAttr) int {
	if attr1.crtIdx < attr2.crtIdx {
		return 1
	} else if attr1.crtIdx > attr2.crtIdx {
		return -1
	}
	return 0
}

// h5aAttrSortTable is H5A__attr_sort_table: sort table containing a list
// of attributes for an object. (qsort is not stable; a stable sort keeps
// the result deterministic when two attributes compare as equal.)
func h5aAttrSortTable(atable *h5aAttrTable, idxType h5aIndex, order h5aIterOrder) error {
	var cmp func(a, b *h5oAttr) int

	/* Pick appropriate comparison routine */
	switch idxType {
	case h5aIndexName:
		switch order {
		case h5aIterInc:
			cmp = h5aAttrCmpNameInc
		case h5aIterDec:
			cmp = h5aAttrCmpNameDec
		case h5aIterNative:
		default:
			return errorf(H5E_ARGS, H5E_BADVALUE, "unknown iteration order %d", order)
		}
	case h5aIndexCrtOrder:
		switch order {
		case h5aIterInc:
			cmp = h5aAttrCmpCorderInc
		case h5aIterDec:
			cmp = h5aAttrCmpCorderDec
		case h5aIterNative:
		default:
			return errorf(H5E_ARGS, H5E_BADVALUE, "unknown iteration order %d", order)
		}
	default:
		return errorf(H5E_ARGS, H5E_BADVALUE, "unknown index type %d", idxType)
	}
	if cmp != nil {
		sort.SliceStable(atable.attrs, func(i, j int) bool { return cmp(atable.attrs[i], atable.attrs[j]) < 0 })
	}
	return nil
}

// h5aAttrIterateTable is H5A__attr_iterate_table: iterate over table
// containing a list of attributes for an object, making appropriate
// callbacks. The operator may return errBIterStop to stop early; that
// error is returned to the caller (the C H5_ITER_STOP return value).
// lastAttr, when not nil, receives the index after the last attribute
// passed to op.
func h5aAttrIterateTable(atable *h5aAttrTable, skip uint64, lastAttr *uint64, op func(attr *h5oAttr) error) error {
	/* Skip over attributes, if requested */
	if lastAttr != nil {
		*lastAttr = skip
	}

	/* Iterate over attribute messages */
	for u := skip; u < uint64(len(atable.attrs)); u++ {
		/* Call the library's callback */
		err := op(atable.attrs[u])

		/* Increment the number of entries passed through */
		if lastAttr != nil {
			*lastAttr++
		}

		/* Check for callback failure and pass along return value */
		if err != nil {
			if errors.Is(err, errBIterStop) {
				return err
			}
			return wrapf(err, H5E_ATTR, H5E_CANTNEXT, "iteration operator failed")
		}
	}
	return nil
}

// h5aGetAinfo is H5A__get_ainfo: retrieves the "attribute info" message
// for an object.  Also sets the number of attributes correctly, if it isn't
// set up yet. Returns a copy of the message (nil when the object header
// has none).
func (f *File) h5aGetAinfo(oh *h5o) (*h5oAinfo, error) {
	/* Check if the "attribute info" message exists */
	if !f.msgExists(oh, h5oAinfoID) {
		return nil, nil
	}

	/* Retrieve the "attribute info" structure */
	v, err := f.msgRead(oh, h5oAinfoID)
	if err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't read AINFO message")
	}
	cached, ok := v.(*h5oAinfo)
	if !ok || cached == nil {
		return nil, errorf(H5E_ATTR, H5E_CANTGET, "can't read AINFO message")
	}
	ainfo := *cached

	/* Check if we don't know how many attributes there are */
	if ainfo.nattrs == ^uint64(0) {
		/* Check if we are using "dense" attribute storage */
		if addrDefined(ainfo.fheapAddr) {
			/* Open the name index v2 B-tree */
			bt2Name, err := f.b2Open(ainfo.nameBt2Addr, h5aBt2Name(), nil)
			if err != nil {
				return nil, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "unable to open v2 B-tree for name index")
			}

			/* Retrieve # of records in "name" B-tree */
			/* (should be same # of records in all indices) */
			ainfo.nattrs = bt2Name.numRecords()
		} else {
			/* Retrieve # of attributes from object header */
			ainfo.nattrs = uint64(f.msgCount(oh, h5oAttrID))
		}
	}
	return &ainfo, nil
}
