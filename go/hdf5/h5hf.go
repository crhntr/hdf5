package hdf5

// Translated from src/H5HF.c (read side only). The package-private types,
// macros and constants of src/H5HFpkg.h live here as well.

/**************************/
/* Package Private Macros */
/**************************/

// H5HF_HDR_MAGIC / H5HF_IBLOCK_MAGIC / H5HF_DBLOCK_MAGIC (H5Fprivate.h)
const (
	h5hfHdrMagic    = "FRHP" /* Header */
	h5hfIblockMagic = "FHIB" /* Indirect block */
	h5hfDblockMagic = "FHDB" /* Direct block */
	h5SizeofMagic   = 4      /* H5_SIZEOF_MAGIC */
)

// Fractal heap format versions (H5HFcache.c).
const (
	h5hfHdrVersion    = 0 /* Header */
	h5hfDblockVersion = 0 /* Direct block */
	h5hfIblockVersion = 0 /* Indirect block */
)

// h5hfECantOperate is H5E_CANTOPERATE, which errors.go does not define yet.
// TODO: replace with H5E_CANTOPERATE once it is added to errors.go.
const h5hfECantOperate errMinor = "H5E_CANTOPERATE"

// Size of checksum information (on disk)
const h5hfSizeofChksum = 4

// Flags for status byte
const (
	h5hfHdrFlagsHugeIDWrapped   = 0x01 /* "huge" object IDs have wrapped */
	h5hfHdrFlagsChecksumDblocks = 0x02 /* checksum direct blocks */
)

// Limits on the creation parameters (H5HFhdr.c, H5HFprivate.h). The C
// library only checks them when creating a heap; they are checked when a
// header is decoded here so that corrupt headers cannot drive the doubling
// table computations out of range.
const (
	h5hfMaxDirectSizeLimit = 2 * 1024 * 1024 * 1024 /* H5HF_MAX_DIRECT_SIZE_LIMIT */
	h5hfWidthLimit         = 64 * 1024              /* H5HF_WIDTH_LIMIT */
	h5hfMaxIDLen           = 4096 + 1               /* H5HF_MAX_ID_LEN */
)

// Heap ID bit flags
const (
	/* Heap ID version (2 bits: 6-7) */
	h5hfIDVersCurr = 0x00 /* Current version of ID format */
	h5hfIDVersMask = 0xC0 /* Mask for getting the ID version from flags */
	/* Heap ID type (2 bits: 4-5) */
	h5hfIDTypeMan      = 0x00 /* "Managed" object - stored in fractal heap blocks */
	h5hfIDTypeHuge     = 0x10 /* "Huge" object - stored in file directly */
	h5hfIDTypeTiny     = 0x20 /* "Tiny" object - stored in heap ID directly */
	h5hfIDTypeReserved = 0x30 /* "?" object - reserved for future use */
	h5hfIDTypeMask     = 0x30 /* Mask for getting the ID type from flags */
	/* Heap ID bits 0-3 reserved for future use */
)

// h5hfMetadataPrefixSize is H5HF_METADATA_PREFIX_SIZE(c): "standard" size of
// prefix information for fractal heap metadata.
func h5hfMetadataPrefixSize(c bool) uint64 {
	n := uint64(h5SizeofMagic + 1) /* Signature + Version */
	if c {
		n += h5hfSizeofChksum /* Metadata checksum */
	}
	return n
}

// h5hfDtableInfoSize is H5HF_DTABLE_INFO_SIZE(h): size of doubling-table
// information.
func h5hfDtableInfoSize(h *h5hfHdr) uint64 {
	return 2 /* Width of table (i.e. # of columns) */ +
		uint64(h.sizeofSize) /* Starting block size */ +
		uint64(h.sizeofSize) /* Maximum direct block size */ +
		2 /* Max. size of heap (log2 of actual value - i.e. the # of bits) */ +
		2 /* Starting # of rows in root indirect block */ +
		uint64(h.sizeofAddr) /* File address of table managed */ +
		2 /* Current # of rows in root indirect block */
}

