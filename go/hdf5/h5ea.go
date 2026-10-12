package hdf5

// Translated from src/H5EA.c (read side only: open, get, iterate). The
// package-private types, macros and constants of src/H5EApkg.h and
// src/H5EAprivate.h live here as well.
//
// An extensible array is a header ("EAHD") pointing to an index block
// ("EAIB") that holds the first few elements directly, the addresses of the
// data blocks ("EADB") of the first super blocks, and the addresses of the
// remaining super blocks ("EASB"). A super block holds the addresses of its
// data blocks and, when those data blocks are larger than a data block page,
// a 'page init' bitmap per data block; the pages of a paged data block follow
// the data block prefix in the file and each carries its own checksum.

import "errors"

/**************************/
/* Package Private Macros */
/**************************/

// H5EA_HDR_MAGIC / H5EA_IBLOCK_MAGIC / H5EA_SBLOCK_MAGIC / H5EA_DBLOCK_MAGIC
// (H5Fprivate.h)
const (
	h5eaHdrMagic    = "EAHD" /* Header */
	h5eaIblockMagic = "EAIB" /* Index block */
	h5eaSblockMagic = "EASB" /* Super block */
	h5eaDblockMagic = "EADB" /* Data block */
)

// H5EA_cls_id_t
const (
	h5eaClsChunk     = 0 /* Extensible array is for indexing dataset chunks w/o filters */
	h5eaClsFiltChunk = 1 /* Extensible array is for indexing dataset chunks w/filters */
	h5eaClsTest      = 2 /* Extensible array is for testing (do not use for actual data) */
	h5eaNumClsID     = 3 /* Number of Extensible Array class IDs (must be last) */
)

// H5EA_SIZEOF_CHKSUM: size of checksum information (on disk).
const h5eaSizeofChksum = 4

// h5eaMetadataPrefixSize is H5EA_METADATA_PREFIX_SIZE: "standard" size of
// prefix information for extensible array metadata.
func h5eaMetadataPrefixSize(c bool) uint64 {
	n := uint64(h5SizeofMagic /* Signature */ +
		1 /* Version */ +
		1 /* Array type */)
	if c {
		n += h5eaSizeofChksum /* Metadata checksum */
	}
	return n
}

// h5eaHeaderSize is H5EA_HEADER_SIZE: size of the extensible array header on
// disk.
func h5eaHeaderSize(sizeofAddr, sizeofSize uint64) uint64 {
	return h5eaMetadataPrefixSize(true) + /* General metadata fields */

		/* General array information */
		1 /* Element Size */ +
		1 /* Max. # of elements bits */ +
		1 /* # of elements to store in index block */ +
		1 /* Min. # elements per data block */ +
		1 /* Min. # of data block pointers for a super block */ +
		1 /* Log2(Max. # of elements in data block page) - i.e. # of bits needed to store max. # of elements in data block page */ +

		/* Extensible Array statistics fields */
		sizeofSize /* Number of super blocks created */ +
		sizeofSize /* Size of super blocks created */ +
		sizeofSize /* Number of data blocks created */ +
		sizeofSize /* Size of data blocks created */ +
		sizeofSize /* Max. index set */ +
		sizeofSize /* Number of elements 'realized' */ +

		/* Extensible Array Header specific fields */
		sizeofAddr /* File address of index block */
}

// h5eaHeaderSizeFile is H5EA_HEADER_SIZE_FILE.
func h5eaHeaderSizeFile(f *File) uint64 {
	return h5eaHeaderSize(uint64(f.sizeofAddr), uint64(f.sizeofSize))
}

// h5eaHeaderSizeHdr is H5EA_HEADER_SIZE_HDR.
func h5eaHeaderSizeHdr(h *h5eaHdr) uint64 {
	return h5eaHeaderSize(h.sizeofAddr, h.sizeofSize)
}

