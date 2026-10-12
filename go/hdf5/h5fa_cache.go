package hdf5

// Translated from src/H5FAcache.c (read side only: the
// get_initial_load_size, verify_chksum and deserialize callbacks of the
// header, data block and data block page cache clients). The
// h5faCache*Load functions play the role of H5C__load_entry: read the
// image, verify the checksum (when the file's checksum option is on) and
// deserialize it.

import "bytes"

/****************/
/* Local Macros */
/****************/

/* Fixed Array format version #'s */
const (
	h5faHdrVersion    = 0 /* Header */
	h5faDblockVersion = 0 /* Data block */
)

/******************/
/* Local Typedefs */
/******************/

// h5faDblockCacheUd is H5FA_dblock_cache_ud_t: info needed for loading data
// block.
type h5faDblockCacheUd struct {
	hdr      *h5faHdr /* Shared fixed array information */
	dblkAddr haddr    /* Address of data block on disk */
}

// h5faDblkPageCacheUd is H5FA_dblk_page_cache_ud_t: info needed for loading
// data block page.
type h5faDblkPageCacheUd struct {
	hdr          *h5faHdr /* Shared fixed array information */
	nelmts       uint64   /* Number of elements in data block page */
	dblkPageAddr haddr    /* Address of data block page on disk */
}

// h5faGetChecksums is H5F_get_checksums for fixed array metadata: the
// checksum stored in the last four bytes of a metadata image and the one
// computed over the rest.
func h5faGetChecksums(img []byte) (stored, computed uint32, err error) {
	if len(img) < h5faSizeofChksum {
		return 0, 0, errorf(H5E_FARRAY, H5E_CANTGET, "image too small for a checksum")
	}
	n := len(img) - h5faSizeofChksum
	return le32(img[n:]), checksumMetadata(img[:n], 0), nil
}

// h5faCacheVerifyChksum is the body shared by all the verify_chksum
// callbacks (H5FA__cache_*_verify_chksum): verify the computed checksum of
// the data structure is the same as the stored chksum.
func h5faCacheVerifyChksum(img []byte) (bool, error) {
	/* Get stored and computed checksums */
	stored, computed, err := h5faGetChecksums(img)
	if err != nil {
		return false, wrapf(err, H5E_FARRAY, H5E_CANTGET, "can't get checksums")
	}
	return stored == computed, nil
}

// h5faCacheLoadImage reads the size-byte image of a piece of fixed array
// metadata at addr and verifies its checksum when the file's checksum
// option is on (H5C__load_entry).
func h5faCacheLoadImage(f *File, addr haddr, size uint64, verifyChksum func([]byte) (bool, error), what string) ([]byte, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "undefined fixed array %s address", what)
	}
	if size > f.eof() {
		return nil, errorf(H5E_FARRAY, H5E_OVERFLOW, "fixed array %s size %d larger than the file", what, size)
	}
	img, err := f.readAt(addr, int(size))
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTLOAD, "unable to read fixed array %s", what)
	}
	if f.opts.verifyChecksums {
		ok, err := verifyChksum(img)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errorf(H5E_FARRAY, H5E_BADCHECKSUM, "incorrect metadata checksum for fixed array %s", what)
		}
	}
	return img, nil
}

