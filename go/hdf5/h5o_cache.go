package hdf5

// Translated from src/H5Ocache.c (read side only): the metadata cache
// callbacks for object headers (H5AC_OHDR) and object header continuation
// chunks (H5AC_OHDR_CHK). The serialize/notify/free callbacks are not
// translated.

import "sort"

// h5oContMsgs is H5O_cont_msgs_t: the continuation messages found while
// decoding an object header, still to be loaded.
type h5oContMsgs struct {
	msgs []h5oContEnt
}

// h5oContEnt is the H5O_cont_t entry stored in H5O_cont_msgs_t (address,
// size and the chunk number the continuation block becomes).
type h5oContEnt struct {
	addr    haddr
	size    uint64
	chunkno int
}

// h5oCommonCacheUd is H5O_common_cache_ud_t.
type h5oCommonCacheUd struct {
	f           *File        /* Pointer to file for object header/chunk */
	contMsgInfo *h5oContMsgs /* Pointer to continuation messages to work on */
	addr        haddr        /* Address of the object header or chunk */
}

// h5oCacheUd is H5O_cache_ud_t: user data for loading the object header
// prefix and first chunk.
type h5oCacheUd struct {
	madeAttempt bool   /* Whether the deserialize routine was already attempted */
	v1PfxNmesgs uint16 /* Number of messages from v1 prefix header */
	chunk0Size  uint64 /* Size of serialized first chunk */
	oh          *h5o   /* Partially deserialized object header, for later use */
	ohVersion   uint8  /* Version of the object header */
	common      h5oCommonCacheUd
}

// h5oChkCacheUd is H5O_chk_cache_ud_t: user data for loading a
// continuation chunk.
type h5oChkCacheUd struct {
	decoding bool   /* Whether the object header is being decoded */
	oh       *h5o   /* Object header for this chunk */
	chunkno  int    /* Index of the chunk */
	size     uint64 /* Size of the chunk in the file */
	common   h5oCommonCacheUd
}

// h5oCacheGetInitialLoadSize is H5O__cache_get_initial_load_size: tell the
// metadata cache how much data to read from file in the first speculative
// read for the object header.
func h5oCacheGetInitialLoadSize() uint64 {
	/* Set the image length size */
	return h5oSpecReadSize
}

// h5oCacheGetFinalLoadSize is H5O__cache_get_final_load_size: tell the
// metadata cache the final size of an object header.
func (f *File) h5oCacheGetFinalLoadSize(image []byte, udata *h5oCacheUd) (uint64, error) {
	/* Deserialize the object header prefix */
	if err := f.h5oPrefixDeserialize(image, udata); err != nil {
		return 0, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "can't deserialize object header prefix")
	}

	/* Set the final size for the cache image */
	actualLen := udata.chunk0Size + h5oSizeofHdr(udata.oh)
	if actualLen < udata.chunk0Size {
		return 0, errorf(H5E_OHDR, H5E_OVERFLOW, "object header chunk 0 size overflows")
	}

	/* Save the oh version to be used later in verify_chksum callback
	   because oh will be freed before leaving this routine */
	udata.ohVersion = udata.oh.version

	/* Free allocated memory: fix github issue #3970 */
	udata.oh = nil
	return actualLen, nil
}

// h5oCacheVerifyChksum is H5O__cache_verify_chksum: verify the computed
// checksum of the data structure is the same as the stored chksum.
func h5oCacheVerifyChksum(image []byte, udata *h5oCacheUd) bool {
	/* There is no checksum for version 1 */
	if udata.ohVersion != h5oVersion1 {
		/* Get stored and computed checksums */
		stored, computed, ok := h5fGetChecksums(image)
		if !ok || stored != computed {
			return false
		}
	}
	return true
}

// h5fGetChecksums is H5F_get_checksums: the checksum stored in the last four
// bytes of a metadata image and the one computed over the rest.
func h5fGetChecksums(image []byte) (stored, computed uint32, ok bool) {
	if len(image) < h5oSizeofChksum {
		return 0, 0, false
	}
	n := len(image) - h5oSizeofChksum
	return le32(image[n:]), checksumMetadata(image[:n], 0), true
}

