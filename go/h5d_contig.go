package hdf5

// Translated from src/H5Dcontig.c (read side): datasets stored as one
// contiguous block in the file. The sieve buffer of the C library is not
// needed: each run of the selection is read directly.

// h5dLopsContig is H5D_LOPS_CONTIG: contiguous storage layout I/O ops.
func h5dLopsContig() *h5dLayoutOps {
	return &h5dLayoutOps{
		name:         "contiguous",
		init:         h5dContigInit,
		isSpaceAlloc: h5dContigIsSpaceAlloc,
		read:         h5dContigRead,
	}
}

// h5dContigCheck is H5D__contig_check: sanity check the contiguous
// storage information against the dataspace and datatype.
func (f *File) h5dContigCheck(layout *h5oLayout, extent *h5sExtent, dtSize uint64) error {
	/* Retrieve the number of elements in the dataspace */
	nelmts := h5sExtentNelem(extent)

	/* Get the datatype's size */
	if dtSize == 0 {
		return errorf(H5E_DATASET, H5E_CANTGET, "unable to retrieve size of datatype")
	}

	/* Compute the size of the dataset's contiguous storage */
	/* Check for overflow during multiplication */
	dataSize, err := h5dMulSize(nelmts, dtSize)
	if err != nil {
		return err
	}

	/* Check for invalid (corrupted in the file, probably) dimensions */
	if addrDefined(layout.contigAddr) {
		relEOA := f.eof()

		/* Check for invalid dataset size (from bad dimensions) putting the
		 * dataset elements off the end of the file
		 */
		end := layout.contigAddr + dataSize
		if end < layout.contigAddr || (dataSize > 0 && end <= layout.contigAddr) {
			return errorf(H5E_DATASET, H5E_OVERFLOW, "invalid dataset size, likely file corruption")
		}
		if end > relEOA {
			return errorf(H5E_DATASET, H5E_OVERFLOW, "invalid dataset size, likely file corruption")
		}
	}
	return nil
}

// h5dContigInit is H5D__contig_init: initialize the info for a contiguous
// dataset.
func h5dContigInit(f *File, dset *h5dShared) error {
	/* Sanity check the dataset's info */
	if err := f.h5dContigCheck(&dset.layout, &dset.space.extent, dset.typ.Size); err != nil {
		return wrapf(err, H5E_DATASET, H5E_BADVALUE, "invalid dataset info")
	}

	/* Compute the size of the contiguous storage for versions of the
	 * layout message less than version 3 because versions 1 & 2 would
	 * truncate the dimension sizes to 32-bits of information. - QAK 5/26/04
	 */
	if dset.layout.version < 3 {
		/* Retrieve the number of elements in the dataspace */
		nelmts := h5sGetSimpleExtentNpoints(dset.space)

		/* Compute the size of the dataset's contiguous storage */
		size, err := h5dMulSize(nelmts, dset.typ.Size)
		if err != nil {
			return err
		}
		dset.layout.contigSize = size
	}
	return nil
}

// h5dContigIsSpaceAlloc is H5D__contig_is_space_alloc: query if space is
// allocated for the layout.
func h5dContigIsSpaceAlloc(dset *h5dShared) bool {
	/* Set return value */
	return addrDefined(dset.layout.contigAddr)
}

// h5dContigIoInit is H5D__contig_io_init: set up the storage information
// for the contiguous read.
func h5dContigIoInit(dinfo *h5dDsetIoInfo) {
	dinfo.contigAddr = dinfo.dset.layout.contigAddr
	dinfo.contigSize = dinfo.dset.layout.contigSize
	dinfo.readvv = h5dContigReadvv
}

// h5dContigRead is H5D__contig_read: read from a contiguous dataset.
func h5dContigRead(_ *File, dinfo *h5dDsetIoInfo) error {
	h5dContigIoInit(dinfo)

	/* Read data through the selection */
	if err := h5dSelectRead(dinfo); err != nil {
		return wrapf(err, H5E_DATASET, H5E_READERROR, "contiguous read failed")
	}
	return nil
}

// h5dContigReadvv is H5D__contig_readvv (H5D__contig_readvv_cb): read a
// run of the contiguous storage into the destination.
func h5dContigReadvv(dinfo *h5dDsetIoInfo, fileOff, memOff, length uint64) error {
	f := dinfo.dset.f
	/* (the extent of the storage was checked against the end of the file
	 *  by H5D__contig_check; f.readInto checks every read against it too) */
	if end := dinfo.contigAddr + fileOff + length; end < dinfo.contigAddr {
		return errorf(H5E_IO, H5E_OVERFLOW, "address overflow in contiguous storage")
	}
	dst, err := dinfo.memSlice(memOff, length)
	if err != nil {
		return err
	}

	/* Write data */
	if err := f.readInto(dinfo.contigAddr+fileOff, dst); err != nil {
		return wrapf(err, H5E_DATASET, H5E_READERROR, "block read failed")
	}
	return nil
}
