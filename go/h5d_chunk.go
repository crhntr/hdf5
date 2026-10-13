package hdf5

// Translated from src/H5Dchunk.c (read side): chunked datasets.
//
// Reading a selection of a chunked dataset (H5D__chunk_io_init +
// H5D__chunk_read) first maps the file selection onto the chunks it
// touches (H5D__create_piece_file_map_all / _hyper, or element by element
// for point selections with H5D__piece_file_cb / H5D__piece_mem_cb), giving
// for every such chunk ("piece") the selection within the chunk and the
// corresponding memory selection. Each piece is then read: the chunk is
// looked up in the chunk index (H5D__chunk_lookup), read and unfiltered
// (H5D__chunk_lock), and the selected elements are copied out
// (H5D__select_read over the chunk buffer, as the C library does through
// its "compact" pseudo-layout); a chunk that was never written yields the
// fill value.
//
// The memory selection of a piece is computed with
// h5sSelectProjectIntersection (the projection of the chunk's part of the
// file selection onto the memory selection), which is what the C library's
// element-by-element H5D__piece_mem_cb builds when the file and memory
// selections do not have the same shape.
//
// The chunk cache (H5D_rdcc_t) is not translated: within one read every
// chunk is read at most once, and nothing is kept between reads.
//
// The chunk index classes live in h5d_btree.go (version 1 B-tree),
// h5d_single.go, h5d_none.go and, for the index types whose callbacks are
// in h5d_btree2.go / h5d_earray.go / h5d_farray.go, in the h5dCops*
// adapters at the end of this file.

import (
	"errors"
	"iter"
	"math/bits"
	"sort"
)

// h5dChunkLayout is H5O_layout_chunk_t: the chunk dimensions and the values
// computed from them and the dataspace when the dataset is opened.
type h5dChunkLayout struct {
	idxType       uint8    /* Type of chunk index */
	flags         uint8    /* Chunk layout flags */
	ndims         uint32   /* Num dimensions in chunk (dataspace rank + 1) */
	dim           []uint32 /* Size of chunk in elements (last = datatype size) */
	size          uint32   /* Size of chunk in bytes */
	nchunks       uint64   /* # of chunks in dataset dimensions */
	maxNchunks    uint64   /* # of chunks in dataset maximum dimensions */
	chunks        []uint64 /* # of chunks in each dataset dimension */
	maxChunks     []uint64 /* # of chunks in each dataset's maximum dimension */
	downChunks    []uint64 /* "down" size of number of chunks in each dimension */
	maxDownChunks []uint64 /* "down" size of number of chunks in each max dim */
}

// h5dChunkStorage is H5O_storage_chunk_t: the chunk index of a dataset.
type h5dChunkStorage struct {
	idxType uint8        /* Type of chunk index */
	idxAddr haddr        /* Address of chunk index */
	ops     *h5dChunkOps /* Pointer to chunked storage operations */

	/* u.single */
	singleNbytes     uint32 /* Size of chunk (in file) */
	singleFilterMask uint32 /* Excluded filters for chunk */

	/* u.btree */
	btreeShared *h5dBtreeShared /* Ref-counted info for B-tree nodes */

	/* u.btree2 */
	bt2    *h5b2      /* v2 B-tree handle */
	bt2Ctx *h5dBt2Ctx /* its callback context */

	/* u.earray / u.farray (open array handles) */
	ea *h5dEarrayIdxInfo
	fa *h5dFarrayIdxInfo
}

// h5dChunkOps is H5D_chunk_ops_t: the read-side callbacks of a chunk index
// class.
type h5dChunkOps struct {
	idxType uint8
	/* Initialize indexing information */
	init func(f *File, dset *h5dShared) error
	/* Query routine to determine if storage/index is allocated */
	isSpaceAlloc func(sc *h5dChunkStorage) bool
	/* Get the address of a chunk */
	getAddr func(f *File, dset *h5dShared, udata *h5dChunkUd) error
	/* Iterate over the chunks (cb may return errBIterStop) */
	iterate func(f *File, dset *h5dShared, cb func(rec *h5dChunkRec) error) error
}

// h5dChunkUd is H5D_chunk_ud_t: the information about a chunk passed to
// and returned by an index lookup.
type h5dChunkUd struct {
	/* Downward */
	scaled []uint64 /* Scaled coordinates of the chunk (rank+1 entries, last 0) */

	/* Upward */
	chunkBlock struct {
		offset haddr  /* Offset of chunk in the file */
		length uint64 /* Size of chunk in the file */
	}
	filterMask uint32 /* Excluded filters */
	chunkIdx   uint64 /* Chunk index for EA, FA indexing */
}

// h5dPieceInfo is H5D_piece_info_t: the part of a read that falls in one
// chunk.
type h5dPieceInfo struct {
	index       uint64   /* "Index" of chunk in dataset */
	piecePoints uint64   /* Number of elements selected in piece */
	scaled      []uint64 /* Scaled coordinates of chunk (rank+1 entries) */
	fspace      *h5s     /* Dataspace describing chunk & selection in it */
	mspace      *h5s     /* Dataspace describing selection in memory corresponding to this chunk */

	/* (point selections: the points of the file and memory selections,
	 *  collected by H5D__piece_file_cb / H5D__piece_mem_cb) */
	filePoints [][]uint64
	memPoints  [][]uint64
}

// h5dChunkMap is H5D_chunk_map_t: the pieces of a read.
type h5dChunkMap struct {
	fNdims   int      /* Number of dimensions for file dataspace */
	chunkDim []uint64 /* Size of chunk in each dimension */
	pieces   []*h5dPieceInfo
}

/*-------------------------------------------------------------------------
 * Chunk layout set up
 *-------------------------------------------------------------------------
 */

