//go:build cgo

package chdf5

/*
#cgo pkg-config: hdf5
#include "chdf5.h"
*/
import "C"

import (
	"fmt"
	"unicode/utf8"
	"unsafe"
)

// A small writing API used to generate test fixtures (for the pure Go
// reader). Supported element types: int8..int64, uint8..uint64, int, uint,
// float32, float64, string (fixed length = longest string, NULLPAD, unless
// WithVariableLengthStrings) and bool (stored as an enum FALSE=0/TRUE=1 of
// a native 8-bit integer, as h5py does).

// Element is the set of Go types WriteDataset and WriteAttribute accept.
type Element interface {
	int8 | int16 | int32 | int64 | int |
		uint8 | uint16 | uint32 | uint64 | uint |
		float32 | float64 | string | bool
}

type groupOptions struct {
	crtOrder              bool
	maxCompact, minDense  int
	phaseChange           bool
	estNumEntries, estLen int
}

// GroupOption configures CreateGroup.
type GroupOption func(*groupOptions)

// WithCreationOrder tracks and indexes link creation order in the new group.
func WithCreationOrder() GroupOption { return func(o *groupOptions) { o.crtOrder = true } }

// WithLinkPhaseChange sets the compact/dense thresholds of new-style groups
// (H5Pset_link_phase_change); use with WithLatestFormat.
func WithLinkPhaseChange(maxCompact, minDense int) GroupOption {
	return func(o *groupOptions) { o.phaseChange, o.maxCompact, o.minDense = true, maxCompact, minDense }
}

// CreateGroup creates a group named name in g. Missing intermediate groups
// of a path are created.
func (g *Group) CreateGroup(name string, opts ...GroupOption) (*Group, error) {
	return g.createGroup(name, true, opts...)
}

func (g *Group) createGroup(name string, intermediate bool, opts ...GroupOption) (*Group, error) {
	var o groupOptions
	for _, opt := range opts {
		opt(&o)
	}
	mu.Lock()
	defer mu.Unlock()
	lcpl := C.H5Pcreate(C.H5P_CLS_LINK_CREATE_ID_g)
	defer C.H5Pclose(lcpl)
	if intermediate {
		C.H5Pset_create_intermediate_group(lcpl, 1)
	}
	gcpl := C.H5Pcreate(C.H5P_CLS_GROUP_CREATE_ID_g)
	defer C.H5Pclose(gcpl)
	if o.crtOrder && C.H5Pset_link_creation_order(gcpl, C.H5P_CRT_ORDER_TRACKED|C.H5P_CRT_ORDER_INDEXED) < 0 {
		return nil, h5err("H5Pset_link_creation_order")
	}
	if o.phaseChange && C.H5Pset_link_phase_change(gcpl, C.unsigned(o.maxCompact), C.unsigned(o.minDense)) < 0 {
		return nil, h5err("H5Pset_link_phase_change")
	}
	cname, free := cstr(name)
	defer free()
	id := C.H5Gcreate2(g.id, cname, lcpl, gcpl, C.H5P_DEFAULT)
	if id < 0 {
		return nil, h5err("H5Gcreate2(%s, %q)", g.path, name)
	}
	o2, err := g.f.wrap(id, joinPath(g.path, name))
	if err != nil {
		return nil, err
	}
	return o2.(*Group), nil
}

// CreateSoftLink creates a soft link name -> target in g.
func (g *Group) CreateSoftLink(name, target string) error {
	mu.Lock()
	defer mu.Unlock()
	cn, f1 := cstr(name)
	defer f1()
	ct, f2 := cstr(target)
	defer f2()
	if C.H5Lcreate_soft(ct, g.id, cn, C.H5P_DEFAULT, C.H5P_DEFAULT) < 0 {
		return h5err("H5Lcreate_soft(%s, %q)", g.path, name)
	}
	return nil
}

