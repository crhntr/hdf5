package hdf5

// Translated from src/H5Dfill.c (read side): filling a selection of a
// buffer, or a whole chunk buffer, with the dataset's fill value.
//
// The fill value is kept in the dataset's (file) datatype and the buffers
// are in the file datatype too, so no conversion is involved: the fill
// value bytes are replicated. For variable-length types the replicated
// bytes are the disk form of the fill value (a global heap reference, or
// all zeros, the "null" sequence/string, for the default fill value), which
// decodes to the same value for every element, as the per-element
// conversion of H5D__fill_refill_vl produces.

import (
	"github.com/crhntr/hdf5/go/h5"
)

// h5dFill is H5D__fill: fill the elements of buf selected by space with
// the fill value fill (one element of the datatype dt; nil means zeros).
func h5dFill(fill []byte, dt *h5.Datatype, buf []byte, space *h5s) error {
	/* Get the memory datatype size */
	dstTypeSize := dt.Size
	if dstTypeSize == 0 {
		return errorf(H5E_DATASET, H5E_BADTYPE, "invalid datatype size")
	}

	/* If there's no fill value, just use zeros */
	var fillBuf []byte
	if fill == nil {
		fillBuf = make([]byte, dstTypeSize)
	} else {
		if uint64(len(fill)) < dstTypeSize {
			return errorf(H5E_DATASET, H5E_BADVALUE, "fill value size doesn't match dataset's datatype size")
		}
		fillBuf = fill[:dstTypeSize]
	}

	/* Fill the selection in the memory buffer */
	if err := h5sSelectFill(fillBuf, dstTypeSize, space, buf); err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "filling selection failed")
	}
	return nil
}

// h5sSelectFill is H5S_select_fill (src/H5Sselect.c, used only by the
// dataset code): replicate the fillSize-byte fill value into every element
// of buf selected by space.
func h5sSelectFill(fill []byte, fillSize uint64, space *h5s, buf []byte) error {
	if !h5sSelectValid(space) {
		return errorf(H5E_DATASPACE, H5E_BADRANGE, "selection is not within the extent")
	}
	/* Loop, while elements left in selection */
	for s := range space.iterSeqs(fillSize) {
		end := s.off + s.len
		if end < s.off || end > uint64(len(buf)) {
			return errorf(H5E_DATASPACE, H5E_OVERFLOW, "selection out of range of the buffer")
		}
		/* Fill each sequence in memory with fill value */
		h5vmArrayFill(buf[s.off:end], fill)
	}
	return nil
}

// h5vmArrayFill is H5VM_array_fill: replicate elmt over dst (whose length
// is a multiple of len(elmt)).
func h5vmArrayFill(dst, elmt []byte) {
	if len(elmt) == 0 || len(dst) == 0 {
		return
	}
	/* Copy the first item */
	n := copy(dst, elmt)
	/* Initialize counters, etc. while compensating for first element copied */
	for n < len(dst) {
		n += copy(dst[n:], dst[:n])
	}
}

// h5dFillInit is the part of H5D__fill_init used for reading: the fill
// value of one element (the user's fill value, or zeros when the fill value
// is the library default or undefined).
func h5dFillInit(fill *h5oFill, dt *h5.Datatype) ([]byte, error) {
	/* Fill the buffer with the user's fill value */
	if fill.buf != nil {
		/* Detect whether the datatype has a VL component */
		/* (The fill value is in the dataset's datatype already) */
		if uint64(len(fill.buf)) < dt.Size {
			return nil, errorf(H5E_DATASET, H5E_BADVALUE, "fill value size doesn't match dataset's datatype size")
		}
		return append([]byte(nil), fill.buf[:dt.Size]...), nil
	}
	/* Fill the buffer with the default fill value */
	return make([]byte, dt.Size), nil
}

// h5dFillChunk fills a chunk buffer with the fill value (H5D__fill_init on
// the chunk buffer followed by H5D__fill_refill_vl for vlen types).
func h5dFillChunk(fill *h5oFill, dt *h5.Datatype, chunk []byte) error {
	elmt, err := h5dFillInit(fill, dt)
	if err != nil {
		return err
	}
	h5vmArrayFill(chunk[:uint64(len(chunk))/dt.Size*dt.Size], elmt)
	return nil
}