// h5dChunkSetInfoReal is H5D__chunk_set_info_real: set the information
// about chunks for a dataset.
func h5dChunkSetInfoReal(layout *h5dChunkLayout, ndims int, currDims, maxDims []uint64) error {
	/* Can happen when corrupt files are parsed */
	if ndims == 0 {
		return errorf(H5E_DATASET, H5E_BADVALUE, "number of dimensions cannot be zero")
	}
	if len(currDims) < ndims || len(maxDims) < ndims || len(layout.dim) < ndims {
		return errorf(H5E_DATASET, H5E_BADVALUE, "dimension arrays shorter than the rank")
	}

	/* Compute the # of chunks in dataset dimensions */
	layout.nchunks = 1
	layout.maxNchunks = 1
	layout.chunks = make([]uint64, ndims)
	layout.maxChunks = make([]uint64, ndims)
	for u := 0; u < ndims; u++ {
		/* Sanity check */
		if layout.dim[u] == 0 {
			return errorf(H5E_DATASET, H5E_BADVALUE, "dimension size must be > 0, dim = %d ", u)
		}
		d := uint64(layout.dim[u])

		/* Round up to the next integer # of chunks, to accommodate partial chunks */
		layout.chunks[u] = currDims[u]/d + boolU64(currDims[u]%d != 0)
		if maxDims[u] == h5sUnlimited {
			layout.maxChunks[u] = h5sUnlimited
		} else {
			layout.maxChunks[u] = maxDims[u]/d + boolU64(maxDims[u]%d != 0)
		}

		/* Accumulate the # of chunks */
		layout.nchunks *= layout.chunks[u]
		layout.maxNchunks *= layout.maxChunks[u]
	}

	/* Get the "down" sizes for each dimension */
	layout.downChunks = make([]uint64, ndims)
	layout.maxDownChunks = make([]uint64, ndims)
	h5vmArrayDown(ndims, layout.chunks, layout.downChunks)
	h5vmArrayDown(ndims, layout.maxChunks, layout.maxDownChunks)
	return nil
}

func boolU64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// h5dChunkSetInfo is H5D__chunk_set_info: set the information about chunks
// for a dataset.
func h5dChunkSetInfo(dset *h5dShared) error {
	/* Set the base layout information */
	if err := h5dChunkSetInfoReal(&dset.chunk, dset.ndims, dset.currDims, dset.maxDims); err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTSET, "can't set layout's chunk info")
	}
	/* (The index's "resize" callback (extensible arrays) is run when the
	 *  array is opened, see h5dCopsEarray.) */
	return nil
}

// h5dChunkSetSizes is H5D__chunk_set_sizes: set the last chunk dimension
// to the datatype size and compute the chunk size in bytes.
func h5dChunkSetSizes(dset *h5dShared) error {
	l := &dset.chunk
	if l.ndims < 1 || int(l.ndims) > len(l.dim) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "invalid chunk dimensionality")
	}

	/* Set the last dimension of the chunk size to the size of the datatype */
	if dset.typ.Size > 0xffffffff {
		return errorf(H5E_DATASET, H5E_CANTINIT, "datatype too large for chunked storage")
	}
	l.dim[l.ndims-1] = uint32(dset.typ.Size)

	/* Compute and store the total size of a chunk */
	/* (Use 64-bit value to ensure that we can detect >4GB chunks) */
	chunkSize := uint64(l.dim[0])
	for u := uint32(1); u < l.ndims; u++ {
		hi, lo := bits.Mul64(chunkSize, uint64(l.dim[u]))
		if hi != 0 {
			return errorf(H5E_DATASET, H5E_CANTINIT, "chunk size must be < 4GB")
		}
		chunkSize = lo
	}

	/* Check for chunk larger than can be represented in 32-bits */
	/* (Chunk size is encoded in 32-bit value in v1 B-tree records) */
	if chunkSize > 0xffffffff {
		return errorf(H5E_DATASET, H5E_CANTINIT, "chunk size must be < 4GB")
	}
	l.size = uint32(chunkSize)
	return nil
}

// h5dLopsChunk is H5D_LOPS_CHUNK: chunked storage layout I/O ops.
func h5dLopsChunk() *h5dLayoutOps {
	return &h5dLayoutOps{
		name:         "chunked",
		init:         h5dChunkInit,
		isSpaceAlloc: h5dChunkIsSpaceAlloc,
		read:         h5dChunkRead,
	}
}

// h5dChunkInit is H5D__chunk_init: initialize the chunk information of a
// dataset (the chunk dimensions from the layout message, the index and the
// number of chunks).
func h5dChunkInit(f *File, dset *h5dShared) error {
	layout := &dset.layout
	sc := &dset.chunkSt

	/* (The layout message's H5O_layout_chunk_t, as H5O__layout_decode sets it) */
	ndims := layout.chunkNdims
	if ndims < 2 || ndims > h5oLayoutNdims || int(ndims) > len(layout.chunkDim) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "bad # of chunk dimensions %d", ndims)
	}
	/* (The dataspace rank must match the chunk rank) */
	if int(ndims)-1 != dset.ndims {
		return errorf(H5E_DATASET, H5E_BADVALUE, "dimensionality of chunks doesn't match the dataspace")
	}
	dset.chunk = h5dChunkLayout{
		idxType: layout.chunkIdxType,
		flags:   layout.chunkFlags,
		ndims:   ndims,
		dim:     make([]uint32, ndims),
	}
	size := uint64(1)
	for u := uint32(0); u < ndims; u++ {
		if layout.chunkDim[u] == 0 || layout.chunkDim[u] > 0xffffffff {
			return errorf(H5E_DATASET, H5E_BADVALUE, "chunk size must be > 0 and < 2^32, dim = %d ", u)
		}
		dset.chunk.dim[u] = uint32(layout.chunkDim[u])
		size *= layout.chunkDim[u]
		if size > 0xffffffff {
			return errorf(H5E_DATASET, H5E_BADVALUE, "chunk size must be < 4GB")
		}
	}
	dset.chunk.size = uint32(size)
	sc.idxType = layout.chunkIdxType
	sc.idxAddr = layout.chunkIdxAddr
	sc.singleNbytes = uint32(layout.singleNbytes)
	sc.singleFilterMask = layout.singleFilterMask
	if sc.ops == nil {
		return errorf(H5E_DATASET, H5E_BADVALUE, "chunk index operations not set up")
	}

	/* Allocate any indexing structures */
	if sc.ops.init != nil {
		if err := sc.ops.init(f, dset); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't initialize indexing information")
		}
	}

	/* Set the number of chunks in dataset, etc. */
	if err := h5dChunkSetInfo(dset); err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to set # of chunks for dataset")
	}
	return nil
}

// h5dChunkIsSpaceAlloc is H5D__chunk_is_space_alloc: query if space is
// allocated for the layout.
func h5dChunkIsSpaceAlloc(dset *h5dShared) bool {
	sc := &dset.chunkSt
	/* Query index layer */
	return sc.ops != nil && sc.ops.isSpaceAlloc(sc)
}