// h5oCacheDeserialize is H5O__cache_deserialize: deserialize the object
// header contained in the supplied buffer (the complete prefix and first
// chunk, as sized by h5oCacheGetFinalLoadSize).
func (f *File) h5oCacheDeserialize(image []byte, udata *h5oCacheUd) (*h5o, error) {
	if err := f.h5oPrefixDeserialize(image, udata); err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "can't deserialize object header prefix")
	}
	oh := udata.oh

	/* Parse the first chunk */
	if err := f.h5oChunkDeserialize(oh, udata.common.addr, udata.chunk0Size, image, &udata.common); err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTINIT, "can't deserialize first object header chunk")
	}

	/* Check for corruption in object header # of messages */
	if oh.version == h5oVersion1 && int(udata.v1PfxNmesgs) < len(oh.mesgs) {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad object header message count")
	}

	/* Note that we've loaded the object header from the file */
	udata.madeAttempt = true
	return oh, nil
}

// h5oCacheLoad is the H5C__load_entry sequence for the H5AC_OHDR class
// (H5O__cache_get_initial_load_size, a speculative read clipped to the end
// of the file, H5O__cache_get_final_load_size, a second read when the final
// size differs, H5O__cache_verify_chksum and H5O__cache_deserialize).
func (f *File) h5oCacheLoad(udata *h5oCacheUd) (*h5o, error) {
	addr := udata.common.addr
	eof := f.eof()
	if addr >= eof {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "object header address %d beyond end of file (%d)", addr, eof)
	}

	/* Speculative read of the prefix (clipped to the end of the file) */
	n := h5oCacheGetInitialLoadSize()
	if addr+n > eof {
		n = eof - addr
	}
	image, err := f.readAt(addr, int(n))
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_READERROR, "unable to read object header")
	}

	/* Get the final load size */
	actualLen, err := f.h5oCacheGetFinalLoadSize(image, udata)
	if err != nil {
		return nil, err
	}
	if actualLen > eof-addr {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "object header size %d extends beyond end of file", actualLen)
	}
	if actualLen != n {
		if image, err = f.readAt(addr, int(actualLen)); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_READERROR, "unable to read object header")
		}
	}

	/* Verify the checksum */
	if f.opts.verifyChecksums && !h5oCacheVerifyChksum(image, udata) {
		return nil, errorf(H5E_OHDR, H5E_BADCHECKSUM, "incorrect metadata checksum after all read attempts (object header at %d)", addr)
	}

	return f.h5oCacheDeserialize(image, udata)
}

// h5oCacheChkGetInitialLoadSize is H5O__cache_chk_get_initial_load_size:
// the size of a continuation chunk comes from its continuation message.
func h5oCacheChkGetInitialLoadSize(udata *h5oChkCacheUd) uint64 {
	/* Set the image length size */
	return udata.size
}

// h5oCacheChkVerifyChksum is H5O__cache_chk_verify_chksum.
func h5oCacheChkVerifyChksum(image []byte, udata *h5oChkCacheUd) bool {
	/* There is no checksum for version 1 */
	if udata.oh.version != h5oVersion1 {
		/* Get stored and computed checksums */
		stored, computed, ok := h5fGetChecksums(image)
		if !ok || stored != computed {
			return false
		}
	}
	return true
}

// h5oCacheChkDeserialize is H5O__cache_chk_deserialize: deserialize an
// object header continuation chunk into udata.oh.
func (f *File) h5oCacheChkDeserialize(image []byte, udata *h5oChkCacheUd) error {
	/* Check if we are still decoding the object header */
	/* (as opposed to bringing a piece of it back from the file) */
	if udata.decoding {
		/* Parse the chunk */
		if err := f.h5oChunkDeserialize(udata.oh, udata.common.addr, udata.size, image, &udata.common); err != nil {
			return wrapf(err, H5E_OHDR, H5E_CANTINIT, "can't deserialize object header chunk")
		}

		/* Set the chunk number for the chunk proxy */
		udata.chunkno = len(udata.oh.chunks) - 1
	}
	return nil
}

