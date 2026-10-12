package hdf5

// Translated from src/H5SMcache.c (read side only): the metadata cache
// callbacks that load the shared object header message master table
// ("SMTB") and list indexes ("SMLI").

// Shared message signatures (H5SM_TABLE_MAGIC, H5SM_LIST_MAGIC in
// src/H5Fprivate.h).
const (
	h5smTableMagic = "SMTB" /* Shared Message Table */
	h5smListMagic  = "SMLI" /* Shared Message List */
)

// h5smTableCacheUd is H5SM_table_cache_ud_t: callback info for loading a
// shared message table index into the cache.
type h5smTableCacheUd struct {
	f *File /* File that shared message index stored as a table is in */
}

// h5smListCacheUd is H5SM_list_cache_ud_t: callback info for loading a
// shared message list index into the cache.
type h5smListCacheUd struct {
	f      *File            /* File that shared message index stored as a table is in */
	header *h5smIndexHeader /* Index header for this list */
}

// h5smCacheTableGetInitialLoadSize is
// H5SM__cache_table_get_initial_load_size: return the size of the master
// table of Shared Object Header Message indexes on disk.
func h5smCacheTableGetInitialLoadSize(udata *h5smTableCacheUd) uint64 {
	/* Set the image length size */
	return h5smTableSize(udata.f)
}

// h5smCacheTableVerifyChksum is H5SM__cache_table_verify_chksum: verify
// the computed checksum of the data structure is the same as the stored
// chksum.
func h5smCacheTableVerifyChksum(image []byte) (bool, error) {
	/* Get stored and computed checksums */
	stored, computed, err := h5b2GetChecksums(image)
	if err != nil {
		return false, wrapf(err, H5E_SOHM, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5smCacheTableDeserialize is H5SM__cache_table_deserialize: given a
// buffer containing the on disk representation of the master table of
// Shared Object Header Message indexes, deserialize the table.
func h5smCacheTableDeserialize(image []byte, udata *h5smTableCacheUd) (*h5smMasterTable, error) {
	f := udata.f
	sohm := f.sohmTable()
	if sohm == nil {
		return nil, errorf(H5E_SOHM, H5E_BADVALUE, "file has no shared message table")
	}

	/* Verify that we're reading version 0 of the table; this is the only
	 * version defined so far.
	 */
	if sohm.version != hdf5SharedheaderVersion {
		return nil, errorf(H5E_SOHM, H5E_VERSION, "bad shared message table version number %d", sohm.version)
	}

	/* Allocate space for the master table in memory */
	table := &h5smMasterTable{}

	/* Read number of indexes and version from file superblock */
	table.numIndexes = uint32(sohm.nindexes)
	if table.numIndexes == 0 || table.numIndexes > h5oShmesgMaxNindexes {
		return nil, errorf(H5E_SOHM, H5E_BADVALUE, "bad number of shared message indexes %d", table.numIndexes)
	}

	/* Compute the size of the SOHM table header on disk.  This is the "table"
	 * itself plus each index within the table
	 */
	table.tableSize = h5smTableSize(f)
	if table.tableSize != uint64(len(image)) {
		return nil, errorf(H5E_SOHM, H5E_BADSIZE, "shared message table image has the wrong size")
	}

	p := f.newImage(image)

	/* Check magic number */
	if string(p.bytes(h5SizeofMagic)) != h5smTableMagic {
		return nil, errorf(H5E_SOHM, H5E_CANTLOAD, "bad SOHM table signature")
	}

	/* Allocate space for the index headers in memory*/
	table.indexes = make([]h5smIndexHeader, table.numIndexes)

	/* Read in the index headers */
	for u := range table.indexes {
		idx := &table.indexes[u]

		/* Verify correct version of index list */
		if p.uint8() != h5smListVersion {
			return nil, errorf(H5E_SOHM, H5E_VERSION, "bad shared message list version number")
		}

		/* Type of the index (list or B-tree) */
		idx.indexType = h5smIndexType(p.uint8())

		/* Type of messages in the index */
		idx.mesgTypes = uint32(p.uint16())

		/* Minimum size of message to share */
		idx.minMesgSize = uint64(p.uint32())

		/* List cutoff; fewer than this number and index becomes a list */
		idx.listMax = uint64(p.uint16())

		/* B-tree cutoff; more than this number and index becomes a B-tree */
		idx.btreeMin = uint64(p.uint16())

		/* Number of messages shared */
		idx.numMessages = uint64(p.uint16())

		/* Address of the actual index */
		idx.indexAddr = p.addr()

		/* Address of the index's heap */
		idx.heapAddr = p.addr()

		/* Compute the size of a list index for this SOHM index */
		idx.listSize = h5smListSize(f, idx.listMax)
	}

	/* checksum verification already done in verify_chksum cb */

	/* Read in checksum */
	_ = p.uint32()

	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_SOHM, H5E_CANTLOAD, "can't decode shared message table")
	}

	/* Sanity check */
	if p.remaining() != 0 {
		return nil, errorf(H5E_SOHM, H5E_BADSIZE, "shared message table image has the wrong size")
	}
	return table, nil
}

// h5smCacheTableLoad reads, verifies and deserializes the master table at
// addr (the H5C__load_entry sequence for H5AC_SOHM_TABLE).
func h5smCacheTableLoad(f *File, addr haddr) (*h5smMasterTable, error) {
	udata := &h5smTableCacheUd{f: f}
	size := h5smCacheTableGetInitialLoadSize(udata)
	if size > f.eof() {
		return nil, errorf(H5E_SOHM, H5E_BADSIZE, "shared message table is larger than the file")
	}
	image, err := f.readAt(addr, int(size))
	if err != nil {
		return nil, wrapf(err, H5E_SOHM, H5E_CANTLOAD, "unable to read shared message table")
	}
	if f.opts.verifyChecksums {
		ok, err := h5smCacheTableVerifyChksum(image)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_SOHM, H5E_BADCHECKSUM, "incorrect metadata checksum for shared message table")
		}
	}
	return h5smCacheTableDeserialize(image, udata)
}

