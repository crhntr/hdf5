package hdf5

// Translated from src/H5Dfill.c (read side): filling a selection of a
// buffer with the dataset's fill value.
//
// The fill value is kept in the dataset's (file) datatype and the buffers
// are in the file datatype too, so no conversion is involved: the fill
// value bytes are replicated. For variable-length types the replicated
// bytes are the disk form of the fill value (a global heap reference, or
// all zeros, the "null" sequence/string, for the default fill value), which
// decodes to the same value for every element, as the conversion in
// H5D__fill produces.
//
// Chunks that were never written are filled through H5D__fill_init and
// H5D__fill_refill_vl instead (H5D__chunk_lock, H5D__nonexistent_readvv):
// for a user-defined fill value of a type with variable-length components
// the refill converts every element back to the file's datatype, which
// writes the sequences to the global heap and so fails on a file opened
// read-only (h5dFillRefillVl).

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
// is the library default or undefined), and whether a user-defined fill
// value has a variable-length component (fb_info->has_vlen_fill_type).
func h5dFillInit(fill *h5oFill, dt *h5.Datatype) (elmt []byte, hasVlenFillType bool, err error) {
	/* Fill the buffer with the user's fill value */
	if fill.buf != nil {
		/* Detect whether the datatype has a VL component */
		hasVlenFillType = h5tDetectClass(dt, h5.ClassVlen)

		/* (The fill value is in the dataset's datatype already) */
		if uint64(len(fill.buf)) < dt.Size {
			return nil, false, errorf(H5E_DATASET, H5E_BADVALUE, "fill value size doesn't match dataset's datatype size")
		}
		return append([]byte(nil), fill.buf[:dt.Size]...), hasVlenFillType, nil
	}
	/* Fill the buffer with the default fill value */
	return make([]byte, dt.Size), false, nil
}

// h5dFillRefillVl is H5D__fill_refill_vl on a file opened read-only: the
// fill value fill (one element of dt, in the file's form) is converted to
// memory and then, for every element of the buffer, back to the file's
// datatype. Converting back writes every non-null variable-length
// component to the global heap (H5T__vlen_disk_write -> H5HG_insert),
// which fails without write intent on the file; a fill value whose
// variable-length components are all null converts without touching the
// heap.
func h5dFillRefillVl(f *File, dt *h5.Datatype, fill []byte) error {
	if h5dFillVlWritesHeap(dt, fill, int(f.sizeofAddr)) {
		return errorf(H5E_DATASET, H5E_CANTCONVERT, "can't refill fill value buffer: no write intent on file")
	}
	return nil
}

// h5dFillVlWritesHeap reports whether converting the element buf of dt
// from memory back to the file's form (H5T__conv_vlen) stores a
// variable-length sequence or string in the global heap. A disk vlen
// whose heap address is not nil (H5T__vlen_disk_isnull) becomes a non-null
// memory string, or a memory sequence that is non-null when it has at
// least one element (H5T__vlen_mem_seq_isnull), and that is written back
// with H5T__vlen_disk_write.
func h5dFillVlWritesHeap(dt *h5.Datatype, buf []byte, sizeofAddr int) bool {
	if dt == nil {
		return false
	}
	switch dt.Class {
	case h5.ClassVlen:
		/* (4-byte sequence length, heap address, 4-byte heap index) */
		if len(buf) < 4+sizeofAddr {
			return false
		}
		seqLen := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
		isNull := true
		for _, b := range buf[4 : 4+sizeofAddr] {
			if b != 0 {
				isNull = false
				break
			}
		}
		if isNull {
			return false
		}
		return dt.VlenKind == h5.VlenString || seqLen > 0
	case h5.ClassCompound:
		for _, m := range dt.Members {
			if m.Type == nil || m.Offset >= uint64(len(buf)) {
				continue
			}
			end := min(m.Offset+m.Type.Size, uint64(len(buf)))
			if h5dFillVlWritesHeap(m.Type, buf[m.Offset:end], sizeofAddr) {
				return true
			}
		}
	case h5.ClassArray:
		if dt.Base == nil || dt.Base.Size == 0 {
			return false
		}
		for off := uint64(0); off+dt.Base.Size <= uint64(len(buf)); off += dt.Base.Size {
			if h5dFillVlWritesHeap(dt.Base, buf[off:off+dt.Base.Size], sizeofAddr) {
				return true
			}
		}
	}
	return false
}
