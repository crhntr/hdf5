package hdf5

// Translated from src/H5Dlayout.c (read side): selecting the I/O
// operations for a dataset's layout and reading the layout, filter
// pipeline and external file list messages when a dataset is opened.

// h5dLayoutOps is H5D_layout_ops_t: the operations of a storage layout
// class used for reading (H5D_LOPS_COMPACT, H5D_LOPS_CONTIG, H5D_LOPS_EFL,
// H5D_LOPS_CHUNK, H5D_LOPS_VIRTUAL).
type h5dLayoutOps struct {
	name string
	/* Initialize layout information */
	init func(f *File, dset *h5dShared) error
	/* Query routine to determine if storage is allocated */
	isSpaceAlloc func(dset *h5dShared) bool
	/* High-level I/O routine for reading data (multi_read / ser_read) */
	read func(f *File, dinfo *h5dDsetIoInfo) error
}

// h5dLayoutSetIoOps is H5D__layout_set_io_ops: set the I/O operations for
// the dataset's layout (and the chunk index operations for a chunked
// dataset).
func h5dLayoutSetIoOps(dataset *h5dShared) error {
	/* Set the I/O functions for each layout type */
	switch dataset.layout.typ {
	case h5dContiguous:
		if dataset.dcplCache.efl.nused > 0 {
			dataset.layoutOps = h5dLopsEfl()
		} else {
			dataset.layoutOps = h5dLopsContig()
		}

	case h5dChunked:
		dataset.layoutOps = h5dLopsChunk()

		/* Set the chunk operations */
		switch dataset.layout.chunkIdxType {
		case h5dChunkIdxBtree:
			dataset.chunkSt.ops = h5dCopsBtree()
		case h5dChunkIdxNone:
			dataset.chunkSt.ops = h5dCopsNone()
		case h5dChunkIdxSingle:
			dataset.chunkSt.ops = h5dCopsSingle()
		case h5dChunkIdxFarray:
			dataset.chunkSt.ops = h5dCopsFarray()
		case h5dChunkIdxEarray:
			dataset.chunkSt.ops = h5dCopsEarray()
		case h5dChunkIdxBt2:
			dataset.chunkSt.ops = h5dCopsBt2()
		default:
			return errorf(H5E_DATASET, H5E_UNSUPPORTED, "unknown chunk index method")
		}

	case h5dCompact:
		dataset.layoutOps = h5dLopsCompact()

	case h5dVirtual:
		dataset.layoutOps = h5dLopsVirtual()

	default:
		return errorf(H5E_DATASET, H5E_UNSUPPORTED, "unknown storage method")
	}
	return nil
}

// h5dLayoutOhRead is H5D__layout_oh_read: read the filter pipeline, layout
// and external file list messages of the dataset and initialize the layout.
func (f *File) h5dLayoutOhRead(dataset *h5dShared) error {
	oh := dataset.oh

	/* Get the optional filters message */
	if f.msgExists(oh, h5oPlineID) {
		/* Retrieve the I/O pipeline message */
		v, err := f.msgRead(oh, h5oPlineID)
		if err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't retrieve message")
		}
		pline, ok := v.(*h5oPline)
		if !ok {
			return errorf(H5E_DATASET, H5E_CANTGET, "can't retrieve message")
		}
		dataset.dcplCache.pline = *pline
		if dataset.dcplCache.pline.nused > len(dataset.dcplCache.pline.filter) {
			return errorf(H5E_DATASET, H5E_BADVALUE, "invalid number of filters in pipeline")
		}
	}

	/*
	 * Get the raw data layout info.  It's actually stored in two locations:
	 * the storage message of the dataset (dataset->storage) and certain
	 * values are copied to the dataset create plist so the user can query
	 * them.
	 */
	v, err := f.msgRead(oh, h5oLayoutID)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to read data layout message")
	}
	layout, ok := v.(*h5oLayout)
	if !ok {
		return errorf(H5E_DATASET, H5E_CANTINIT, "unable to read data layout message")
	}
	/* (copy the message: the chunk dimensions are adjusted below) */
	dataset.layout = *layout
	dataset.layout.chunkDim = append([]uint64(nil), layout.chunkDim...)

	/* (H5O_msg_read sets up the layout operations with H5D__layout_set_io_ops) */
	if err := h5dLayoutSetIoOps(dataset); err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to set layout I/O operations")
	}

	/* Check for external file list message (which might not exist) */
	if f.msgExists(oh, h5oEflID) {
		/* Retrieve the EFL  message */
		v, err := f.msgRead(oh, h5oEflID)
		if err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't retrieve message")
		}
		efl, ok := v.(*h5oEfl)
		if !ok {
			return errorf(H5E_DATASET, H5E_CANTGET, "can't retrieve message")
		}
		dataset.dcplCache.efl = *efl
		if efl.nused > len(efl.slot) {
			return errorf(H5E_DATASET, H5E_BADVALUE, "invalid external file list")
		}

		/* Set the dataset's I/O operations */
		dataset.layoutOps = h5dLopsEfl()
	}

	/* Sanity check that the layout operations are set up */
	if dataset.layoutOps == nil {
		return errorf(H5E_DATASET, H5E_CANTINIT, "layout operations not set up")
	}

	/* Initialize the layout information for the dataset */
	if dataset.layoutOps.init != nil {
		if err := dataset.layoutOps.init(f, dataset); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to initialize layout information")
		}
	}

	/* Adjust chunk dimensions to omit datatype size (in last dimension) for creation property */
	/* Set chunk sizes */
	if dataset.layout.typ == h5dChunked {
		if err := h5dChunkSetSizes(dataset); err != nil {
			return wrapf(err, H5E_DATASET, H5E_BADVALUE, "unable to set chunk sizes")
		}
	}
	return nil
}
