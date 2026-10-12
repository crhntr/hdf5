package hdf5

// Translated from src/H5Dvirtual.c (read side): virtual datasets (VDS).
//
// A virtual dataset maps selections of its own dataspace ("virtual
// selections") onto selections of source datasets, possibly in other files.
// The mappings are decoded from the global heap by the layout message
// decoder (h5oLayoutDecodeVirtual); the post-decode processing the C
// H5O__layout_decode performs (parsing "printf" style source names,
// finding the unlimited dimensions, checking the mappings, computing the
// minimum dimensions) is done here by h5dVirtualInit.
//
// Reading (H5D__virtual_read) projects the requested file selection onto
// each mapping's virtual selection and from there onto the source
// selection, reads those elements from the source dataset (with h5dRead)
// straight into the destination buffer, and fills the elements that no
// mapping covers with the fill value.
//
// The source datasets are opened through dVirtualOpenSource, which locates
// the source file like H5F_prefix_open_file and the source dataset with the
// group traversal (H5D__open_name).
//
// Deviations: the source dataset must have the same datatype size as the
// virtual dataset (the C library converts between the two datatypes; this
// reader keeps the elements in their file datatype, except that atomic
// numeric elements differing only in byte order are swapped), and a source
// dataset in another file must not have variable-length or reference
// elements (their heap references are only meaningful in the source file).

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/HDFGroup/hdf5/go/h5"
)

// H5O_virtual_space_status_t
const (
	h5oVirtualStatusInvalid   = 0 /* Space extent is invalid            */
	h5oVirtualStatusSelBounds = 1 /* Space extent set to bounds of selection */
	h5oVirtualStatusUser      = 2 /* Space extent provided by application */
	h5oVirtualStatusCorrect   = 3 /* Space extent matches dataset       */
)

// H5D_vds_view_t
const (
	h5dVdsFirstMissing  = 0
	h5dVdsLastAvailable = 1
)

// h5dVirtualDefSubDsetSize is H5D_VIRTUAL_DEF_SUB_DSET_SIZE: default size
// of the sub_dset array.
const h5dVirtualDefSubDsetSize = 128

// h5dVirtualMaxSubDsets bounds the number of "printf" source datasets
// searched for one mapping.
const h5dVirtualMaxSubDsets = 1 << 20

// h5dVirtualNameSeg is H5O_storage_virtual_name_seg_t: one segment of a
// parsed source name (the text between two %b block number substitutions).
type h5dVirtualNameSeg struct {
	nameSegment string /* String for this name segment */
	set         bool   /* whether nameSegment was allocated (non-NULL) */
}

// h5dVirtualSrcdset is H5O_storage_virtual_srcdset_t: a source dataset of
// a mapping.
type h5dVirtualSrcdset struct {
	/* Stored */
	virtualSelect *h5s /* Selection in the virtual dataset that is mapped to source selection */

	/* Not stored */
	fileName             string     /* Source file name used for virtual dataset mapping */
	dsetName             string     /* Source dataset name used for virtual dataset mapping */
	namesSet             bool       /* whether fileName/dsetName are resolved */
	clippedSourceSelect  *h5s       /* Clipped version of source_select  */
	clippedVirtualSelect *h5s       /* Clipped version of virtual_select */
	dset                 *h5dShared /* Source dataset                     */
	dsetFile             *File      /* File of the source dataset */
	dsetExists           bool       /* Whether the dataset exists (was opened successfully) */

	/* Temporary - only used during I/O operation, NULL at all other times */
	projectedMemSpace *h5s /* Selection within mem_space for this mapping */
}

// h5dVirtualEnt is H5O_storage_virtual_ent_t: one mapping.
type h5dVirtualEnt struct {
	/* Stored */
	sourceDset     h5dVirtualSrcdset /* Information about the source dataset */
	sourceFileName string            /* Original (unparsed) source file name */
	sourceDsetName string            /* Original (unparsed) source dataset name */
	sourceSelect   *h5s              /* Selection in the source dataset for mapping */

	/* Not stored */
	subDset              []h5dVirtualSrcdset /* Array of sub-source dataset info structs (len = sub_dset_nalloc) */
	subDsetNused         int                 /* Number of slots "used" in sub_dset */
	subDsetIoStart       int                 /* First element in sub_dset involved in current I/O op. */
	subDsetIoEnd         int                 /* First element in sub_dset outside of current I/O op. */
	parsedSourceFileName []h5dVirtualNameSeg /* Parsed version of source_dset.file_name */
	psfnStaticStrlen     int                 /* Length of parsed_source_file_name without block number substitutions */
	psfnNsubs            int                 /* Number of block number substitutions in parsed_source_file_name */
	parsedSourceDsetName []h5dVirtualNameSeg /* Parsed version of source_dset.dset_name */
	psdnStaticStrlen     int                 /* Length of parsed_source_dset_name without block number substitutions */
	psdnNsubs            int                 /* Number of block number substitutions in parsed_source_dset_name */
	unlimDimSource       int                 /* Unlimited dimension in source_select */
	unlimDimVirtual      int                 /* Unlimited dimension in virtual_select */
	unlimExtentSource    uint64              /* Extent of unlimited dimension in source dset last time virtual_select was patched to match selection */
	unlimExtentVirtual   uint64              /* Extent of unlimited dimension in virtual dset last time source_select was patched to match selection */
	clipSizeVirtual      uint64              /* Size selection would be clipped to in virtual selection, ignoring other mappings, when source extent == unlim_extent_source */
	clipSizeSource       uint64              /* Size selection would be clipped to in source selection when virtual extent == unlim_extent_virtual */
	sourceSpaceStatus    int                 /* Extent patching status of source_select */
	virtualSpaceStatus   int                 /* Extent patching status of virtual_select */
}

// h5dVirtualStorage is H5O_storage_virtual_t: the virtual storage of a
// dataset.
type h5dVirtualStorage struct {
	list      []h5dVirtualEnt /* Array of virtual dataset mapping entries */
	minDims   []uint64        /* Minimum extent of VDS (maximum of all non-unlimited selection bounds) */
	view      int             /* Method for calculating the extent of the virtual dataset with unlimited selections */
	printfGap uint64          /* Maximum number of sequential missing source datasets before terminating the search for more */
	init      bool            /* Whether all information has been completely initialized */
}

// h5dLopsVirtual is H5D_LOPS_VIRTUAL: virtual storage layout I/O ops.
func h5dLopsVirtual() *h5dLayoutOps {
	return &h5dLayoutOps{
		name:         "virtual",
		init:         h5dVirtualInit,
		isSpaceAlloc: h5dVirtualIsSpaceAlloc,
		read:         h5dVirtualRead,
	}
}

/*-------------------------------------------------------------------------
 * Dataspace helpers (H5S_extent_copy / H5S_set_extent on the dataspaces
 * held by the mappings, which the C code updates in place)
 *-------------------------------------------------------------------------
 */

// h5dSpaceExtentCopy is H5S_extent_copy(dst, src): give dst (in place) the
// extent of src, keeping its selection.
func h5dSpaceExtentCopy(dst *h5s, src *h5s) error {
	if dst.sel != nil && (dst.sel.typ == h5sSelPoints || dst.sel.typ == h5sSelHyperslabs) && dst.sel.rank != src.extent.rank {
		return errorf(H5E_DATASPACE, H5E_BADRANGE, "selection rank %d does not match the extent rank %d", dst.sel.rank, src.extent.rank)
	}
	dst.extent = src.extent
	dst.extent.size = append([]uint64(nil), src.extent.size...)
	if src.extent.max != nil {
		dst.extent.max = append([]uint64(nil), src.extent.max...)
	}
	if dst.sel != nil && (dst.sel.typ == h5sSelAll || dst.sel.typ == h5sSelNone) {
		sel := *dst.sel
		sel.rank = src.extent.rank
		dst.sel = &sel
	}
	return nil
}

// h5dSpaceSetExtent is H5S_set_extent(space, dims): change the current
// dimensions of space in place.
func h5dSpaceSetExtent(space *h5s, dims []uint64) error {
	rank := int(space.extent.rank)
	if len(dims) < rank {
		return errorf(H5E_DATASPACE, H5E_BADVALUE, "dimension array shorter than the rank")
	}
	nelem := uint64(1)
	size := make([]uint64, rank)
	for u := range rank {
		size[u] = dims[u]
		var err error
		if nelem, err = h5dMulSize(nelem, dims[u]); err != nil {
			return err
		}
	}
	space.extent.size = size
	space.extent.nelem = nelem
	return nil
}

