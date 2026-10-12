package hdf5

// Translated from src/H5EAcache.c (read side only: the
// get_initial_load_size, verify_chksum and deserialize callbacks of the
// header, index block, super block, data block and data block page cache
// clients). The h5eaCache*Load functions play the role of H5C__load_entry:
// read the image, verify the checksum (when the file's checksum option is
// on) and deserialize it.

import "bytes"

/****************/
/* Local Macros */
/****************/

/* Fractal heap format version #'s */
const (
	h5eaHdrVersion    = 0 /* Header */
	h5eaIblockVersion = 0 /* Index block */
	h5eaSblockVersion = 0 /* Super block */
	h5eaDblockVersion = 0 /* Data block */
)

/******************/
/* Local Typedefs */
/******************/

// h5eaSblockCacheUd is H5EA_sblock_cache_ud_t: info needed for loading
// super block.
type h5eaSblockCacheUd struct {
	hdr      *h5eaHdr /* Shared extensible array information */
	sblkIdx  uint64   /* Index of super block */
	sblkAddr haddr    /* Address of super block */
}

// h5eaDblockCacheUd is H5EA_dblock_cache_ud_t: info needed for loading data
// block.
type h5eaDblockCacheUd struct {
	hdr      *h5eaHdr /* Shared extensible array information */
	nelmts   uint64   /* Number of elements in data block */
	dblkAddr haddr    /* Address of data block */
}

// h5eaDblkPageCacheUd is H5EA_dblk_page_cache_ud_t: info needed for loading
// data block page.
type h5eaDblkPageCacheUd struct {
	hdr          *h5eaHdr /* Shared extensible array information */
	dblkPageAddr haddr    /* Address of data block page */
}

// h5eaGetChecksums is H5F_get_checksums for extensible array metadata: the
// checksum stored in the last four bytes of a metadata image and the one
// computed over the rest.
func h5eaGetChecksums(img []byte) (stored, computed uint32, err error) {
	if len(img) < h5eaSizeofChksum {
		return 0, 0, errorf(H5E_EARRAY, H5E_CANTGET, "image too small for a checksum")
	}
	n := len(img) - h5eaSizeofChksum
	return le32(img[n:]), checksumMetadata(img[:n], 0), nil
}

// h5eaCacheVerifyChksum is the body shared by all the verify_chksum
// callbacks (H5EA__cache_*_verify_chksum): verify the computed checksum of
// the data structure is the same as the stored chksum.
func h5eaCacheVerifyChksum(img []byte) (bool, error) {
	/* Get stored and computed checksums */
	stored, computed, err := h5eaGetChecksums(img)
	if err != nil {
		return false, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't get checksums")
	}
	return stored == computed, nil
}

// h5eaCacheLoadImage reads the size-byte image of a piece of extensible
// array metadata at addr and verifies its checksum when the file's checksum
// option is on (H5C__load_entry).
func h5eaCacheLoadImage(f *File, addr haddr, size uint64, verifyChksum func([]byte) (bool, error), what string) ([]byte, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "undefined extensible array %s address", what)
	}
	if size > f.eof() {
		return nil, errorf(H5E_EARRAY, H5E_OVERFLOW, "extensible array %s size %d larger than the file", what, size)
	}
	img, err := f.readAt(addr, int(size))
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTLOAD, "unable to read extensible array %s", what)
	}
	if f.opts.verifyChecksums {
		ok, err := verifyChksum(img)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_EARRAY, H5E_BADCHECKSUM, "incorrect metadata checksum for extensible array %s", what)
		}
	}
	return img, nil
}

// h5eaCacheDecodeElmts calls the class' decode callback for each element of
// elmts (cls->decode(image, elmts, nelmts, ctx)), consuming exactly
// raw_elmt_size bytes of p per element.
func h5eaCacheDecodeElmts(hdr *h5eaHdr, p *image, elmts []any) error {
	for i := range elmts {
		q := p.sub(int(hdr.eltSize))
		if err := q.done(); err != nil {
			return err
		}
		v, err := hdr.cls.decode(hdr.f, hdr.cbCtx, q)
		if err != nil {
			return err
		}
		elmts[i] = v
	}
	return nil
}

