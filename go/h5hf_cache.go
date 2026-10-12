package hdf5

// Translated from src/H5HFcache.c (read side only: the get_load_size,
// verify_chksum and deserialize callbacks of the header, indirect block and
// direct block cache clients). The encoded I/O filter pipeline stored in a
// filtered heap's header is decoded by h5hfDecodePline, a private copy of
// H5O__pline_decode (src/H5Opline.c).

import "bytes"

// h5hfHdrPrefixDecode is H5HF__hdr_prefix_decode: decode a fractal heap
// header's prefix.
func h5hfHdrPrefixDecode(hdr *h5hfHdr, p *image) error {
	/* Magic number */
	if p.overflow(h5SizeofMagic) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5hfHdrMagic)) {
		return errorf(H5E_HEAP, H5E_BADVALUE, "wrong fractal heap header signature")
	}

	/* Version */
	if p.overflow(1 + 2 + 2) {
		return errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	hdr.version = p.uint8()
	if hdr.version != h5hfHdrVersion {
		return errorf(H5E_HEAP, H5E_VERSION, "wrong fractal heap header version")
	}

	/* General heap information */
	hdr.idLen = p.uint16()     /* Heap ID length */
	hdr.filterLen = p.uint16() /* I/O filters' encoded length */
	return p.done()
}

// h5hfDtableDecode is H5HF__dtable_decode: decodes the metadata for a
// doubling table.
func h5hfDtableDecode(p *image, dtable *h5hfDtable) {
	/* Table width */
	dtable.width = p.uint16()

	/* Starting block size */
	dtable.startBlockSize = p.length()

	/* Maximum direct block size */
	dtable.maxDirectSize = p.length()

	/* Maximum heap size (as # of bits) */
	dtable.maxIndexBits = p.uint16()

	/* Starting # of rows in root indirect block */
	dtable.startRootRows = p.uint16()

	/* Address of table */
	dtable.tableAddr = p.addr()

	/* Current # of rows in root indirect block */
	dtable.curRootRows = p.uint16()
}

// h5hfCacheHdrGetInitialLoadSize is H5HF__cache_hdr_get_initial_load_size:
// the size of the fractal heap header on disk, presuming that there is no
// I/O filtering data in the header.
func h5hfCacheHdrGetInitialLoadSize(f *File) uint64 {
	/* Set the internal parameters for the heap */
	dummyHdr := h5hfHdr{f: f, sizeofSize: f.sizeofSize, sizeofAddr: f.sizeofAddr}

	/* Compute the 'base' size of the fractal heap header on disk */
	return h5hfHeaderSize(&dummyHdr)
}

// h5hfCacheHdrGetFinalLoadSize is H5HF__cache_hdr_get_final_load_size:
// determine the final size of the fractal heap header on disk from the
// initial image.
func h5hfCacheHdrGetFinalLoadSize(f *File, img []byte) (uint64, error) {
	var hdr h5hfHdr
	actualLen := uint64(len(img))

	/* Deserialize the fractal heap header's prefix */
	if err := h5hfHdrPrefixDecode(&hdr, f.newImage(img)); err != nil {
		return 0, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode fractal heap header prefix")
	}

	/* Check for I/O filter info on this heap */
	if hdr.filterLen > 0 {
		/* Compute the extra heap header size */
		actualLen += uint64(f.sizeofSize) /* Size of size for filtered root direct block */ +
			4 /* Size of filter mask for filtered root direct block */ +
			uint64(hdr.filterLen) /* Size of encoded I/O filter info */
	}
	return actualLen, nil
}

// h5hfGetChecksums is H5F_get_checksums: the checksum stored in the last
// four bytes of a metadata image and the one computed over the rest.
func h5hfGetChecksums(img []byte) (stored, computed uint32, err error) {
	if len(img) < h5hfSizeofChksum {
		return 0, 0, errorf(H5E_HEAP, H5E_CANTGET, "image too small for a checksum")
	}
	n := len(img) - h5hfSizeofChksum
	return le32(img[n:]), checksumMetadata(img[:n], 0), nil
}