// h5dSpaceFromSelection builds a dataspace for a selection decoded without
// one (H5S_select_deserialize with a NULL space): its extent has the
// selection's rank and zero dimensions until it is patched.
func h5dSpaceFromSelection(sel *h5sSelection) *h5s {
	space := &h5s{extent: h5sExtent{typ: h5.SpaceSimple, rank: sel.rank, size: make([]uint64, sel.rank)}}
	space.sel = sel
	return space
}

/*-------------------------------------------------------------------------
 * Mapping set up (the virtual part of H5O__layout_decode)
 *-------------------------------------------------------------------------
 */

// h5dVirtualCheckMappingPre is H5D_virtual_check_mapping_pre: check that
// the selections of a mapping are legal.
func h5dVirtualCheckMappingPre(vspace, srcSpace *h5s, spaceStatus int) error {
	/* Check for point selections (currently unsupported) */
	if h5sGetSelectType(vspace) == h5sSelPoints {
		return errorf(H5E_DATASET, H5E_UNSUPPORTED, "point selections not currently supported with virtual datasets")
	}
	if h5sGetSelectType(srcSpace) == h5sSelPoints {
		return errorf(H5E_DATASET, H5E_UNSUPPORTED, "point selections not currently supported with virtual datasets")
	}

	/* Get number of elements in spaces */
	nelmtsVs := h5sGetSelectNpoints(vspace)
	nelmtsSs := h5sGetSelectNpoints(srcSpace)

	/* Check for unlimited vspace */
	if nelmtsVs == h5sUnlimited {
		/* Check for unlimited src_space */
		if nelmtsSs == h5sUnlimited {
			/* Non-printf unlimited selection.  Make sure both selections have
			 * the same number of elements in the non-unlimited dimension.  Note
			 * we can always check this even if the space status is invalid
			 * because unlimited selections are never dependent on the extent.
			 */
			nenuVs, err := h5sGetSelectNumElemNonUnlim(vspace)
			if err != nil {
				return wrapf(err, H5E_DATASET, H5E_CANTCOUNT, "can't get number of elements in non-unlimited dimension")
			}
			nenuSs, err := h5sGetSelectNumElemNonUnlim(srcSpace)
			if err != nil {
				return wrapf(err, H5E_DATASET, H5E_CANTCOUNT, "can't get number of elements in non-unlimited dimension")
			}
			if nenuVs != nenuSs {
				return errorf(H5E_ARGS, H5E_BADVALUE, "numbers of elements in the non-unlimited dimensions is different for source and virtual spaces")
			}
		}
		/* We will handle the printf case after parsing the source names */
	} else if spaceStatus != h5oVirtualStatusInvalid {
		/* Limited selections.  Check number of points is the same. */
		if nelmtsVs != nelmtsSs {
			return errorf(H5E_ARGS, H5E_BADVALUE, "virtual and source space selections have different numbers of elements")
		}
	}
	return nil
}

// h5dVirtualCheckMappingPost is H5D_virtual_check_mapping_post: check that
// a mapping is legal, after the mapping is otherwise complete.
func h5dVirtualCheckMappingPost(ent *h5dVirtualEnt) error {
	/* Get number of elements in spaces */
	nelmtsVs := h5sGetSelectNpoints(ent.sourceDset.virtualSelect)
	nelmtsSs := h5sGetSelectNpoints(ent.sourceSelect)

	/* Check for printf selection */
	if nelmtsVs == h5sUnlimited && nelmtsSs != h5sUnlimited {
		/* Make sure there at least one %b substitution in the source file or
		 * dataset name */
		if ent.psfnNsubs == 0 && ent.psdnNsubs == 0 {
			return errorf(H5E_PLIST, H5E_BADVALUE, "unlimited virtual selection, limited source selection, and no printf specifiers in source names")
		}

		/* Make sure virtual space uses hyperslab selection */
		if h5sGetSelectType(ent.sourceDset.virtualSelect) != h5sSelHyperslabs {
			return errorf(H5E_PLIST, H5E_BADVALUE, "virtual selection with printf mapping must be hyperslab")
		}

		/* Check that the number of elements in one block in the virtual
		 * selection matches the total number of elements in the source
		 * selection, if the source space status is not invalid (virtual space
		 * status does not matter here because it is unlimited) */
		if ent.sourceSpaceStatus != h5oVirtualStatusInvalid {
			/* Get first block in virtual selection */
			tmpSpace, err := h5sHyperGetUnlimBlock(ent.sourceDset.virtualSelect, 0)
			if err != nil {
				return wrapf(err, H5E_PLIST, H5E_CANTGET, "can't get first block in virtual selection")
			}

			/* Check number of points */
			if h5sGetSelectNpoints(tmpSpace) != nelmtsSs {
				return errorf(H5E_ARGS, H5E_BADVALUE, "virtual (single block) and source space selections have different numbers of elements")
			}
		}
	} else if ent.psfnNsubs > 0 || ent.psdnNsubs > 0 {
		/* Make sure there are no printf substitutions */
		return errorf(H5E_PLIST, H5E_BADVALUE, "printf specifier(s) in source name(s) without an unlimited virtual selection and limited source selection")
	}
	return nil
}

// h5dVirtualUpdateMinDims is H5D_virtual_update_min_dims: update the
// layout's "min_dims" to take into account the idx'th mapping.
func h5dVirtualUpdateMinDims(virt *h5dVirtualStorage, idx int) error {
	ent := &virt.list[idx]

	/* Get type of selection */
	selType := h5sGetSelectType(ent.sourceDset.virtualSelect)

	/* Do not update min_dims for "all" or "none" selections */
	if selType == h5sSelAll || selType == h5sSelNone {
		return nil
	}

	/* Get rank of vspace */
	rank := int(ent.sourceDset.virtualSelect.extent.rank)

	/* Get selection bounds */
	_, boundsEnd, err := h5sGetSelectBounds(ent.sourceDset.virtualSelect)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to get selection bounds")
	}

	/* Update min_dims */
	for i := 0; i < rank && i < len(virt.minDims); i++ {
		/* Don't check unlimited dimensions in the selection */
		if i != ent.unlimDimVirtual && boundsEnd[i] >= virt.minDims[i] {
			virt.minDims[i] = boundsEnd[i] + 1
		}
	}
	return nil
}

// h5dVirtualCheckMinDims is H5D_virtual_check_min_dims: check that the
// dataset's dimensions are at least the minimum dimensions computed from
// the mappings.
func h5dVirtualCheckMinDims(dset *h5dShared) error {
	/* Verify that dimensions are larger than min_dims */
	for i := 0; i < dset.ndims && i < len(dset.virt.minDims); i++ {
		if dset.currDims[i] < dset.virt.minDims[i] {
			return errorf(H5E_ARGS, H5E_BADVALUE, "virtual dataset dimensions not large enough to contain all limited dimensions in all selections")
		}
	}
	return nil
}

// h5dVirtualStrAppend is H5D__virtual_str_append: append src to the name
// segment seg.
func h5dVirtualStrAppend(src string, seg *h5dVirtualNameSeg) {
	seg.nameSegment += src
	seg.set = true
}