// h5eaIblockSize is H5EA_IBLOCK_SIZE: size of the extensible array index
// block on disk.
func h5eaIblockSize(i *h5eaIblock) uint64 {
	return h5eaMetadataPrefixSize(true) + /* General metadata fields */

		/* Sanity-checking fields */
		i.hdr.sizeofAddr /* File address of array owning the block */ +

		/* Extensible Array Index Block specific fields */
		uint64(i.hdr.idxBlkElmts)*uint64(i.hdr.eltSize) /* Elements in index block  */ +
		i.ndblkAddrs*i.hdr.sizeofAddr /* Data block addresses in index block  */ +
		i.nsblkAddrs*i.hdr.sizeofAddr /* Super block addresses in index block  */
}

// h5eaSblockSize is H5EA_SBLOCK_SIZE: size of the extensible array super
// block on disk.
func h5eaSblockSize(s *h5eaSblock) uint64 {
	return h5eaMetadataPrefixSize(true) + /* General metadata fields */

		/* Sanity-checking fields */
		s.hdr.sizeofAddr /* File address of array owning the block */ +
		uint64(s.hdr.arrOffSize) /* Offset of the block in the array */ +

		/* Extensible Array Super Block specific fields */
		s.ndblks*s.dblkPageInitSize /* Data block 'page init' bitmasks in super block (can be 0 if no pages) */ +
		s.ndblks*s.hdr.sizeofAddr /* Data block addresses in super block  */
}

// h5eaDblockPrefixSize is H5EA_DBLOCK_PREFIX_SIZE: size of the extensible
// array data block prefix on disk.
func h5eaDblockPrefixSize(d *h5eaDblock) uint64 {
	return h5eaMetadataPrefixSize(true) + /* General metadata fields */

		/* Sanity-checking fields */
		d.hdr.sizeofAddr /* File address of array owning the block */ +
		uint64(d.hdr.arrOffSize) /* Offset of the block in the array */
}

// h5eaDblockSize is H5EA_DBLOCK_SIZE: size of the extensible array data
// block on disk.
func h5eaDblockSize(d *h5eaDblock) uint64 {
	return h5eaDblockPrefixSize(d) + /* Data block prefix size  */

		/* Extensible Array Data Block specific fields */
		d.nelmts*uint64(d.hdr.eltSize) /* Elements in data block  */ +
		d.npages*h5eaSizeofChksum /* Checksum for each page */
}

// h5eaDblkPageSize is H5EA_DBLK_PAGE_SIZE: size of the extensible array data
// block page on disk.
func h5eaDblkPageSize(h *h5eaHdr) uint64 {
	return h.dblkPageNelmts*uint64(h.eltSize) /* Elements in data block page */ +
		h5eaSizeofChksum /* Checksum for each page */
}

// h5eaSizeofOffsetBits is H5EA_SIZEOF_OFFSET_BITS: compute the # of bytes
// required to store an offset into a given buffer size.
func h5eaSizeofOffsetBits(b uint64) uint64 { return (b + 7) / 8 }

// h5eaSblkFirstIdx is H5EA_SBLK_FIRST_IDX: compute the first super block
// index that will hold a certain # of data block pointers.
func h5eaSblkFirstIdx(m uint8) uint64 { return 2 * uint64(h5vmLog2Of2(uint32(m))) }

/****************************/
/* Package Private Typedefs */
/****************************/

// h5eaClass is H5EA_class_t (decode side). decode decodes one element of
// hdr.eltSize bytes from p (the C callback decodes nelmts elements at a
// time; the loop is in the callers); fill returns the value of a
// never-written element.
type h5eaClass struct {
	id     uint8
	name   string /* Name of class (for debugging) */
	decode func(f *File, ctx any, p *image) (any, error)
	fill   func(ctx any) any
}