// h5hfCacheHdrVerifyChksum is H5HF__cache_hdr_verify_chksum: verify the
// computed checksum of the data structure is the same as the stored
// checksum.
func h5hfCacheHdrVerifyChksum(img []byte) (bool, error) {
	/* Get stored and computed checksums */
	stored, computed, err := h5hfGetChecksums(img)
	if err != nil {
		return false, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5hfCacheHdrDeserialize is H5HF__cache_hdr_deserialize: given a buffer
// containing an on disk image of a fractal heap header block, decode it into
// a new h5hfHdr.
func h5hfCacheHdrDeserialize(f *File, img []byte, addr haddr) (*h5hfHdr, error) {
	p := f.newImage(img)

	/* Allocate space for the fractal heap data structure (H5HF__hdr_alloc) */
	hdr := &h5hfHdr{f: f, addr: addr, sizeofSize: f.sizeofSize, sizeofAddr: f.sizeofAddr}

	/* Deserialize the fractal heap header's prefix */
	if err := h5hfHdrPrefixDecode(hdr, p); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode fractal heap header prefix")
	}

	/* Make sure the fixed-size part of the header is present */
	if p.overflow(int(h5hfHeaderSize(hdr)) - p.off) {
		return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Heap status flags */
	/* (bit 0: "huge" object IDs have wrapped) */
	/* (bit 1: checksum direct blocks) */
	heapFlags := p.uint8()
	hdr.flags = heapFlags
	hdr.hugeIDsWrapped = heapFlags&h5hfHdrFlagsHugeIDWrapped != 0
	hdr.checksumDblocks = heapFlags&h5hfHdrFlagsChecksumDblocks != 0

	/* "Huge" object information */
	hdr.maxManSize = p.uint32() /* Max. size of "managed" objects */
	hdr.nextManID = p.length()  /* Next ID to use for "huge" object */
	hdr.hugeBt2Addr = p.addr()  /* Address of "huge" object tracker B-tree */

	/* "Managed" object free space information */
	hdr.freeSpace = p.length() /* Internal free space in managed direct blocks */
	hdr.fsAddr = p.addr()      /* Address of free section header */

	/* Heap statistics */
	hdr.manSize = p.length()
	hdr.manAllocSize = p.length()
	hdr.manIterOff = p.length()
	hdr.manNobjs = p.length()
	hdr.hugeSize = p.length()
	hdr.hugeNobjs = p.length()
	hdr.tinySize = p.length()
	hdr.tinyNobjs = p.length()

	/* Managed objects' doubling-table info */
	h5hfDtableDecode(p, &hdr.manDtable)
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "unable to decode managed obj. doubling table info")
	}

	/* Set the fractal heap header's 'base' size */
	hdr.heapSize = h5hfHeaderSize(hdr)

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != hdr.heapSize-h5hfSizeofChksum {
		return nil, errorf(H5E_HEAP, H5E_BADSIZE, "bad decoded fractal heap header size")
	}

	/* Check for I/O filter information to decode */
	if hdr.filterLen > 0 {
		/* Compute the heap header's size */
		hdr.heapSize += uint64(hdr.sizeofSize) /* Size of size for filtered root direct block */ +
			4 /* Size of filter mask for filtered root direct block */ +
			uint64(hdr.filterLen) /* Size of encoded I/O filter info */

		/* Sanity check */
		/* (A header with filter info is > than a standard header) */
		if uint64(len(img)) < hdr.heapSize {
			return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}

		/* Decode the size of a filtered root direct block */
		hdr.plineRootDirectSize = p.length()

		/* Decode the filter mask for a filtered root direct block */
		hdr.plineRootDirectFilterMask = p.uint32()

		/* Decode I/O filter information */
		pline, err := h5hfDecodePline(p.sub(int(hdr.filterLen)))
		if err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode I/O pipeline filters")
		}
		hdr.pline = pline
	}

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode fractal heap header")
	}

	/* Sanity check */
	if uint64(p.off) != hdr.heapSize {
		return nil, errorf(H5E_HEAP, H5E_BADSIZE, "bad decoded fractal heap header size")
	}

	/* Check the heap ID length (validated at creation time in the C
	 * library; every heap ID routine depends on it) */
	if hdr.idLen == 0 || hdr.idLen > h5hfMaxIDLen {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "invalid fractal heap ID length %d", hdr.idLen)
	}

	/* Finish initialization of heap header */
	if err := h5hfHdrFinishInit(hdr); err != nil {
		return nil, wrapf(err, H5E_RESOURCE, H5E_CANTINIT, "can't finish initializing shared fractal heap header")
	}
	return hdr, nil
}