// h5dVirtualParseSourceName is H5D_virtual_parse_source_name: parse a
// source file or dataset name for "printf" style %b block number
// specifiers ('%%' is a literal '%'). The parsed name is nil when the name
// has no '%' at all.
func h5dVirtualParseSourceName(sourceName string) (parsedName []h5dVirtualNameSeg, staticStrlen, nsubs int, err error) {
	var tmpParsedName []h5dVirtualNameSeg
	cur := -1 /* index of the segment *tmp_parsed_name_p points at (-1: not allocated yet) */
	next := 0 /* index *tmp_parsed_name_p refers to */

	/* Initialize p and tmp_static_strlen */
	p := sourceName
	tmpStaticStrlen := len(sourceName)
	tmpNsubs := 0

	/* Iterate over name */
	/* Note this will not work with UTF-8!  We should support this eventually
	 * -NAF 5/18/2015 */
	for {
		i := strings.IndexByte(p, '%')
		if i < 0 {
			break
		}

		/* Allocate name segment struct if necessary */
		if cur != next {
			tmpParsedName = append(tmpParsedName, h5dVirtualNameSeg{})
			cur = next
		}

		/* Check for type of format specifier */
		var spec byte
		if i+1 < len(p) {
			spec = p[i+1]
		}
		switch spec {
		case 'b':
			/* Check for blank string before specifier */
			if i != 0 {
				/* Append string to name segment */
				h5dVirtualStrAppend(p[:i], &tmpParsedName[cur])
			}

			/* Update other variables */
			next = cur + 1
			tmpStaticStrlen -= 2
			tmpNsubs++
		case '%':
			/* Append string to name segment (include first '%') */
			h5dVirtualStrAppend(p[:i+1], &tmpParsedName[cur])

			/* Update other variables */
			tmpStaticStrlen--
		default:
			return nil, 0, 0, errorf(H5E_ARGS, H5E_BADVALUE, "invalid format specifier")
		}
		p = p[i+2:]
	}

	/* Copy last segment of name, if any, unless the parsed name was not
	 * allocated */
	if tmpParsedName != nil && p != "" {
		/* Allocate name segment struct if necessary */
		if cur != next {
			tmpParsedName = append(tmpParsedName, h5dVirtualNameSeg{})
			cur = next
		}

		/* Append string to name segment */
		h5dVirtualStrAppend(p, &tmpParsedName[cur])
	}
	return tmpParsedName, tmpStaticStrlen, tmpNsubs, nil
}

// h5dVirtualCopyParsedName is H5D__virtual_copy_parsed_name: deep copy a
// parsed source name.
func h5dVirtualCopyParsedName(src []h5dVirtualNameSeg) []h5dVirtualNameSeg {
	if src == nil {
		return nil
	}
	return append([]h5dVirtualNameSeg(nil), src...)
}

// h5dVirtualBuildSourceName is H5D__virtual_build_source_name: build a
// source file or dataset name from a parsed name, substituting blockno for
// each %b.
func h5dVirtualBuildSourceName(sourceName string, parsedName []h5dVirtualNameSeg, staticStrlen, nsubs int, blockno uint64) string {
	/* Check for static name */
	if nsubs == 0 {
		if parsedName != nil {
			return parsedName[0].nameSegment
		}
		return sourceName
	}

	var b strings.Builder
	b.Grow(staticStrlen + nsubs*20)
	nsubsRem := nsubs

	/* Build name */
	for _, nameSeg := range parsedName {
		/* Add name segment */
		if nameSeg.set {
			b.WriteString(nameSeg.nameSegment)
		}

		/* Add block number */
		if nsubsRem > 0 {
			fmt.Fprintf(&b, "%d", blockno)
			nsubsRem--
		}
	}
	return b.String()
}

// h5dVirtualInitMappings performs, for every mapping of the layout message,
// what H5O__layout_decode does after decoding the selections: parse the
// source names, find the unlimited dimensions, set the clipped selections
// of limited mappings, check the mapping and update the minimum dimensions.
func h5dVirtualInitMappings(dset *h5dShared, msg *h5oStorageVirtual) (*h5dVirtualStorage, error) {
	virt := &h5dVirtualStorage{minDims: make([]uint64, dset.ndims)}
	if msg == nil {
		return virt, nil
	}
	virt.list = make([]h5dVirtualEnt, len(msg.list))
	for i := range msg.list {
		m := &msg.list[i]
		ent := &virt.list[i]
		if m.sourceSelect == nil || m.virtualSelect == nil {
			return nil, errorf(H5E_OHDR, H5E_CANTDECODE, "missing virtual mapping selection")
		}
		ent.sourceFileName = m.sourceFileName
		ent.sourceDsetName = m.sourceDsetName
		ent.sourceSelect = h5dSpaceFromSelection(m.sourceSelect)
		ent.sourceDset.virtualSelect = h5dSpaceFromSelection(m.virtualSelect)

		/* Parse source file and dataset names for "printf"
		 * style format specifiers */
		var err error
		if ent.parsedSourceFileName, ent.psfnStaticStrlen, ent.psfnNsubs, err = h5dVirtualParseSourceName(ent.sourceFileName); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTINIT, "can't parse source file name")
		}
		if ent.parsedSourceDsetName, ent.psdnStaticStrlen, ent.psdnNsubs, err = h5dVirtualParseSourceName(ent.sourceDsetName); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTINIT, "can't parse source dataset name")
		}

		/* Set source names in source_dset struct */
		if ent.psfnNsubs == 0 && ent.psdnNsubs == 0 {
			ent.sourceDset.fileName = h5dVirtualBuildSourceName(ent.sourceFileName, ent.parsedSourceFileName, ent.psfnStaticStrlen, 0, 0)
			ent.sourceDset.dsetName = h5dVirtualBuildSourceName(ent.sourceDsetName, ent.parsedSourceDsetName, ent.psdnStaticStrlen, 0, 0)
			ent.sourceDset.namesSet = true
		}

		/* Unlim_dim fields */
		ent.unlimDimSource = h5sGetSelectUnlimDim(ent.sourceSelect)
		ent.unlimDimVirtual = h5sGetSelectUnlimDim(ent.sourceDset.virtualSelect)
		ent.unlimExtentSource = h5sUnlimited
		ent.unlimExtentVirtual = h5sUnlimited
		ent.clipSizeSource = h5sUnlimited
		ent.clipSizeVirtual = h5sUnlimited

		/* Clipped selections */
		if ent.unlimDimVirtual < 0 {
			ent.sourceDset.clippedSourceSelect = ent.sourceSelect
			ent.sourceDset.clippedVirtualSelect = ent.sourceDset.virtualSelect
		}

		/* Check mapping for validity (do both pre and post
		 * checks here, since we had to allocate the entry list
		 * before decoding the selections anyways) */
		if err := h5dVirtualCheckMappingPre(ent.sourceDset.virtualSelect, ent.sourceSelect, h5oVirtualStatusInvalid); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_BADVALUE, "invalid mapping selections")
		}
		if err := h5dVirtualCheckMappingPost(ent); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_BADVALUE, "invalid mapping entry")
		}

		/* Update min_dims */
		if ent.sourceDset.virtualSelect.extent.rank != uint32(dset.ndims) && h5sGetSelectType(ent.sourceDset.virtualSelect) != h5sSelAll && h5sGetSelectType(ent.sourceDset.virtualSelect) != h5sSelNone {
			return nil, errorf(H5E_OHDR, H5E_BADVALUE, "virtual selection rank does not match the dataset")
		}
		if err := h5dVirtualUpdateMinDims(virt, i); err != nil {
			return nil, wrapf(err, H5E_OHDR, H5E_CANTINIT, "unable to update virtual dataset minimum dimensions")
		}
	}
	return virt, nil
}

/*-------------------------------------------------------------------------
 * Source datasets
 *-------------------------------------------------------------------------
 */

// h5dVirtualOpenSourceDset is H5D__virtual_open_source_dset: try to open
// the source dataset of a mapping (or of one "printf" block of it).
func (f *File) h5dVirtualOpenSourceDset(vdset *h5dShared, virtualEnt *h5dVirtualEnt, sourceDset *h5dVirtualSrcdset) error {
	/* Sanity check */
	if sourceDset.dset != nil {
		return errorf(H5E_DATASET, H5E_BADVALUE, "source dataset already open")
	}

	/* Try opening the source file and the source dataset ("." is the
	 * virtual dataset's file) */
	vf := vdset.f
	if vf == nil {
		vf = f
	}
	srcFile, dset, err := vf.dVirtualOpenSource(sourceDset.fileName, sourceDset.dsetName)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return wrapf(err, H5E_DATASET, H5E_CANTOPENFILE, "can't try opening file")
	}

	/* Dataset does not exist */
	if err != nil || dset == nil {
		sourceDset.dsetExists = false
		return nil
	}

	/* Dataset exists */
	sourceDset.dset = dset
	sourceDset.dsetFile = srcFile
	sourceDset.dsetExists = true

	/* Patch the source selection if necessary */
	if virtualEnt.sourceSpaceStatus != h5oVirtualStatusCorrect {
		if err := h5dSpaceExtentCopy(virtualEnt.sourceSelect, dset.space); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTCOPY, "can't copy source dataspace extent")
		}
		virtualEnt.sourceSpaceStatus = h5oVirtualStatusCorrect
	}
	return nil
}

