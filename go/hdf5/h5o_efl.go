package hdf5

// Translated from src/H5Oefl.c (decode only): the external data files
// message.

/* the version number of the external file list message */
const h5oEflVersion = 1

// h5oEflDecode is H5O__efl_decode: decode an external file list message;
// the file names are read from the local heap the message refers to.
func (f *File) h5oEflDecode(p *image) (*h5oEfl, error) {
	mesg := &h5oEfl{}

	/* Version (1 byte) */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	version := p.uint8()
	if version != h5oEflVersion {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for external file list message")
	}

	/* Reserved (3 bytes) */
	if p.overflow(3) {
		return nil, errDecodeOverflow()
	}
	p.skip(3)

	/* Number of slots (2x 2 bytes) */
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	mesg.nalloc = int(p.uint16())
	if mesg.nalloc <= 0 {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad number of allocated slots when parsing efl msg")
	}
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	mesg.nused = int(p.uint16())
	if mesg.nused > mesg.nalloc {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad number of in-use slots when parsing efl msg")
	}

	/* Heap address */
	if p.overflow(int(f.sizeofAddr)) {
		return nil, errDecodeOverflow()
	}
	mesg.heapAddr = p.addr()
	if !addrDefined(mesg.heapAddr) {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad local heap address when parsing efl msg")
	}

	/* Decode the file list */
	mesg.slot = make([]h5oEflEntry, mesg.nalloc)

	heap, err := f.hlProtect(mesg.heapAddr)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to protect local heap")
	}

	/* Get the size of the heap block */
	blockSize := heap.heapGetSize()

	for u := 0; u < mesg.nused; u++ {
		/* Name */
		if p.overflow(int(f.sizeofSize)) {
			return nil, errDecodeOverflow()
		}
		mesg.slot[u].nameOffset = p.length()
		s, err := heap.offsetInto(mesg.slot[u].nameOffset)
		if err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTGET, "unable to get external file name")
		}
		if len(s) == 0 || s[0] == 0 {
			return nil, errorf(H5E_OHDR, H5E_CANTGET, "invalid external file name")
		}
		/* (H5MM_strndup(s, block_size - name_offset)) */
		name, err := heap.stringAt(mesg.slot[u].nameOffset)
		if err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTGET, "unable to get external file name")
		}
		if uint64(len(name)) > blockSize-mesg.slot[u].nameOffset {
			name = name[:blockSize-mesg.slot[u].nameOffset]
		}
		mesg.slot[u].name = name

		/* File offset */
		if p.overflow(int(f.sizeofSize)) {
			return nil, errDecodeOverflow()
		}
		mesg.slot[u].offset = int64(p.length()) /* Decode into an hsize_t to avoid sign warnings */

		/* Size */
		if p.overflow(int(f.sizeofSize)) {
			return nil, errDecodeOverflow()
		}
		mesg.slot[u].size = p.length()
	}
	return mesg, nil
}