// h5eaCacheHdrGetInitialLoadSize is H5EA__cache_hdr_get_initial_load_size.
func h5eaCacheHdrGetInitialLoadSize(f *File) uint64 {
	/* Set the image length size */
	return h5eaHeaderSizeFile(f)
}

// h5eaCacheHdrVerifyChksum is H5EA__cache_hdr_verify_chksum: verify the
// computed checksum of the data structure is the same as the stored chksum.
func h5eaCacheHdrVerifyChksum(img []byte) (bool, error) {
	return h5eaCacheVerifyChksum(img)
}

// h5eaCacheHdrDeserialize is H5EA__cache_hdr_deserialize: loads a data
// structure from the disk.
func h5eaCacheHdrDeserialize(f *File, img []byte, addr haddr, cls *h5eaClass, ctx any) (*h5eaHdr, error) {
	p := f.newImage(img)

	/* Allocate space for the extensible array data structure */
	hdr := h5eaHdrAlloc(f)

	/* Set the extensible array header's address */
	hdr.addr = addr

	/* (the whole fixed-size header must be present) */
	if p.overflow(int(h5eaHeaderSizeFile(f))) {
		return nil, errorf(H5E_EARRAY, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Magic number */
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5eaHdrMagic)) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "wrong extensible array header signature")
	}

	/* Version */
	hdr.version = p.uint8()
	if hdr.version != h5eaHdrVersion {
		return nil, errorf(H5E_EARRAY, H5E_VERSION, "wrong extensible array header version")
	}

	/* Extensible array class */
	id := p.uint8()
	if id >= h5eaNumClsID {
		return nil, errorf(H5E_EARRAY, H5E_BADTYPE, "incorrect extensible array class")
	}
	hdr.clsID = id

	/* General array creation/configuration information */
	hdr.eltSize = p.uint8()               /* Element size in file (in bytes) */
	hdr.maxNelmtsBits = p.uint8()         /* Log2(Max. # of elements in array) - i.e. # of bits needed to store max. # of elements */
	hdr.idxBlkElmts = p.uint8()           /* # of elements to store in index block */
	hdr.dataBlkMinElmts = p.uint8()       /* Min. # of elements per data block */
	hdr.supBlkMinDataPtrs = p.uint8()     /* Min. # of data block pointers for a super block */
	hdr.maxDblkPageNelmtsBits = p.uint8() /* Log2(Max. # of elements in data block page) - i.e. # of bits needed to store max. # of elements in data block page */

	/* Array statistics */
	hdr.hdrSize = uint64(len(img)) /* Size of header in file */
	hdr.nsuperBlks = p.length()    /* Number of super blocks created */
	hdr.superBlkSize = p.length()  /* Size of super blocks created */
	hdr.ndataBlks = p.length()     /* Number of data blocks created */
	hdr.dataBlkSize = p.length()   /* Size of data blocks created */
	hdr.maxIdxSet = p.length()     /* Max. index set (+1) */
	hdr.nelmts = p.length()        /* Number of elements 'realized' */

	/* Internal information */
	hdr.idxBlkAddr = p.addr() /* Address of index block */

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5eaSizeofChksum {
		return nil, errorf(H5E_EARRAY, H5E_BADSIZE, "bad decoded extensible array header size")
	}

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array header")
	}

	/* Finish initializing extensible array header */
	if err := h5eaHdrInit(hdr, cls, ctx); err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTINIT, "initialization failed for extensible array header")
	}
	if hdr.size != uint64(len(img)) {
		return nil, errorf(H5E_EARRAY, H5E_BADSIZE, "bad extensible array header size")
	}

	/* Index block statistics */
	/* (computed after H5EA__hdr_init here: the index block size depends on
	 *  the number of super blocks it computes) */
	if addrDefined(hdr.idxBlkAddr) {
		/* Set index block count for file */
		hdr.nindexBlks = 1

		/* Set up fake index block for computing size on disk */
		iblock := h5eaIblock{hdr: hdr}
		iblock.nsblks = h5eaSblkFirstIdx(hdr.supBlkMinDataPtrs)
		iblock.ndblkAddrs = 2 * (uint64(hdr.supBlkMinDataPtrs) - 1)
		iblock.nsblkAddrs = hdr.nsblks - iblock.nsblks

		/* Compute size of index block in file */
		hdr.indexBlkSize = h5eaIblockSize(&iblock)
	} else {
		hdr.nindexBlks = 0   /* Number of index blocks in file */
		hdr.indexBlkSize = 0 /* Size of index blocks in file */
	}

	return hdr, nil
}