// h5eaSblkInfo is H5EA_sblk_info_t: information for each super block in
// extensible array.
type h5eaSblkInfo struct {
	ndblks     uint64 /* Number of data blocks for a super block */
	dblkNelmts uint64 /* Number of elements in each data block for super block */
	startIdx   uint64 /* Index of first element in super block */
	startDblk  uint64 /* Index of first data block in super block */
}

// h5eaHdr is H5EA_hdr_t: the extensible array header information. The
// creation parameters (H5EA_create_t cparam) and the stored statistics
// (H5EA_stat_t stats.stored) are flattened into it.
type h5eaHdr struct {
	addr    haddr /* Address of header in file */
	version uint8
	clsID   uint8 /* cparam.cls->id */
	eltSize uint8 /* cparam.raw_elmt_size: element size in file (in bytes) */
	// H5EA_create_t cparam
	maxNelmtsBits         uint8 /* Log2(Max. # of elements in array) - i.e. # of bits needed to store max. # of elements */
	idxBlkElmts           uint8 /* # of elements to store in index block */
	supBlkMinDataPtrs     uint8 /* Min. # of data block pointers for a super block */
	dataBlkMinElmts       uint8 /* Min. # of elements per data block */
	maxDblkPageNelmtsBits uint8 /* Log2(Max. # of elements in data block page) */
	// stats (stored)
	nsuperBlks, ndataBlks, nelmts, maxIdxSet uint64
	idxBlkAddr                               haddr /* Address of index block in header */

	superBlkSize uint64 /* stats.stored.super_blk_size: size of super blocks allocated */
	dataBlkSize  uint64 /* stats.stored.data_blk_size: size of data blocks allocated */
	// stats (computed)
	hdrSize      uint64 /* Size of header */
	nindexBlks   uint64 /* # of index blocks (should be 0 or 1) */
	indexBlkSize uint64 /* Size of index blocks allocated */

	/* Computed/cached values (not stored in header) */
	f          *File
	size       uint64 /* Size of header in file */
	sizeofAddr uint64 /* Size of file addresses */
	sizeofSize uint64 /* Size of file sizes */
	arrOffSize uint8  /* Size of array offsets (in bytes) */

	/* Super block information (not stored) */
	nsblks   uint64         /* Number of superblocks needed for array */
	sblkInfo []h5eaSblkInfo /* Array of information for each super block */

	/* Data block information (not stored) */
	dblkPageNelmts uint64 /* # of elements per data block page */

	/* Client information (not stored) */
	cls   *h5eaClass /* cparam.cls */
	cbCtx any        /* Callback context */
}

// h5eaIblock is H5EA_iblock_t: the extensible array index block
// information.
type h5eaIblock struct {
	/* Extensible array information (stored) */
	elmts     []any   /* Buffer for elements stored in index block  */
	dblkAddrs []haddr /* Buffer for addresses of data blocks in index block */
	sblkAddrs []haddr /* Buffer for addresses of super blocks in index block */

	/* Internal array information (not stored) */
	hdr  *h5eaHdr /* Shared array header info */
	addr haddr    /* Address of this index block on disk */
	size uint64   /* Size of index block on disk */

	/* Computed/cached values (not stored) */
	nsblks     uint64 /* # of super blocks whose data block addresses are in index block */
	ndblkAddrs uint64 /* Number of pointers to data blocks in index block */
	nsblkAddrs uint64 /* Number of pointers to super blocks in index block */
}

// h5eaSblock is H5EA_sblock_t: the extensible array super block
// information.
type h5eaSblock struct {
	/* Extensible array information (stored) */
	blockOff  uint64  /* Offset of the block within the array's address space */
	dblkAddrs []haddr /* Addresses of data blocks in super block */
	pageInit  []byte  /* Bitmap of whether a data block page is initialized */

	/* Internal array information (not stored) */
	hdr  *h5eaHdr /* Shared array header info */
	addr haddr    /* Address of this index block on disk */
	size uint64   /* Size of index block on disk */

	/* Computed/cached values (not stored) */
	idx              uint64 /* Super block index within the extensible array */
	ndblks           uint64 /* # of data block addresses that are in super block */
	dblkNelmts       uint64 /* # of elements for data blocks reachable through this super block */
	dblkNpages       uint64 /* # of pages in each data block */
	dblkPageInitSize uint64 /* Size of 'page init' bitmask for each data block */
	dblkPageSize     uint64 /* Size of a data block page */
}

