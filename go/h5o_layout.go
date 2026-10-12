package hdf5

// Translated from src/H5Olayout.c (decode only): the data layout message.
// The parts of the virtual dataset decoding that belong to the dataset
// module (H5D_virtual_parse_source_name, H5D_virtual_check_mapping_pre/post,
// H5D_virtual_update_min_dims) are not translated here.

import "bytes"

// Layout message versions (H5Oprivate.h).
const (
	/* Initial version of the layout information */
	h5oLayoutVersion1 = 1

	/* This version added support for the "compact" layout */
	h5oLayoutVersion2 = 2

	/* This version dropped the dimensions for contiguous & compact storage,
	 * dropped the dimensionality for compact storage and stores chunk
	 * dimensions as 32-bit values */
	h5oLayoutVersion3 = 3

	/* This version adds different types of indices to chunked datasets,
	 * allows for "virtual" datasets and encodes chunk dimensions in a
	 * variable number of bytes */
	h5oLayoutVersion4 = 4

	/* The latest version of the format. */
	h5oLayoutVersionLatest = h5oLayoutVersion4

	/* Version # of encoded virtual dataset global heap blocks */
	h5oLayoutVdsGhEncVers = 0

	h5oLayoutAllChunkFlags = h5oLayoutChunkDontFilterPartialBoundChunks | h5oLayoutChunkSingleIndexWithFilter

	h5dChunkIdxNtypes = 6 /* H5D_CHUNK_IDX_NTYPES: this one must be last! */
)

