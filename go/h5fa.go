package hdf5

// Translated from src/H5FA.c (read side only: open, get, iterate). The
// package-private types, macros and constants of src/H5FApkg.h and
// src/H5FAprivate.h live here as well.
//
// A fixed array is a header ("FAHD") pointing to a single data block
// ("FADB"). When the array has more elements than a data block page holds,
// the data block is paged: its prefix holds a 'page init' bitmap and the
// pages (each with its own checksum) follow the prefix in the file.

import "errors"

/**************************/
/* Package Private Macros */
/**************************/

// H5FA_HDR_MAGIC / H5FA_DBLOCK_MAGIC (H5Fprivate.h)
const (
	h5faHdrMagic    = "FAHD" /* Header */
	h5faDblockMagic = "FADB" /* Data block */
)

// H5FA_cls_id_t
const (
	h5faClsChunk     = 0 /* Chunk index of dataset (unfiltered) */
	h5faClsFiltChunk = 1 /* Chunk index of dataset (filtered) */
	h5faClsTest      = 2 /* Fixed array is for testing (do not use for actual data) */
	h5faNumClsID     = 3 /* Number of Fixed Array class IDs (must be last) */
)

// H5FA_SIZEOF_CHKSUM: size of checksum information (on disk).
const h5faSizeofChksum = 4

// h5faMetadataPrefixSize is H5FA_METADATA_PREFIX_SIZE: "standard" size of
// prefix information for fixed array metadata.
func h5faMetadataPrefixSize(c bool) uint64 {
	n := uint64(h5SizeofMagic /* Signature */ +
		1 /* Version */ +
		1 /* Array type */)
	if c {
		n += h5faSizeofChksum /* Metadata checksum */
	}
	return n
}

// h5faHeaderSize is H5FA_HEADER_SIZE: size of the Fixed Array header on
// disk.
func h5faHeaderSize(sizeofAddr, sizeofSize uint64) uint64 {
	return h5faMetadataPrefixSize(true) + /* General metadata fields */
		/* General array information */
		1 /* Element Size */ +
		1 /* Log2(Max. # of elements in data block page) - i.e. # of bits needed to store max. # of elements in data block page */ +

		/* Fixed Array statistics fields */
		sizeofSize /* # of elements in the fixed array */ +

		/* Fixed Array Header specific fields */
		sizeofAddr /* File address of Fixed Array data block */
}

// h5faHeaderSizeFile is H5FA_HEADER_SIZE_FILE.
func h5faHeaderSizeFile(f *File) uint64 {
	return h5faHeaderSize(uint64(f.sizeofAddr), uint64(f.sizeofSize))
}

// h5faHeaderSizeHdr is H5FA_HEADER_SIZE_HDR.
func h5faHeaderSizeHdr(h *h5faHdr) uint64 {
	return h5faHeaderSize(h.sizeofAddr, h.sizeofSize)
}

// h5faDblockPrefixSize is H5FA_DBLOCK_PREFIX_SIZE: size of the Fixed Array
// data block prefix on disk.
func h5faDblockPrefixSize(d *h5faDblock) uint64 {
	return h5faMetadataPrefixSize(true) + /* General metadata fields */
		/* Sanity-checking fields */
		d.hdr.sizeofAddr /* File address of Fixed Array header owning the data block */ +

		/* Fixed Array Data Block specific fields */
		d.dblkPageInitSize /* Fixed array data block 'page init' bitmasks (can be 0 if no pages) */
}

// h5faDblockSize is H5FA_DBLOCK_SIZE: size of the Fixed Array data block on
// disk.
func h5faDblockSize(d *h5faDblock) uint64 {
	return h5faDblockPrefixSize(d) + /* Data block prefix size  */
		/* Fixed Array Elements|Pages of Elements*/
		d.hdr.nelmts*uint64(d.hdr.eltSize) +
		d.npages*h5faSizeofChksum /* Checksum */
}

// h5faDblkPageSize is H5FA_DBLK_PAGE_SIZE: size of the Fixed Array data
// block page on disk.
func h5faDblkPageSize(h *h5faHdr, nelmts uint64) uint64 {
	return nelmts*uint64(h.eltSize) /* Elements in data block page */ +
		h5faSizeofChksum /* Checksum for each page */
}

/****************************/
/* Package Private Typedefs */
/****************************/