// h5eaDblock is H5EA_dblock_t: the extensible array data block information.
type h5eaDblock struct {
	/* Extensible array information (stored) */
	blockOff uint64 /* Offset of the block within the array's address space */
	elmts    []any  /* Buffer for elements stored in data block  */

	/* Internal array information (not stored) */
	hdr  *h5eaHdr /* Shared array header info */
	addr haddr    /* Address of this data block on disk */
	size uint64   /* Size of data block on disk */

	/* Computed/cached values (not stored) */
	nelmts uint64 /* Number of elements in block */
	npages uint64 /* Number of pages in a block (zero if not paged) */
}

// h5eaDblkPage is H5EA_dblk_page_t: the extensible array data block page
// information.
type h5eaDblkPage struct {
	/* Extensible array information (stored) */
	elmts []any /* Buffer for elements stored in data block page */

	/* Internal array information (not stored) */
	hdr  *h5eaHdr /* Shared array header info */
	addr haddr    /* Address of this data block page on disk */
	size uint64   /* Size of data block page on disk */
}

// h5ea is H5EA_t: an open extensible array.
type h5ea struct {
	f   *File
	hdr *h5eaHdr   /* Pointer to internal extensible array header info */
	cls *h5eaClass /* Class the array was opened with */
	ctx any        /* Client callback context */
}

// h5eaNew is H5EA__new: allocate and initialize a new extensible array
// wrapper in memory.
func h5eaNew(f *File, eaAddr haddr, fromOpen bool, cls *h5eaClass, ctx any) (*h5ea, error) {
	/* Check arguments */
	if !addrDefined(eaAddr) {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "undefined extensible array header address")
	}
	if cls == nil || cls.decode == nil || cls.fill == nil {
		return nil, errorf(H5E_EARRAY, H5E_BADVALUE, "invalid extensible array class")
	}

	/* Allocate extensible array wrapper */
	ea := &h5ea{}

	/* Lock the array header into memory */
	hdr, err := h5eaHdrProtect(f, eaAddr, cls, ctx)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to load extensible array header")
	}

	/* (pending deletion can't happen for a read-only file) */
	_ = fromOpen

	/* Point extensible array wrapper at header */
	ea.hdr = hdr

	/* Set file pointer for this array open context */
	ea.f = f
	ea.cls = hdr.cls
	ea.ctx = hdr.cbCtx

	return ea, nil
}

// eaOpen is H5EA_open: open an existing extensible array in a file. cls must
// be the class whose id is stored in the header (the C library maps the id
// through H5EA_client_class_g); ctx is the client callback context (the
// result of the class' crt_context callback).
func (f *File) eaOpen(addr haddr, cls *h5eaClass, ctx any) (*h5ea, error) {
	/* Allocate and initialize new extensible array wrapper */
	ea, err := h5eaNew(f, addr, true, cls, ctx)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTINIT, "allocation and/or initialization failed for extensible array wrapper")
	}
	return ea, nil
}

// getNelmts is H5EA_get_nelmts: query the current number of elements in
// array.
func (ea *h5ea) getNelmts() uint64 {
	/* Retrieve the max. index set */
	return ea.hdr.maxIdxSet
}

// getAddr is H5EA_get_addr: query the address of the array.
func (ea *h5ea) getAddr() haddr {
	/* Retrieve the address of the extensible array's header */
	return ea.hdr.addr
}