// CreateHardLink creates a hard link name in g to the object at target
// (a path relative to g or absolute).
func (g *Group) CreateHardLink(name, target string) error {
	mu.Lock()
	defer mu.Unlock()
	cn, f1 := cstr(name)
	defer f1()
	ct, f2 := cstr(target)
	defer f2()
	if C.H5Lcreate_hard(g.id, ct, g.id, cn, C.H5P_DEFAULT, C.H5P_DEFAULT) < 0 {
		return h5err("H5Lcreate_hard(%s, %q)", g.path, name)
	}
	return nil
}

// CreateExternalLink creates an external link name in g to path in file.
func (g *Group) CreateExternalLink(name, file, path string) error {
	mu.Lock()
	defer mu.Unlock()
	cn, f1 := cstr(name)
	defer f1()
	cf, f2 := cstr(file)
	defer f2()
	cp, f3 := cstr(path)
	defer f3()
	if C.H5Lcreate_external(cf, cp, g.id, cn, C.H5P_DEFAULT, C.H5P_DEFAULT) < 0 {
		return h5err("H5Lcreate_external(%s, %q)", g.path, name)
	}
	return nil
}

type datasetOptions struct {
	chunk      []uint64
	maxDims    []uint64
	deflate    int
	shuffle    bool
	fletcher32 bool
	fill       any
	layout     int // -1: default
	varStrings bool
	allocTime  int // -1: default
}

// DatasetOption configures WriteDataset (and, for the datatype options,
// WriteAttribute).
type DatasetOption func(*datasetOptions)

// WithChunk sets chunked storage with the given chunk dimensions.
func WithChunk(dims ...uint64) DatasetOption {
	return func(o *datasetOptions) { o.chunk = append([]uint64(nil), dims...) }
}

// WithMaxDims sets the maximum dimensions (h5.Unlimited for unlimited);
// extendible datasets need WithChunk.
func WithMaxDims(dims ...uint64) DatasetOption {
	return func(o *datasetOptions) { o.maxDims = append([]uint64(nil), dims...) }
}

// WithDeflate adds the deflate filter at the given level (needs WithChunk).
func WithDeflate(level int) DatasetOption { return func(o *datasetOptions) { o.deflate = level + 1 } }

// WithShuffle adds the shuffle filter (needs WithChunk).
func WithShuffle() DatasetOption { return func(o *datasetOptions) { o.shuffle = true } }

// WithFletcher32 adds the Fletcher32 checksum filter (needs WithChunk).
func WithFletcher32() DatasetOption { return func(o *datasetOptions) { o.fletcher32 = true } }

// WithFillValue sets the fill value; v must have the dataset's element type.
func WithFillValue(v any) DatasetOption { return func(o *datasetOptions) { o.fill = v } }

// WithCompact selects compact storage.
func WithCompact() DatasetOption { return func(o *datasetOptions) { o.layout = C.H5D_COMPACT } }

// WithContiguous selects contiguous storage (the default without WithChunk).
func WithContiguous() DatasetOption { return func(o *datasetOptions) { o.layout = C.H5D_CONTIGUOUS } }

// WithAllocTime sets the space allocation time (H5D_alloc_time_t value).
func WithAllocTime(t int) DatasetOption { return func(o *datasetOptions) { o.allocTime = t } }

// WithVariableLengthStrings stores strings as variable-length strings.
func WithVariableLengthStrings() DatasetOption {
	return func(o *datasetOptions) { o.varStrings = true }
}

// memData is a memory datatype and a C buffer holding the elements.
type memData struct {
	tid  C.hid_t
	buf  unsafe.Pointer
	free []unsafe.Pointer
}

func (m *memData) release() {
	if m.tid > 0 {
		C.H5Tclose(m.tid)
	}
	for _, p := range m.free {
		C.free(p)
	}
	if m.buf != nil {
		C.free(m.buf)
	}
}

