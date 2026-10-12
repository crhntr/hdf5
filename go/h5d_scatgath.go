package hdf5

// Translated from src/H5Dscatgath.c (read side): gathering the elements of
// a file selection into a contiguous buffer and scattering a contiguous
// buffer into the elements of a memory selection.
//
// In the C library these are the halves of H5D__scatgath_read, the read
// path used when the data must be converted between the file and memory
// datatypes. Reading here never converts (the buffer stays in the file
// datatype), so H5D__scatgath_read reduces to a gather into a temporary
// buffer followed by a scatter; it is used where the C library uses it
// without a conversion step: filling selections with variable-length fill
// values (H5D__fill) and copying between selections of two buffers.

// h5dGatherFile is H5D__gather_file: gather nelmts elements of the file
// selection of dinfo (starting with the first) into tgathBuf, through the
// layout's readvv routine.
func h5dGatherFile(dinfo *h5dDsetIoInfo, nelmts uint64, tgathBuf []byte) error {
	need, err := h5dMulSize(nelmts, dinfo.typeSize)
	if err != nil || need > uint64(len(tgathBuf)) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "gather buffer too small")
	}
	if nelmts == 0 {
		return nil
	}

	/* Set up temporary I/O info object: the memory side is the gather
	 * buffer, one contiguous run of nelmts elements */
	memSpace, err := h5sCreateSimple([]uint64{nelmts}, nil)
	if err != nil {
		return err
	}
	tmp := *dinfo
	tmp.memSpace = memSpace
	tmp.buf = tgathBuf
	tmp.nelmts = nelmts

	/* Read data */
	if err := h5dSelectIO(&tmp, dinfo.typeSize); err != nil {
		return wrapf(err, H5E_DATASET, H5E_READERROR, "read error")
	}
	return nil
}

// h5dScatterMem is H5D__scatter_mem: scatter the elements of the
// contiguous buffer tscatBuf into the elements of buf selected by space
// (elmtSize bytes per element).
func h5dScatterMem(tscatBuf []byte, space *h5s, elmtSize uint64, buf []byte) error {
	if !h5sSelectValid(space) {
		return errorf(H5E_DATASPACE, H5E_BADRANGE, "selection is not within the extent")
	}
	var off uint64
	/* Loop, while sequences left to process */
	for s := range space.iterSeqs(elmtSize) {
		end := s.off + s.len
		if end < s.off || end > uint64(len(buf)) || off+s.len > uint64(len(tscatBuf)) {
			return errorf(H5E_DATASET, H5E_OVERFLOW, "scatter out of range of the buffers")
		}
		copy(buf[s.off:end], tscatBuf[off:off+s.len])

		/* Advance offset in destination buffer */
		off += s.len
	}
	return nil
}

// h5dGatherMem is H5D__gather_mem: gather the elements of buf selected by
// space into the contiguous buffer tgathBuf.
func h5dGatherMem(buf []byte, space *h5s, elmtSize uint64, tgathBuf []byte) error {
	if !h5sSelectValid(space) {
		return errorf(H5E_DATASPACE, H5E_BADRANGE, "selection is not within the extent")
	}
	var off uint64
	for s := range space.iterSeqs(elmtSize) {
		end := s.off + s.len
		if end < s.off || end > uint64(len(buf)) || off+s.len > uint64(len(tgathBuf)) {
			return errorf(H5E_DATASET, H5E_OVERFLOW, "gather out of range of the buffers")
		}
		copy(tgathBuf[off:off+s.len], buf[s.off:end])

		/* Advance offset in gather buffer */
		off += s.len
	}
	return nil
}

// h5dScatgathRead is H5D__scatgath_read without a datatype conversion:
// gather the selected file elements into a temporary buffer and scatter
// them into the memory selection.
func h5dScatgathRead(dinfo *h5dDsetIoInfo) error {
	/* Check for NOOP read */
	if dinfo.nelmts == 0 {
		return nil
	}
	size, err := h5dMulSize(dinfo.nelmts, dinfo.typeSize)
	if err != nil {
		return err
	}
	tconv := make([]byte, size)

	/* Gather the data from disk into the datatype conversion buffer. */
	if err := h5dGatherFile(dinfo, dinfo.nelmts, tconv); err != nil {
		return wrapf(err, H5E_IO, H5E_READERROR, "file gather failed")
	}

	/* Scatter the data into memory */
	if err := h5dScatterMem(tconv, dinfo.memSpace, dinfo.typeSize, dinfo.buf); err != nil {
		return wrapf(err, H5E_DATASET, H5E_READERROR, "scatter failed")
	}
	return nil
}
