package hdf5

// Translated from src/H5SM.c (read side only): shared object header
// messages. The package types and size macros come from src/H5SMpkg.h; the
// record decoding lives in h5sm_btree2.go (src/H5SMbtree2.c,
// src/H5SMmessage.c) and the table / list loading in h5sm_cache.go
// (src/H5SMcache.c).
//
// The shared message table message (H5O_SHMESG_ID) of the superblock
// extension is decoded by superExtRead into f.sohm; the master table it
// points to is loaded lazily, through the metadata cache (f.cached), the
// first time a shared message is read.

/* Maximum number of shared message indexes (H5O_SHMESG_MAX_NINDEXES in src/H5Opublic.h) */
const h5oShmesgMaxNindexes = 8

/* Flags for the shared message indexes (H5O_SHMESG_*_FLAG in src/H5Opublic.h) */
const (
	h5oShmesgNoneFlag    = 0x0000            /* No shared messages */
	h5oShmesgSdspaceFlag = 1 << h5oSdspaceID /* Simple Dataspace Message.  */
	h5oShmesgDtypeFlag   = 1 << h5oDtypeID   /* Datatype Message.  */
	h5oShmesgFillFlag    = 1 << h5oFillNewID /* Fill Value Message. */
	h5oShmesgPlineFlag   = 1 << h5oPlineID   /* Filter pipeline message.  */
	h5oShmesgAttrFlag    = 1 << h5oAttrID    /* Attribute Message.  */
	h5oShmesgAllFlag     = h5oShmesgSdspaceFlag | h5oShmesgDtypeFlag | h5oShmesgFillFlag | h5oShmesgPlineFlag | h5oShmesgAttrFlag
)

/****************************/
/* Package Macros (H5SMpkg.h) */
/****************************/

/* Size of checksum information (on disk) */
const h5smSizeofChecksum = 4

/* Version of Shared Object Header Message List Indexes */
const h5smListVersion = 0

// h5smHeapLocSize is H5SM_HEAP_LOC_SIZE.
const h5smHeapLocSize = 4 /* Reference count */ +
	h5oFheapIDLen /* size of heap ID on disk */

// h5smOhLocSize is H5SM_OH_LOC_SIZE(f).
func h5smOhLocSize(f *File) uint64 {
	return 1 /* reserved (possible flags?) */ +
		1 /* message type ID */ +
		2 /* creation index of message in OH */ +
		uint64(f.sizeofAddr) /* address of OH */
}

// h5smSohmEntrySize is H5SM_SOHM_ENTRY_SIZE(f).
func h5smSohmEntrySize(f *File) uint64 {
	return 1 /* Message location */ +
		4 /* Hash value */ +
		max(h5smHeapLocSize, h5smOhLocSize(f)) /* Entry */
}

// h5smIndexHeaderSize is H5SM_INDEX_HEADER_SIZE(f).
func h5smIndexHeaderSize(f *File) uint64 {
	return 1 /* Whether index is a list or B-tree */ +
		1 /* Version of index format */ +
		2 /* Type of messages stored in the index */ +
		4 /* Minimum size of messages to share */ +
		(3 * 2) /* B-tree cutoff, list cutoff, # of shared messages */ +
		uint64(f.sizeofAddr) /* Location of list or B-tree */ +
		uint64(f.sizeofAddr) /* Address of heap */
}

/* Format overhead for all SOHM tree metadata in the file (H5SM_METADATA_PREFIX_SIZE) */
const h5smMetadataPrefixSize = h5SizeofMagic /* Signature */ +
	h5smSizeofChecksum /* Checksum */

// h5smTableSize is H5SM_TABLE_SIZE(f).
func h5smTableSize(f *File) uint64 {
	var nindexes uint64
	if sohm := f.sohmTable(); sohm != nil {
		nindexes = uint64(sohm.nindexes)
	}
	return /* General metadata fields */ h5smMetadataPrefixSize +
		/* Indices */ nindexes*h5smIndexHeaderSize(f)
}

// h5smListSize is H5SM_LIST_SIZE(f, num_mesg).
func h5smListSize(f *File, numMesg uint64) uint64 {
	return /* General metadata fields */ h5smMetadataPrefixSize +
		/* Message entries */ h5smSohmEntrySize(f)*numMesg
}

/****************************/
/* Package Typedefs (H5SMpkg.h) */
/****************************/

