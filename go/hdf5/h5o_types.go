package hdf5

// In-core representations of object header messages, translated from the
// structs in src/H5Oprivate.h, plus the object header API used by the rest
// of the package. The decoders live in h5o_*.go (one per C file).

import "github.com/HDFGroup/hdf5/go/h5"

// Message type IDs (H5O_*_ID in H5Oprivate.h).
const (
	h5oNullID         = 0x0000 /* Null Message.  */
	h5oSdspaceID      = 0x0001 /* Dataspace Message. */
	h5oLinfoID        = 0x0002 /* Link info Message. */
	h5oDtypeID        = 0x0003 /* Datatype Message.  */
	h5oFillID         = 0x0004 /* Fill Value Message. (Old)  */
	h5oFillNewID      = 0x0005 /* Fill Value Message. (New)  */
	h5oLinkID         = 0x0006 /* Link Message. */
	h5oEflID          = 0x0007 /* External File List Message  */
	h5oLayoutID       = 0x0008 /* Data Layout Message.  */
	h5oBogusValidID   = 0x0009 /* "Bogus valid" Message.  */
	h5oGinfoID        = 0x000a /* Group info Message.  */
	h5oPlineID        = 0x000b /* Filter pipeline message.  */
	h5oAttrID         = 0x000c /* Attribute Message.  */
	h5oNameID         = 0x000d /* Object name message.  */
	h5oMtimeID        = 0x000e /* Modification time message. (Old)  */
	h5oShmesgID       = 0x000f /* Shared message "SOHM" table. */
	h5oContID         = 0x0010 /* Object header continuation message.  */
	h5oStabID         = 0x0011 /* Symbol table message.  */
	h5oMtimeNewID     = 0x0012 /* Modification time message. (New)  */
	h5oBtreekID       = 0x0013 /* v1 B-tree 'K' values message.  */
	h5oDrvinfoID      = 0x0014 /* Driver info message.  */
	h5oAinfoID        = 0x0015 /* Attribute info message.  */
	h5oRefcountID     = 0x0016 /* Reference count message.  */
	h5oFsinfoID       = 0x0017 /* File space info message.  */
	h5oMdciMsgID      = 0x0018 /* Metadata cache image message.  */
	h5oUnknownID      = 0x0019 /* Placeholder message ID for unknown message.  */
	h5oBogusInvalidID = 0x001a /* "Bogus invalid" Message.  */
	h5oMsgTypes       = 0x001b /* Number of message types */
)

// Message flags (H5O_MSG_FLAG_* in H5Opublic.h / H5Oprivate.h).
const (
	h5oMsgFlagConstant            = 0x01
	h5oMsgFlagShared              = 0x02
	h5oMsgFlagDontshare           = 0x04
	h5oMsgFlagFailIfUnknown       = 0x08
	h5oMsgFlagMarkIfUnknown       = 0x10
	h5oMsgFlagWasUnknown          = 0x20
	h5oMsgFlagShareable           = 0x40
	h5oMsgFlagFailIfUnknownAlways = 0x80
	h5oMsgFlagBits                = 0xff
)

// h5oMesg is H5O_mesg_t: one message in an object header.
type h5oMesg struct {
	typeID  uint16 /* type of message */
	flags   uint8  /* message flags */
	crtIdx  uint16 /* creation index for message */
	raw     []byte /* raw encoded message (excluding the message header) */
	rawOff  uint64 /* file address of raw (for debugging / shared messages) */
	native  any    /* decoded message, one of the h5o* structs below; nil until decoded */
	chunkno int    /* chunk number for this mesg */
}

// h5oChunk is H5O_chunk_t: a chunk (the header prefix or a continuation block)
// of an object header.
type h5oChunk struct {
	addr haddr  /* chunk's address in the file */
	size uint64 /* chunk's size in bytes */
	gap  uint64 /* gap bytes at end of chunk (v2 only) */
}

// h5o is H5O_t: an in-core object header.
type h5o struct {
	addr    haddr
	version uint8  /* Version of object header */
	flags   uint8  /* Object header status flags (v2) */
	nlink   uint32 /* link count */
	// Times (v2 headers with the time-tracking flag).
	atime, mtime, ctime, btime uint32
	hasTimes                   bool
	// Attribute storage phase-change values (v2 with H5O_HDR_ATTR_STORE_PHASE_CHANGE).
	maxCompact, minDense uint16
	hasAttrPhase         bool
	chunks               []h5oChunk
	mesgs                []*h5oMesg
}

// H5O_shared_t: header of messages stored in the shared message heap or
// another object header (H5O_SHARED_*).
type h5oShared struct {
	typ       uint8  /* H5O_SHARE_TYPE_*: 0 unshared, 1 SOHM, 2 committed, 3 here */
	heapID    []byte /* SOHM heap ID (typ == 1) */
	oh        haddr  /* address of object header holding the message (typ == 2) */
	msgTypeID uint16
}

