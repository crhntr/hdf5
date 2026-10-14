package hdf5

// Translated from src/H5HLcache.c (read side only): local heap prefix and
// data block deserialization. The C library caches the prefix and (when it
// is not contiguous with the prefix) the data block as two cache entries;
// here both are decoded by one load function and cached as one *h5hl (see
// hlProtect in h5hl.go).

import (
	"bytes"
	"math"
)

/****************/
/* Local Macros */
/****************/

const h5hlVersion = 0 /* Local heap collection version    */

// h5hlSpecReadSize is H5HL_SPEC_READ_SIZE: the local heap size to
// speculatively read in (needs to be more than the local heap prefix size to
// work at all and should be larger than the default local heap size to save
// the extra I/O operations).
const h5hlSpecReadSize = 512

// h5hlMagic is H5HL_MAGIC.
var h5hlMagic = []byte("HEAP")

// h5hlFreeNull is H5HL_FREE_NULL: value indicating end of free list on disk.
const h5hlFreeNull = 1

// h5hlAlign is H5HL_ALIGN(X): align on 8-byte boundary.
func h5hlAlign(x uint64) uint64 { return (x + 7) &^ 7 }

// h5hlSizeofHdr is H5HL_SIZEOF_HDR(F).
func h5hlSizeofHdr(sizeofAddr, sizeofSize int) int {
	return int(h5hlAlign(uint64(h5SizeofMagic + /* heap signature   */
		1 + /* version          */
		3 + /* reserved         */
		sizeofSize + /* data size        */
		sizeofSize + /* free list head   */
		sizeofAddr))) /* data address     */
}

// h5hlCachePrfxUd is H5HL_cache_prfx_ud_t: callback information for loading
// a local heap prefix from disk.
type h5hlCachePrfxUd struct {
	sizeofSize int   /* Size of file sizes */
	sizeofAddr int   /* Size of file addresses */
	prfxAddr   haddr /* Address of prefix */
	sizeofPrfx int   /* Size of heap prefix */
}

// hlHdrDeserialize is H5HL__hdr_deserialize: decode a local heap's header.
func hlHdrDeserialize(heap *h5hl, buf []byte, udata *h5hlCachePrfxUd) error {
	p := &image{buf: buf, sizeofAddr: uint8(udata.sizeofAddr), sizeofSize: uint8(udata.sizeofSize)}

	/* Magic number */
	if p.overflow(h5SizeofMagic) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), h5hlMagic) {
		return errorf(H5E_HEAP, H5E_BADVALUE, "bad local heap signature")
	}

	/* Version */
	if p.overflow(1) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if h5hlVersion != p.uint8() {
		return errorf(H5E_HEAP, H5E_VERSION, "wrong version number in local heap")
	}

	/* Reserved */
	if p.overflow(3) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	p.skip(3)

	/* Store the prefix's address & length */
	heap.addr = udata.prfxAddr
	heap.prfxSize = udata.sizeofPrfx

	/* Heap data size */
	if p.overflow(udata.sizeofSize) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	heap.dblkSize = p.uintVar(udata.sizeofSize)

	/* Free list head */
	if p.overflow(udata.sizeofSize) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	heap.freeHead = p.uintVar(udata.sizeofSize)
	if heap.freeHead != h5hlFreeNull && heap.freeHead >= heap.dblkSize {
		return errorf(H5E_HEAP, H5E_BADVALUE, "bad heap free list")
	}

	/* Heap data address */
	if p.overflow(udata.sizeofAddr) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	heap.dblkAddr = p.addrLen(udata.sizeofAddr)

	/* Check that the datablock address is valid (might not be true
	 * in a corrupt file)
	 */
	if !addrDefined(heap.dblkAddr) {
		return errorf(H5E_HEAP, H5E_BADVALUE, "bad datablock address")
	}
	return p.done()
}

// hlFlDeserialize is H5HL__fl_deserialize: deserialize the free list for a
// heap data block.
func hlFlDeserialize(heap *h5hl) error {
	sizeofSize := uint64(heap.sizeofSize)

	/* (Offsets of the free blocks seen so far, one bit per byte of the
	 *  data block: a corrupt list could cycle, and the C loop would not
	 *  end. The blocks may overlap, so a list without a cycle may hold
	 *  more blocks than fit side by side in the data block.) */
	var visited []uint64
	if heap.freeHead != h5hlFreeNull {
		visited = make([]uint64, (heap.dblkSize+63)/64)
	}

	/* Build free list */
	freeBlock := heap.freeHead
	for freeBlock != h5hlFreeNull {
		/* Sanity check */
		if freeBlock+2*sizeofSize > heap.dblkSize || freeBlock+2*sizeofSize < freeBlock {
			return errorf(H5E_HEAP, H5E_BADRANGE, "bad heap free list")
		}
		if visited[freeBlock/64]&(1<<(freeBlock%64)) != 0 {
			return errorf(H5E_HEAP, H5E_BADRANGE, "bad heap free list: cycle at offset %d", freeBlock)
		}
		visited[freeBlock/64] |= 1 << (freeBlock % 64)

		/* Allocate & initialize free list node */
		fl := h5hlFree{offset: freeBlock}

		/* Decode offset of next free block */
		p := &image{buf: heap.image[freeBlock:], sizeofSize: heap.sizeofSize}
		freeBlock = p.length()
		if freeBlock == 0 {
			return errorf(H5E_HEAP, H5E_BADVALUE, "free block size is zero?")
		}

		/* Decode length of this free block */
		fl.size = p.length()
		if fl.offset+fl.size > heap.dblkSize || fl.offset+fl.size < fl.offset {
			return errorf(H5E_HEAP, H5E_BADRANGE, "bad heap free list")
		}

		/* Append node onto list */
		heap.freelist = append(heap.freelist, fl)
	}
	return nil
}