// h5smIndexType is H5SM_index_type_t: types of message indices.
type h5smIndexType int8

const (
	h5smIndexBadtype h5smIndexType = -1
	h5smIndexList    h5smIndexType = 0 /* Index is an unsorted list (H5SM_LIST) */
	h5smIndexBtree   h5smIndexType = 1 /* Index is a sorted B-tree (H5SM_BTREE) */
)

// h5smIndexHeader is H5SM_index_header_t: a SOHM index header.
type h5smIndexHeader struct {
	/* Stored */
	mesgTypes   uint32        /* Bit flag vector of message types */
	minMesgSize uint64        /* number of messages being tracked */
	listMax     uint64        /* >= this many messages, index with a B-tree */
	btreeMin    uint64        /* <= this many messages, index with a list again */
	numMessages uint64        /* number of messages being tracked */
	indexType   h5smIndexType /* Is the index a list or a B-tree? */
	indexAddr   haddr         /* Address of the actual index (list or B-tree) */
	heapAddr    haddr         /* Address of the fheap used to store shared messages */

	/* Not stored */
	listSize uint64 /* Size of list index on disk */
}

// h5smList is H5SM_list_t: a SOHM list index.
type h5smList struct {
	header   *h5smIndexHeader /* Pointer to the corresponding index header */
	messages []h5smSohm       /* Actual list, stored as an array */
}

// h5smMasterTable is H5SM_master_table_t: the SOHM master table.
type h5smMasterTable struct {
	tableSize  uint64            /* Size of table on disk */
	numIndexes uint32            /* Number of indexes */
	indexes    []h5smIndexHeader /* Array of num_indexes indexes */
}

// h5smReadUdata is H5SM_read_udata_t (src/H5SM.c): data exchange structure
// for the H5SM__read_iter_op and H5SM__read_mesg_fh_cb callbacks.
type h5smReadUdata struct {
	file        *File  /* File in which sharing is happening (in) */
	idx         uint32 /* index of message in OH, if applicable (in) */
	encodingBuf []byte /* The encoded message (out) */
}

// sohmTable returns the shared message table message decoded from the
// superblock extension (H5F_SOHM_ADDR / H5F_SOHM_VERS / H5F_SOHM_NINDEXES),
// or nil when the file has none.
func (f *File) sohmTable() *h5oShmesgTable {
	t, _ := f.sohm.(*h5oShmesgTable)
	return t
}

// smTableProtect is H5AC_protect(f, H5AC_SOHM_TABLE, H5F_SOHM_ADDR(f), ...):
// load (through the metadata cache) the SOHM master table.
func (f *File) smTableProtect() (*h5smMasterTable, error) {
	sohm := f.sohmTable()
	if sohm == nil || !addrDefined(sohm.addr) {
		return nil, errorf(H5E_SOHM, H5E_BADVALUE, "file has no shared message table")
	}
	v, err := f.cached("H5SM_TABLE", sohm.addr, func() (any, error) {
		return h5smCacheTableLoad(f, sohm.addr)
	})
	if err != nil {
		return nil, wrapf(err, H5E_SOHM, H5E_CANTPROTECT, "unable to load SOHM master table")
	}
	table, ok := v.(*h5smMasterTable)
	if !ok {
		return nil, errorf(H5E_SOHM, H5E_CANTPROTECT, "cached object at address %d is not a SOHM master table", sohm.addr)
	}
	return table, nil
}

// smListProtect is H5AC_protect(f, H5AC_SOHM_LIST, header->index_addr,
// ...): load (through the metadata cache) the list index of header, which
// must be an H5SM_LIST index.
func (f *File) smListProtect(header *h5smIndexHeader) (*h5smList, error) {
	if header.indexType != h5smIndexList {
		return nil, errorf(H5E_SOHM, H5E_BADTYPE, "shared message index is not a list")
	}
	if !addrDefined(header.indexAddr) {
		return nil, errorf(H5E_SOHM, H5E_BADVALUE, "shared message list address is undefined")
	}
	v, err := f.cached("H5SM_LIST", header.indexAddr, func() (any, error) {
		return h5smCacheListLoad(f, header)
	})
	if err != nil {
		return nil, wrapf(err, H5E_SOHM, H5E_CANTPROTECT, "unable to load SOHM index")
	}
	list, ok := v.(*h5smList)
	if !ok {
		return nil, errorf(H5E_SOHM, H5E_CANTPROTECT, "cached object at address %d is not a SOHM list", header.indexAddr)
	}
	return list, nil
}

