//go:build cgo

package chdf5

/*
#cgo pkg-config: hdf5
#include "chdf5.h"
*/
import "C"

import (
	"fmt"
	"iter"
	"runtime"
	"strings"
	"unsafe"

	"github.com/HDFGroup/hdf5/go/h5"
)

// Object is a group, dataset or committed datatype.
type Object interface {
	Path() string
	Info() (h5.ObjectInfo, error)
	Attributes() iter.Seq2[*Attribute, error]
	Attribute(name string) (*Attribute, error)
	NumAttributes() (int, error)
	Comment() (string, error)
	Close() error

	base() *object
}

// object is the part shared by groups, datasets and committed datatypes:
// an open libhdf5 object id.
type object struct {
	f       *File
	id      C.hid_t
	path    string
	closed  bool
	cleanup runtime.Cleanup
}

func (o *object) base() *object { return o }

// Path returns the path the object was opened by.
func (o *object) Path() string { return o.path }

// ID returns the libhdf5 object identifier (valid until Close).
func (o *object) ID() int64 { return int64(o.id) }

// Close releases the object id.
func (o *object) Close() error {
	mu.Lock()
	defer mu.Unlock()
	return o.closeLocked()
}

func (o *object) closeLocked() error {
	if o.closed {
		return nil
	}
	o.closed = true
	o.cleanup.Stop()
	if o.f.state.closed || C.H5Iis_valid(o.id) <= 0 {
		return nil
	}
	if C.H5Oclose(o.id) < 0 {
		return h5err("H5Oclose(%s)", o.path)
	}
	return nil
}

type idRef struct {
	f  *fileState
	id C.hid_t
}

// track registers a cleanup closing the id when owner becomes unreachable.
func track[T any](owner *T, o *object) {
	o.cleanup = runtime.AddCleanup(owner, func(r idRef) {
		mu.Lock()
		defer mu.Unlock()
		if !r.f.closed && C.H5Iis_valid(r.id) > 0 {
			C.H5Idec_ref(r.id)
		}
	}, idRef{o.f.state, o.id})
}

// Info returns the object's address, type, reference count, header
// version, number of attributes and times (H5Oget_info3 and
// H5Oget_native_info).
func (o *object) Info() (h5.ObjectInfo, error) {
	mu.Lock()
	defer mu.Unlock()
	var oi C.chdf5_oinfo
	if C.chdf5_get_oinfo(o.id, 1, &oi) < 0 {
		return h5.ObjectInfo{}, h5err("H5Oget_info3(%s)", o.path)
	}
	return h5.ObjectInfo{
		Address:       uint64(oi.addr),
		Type:          h5.ObjectType(oi._type),
		RefCount:      uint32(oi.rc),
		HeaderVersion: uint8(oi.hdr_version),
		NumAttrs:      uint64(oi.num_attrs),
		ATime:         int64(oi.atime),
		MTime:         int64(oi.mtime),
		CTime:         int64(oi.ctime),
		BTime:         int64(oi.btime),
	}, nil
}

// NumAttributes returns the number of attributes.
func (o *object) NumAttributes() (int, error) {
	mu.Lock()
	defer mu.Unlock()
	var oi C.chdf5_oinfo
	if C.chdf5_get_oinfo(o.id, 0, &oi) < 0 {
		return 0, h5err("H5Oget_info3(%s)", o.path)
	}
	return int(oi.num_attrs), nil
}

// Comment returns the object's comment ("" when none).
func (o *object) Comment() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	n := C.H5Oget_comment(o.id, nil, 0)
	if n < 0 {
		return "", h5err("H5Oget_comment(%s)", o.path)
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, int(n)+1)
	if C.H5Oget_comment(o.id, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf))) < 0 {
		return "", h5err("H5Oget_comment(%s)", o.path)
	}
	return string(buf[:n]), nil
}

// attrNames lists the attribute names in name order.
func (o *object) attrNames() ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	var l C.chdf5_names
	if C.chdf5_attr_names(o.id, &l) < 0 {
		C.chdf5_names_free(&l)
		return nil, h5err("H5Aiterate2(%s)", o.path)
	}
	return takeNames(&l), nil
}

