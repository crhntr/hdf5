package h5

// Decoded values.
//
// Both implementations can decode a dataset or attribute into a tree of
// ordinary Go values built from a small fixed set of types so the conformance
// tests can compare them with reflect.DeepEqual / testify. The mapping from a
// Datatype to Go is:
//
//	integer (by Size & Sign)   int8 int16 int32 int64 uint8 uint16 uint32 uint64
//	integer, other sizes       int64 / uint64 (sign extended)
//	float, Size 4              float32
//	float, otherwise           float64 (non IEEE layouts are converted)
//	string (fixed)             string (padding stripped per StrPad)
//	vlen string                string
//	bitfield                   uint64 (Precision bits, right aligned)
//	opaque, time               []byte
//	reference (object)         ObjectRef
//	reference (region)         RegionRef
//	enum                       Enum
//	compound                   Compound
//	array                      []any  (row-major, one entry per element)
//	vlen sequence              []any
//
// A dataset or attribute with N elements decodes to a []any of N values in
// row-major order of the dataspace; a scalar decodes to a 1-element []any and
// a null dataspace to an empty one.

// Enum is a decoded enumeration value.
type Enum struct {
	Name  string // "" if the raw value is not a member
	Value int64
}

// Compound is a decoded compound value; Fields is in member order.
type Compound struct {
	Fields []Field
}

// Field is one member of a Compound.
type Field struct {
	Name  string
	Value any
}

// ObjectRef is a decoded object reference: the address of the referenced
// object header (relative to the file base address). Undefined is true for
// the "null" reference.
type ObjectRef struct {
	Address   uint64
	Undefined bool
}

// RegionRef is a decoded dataset-region reference: the dataset address and a
// canonical description of the selection.
type RegionRef struct {
	Address   uint64
	Undefined bool
	Selection Selection
}

// SelectionType mirrors H5S_sel_type.
type SelectionType uint8

const (
	SelNone      SelectionType = 0
	SelPoints    SelectionType = 1
	SelHyperslab SelectionType = 2
	SelAll       SelectionType = 3
)

// Selection is a canonical serialized selection. Hyperslab selections are
// expressed as a list of blocks (start, end inclusive, per dimension),
// which is how both the file format (version 1) and H5Sget_select_hyper_blocklist
// report them; point selections as a list of coordinates.
type Selection struct {
	Type   SelectionType
	Rank   int
	Blocks [][2][]uint64 // for SelHyperslab: {start, end} pairs
	Points [][]uint64    // for SelPoints
}