// h5eaCacheHdrLoad loads (H5C__load_entry) an extensible array header.
func h5eaCacheHdrLoad(f *File, addr haddr, cls *h5eaClass, ctx any) (*h5eaHdr, error) {
	img, err := h5eaCacheLoadImage(f, addr, h5eaCacheHdrGetInitialLoadSize(f), h5eaCacheHdrVerifyChksum, "header")
	if err != nil {
		return nil, err
	}
	return h5eaCacheHdrDeserialize(f, img, addr, cls, ctx)
}

// h5eaCacheIblockGetInitialLoadSize is
// H5EA__cache_iblock_get_initial_load_size.
func h5eaCacheIblockGetInitialLoadSize(hdr *h5eaHdr) uint64 {
	/* Set up fake index block for computing size on disk */
	iblock := h5eaIblock{hdr: hdr}
	iblock.nsblks = h5eaSblkFirstIdx(hdr.supBlkMinDataPtrs)
	iblock.ndblkAddrs = 2 * (uint64(hdr.supBlkMinDataPtrs) - 1)
	iblock.nsblkAddrs = hdr.nsblks - iblock.nsblks

	/* Set the image length size */
	return h5eaIblockSize(&iblock)
}

// h5eaCacheIblockVerifyChksum is H5EA__cache_iblock_verify_chksum.
func h5eaCacheIblockVerifyChksum(img []byte) (bool, error) {
	return h5eaCacheVerifyChksum(img)
}

// h5eaCacheIblockDeserialize is H5EA__cache_iblock_deserialize: loads a data
// structure from the disk.
func h5eaCacheIblockDeserialize(img []byte, hdr *h5eaHdr) (*h5eaIblock, error) {
	p := hdr.f.newImage(img)

	/* Allocate the extensible array index block */
	iblock, err := h5eaIblockAlloc(hdr)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for extensible array index block")
	}

	/* Set the extensible array index block's address */
	iblock.addr = hdr.idxBlkAddr

	/* (the whole index block must be present) */
	if p.overflow(int(h5eaIblockSize(iblock))) {
		return nil, errorf(H5E_EARRAY, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Magic number */
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5eaIblockMagic)) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "wrong extensible array index block signature")
	}

	/* Version */
	if p.uint8() != h5eaIblockVersion {
		return nil, errorf(H5E_EARRAY, H5E_VERSION, "wrong extensible array index block version")
	}

	/* Extensible array type */
	if p.uint8() != hdr.clsID {
		return nil, errorf(H5E_EARRAY, H5E_BADTYPE, "incorrect extensible array class")
	}

	/* Address of header for array that owns this block (just for file integrity checks) */
	if arrAddr := p.addr(); arrAddr != hdr.addr {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "wrong extensible array header address")
	}

	/* Internal information */

	/* Decode elements in index block */
	if hdr.idxBlkElmts > 0 {
		/* Convert from raw elements on disk into native elements in memory */
		if err := h5eaCacheDecodeElmts(hdr, p, iblock.elmts); err != nil {
			return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array index elements")
		}
	}

	/* Decode data block addresses in index block */
	for u := uint64(0); u < iblock.ndblkAddrs; u++ {
		iblock.dblkAddrs[u] = p.addr()
	}

	/* Decode super block addresses in index block */
	for u := uint64(0); u < iblock.nsblkAddrs; u++ {
		iblock.sblkAddrs[u] = p.addr()
	}

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5eaSizeofChksum {
		return nil, errorf(H5E_EARRAY, H5E_BADSIZE, "bad decoded extensible array index block size")
	}

	/* Save the index block's size */
	iblock.size = uint64(len(img))

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array index block")
	}

	return iblock, nil
}