// cbuf copies the bytes of a slice of fixed-size values into C memory.
func cbuf[E any](data []E) unsafe.Pointer {
	n := uintptr(len(data)) * unsafe.Sizeof(*new(E))
	p := C.malloc(C.size_t(max(n, 1)))
	if n > 0 {
		copy(unsafe.Slice((*byte)(p), n), unsafe.Slice((*byte)(unsafe.Pointer(&data[0])), n))
	}
	return p
}

func convertSlice[E, T Element](data []T, conv func(T) E) []E {
	out := make([]E, len(data))
	for i, v := range data {
		out[i] = conv(v)
	}
	return out
}

// memDataOf builds the memory datatype and buffer for data. mu must be held.
func memDataOf[T Element](data []T, varStrings bool) (*memData, error) {
	m := &memData{}
	var zero T
	native := func(id C.hid_t) C.hid_t { return C.H5Tcopy(id) }
	switch any(zero).(type) {
	case int8:
		m.tid, m.buf = native(C.H5T_NATIVE_SCHAR_g), cbuf(data)
	case int16:
		m.tid, m.buf = native(C.H5T_NATIVE_SHORT_g), cbuf(data)
	case int32:
		m.tid, m.buf = native(C.H5T_NATIVE_INT_g), cbuf(data)
	case int64, int:
		m.tid, m.buf = native(C.H5T_NATIVE_LLONG_g), cbuf(convertSlice(data, func(v T) int64 { return int64(reflectInt(v)) }))
	case uint8:
		m.tid, m.buf = native(C.H5T_NATIVE_UCHAR_g), cbuf(data)
	case uint16:
		m.tid, m.buf = native(C.H5T_NATIVE_USHORT_g), cbuf(data)
	case uint32:
		m.tid, m.buf = native(C.H5T_NATIVE_UINT_g), cbuf(data)
	case uint64, uint:
		m.tid, m.buf = native(C.H5T_NATIVE_ULLONG_g), cbuf(convertSlice(data, func(v T) uint64 { return reflectUint(v) }))
	case float32:
		m.tid, m.buf = native(C.H5T_NATIVE_FLOAT_g), cbuf(data)
	case float64:
		m.tid, m.buf = native(C.H5T_NATIVE_DOUBLE_g), cbuf(data)
	case bool:
		m.tid = C.H5Tenum_create(C.H5T_NATIVE_SCHAR_g)
		if m.tid >= 0 {
			for i, name := range []string{"FALSE", "TRUE"} {
				v := C.schar(i)
				cn, free := cstr(name)
				rc := C.H5Tenum_insert(m.tid, cn, unsafe.Pointer(&v))
				free()
				if rc < 0 {
					m.release()
					return nil, h5err("H5Tenum_insert")
				}
			}
		}
		m.buf = cbuf(convertSlice(data, func(v T) int8 {
			if any(v).(bool) {
				return 1
			}
			return 0
		}))
	case string:
		strs := make([]string, len(data))
		longest, ascii := 1, true
		for i, v := range data {
			s := any(v).(string)
			strs[i] = s
			longest = max(longest, len(s))
			for j := 0; j < len(s); j++ {
				if s[j] >= utf8.RuneSelf {
					ascii = false
				}
			}
		}
		m.tid = C.H5Tcopy(C.H5T_C_S1_g)
		if m.tid < 0 {
			return nil, h5err("H5Tcopy")
		}
		cset := C.H5T_cset_t(C.H5T_CSET_UTF8)
		if ascii {
			cset = C.H5T_CSET_ASCII
		}
		C.H5Tset_cset(m.tid, cset)
		if varStrings {
			C.H5Tset_size(m.tid, C.H5T_VARIABLE)
			ptrs := C.malloc(C.size_t(max(len(strs), 1)) * C.size_t(unsafe.Sizeof(uintptr(0))))
			for i, s := range strs {
				cs := unsafe.Pointer(C.CString(s))
				m.free = append(m.free, cs)
				C.chdf5_put_ptr(unsafe.Add(ptrs, uintptr(i)*unsafe.Sizeof(uintptr(0))), cs)
			}
			m.buf = ptrs
		} else {
			C.H5Tset_size(m.tid, C.size_t(longest))
			C.H5Tset_strpad(m.tid, C.H5T_STR_NULLPAD)
			b := make([]byte, longest*len(strs))
			for i, s := range strs {
				copy(b[i*longest:], s)
			}
			m.buf = cbuf(b)
		}
	default:
		return nil, fmt.Errorf("chdf5: unsupported element type %T", zero)
	}
	if m.tid < 0 {
		m.tid = 0
		m.release()
		return nil, h5err("memory datatype")
	}
	return m, nil
}

