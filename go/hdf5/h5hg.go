package hdf5

// Translated from src/H5HG.c (read path only).

// h5hgT is H5HG_t: a reference to an object in a global heap collection.
type h5hgT struct {
	addr haddr  /* address of collection */
	idx  uint32 /* object ID within collection */
}

// h5hgHeapIDSize is H5HG_HEAP_ID_SIZE(F): the encoded size of a global heap
// ID (collection address + 32-bit object index).
func (f *File) h5hgHeapIDSize() int { return int(f.sizeofAddr) + 4 }

// hgProtect is H5HG__protect: load (through the metadata cache) the global
// heap collection at addr.
func (f *File) hgProtect(addr haddr) (*h5hgHeap, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "undefined global heap address")
	}

	/* Lock the heap into memory */
	v, err := f.cached("H5HG", addr, func() (any, error) {
		heap, err := f.hgCacheHeapLoad(addr)
		if err != nil {
			return nil, err
		}
		/* Set the heap's address */
		heap.addr = addr
		return heap, nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect global heap")
	}
	return v.(*h5hgHeap), nil
}

// hgObject returns the descriptor of object hobj after the checks shared by
// H5HG_read and H5HG_get_obj_size.
func (f *File) hgObject(hobj h5hgT) (*h5hgHeap, h5hgObj, error) {
	/* Heap object idx 0 is the free space in the heap and should never be given out */
	if 0 == hobj.idx {
		return nil, h5hgObj{}, errorf(H5E_HEAP, H5E_BADVALUE, "bad heap index, heap object = {%x, %d}", hobj.addr, hobj.idx)
	}

	/* Load the heap */
	heap, err := f.hgProtect(hobj.addr)
	if err != nil {
		return nil, h5hgObj{}, err
	}

	/* Sanity check the heap object */
	if uint64(hobj.idx) >= heap.nused {
		return nil, h5hgObj{}, errorf(H5E_HEAP, H5E_BADVALUE, "bad heap index, heap object = {%x, %d}", hobj.addr, hobj.idx)
	}
	obj := heap.obj[hobj.idx]
	if obj.begin < 0 {
		return nil, h5hgObj{}, errorf(H5E_HEAP, H5E_BADVALUE, "bad heap pointer, heap object = {%x, %d}", hobj.addr, hobj.idx)
	}
	return heap, obj, nil
}

// hgGetObjSize is H5HG_get_obj_size: the size of the global heap object
// hobj.
func (f *File) hgGetObjSize(hobj h5hgT) (uint64, error) {
	_, obj, err := f.hgObject(hobj)
	if err != nil {
		return 0, err
	}

	/* Set object size */
	return obj.size, nil
}

// hgRead is H5HG_read: return a copy of the bytes of the global heap object
// hobj. Index 0 (the free space object) and unknown indices are errors.
func (f *File) hgRead(hobj h5hgT) ([]byte, error) {
	heap, obj, err := f.hgObject(hobj)
	if err != nil {
		return nil, err
	}

	size := obj.size
	p := uint64(obj.begin) + f.h5hgSizeofObjhdr()
	if p+size > uint64(len(heap.chunk)) || p+size < p {
		return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "heap object extends beyond the collection, heap object = {%x, %d}", hobj.addr, hobj.idx)
	}

	/* Allocate a buffer for the object read in */
	object := make([]byte, size)
	copy(object, heap.chunk[p:p+size])
	return object, nil
}