/*-------------------------------------------------------------------------
 * Mapping a file selection onto chunks
 *-------------------------------------------------------------------------
 */

// h5dChunkIoInit is H5D__chunk_io_init (with H5D__chunk_io_init_selections):
// build the chunk map of a read.
func h5dChunkIoInit(dinfo *h5dDsetIoInfo) (*h5dChunkMap, error) {
	dset := dinfo.dset
	fm := &h5dChunkMap{fNdims: dset.ndims}

	/* Get dim number and dimensionality for each dataspace */
	if int(dinfo.fileSpace.extent.rank) != fm.fNdims {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "file dataspace rank does not match the dataset")
	}

	/* Normalize the chunk dimensions (without the datatype size) */
	fm.chunkDim = make([]uint64, fm.fNdims)
	for u := range fm.chunkDim {
		fm.chunkDim[u] = uint64(dset.chunk.dim[u])
	}

	/* Special case for only one element in selection */
	/* (usually appending a record) */
	if dinfo.nelmts == 1 && h5sGetSelectType(dinfo.fileSpace) != h5sSelAll {
		/* Set up chunk mapping for single element */
		if err := h5dCreatePieceMapSingle(dinfo, fm); err != nil {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to create chunk selections for single element")
		}
		return fm, nil
	}

	/* Get type of selection on disk & in memory */
	switch fselType := h5sGetSelectType(dinfo.fileSpace); fselType {
	case h5sSelAll:
		if err := h5dCreatePieceFileMapAll(dinfo, fm); err != nil {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to create file chunk selections")
		}
	case h5sSelHyperslabs:
		if err := h5dCreatePieceFileMapHyper(dinfo, fm); err != nil {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to create file chunk selections")
		}
	case h5sSelPoints, h5sSelNone:
		/* Spaces might not be the same shape, iterate over the file selection directly */
		if err := h5dCreatePieceMapPoints(dinfo, fm); err != nil {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to create file chunk selections")
		}
		return fm, nil
	default:
		return nil, errorf(H5E_DATASET, H5E_BADSELECT, "unable to get type of selection")
	}

	/* Build the memory selection for each chunk */
	if err := h5dCreatePieceMemMap(dinfo, fm); err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to create memory chunk selections")
	}
	return fm, nil
}

// h5dChunkIndexScaled is H5VM_chunk_index_scaled: the scaled coordinates
// and linear index of the chunk holding the element at coords.
func h5dChunkIndexScaled(dset *h5dShared, coords []uint64) (scaled []uint64, index uint64) {
	ndims := dset.ndims
	scaled = make([]uint64, ndims+1)
	for u := 0; u < ndims; u++ {
		scaled[u] = coords[u] / uint64(dset.chunk.dim[u])
	}
	return scaled, h5vmArrayOffsetPre(ndims, dset.chunk.downChunks, scaled)
}

// h5dCreatePieceMapSingle is H5D__create_piece_map_single: create the
// piece of a read of a single element.
func h5dCreatePieceMapSingle(dinfo *h5dDsetIoInfo, fm *h5dChunkMap) error {
	dset := dinfo.dset

	/* Get coordinate for selection */
	selStart, _, err := h5sGetSelectBounds(dinfo.fileSpace)
	if err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_CANTGET, "can't get file selection bound info")
	}

	/* Initialize the 'single piece' file & memory piece information */
	pieceInfo := &h5dPieceInfo{piecePoints: 1}

	/* Set chunk location & hyperslab size */
	coords := make([]uint64, fm.fNdims)
	pieceInfo.scaled, pieceInfo.index = h5dChunkIndexScaled(dset, selStart)
	inChunk := make([]uint64, fm.fNdims)
	for u := 0; u < fm.fNdims; u++ {
		coords[u] = pieceInfo.scaled[u] * fm.chunkDim[u]
		inChunk[u] = selStart[u] - coords[u]
	}

	/* Copy selection for file's dataspace into chunk dataspace */
	/* Move selection back to have correct offset in chunk */
	fspace, err := h5sCreateSimple(fm.chunkDim, nil)
	if err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_CANTCOPY, "unable to copy file selection")
	}
	h5sSelectElements(fspace, [][]uint64{inChunk})
	pieceInfo.fspace = fspace

	/* Just point at the memory dataspace & selection */
	pieceInfo.mspace = dinfo.memSpace

	fm.pieces = append(fm.pieces, pieceInfo)
	return nil
}

