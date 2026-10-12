//go:build cgo

package chdf5

/*
#cgo pkg-config: hdf5
#include "chdf5.h"
#include "H5FDfamily.h"
#include "H5FDmulti.h"
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"github.com/HDFGroup/hdf5/go/h5"
)

// File is an open HDF5 file (an H5F id opened with H5F_CLOSE_STRONG, so
// closing it closes every object still open in it).
type File struct {
	id     C.hid_t
	name   string
	driver string
	opts   options

	// Access property lists applied when opening objects: link access
	// (external link prefix / traversal), group, dataset (external file and
	// virtual prefixes) and datatype access.
	lapl, gapl, dapl, tapl C.hid_t

	root    *Group
	state   *fileState
	cleanup runtime.Cleanup
}

// fileState is shared with the cleanups of the file's objects (which must
// not reference the File itself).
type fileState struct{ closed bool }

type options struct {
	driver          string
	extLinks        bool
	extLinkPrefix   *string
	extFilePrefix   *string
	virtualPrefix   *string
	familyMemberSiz uint64
}

// Option configures Open.
type Option func(*options)

// WithExternalLinks enables or disables following external links (enabled
// by default). Disabling installs an H5Pset_elink_cb callback that refuses
// every traversal.
func WithExternalLinks(on bool) Option { return func(o *options) { o.extLinks = on } }

// WithExternalLinkPrefix sets the prefix used to locate the target files of
// external links (H5Pset_elink_prefix).
func WithExternalLinkPrefix(prefix string) Option {
	return func(o *options) { o.extLinkPrefix = &prefix }
}

// WithExternalFilePrefix sets the prefix used to locate external data files
// (H5Pset_efile_prefix).
func WithExternalFilePrefix(prefix string) Option {
	return func(o *options) { o.extFilePrefix = &prefix }
}

// WithVirtualPrefix sets the prefix used to locate the source files of
// virtual datasets (H5Pset_virtual_prefix).
func WithVirtualPrefix(prefix string) Option {
	return func(o *options) { o.virtualPrefix = &prefix }
}

// WithDriver selects a multi-file driver: "family" (name is a printf
// pattern such as "f%05d.h5", H5Pset_fapl_family), "multi" (name is the
// base name, members "<name>-s.h5", "<name>-b.h5", ...; H5Pset_fapl_multi
// with the default memory map) or "split" (members "<name>-m.h5" and
// "<name>-r.h5", H5Pset_fapl_split). "" or "sec2" selects the default
// driver.
func WithDriver(name string) Option { return func(o *options) { o.driver = name } }

// WithFamilyMemberSize sets the member size passed to H5Pset_fapl_family
// (0, the default, lets the library use the size recorded in the file).
func WithFamilyMemberSize(size uint64) Option {
	return func(o *options) { o.familyMemberSiz = size }
}

// Open opens an HDF5 file read-only (H5Fopen with H5F_ACC_RDONLY).
func Open(name string, opts ...Option) (*File, error) {
	o := options{extLinks: true}
	for _, opt := range opts {
		opt(&o)
	}
	mu.Lock()
	defer mu.Unlock()

	fapl := C.H5Pcreate(C.H5P_CLS_FILE_ACCESS_ID_g)
	if fapl < 0 {
		return nil, h5err("H5Pcreate(fapl)")
	}
	defer C.H5Pclose(fapl)
	if C.H5Pset_fclose_degree(fapl, C.H5F_CLOSE_STRONG) < 0 {
		return nil, h5err("H5Pset_fclose_degree")
	}
	if err := setDriver(fapl, &o); err != nil {
		return nil, err
	}
	f := &File{name: name, driver: o.driver, opts: o, state: &fileState{}}
	if err := f.makeAccessLists(); err != nil {
		f.closeLists()
		return nil, err
	}
	cname, free := cstr(name)
	defer free()
	f.id = C.H5Fopen(cname, C.H5F_ACC_RDONLY, fapl)
	if f.id < 0 {
		err := h5err("H5Fopen(%q)", name)
		f.closeLists()
		return nil, err
	}
	if err := f.openRoot(); err != nil {
		C.H5Fclose(f.id)
		f.closeLists()
		return nil, err
	}
	f.setCleanup()
	return f, nil
}

func setDriver(fapl C.hid_t, o *options) error {
	switch o.driver {
	case "", "sec2":
	case "family":
		if C.H5Pset_fapl_family(fapl, C.hsize_t(o.familyMemberSiz), C.H5P_DEFAULT) < 0 {
			return h5err("H5Pset_fapl_family")
		}
	case "multi":
		if C.H5Pset_fapl_multi(fapl, nil, nil, nil, nil, true) < 0 {
			return h5err("H5Pset_fapl_multi")
		}
	case "split":
		m, fm := cstr("-m.h5")
		defer fm()
		r, fr := cstr("-r.h5")
		defer fr()
		if C.H5Pset_fapl_split(fapl, m, C.H5P_DEFAULT, r, C.H5P_DEFAULT) < 0 {
			return h5err("H5Pset_fapl_split")
		}
	default:
		return fmt.Errorf("chdf5: unknown driver %q", o.driver)
	}
	return nil
}

// makeAccessLists creates the link/group/dataset/datatype access property
// lists carrying the prefixes. mu must be held.
func (f *File) makeAccessLists() error {
	o := &f.opts
	mk := func(cls C.hid_t, what string) (C.hid_t, error) {
		id := C.H5Pcreate(cls)
		if id < 0 {
			return id, h5err("H5Pcreate(%s)", what)
		}
		if o.extLinkPrefix != nil {
			p, free := cstr(*o.extLinkPrefix)
			defer free()
			if C.H5Pset_elink_prefix(id, p) < 0 {
				return id, h5err("H5Pset_elink_prefix")
			}
		}
		if !o.extLinks && C.chdf5_deny_elinks(id) < 0 {
			return id, h5err("H5Pset_elink_cb")
		}
		return id, nil
	}
	var err error
	if f.lapl, err = mk(C.H5P_CLS_LINK_ACCESS_ID_g, "lapl"); err != nil {
		return err
	}
	if f.gapl, err = mk(C.H5P_CLS_GROUP_ACCESS_ID_g, "gapl"); err != nil {
		return err
	}
	if f.tapl, err = mk(C.H5P_CLS_DATATYPE_ACCESS_ID_g, "tapl"); err != nil {
		return err
	}
	if f.dapl, err = mk(C.H5P_CLS_DATASET_ACCESS_ID_g, "dapl"); err != nil {
		return err
	}
	if o.extFilePrefix != nil {
		p, free := cstr(*o.extFilePrefix)
		defer free()
		if C.H5Pset_efile_prefix(f.dapl, p) < 0 {
			return h5err("H5Pset_efile_prefix")
		}
	}
	if o.virtualPrefix != nil {
		p, free := cstr(*o.virtualPrefix)
		defer free()
		if C.H5Pset_virtual_prefix(f.dapl, p) < 0 {
			return h5err("H5Pset_virtual_prefix")
		}
	}
	return nil
}

func (f *File) closeLists() {
	for _, id := range []*C.hid_t{&f.lapl, &f.gapl, &f.dapl, &f.tapl} {
		if *id > 0 {
			C.H5Pclose(*id)
		}
		*id = 0
	}
}

// openRoot opens "/" as f.root. mu must be held.
func (f *File) openRoot() error {
	root, err := f.openAt(f.id, "/", "/")
	if err != nil {
		return err
	}
	g, ok := root.(*Group)
	if !ok {
		return fmt.Errorf("chdf5: root object is not a group")
	}
	f.root = g
	return nil
}

func (f *File) setCleanup() {
	type fileRef struct {
		st    *fileState
		id    C.hid_t
		lists [4]C.hid_t
	}
	f.cleanup = runtime.AddCleanup(f, func(r fileRef) {
		mu.Lock()
		defer mu.Unlock()
		if r.st.closed {
			return
		}
		r.st.closed = true
		for _, id := range r.lists {
			if id > 0 {
				C.H5Pclose(id)
			}
		}
		if C.H5Iis_valid(r.id) > 0 {
			C.H5Fclose(r.id)
		}
	}, fileRef{f.state, f.id, [4]C.hid_t{f.lapl, f.gapl, f.dapl, f.tapl}})
}

// Close closes the file and every object still open in it.
func (f *File) Close() error {
	mu.Lock()
	defer mu.Unlock()
	if f.state.closed {
		return nil
	}
	f.state.closed = true
	f.cleanup.Stop()
	f.closeLists()
	if C.H5Fclose(f.id) < 0 {
		return h5err("H5Fclose(%q)", f.name)
	}
	return nil
}

// Name returns the name the file was opened with.
func (f *File) Name() string { return f.name }

// Root returns the root group.
func (f *File) Root() *Group { return f.root }

// Open opens the object at an absolute path, following soft and external
// links.
func (f *File) Open(path string) (Object, error) { return f.root.Open(path) }

// OpenGroup opens the group at path.
func (f *File) OpenGroup(path string) (*Group, error) { return f.root.OpenGroup(path) }

// OpenDataset opens the dataset at path.
func (f *File) OpenDataset(path string) (*Dataset, error) { return f.root.OpenDataset(path) }

// OpenDatatype opens the committed datatype at path.
func (f *File) OpenDatatype(path string) (*Datatype, error) { return f.root.OpenDatatype(path) }

// OpenAddress opens the object whose header is at addr (H5Oopen_by_token).
// Its Path is the name libhdf5 reports for it (H5Iget_name), "" if none.
func (f *File) OpenAddress(addr uint64) (Object, error) {
	mu.Lock()
	defer mu.Unlock()
	id := C.chdf5_open_addr(f.id, C.haddr_t(addr))
	if id < 0 {
		return nil, h5err("open object at address %d", addr)
	}
	return f.wrap(id, idName(id))
}

// idName is H5Iget_name. mu must be held.
func idName(id C.hid_t) string {
	n := C.H5Iget_name(id, nil, 0)
	if n <= 0 {
		return ""
	}
	buf := make([]byte, int(n)+1)
	C.H5Iget_name(id, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	return string(buf[:n])
}

// Superblock describes the file's superblock.
//
// Version, the address/length sizes and the B-tree K values come from
// libhdf5 (H5Fget_info2 and the file creation property list), the root
// group address from H5Oget_info3 on "/". libhdf5 has no public query for
// the base address, the superblock extension and driver info block
// addresses, the stored end-of-file address, the status flags or the
// driver id string, so those are decoded from the raw superblock bytes.
func (f *File) Superblock() h5.SuperblockInfo {
	mu.Lock()
	defer mu.Unlock()
	var sb h5.SuperblockInfo
	var info C.H5F_info2_t
	haveInfo := C.H5Fget_info2(f.id, &info) >= 0
	if haveInfo {
		sb.Version = uint8(info.super.version)
	}
	if fcpl := C.H5Fget_create_plist(f.id); fcpl >= 0 {
		var sa, ss C.size_t
		if C.H5Pget_sizes(fcpl, &sa, &ss) >= 0 {
			sb.SizeOfOffsets, sb.SizeOfLengths = uint8(sa), uint8(ss)
		}
		var ik, lk C.unsigned
		if C.H5Pget_sym_k(fcpl, &ik, &lk) >= 0 {
			sb.SymLeafK, sb.SnodeBtreeK = uint(lk), uint(ik)
		}
		if C.H5Pget_istore_k(fcpl, &ik) >= 0 {
			sb.ChunkBtreeK = uint(ik)
		}
		C.H5Pclose(fcpl)
	}
	var oi C.chdf5_oinfo
	if C.chdf5_get_oinfo(f.root.id, 0, &oi) >= 0 {
		sb.RootGroupAddress = uint64(oi.addr)
	}
	C.H5Eclear2(C.H5E_DEFAULT)
	sb.ExtensionAddress, sb.DriverInfoAddress, sb.EndOfFileAddress = haddrUndef, haddrUndef, haddrUndef
	if raw, err := readRawSuperblock(f.superblockFile(), int(sb.SizeOfOffsets)); err == nil {
		sb.BaseAddress = raw.base
		sb.ExtensionAddress = raw.ext
		sb.DriverInfoAddress = raw.drv
		sb.EndOfFileAddress = raw.eof
		sb.StatusFlags = raw.flags
		sb.DriverName = raw.driverName
		if !haveInfo {
			// H5Fget_info2 also sizes the superblock extension and free
			// space; it fails when those cannot be read.
			sb.Version = raw.version
		}
	}
	return sb
}

// superblockFile is the member file holding the superblock.
func (f *File) superblockFile() string {
	switch f.driver {
	case "family":
		return fmt.Sprintf(f.name, 0)
	case "multi":
		return f.name + "-s.h5"
	case "split":
		return f.name + "-m.h5"
	}
	return f.name
}

type rawSuper struct {
	version             uint8
	base, ext, drv, eof uint64
	flags               uint8
	driverName          string
}

// readRawSuperblock decodes the fields of the superblock libhdf5 does not
// expose (format: "Superblock" in the file format specification).
func readRawSuperblock(name string, sizeofAddr int) (*rawSuper, error) {
	fp, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer fp.Close()
	st, err := fp.Stat()
	if err != nil {
		return nil, err
	}
	sig := []byte("\211HDF\r\n\032\n")
	var at int64 = -1
	buf := make([]byte, 8)
	for off := int64(0); off+8 <= st.Size(); off = max(512, off*2) {
		if _, err := fp.ReadAt(buf, off); err != nil {
			return nil, err
		}
		if bytes.Equal(buf, sig) {
			at = off
			break
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("chdf5: no superblock signature in %s", name)
	}
	hdr := make([]byte, 256)
	n, err := fp.ReadAt(hdr, at)
	if err != nil && err != io.EOF {
		return nil, err
	}
	hdr = hdr[:n]
	addr := func(p []byte) uint64 {
		v := uint64(0)
		allOnes := true
		for i := sizeofAddr - 1; i >= 0; i-- {
			v = v<<8 | uint64(p[i])
			if p[i] != 0xff {
				allOnes = false
			}
		}
		if allOnes {
			return haddrUndef
		}
		return v
	}
	r := &rawSuper{base: uint64(at)}
	vers := hdr[8]
	r.version = vers
	var p []byte
	if vers < 2 {
		if sizeofAddr == 0 {
			sizeofAddr = int(hdr[13])
		}
		r.flags = uint8(binary.LittleEndian.Uint32(hdr[20:]))
		p = hdr[24:]
		if vers == 1 {
			p = p[4:]
		}
		if len(p) < 4*sizeofAddr {
			return nil, fmt.Errorf("chdf5: short superblock")
		}
		r.ext = addr(p[sizeofAddr:])
		r.eof = addr(p[2*sizeofAddr:])
		r.drv = addr(p[3*sizeofAddr:])
	} else {
		if sizeofAddr == 0 {
			sizeofAddr = int(hdr[9])
		}
		r.flags = hdr[11]
		p = hdr[12:]
		if len(p) < 3*sizeofAddr {
			return nil, fmt.Errorf("chdf5: short superblock")
		}
		r.ext = addr(p[sizeofAddr:])
		r.eof = addr(p[2*sizeofAddr:])
		r.drv = haddrUndef
	}
	if r.drv != haddrUndef {
		d := make([]byte, 16)
		if _, err := fp.ReadAt(d, at+int64(r.drv)); err == nil {
			r.driverName = string(d[8:16])
		}
	}
	return r, nil
}

// Libver is H5F_libver_t.
type Libver int

const (
	LibverEarliest Libver = C.H5F_LIBVER_EARLIEST
	LibverV18      Libver = C.H5F_LIBVER_V18
	LibverV110     Libver = C.H5F_LIBVER_V110
	LibverV112     Libver = C.H5F_LIBVER_V112
	LibverV114     Libver = C.H5F_LIBVER_V114
	LibverLatest   Libver = C.H5F_LIBVER_LATEST
)

type createOptions struct {
	libver            bool
	low, high         Libver
	userblock         uint64
	sizeofAddr        int
	sizeofSize        int
	symIK, symLK      int
	istoreK           int
	crtOrder          bool
	fsStrategy        int
	fsPersist         bool
	fsThreshold       uint64
	fsSet             bool
	fsPageSize        uint64
	sharedMessageMin  int
	sharedMessageSet  bool
	sharedMessageMask uint
}

// CreateOption configures Create.
type CreateOption func(*createOptions)

// WithLatestFormat is WithLibverBounds(LibverLatest, LibverLatest).
func WithLatestFormat() CreateOption { return WithLibverBounds(LibverLatest, LibverLatest) }

// WithLibverBounds sets the library version bounds (H5Pset_libver_bounds),
// which select the file format versions used.
func WithLibverBounds(low, high Libver) CreateOption {
	return func(o *createOptions) { o.libver, o.low, o.high = true, low, high }
}

// WithUserblock reserves a user block of size bytes (H5Pset_userblock).
func WithUserblock(size uint64) CreateOption { return func(o *createOptions) { o.userblock = size } }

// WithSizes sets the sizes of addresses and lengths (H5Pset_sizes).
func WithSizes(sizeofAddr, sizeofSize int) CreateOption {
	return func(o *createOptions) { o.sizeofAddr, o.sizeofSize = sizeofAddr, sizeofSize }
}

// WithSymK sets the symbol table B-tree K values (H5Pset_sym_k).
func WithSymK(ik, lk int) CreateOption { return func(o *createOptions) { o.symIK, o.symLK = ik, lk } }

// WithIstoreK sets the chunk B-tree K value (H5Pset_istore_k).
func WithIstoreK(ik int) CreateOption { return func(o *createOptions) { o.istoreK = ik } }

// WithRootCreationOrder tracks and indexes link creation order in the root
// group (H5Pset_link_creation_order on the file creation property list).
func WithRootCreationOrder() CreateOption { return func(o *createOptions) { o.crtOrder = true } }

// WithFileSpaceStrategy sets the file space strategy (H5F_fspace_strategy_t
// value), persistence and threshold (H5Pset_file_space_strategy).
func WithFileSpaceStrategy(strategy int, persist bool, threshold uint64) CreateOption {
	return func(o *createOptions) {
		o.fsSet, o.fsStrategy, o.fsPersist, o.fsThreshold = true, strategy, persist, threshold
	}
}

// WithFileSpacePageSize sets the file space page size
// (H5Pset_file_space_page_size).
func WithFileSpacePageSize(size uint64) CreateOption {
	return func(o *createOptions) { o.fsPageSize = size }
}

// WithSharedMessages enables one shared object header message index for the
// message types in mask (H5O_SHMESG_*_FLAG) with the given minimum message
// size (H5Pset_shared_mesg_nindexes / H5Pset_shared_mesg_index).
func WithSharedMessages(mask uint, minSize int) CreateOption {
	return func(o *createOptions) {
		o.sharedMessageSet, o.sharedMessageMask, o.sharedMessageMin = true, mask, minSize
	}
}

// Create creates (truncates) an HDF5 file for writing test fixtures. The
// returned File supports the reading API as well.
func Create(name string, opts ...CreateOption) (*File, error) {
	var o createOptions
	for _, opt := range opts {
		opt(&o)
	}
	mu.Lock()
	defer mu.Unlock()
	fcpl := C.H5Pcreate(C.H5P_CLS_FILE_CREATE_ID_g)
	if fcpl < 0 {
		return nil, h5err("H5Pcreate(fcpl)")
	}
	defer C.H5Pclose(fcpl)
	fapl := C.H5Pcreate(C.H5P_CLS_FILE_ACCESS_ID_g)
	if fapl < 0 {
		return nil, h5err("H5Pcreate(fapl)")
	}
	defer C.H5Pclose(fapl)
	if C.H5Pset_fclose_degree(fapl, C.H5F_CLOSE_STRONG) < 0 {
		return nil, h5err("H5Pset_fclose_degree")
	}
	if o.libver && C.H5Pset_libver_bounds(fapl, C.H5F_libver_t(o.low), C.H5F_libver_t(o.high)) < 0 {
		return nil, h5err("H5Pset_libver_bounds")
	}
	if o.userblock != 0 && C.H5Pset_userblock(fcpl, C.hsize_t(o.userblock)) < 0 {
		return nil, h5err("H5Pset_userblock")
	}
	if o.sizeofAddr != 0 && C.H5Pset_sizes(fcpl, C.size_t(o.sizeofAddr), C.size_t(o.sizeofSize)) < 0 {
		return nil, h5err("H5Pset_sizes")
	}
	if o.symIK != 0 && C.H5Pset_sym_k(fcpl, C.unsigned(o.symIK), C.unsigned(o.symLK)) < 0 {
		return nil, h5err("H5Pset_sym_k")
	}
	if o.istoreK != 0 && C.H5Pset_istore_k(fcpl, C.unsigned(o.istoreK)) < 0 {
		return nil, h5err("H5Pset_istore_k")
	}
	if o.crtOrder && C.H5Pset_link_creation_order(fcpl, C.H5P_CRT_ORDER_TRACKED|C.H5P_CRT_ORDER_INDEXED) < 0 {
		return nil, h5err("H5Pset_link_creation_order")
	}
	if o.fsSet {
		if C.H5Pset_file_space_strategy(fcpl, C.H5F_fspace_strategy_t(o.fsStrategy), C.hbool_t(o.fsPersist), C.hsize_t(o.fsThreshold)) < 0 {
			return nil, h5err("H5Pset_file_space_strategy")
		}
	}
	if o.fsPageSize != 0 && C.H5Pset_file_space_page_size(fcpl, C.hsize_t(o.fsPageSize)) < 0 {
		return nil, h5err("H5Pset_file_space_page_size")
	}
	if o.sharedMessageSet {
		if C.H5Pset_shared_mesg_nindexes(fcpl, 1) < 0 ||
			C.H5Pset_shared_mesg_index(fcpl, 0, C.unsigned(o.sharedMessageMask), C.unsigned(o.sharedMessageMin)) < 0 {
			return nil, h5err("H5Pset_shared_mesg_index")
		}
	}
	f := &File{name: name, opts: options{extLinks: true}, state: &fileState{}}
	if err := f.makeAccessLists(); err != nil {
		f.closeLists()
		return nil, err
	}
	cname, free := cstr(name)
	defer free()
	f.id = C.H5Fcreate(cname, C.H5F_ACC_TRUNC, fcpl, fapl)
	if f.id < 0 {
		err := h5err("H5Fcreate(%q)", name)
		f.closeLists()
		return nil, err
	}
	if err := f.openRoot(); err != nil {
		C.H5Fclose(f.id)
		f.closeLists()
		return nil, err
	}
	f.setCleanup()
	return f, nil
}

// CreateGroup creates the group at an absolute path, creating missing
// intermediate groups.
func (f *File) CreateGroup(path string) (*Group, error) {
	g := f.root
	path = strings.Trim(path, "/")
	return g.createGroup(path, true)
}
