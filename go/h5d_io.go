package hdf5

// Translated from src/H5Dio.c (read side): H5D__read for one dataset.
//
// The read always targets a buffer of elements in the dataset's file
// datatype: there is no memory datatype and hence no datatype conversion
// (the canonical decoding of the elements, h5tDecodeValues, plays the role
// of the conversion). The memory dataspace is one-dimensional over the
// elements of the destination buffer: dRead uses an "all" selection of
// npoints elements, so the n-th selected file element (in selection order)
// lands in element n of the buffer; virtual datasets read their source
// datasets into sub-selections of that space.

import (
	"github.com/crhntr/hdf5/go/h5"
)

// h5dDsetIoInfo is H5D_dset_io_info_t: the information about one dataset
// for an I/O operation.
type h5dDsetIoInfo struct {
	dset *h5dShared

	fileSpace *h5s   /* file dataspace (the dataset's extent with the selection to read) */
	memSpace  *h5s   /* memory dataspace (one-dimensional over the elements of buf) */
	buf       []byte /* destination buffer */
	nelmts    uint64 /* number of elements selected in file & memory dataspaces */
	typeSize  uint64 /* size of an element (type_info.src_type_size) */

	/* Layout "readvv" routine and the storage it reads (H5D_storage_t) */
	readvv     h5dReadvvFunc
	contigAddr haddr  /* store->contig.dset_addr */
	contigSize uint64 /* store->contig.dset_size */
	compactBuf []byte /* store->compact.buf */
}

// memSlice returns the length bytes of the destination buffer at byte
// offset off.
func (dinfo *h5dDsetIoInfo) memSlice(off, length uint64) ([]byte, error) {
	end := off + length
	if end < off || end > uint64(len(dinfo.buf)) {
		return nil, errorf(H5E_DATASET, H5E_OVERFLOW, "memory sequence out of range of the destination buffer")
	}
	return dinfo.buf[off:end], nil
}

// h5dFileSpaceOf attaches the selection of fileSpace to (a copy of) the
// dataset's extent, checking that the dataspace has the dataset's rank and
// that the selection is within the extent (H5Dread's
// H5S_has_extent / H5S_SELECT_VALID checks).
func h5dFileSpaceOf(ds *h5dShared, fileSpace *h5s) (*h5s, error) {
	if fileSpace == nil {
		return h5sCopy(ds.space), nil
	}
	if fileSpace.extent.rank != ds.space.extent.rank {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "file dataspace rank %d does not match dataset rank %d", fileSpace.extent.rank, ds.space.extent.rank)
	}
	space := h5sCopy(ds.space)
	if fileSpace.sel != nil {
		sel := *fileSpace.sel
		space.sel = &sel
	}
	/* Check for valid selection */
	if !h5sSelectValid(space) {
		return nil, errorf(H5E_ARGS, H5E_BADRANGE, "selection + offset not within extent")
	}
	return space, nil
}

// dRead is H5Dread with the dataset's file datatype as the memory type and
// a contiguous memory buffer: it returns the raw bytes of the elements
// selected by fileSpace (in selection order). A nil fileSpace reads the
// whole dataset; otherwise fileSpace must have the dataset's rank (its
// extent is replaced by the dataset's).
func (f *File) dRead(ds *h5dShared, fileSpace *h5s) ([]byte, error) {
	if ds == nil {
		return nil, errorf(H5E_ARGS, H5E_BADTYPE, "not a dataset")
	}
	fs, err := h5dFileSpaceOf(ds, fileSpace)
	if err != nil {
		return nil, err
	}

	/* Allocate the destination buffer */
	nelmts := h5sGetSelectNpoints(fs)
	size, err := h5dMulSize(nelmts, ds.typ.Size)
	if err != nil {
		return nil, err
	}
	if size > uint64(int(^uint(0)>>1)) {
		return nil, errorf(H5E_DATASET, H5E_OVERFLOW, "selection too large to read (%d bytes)", size)
	}
	buf := make([]byte, size)
	if nelmts == 0 {
		return buf, nil
	}

	/* The memory dataspace: all of nelmts elements */
	memSpace, err := h5sCreateSimple([]uint64{nelmts}, nil)
	if err != nil {
		return nil, err
	}
	if err := f.h5dRead(ds, fs, memSpace, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// dReadValues is dRead followed by the conversion of the elements to
// canonical values (h5/value.go).
func (f *File) dReadValues(ds *h5dShared, fileSpace *h5s) ([]any, error) {
	buf, err := f.dRead(ds, fileSpace)
	if err != nil {
		return nil, err
	}
	n := uint64(len(buf)) / ds.typ.Size
	vals, err := h5tDecodeValues(f, ds.typ, buf, n)
	if err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTCONVERT, "datatype conversion failed")
	}
	return vals, nil
}

