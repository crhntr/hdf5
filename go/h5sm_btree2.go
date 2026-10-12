package hdf5

// Translated from src/H5SMbtree2.c (read side only): the v2 B-tree record
// class for shared object header message indexes (H5SM_INDEX, B-tree type
// 7), its callback context, plus the record callbacks it uses from
// src/H5SMmessage.c (H5SM__compare_cb, H5SM__compare_iter_op,
// H5SM__message_compare, H5SM__message_decode) and the record types of
// src/H5SMpkg.h.
//
// Usage: open the index with the context built from the file,
//
//	bt2, err := f.b2Open(index.indexAddr, h5smIndex(), h5smBt2CrtContext(f))
//
// The records are *h5smSohm values. h5smMessageDecode is also the decoder of
// the records of a list index (H5SM__cache_list_deserialize). To search,
// pass a *h5smMesgKey as udata.

import "bytes"

// H5SM_storage_loc_t: where a message is stored.
type h5smStorageLoc int8

const (
	h5smNoLoc  h5smStorageLoc = -1
	h5smInHeap h5smStorageLoc = 0 /* Message is stored in the heap */
	h5smInOh   h5smStorageLoc = 1 /* Message is stored in an object header */
)

// h5oMesgLoc is H5O_mesg_loc_t (src/H5Oprivate.h): a message's location in
// an object header.
type h5oMesgLoc struct {
	index  uint32 /* index within object header */
	ohAddr haddr  /* address of object header */
}

// h5smHeapLoc is H5SM_heap_loc_t: a record's location if it's stored in the
// heap.
type h5smHeapLoc struct {
	refCount uint64              /* Number of times this message is used in the file */
	fheapID  [h5oFheapIDLen]byte /* ID of the OHM in the fractal heap */
}

// h5smSohm is H5SM_sohm_t: a SOHM index node (record). The C union u is
// split into mesgLoc (location == h5smInOh) and heapLoc (location ==
// h5smInHeap).
type h5smSohm struct {
	location  h5smStorageLoc /* Type of message location */
	hash      uint32         /* Hash value for encoded OHM */
	msgTypeID uint32         /* Message's type ID */
	mesgLoc   h5oMesgLoc     /* Location of message in object header */
	heapLoc   h5smHeapLoc    /* Heap ID for message in SOHM heap */
}

// h5smMesgKey is H5SM_mesg_key_t: used to search lists and B-trees for a
// certain message.
type h5smMesgKey struct {
	file     *File    /* File in which sharing is happening */
	fheap    *h5hf    /* The heap for this message type, open. */
	encoding []byte   /* The message encoded, or NULL */
	message  h5smSohm /* The message to find/insert.
	 * If the message doesn't yet have a
	 * heap ID, the heap ID will be 0. */
}

// h5smBt2Ctx is H5SM_bt2_ctx_t: v2 B-tree client callback context.
type h5smBt2Ctx struct {
	sizeofAddr uint8 /* Size of file addresses */
}

// h5smCompareUdata is H5SM_compare_udata_t (src/H5SMmessage.c): udata
// struct for calls to H5SM__compare_cb and H5SM__compare_iter_op.
type h5smCompareUdata struct {
	key *h5smMesgKey /* Key; compare this against stored message */
	idx uint32       /* Index of the message in the OH, if applicable */
	ret int          /* Return value; set this to result of memcmp */
}

// h5smIndex is H5SM_INDEX: v2 B-tree class for SOHM indexes.
func h5smIndex() *h5b2Class {
	return &h5b2Class{
		id:      h5b2SohmIndexID,       /* Type of B-tree */
		compare: h5smMessageCompareBt2, /* Record comparison callback */
		decode:  h5smMessageDecodeBt2,  /* Record decoding callback */
	}
}

// h5smBt2CrtContext is H5SM__bt2_crt_context: create client callback
// context.
func h5smBt2CrtContext(f *File) *h5smBt2Ctx {
	/* Determine the size of addresses & lengths in the file */
	return &h5smBt2Ctx{sizeofAddr: f.sizeofAddr}
}