// Attributes iterates over the attributes in name order (H5_INDEX_NAME,
// H5_ITER_INC). Each Attribute holds an open id; Close it when done (or
// leave it to the garbage collector).
func (o *object) Attributes() iter.Seq2[*Attribute, error] {
	return func(yield func(*Attribute, error) bool) {
		names, err := o.attrNames()
		if err != nil {
			yield(nil, err)
			return
		}
		for _, name := range names {
			a, err := o.Attribute(name)
			if !yield(a, err) || err != nil {
				return
			}
		}
	}
}

// Attribute opens the attribute name.
func (o *object) Attribute(name string) (*Attribute, error) {
	mu.Lock()
	defer mu.Unlock()
	cname, free := cstr(name)
	defer free()
	id := C.H5Aopen(o.id, cname, C.H5P_DEFAULT)
	if id < 0 {
		return nil, h5err("H5Aopen(%s, %q)", o.path, name)
	}
	a := &Attribute{f: o.f, id: id, name: name, parent: o.path}
	if err := a.describe(); err != nil {
		C.H5Aclose(id)
		return nil, err
	}
	a.cleanup = runtime.AddCleanup(a, func(r idRef) {
		mu.Lock()
		defer mu.Unlock()
		if !r.f.closed && C.H5Iis_valid(r.id) > 0 {
			C.H5Idec_ref(r.id)
		}
	}, idRef{o.f.state, id})
	return a, nil
}

// Group is an open group.
type Group struct{ object }

// Datatype is an open committed (named) datatype.
type Datatype struct {
	object
	dt *h5.Datatype
}

// Datatype returns the description of the committed datatype.
func (t *Datatype) Datatype() *h5.Datatype { return t.dt }

// joinPath appends name to the path of a group.
func joinPath(dir, name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	if dir == "/" || dir == "" {
		return "/" + name
	}
	return dir + "/" + name
}

// openAt opens the object name relative to loc. mu must be held.
func (f *File) openAt(loc C.hid_t, name, path string) (Object, error) {
	cname, free := cstr(name)
	defer free()
	var oi C.chdf5_oinfo
	if C.chdf5_get_oinfo_by_name(loc, cname, f.lapl, &oi) < 0 {
		return nil, h5err("H5Oget_info_by_name3(%q)", path)
	}
	var id C.hid_t
	switch h5.ObjectType(oi._type) {
	case h5.ObjectGroup:
		id = C.H5Gopen2(loc, cname, f.gapl)
	case h5.ObjectDataset:
		id = C.H5Dopen2(loc, cname, f.dapl)
	case h5.ObjectNamedDatatype:
		id = C.H5Topen2(loc, cname, f.tapl)
	default:
		id = C.H5Oopen(loc, cname, f.lapl)
	}
	if id < 0 {
		return nil, h5err("open %q", path)
	}
	return f.wrap(id, path)
}

// wrap builds the Go object for an open id. On error the id is closed.
// mu must be held.
func (f *File) wrap(id C.hid_t, path string) (Object, error) {
	switch C.H5Iget_type(id) {
	case C.H5I_GROUP:
		g := &Group{object{f: f, id: id, path: path}}
		track(g, &g.object)
		return g, nil
	case C.H5I_DATASET:
		d := &Dataset{object: object{f: f, id: id, path: path}}
		if err := d.describe(); err != nil {
			C.H5Oclose(id)
			return nil, err
		}
		track(d, &d.object)
		return d, nil
	case C.H5I_DATATYPE:
		sa, err := sizeofAddrOf(id)
		if err != nil {
			C.H5Oclose(id)
			return nil, err
		}
		dt, err := describeType(id, sa)
		if err != nil {
			C.H5Oclose(id)
			return nil, fmt.Errorf("chdf5: %s: %w", path, err)
		}
		t := &Datatype{object: object{f: f, id: id, path: path}, dt: dt}
		track(t, &t.object)
		return t, nil
	}
	C.H5Oclose(id)
	return nil, fmt.Errorf("chdf5: %s: unsupported object type", path)
}