// hlCachePrefixGetFinalLoadSize is H5HL__cache_prefix_get_final_load_size:
// decode the header from the speculatively read image and return the final
// size of the cache image (the prefix, plus the data block when it is
// contiguous with the prefix).
func hlCachePrefixGetFinalLoadSize(buf []byte, udata *h5hlCachePrfxUd) (int, error) {
	heap := &h5hl{}

	/* Deserialize the heap's header */
	if err := hlHdrDeserialize(heap, buf, udata); err != nil {
		return 0, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode local heap header")
	}

	/* Set the final size for the cache image */
	actualLen := uint64(heap.prfxSize)

	/* Check if heap block exists */
	if heap.dblkSize != 0 {
		/* Check if heap data block is contiguous with header */
		if heap.addr+uint64(heap.prfxSize) == heap.dblkAddr {
			/* Note that the heap should be a single object in the cache */
			actualLen += heap.dblkSize
		}
	}
	if actualLen < uint64(heap.prfxSize) || actualLen > uint64(math.MaxInt) {
		return 0, errorf(H5E_HEAP, H5E_BADSIZE, "bad local heap size")
	}
	return int(actualLen), nil
}

// hlCachePrefixDeserialize is H5HL__cache_prefix_deserialize: given a buffer
// containing the on disk image of the local heap prefix, deserialize it into
// a new in-core heap. When the data block is contiguous with the prefix the
// buffer also holds the data block and the heap is complete; otherwise the
// caller must load the data block with hlCacheDatablockDeserialize.
func hlCachePrefixDeserialize(buf []byte, udata *h5hlCachePrfxUd) (*h5hl, error) {
	/* Allocate space in memory for the heap */
	heap := &h5hl{sizeofSize: uint8(udata.sizeofSize), sizeofAddr: uint8(udata.sizeofAddr), prfxSize: udata.sizeofPrfx}

	/* Deserialize the heap's header */
	if err := hlHdrDeserialize(heap, buf, udata); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode local heap header")
	}

	/* Check if heap block exists */
	if heap.dblkSize != 0 {
		/* Check if heap data block is contiguous with header */
		if heap.addr+uint64(heap.prfxSize) == heap.dblkAddr {
			/* Note that the heap should be a single object in the cache */
			heap.singleCacheObj = true

			/* Set image to the start of the data block.  This is necessary
			 * because there may be a gap between the used portion of the
			 * prefix and the data block due to alignment constraints. */
			p := &image{buf: buf}
			p.skip(heap.prfxSize)

			/* Copy the heap data from the speculative read buffer */
			if heap.dblkSize > uint64(p.remaining()) || p.overflow(int(heap.dblkSize)) {
				return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			heap.image = append([]byte(nil), p.bytes(int(heap.dblkSize))...)

			/* Build free list */
			if err := hlFlDeserialize(heap); err != nil {
				return nil, wrapf(err, H5E_HEAP, H5E_CANTINIT, "can't initialize free list")
			}
		} else {
			/* Note that the heap should _NOT_ be a single
			 * object in the cache
			 */
			heap.singleCacheObj = false
		}
	}
	return heap, nil
}

// hlCacheDatablockDeserialize is H5HL__cache_datablock_deserialize: given a
// buffer containing the on disk image of a local heap data block, attach it
// to heap and build the free list.
func hlCacheDatablockDeserialize(heap *h5hl, buf []byte) error {
	if uint64(len(buf)) != heap.dblkSize || heap.singleCacheObj {
		return errorf(H5E_HEAP, H5E_BADVALUE, "bad local heap data block")
	}

	/* Check for heap still retaining image */
	if heap.image == nil {
		/* copy the datablock from the read buffer */
		heap.image = buf

		/* Build free list */
		if err := hlFlDeserialize(heap); err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTINIT, "can't initialize free list")
		}
	}
	return nil
}
