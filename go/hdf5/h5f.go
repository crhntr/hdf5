package hdf5

// File-level state and raw I/O. Translated (loosely) from src/H5F.c,
// src/H5Fint.c and src/H5Fsuper.c: H5F_t / H5F_shared_t become File, the
// virtual file driver (H5FD) becomes the fileDriver interface.

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

// fileDriver is the subset of H5FD used for reading: positional reads and
// the end-of-file address. Addresses passed to ReadAt are absolute file
// offsets (the File adds the base address before calling).
type fileDriver interface {
	io.ReaderAt
	// EOF is H5FD_get_eof: the size of the underlying file(s).
	EOF() uint64
	// Close releases the underlying file(s).
	Close() error
}

// sec2Driver is the H5FD_sec2 driver: a single POSIX file.
type sec2Driver struct {
	f    *os.File
	size uint64
}

func (d *sec2Driver) ReadAt(b []byte, off int64) (int, error) { return d.f.ReadAt(b, off) }
func (d *sec2Driver) EOF() uint64                             { return d.size }
func (d *sec2Driver) Close() error                            { return d.f.Close() }

// readerAtDriver wraps an arbitrary io.ReaderAt (the "core" / file-image
// case).
type readerAtDriver struct {
	r    io.ReaderAt
	size uint64
}

