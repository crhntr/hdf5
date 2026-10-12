package hdf5

// The message class table H5O_msg_class_g (src/H5Oint.c) and the parts of
// the H5O_msg_class_t structs (src/H5O*.c) used for reading.

// h5oMsgClass is H5O_msg_class_t (read side): the message type ID, its
// name, whether messages of the class may be shared, the decode callback
// (with the H5O_SHARED_DECODE wrapper for shareable classes) and the
// set_crt_index callback.
type h5oMsgClass struct {
	id         uint16 /*message type ID on disk   */
	name       string /*for debugging             */
	shareFlags uint8  /* Message sharing settings */
	// decode returns the native message: *h5sExtent (sdspace), *h5oLinfo,
	// *h5.Datatype (dtype), *h5oFill (fill and fill new), *h5oLink, *h5oEfl,
	// *h5oLayout, *h5oGinfo, *h5oPline, *h5oAttr, h5oName, h5oMtime (mtime
	// and mtime new), *h5oShmesgTable, *h5oCont, *h5oStab, *h5oBtreek,
	// *h5oDrvinfo, *h5oAinfo, h5oRefcount, *h5oFsinfo, *h5oMdci, *h5oBogus,
	// h5oUnknown. It is nil for the null message.
	decode      func(f *File, oh *h5o, mesgFlags uint8, p *image) (any, error)
	setCrtIndex func(native any, crtIdx uint16) any /* Set message's creation index */
}

// shareable reports whether messages of the class may be shared
// (H5O_SHARE_IS_SHARABLE).
func (c *h5oMsgClass) shareable() bool { return c.shareFlags&h5oShareIsSharable != 0 }