// h5faClass is H5FA_class_t (decode side). decode decodes one element of
// hdr.eltSize bytes from p (the C callback decodes nelmts elements at a
// time; the loop is in the callers); fill returns the value of a
// never-written element.
type h5faClass struct {
	id     uint8
	name   string                                        /* Name of class (for debugging) */
	decode func(f *File, ctx any, p *image) (any, error) /* decode one element of hdr.eltSize bytes */
	fill   func(ctx any) any                             /* value of a never-written element */
}

// h5faHdr is H5FA_hdr_t: the Fixed Array header information. The creation
// parameters (H5FA_create_t cparam) are flattened into it.
type h5faHdr struct {
	addr    haddr /* Address of header in file */
	version uint8
	clsID   uint8 /* cparam.cls->id */
	eltSize uint8 /* Element size in file (in bytes) */
	// maxNelmtsBits holds cparam.max_dblk_page_nelmts_bits, as does
	// maxDblkPageNelmtsBits (the field predates the translation).
	maxNelmtsBits uint8
	nelmts        uint64 /* # of elements in the array */
	dblkAddr      haddr  /* Address of data block */

	maxDblkPageNelmtsBits uint8 /* Log2(Max. # of elements in a data block page) */

	/* Statistics for Fixed Array (stored in header) */
	hdrSize  uint64 /* stats.hdr_size: size of header */
	dblkSize uint64 /* stats.dblk_size: size of data block */

	/* Computed/cached values (not stored in header) */
	f          *File
	size       uint64 /* Size of header in file */
	sizeofAddr uint64 /* Size of file addresses */
	sizeofSize uint64 /* Size of file sizes */

	/* Client information (not stored) */
	cls   *h5faClass /* cparam.cls */
	cbCtx any        /* Callback context */
}

// h5faDblock is H5FA_dblock_t: the fixed array data block information.
type h5faDblock struct {
	/* Fixed array information (stored) */
	dblkPageInit []byte /* Bitmap of whether a data block page is initialized */
	elmts        []any  /* Buffer for elements stored in data block */

	/* Internal array information (not stored) */
	hdr *h5faHdr /* Shared array header info */

	/* Computed/cached values (not stored) */
	addr           haddr  /* Address of this data block on disk */
	size           uint64 /* Size of data block on disk */
	npages         uint64 /* Number of pages in data block (zero if not paged) */
	lastPageNelmts uint64 /* Number of elements in last page, if paged */

	/* Fixed Array data block information (not stored) */
	dblkPageNelmts   uint64 /* # of elements per data block page */
	dblkPageSize     uint64 /* Size of a data block page */
	dblkPageInitSize uint64 /* Size of 'page init' bitmask */
}

// h5faDblkPage is H5FA_dblk_page_t: the fixed array data block page
// information.
type h5faDblkPage struct {
	/* Fixed array information (stored) */
	elmts []any /* Buffer for elements stored in data block page */

	/* Internal array information (not stored) */
	hdr *h5faHdr /* Shared array header info */

	/* Computed/cached values (not stored) */
	addr   haddr  /* Address of this data block page on disk */
	size   uint64 /* Size of data block page on disk */
	nelmts uint64 /* Number of elements in data block page */
}

// h5fa is H5FA_t: an open fixed array.
type h5fa struct {
	f   *File
	hdr *h5faHdr   /* Pointer to internal fixed array header info */
	cls *h5faClass /* Class the array was opened with */
	ctx any        /* Client callback context */
}

// h5faNew is H5FA__new: allocate and initialize a new fixed array wrapper in
// memory.
func h5faNew(f *File, faAddr haddr, fromOpen bool, cls *h5faClass, ctx any) (*h5fa, error) {
	/* Check arguments */
	if !addrDefined(faAddr) {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "undefined fixed array header address")
	}
	if cls == nil || cls.decode == nil || cls.fill == nil {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "invalid fixed array class")
	}

	/* Allocate fixed array wrapper */
	fa := &h5fa{}

	/* Lock the array header into memory */
	hdr, err := h5faHdrProtect(f, faAddr, cls, ctx)
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTPROTECT, "unable to load fixed array header")
	}

	/* (pending deletion can't happen for a read-only file) */
	_ = fromOpen

	/* Point fixed array wrapper at header */
	fa.hdr = hdr

	/* Set file pointer for this array open context */
	fa.f = f
	fa.cls = hdr.cls
	fa.ctx = hdr.cbCtx

	return fa, nil
}