// sizeofAddrOf returns the size of addresses in the file holding id (which
// may be a file reached through an external link). mu must be held.
func sizeofAddrOf(id C.hid_t) (uint64, error) {
	fid := C.H5Iget_file_id(id)
	if fid < 0 {
		return 0, h5err("H5Iget_file_id")
	}
	defer C.H5Fclose(fid)
	fcpl := C.H5Fget_create_plist(fid)
	if fcpl < 0 {
		return 0, h5err("H5Fget_create_plist")
	}
	defer C.H5Pclose(fcpl)
	var sa, ss C.size_t
	if C.H5Pget_sizes(fcpl, &sa, &ss) < 0 {
		return 0, h5err("H5Pget_sizes")
	}
	return uint64(sa), nil
}

// Open opens the object at a path relative to g (or absolute), following
// soft and external links.
func (g *Group) Open(name string) (Object, error) {
	mu.Lock()
	defer mu.Unlock()
	if name == "/" || name == "." {
		path := g.path
		if name == "/" {
			path = "/"
		}
		cname, free := cstr(name)
		defer free()
		id := C.H5Oopen(g.id, cname, g.f.lapl)
		if id < 0 {
			return nil, h5err("H5Oopen(%q)", name)
		}
		return g.f.wrap(id, path)
	}
	return g.f.openAt(g.id, name, joinPath(g.path, strings.TrimSuffix(name, "/")))
}

// OpenGroup opens the group at a path relative to g.
func (g *Group) OpenGroup(name string) (*Group, error) {
	o, err := g.Open(name)
	if err != nil {
		return nil, err
	}
	if x, ok := o.(*Group); ok {
		return x, nil
	}
	o.Close()
	return nil, fmt.Errorf("chdf5: %s is not a group", o.Path())
}

// OpenDataset opens the dataset at a path relative to g.
func (g *Group) OpenDataset(name string) (*Dataset, error) {
	o, err := g.Open(name)
	if err != nil {
		return nil, err
	}
	if x, ok := o.(*Dataset); ok {
		return x, nil
	}
	o.Close()
	return nil, fmt.Errorf("chdf5: %s is not a dataset", o.Path())
}

// OpenDatatype opens the committed datatype at a path relative to g.
func (g *Group) OpenDatatype(name string) (*Datatype, error) {
	o, err := g.Open(name)
	if err != nil {
		return nil, err
	}
	if x, ok := o.(*Datatype); ok {
		return x, nil
	}
	o.Close()
	return nil, fmt.Errorf("chdf5: %s is not a committed datatype", o.Path())
}

// NumLinks returns the number of links in the group (H5Gget_info).
func (g *Group) NumLinks() (int, error) {
	mu.Lock()
	defer mu.Unlock()
	var info C.H5G_info_t
	if C.H5Gget_info(g.id, &info) < 0 {
		return 0, h5err("H5Gget_info(%s)", g.path)
	}
	return int(info.nlinks), nil
}

func (g *Group) linkNames(idx C.int) ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	var l C.chdf5_names
	if C.chdf5_link_names(g.id, idx, &l) < 0 {
		C.chdf5_names_free(&l)
		return nil, h5err("H5Literate2(%s)", g.path)
	}
	return takeNames(&l), nil
}

func (g *Group) links(idx C.int) iter.Seq2[h5.Link, error] {
	return func(yield func(h5.Link, error) bool) {
		names, err := g.linkNames(idx)
		if err != nil {
			yield(h5.Link{}, err)
			return
		}
		for _, name := range names {
			l, err := g.Link(name)
			if !yield(l, err) || err != nil {
				return
			}
		}
	}
}

// Links iterates over the links of the group in name order
// (H5Literate2 with H5_INDEX_NAME, H5_ITER_INC).
func (g *Group) Links() iter.Seq2[h5.Link, error] { return g.links(C.H5_INDEX_NAME) }

// LinksByCreationOrder iterates over the links in creation order; it fails
// when the group does not track creation order.
func (g *Group) LinksByCreationOrder() iter.Seq2[h5.Link, error] {
	return func(yield func(h5.Link, error) bool) {
		mu.Lock()
		var flags C.unsigned
		gcpl := C.H5Gget_create_plist(g.id)
		var err error
		if gcpl < 0 {
			err = h5err("H5Gget_create_plist(%s)", g.path)
		} else {
			if C.H5Pget_link_creation_order(gcpl, &flags) < 0 {
				err = h5err("H5Pget_link_creation_order(%s)", g.path)
			} else if flags&C.H5P_CRT_ORDER_TRACKED == 0 {
				err = fmt.Errorf("chdf5: %s: link creation order is not tracked", g.path)
			}
			C.H5Pclose(gcpl)
		}
		mu.Unlock()
		if err != nil {
			yield(h5.Link{}, err)
			return
		}
		for l, err := range g.links(C.H5_INDEX_CRT_ORDER) {
			if !yield(l, err) || err != nil {
				return
			}
		}
	}
}