// h5hfHeaderSize is H5HF_HEADER_SIZE(h): size of the fractal heap header on
// disk (this is the fixed-len portion, the variable-len I/O filter
// information follows this information, if there are I/O filters for the
// heap).
func h5hfHeaderSize(h *h5hfHdr) uint64 {
	/* General metadata fields */
	return h5hfMetadataPrefixSize(true) +

		/* Fractal Heap Header specific fields */

		/* General heap information */
		2 /* Heap ID len */ +
		2 /* I/O filters' encoded len */ +
		1 /* Status flags */ +

		/* "Huge" object fields */
		4 /* Max. size of "managed" object */ +
		uint64(h.sizeofSize) /* Next ID for "huge" object */ +
		uint64(h.sizeofAddr) /* File address of "huge" object tracker B-tree  */ +

		/* "Managed" object free space fields */
		uint64(h.sizeofSize) /* Total man. free space */ +
		uint64(h.sizeofAddr) /* File address of free section header */ +

		/* Statistics fields */
		8*uint64(h.sizeofSize) /* man. size, man. alloc size, man. iter offset, man. nobjs, huge size, huge nobjs, tiny size, tiny nobjs */ +

		/* "Managed" object doubling table info */
		h5hfDtableInfoSize(h) /* Size of managed obj. doubling-table info */
}

// h5hfManAbsDirectOverhead is H5HF_MAN_ABS_DIRECT_OVERHEAD(h): size of
// overhead for a direct block.
func h5hfManAbsDirectOverhead(h *h5hfHdr) uint64 {
	/* General metadata fields */
	return h5hfMetadataPrefixSize(h.checksumDblocks) +

		/* Fractal heap managed, absolutely mapped direct block specific fields */
		uint64(h.sizeofAddr) /* File address of heap owning the block */ +
		uint64(h.heapOffSize) /* Offset of the block in the heap */
}

// h5hfManIndirectChildDirEntrySize is
// H5HF_MAN_INDIRECT_CHILD_DIR_ENTRY_SIZE(h): size of managed indirect block
// entry for a child direct block.
func h5hfManIndirectChildDirEntrySize(h *h5hfHdr) uint64 {
	if h.filterLen > 0 {
		/* Size of entries for filtered direct blocks */
		return uint64(h.sizeofAddr) + uint64(h.sizeofSize) + 4
	}
	/* Size of entries for un-filtered direct blocks */
	return uint64(h.sizeofAddr)
}

// h5hfManIndirectSize is H5HF_MAN_INDIRECT_SIZE(h, r): size of managed
// indirect block.
func h5hfManIndirectSize(h *h5hfHdr, r uint32) uint64 {
	dirRows := min(r, h.manDtable.maxDirectRows)
	var indirRows uint32
	if r > h.manDtable.maxDirectRows {
		indirRows = r - h.manDtable.maxDirectRows
	}
	/* General metadata fields */
	return h5hfMetadataPrefixSize(true) +

		/* Fractal heap managed, absolutely mapped indirect block specific fields */
		uint64(h.sizeofAddr) /* File address of heap owning the block */ +
		uint64(h.heapOffSize) /* Offset of the block in the heap */ +
		uint64(dirRows)*uint64(h.manDtable.width)*h5hfManIndirectChildDirEntrySize(h) /* Size of entries for direct blocks */ +
		uint64(indirRows)*uint64(h.manDtable.width)*uint64(h.sizeofAddr) /* Size of entries for indirect blocks */
}

// h5hfSizeofOffsetBits is H5HF_SIZEOF_OFFSET_BITS(b): the # of bytes
// required to store an offset of b bits.
func h5hfSizeofOffsetBits(b uint32) uint32 { return (b + 7) / 8 }

// h5hfSizeofOffsetLen is H5HF_SIZEOF_OFFSET_LEN(l): the # of bytes required
// to store an offset into a buffer of size l (a power of two).
func h5hfSizeofOffsetLen(l uint64) uint32 {
	return h5hfSizeofOffsetBits(h5hfLog2Of2(uint32(l)))
}

/****************************/
/* Package Private Typedefs */
/****************************/