// h5faCacheDecodeElmts calls the class' decode callback for each element of
// elmts (cls->decode(image, elmts, nelmts, ctx)), consuming exactly
// raw_elmt_size bytes of p per element.
func h5faCacheDecodeElmts(hdr *h5faHdr, p *image, elmts []any) error {
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

// h5faCacheHdrGetInitialLoadSize is H5FA__cache_hdr_get_initial_load_size.
func h5faCacheHdrGetInitialLoadSize(f *File) uint64 {
	/* Set the image length size */
	return h5faHeaderSizeFile(f)
}

// h5faCacheHdrVerifyChksum is H5FA__cache_hdr_verify_chksum: verify the
// computed checksum of the data structure is the same as the stored chksum.
func h5faCacheHdrVerifyChksum(img []byte) (bool, error) {
	return h5faCacheVerifyChksum(img)
}

// h5faCacheHdrDeserialize is H5FA__cache_hdr_deserialize: loads a data
// structure from the disk.
func h5faCacheHdrDeserialize(f *File, img []byte, addr haddr, cls *h5faClass, ctx any) (*h5faHdr, error) {
	p := f.newImage(img)

	/* Allocate space for the fixed array data structure */
	hdr := h5faHdrAlloc(f)

	/* Set the fixed array header's address */
	hdr.addr = addr

	/* (the whole fixed-size header must be present) */
	if p.overflow(int(h5faHeaderSizeFile(f))) {
		return nil, errorf(H5E_FARRAY, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}

	/* Magic number */
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5faHdrMagic)) {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "wrong fixed array header signature")
	}

	/* Version */
	hdr.version = p.uint8()
	if hdr.version != h5faHdrVersion {
		return nil, errorf(H5E_FARRAY, H5E_VERSION, "wrong fixed array header version")
	}

	/* Fixed array class */
	id := p.uint8()
	if id >= h5faNumClsID {
		return nil, errorf(H5E_FARRAY, H5E_BADTYPE, "incorrect fixed array class")
	}
	hdr.clsID = id

	/* General array creation/configuration information */
	hdr.eltSize = p.uint8()               /* Element size in file (in bytes) */
	hdr.maxDblkPageNelmtsBits = p.uint8() /* Log2(Max. # of elements in data block page) - i.e. # of bits needed to store max. # of elements in data block page. */
	hdr.maxNelmtsBits = hdr.maxDblkPageNelmtsBits

	/* Array statistics */
	hdr.nelmts = p.length() /* Number of elements */

	/* Internal information */
	hdr.dblkAddr = p.addr() /* Address of index block */

	/* (the C code shifts by these and divides by the element size) */
	if hdr.eltSize == 0 {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "element size must be greater than zero")
	}
	if hdr.maxDblkPageNelmtsBits >= 64 {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "bad max. # of elements per data block page bits %d", hdr.maxDblkPageNelmtsBits)
	}

	/* Check for data block */
	if addrDefined(hdr.dblkAddr) {
		/* Set up fake data block for computing size on disk */
		dblock := h5faDblock{hdr: hdr}
		dblkPageNelmts := uint64(1) << hdr.maxDblkPageNelmtsBits /* # of elements per data block page */
		if hdr.nelmts > dblkPageNelmts {
			dblock.npages = ((hdr.nelmts + dblkPageNelmts) - 1) / dblkPageNelmts
			dblock.dblkPageInitSize = (dblock.npages + 7) / 8
		}

		/* Compute Fixed Array data block size for hdr statistics */
		hdr.dblkSize = h5faDblockSize(&dblock)
	}

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5faSizeofChksum {
		return nil, errorf(H5E_FARRAY, H5E_BADSIZE, "bad decoded fixed array header size")
	}

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTDECODE, "can't decode fixed array header")
	}

	/* Finish initializing fixed array header */
	if err := h5faHdrInit(hdr, cls, ctx); err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTINIT, "initialization failed for fixed array header")
	}
	if hdr.size != uint64(len(img)) {
		return nil, errorf(H5E_FARRAY, H5E_BADSIZE, "bad fixed array header size")
	}

	return hdr, nil
}

// h5faCacheHdrLoad loads (H5C__load_entry) a fixed array header.
func h5faCacheHdrLoad(f *File, addr haddr, cls *h5faClass, ctx any) (*h5faHdr, error) {
	img, err := h5faCacheLoadImage(f, addr, h5faCacheHdrGetInitialLoadSize(f), h5faCacheHdrVerifyChksum, "header")
	if err != nil {
		return nil, err
	}
	return h5faCacheHdrDeserialize(f, img, addr, cls, ctx)
}

// h5faCacheDblockGetInitialLoadSize is
// H5FA__cache_dblock_get_initial_load_size.
func h5faCacheDblockGetInitialLoadSize(udata *h5faDblockCacheUd) uint64 {
	/* Set up fake data block for computing size on disk */
	/* (Note: extracted from H5FA__dblock_alloc) */

	/* Set up fake data block for computing size on disk
	 *
	 * need: dblock->hdr
	 *       dblock->npages
	 *       dblock->dblk_page_init_size
	 */
	dblock := h5faDblock{hdr: udata.hdr}
	dblkPageNelmts := uint64(1) << udata.hdr.maxDblkPageNelmtsBits /* # of elements per data block page */
	if udata.hdr.nelmts > dblkPageNelmts {
		dblock.npages = ((udata.hdr.nelmts + dblkPageNelmts) - 1) / dblkPageNelmts
		dblock.dblkPageInitSize = (dblock.npages + 7) / 8
	}

	/* Set the image length size */
	if dblock.npages == 0 {
		return h5faDblockSize(&dblock)
	}
	return h5faDblockPrefixSize(&dblock)
}

// h5faCacheDblockVerifyChksum is H5FA__cache_dblock_verify_chksum.
func h5faCacheDblockVerifyChksum(img []byte) (bool, error) {
	return h5faCacheVerifyChksum(img)
}