// Link describes the link name in g without following it.
func (g *Group) Link(name string) (h5.Link, error) {
	mu.Lock()
	defer mu.Unlock()
	cname, free := cstr(name)
	defer free()
	var li C.chdf5_linfo
	if C.chdf5_get_linfo(g.id, cname, g.f.lapl, &li) < 0 {
		return h5.Link{}, h5err("H5Lget_info2(%s, %q)", g.path, name)
	}
	l := h5.Link{
		Name:             name,
		Type:             h5.LinkType(li._type),
		Cset:             h5.Cset(li.cset),
		HasCreationOrder: li.corder_valid != 0,
	}
	if l.HasCreationOrder {
		l.CreationOrder = int64(li.corder)
	}
	if l.Type == h5.LinkHard {
		l.Address = uint64(li.addr)
		return l, nil
	}
	val := make([]byte, int(li.val_size)+1)
	if C.H5Lget_val(g.id, cname, unsafe.Pointer(&val[0]), C.size_t(len(val)), g.f.lapl) < 0 {
		return h5.Link{}, h5err("H5Lget_val(%s, %q)", g.path, name)
	}
	val = val[:li.val_size]
	switch l.Type {
	case h5.LinkSoft:
		s := val
		if i := indexByte(s, 0); i >= 0 {
			s = s[:i]
		}
		l.SoftPath = string(s)
	case h5.LinkExternal:
		l.UserData = append([]byte(nil), val...)
		var flags C.unsigned
		var file, path *C.char
		if len(val) > 0 && C.H5Lunpack_elink_val(unsafe.Pointer(&val[0]), C.size_t(len(val)), &flags, &file, &path) >= 0 {
			l.ExternalFile = C.GoString(file)
			l.ExternalPath = C.GoString(path)
		} else {
			C.H5Eclear2(C.H5E_DEFAULT)
		}
	default:
		l.UserData = append([]byte(nil), val...)
	}
	return l, nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Visit is one step of Walk.
type Visit struct {
	Path   string  // absolute path
	Link   h5.Link // the link reaching the object
	Object Object  // nil for soft/external/dangling links
}

// Walk visits every link below g, depth first, links in name order. Groups
// reached by hard links are descended into once: a hard link to a group
// already visited (by object address) is reported but not followed, as in
// h5ls -r. Soft and external links are reported with a nil Object and not
// followed. The objects are left open for the caller (Close them or leave
// them to the garbage collector).
func (g *Group) Walk() iter.Seq2[Visit, error] {
	return func(yield func(Visit, error) bool) {
		info, err := g.Info()
		if err != nil {
			yield(Visit{}, err)
			return
		}
		seen := map[uint64]bool{info.Address: true}
		g.walk(g.path, seen, yield)
	}
}

func (g *Group) walk(dir string, seen map[uint64]bool, yield func(Visit, error) bool) bool {
	for l, err := range g.Links() {
		if err != nil {
			return yield(Visit{}, err)
		}
		v := Visit{Path: joinPath(dir, l.Name), Link: l}
		if l.Type != h5.LinkHard {
			if !yield(v, nil) {
				return false
			}
			continue
		}
		obj, err := g.Open(l.Name)
		if err != nil {
			if !yield(v, err) {
				return false
			}
			continue
		}
		v.Object = obj
		if !yield(v, nil) {
			return false
		}
		if _, ok := obj.(*Group); !ok || seen[l.Address] {
			continue
		}
		seen[l.Address] = true
		// The yielded object belongs to the caller (who may close it):
		// descend through a handle of our own.
		sub, err := g.OpenGroup(l.Name)
		if err != nil {
			if !yield(Visit{Path: v.Path, Link: l}, err) {
				return false
			}
			continue
		}
		ok := sub.walk(v.Path, seen, yield)
		sub.Close()
		if !ok {
			return false
		}
	}
	return true
}