// h5smCompareCb is H5SM__compare_cb (src/H5SMmessage.c): callback for
// H5HF_op, used in H5SM__message_compare below. Determines whether the
// search key passed in in udata is equal to obj or not.
//
// Passes back the result in udata.ret.
func h5smCompareCb(obj []byte, udata *h5smCompareUdata) error {
	/* If the encoding sizes are different, it's not the same object */
	if len(udata.key.encoding) > len(obj) {
		udata.ret = 1
	} else if len(udata.key.encoding) < len(obj) {
		udata.ret = -1
	} else {
		/* Sizes are the same.  Return result of memcmp */
		udata.ret = bytes.Compare(udata.key.encoding, obj)
	}
	return nil
}

// h5smCompareIterOp is H5SM__compare_iter_op (src/H5SMmessage.c): OH
// iteration callback to compare a key against a message in an OH.
//
// Returns stop=true if this is the message we're searching for (with the
// memcmp result returned in udata).
func h5smCompareIterOp(oh *h5o, mesg *h5oMesg, sequence uint32, udata *h5smCompareUdata) (stop bool, err error) {
	/* Check the creation index for this message */
	if sequence == udata.idx {
		/* (H5O_ALIGN_OH(oh, udata->key->encoding_size)) */
		alignedEncodedSize := uint64(len(udata.key.encoding))
		if oh.version == 1 {
			alignedEncodedSize = h5AlignOld(alignedEncodedSize)
		}

		/* Sanity check the message's length */
		if len(mesg.raw) == 0 {
			return false, errorf(H5E_SOHM, H5E_BADVALUE, "shared message has no raw data")
		}

		if alignedEncodedSize > uint64(len(mesg.raw)) {
			udata.ret = 1
		} else if alignedEncodedSize < uint64(len(mesg.raw)) {
			udata.ret = -1
		} else {
			udata.ret = bytes.Compare(udata.key.encoding, mesg.raw[:len(udata.key.encoding)])
		}

		/* Indicate that we found the message we were looking for */
		return true, nil
	}
	return false, nil
}