func reflectInt[T Element](v T) int64 {
	switch x := any(v).(type) {
	case int:
		return int64(x)
	case int64:
		return x
	}
	return 0
}

func reflectUint[T Element](v T) uint64 {
	switch x := any(v).(type) {
	case uint:
		return uint64(x)
	case uint64:
		return x
	}
	return 0
}

// createSpace creates a scalar (len(dims) == 0) or simple dataspace. mu
// must be held.
func createSpace(dims, maxDims []uint64, n int) (C.hid_t, error) {
	want := uint64(1)
	for _, d := range dims {
		want *= d
	}
	if want != uint64(n) {
		return -1, fmt.Errorf("chdf5: %d values for dimensions %v", n, dims)
	}
	if len(dims) == 0 {
		id := C.H5Screate(C.H5S_SCALAR)
		if id < 0 {
			return -1, h5err("H5Screate")
		}
		return id, nil
	}
	cd := make([]C.hsize_t, len(dims))
	for i, d := range dims {
		cd[i] = C.hsize_t(d)
	}
	var cm *C.hsize_t
	if maxDims != nil {
		if len(maxDims) != len(dims) {
			return -1, fmt.Errorf("chdf5: max dims %v do not match dims %v", maxDims, dims)
		}
		m := make([]C.hsize_t, len(maxDims))
		for i, d := range maxDims {
			m[i] = C.hsize_t(d)
		}
		cm = &m[0]
	}
	id := C.H5Screate_simple(C.int(len(dims)), &cd[0], cm)
	if id < 0 {
		return -1, h5err("H5Screate_simple")
	}
	return id, nil
}

