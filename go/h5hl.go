package hdf5

// Translated from src/H5HL.c (read side only): local heaps.

// h5hlFree is H5HL_free_t: one node of a local heap's free list.
type h5hlFree struct {
	offset uint64 /* offset of free block         */
	size   uint64 /* size of free block           */
}

// h5hl is H5HL_t: a local heap (collection of NUL-terminated strings and
// other small objects referenced by offset).
type h5hl struct {
	addr     haddr  /* address of heap prefix (prfx_addr) */
	dblkAddr haddr  /* address of data block */
	dblkSize uint64 /* size of heap data block on disk and in mem */
	freeHead uint64 /* offset of first free block (free_block) */
	image    []byte /* the heap's data block (dblk_image) */

	sizeofSize     uint8      /* Size of file sizes */
	sizeofAddr     uint8      /* Size of file addresses */
	prfxSize       int        /* size of heap prefix */
	singleCacheObj bool       /* Indicate if the heap is a single object in the cache */
	freelist       []h5hlFree /* the free list */
}

// hlProtect is H5HL_protect: load (through the cache) the local heap at addr.
//
// The C function protects the prefix (H5AC_LHEAP_PRFX, a speculative load of
// H5HL_SPEC_READ_SIZE bytes trimmed by H5HL__cache_prefix_get_final_load_size)
// and then, if the data block is not contiguous with the prefix, the data
// block (H5AC_LHEAP_DBLK). Both loads are done here and the complete heap is
// cached under ("H5HL", addr).
func (f *File) hlProtect(addr haddr) (*h5hl, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix: undefined address")
	}
	v, err := f.cached("H5HL", addr, func() (any, error) {
		/* Construct the user data for protect callback */
		prfxUdata := &h5hlCachePrfxUd{
			sizeofSize: int(f.sizeofSize),
			sizeofAddr: int(f.sizeofAddr),
			prfxAddr:   addr,
			sizeofPrfx: h5hlSizeofHdr(int(f.sizeofAddr), int(f.sizeofSize)),
		}

		/* Protect the local heap prefix (speculative read, as H5C__load_entry
		 * does for H5AC__CLASS_SPECULATIVE_LOAD_FLAG clients: the initial
		 * read is clipped to the end of the file) */
		n := uint64(h5hlSpecReadSize)
		if eof := f.eof(); addr >= eof {
			return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix: address %d beyond end of file", addr)
		} else if addr+n > eof {
			n = eof - addr
		}
		buf, err := f.readAt(addr, int(n))
		if err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix")
		}
		actualLen, err := hlCachePrefixGetFinalLoadSize(buf, prfxUdata)
		if err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix")
		}
		if actualLen != len(buf) {
			if buf, err = f.readAt(addr, actualLen); err != nil {
				return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix")
			}
		}
		heap, err := hlCachePrefixDeserialize(buf, prfxUdata)
		if err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix")
		}

		/* Check if heap has separate data block */
		if !heap.singleCacheObj && heap.dblkSize > 0 {
			/* Protect the local heap data block */
			if heap.dblkSize > f.eof() {
				return nil, errorf(H5E_HEAP, H5E_CANTPROTECT, "unable to load heap data block: size %d exceeds file size", heap.dblkSize)
			}
			dbuf, err := f.readAt(heap.dblkAddr, int(heap.dblkSize))
			if err != nil {
				return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap data block")
			}
			if err := hlCacheDatablockDeserialize(heap, dbuf); err != nil {
				return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap data block")
			}
		}
		return heap, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*h5hl), nil
}

// offsetInto is H5HL_offset_into: the heap bytes starting at offset, or an
// error when the offset is outside the heap.
func (h *h5hl) offsetInto(offset uint64) ([]byte, error) {
	if offset >= h.dblkSize || offset >= uint64(len(h.image)) {
		return nil, errorf(H5E_HEAP, H5E_CANTGET, "unable to offset into local heap data block")
	}
	return h.image[offset:], nil
}

// stringAt returns the NUL-terminated string at offset (the common use of
// H5HL_offset_into for link names and soft link values). As in the C callers
// (H5MM_strndup(s, block_size - offset)) the string ends at the first NUL or
// at the end of the data block.
func (h *h5hl) stringAt(offset uint64) (string, error) {
	b, err := h.offsetInto(offset)
	if err != nil {
		return "", err
	}
	for i, c := range b {
		if c == 0 {
			return string(b[:i]), nil
		}
	}
	return string(b), nil
}

// heapGetSize is H5HL_heap_get_size: the current size of the heap's data
// block.
func (h *h5hl) heapGetSize() uint64 { return h.dblkSize }

// hlGetSize is H5HL_get_size: the size of the data block of the local heap
// at addr.
func (f *File) hlGetSize(addr haddr) (uint64, error) {
	heap, err := f.hlProtect(addr)
	if err != nil {
		return 0, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix")
	}
	return heap.dblkSize, nil
}

// hlHeapsize is H5HL_heapsize: the size in bytes (prefix plus data block) of
// the local heap at addr, for storage accounting.
func (f *File) hlHeapsize(addr haddr) (uint64, error) {
	heap, err := f.hlProtect(addr)
	if err != nil {
		return 0, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to load heap prefix")
	}
	return uint64(heap.prfxSize) + heap.dblkSize, nil
}