// dVirtualOpenSource is the part of H5D__virtual_open_source_dset that
// opens the source file and the source dataset of a mapping; f is the file
// of the virtual dataset.
//
// fileName "." is the virtual dataset's own file. Any other name is located
// the way H5F_prefix_open_file does with H5F_PREFIX_VDS (an absolute name as
// is, then the HDF5_VDS_PREFIX environment variable, the virtual prefix of
// the access properties (WithVirtualPrefix, "${ORIGIN}" expanded), the
// directory of f, the current directory and the directory of f's resolved
// name); opened files are kept in f's external file cache and closed with
// f. dsetName is looked up from the root group of the source file
// (H5D__open_name).
//
// As in the C library (which opens the file with try = true and clears the
// error stack when H5D__open_name fails), a source file or a source dataset
// that cannot be opened is not an error: the result is (nil, nil, nil) and
// the mapping reads as the fill value.
func (f *File) dVirtualOpenSource(fileName, dsetName string) (srcFile *File, ds *h5dShared, err error) {
	/* Check if we need to open the source file */
	if fileName != "." {
		/* Try opening the file */
		srcFile, err = f.prefixOpenFile(h5fPrefixVDS, f.h5dBuildFilePrefix(f.opts.vdsPrefix), fileName)
		if err != nil {
			/* (try = true: the source file does not exist) */
			return nil, nil, nil
		}
	} else {
		/* Source file is ".", use the virtual dataset's file */
		srcFile = f
	}

	/* Set up the root group in the destination file */
	srcRootLoc, err := srcFile.gRootLoc()
	if err != nil {
		return nil, nil, nil
	}

	/* Try opening the source dataset */
	dset, err := openDatasetName(srcRootLoc, dsetName)
	if err != nil {
		/* Dataset does not exist (the C code resets the error stack) */
		return nil, nil, nil
	}
	if ds, err = dset.f.dOpen(dset.oh); err != nil {
		return nil, nil, nil
	}
	return dset.f, ds, nil
}

// h5dVirtualSourceDims returns the current dimensions of the source
// selection's extent.
func h5dVirtualSourceDims(space *h5s) []uint64 {
	dims, _ := h5sGetSimpleExtentDims(space)
	return dims
}

// h5dVirtualSetExtentUnlim is H5D__virtual_set_extent_unlim: set the extent
// of the virtual dataset by checking the extents of the source datasets
// where an unlimited selection matching. Dimensions that are not unlimited
// in any virtual mapping selections are not affected.
func (f *File) h5dVirtualSetExtentUnlim(dset *h5dShared) error {
	storage := dset.virt
	if storage == nil {
		return errorf(H5E_DATASET, H5E_BADVALUE, "virtual storage not initialized")
	}

	/* Get rank of VDS */
	rank := dset.ndims

	/* Initialize new_dims to HSIZE_UNDEF */
	newDims := make([]uint64, rank)
	for i := range newDims {
		newDims[i] = h5sUnlimited
	}

	/* Iterate over mappings */
	for i := range storage.list {
		ent := &storage.list[i]

		/* Check for unlimited dimension */
		if ent.unlimDimVirtual < 0 {
			continue
		}
		var clipSize uint64

		/* Check for "printf" source dataset resolution */
		if ent.unlimDimSource >= 0 {
			/* Non-printf mapping */
			/* Open source dataset */
			if ent.sourceDset.dset == nil {
				if err := f.h5dVirtualOpenSourceDset(dset, ent, &ent.sourceDset); err != nil {
					return wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "unable to open source dataset")
				}
			}

			/* Check if source dataset is open */
			if ent.sourceDset.dset != nil {
				/* Retrieve current source dataset extent and patch mapping
				 */
				if err := h5dSpaceExtentCopy(ent.sourceSelect, ent.sourceDset.dset.space); err != nil {
					return wrapf(err, H5E_DATASET, H5E_CANTCOPY, "can't copy source dataspace extent")
				}

				/* Get source space dimensions */
				currDims := h5dVirtualSourceDims(ent.sourceSelect)
				if ent.unlimDimSource >= len(currDims) {
					return errorf(H5E_DATASET, H5E_CANTGET, "can't get source space dimensions")
				}

				/* Check if the source extent in the unlimited dimension
				 * changed since the last time the VDS extent/mapping
				 * was updated */
				if currDims[ent.unlimDimSource] == ent.unlimExtentSource {
					/* Use cached result for clip size */
					clipSize = ent.clipSizeVirtual
				} else {
					/* Get size that virtual selection would be clipped to
					 * to match size of source selection within source
					 * extent */
					var err error
					clipSize, err = h5sHyperGetClipExtentMatch(ent.sourceDset.virtualSelect, ent.sourceSelect, currDims[ent.unlimDimSource], storage.view == h5dVdsFirstMissing)
					if err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get clip extent")
					}

					/* If we are setting the extent by the last available
					 * data, clip virtual_select and source_select.  Note
					 * that if we used the cached clip_size above or it
					 * happens to be the same, the virtual selection will
					 * already be clipped to the correct size.  Likewise,
					 * if we used the cached clip_size the source selection
					 * will already be correct. */
					if storage.view == h5dVdsLastAvailable {
						if clipSize != ent.clipSizeVirtual {
							/* Copy virtual selection */
							/* Clip virtual selection */
							if ent.sourceDset.clippedVirtualSelect, err = h5sHyperClipUnlim(ent.sourceDset.virtualSelect, clipSize); err != nil {
								return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
							}
						}

						/* Copy source selection */
						/* Clip source selection */
						if ent.sourceDset.clippedSourceSelect, err = h5sHyperClipUnlim(ent.sourceSelect, currDims[ent.unlimDimSource]); err != nil {
							return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
						}
					}

					/* Update cached values unlim_extent_source and
					 * clip_size_virtual */
					ent.unlimExtentSource = currDims[ent.unlimDimSource]
					ent.clipSizeVirtual = clipSize
				}
			} else {
				clipSize = 0
			}
		} else {
			/* printf mapping */
			var firstMissing uint64 /* First missing dataset in the current block of missing datasets */

			/* Search for source datasets */
			for j := uint64(0); j <= storage.printfGap+firstMissing; j++ {
				if j >= h5dVirtualMaxSubDsets {
					return errorf(H5E_DATASET, H5E_CANTALLOC, "too many printf source datasets")
				}

				/* Check for running out of space in sub_dset array */
				if j >= uint64(len(ent.subDset)) {
					if len(ent.subDset) == 0 {
						/* Allocate sub_dset */
						ent.subDset = make([]h5dVirtualSrcdset, h5dVirtualDefSubDsetSize)
					} else {
						/* Extend sub_dset */
						ent.subDset = append(ent.subDset, make([]h5dVirtualSrcdset, len(ent.subDset))...)
					}
				}
				sub := &ent.subDset[j]

				/* Check if the dataset was already opened */
				if sub.dsetExists {
					firstMissing = j + 1
					continue
				}
				if err := f.h5dVirtualResolveSubDset(ent, j); err != nil {
					return err
				}

				/* Initialize clipped selections */
				if sub.clippedSourceSelect == nil {
					sub.clippedSourceSelect = ent.sourceSelect
				}
				if sub.clippedVirtualSelect == nil {
					sub.clippedVirtualSelect = sub.virtualSelect
				}

				/* Open source dataset */
				if err := f.h5dVirtualOpenSourceDset(dset, ent, sub); err != nil {
					return wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "unable to open source dataset")
				}

				if sub.dset != nil {
					/* Update first_missing */
					firstMissing = j + 1

					/* Close source dataset so we don't have huge
					 * numbers of datasets open */
					sub.dset = nil
				}
			}

			/* Check if the size changed */
			if firstMissing == uint64(ent.subDsetNused) && ent.clipSizeVirtual != h5sUnlimited {
				/* Use cached clip_size */
				clipSize = ent.clipSizeVirtual
			} else {
				/* Check for no datasets */
				if firstMissing == 0 {
					/* Set clip size to 0 */
					clipSize = 0
				} else {
					/* Get clip size from selection */
					if storage.view == h5dVdsLastAvailable {
						/* Get bounds from last valid virtual selection */
						_, boundsEnd, err := h5sGetSelectBounds(ent.subDset[firstMissing-1].virtualSelect)
						if err != nil {
							return wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to get selection bounds")
						}

						/* Set clip_size to bounds_end in unlimited
						 * dimension */
						clipSize = boundsEnd[ent.unlimDimVirtual] + 1
					} else {
						/* Get bounds from first missing virtual selection
						 */
						if err := f.h5dVirtualResolveSubDset(ent, firstMissing); err != nil {
							return err
						}
						boundsStart, _, err := h5sGetSelectBounds(ent.subDset[firstMissing].virtualSelect)
						if err != nil {
							return wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to get selection bounds")
						}

						/* Set clip_size to bounds_start in unlimited
						 * dimension */
						clipSize = boundsStart[ent.unlimDimVirtual]
					}
				}

				/* Set sub_dset_nused and clip_size_virtual */
				ent.subDsetNused = int(firstMissing)
				ent.clipSizeVirtual = clipSize
			}
		}

		/* Update new_dims */
		if ent.unlimDimVirtual >= rank {
			return errorf(H5E_DATASET, H5E_BADVALUE, "unlimited dimension out of range")
		}
		if newDims[ent.unlimDimVirtual] == h5sUnlimited ||
			(storage.view == h5dVdsFirstMissing && clipSize < newDims[ent.unlimDimVirtual]) ||
			(storage.view != h5dVdsFirstMissing && clipSize > newDims[ent.unlimDimVirtual]) {
			newDims[ent.unlimDimVirtual] = clipSize
		}
	}

	/* Get current VDS dimensions */
	currDims := append([]uint64(nil), dset.currDims...)

	/* Calculate new extent */
	changed := false
	for i := 0; i < rank; i++ {
		if newDims[i] == h5sUnlimited {
			newDims[i] = currDims[i]
		} else if i < len(storage.minDims) && newDims[i] < storage.minDims[i] {
			newDims[i] = storage.minDims[i]
		}
		if newDims[i] != currDims[i] {
			changed = true
		}
	}

	/* Update extent if it changed */
	if changed {
		/* Update VDS extent */
		if err := h5dSpaceSetExtent(dset.space, newDims); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
		}
		if err := h5dCacheDataspaceInfo(dset); err != nil {
			return err
		}
	}

	/* If we did not change the VDS dimensions, there is nothing more to update
	 */
	if changed || (!storage.init && storage.view == h5dVdsFirstMissing) {
		/* Iterate over mappings again to update source selections and virtual
		 * mapping extents */
		for i := range storage.list {
			ent := &storage.list[i]

			/* If there is an unlimited dimension, we are setting extent by the
			 * minimum of mappings, and the virtual extent in the unlimited
			 * dimension has changed since the last time the VDS extent/mapping
			 * was updated, we must adjust the selections */
			if ent.unlimDimVirtual >= 0 && storage.view == h5dVdsFirstMissing && newDims[ent.unlimDimVirtual] != ent.unlimExtentVirtual {
				/* Check for "printf" style mapping */
				if ent.unlimDimSource >= 0 {
					/* Non-printf mapping */
					/* Copy virtual selection */
					/* Clip space to virtual extent */
					var err error
					if ent.sourceDset.clippedVirtualSelect, err = h5sHyperClipUnlim(ent.sourceDset.virtualSelect, newDims[ent.unlimDimSource]); err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
					}

					/* Get size that source selection will be clipped to to
					 * match size of virtual selection */
					clipSize, err := h5sHyperGetClipExtent(ent.sourceSelect, ent.sourceDset.clippedVirtualSelect, false)
					if err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get clip extent")
					}

					/* Check if the clip size changed */
					if clipSize != ent.clipSizeSource {
						/* Copy source selection */
						/* Clip source selection */
						if ent.sourceDset.clippedSourceSelect, err = h5sHyperClipUnlim(ent.sourceSelect, clipSize); err != nil {
							return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
						}

						/* Update cached value clip_size_source */
						ent.clipSizeSource = clipSize
					}
				} else {
					/* printf mapping */
					/* Get index of first incomplete block in virtual
					 * selection */
					firstIncBlock, _, err := h5sHyperGetFirstIncBlock(ent.sourceDset.virtualSelect, newDims[ent.unlimDimVirtual])
					if err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get first incomplete block")
					}

					/* Iterate over sub datasets */
					for j := range ent.subDset {
						sub := &ent.subDset[j]

						/* Initialize clipped source selection to point to
						 * base source selection */
						sub.clippedSourceSelect = ent.sourceSelect

						/* Initialize clipped virtual selection to point to
						 * unclipped virtual selection */
						sub.clippedVirtualSelect = sub.virtualSelect

						/* Only initialize clipped selections if it is a
						 * complete block, for incomplete blocks defer to
						 * H5D__virtual_pre_io() as we may not have a valid
						 * source extent here.  For unused blocks we will never
						 * need clipped selections (until the extent is
						 * recalculated in this function). */
						if uint64(j) >= firstIncBlock {
							/* Clear clipped source and virtual selections */
							sub.clippedSourceSelect = nil
							sub.clippedVirtualSelect = nil
						}
					}
				}

				/* Update cached value unlim_extent_virtual */
				ent.unlimExtentVirtual = newDims[ent.unlimDimVirtual]
			}

			/* Update top level virtual_select and clipped_virtual_select
			 * extents */
			if err := h5dSpaceSetExtent(ent.sourceDset.virtualSelect, newDims); err != nil {
				return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
			}
			if ent.sourceDset.clippedVirtualSelect != nil && ent.sourceDset.clippedVirtualSelect != ent.sourceDset.virtualSelect {
				if err := h5dSpaceSetExtent(ent.sourceDset.clippedVirtualSelect, newDims); err != nil {
					return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
				}
			}

			/* Update sub dataset virtual_select and clipped_virtual_select
			 * extents */
			for j := range ent.subDset {
				sub := &ent.subDset[j]
				if sub.virtualSelect != nil {
					if err := h5dSpaceSetExtent(sub.virtualSelect, newDims); err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
					}
					if sub.clippedVirtualSelect != nil && sub.clippedVirtualSelect != sub.virtualSelect {
						if err := h5dSpaceSetExtent(sub.clippedVirtualSelect, newDims); err != nil {
							return wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
						}
					}
				}
			}
		}
	}

	/* Mark layout as fully initialized */
	storage.init = true
	return nil
}

