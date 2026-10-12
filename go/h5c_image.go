package hdf5

// Translated from src/H5Cimage.c (loading side, read-only files): the
// metadata cache image. A file closed with a metadata cache image
// (H5Pset_mdc_image_config) has a 'metadata cache image' message in its
// superblock extension pointing to a block holding the serialized images
// of the cache entries at close time; those entries need not have been
// written to their own addresses. libhdf5 loads the block on the first
// cache protect after the superblock is read and inserts every entry into
// the cache as a "prefetched" entry; a later protect of that address
// deserializes the prefetched image instead of reading the file
// (H5C__deserialize_prefetched_entry).
//
// Here the decoded entries are kept in File.cacheImage and overlaid on the
// bytes read from the file (File.readAt / File.readInto), which gives the
// metadata decoders the same images. The flush dependencies, LRU ranks and
// adaptive resize status only matter to a writable cache and are not
// kept.

import (
	"bytes"
	"sort"
)

const (
	// H5C__MDCI_BLOCK_SIGNATURE / H5C__MDCI_BLOCK_SIGNATURE_LEN.
	h5cMdciBlockSignature    = "MDCI"
	h5cMdciBlockSignatureLen = 4
	// H5C__MDCI_BLOCK_VERSION_0.
	h5cMdciBlockVersion0 = 0
	// H5C__MDCI_HEADER_HAVE_RESIZE_STATUS.
	h5cMdciHeaderHaveResizeStatus = 0x01
)

// h5cPrefetched is a prefetched cache entry (the fields of
// H5C_cache_entry_t that H5C__reconstruct_cache_entry sets and reading
// needs): the entry's address, its on-disk image and its flush dependency
// parents.
type h5cPrefetched struct {
	typeID        uint8
	addr          haddr
	image         []byte
	fdParentAddrs []haddr
}

// h5cCacheImageBlockEntryHeaderSize is
// H5C__cache_image_block_entry_header_size.
func (f *File) h5cCacheImageBlockEntryHeaderSize() int {
	return 1 + /* type                     */
		1 + /* flags                    */
		1 + /* ring                     */
		1 + /* age                      */
		2 + /* dependency child count   */
		2 + /* dirty dep child count    */
		2 + /* dependency parent count  */
		4 + /* index in LRU             */
		int(f.sizeofAddr) + /* entry offset             */
		int(f.sizeofSize) /* entry length             */
}

// h5cCacheImageBlockHeaderSize is H5C__cache_image_block_header_size.
func (f *File) h5cCacheImageBlockHeaderSize() int {
	return 4 + /* signature           */
		1 + /* version             */
		1 + /* flags               */
		int(f.sizeofSize) + /* image data length   */
		4 /* num_entries         */
}

// h5cReadCacheImage is H5C__read_cache_image: read the cache image block.
func (f *File) h5cReadCacheImage(addr haddr, length uint64) ([]byte, error) {
	buf, err := f.readAt(addr, int(length))
	if err != nil {
		return nil, wrapf(err, H5E_CACHE, H5E_READERROR, "Can't read metadata cache image block")
	}
	return buf, nil
}

// h5cLoadCacheImage is H5C__load_cache_image for a file opened read-only
// (the image is neither deleted nor freed): load the cache image block at
// addr, decode it and keep its entries. It is called when the superblock
// extension has a metadata cache image message, at the point where
// H5C_load_cache_image_on_next_protect arranges for it.
func (f *File) h5cLoadCacheImage(addr haddr, length uint64) error {
	/* If the image address is defined, load the image, decode it,
	 * and insert its contents into the metadata cache.
	 *
	 * Note that under normal operating conditions, it is an error if the
	 * image address is HADDR_UNDEF.  However, to facilitate testing,
	 * we allow this special value of the image address which means that
	 * no image exists, and that the load operation should be skipped
	 * silently.
	 */
	if !addrDefined(addr) {
		return nil
	}

	/* Load the image from file */
	buf, err := f.h5cReadCacheImage(addr, length)
	if err != nil {
		return wrapf(err, H5E_CACHE, H5E_READERROR, "Can't read metadata cache image block")
	}

	/* Reconstruct cache contents, from image */
	entries, err := f.h5cReconstructCacheContents(buf, length)
	if err != nil {
		return wrapf(err, H5E_CACHE, H5E_CANTDECODE, "Can't reconstruct cache contents from image block")
	}
	f.cacheImage = entries
	return nil
}

