package h5

// LinkType mirrors H5L_type_t.
type LinkType int

const (
	LinkHard     LinkType = 0
	LinkSoft     LinkType = 1
	LinkExternal LinkType = 64
	// User-defined link classes are 65..255; LinkExternal is the only
	// built-in user-defined class.
)

func (t LinkType) String() string {
	switch t {
	case LinkHard:
		return "hard"
	case LinkSoft:
		return "soft"
	case LinkExternal:
		return "external"
	}
	return "user-defined"
}

// Link describes one link in a group.
type Link struct {
	Name string
	Type LinkType

	// Hard links: address of the object header.
	Address uint64
	// Soft links: the path.
	SoftPath string
	// External links: target file and object path.
	ExternalFile string
	ExternalPath string
	// User-defined links (including external): raw link value.
	UserData []byte

	// Creation order, and whether it was tracked for this link.
	CreationOrder    int64
	HasCreationOrder bool
	Cset             Cset
}

// ObjectType mirrors H5O_type_t.
type ObjectType int

const (
	ObjectGroup         ObjectType = 0
	ObjectDataset       ObjectType = 1
	ObjectNamedDatatype ObjectType = 2
	ObjectUnknown       ObjectType = -1
)

func (t ObjectType) String() string {
	switch t {
	case ObjectGroup:
		return "group"
	case ObjectDataset:
		return "dataset"
	case ObjectNamedDatatype:
		return "datatype"
	}
	return "unknown"
}

// ObjectInfo is the subset of H5O_info_t both implementations can report.
type ObjectInfo struct {
	Address       uint64
	Type          ObjectType
	RefCount      uint32
	HeaderVersion uint8 // object header version (1 or 2)
	NumAttrs      uint64
	// Times are seconds since the Unix epoch; zero when not tracked.
	ATime, MTime, CTime, BTime int64
}

// SuperblockInfo is the decoded superblock as reported to users.
type SuperblockInfo struct {
	Version           uint8
	SizeOfOffsets     uint8
	SizeOfLengths     uint8
	BaseAddress       uint64
	ExtensionAddress  uint64 // ^uint64(0) when absent
	DriverInfoAddress uint64 // ^uint64(0) when absent
	RootGroupAddress  uint64
	EndOfFileAddress  uint64
	StatusFlags       uint8
	SymLeafK          uint
	SnodeBtreeK       uint
	ChunkBtreeK       uint
	DriverName        string // 8-character driver id from the driver info block (superblock extension driver info message for superblock versions 2 and 3), "" when absent
}