// h5hf is H5HF_t: an opened fractal heap.
type h5hf struct {
	f   *File
	hdr *h5hfHdr

	/* v2 B-tree handle for huge objects (H5HF_hdr_t.huge_bt2 in the C
	 * library; kept in the open wrapper here because the cached header is
	 * immutable). Opened on first use. */
	hugeBt2 *h5b2
}

// h5hfHdr is H5HF_hdr_t (decoded header fields).
type h5hfHdr struct {
	addr                                        haddr /* Address of heap header in the file (heap_addr) */
	version                                     uint8
	heapOffSize                                 uint8  /* Size of heap offsets (in bytes) */
	heapLenSize                                 uint8  /* Size of heap ID lengths (in bytes) */
	idLen                                       uint16 /* Size of heap IDs (in bytes) */
	filterLen                                   uint16 /* Size of I/O filter information (in bytes) */
	flags                                       uint8
	maxManSize                                  uint32 /* Max. size of object to manage in doubling table */
	nextManID                                   uint64 /* Next ID for huge objects (huge_next_id) */
	hugeBt2Addr                                 haddr  /* Address of v2 B-tree for tracking "huge" object info */
	freeSpace                                   uint64 /* Total amount of free space in managed blocks (total_man_free) */
	fsAddr                                      haddr  /* Address of free space header on disk */
	manDtable                                   h5hfDtable
	manSize, manAllocSize, manIterOff, manNobjs uint64
	hugeSize, hugeNobjs, tinySize, tinyNobjs    uint64
	pline                                       *h5oPline /* I/O filter pipeline for heap objects, if any */
	plineRootDirectSize                         uint64    /* Size of filtered root direct block */
	plineRootDirectFilterMask                   uint32    /* I/O filter mask for filtered root direct block */
	checksumDblocks                             bool      /* Should the direct blocks in the heap be checksummed? */
	hugeIDsDirect                               bool      /* Flag to indicate that 'huge' object's offset & length are stored directly in heap ID */
	hugeIDsWrapped                              bool      /* Have "huge" object IDs wrapped around? */

	/* Cached/computed values (not stored in header) */
	f               *File  /* Pointer to file for heap */
	sizeofSize      uint8  /* Size of file sizes */
	sizeofAddr      uint8  /* Size of file addresses */
	heapSize        uint64 /* Size of heap header in the file */
	hugeMaxID       uint64 /* Max. 'huge' heap ID before rolling 'huge' heap IDs over */
	hugeIDSize      uint8  /* Size of 'huge' heap IDs (in bytes) */
	tinyMaxLen      uint32 /* Max. size of tiny objects for this heap */
	tinyLenExtended bool   /* Flag to indicate that 'tiny' object's length is stored in extended form (i.e. w/extra byte) */
}

// h5hfDtable is H5HF_dtable_t: the doubling table parameters.
type h5hfDtable struct {
	/* Creation parameters for table (H5HF_dtable_cparam_t) */
	width          uint16 /* Number of columns in the table (must be power of 2) */
	startBlockSize uint64 /* Starting block size for table (must be power of 2) */
	maxDirectSize  uint64 /* Maximum size of a direct block (must be power of 2) */
	maxIndexBits   uint16 /* Maximum ID/offset for table (integer log2 of actual value, ie. the # of bits required) (cparam.max_index) */
	startRootRows  uint16 /* Starting number of rows for root indirect block */

	/* Derived information (stored, varies during lifetime of table) */
	tableAddr   haddr  /* Address of first block for table (undefined if no space allocated for table) */
	curRootRows uint16 /* Current number of rows in the root indirect block (0 indicates that the tableAddr field points to direct block (of startBlockSize) instead of indirect root block) */

	/* Computed information (not stored) */
	startBits        uint16   /* # of bits for starting block size (i.e. log2(start_block_size)) */
	maxDirectBits    uint16   /* # of bits for max. direct block size (i.e. log2(max_direct_size)) */
	maxRootRows      uint32   /* Maximum # of rows in root indirect block */
	maxDirectRows    uint32   /* Maximum # of direct rows in any indirect block */
	maxDirRows       uint32   /* Same as maxDirectRows (kept for the shared stub layout) */
	maxDirBlkOffSize uint32   /* Max. size of offsets in direct blocks */
	firstRowBits     uint32   /* # of bits in address of first row */
	numIDFirstRow    uint64   /* Number of IDs in first row of table */
	rowBlockSize     []uint64 /* Block size per row of indirect block */
	rowBlockOff      []uint64 /* Cumulative offset per row of indirect block */
	rowTotDblockFree []uint64 /* Total free space in dblocks for this row */
	rowMaxDblockFree []uint64 /* Max. free space in dblocks for this row */
}

