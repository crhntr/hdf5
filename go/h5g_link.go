package hdf5

// Translated from src/H5Glink.c (read side only): link tables (used to
// iterate over links in an order the storage does not provide natively),
// the conversion of a link message into the information reported to users,
// and the construction of an object location from a link.

import (
	"errors"
	"slices"
	"strings"

	"github.com/crhntr/hdf5/go/h5"
)

// h5gLinkTable is H5G_link_table_t: a table of links of a group. The links
// may be shared with cached object header messages and must not be
// modified.
type h5gLinkTable struct {
	lnks []*h5oLink /* Pointer to array of links (nlinks = len(lnks)) */
}

// h5gLibIterate is H5G_lib_iterate_t: the library-internal callback for
// iterating over the links of a group. It returns errBIterStop to stop the
// iteration early (H5_ITER_STOP) and any other error to fail it.
type h5gLibIterate func(lnk *h5oLink) error

// h5gLinkCmpNameInc is H5G__link_cmp_name_inc: compare two link names, in
// increasing alphabetic order (same as strcmp()).
func h5gLinkCmpNameInc(lnk1, lnk2 *h5oLink) int {
	return strings.Compare(lnk1.name, lnk2.name)
}

// h5gLinkCmpNameDec is H5G__link_cmp_name_dec: compare two link names, in
// decreasing alphabetic order (opposite strcmp()).
func h5gLinkCmpNameDec(lnk1, lnk2 *h5oLink) int {
	return strings.Compare(lnk2.name, lnk1.name)
}

// h5gLinkCmpCorderInc is H5G__link_cmp_corder_inc: compare two link creation
// orders, in increasing order.
func h5gLinkCmpCorderInc(lnk1, lnk2 *h5oLink) int {
	if lnk1.corder < lnk2.corder {
		return -1
	} else if lnk1.corder > lnk2.corder {
		return 1
	}
	return 0
}

// h5gLinkCmpCorderDec is H5G__link_cmp_corder_dec: compare two link creation
// orders, in decreasing order.
func h5gLinkCmpCorderDec(lnk1, lnk2 *h5oLink) int {
	if lnk1.corder < lnk2.corder {
		return 1
	} else if lnk1.corder > lnk2.corder {
		return -1
	}
	return 0
}

// gLinkToInfo is H5G_link_to_info: retrieve information from a link
// object, as the public h5.Link.
//
// The C function only reports the size of the value of soft and
// user-defined links (asking the link class's query callback); here the
// value itself is reported: the soft link path, the raw user-defined data
// and, for external links, the target file and object path unpacked with
// H5Lunpack_elink_val (left empty when the value cannot be unpacked, which
// the C library does not check here either).
func gLinkToInfo(lnk *h5oLink) (h5.Link, error) {
	var info h5.Link

	/* Get information from the link */
	info.Name = lnk.name
	info.Cset = lnk.cset
	info.CreationOrder = lnk.corder
	info.HasCreationOrder = lnk.corderValid
	info.Type = lnk.typ

	switch lnk.typ {
	case h5.LinkHard:
		/* Serialize the address into a VOL token */
		info.Address = lnk.addr

	case h5.LinkSoft:
		info.SoftPath = lnk.softName

	default:
		if lnk.typ < h5lTypeUdMin || lnk.typ > h5lTypeMax {
			return h5.Link{}, errorf(H5E_LINK, H5E_BADTYPE, "unknown link class")
		}

		/* User-defined link; call its query function to get the link udata size. */
		/* Get the link class for this type of link.  It's okay if the class
		 * isn't registered, though--we just can't give any more information
		 * about it
		 */
		linkClass := h5lFindClass(lnk.typ)
		if linkClass != nil && linkClass.queryFunc != nil {
			/* Call the link's query routine to retrieve the user-defined link's value */
			val, err := linkClass.queryFunc(lnk.name, lnk.udData)
			if err != nil {
				return h5.Link{}, wrapf(err, H5E_LINK, H5E_CALLBACK, "query buffer size callback returned failure")
			}
			info.UserData = val
		} else {
			info.UserData = append([]byte(nil), lnk.udData...)
		}

		if lnk.typ == h5.LinkExternal {
			if _, file, path, err := h5lUnpackElinkVal(lnk.udData); err == nil {
				info.ExternalFile = file
				info.ExternalPath = path
			}
		}
	}
	return info, nil
}

// gLinkToLoc is H5G__link_to_loc: build the location of the object a link
// points to (the address is only known for hard links; it is undefined for
// soft and user-defined links until they are traversed).
func gLinkToLoc(grpLoc *gLoc, lnk *h5oLink) (*gLoc, error) {
	/*
	 * Build location from the link
	 */

	/* Check for unknown library-internal link */
	if lnk.typ > h5lTypeBuiltinMax && lnk.typ < h5lTypeUdMin {
		return nil, errorf(H5E_SYM, H5E_UNSUPPORTED, "unknown link type")
	}

	/* Build object's group hier. location */
	objLoc := &gLoc{
		path: h5gNameSet(grpLoc.path, lnk.name),
		/* Set the object location, if it's a hard link set the address also */
		f:    grpLoc.f,
		addr: haddrUndef,
	}
	if lnk.typ == h5.LinkHard {
		objLoc.addr = lnk.addr
	}
	return objLoc, nil
}

// h5gLinkSortTable is H5G__link_sort_table: sort a table containing a list
// of links for a group.
func h5gLinkSortTable(ltable *h5gLinkTable, idxType h5Index, order h5IterOrder) {
	/* Can't sort when empty since the links table will be NULL */
	if len(ltable.lnks) == 0 {
		return
	}

	/* Pick appropriate sorting routine */
	/* (qsort in the C code, a stable merge sort in glibc: links that
	 *  compare equal keep the order of the table) */
	if idxType == h5IndexName {
		if order == h5IterInc {
			slices.SortStableFunc(ltable.lnks, h5gLinkCmpNameInc)
		} else if order == h5IterDec {
			slices.SortStableFunc(ltable.lnks, h5gLinkCmpNameDec)
		}
		/* else order == H5_ITER_NATIVE */
	} else {
		if order == h5IterInc {
			slices.SortStableFunc(ltable.lnks, h5gLinkCmpCorderInc)
		} else if order == h5IterDec {
			slices.SortStableFunc(ltable.lnks, h5gLinkCmpCorderDec)
		}
		/* else order == H5_ITER_NATIVE */
	}
}

// h5gLinkIterateTable is H5G__link_iterate_table: iterate over a table
// containing a list of links for a group, making appropriate callbacks.
// The iteration stops without error when op returns errBIterStop. lastLnk,
// if not nil, receives the number of links passed through (including the
// skipped ones).
func h5gLinkIterateTable(ltable *h5gLinkTable, skip uint64, lastLnk *uint64, op h5gLibIterate) error {
	/* Skip over links, if requested */
	if lastLnk != nil {
		*lastLnk += skip
	}

	/* Iterate over link messages */
	for u := skip; u < uint64(len(ltable.lnks)); u++ {
		/* Make the callback */
		err := op(ltable.lnks[u])

		/* Increment the number of entries passed through */
		if lastLnk != nil {
			*lastLnk++
		}

		if err != nil {
			if errors.Is(err, errBIterStop) {
				return nil
			}
			/* Check for callback failure and pass along return value */
			return wrapf(err, H5E_SYM, H5E_CANTNEXT, "iteration operator failed")
		}
	}
	return nil
}
