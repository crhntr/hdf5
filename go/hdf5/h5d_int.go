package hdf5

// Translated from src/H5Dint.c (read side): opening a dataset from its
// object header (H5D__open_oid) into an in-core h5dShared (H5D_shared_t),
// and the queries the public Dataset API is built on (H5D__get_space,
// H5D__get_type, H5D__get_storage_size, H5D__get_offset and the parts of
// H5D_get_create_plist that report the layout, filters and fill value).
//
// Entry points for the public API:
//
//	(f *File) dOpen(oh *h5o) (*h5dShared, error)
//	(f *File) dRead(ds, fileSpace) ([]byte, error)        (h5d_io.go)
//	(f *File) dReadValues(ds, fileSpace) ([]any, error)   (h5d_io.go)
//	(ds *h5dShared) datatype() / dataspace() / layoutInfo() / filters() /
//	                fillValue(f) / storageSize(f)
//
// A hyperslab read is h5sSelectHyperslabPublic(ds.space, hs) followed by
// dRead / dReadValues with the returned dataspace.

import (
	"errors"
	"math/bits"
	"path/filepath"
	"strings"

	"github.com/HDFGroup/hdf5/go/h5"
)

// h5dShared is H5D_shared_t: the in-core description of an open dataset
// (the fields used when reading).
type h5dShared struct {
	f    *File /* file the dataset lives in (oloc.file) */
	oh   *h5o  /* the dataset's object header */
	addr haddr /* address of the object header (oloc.addr) */

	typ   *h5.Datatype /* Datatype for this dataset (in the file) */
	space *h5s         /* Dataspace of this dataset ("all" selection) */

	/* Cached dataspace info */
	ndims    int      /* Number of dimensions */
	currDims []uint64 /* Current dimension sizes */
	maxDims  []uint64 /* Maximum dimension sizes (H5S_UNLIMITED for unlimited) */

	/* Cached dataset creation property list values */
	dcplCache h5dDcplCache

	layout    h5oLayout     /* Data layout (a private copy of the layout message) */
	layoutOps *h5dLayoutOps /* layout.ops: I/O operations for the layout class */

	/* Chunked storage (layout.u.chunk and layout.storage.u.chunk) */
	chunk   h5dChunkLayout
	chunkSt h5dChunkStorage

	/* Virtual storage (layout.storage.u.virt) */
	virt *h5dVirtualStorage

	/* Prefixes from the access property list (H5D__build_file_prefix) */
	extfilePrefix string /* expanded H5D_ACS_EFILE_PREFIX_NAME; "" for none */
	vdsPrefix     string /* expanded H5D_ACS_VDS_PREFIX_NAME; "" for none */
}

// h5dDcplCache is H5D_dcpl_cache_t: the dataset creation properties cached
// for I/O.
type h5dDcplCache struct {
	pline h5oPline /* I/O pipeline info (nused == 0 when there is no filter pipeline message) */
	fill  h5oFill  /* Fill value info */
	efl   h5oEfl   /* External file list info (nused == 0 when there is none) */
}

// h5dCacheDataspaceInfo is H5D__cache_dataspace_info: cache the dataspace
// dimensions in the dataset.
func h5dCacheDataspaceInfo(dset *h5dShared) error {
	/* Cache info for dataset's dataspace */
	ndims := h5sGetSimpleExtentNdims(dset.space)
	if ndims < 0 || ndims > h5sMaxRank {
		return errorf(H5E_DATASET, H5E_CANTGET, "can't cache dataspace dimensions")
	}
	dset.ndims = ndims
	dset.currDims, dset.maxDims = h5sGetSimpleExtentDims(dset.space)
	if dset.currDims == nil {
		dset.currDims, dset.maxDims = []uint64{}, []uint64{}
	}
	return nil
}

// h5dBuildFilePrefix is H5D__build_file_prefix: expand the external file
// (or virtual source file) prefix. "${ORIGIN}" at the start of the prefix
// is replaced by the directory of the HDF5 file. An empty result means "no
// prefix" (names are relative to the current directory).
func (f *File) h5dBuildFilePrefix(prefix string) string {
	/* Prefix has to be checked for NULL / empty string again because the
	 * code above might have updated it.
	 */
	if prefix == "" || prefix == "." {
		/* filename is interpreted as relative to the current directory,
		 * does not need to be expanded
		 */
		return ""
	}
	if strings.HasPrefix(prefix, "${ORIGIN}") {
		/* Replace ${ORIGIN} at beginning of prefix by directory of HDF5 file */
		dir := f.dir
		if dir == "" {
			dir = "."
		}
		return dir + "/" + strings.TrimPrefix(strings.TrimPrefix(prefix, "${ORIGIN}"), "/")
	}
	return prefix
}