// h5hfCacheHdrLoad is the H5C__load_entry sequence for a fractal heap
// header: read the initial image, extend it to the final load size, verify
// the checksum and deserialize it.
func h5hfCacheHdrLoad(f *File, addr haddr) (*h5hfHdr, error) {
	initLen := h5hfCacheHdrGetInitialLoadSize(f)
	img, err := f.readAt(addr, int(initLen))
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTLOAD, "unable to read fractal heap header")
	}
	finalLen, err := h5hfCacheHdrGetFinalLoadSize(f, img)
	if err != nil {
		return nil, err
	}
	if finalLen != initLen {
		if img, err = f.readAt(addr, int(finalLen)); err != nil {
			return nil, wrapf(err, H5E_HEAP, H5E_CANTLOAD, "unable to read fractal heap header")
		}
	}
	if f.opts.verifyChecksums {
		ok, err := h5hfCacheHdrVerifyChksum(img)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_HEAP, H5E_BADCHECKSUM, "incorrect metadata checksum for fractal heap header")
		}
	}
	return h5hfCacheHdrDeserialize(f, img, addr)
}

// h5hfCacheIblockGetInitialLoadSize is
// H5HF__cache_iblock_get_initial_load_size: the size of the on disk image
// of an indirect block with nrows rows.
func h5hfCacheIblockGetInitialLoadSize(hdr *h5hfHdr, nrows uint32) uint64 {
	return h5hfManIndirectSize(hdr, nrows)
}