// h5dVirtualResolveSubDset resolves the file name, dataset name and virtual
// selection of the j'th "printf" source dataset of a mapping (the "Resolve
// file name / dset name / virtual selection for block" steps).
func (f *File) h5dVirtualResolveSubDset(ent *h5dVirtualEnt, j uint64) error {
	if j >= uint64(len(ent.subDset)) {
		return errorf(H5E_DATASET, H5E_BADVALUE, "sub dataset index out of range")
	}
	sub := &ent.subDset[j]

	/* Resolve file name */
	/* Resolve dset name */
	if !sub.namesSet {
		sub.fileName = h5dVirtualBuildSourceName(ent.sourceFileName, ent.parsedSourceFileName, ent.psfnStaticStrlen, ent.psfnNsubs, j)
		sub.dsetName = h5dVirtualBuildSourceName(ent.sourceDsetName, ent.parsedSourceDsetName, ent.psdnStaticStrlen, ent.psdnNsubs, j)
		sub.namesSet = true
	}

	/* Resolve virtual selection for block */
	if sub.virtualSelect == nil {
		vs, err := h5sHyperGetUnlimBlock(ent.sourceDset.virtualSelect, j)
		if err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to get block in unlimited selection")
		}
		sub.virtualSelect = vs
	}
	return nil
}