// h5eaCacheIblockLoad loads (H5C__load_entry) the index block of an
// extensible array.
func h5eaCacheIblockLoad(hdr *h5eaHdr) (*h5eaIblock, error) {
	img, err := h5eaCacheLoadImage(hdr.f, hdr.idxBlkAddr, h5eaCacheIblockGetInitialLoadSize(hdr), h5eaCacheIblockVerifyChksum, "index block")
	if err != nil {
		return nil, err
	}
	return h5eaCacheIblockDeserialize(img, hdr)
}

// h5eaCacheSblockGetInitialLoadSize is
// H5EA__cache_sblock_get_initial_load_size.
func h5eaCacheSblockGetInitialLoadSize(udata *h5eaSblockCacheUd) uint64 {
	/* Set up fake super block for computing size on disk */
	/* (Note: extracted from H5EA__sblock_alloc) */
	sblock := h5eaSblock{hdr: udata.hdr}
	sblock.ndblks = udata.hdr.sblkInfo[udata.sblkIdx].ndblks
	sblock.dblkNelmts = udata.hdr.sblkInfo[udata.sblkIdx].dblkNelmts

	/* Check if # of elements in data blocks requires paging */
	if sblock.dblkNelmts > udata.hdr.dblkPageNelmts {
		/* Compute # of pages in each data block from this super block */
		sblock.dblkNpages = sblock.dblkNelmts / udata.hdr.dblkPageNelmts

		/* Compute size of buffer for each data block's 'page init' bitmask */
		sblock.dblkPageInitSize = (sblock.dblkNpages + 7) / 8
	}

	/* Set the image length size */
	return h5eaSblockSize(&sblock)
}

// h5eaCacheSblockVerifyChksum is H5EA__cache_sblock_verify_chksum.
func h5eaCacheSblockVerifyChksum(img []byte) (bool, error) {
	return h5eaCacheVerifyChksum(img)
}

// h5eaCacheSblockDeserialize is H5EA__cache_sblock_deserialize: loads a data
// structure from the disk.
func h5eaCacheSblockDeserialize(img []byte, udata *h5eaSblockCacheUd) (*h5eaSblock, error) {
	hdr := udata.hdr
	p := hdr.f.newImage(img)

	/* Allocate the extensible array super block */
	sblock, err := h5eaSblockAlloc(hdr, udata.sblkIdx)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for extensible array super block")
	}

	/* Set the extensible array super block's address */
	sblock.addr = udata.sblkAddr

	/* (the whole super block must be present) */
	if p.overflow(int(h5eaSblockSize(sblock))) {
		return nil, errorf(H5E_EARRAY, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Magic number */
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5eaSblockMagic)) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "wrong extensible array super block signature")
	}

	/* Version */
	if p.uint8() != h5eaSblockVersion {
		return nil, errorf(H5E_EARRAY, H5E_VERSION, "wrong extensible array super block version")
	}

	/* Extensible array type */
	if p.uint8() != hdr.clsID {
		return nil, errorf(H5E_EARRAY, H5E_BADTYPE, "incorrect extensible array class")
	}

	/* Address of header for array that owns this block (just for file integrity checks) */
	if arrAddr := p.addr(); arrAddr != hdr.addr {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "wrong extensible array header address")
	}

	/* Offset of block within the array's address space */
	sblock.blockOff = p.uintVar(int(hdr.arrOffSize))

	/* Internal information */

	/* Check for 'page init' bitmasks for this super block */
	if sblock.dblkNpages > 0 {
		totPageInitSize := sblock.ndblks * sblock.dblkPageInitSize /* Compute total size of 'page init' buffer */

		/* Retrieve the 'page init' bitmasks */
		copy(sblock.pageInit, p.bytes(int(totPageInitSize)))
	}

	/* Decode data block addresses */
	for u := uint64(0); u < sblock.ndblks; u++ {
		sblock.dblkAddrs[u] = p.addr()
	}

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5eaSizeofChksum {
		return nil, errorf(H5E_EARRAY, H5E_BADSIZE, "bad decoded extensible array super block size")
	}

	/* Save the super block's size */
	sblock.size = uint64(len(img))

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array super block")
	}

	return sblock, nil
}