// h5hfCacheIblockVerifyChksum is H5HF__cache_iblock_verify_chksum.
func h5hfCacheIblockVerifyChksum(img []byte) (bool, error) {
	/* Get stored and computed checksums */
	stored, computed, err := h5hfGetChecksums(img)
	if err != nil {
		return false, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5hfCacheIblockDeserialize is H5HF__cache_iblock_deserialize: decode the
// on disk image of an indirect block with nrows rows whose parent is
// described by parInfo.
func h5hfCacheIblockDeserialize(img []byte, parInfo *h5hfParent, nrows uint32) (*h5hfIndirect, error) {
	hdr := parInfo.hdr
	dt := &hdr.manDtable
	p := hdr.f.newImage(img)

	/* Allocate space for the fractal heap indirect block */
	iblock := &h5hfIndirect{heapAddr: hdr.addr}

	/* Set block's internal information */
	iblock.nrows = nrows
	iblock.nchildren = 0

	/* Compute size of indirect block */
	iblock.size = h5hfManIndirectSize(hdr, iblock.nrows)

	/* sanity check */
	if iblock.size != uint64(len(img)) {
		return nil, errorf(H5E_HEAP, H5E_BADSIZE, "fractal heap indirect block image has the wrong size")
	}

	/* Magic number */
	if p.overflow(h5SizeofMagic + 1) {
		return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5hfIblockMagic)) {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "wrong fractal heap indirect block signature")
	}

	/* Version */
	if p.uint8() != h5hfIblockVersion {
		return nil, errorf(H5E_HEAP, H5E_VERSION, "wrong fractal heap direct block version")
	}

	/* Address of heap that owns this block */
	heapAddr := p.addr()
	if heapAddr != hdr.addr {
		return nil, errorf(H5E_HEAP, H5E_CANTLOAD, "incorrect heap header address for direct block")
	}

	/* Address of parent block */
	iblock.parEntry = parInfo.entry
	if parInfo.iblock != nil {
		/* Set max. # of rows in this block */
		iblock.maxRows = iblock.nrows
	} else {
		/* Set max. # of rows in this block */
		iblock.maxRows = dt.maxRootRows
	}

	/* Offset of heap within the heap's address space */
	iblock.blockOff = p.uintVar(int(hdr.heapOffSize))

	/* Allocate & decode child block entry tables */
	if iblock.nrows == 0 {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "fractal heap indirect block has no rows")
	}
	nents := iblock.nrows * uint32(dt.width)
	iblock.ents = make([]haddr, nents)

	if hdr.filterLen > 0 {
		/* Compute the number of direct rows for this indirect block */
		dirRows := min(iblock.nrows, dt.maxDirectRows)

		/* Allocate indirect block filtered entry array */
		iblock.filtEnts = make([]h5hfIndirectFiltEnt, dirRows*uint32(dt.width))
	}

	for u := uint32(0); u < nents; u++ {
		/* Decode child block address */
		iblock.ents[u] = p.addr()

		/* Check for heap with I/O filters */
		if hdr.filterLen > 0 {
			/* Decode extra information for direct blocks */
			if u < dt.maxDirectRows*uint32(dt.width) {
				/* Size of filtered direct block */
				iblock.filtEnts[u].size = p.length()

				/* Sanity check */
				/* (either both the address & size are defined or both are
				 *  not defined)
				 */
				if addrDefined(iblock.ents[u]) != (iblock.filtEnts[u].size != 0) {
					return nil, errorf(H5E_HEAP, H5E_BADVALUE, "inconsistent filtered direct block entry")
				}

				/* I/O filter mask for filtered direct block */
				iblock.filtEnts[u].filterMask = p.uint32()
			}
		}

		/* Count child blocks */
		if addrDefined(iblock.ents[u]) {
			iblock.nchildren++
			iblock.maxChild = u
		}
	}

	/* Sanity check */
	/* (indirect blocks w/no children should have been deleted) */
	if iblock.nchildren == 0 {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "fractal heap indirect block has no children")
	}

	/* checksum verification already done by verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode fractal heap indirect block")
	}

	/* Sanity check */
	if uint64(p.off) != iblock.size {
		return nil, errorf(H5E_HEAP, H5E_BADSIZE, "bad decoded fractal heap indirect block size")
	}

	/* (Child indirect blocks are not pinned here: they are loaded through
	 *  the cache by address when needed) */
	return iblock, nil
}

// h5hfDblockCacheUd is H5HF_dblock_cache_ud_t: parent and other info for
// deserializing a direct block.
type h5hfDblockCacheUd struct {
	parInfo    h5hfParent /* Parent info */
	odiSize    uint64     /* On disk image size of the direct block */
	dblockSize uint64     /* Size of the direct block */
	filterMask uint32     /* Excluded filters for direct block */
}

// h5hfCacheDblockGetInitialLoadSize is
// H5HF__cache_dblock_get_initial_load_size: the size of the on disk image of
// the direct block.
func h5hfCacheDblockGetInitialLoadSize(udata *h5hfDblockCacheUd) (uint64, error) {
	parInfo := &udata.parInfo
	hdr := parInfo.hdr

	/* Check for I/O filters on this heap */
	if hdr.filterLen > 0 {
		/* Check for root direct block */
		if parInfo.iblock == nil {
			/* filtered root direct block */
			return hdr.plineRootDirectSize, nil
		}
		/* filtered direct block */
		if parInfo.entry >= uint32(len(parInfo.iblock.filtEnts)) {
			return 0, errorf(H5E_HEAP, H5E_BADRANGE, "filtered direct block entry out of range")
		}
		return parInfo.iblock.filtEnts[parInfo.entry].size, nil
	}
	return udata.dblockSize, nil
}

