package hdf5

// Translated from src/H5Opkg.h and the object header parts of
// src/H5Opublic.h / src/H5Oprivate.h / src/H5Fprivate.h: constants and the
// size macros used when decoding object headers.

/* Object header macros */
const (
	h5oNmesgs  = 8 /*initial number of messages	     */
	h5oNchunks = 2 /*initial number of chunks	     */
	/* Min. obj header data size (must be big enough for a message prefix and a continuation message) */
	h5oMinSize        = 22
	h5oMaxCrtOrderIdx = 65535 /* Max. creation order index value   */
	h5oMesgMaxSize    = 65536 /*max obj header message size	     */
)

/* Versions of object header structure */
const (
	/* Initial version of the object header format */
	h5oVersion1 = 1

	/* Revised version - leaves out reserved bytes and alignment padding, and adds
	 *      magic number as prefix and checksum as suffix for all chunks.
	 */
	h5oVersion2 = 2

	/* The latest version of the format. */
	h5oVersionLatest = h5oVersion2
)

/* Size of checksum (on disk) */
const h5oSizeofChksum = 4

/* Default values for some of the object creation properties */
const (
	h5oCrtAttrMaxCompactDef = 8
	h5oCrtAttrMinDenseDef   = 6
	h5oCrtOhdrFlagsDef      = h5oHdrStoreTimes
)

/* Object header status flag definitions (H5Opublic.h) */
const (
	h5oHdrChunk0Size           = 0x03 /* 2-bit field indicating # of bytes to store the size of chunk 0's data */
	h5oHdrAttrCrtOrderTracked  = 0x04 /* Attribute creation order is tracked */
	h5oHdrAttrCrtOrderIndexed  = 0x08 /* Attribute creation order has index */
	h5oHdrAttrStorePhaseChange = 0x10 /* Non-default attribute storage phase change values stored */
	h5oHdrStoreTimes           = 0x20 /* Store access, modification, change & birth times for object */
	h5oHdrAllFlags             = h5oHdrChunk0Size | h5oHdrAttrCrtOrderTracked | h5oHdrAttrCrtOrderIndexed | h5oHdrAttrStorePhaseChange | h5oHdrStoreTimes
	h5oHdrChunk0Size1          = 0x00 /* Use 1-byte value for chunk #0 size */
	h5oHdrChunk0Size2          = 0x01 /* Use 2-byte value for chunk #0 size */
	h5oHdrChunk0Size4          = 0x02 /* Use 4-byte value for chunk #0 size */
	h5oHdrChunk0Size8          = 0x03 /* Use 8-byte value for chunk #0 size */
)

// H5O_HDR_MAGIC / H5O_CHK_MAGIC (H5Fprivate.h).
const (
	h5oHdrMagic = "OHDR" /* Header */
	h5oChkMagic = "OCHK" /* Continuation chunk */
)

/* Set the object header size to speculatively read in */
/* (needs to be more than the object header prefix size to work at all and
 *      should be larger than the largest object type's default object header
 *      size to save the extra I/O operations) */
const h5oSpecReadSize = 512

/* Flags for a message class's "sharability" */
const (
	h5oShareIsSharable = 0x01
	h5oShareInOhdr     = 0x02
)

// h5oAlignOld is H5O_ALIGN_OLD(X).
func h5oAlignOld(x uint64) uint64 { return 8 * ((x + 7) / 8) }

// h5oAlignVers is H5O_ALIGN_VERS(V, X).
func h5oAlignVers(v uint8, x uint64) uint64 {
	if v == h5oVersion1 {
		return h5oAlignOld(x)
	}
	return x
}

// h5oAlignOh is H5O_ALIGN_OH(O, X).
func h5oAlignOh(oh *h5o, x uint64) uint64 { return h5oAlignVers(oh.version, x) }

// h5oSizeofHdr is H5O_SIZEOF_HDR(O): the size of the object header prefix.
func h5oSizeofHdr(oh *h5o) uint64 {
	if oh.version == h5oVersion1 {
		return h5oAlignOld(1 + /*version number	*/
			1 + /*reserved 		*/
			2 + /*number of messages	*/
			4 + /*reference count	*/
			4) /*chunk data size	*/
	}
	n := uint64(h5SizeofMagic + /*magic number  	*/
		1 + /*version number 	*/
		1) /*flags		 	*/
	if oh.flags&h5oHdrStoreTimes != 0 {
		n += 4 + /*access time		*/
			4 + /*modification time	*/
			4 + /*change time		*/
			4 /*birth time		*/
	}
	if oh.flags&h5oHdrAttrStorePhaseChange != 0 {
		n += 2 + /*max compact attributes */
			2 /*min dense attributes	*/
	}
	n += 1 << (oh.flags & h5oHdrChunk0Size) /*chunk 0 data size */
	n += h5oSizeofChksum                    /*checksum size	*/
	return n
}

// h5oSizeofMsghdrVers is H5O_SIZEOF_MSGHDR_VERS(V, C): the size of an
// object header message prefix.
func h5oSizeofMsghdrVers(v uint8, c bool) uint64 {
	if v == h5oVersion1 {
		return h5oAlignOld(2 + /*message type		*/
			2 + /*sizeof message data	*/
			1 + /*flags              	*/
			3) /*reserved		*/
	}
	n := uint64(1 + /*message type		*/
		2 + /*sizeof message data	*/
		1) /*flags              	*/
	if c {
		n += 2 /*creation index     	*/
	}
	return n
}

// h5oSizeofMsghdrOh is H5O_SIZEOF_MSGHDR_OH(O).
func h5oSizeofMsghdrOh(oh *h5o) uint64 {
	return h5oSizeofMsghdrVers(oh.version, oh.flags&h5oHdrAttrCrtOrderTracked != 0)
}

// h5oSizeofChkhdrVers is H5O_SIZEOF_CHKHDR_VERS(V): the size of the chunk
// "header" (magic number and checksum) for each chunk.
func h5oSizeofChkhdrVers(v uint8) uint64 {
	if v == h5oVersion1 {
		return 0 + /*no magic #  */
			0 /*no checksum */
	}
	return h5SizeofMagic + /*magic #  */
		h5oSizeofChksum /*checksum */
}

// h5oSizeofChkhdrOh is H5O_SIZEOF_CHKHDR_OH(O).
func h5oSizeofChkhdrOh(oh *h5o) uint64 { return h5oSizeofChkhdrVers(oh.version) }

// h5oSizeofChksumVers is H5O_SIZEOF_CHKSUM_VERS(V): the size of the checksum
// for each chunk.
func h5oSizeofChksumVers(v uint8) uint64 {
	if v == h5oVersion1 {
		return 0 /*no checksum */
	}
	return h5oSizeofChksum /*checksum */
}

// h5oSizeofChksumOh is H5O_SIZEOF_CHKSUM_OH(O).
func h5oSizeofChksumOh(oh *h5o) uint64 { return h5oSizeofChksumVers(oh.version) }

// errDecodeOverflow is the error of the H5_IS_BUFFER_OVERFLOW checks in the
// message decoders: HGOTO_ERROR(H5E_OHDR, H5E_OVERFLOW, ..., "ran off end
// of input buffer while decoding").
func errDecodeOverflow() error {
	return errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
}
