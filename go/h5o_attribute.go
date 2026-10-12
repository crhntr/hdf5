package hdf5

// Translated from src/H5Oattribute.c (read side only): object header
// routines for attributes. An object's attributes are either stored
// "compactly" as attribute messages in its object header, or "densely" in
// a fractal heap indexed by v2 B-trees (h5a_dense.go), which the attribute
// info message (version 2 object headers only) points to. Creating,
// writing, renaming and removing attributes are not translated.

import "errors"

// h5oIterOpn is H5O_iter_opn_t: user data for iteration callback for
// opening an attribute.
type h5oIterOpn struct {
	/* down */
	name string /* Name of attribute to open */

	/* up */
	attr *h5oAttr /* Attribute data to update object header with */
}

// h5oIterXst is H5O_iter_xst_t: user data for iteration callback for
// checking for an attribute.
type h5oIterXst struct {
	/* down */
	name string /* Name of attribute to open */

	/* up */
	exists bool /* Pointer to flag to indicate attribute exists */
}

// h5oAttrOpenCb is H5O__attr_open_cb: object header iterator callback
// routine to open an attribute stored compactly.
func h5oAttrOpenCb(oh *h5o, native any, sequence uint32, udata *h5oIterOpn) error {
	attr, ok := native.(*h5oAttr)
	if !ok || attr == nil {
		return errorf(H5E_ATTR, H5E_BADTYPE, "attribute message decoded as %T", native)
	}

	/* Check for correct attribute message to modify */
	if attr.name == udata.name {
		/* Make a copy of the attribute to return */
		udata.attr = h5aCopy(attr)

		/* Assign [somewhat arbitrary] creation order value, for older versions
		 * of the format or if creation order is not tracked */
		if oh.version == h5oVersion1 || oh.flags&h5oHdrAttrCrtOrderTracked == 0 {
			udata.attr.crtIdx = sequence
		}

		/* Stop iterating */
		return errBIterStop
	}
	return nil
}

// oAttrOpenByName is H5O__attr_open_by_name: open an existing attribute in
// an object header. A missing attribute is an error matching ErrNotFound.
// The returned attribute is a copy that the caller owns.
func (f *File) oAttrOpenByName(oh *h5o, name string) (*h5oAttr, error) {
	var ainfo *h5oAinfo /* Attribute information for object */

	/* Check for attribute info stored */
	if oh.version > h5oVersion1 {
		/* Check for (& retrieve if available) attribute info */
		var err error
		if ainfo, err = f.h5aGetAinfo(oh); err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't check for attribute info message")
		}
	}

	/* (Attributes are not tracked as open objects here, so the attribute
	 *  is always opened as a new object) */
	var openedAttr *h5oAttr

	/* Check for attributes in dense storage */
	if ainfo != nil && addrDefined(ainfo.fheapAddr) {
		/* Open attribute with dense storage */
		attr, err := f.h5aDenseOpen(ainfo, name)
		if err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "can't open attribute")
		}
		openedAttr = attr
	} else {
		/* Set up user data for callback */
		udata := h5oIterOpn{name: name}

		/* Iterate over attributes, to locate correct one to open */
		var sequence uint32
		err := f.msgIterate(oh, h5oAttrID, func(_ *h5oMesg, native any) error {
			err := h5oAttrOpenCb(oh, native, sequence, &udata)
			sequence++
			return err
		})
		if err != nil {
			return nil, wrapf(err, H5E_ATTR, H5E_CANTOPENOBJ, "error updating attribute")
		}

		/* Check that we found the attribute */
		if udata.attr == nil {
			return nil, errorf(H5E_ATTR, H5E_NOTFOUND, "can't locate attribute: '%s'", name)
		}

		/* Get attribute opened from object header */
		openedAttr = udata.attr
	}

	/* Mark datatype as being on disk now */
	if err := h5aOpenCommon(openedAttr); err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTINIT, "invalid attribute")
	}
	return openedAttr, nil
}

// h5oAttrOpenByIdxCb is H5O__attr_open_by_idx_cb: callback routine opening
// an attribute by index.
func h5oAttrOpenByIdxCb(attr *h5oAttr, retAttr **h5oAttr) error {
	/* Copy attribute information.  Shared some attribute information. */
	*retAttr = h5aCopy(attr)
	return errBIterStop
}

// oAttrOpenByIdx is H5O__attr_open_by_idx: open an existing attribute in
// an object header according to an index.
func (f *File) oAttrOpenByIdx(oh *h5o, idxType h5aIndex, order h5aIterOrder, n uint64) (*h5oAttr, error) {
	var openedAttr *h5oAttr

	/* Iterate over attributes to locate correct one */
	err := f.oAttrIterateReal(oh, idxType, order, n, nil, func(attr *h5oAttr) error {
		return h5oAttrOpenByIdxCb(attr, &openedAttr)
	})
	if err != nil && !errors.Is(err, errBIterStop) {
		return nil, wrapf(err, H5E_ATTR, H5E_BADITER, "can't locate attribute")
	}
	if openedAttr == nil {
		return nil, errorf(H5E_ATTR, H5E_NOTFOUND, "can't locate attribute")
	}

	/* Mark datatype as being on disk now */
	if err := h5aOpenCommon(openedAttr); err != nil {
		return nil, wrapf(err, H5E_ATTR, H5E_CANTINIT, "invalid attribute")
	}
	return openedAttr, nil
}