// h5dVirtualInitAll is H5D__virtual_init_all: finish initializing the
// layout in preparation for I/O (only necessary if
// H5D__virtual_set_extent_unlim has not been called yet): initialize the
// clipped selections of all mappings.
func (f *File) h5dVirtualInitAll(dset *h5dShared) error {
	storage := dset.virt

	/* Get current VDS dimensions */
	virtualDims := dset.currDims

	/* Iterate over mappings */
	for i := range storage.list {
		ent := &storage.list[i]

		/* Check for unlimited dimension */
		if ent.unlimDimVirtual < 0 {
			/* Limited mapping, just make sure the clipped selections were
			 * already set.  Again, no need to open the source file. */
			continue
		}
		if ent.unlimDimVirtual >= len(virtualDims) {
			return errorf(H5E_DATASET, H5E_BADVALUE, "unlimited dimension out of range")
		}

		/* Check for "printf" source dataset resolution */
		if ent.unlimDimSource >= 0 {
			/* Non-printf mapping */
			/* Open source dataset */
			if ent.sourceDset.dset == nil {
				if err := f.h5dVirtualOpenSourceDset(dset, ent, &ent.sourceDset); err != nil {
					return wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "unable to open source dataset")
				}
			}

			/* Check if source dataset is open */
			if ent.sourceDset.dset != nil {
				/* Retrieve current source dataset extent and patch mapping
				 */
				if err := h5dSpaceExtentCopy(ent.sourceSelect, ent.sourceDset.dset.space); err != nil {
					return wrapf(err, H5E_DATASET, H5E_CANTCOPY, "can't copy source dataspace extent")
				}

				/* Get source space dimensions */
				sourceDims := h5dVirtualSourceDims(ent.sourceSelect)
				if ent.unlimDimSource >= len(sourceDims) {
					return errorf(H5E_DATASET, H5E_CANTGET, "can't get source space dimensions")
				}

				/* Get size that source selection would be clipped to to
				 * match size of virtual selection */
				clipSize, err := h5sHyperGetClipExtentMatch(ent.sourceSelect, ent.sourceDset.virtualSelect, virtualDims[ent.unlimDimVirtual], false)
				if err != nil {
					return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get clip extent")
				}

				/* Check if the clip size is within the current extent of
				 * the source dataset */
				if clipSize <= sourceDims[ent.unlimDimSource] {
					/* Clip virtual selection to extent */
					if ent.sourceDset.clippedVirtualSelect, err = h5sHyperClipUnlim(ent.sourceDset.virtualSelect, virtualDims[ent.unlimDimVirtual]); err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
					}

					/* Clip source selection to clip_size */
					if ent.sourceDset.clippedSourceSelect, err = h5sHyperClipUnlim(ent.sourceSelect, clipSize); err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
					}
				} else {
					/* Get size that virtual selection will be clipped to to
					 * match size of source selection within source extent
					 */
					if clipSize, err = h5sHyperGetClipExtentMatch(ent.sourceDset.virtualSelect, ent.sourceSelect, sourceDims[ent.unlimDimSource], false); err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get clip extent")
					}

					/* Clip virtual selection to clip_size */
					if ent.sourceDset.clippedVirtualSelect, err = h5sHyperClipUnlim(ent.sourceDset.virtualSelect, clipSize); err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
					}

					/* Clip source selection to extent */
					if ent.sourceDset.clippedSourceSelect, err = h5sHyperClipUnlim(ent.sourceSelect, sourceDims[ent.unlimDimSource]); err != nil {
						return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "failed to clip unlimited selection")
					}
				}
			} else {
				/* (No source dataset: no clipped selections) */
				ent.sourceDset.clippedVirtualSelect = nil
				ent.sourceDset.clippedSourceSelect = nil
			}
		} else {
			/* printf mapping */
			/* Get number of sub-source datasets in current extent */
			subDsetMax64, partialBlock, err := h5sHyperGetFirstIncBlock(ent.sourceDset.virtualSelect, virtualDims[ent.unlimDimVirtual])
			if err != nil {
				return wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get first incomplete block")
			}
			if partialBlock {
				subDsetMax64++
			}
			if subDsetMax64 > h5dVirtualMaxSubDsets {
				return errorf(H5E_DATASET, H5E_CANTALLOC, "too many printf source datasets")
			}
			subDsetMax := int(subDsetMax64)

			/* Allocate or grow the sub_dset array if necessary */
			if len(ent.subDset) < subDsetMax {
				ent.subDset = append(ent.subDset, make([]h5dVirtualSrcdset, subDsetMax-len(ent.subDset))...)
			}

			/* Iterate over sub dsets */
			for j := 0; j < subDsetMax; j++ {
				if err := f.h5dVirtualResolveSubDset(ent, uint64(j)); err != nil {
					return err
				}
				sub := &ent.subDset[j]

				/* Initialize clipped source selection to point to base
				 * source selection */
				sub.clippedSourceSelect = ent.sourceSelect

				/* Initialize clipped virtual selection to point to
				 * unclipped virtual selection */
				sub.clippedVirtualSelect = sub.virtualSelect

				/* Clear clipped selections if this is a partial block,
				 * defer calculation of real clipped selections to
				 * H5D__virtual_pre_io() as we may not have a valid source
				 * extent here */
				if j == subDsetMax-1 && partialBlock {
					/* Clear clipped source and virtual selections */
					sub.clippedSourceSelect = nil
					sub.clippedVirtualSelect = nil
				}
				/* Note we do not need to open the source file, this will
				 * happen later in H5D__virtual_pre_io() */
			}

			/* Update sub_dset_nused */
			ent.subDsetNused = subDsetMax
		}
	}

	/* Mark layout as fully initialized */
	storage.init = true
	return nil
}

// h5dVirtualInit is H5D__virtual_init: initialize the virtual layout
// information for a dataset (with the post-decode processing of the
// mappings).
func h5dVirtualInit(_ *File, dset *h5dShared) error {
	storage, err := h5dVirtualInitMappings(dset, dset.layout.virt)
	if err != nil {
		return err
	}
	dset.virt = storage

	/* Check that the dimensions of the VDS are large enough */
	if err := h5dVirtualCheckMinDims(dset); err != nil {
		return wrapf(err, H5E_ARGS, H5E_BADVALUE, "virtual dataset dimensions not large enough to contain all limited dimensions in all selections")
	}

	/* Patch the virtual selection dataspaces.  Note we always patch the space
	 * status because this layout could be from an old version held in the
	 * object header message code.  We cannot update that held message because
	 * the layout message is constant, so just overwrite the values here (and
	 * invalidate other fields by setting storage->init to false below).  Also
	 * remove offset from selections.  We only have to update
	 * source_space_status and virtual_space_status because others will be based
	 * on these and should therefore already have been normalized. */
	for i := range storage.list {
		ent := &storage.list[i]

		/* Patch extent */
		if err := h5dSpaceExtentCopy(ent.sourceDset.virtualSelect, dset.space); err != nil {
			return wrapf(err, H5E_DATASET, H5E_CANTCOPY, "can't copy virtual dataspace extent")
		}
		ent.virtualSpaceStatus = h5oVirtualStatusCorrect

		/* Mark source extent as invalid */
		ent.sourceSpaceStatus = h5oVirtualStatusInvalid

		/* Normalize offsets, toss out old offset values */
		if s, _, ok, err := h5sHyperNormalizeOffset(ent.sourceDset.virtualSelect); err != nil {
			return wrapf(err, H5E_DATASET, H5E_BADSELECT, "unable to normalize dataspace by offset")
		} else if ok {
			*ent.sourceDset.virtualSelect = *s
		}
		if s, _, ok, err := h5sHyperNormalizeOffset(ent.sourceSelect); err != nil {
			return wrapf(err, H5E_DATASET, H5E_BADSELECT, "unable to normalize dataspace by offset")
		} else if ok {
			*ent.sourceSelect = *s
		}
	}

	/* Get view option */
	/* (H5Pget_virtual_view default) */
	storage.view = h5dVdsLastAvailable

	/* Get printf gap if view is H5D_VDS_LAST_AVAILABLE, otherwise set to 0 */
	/* (H5Pget_virtual_printf_gap default) */
	storage.printfGap = 0

	/* Mark layout as not fully initialized (must be done prior to I/O for
	 * unlimited/printf selections) */
	storage.init = false
	return nil
}

// h5dVirtualIsSpaceAlloc is H5D__virtual_is_space_alloc: always true, since
// the global heap object containing the mappings is created when the layout
// message is encoded, and nothing else needs to be allocated for virtual
// datasets.
func h5dVirtualIsSpaceAlloc(*h5dShared) bool { return true }

/*-------------------------------------------------------------------------
 * Reading
 *-------------------------------------------------------------------------
 */

