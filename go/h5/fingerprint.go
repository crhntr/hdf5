package h5

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
)

// Canonical encoding and fingerprints.
//
// WriteCanonical serializes a value of the shared vocabulary into a
// deterministic byte stream: every value starts with a one-byte type tag,
// numbers are fixed-width little endian (floats by their IEEE bit pattern,
// so NaN payloads and -0 are distinguished), strings and slices are
// length-prefixed and struct fields are written in declaration order. It
// does not depend on pointer addresses, map iteration order or fmt.
//
// Equality of encodings follows what the conformance suite compares:
//
//   - in value trees ([]any as returned by ReadValues) an int32 differs from
//     an int64 of the same value, a nil []any or []byte equals an empty one
//     (the suite compares their lengths and contents), compound fields
//     are compared in order, with their names;
//   - in the descriptions (Datatype, Dataspace, Layout, Link, ...) and in
//     RegionRef selections, a nil slice differs from an empty one
//     (reflect.DeepEqual).
//
// Fingerprints of this encoding are stored in the golden file of the pure
// Go module's tests (go/testdata/golden): changing the encoding requires
// regenerating that file (cd cgo && make golden).

// Type tags of the canonical encoding.
const (
	tagNil byte = iota
	tagInt8
	tagInt16
	tagInt32
	tagInt64
	tagUint8
	tagUint16
	tagUint32
	tagUint64
	tagFloat32
	tagFloat64
	tagString
	tagBytes
	tagBool
	tagList
	tagEnum
	tagCompound
	tagObjectRef
	tagRegionRef
	tagAttrRef
	tagSelection
	tagHyperslab
	tagDatatype
	tagDataspace
	tagLink
	tagObjectInfo
	tagSuperblock
	tagLayout
	tagFilter
	tagFillValue
	tagVirtualMapping
	tagExternalFile
	tagLinks
	tagFilters
	tagStrings
	tagUint64s
	tagFloat64s
	tagInt
	tagUint
	tagInt64s
	tagUint32s
	tagFloat32s
)

// FingerprintSize is the length in hex digits of a Fingerprint.
const FingerprintSize = 16

// Fingerprint returns the first 8 bytes of the SHA-256 of the canonical
// encoding of v (WriteCanonical), as 16 lowercase hex digits. It panics if
// v holds a type WriteCanonical does not support.
func Fingerprint(v any) string {
	h := sha256.New()
	if err := WriteCanonical(h, v); err != nil {
		panic(err)
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:FingerprintSize/2])
}

// WriteCanonical writes the canonical encoding of v to w. Supported are
// the canonical values (nil, the integer types, float32, float64, string,
// []byte, bool, []any, Enum, Compound, ObjectRef, RegionRef, AttrRef),
// Selection, Hyperslab, Datatype, Dataspace, Link, ObjectInfo,
// SuperblockInfo, Layout, VirtualMapping, ExternalFile, Filter,
// FillValueInfo, pointers to those structs, and slices of Link, Filter,
// string, uint64, int64, uint32, float32 and float64.
func WriteCanonical(w io.Writer, v any) error {
	e := &encoder{w: w, buf: make([]byte, 0, 4096)}
	e.value(v)
	if e.err == nil {
		e.flush()
	}
	return e.err
}

type encoder struct {
	w   io.Writer
	buf []byte
	err error
}

func (e *encoder) flush() {
	if e.err == nil && len(e.buf) > 0 {
		_, e.err = e.w.Write(e.buf)
	}
	e.buf = e.buf[:0]
}

func (e *encoder) grow() {
	if len(e.buf) >= 4096 {
		e.flush()
	}
}

func (e *encoder) tag(t byte) {
	e.grow()
	e.buf = append(e.buf, t)
}

func (e *encoder) u8(v uint8)   { e.buf = append(e.buf, v) }
func (e *encoder) u16(v uint16) { e.buf = binary.LittleEndian.AppendUint16(e.buf, v) }
func (e *encoder) u32(v uint32) { e.buf = binary.LittleEndian.AppendUint32(e.buf, v) }
func (e *encoder) u64(v uint64) { e.buf = binary.LittleEndian.AppendUint64(e.buf, v) }

