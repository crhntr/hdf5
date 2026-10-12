//go:build cgo

package chdf5

/*
#cgo pkg-config: hdf5
#include "chdf5.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/HDFGroup/hdf5/go/h5"
)

// Dataset is an open dataset.
type Dataset struct {
	object
	dt    *h5.Datatype
	space h5.Dataspace
}

// describe fills the datatype and dataspace descriptions. mu must be held.
func (d *Dataset) describe() error {
	sa, err := sizeofAddrOf(d.id)
	if err != nil {
		return err
	}
	tid := C.H5Dget_type(d.id)
	if tid < 0 {
		return h5err("H5Dget_type(%s)", d.path)
	}
	defer C.H5Tclose(tid)
	if d.dt, err = describeType(tid, sa); err != nil {
		return fmt.Errorf("chdf5: %s: %w", d.path, err)
	}
	sid := C.H5Dget_space(d.id)
	if sid < 0 {
		return h5err("H5Dget_space(%s)", d.path)
	}
	defer C.H5Sclose(sid)
	d.space, err = spaceOf(sid)
	return err
}

// Datatype describes the dataset's datatype.
func (d *Dataset) Datatype() *h5.Datatype { return d.dt }

// Dataspace describes the dataset's dataspace.
func (d *Dataset) Dataspace() h5.Dataspace { return d.space }

// spaceOf describes a dataspace extent. The extent version and whether
// maximum dimensions were stored come from the H5Sencode2 serialization
// (the dataspace message). mu must be held.
func spaceOf(sid C.hid_t) (h5.Dataspace, error) {
	cls := C.H5Sget_simple_extent_type(sid)
	if cls < 0 {
		return h5.Dataspace{}, h5err("H5Sget_simple_extent_type")
	}
	ds := h5.Dataspace{Class: h5.SpaceClass(cls)}
	var n C.size_t
	if C.H5Sencode2(sid, nil, &n, C.H5P_DEFAULT) < 0 {
		return h5.Dataspace{}, h5err("H5Sencode2")
	}
	hasMax := false
	if n >= 10 {
		buf := C.malloc(n)
		defer C.free(buf)
		if C.H5Sencode2(sid, buf, &n, C.H5P_DEFAULT) < 0 {
			return h5.Dataspace{}, h5err("H5Sencode2")
		}
		// id, encode version, sizeof size, 4-byte extent size, then the
		// message: version, rank, flags.
		b := unsafe.Slice((*byte)(buf), 10)
		ds.Version = b[7]
		hasMax = b[9]&1 != 0
	}
	if ds.Class != h5.SpaceSimple {
		return ds, nil
	}
	rank := int(C.H5Sget_simple_extent_ndims(sid))
	if rank < 0 {
		return h5.Dataspace{}, h5err("H5Sget_simple_extent_ndims")
	}
	dims := make([]C.hsize_t, max(rank, 1))
	maxd := make([]C.hsize_t, max(rank, 1))
	if C.H5Sget_simple_extent_dims(sid, &dims[0], &maxd[0]) < 0 {
		return h5.Dataspace{}, h5err("H5Sget_simple_extent_dims")
	}
	ds.Dims = hsizes(dims[:rank])
	if hasMax {
		ds.MaxDims = hsizes(maxd[:rank])
	}
	return ds, nil
}

// withDcpl calls fn with the dataset creation property list.
func (d *Dataset) withDcpl(fn func(dcpl C.hid_t) error) error {
	mu.Lock()
	defer mu.Unlock()
	dcpl := C.H5Dget_create_plist(d.id)
	if dcpl < 0 {
		return h5err("H5Dget_create_plist(%s)", d.path)
	}
	defer C.H5Pclose(dcpl)
	return fn(dcpl)
}

// Layout describes the storage layout. Version and ChunkIndex are not
// available through the libhdf5 API and are left zero.
func (d *Dataset) Layout() (h5.Layout, error) {
	var l h5.Layout
	err := d.withDcpl(func(dcpl C.hid_t) error {
		lay := C.H5Pget_layout(dcpl)
		if lay < 0 {
			return h5err("H5Pget_layout(%s)", d.path)
		}
		l.Class = h5.LayoutClass(lay)
		switch lay {
		case C.H5D_CHUNKED:
			dims := make([]C.hsize_t, 32)
			nd := C.H5Pget_chunk(dcpl, 32, &dims[0])
			if nd < 0 {
				return h5err("H5Pget_chunk(%s)", d.path)
			}
			l.ChunkDims = hsizes(dims[:nd])
		case C.H5D_CONTIGUOUS:
			addr := uint64(C.H5Dget_offset(d.id))
			C.H5Eclear2(C.H5E_DEFAULT)
			l.Address = addr
			l.AddressDefined = addr != haddrUndef
			if l.AddressDefined {
				l.Size = uint64(C.H5Dget_storage_size(d.id))
			} else {
				l.Size = d.space.NumElems() * d.dt.Size
			}
			nx := C.H5Pget_external_count(dcpl)
			if nx < 0 {
				return h5err("H5Pget_external_count(%s)", d.path)
			}
			for i := 0; i < int(nx); i++ {
				name := make([]byte, 4096)
				var off C.off_t
				var size C.hsize_t
				if C.H5Pget_external(dcpl, C.unsigned(i), C.size_t(len(name)), (*C.char)(unsafe.Pointer(&name[0])), &off, &size) < 0 {
					return h5err("H5Pget_external(%s)", d.path)
				}
				l.External = append(l.External, h5.ExternalFile{
					Name: C.GoString((*C.char)(unsafe.Pointer(&name[0]))), Offset: uint64(off), Size: uint64(size),
				})
			}
		case C.H5D_COMPACT:
			l.CompactSize = uint64(C.H5Dget_storage_size(d.id))
		case C.H5D_VIRTUAL:
			var n C.size_t
			if C.H5Pget_virtual_count(dcpl, &n) < 0 {
				return h5err("H5Pget_virtual_count(%s)", d.path)
			}
			for i := C.size_t(0); i < n; i++ {
				m, err := virtualMapping(dcpl, i)
				if err != nil {
					return err
				}
				l.Virtual = append(l.Virtual, m)
			}
		}
		return nil
	})
	return l, err
}

func virtualMapping(dcpl C.hid_t, i C.size_t) (h5.VirtualMapping, error) {
	var m h5.VirtualMapping
	str := func(get func(buf *C.char, n C.size_t) C.ssize_t) (string, error) {
		n := get(nil, 0)
		if n < 0 {
			return "", h5err("H5Pget_virtual_*name")
		}
		buf := make([]byte, int(n)+1)
		if get((*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf))) < 0 {
			return "", h5err("H5Pget_virtual_*name")
		}
		return string(buf[:n]), nil
	}
	var err error
	if m.SourceFile, err = str(func(b *C.char, n C.size_t) C.ssize_t { return C.H5Pget_virtual_filename(dcpl, i, b, n) }); err != nil {
		return m, err
	}
	if m.SourceDataset, err = str(func(b *C.char, n C.size_t) C.ssize_t { return C.H5Pget_virtual_dsetname(dcpl, i, b, n) }); err != nil {
		return m, err
	}
	vs := C.H5Pget_virtual_vspace(dcpl, i)
	if vs < 0 {
		return m, h5err("H5Pget_virtual_vspace")
	}
	defer C.H5Sclose(vs)
	if m.VirtualSelection, err = selectionOf(vs); err != nil {
		return m, err
	}
	ss := C.H5Pget_virtual_srcspace(dcpl, i)
	if ss < 0 {
		return m, h5err("H5Pget_virtual_srcspace")
	}
	defer C.H5Sclose(ss)
	m.SourceSelection, err = selectionOf(ss)
	return m, err
}

// Filters lists the filter pipeline (H5Pget_filter2). For filters stored
// without a name libhdf5 reports the registered filter's name.
func (d *Dataset) Filters() ([]h5.Filter, error) {
	var out []h5.Filter
	err := d.withDcpl(func(dcpl C.hid_t) error {
		n := C.H5Pget_nfilters(dcpl)
		if n < 0 {
			return h5err("H5Pget_nfilters(%s)", d.path)
		}
		for i := 0; i < int(n); i++ {
			var flags, config C.unsigned
			cd := make([]C.unsigned, 64)
			nelmts := C.size_t(len(cd))
			name := make([]byte, 1024)
			id := C.H5Pget_filter2(dcpl, C.unsigned(i), &flags, &nelmts, &cd[0], C.size_t(len(name)), (*C.char)(unsafe.Pointer(&name[0])), &config)
			if id < 0 {
				return h5err("H5Pget_filter2(%s)", d.path)
			}
			if int(nelmts) > len(cd) {
				cd = make([]C.unsigned, nelmts)
				id = C.H5Pget_filter2(dcpl, C.unsigned(i), &flags, &nelmts, &cd[0], C.size_t(len(name)), (*C.char)(unsafe.Pointer(&name[0])), &config)
				if id < 0 {
					return h5err("H5Pget_filter2(%s)", d.path)
				}
			}
			f := h5.Filter{ID: h5.FilterID(id), Name: C.GoString((*C.char)(unsafe.Pointer(&name[0]))), Flags: uint16(flags)}
			if nelmts > 0 {
				f.CDValues = make([]uint32, nelmts)
				for j := range f.CDValues {
					f.CDValues[j] = uint32(cd[j])
				}
			}
			out = append(out, f)
		}
		return nil
	})
	return out, err
}

// FillValue describes the fill value property. Version is not available
// through the libhdf5 API and is left zero. Value holds the raw fill value
// in the dataset's datatype (H5Pget_fill_value) for datatypes without
// variable-length or reference parts; it is nil otherwise.
func (d *Dataset) FillValue() (h5.FillValueInfo, error) {
	var fv h5.FillValueInfo
	err := d.withDcpl(func(dcpl C.hid_t) error {
		var at C.H5D_alloc_time_t
		if C.H5Pget_alloc_time(dcpl, &at) < 0 {
			return h5err("H5Pget_alloc_time(%s)", d.path)
		}
		var ft C.H5D_fill_time_t
		if C.H5Pget_fill_time(dcpl, &ft) < 0 {
			return h5err("H5Pget_fill_time(%s)", d.path)
		}
		var st C.H5D_fill_value_t
		if C.H5Pfill_value_defined(dcpl, &st) < 0 {
			return h5err("H5Pfill_value_defined(%s)", d.path)
		}
		fv.AllocTime, fv.FillTime = uint8(at), uint8(ft)
		fv.Defined = st == C.H5D_FILL_VALUE_USER_DEFINED
		fv.Undefined = st == C.H5D_FILL_VALUE_UNDEFINED
		if !fv.Defined || hasVarParts(d.dt) {
			return nil
		}
		tid := C.H5Dget_type(d.id)
		if tid < 0 {
			return h5err("H5Dget_type(%s)", d.path)
		}
		defer C.H5Tclose(tid)
		size := C.H5Tget_size(tid)
		buf := C.calloc(size, 1)
		defer C.free(buf)
		if C.H5Pget_fill_value(dcpl, tid, buf) < 0 {
			return h5err("H5Pget_fill_value(%s)", d.path)
		}
		fv.Value = C.GoBytes(buf, C.int(size))
		return nil
	})
	return fv, err
}

// hasVarParts reports whether dt contains variable-length or reference
// types (whose memory form differs from the file form).
func hasVarParts(dt *h5.Datatype) bool {
	switch dt.Class {
	case h5.ClassVlen, h5.ClassReference:
		return true
	case h5.ClassCompound:
		for _, m := range dt.Members {
			if hasVarParts(m.Type) {
				return true
			}
		}
	case h5.ClassArray:
		return hasVarParts(dt.Base)
	}
	return false
}

// ReadValues reads the whole dataset as canonical values (h5/value.go) in
// row-major order.
func (d *Dataset) ReadValues() ([]any, error) {
	mu.Lock()
	defer mu.Unlock()
	return d.readLocked(nil)
}

// ReadHyperslab reads the elements selected by sel, in row-major order.
func (d *Dataset) ReadHyperslab(sel h5.Hyperslab) ([]any, error) {
	mu.Lock()
	defer mu.Unlock()
	return d.readLocked(&sel)
}

func (d *Dataset) readLocked(sel *h5.Hyperslab) ([]any, error) {
	tid := C.H5Dget_type(d.id)
	if tid < 0 {
		return nil, h5err("H5Dget_type(%s)", d.path)
	}
	defer C.H5Tclose(tid)
	fspace := C.H5Dget_space(d.id)
	if fspace < 0 {
		return nil, h5err("H5Dget_space(%s)", d.path)
	}
	defer C.H5Sclose(fspace)
	if sel == nil {
		count := d.space.NumElems()
		return readValues(d.id, tid, d.dt, count, fspace, func(mem C.hid_t, buf unsafe.Pointer) C.herr_t {
			return C.H5Dread(d.id, mem, C.H5S_ALL, C.H5S_ALL, C.H5P_DEFAULT, buf)
		})
	}
	rank := len(d.space.Dims)
	if d.space.Class != h5.SpaceSimple || len(sel.Start) != rank || len(sel.Count) != rank ||
		(sel.Stride != nil && len(sel.Stride) != rank) || (sel.Block != nil && len(sel.Block) != rank) {
		return nil, fmt.Errorf("chdf5: %s: hyperslab rank does not match dataspace rank %d", d.path, rank)
	}
	cs := func(v []uint64) *C.hsize_t {
		if v == nil {
			return nil
		}
		c := (*C.hsize_t)(C.malloc(C.size_t(len(v)) * C.size_t(unsafe.Sizeof(C.hsize_t(0)))))
		copy(unsafe.Slice(c, len(v)), unsafe.Slice((*C.hsize_t)(unsafe.Pointer(&v[0])), len(v)))
		return c
	}
	start, stride, count, block := cs(sel.Start), cs(sel.Stride), cs(sel.Count), cs(sel.Block)
	for _, p := range []*C.hsize_t{start, stride, count, block} {
		if p != nil {
			defer C.free(unsafe.Pointer(p))
		}
	}
	if C.H5Sselect_hyperslab(fspace, C.H5S_SELECT_SET, start, stride, count, block) < 0 {
		return nil, h5err("H5Sselect_hyperslab(%s)", d.path)
	}
	if C.H5Sselect_valid(fspace) <= 0 {
		C.H5Eclear2(C.H5E_DEFAULT)
		return nil, fmt.Errorf("chdf5: %s: hyperslab selection out of bounds", d.path)
	}
	np := C.H5Sget_select_npoints(fspace)
	if np < 0 {
		return nil, h5err("H5Sget_select_npoints")
	}
	if np == 0 {
		return []any{}, nil
	}
	n := C.hsize_t(np)
	mspace := C.H5Screate_simple(1, &n, nil)
	if mspace < 0 {
		return nil, h5err("H5Screate_simple")
	}
	defer C.H5Sclose(mspace)
	return readValues(d.id, tid, d.dt, uint64(np), mspace, func(mem C.hid_t, buf unsafe.Pointer) C.herr_t {
		return C.H5Dread(d.id, mem, mspace, fspace, C.H5P_DEFAULT, buf)
	})
}

// ReadInto reads the dataset into dst, a pointer to a slice of a supported
// Go type (see h5.ConvertInto).
func (d *Dataset) ReadInto(dst any) error {
	vals, err := d.ReadValues()
	if err != nil {
		return err
	}
	return h5.ConvertInto(vals, dst)
}

// Read reads the whole dataset as a []T (see h5.ConvertValues).
func Read[T any](d *Dataset) ([]T, error) {
	vals, err := d.ReadValues()
	if err != nil {
		return nil, err
	}
	return h5.ConvertValues[T](vals)
}

// Attribute is an open attribute.
type Attribute struct {
	f       *File
	id      C.hid_t
	name    string
	parent  string
	dt      *h5.Datatype
	space   h5.Dataspace
	closed  bool
	cleanup runtime.Cleanup
}

// describe fills the datatype and dataspace descriptions. mu must be held.
func (a *Attribute) describe() error {
	sa, err := sizeofAddrOf(a.id)
	if err != nil {
		return err
	}
	tid := C.H5Aget_type(a.id)
	if tid < 0 {
		return h5err("H5Aget_type(%s@%s)", a.parent, a.name)
	}
	defer C.H5Tclose(tid)
	if a.dt, err = describeType(tid, sa); err != nil {
		return fmt.Errorf("chdf5: %s@%s: %w", a.parent, a.name, err)
	}
	sid := C.H5Aget_space(a.id)
	if sid < 0 {
		return h5err("H5Aget_space(%s@%s)", a.parent, a.name)
	}
	defer C.H5Sclose(sid)
	a.space, err = spaceOf(sid)
	return err
}

// Name returns the attribute name.
func (a *Attribute) Name() string { return a.name }

// Datatype describes the attribute's datatype.
func (a *Attribute) Datatype() *h5.Datatype { return a.dt }

// Dataspace describes the attribute's dataspace.
func (a *Attribute) Dataspace() h5.Dataspace { return a.space }

// Close releases the attribute id.
func (a *Attribute) Close() error {
	mu.Lock()
	defer mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	a.cleanup.Stop()
	if a.f.state.closed || C.H5Iis_valid(a.id) <= 0 {
		return nil
	}
	if C.H5Aclose(a.id) < 0 {
		return h5err("H5Aclose(%s@%s)", a.parent, a.name)
	}
	return nil
}

// ReadValues reads the attribute as canonical values.
func (a *Attribute) ReadValues() ([]any, error) {
	mu.Lock()
	defer mu.Unlock()
	tid := C.H5Aget_type(a.id)
	if tid < 0 {
		return nil, h5err("H5Aget_type(%s@%s)", a.parent, a.name)
	}
	defer C.H5Tclose(tid)
	sid := C.H5Aget_space(a.id)
	if sid < 0 {
		return nil, h5err("H5Aget_space(%s@%s)", a.parent, a.name)
	}
	defer C.H5Sclose(sid)
	return readValues(a.id, tid, a.dt, a.space.NumElems(), sid, func(mem C.hid_t, buf unsafe.Pointer) C.herr_t {
		return C.H5Aread(a.id, mem, buf)
	})
}

// ReadInto reads the attribute into dst, a pointer to a slice.
func (a *Attribute) ReadInto(dst any) error {
	vals, err := a.ReadValues()
	if err != nil {
		return err
	}
	return h5.ConvertInto(vals, dst)
}

// ReadAttribute reads the attribute as a []T.
func ReadAttribute[T any](a *Attribute) ([]T, error) {
	vals, err := a.ReadValues()
	if err != nil {
		return nil, err
	}
	return h5.ConvertValues[T](vals)
}

// FilterAvailable reports whether libhdf5 can apply the filter
// (H5Zfilter_avail; plugins are loaded on demand).
func FilterAvailable(id h5.FilterID) bool {
	mu.Lock()
	defer mu.Unlock()
	r := C.H5Zfilter_avail(C.H5Z_filter_t(id))
	C.H5Eclear2(C.H5E_DEFAULT)
	return r > 0
}