// oAttrIterateReal is H5O_attr_iterate_real: internal routine to iterate
// over attributes for an object, in the order of the index idxType, after
// skipping skip attributes. The attributes passed to op are copies that op
// may keep. op may return errBIterStop to stop early, which is returned
// (possibly wrapped) to the caller; lastAttr, when not nil, receives the
// number of attributes examined.
func (f *File) oAttrIterateReal(oh *h5o, idxType h5aIndex, order h5aIterOrder, skip uint64, lastAttr *uint64, attrOp func(attr *h5oAttr) error) error {
	var ainfo *h5oAinfo /* Attribute information for object */

	/* Check for attribute info stored */
	if oh.version > h5oVersion1 {
		/* Check for (& retrieve if available) attribute info */
		var err error
		if ainfo, err = f.h5aGetAinfo(oh); err != nil {
			return wrapf(err, H5E_ATTR, H5E_CANTGET, "can't check for attribute info message")
		}
	}

	/* Check for attributes stored densely */
	if ainfo != nil && addrDefined(ainfo.fheapAddr) {
		/* Check for skipping too many attributes */
		if skip > 0 && skip >= ainfo.nattrs {
			return errorf(H5E_ARGS, H5E_BADVALUE, "invalid index specified")
		}

		/* Iterate over attributes in dense storage */
		if err := f.h5aDenseIterate(ainfo, idxType, order, skip, lastAttr, attrOp); err != nil {
			if errors.Is(err, errBIterStop) {
				return err
			}
			return wrapf(err, H5E_ATTR, H5E_BADITER, "error iterating over attributes")
		}
		return nil
	}

	/* Build table of attributes for compact storage */
	atable, err := f.h5aCompactBuildTable(oh, idxType, order)
	if err != nil {
		return wrapf(err, H5E_ATTR, H5E_CANTINIT, "error building attribute table")
	}

	/* Check for skipping too many attributes */
	if skip > 0 && skip >= uint64(len(atable.attrs)) {
		return errorf(H5E_ARGS, H5E_BADVALUE, "invalid index specified")
	}

	/* Iterate over attributes in table */
	if err := h5aAttrIterateTable(atable, skip, lastAttr, attrOp); err != nil {
		if errors.Is(err, errBIterStop) {
			return err
		}
		return wrapf(err, H5E_ATTR, H5E_BADITER, "iteration operator failed")
	}
	return nil
}

// oAttrCount is H5O__attr_count_real: determine the # of attributes on an
// object.
func (f *File) oAttrCount(oh *h5o) (uint64, error) {
	/* Check for attributes stored densely */
	if oh.version > h5oVersion1 {
		/* Attempt to get the attribute information from the object header */
		ainfo, err := f.h5aGetAinfo(oh)
		if err != nil {
			return 0, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't check for attribute info message")
		}
		if ainfo != nil {
			return ainfo.nattrs, nil
		}
		return 0, nil
	}

	/* Loop over all messages, counting the attributes */
	return uint64(f.msgCount(oh, h5oAttrID)), nil
}

// h5oAttrExistsCb is H5O__attr_exists_cb: object header iterator callback
// routine to check for an attribute stored compactly, by name.
func h5oAttrExistsCb(native any, udata *h5oIterXst) error {
	attr, ok := native.(*h5oAttr)
	if !ok || attr == nil {
		return errorf(H5E_ATTR, H5E_BADTYPE, "attribute message decoded as %T", native)
	}

	/* Check for correct attribute message */
	if attr.name == udata.name {
		/* Indicate that this message is the attribute sought */
		udata.exists = true

		/* Stop iterating */
		return errBIterStop
	}
	return nil
}

// oAttrExists is H5O__attr_exists: determine if an attribute with a
// particular name exists on an object.
func (f *File) oAttrExists(oh *h5o, name string) (bool, error) {
	var ainfo *h5oAinfo /* Attribute information for object */

	/* Check for attribute info stored */
	if oh.version > h5oVersion1 {
		/* Check for (& retrieve if available) attribute info */
		var err error
		if ainfo, err = f.h5aGetAinfo(oh); err != nil {
			return false, wrapf(err, H5E_ATTR, H5E_CANTGET, "can't check for attribute info message")
		}
	}

	/* Check for attributes stored densely */
	if ainfo != nil && addrDefined(ainfo.fheapAddr) {
		/* Check if attribute exists in dense storage */
		exists, err := f.h5aDenseExists(ainfo, name)
		if err != nil {
			return false, wrapf(err, H5E_ATTR, H5E_BADITER, "error checking for existence of attribute")
		}
		return exists, nil
	}

	/* Set up user data for callback */
	udata := h5oIterXst{name: name}

	/* Iterate over existing attributes, checking for attribute with same name */
	if err := f.msgIterate(oh, h5oAttrID, func(_ *h5oMesg, native any) error {
		return h5oAttrExistsCb(native, &udata)
	}); err != nil {
		return false, wrapf(err, H5E_ATTR, H5E_BADITER, "error checking for existence of attribute")
	}
	return udata.exists, nil
}