// h5dVirtualPreIo is H5D__virtual_pre_io: project the file selection onto
// the memory selection for each mapping (and sub-source dataset), opening
// the source datasets that are needed. It returns the total number of
// elements mapped.
func (f *File) h5dVirtualPreIo(dinfo *h5dDsetIoInfo, storage *h5dVirtualStorage, fileSpace, memSpace *h5s) (uint64, error) {
	dset := dinfo.dset
	var (
		boundsStart, boundsEnd []uint64
		boundsInit             bool
	)
	rank := dset.ndims

	/* Initialize layout if necessary */
	if !storage.init {
		if err := f.h5dVirtualInitAll(dset); err != nil {
			return 0, wrapf(err, H5E_DATASET, H5E_CANTINIT, "can't initialize virtual layout")
		}
	}

	/* Initialize tot_nelmts */
	var totNelmts uint64

	/* Iterate over mappings */
	for i := range storage.list {
		ent := &storage.list[i]

		/* Sanity check that the virtual space has been patched by now */
		if ent.virtualSpaceStatus != h5oVirtualStatusCorrect {
			return 0, errorf(H5E_DATASET, H5E_BADVALUE, "virtual selection extent not set")
		}

		/* Check for "printf" source dataset resolution */
		if ent.psfnNsubs > 0 || ent.psdnNsubs > 0 {
			/* Get selection bounds if necessary */
			if !boundsInit {
				var err error
				/* Get selection bounds */
				if boundsStart, boundsEnd, err = h5sGetSelectBounds(fileSpace); err != nil {
					return 0, wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to get selection bounds")
				}

				/* Adjust bounds_end to represent the extent just enclosing them
				 * (add 1) */
				for j := range rank {
					boundsEnd[j]++
				}

				/* Bounds are now initialized */
				boundsInit = true
			}

			/* Get index of first block in virtual selection */
			ioStart, _, err := h5sHyperGetFirstIncBlock(ent.sourceDset.virtualSelect, boundsStart[ent.unlimDimVirtual])
			if err != nil {
				return 0, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get first incomplete block")
			}

			/* Get index of first block outside of virtual selection */
			ioEnd, partialBlock, err := h5sHyperGetFirstIncBlock(ent.sourceDset.virtualSelect, boundsEnd[ent.unlimDimVirtual])
			if err != nil {
				return 0, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get first incomplete block")
			}
			if partialBlock {
				ioEnd++
			}
			if ioEnd > uint64(ent.subDsetNused) {
				ioEnd = uint64(ent.subDsetNused)
			}
			if ioStart > ioEnd {
				ioStart = ioEnd
			}
			ent.subDsetIoStart, ent.subDsetIoEnd = int(ioStart), int(ioEnd)

			/* Iterate over sub-source dsets */
			for j := ent.subDsetIoStart; j < ent.subDsetIoEnd; j++ {
				sub := &ent.subDset[j]

				/* Check for clipped virtual selection */
				if sub.clippedVirtualSelect == nil {
					/* This should only be NULL if this is a partial block */
					if j != ent.subDsetIoEnd-1 || !partialBlock {
						return 0, errorf(H5E_DATASET, H5E_BADVALUE, "missing clipped virtual selection")
					}

					/* If the source space status is not correct, we must try to
					 * open the source dataset to patch it */
					if ent.sourceSpaceStatus != h5oVirtualStatusCorrect {
						if sub.dset == nil {
							if err := f.h5dVirtualOpenSourceDset(dset, ent, sub); err != nil {
								return 0, wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "unable to open source dataset")
							}
						}
					}

					/* If we obtained a valid source space, we must create
					 * clipped source and virtual selections, otherwise we
					 * cannot do this and we will leave them NULL.  This doesn't
					 * hurt anything because we can't do I/O because the dataset
					 * must not have been found. */
					if ent.sourceSpaceStatus == h5oVirtualStatusCorrect {
						/* Get bounds of virtual selection */
						_, vboundsEnd, err := h5sGetSelectBounds(sub.virtualSelect)
						if err != nil {
							return 0, wrapf(err, H5E_DATASET, H5E_CANTGET, "unable to get selection bounds")
						}

						/* Convert bounds to extent (add 1) */
						for k := range vboundsEnd {
							vboundsEnd[k]++
						}

						/* Temporarily set extent of virtual selection to bounds */
						if err := h5dSpaceSetExtent(sub.virtualSelect, vboundsEnd); err != nil {
							return 0, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
						}

						/* Get current VDS dimensions */
						tmpDims := append([]uint64(nil), dset.currDims...)

						/* Copy virtual selection */
						/* Clip virtual selection to real virtual extent */
						clipped, err := h5dSelectAndBlock(sub.virtualSelect, make([]uint64, rank), tmpDims)
						if err != nil {
							return 0, wrapf(err, H5E_DATASET, H5E_CANTSELECT, "unable to clip hyperslab")
						}
						sub.clippedVirtualSelect = clipped

						/* Project intersection of virtual space and clipped
						 * virtual space onto source space (create
						 * clipped_source_select) */
						if sub.clippedSourceSelect, err = h5sSelectProjectIntersection(sub.virtualSelect, ent.sourceSelect, sub.clippedVirtualSelect); err != nil {
							return 0, wrapf(err, H5E_DATASET, H5E_CANTCLIP, "can't project virtual intersection onto memory space")
						}

						/* Set extents of virtual_select and
						 * clipped_virtual_select to virtual extent */
						if err := h5dSpaceSetExtent(sub.virtualSelect, tmpDims); err != nil {
							return 0, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
						}
						if err := h5dSpaceSetExtent(sub.clippedVirtualSelect, tmpDims); err != nil {
							return 0, wrapf(err, H5E_DATASET, H5E_CANTINIT, "unable to modify size of dataspace")
						}
					}
				}

				/* Only continue if we managed to obtain a
				 * clipped_virtual_select */
				if sub.clippedVirtualSelect != nil {
					n, err := f.h5dVirtualProjectMem(dset, ent, sub, fileSpace, memSpace)
					if err != nil {
						return 0, err
					}
					totNelmts += n
				}
			}
		} else {
			if ent.sourceDset.clippedVirtualSelect != nil {
				n, err := f.h5dVirtualProjectMem(dset, ent, &ent.sourceDset, fileSpace, memSpace)
				if err != nil {
					return 0, err
				}
				totNelmts += n
			} else {
				/* If there is no clipped_dim_virtual, this must be an unlimited
				 * selection whose dataset was not found in the last call to
				 * H5Dget_space().  Do not attempt to open it as this might
				 * affect the extent and we are not going to recalculate it
				 * here. */
				if ent.unlimDimVirtual < 0 {
					return 0, errorf(H5E_DATASET, H5E_BADVALUE, "missing clipped virtual selection")
				}
			}
		}
	}
	return totNelmts, nil
}

// h5dVirtualProjectMem is the common part of H5D__virtual_pre_io for one
// source dataset: project the intersection of the file space and the
// mapping's virtual space onto the memory space, and open the source
// dataset if anything is selected. It returns the number of elements
// mapped (0 when the source dataset does not exist).
func (f *File) h5dVirtualProjectMem(dset *h5dShared, ent *h5dVirtualEnt, src *h5dVirtualSrcdset, fileSpace, memSpace *h5s) (uint64, error) {
	/* Project intersection of file space and mapping virtual space onto
	 * memory space */
	proj, err := h5sSelectProjectIntersection(fileSpace, memSpace, src.clippedVirtualSelect)
	if err != nil {
		return 0, wrapf(err, H5E_DATASET, H5E_CANTCLIP, "can't project virtual intersection onto memory space")
	}
	src.projectedMemSpace = proj

	/* Check number of elements selected */
	selectNelmts := h5sGetSelectNpoints(proj)

	/* Check if anything is selected */
	if selectNelmts > 0 {
		/* Open source dataset */
		if src.dset == nil {
			/* Try to open dataset */
			if err := f.h5dVirtualOpenSourceDset(dset, ent, src); err != nil {
				return 0, wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "unable to open source dataset")
			}
		}

		/* If the source dataset is not open, mark the selected
		 * elements as zero so projected_mem_space is freed */
		if src.dset == nil {
			selectNelmts = 0
		}
	}

	/* If there are not elements selected in this mapping, free
	 * projected_mem_space, otherwise update tot_nelmts */
	if selectNelmts == 0 {
		src.projectedMemSpace = nil
	}
	return selectNelmts, nil
}

// h5dVirtualPostIo is H5D__virtual_post_io: free the projected memory
// spaces of the I/O operation.
func h5dVirtualPostIo(storage *h5dVirtualStorage) {
	/* Iterate over mappings */
	for i := range storage.list {
		ent := &storage.list[i]
		/* Check for "printf" source dataset resolution */
		if ent.psfnNsubs > 0 || ent.psdnNsubs > 0 {
			/* Iterate over sub-source dsets */
			for j := ent.subDsetIoStart; j < ent.subDsetIoEnd && j < len(ent.subDset); j++ {
				/* Close projected memory space */
				ent.subDset[j].projectedMemSpace = nil
			}
		} else {
			/* Close projected memory space */
			ent.sourceDset.projectedMemSpace = nil
		}
	}
}