func (e *encoder) boolean(b bool) {
	if b {
		e.u8(1)
	} else {
		e.u8(0)
	}
}

// length writes a slice or string length.
func (e *encoder) length(n int) { e.u64(uint64(n)) }

// nilable writes the nil marker of a slice whose nil-ness is significant
// and reports whether the slice is non-nil (its length follows).
func (e *encoder) nilable(isNil bool) bool {
	e.boolean(!isNil)
	return !isNil
}

func (e *encoder) str(s string) {
	e.length(len(s))
	e.grow()
	e.buf = append(e.buf, s...)
}

func (e *encoder) bytes(b []byte) {
	e.length(len(b))
	e.grow()
	e.buf = append(e.buf, b...)
}

// nbytes writes a []byte whose nil-ness is significant.
func (e *encoder) nbytes(b []byte) {
	if e.nilable(b == nil) {
		e.bytes(b)
	}
}

func (e *encoder) uint64s(s []uint64) {
	if e.nilable(s == nil) {
		e.length(len(s))
		for _, v := range s {
			e.grow()
			e.u64(v)
		}
	}
}

func (e *encoder) int64s(s []int64) {
	if e.nilable(s == nil) {
		e.length(len(s))
		for _, v := range s {
			e.grow()
			e.u64(uint64(v))
		}
	}
}

func (e *encoder) uint32s(s []uint32) {
	if e.nilable(s == nil) {
		e.length(len(s))
		for _, v := range s {
			e.grow()
			e.u32(v)
		}
	}
}

func (e *encoder) strings(s []string) {
	if e.nilable(s == nil) {
		e.length(len(s))
		for _, v := range s {
			e.str(v)
		}
	}
}

func (e *encoder) value(v any) {
	if e.err != nil {
		return
	}
	switch v := v.(type) {
	case nil:
		e.tag(tagNil)
	case int8:
		e.tag(tagInt8)
		e.u8(uint8(v))
	case int16:
		e.tag(tagInt16)
		e.u16(uint16(v))
	case int32:
		e.tag(tagInt32)
		e.u32(uint32(v))
	case int64:
		e.tag(tagInt64)
		e.u64(uint64(v))
	case int:
		e.tag(tagInt)
		e.u64(uint64(v))
	case uint8:
		e.tag(tagUint8)
		e.u8(v)
	case uint16:
		e.tag(tagUint16)
		e.u16(v)
	case uint32:
		e.tag(tagUint32)
		e.u32(v)
	case uint64:
		e.tag(tagUint64)
		e.u64(v)
	case uint:
		e.tag(tagUint)
		e.u64(uint64(v))
	case float32:
		e.tag(tagFloat32)
		e.u32(math.Float32bits(v))
	case float64:
		e.tag(tagFloat64)
		e.u64(math.Float64bits(v))
	case bool:
		e.tag(tagBool)
		e.boolean(v)
	case string:
		e.tag(tagString)
		e.str(v)
	case []byte:
		e.tag(tagBytes)
		e.bytes(v)
	case []any:
		e.tag(tagList)
		e.length(len(v))
		for _, x := range v {
			e.value(x)
		}
	case Enum:
		e.tag(tagEnum)
		e.str(v.Name)
		e.u64(uint64(v.Value))
	case Compound:
		e.tag(tagCompound)
		e.length(len(v.Fields))
		for _, f := range v.Fields {
			e.str(f.Name)
			e.value(f.Value)
		}
	case ObjectRef:
		e.tag(tagObjectRef)
		e.u64(v.Address)
		e.boolean(v.Undefined)
	case RegionRef:
		e.tag(tagRegionRef)
		e.u64(v.Address)
		e.boolean(v.Undefined)
		e.selection(v.Selection)
	case AttrRef:
		e.tag(tagAttrRef)
		e.u64(v.Address)
		e.boolean(v.Undefined)
		e.str(v.Name)
	case Selection:
		e.selection(v)
	case *Selection:
		if v == nil {
			e.tag(tagNil)
		} else {
			e.selection(*v)
		}
	case Hyperslab:
		e.hyperslab(&v)
	case *Hyperslab:
		e.hyperslab(v)
	case Datatype:
		e.datatype(&v)
	case *Datatype:
		e.datatype(v)
	case Dataspace:
		e.dataspace(v)
	case *Dataspace:
		if v == nil {
			e.tag(tagNil)
		} else {
			e.dataspace(*v)
		}
	case Link:
		e.link(v)
	case []Link:
		e.tag(tagLinks)
		if e.nilable(v == nil) {
			e.length(len(v))
			for _, l := range v {
				e.link(l)
			}
		}
	case ObjectInfo:
		e.objectInfo(v)
	case SuperblockInfo:
		e.superblock(v)
	case Layout:
		e.layout(v)
	case VirtualMapping:
		e.virtualMapping(v)
	case ExternalFile:
		e.externalFile(v)
	case Filter:
		e.filter(v)
	case []Filter:
		e.tag(tagFilters)
		if e.nilable(v == nil) {
			e.length(len(v))
			for _, f := range v {
				e.filter(f)
			}
		}
	case FillValueInfo:
		e.fillValue(v)
	case []string:
		e.tag(tagStrings)
		e.strings(v)
	case []uint64:
		e.tag(tagUint64s)
		e.uint64s(v)
	case []int64:
		e.tag(tagInt64s)
		e.int64s(v)
	case []uint32:
		e.tag(tagUint32s)
		e.uint32s(v)
	case []float64:
		e.tag(tagFloat64s)
		if e.nilable(v == nil) {
			e.length(len(v))
			for _, f := range v {
				e.grow()
				e.u64(math.Float64bits(f))
			}
		}
	case []float32:
		e.tag(tagFloat32s)
		if e.nilable(v == nil) {
			e.length(len(v))
			for _, f := range v {
				e.grow()
				e.u32(math.Float32bits(f))
			}
		}
	default:
		e.err = fmt.Errorf("h5: no canonical encoding for %T", v)
	}
}