// h5eaCacheSblockLoad loads (H5C__load_entry) a super block of an
// extensible array.
func h5eaCacheSblockLoad(udata *h5eaSblockCacheUd) (*h5eaSblock, error) {
	img, err := h5eaCacheLoadImage(udata.hdr.f, udata.sblkAddr, h5eaCacheSblockGetInitialLoadSize(udata), h5eaCacheSblockVerifyChksum, "super block")
	if err != nil {
		return nil, err
	}
	return h5eaCacheSblockDeserialize(img, udata)
}

// h5eaCacheDblockGetInitialLoadSize is
// H5EA__cache_dblock_get_initial_load_size.
func h5eaCacheDblockGetInitialLoadSize(udata *h5eaDblockCacheUd) uint64 {
	/* Set up fake data block for computing size on disk */
	/* (Note: extracted from H5EA__dblock_alloc) */

	/* need to set:
	 *
	 *    dblock.hdr
	 *    dblock.npages
	 *    dblock.nelmts
	 *
	 * before we invoke either H5EA_DBLOCK_PREFIX_SIZE() or
	 * H5EA_DBLOCK_SIZE().
	 */
	dblock := h5eaDblock{hdr: udata.hdr, nelmts: udata.nelmts}

	if udata.nelmts > udata.hdr.dblkPageNelmts {
		/* Set the # of pages in the direct block */
		dblock.npages = udata.nelmts / udata.hdr.dblkPageNelmts
	}

	/* Set the image length size */
	if dblock.npages == 0 {
		return h5eaDblockSize(&dblock)
	}
	return h5eaDblockPrefixSize(&dblock)
}

// h5eaCacheDblockVerifyChksum is H5EA__cache_dblock_verify_chksum.
func h5eaCacheDblockVerifyChksum(img []byte) (bool, error) {
	return h5eaCacheVerifyChksum(img)
}

// h5eaCacheDblockDeserialize is H5EA__cache_dblock_deserialize: loads a data
// structure from the disk.
func h5eaCacheDblockDeserialize(img []byte, udata *h5eaDblockCacheUd) (*h5eaDblock, error) {
	hdr := udata.hdr
	p := hdr.f.newImage(img)

	/* Allocate the extensible array data block */
	dblock, err := h5eaDblockAlloc(hdr, udata.nelmts)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for extensible array data block")
	}

	if !((dblock.npages == 0 && uint64(len(img)) == h5eaDblockSize(dblock)) ||
		uint64(len(img)) == h5eaDblockPrefixSize(dblock)) {
		return nil, errorf(H5E_EARRAY, H5E_BADSIZE, "bad extensible array data block image size")
	}

	/* Set the extensible array data block's information */
	dblock.addr = udata.dblkAddr

	/* Magic number */
	if p.overflow(h5SizeofMagic + 2) {
		return nil, errorf(H5E_EARRAY, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5eaDblockMagic)) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "wrong extensible array data block signature")
	}

	/* Version */
	if p.uint8() != h5eaDblockVersion {
		return nil, errorf(H5E_EARRAY, H5E_VERSION, "wrong extensible array data block version")
	}

	/* Extensible array type */
	if p.uint8() != hdr.clsID {
		return nil, errorf(H5E_EARRAY, H5E_BADTYPE, "incorrect extensible array class")
	}

	/* Address of header for array that owns this block (just for file integrity checks) */
	if arrAddr := p.addr(); arrAddr != hdr.addr {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "wrong extensible array header address")
	}

	/* Offset of block within the array's address space */
	dblock.blockOff = p.uintVar(int(hdr.arrOffSize))

	/* Internal information */

	/* Only decode elements if the data block is not paged */
	if dblock.npages == 0 {
		/* Decode elements in data block */
		/* Convert from raw elements on disk into native elements in memory */
		if err := h5eaCacheDecodeElmts(hdr, p, dblock.elmts); err != nil {
			return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array data elements")
		}
	}

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5eaSizeofChksum {
		return nil, errorf(H5E_EARRAY, H5E_BADSIZE, "bad decoded extensible array data block size")
	}

	/* Set the data block's size */
	/* (Note: This is not the same as the image length, for paged data blocks) */
	dblock.size = h5eaDblockSize(dblock)

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array data block")
	}

	return dblock, nil
}