// h5eaLookupElmt is H5EA__lookup_elmt (read-only access): retrieve the
// element buffer of the metadata object holding a given element in the
// array, and the index of the element in that buffer. A nil buffer means
// the object holding the element has not been created yet.
func h5eaLookupElmt(ea *h5ea, idx uint64) (thingElmtBuf []any, thingElmtIdx uint64, err error) {
	hdr := ea.hdr /* Header for EA */

	/* Check if we should create the index block */
	if !addrDefined(hdr.idxBlkAddr) {
		/* (read-only access: nothing to look up) */
		return nil, 0, nil
	}

	/* Protect index block */
	iblock, err := h5eaIblockProtect(hdr)
	if err != nil {
		return nil, 0, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array index block, address = %d", hdr.idxBlkAddr)
	}

	/* Check if element is in index block */
	if idx < uint64(hdr.idxBlkElmts) {
		/* Set 'thing' info to refer to the index block */
		return iblock.elmts, idx, nil
	}

	/* Get super block index where element is located */
	sblkIdx := h5eaDblockSblkIdx(hdr, idx)
	if sblkIdx >= hdr.nsblks {
		return nil, 0, errorf(H5E_EARRAY, H5E_BADRANGE, "extensible array index %d beyond the array's maximum size", idx)
	}
	info := &hdr.sblkInfo[sblkIdx]

	/* Adjust index to offset in super block */
	elmtIdx := idx - (uint64(hdr.idxBlkElmts) + info.startIdx)

	/* Check for data block containing element address in the index block */
	if sblkIdx < iblock.nsblks {
		/* Compute the data block index in index block */
		dblkIdx := info.startDblk + (elmtIdx / info.dblkNelmts)
		if dblkIdx >= iblock.ndblkAddrs {
			return nil, 0, errorf(H5E_EARRAY, H5E_BADRANGE, "extensible array data block index %d out of range", dblkIdx)
		}

		/* Check if the data block has been allocated on disk yet */
		if !addrDefined(iblock.dblkAddrs[dblkIdx]) {
			/* (read-only access) */
			return nil, 0, nil
		}

		/* Protect data block */
		dblock, err := h5eaDblockProtect(hdr, iblock.dblkAddrs[dblkIdx], info.dblkNelmts)
		if err != nil {
			return nil, 0, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array data block, address = %d", iblock.dblkAddrs[dblkIdx])
		}

		/* Adjust index to offset in data block */
		elmtIdx %= info.dblkNelmts

		/* (a data block reached through the index block is never paged:
		 *  its size is below the first super block's) */
		if dblock.npages > 0 {
			return nil, 0, errorf(H5E_EARRAY, H5E_BADVALUE, "paged extensible array data block in index block")
		}

		/* Set 'thing' info to refer to the data block */
		return dblock.elmts, elmtIdx, nil
	}

	/* Calculate offset of super block in index block's array */
	sblkOff := sblkIdx - iblock.nsblks
	if sblkOff >= iblock.nsblkAddrs {
		return nil, 0, errorf(H5E_EARRAY, H5E_BADRANGE, "extensible array super block index %d out of range", sblkIdx)
	}

	/* Check if the super block has been allocated on disk yet */
	if !addrDefined(iblock.sblkAddrs[sblkOff]) {
		/* (read-only access) */
		return nil, 0, nil
	}

	/* Protect super block */
	sblock, err := h5eaSblockProtect(hdr, iblock.sblkAddrs[sblkOff], sblkIdx)
	if err != nil {
		return nil, 0, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array super block, address = %d", iblock.sblkAddrs[sblkOff])
	}

	/* Compute the data block index in super block */
	dblkIdx := elmtIdx / sblock.dblkNelmts
	if dblkIdx >= sblock.ndblks {
		return nil, 0, errorf(H5E_EARRAY, H5E_BADRANGE, "extensible array data block index %d out of range", dblkIdx)
	}

	/* Check if the data block has been allocated on disk yet */
	if !addrDefined(sblock.dblkAddrs[dblkIdx]) {
		/* (read-only access) */
		return nil, 0, nil
	}

	/* Adjust index to offset in data block */
	elmtIdx %= sblock.dblkNelmts

	/* Check if the data block is paged */
	if sblock.dblkNpages > 0 {
		/* Compute page index */
		pageIdx := elmtIdx / hdr.dblkPageNelmts

		/* Compute 'page init' index */
		pageInitIdx := (dblkIdx * sblock.dblkNpages) + pageIdx

		/* Adjust index to offset in data block page */
		elmtIdx %= hdr.dblkPageNelmts

		/* Compute data block page address */
		dblkPageAddr := sblock.dblkAddrs[dblkIdx] + h5eaDblockPrefixSize(&h5eaDblock{hdr: hdr}) +
			(pageIdx * sblock.dblkPageSize)

		/* Check if page has been initialized yet */
		if !h5vmBitGet(sblock.pageInit, pageInitIdx) {
			/* (read-only access) */
			return nil, 0, nil
		}

		/* Protect data block page */
		dblkPage, err := h5eaDblkPageProtect(hdr, dblkPageAddr)
		if err != nil {
			return nil, 0, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array data block page, address = %d", dblkPageAddr)
		}

		/* Set 'thing' info to refer to the data block page */
		return dblkPage.elmts, elmtIdx, nil
	}

	/* Protect data block */
	dblock, err := h5eaDblockProtect(hdr, sblock.dblkAddrs[dblkIdx], sblock.dblkNelmts)
	if err != nil {
		return nil, 0, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect extensible array data block, address = %d", sblock.dblkAddrs[dblkIdx])
	}

	/* Set 'thing' info to refer to the data block */
	return dblock.elmts, elmtIdx, nil
}