func (e *encoder) selection(s Selection) {
	e.tag(tagSelection)
	e.u8(uint8(s.Type))
	e.u64(uint64(s.Rank))
	if e.nilable(s.Blocks == nil) {
		e.length(len(s.Blocks))
		for _, b := range s.Blocks {
			e.uint64s(b[0])
			e.uint64s(b[1])
		}
	}
	if e.nilable(s.Points == nil) {
		e.length(len(s.Points))
		for _, p := range s.Points {
			e.uint64s(p)
		}
	}
	e.hyperslab(s.Unlimited)
}

func (e *encoder) hyperslab(h *Hyperslab) {
	if h == nil {
		e.tag(tagNil)
		return
	}
	e.tag(tagHyperslab)
	e.uint64s(h.Start)
	e.uint64s(h.Stride)
	e.uint64s(h.Count)
	e.uint64s(h.Block)
}

func (e *encoder) datatype(dt *Datatype) {
	if dt == nil {
		e.tag(tagNil)
		return
	}
	e.tag(tagDatatype)
	e.u8(uint8(dt.Class))
	e.u8(dt.Version)
	e.u64(dt.Size)
	e.u8(uint8(dt.Order))
	e.u32(dt.Precision)
	e.u32(dt.Offset)
	e.u8(uint8(dt.LSBPad))
	e.u8(uint8(dt.MSBPad))
	e.u8(uint8(dt.Sign))
	f := dt.Float
	e.u32(f.SignPos)
	e.u32(f.ExpPos)
	e.u32(f.ExpSize)
	e.u32(f.MantPos)
	e.u32(f.MantSize)
	e.u64(f.ExpBias)
	e.u8(uint8(f.Norm))
	e.u8(uint8(f.IntPad))
	e.u8(uint8(dt.StrPad))
	e.u8(uint8(dt.Cset))
	e.str(dt.Tag)
	e.u8(uint8(dt.RefType))
	if e.nilable(dt.Members == nil) {
		e.length(len(dt.Members))
		for _, m := range dt.Members {
			e.str(m.Name)
			e.u64(m.Offset)
			e.datatype(m.Type)
		}
	}
	e.strings(dt.EnumNames)
	e.int64s(dt.EnumValues)
	e.u8(uint8(dt.VlenKind))
	e.uint64s(dt.ArrayDims)
	e.datatype(dt.Base)
}

