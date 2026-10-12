package hdf5

// Translated from src/H5Dcompact.c (read side): datasets whose raw data is
// stored in the layout message itself.

// h5dLopsCompact is H5D_LOPS_COMPACT: compact storage layout I/O ops.
func h5dLopsCompact() *h5dLayoutOps {
	return &h5dLayoutOps{
		name:         "compact",
		init:         h5dCompactInit,
		isSpaceAlloc: h5dCompactIsSpaceAlloc,
		read:         h5dCompactRead,
	}
}

// h5dCompactInit is H5D__compact_init: initialize the info for a compact
// dataset.
func h5dCompactInit(_ *File, dset *h5dShared) error {
	/*
	 * Now that we've read the dataset's datatype, dataspace and
	 * layout information, perform a quick check for compact datasets
	 * to ensure that the size of the internal buffer that was
	 * allocated for the dataset's raw data matches the size of
	 * the data. A corrupted file can cause a mismatch between the
	 * two, which might result in buffer overflows during future
	 * I/O to the dataset.
	 */
	dtSize := dset.typ.Size
	if dtSize == 0 {
		return errorf(H5E_DATASET, H5E_CANTGET, "can't get datatype size")
	}
	nelmts := h5sGetSimpleExtentNpoints(dset.space)

	/* Compute the size of the dataset's contiguous storage */
	/* Check for overflow during multiplication */
	dataSize, err := h5dMulSize(nelmts, dtSize)
	if err != nil {
		return err
	}

	/* Check for mismatch */
	if dset.layout.compactSize != dataSize || uint64(len(dset.layout.compactBuf)) != dataSize {
		return errorf(H5E_DATASET, H5E_BADVALUE, "bad value from dataset header - size of compact dataset's data buffer doesn't match size of dataset data")
	}
	return nil
}

// h5dCompactIsSpaceAlloc is H5D__compact_is_space_alloc: compact storage
// is always allocated.
func h5dCompactIsSpaceAlloc(*h5dShared) bool { return true }

// h5dCompactIoInit is H5D__compact_io_init: point the I/O info at the
// compact data buffer.
func h5dCompactIoInit(dinfo *h5dDsetIoInfo) {
	dinfo.compactBuf = dinfo.dset.layout.compactBuf
	dinfo.readvv = h5dCompactReadvv
}

// h5dCompactRead is the layout "ser_read" routine of compact storage
// (H5D__select_read with H5D__compact_readvv).
func h5dCompactRead(_ *File, dinfo *h5dDsetIoInfo) error {
	h5dCompactIoInit(dinfo)
	return h5dSelectRead(dinfo)
}

// h5dCompactReadvv is H5D__compact_readvv (H5D__compact_iovv_memmanage_cb):
// copy a run of the compact buffer into the destination.
func h5dCompactReadvv(dinfo *h5dDsetIoInfo, fileOff, memOff, length uint64) error {
	end := fileOff + length
	if end < fileOff || end > uint64(len(dinfo.compactBuf)) {
		return errorf(H5E_IO, H5E_READERROR, "read past end of compact data")
	}
	dst, err := dinfo.memSlice(memOff, length)
	if err != nil {
		return err
	}
	copy(dst, dinfo.compactBuf[fileOff:end])
	return nil
}
