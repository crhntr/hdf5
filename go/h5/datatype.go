package h5

import "fmt"

// Class is the datatype class, numbered as in the HDF5 file format
// specification ("Datatype Message", class field) and as H5T_class_t.
type Class uint8

const (
	ClassInteger   Class = 0 // fixed-point
	ClassFloat     Class = 1 // floating-point
	ClassTime      Class = 2
	ClassString    Class = 3
	ClassBitfield  Class = 4
	ClassOpaque    Class = 5
	ClassCompound  Class = 6
	ClassReference Class = 7
	ClassEnum      Class = 8
	ClassVlen      Class = 9
	ClassArray     Class = 10
)

func (c Class) String() string {
	switch c {
	case ClassInteger:
		return "integer"
	case ClassFloat:
		return "float"
	case ClassTime:
		return "time"
	case ClassString:
		return "string"
	case ClassBitfield:
		return "bitfield"
	case ClassOpaque:
		return "opaque"
	case ClassCompound:
		return "compound"
	case ClassReference:
		return "reference"
	case ClassEnum:
		return "enum"
	case ClassVlen:
		return "vlen"
	case ClassArray:
		return "array"
	}
	return fmt.Sprintf("class(%d)", uint8(c))
}

// ByteOrder mirrors H5T_order_t.
type ByteOrder uint8

const (
	OrderLE    ByteOrder = 0
	OrderBE    ByteOrder = 1
	OrderVAX   ByteOrder = 2
	OrderMixed ByteOrder = 3 // compound with members of mixed order
	OrderNone  ByteOrder = 4 // no particular order (strings, opaque, ...)
)

// Sign mirrors H5T_sign_t.
type Sign uint8

const (
	SignNone Sign = 0 // unsigned
	Sign2    Sign = 1 // two's complement
)

// Pad mirrors H5T_pad_t (bit padding of integer/float fields).
type Pad uint8

const (
	PadZero       Pad = 0
	PadOne        Pad = 1
	PadBackground Pad = 2
)

// Norm mirrors H5T_norm_t (mantissa normalization of floats).
type Norm uint8

const (
	NormImplied Norm = 0
	NormMSBSet  Norm = 1
	NormNone    Norm = 2
)

// StrPad mirrors H5T_str_t.
type StrPad uint8

const (
	StrNullTerm StrPad = 0
	StrNullPad  StrPad = 1
	StrSpacePad StrPad = 2
)

// Cset mirrors H5T_cset_t.
type Cset uint8

const (
	CsetASCII Cset = 0
	CsetUTF8  Cset = 1
)

// RefType mirrors the reference "type" field of the datatype message /
// H5R_type_t. Object and dataset-region references are the classic (pre 1.12)
// kinds; the others are the revised references introduced in HDF5 1.12.
type RefType uint8

const (
	RefObject1 RefType = 0 // H5R_OBJECT1: haddr_t sized object reference
	RefRegion1 RefType = 1 // H5R_DATASET_REGION1: global-heap backed region reference
	RefObject2 RefType = 2 // H5R_OBJECT2
	RefRegion2 RefType = 3 // H5R_DATASET_REGION2
	RefAttr    RefType = 4 // H5R_ATTR
)

// Datatype is a fully decoded HDF5 datatype. It is a tree: compound members,
// enum/vlen/array base types are themselves Datatypes.
//
// The leaf fields mirror the "Datatype Message" of the file format
// specification (and H5T_shared_t in the C library) so that the decoder in
// package hdf5 is a faithful translation of H5O__dtype_decode_helper while the
// same struct can be populated from the H5T* query API by package chdf5.
type Datatype struct {
	Class   Class
	Version uint8  // datatype message version (1..4); 0 when unknown (cgo)
	Size    uint64 // size in bytes of one element

	// Atomic properties (integer, float, time, string, bitfield, opaque,
	// reference). Precision/Offset are in bits.
	Order     ByteOrder
	Precision uint32
	Offset    uint32
	LSBPad    Pad
	MSBPad    Pad

	// Integer / bitfield.
	Sign Sign

	// Floating point.
	Float FloatInfo

	// String.
	StrPad StrPad
	Cset   Cset

	// Opaque.
	Tag string

	// Reference.
	RefType RefType

	// Compound.
	Members []Member

	// Enum. Values are stored as the raw little-endian unsigned
	// representation of the base type widened to uint64 (sign-extended for
	// signed bases) so both implementations agree.
	EnumNames  []string
	EnumValues []int64

	// Vlen.
	VlenKind VlenKind // sequence or string
	// Vlen string properties are StrPad/Cset above.

	// Array.
	ArrayDims []uint64

	// Base type for enum, vlen and array.
	Base *Datatype
}

// VlenKind mirrors H5T_vlen_type_t.
type VlenKind uint8

const (
	VlenSequence VlenKind = 0
	VlenString   VlenKind = 1
)

// FloatInfo mirrors the floating-point properties of the datatype message.
type FloatInfo struct {
	SignPos  uint32 // bit position of sign bit
	ExpPos   uint32
	ExpSize  uint32 // bits
	MantPos  uint32
	MantSize uint32 // bits
	ExpBias  uint64
	Norm     Norm
	IntPad   Pad // padding of internal (unused) bits
}

// Member is a compound member.
type Member struct {
	Name   string
	Offset uint64 // byte offset within the compound
	Type   *Datatype
}

// NumElems returns the number of base elements in an array type (product of
// ArrayDims), or 1 for non-array types.
func (dt *Datatype) NumElems() uint64 {
	n := uint64(1)
	for _, d := range dt.ArrayDims {
		n *= d
	}
	return n
}

// IsVlenString reports whether dt is a variable-length string.
func (dt *Datatype) IsVlenString() bool {
	return dt.Class == ClassVlen && dt.VlenKind == VlenString
}

// String renders a compact, h5dump-like description.
func (dt *Datatype) String() string {
	if dt == nil {
		return "<nil>"
	}
	switch dt.Class {
	case ClassInteger:
		s := "int"
		if dt.Sign == SignNone {
			s = "uint"
		}
		return fmt.Sprintf("%s%d%s", s, dt.Precision, dt.Order.suffix())
	case ClassFloat:
		return fmt.Sprintf("float%d%s", dt.Size*8, dt.Order.suffix())
	case ClassString:
		return fmt.Sprintf("string[%d]", dt.Size)
	case ClassVlen:
		if dt.VlenKind == VlenString {
			return "vlen string"
		}
		return fmt.Sprintf("vlen(%s)", dt.Base)
	case ClassArray:
		return fmt.Sprintf("array%v(%s)", dt.ArrayDims, dt.Base)
	case ClassCompound:
		return fmt.Sprintf("compound{%d members, %d bytes}", len(dt.Members), dt.Size)
	case ClassEnum:
		return fmt.Sprintf("enum(%s){%d}", dt.Base, len(dt.EnumNames))
	}
	return fmt.Sprintf("%s[%d]", dt.Class, dt.Size)
}

func (o ByteOrder) suffix() string {
	switch o {
	case OrderLE:
		return "le"
	case OrderBE:
		return "be"
	case OrderVAX:
		return "vax"
	}
	return ""
}