func (e *encoder) dataspace(s Dataspace) {
	e.tag(tagDataspace)
	e.u8(uint8(s.Class))
	e.u8(s.Version)
	e.uint64s(s.Dims)
	e.uint64s(s.MaxDims)
}

func (e *encoder) link(l Link) {
	e.tag(tagLink)
	e.str(l.Name)
	e.u64(uint64(l.Type))
	e.u64(l.Address)
	e.str(l.SoftPath)
	e.str(l.ExternalFile)
	e.str(l.ExternalPath)
	e.nbytes(l.UserData)
	e.u64(uint64(l.CreationOrder))
	e.boolean(l.HasCreationOrder)
	e.u8(uint8(l.Cset))
}

func (e *encoder) objectInfo(o ObjectInfo) {
	e.tag(tagObjectInfo)
	e.u64(o.Address)
	e.u64(uint64(o.Type))
	e.u32(o.RefCount)
	e.u8(o.HeaderVersion)
	e.u64(o.NumAttrs)
	e.u64(uint64(o.ATime))
	e.u64(uint64(o.MTime))
	e.u64(uint64(o.CTime))
	e.u64(uint64(o.BTime))
}

func (e *encoder) superblock(s SuperblockInfo) {
	e.tag(tagSuperblock)
	e.u8(s.Version)
	e.u8(s.SizeOfOffsets)
	e.u8(s.SizeOfLengths)
	e.u64(s.BaseAddress)
	e.u64(s.ExtensionAddress)
	e.u64(s.DriverInfoAddress)
	e.u64(s.RootGroupAddress)
	e.u64(s.EndOfFileAddress)
	e.u8(s.StatusFlags)
	e.u64(uint64(s.SymLeafK))
	e.u64(uint64(s.SnodeBtreeK))
	e.u64(uint64(s.ChunkBtreeK))
	e.str(s.DriverName)
}

func (e *encoder) layout(l Layout) {
	e.tag(tagLayout)
	e.u8(uint8(l.Class))
	e.u8(l.Version)
	e.uint64s(l.ChunkDims)
	e.u8(uint8(l.ChunkIndex))
	e.u64(l.Address)
	e.boolean(l.AddressDefined)
	e.u64(l.Size)
	e.u64(l.CompactSize)
	if e.nilable(l.External == nil) {
		e.length(len(l.External))
		for _, x := range l.External {
			e.externalFile(x)
		}
	}
	if e.nilable(l.Virtual == nil) {
		e.length(len(l.Virtual))
		for _, m := range l.Virtual {
			e.virtualMapping(m)
		}
	}
}

func (e *encoder) virtualMapping(m VirtualMapping) {
	e.tag(tagVirtualMapping)
	e.str(m.SourceFile)
	e.str(m.SourceDataset)
	e.selection(m.SourceSelection)
	e.selection(m.VirtualSelection)
}

func (e *encoder) externalFile(x ExternalFile) {
	e.tag(tagExternalFile)
	e.str(x.Name)
	e.u64(x.Offset)
	e.u64(x.Size)
}

func (e *encoder) filter(f Filter) {
	e.tag(tagFilter)
	e.u16(uint16(f.ID))
	e.str(f.Name)
	e.u16(f.Flags)
	e.uint32s(f.CDValues)
}

func (e *encoder) fillValue(f FillValueInfo) {
	e.tag(tagFillValue)
	e.u8(f.Version)
	e.u8(f.AllocTime)
	e.u8(f.FillTime)
	e.boolean(f.Defined)
	e.nbytes(f.Value)
	e.boolean(f.Undefined)
}