// h5cDecodeCacheImageHeader is H5C__decode_cache_image_header: decode the
// metadata cache image block header and return the number of entries.
func (f *File) h5cDecodeCacheImageHeader(p *image, imageLen uint64) (uint32, error) {
	start := p.off

	/* Check signature */
	if p.overflow(h5cMdciBlockSignatureLen) || !bytes.Equal(p.bytes(h5cMdciBlockSignatureLen), []byte(h5cMdciBlockSignature)) {
		return 0, errorf(H5E_CACHE, H5E_BADVALUE, "Bad metadata cache image header signature")
	}

	/* Check version */
	version := p.uint8()
	if version != h5cMdciBlockVersion0 {
		return 0, errorf(H5E_CACHE, H5E_BADVALUE, "Bad metadata cache image version")
	}

	/* Decode flags */
	flags := p.uint8()
	if flags&h5cMdciHeaderHaveResizeStatus != 0 {
		return 0, errorf(H5E_CACHE, H5E_BADVALUE, "MDC resize status not yet supported")
	}

	/* Read image data length */
	imageDataLen := p.length()

	/* For now -- will become <= eventually */
	if imageDataLen != imageLen {
		return 0, errorf(H5E_CACHE, H5E_BADVALUE, "Bad metadata cache image data length")
	}

	/* Read num entries */
	numEntries := p.uint32()
	if err := p.done(); err != nil {
		return 0, wrapf(err, H5E_CACHE, H5E_BADVALUE, "Bad header image len")
	}
	if numEntries == 0 {
		return 0, errorf(H5E_CACHE, H5E_BADVALUE, "Bad metadata cache entry count")
	}

	/* Verify expected length of header */
	if p.off-start != f.h5cCacheImageBlockHeaderSize() {
		return 0, errorf(H5E_CACHE, H5E_BADVALUE, "Bad header image len")
	}
	return numEntries, nil
}

// h5cReconstructCacheContents is H5C__reconstruct_cache_contents: decode
// the cache image block into prefetched entries (sorted by address).
func (f *File) h5cReconstructCacheContents(buf []byte, imageLen uint64) ([]h5cPrefetched, error) {
	/* Decode metadata cache image header */
	p := f.newImage(buf)
	numEntries, err := f.h5cDecodeCacheImageHeader(p, imageLen)
	if err != nil {
		return nil, wrapf(err, H5E_CACHE, H5E_CANTDECODE, "cache image header decode failed")
	}

	/* Reconstruct entries in image (each entry needs at least its header:
	 * bound the allocation by the image size) */
	if uint64(numEntries) > uint64(p.remaining())/uint64(f.h5cCacheImageBlockEntryHeaderSize()) {
		return nil, errorf(H5E_CACHE, H5E_BADVALUE, "Bad metadata cache entry count")
	}
	entries := make([]h5cPrefetched, 0, numEntries)
	index := make(map[haddr]bool, numEntries)
	for u := uint32(0); u < numEntries; u++ {
		/* Create the prefetched entry described by the ith
		 * entry in cache_ptr->image_entrise.
		 */
		e, err := f.h5cReconstructCacheEntry(p)
		if err != nil {
			return nil, wrapf(err, H5E_CACHE, H5E_SYSTEM, "reconstruction of cache entry failed")
		}

		/* Insert the prefetched entry in the index */
		if index[e.addr] {
			return nil, errorf(H5E_CACHE, H5E_CANTINSERT, "entry already in cache")
		}
		index[e.addr] = true

		/* If the prefetched entry is the child in one or more flush
		 * dependency relationships, recreate those flush dependencies.
		 */
		for _, parent := range e.fdParentAddrs {
			/* Find the parent entry */
			if !index[parent] {
				return nil, errorf(H5E_CACHE, H5E_NOTFOUND, "fd parent not in cache?!?")
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].addr < entries[j].addr })
	return entries, nil
}