// h5smTypeToFlag is H5SM__type_to_flag: get the shared message flag for a
// given message type.
func h5smTypeToFlag(typeID uint16) (uint32, error) {
	/* Translate the H5O type_id into an H5SM type flag */
	switch typeID {
	case h5oFillID:
		typeID = h5oFillNewID
		return uint32(1) << typeID, nil
	case h5oSdspaceID, h5oDtypeID, h5oFillNewID, h5oPlineID, h5oAttrID:
		return uint32(1) << typeID, nil
	}
	return 0, errorf(H5E_SOHM, H5E_BADTYPE, "unknown message type ID")
}

// h5smGetIndex is H5SM__get_index: get the index number for a given
// message type.
//
// Returns the number of the index in the supplied table that holds
// messages of type typeID, or negative if there is no index for this
// message type.
func h5smGetIndex(table *h5smMasterTable, typeID uint16) (int, error) {
	/* Translate the H5O type_id into an H5SM type flag */
	typeFlag, err := h5smTypeToFlag(typeID)
	if err != nil {
		return -1, wrapf(err, H5E_SOHM, H5E_CANTGET, "can't map message type to flag")
	}

	/* Search the indexes until we find one that matches this flag or we've
	 * searched them all.
	 */
	for x := range table.indexes {
		if table.indexes[x].mesgTypes&typeFlag != 0 {
			return x, nil
		}
	}
	return -1, nil
}

// smTypeShared is H5SM_type_shared: check if a given message type is
// shared in a file.
func (f *File) smTypeShared(typeID uint16) (bool, error) {
	/* Translate the H5O type_id into an H5SM type flag */
	typeFlag, err := h5smTypeToFlag(typeID)
	if err != nil {
		return false, wrapf(err, H5E_SOHM, H5E_CANTGET, "can't map message type to flag")
	}

	/* Look up the master SOHM table */
	sohm := f.sohmTable()
	if sohm == nil || !addrDefined(sohm.addr) {
		/* No shared messages of any type */
		return false, nil
	}
	table, err := f.smTableProtect()
	if err != nil {
		return false, wrapf(err, H5E_SOHM, H5E_CANTPROTECT, "unable to load SOHM master table")
	}

	/* Search the indexes until we find one that matches this flag or we've
	 * searched them all.
	 */
	for u := range table.indexes {
		if table.indexes[u].mesgTypes&typeFlag != 0 {
			return true, nil
		}
	}
	return false, nil
}

// smGetFheapAddr is H5SM_get_fheap_addr: gets the address of the fractal
// heap used to store messages of type typeID.
func (f *File) smGetFheapAddr(typeID uint16) (haddr, error) {
	/* Look up the master SOHM table */
	table, err := f.smTableProtect()
	if err != nil {
		return haddrUndef, wrapf(err, H5E_SOHM, H5E_CANTPROTECT, "unable to load SOHM master table")
	}

	/* Look up index for message type */
	indexNum, err := h5smGetIndex(table, typeID)
	if err != nil {
		return haddrUndef, wrapf(err, H5E_SOHM, H5E_CANTGET, "unable to check for SOHM index")
	}
	if indexNum < 0 {
		return haddrUndef, errorf(H5E_SOHM, H5E_NOTFOUND, "unable to find correct SOHM index")
	}

	/* Retrieve heap address for index */
	return table.indexes[indexNum].heapAddr, nil
}

// h5smInfo is the shared message information H5SM_get_info stores in the
// file creation property list (H5Pget_shared_mesg_nindexes,
// H5Pget_shared_mesg_index, H5Pget_shared_mesg_phase_change).
type h5smInfo struct {
	nindexes   uint32   /* Number of shared messages indexes in the table */
	indexFlags []uint32 /* Message flags for each index */
	minsizes   []uint32 /* Minimum message size for each index */
	listMax    uint32   /* SOHM list-to-btree cutoff */
	btreeMin   uint32   /* SOHM btree-to-list cutoff */
	// storeMsgCrtIdx is H5F_STORE_MSG_CRT_IDX: attributes are shared in this
	// file, so creation indices are tracked on object header messages.
	storeMsgCrtIdx bool
}