// h5dCreatePieceFileMapAll is H5D__create_piece_file_map_all: create all
// chunk selections in the file, for an "all" selection.
func h5dCreatePieceFileMapAll(dinfo *h5dDsetIoInfo, fm *h5dChunkMap) error {
	/* Get number of elements selected in file */
	selPoints := dinfo.nelmts

	/* Get dataspace dimensions */
	fileDims := dinfo.fileSpace.extent.size
	ndims := fm.fNdims

	/* Set initial chunk location, partial dimensions, etc */
	scaled := make([]uint64, ndims)
	coords := make([]uint64, ndims)
	end := make([]uint64, ndims)
	partialDimSize := make([]uint64, ndims)
	currPartialClip := make([]uint64, ndims)
	isPartialDim := make([]bool, ndims)
	numPartialDims := 0
	for u := 0; u < ndims; u++ {
		/* Validate this chunk dimension */
		if fm.chunkDim[u] == 0 {
			return errorf(H5E_DATASET, H5E_BADVALUE, "chunk size must be > 0, dim = %d ", u)
		}

		/* Set up start / end coordinates for first chunk */
		end[u] = fm.chunkDim[u] - 1

		/* Initialize partial chunk dimension information */
		partialDimSize[u] = fileDims[u] % fm.chunkDim[u]
		if fileDims[u] < fm.chunkDim[u] {
			currPartialClip[u] = partialDimSize[u]
			isPartialDim[u] = true
			numPartialDims++
		} else {
			currPartialClip[u] = fm.chunkDim[u]
			isPartialDim[u] = false
		}
	}

	/* Set the index of this chunk */
	var chunkIndex uint64

	/* Iterate through each chunk in the dataset */
	for selPoints > 0 {
		/* Add temporary chunk to the list of pieces */
		pieceInfo := &h5dPieceInfo{index: chunkIndex}

		/* Set the file chunk dataspace */
		fspace, err := h5sCreateSimple(fm.chunkDim, nil)
		if err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTCREATE, "unable to create dataspace for chunk")
		}

		/* If there are partial dimensions for this chunk, set the hyperslab for them */
		if numPartialDims > 0 {
			if err := h5sSelectHyperslab(fspace, make([]uint64, ndims), nil, currPartialClip, nil); err != nil {
				return wrapf(err, H5E_DATASET, H5E_CANTSELECT, "can't create chunk selection")
			}
		}
		pieceInfo.fspace = fspace

		/* Copy the chunk's scaled coordinates */
		pieceInfo.scaled = append(append(make([]uint64, 0, ndims+1), scaled...), 0)

		/* Get number of elements selected in chunk */
		chunkPoints := h5sGetSelectNpoints(fspace)
		if chunkPoints == 0 || chunkPoints > selPoints {
			return errorf(H5E_DATASET, H5E_BADVALUE, "inconsistent chunk selection")
		}
		pieceInfo.piecePoints = chunkPoints
		fm.pieces = append(fm.pieces, pieceInfo)

		/* Decrement # of points left in file selection */
		selPoints -= chunkPoints

		/* Advance to next chunk if we are not done */
		if selPoints > 0 {
			/* Increment chunk index */
			chunkIndex++

			/* Set current increment dimension */
			currDim := ndims - 1

			/* Increment chunk location in fastest changing dimension */
			coords[currDim] += fm.chunkDim[currDim]
			scaled[currDim]++
			end[currDim] += fm.chunkDim[currDim]

			/* Bring chunk location back into bounds, if necessary */
			if coords[currDim] >= fileDims[currDim] {
				for {
					/* Reset current dimension's location to 0 */
					coords[currDim] = 0
					scaled[currDim] = 0
					end[currDim] = fm.chunkDim[currDim] - 1

					/* Check for previous partial chunk in this dimension */
					if isPartialDim[currDim] && end[currDim] < fileDims[currDim] {
						/* Reset partial chunk information for this dimension */
						currPartialClip[currDim] = fm.chunkDim[currDim]
						isPartialDim[currDim] = false
						numPartialDims--
					}

					/* Decrement current dimension */
					currDim--

					/* Check for valid current dim */
					if currDim >= 0 {
						/* Increment chunk location in current dimension */
						coords[currDim] += fm.chunkDim[currDim]
						scaled[currDim]++
						end[currDim] = (coords[currDim] + fm.chunkDim[currDim]) - 1
					}
					if !(currDim >= 0 && coords[currDim] >= fileDims[currDim]) {
						break
					}
				}
			}

			/* Check for valid current dim */
			if currDim >= 0 {
				/* Check for partial chunk in this dimension */
				if !isPartialDim[currDim] && fileDims[currDim] <= end[currDim] {
					/* Set partial chunk information for this dimension */
					currPartialClip[currDim] = partialDimSize[currDim]
					isPartialDim[currDim] = true
					numPartialDims++
				}
			} else {
				return errorf(H5E_DATASET, H5E_BADVALUE, "ran out of chunks before the end of the selection")
			}
		}
	}
	return nil
}

// h5dChunkBlockSpace returns a copy of space with the block start..start+
// dim-1 (clipped to the extent) selected: the region of the chunk at start.
func h5dChunkBlockSpace(space *h5s, start, dim []uint64) (*h5s, error) {
	ret := h5sCopy(space)
	ret.sel = nil
	block := make([]uint64, len(start))
	for u := range block {
		block[u] = min(dim[u], space.extent.size[u]-start[u])
	}
	ones := h5sHyperOnes(len(start))
	if err := h5sSelectHyperslab(ret, start, nil, ones, block); err != nil {
		return nil, err
	}
	return ret, nil
}

// h5dSelectAndBlock returns the part of the selection of space that lies
// in the block start..start+dim-1 (H5S_combine_hyperslab(space,
// H5S_SELECT_AND, start, NULL, dim, NULL)), with space's extent.
func h5dSelectAndBlock(space *h5s, start, dim []uint64) (*h5s, error) {
	blk, err := h5dChunkBlockSpace(space, start, dim)
	if err != nil {
		return nil, err
	}
	/* (The intersection of a selection with a block is the projection of
	 *  the selection onto itself restricted to the block) */
	return h5sSelectProjectIntersection(space, space, blk)
}