// dOpen is H5D__open_oid (with the parts of H5D_open that set up the
// access properties): open the dataset whose object header is oh.
func (f *File) dOpen(oh *h5o) (*h5dShared, error) {
	if oh == nil || len(oh.chunks) == 0 {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "no object header")
	}
	dataset := &h5dShared{f: f, oh: oh, addr: h5oGetOhAddr(oh)}

	/* Get the type and space */
	v, err := f.msgRead(oh, h5oDtypeID)
	if err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to load type info from dataset header")
	}
	dt, ok := v.(*h5.Datatype)
	if !ok || dt == nil {
		return nil, errorf(H5E_DATASET, H5E_CANTINIT, "unable to load type info from dataset header")
	}
	if dt.Size == 0 {
		return nil, errorf(H5E_DATASET, H5E_CANTINIT, "invalid datatype size")
	}
	dataset.typ = dt

	/* (H5S_read) */
	v, err = f.msgRead(oh, h5oSdspaceID)
	if err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to load dataspace info from dataset header")
	}
	ext, ok := v.(*h5sExtent)
	if !ok || ext == nil {
		return nil, errorf(H5E_DATASET, H5E_CANTINIT, "unable to load dataspace info from dataset header")
	}
	if dataset.space, err = h5sFromExtent(ext); err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to load dataspace info from dataset header")
	}

	/* Cache the dataset's dataspace info */
	if err := h5dCacheDataspaceInfo(dataset); err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTCOPY, "can't cache dataspace info")
	}

	/* (H5D_open: get the external file and virtual dataset prefixes) */
	dataset.extfilePrefix = f.h5dBuildFilePrefix(f.opts.extFilePrefix)
	dataset.vdsPrefix = f.h5dBuildFilePrefix(f.opts.vdsPrefix)

	/* Get the layout/pline/efl message information */
	if err := f.h5dLayoutOhRead(dataset); err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get layout/pline/efl info")
	}

	/* Point at dataset's copy, to cache it for later */
	fillProp := &dataset.dcplCache.fill

	/* Try to get the new fill value message from the object header */
	if f.msgExists(oh, h5oFillNewID) {
		v, err := f.msgRead(oh, h5oFillNewID)
		if err != nil {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't retrieve message")
		}
		fill, ok := v.(*h5oFill)
		if !ok {
			return nil, errorf(H5E_DATASET, H5E_CANTGET, "can't retrieve message")
		}
		*fillProp = *fill
	} else {
		/* Default fill value properties (H5D_def_dset.dcpl_cache.fill) */
		*fillProp = h5oFill{allocTime: h5dAllocTimeLate, fillTime: h5dFillTimeIfset}

		/* For backward compatibility, try to retrieve the old fill value message */
		if f.msgExists(oh, h5oFillID) {
			v, err := f.msgRead(oh, h5oFillID)
			if err != nil {
				return nil, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't retrieve message")
			}
			fill, ok := v.(*h5oFill)
			if !ok {
				return nil, errorf(H5E_DATASET, H5E_CANTGET, "can't retrieve message")
			}
			*fillProp = *fill
		} else {
			/* Set the space allocation time appropriately, based on the type of dataset storage */
			switch dataset.layout.typ {
			case h5dCompact:
				fillProp.allocTime = h5dAllocTimeEarly
			case h5dContiguous:
				fillProp.allocTime = h5dAllocTimeLate
			case h5dChunked:
				fillProp.allocTime = h5dAllocTimeIncr
			case h5dVirtual:
				fillProp.allocTime = h5dAllocTimeIncr
			default:
				return nil, errorf(H5E_DATASET, H5E_UNSUPPORTED, "not implemented yet")
			}
		}

		/* If "old" fill value size is 0 (undefined), map it to -1 */
		if fillProp.size == 0 {
			fillProp.size = -1
		}
	}

	/* Check if there is a fill value, but no type yet */
	/* (The fill value is kept in the dataset's datatype, so no conversion
	 *  is ever needed; just make sure the buffer holds one element.) */
	if fillProp.buf != nil && uint64(len(fillProp.buf)) < dt.Size {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "fill value size doesn't match dataset's datatype size")
	}

	/* (H5Dget_space runs H5D__virtual_set_extent_unlim: do it once here so
	 *  that dataspace() reports the extent derived from the source
	 *  datasets. While the source dataset lookup is not available
	 *  (ErrUnsupported) the stored extent is kept; reads then fail.) */
	if dataset.layout.typ == h5dVirtual {
		if err := f.h5dVirtualSetExtentUnlim(dataset); err != nil && !errors.Is(err, ErrUnsupported) {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to update virtual dataset extent")
		}
	}
	return dataset, nil
}

