package hdf5

// Translated from src/H5Oint.c (read side only): loading object headers
// (H5O_protect), object classes, and object/header information. The
// "isa" callbacks of the object classes (H5O__group_isa in src/H5Goh.c,
// H5O__dset_isa in src/H5Doh.c and H5O__dtype_isa in src/H5Toh.c) are
// translated here too, as they are only used by H5O__obj_class_real.

import "github.com/crhntr/hdf5/go/h5"

// h5oObjClass is H5O_obj_class_t (the parts used for reading).
type h5oObjClass struct {
	typ  h5.ObjectType /*object type on disk	     */
	name string        /*for debugging		     */
	isa  func(f *File, oh *h5o) (bool, error)
}

// h5oObjClassG is H5O_obj_class_g: the object classes, from the most
// general to the most specific (any object that has a datatype message is a
// datatype but only some of them are datasets).
func h5oObjClassG() []h5oObjClass {
	return []h5oObjClass{
		{h5.ObjectNamedDatatype, "named datatype", h5oDtypeIsa}, /* Datatype object (H5O_TYPE_NAMED_DATATYPE - 2) */
		{h5.ObjectDataset, "dataset", h5oDsetIsa},               /* Dataset object (H5O_TYPE_DATASET - 1) */
		{h5.ObjectGroup, "group", h5oGroupIsa},                  /* Group object (H5O_TYPE_GROUP - 0) */
	}
}

// oProtect is H5O_protect: load (through the cache) the object header at
// addr, including continuation chunks. Messages are decoded lazily by
// msgRead.
func (f *File) oProtect(addr haddr) (*h5o, error) {
	/* Check for valid address */
	if !addrDefined(addr) {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "address undefined")
	}

	v, err := f.cached("H5O", addr, func() (any, error) {
		/* Construct the user data for protect callback */
		contMsgInfo := &h5oContMsgs{}
		udata := &h5oCacheUd{
			common: h5oCommonCacheUd{f: f, contMsgInfo: contMsgInfo, addr: addr},
		}

		/* Lock the object header into the cache */
		oh, err := f.h5oCacheLoad(udata)
		if err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to load object header")
		}

		/* Check if there are any continuation messages to process */
		if len(contMsgInfo.msgs) > 0 {
			/* Construct the user data for protecting chunks */
			chkUdata := &h5oChkCacheUd{
				decoding: true,
				oh:       oh,
				chunkno:  -1, /* Set to invalid value, for better error detection */
				common:   h5oCommonCacheUd{f: f, contMsgInfo: contMsgInfo},
			}

			/* (The metadata cache holds one entry per address: a
			 * continuation message naming the address of the object
			 * header itself finds an entry of the wrong type, and one
			 * naming a chunk loaded already gets that chunk back without
			 * adding to the header; chunks that overlap at different
			 * addresses are separate entries. loaded stands for the cache
			 * entries of this header's chunks. chunkBytes bounds the work
			 * done on corrupt files with overlapping chunks, which is not
			 * in the C code: the chunks of a header that does not overlap
			 * itself fit in the file.) */
			loaded := map[haddr]bool{}
			chunkBytes := oh.chunks[0].size

			/* Read in continuation messages, until there are no more */
			/* (Note that loading chunks could increase the # of continuation
			 *      messages if new ones are found - QAK, 19/11/2016)
			 */
			for currMsg := 0; currMsg < len(contMsgInfo.msgs); currMsg++ {
				cont := contMsgInfo.msgs[currMsg]
				chkcnt := len(oh.chunks)

				if !addrDefined(cont.addr) {
					return nil, errorf(H5E_OHDR, H5E_CANTPROTECT, "unable to load object header chunk: undefined address")
				}
				if cont.addr == oh.chunks[0].addr {
					return nil, errorf(H5E_OHDR, H5E_CANTPROTECT, "unable to load object header chunk: incorrect cache entry type")
				}
				if loaded[cont.addr] {
					continue
				}
				if cont.size > f.eof()-chunkBytes {
					return nil, errorf(H5E_OHDR, H5E_CANTPROTECT, "unable to load object header chunk: the chunks of the object header at %d are larger than the file", addr)
				}
				chunkBytes += cont.size

				/* Bring the chunk into the cache */
				/* (which adds to the object header) */
				chkUdata.common.addr = cont.addr
				chkUdata.size = cont.size
				if err := f.h5oCacheChkLoad(chkUdata); err != nil {
					return nil, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to load object header chunk")
				}
				loaded[cont.addr] = true

				/* Sanity check */
				if chkUdata.chunkno != chkcnt || len(oh.chunks) != chkcnt+1 {
					return nil, errorf(H5E_OHDR, H5E_CANTPROTECT, "object header chunk numbering mismatch")
				}
			}
		}

		/* (H5_STRICT_FORMAT_CHECKS is not enabled by default: an incorrect #
		 * of messages in a v1 object header is not an error.) */
		return oh, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*h5o), nil
}