// h5dTypeHasHeapRefs reports whether elements of dt refer to data in the
// file's heaps (variable-length data, references).
func h5dTypeHasHeapRefs(dt *h5.Datatype) bool {
	if dt == nil {
		return false
	}
	switch dt.Class {
	case h5.ClassVlen, h5.ClassReference:
		return true
	case h5.ClassCompound:
		for _, m := range dt.Members {
			if h5dTypeHasHeapRefs(m.Type) {
				return true
			}
		}
	case h5.ClassArray, h5.ClassEnum:
		return h5dTypeHasHeapRefs(dt.Base)
	}
	return false
}

// h5dVirtualReadOne is H5D__virtual_read_one: read from a single source
// dataset in a virtual dataset.
func (f *File) h5dVirtualReadOne(dinfo *h5dDsetIoInfo, sourceDset *h5dVirtualSrcdset) error {
	/* Only perform I/O if there is a projected memory space, otherwise there
	 * were no elements in the projection or the source dataset could not be
	 * opened */
	if sourceDset.projectedMemSpace == nil {
		return nil
	}
	src := sourceDset.dset
	if src == nil || sourceDset.clippedSourceSelect == nil {
		return errorf(H5E_DATASET, H5E_BADVALUE, "source dataset not open")
	}

	/* (No datatype conversion: the source elements must have the virtual
	 *  dataset's size, and heap references must come from this file) */
	if src.typ.Size != dinfo.dset.typ.Size {
		return errorf(H5E_DATASET, H5E_UNSUPPORTED, "datatype conversion between source (%d bytes) and virtual dataset (%d bytes) elements not supported", src.typ.Size, dinfo.dset.typ.Size)
	}
	if sourceDset.dsetFile != nil && sourceDset.dsetFile != f && h5dTypeHasHeapRefs(src.typ) {
		return errorf(H5E_DATASET, H5E_UNSUPPORTED, "variable-length or reference elements from a source dataset in another file not supported")
	}

	/* Project intersection of file space and mapping virtual space onto
	 * mapping source space */
	projectedSrcSpace, err := h5sSelectProjectIntersection(sourceDset.clippedVirtualSelect, sourceDset.clippedSourceSelect, dinfo.fileSpace)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "can't project virtual intersection onto source space")
	}

	/* (the source dataset's own extent) */
	fs, err := h5dFileSpaceOf(src, projectedSrcSpace)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_BADSELECT, "invalid source selection")
	}

	/* Read in the point (with the custom VL memory allocator) */
	srcFile := sourceDset.dsetFile
	if srcFile == nil {
		srcFile = f
	}
	if err := srcFile.h5dRead(src, fs, sourceDset.projectedMemSpace, dinfo.buf); err != nil {
		return wrapf(err, H5E_DATASET, H5E_READERROR, "can't read source dataset")
	}

	/* (The one datatype conversion done here: atomic source elements that
	 *  differ from the virtual dataset's only by byte order) */
	if h5dVirtualByteOrderOnly(src.typ, dinfo.dset.typ) {
		size := src.typ.Size
		for s := range sourceDset.projectedMemSpace.iterSeqs(size) {
			end := s.off + s.len
			if end < s.off || end > uint64(len(dinfo.buf)) {
				return errorf(H5E_DATASET, H5E_OVERFLOW, "memory sequence out of range of the destination buffer")
			}
			for off := s.off; off+size <= end; off += size {
				slices.Reverse(dinfo.buf[off : off+size])
			}
		}
	}
	return nil
}

// h5dVirtualByteOrderOnly reports whether the elements of src convert to
// dst by reversing their bytes: both are integer, float or bitfield types
// of the same size and layout, one little-endian and the other big-endian
// (the H5T_conv_order path of H5T_convert).
func h5dVirtualByteOrderOnly(src, dst *h5.Datatype) bool {
	if src == nil || dst == nil || src.Class != dst.Class || src.Size != dst.Size || src.Size < 2 {
		return false
	}
	switch src.Class {
	case h5.ClassInteger, h5.ClassFloat, h5.ClassBitfield:
	default:
		return false
	}
	if !(src.Order == h5.OrderLE && dst.Order == h5.OrderBE) && !(src.Order == h5.OrderBE && dst.Order == h5.OrderLE) {
		return false
	}
	return src.Precision == dst.Precision && src.Offset == dst.Offset && src.Sign == dst.Sign &&
		src.LSBPad == dst.LSBPad && src.MSBPad == dst.MSBPad && src.Float == dst.Float
}

// h5dVirtualRead is H5D__virtual_read: read from a virtual dataset.
func h5dVirtualRead(f *File, dinfo *h5dDsetIoInfo) error {
	storage := dinfo.dset.virt
	if storage == nil {
		return errorf(H5E_DATASET, H5E_BADVALUE, "virtual storage not initialized")
	}

	/* Initialize nelmts */
	nelmts := h5sGetSelectNpoints(dinfo.fileSpace)

	/* Prepare for I/O operation */
	totNelmts, err := f.h5dVirtualPreIo(dinfo, storage, dinfo.fileSpace, dinfo.memSpace)
	defer h5dVirtualPostIo(storage)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTCLIP, "unable to prepare for I/O operation")
	}

	/* Iterate over mappings */
	for i := range storage.list {
		ent := &storage.list[i]

		/* Check for "printf" source dataset resolution */
		if ent.psfnNsubs > 0 || ent.psdnNsubs > 0 {
			/* Iterate over sub-source dsets */
			for j := ent.subDsetIoStart; j < ent.subDsetIoEnd; j++ {
				if err := f.h5dVirtualReadOne(dinfo, &ent.subDset[j]); err != nil {
					return wrapf(err, H5E_DATASET, H5E_READERROR, "unable to read source dataset")
				}
			}
		} else {
			/* Read from source dataset */
			if err := f.h5dVirtualReadOne(dinfo, &ent.sourceDset); err != nil {
				return wrapf(err, H5E_DATASET, H5E_READERROR, "unable to read source dataset")
			}
		}
	}

	/* Fill unmapped part of buffer with fill value */
	if totNelmts < nelmts {
		if err := h5dVirtualReadFill(dinfo, storage); err != nil {
			return err
		}
	}
	return nil
}

// h5dVirtualReadFill is the fill step of H5D__virtual_read: write the fill
// value to the elements of the memory selection that no mapping covered
// (the memory selection minus the projected memory spaces).
func h5dVirtualReadFill(dinfo *h5dDsetIoInfo, storage *h5dVirtualStorage) error {
	dset := dinfo.dset

	/* Check the fill value status */
	fillStatus := h5pIsFillValueDefined(&dset.dcplCache.fill)

	/* Always write fill value to memory buffer unless it is undefined */
	if fillStatus == h5dFillValueUndefined {
		return nil
	}

	/* Start with fill space equal to memory space */
	/* (H5S_select_subtract is not translated: mark the memory elements the
	 *  mappings wrote, then fill the others) */
	nmem := dinfo.memSpace.extent.nelem
	covered := make([]bool, nmem)
	mark := func(space *h5s) error {
		if space == nil {
			return nil
		}
		for s := range space.iterSeqs(1) {
			if s.off+s.len > nmem {
				return errorf(H5E_DATASET, H5E_CANTCLIP, "unable to clip fill selection")
			}
			for k := s.off; k < s.off+s.len; k++ {
				covered[k] = true
			}
		}
		return nil
	}

	/* Iterate over mappings */
	for i := range storage.list {
		ent := &storage.list[i]
		/* Check for "printf" source dataset resolution */
		if ent.psfnNsubs > 0 || ent.psdnNsubs > 0 {
			/* Iterate over sub-source dsets */
			for j := ent.subDsetIoStart; j < ent.subDsetIoEnd; j++ {
				if err := mark(ent.subDset[j].projectedMemSpace); err != nil {
					return err
				}
			}
		} else if err := mark(ent.sourceDset.projectedMemSpace); err != nil {
			/* Subtract projected memory space from fill space */
			return err
		}
	}

	/* Write fill values to memory buffer */
	fill, err := h5dFillInit(&dset.dcplCache.fill, dset.typ)
	if err != nil {
		return wrapf(err, H5E_DATASET, H5E_CANTINIT, "filling buf failed")
	}
	elmtSize := dset.typ.Size
	for s := range dinfo.memSpace.iterSeqs(1) {
		for k := s.off; k < s.off+s.len && k < nmem; k++ {
			if covered[k] {
				continue
			}
			dst, err := dinfo.memSlice(k*elmtSize, elmtSize)
			if err != nil {
				return err
			}
			copy(dst, fill)
		}
	}
	return nil
}