func (d *readerAtDriver) ReadAt(b []byte, off int64) (int, error) { return d.r.ReadAt(b, off) }
func (d *readerAtDriver) EOF() uint64                             { return d.size }
func (d *readerAtDriver) Close() error {
	if c, ok := d.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// File is an open HDF5 file. It corresponds to H5F_t + H5F_shared_t in the
// C library: it owns the file driver, the decoded superblock, the sizes of
// addresses and lengths, and a cache of decoded metadata (the role of H5AC).
type File struct {
	name string // path as given to Open ("" for OpenReader)
	dir  string // directory of name, for resolving external links/files
	fd   fileDriver

	// From the superblock.
	sizeofAddr uint8 // H5F_SIZEOF_ADDR
	sizeofSize uint8 // H5F_SIZEOF_SIZE
	baseAddr   haddr // absolute offset of HDF5 address 0 (location of the superblock)
	sblock     *superblock
	symLeafK   uint    // H5F_SYM_LEAF_K
	btreeK     [2]uint // H5F_KVALUE for H5B_SNODE_ID (0) and H5B_CHUNK_ID (1)
	drvinfo    *drvinfo

	// Shared object header message table (set by h5sm.go when the superblock
	// extension carries a shared message table message); nil otherwise.
	sohm any

	// Options.
	opts openOptions

	// Metadata cache (H5AC). Keyed by (kind, address); values are the decoded
	// in-core structures (object headers, heaps, B-tree nodes, ...).
	mu    sync.Mutex
	cache map[cacheKey]any

	// Open external files (external links, VDS sources, EFL): keyed by the
	// resolved path. Closed with the file.
	extFiles map[string]*File

	closed bool
}

type cacheKey struct {
	kind string
	addr haddr
}

// cached returns the cached value for (kind, addr), loading it with load on
// a miss. It is the Go counterpart of H5AC_protect for read-only use.
func (f *File) cached(kind string, addr haddr, load func() (any, error)) (any, error) {
	f.mu.Lock()
	v, ok := f.cache[cacheKey{kind, addr}]
	f.mu.Unlock()
	if ok {
		return v, nil
	}
	v, err := load()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.cache == nil {
		f.cache = make(map[cacheKey]any)
	}
	f.cache[cacheKey{kind, addr}] = v
	f.mu.Unlock()
	return v, nil
}

// openOptions are the read-side file access properties (a tiny subset of
// H5P FAPL).
type openOptions struct {
	// Verify metadata checksums (H5F_ACS_METADATA_READ_ATTEMPTS / default
	// behaviour of the library is to verify).
	verifyChecksums bool
	// Follow external links by opening the target files.
	followExternalLinks bool
	// Prefix used to locate external link target files (HDF5_EXT_PREFIX).
	extLinkPrefix string
	// Prefix used to locate external data files (HDF5_EXTFILE_PREFIX).
	extFilePrefix string
	// Prefix used to locate virtual dataset source files (HDF5_VDS_PREFIX).
	vdsPrefix string
	// Family driver member size override (0: use the driver info block).
	familyMemberSize uint64
}

func defaultOpenOptions() openOptions {
	return openOptions{
		verifyChecksums:     true,
		followExternalLinks: true,
		extLinkPrefix:       os.Getenv("HDF5_EXT_PREFIX"),
		extFilePrefix:       os.Getenv("HDF5_EXTFILE_PREFIX"),
		vdsPrefix:           os.Getenv("HDF5_VDS_PREFIX"),
	}
}

// Option configures Open.
type Option func(*openOptions)

// WithChecksumVerification enables or disables verification of metadata
// checksums (enabled by default).
func WithChecksumVerification(on bool) Option {
	return func(o *openOptions) { o.verifyChecksums = on }
}

// WithExternalLinks enables or disables following external links (enabled
// by default).
func WithExternalLinks(on bool) Option {
	return func(o *openOptions) { o.followExternalLinks = on }
}

// WithExternalLinkPrefix sets the directory prefix used to locate the target
// files of external links (H5Pset_elink_prefix).
func WithExternalLinkPrefix(prefix string) Option {
	return func(o *openOptions) { o.extLinkPrefix = prefix }
}

// WithExternalFilePrefix sets the directory prefix used to locate external
// data files (H5Pset_efile_prefix).
func WithExternalFilePrefix(prefix string) Option {
	return func(o *openOptions) { o.extFilePrefix = prefix }
}

// WithVirtualPrefix sets the directory prefix used to locate the source
// files of virtual datasets (H5Pset_virtual_prefix).
func WithVirtualPrefix(prefix string) Option {
	return func(o *openOptions) { o.vdsPrefix = prefix }
}

// Open opens an HDF5 file for reading (H5Fopen with H5F_ACC_RDONLY).
//
// Files written with the family driver are detected from the superblock's
// driver information and opened as a set when name is a member file name
// pattern (as in H5Pset_fapl_family) or the first member.
func Open(name string, opts ...Option) (*File, error) {
	o := defaultOpenOptions()
	for _, opt := range opts {
		opt(&o)
	}
	fd, err := openDriver(name, &o)
	if err != nil {
		return nil, err
	}
	f, err := openWithDriver(name, fd, o)
	if err != nil {
		fd.Close()
		return nil, err
	}
	return f, nil
}

// OpenReader opens an HDF5 file image held by an io.ReaderAt of the given
// size (the "core" driver / H5LTopen_file_image). The reader is closed with
// the file if it implements io.Closer.
func OpenReader(r io.ReaderAt, size int64, opts ...Option) (*File, error) {
	o := defaultOpenOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return openWithDriver("", &readerAtDriver{r: r, size: uint64(size)}, o)
}

func openWithDriver(name string, fd fileDriver, o openOptions) (*File, error) {
	f := &File{name: name, fd: fd, opts: o}
	if name != "" {
		f.dir = filepath.Dir(name)
	}
	if err := f.superRead(); err != nil {
		return nil, err
	}
	return f, nil
}

// Name returns the path the file was opened with.
func (f *File) Name() string { return f.name }

// Close closes the file and any external files opened through it.
func (f *File) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	extFiles := f.extFiles
	f.extFiles = nil
	f.mu.Unlock()
	var first error
	for _, ef := range extFiles {
		if err := ef.Close(); err != nil && first == nil {
			first = err
		}
	}
	if err := f.fd.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

// readAt reads n bytes at HDF5 address addr (relative to the base address).
// It is the Go counterpart of H5F_block_read / H5FD_read for a buffer that
// must be completely present in the file.
func (f *File) readAt(addr haddr, n int) ([]byte, error) {
	if !addrDefined(addr) {
		return nil, errorf(H5E_IO, H5E_BADVALUE, "undefined address")
	}
	if n < 0 {
		return nil, errorf(H5E_IO, H5E_BADVALUE, "negative read size")
	}
	abs := f.baseAddr + addr
	if abs < addr || abs+uint64(n) < abs {
		return nil, errorf(H5E_IO, H5E_OVERFLOW, "address overflow")
	}
	if eof := f.fd.EOF(); abs+uint64(n) > eof {
		return nil, errorf(H5E_IO, H5E_OVERFLOW, "addr overflow, addr = %d, size = %d, eoa = %d", addr, n, eof)
	}
	buf := make([]byte, n)
	if _, err := f.fd.ReadAt(buf, int64(abs)); err != nil && err != io.EOF {
		return nil, wrapf(err, H5E_IO, H5E_READERROR, "file read failed")
	}
	return buf, nil
}

// readInto is readAt into a caller-provided buffer (used for raw data).
func (f *File) readInto(addr haddr, buf []byte) error {
	if !addrDefined(addr) {
		return errorf(H5E_IO, H5E_BADVALUE, "undefined address")
	}
	abs := f.baseAddr + addr
	if abs < addr || abs+uint64(len(buf)) < abs {
		return errorf(H5E_IO, H5E_OVERFLOW, "address overflow")
	}
	if eof := f.fd.EOF(); abs+uint64(len(buf)) > eof {
		return errorf(H5E_IO, H5E_OVERFLOW, "addr overflow, addr = %d, size = %d, eoa = %d", addr, len(buf), eof)
	}
	if _, err := f.fd.ReadAt(buf, int64(abs)); err != nil && err != io.EOF {
		return wrapf(err, H5E_IO, H5E_READERROR, "file read failed")
	}
	return nil
}

// readImage reads n bytes at addr and wraps them in a decoding cursor.
func (f *File) readImage(addr haddr, n int) (*image, error) {
	buf, err := f.readAt(addr, n)
	if err != nil {
		return nil, err
	}
	return f.newImage(buf), nil
}

// eof is H5F_get_eof relative to the base address.
func (f *File) eof() uint64 {
	e := f.fd.EOF()
	if e < f.baseAddr {
		return 0
	}
	return e - f.baseAddr
}

// openDriver opens name with the sec2 driver. Detection of family/multi
// driver files happens after the superblock is read (see h5fd_family.go /
// h5fd_multi.go) and replaces f.fd.
func openDriver(name string, o *openOptions) (fileDriver, error) {
	// Family patterns, multi/split base names and driver-info-less family
	// files cannot be opened as a single flat file (h5fd_sb.go).
	if fd, err := openMemberDriver(name, o); fd != nil || err != nil {
		return fd, err
	}
	osf, err := os.Open(name)
	if err != nil {
		return nil, wrapf(err, H5E_FILE, H5E_CANTOPENFILE, "unable to open file")
	}
	st, err := osf.Stat()
	if err != nil {
		osf.Close()
		return nil, wrapf(err, H5E_FILE, H5E_CANTOPENFILE, "unable to stat file")
	}
	return &sec2Driver{f: osf, size: uint64(st.Size())}, nil
}