// oObjType is H5O_obj_type: the type of the object whose header is at addr.
func (f *File) oObjType(addr haddr) (h5.ObjectType, error) {
	/* Load the object header */
	oh, err := f.oProtect(addr)
	if err != nil {
		return h5.ObjectUnknown, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to load object header")
	}

	/* Retrieve the type of the object */
	return f.oObjTypeReal(oh), nil
}

// oObjTypeReal is H5O__obj_type_real: the type of the object whose header
// is oh (h5.ObjectUnknown when no class matches).
func (f *File) oObjTypeReal(oh *h5o) h5.ObjectType {
	/* Look up class for object header */
	objClass, err := f.oObjClassReal(oh)
	if err != nil {
		/* Set type to "unknown" */
		return h5.ObjectUnknown
	}
	/* Set object type */
	return objClass.typ
}

// oObjClass is H5O__obj_class: the class of the object whose header is at
// addr.
func (f *File) oObjClass(addr haddr) (*h5oObjClass, error) {
	/* Load the object header */
	oh, err := f.oProtect(addr)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to load object header")
	}

	/* Test whether entry qualifies as a particular type of object */
	c, err := f.oObjClassReal(oh)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTGET, "unable to determine object type")
	}
	return c, nil
}

// oObjClassReal is H5O__obj_class_real: the class of the object whose
// header is oh.
func (f *File) oObjClassReal(oh *h5o) (*h5oObjClass, error) {
	classes := h5oObjClassG()

	/* Test whether entry qualifies as a particular type of object */
	/* (Note: loop is in reverse order, to test specific objects first) */
	for i := len(classes); i > 0; i-- {
		isa, err := classes[i-1].isa(f, oh)
		if err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTINIT, "unable to determine object type")
		}
		if isa {
			return &classes[i-1], nil
		}
	}
	return nil, errorf(H5E_OHDR, H5E_CANTINIT, "unable to determine object type")
}

// h5oGroupIsa is H5O__group_isa (src/H5Goh.c): an object is a group if its
// header has a symbol table or a link info message.
func h5oGroupIsa(f *File, oh *h5o) (bool, error) {
	/* Check for any of the messages that indicate a group */
	stabExists := f.msgExists(oh, h5oStabID)
	linfoExists := f.msgExists(oh, h5oLinfoID)
	return stabExists || linfoExists, nil
}

// h5oDsetIsa is H5O__dset_isa (src/H5Doh.c): an object is a dataset if its
// header has a datatype and a dataspace message.
func h5oDsetIsa(f *File, oh *h5o) (bool, error) {
	/* Datatype */
	if !f.msgExists(oh, h5oDtypeID) {
		return false, nil
	}

	/* Layout */
	if !f.msgExists(oh, h5oSdspaceID) {
		return false, nil
	}
	return true, nil
}

// h5oDtypeIsa is H5O__dtype_isa (src/H5Toh.c): an object is a named
// datatype if its header has a datatype message.
func h5oDtypeIsa(f *File, oh *h5o) (bool, error) {
	return f.msgExists(oh, h5oDtypeID), nil
}

// h5oHdrInfo is H5O_hdr_info_t: information about an object header.
type h5oHdrInfo struct {
	version uint8 /* Version number of header format in file */
	nmesgs  int   /* Number of object header messages */
	nchunks int   /* Number of object header chunks */
	flags   uint8 /* Object header flags */
	space   struct {
		total uint64 /* Total space for storing object header in file */
		meta  uint64 /* Space within header for object header metadata information */
		mesg  uint64 /* Space within header for actual message information */
		free  uint64 /* Free space within object header */
	}
	mesg struct {
		present uint64 /* Flags to indicate presence of message type in header */
		shared  uint64 /* Flags to indicate message type is shared in header */
	}
}

