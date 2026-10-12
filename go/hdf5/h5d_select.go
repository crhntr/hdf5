package hdf5

// Translated from src/H5Dselect.c (read side): moving the elements of a
// file selection into the elements of a memory selection, sequence by
// sequence, through the layout's "readvv" callback.
//
// The C library fills arrays of {offset, length} sequences for the file and
// memory selections and hands both lists to the layout's readvv routine,
// which pairs them up with H5VM_opvv. Here the selections' sequences come
// from (*h5s).iterSeqs and h5dSelectIO pairs them itself, calling the
// layout's per-pair callback (the C *_readvv_cb operator of H5VM_opvv)
// once for each run that is contiguous in both selections.

import "iter"

// h5dReadvvFunc is the per-sequence operator of a layout's readvv routine
// (the H5VM_opvv callbacks H5D__contig_readvv_cb, H5D__compact_iovv_memmanage_cb,
// H5D__efl_readvv_cb, ...): copy length bytes starting at byte offset
// fileOff of the storage into dinfo.buf at byte offset memOff.
type h5dReadvvFunc func(dinfo *h5dDsetIoInfo, fileOff, memOff, length uint64) error

// h5dSelectIO is H5D__select_io for reading: perform I/O between the
// file selection and the memory selection of dinfo, elmtSize bytes per
// element, through dinfo.readvv.
func h5dSelectIO(dinfo *h5dDsetIoInfo, elmtSize uint64) error {
	if elmtSize == 0 {
		return errorf(H5E_DATASPACE, H5E_BADVALUE, "invalid elmt_size of 0")
	}
	if dinfo.readvv == nil {
		return errorf(H5E_DATASET, H5E_UNSUPPORTED, "layout has no read routine")
	}

	/* Initialize nelmts */
	nelmts := dinfo.nelmts
	if nelmts == 0 {
		return nil
	}

	/* (H5S_select_iter_init requires valid selections) */
	if !h5sSelectValid(dinfo.fileSpace) {
		return errorf(H5E_DATASPACE, H5E_BADRANGE, "file selection is not within the extent")
	}
	if !h5sSelectValid(dinfo.memSpace) {
		return errorf(H5E_DATASPACE, H5E_BADRANGE, "memory selection is not within the extent")
	}
	total, err := h5dMulSize(nelmts, elmtSize)
	if err != nil {
		return err
	}

	/* Check for only one element in selection */
	if nelmts == 1 {
		/* Get offset of first element in selections */
		singleFileOff, err := h5sGetSelectOffset(dinfo.fileSpace)
		if err != nil {
			return wrapf(err, H5E_INTERNAL, H5E_UNSUPPORTED, "can't retrieve file selection offset")
		}
		singleMemOff, err := h5sGetSelectOffset(dinfo.memSpace)
		if err != nil {
			return wrapf(err, H5E_INTERNAL, H5E_UNSUPPORTED, "can't retrieve memory selection offset")
		}

		/* Perform I/O on memory and file sequences */
		if err := dinfo.readvv(dinfo, singleFileOff*elmtSize, singleMemOff*elmtSize, elmtSize); err != nil {
			return wrapf(err, H5E_DATASPACE, H5E_READERROR, "read error")
		}
		return nil
	}

	/* Initialize memory iterator */
	nextMem, stopMem := iter.Pull(dinfo.memSpace.iterSeqs(elmtSize))
	defer stopMem()
	var mem seq /* current memory sequence (remaining part) */

	/* Loop, until all bytes are processed */
	left := total
	for file := range dinfo.fileSpace.iterSeqs(elmtSize) {
		for file.len > 0 {
			if left == 0 {
				return errorf(H5E_DATASPACE, H5E_BADVALUE, "file selection has more elements than the memory selection")
			}
			/* Check if more memory sequences are needed */
			if mem.len == 0 {
				var ok bool
				if mem, ok = nextMem(); !ok || mem.len == 0 {
					return errorf(H5E_INTERNAL, H5E_UNSUPPORTED, "sequence length generation failed")
				}
			}

			/* Perform I/O on memory and file sequences */
			n := min(file.len, mem.len, left)
			if err := dinfo.readvv(dinfo, file.off, mem.off, n); err != nil {
				return wrapf(err, H5E_DATASPACE, H5E_READERROR, "read error")
			}
			file.off += n
			file.len -= n
			mem.off += n
			mem.len -= n
			left -= n
		}
	}
	if left != 0 {
		return errorf(H5E_DATASPACE, H5E_BADVALUE, "selections have different numbers of elements")
	}
	return nil
}

// h5dSelectRead is H5D__select_read: read the elements of the file
// selection into the memory selection.
func h5dSelectRead(dinfo *h5dDsetIoInfo) error {
	/* Call generic selection operation */
	if err := h5dSelectIO(dinfo, dinfo.typeSize); err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_READERROR, "read error")
	}
	return nil
}