// h5smMessageCompare is H5SM__message_compare (src/H5SMmessage.c):
// determine whether the search key rec1 represents a shared message that is
// equal to rec2 or not, and if not, whether rec1 is "greater than" or "less
// than" rec2.
func h5smMessageCompare(key *h5smMesgKey, mesg *h5smSohm) (int, error) {
	/* If the key has an fheap ID, we're looking for a message that's
	 * already in the index; if the fheap ID matches, we've found the message
	 * and can stop immediately.
	 * Likewise, if the message has an OH location that is matched by the
	 * message in the index, we've found the message.
	 */
	if mesg.location == h5smInHeap && key.message.location == h5smInHeap {
		if key.message.heapLoc.fheapID == mesg.heapLoc.fheapID {
			return 0, nil
		}
	} else if mesg.location == h5smInOh && key.message.location == h5smInOh {
		if key.message.mesgLoc.ohAddr == mesg.mesgLoc.ohAddr &&
			key.message.mesgLoc.index == mesg.mesgLoc.index &&
			key.message.msgTypeID == mesg.msgTypeID {
			return 0, nil
		}
	}

	/* Compare hash values */
	if key.message.hash > mesg.hash {
		return 1, nil
	} else if key.message.hash < mesg.hash {
		return -1, nil
	}

	/* If the hash values match, make sure the messages are really the same */
	/* Hash values match; compare the encoded message with the one in
	 * the index.
	 */
	if len(key.encoding) == 0 {
		return 0, errorf(H5E_SOHM, H5E_CANTCOMPARE, "shared message search key has no encoding")
	}

	/* Set up user data for callback */
	udata := h5smCompareUdata{key: key}

	/* Compare the encoded message with either the message in the heap or
	 * the message in an object header.
	 */
	if mesg.location == h5smInHeap {
		if key.fheap == nil {
			return 0, errorf(H5E_HEAP, H5E_CANTCOMPARE, "no fractal heap for shared messages")
		}

		/* Call heap op routine with comparison callback */
		if err := key.fheap.op(mesg.heapLoc.fheapID[:], func(obj []byte) error { return h5smCompareCb(obj, &udata) }); err != nil {
			return 0, wrapf(err, H5E_HEAP, H5E_CANTCOMPARE, "can't compare btree2 records")
		}
	} else {
		/* Sanity checks */
		if key.file == nil || mesg.location != h5smInOh {
			return 0, errorf(H5E_SOHM, H5E_CANTCOMPARE, "invalid shared message location")
		}

		/* Set up object location */
		oh, err := key.file.oProtect(mesg.mesgLoc.ohAddr)
		if err != nil {
			return 0, wrapf(err, H5E_SYM, H5E_NOTFOUND, "error iterating over links")
		}

		/* Finish setting up user data for iterator */
		udata.idx = mesg.mesgLoc.index

		/* Locate the right message and compare with it */
		/* (H5O_msg_iterate over the messages of type msg_type_id, with
		 *  'sequence' counting the messages of that type) */
		var sequence uint32
		for _, m := range oh.mesgs {
			if uint32(m.typeID) != mesg.msgTypeID {
				continue
			}
			stop, err := h5smCompareIterOp(oh, m, sequence, &udata)
			if err != nil {
				return 0, wrapf(err, H5E_SYM, H5E_NOTFOUND, "error iterating over links")
			}
			if stop {
				break
			}
			sequence++
		}
	}

	return udata.ret, nil
}

// h5smMessageCompareBt2 adapts H5SM__message_compare to the h5b2Class
// compare signature: rec1 is a *h5smMesgKey, rec2 a *h5smSohm.
func h5smMessageCompareBt2(f *File, ctx any, rec1 any, rec2 any) (int, error) {
	key, ok1 := rec1.(*h5smMesgKey)
	mesg, ok2 := rec2.(*h5smSohm)
	if !ok1 || !ok2 {
		return 0, errorf(H5E_SOHM, H5E_BADTYPE, "unexpected shared message record types %T, %T", rec1, rec2)
	}
	return h5smMessageCompare(key, mesg)
}

// h5smMessageDecode is H5SM__message_decode (src/H5SMmessage.c): read an
// encoded SOHM message from p into an h5smSohm struct.
func h5smMessageDecode(p *image, ctx *h5smBt2Ctx) (*h5smSohm, error) {
	message := &h5smSohm{}

	message.location = h5smStorageLoc(int8(p.uint8()))
	message.hash = p.uint32()

	if message.location == h5smInHeap {
		message.heapLoc.refCount = uint64(p.uint32())
		copy(message.heapLoc.fheapID[:], p.bytes(h5oFheapIDLen))
	} else {
		if message.location != h5smInOh {
			return nil, errorf(H5E_SOHM, H5E_BADVALUE, "invalid shared message location %d", message.location)
		}

		p.skip(1) /* reserved */
		message.msgTypeID = uint32(p.uint8())
		message.mesgLoc.index = uint32(p.uint16())
		message.mesgLoc.ohAddr = p.addrLen(int(ctx.sizeofAddr))
	}
	if err := p.done(); err != nil {
		return nil, err
	}
	return message, nil
}

// h5smMessageDecodeBt2 adapts H5SM__message_decode to the h5b2Class decode
// signature (ctx is a *h5smBt2Ctx; one is built from f when it is nil).
func h5smMessageDecodeBt2(f *File, ctx any, p *image) (any, error) {
	c, ok := ctx.(*h5smBt2Ctx)
	if !ok || c == nil {
		c = h5smBt2CrtContext(f)
	}
	return h5smMessageDecode(p, c)
}
