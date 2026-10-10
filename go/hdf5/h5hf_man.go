package hdf5

// Translated from src/H5HFman.c (read side only: "managed" objects, stored
// in the heap's direct blocks).

// h5hfManIDDecode is H5HF_MAN_ID_DECODE: decode a "managed" heap ID into
// its flags, the object's offset within the heap and its length.
func h5hfManIDDecode(hdr *h5hfHdr, id []byte) (flags uint8, off, length uint64, err error) {
	p := &image{buf: id}
	if p.overflow(1 + int(hdr.heapOffSize) + int(hdr.heapLenSize)) {
		return 0, 0, 0, errorf(H5E_HEAP, H5E_BADVALUE, "heap ID too short for a managed object (%d bytes)", len(id))
	}
	flags = p.uint8()
	off = p.uintVar(int(hdr.heapOffSize))
	length = p.uintVar(int(hdr.heapLenSize))
	return flags, off, length, nil
}

// h5hfManGetObjLen is H5HF__man_get_obj_len: get the size of a managed heap
// object.
func h5hfManGetObjLen(hdr *h5hfHdr, id []byte) (uint64, error) {
	/* Skip over the flag byte and the object offset, retrieve the entry
	 * length */
	_, _, objLen, err := h5hfManIDDecode(hdr, id)
	return objLen, err
}

// h5hfManGetObjOff is H5HF__man_get_obj_off: get the offset of a managed
// heap object.
func h5hfManGetObjOff(hdr *h5hfHdr, id []byte) (uint64, error) {
	/* Skip over the flag byte, retrieve the object offset */
	_, objOff, _, err := h5hfManIDDecode(hdr, id)
	return objOff, err
}

// h5hfManOpReal is H5HF__man_op_real: internal routine to perform an
// operation on a managed heap object (read-only: H5HF_OP_MODIFY is not
// supported). op receives the object's bytes inside the cached direct
// block.
func h5hfManOpReal(hdr *h5hfHdr, id []byte, op func(obj []byte) error) error {
	dt := &hdr.manDtable

	/* Skip over the flag byte */
	/* Decode the object offset within the heap & its length */
	_, objOff, objLen, err := h5hfManIDDecode(hdr, id)
	if err != nil {
		return err
	}

	/* Check for bad offset or length */
	if objOff == 0 {
		return errorf(H5E_HEAP, H5E_BADRANGE, "invalid fractal heap offset")
	}
	if objOff > hdr.manSize {
		return errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap object offset too large")
	}
	if objLen == 0 {
		return errorf(H5E_HEAP, H5E_BADRANGE, "invalid fractal heap object size")
	}
	if objLen > dt.maxDirectSize {
		return errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap object size too large for direct block")
	}
	if objLen > uint64(hdr.maxManSize) {
		return errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap object should be standalone")
	}

	var dblock *h5hfDirect
	var dblockSize uint64

	/* Check for root direct block */
	if dt.curRootRows == 0 {
		/* Set direct block info */
		dblockAddr := dt.tableAddr
		dblockSize = dt.startBlockSize

		/* Lock direct block */
		if dblock, err = h5hfManDblockProtect(hdr, dblockAddr, dblockSize, nil, 0); err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap direct block")
		}
	} else {
		/* Look up indirect block containing direct block */
		iblock, entry, err := h5hfManDblockLocate(hdr, objOff)
		if err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTGET, "can't compute row & column of section")
		}

		/* Set direct block info */
		dblockAddr := iblock.ents[entry]
		dblockSize = dt.rowBlockSize[entry/uint32(dt.width)]

		/* Check for offset of invalid direct block */
		if !addrDefined(dblockAddr) {
			return errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap ID not in allocated direct block")
		}

		/* Lock direct block */
		if dblock, err = h5hfManDblockProtect(hdr, dblockAddr, dblockSize, iblock, entry); err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap direct block")
		}
	}

	/* Compute offset of object within block */
	if objOff < dblock.blockOff || objOff-dblock.blockOff >= dblockSize {
		return errorf(H5E_HEAP, H5E_BADRANGE, "fractal heap object offset outside of its direct block")
	}
	blkOff := objOff - dblock.blockOff

	/* Check for object's offset in the direct block prefix information */
	if blkOff < h5hfManAbsDirectOverhead(hdr) {
		return errorf(H5E_HEAP, H5E_BADRANGE, "object located in prefix of direct block")
	}

	/* Check for object's length overrunning the end of the direct block */
	if blkOff+objLen > dblockSize {
		return errorf(H5E_HEAP, H5E_BADRANGE, "object overruns end of direct block")
	}

	/* Call the user's 'op' callback */
	if err := op(dblock.blk[blkOff : blkOff+objLen]); err != nil {
		return wrapf(err, H5E_HEAP, h5hfECantOperate, "application's callback failed")
	}
	return nil
}

// h5hfManRead is H5HF__man_read: read (a copy of) an object from a managed
// heap.
func h5hfManRead(hdr *h5hfHdr, id []byte) ([]byte, error) {
	var obj []byte

	/* Call the internal 'op' routine routine */
	err := h5hfManOpReal(hdr, id, func(b []byte) error {
		obj = append([]byte(nil), b...)
		return nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, h5hfECantOperate, "unable to operate on heap object")
	}
	return obj, nil
}

// h5hfManOp is H5HF__man_op: operate directly on an object from a managed
// heap.
func h5hfManOp(hdr *h5hfHdr, id []byte, op func(obj []byte) error) error {
	/* Call the internal 'op' routine routine */
	if err := h5hfManOpReal(hdr, id, op); err != nil {
		return wrapf(err, H5E_HEAP, h5hfECantOperate, "unable to operate on heap object")
	}
	return nil
}