// h5dCreatePieceFileMapHyper is H5D__create_piece_file_map_hyper: create
// all chunk selections in the file, for a hyperslab selection.
func h5dCreatePieceFileMapHyper(dinfo *h5dDsetIoInfo, fm *h5dChunkMap) error {
	dset := dinfo.dset
	ndims := fm.fNdims

	/* Get number of elements selected in file */
	selPoints := dinfo.nelmts

	/* Get bounding box for selection (to reduce the number of chunks to iterate over) */
	selStart, selEnd, err := h5sGetSelectBounds(dinfo.fileSpace)
	if err != nil {
		return wrapf(err, H5E_DATASPACE, H5E_CANTGET, "can't get file selection bound info")
	}

	/* Set initial chunk location & hyperslab size */
	startScaled := make([]uint64, ndims)
	scaled := make([]uint64, ndims)
	startCoords := make([]uint64, ndims)
	coords := make([]uint64, ndims)
	end := make([]uint64, ndims)
	for u := 0; u < ndims; u++ {
		/* Validate this chunk dimension */
		if fm.chunkDim[u] == 0 {
			return errorf(H5E_DATASET, H5E_BADVALUE, "chunk size must be > 0, dim = %d ", u)
		}
		scaled[u] = selStart[u] / fm.chunkDim[u]
		startScaled[u] = scaled[u]
		coords[u] = scaled[u] * fm.chunkDim[u]
		startCoords[u] = coords[u]
		end[u] = (coords[u] + fm.chunkDim[u]) - 1
	}

	/* Calculate the index of this chunk */
	chunkIndex := h5vmArrayOffsetPre(ndims, dset.chunk.downChunks, scaled)

	/* Iterate through each chunk in the dataset */
	for selPoints > 0 {
		/* Check for intersection of current chunk and file selection */
		intersect, err := h5sSelectIntersectBlock(dinfo.fileSpace, coords, end)
		if err != nil {
			return wrapf(err, H5E_DATASPACE, H5E_CANTCOMPARE, "can't check chunk intersection")
		}
		if intersect {
			/* Create dataspace for chunk, 'AND'ing the overall selection with
			 *  the current chunk.
			 */
			tmpFchunk, err := h5dSelectAndBlock(dinfo.fileSpace, coords, fm.chunkDim)
			if err != nil {
				return wrapf(err, H5E_DATASPACE, H5E_CANTCOPY, "unable to combine file space selection with chunk block")
			}

			/* Move selection back to have correct offset in chunk */
			if tmpFchunk, err = h5sSelectAdjustU(tmpFchunk, coords); err != nil {
				return wrapf(err, H5E_DATASET, H5E_CANTSELECT, "can't adjust chunk selection")
			}

			/* Resize chunk's dataspace dimensions to size of chunk */
			chunkSpace, err := h5sCreateSimple(fm.chunkDim, nil)
			if err != nil {
				return wrapf(err, H5E_DATASET, H5E_CANTSELECT, "can't adjust chunk dimensions")
			}
			chunkSpace.sel = tmpFchunk.sel

			/* Add temporary chunk to the list of chunks */
			pieceInfo := &h5dPieceInfo{
				index:  chunkIndex,
				fspace: chunkSpace,
				scaled: append(append(make([]uint64, 0, ndims+1), scaled...), 0),
			}

			/* Get number of elements selected in chunk */
			chunkPoints := h5sGetSelectNpoints(chunkSpace)
			if chunkPoints > selPoints {
				return errorf(H5E_DATASET, H5E_BADVALUE, "inconsistent chunk selection")
			}
			pieceInfo.piecePoints = chunkPoints
			if chunkPoints > 0 {
				fm.pieces = append(fm.pieces, pieceInfo)
			}

			/* Decrement # of points left in file selection */
			selPoints -= chunkPoints

			/* Leave if we are done */
			if selPoints == 0 {
				return nil
			}
		}

		/* Increment chunk index */
		chunkIndex++

		/* Set current increment dimension */
		currDim := ndims - 1

		/* Increment chunk location in fastest changing dimension */
		coords[currDim] += fm.chunkDim[currDim]
		end[currDim] += fm.chunkDim[currDim]
		scaled[currDim]++

		/* Bring chunk location back into bounds, if necessary */
		if coords[currDim] > selEnd[currDim] {
			for {
				/* Reset current dimension's location to 0 */
				scaled[currDim] = startScaled[currDim]
				coords[currDim] = startCoords[currDim]
				end[currDim] = (coords[currDim] + fm.chunkDim[currDim]) - 1

				/* Decrement current dimension */
				currDim--

				/* Check for valid current dim */
				if currDim >= 0 {
					/* Increment chunk location in current dimension */
					scaled[currDim]++
					coords[currDim] += fm.chunkDim[currDim]
					end[currDim] = (coords[currDim] + fm.chunkDim[currDim]) - 1
				}
				if !(currDim >= 0 && coords[currDim] > selEnd[currDim]) {
					break
				}
			}
			if currDim < 0 {
				return errorf(H5E_DATASET, H5E_BADVALUE, "ran out of chunks before the end of the selection")
			}

			/* Re-calculate the index of this chunk */
			chunkIndex = h5vmArrayOffsetPre(ndims, dset.chunk.downChunks, scaled)
		}
	}
	return nil
}

// h5dCreatePieceMemMap builds the memory selection of every piece of a
// hyperslab or "all" file selection: the projection of the piece's part of
// the file selection onto the memory selection (what H5D__piece_mem_cb
// computes element by element; for a single piece the memory selection is
// the whole memory selection, as in H5D__create_piece_mem_map_1d).
func h5dCreatePieceMemMap(dinfo *h5dDsetIoInfo, fm *h5dChunkMap) error {
	/* Check for all I/O going to a single chunk */
	if len(fm.pieces) == 1 {
		/* Just point at the memory dataspace & selection */
		fm.pieces[0].mspace = dinfo.memSpace
		return nil
	}
	for _, piece := range fm.pieces {
		coords := make([]uint64, fm.fNdims)
		for u := range coords {
			coords[u] = piece.scaled[u] * fm.chunkDim[u]
		}
		blk, err := h5dChunkBlockSpace(dinfo.fileSpace, coords, fm.chunkDim)
		if err != nil {
			return wrapf(err, H5E_DATASPACE, H5E_CANTSELECT, "can't create chunk block selection")
		}
		mspace, err := h5sSelectProjectIntersection(dinfo.fileSpace, dinfo.memSpace, blk)
		if err != nil {
			return wrapf(err, H5E_DATASPACE, H5E_CANTCLIP, "can't project chunk selection onto memory space")
		}
		if h5sGetSelectNpoints(mspace) != piece.piecePoints {
			return errorf(H5E_DATASPACE, H5E_BADVALUE, "memory selection of chunk has %d elements, file selection %d", h5sGetSelectNpoints(mspace), piece.piecePoints)
		}
		piece.mspace = mspace
	}
	return nil
}