// h5oLayoutDecode is H5O__layout_decode: decode a data layout message.
func (f *File) h5oLayoutDecode(p *image) (*h5oLayout, error) {
	mesg := &h5oLayout{}

	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	mesg.version = p.uint8()
	if mesg.version < h5oLayoutVersion1 || mesg.version > h5oLayoutVersion4 {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad version number for layout message")
	}

	if mesg.version < h5oLayoutVersion3 {
		/* Dimensionality */
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		ndims := uint32(p.uint8()) /* Num dimensions in chunk */
		if ndims == 0 || ndims > h5oLayoutNdims {
			return nil, errorf(H5E_OHDR, H5E_BADVALUE, "dimensionality is out of range")
		}
		mesg.ndims = ndims

		/* Layout class */
		if p.overflow(1) {
			return nil, errDecodeOverflow()
		}
		mesg.typ = p.uint8()
		if h5dContiguous != mesg.typ && h5dChunked != mesg.typ && h5dCompact != mesg.typ {
			return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad layout type for layout message")
		}

		/* Reserved bytes */
		if p.overflow(5) {
			return nil, errDecodeOverflow()
		}
		p.skip(5)

		/* Address */
		switch mesg.typ {
		case h5dContiguous:
			if p.overflow(int(f.sizeofAddr)) {
				return nil, errDecodeOverflow()
			}
			mesg.contigAddr = p.addr()
		case h5dChunked:
			if p.overflow(int(f.sizeofAddr)) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkIdxAddr = p.addr()

			/* Set the chunk operations
			 * (Only "btree" indexing type currently supported in this version)
			 */
			mesg.chunkIdxType = h5dChunkIdxBtree
		case h5dCompact:
		default:
			return nil, errorf(H5E_OHDR, H5E_BADVALUE, "invalid layout type")
		}

		/* Read the size */
		if mesg.typ != h5dChunked {
			/* Don't compute size of contiguous storage here, due to possible
			 * truncation of the dimension sizes when they were stored in this
			 * version of the layout message.  Compute the contiguous storage
			 * size in the dataset code, where we've got the dataspace
			 * information available also.
			 */
			if p.overflow(int(ndims) * 4) {
				return nil, errDecodeOverflow()
			}
			/* (the dimension sizes are kept in mesg.dim for the dataset code) */
			mesg.dim = make([]uint64, ndims)
			for u := range mesg.dim {
				mesg.dim[u] = uint64(p.uint32())
			}
		} else {
			if ndims < 2 {
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad dimensions for chunked storage")
			}
			mesg.chunkNdims = ndims
			if p.overflow(int(ndims) * 4) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkDim = make([]uint64, ndims)
			for u := range mesg.chunkDim {
				mesg.chunkDim[u] = uint64(p.uint32())

				/* Just in case that something goes very wrong, such as file corruption */
				if mesg.chunkDim[u] == 0 {
					return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad chunk dimension value when parsing layout message - chunk dimension must be positive: mesg->u.chunk.dim[%d] = %d", u, mesg.chunkDim[u])
				}
			}
		}

		if mesg.typ == h5dCompact {
			if p.overflow(4) {
				return nil, errDecodeOverflow()
			}
			mesg.compactSize = uint64(p.uint32())
			if mesg.compactSize > 0 {
				/* Ensure that size doesn't exceed buffer size, due to possible data corruption */
				if mesg.compactSize > uint64(p.remaining()) || p.overflow(int(mesg.compactSize)) {
					return nil, errDecodeOverflow()
				}
				mesg.compactBuf = append([]byte(nil), p.bytes(int(mesg.compactSize))...)
			}
		}
		return mesg, nil
	}

	/* Layout & storage class */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	mesg.typ = p.uint8()

	/* Interpret the rest of the message according to the layout class */
	switch mesg.typ {
	case h5dCompact:
		/* Compact data size */
		if p.overflow(2) {
			return nil, errDecodeOverflow()
		}
		mesg.compactSize = uint64(p.uint16())
		if mesg.compactSize > 0 {
			/* Ensure that size doesn't exceed buffer size, due to possible data corruption */
			if p.overflow(int(mesg.compactSize)) {
				return nil, errDecodeOverflow()
			}

			/* Compact data */
			mesg.compactBuf = append([]byte(nil), p.bytes(int(mesg.compactSize))...)
		}

	case h5dContiguous:
		/* Contiguous storage address */
		if p.overflow(int(f.sizeofAddr)) {
			return nil, errDecodeOverflow()
		}
		mesg.contigAddr = p.addr()

		/* Contiguous storage size */
		if p.overflow(int(f.sizeofSize)) {
			return nil, errDecodeOverflow()
		}
		mesg.contigSize = p.length()

	case h5dChunked:
		if mesg.version < h5oLayoutVersion4 {
			/* Set the chunked layout flags */
			mesg.chunkFlags = 0

			/* Dimensionality */
			if p.overflow(1) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkNdims = uint32(p.uint8())
			if mesg.chunkNdims > h5oLayoutNdims {
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "dimensionality is too large")
			}
			if mesg.chunkNdims < 2 {
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad dimensions for chunked storage")
			}

			/* B-tree address */
			if p.overflow(int(f.sizeofAddr)) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkIdxAddr = p.addr()

			if p.overflow(int(mesg.chunkNdims) * 4) {
				return nil, errDecodeOverflow()
			}

			/* Chunk dimensions */
			mesg.chunkDim = make([]uint64, mesg.chunkNdims)
			for u := range mesg.chunkDim {
				mesg.chunkDim[u] = uint64(p.uint32())

				/* Just in case that something goes very wrong, such as file corruption. */
				if mesg.chunkDim[u] == 0 {
					return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad chunk dimension value when parsing layout message - chunk dimension must be positive: mesg->u.chunk.dim[%d] = %d", u, mesg.chunkDim[u])
				}
			}

			/* Set the chunk operations
			 * (Only "btree" indexing type supported with v3 of message format)
			 */
			mesg.chunkIdxType = h5dChunkIdxBtree
		} else {
			/* Get the chunked layout flags */
			if p.overflow(1) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkFlags = p.uint8()

			/* Check for valid flags */
			/* (Currently issues an error for all non-zero values,
			 *      until features are added for the flags)
			 */
			if mesg.chunkFlags&^h5oLayoutAllChunkFlags != 0 {
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad flag value for message")
			}

			/* Dimensionality */
			if p.overflow(1) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkNdims = uint32(p.uint8())
			if mesg.chunkNdims > h5oLayoutNdims {
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "dimensionality is too large")
			}

			/* Encoded # of bytes for each chunk dimension */
			if p.overflow(1) {
				return nil, errDecodeOverflow()
			}
			encBytesPerDim := int(p.uint8())
			if encBytesPerDim == 0 || encBytesPerDim > 8 {
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "encoded chunk dimension size is too large")
			}

			if p.overflow(int(mesg.chunkNdims) * encBytesPerDim) {
				return nil, errDecodeOverflow()
			}

			/* Chunk dimensions */
			mesg.chunkDim = make([]uint64, mesg.chunkNdims)
			for u := range mesg.chunkDim {
				mesg.chunkDim[u] = p.uintVar(encBytesPerDim)

				/* Just in case that something goes very wrong, such as file corruption. */
				if mesg.chunkDim[u] == 0 {
					return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad chunk dimension value when parsing layout message - chunk dimension must be positive: mesg->u.chunk.dim[%d] = %d", u, mesg.chunkDim[u])
				}
			}

			/* Chunk index type */
			if p.overflow(1) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkIdxType = p.uint8()
			if mesg.chunkIdxType >= h5dChunkIdxNtypes {
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "unknown chunk index type")
			}

			switch mesg.chunkIdxType {
			case h5dChunkIdxBtree:
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "v1 B-tree index type should never be in a v4 layout message")

			case h5dChunkIdxNone: /* Implicit Index */

			case h5dChunkIdxSingle: /* Single Chunk Index */
				if mesg.chunkFlags&h5oLayoutChunkSingleIndexWithFilter != 0 {
					if p.overflow(int(f.sizeofSize) + 4) {
						return nil, errDecodeOverflow()
					}
					mesg.singleFiltered = true
					nbytes := p.length()
					if nbytes > 0xffffffff {
						/* (H5_CHECKED_ASSIGN to a uint32_t) */
						return nil, errorf(H5E_OHDR, H5E_BADVALUE, "single chunk size %d out of range", nbytes)
					}
					mesg.singleNbytes = nbytes
					mesg.singleFilterMask = p.uint32()
				}

			case h5dChunkIdxFarray:
				/* Fixed array creation parameters */
				if p.overflow(1) {
					return nil, errDecodeOverflow()
				}
				mesg.farrayMaxDblkPageNelmtsBits = p.uint8()
				if 0 == mesg.farrayMaxDblkPageNelmtsBits {
					return nil, errorf(H5E_OHDR, H5E_BADVALUE, "invalid fixed array creation parameter")
				}

			case h5dChunkIdxEarray:
				/* Extensible array creation parameters */
				cp := &mesg.earrayCparam
				for _, v := range []*uint8{&cp.maxNelmtsBits, &cp.idxBlkElmts, &cp.supBlkMinDataPtrs, &cp.dataBlkMinElmts, &cp.maxDblkPageNelmtsBits} {
					if p.overflow(1) {
						return nil, errDecodeOverflow()
					}
					*v = p.uint8()
					if 0 == *v {
						return nil, errorf(H5E_OHDR, H5E_BADVALUE, "invalid extensible array creation parameter")
					}
				}

			case h5dChunkIdxBt2: /* v2 B-tree index */
				if p.overflow(4) {
					return nil, errDecodeOverflow()
				}
				mesg.bt2Cparam.nodeSize = p.uint32()
				if p.overflow(1) {
					return nil, errDecodeOverflow()
				}
				mesg.bt2Cparam.splitPercent = p.uint8()
				if mesg.bt2Cparam.splitPercent == 0 || mesg.bt2Cparam.splitPercent > 100 {
					return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad value for v2 B-tree split percent value - must be > 0 and <= 100: split percent = %d", mesg.bt2Cparam.splitPercent)
				}
				if p.overflow(1) {
					return nil, errDecodeOverflow()
				}
				mesg.bt2Cparam.mergePercent = p.uint8()
				if mesg.bt2Cparam.mergePercent == 0 || mesg.bt2Cparam.mergePercent > 100 {
					return nil, errorf(H5E_OHDR, H5E_BADVALUE, "bad value for v2 B-tree merge percent value - must be > 0 and <= 100: merge percent = %d", mesg.bt2Cparam.mergePercent)
				}

			default:
				return nil, errorf(H5E_OHDR, H5E_BADVALUE, "Invalid chunk index type")
			}

			/* Chunk index address */
			if p.overflow(int(f.sizeofAddr)) {
				return nil, errDecodeOverflow()
			}
			mesg.chunkIdxAddr = p.addr()
		}

	case h5dVirtual:
		/* Check version */
		if mesg.version < h5oLayoutVersion4 {
			return nil, errorf(H5E_OHDR, H5E_VERSION, "invalid layout version with virtual layout")
		}

		/* Heap information */
		if p.overflow(int(f.sizeofAddr)) {
			return nil, errDecodeOverflow()
		}
		mesg.virtHeapAddr = p.addr()

		/* NOTE: virtual mapping global heap entry address could be undefined */
		if p.overflow(4) {
			return nil, errDecodeOverflow()
		}
		mesg.virtHeapIdx = p.uint32()

		/* Decode heap block if it exists */
		if mesg.virtHeapAddr != haddrUndef {
			virt, err := f.h5oLayoutDecodeVirtual(mesg.virtHeapAddr, mesg.virtHeapIdx)
			if err != nil {
				return nil, err
			}
			mesg.virt = virt
		}

	default:
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "Invalid layout class")
	}
	return mesg, nil
}