// h5oCacheChkLoad is the H5C__load_entry sequence for the H5AC_OHDR_CHK
// class: read the chunk, verify its checksum and deserialize it.
func (f *File) h5oCacheChkLoad(udata *h5oChkCacheUd) error {
	addr := udata.common.addr
	n := h5oCacheChkGetInitialLoadSize(udata)
	if eof := f.eof(); addr >= eof || n > eof-addr {
		return errorf(H5E_OHDR, H5E_CANTLOAD, "object header chunk at %d (size %d) extends beyond end of file (%d)", addr, n, eof)
	}
	image, err := f.readAt(addr, int(n))
	if err != nil {
		return wrapf(err, H5E_OHDR, H5E_READERROR, "unable to read object header chunk")
	}
	if f.opts.verifyChecksums && !h5oCacheChkVerifyChksum(image, udata) {
		return errorf(H5E_OHDR, H5E_BADCHECKSUM, "incorrect metadata checksum after all read attempts (object header chunk at %d)", addr)
	}
	return f.h5oCacheChkDeserialize(image, udata)
}

// h5oAddContMsg is H5O__add_cont_msg: add information from a continuation
// message to the list of continuation messages in the object header.
func h5oAddContMsg(contMsgInfo *h5oContMsgs, cont *h5oCont, chunkno int) {
	/* Init the continuation message info */
	contMsgInfo.msgs = append(contMsgInfo.msgs, h5oContEnt{addr: cont.addr, size: cont.size, chunkno: chunkno})
}