// h5dCreatePieceMapPoints builds the pieces of a point (or "none") file
// selection element by element: H5D__piece_file_cb adds each point to the
// file selection of its chunk, and H5D__piece_mem_cb the corresponding
// memory element to the chunk's memory selection.
func h5dCreatePieceMapPoints(dinfo *h5dDsetIoInfo, fm *h5dChunkMap) error {
	dset := dinfo.dset
	byIndex := map[uint64]*h5dPieceInfo{}
	var (
		lastIndex uint64 = ^uint64(0)
		last      *h5dPieceInfo
	)

	/* Iterate over the memory selection alongside the file selection */
	nextMem, stopMem := iter.Pull(dinfo.memSpace.iterPoints())
	defer stopMem()

	for coords := range dinfo.fileSpace.iterPoints() {
		/* Calculate the index of this chunk */
		scaled, chunkIndex := h5dChunkIndexScaled(dset, coords)

		/* Find correct chunk in file & memory skip list */
		pieceInfo := last
		if chunkIndex != lastIndex {
			pieceInfo = byIndex[chunkIndex]
			if pieceInfo == nil {
				/* Allocate the file & memory chunk information */
				pieceInfo = &h5dPieceInfo{index: chunkIndex, scaled: scaled}
				byIndex[chunkIndex] = pieceInfo
				fm.pieces = append(fm.pieces, pieceInfo)
			}

			/* Update the "last chunk seen" information */
			lastIndex, last = chunkIndex, pieceInfo
		}

		/* Get the offset of the element within the chunk */
		coordsInChunk := make([]uint64, fm.fNdims)
		for u := 0; u < fm.fNdims; u++ {
			coordsInChunk[u] = coords[u] - pieceInfo.scaled[u]*fm.chunkDim[u]
		}

		/* Add point to file selection for chunk */
		pieceInfo.filePoints = append(pieceInfo.filePoints, coordsInChunk)

		/* Increment the number of elemented selected in chunk */
		pieceInfo.piecePoints++

		/* Get coordinates of selection iterator for memory */
		coordsInMem, ok := nextMem()
		if !ok {
			return errorf(H5E_DATASPACE, H5E_CANTNEXT, "unable to move to next iterator location")
		}

		/* Add point to memory selection for chunk */
		pieceInfo.memPoints = append(pieceInfo.memPoints, append([]uint64(nil), coordsInMem...))
	}

	/* Build the chunk dataspaces */
	sort.Slice(fm.pieces, func(i, j int) bool { return fm.pieces[i].index < fm.pieces[j].index })
	for _, pieceInfo := range fm.pieces {
		fspace, err := h5sCreateSimple(fm.chunkDim, nil)
		if err != nil {
			return wrapf(err, H5E_DATASPACE, H5E_CANTCREATE, "unable to create dataspace for chunk")
		}
		h5sSelectElements(fspace, pieceInfo.filePoints)
		pieceInfo.fspace = fspace

		mspace := h5sCopy(dinfo.memSpace)
		mspace.sel = nil
		h5sSelectElements(mspace, pieceInfo.memPoints)
		pieceInfo.mspace = mspace
		pieceInfo.filePoints, pieceInfo.memPoints = nil, nil
	}
	return nil
}

/*-------------------------------------------------------------------------
 * Reading
 *-------------------------------------------------------------------------
 */

// h5dChunkRead is H5D__chunk_read: read the selected elements of a chunked
// dataset.
func h5dChunkRead(f *File, dinfo *h5dDsetIoInfo) error {
	dset := dinfo.dset

	/* (H5D__chunk_io_init) */
	fm, err := h5dChunkIoInit(dinfo)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't initialize I/O info")
	}

	skipMissingChunks := false /* Whether to skip missing chunks */
	{
		fill := &dset.dcplCache.fill /* Fill value info */

		/* Check the fill value status */
		fillStatus := h5pIsFillValueDefined(fill)

		/* If we are never to return fill values, or if we would return them
		 * but they aren't set, set the flag to skip missing chunks.
		 */
		if fill.fillTime == h5dFillTimeNever ||
			(fill.fillTime == h5dFillTimeIfset && fillStatus != h5dFillValueUserDefined &&
				fillStatus != h5dFillValueDefault) {
			skipMissingChunks = true
		}
	}

	/* Iterate through nodes in chunk skip list */
	for _, chunkInfo := range fm.pieces {
		/* Get the info for the chunk in the file */
		udata, err := f.h5dChunkLookup(dset, chunkInfo.scaled)
		if err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTGET, "error looking up chunk address")
		}

		/* Sanity check */
		if addrDefined(udata.chunkBlock.offset) != (udata.chunkBlock.length > 0) {
			return errorf(H5E_DATASET, H5E_BADVALUE, "chunk index entry with address %d has %d bytes", udata.chunkBlock.offset, udata.chunkBlock.length)
		}

		/* Set up the I/O info object for this chunk */
		chk := *dinfo
		chk.fileSpace = chunkInfo.fspace
		chk.memSpace = chunkInfo.mspace
		chk.nelmts = chunkInfo.piecePoints

		/* Check for non-existent chunk & skip it if appropriate */
		if addrDefined(udata.chunkBlock.offset) {
			/* Lock the chunk into the cache */
			chunk, err := f.h5dChunkLock(dset, udata, chunkInfo.scaled)
			if err != nil {
				return wrapf(err, H5E_IO, H5E_READERROR, "unable to read raw data chunk")
			}

			/* Set up the storage buffer information for this chunk */
			/* Point I/O info at compact I/O info for this chunk */
			chk.compactBuf = chunk
			chk.readvv = h5dCompactReadvv

			/* Perform the actual read operation */
			if err := h5dSelectRead(&chk); err != nil {
				return wrapf(err, H5E_DATASET, H5E_READERROR, "chunked read failed")
			}
		} else if !skipMissingChunks {
			/* Perform the actual read operation from the nonexistent chunk
			 */
			if err := h5dNonexistentRead(f, &chk); err != nil {
				return wrapf(err, H5E_DATASET, H5E_READERROR, "chunked read failed")
			}
		}
	}
	return nil
}

// h5dChunkLookup is H5D__chunk_lookup: look up the address, size and filter
// mask of the chunk at the scaled coordinates (rank+1 entries).
func (f *File) h5dChunkLookup(dset *h5dShared, scaled []uint64) (*h5dChunkUd, error) {
	sc := &dset.chunkSt
	if sc.ops == nil {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "chunk index operations not set up")
	}
	if len(scaled) < dset.ndims {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "chunk coordinates shorter than the rank")
	}

	/* Initialize the query information about the chunk we are looking for */
	udata := &h5dChunkUd{scaled: make([]uint64, dset.ndims+1)}
	copy(udata.scaled, scaled[:dset.ndims])

	/* Reset information about the chunk we are looking for */
	udata.chunkBlock.offset = haddrUndef
	udata.chunkBlock.length = 0
	udata.filterMask = 0

	/* Go get the chunk information */
	if err := sc.ops.getAddr(f, dset, udata); err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't query chunk address")
	}
	return udata, nil
}

// h5dChunkIsPartialEdgeChunk is H5D__chunk_is_partial_edge_chunk: whether
// the chunk at scaled extends past the dataset's current dimensions.
func h5dChunkIsPartialEdgeChunk(dsetNdims int, chunkDims []uint32, scaled, dsetDims []uint64) bool {
	/* check if this is a partial edge chunk */
	for u := 0; u < dsetNdims; u++ {
		if (scaled[u]+1)*uint64(chunkDims[u]) > dsetDims[u] {
			return true
		}
	}
	return false
}

