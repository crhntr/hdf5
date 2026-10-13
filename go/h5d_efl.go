package hdf5

// Translated from src/H5Defl.c (read side): contiguous datasets whose raw
// data lives in a list of external files (the External Data Files
// message). The dataset's address space is the concatenation of the
// segments {offset, size} of the external files, in list order.

import (
	"io"
	"os"
)

// h5oEflUnlimited is H5O_EFL_UNLIMITED: the size of an external file
// segment that extends to the end of the file.
const h5oEflUnlimited = ^uint64(0)

// h5dLopsEfl is H5D_LOPS_EFL: external file list storage I/O ops.
func h5dLopsEfl() *h5dLayoutOps {
	return &h5dLayoutOps{
		name:         "external",
		init:         h5dEflInit,
		isSpaceAlloc: h5dEflIsSpaceAlloc,
		read:         h5dEflRead,
	}
}

// h5oEflTotalSize is H5O_efl_total_size: the total size of the external
// storage (H5O_EFL_UNLIMITED when the last segment is unlimited).
func h5oEflTotalSize(efl *h5oEfl) (uint64, error) {
	if efl.nused > 0 && efl.slot[efl.nused-1].size == h5oEflUnlimited {
		return h5oEflUnlimited, nil
	}
	var totalSize uint64
	for u := 0; u < efl.nused; u++ {
		tmp := totalSize + efl.slot[u].size
		if tmp < totalSize {
			return 0, errorf(H5E_EFL, H5E_OVERFLOW, "total external storage size overflowed")
		}
		totalSize = tmp
	}
	return totalSize, nil
}

// h5dEflInit is H5D__efl_init: initialize the info for an EFL dataset.
func h5dEflInit(_ *File, dset *h5dShared) error {
	/* Retrieve the size of the dataset's datatype */
	dtSize := dset.typ.Size
	if dtSize == 0 {
		return errorf(H5E_DATASET, H5E_CANTINIT, "unable to determine datatype size")
	}

	/* Retrieve the number of elements in the dataspace */
	nelmts := h5sGetSimpleExtentNpoints(dset.space)

	/* Compute the size of the dataset's contiguous storage */
	/* Check for overflow during multiplication */
	dataSize, err := h5dMulSize(nelmts, dtSize)
	if err != nil {
		return err
	}

	/* Check for storage overflows */
	maxStorage, err := h5oEflTotalSize(&dset.dcplCache.efl)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to retrieve size of external file")
	}
	if maxStorage != h5oEflUnlimited && dataSize > maxStorage {
		return errorf(H5E_DATASET, H5E_CANTINIT, "dataspace size exceeds external storage size")
	}
	return nil
}

// h5dEflIsSpaceAlloc is H5D__efl_is_space_alloc: external storage is
// always "allocated".
func h5dEflIsSpaceAlloc(*h5dShared) bool { return true }

// h5dEflOpenFile opens the external file name: H5_combine_path with the
// dataset's external file prefix, then HDopen. As in libhdf5, without a
// prefix a relative name is relative to the current directory only.
func h5dEflOpenFile(dset *h5dShared, name string) (*os.File, error) {
	fullName := h5dCombinePath(dset.extfilePrefix, name)
	fh, err := os.Open(fullName)
	if err != nil {
		return nil, wrapf(err, H5E_EFL, H5E_CANTOPENFILE, "unable to open external raw data file")
	}
	return fh, nil
}

// h5dEflReadData is H5D__efl_read: read size bytes at logical address addr
// of the external storage into buf.
func h5dEflReadData(efl *h5oEfl, dset *h5dShared, addr uint64, buf []byte) error {
	/* Find the first efl member from which to read */
	var (
		u    int
		cur  uint64
		skip uint64
	)
	for u = 0; u < efl.nused; u++ {
		if efl.slot[u].size == h5oEflUnlimited || addr < cur+efl.slot[u].size {
			skip = addr - cur
			break
		}
		cur += efl.slot[u].size
	}

	/* Read the data */
	for len(buf) > 0 {
		if u >= efl.nused {
			return errorf(H5E_EFL, H5E_OVERFLOW, "read past logical end of file")
		}
		slot := &efl.slot[u]
		/* (H5F_OVERFLOW_HSIZET2OFFT((hsize_t)offset + skip): the sum,
		 *  computed modulo 2^64, must be a valid HDoff_t; a negative
		 *  segment offset is accepted when the position in the segment
		 *  brings it back into range) */
		fileOff := slot.offset + int64(skip)
		if fileOff < 0 {
			return errorf(H5E_EFL, H5E_OVERFLOW, "external file address overflowed")
		}
		fh, err := h5dEflOpenFile(dset, slot.name)
		if err != nil {
			return err
		}
		toRead := min(slot.size-skip, uint64(len(buf)))

		/* Inner loop - read to_read bytes from a single external file */
		n, err := fh.ReadAt(buf[:toRead], fileOff)
		fh.Close()
		if err != nil && err != io.EOF {
			return wrapf(err, H5E_EFL, H5E_READERROR, "read error in external raw data file")
		}
		/* End of file on disk, fill the remaining sectors to be read from this file with 0 */
		clear(buf[n:toRead])

		/* Prepare to advance to next external file */
		buf = buf[toRead:]
		skip = 0
		u++
	}
	return nil
}

// h5dEflIoInit is H5D__efl_io_init.
func h5dEflIoInit(dinfo *h5dDsetIoInfo) {
	dinfo.readvv = h5dEflReadvv
}

// h5dEflRead is the "ser_read" routine of external storage (H5D__select_read
// with H5D__efl_readvv).
func h5dEflRead(_ *File, dinfo *h5dDsetIoInfo) error {
	h5dEflIoInit(dinfo)
	return h5dSelectRead(dinfo)
}

// h5dEflReadvv is H5D__efl_readvv (H5D__efl_readvv_cb).
func h5dEflReadvv(dinfo *h5dDsetIoInfo, fileOff, memOff, length uint64) error {
	dst, err := dinfo.memSlice(memOff, length)
	if err != nil {
		return err
	}
	/* Read data */
	if err := h5dEflReadData(&dinfo.dset.dcplCache.efl, dinfo.dset, fileOff, dst); err != nil {
		return wrapf(err, H5E_DATASET, H5E_READERROR, "EFL read failed")
	}
	return nil
}