// h5dGetStorageSize is H5D__get_storage_size: the amount of storage
// allocated for the raw data.
func (f *File) h5dGetStorageSize(dset *h5dShared) (uint64, error) {
	switch dset.layout.typ {
	case h5dChunked:
		if dset.layoutOps.isSpaceAlloc(dset) {
			n, err := f.h5dChunkAllocated(dset)
			if err != nil {
				return 0, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't retrieve chunked dataset allocated size")
			}
			return n, nil
		}
		return 0, nil

	case h5dContiguous:
		/* Datasets which are not allocated yet are using no space on disk */
		if dset.layoutOps.isSpaceAlloc(dset) {
			return dset.layout.contigSize, nil
		}
		return 0, nil

	case h5dCompact:
		return dset.layout.compactSize, nil

	case h5dVirtual:
		/* Just set to 0, as virtual datasets do not actually store raw data
		 */
		return 0, nil
	}
	return 0, errorf(H5E_ARGS, H5E_BADTYPE, "not a dataset type")
}

// h5dGetOffset is H5D__get_offset: the absolute file offset of the raw data
// of a contiguous dataset, or HADDR_UNDEF.
func (f *File) h5dGetOffset(dset *h5dShared) haddr {
	switch dset.layout.typ {
	case h5dVirtual, h5dChunked, h5dCompact:
		return haddrUndef

	case h5dContiguous:
		/* If dataspace hasn't been allocated or dataset is stored in
		 * an external file, the value will be HADDR_UNDEF.
		 */
		if dset.dcplCache.efl.nused == 0 || addrDefined(dset.layout.contigAddr) {
			if !addrDefined(dset.layout.contigAddr) {
				return haddrUndef
			}
			/* Return the absolute dataset offset from the beginning of file. */
			return dset.layout.contigAddr + f.baseAddr
		}
	}
	return haddrUndef
}

// h5dGetSpace is H5D__get_space: a copy of the dataset's dataspace (for a
// virtual dataset, after updating its extent from the source datasets).
func (f *File) h5dGetSpace(dset *h5dShared) (*h5s, error) {
	/* If the layout is virtual, update the extent */
	if dset.layout.typ == h5dVirtual {
		if err := f.h5dVirtualSetExtentUnlim(dset); err != nil {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to update virtual dataset extent")
		}
	}
	return h5sCopy(dset.space), nil
}

// h5dGetType is H5D__get_type: the dataset's datatype.
func h5dGetType(dset *h5dShared) *h5.Datatype { return dset.typ }

// datatype returns the dataset's datatype (shared; do not modify).
func (ds *h5dShared) datatype() *h5.Datatype { return h5dGetType(ds) }

// dataspace returns the dataset's dataspace extent.
func (ds *h5dShared) dataspace() h5.Dataspace { return ds.space.extent.public() }

// storageSize is H5Dget_storage_size (0 on error, as the C API returns).
func (ds *h5dShared) storageSize(f *File) uint64 {
	n, err := f.h5dGetStorageSize(ds)
	if err != nil {
		return 0
	}
	return n
}

// layoutInfo describes the dataset's storage layout (the H5Pget_layout,
// H5Pget_chunk, H5Dget_offset / H5Dget_storage_size and H5Pget_external
// queries).
func (ds *h5dShared) layoutInfo() (h5.Layout, error) {
	l := h5.Layout{Class: h5.LayoutClass(ds.layout.typ), Version: ds.layout.version}
	switch ds.layout.typ {
	case h5dCompact:
		l.CompactSize = ds.layout.compactSize
	case h5dContiguous:
		if a := ds.f.h5dGetOffset(ds); addrDefined(a) {
			l.Address, l.AddressDefined = a, true
		}
		l.Size = ds.layout.contigSize
		for u := 0; u < ds.dcplCache.efl.nused && u < len(ds.dcplCache.efl.slot); u++ {
			s := ds.dcplCache.efl.slot[u]
			l.External = append(l.External, h5.ExternalFile{Name: s.name, Offset: uint64(s.offset), Size: s.size})
		}
	case h5dChunked:
		l.ChunkIndex = h5.ChunkIndex(ds.chunk.idxType)
		if ds.chunk.ndims < 1 {
			return l, errorf(H5E_DATASET, H5E_BADVALUE, "invalid chunk dimensionality")
		}
		l.ChunkDims = make([]uint64, ds.chunk.ndims-1)
		for u := range l.ChunkDims {
			l.ChunkDims[u] = uint64(ds.chunk.dim[u])
		}
	case h5dVirtual:
	default:
		return l, errorf(H5E_DATASET, H5E_BADTYPE, "unknown dataset layout type %d", ds.layout.typ)
	}
	return l, nil
}