// h5oGetHdrInfoReal is H5O__get_hdr_info_real.
func h5oGetHdrInfoReal(oh *h5o) h5oHdrInfo {
	var hdr h5oHdrInfo

	/* Set the version for the object header */
	hdr.version = oh.version

	/* Set the number of messages & chunks */
	hdr.nmesgs = len(oh.mesgs)
	hdr.nchunks = len(oh.chunks)

	/* Set the status flags */
	hdr.flags = oh.flags

	/* Iterate over all the messages, accumulating message size & type information */
	nchunks := uint64(len(oh.chunks))
	if nchunks > 0 {
		nchunks--
	}
	hdr.space.meta = h5oSizeofHdr(oh) + h5oSizeofChkhdrOh(oh)*nchunks
	for _, currMsg := range oh.mesgs {
		/* Accumulate space usage information, based on the type of message */
		switch currMsg.typeID {
		case h5oNullID:
			hdr.space.free += h5oSizeofMsghdrOh(oh) + uint64(len(currMsg.raw))
		case h5oContID:
			hdr.space.meta += h5oSizeofMsghdrOh(oh) + uint64(len(currMsg.raw))
		default:
			hdr.space.meta += h5oSizeofMsghdrOh(oh)
			hdr.space.mesg += uint64(len(currMsg.raw))
		}

		/* Set flag to indicate presence of message type */
		typeFlag := uint64(1) << currMsg.typeID
		hdr.mesg.present |= typeFlag

		/* Set flag if the message is shared in some way */
		if currMsg.flags&h5oMsgFlagShared != 0 {
			hdr.mesg.shared |= typeFlag
		}
	}

	/* Iterate over all the chunks, adding any gaps to the free space */
	for _, currChunk := range oh.chunks {
		/* Accumulate the size of the header on disk */
		hdr.space.total += currChunk.size

		/* If the chunk has a gap, add it to the free space */
		hdr.space.free += currChunk.gap
	}
	return hdr
}

// oGetInfo is H5O_get_info (with all fields requested) for an object
// header that has already been loaded: the data model information for the
// object.
func (f *File) oGetInfo(oh *h5o) (h5.ObjectInfo, error) {
	/* Get class for object */
	objClass, err := f.oObjClassReal(oh)
	if err != nil {
		return h5.ObjectInfo{}, wrapf(err, H5E_OHDR, H5E_CANTGET, "unable to determine object class")
	}

	/* Reset the object info structure */
	var oinfo h5.ObjectInfo

	/* Get basic information */
	/* Set the object's address into the token */
	oinfo.Address = oh.addr

	/* Retrieve the type of the object */
	oinfo.Type = objClass.typ

	/* Set the object's reference count */
	oinfo.RefCount = oh.nlink
	oinfo.HeaderVersion = oh.version

	/* Get time information */
	if oh.version > h5oVersion1 {
		oinfo.ATime = int64(oh.atime)
		oinfo.MTime = int64(oh.mtime)
		oinfo.CTime = int64(oh.ctime)
		oinfo.BTime = int64(oh.btime)
	} else {
		/* No information for access & modification fields */
		/* (we stopped updating the "modification time" header message for
		 *      raw data changes, so the "modification time" header message
		 *      is closest to the 'change time', in POSIX terms - QAK)
		 */
		oinfo.ATime = 0
		oinfo.MTime = 0
		oinfo.BTime = 0

		/* Might be information for modification time */
		if f.msgExists(oh, h5oMtimeID) {
			/* Get "old style" modification time info */
			v, err := f.msgRead(oh, h5oMtimeID)
			if err != nil {
				return h5.ObjectInfo{}, wrapf(err, H5E_OHDR, H5E_CANTGET, "can't read MTIME message")
			}
			oinfo.CTime = int64(v.(h5oMtime))
		} else if f.msgExists(oh, h5oMtimeNewID) {
			/* Check for "new style" modification time info */
			/* Get "new style" modification time info */
			v, err := f.msgRead(oh, h5oMtimeNewID)
			if err != nil {
				return h5.ObjectInfo{}, wrapf(err, H5E_OHDR, H5E_CANTGET, "can't read MTIME_NEW message")
			}
			oinfo.CTime = int64(v.(h5oMtime))
		} else {
			oinfo.CTime = 0
		}
	}

	/* Retrieve # of attributes */
	nattrs, err := f.oAttrCount(oh)
	if err != nil {
		return h5.ObjectInfo{}, wrapf(err, H5E_OHDR, H5E_CANTGET, "can't retrieve attribute count")
	}
	oinfo.NumAttrs = nattrs
	return oinfo, nil
}

// oGetNlinks is H5O_get_nlinks: the number of link messages in the object
// header at addr (the C code counts them while loading the header, in
// link_msgs_seen).
func (f *File) oGetNlinks(addr haddr) (uint64, error) {
	/* Get the object header */
	oh, err := f.oProtect(addr)
	if err != nil {
		return 0, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to load object header")
	}

	/* Retrieve the # of link messages seen when the object header was loaded */
	return uint64(f.msgCount(oh, h5oLinkID)), nil
}

// h5oGetOhAddr is H5O_get_oh_addr.
func h5oGetOhAddr(oh *h5o) haddr { return oh.chunks[0].addr }

// oGetRcAndType is H5O_get_rc_and_type: the reference count and type of
// the object whose header is at addr.
func (f *File) oGetRcAndType(addr haddr) (uint32, h5.ObjectType, error) {
	/* Get the object header */
	oh, err := f.oProtect(addr)
	if err != nil {
		return 0, h5.ObjectUnknown, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to load object header")
	}

	/* Set the object's reference count */
	/* Retrieve the type of the object */
	return oh.nlink, f.oObjTypeReal(oh), nil
}
