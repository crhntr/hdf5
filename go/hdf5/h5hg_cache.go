package hdf5

// Translated from src/H5HGcache.c (read side only) and the package macros
// of src/H5HGpkg.h.

import "bytes"

// H5HG_MAGIC / H5_SIZEOF_MAGIC
var h5hgMagic = []byte("GCOL")

const h5SizeofMagic = 4

// Global heap collection version.
const h5hgVersion = 1

// All global heap collections are at least this big.  This allows us to read
// most collections with a single read() since we don't have to read a few
// bytes of header to figure out the size.  If the heap is larger than this
// then a second read gets the rest after we've decoded the header.
const h5hgMinsize = 4096

// Pad all global heap messages to a multiple of eight bytes so we can load
// the entire collection into memory and operate on it there.
const h5hgAlignment = 8

// h5hgAlign is H5HG_ALIGN(X).
func h5hgAlign(x uint64) uint64 {
	return h5hgAlignment * ((x + h5hgAlignment - 1) / h5hgAlignment)
}

// h5hgIsaligned is H5HG_ISALIGNED(X).
func h5hgIsaligned(x uint64) bool { return x == h5hgAlign(x) }

// h5hgSizeofHdr is H5HG_SIZEOF_HDR(f): the size of the collection header,
// always a multiple of the alignment so that the stuff that follows the
// header is aligned.
func (f *File) h5hgSizeofHdr() uint64 {
	return h5hgAlign(4 + /*magic number		*/
		1 + /*version number	*/
		3 + /*reserved		*/
		uint64(f.sizeofSize)) /*collection size	*/
}

// h5hgSizeofObjhdr is H5HG_SIZEOF_OBJHDR(f): the overhead associated with
// each object in the heap, always a multiple of the alignment so that the
// stuff that follows the header is aligned.
func (f *File) h5hgSizeofObjhdr() uint64 {
	return h5hgAlign(2 + /*object id number	*/
		2 + /*reference count	*/
		4 + /*reserved		*/
		uint64(f.sizeofSize)) /*object data size	*/
}

// h5hgNobjs is H5HG_NOBJS(f, z): the initial guess for the number of
// messages in a collection.  We assume that all objects in that collection
// are zero length, giving the maximum possible number of objects in the
// collection.  The `+2' accounts for rounding and for the free space object.
func (f *File) h5hgNobjs(z uint64) uint64 {
	return (z-f.h5hgSizeofHdr())/f.h5hgSizeofObjhdr() + 2
}

// h5hgObj is H5HG_obj_t.
type h5hgObj struct {
	nrefs int    /* Reference count */
	size  uint64 /* Total size of object */
	begin int    /* Offset of the object in heap.chunk (INCLUDES header); -1 when unused */
}

// h5hgHeap is H5HG_heap_t.
type h5hgHeap struct {
	addr  haddr  /* collection address */
	size  uint64 /* total size of collection */
	chunk []byte /* the collection, incl. header */
	nused uint64 /* object slots used */
	obj   []h5hgObj
}

// hgHdrDeserialize is H5HG__hdr_deserialize: decode a global heap's header.
func (f *File) hgHdrDeserialize(heap *h5hgHeap, img []byte) error {
	p := f.newImage(img)

	/* Magic number */
	if p.overflow(h5SizeofMagic) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), h5hgMagic) {
		return errorf(H5E_HEAP, H5E_BADVALUE, "bad global heap collection signature")
	}

	/* Version */
	if p.overflow(1) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if h5hgVersion != p.uint8() {
		return errorf(H5E_HEAP, H5E_VERSION, "wrong version number in global heap")
	}

	/* Reserved */
	if p.overflow(3) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	p.skip(3)

	/* Size */
	if p.overflow(int(f.sizeofSize)) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	heap.size = p.length()
	if heap.size < h5hgMinsize {
		return errorf(H5E_HEAP, H5E_BADVALUE, "global heap size is too small")
	}
	return nil
}

// hgCacheHeapGetInitialLoadSize is H5HG__cache_heap_get_initial_load_size:
// the initial speculative read size.
func hgCacheHeapGetInitialLoadSize() uint64 { return h5hgMinsize }

// hgCacheHeapGetFinalLoadSize is H5HG__cache_heap_get_final_load_size:
// decode the header of a speculatively read heap and return the actual
// collection size.
func (f *File) hgCacheHeapGetFinalLoadSize(img []byte) (uint64, error) {
	var heap h5hgHeap

	/* Deserialize the heap's header */
	if err := f.hgHdrDeserialize(&heap, img); err != nil {
		return 0, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode global heap prefix")
	}

	/* Set the actual global heap size */
	return heap.size, nil
}

// hgCacheHeapLoad performs the metadata cache's load sequence for a global
// heap collection (H5C__load_entry with the H5AC_GHEAP class): a speculative
// read of the initial load size (trimmed to the end of the file), the final
// load size from the header, a read of the whole collection and
// deserialization.
func (f *File) hgCacheHeapLoad(addr haddr) (*h5hgHeap, error) {
	eof := f.eof()
	if addr >= eof {
		return nil, errorf(H5E_HEAP, H5E_CANTLOAD, "global heap address %d is beyond the end of file %d", addr, eof)
	}
	n := hgCacheHeapGetInitialLoadSize()
	if addr+n > eof || addr+n < addr {
		/* Speculative loads are trimmed to the end of the file */
		n = eof - addr
	}
	img, err := f.readAt(addr, int(n))
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTLOAD, "unable to read global heap")
	}
	actual, err := f.hgCacheHeapGetFinalLoadSize(img)
	if err != nil {
		return nil, err
	}
	if actual != uint64(len(img)) {
		if actual > eof-addr {
			return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "global heap collection size %d extends beyond the end of file", actual)
		}
		if img, err = f.readAt(addr, int(actual)); err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTLOAD, "unable to read global heap")
		}
	}
	return f.hgCacheHeapDeserialize(img)
}

