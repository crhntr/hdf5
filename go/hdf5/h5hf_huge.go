package hdf5

// Translated from src/H5HFhuge.c (read side only: "huge" objects, stored
// directly in the file and located either by the heap ID itself or through
// a v2 B-tree).

// h5hfHugeInit is H5HF__huge_init: initialize information for tracking
// 'huge' objects.
func h5hfHugeInit(hdr *h5hfHdr) {
	/* Compute information about 'huge' objects for the heap */
	idLen := uint32(hdr.idLen)
	sizeofAddr := uint32(hdr.sizeofAddr)
	sizeofSize := uint32(hdr.sizeofSize)

	/* Check if we can completely hold the 'huge' object's offset & length in
	 *  the file in the heap ID (which will speed up accessing it) and we don't
	 *  have any I/O pipeline filters.
	 */
	if hdr.filterLen > 0 {
		if idLen-1 >= sizeofAddr+sizeofSize+4+sizeofSize {
			/* Indicate that v2 B-tree doesn't have to be used to locate object */
			hdr.hugeIDsDirect = true

			/* Set the size of 'huge' object IDs */
			hdr.hugeIDSize = uint8(sizeofAddr + sizeofSize + sizeofSize)
		} else {
			/* Indicate that v2 B-tree must be used to access object */
			hdr.hugeIDsDirect = false
		}
	} else {
		if sizeofAddr+sizeofSize <= idLen-1 {
			/* Indicate that v2 B-tree doesn't have to be used to locate object */
			hdr.hugeIDsDirect = true

			/* Set the size of 'huge' object IDs */
			hdr.hugeIDSize = uint8(sizeofAddr + sizeofSize)
		} else {
			/* Indicate that v2 B-tree must be used to locate object */
			hdr.hugeIDsDirect = false
		}
	}
	if !hdr.hugeIDsDirect {
		/* Set the size and maximum value of 'huge' object ID */
		if idLen-1 < 8 {
			hdr.hugeIDSize = uint8(idLen - 1)
			hdr.hugeMaxID = (uint64(1) << (uint(hdr.hugeIDSize) * 8)) - 1
		} else {
			hdr.hugeIDSize = 8
			hdr.hugeMaxID = ^uint64(0)
		}
	}
}

// h5hfHugeBt2Open opens the v2 B-tree tracking the heap's indirectly
// accessed 'huge' objects, if it is not open yet ("Check if v2 B-tree is
// open yet" in the C routines).
func h5hfHugeBt2Open(fh *h5hf) (*h5b2, error) {
	hdr := fh.hdr
	if fh.hugeBt2 == nil {
		/* Sanity check */
		if !addrDefined(hdr.hugeBt2Addr) {
			return nil, errorf(H5E_HEAP, H5E_BADVALUE, "fractal heap has no v2 B-tree for 'huge' objects")
		}

		cls := h5hfHugeBt2Indir()
		if hdr.filterLen > 0 {
			cls = h5hfHugeBt2FiltIndir()
		}

		/* Open existing v2 B-tree */
		bt2, err := fh.f.b2Open(hdr.hugeBt2Addr, cls, h5hfHugeBt2CrtContext(fh.f))
		if err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTOPENOBJ, "unable to open v2 B-tree for tracking 'huge' heap objects")
		}
		fh.hugeBt2 = bt2
	}
	return fh.hugeBt2, nil
}