// h5oMsgClassG is H5O_msg_class_g: the message class for a message type ID,
// and false for IDs without a class (0x0009 "bogus valid" unless
// H5O_ENABLE_BOGUS, and IDs past H5O_UNKNOWN_ID), which the object header
// decoder maps to the "unknown" class.
//
// It is a function rather than a package-level table because the decode
// callbacks refer back to the table (through msgRead), which Go reports as
// an initialization cycle.
func h5oMsgClassG(id uint16) (*h5oMsgClass, bool) {
	// shared wraps a real decoder with the H5O_SHARED_DECODE template.
	shared := func(id uint16, real func(f *File, oh *h5o, mesgFlags uint8, p *image) (any, error)) func(f *File, oh *h5o, mesgFlags uint8, p *image) (any, error) {
		return func(f *File, oh *h5o, mesgFlags uint8, p *image) (any, error) {
			return f.h5oSharedDecodeTmpl(oh, mesgFlags, p, id, real)
		}
	}
	const sharable = h5oShareIsSharable | h5oShareInOhdr

	switch id {
	case h5oNullID: /*0x0000 Null                                */
		return &h5oMsgClass{id: id, name: "null"}, true
	case h5oSdspaceID: /*0x0001 Dataspace                           */
		return &h5oMsgClass{id: id, name: "dataspace", shareFlags: sharable,
			decode: shared(id, func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oSdspaceDecode(f, p) })}, true
	case h5oLinfoID: /*0x0002 Link information                    */
		return &h5oMsgClass{id: id, name: "linfo",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oLinfoDecode(f, p) }}, true
	case h5oDtypeID: /*0x0003 Datatype                            */
		return &h5oMsgClass{id: id, name: "datatype", shareFlags: sharable,
			decode: shared(id, func(f *File, oh *h5o, _ uint8, p *image) (any, error) { return h5oDtypeDecodeOh(f, oh, p) })}, true
	case h5oFillID: /*0x0004 Old data storage -- fill value      */
		return &h5oMsgClass{id: id, name: "fill", shareFlags: sharable,
			decode: shared(id, func(f *File, oh *h5o, _ uint8, p *image) (any, error) { return f.h5oFillOldDecode(oh, p) })}, true
	case h5oFillNewID: /*0x0005 New data storage -- fill value      */
		return &h5oMsgClass{id: id, name: "fill_new", shareFlags: sharable,
			decode: shared(id, func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oFillNewDecode(f, p) })}, true
	case h5oLinkID: /*0x0006 Link                                */
		return &h5oMsgClass{id: id, name: "link",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oLinkDecode(f, p) }}, true
	case h5oEflID: /*0x0007 Data storage -- external data files */
		return &h5oMsgClass{id: id, name: "external file list",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return f.h5oEflDecode(p) }}, true
	case h5oLayoutID: /*0x0008 Data Layout                         */
		return &h5oMsgClass{id: id, name: "layout",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return f.h5oLayoutDecode(p) }}, true
	case h5oBogusValidID: /*0x0009 "Bogus valid" (for testing)     */
		/* (only registered when the library is built with H5O_ENABLE_BOGUS) */
		return nil, false
	case h5oGinfoID: /*0x000A Group information               */
		return &h5oMsgClass{id: id, name: "ginfo",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oGinfoDecode(f, p) }}, true
	case h5oPlineID: /*0x000B Data storage -- filter pipeline */
		return &h5oMsgClass{id: id, name: "filter pipeline", shareFlags: sharable,
			decode: shared(id, func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oPlineDecode(f, p) })}, true
	case h5oAttrID: /*0x000C Attribute                       */
		return &h5oMsgClass{id: id, name: "attribute", shareFlags: h5oShareIsSharable,
			decode:      shared(id, func(f *File, oh *h5o, _ uint8, p *image) (any, error) { return f.h5oAttrDecode(oh, p) }),
			setCrtIndex: h5oAttrSetCrtIndex}, true
	case h5oNameID: /*0x000D Object name                     */
		return &h5oMsgClass{id: id, name: "name",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oNameDecode(f, p) }}, true
	case h5oMtimeID: /*0x000E Object modification date and time */
		return &h5oMsgClass{id: id, name: "mtime",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oMtimeDecode(f, p) }}, true
	case h5oShmesgID: /*0x000F File-wide shared message table  */
		return &h5oMsgClass{id: id, name: "shared message table",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oShmesgDecode(f, p) }}, true
	case h5oContID: /*0x0010 Object header continuation      */
		return &h5oMsgClass{id: id, name: "hdr continuation",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oContDecode(f, p) }}, true
	case h5oStabID: /*0x0011 Symbol table                    */
		return &h5oMsgClass{id: id, name: "stab",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oStabDecode(f, p) }}, true
	case h5oMtimeNewID: /*0x0012 New Object modification date and time */
		return &h5oMsgClass{id: id, name: "mtime_new",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oMtimeNewDecode(f, p) }}, true
	case h5oBtreekID: /*0x0013 Non-default v1 B-tree 'K' values */
		return &h5oMsgClass{id: id, name: "v1 B-tree 'K' values",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oBtreekDecode(f, p) }}, true
	case h5oDrvinfoID: /*0x0014 Driver info settings            */
		return &h5oMsgClass{id: id, name: "driver info",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oDrvinfoDecode(f, p) }}, true
	case h5oAinfoID: /*0x0015 Attribute information           */
		return &h5oMsgClass{id: id, name: "ainfo",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oAinfoDecode(f, p) }}, true
	case h5oRefcountID: /*0x0016 Object's ref. count             */
		return &h5oMsgClass{id: id, name: "refcount",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oRefcountDecode(f, p) }}, true
	case h5oFsinfoID: /*0x0017 Free-space manager info         */
		return &h5oMsgClass{id: id, name: "fsinfo",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return f.h5oFsinfoDecode(p) }}, true
	case h5oMdciMsgID: /*0x0018 Metadata cache image            */
		return &h5oMsgClass{id: id, name: "mdci",
			decode: func(f *File, _ *h5o, _ uint8, p *image) (any, error) { return h5oMdciDecode(f, p) }}, true
	case h5oUnknownID: /*0x0019 Placeholder for unknown message */
		/* (unknown messages are given their native form, the original
		 * message type ID, when the object header is decoded) */
		return &h5oMsgClass{id: id, name: "unknown"}, true
	}
	return nil, false
}