// WriteDataset creates the dataset name in g with the given dimensions
// (nil for a scalar) and writes data (row-major) to it.
func WriteDataset[T Element](g *Group, name string, dims []uint64, data []T, opts ...DatasetOption) (*Dataset, error) {
	o := datasetOptions{layout: -1, allocTime: -1}
	for _, opt := range opts {
		opt(&o)
	}
	mu.Lock()
	defer mu.Unlock()
	m, err := memDataOf(data, o.varStrings)
	if err != nil {
		return nil, err
	}
	defer m.release()
	space, err := createSpace(dims, o.maxDims, len(data))
	if err != nil {
		return nil, err
	}
	defer C.H5Sclose(space)
	dcpl := C.H5Pcreate(C.H5P_CLS_DATASET_CREATE_ID_g)
	if dcpl < 0 {
		return nil, h5err("H5Pcreate(dcpl)")
	}
	defer C.H5Pclose(dcpl)
	if o.chunk != nil {
		cd := make([]C.hsize_t, len(o.chunk))
		for i, d := range o.chunk {
			cd[i] = C.hsize_t(d)
		}
		if C.H5Pset_chunk(dcpl, C.int(len(cd)), &cd[0]) < 0 {
			return nil, h5err("H5Pset_chunk")
		}
	}
	if o.layout >= 0 && C.H5Pset_layout(dcpl, C.H5D_layout_t(o.layout)) < 0 {
		return nil, h5err("H5Pset_layout")
	}
	if o.allocTime >= 0 && C.H5Pset_alloc_time(dcpl, C.H5D_alloc_time_t(o.allocTime)) < 0 {
		return nil, h5err("H5Pset_alloc_time")
	}
	if o.fletcher32 && C.H5Pset_fletcher32(dcpl) < 0 {
		return nil, h5err("H5Pset_fletcher32")
	}
	if o.shuffle && C.H5Pset_shuffle(dcpl) < 0 {
		return nil, h5err("H5Pset_shuffle")
	}
	if o.deflate > 0 && C.H5Pset_deflate(dcpl, C.unsigned(o.deflate-1)) < 0 {
		return nil, h5err("H5Pset_deflate")
	}
	if o.fill != nil {
		v, ok := o.fill.(T)
		if !ok {
			return nil, fmt.Errorf("chdf5: fill value %T does not match element type %T", o.fill, *new(T))
		}
		fm, err := memDataOf([]T{v}, o.varStrings)
		if err != nil {
			return nil, err
		}
		defer fm.release()
		if C.H5Pset_fill_value(dcpl, fm.tid, fm.buf) < 0 {
			return nil, h5err("H5Pset_fill_value")
		}
	}
	cname, free := cstr(name)
	defer free()
	id := C.H5Dcreate2(g.id, cname, m.tid, space, C.H5P_DEFAULT, dcpl, C.H5P_DEFAULT)
	if id < 0 {
		return nil, h5err("H5Dcreate2(%s, %q)", g.path, name)
	}
	if len(data) > 0 && C.H5Dwrite(id, m.tid, C.H5S_ALL, C.H5S_ALL, C.H5P_DEFAULT, m.buf) < 0 {
		err := h5err("H5Dwrite(%s, %q)", g.path, name)
		C.H5Dclose(id)
		return nil, err
	}
	obj, err := g.f.wrap(id, joinPath(g.path, name))
	if err != nil {
		return nil, err
	}
	return obj.(*Dataset), nil
}

// WriteAttribute creates the attribute name on obj with the given
// dimensions (nil for a scalar) and writes data to it. Of the
// DatasetOptions only WithVariableLengthStrings applies.
func WriteAttribute[T Element](obj Object, name string, dims []uint64, data []T, opts ...DatasetOption) error {
	var o datasetOptions
	for _, opt := range opts {
		opt(&o)
	}
	mu.Lock()
	defer mu.Unlock()
	m, err := memDataOf(data, o.varStrings)
	if err != nil {
		return err
	}
	defer m.release()
	space, err := createSpace(dims, nil, len(data))
	if err != nil {
		return err
	}
	defer C.H5Sclose(space)
	b := obj.base()
	cname, free := cstr(name)
	defer free()
	id := C.H5Acreate2(b.id, cname, m.tid, space, C.H5P_DEFAULT, C.H5P_DEFAULT)
	if id < 0 {
		return h5err("H5Acreate2(%s, %q)", b.path, name)
	}
	defer C.H5Aclose(id)
	if C.H5Awrite(id, m.tid, m.buf) < 0 {
		return h5err("H5Awrite(%s, %q)", b.path, name)
	}
	return nil
}

// SetComment sets the comment of obj (H5Oset_comment).
func SetComment(obj Object, comment string) error {
	mu.Lock()
	defer mu.Unlock()
	b := obj.base()
	cc, free := cstr(comment)
	defer free()
	if C.H5Oset_comment(b.id, cc) < 0 {
		return h5err("H5Oset_comment(%s)", b.path)
	}
	return nil
}

// Flush flushes the file to disk (H5Fflush).
func (f *File) Flush() error {
	mu.Lock()
	defer mu.Unlock()
	if C.H5Fflush(f.id, C.H5F_SCOPE_GLOBAL) < 0 {
		return h5err("H5Fflush(%s)", f.name)
	}
	return nil
}
