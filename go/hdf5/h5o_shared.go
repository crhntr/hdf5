package hdf5

// Translated from src/H5Oshared.c (read side only) and the
// H5O_SHARED_DECODE template of src/H5Oshared.h.

/* Old version, with full symbol table entry as link for object header sharing */
const h5oSharedVersion1 = 1

/* New version, with just address of object as link for object header sharing */
const h5oSharedVersion2 = 2

/* Newest version, which recognizes messages that are stored in the SOHM heap */
const (
	h5oSharedVersion3      = 3
	h5oSharedVersionLatest = h5oSharedVersion3
)

// h5oFheapIDLen is H5O_FHEAP_ID_LEN: the size of a fractal heap ID for
// shared messages (H5O_fheap_id_t).
const h5oFheapIDLen = 8

// h5oSharedRead is H5O__shared_read: read a message referred to by a shared
// message. openOh is the object header being decoded (may be nil).
//
// A committed message must resolve to an unshared message: the C library
// never writes a shared message into the header of a committed object, and
// refusing chains of committed references bounds the recursion on corrupt
// files whose shared messages refer to each other.
func (f *File) h5oSharedRead(openOh *h5o, shared *h5oShared, typeID uint16) (any, error) {
	var (
		ret any
		err error
	)

	/* This message could have a heap ID (SOHM) or the address of an object
	 * header on disk (named datatype)
	 */
	switch shared.typ {
	case h5oShareTypeSohm:
		/* Check for implicit shared object header message */
		/* (retrieving the message from the shared message heap, opening
		 * the fractal heap and decoding the message is done by the shared
		 * message module) */
		if ret, err = f.smReadMessage(shared, typeID); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTLOAD, "can't read message from fractal heap.")
		}
	case h5oShareTypeCommitted:
		/* Build the object location for the shared message's object header */
		oh := openOh
		if openOh == nil || shared.oh != h5oGetOhAddr(openOh) {
			/* The shared message is in another object header */
			if oh, err = f.oProtect(shared.oh); err != nil {
				return nil, wrapf(err, H5E_OHDR, H5E_READERROR, "unable to read message")
			}
		}
		/* else: The shared message is in the already opened object header.
		 * This is possible, for example, if an attribute's datatype is
		 * shared in the same object header the attribute is in.  Read the
		 * message directly. */

		/* (refuse chains of committed references, see above) */
		for _, m := range oh.mesgs {
			if m.typeID == typeID {
				if m.flags&h5oMsgFlagShared != 0 {
					return nil, errorf(H5E_OHDR, H5E_READERROR, "committed message at %d refers to another shared message", shared.oh)
				}
				break
			}
		}
		if ret, err = f.msgRead(oh, typeID); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_READERROR, "unable to read message")
		}
	default:
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "invalid shared message type %d", shared.typ)
	}

	/* Mark the message as shared */
	return h5oMsgSetShare(typeID, shared, ret), nil
}

// h5oMsgSetShare is H5O_msg_set_share (src/H5Omessage.c) for the classes
// whose native form records sharing information: only attributes keep it
// (h5oAttr.shared). The native value is copied, never modified in place.
func h5oMsgSetShare(typeID uint16, share *h5oShared, native any) any {
	if attr, ok := native.(*h5oAttr); ok && typeID == h5oAttrID {
		a := *attr
		sh := *share
		a.shared = &sh
		return &a
	}
	return native
}

// h5oSharedDecodeHeader is the part of H5O__shared_decode that decodes the
// shared message header (version, type, and heap ID or object header
// address) into an h5oShared.
func h5oSharedDecodeHeader(f *File, p *image, typeID uint16) (*h5oShared, error) {
	overflow := func() error {
		return errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	shMesg := &h5oShared{}

	/* Version */
	if p.overflow(1) {
		return nil, overflow()
	}
	version := p.uint8()
	if version < h5oSharedVersion1 || version > h5oSharedVersionLatest {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for shared object message")
	}

	/* Get the shared information type
	 * Flags are unused before version 3.
	 */
	if p.overflow(1) {
		return nil, overflow()
	}
	if version >= h5oSharedVersion2 {
		shMesg.typ = p.uint8()
	} else {
		shMesg.typ = h5oShareTypeCommitted
		p.skip(1)
	}

	/* Skip reserved bytes (for version 1) */
	if version == h5oSharedVersion1 {
		if p.overflow(6) {
			return nil, overflow()
		}
		p.skip(6)
	}

	/* Body */
	if version == h5oSharedVersion1 {
		/* Decode stored "symbol table entry" into message location */
		if p.overflow(int(f.sizeofSize)) {
			return nil, overflow()
		}
		p.skip(int(f.sizeofSize)) /* Skip over local heap address */
		if p.overflow(int(f.sizeofAddr)) {
			return nil, overflow()
		}
		shMesg.oh = p.addr()
	} else if version >= h5oSharedVersion2 {
		/* If this message is in the heap, copy a heap ID.
		 * Otherwise, it is a named datatype, so copy an H5O_loc_t.
		 */
		if shMesg.typ == h5oShareTypeSohm {
			if version < h5oSharedVersion3 {
				return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "shared message heap ID in shared message version %d", version)
			}
			if p.overflow(h5oFheapIDLen) {
				return nil, overflow()
			}
			shMesg.heapID = append([]byte(nil), p.bytes(h5oFheapIDLen)...)
			shMesg.oh = haddrUndef
		} else {
			/* The H5O_COMMITTED_FLAG should be set if this message
			 * is from an older version before the flag existed.
			 */
			if version < h5oSharedVersion3 {
				shMesg.typ = h5oShareTypeCommitted
			}
			if p.overflow(int(f.sizeofAddr)) {
				return nil, overflow()
			}
			shMesg.oh = p.addr()
		}
	}

	/* Set file pointer & message type for all types of shared messages */
	shMesg.msgTypeID = typeID
	return shMesg, nil
}

// h5oSharedDecode is H5O__shared_decode: decode a shared object message
// and retrieve the actual message it refers to.
func (f *File) h5oSharedDecode(openOh *h5o, p *image, typeID uint16) (any, error) {
	shMesg, err := h5oSharedDecodeHeader(f, p, typeID)
	if err != nil {
		return nil, err
	}

	/* Retrieve actual message, through decoded shared message info */
	ret, err := f.h5oSharedRead(openOh, shMesg, typeID)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_READERROR, "unable to retrieve native message")
	}
	return ret, nil
}

// h5oSharedDecodeTmpl is the H5O_SHARED_DECODE template (src/H5Oshared.h):
// decode an object header message that may be shared, either through the
// shared message interface or directly with decodeReal.
func (f *File) h5oSharedDecodeTmpl(openOh *h5o, mesgFlags uint8, p *image, typeID uint16,
	decodeReal func(f *File, oh *h5o, mesgFlags uint8, p *image) (any, error),
) (any, error) {
	/* Check for shared message */
	if mesgFlags&h5oMsgFlagShared != 0 {
		/* Retrieve native message info indirectly through shared message */
		ret, err := f.h5oSharedDecode(openOh, p, typeID)
		if err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "unable to decode shared message")
		}
		return ret, nil
	}

	/* Decode native message directly */
	ret, err := decodeReal(f, openOh, mesgFlags, p)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "unable to decode native message")
	}
	return ret, nil
}