// h5hfHugeLookupIndir looks up the record of an indirectly accessed,
// unfiltered 'huge' object by the ID decoded from p (the common
// "UINT64DECODE_VAR + H5B2_find(H5HF__huge_bt2_indir_found)" sequence).
func h5hfHugeLookupIndir(fh *h5hf, p *image) (*h5hfHugeBt2IndirRec, error) {
	bt2, err := h5hfHugeBt2Open(fh)
	if err != nil {
		return nil, err
	}

	/* Get ID for looking up 'huge' object in v2 B-tree */
	var searchRec h5hfHugeBt2IndirRec
	searchRec.id = p.uintVar(int(fh.hdr.hugeIDSize))
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_BADVALUE, "heap ID too short for a 'huge' object")
	}

	/* Look up object in v2 B-tree */
	var foundRec h5hfHugeBt2IndirRec
	found, err := bt2.find(&searchRec, func(rec any) error { return h5hfHugeBt2IndirFound(rec, &foundRec) })
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_NOTFOUND, "can't check for object in v2 B-tree")
	}
	if !found {
		return nil, errorf(H5E_HEAP, H5E_NOTFOUND, "can't find object in v2 B-tree")
	}
	return &foundRec, nil
}

// h5hfHugeLookupFiltIndir is h5hfHugeLookupIndir for filtered heaps
// (H5HF__huge_bt2_filt_indir_found).
func h5hfHugeLookupFiltIndir(fh *h5hf, p *image) (*h5hfHugeBt2FiltIndirRec, error) {
	bt2, err := h5hfHugeBt2Open(fh)
	if err != nil {
		return nil, err
	}

	/* Get ID for looking up 'huge' object in v2 B-tree */
	var searchRec h5hfHugeBt2FiltIndirRec
	searchRec.id = p.uintVar(int(fh.hdr.hugeIDSize))
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_BADVALUE, "heap ID too short for a 'huge' object")
	}

	/* Look up object in v2 B-tree */
	var foundRec h5hfHugeBt2FiltIndirRec
	found, err := bt2.find(&searchRec, func(rec any) error { return h5hfHugeBt2FiltIndirFound(rec, &foundRec) })
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_NOTFOUND, "can't check for object in v2 B-tree")
	}
	if !found {
		return nil, errorf(H5E_HEAP, H5E_NOTFOUND, "can't find object in v2 B-tree")
	}
	return &foundRec, nil
}

// h5hfHugeGetObjLen is H5HF__huge_get_obj_len: get the size of a 'huge'
// object in a fractal heap.
func h5hfHugeGetObjLen(fh *h5hf, id []byte) (uint64, error) {
	hdr := fh.hdr
	p := fh.f.newImage(id)

	/* Skip over the flag byte */
	p.skip(1)

	/* Check if 'huge' object ID encodes address & length directly */
	if hdr.hugeIDsDirect {
		if hdr.filterLen > 0 {
			/* Skip over filtered object info */
			p.skip(int(hdr.sizeofAddr) + int(hdr.sizeofSize) + 4)
		} else {
			/* Skip over object offset in file */
			p.skip(int(hdr.sizeofAddr))
		}

		/* Retrieve the object's length */
		objLen := p.length()
		if err := p.done(); err != nil {
			return 0, wrapf(err, H5E_HEAP, H5E_BADVALUE, "heap ID too short for a 'huge' object")
		}
		return objLen, nil
	}

	if hdr.filterLen > 0 {
		foundRec, err := h5hfHugeLookupFiltIndir(fh, p)
		if err != nil {
			return 0, err
		}

		/* Retrieve the object's length */
		return foundRec.objSize, nil
	}
	foundRec, err := h5hfHugeLookupIndir(fh, p)
	if err != nil {
		return 0, err
	}

	/* Retrieve the object's length */
	return foundRec.len, nil
}

// h5hfHugeGetObjOff is H5HF__huge_get_obj_off: get the offset (file
// address) of a 'huge' object in a fractal heap.
func h5hfHugeGetObjOff(fh *h5hf, id []byte) (uint64, error) {
	hdr := fh.hdr
	p := fh.f.newImage(id)

	/* Skip over the flag byte */
	p.skip(1)

	var objAddr haddr

	/* Check if 'huge' object ID encodes address & length directly */
	if hdr.hugeIDsDirect {
		/* Retrieve the object's address (common) */
		objAddr = p.addr()
		if err := p.done(); err != nil {
			return 0, wrapf(err, H5E_HEAP, H5E_BADVALUE, "heap ID too short for a 'huge' object")
		}
	} else if hdr.filterLen > 0 {
		foundRec, err := h5hfHugeLookupFiltIndir(fh, p)
		if err != nil {
			return 0, err
		}

		/* Retrieve the object's address & length */
		objAddr = foundRec.addr
	} else {
		foundRec, err := h5hfHugeLookupIndir(fh, p)
		if err != nil {
			return 0, err
		}

		/* Retrieve the object's address & length */
		objAddr = foundRec.addr
	}

	/* Set the value to return */
	return objAddr, nil
}