// h5oPrefixDeserialize is H5O__prefix_deserialize: deserialize an object
// header prefix into a new udata.oh.
func (f *File) h5oPrefixDeserialize(image []byte, udata *h5oCacheUd) error {
	p := f.newImage(image)
	overflow := func() error {
		return errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Allocate space for the new object header data structure */
	oh := &h5o{addr: udata.common.addr}

	/* Check for presence of magic number */
	/* (indicates version 2 or later) */
	if p.overflow(h5SizeofMagic) {
		return overflow()
	}
	if string(image[:h5SizeofMagic]) == h5oHdrMagic {
		/* Magic number (bounds checked above) */
		p.skip(h5SizeofMagic)

		/* Version */
		if p.overflow(1) {
			return overflow()
		}
		oh.version = p.uint8()
		if h5oVersion2 != oh.version {
			return errorf(H5E_OHDR, H5E_VERSION, "bad object header version number")
		}

		/* Flags */
		if p.overflow(1) {
			return overflow()
		}
		oh.flags = p.uint8()
		if oh.flags&^h5oHdrAllFlags != 0 {
			return errorf(H5E_OHDR, H5E_BADVALUE, "unknown object header status flag(s)")
		}

		/* Number of links to object (unless overridden by refcount message) */
		oh.nlink = 1

		/* Time fields */
		if oh.flags&h5oHdrStoreTimes != 0 {
			if p.overflow(4 + 4 + 4 + 4) {
				return overflow()
			}
			oh.atime = p.uint32()
			oh.mtime = p.uint32()
			oh.ctime = p.uint32()
			oh.btime = p.uint32()
			oh.hasTimes = true
		} else {
			oh.atime, oh.mtime, oh.ctime, oh.btime = 0, 0, 0, 0
		}

		/* Attribute fields */
		if oh.flags&h5oHdrAttrStorePhaseChange != 0 {
			if p.overflow(2 + 2) {
				return overflow()
			}
			oh.maxCompact = p.uint16()
			oh.minDense = p.uint16()
			if oh.maxCompact < oh.minDense {
				return errorf(H5E_OHDR, H5E_BADVALUE, "bad object header attribute phase change values")
			}
			oh.hasAttrPhase = true
		} else {
			oh.maxCompact = h5oCrtAttrMaxCompactDef
			oh.minDense = h5oCrtAttrMinDenseDef
		}

		/* First chunk size */
		switch oh.flags & h5oHdrChunk0Size {
		case 0: /* 1 byte size */
			if p.overflow(1) {
				return overflow()
			}
			udata.chunk0Size = uint64(p.uint8())
		case 1: /* 2 byte size */
			if p.overflow(2) {
				return overflow()
			}
			udata.chunk0Size = uint64(p.uint16())
		case 2: /* 4 byte size */
			if p.overflow(4) {
				return overflow()
			}
			udata.chunk0Size = uint64(p.uint32())
		case 3: /* 8 byte size */
			if p.overflow(8) {
				return overflow()
			}
			udata.chunk0Size = p.uint64()
		default:
			return errorf(H5E_OHDR, H5E_BADVALUE, "bad size for chunk 0")
		}
		if udata.chunk0Size > 0 && udata.chunk0Size < h5oSizeofMsghdrOh(oh) {
			return errorf(H5E_OHDR, H5E_BADVALUE, "bad object header chunk size")
		}
	} else {
		/* Version */
		if p.overflow(1) {
			return overflow()
		}
		oh.version = p.uint8()
		if h5oVersion1 != oh.version {
			return errorf(H5E_OHDR, H5E_VERSION, "bad object header version number")
		}

		/* Flags */
		oh.flags = h5oCrtOhdrFlagsDef

		/* Reserved */
		if p.overflow(1) {
			return overflow()
		}
		p.skip(1)

		/* Number of messages */
		if p.overflow(2) {
			return overflow()
		}
		udata.v1PfxNmesgs = p.uint16()

		/* Link count */
		if p.overflow(4) {
			return overflow()
		}
		oh.nlink = p.uint32()

		/* Reset unused time fields */
		oh.atime, oh.mtime, oh.ctime, oh.btime = 0, 0, 0, 0

		/* Reset unused attribute fields */
		oh.maxCompact = 0
		oh.minDense = 0

		/* First chunk size */
		if p.overflow(4) {
			return overflow()
		}
		udata.chunk0Size = uint64(p.uint32())
		if (udata.v1PfxNmesgs > 0 && udata.chunk0Size < h5oSizeofMsghdrOh(oh)) ||
			(udata.v1PfxNmesgs == 0 && udata.chunk0Size > 0) {
			return errorf(H5E_OHDR, H5E_BADVALUE, "bad object header chunk size")
		}

		/* Reserved, in version 1 (for 8-byte alignment padding) */
		if p.overflow(4) {
			return overflow()
		}
		p.skip(4)
	}

	/* Verify object header prefix length */
	if uint64(p.off) != h5oSizeofHdr(oh)-h5oSizeofChksumOh(oh) {
		return errorf(H5E_OHDR, H5E_BADVALUE, "bad object header prefix length")
	}

	/* Save the object header for later use in 'deserialize' callback */
	udata.oh = oh
	return nil
}

// h5oChunkDeserialize is H5O__chunk_deserialize: deserialize a chunk of an
// object header, appending its messages to oh.mesgs and the continuation
// messages it holds to udata.contMsgInfo.
//
// The file is always open read-only here, so the C code paths that depend
// on H5F_ACC_RDWR (merging adjacent null messages, marking messages with
// the "mark if unknown" flag) never apply.
func (f *File) h5oChunkDeserialize(oh *h5o, addr haddr, chunkSize uint64, image []byte, udata *h5oCommonCacheUd) error {
	overflow := func() error {
		return errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Init the chunk data info */
	chunkno := len(oh.chunks)
	chunk := h5oChunk{addr: addr}
	if chunkno == 0 {
		/* First chunk's 'image' includes room for the object header prefix */
		chunk.size = chunkSize + h5oSizeofHdr(oh)
	} else {
		chunk.size = chunkSize
	}

	/* Copy disk image into chunk's image */
	if uint64(len(image)) < chunk.size {
		return errorf(H5E_OHDR, H5E_CANTCOPY, "attempted to copy too many disk image bytes into buffer")
	}
	chunkImage := image[:chunk.size]

	/* Point into chunk image to decode */
	p := f.newImage(chunkImage)

	/* Skip over [already decoded] prefix in special case of chunk 0 */
	if chunkno == 0 {
		skip := int(h5oSizeofHdr(oh) - h5oSizeofChksumOh(oh))
		if p.overflow(skip) {
			return overflow()
		}
		p.skip(skip)
	} else if chunkno > 0 && oh.version > h5oVersion1 {
		/* Check for magic # on chunks > 0 in later versions of the format */
		/* Magic number */
		if p.overflow(h5SizeofMagic) {
			return overflow()
		}
		if string(p.bytes(h5SizeofMagic)) != h5oChkMagic {
			return errorf(H5E_OHDR, H5E_CANTLOAD, "wrong object header chunk signature")
		}
	}

	/* Decode messages from this chunk */
	if chunk.size < h5oSizeofChksumOh(oh) {
		return overflow()
	}
	eom := int(chunk.size - h5oSizeofChksumOh(oh))
	nullcnt := 0

	for p.off < eom {
		var (
			mesgSize int    /* Size of message read in */
			id       uint16 /* ID (type) of current message */
			flags    uint8  /* Flags for current message */
			crtIdx   uint16 /* Creation index for current message */
		)

		/* Decode message prefix info */

		/* Version # */
		if oh.version == h5oVersion1 {
			if p.overflow(2) {
				return overflow()
			}
			id = p.uint16()
		} else {
			if p.overflow(1) {
				return overflow()
			}
			id = uint16(p.uint8())
		}

		/* Message size */
		if p.overflow(2) {
			return overflow()
		}
		mesgSize = int(p.uint16())
		if uint64(mesgSize) != h5oAlignOh(oh, uint64(mesgSize)) {
			return errorf(H5E_OHDR, H5E_CANTLOAD, "message not aligned")
		}

		/* Message flags */
		if p.overflow(1) {
			return overflow()
		}
		flags = p.uint8()
		if flags&^h5oMsgFlagBits != 0 {
			return errorf(H5E_OHDR, H5E_CANTLOAD, "unknown flag for message")
		}
		if flags&h5oMsgFlagShared != 0 && flags&h5oMsgFlagDontshare != 0 {
			return errorf(H5E_OHDR, H5E_CANTLOAD, "bad flag combination for message")
		}
		if flags&h5oMsgFlagWasUnknown != 0 && flags&h5oMsgFlagFailIfUnknown != 0 {
			return errorf(H5E_OHDR, H5E_CANTLOAD, "bad flag combination for message")
		}
		if flags&h5oMsgFlagWasUnknown != 0 && flags&h5oMsgFlagMarkIfUnknown == 0 {
			return errorf(H5E_OHDR, H5E_CANTLOAD, "bad flag combination for message")
		}
		/* Delay checking the "shareable" flag until we've made sure id
		 * references a valid message class that this version of the library
		 * knows about */

		/* Reserved bytes/creation index */
		if oh.version == h5oVersion1 {
			/* Reserved bytes */
			if p.overflow(3) {
				return overflow()
			}
			p.skip(3)
		} else {
			/* Only decode creation index if they are being tracked */
			if oh.flags&h5oHdrAttrCrtOrderTracked != 0 {
				if p.overflow(2) {
					return overflow()
				}
				crtIdx = p.uint16()
			}
		}

		/* Try to detect invalidly formatted object header message that
		 *  extends past end of chunk.
		 */
		if p.off+mesgSize > eom {
			return errorf(H5E_OHDR, H5E_CANTINIT, "corrupt object header")
		}

		/* Increment count of null messages */
		if h5oNullID == id {
			nullcnt++
		}

		/* (Combining adjacent 'null' messages only happens when the file is
		 * open for writing.) */
		{
			/* Initialize information about message */
			mesg := &h5oMesg{
				flags:   flags,
				crtIdx:  crtIdx,
				raw:     chunkImage[p.off : p.off+mesgSize],
				rawOff:  addr + uint64(p.off),
				chunkno: chunkno,
			}

			/* Point unknown messages at 'unknown' message class */
			/* (Usually from future versions of the library) */
			cls, known := h5oMsgClassG(id)
			if id >= h5oUnknownID || !known {
				/* Save the original message type ID */
				/* Save 'native' form of unknown message */
				mesg.native = h5oUnknown(id)

				/* Set message to "unknown" class */
				mesg.typeID = h5oUnknownID

				/* Check for "fail if unknown" message flags */
				/* (H5O_MSG_FLAG_FAIL_IF_UNKNOWN_AND_OPEN_FOR_WRITE only applies
				 * to files open for writing; marking with
				 * H5O_MSG_FLAG_WAS_UNKNOWN also requires write access) */
				if flags&h5oMsgFlagFailIfUnknownAlways != 0 {
					return errorf(H5E_OHDR, H5E_BADMESG, "unknown message with 'fail if unknown' flag found")
				}
			} else {
				/* Check for message of unshareable class marked as "shareable"
				 */
				if flags&h5oMsgFlagShareable != 0 && cls.shareFlags&h5oShareIsSharable == 0 {
					return errorf(H5E_OHDR, H5E_CANTLOAD, "message of unshareable class flagged as shareable")
				}

				/* Set message class for "known" messages */
				mesg.typeID = id
			}

			/* Do some inspection/interpretation of new messages from this chunk */
			/* (detect continuation messages, ref. count messages, etc.) */

			/* Check if message is a continuation message */
			if h5oContID == id {
				/* Decode continuation message */
				cont, err := h5oContDecode(f, f.newImage(mesg.raw))
				if err != nil {
					return wrapf(err, H5E_OHDR, H5E_BADMESG, "bad continuation message found")
				}

				/* Save 'native' form of continuation message */
				mesg.native = cont

				/* Add to continuation messages left to interpret */
				/* (the next continuation message/chunk) */
				h5oAddContMsg(udata.contMsgInfo, cont, len(udata.contMsgInfo.msgs)+1)
			} else if h5oRefcountID == id {
				/* Check if message is a ref. count message */

				/* Decode ref. count message */
				if oh.version <= h5oVersion1 {
					return errorf(H5E_OHDR, H5E_VERSION, "object header version does not support reference count message")
				}
				refcount, err := h5oRefcountDecode(f, f.newImage(mesg.raw))
				if err != nil {
					return wrapf(err, H5E_OHDR, H5E_CANTSET, "can't decode refcount")
				}

				/* Save 'native' form of ref. count message */
				mesg.native = refcount

				/* Set object header values */
				oh.nlink = uint32(refcount)
			}
			/* (The C code also counts link and attribute messages here
			 * (link_msgs_seen / attr_msgs_seen); h5oGetNlinks counts them on
			 * demand instead.) */

			oh.mesgs = append(oh.mesgs, mesg)
		}

		/* Advance decode pointer past message */
		p.skip(mesgSize)

		/* Check for 'gap' at end of chunk */
		if gap := eom - p.off; gap > 0 && uint64(gap) < h5oSizeofMsghdrOh(oh) {
			/* Gaps can only occur in later versions of the format */
			if oh.version == h5oVersion1 {
				return errorf(H5E_OHDR, H5E_BADMESG, "gap found in early version of file format")
			}

			/* Gaps should only occur in chunks with no null messages */
			if nullcnt != 0 {
				return errorf(H5E_OHDR, H5E_BADMESG, "gap in chunk with no null messages")
			}

			/* Set gap information for chunk */
			chunk.gap = uint64(gap)

			/* Increment location in chunk */
			p.skip(gap)
		}
	}

	/* Check for correct checksum on chunks, in later versions of the format */
	if oh.version > h5oVersion1 {
		/* checksum verification already done in verify_chksum cb */

		/* Metadata checksum */
		if p.overflow(4) {
			return overflow()
		}
		_ = p.uint32()
	}

	/* Size check */
	if uint64(p.off) != chunk.size {
		return errorf(H5E_OHDR, H5E_OVERFLOW, "object header image size mismatch")
	}

	oh.chunks = append(oh.chunks, chunk)
	return nil
}

// h5oChunkOverlaps reports whether the byte range [addr, addr+size) overlaps
// one of the (sorted, non-overlapping) chunk ranges in spans, and returns
// the index where it should be inserted. Valid object headers never have
// overlapping chunks; the check bounds the work done on corrupt files whose
// continuation messages point back into already-loaded chunks.
func h5oChunkOverlaps(spans [][2]uint64, addr, size uint64) (int, bool) {
	end := addr + size
	if size == 0 {
		end = addr + 1
	}
	i := sort.Search(len(spans), func(i int) bool { return spans[i][1] > addr })
	if i < len(spans) && spans[i][0] < end {
		return i, true
	}
	return i, false
}