// h5dChunkLock is the read part of H5D__chunk_lock: read the chunk
// described by udata from the file and run it through the filter pipeline,
// returning the chunk's layout.size bytes.
func (f *File) h5dChunkLock(dset *h5dShared, udata *h5dChunkUd, scaled []uint64) ([]byte, error) {
	pline := &dset.dcplCache.pline
	chunkSize := uint64(dset.chunk.size)

	/* Save the chunk info so the cache stays consistent */
	chunkAddr := udata.chunkBlock.offset
	chunkAlloc := udata.chunkBlock.length

	/* Check if we should disable filters on this chunk */
	oldPline := pline
	if pline.nused > 0 {
		if dset.chunk.flags&h5oLayoutChunkDontFilterPartialBoundChunks != 0 {
			/* Check if this is an edge chunk */
			if h5dChunkIsPartialEdgeChunk(dset.ndims, dset.chunk.dim, scaled, dset.currDims) {
				/* Disable the filters for both writing and reading */
				oldPline = nil
			}
		}
	}

	/* Check if the chunk exists on disk */
	if !addrDefined(chunkAddr) {
		return nil, errorf(H5E_DATASET, H5E_BADVALUE, "chunk is not allocated")
	}

	/* Chunk size on disk isn't [likely] the same size as the final chunk
	 * size in memory, so allocate memory big enough. */
	if chunkAlloc == 0 || chunkAlloc > f.eof() {
		return nil, errorf(H5E_IO, H5E_READERROR, "invalid chunk size %d in the chunk index", chunkAlloc)
	}
	chunk, err := f.readAt(chunkAddr, int(chunkAlloc))
	if err != nil {
		return nil, wrapf(err, H5E_IO, H5E_READERROR, "unable to read raw data chunk")
	}

	if oldPline != nil && oldPline.nused > 0 {
		if chunk, err = h5zPipeline(oldPline, udata.filterMask, chunk); err != nil {
			return nil, wrapf(err, H5E_DATASET, H5E_CANTFILTER, "data pipeline read failed")
		}
	}

	/* (The chunk buffer must hold a whole chunk) */
	if uint64(len(chunk)) < chunkSize {
		return nil, errorf(H5E_DATASET, H5E_BADSIZE, "chunk holds %d bytes, expected %d", len(chunk), chunkSize)
	}
	return chunk[:chunkSize], nil
}

// h5dNonexistentRead is H5D__select_read with H5D_LOPS_NONEXISTENT
// (H5D__nonexistent_readvv): read from a chunk that was never written,
// i.e. fill the memory selection with the fill value. (A chunk the C
// library brings into its chunk cache instead, H5D__chunk_lock, is filled
// the same way: H5D__fill_init followed by H5D__fill_refill_vl.)
func h5dNonexistentRead(f *File, dinfo *h5dDsetIoInfo) error {
	dset := dinfo.dset

	/* Initialize the fill value buffer */
	fill, hasVlenFillType, err := h5dFillInit(&dset.dcplCache.fill, dset.typ)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't initialize fill buffer info")
	}

	/* Check for VL datatype & fill the buffer with VL datatype fill values */
	if hasVlenFillType {
		if err := h5dFillRefillVl(f, dset.typ, fill); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTCONVERT, "can't refill fill value buffer")
		}
	}
	if err := h5dFill(dset.dcplCache.fill.buf, dset.typ, dinfo.buf, dinfo.memSpace); err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't initialize fill buffer info")
	}
	return nil
}

// h5dChunkAllocated is H5D__chunk_allocated: the total size of the
// allocated chunks in the file.
func (f *File) h5dChunkAllocated(dset *h5dShared) (uint64, error) {
	var chunkBytes uint64
	sc := &dset.chunkSt

	/* Iterate over the chunks */
	err := sc.ops.iterate(f, dset, func(rec *h5dChunkRec) error {
		chunkBytes += uint64(rec.nbytes)
		return nil
	})
	if err != nil {
		return 0, wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to retrieve allocated chunk information from index")
	}
	return chunkBytes, nil
}

// h5dChunkIter is H5D__chunk_iter: call op for every allocated chunk with
// its logical offset (in elements), filter mask, absolute file address and
// size in the file. op may return errBIterStop to stop early.
func (f *File) h5dChunkIter(dset *h5dShared, op func(offset []uint64, filterMask uint32, addr haddr, nbytes uint64) error) error {
	if dset.layout.typ != h5dChunked {
		return errorf(H5E_DATASET, H5E_BADVALUE, "not a chunked dataset")
	}
	sc := &dset.chunkSt

	/* If the dataset is not written, return without errors */
	if !addrDefined(sc.idxAddr) {
		return nil
	}
	err := sc.ops.iterate(f, dset, func(rec *h5dChunkRec) error {
		/* Similar to H5D__get_chunk_info */
		offset := make([]uint64, dset.ndims)
		for i := range offset {
			if i < len(rec.scaled) {
				offset[i] = rec.scaled[i] * uint64(dset.chunk.dim[i])
			}
		}
		return op(offset, rec.filterMask, f.baseAddr+rec.chunkAddr, uint64(rec.nbytes))
	})
	if errors.Is(err, errBIterStop) {
		return nil
	}
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTNEXT, "chunk iteration failed")
	}
	return nil
}

/*-------------------------------------------------------------------------
 * Index classes whose callbacks live in the files of other modules
 * (H5D_COPS_BT2 in h5d_btree2.go, H5D_COPS_EARRAY in h5d_earray.go,
 * H5D_COPS_FARRAY in h5d_farray.go): adapters to h5dChunkOps.
 *-------------------------------------------------------------------------
 */

// h5dChunkIdxIsSpaceAlloc is the is_space_alloc callback shared by all
// index types: the index exists when its address is defined.
func h5dChunkIdxIsSpaceAlloc(sc *h5dChunkStorage) bool { return addrDefined(sc.idxAddr) }

// h5dCopsBt2 is H5D_COPS_BT2.
func h5dCopsBt2() *h5dChunkOps {
	return &h5dChunkOps{
		idxType:      h5dChunkIdxBt2,
		isSpaceAlloc: h5dChunkIdxIsSpaceAlloc,
		getAddr:      h5dBt2IdxGetAddr,
		iterate:      h5dBt2IdxIterate,
	}
}