// h5hfIndirectFiltEnt is H5HF_indirect_filt_ent_t: extern indirect block
// doubling table entry for compressed direct blocks (only exists for
// indirect blocks in heaps that have I/O filters).
type h5hfIndirectFiltEnt struct {
	size       uint64 /* Size of child direct block, after passing though I/O filters */
	filterMask uint32 /* Excluded filters for child direct block */
}

// h5hfIndirect is H5HF_indirect_t: a fractal heap indirect block (the parts
// stored on disk plus the values derived when it is loaded).
type h5hfIndirect struct {
	heapAddr  haddr  /* Address of the heap header that owns this block */
	addr      haddr  /* Address of this indirect block on disk */
	size      uint64 /* Size of indirect block on disk */
	nrows     uint32 /* Total # of rows in indirect block */
	maxRows   uint32 /* Max. # of rows in indirect block */
	nchildren uint32 /* Number of child blocks */
	maxChild  uint32 /* Max. offset used in child entries */
	parEntry  uint32 /* Entry in parent's table */

	/* Stored values */
	blockOff uint64                /* Offset of the block within the heap's address space */
	ents     []haddr               /* Block entry table (H5HF_indirect_ent_t.addr) */
	filtEnts []h5hfIndirectFiltEnt /* Filtered information for direct blocks */
}

// h5hfDirect is H5HF_direct_t: a fractal heap direct block.
type h5hfDirect struct {
	heapAddr haddr  /* Address of the heap header that owns this block */
	parEntry uint32 /* Entry in parent's table */
	size     uint64 /* Size of direct block */
	blk      []byte /* Block data (decoded, i.e. after the I/O filters) */

	/* Stored values */
	blockOff uint64 /* Offset of the block within the heap's address space */
}

// h5hfParent is H5HF_parent_t: fractal heap "parent info" (for loading a
// block).
type h5hfParent struct {
	hdr    *h5hfHdr      /* Pointer to heap header info */
	iblock *h5hfIndirect /* Pointer to parent indirect block */
	entry  uint32        /* Location of block in parent's entry table */
}

/*********************/
/* H5HF.c            */
/*********************/

// hfOpen is H5HF_open: load the heap header at addr.
func (f *File) hfOpen(addr haddr) (*h5hf, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_HEAP, H5E_BADVALUE, "undefined fractal heap address")
	}

	/* Load the heap header into memory */
	hdr, err := h5hfHdrProtect(f, addr)
	if err != nil {
		return nil, wrapf(err, H5E_HEAP, H5E_CANTPROTECT, "unable to protect fractal heap header")
	}

	/* Create fractal heap info */
	return &h5hf{f: f, hdr: hdr}, nil
}

// idLen is H5HF_get_id_len.
func (fh *h5hf) idLen() int { return int(fh.hdr.idLen) }

// heapAddr is H5HF_get_heap_addr.
func (fh *h5hf) heapAddr() haddr { return fh.hdr.addr }

// objLen is H5HF_get_obj_len: the size of the object identified by id.
func (fh *h5hf) objLen(id []byte) (uint64, error) {
	if len(id) == 0 {
		return 0, errorf(H5E_HEAP, H5E_BADVALUE, "empty heap ID")
	}

	/* Get the ID flags */
	idFlags := id[0]

	/* Check for correct heap ID version */
	if idFlags&h5hfIDVersMask != h5hfIDVersCurr {
		return 0, errorf(H5E_HEAP, H5E_VERSION, "incorrect heap ID version")
	}

	/* Check type of object in heap */
	switch idFlags & h5hfIDTypeMask {
	case h5hfIDTypeMan:
		n, err := h5hfManGetObjLen(fh.hdr, id)
		if err != nil {
			return 0, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't get 'managed' object's length")
		}
		return n, nil
	case h5hfIDTypeHuge:
		n, err := h5hfHugeGetObjLen(fh, id)
		if err != nil {
			return 0, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't get 'huge' object's length")
		}
		return n, nil
	case h5hfIDTypeTiny:
		n, err := h5hfTinyGetObjLen(fh.hdr, id)
		if err != nil {
			return 0, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't get 'tiny' object's length")
		}
		return n, nil
	}
	return 0, errorf(H5E_HEAP, H5E_UNSUPPORTED, "heap ID type not supported yet")
}