// h5faCacheDblockDeserialize is H5FA__cache_dblock_deserialize: loads a data
// structure from the disk.
func h5faCacheDblockDeserialize(img []byte, udata *h5faDblockCacheUd) (*h5faDblock, error) {
	hdr := udata.hdr
	p := hdr.f.newImage(img)

	/* Allocate the fixed array data block */
	dblock, err := h5faDblockAlloc(hdr)
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTALLOC, "memory allocation failed for fixed array data block")
	}

	if !((dblock.npages == 0 && uint64(len(img)) == h5faDblockSize(dblock)) ||
		uint64(len(img)) == h5faDblockPrefixSize(dblock)) {
		return nil, errorf(H5E_FARRAY, H5E_BADSIZE, "bad fixed array data block image size")
	}

	/* Set the fixed array data block's information */
	dblock.addr = udata.dblkAddr

	/* Magic number */
	if p.overflow(h5SizeofMagic + 2) {
		return nil, errorf(H5E_FARRAY, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	if !bytes.Equal(p.bytes(h5SizeofMagic), []byte(h5faDblockMagic)) {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "wrong fixed array data block signature")
	}

	/* Version */
	if p.uint8() != h5faDblockVersion {
		return nil, errorf(H5E_FARRAY, H5E_VERSION, "wrong fixed array data block version")
	}

	/* Fixed array type */
	if p.uint8() != hdr.clsID {
		return nil, errorf(H5E_FARRAY, H5E_BADTYPE, "incorrect fixed array class")
	}

	/* Address of header for array that owns this block (just for file integrity checks) */
	if arrAddr := p.addr(); arrAddr != hdr.addr {
		return nil, errorf(H5E_FARRAY, H5E_BADVALUE, "wrong fixed array header address")
	}

	/* Page initialization flags */
	if dblock.npages > 0 {
		copy(dblock.dblkPageInit, p.bytes(int(dblock.dblkPageInitSize)))
	}

	/* Only decode elements if the data block is not paged */
	if dblock.npages == 0 {
		/* Decode elements in data block */
		/* Convert from raw elements on disk into native elements in memory */
		if err := h5faCacheDecodeElmts(hdr, p, dblock.elmts); err != nil {
			return nil, wrapf(err, H5E_FARRAY, H5E_CANTDECODE, "can't decode fixed array data elements")
		}
	}

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5faSizeofChksum {
		return nil, errorf(H5E_FARRAY, H5E_BADSIZE, "bad decoded fixed array data block size")
	}

	/* Set the data block's size */
	dblock.size = h5faDblockSize(dblock)

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTDECODE, "can't decode fixed array data block")
	}

	return dblock, nil
}

// h5faCacheDblockLoad loads (H5C__load_entry) the data block of a fixed
// array (only the prefix, for a paged data block).
func h5faCacheDblockLoad(udata *h5faDblockCacheUd) (*h5faDblock, error) {
	img, err := h5faCacheLoadImage(udata.hdr.f, udata.dblkAddr, h5faCacheDblockGetInitialLoadSize(udata), h5faCacheDblockVerifyChksum, "data block")
	if err != nil {
		return nil, err
	}
	return h5faCacheDblockDeserialize(img, udata)
}

// h5faCacheDblkPageGetInitialLoadSize is
// H5FA__cache_dblk_page_get_initial_load_size.
func h5faCacheDblkPageGetInitialLoadSize(udata *h5faDblkPageCacheUd) uint64 {
	/* Set the image length size */
	return h5faDblkPageSize(udata.hdr, udata.nelmts)
}

// h5faCacheDblkPageVerifyChksum is H5FA__cache_dblk_page_verify_chksum.
func h5faCacheDblkPageVerifyChksum(img []byte) (bool, error) {
	return h5faCacheVerifyChksum(img)
}

// h5faCacheDblkPageDeserialize is H5FA__cache_dblk_page_deserialize: loads
// a data structure from the disk.
func h5faCacheDblkPageDeserialize(img []byte, udata *h5faDblkPageCacheUd) (*h5faDblkPage, error) {
	hdr := udata.hdr
	p := hdr.f.newImage(img)

	/* Allocate the fixed array data block page */
	dblkPage, err := h5faDblkPageAlloc(hdr, udata.nelmts)
	if err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTALLOC, "memory allocation failed for fixed array data block page")
	}

	/* Set the fixed array data block's information */
	dblkPage.addr = udata.dblkPageAddr

	/* Internal information */

	/* Decode elements in data block page */
	/* Convert from raw elements on disk into native elements in memory */
	if err := h5faCacheDecodeElmts(hdr, p, dblkPage.elmts); err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTDECODE, "can't decode fixed array data elements")
	}

	/* Sanity check */
	/* (allow for checksum not decoded yet) */
	if uint64(p.off) != uint64(len(img))-h5faSizeofChksum {
		return nil, errorf(H5E_FARRAY, H5E_BADSIZE, "bad decoded fixed array data block page size")
	}

	/* Set the data block page's size */
	dblkPage.size = uint64(len(img))

	/* checksum verification already done in verify_chksum cb */

	/* Metadata checksum */
	_ = p.uint32()
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_FARRAY, H5E_CANTDECODE, "can't decode fixed array data block page")
	}

	return dblkPage, nil
}

// h5faCacheDblkPageLoad loads (H5C__load_entry) a data block page of a
// fixed array.
func h5faCacheDblkPageLoad(udata *h5faDblkPageCacheUd) (*h5faDblkPage, error) {
	img, err := h5faCacheLoadImage(udata.hdr.f, udata.dblkPageAddr, h5faCacheDblkPageGetInitialLoadSize(udata), h5faCacheDblkPageVerifyChksum, "data block page")
	if err != nil {
		return nil, err
	}
	return h5faCacheDblkPageDeserialize(img, udata)
}