// h5oLayoutDecodeVirtual is the part of H5O__layout_decode that reads the
// encoded virtual dataset mapping list from the global heap object
// {addr, idx}.
func (f *File) h5oLayoutDecodeVirtual(addr haddr, idx uint32) (*h5oStorageVirtual, error) {
	/* Read heap */
	heapBlock, err := f.hgRead(h5hgT{addr: addr, idx: idx})
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_READERROR, "Unable to read global heap block")
	}
	blockSize := len(heapBlock)
	p := f.newImage(heapBlock)

	/* Decode the version number of the heap block encoding */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	heapVers := p.uint8()
	if h5oLayoutVdsGhEncVers != heapVers {
		return nil, errorf(H5E_OHDR, H5E_VERSION, "bad version # of encoded VDS heap information, expected %d, got %d", h5oLayoutVdsGhEncVers, heapVers)
	}
	virt := &h5oStorageVirtual{version: uint32(heapVers)}

	/* Number of entries */
	if p.overflow(int(f.sizeofSize)) {
		return nil, errDecodeOverflow()
	}
	tmpHsize := p.length()

	/* Allocate entry list */
	/* (each entry needs at least two NUL-terminated names) */
	if tmpHsize > uint64(p.remaining())/2 {
		return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding - too many virtual mappings (%d)", tmpHsize)
	}
	virt.list = make([]h5oStorageVirtualEnt, tmpHsize)

	/* Decode each entry */
	for i := range virt.list {
		ent := &virt.list[i]

		/* Source file name */
		if p.remaining() <= 0 {
			return nil, errDecodeOverflow()
		}
		n := bytes.IndexByte(p.rest(), 0)
		if n < 0 {
			return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding - unterminated source file name string")
		}
		ent.sourceFileName = string(p.bytes(n))
		p.skip(1)

		/* Source dataset name */
		if p.remaining() <= 0 {
			return nil, errDecodeOverflow()
		}
		n = bytes.IndexByte(p.rest(), 0)
		if n < 0 {
			return nil, errorf(H5E_OHDR, H5E_OVERFLOW, "ran off end of input buffer while decoding - unterminated source dataset name string")
		}
		ent.sourceDsetName = string(p.bytes(n))
		p.skip(1)

		/* Source selection */
		if p.remaining() <= 0 {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding layout")
		}
		if ent.sourceSelect, err = h5sSelectDeserialize(f, p); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "can't decode source space selection")
		}

		/* Virtual selection */
		/* Buffer space must be updated after previous deserialization */
		if p.remaining() <= 0 {
			return nil, errorf(H5E_DATASPACE, H5E_OVERFLOW, "buffer overflow while decoding layout")
		}
		if ent.virtualSelect, err = h5sSelectDeserialize(f, p); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTDECODE, "can't decode virtual space selection")
		}

		/* (Parsing the source names for printf-style format specifiers,
		 * the unlimited-dimension bookkeeping and the mapping checks are
		 * done by the dataset module.) */
	}

	/* Read stored checksum */
	if p.overflow(4) {
		return nil, errDecodeOverflow()
	}
	storedChksum := p.uint32()

	/* Compute checksum */
	if blockSize < 4 {
		return nil, errDecodeOverflow()
	}
	computedChksum := checksumMetadata(heapBlock[:blockSize-4], 0)

	/* Verify checksum */
	if storedChksum != computedChksum {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "incorrect metadata checksum for global heap block")
	}

	/* Verify that the heap block size is correct */
	if p.off != blockSize {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "incorrect heap block size")
	}
	return virt, nil
}