// h5hfCacheDblockVerifyChksum is H5HF__cache_dblock_verify_chksum: verify
// the checksum of a direct block. blk is the block as decoded by the I/O
// filters (the C callback runs the pipeline itself and hands the result to
// the deserialize callback; here the caller does that once). The checksum
// field is zeroed while the checksum is computed and restored afterwards.
func h5hfCacheDblockVerifyChksum(hdr *h5hfHdr, blk []byte) (bool, error) {
	/* Get out if data block is not checksummed */
	if !hdr.checksumDblocks {
		return true, nil
	}

	/* Decode checksum */
	chkSize := h5hfManAbsDirectOverhead(hdr) - h5hfSizeofChksum
	if uint64(len(blk)) < chkSize+h5hfSizeofChksum {
		return false, errorf(H5E_HEAP, H5E_OVERFLOW, "direct block too small for its prefix")
	}
	chk := blk[chkSize : chkSize+h5hfSizeofChksum]

	/* Metadata checksum */
	storedChksum := le32(chk)

	/* Reset checksum field, for computing the checksum */
	var saved [h5hfSizeofChksum]byte
	copy(saved[:], chk)
	clear(chk)

	/* Compute checksum on entire direct block */
	computedChksum := checksumMetadata(blk, 0)

	/* Restore the checksum */
	copy(chk, saved[:])

	/* Verify checksum */
	return storedChksum == computedChksum, nil
}

// h5hfCacheDblockDeserialize is H5HF__cache_dblock_deserialize: decode the
// (already de-filtered) image of a direct block. The returned block owns
// blk.
func h5hfCacheDblockDeserialize(blk []byte, udata *h5hfDblockCacheUd) (*h5hfDirect, error) {
	parInfo := &udata.parInfo
	hdr := parInfo.hdr

	/* Allocate space for the fractal heap direct block */
	dblock := &h5hfDirect{heapAddr: hdr.addr}

	/* Set block's internal information */
	dblock.size = udata.dblockSize
	if uint64(len(blk)) != dblock.size {
		return nil, errorf(H5E_HEAP, H5E_BADSIZE, "fractal heap direct block has size %d, expected %d", len(blk), dblock.size)
	}
	dblock.blk = blk

	/* Start decoding direct block */
	p := hdr.f.newImage(dblock.blk)

	/* Magic number */
	if p.overflow(h5SizeofMagic + 1) {
		return nil, errorf(H5E_HEAP, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5hfDblockMagic)) {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "wrong fractal heap direct block signature")
	}

	/* Version */
	if p.uint8() != h5hfDblockVersion {
		return nil, errorf(H5E_HEAP, H5E_VERSION, "wrong fractal heap direct block version")
	}

	/* Address of heap that owns this block (just for file integrity checks) */
	heapAddr := p.addr()
	if heapAddr != hdr.addr {
		return nil, errorf(H5E_HEAP, H5E_CANTLOAD, "incorrect heap header address for direct block")
	}

	/* Address of parent block */
	dblock.parEntry = parInfo.entry

	/* Offset of heap within the heap's address space */
	dblock.blockOff = p.uintVar(int(hdr.heapOffSize))

	/* Decode checksum on direct block, if requested */
	if hdr.checksumDblocks {
		/* checksum verification already done in verify_chksum cb */

		/* Metadata checksum */
		_ = p.uint32()
	}
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTDECODE, "can't decode fractal heap direct block")
	}

	/* Sanity check */
	if uint64(p.off) != h5hfManAbsDirectOverhead(hdr) {
		return nil, errorf(H5E_HEAP, H5E_BADSIZE, "bad decoded fractal heap direct block prefix size")
	}
	return dblock, nil
}