// h5smCacheListGetInitialLoadSize is
// H5SM__cache_list_get_initial_load_size: return the size of the list
// index on disk.
func h5smCacheListGetInitialLoadSize(udata *h5smListCacheUd) uint64 {
	/* Set the image length size */
	return udata.header.listSize
}

// h5smCacheListVerifyChksum is H5SM__cache_list_verify_chksum: verify the
// computed checksum of the data structure is the same as the stored
// chksum.
func h5smCacheListVerifyChksum(image []byte, udata *h5smListCacheUd) (bool, error) {
	/* Exact size with checksum at the end */
	chkSize := h5smListSize(udata.f, udata.header.numMessages)
	if chkSize > uint64(len(image)) {
		return false, errorf(H5E_SOHM, H5E_BADSIZE, "shared message list has more messages than fit")
	}

	/* Get stored and computed checksums */
	stored, computed, err := h5b2GetChecksums(image[:chkSize])
	if err != nil {
		return false, wrapf(err, H5E_SOHM, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5smCacheListDeserialize is H5SM__cache_list_deserialize: given a buffer
// containing the on disk image of a list of SOHM message, deserialize the
// list.
func h5smCacheListDeserialize(image []byte, udata *h5smListCacheUd) (*h5smList, error) {
	f := udata.f
	header := udata.header

	/* (The list holds at most list_max messages) */
	if header.numMessages > header.listMax {
		return nil, errorf(H5E_SOHM, H5E_BADVALUE, "shared message list has more messages (%d) than its maximum (%d)", header.numMessages, header.listMax)
	}
	if uint64(len(image)) != header.listSize {
		return nil, errorf(H5E_SOHM, H5E_BADSIZE, "shared message list image has the wrong size")
	}

	/* Allocate space for the SOHM list data structure */
	list := &h5smList{header: header}

	/* Allocate list in memory as an array*/
	list.messages = make([]h5smSohm, header.listMax)

	p := f.newImage(image)

	/* Check magic number */
	if string(p.bytes(h5SizeofMagic)) != h5smListMagic {
		return nil, errorf(H5E_SOHM, H5E_CANTLOAD, "bad SOHM list signature")
	}

	/* Read messages into the list array */
	ctx := h5smBt2CrtContext(f)
	entrySize := int(h5smSohmEntrySize(f))
	for u := uint64(0); u < header.numMessages; u++ {
		mesg, err := h5smMessageDecode(p.sub(entrySize), ctx)
		if err != nil {
			return nil, wrapf(err, H5E_SOHM, H5E_CANTLOAD, "can't decode shared message")
		}
		list.messages[u] = *mesg
	}

	/* checksum verification already done in verify_chksum cb */

	/* Read in checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_SOHM, H5E_CANTLOAD, "can't decode shared message list")
	}

	/* Initialize the rest of the array */
	for u := header.numMessages; u < header.listMax; u++ {
		list.messages[u].location = h5smNoLoc
	}
	return list, nil
}

// h5smCacheListLoad reads, verifies and deserializes the list index of
// header (the H5C__load_entry sequence for H5AC_SOHM_LIST).
func h5smCacheListLoad(f *File, header *h5smIndexHeader) (*h5smList, error) {
	udata := &h5smListCacheUd{f: f, header: header}
	size := h5smCacheListGetInitialLoadSize(udata)
	if size > f.eof() {
		return nil, errorf(H5E_SOHM, H5E_BADSIZE, "shared message list is larger than the file")
	}
	image, err := f.readAt(header.indexAddr, int(size))
	if err != nil {
		return nil, wrapf(err, H5E_SOHM, H5E_CANTLOAD, "unable to read shared message list")
	}
	if f.opts.verifyChecksums {
		ok, err := h5smCacheListVerifyChksum(image, udata)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_SOHM, H5E_BADCHECKSUM, "incorrect metadata checksum for shared message list")
		}
	}
	return h5smCacheListDeserialize(image, udata)
}