// dReadHyperslab reads the regular hyperslab hs of the dataset
// (H5Sselect_hyperslab on the dataset's dataspace + dReadValues).
func (f *File) dReadHyperslab(ds *h5dShared, hs h5.Hyperslab) ([]any, error) {
	space, err := h5sSelectHyperslabPublic(ds.space, hs)
	if err != nil {
		return nil, err
	}
	return f.dReadValues(ds, space)
}

// h5dRead is H5D__read for a single dataset: read the elements selected by
// fileSpace (with the dataset's extent) into the elements of buf selected
// by memSpace (one-dimensional over the elements of buf).
func (f *File) h5dRead(dset *h5dShared, fileSpace, memSpace *h5s, buf []byte) error {
	dinfo := &h5dDsetIoInfo{
		dset:      dset,
		fileSpace: fileSpace,
		memSpace:  memSpace,
		buf:       buf,
		typeSize:  dset.typ.Size,
	}

	/* Make certain that the number of elements in each selection is the same, and cache nelmts in
	 * dset_info */
	dinfo.nelmts = h5sGetSelectNpoints(memSpace)
	if dinfo.nelmts != h5sGetSelectNpoints(fileSpace) {
		return errorf(H5E_ARGS, H5E_BADVALUE, "src and dest dataspaces have different number of elements selected")
	}
	if memSpace.extent.rank != 1 && dinfo.nelmts > 0 {
		return errorf(H5E_ARGS, H5E_BADVALUE, "memory dataspace must be one-dimensional")
	}
	if dinfo.nelmts > 0 {
		need, err := h5dMulSize(memSpace.extent.nelem, dinfo.typeSize)
		if err != nil || need > uint64(len(buf)) {
			return errorf(H5E_ARGS, H5E_BADVALUE, "destination buffer too small for the memory dataspace")
		}
	}

	/* If space hasn't been allocated and not using external storage,
	 * return fill value to buffer if fill time is upon allocation, or
	 * do nothing if fill time is never.  If the dataset is compact and
	 * fill time is NEVER, there is no way to tell whether part of data
	 * has been overwritten.  So just proceed in reading.
	 */
	if dinfo.nelmts > 0 && dset.dcplCache.efl.nused == 0 && !dset.layoutOps.isSpaceAlloc(dset) {
		fill := &dset.dcplCache.fill

		/* Retrieve dataset's fill-value properties */
		fillStatus := h5pIsFillValueDefined(fill)

		/* Should be impossible, but check anyway... */
		if fillStatus == h5dFillValueUndefined && (fill.fillTime == h5dFillTimeAlloc || fill.fillTime == h5dFillTimeIfset) {
			return errorf(H5E_DATASET, H5E_READERROR, "read failed: dataset doesn't exist, no data can be read")
		}

		/* If we're never going to fill this dataset, just leave the junk in the user's buffer */
		if fill.fillTime != h5dFillTimeNever {
			/* Go fill the user's selection with the dataset's fill value */
			if err := h5dFill(fill.buf, dset.typ, buf, memSpace); err != nil {
				return wrapf(err, H5E_DATASET, H5E_READERROR, "filling buf failed")
			}
		}

		/* No need to perform any more I/O for this dataset */
		return nil
	}
	if dinfo.nelmts == 0 {
		return nil
	}

	/* Invoke correct "high level" I/O routine */
	if err := dset.layoutOps.read(f, dinfo); err != nil {
		return wrapf(err, H5E_DATASET, H5E_READERROR, "can't read data")
	}
	return nil
}