// get is H5EA_get: the decoded element at idx (cls.fill(ctx) when the
// containing block has not been allocated).
func (ea *h5ea) get(idx uint64) (any, error) {
	hdr := ea.hdr /* Header for EA */

	/* Check for element beyond max. element in array */
	if idx >= hdr.maxIdxSet {
		/* Call the class's 'fill' callback */
		return ea.cls.fill(ea.ctx), nil
	}

	/* Look up the array metadata containing the element we want to set */
	thingElmtBuf, thingElmtIdx, err := h5eaLookupElmt(ea, idx)
	if err != nil {
		return nil, wrapf(err, H5E_EARRAY, H5E_CANTPROTECT, "unable to protect array metadata")
	}

	/* Check if the thing holding the element has been created yet */
	if thingElmtBuf == nil {
		/* Call the class's 'fill' callback */
		return ea.cls.fill(ea.ctx), nil
	}
	if thingElmtIdx >= uint64(len(thingElmtBuf)) {
		return nil, errorf(H5E_EARRAY, H5E_BADRANGE, "extensible array element index %d out of range", thingElmtIdx)
	}

	/* Get element from thing's element buffer */
	return thingElmtBuf[thingElmtIdx], nil
}

// iterate is H5EA_iterate: iterate over the elements of an extensible array
// (indices 0 .. max_idx_set-1), calling op for each. op may return
// errBIterStop to stop early without error.
func (ea *h5ea) iterate(op func(idx uint64, elmt any) error) error {
	/* Iterate over all elements in array */
	for u := uint64(0); u < ea.hdr.maxIdxSet; u++ {
		/* Get array element */
		elmt, err := ea.get(u)
		if err != nil {
			return wrapf(err, H5E_EARRAY, H5E_CANTGET, "unable to delete fixed array")
		}

		/* Make callback */
		if err := op(u, elmt); err != nil {
			if errors.Is(err, errBIterStop) {
				return nil
			}
			return wrapf(err, H5E_EARRAY, H5E_BADITER, "iteration callback error")
		}
	}
	return nil
}