// smGetInfo is H5SM_get_info: get the shared message info for a file, if
// there is any (a zero h5smInfo when the file has no shared message
// table). The table message itself was read from the superblock extension
// by superExtRead.
func (f *File) smGetInfo() (h5smInfo, error) {
	var info h5smInfo

	/* Check for the extension having a 'shared message info' message */
	sohm := f.sohmTable()
	if sohm == nil {
		/* No SOHM info in file */
		/* Shared object header messages are disabled */
		return info, nil
	}
	if !addrDefined(sohm.addr) || sohm.nindexes == 0 || sohm.nindexes > h5oShmesgMaxNindexes {
		return info, errorf(H5E_SOHM, H5E_BADVALUE, "invalid shared message table message")
	}

	/* Read the rest of the SOHM table information from the cache */
	table, err := f.smTableProtect()
	if err != nil {
		return info, wrapf(err, H5E_SOHM, H5E_CANTPROTECT, "unable to load SOHM master table")
	}

	/* Get index conversion limits */
	info.listMax = uint32(table.indexes[0].listMax)
	info.btreeMin = uint32(table.indexes[0].btreeMin)

	/* Iterate through all indices */
	info.nindexes = table.numIndexes
	for u := range table.indexes {
		/* Pack information about the individual SOHM index */
		info.indexFlags = append(info.indexFlags, table.indexes[u].mesgTypes)
		info.minsizes = append(info.minsizes, uint32(table.indexes[u].minMesgSize))

		/* Check for sharing attributes in this file, which means that creation
		 *  indices must be tracked on object header message in the file.
		 */
		if table.indexes[u].mesgTypes&h5oShmesgAttrFlag != 0 {
			info.storeMsgCrtIdx = true
		}
	}
	return info, nil
}

// h5smReconstitute is H5SM_reconstitute: reconstitute a shared object
// header message structure from a plain heap ID.
func h5smReconstitute(msgTypeID uint16, heapID [h5oFheapIDLen]byte) *h5oShared {
	/* Set flag for shared message */
	return &h5oShared{
		typ:       h5oShareTypeSohm,
		heapID:    append([]byte(nil), heapID[:]...),
		oh:        haddrUndef,
		msgTypeID: msgTypeID,
	}
}

// h5smReadIterOp is H5SM__read_iter_op: OH iteration callback to get the
// encoded version of a message by index.
//
// Returns stop=true if this is the message we're searching for (with the
// encoded value returned in udata).
func h5smReadIterOp(mesg *h5oMesg, sequence uint32, udata *h5smReadUdata) (stop bool, err error) {
	/* Check the creation index for this message */
	if sequence == udata.idx {
		/* Get the message's encoded size */
		if len(mesg.raw) == 0 {
			return false, errorf(H5E_SOHM, H5E_BADVALUE, "shared message has no raw data")
		}

		/* Copy the encoded message into the buffer to return */
		udata.encodingBuf = append([]byte(nil), mesg.raw...)

		/* Found the message we were looking for */
		return true, nil
	}
	return false, nil
}

// h5smReadMesgFhCb is H5SM__read_mesg_fh_cb: callback for H5HF_op, used in
// H5SM__read_mesg below. Makes a copy of the message in the heap data,
// returned in the udata struct.
func h5smReadMesgFhCb(obj []byte, udata *h5smReadUdata) error {
	/* Copy the message from the heap */
	udata.encodingBuf = append([]byte(nil), obj...)
	return nil
}