// h5dBt2IdxOpenIfNeeded opens the dataset's v2 B-tree chunk index if it is
// not open yet (H5D_BT2_IDX_IS_OPEN / H5D__bt2_idx_open).
func (f *File) h5dBt2IdxOpenIfNeeded(dset *h5dShared) error {
	sc := &dset.chunkSt
	if sc.bt2 != nil {
		return nil
	}
	bt2, ctx, err := h5dBt2IdxOpen(f, &dset.layout)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open v2 B-tree")
	}
	sc.bt2, sc.bt2Ctx = bt2, ctx
	return nil
}

// h5dBt2IdxGetAddr is H5D__bt2_idx_get_addr.
func h5dBt2IdxGetAddr(f *File, dset *h5dShared, udata *h5dChunkUd) error {
	/* Check if the v2 B-tree is open yet */
	if err := f.h5dBt2IdxOpenIfNeeded(dset); err != nil {
		return err
	}
	sc := &dset.chunkSt

	/* Prepare user data for compare callback */
	ndims := dset.chunk.ndims - 1
	bt2Udata := &h5dBt2Ud{ndims: ndims}
	bt2Udata.rec.chunkAddr = haddrUndef

	/* Set the chunk offset to be searched for */
	bt2Udata.rec.scaled = append([]uint64(nil), udata.scaled[:ndims]...)

	/* Go get chunk information from v2 B-tree */
	var foundRec *h5dChunkRec
	found, err := sc.bt2.find(bt2Udata, func(rec any) error {
		r, ok := rec.(*h5dChunkRec)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected v2 B-tree record %T", rec)
		}
		foundRec = r
		return nil
	})
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTFIND, "can't check for chunk in v2 B-tree")
	}

	/* Check if chunk was found */
	if found && foundRec != nil {
		/* Set common info for the chunk */
		udata.chunkBlock.offset = foundRec.chunkAddr

		/* Set other info for the chunk */
		if dset.dcplCache.pline.nused > 0 { /* filtered chunk */
			udata.chunkBlock.length = uint64(foundRec.nbytes)
			udata.filterMask = foundRec.filterMask
		} else { /* non-filtered chunk */
			udata.chunkBlock.length = uint64(dset.chunk.size)
			udata.filterMask = 0
		}
		if !addrDefined(udata.chunkBlock.offset) {
			udata.chunkBlock.length = 0
		}
	} else {
		udata.chunkBlock.offset = haddrUndef
		udata.chunkBlock.length = 0
		udata.filterMask = 0
	}
	return nil
}

// h5dBt2IdxIterate is H5D__bt2_idx_iterate.
func h5dBt2IdxIterate(f *File, dset *h5dShared, cb func(rec *h5dChunkRec) error) error {
	/* Check if the v2 B-tree is open yet */
	if err := f.h5dBt2IdxOpenIfNeeded(dset); err != nil {
		return err
	}
	/* Iterate over the records in the v2 B-tree */
	return dset.chunkSt.bt2.iterate(func(rec any) error {
		r, ok := rec.(*h5dChunkRec)
		if !ok {
			return errorf(H5E_DATASET, H5E_BADTYPE, "unexpected v2 B-tree record %T", rec)
		}
		return cb(r)
	})
}

// h5dCopsEarray is H5D_COPS_EARRAY.
func h5dCopsEarray() *h5dChunkOps {
	return &h5dChunkOps{
		idxType:      h5dChunkIdxEarray,
		isSpaceAlloc: func(sc *h5dChunkStorage) bool { return h5dEarrayIdxIsSpaceAlloc(sc.idxAddr) },
		/* H5D__earray_idx_init, at dataset open: the dataspace must have
		 * exactly one unlimited dimension (the index layout is set up again
		 * on first use) */
		init: func(_ *File, dset *h5dShared) error {
			return h5dEarrayIdxInit(&h5dChunkIdxLayout{}, dset.maxDims)
		},
		getAddr: func(f *File, dset *h5dShared, udata *h5dChunkUd) error {
			sc := &dset.chunkSt
			if sc.ea == nil {
				info, err := f.dEarrayIdxSetup(&dset.layout, &dset.dcplCache.pline, dset.maxDims)
				if err != nil {
					return err
				}
				sc.ea = info
			}
			addr, length, mask, idx, err := h5dEarrayIdxGetAddr(sc.ea, udata.scaled)
			if err != nil {
				return err
			}
			udata.chunkBlock.offset, udata.chunkBlock.length, udata.filterMask, udata.chunkIdx = addr, length, mask, idx
			if !addrDefined(addr) {
				udata.chunkBlock.length = 0
			}
			return nil
		},
		iterate: func(f *File, dset *h5dShared, cb func(rec *h5dChunkRec) error) error {
			return f.dEarrayIdxIterate(&dset.layout, &dset.dcplCache.pline, dset.maxDims, func(scaled []uint64, addr haddr, nbytes uint64, filterMask uint32) error {
				return cb(&h5dChunkRec{scaled: scaled, nbytes: uint32(nbytes), filterMask: filterMask, chunkAddr: addr})
			})
		},
	}
}

// h5dCopsFarray is H5D_COPS_FARRAY.
func h5dCopsFarray() *h5dChunkOps {
	return &h5dChunkOps{
		idxType:      h5dChunkIdxFarray,
		isSpaceAlloc: func(sc *h5dChunkStorage) bool { return h5dFarrayIdxIsSpaceAlloc(sc.idxAddr) },
		getAddr: func(f *File, dset *h5dShared, udata *h5dChunkUd) error {
			sc := &dset.chunkSt
			if sc.fa == nil {
				info, err := f.dFarrayIdxSetup(&dset.layout, &dset.dcplCache.pline, dset.maxDims)
				if err != nil {
					return err
				}
				sc.fa = info
			}
			addr, length, mask, idx, err := h5dFarrayIdxGetAddr(sc.fa, udata.scaled)
			if err != nil {
				return err
			}
			udata.chunkBlock.offset, udata.chunkBlock.length, udata.filterMask, udata.chunkIdx = addr, length, mask, idx
			if !addrDefined(addr) {
				udata.chunkBlock.length = 0
			}
			return nil
		},
		iterate: func(f *File, dset *h5dShared, cb func(rec *h5dChunkRec) error) error {
			return f.dFarrayIdxIterate(&dset.layout, &dset.dcplCache.pline, dset.maxDims, func(scaled []uint64, addr haddr, nbytes uint64, filterMask uint32) error {
				return cb(&h5dChunkRec{scaled: scaled, nbytes: uint32(nbytes), filterMask: filterMask, chunkAddr: addr})
			})
		},
	}
}
