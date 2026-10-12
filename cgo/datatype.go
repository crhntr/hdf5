//go:build cgo

package hdf5

/*
#cgo pkg-config: hdf5
#include "chdf5.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/crhntr/hdf5/go/h5"
)

// describeType fills an h5.Datatype from the H5T query API so that it
// equals what the pure Go package (github.com/crhntr/hdf5/go) decodes from
// the datatype message.
//
// Datatypes returned by H5Dget_type / H5Aget_type / H5Topen2 are marked as
// "in memory" by the library, which changes the size of variable-length and
// reference types (and so the size of compounds and arrays containing them,
// and the offsets of later compound members). The on-disk sizes are
// recomputed here from the size of addresses in the file (sizeofAddr), as
// H5T_set_loc(..., H5T_LOC_DISK) computes them.
//
// The datatype message version, and the reference type of reference
// datatypes, have no getter: they are read from the H5Tencode serialization
// of each (sub)type, which is the datatype message itself.
//
// mu must be held.
func describeType(tid C.hid_t, sizeofAddr uint64) (*h5.Datatype, error) {
	dt, _, err := describe(tid, sizeofAddr, false)
	return dt, err
}

// describe returns the description and the in-memory size of tid.
// inVlen is set for the base type of a variable-length sequence.
func describe(tid C.hid_t, sizeofAddr uint64, inVlen bool) (*h5.Datatype, uint64, error) {
	cls := C.H5Tget_class(tid)
	if cls < 0 {
		return nil, 0, h5err("H5Tget_class")
	}
	memSize := uint64(C.H5Tget_size(tid))
	if memSize == 0 {
		return nil, 0, h5err("H5Tget_size")
	}
	version, flags, err := encodedHeader(tid)
	if err != nil {
		return nil, 0, err
	}
	dt := &h5.Datatype{Version: version, Size: memSize, Order: h5.OrderNone}
	switch cls {
	case C.H5T_INTEGER, C.H5T_BITFIELD:
		if cls == C.H5T_INTEGER {
			dt.Class = h5.ClassInteger
			dt.Sign = h5.Sign(C.H5Tget_sign(tid))
		} else {
			dt.Class = h5.ClassBitfield
		}
		if err := atomic(tid, dt); err != nil {
			return nil, 0, err
		}
	case C.H5T_FLOAT:
		dt.Class = h5.ClassFloat
		if err := atomic(tid, dt); err != nil {
			return nil, 0, err
		}
		var spos, epos, esize, mpos, msize C.size_t
		if C.H5Tget_fields(tid, &spos, &epos, &esize, &mpos, &msize) < 0 {
			return nil, 0, h5err("H5Tget_fields")
		}
		dt.Float = h5.FloatInfo{
			SignPos: uint32(spos), ExpPos: uint32(epos), ExpSize: uint32(esize),
			MantPos: uint32(mpos), MantSize: uint32(msize),
			ExpBias: uint64(C.H5Tget_ebias(tid)),
			Norm:    h5.Norm(C.H5Tget_norm(tid)),
			IntPad:  h5.Pad(C.H5Tget_inpad(tid)),
		}
	case C.H5T_TIME:
		dt.Class = h5.ClassTime
		dt.Order = h5.ByteOrder(C.H5Tget_order(tid))
		dt.Precision = uint32(C.H5Tget_precision(tid))
	case C.H5T_STRING:
		isVar := C.H5Tis_variable_str(tid)
		if isVar < 0 {
			return nil, 0, h5err("H5Tis_variable_str")
		}
		dt.StrPad = h5.StrPad(C.H5Tget_strpad(tid))
		dt.Cset = h5.Cset(C.H5Tget_cset(tid))
		if isVar > 0 {
			dt.Class = h5.ClassVlen
			dt.VlenKind = h5.VlenString
			dt.Size = vlenDiskSize(sizeofAddr)
			base, err := superOf(tid, sizeofAddr, false)
			if err != nil {
				return nil, 0, err
			}
			dt.Base = base
		} else {
			dt.Class = h5.ClassString
			dt.Precision = uint32(8 * dt.Size)
		}
	case C.H5T_OPAQUE:
		dt.Class = h5.ClassOpaque
		if tag := C.H5Tget_tag(tid); tag != nil {
			dt.Tag = C.GoString(tag)
			C.H5free_memory(unsafe.Pointer(tag))
		}
	case C.H5T_REFERENCE:
		dt.Class = h5.ClassReference
		dt.RefType = h5.RefType(flags & 0x0f)
		switch {
		case inVlen:
			// H5T_set_loc does not recurse from a sequence into its
			// reference base type: the size is the one decoded from the
			// datatype message, which is what H5T__conv_vlen uses for
			// the elements of a sequence.
		case dt.RefType == h5.RefObject1:
			dt.Size = sizeofAddr
		case dt.RefType == h5.RefRegion1:
			dt.Size = sizeofAddr + 4
		default:
			// H5T__ref_set_loc: MAX(4 + H5R_ENCODE_HEADER_SIZE + blob id
			// size, encoded size of an H5R_OBJECT2 reference).
			dt.Size = max(4+2+sizeofAddr+4, 2+1+sizeofAddr)
		}
		dt.Precision = uint32(8 * dt.Size)
	case C.H5T_ENUM:
		dt.Class = h5.ClassEnum
		base, err := superOf(tid, sizeofAddr, false)
		if err != nil {
			return nil, 0, err
		}
		dt.Base = base
		n := int(C.H5Tget_nmembers(tid))
		if n < 0 {
			return nil, 0, h5err("H5Tget_nmembers")
		}
		raw := make([]byte, max(memSize, 8))
		dt.EnumNames = make([]string, n)
		dt.EnumValues = make([]int64, n)
		for i := 0; i < n; i++ {
			name := C.H5Tget_member_name(tid, C.unsigned(i))
			if name == nil {
				return nil, 0, h5err("H5Tget_member_name")
			}
			dt.EnumNames[i] = C.GoString(name)
			C.H5free_memory(unsafe.Pointer(name))
			if C.H5Tget_member_value(tid, C.unsigned(i), unsafe.Pointer(&raw[0])) < 0 {
				return nil, 0, h5err("H5Tget_member_value")
			}
			dt.EnumValues[i] = enumRawValue(base, raw[:base.Size])
		}
	case C.H5T_VLEN:
		dt.Class = h5.ClassVlen
		dt.VlenKind = h5.VlenSequence
		dt.Size = vlenDiskSize(sizeofAddr)
		base, err := superOf(tid, sizeofAddr, true)
		if err != nil {
			return nil, 0, err
		}
		dt.Base = base
	case C.H5T_ARRAY:
		dt.Class = h5.ClassArray
		nd := int(C.H5Tget_array_ndims(tid))
		if nd < 0 {
			return nil, 0, h5err("H5Tget_array_ndims")
		}
		dims := make([]C.hsize_t, max(nd, 1))
		if C.H5Tget_array_dims2(tid, &dims[0]) < 0 {
			return nil, 0, h5err("H5Tget_array_dims2")
		}
		dt.ArrayDims = make([]uint64, nd)
		for i := range nd {
			dt.ArrayDims[i] = uint64(dims[i])
		}
		base, err := superOf(tid, sizeofAddr, false)
		if err != nil {
			return nil, 0, err
		}
		dt.Base = base
		dt.Size = dt.NumElems() * base.Size
	case C.H5T_COMPOUND:
		dt.Class = h5.ClassCompound
		n := int(C.H5Tget_nmembers(tid))
		if n < 0 {
			return nil, 0, h5err("H5Tget_nmembers")
		}
		dt.Members = make([]h5.Member, n)
		// Undo the offset shifts of H5T_set_loc: members are sorted by
		// offset and each member whose size changed moves the following
		// ones by the difference.
		var accum int64
		for i := 0; i < n; i++ {
			name := C.H5Tget_member_name(tid, C.unsigned(i))
			if name == nil {
				return nil, 0, h5err("H5Tget_member_name")
			}
			dt.Members[i].Name = C.GoString(name)
			C.H5free_memory(unsafe.Pointer(name))
			off := uint64(C.H5Tget_member_offset(tid, C.unsigned(i)))
			mt := C.H5Tget_member_type(tid, C.unsigned(i))
			if mt < 0 {
				return nil, 0, h5err("H5Tget_member_type")
			}
			mdt, msize, err := describe(mt, sizeofAddr, false)
			C.H5Tclose(mt)
			if err != nil {
				return nil, 0, err
			}
			dt.Members[i].Offset = uint64(int64(off) - accum)
			dt.Members[i].Type = mdt
			accum += int64(msize) - int64(mdt.Size)
		}
		dt.Size = uint64(int64(memSize) - accum)
	default:
		return nil, 0, fmt.Errorf("chdf5: unsupported datatype class %d", int(cls))
	}
	return dt, memSize, nil
}

// atomic fills the integer/float/bitfield properties.
func atomic(tid C.hid_t, dt *h5.Datatype) error {
	order := C.H5Tget_order(tid)
	if order < 0 {
		return h5err("H5Tget_order")
	}
	dt.Order = h5.ByteOrder(order)
	dt.Precision = uint32(C.H5Tget_precision(tid))
	off := C.H5Tget_offset(tid)
	if off < 0 {
		return h5err("H5Tget_offset")
	}
	dt.Offset = uint32(off)
	var lsb, msb C.H5T_pad_t
	if C.H5Tget_pad(tid, &lsb, &msb) < 0 {
		return h5err("H5Tget_pad")
	}
	dt.LSBPad, dt.MSBPad = h5.Pad(lsb), h5.Pad(msb)
	return nil
}

// superOf describes the base type of an enum, vlen or array.
func superOf(tid C.hid_t, sizeofAddr uint64, inVlen bool) (*h5.Datatype, error) {
	st := C.H5Tget_super(tid)
	if st < 0 {
		return nil, h5err("H5Tget_super")
	}
	defer C.H5Tclose(st)
	dt, _, err := describe(st, sizeofAddr, inVlen)
	return dt, err
}

// vlenDiskSize is the size of a vlen element in the file: the sequence
// length and a global heap ID (H5T__vlen_set_loc for H5T_LOC_DISK).
func vlenDiskSize(sizeofAddr uint64) uint64 { return 4 + sizeofAddr + 4 }

// encodedHeader returns the version and the class bit field of the
// datatype message for tid, from its H5Tencode serialization.
func encodedHeader(tid C.hid_t) (uint8, uint32, error) {
	var n C.size_t
	if C.H5Tencode(tid, nil, &n) < 0 {
		return 0, 0, h5err("H5Tencode")
	}
	if n < 6 {
		return 0, 0, fmt.Errorf("chdf5: short H5Tencode output")
	}
	buf := C.malloc(n)
	defer C.free(buf)
	if C.H5Tencode(tid, buf, &n) < 0 {
		return 0, 0, h5err("H5Tencode")
	}
	b := unsafe.Slice((*byte)(buf), 6)
	// b[0]: H5O_DTYPE_ID, b[1]: H5T_ENCODE_VERSION, then the message:
	// version<<4 | class, and 24 bits of class flags.
	return b[2] >> 4, uint32(b[3]) | uint32(b[4])<<8 | uint32(b[5])<<16, nil
}

// enumRawValue converts a raw enum member value in the base type to the
// int64 stored in h5.Datatype.EnumValues (as the pure Go package does).
func enumRawValue(base *h5.Datatype, raw []byte) int64 {
	switch base.Class {
	case h5.ClassInteger, h5.ClassBitfield:
		return int64(integerBits(base, raw))
	}
	v := uint64(0)
	for i := len(raw) - 1; i >= 0; i-- {
		v = v<<8 | uint64(raw[i])
	}
	return int64(v)
}

// integerBits extracts the Precision bits at Offset of an integer or
// bitfield element as an unsigned value, sign extended for signed integers
// (the same rule as the pure Go package).
func integerBits(dt *h5.Datatype, elem []byte) uint64 {
	s := elem
	if dt.Order == h5.OrderBE {
		s = make([]byte, len(elem))
		for i := range elem {
			s[len(elem)-1-i] = elem[i]
		}
	}
	total := uint64(len(s)) * 8
	off, prec := uint64(dt.Offset), uint64(dt.Precision)
	if off >= total || prec == 0 {
		return 0
	}
	if off+prec > total {
		prec = total - off
	}
	n := min(prec, 64)
	var v uint64
	for i := uint64(0); i < n; i++ {
		bit := off + i
		if s[bit/8]>>(bit%8)&1 != 0 {
			v |= 1 << i
		}
	}
	if dt.Class == h5.ClassInteger && dt.Sign == h5.Sign2 && prec < 64 && v>>(prec-1)&1 != 0 {
		v |= ^uint64(0) << prec
	}
	return v
}