// h5eaCacheDblockLoad loads (H5C__load_entry) a data block of an extensible
// array (only the prefix, for a paged data block).
func h5eaCacheDblockLoad(udata *h5eaDblockCacheUd) (*h5eaDblock, error) {
	img, err := h5eaCacheLoadImage(udata.hdr.f, udata.dblkAddr, h5eaCacheDblockGetInitialLoadSize(udata), h5eaCacheDblockVerifyChksum, "data block")
	if err != nil {
		return nil, err
	}
	return h5eaCacheDblockDeserialize(img, udata)
}

// h5eaCacheDblkPageGetInitialLoadSize is
// H5EA__cache_dblk_page_get_initial_load_size.
func h5eaCacheDblkPageGetInitialLoadSize(udata *h5eaDblkPageCacheUd) uint64 {
	/* Set the image length size */
	return h5eaDblkPageSize(udata.hdr)
}

// h5eaCacheDblkPageVerifyChksum is H5EA__cache_dblk_page_verify_chksum.
func h5eaCacheDblkPageVerifyChksum(img []byte) (bool, error) {
	return h5eaCacheVerifyChksum(img)
}

// h5eaCacheDblkPageDeserialize is H5EA__cache_dblk_page_deserialize: loads
// a data structure from the disk.
func h5eaCacheDblkPageDeserialize(img []byte, udata *h5eaDblkPageCacheUd) (*h5eaDblkPage, error) {
	hdr := udata.hdr
	p := hdr.f.newImage(img)

	/* Allocate the extensible array data block page */
	dblkPage, err := h5eaDblkPageAlloc(hdr)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTALLOC, "memory allocation failed for extensible array data block page")
	}

	/* Set the extensible array data block page's information */
	dblkPage.addr = udata.dblkPageAddr

	/* Internal information */

	/* Decode elements in data block page */
	/* Convert from raw elements on disk into native elements in memory */
	if err := h5eaCacheDecodeElmts(hdr, p, dblkPage.elmts); err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array data elements")
	}

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5eaSizeofChksum {
		return nil, errorf(H5E_EARRAY, H5E_BADSIZE, "bad decoded extensible array data block page size")
	}

	/* Set the data block page's size */
	dblkPage.size = uint64(len(img))

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTDECODE, "can't decode extensible array data block page")
	}

	return dblkPage, nil
}

// h5eaCacheDblkPageLoad loads (H5C__load_entry) a data block page of an
// extensible array.
func h5eaCacheDblkPageLoad(udata *h5eaDblkPageCacheUd) (*h5eaDblkPage, error) {
	img, err := h5eaCacheLoadImage(udata.hdr.f, udata.dblkPageAddr, h5eaCacheDblkPageGetInitialLoadSize(udata), h5eaCacheDblkPageVerifyChksum, "data block page")
	if err != nil {
		return nil, err
	}
	return h5eaCacheDblkPageDeserialize(img, udata)
}