// objOff is H5HF_get_obj_off: the offset of the object identified by id
// (in the heap's address space for managed objects, the file address for
// huge objects, 0 for tiny objects).
func (fh *h5hf) objOff(id []byte) (uint64, error) {
	if len(id) == 0 {
		return 0, errorf(H5E_HEAP, H5E_BADVALUE, "empty heap ID")
	}

	/* Get the ID flags */
	idFlags := id[0]

	/* Check for correct heap ID version */
	if idFlags&h5hfIDVersMask != h5hfIDVersCurr {
		return 0, errorf(H5E_HEAP, H5E_VERSION, "incorrect heap ID version")
	}

	/* Check type of object in heap */
	switch idFlags & h5hfIDTypeMask {
	case h5hfIDTypeMan:
		return h5hfManGetObjOff(fh.hdr, id)
	case h5hfIDTypeHuge:
		/* Huge objects are located directly in the file */
		off, err := h5hfHugeGetObjOff(fh, id)
		if err != nil {
			return 0, wrapf(err, H5E_HEAP, H5E_CANTGET, "can't get 'huge' object's offset")
		}
		return off, nil
	case h5hfIDTypeTiny:
		/* Tiny objects are not stored in the heap */
		return 0, nil
	}
	return 0, errorf(H5E_HEAP, H5E_UNSUPPORTED, "heap ID type not supported yet")
}

// read is H5HF_read: return a copy of the object identified by the heap ID.
func (fh *h5hf) read(id []byte) ([]byte, error) {
	var obj []byte
	err := fh.dispatch(id, "read", func(b []byte) error {
		/* H5HF__op_read: perform "read", using a copy */
		obj = append([]byte(nil), b...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// op is H5HF_op: call op on the object identified by the heap ID without
// copying it. The slice passed to op aliases cached heap data and must not
// be modified or retained.
func (fh *h5hf) op(id []byte, op func(obj []byte) error) error {
	return fh.dispatch(id, "operate on", op)
}

// dispatch is the common body of H5HF_read and H5HF_op: check the heap ID
// version and call the managed/huge/tiny routine for its type.
func (fh *h5hf) dispatch(id []byte, what string, op func(obj []byte) error) error {
	if len(id) == 0 {
		return errorf(H5E_HEAP, H5E_BADVALUE, "empty heap ID")
	}

	/* Get the ID flags */
	idFlags := id[0]

	/* Check for correct heap ID version */
	if idFlags&h5hfIDVersMask != h5hfIDVersCurr {
		return errorf(H5E_HEAP, H5E_VERSION, "incorrect heap ID version")
	}

	/* Check type of object in heap */
	switch idFlags & h5hfIDTypeMask {
	case h5hfIDTypeMan:
		/* Read object from managed heap blocks */
		if err := h5hfManOp(fh.hdr, id, op); err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTGET, "can't %s object from fractal heap", what)
		}
	case h5hfIDTypeHuge:
		/* Read 'huge' object from file */
		if err := h5hfHugeOp(fh, id, op); err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTGET, "can't %s 'huge' object from fractal heap", what)
		}
	case h5hfIDTypeTiny:
		/* Read 'tiny' object from file */
		if err := h5hfTinyOp(fh.hdr, id, op); err != nil {
			return wrapf(err, H5E_HEAP, H5E_CANTGET, "can't %s 'tiny' object from fractal heap", what)
		}
	default:
		return errorf(H5E_HEAP, H5E_UNSUPPORTED, "heap ID type not supported yet")
	}
	return nil
}