// H5O_PLINE_VERSION_* (H5Oprivate.h) and H5Z_MAX_NFILTERS (H5Zpublic.h),
// for h5hfDecodePline.
const (
	h5hfPlineVersion1      = 1
	h5hfPlineVersion2      = 2
	h5hfPlineVersionLatest = h5hfPlineVersion2
	h5hfZMaxNfilters       = 32
)

// h5hfDecodePline is a private copy of H5O__pline_decode (src/H5Opline.c):
// decode the filter pipeline message stored in a filtered heap's header.
// It is kept here until the object header message decoders are merged; it
// should then be replaced by a call to the shared decoder.
func h5hfDecodePline(p *image) (*h5oPline, error) {
	/* Allocate space for I/O pipeline message */
	pline := &h5oPline{}

	/* Version */
	if p.overflow(1) {
		return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	pline.version = p.uint8()
	if pline.version < h5hfPlineVersion1 || pline.version > h5hfPlineVersionLatest {
		return nil, errorf(H5E_PLINE, H5E_CANTLOAD, "bad version number for filter pipeline message")
	}

	/* Number of filters */
	if p.overflow(1) {
		return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	pline.nused = int(p.uint8())
	if pline.nused > h5hfZMaxNfilters {
		return nil, errorf(H5E_PLINE, H5E_CANTLOAD, "filter pipeline message has too many filters")
	}

	/* Reserved */
	if pline.version == h5hfPlineVersion1 {
		if p.overflow(6) {
			return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		p.skip(6)
	}

	/* Allocate array for filters */
	pline.filter = make([]h5zFilterInfo, pline.nused)

	/* Decode filters */
	for i := range pline.filter {
		filter := &pline.filter[i]

		/* Filter ID */
		if p.overflow(2) {
			return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		filter.id = int32(p.uint16())

		/* Length of filter name */
		var nameLength int
		if pline.version > h5hfPlineVersion1 && filter.id < h5zFilterReserved {
			nameLength = 0
		} else {
			if p.overflow(2) {
				return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			nameLength = int(p.uint16())
			if pline.version == h5hfPlineVersion1 && nameLength%8 != 0 {
				return nil, errorf(H5E_PLINE, H5E_CANTLOAD, "filter name length is not a multiple of eight")
			}
		}

		/* Filter flags */
		if p.overflow(2) {
			return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		filter.flags = p.uint16()

		/* Number of filter parameters ("client data elements") */
		if p.overflow(2) {
			return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		cdNelmts := int(p.uint16())

		/* Filter name, if there is one */
		if nameLength > 0 {
			/* Determine actual name length (without padding, but with null terminator) */
			rest := p.rest()
			actualNameLength := bytes.IndexByte(rest, 0)
			if actualNameLength < 0 {
				return nil, errorf(H5E_OHDR, H5E_CANTALLOC, "filter name not null terminated")
			}
			filter.name = string(rest[:actualNameLength])

			if p.overflow(nameLength) {
				return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			p.skip(nameLength)
		}

		/* Filter parameters */
		if cdNelmts > 0 {
			/* (make sure the values are present before allocating space
			 *  for them) */
			if p.overflow(4 * cdNelmts) {
				return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
			}
			filter.cdValues = make([]uint32, cdNelmts)

			/* Read the client data values and the padding */
			for j := range filter.cdValues {
				filter.cdValues[j] = p.uint32()
			}

			if pline.version == h5hfPlineVersion1 && cdNelmts%2 != 0 {
				if p.overflow(4) {
					return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding")
				}
				p.skip(4) /* padding */
			}
		}
	}
	if err := p.done(); err != nil {
		return nil, err
	}
	return pline, nil
}