// faOpen is H5FA_open: open an existing fixed array in a file. cls must be
// the class whose id is stored in the header (the C library maps the id
// through H5FA_client_class_g); ctx is the client callback context (the
// result of the class' crt_context callback).
func (f *File) faOpen(addr haddr, cls *h5faClass, ctx any) (*h5fa, error) {
	/* Allocate and initialize new fixed array wrapper */
	fa, err := h5faNew(f, addr, true, cls, ctx)
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTINIT, "allocation and/or initialization failed for fixed array wrapper")
	}
	return fa, nil
}

// getNelmts is H5FA_get_nelmts: query the current number of elements in
// array.
func (fa *h5fa) getNelmts() uint64 {
	/* Retrieve the current number of elements in the fixed array */
	return fa.hdr.nelmts
}

// getAddr is H5FA_get_addr: query the address of the array.
func (fa *h5fa) getAddr() haddr {
	/* Retrieve the address of the fixed array's header */
	return fa.hdr.addr
}

// get is H5FA_get: the decoded element at idx (cls.fill(ctx) when the data
// block/page has not been allocated).
func (fa *h5fa) get(idx uint64) (any, error) {
	hdr := fa.hdr /* Header for FA */

	/* (the C routine does not check the index) */
	if idx >= hdr.nelmts {
		return nil, errorf(H5E_FARRAY, H5E_BADRANGE, "fixed array index %d out of range (%d elements)", idx, hdr.nelmts)
	}

	/* Check if the fixed array data block has been allocated on disk yet */
	if !addrDefined(hdr.dblkAddr) {
		/* Call the class's 'fill' callback */
		return fa.cls.fill(fa.ctx), nil
	}

	/* Get the data block */
	dblock, err := h5faDblockProtect(hdr, hdr.dblkAddr)
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTPROTECT, "unable to protect fixed array data block, address = %d", hdr.dblkAddr)
	}

	/* Check for paged data block */
	if dblock.npages == 0 {
		/* Retrieve element from data block */
		return dblock.elmts[idx], nil
	}

	/* paging */

	/* Compute the page index */
	pageIdx := idx / dblock.dblkPageNelmts

	/* Check if the page is defined yet */
	if !h5vmBitGet(dblock.dblkPageInit, pageIdx) {
		/* Call the class's 'fill' callback */
		/* We've retrieved the value, leave now */
		return fa.cls.fill(fa.ctx), nil
	}

	/* get the page */

	/* Compute the element index */
	elmtIdx := idx % dblock.dblkPageNelmts

	/* Compute the address of the data block */
	dblkPageAddr := dblock.addr + h5faDblockPrefixSize(dblock) + (pageIdx * dblock.dblkPageSize)

	/* Check for using last page, to set the number of elements on the page */
	var dblkPageNelmts uint64 /* # of elements in a data block page */
	if (pageIdx + 1) == dblock.npages {
		dblkPageNelmts = dblock.lastPageNelmts
	} else {
		dblkPageNelmts = dblock.dblkPageNelmts
	}

	/* Protect the data block page */
	dblkPage, err := h5faDblkPageProtect(hdr, dblkPageAddr, dblkPageNelmts)
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTPROTECT, "unable to protect fixed array data block page, address = %d", dblkPageAddr)
	}
	if elmtIdx >= uint64(len(dblkPage.elmts)) {
		return nil, errorf(H5E_FARRAY, H5E_BADRANGE, "fixed array element index %d out of range", elmtIdx)
	}

	/* Retrieve element from data block */
	return dblkPage.elmts[elmtIdx], nil
}

// iterate is H5FA_iterate: iterate over the elements of a fixed array,
// calling op for each. op may return errBIterStop to stop early without
// error.
func (fa *h5fa) iterate(op func(idx uint64, elmt any) error) error {
	/* Iterate over all elements in array */
	for u := uint64(0); u < fa.hdr.nelmts; u++ {
		/* Get array element */
		elmt, err := fa.get(u)
		if err != nil {
			return wrapf(err, H5E_FARRAY, H5E_CANTGET, "unable to delete fixed array")
		}

		/* Invoke callback */
		if err := op(u, elmt); err != nil {
			if errors.Is(err, errBIterStop) {
				return nil
			}
			return wrapf(err, H5E_FARRAY, H5E_BADITER, "iteration callback error")
		}
	}
	return nil
}
