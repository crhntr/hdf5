package h5

// LayoutClass mirrors H5D_layout_t.
type LayoutClass uint8

const (
	LayoutCompact    LayoutClass = 0
	LayoutContiguous LayoutClass = 1
	LayoutChunked    LayoutClass = 2
	LayoutVirtual    LayoutClass = 3
)

func (c LayoutClass) String() string {
	switch c {
	case LayoutCompact:
		return "compact"
	case LayoutContiguous:
		return "contiguous"
	case LayoutChunked:
		return "chunked"
	case LayoutVirtual:
		return "virtual"
	}
	return "unknown"
}

// ChunkIndex mirrors H5D_chunk_index_t (layout message version 4).
type ChunkIndex uint8

const (
	ChunkIndexBTree1 ChunkIndex = 0 // version 1 B-tree
	ChunkIndexSingle ChunkIndex = 1
	ChunkIndexNone   ChunkIndex = 2 // implicit
	ChunkIndexFArray ChunkIndex = 3 // fixed array
	ChunkIndexEArray ChunkIndex = 4 // extensible array
	ChunkIndexBTree2 ChunkIndex = 5 // version 2 B-tree
)

// Layout describes the storage layout of a dataset.
type Layout struct {
	Class   LayoutClass
	Version uint8 // layout message version (1..4); 0 when unknown

	// Chunked.
	ChunkDims  []uint64 // not including the trailing datatype-size "dimension"
	ChunkIndex ChunkIndex

	// Contiguous: address and size of the data; AddressDefined is false when
	// storage was never allocated.
	Address        uint64
	AddressDefined bool
	Size           uint64

	// Compact: size of the in-header data.
	CompactSize uint64

	// External files (External Data Files message), contiguous only.
	External []ExternalFile

	// Virtual: the source mappings, in mapping order.
	Virtual []VirtualMapping
}

// VirtualMapping is one mapping of a virtual dataset: the elements of the
// virtual dataset selected by VirtualSelection come from the elements
// selected by SourceSelection of SourceDataset in SourceFile ("." for the
// same file). Unlimited hyperslab selections are described by
// Selection.Unlimited.
type VirtualMapping struct {
	SourceFile       string
	SourceDataset    string
	SourceSelection  Selection
	VirtualSelection Selection
}

// ExternalFile is one entry of the External Data Files message.
type ExternalFile struct {
	Name   string
	Offset uint64
	Size   uint64
}

// FilterID mirrors H5Z_filter_t for the built-in filters.
type FilterID uint16

const (
	FilterDeflate     FilterID = 1
	FilterShuffle     FilterID = 2
	FilterFletcher32  FilterID = 3
	FilterSzip        FilterID = 4
	FilterNbit        FilterID = 5
	FilterScaleOffset FilterID = 6
)

// Filter is one entry of the filter pipeline message.
type Filter struct {
	ID       FilterID
	Name     string
	Flags    uint16 // H5Z_FLAG_OPTIONAL = 0x0001
	CDValues []uint32
}

// FillValueInfo mirrors the fill value message.
type FillValueInfo struct {
	Version   uint8
	AllocTime uint8 // H5D_alloc_time_t
	FillTime  uint8 // H5D_fill_time_t
	Defined   bool  // a user fill value is present
	Value     []byte
	Undefined bool // fill value explicitly undefined
}
