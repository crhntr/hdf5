//go:build cgo

package hdf5

/*
#cgo pkg-config: hdf5
#include "chdf5.h"
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"github.com/crhntr/hdf5/go/h5"
)

// Reading converts the file datatype to a memory datatype built here (the
// native equivalent of each leaf, see memType) with H5Dread / H5Aread and
// decodes the memory buffer to the canonical values of h5/value.go.

type memKind int

const (
	memInt      memKind = iota // native integer (H5Tget_native_type)
	memRawInt                  // file integer read as is (no native equivalent)
	memFloat32                 // H5T_NATIVE_FLOAT
	memFloat64                 // H5T_NATIVE_DOUBLE
	memBitfield                // H5T_NATIVE_B64
	memString                  // fixed-length string, file type
	memBytes                   // opaque / time, file type
	memEnum                    // enum, file type
	memRefObj1                 // hobj_ref_t
	memRef2                    // H5R_ref_t (region references and revised references)
	memCompound
	memArray
	memVlenSeq // hvl_t
	memVlenStr // char *
)

// memNode describes how one element of the memory type is decoded.
type memNode struct {
	kind   memKind
	size   uintptr      // memory size of one element
	dt     *h5.Datatype // file datatype description
	signed bool         // memInt: native type is signed
	fields []memField   // memCompound
	base   *memNode     // memArray, memVlenSeq
	nelem  int          // memArray
	names  map[int64]string
}

type memField struct {
	name   string
	offset uintptr
	node   *memNode
}

func align8(n uintptr) uintptr { return (n + 7) &^ 7 }

// memType builds the memory datatype for the file datatype tid (described
// by dt). The caller closes the returned id. mu must be held.
func memType(tid C.hid_t, dt *h5.Datatype) (C.hid_t, *memNode, error) {
	n := &memNode{dt: dt}
	var id C.hid_t
	switch dt.Class {
	case h5.ClassInteger:
		id = C.H5Tget_native_type(tid, C.H5T_DIR_DEFAULT)
		if id < 0 {
			C.H5Eclear2(C.H5E_DEFAULT)
			id = C.H5Tcopy(tid)
			n.kind = memRawInt
		} else {
			n.kind = memInt
			n.signed = C.H5Tget_sign(id) == C.H5T_SGN_2
		}
	case h5.ClassFloat:
		if isIEEESingle(dt) {
			n.kind, id = memFloat32, C.H5Tcopy(C.H5T_NATIVE_FLOAT_g)
		} else {
			n.kind, id = memFloat64, C.H5Tcopy(C.H5T_NATIVE_DOUBLE_g)
		}
	case h5.ClassBitfield:
		n.kind, id = memBitfield, C.H5Tcopy(C.H5T_NATIVE_B64_g)
	case h5.ClassString:
		n.kind, id = memString, C.H5Tcopy(tid)
	case h5.ClassTime, h5.ClassOpaque:
		n.kind, id = memBytes, C.H5Tcopy(tid)
	case h5.ClassEnum:
		n.kind, id = memEnum, C.H5Tcopy(tid)
		n.names = make(map[int64]string, len(dt.EnumNames))
		for i := len(dt.EnumValues) - 1; i >= 0; i-- {
			n.names[dt.EnumValues[i]] = dt.EnumNames[i]
		}
	case h5.ClassReference:
		switch dt.RefType {
		case h5.RefObject1:
			n.kind, id = memRefObj1, C.H5Tcopy(C.H5T_STD_REF_OBJ_g)
		default:
			// Region references (old and revised) and the revised
			// object/attribute references are read as H5R_ref_t.
			n.kind, id = memRef2, C.H5Tcopy(C.H5T_STD_REF_g)
		}
	case h5.ClassVlen:
		if dt.VlenKind == h5.VlenString {
			n.kind = memVlenStr
			id = C.H5Tcopy(C.H5T_C_S1_g)
			if id >= 0 {
				if C.H5Tset_size(id, C.H5T_VARIABLE) < 0 ||
					C.H5Tset_cset(id, C.H5T_cset_t(dt.Cset)) < 0 ||
					C.H5Tset_strpad(id, C.H5T_str_t(dt.StrPad)) < 0 {
					C.H5Tclose(id)
					return -1, nil, h5err("vlen string memory type")
				}
			}
			break
		}
		st := C.H5Tget_super(tid)
		if st < 0 {
			return -1, nil, h5err("H5Tget_super")
		}
		bid, bn, err := memType(st, dt.Base)
		C.H5Tclose(st)
		if err != nil {
			return -1, nil, err
		}
		n.kind, n.base = memVlenSeq, bn
		id = C.H5Tvlen_create(bid)
		C.H5Tclose(bid)
	case h5.ClassArray:
		st := C.H5Tget_super(tid)
		if st < 0 {
			return -1, nil, h5err("H5Tget_super")
		}
		bid, bn, err := memType(st, dt.Base)
		C.H5Tclose(st)
		if err != nil {
			return -1, nil, err
		}
		n.kind, n.base, n.nelem = memArray, bn, int(dt.NumElems())
		dims := make([]C.hsize_t, max(len(dt.ArrayDims), 1))
		for i, d := range dt.ArrayDims {
			dims[i] = C.hsize_t(d)
		}
		id = C.H5Tarray_create2(bid, C.unsigned(len(dt.ArrayDims)), &dims[0])
		C.H5Tclose(bid)
	case h5.ClassCompound:
		n.kind = memCompound
		type built struct {
			id   C.hid_t
			name *C.char
		}
		parts := make([]built, len(dt.Members))
		defer func() {
			for _, p := range parts {
				if p.id > 0 {
					C.H5Tclose(p.id)
				}
				if p.name != nil {
					C.free(unsafe.Pointer(p.name))
				}
			}
		}()
		var off uintptr
		for i, m := range dt.Members {
			mt := C.H5Tget_member_type(tid, C.unsigned(i))
			if mt < 0 {
				return -1, nil, h5err("H5Tget_member_type")
			}
			mid, mn, err := memType(mt, m.Type)
			C.H5Tclose(mt)
			if err != nil {
				return -1, nil, err
			}
			parts[i] = built{mid, C.CString(m.Name)}
			off = align8(off)
			n.fields = append(n.fields, memField{name: m.Name, offset: off, node: mn})
			off += mn.size
		}
		id = C.H5Tcreate(C.H5T_COMPOUND, C.size_t(max(align8(off), 1)))
		if id < 0 {
			return -1, nil, h5err("H5Tcreate(compound)")
		}
		for i, p := range parts {
			if C.H5Tinsert(id, p.name, C.size_t(n.fields[i].offset), p.id) < 0 {
				C.H5Tclose(id)
				return -1, nil, h5err("H5Tinsert")
			}
		}
	default:
		return -1, nil, fmt.Errorf("chdf5: unsupported datatype class %s", dt.Class)
	}
	if id < 0 {
		return -1, nil, h5err("memory datatype for %s", dt)
	}
	n.size = uintptr(C.H5Tget_size(id))
	return id, n, nil
}

// isIEEESingle reports whether dt has the IEEE binary32 layout (in either
// byte order); such floats decode to float32, all others to float64.
func isIEEESingle(dt *h5.Datatype) bool {
	return dt.Size == 4 && (dt.Order == h5.OrderLE || dt.Order == h5.OrderBE) &&
		dt.Precision == 32 && dt.Offset == 0 &&
		dt.LSBPad == h5.PadZero && dt.MSBPad == h5.PadZero &&
		dt.Float == h5.FloatInfo{SignPos: 31, ExpPos: 23, ExpSize: 8, MantPos: 0, MantSize: 23, ExpBias: 127, Norm: h5.NormImplied, IntPad: h5.PadZero}
}

// decoder carries what decoding references needs: an object id in the file
// the data was read from.
type decoder struct {
	loc C.hid_t
}

// decodeAll decodes n elements at buf. mu must be held.
func (d *decoder) decodeAll(buf unsafe.Pointer, node *memNode, n int) ([]any, error) {
	out := make([]any, n)
	for i := range out {
		v, err := d.decode(unsafe.Add(buf, uintptr(i)*node.size), node)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func cbytes(p unsafe.Pointer, n uintptr) []byte { return unsafe.Slice((*byte)(p), n) }

func (d *decoder) decode(p unsafe.Pointer, n *memNode) (any, error) {
	switch n.kind {
	case memInt:
		b := cbytes(p, n.size)
		var v uint64
		switch n.size {
		case 1:
			v = uint64(b[0])
			if n.signed {
				v = uint64(int64(int8(b[0])))
			}
		case 2:
			x := binary.NativeEndian.Uint16(b)
			v = uint64(x)
			if n.signed {
				v = uint64(int64(int16(x)))
			}
		case 4:
			x := binary.NativeEndian.Uint32(b)
			v = uint64(x)
			if n.signed {
				v = uint64(int64(int32(x)))
			}
		case 8:
			v = binary.NativeEndian.Uint64(b)
		default:
			return nil, fmt.Errorf("chdf5: unexpected native integer size %d", n.size)
		}
		return canonicalInt(n.dt, v), nil
	case memRawInt:
		return canonicalInt(n.dt, integerBits(n.dt, cbytes(p, n.size))), nil
	case memFloat32:
		return math.Float32frombits(binary.NativeEndian.Uint32(cbytes(p, 4))), nil
	case memFloat64:
		return math.Float64frombits(binary.NativeEndian.Uint64(cbytes(p, 8))), nil
	case memBitfield:
		return binary.NativeEndian.Uint64(cbytes(p, 8)), nil
	case memString:
		b := cbytes(p, n.size)
		switch n.dt.StrPad {
		case h5.StrNullTerm:
			if i := bytes.IndexByte(b, 0); i >= 0 {
				b = b[:i]
			}
		case h5.StrNullPad:
			b = bytes.TrimRight(b, "\x00")
		case h5.StrSpacePad:
			b = bytes.TrimRight(b, " ")
		}
		return string(b), nil
	case memBytes:
		return bytes.Clone(cbytes(p, n.size)), nil
	case memEnum:
		v := enumRawValue(n.dt.Base, cbytes(p, n.size))
		return h5.Enum{Name: n.names[v], Value: v}, nil
	case memRefObj1:
		addr := binary.NativeEndian.Uint64(cbytes(p, 8))
		switch {
		case addr == 0:
			return h5.ObjectRef{Undefined: true}, nil
		case addr == haddrUndef:
			return h5.ObjectRef{Address: addr, Undefined: true}, nil
		}
		return h5.ObjectRef{Address: addr}, nil
	case memRef2:
		if allZero(cbytes(p, n.size)) {
			switch n.dt.RefType {
			case h5.RefRegion1, h5.RefRegion2:
				return h5.RegionRef{Undefined: true}, nil
			case h5.RefAttr:
				return h5.AttrRef{Undefined: true}, nil
			}
			return h5.ObjectRef{Undefined: true}, nil
		}
		var typ C.int
		var addr C.haddr_t
		var space C.hid_t
		var attr *C.char
		rc := C.chdf5_ref2(d.loc, (*C.H5R_ref_t)(p), &typ, &addr, &space, &attr)
		if space > 0 {
			defer C.H5Sclose(space)
		}
		if attr != nil {
			defer C.free(unsafe.Pointer(attr))
		}
		if rc < 0 {
			return nil, h5err("dereference reference")
		}
		a := uint64(addr)
		switch h5.RefType(typ) {
		case h5.RefRegion1, h5.RefRegion2:
			sel, err := selectionOf(space)
			if err != nil {
				return nil, err
			}
			return h5.RegionRef{Address: a, Undefined: a == haddrUndef, Selection: sel}, nil
		case h5.RefAttr:
			return h5.AttrRef{Address: a, Undefined: a == haddrUndef, Name: C.GoString(attr)}, nil
		}
		return h5.ObjectRef{Address: a, Undefined: a == haddrUndef}, nil
	case memCompound:
		c := h5.Compound{Fields: make([]h5.Field, len(n.fields))}
		for i, f := range n.fields {
			v, err := d.decode(unsafe.Add(p, f.offset), f.node)
			if err != nil {
				return nil, err
			}
			c.Fields[i] = h5.Field{Name: f.name, Value: v}
		}
		return c, nil
	case memArray:
		return d.decodeAll(p, n.base, n.nelem)
	case memVlenSeq:
		length := int(C.chdf5_size_at(p))
		data := C.chdf5_ptr_at(unsafe.Add(p, unsafe.Sizeof(C.size_t(0))))
		if length == 0 || data == nil {
			return []any{}, nil
		}
		return d.decodeAll(data, n.base, length)
	case memVlenStr:
		s := C.chdf5_ptr_at(p)
		if s == nil {
			return "", nil
		}
		return C.GoString((*C.char)(s)), nil
	}
	return nil, fmt.Errorf("chdf5: internal error: memory kind %d", n.kind)
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// canonicalInt maps an integer (sign extended to 64 bits) to the Go type
// selected by the file datatype's size and sign.
func canonicalInt(dt *h5.Datatype, v uint64) any {
	if dt.Sign == h5.Sign2 {
		switch dt.Size {
		case 1:
			return int8(v)
		case 2:
			return int16(v)
		case 4:
			return int32(v)
		}
		return int64(v)
	}
	switch dt.Size {
	case 1:
		return uint8(v)
	case 2:
		return uint16(v)
	case 4:
		return uint32(v)
	}
	return v
}

// selectionOf describes the selection of a dataspace. mu must be held.
func selectionOf(space C.hid_t) (h5.Selection, error) {
	st := C.H5Sget_select_type(space)
	if st < 0 {
		return h5.Selection{}, h5err("H5Sget_select_type")
	}
	rank := int(C.H5Sget_simple_extent_ndims(space))
	if rank < 0 {
		return h5.Selection{}, h5err("H5Sget_simple_extent_ndims")
	}
	sel := h5.Selection{Type: h5.SelectionType(st), Rank: rank}
	switch st {
	case C.H5S_SEL_POINTS:
		np := C.H5Sget_select_elem_npoints(space)
		if np < 0 {
			return h5.Selection{}, h5err("H5Sget_select_elem_npoints")
		}
		if np == 0 || rank == 0 {
			break
		}
		buf := make([]C.hsize_t, int(np)*rank)
		if C.H5Sget_select_elem_pointlist(space, 0, C.hsize_t(np), &buf[0]) < 0 {
			return h5.Selection{}, h5err("H5Sget_select_elem_pointlist")
		}
		sel.Points = make([][]uint64, np)
		for i := range sel.Points {
			sel.Points[i] = hsizes(buf[i*rank : (i+1)*rank])
		}
	case C.H5S_SEL_HYPERSLABS:
		nb := C.H5Sget_select_hyper_nblocks(space)
		if nb < 0 {
			// Unlimited selections (virtual datasets) have no block list.
			C.H5Eclear2(C.H5E_DEFAULT)
			if rank == 0 {
				break
			}
			v := make([]C.hsize_t, 4*rank)
			if C.H5Sget_regular_hyperslab(space, &v[0], &v[rank], &v[2*rank], &v[3*rank]) < 0 {
				return h5.Selection{}, h5err("H5Sget_regular_hyperslab")
			}
			sel.Unlimited = &h5.Hyperslab{
				Start: hsizes(v[:rank]), Stride: hsizes(v[rank : 2*rank]),
				Count: hsizes(v[2*rank : 3*rank]), Block: hsizes(v[3*rank:]),
			}
			break
		}
		if nb == 0 || rank == 0 {
			break
		}
		buf := make([]C.hsize_t, int(nb)*2*rank)
		if C.H5Sget_select_hyper_blocklist(space, 0, C.hsize_t(nb), &buf[0]) < 0 {
			return h5.Selection{}, h5err("H5Sget_select_hyper_blocklist")
		}
		sel.Blocks = make([][2][]uint64, nb)
		for i := range sel.Blocks {
			b := buf[i*2*rank:]
			sel.Blocks[i] = [2][]uint64{hsizes(b[:rank]), hsizes(b[rank : 2*rank])}
		}
	}
	return sel, nil
}

func hsizes(b []C.hsize_t) []uint64 {
	out := make([]uint64, len(b))
	for i, x := range b {
		out[i] = uint64(x)
	}
	return out
}

// readInto reads count elements with read (H5Dread / H5Aread into a C
// buffer of the memory type) and decodes them. mu must be held.
func readValues(loc C.hid_t, tid C.hid_t, dt *h5.Datatype, count uint64, reclaimSpace C.hid_t,
	read func(mem C.hid_t, buf unsafe.Pointer) C.herr_t) ([]any, error) {
	if count == 0 {
		return []any{}, nil
	}
	mem, node, err := memType(tid, dt)
	if err != nil {
		return nil, err
	}
	defer C.H5Tclose(mem)
	total := uintptr(count) * node.size
	if node.size != 0 && total/node.size != uintptr(count) {
		return nil, fmt.Errorf("chdf5: %d elements of %d bytes overflow", count, node.size)
	}
	buf := C.calloc(C.size_t(max(total, 1)), 1)
	if buf == nil {
		return nil, fmt.Errorf("chdf5: cannot allocate %d bytes", total)
	}
	defer C.free(buf)
	if read(mem, buf) < 0 {
		return nil, h5err("read")
	}
	d := &decoder{loc: loc}
	vals, derr := d.decodeAll(buf, node, int(count))
	if C.H5Treclaim(mem, reclaimSpace, C.H5P_DEFAULT, buf) < 0 {
		rerr := h5err("H5Treclaim")
		if derr == nil {
			derr = rerr
		}
	}
	return vals, derr
}
