package hdf5

// Translated from src/H5Ofsinfo.c (decode only): the file space info
// message (superblock extension).

// File space info message versions (H5Oprivate.h).
const (
	/* Version 0: deprecated */
	h5oFsinfoVersion0 = 0

	/* Version 1 */
	h5oFsinfoVersion1      = 1
	h5oFsinfoVersionLatest = h5oFsinfoVersion1
)

// H5F_fspace_strategy_t (H5Fpublic.h).
const (
	h5fFspaceStrategyFsmAggr = 0 /* Mechanisms: free-space managers, aggregators, and virtual file drivers */
	h5fFspaceStrategyPage    = 1 /* Mechanisms: free-space managers with embedded paged aggregation and virtual file drivers */
	h5fFspaceStrategyAggr    = 2 /* Mechanisms: aggregators and virtual file drivers */
	h5fFspaceStrategyNone    = 3 /* Mechanisms: virtual file drivers */
	h5fFspaceStrategyNtypes  = 4 /* Sentinel */
)

// H5F_file_space_type_t (deprecated, H5Fpublic.h): the strategies of
// version 0 messages.
const (
	h5fFileSpaceDefault    = 0 /* Default (or current) free space strategy setting */
	h5fFileSpaceAllPersist = 1 /* Persistent free space managers, aggregators, virtual file driver */
	h5fFileSpaceAll        = 2 /* Non-persistent free space managers, aggregators, virtual file driver */
	h5fFileSpaceAggrVfd    = 3 /* Aggregators, Virtual file driver */
	h5fFileSpaceVfd        = 4 /* Virtual file driver */
	h5fFileSpaceNtypes     = 5 /* Sentinel */
)

// File space defaults (H5Fprivate.h).
const (
	h5fFreeSpacePersistDef     = false
	h5fFreeSpaceThresholdDef   = 1
	h5fFileSpacePageSizeDef    = 4096
	h5fFileSpacePageSizeMin    = 512
	h5fFileSpacePgendMetaThres = 0

	h5fdMemNtypes    = 7  /* H5FD_MEM_NTYPES: H5FD_MEM_SUPER (1) .. H5FD_MEM_OHDR (6), plus sentinel */
	h5fMemPageSuper  = 1  /* H5F_MEM_PAGE_SUPER */
	h5fMemPageNtypes = 13 /* H5F_MEM_PAGE_NTYPES */
)

// h5oFsinfoDecode is H5O__fsinfo_decode: decode a file space info message.
func (f *File) h5oFsinfoDecode(p *image) (*h5oFsinfo, error) {
	/* Allocate space for message */
	fsinfo := &h5oFsinfo{}

	for ptype := h5fMemPageSuper; ptype < h5fMemPageNtypes; ptype++ {
		fsinfo.fsAddr[ptype-1] = haddrUndef
	}

	/* Version of message */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	vers := p.uint8()

	if vers == h5oFsinfoVersion0 {
		var threshold uint64 /* Threshold */

		fsinfo.persist = h5fFreeSpacePersistDef
		fsinfo.threshold = h5fFreeSpaceThresholdDef
		fsinfo.pageSize = h5fFileSpacePageSizeDef
		fsinfo.pgendMetaThres = h5fFileSpacePgendMetaThres
		fsinfo.eoa = haddrUndef

		if p.overflow(1 + int(f.sizeofSize)) {
			return nil, errDecodeOverflow()
		}
		strategy := p.uint8()  /* File space strategy */
		threshold = p.length() /* Free-space section threshold */

		/* Map version 0 (deprecated) to version 1 message */
		switch strategy {
		case h5fFileSpaceAllPersist:
			fsinfo.strategy = h5fFspaceStrategyFsmAggr
			fsinfo.persist = true
			fsinfo.threshold = threshold
			/* (H5F_get_eoa(f, H5FD_MEM_DEFAULT): the end of the file) */
			fsinfo.eoa = f.eof()
			for typ := 1; typ < h5fdMemNtypes; typ++ {
				if p.overflow(int(f.sizeofAddr)) {
					return nil, errDecodeOverflow()
				}
				fsinfo.fsAddr[typ-1] = p.addr()
			}
		case h5fFileSpaceAll:
			fsinfo.strategy = h5fFspaceStrategyFsmAggr
			fsinfo.threshold = threshold
		case h5fFileSpaceAggrVfd:
			fsinfo.strategy = h5fFspaceStrategyAggr
		case h5fFileSpaceVfd:
			fsinfo.strategy = h5fFspaceStrategyNone
		default:
			return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid file space strategy")
		}

		fsinfo.version = h5oFsinfoVersion1
		/* (fsinfo->mapped = true: only used when the file is open for writing) */
	} else {
		if vers < h5oFsinfoVersion1 {
			return nil, errorf(H5E_ARGS, H5E_BADVALUE, "bad version number")
		}
		fsinfo.version = vers
		if p.overflow(1 + 1) {
			return nil, errDecodeOverflow()
		}
		fsinfo.strategy = p.uint8()     /* File space strategy */
		fsinfo.persist = p.uint8() != 0 /* Free-space persist or not */
		if p.overflow(int(f.sizeofSize)) {
			return nil, errDecodeOverflow()
		}
		fsinfo.threshold = p.length() /* Free-space section threshold */
		if p.overflow(int(f.sizeofSize)) {
			return nil, errDecodeOverflow()
		}
		fsinfo.pageSize = p.length() /* File space page size */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		fsinfo.pgendMetaThres = p.uint16() /* Page end metadata threshold */
		if p.overflow(int(f.sizeofAddr)) {
			return nil, errDecodeOverflow()
		}
		fsinfo.eoa = p.addr() /* EOA before free-space header and section info */

		/* Decode addresses of free space managers, if persisting */
		if fsinfo.persist {
			for ptype := h5fMemPageSuper; ptype < h5fMemPageNtypes; ptype++ {
				if p.overflow(int(f.sizeofAddr)) {
					return nil, errDecodeOverflow()
				}
				fsinfo.fsAddr[ptype-1] = p.addr()
			}
		}
		/* (fsinfo->mapped = false) */
	}
	return fsinfo, nil
}

// h5oFsinfoCheckVersion is H5O_fsinfo_check_version with the default
// (latest) upper library version bound: the message version must not be
// newer than this library knows.
func h5oFsinfoCheckVersion(fsinfo *h5oFsinfo) error {
	/* Check the version */
	if fsinfo.version > h5oFsinfoVersionLatest {
		return errorf(H5E_OHDR, H5E_BADRANGE, "File space info message's version out of bounds")
	}
	return nil
}
