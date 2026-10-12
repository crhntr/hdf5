package hdf5

// Translated from the superblock extension part of H5F__super_read
// (src/H5Fsuper.c) and H5F__super_ext_open, plus the part of H5SM_get_info
// (src/H5SM.c) that reads the shared message table message.

// superExtOpen is H5F__super_ext_open: open the superblock extension
// object header.
func (f *File) superExtOpen(extAddr haddr) (*h5o, error) {
	/* Set up "fake" object location for superblock extension */
	oh, err := f.oProtect(extAddr)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTOPENOBJ, "unable to open superblock extension")
	}
	return oh, nil
}

// superExtRead decodes the superblock extension object header messages
// that matter for reading: driver info (H5O_DRVINFO_ID), the shared message
// table (H5O_SHMESG_ID, which sets f.sohm to a *h5oShmesgTable), B-tree 'K'
// values (H5O_BTREEK_ID), file space info (H5O_FSINFO_ID) and the metadata
// cache image message (H5O_MDCI_MSG_ID). It is called by superRead when the
// superblock has an extension address.
func (f *File) superExtRead() error {
	sblock := f.sblock

	/* Decode the optional superblock extension info */
	if !addrDefined(sblock.extAddr) {
		return nil
	}

	/* Sanity check - superblock extension should only be defined for
	 *      superblock version >= 2.
	 */
	if sblock.superVers < hdf5SuperblockVersion2 {
		return errorf(H5E_FILE, H5E_BADVALUE, "invalid superblock - extension message should not be defined for version < 2")
	}

	/* (The C code enlarges the 'eoa' for the object header memory type when
	 * the extension lies past the stored EOF (split/multi VFD); reads here
	 * are bounded by the real end of the file instead.) */

	/* Open the superblock extension */
	extLoc, err := f.superExtOpen(sblock.extAddr)
	if err != nil {
		return wrapf(err, H5E_FILE, H5E_CANTOPENOBJ, "unable to open file's superblock extension")
	}

	/* Check for the extension having a 'driver info' message */
	if f.msgExists(extLoc, h5oDrvinfoID) {
		/* Retrieve the 'driver info' structure */
		v, err := f.msgRead(extLoc, h5oDrvinfoID)
		if err != nil {
			return wrapf(err, H5E_FILE, H5E_CANTGET, "driver info message not present")
		}
		di := v.(*h5oDrvinfo)

		/* Validate and decode driver information */
		d := &drvinfo{name: di.name, buf: di.buf}
		if err := f.sbLoad(d); err != nil {
			return wrapf(err, H5E_FILE, H5E_CANTDECODE, "unable to decode driver information")
		}
		f.drvinfo = d
	}

	/* Read in the shared OH message information if there is any */
	if f.msgExists(extLoc, h5oShmesgID) {
		v, err := f.msgRead(extLoc, h5oShmesgID)
		if err != nil {
			return wrapf(err, H5E_SOHM, H5E_CANTGET, "shared message info message not present")
		}
		/* Set SOHM info from file */
		f.sohm = v.(*h5oShmesgTable)
	}

	/* Check for the extension having a 'v1 B-tree "K"' message */
	if f.msgExists(extLoc, h5oBtreekID) {
		/* Retrieve the 'v1 B-tree "K"' structure */
		v, err := f.msgRead(extLoc, h5oBtreekID)
		if err != nil {
			return wrapf(err, H5E_FILE, H5E_CANTGET, "v1 B-tree 'K' info message not present")
		}
		btreek := v.(*h5oBtreek)

		/* Set non-default v1 B-tree 'K' value info from file */
		sblock.btreeK[h5bChunkID] = btreek.btreeK[h5bChunkID]
		sblock.btreeK[h5bSnodeID] = btreek.btreeK[h5bSnodeID]
		sblock.symLeafK = btreek.symLeafK
		f.btreeK = sblock.btreeK
		f.symLeafK = sblock.symLeafK
	}

	/* Check for the extension having a 'free-space manager info' message */
	if f.msgExists(extLoc, h5oFsinfoID) {
		/* Get message flags */
		flags, err := f.msgGetFlags(extLoc, h5oFsinfoID)
		if err != nil {
			return wrapf(err, H5E_FILE, H5E_CANTGET, "unable to message flags for free-space manager info message")
		}

		/* If message is NOT marked "unknown"--set up file space info  */
		if flags&h5oMsgFlagWasUnknown == 0 {
			/* Retrieve the 'file space info' structure */
			v, err := f.msgRead(extLoc, h5oFsinfoID)
			if err != nil {
				return wrapf(err, H5E_FILE, H5E_CANTGET, "unable to get free-space manager info message")
			}
			fsinfo := v.(*h5oFsinfo)

			/* Version bounds check */
			if err := h5oFsinfoCheckVersion(fsinfo); err != nil {
				return wrapf(err, H5E_FILE, H5E_BADRANGE, "File space info message's version out of bounds")
			}

			/* (The file space strategy, persistence, threshold and the
			 * free-space manager addresses only matter for writing.) */
			if fsinfo.pageSize < h5fFileSpacePageSizeMin {
				return errorf(H5E_FILE, H5E_BADVALUE, "file space page size too small")
			}
		}
	}

	/* Check for the extension having a 'metadata cache image' message */
	if f.msgExists(extLoc, h5oMdciMsgID) {
		/* Retrieve the 'metadata cache image message' structure */
		if _, err := f.msgRead(extLoc, h5oMdciMsgID); err != nil {
			return wrapf(err, H5E_FILE, H5E_CANTGET, "unable to get metadata cache image message")
		}
		/* (H5AC_load_cache_image_on_next_protect: loading the metadata
		 * cache image into the cache is not translated.) */
	}
	return nil
}