// h5cReconstructCacheEntry is H5C__reconstruct_cache_entry: decode one
// prefetched entry from the cache image block.
func (f *File) h5cReconstructCacheEntry(p *image) (h5cPrefetched, error) {
	var e h5cPrefetched
	start := p.off
	if p.overflow(f.h5cCacheImageBlockEntryHeaderSize()) {
		return e, errorf(H5E_CACHE, H5E_BADSIZE, "Bad entry image len")
	}

	/* Decode type id */
	e.typeID = p.uint8()

	/* Decode flags */
	_ = p.uint8()

	/* Decode ring */
	_ = p.uint8()

	/* Decode age */
	_ = p.uint8()

	/* Decode dependency child count */
	fdChildCount := p.uint16()

	/* Decode dirty dependency child count */
	fdDirtyChildCount := p.uint16()
	/* (the file is read only) */
	fdDirtyChildCount = 0
	if fdDirtyChildCount > fdChildCount {
		return e, errorf(H5E_CACHE, H5E_BADVALUE, "invalid dirty flush dependency child count")
	}

	/* Decode dependency parent count */
	fdParentCount := p.uint16()

	/* Decode index in LRU */
	_ = p.int32()

	/* Decode entry offset */
	e.addr = p.addr()
	if !addrDefined(e.addr) {
		return e, errorf(H5E_CACHE, H5E_BADVALUE, "invalid entry offset")
	}

	/* Decode entry length */
	size := p.length()
	if size == 0 {
		return e, errorf(H5E_CACHE, H5E_BADVALUE, "invalid entry size")
	}

	/* Verify expected length of entry image */
	if p.off-start != f.h5cCacheImageBlockEntryHeaderSize() {
		return e, errorf(H5E_CACHE, H5E_BADSIZE, "Bad entry image len")
	}

	/* If parent count greater than zero, allocate array for parent
	 * addresses, and decode addresses into the array.
	 */
	if fdParentCount > 0 {
		if p.overflow(int(fdParentCount) * int(f.sizeofAddr)) {
			return e, errorf(H5E_CACHE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		e.fdParentAddrs = make([]haddr, fdParentCount)
		for u := range e.fdParentAddrs {
			e.fdParentAddrs[u] = p.addr()
			if !addrDefined(e.fdParentAddrs[u]) {
				return e, errorf(H5E_CACHE, H5E_BADVALUE, "invalid flush dependency parent offset")
			}
		}
	}

	/* Copy the entry image from the cache image block */
	if size > uint64(p.remaining()) {
		return e, errorf(H5E_CACHE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	e.image = p.bytes(int(size))
	if e.addr+size < e.addr {
		return e, errorf(H5E_CACHE, H5E_BADVALUE, "invalid entry offset")
	}
	return e, nil
}

// cacheImageEntry returns the prefetched entry holding all of [addr,
// addr+n), if any (H5C__SEARCH_INDEX finding a prefetched entry: the
// protect deserializes its image without reading the file).
func (f *File) cacheImageEntry(addr haddr, n uint64) []byte {
	i := sort.Search(len(f.cacheImage), func(i int) bool { return f.cacheImage[i].addr > addr }) - 1
	if i < 0 {
		return nil
	}
	e := &f.cacheImage[i]
	if off := addr - e.addr; off <= uint64(len(e.image)) && n <= uint64(len(e.image))-off {
		return e.image[off : off+n]
	}
	return nil
}

// overlayCacheImage copies the parts of the prefetched entries that
// overlap buf (read at addr) over it.
func (f *File) overlayCacheImage(addr haddr, buf []byte) {
	end := addr + uint64(len(buf))
	for i := range f.cacheImage {
		e := &f.cacheImage[i]
		eEnd := e.addr + uint64(len(e.image))
		if e.addr >= end {
			break
		}
		if eEnd <= addr {
			continue
		}
		lo, hi := max(addr, e.addr), min(end, eEnd)
		copy(buf[lo-addr:hi-addr], e.image[lo-e.addr:hi-e.addr])
	}
}