const (
	h5oShareTypeUnshared  = 0
	h5oShareTypeSohm      = 1
	h5oShareTypeCommitted = 2
	h5oShareTypeHere      = 3
)

// h5oStab is H5O_stab_t.
type h5oStab struct {
	btreeAddr haddr /* Address of B-tree */
	heapAddr  haddr /* Address of name heap */
}

// h5oLinfo is H5O_linfo_t.
type h5oLinfo struct {
	trackCorder   bool   /* Are creation order values tracked on links? */
	indexCorder   bool   /* Are creation order values indexed on links? */
	maxCorder     int64  /* Current max. creation order value for group */
	fheapAddr     haddr  /* Address of fractal heap for storing "dense" links */
	nameBt2Addr   haddr  /* Address of v2 B-tree for indexing names of links */
	corderBt2Addr haddr  /* Address of v2 B-tree for indexing creation order of links */
	nlinks        uint64 /* Number of links in the group (set from the B-tree, ^0 when unknown) */
}

// h5oGinfo is H5O_ginfo_t.
type h5oGinfo struct {
	storeLinkPhaseChange bool
	maxCompact           uint16
	minDense             uint16
	storeEstEntryInfo    bool
	estNumEntries        uint16
	estNameLen           uint16
}

// h5oLink is H5O_link_t.
type h5oLink struct {
	typ         h5.LinkType
	corderValid bool
	corder      int64
	cset        h5.Cset
	name        string
	// u.hard
	addr haddr
	// u.soft
	softName string
	// u.ud
	udSize int
	udData []byte
}

// h5oEflEntry is H5O_efl_entry_t.
type h5oEflEntry struct {
	nameOffset uint64 /* offset of name within heap */
	name       string
	offset     int64  /* offset of data within file */
	size       uint64 /* size allocated within file */
}

// h5oEfl is H5O_efl_t.
type h5oEfl struct {
	heapAddr haddr
	nalloc   int
	nused    int
	slot     []h5oEflEntry
}

// H5D_layout_t / H5D_chunk_index_t values.
const (
	h5dCompact    = 0
	h5dContiguous = 1
	h5dChunked    = 2
	h5dVirtual    = 3

	h5dChunkIdxBtree  = 0
	h5dChunkIdxSingle = 1
	h5dChunkIdxNone   = 2
	h5dChunkIdxFarray = 3
	h5dChunkIdxEarray = 4
	h5dChunkIdxBt2    = 5

	h5oLayoutNdims = 33 // H5O_LAYOUT_NDIMS (H5S_MAX_RANK + 1)
)

// h5oLayout is H5O_layout_t (the parts that can be decoded from the file).
type h5oLayout struct {
	typ     uint8 /* Type of layout: H5D_COMPACT / CONTIGUOUS / CHUNKED / VIRTUAL */
	version uint8 /* Version of layout message */

	// storage.u.compact
	compactSize uint64
	compactBuf  []byte

	// storage.u.contig
	contigAddr haddr
	contigSize uint64

	// u.chunk
	chunkNdims   uint32   /* Num dimensions in chunk (including the datatype-size dimension) */
	chunkDim     []uint64 /* Size of chunk in elements, chunkNdims entries (last = datatype size) */
	chunkIdxType uint8    /* H5D_CHUNK_IDX_* */
	chunkIdxAddr haddr    /* Address of chunk index */
	chunkFlags   uint8    /* v4: H5O_LAYOUT_CHUNK_*FLAG */
	// u.chunk.u for v4 index types
	singleFiltered              bool
	singleNbytes                uint64
	singleFilterMask            uint32
	farrayMaxDblkPageNelmtsBits uint8
	earrayCparam                h5eaCreateParams
	bt2Cparam                   h5b2CreateParams

	// storage.u.virt (version 4, H5D_VIRTUAL): the mappings are stored in the
	// global heap at this address/index.
	virtHeapAddr haddr
	virtHeapIdx  uint32
	virt         *h5oStorageVirtual

	// Layout versions 1 & 2 stored the dimensions and size in the message.
	ndims uint32
	dim   []uint64
}

// H5O_LAYOUT_CHUNK_* flags (v4).
const (
	h5oLayoutChunkDontFilterPartialBoundChunks = 0x01
	h5oLayoutChunkSingleIndexWithFilter        = 0x02
)

// h5eaCreateParams is H5EA_create_t (the subset stored in the layout message).
type h5eaCreateParams struct {
	maxNelmtsBits         uint8
	idxBlkElmts           uint8
	supBlkMinDataPtrs     uint8
	dataBlkMinElmts       uint8
	maxDblkPageNelmtsBits uint8
}

// h5b2CreateParams is H5B2_create_t (the subset stored in the layout message).
type h5b2CreateParams struct {
	nodeSize     uint32
	splitPercent uint8
	mergePercent uint8
}