// h5hfHugeOpReal is H5HF__huge_op_real: internal routine to perform an
// operation on a 'huge' object (read-only). op receives a freshly read (and
// de-filtered) buffer.
func h5hfHugeOpReal(fh *h5hf, id []byte, op func(obj []byte) error) error {
	hdr := fh.hdr
	p := fh.f.newImage(id)

	var objAddr haddr
	var objSize uint64
	var filterMask uint32

	/* Skip over the flag byte */
	p.skip(1)

	/* Check for 'huge' object ID that encodes address & length directly */
	if hdr.hugeIDsDirect {
		/* Retrieve the object's address and length (common) */
		objAddr = p.addr()
		objSize = p.length()

		/* Retrieve extra information needed for filtered objects */
		if hdr.filterLen > 0 {
			filterMask = p.uint32()
		}
		if err := p.done(); err != nil {
			return wrapf(err, H5E_HEAP, H5E_BADVALUE, "heap ID too short for a 'huge' object")
		}
	} else if hdr.filterLen > 0 {
		foundRec, err := h5hfHugeLookupFiltIndir(fh, p)
		if err != nil {
			return err
		}

		/* Retrieve the object's address & length */
		objAddr = foundRec.addr
		objSize = foundRec.len
		filterMask = foundRec.filterMask
	} else {
		foundRec, err := h5hfHugeLookupIndir(fh, p)
		if err != nil {
			return err
		}

		/* Retrieve the object's address & length */
		objAddr = foundRec.addr
		objSize = foundRec.len
	}

	/* Set up buffer for reading */
	if objSize > fh.f.eof() {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "'huge' object size %d larger than the file", objSize)
	}

	/* Read the object's (possibly filtered) data from the file */
	readBuf, err := fh.f.readAt(objAddr, int(objSize))
	if err != nil {
		return wrapf(err, H5E_HEAP, H5E_READERROR, "can't read 'huge' object's data from the file")
	}

	/* Check for I/O pipeline filter on heap */
	if hdr.filterLen > 0 {
		/* De-filter the object */
		if readBuf, err = h5zPipeline(hdr.pline, filterMask, readBuf); err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTFILTER, "input filter failed")
		}
	}

	/* Call the user's 'op' callback */
	if err := op(readBuf); err != nil {
		return wrapf(err, H5E_HEAP, h5hfECantOperate, "application's callback failed")
	}
	return nil
}

// h5hfHugeRead is H5HF__huge_read: read a 'huge' object from the heap.
func h5hfHugeRead(fh *h5hf, id []byte) ([]byte, error) {
	var obj []byte

	/* Call the internal 'op' routine */
	err := h5hfHugeOpReal(fh, id, func(b []byte) error {
		/* (the buffer was freshly allocated by the read) */
		obj = b
		return nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, h5hfECantOperate, "unable to operate on heap object")
	}
	return obj, nil
}

// h5hfHugeOp is H5HF__huge_op: operate directly on a 'huge' object.
func h5hfHugeOp(fh *h5hf, id []byte, op func(obj []byte) error) error {
	/* Call the internal 'op' routine routine */
	if err := h5hfHugeOpReal(fh, id, op); err != nil {
		return wrapf(err, H5E_HEAP, h5hfECantOperate, "unable to operate on heap object")
	}
	return nil
}