// h5smReadMesg is H5SM__read_mesg: given an H5SM_sohm_t record, return the
// encoded message, either from the object header that holds it or from the
// index's fractal heap.
func h5smReadMesg(f *File, mesg *h5smSohm, fheap *h5hf, openOh *h5o) ([]byte, error) {
	/* Set up user data for message iteration */
	/* (The C code then resets udata.idx to 0, so that the first message of
	 *  the type is always read; the message's index is what is meant.) */
	udata := h5smReadUdata{file: f, idx: mesg.mesgLoc.index}

	/* Get the message size and encoded message for the message to be deleted,
	 * either from its OH or from the heap.
	 */
	if mesg.location == h5smInOh {
		/* Read message from object header */
		if mesg.msgTypeID >= h5oMsgTypes {
			return nil, errorf(H5E_SOHM, H5E_BADTYPE, "invalid message type %d", mesg.msgTypeID)
		}
		typeID := uint16(mesg.msgTypeID) /* map the type ID to the actual type object */

		var oh *h5o
		if openOh == nil || mesg.mesgLoc.ohAddr != h5oGetOhAddr(openOh) {
			/* Open the object in the file */
			/* Load the object header from the cache */
			var err error
			if oh, err = f.oProtect(mesg.mesgLoc.ohAddr); err != nil {
				return nil, wrapf(err, H5E_SOHM, H5E_CANTPROTECT, "unable to load object header")
			}
		} else {
			oh = openOh
		}

		/* Use the "real" iterate routine so it doesn't try to protect the OH */
		/* (H5O__msg_iterate_real over the messages of the type, with
		 *  'sequence' counting the messages of that type) */
		var sequence uint32
		for _, m := range oh.mesgs {
			if m.typeID != typeID {
				continue
			}
			stop, err := h5smReadIterOp(m, sequence, &udata)
			if err != nil {
				return nil, wrapf(err, H5E_SOHM, H5E_BADITER, "unable to iterate over object header messages")
			}
			if stop {
				break
			}
			sequence++
		}
	} else {
		if mesg.location != h5smInHeap {
			return nil, errorf(H5E_SOHM, H5E_BADVALUE, "invalid shared message location %d", mesg.location)
		}
		if fheap == nil {
			return nil, errorf(H5E_SOHM, H5E_BADVALUE, "no fractal heap for shared messages")
		}

		/* Copy the message from the heap */
		if err := fheap.op(mesg.heapLoc.fheapID[:], func(obj []byte) error { return h5smReadMesgFhCb(obj, &udata) }); err != nil {
			return nil, wrapf(err, H5E_SOHM, H5E_CANTLOAD, "can't read message from fractal heap.")
		}
	}
	if len(udata.encodingBuf) == 0 {
		return nil, errorf(H5E_SOHM, H5E_NOTFOUND, "shared message not found")
	}

	/* Record the returned values */
	return udata.encodingBuf, nil
}

// smReadMessage reads the message described by sh (sh.typ ==
// h5oShareTypeSohm, sh.heapID set) from the file's shared message heap and
// decodes it as a message of class typeID. It is the SOHM branch of
// H5O__shared_read (src/H5Oshared.c): H5SM_get_fheap_addr, H5HF_open,
// H5HF_get_obj_len / H5HF_read, then the class's decode callback with no
// message flags.
//
// The returned value is freshly decoded (not cached) and may be modified
// by the caller.
func (f *File) smReadMessage(sh *h5oShared, typeID uint16) (any, error) {
	if sh == nil || sh.typ != h5oShareTypeSohm {
		return nil, errorf(H5E_SOHM, H5E_BADVALUE, "not a message in the shared message heap")
	}
	if len(sh.heapID) != h5oFheapIDLen {
		return nil, errorf(H5E_SOHM, H5E_BADVALUE, "invalid shared message heap ID length %d", len(sh.heapID))
	}
	if typeID >= h5oMsgTypes {
		return nil, errorf(H5E_SOHM, H5E_BADTYPE, "invalid message type %d", typeID)
	}

	/* Get the shared message's fractal heap address */
	fheapAddr, err := f.smGetFheapAddr(typeID)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTGET, "unable to get fheap address for shared messages")
	}

	/* Open the fractal heap */
	fheap, err := f.hfOpen(fheapAddr)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTOPENFILE, "unable to open fractal heap")
	}

	/* Get the size of the message in the heap */
	bufSize, err := fheap.objLen(sh.heapID)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTGET, "can't get message size from fractal heap.")
	}
	if bufSize == 0 || bufSize > f.eof() {
		return nil, errorf(H5E_OHDR, H5E_BADSIZE, "invalid shared message size %d", bufSize)
	}

	/* Retrieve the message from the heap */
	buf, err := fheap.read(sh.heapID)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTLOAD, "can't read message from fractal heap.")
	}
	if uint64(len(buf)) != bufSize {
		return nil, errorf(H5E_OHDR, H5E_BADSIZE, "shared message size mismatch")
	}

	/* Decode the message */
	ret, err := f.msgDecode(nil, typeID, buf)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "can't decode shared message.")
	}
	if ret == nil {
		return nil, errorf(H5E_OHDR, H5E_CANTDECODE, "can't decode shared message.")
	}
	return ret, nil
}