// h5oStorageVirtual is H5O_storage_virtual_t (decoded from the global heap).
type h5oStorageVirtual struct {
	version uint32
	list    []h5oStorageVirtualEnt
}

// h5oStorageVirtualEnt is H5O_storage_virtual_ent_t.
type h5oStorageVirtualEnt struct {
	sourceFileName string
	sourceDsetName string
	sourceSelect   *h5sSelection /* selection in the source dataset */
	virtualSelect  *h5sSelection /* selection in the virtual dataset */
}

// H5Z_FLAG_* / H5Z_filter_t
const (
	h5zFlagDefmask   = 0x00ff
	h5zFlagMandatory = 0x0000
	h5zFlagOptional  = 0x0001
	h5zFlagInvmask   = 0xff00
	h5zFlagReverse   = 0x0100
	h5zFlagSkipEdc   = 0x0200

	h5zFilterError       = -1
	h5zFilterNone        = 0
	h5zFilterDeflate     = 1
	h5zFilterShuffle     = 2
	h5zFilterFletcher32  = 3
	h5zFilterSzip        = 4
	h5zFilterNbit        = 5
	h5zFilterScaleoffset = 6
	h5zFilterReserved    = 256
	h5zFilterMax         = 65535
)

// h5zFilterInfo is H5Z_filter_info_t.
type h5zFilterInfo struct {
	id       int32    /* Filter ID */
	flags    uint16   /* Flags */
	name     string   /* Optional filter name */
	cdValues []uint32 /* Client data values */
}

// h5oPline is H5O_pline_t.
type h5oPline struct {
	version uint8
	nused   int
	filter  []h5zFilterInfo
}

// H5D_alloc_time_t / H5D_fill_time_t
const (
	h5dAllocTimeDefault = 0
	h5dAllocTimeEarly   = 1
	h5dAllocTimeLate    = 2
	h5dAllocTimeIncr    = 3

	h5dFillTimeAlloc = 0
	h5dFillTimeNever = 1
	h5dFillTimeIfset = 2

	h5dFillValueError       = -1
	h5dFillValueUndefined   = 0
	h5dFillValueDefault     = 1
	h5dFillValueUserDefined = 2
)

// h5oFill is H5O_fill_t (both the old and new fill value messages decode
// into it).
type h5oFill struct {
	version     uint8
	size        int64 /* Size of fill value, -1 means undefined */
	buf         []byte
	allocTime   uint8
	fillTime    uint8
	fillDefined bool /* Whether fill value is defined */
}

// h5oAttr is H5A_t as far as the attribute message describes it.
type h5oAttr struct {
	version  uint8
	name     string
	cset     h5.Cset /* Character set of attribute name (v3) */
	dt       *h5.Datatype
	dtShared *h5oShared /* set when the datatype message is shared */
	ds       *h5sExtent
	dsShared *h5oShared
	data     []byte /* raw attribute data in the file datatype */
	dataSize uint64
	crtIdx   uint16 /* creation index (dense storage) */
	// sharedInfo for shared attributes
	shared *h5oShared
}

// h5oAinfo is H5O_ainfo_t.
type h5oAinfo struct {
	trackCorder   bool
	indexCorder   bool
	maxCrtIdx     uint16
	nattrs        uint64 /* Number of attributes on the object (^0 when not stored) */
	fheapAddr     haddr
	nameBt2Addr   haddr
	corderBt2Addr haddr
}

// h5oShmesgTable is H5O_shmesg_table_t.
type h5oShmesgTable struct {
	addr     haddr
	version  uint8
	nindexes uint8
}

// h5oBtreek is H5O_btreek_t.
type h5oBtreek struct {
	btreeK   [2]uint
	symLeafK uint
}

// h5oDrvinfo is H5O_drvinfo_t; see also drvinfo in h5f_super.go.
type h5oDrvinfo struct {
	name string
	buf  []byte
}

// h5oFsinfo is H5O_fsinfo_t (file space info; read for completeness).
type h5oFsinfo struct {
	version        uint8
	strategy       uint8
	persist        bool
	threshold      uint64
	pageSize       uint64
	pgendMetaThres uint16
	eoa            haddr
	fsAddr         [12]haddr /* H5F_MEM_PAGE_NTYPES - 1 addresses of free-space managers */
}

// h5oRefcount is H5O_refcount_t.
type h5oRefcount uint32

// h5oMtime is time_t (seconds since the epoch) for both mtime messages.
type h5oMtime int64

// h5oName is the object comment message.
type h5oName string

// h5oCont is H5O_cont_t.
type h5oCont struct {
	addr haddr
	size uint64
}

// h5oBogus is H5O_bogus_t.
type h5oBogus struct {
	u uint32
}

// h5oUnknown is H5O_unknown_t: the type id of an unknown message.
type h5oUnknown uint16

// h5oMdci is H5O_mdci_t.
type h5oMdci struct {
	addr haddr
	size uint64
}
