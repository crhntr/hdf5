package hdf5

// Translated from src/H5HFtiny.c (read side only: "tiny" objects, stored
// directly in the heap ID).

// Tiny object length information
const (
	h5hfTinyLenShort  = 16     /* Max. length able to be encoded in first heap ID byte */
	h5hfTinyMaskShort = 0x0F   /* Mask for length in first heap ID byte                */
	h5hfTinyMaskExt   = 0x0FFF /* Mask for length in two heap ID bytes                 */
	h5hfTinyMaskExt1  = 0x0F00 /* Mask for length in first byte of two heap ID bytes   */
	h5hfTinyMaskExt2  = 0x00FF /* Mask for length in second byte of two heap ID bytes  */
)

// h5hfTinyInit is H5HF__tiny_init: initialize information for tracking
// 'tiny' objects.
func h5hfTinyInit(hdr *h5hfHdr) {
	/* Compute information about 'tiny' objects for the heap */

	/* Check if tiny objects need an extra byte for their length */
	/* (account for boundary condition when length of an object would need an
	 *  extra byte, but using that byte means that the extra length byte is
	 *  unnecessary)
	 */
	idLen := uint32(hdr.idLen)
	if idLen-1 <= h5hfTinyLenShort {
		hdr.tinyMaxLen = idLen - 1
		hdr.tinyLenExtended = false
	} else if idLen-1 == h5hfTinyLenShort+1 {
		hdr.tinyMaxLen = h5hfTinyLenShort
		hdr.tinyLenExtended = false
	} else {
		hdr.tinyMaxLen = idLen - 2
		hdr.tinyLenExtended = true
	}
}

// h5hfTinyGetObjLen is H5HF__tiny_get_obj_len: get the size of a 'tiny'
// object in a fractal heap.
func h5hfTinyGetObjLen(hdr *h5hfHdr, id []byte) (uint64, error) {
	var encObjSize uint64

	/* Check if 'tiny' object ID is in extended form, and retrieve encoded size */
	if !hdr.tinyLenExtended {
		if len(id) < 1 {
			return 0, errorf(H5E_HEAP, H5E_BADVALUE, "heap ID too short for a tiny object")
		}
		encObjSize = uint64(id[0] & h5hfTinyMaskShort)
	} else {
		if len(id) < 2 {
			return 0, errorf(H5E_HEAP, H5E_BADVALUE, "heap ID too short for a tiny object")
		}
		encObjSize = uint64(id[1]) | (uint64(id[0])<<8)&h5hfTinyMaskExt1
	}

	/* Set the object's length */
	return encObjSize + 1, nil
}

// h5hfTinyOpReal is H5HF__tiny_op_real: internal routine to perform
// operation on 'tiny' object. op receives a slice of id.
func h5hfTinyOpReal(hdr *h5hfHdr, id []byte, op func(obj []byte) error) error {
	/* Get the object's encoded length */
	encObjSize, err := h5hfTinyGetObjLen(hdr, id)
	if err != nil {
		return err
	}

	/* Advance past flag byte(s) */
	start := uint64(1)
	if hdr.tinyLenExtended {
		start = 2
	}

	/* (the object must be inside the heap ID) */
	if start+encObjSize > uint64(len(id)) {
		return errorf(H5E_HEAP, H5E_BADRANGE, "tiny object of %d bytes overruns heap ID of %d bytes", encObjSize, len(id))
	}

	/* Call the user's 'op' callback */
	if err := op(id[start : start+encObjSize]); err != nil {
		return wrapf(err, H5E_HEAP, h5hfECantOperate, "application's callback failed")
	}
	return nil
}

// h5hfTinyRead is H5HF__tiny_read: read (a copy of) a 'tiny' object from
// the heap.
func h5hfTinyRead(hdr *h5hfHdr, id []byte) ([]byte, error) {
	var obj []byte

	/* Call the internal 'op' routine */
	err := h5hfTinyOpReal(hdr, id, func(b []byte) error {
		obj = append([]byte(nil), b...)
		return nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, h5hfECantOperate, "unable to operate on heap object")
	}
	return obj, nil
}

// h5hfTinyOp is H5HF__tiny_op: operate directly on a 'tiny' object.
func h5hfTinyOp(hdr *h5hfHdr, id []byte, op func(obj []byte) error) error {
	/* Call the internal 'op' routine routine */
	if err := h5hfTinyOpReal(hdr, id, op); err != nil {
		return wrapf(err, H5E_HEAP, h5hfECantOperate, "unable to operate on heap object")
	}
	return nil
}