// hgCacheHeapDeserialize is H5HG__cache_heap_deserialize: given a buffer
// containing the on disk image of the global heap, deserialize it into a
// new h5hgHeap.
func (f *File) hgCacheHeapDeserialize(img []byte) (*h5hgHeap, error) {
	if len(img) < h5hgMinsize {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "global heap image too small")
	}

	/* Allocate a new global heap */
	heap := &h5hgHeap{chunk: img}
	pEnd := uint64(len(heap.chunk)) // one past the end of the image buffer

	/* Deserialize the heap's header */
	hdrSize := f.h5hgSizeofHdr()
	if hdrSize > pEnd {
		return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if err := f.hgHdrDeserialize(heap, heap.chunk); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode global heap header")
	}
	if heap.size > pEnd {
		return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "global heap size exceeds image size")
	}

	/* Decode each object */
	objhdrSize := f.h5hgSizeofObjhdr()

	/* Set the p pointer to the objects in heap->chunk */
	p := hdrSize
	end := heap.size

	/* Set the number of allocated objects. Calloc the obj array because the
	 * file format spec makes no guarantee about the order of the objects,
	 * and unused slots must be set to zero. (Object IDs are 16-bit, so the
	 * array never needs more than 65536 slots.)
	 */
	nalloc := min(f.h5hgNobjs(heap.size), 1<<16)
	heap.obj = make([]h5hgObj, nalloc)
	for i := range heap.obj {
		heap.obj[i].begin = -1
	}
	var maxIdx uint64

	for p < end {
		if p+objhdrSize > end {
			/* The last bit of space is too tiny for an object header, so
			 * we assume that it's free space.
			 */
			if heap.obj[0].begin >= 0 {
				return nil, errorf(H5E_HEAP, H5E_BADVALUE, "object 0 should not be set")
			}
			heap.obj[0].size = end - p
			heap.obj[0].begin = int(p)

			/* No buffer overflow check here since this just moves the pointer
			 * to the end of the buffer, which was calculated above
			 */
			p += heap.obj[0].size
		} else {
			/* Parse a normal heap entry */
			begin := p /* start of object */
			q := f.newImage(heap.chunk)
			q.off = int(p)

			if q.overflow(2) {
				return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			idx := uint64(q.uint16())

			/* Check if we need more room to store heap objects */
			if idx >= uint64(len(heap.obj)) {
				/* Determine the new number of objects to index */
				newAlloc := max(uint64(len(heap.obj))*2, idx+1)
				if idx >= newAlloc {
					return nil, errorf(H5E_HEAP, H5E_BADVALUE, "inappropriate heap index")
				}
				newAlloc = min(newAlloc, 1<<16)

				/* Reallocate array of objects */
				for uint64(len(heap.obj)) < newAlloc {
					heap.obj = append(heap.obj, h5hgObj{begin: -1})
				}
				if uint64(len(heap.obj)) <= heap.nused {
					return nil, errorf(H5E_HEAP, H5E_BADVALUE, "inappropriate # allocated slots")
				}
			}

			/* Number of references */
			if q.overflow(2) {
				return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			heap.obj[idx].nrefs = int(q.uint16())

			/* Reserved bytes */
			if q.overflow(4) {
				return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			q.skip(4)

			/* Object length */
			if q.overflow(int(f.sizeofSize)) {
				return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			heap.obj[idx].size = q.length()

			/* Object
			 *
			 * Points to beginning of object, INCLUDING the header.
			 */
			heap.obj[idx].begin = int(begin)

			/* The total storage size includes the size of the object
			 * header and is zero padded so the next object header is
			 * properly aligned. The last bit of space is the free space
			 * object whose size is never padded and already includes the
			 * object header.
			 */
			var need uint64
			if idx > 0 {
				if heap.obj[idx].size > pEnd {
					return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				need = objhdrSize + h5hgAlign(heap.obj[idx].size)
				if idx > maxIdx {
					maxIdx = idx
				}
			} else {
				need = heap.obj[idx].size
				/* The free space object includes its header; a smaller size
				 * would not advance the parser (the C code loops forever) */
				if need < objhdrSize {
					return nil, errorf(H5E_HEAP, H5E_BADVALUE, "bad global heap free space size")
				}
			}

			/* Make sure the extra padding doesn't cause us to overrun
			 * the buffer
			 */
			if need > pEnd || begin+need > pEnd {
				return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			p = begin + need
		}
	}

	/* Post-parse checks */
	if p != end {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "partially decoded global heap")
	}
	if !h5hgIsaligned(heap.obj[0].size) {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "decoded global heap is not aligned")
	}

	/* Set the next index value to use when creating a new object */
	if maxIdx > 0 {
		heap.nused = maxIdx + 1
	} else {
		heap.nused = 1
	}

	if maxIdx >= heap.nused {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "bad `next unused` heap index value")
	}
	return heap, nil
}
