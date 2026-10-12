package hdf5

// Translated from src/H5Omessage.c (read side only), plus the
// H5O_LOAD_NATIVE macro from src/H5Opkg.h.

import "errors"

// h5oLoadNative is the H5O_LOAD_NATIVE macro: decode mesg (resolving shared
// messages) if it has not been decoded yet, and memoise the result in
// mesg.native.
//
// The decoded value is stored under f.mu so that concurrent readers of the
// same cached header do not race; two goroutines may decode the same message
// at the same time, in which case the first stored value wins.
func (f *File) h5oLoadNative(oh *h5o, mesg *h5oMesg) (any, error) {
	f.mu.Lock()
	native := mesg.native
	f.mu.Unlock()
	if native != nil {
		return native, nil
	}

	msgType, ok := h5oMsgClassG(mesg.typeID)
	if !ok {
		return nil, errorf(H5E_OHDR, H5E_BADTYPE, "invalid message type %d", mesg.typeID)
	}

	/* Decode the message */
	if msgType.decode == nil {
		/* (the null message class has no decode callback) */
		return nil, nil
	}
	native, err := msgType.decode(f, oh, mesg.flags, f.newImage(mesg.raw))
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "unable to decode message")
	}
	if native == nil {
		return nil, errorf(H5E_OHDR, H5E_CANTDECODE, "unable to decode message")
	}

	/* Set the message's "shared info", if it's shareable */
	if mesg.flags&h5oMsgFlagShareable != 0 {
		native = h5oUpdateShared(native, h5oShareTypeHere, msgType.id, mesg.crtIdx, h5oGetOhAddr(oh))
	}

	/* Set the message's "creation index", if it has one */
	if msgType.setCrtIndex != nil {
		/* Set the creation index for the message */
		native = msgType.setCrtIndex(native, mesg.crtIdx)
	}

	f.mu.Lock()
	if mesg.native == nil {
		mesg.native = native
	} else {
		native = mesg.native
	}
	f.mu.Unlock()
	return native, nil
}

// h5oUpdateShared is the H5O_UPDATE_SHARED macro applied to a decoded
// message: record where a shareable message is stored. Only attribute
// messages carry the shared information in their native form (h5oAttr);
// for the other shareable classes the native value is returned unchanged.
// The native value is copied, never modified in place, since it may be a
// cached value.
func h5oUpdateShared(native any, typ uint8, msgTypeID uint16, crtIdx uint16, ohAddr haddr) any {
	if attr, ok := native.(*h5oAttr); ok {
		a := *attr
		a.shared = &h5oShared{typ: typ, oh: ohAddr, msgTypeID: msgTypeID}
		_ = crtIdx /* (u.loc.index: not represented in h5oShared) */
		return &a
	}
	return native
}

// msgRead is H5O_msg_read_oh: decode (resolving shared messages) and return
// the first message of the given type as one of the h5o* structs in
// h5o_types.go (see h5oMsgClassG for the Go type of each message class).
// Returns ErrNotFound when the header has no such message. The returned
// value is shared with the cached header and must not be modified.
func (f *File) msgRead(oh *h5o, typeID uint16) (any, error) {
	/* check args */
	if typeID >= h5oMsgTypes {
		return nil, errorf(H5E_OHDR, H5E_BADTYPE, "invalid message type %d", typeID)
	}

	/* Scan through the messages looking for the right one */
	idx := -1
	for i, m := range oh.mesgs {
		if m.typeID == typeID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errorf(H5E_OHDR, H5E_NOTFOUND, "message type not found")
	}

	/*
	 * Decode the message if necessary.  If the message is shared then retrieve
	 * native message through the shared interface.
	 */
	native, err := f.h5oLoadNative(oh, oh.mesgs[idx])
	if err != nil {
		return nil, err
	}
	return native, nil
}

// msgCount is H5O_msg_count / H5O__msg_count_real: the number of messages
// of the given type in the header.
func (f *File) msgCount(oh *h5o, typeID uint16) int {
	/* Loop over all messages, counting the ones of the type looked for */
	n := 0
	for _, m := range oh.mesgs {
		if m.typeID == typeID {
			n++
		}
	}
	return n
}

// msgExists is H5O_msg_exists_oh: whether a message of the given type
// exists in the header, without decoding it.
func (f *File) msgExists(oh *h5o, typeID uint16) bool {
	/* Scan through the messages looking for the right one */
	for _, m := range oh.mesgs {
		if m.typeID == typeID {
			return true
		}
	}
	return false
}

// msgIterate is H5O_msg_iterate / H5O__msg_iterate_real: decode and visit
// every message of the given type, in header order. The operator may return
// errBIterStop to stop early without error.
func (f *File) msgIterate(oh *h5o, typeID uint16, op func(msg *h5oMesg, native any) error) error {
	if typeID >= h5oMsgTypes {
		return errorf(H5E_OHDR, H5E_BADTYPE, "invalid message type %d", typeID)
	}

	/* Iterate over messages */
	for _, idxMsg := range oh.mesgs {
		if idxMsg.typeID != typeID {
			continue
		}
		/* Decode the message if necessary.  */
		native, err := f.h5oLoadNative(oh, idxMsg)
		if err != nil {
			return err
		}

		/* Check for iterator callback indicating to get out of loop */
		if err := op(idxMsg, native); err != nil {
			if errors.Is(err, errBIterStop) {
				return nil
			}
			return err
		}
	}
	return nil
}

// msgDecode is H5O_msg_decode: decode a raw message of the given type that
// is not stored in an object header (e.g. a message read from the shared
// message heap). openOh may be nil.
func (f *File) msgDecode(openOh *h5o, typeID uint16, buf []byte) (any, error) {
	/* check args */
	msgType, ok := h5oMsgClassG(typeID)
	if !ok || msgType.decode == nil {
		return nil, errorf(H5E_OHDR, H5E_BADTYPE, "invalid message type %d", typeID)
	}

	/* decode */
	native, err := msgType.decode(f, openOh, 0, f.newImage(buf))
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "unable to decode message")
	}
	return native, nil
}

// msgGetFlags is H5O_msg_get_flags: the flags of the first message of the
// given type.
func (f *File) msgGetFlags(oh *h5o, typeID uint16) (uint8, error) {
	/* Locate message of correct type */
	for _, m := range oh.mesgs {
		if m.typeID == typeID {
			/* Set return value */
			return m.flags, nil
		}
	}
	return 0, errorf(H5E_OHDR, H5E_NOTFOUND, "message type not found")
}