// h5dFilterName returns the name H5Pget_filter2 reports for a filter: its
// name in the pipeline message, else the name of the library's filter
// class.
func h5dFilterName(filter *h5zFilterInfo) string {
	if filter.name != "" {
		return filter.name
	}
	switch filter.id {
	case h5zFilterDeflate:
		return "deflate"
	case h5zFilterShuffle:
		return "shuffle"
	case h5zFilterFletcher32:
		return "fletcher32"
	case h5zFilterSzip:
		return "szip"
	case h5zFilterNbit:
		return "nbit"
	case h5zFilterScaleoffset:
		return "scaleoffset"
	}
	/* Check for unknown library filter */
	/* (probably from a future version of the library) */
	if filter.id < h5zFilterReserved {
		return "Unknown library filter"
	}
	return "Unknown filter"
}

// filters returns the filter pipeline (H5Pget_nfilters / H5Pget_filter2).
func (ds *h5dShared) filters() []h5.Filter {
	pline := &ds.dcplCache.pline
	var out []h5.Filter
	for u := 0; u < pline.nused && u < len(pline.filter); u++ {
		fi := &pline.filter[u]
		out = append(out, h5.Filter{
			ID:       h5.FilterID(fi.id),
			Name:     h5dFilterName(fi),
			Flags:    fi.flags,
			CDValues: append([]uint32(nil), fi.cdValues...),
		})
	}
	return out
}

// h5pIsFillValueDefined is H5P_is_fill_value_defined.
func h5pIsFillValueDefined(fill *h5oFill) int {
	/* Check if the fill value was never set */
	if fill.size == -1 && fill.buf == nil {
		return h5dFillValueUndefined
	}
	/* Check if the fill value was set to the default fill value by the library */
	if fill.size == 0 && fill.buf == nil {
		return h5dFillValueDefault
	}
	/* Check if the fill value was set by the application */
	if fill.size > 0 && fill.buf != nil {
		return h5dFillValueUserDefined
	}
	return h5dFillValueError
}

// fillValue reports the fill value properties (H5Pget_fill_value,
// H5Pfill_value_defined, H5Pget_fill_time, H5Pget_alloc_time). Value holds
// the user-defined fill value as raw bytes in the dataset's datatype; it is
// checked to decode (h5tDecodeValues) so that a broken fill value is
// reported here.
func (ds *h5dShared) fillValue(f *File) (h5.FillValueInfo, error) {
	fill := &ds.dcplCache.fill
	info := h5.FillValueInfo{
		Version:   fill.version,
		AllocTime: fill.allocTime,
		FillTime:  fill.fillTime,
	}
	switch h5pIsFillValueDefined(fill) {
	case h5dFillValueUndefined:
		info.Undefined = true
	case h5dFillValueDefault:
	case h5dFillValueUserDefined:
		info.Defined = true
		info.Value = append([]byte(nil), fill.buf[:ds.typ.Size]...)
		if _, err := h5tDecodeValues(f, ds.typ, info.Value, 1); err != nil {
			return info, wrapf(err, H5E_DATASET, H5E_CANTCONVERT, "unable to convert fill value")
		}
	default:
		return info, errorf(H5E_PLIST, H5E_BADVALUE, "invalid fill value")
	}
	return info, nil
}

// fillValueDecoded returns the fill value as a canonical value (nil when
// the fill value is undefined; the zero element when it is the library
// default).
func (ds *h5dShared) fillValueDecoded(f *File) (any, error) {
	fill := &ds.dcplCache.fill
	if h5pIsFillValueDefined(fill) == h5dFillValueUndefined {
		return nil, nil
	}
	buf, err := h5dFillInit(fill, ds.typ)
	if err != nil {
		return nil, err
	}
	vals, err := h5tDecodeValues(f, ds.typ, buf, 1)
	if err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTCONVERT, "unable to convert fill value")
	}
	return vals[0], nil
}

// h5dMulSize returns a*b, failing on overflow.
func h5dMulSize(a, b uint64) (uint64, error) {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return 0, errorf(H5E_DATASET, H5E_OVERFLOW, "size of dataset's storage overflowed")
	}
	return lo, nil
}

// h5dCombinePath is H5_combine_path: prefix a relative name with a
// directory prefix ("" for none). Absolute names are returned unchanged.
func h5dCombinePath(prefix, name string) string {
	if prefix == "" || filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(prefix, name)
}
