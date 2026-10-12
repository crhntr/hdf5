package hdf5

// Translated from src/H5Gint.c (read side only): opening groups, and
// iterating over (H5G_iterate) and recursively visiting (H5G_visit) the
// links of a group.
//
// There are no group IDs or open-object lists here: an open group is its
// location and its (cached) object header.

import (
	"errors"

	"github.com/HDFGroup/hdf5/go/h5"
)

// gOpenName is H5G__open_name: open the group name (relative to loc).
func gOpenName(loc *gLoc, name string) (*gLoc, *h5o, error) {
	/* Find the group object */
	grpLoc, err := gLocFind(nil, loc, name)
	if err != nil {
		return nil, nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "group not found")
	}

	/* Check that the object found is the correct type */
	objType, err := grpLoc.f.oObjType(grpLoc.addr)
	if err != nil {
		return nil, nil, wrapf(err, H5E_SYM, H5E_CANTGET, "can't get object type")
	}
	if objType != h5.ObjectGroup {
		return nil, nil, errorf(H5E_SYM, H5E_BADTYPE, "not a group")
	}

	/* Open the group */
	oh, err := gOpen(grpLoc)
	if err != nil {
		return nil, nil, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open group")
	}
	return grpLoc, oh, nil
}

// gOpen is H5G_open: open a group at a location (the C function also
// shares the group's in-core state between opens through the file's list
// of open objects; here the object header cache plays that role).
func gOpen(loc *gLoc) (*h5o, error) {
	/* Open the group object */
	oh, err := gOpenOid(loc)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "not found")
	}
	return oh, nil
}

// gOpenOid is H5G__open_oid: open the object header of a group and check
// that it has the messages of a group.
func gOpenOid(loc *gLoc) (*h5o, error) {
	/* Grab the object header */
	oh, err := loc.f.oProtect(loc.addr)
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open group")
	}

	/* Check if this object has the right message(s) to be treated as a group */
	if !loc.f.msgExists(oh, h5oStabID) && !loc.f.msgExists(oh, h5oLinfoID) {
		return nil, errorf(H5E_SYM, H5E_CANTOPENOBJ, "not a group")
	}
	return oh, nil
}

// gIterate is H5G_iterate: iterate over the links of the group groupName
// (relative to loc), calling op with each link's information. op may
// return errBIterStop to stop early.
func gIterate(loc *gLoc, groupName string, idxType h5Index, order h5IterOrder, skip uint64, lastLnk *uint64, op func(info h5.Link) error) error {
	/* Open the group on which to operate. */
	grpLoc, grpOh, err := gOpenName(loc, groupName)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open group")
	}

	/* Call the real group iteration routine */
	/* (H5G__iterate_cb: H5G_LINK_OP_NEW) */
	if err := grpLoc.f.gObjIterate(grpOh, idxType, order, skip, lastLnk, func(lnk *h5oLink) error {
		/* Retrieve the info for the link */
		info, err := gLinkToInfo(lnk)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get info for link")
		}

		/* Make the application callback */
		return op(info)
	}); err != nil {
		return wrapf(err, H5E_SYM, H5E_BADITER, "error iterating over links")
	}
	return nil
}

// h5gVisitKey is H5_obj_t: the "position" of an object (its file and
// address), used to detect objects visited before.
type h5gVisitKey struct {
	f    *File
	addr haddr
}

// h5gVisitOp is the H5L_iterate2_t operator of H5G_visit: called for each
// link with the location of the group holding the link, the path of the
// link relative to the starting group, and the link. It may return
// errBIterStop to stop the visit early.
type h5gVisitOp func(grpLoc *gLoc, path string, lnk *h5oLink) error

// h5gIterVisitUd is H5G_iter_visit_ud_t: user data for the link visit
// callback.
type h5gIterVisitUd struct {
	/* Common information */
	currLoc *gLoc /* Location of starting group */

	/* Information for visiting links */
	idxType h5Index     /* Index to use */
	order   h5IterOrder /* Iteration order within index */
	visited map[h5gVisitKey]bool
	op      h5gVisitOp /* Application callback */
	stopped bool       /* The application callback asked to stop */

	/* Information for recursion */
	path string /* Path name of the link */
}

// gVisitCb is H5G__visit_cb: callback function for recursively visiting
// links from a group.
func gVisitCb(udata *h5gIterVisitUd, lnk *h5oLink) error {
	/* Build the link's relative path name */
	oldPath := udata.path
	defer func() {
		/* Reset path back to incoming path */
		udata.path = oldPath
	}()
	udata.path = oldPath + lnk.name

	/* Make the application callback */
	if err := udata.op(udata.currLoc, udata.path, lnk); err != nil {
		if errors.Is(err, errBIterStop) {
			udata.stopped = true
		}
		return err
	}

	/* Check for doing more work */
	if lnk.typ == h5.LinkHard {
		/* Find the object */
		/* (H5G_loc_find on the link's name resolves to the link's address:
		 *  without mount points there is nothing else to resolve) */
		objLoc, err := gLinkToLoc(udata.currLoc, lnk)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_NOTFOUND, "object not found")
		}

		/* Construct unique "position" for this object */
		objPos := h5gVisitKey{f: objLoc.f, addr: objLoc.addr}

		/* Check if we've seen the object the link references before */
		if !udata.visited[objPos] {
			/* Get the object's reference count and type */
			_, otype, err := objLoc.f.oGetRcAndType(objLoc.addr)
			if err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get object info")
			}

			/* Add it to the list of visited objects */
			udata.visited[objPos] = true

			/* If it's a group, we recurse into it */
			if otype == h5.ObjectGroup {
				oldLoc := udata.currLoc  /* Pointer to previous group location info */
				idxType := udata.idxType /* Type of index to use */

				/* Add the path separator to the current path */
				udata.path += "/"

				objOh, err := objLoc.f.oProtect(objLoc.addr)
				if err != nil {
					return wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get object info")
				}

				/* Attempt to get the link info for this group */
				linfo, linfoExists, err := objLoc.f.gObjGetLinfo(objOh)
				if err != nil {
					return wrapf(err, H5E_SYM, H5E_CANTGET, "can't check for link info message")
				}
				if linfoExists {
					/* Check for creation order tracking, if creation order index lookup requested */
					if idxType == h5IndexCrtOrder {
						/* Check if creation order is tracked */
						if !linfo.trackCorder {
							/* Switch to name order for this group */
							idxType = h5IndexName
						}
					}
				} else {
					/* Can only perform name lookups on groups with symbol tables */
					if idxType != h5IndexName {
						/* Switch to name order for this group */
						idxType = h5IndexName
					}
				}

				/* Point to this group's location info */
				udata.currLoc = objLoc

				/* Iterate over links in group */
				err = objLoc.f.gObjIterate(objOh, idxType, udata.order, 0, nil, func(l *h5oLink) error {
					return gVisitCb(udata, l)
				})

				/* Restore location */
				udata.currLoc = oldLoc

				if err != nil {
					return err
				}
				if udata.stopped {
					return errBIterStop
				}
			}
		}
	}
	return nil
}

// gVisit is H5G_visit: recursively visit the links of the group groupName
// (relative to loc) and of the groups below it, depth first, calling op
// for each link (before descending into the group it points to). Objects
// are only descended into the first time they are reached, which cuts
// hard-link cycles. Soft and external links are reported, not followed.
// op may return errBIterStop to stop early (gVisit then returns nil).
func gVisit(loc *gLoc, groupName string, idxType h5Index, order h5IterOrder, op h5gVisitOp) error {
	/* Open the group to begin visiting within */
	startLoc, grpOh, err := gOpenName(loc, groupName)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open group")
	}

	/* Set up user data */
	udata := &h5gIterVisitUd{
		currLoc: startLoc,
		idxType: idxType,
		order:   order,
		op:      op,

		/* Allocate space for the path name */
		path: "",

		/* Create skip list to store visited object information */
		visited: map[h5gVisitKey]bool{},
	}

	/* Add it to the list of visited objects */
	udata.visited[h5gVisitKey{f: startLoc.f, addr: startLoc.addr}] = true

	/* Attempt to get the link info for this group */
	linfo, linfoExists, err := startLoc.f.gObjGetLinfo(grpOh)
	if err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTGET, "can't check for link info message")
	}
	if linfoExists {
		/* Check for creation order tracking, if creation order index lookup requested */
		if idxType == h5IndexCrtOrder {
			/* Check if creation order is tracked */
			if !linfo.trackCorder {
				/* Switch to name order for this group */
				idxType = h5IndexName
			}
		}
	} else {
		/* Can only perform name lookups on groups with symbol tables */
		if idxType != h5IndexName {
			/* Switch to name order for this group */
			idxType = h5IndexName
		}
	}

	/* Call the link iteration routine */
	if err := startLoc.f.gObjIterate(grpOh, idxType, order, 0, nil, func(lnk *h5oLink) error {
		return gVisitCb(udata, lnk)
	}); err != nil {
		return wrapf(err, H5E_SYM, H5E_BADITER, "can't visit links")
	}
	return nil
}

// gGetInfoByName is H5G__get_info_by_name: information about the group
// name (relative to loc).
func gGetInfoByName(loc *gLoc, name string) (h5gInfo, error) {
	/* Find the object's location */
	grpLoc, err := gLocFind(nil, loc, name)
	if err != nil {
		return h5gInfo{}, wrapf(err, H5E_SYM, H5E_NOTFOUND, "group not found")
	}

	/* Retrieve the group's information */
	oh, err := grpLoc.f.oProtect(grpLoc.addr)
	if err != nil {
		return h5gInfo{}, wrapf(err, H5E_SYM, H5E_CANTINIT, "can't retrieve group info")
	}
	info, err := grpLoc.f.gObjInfo(oh)
	if err != nil {
		return h5gInfo{}, wrapf(err, H5E_SYM, H5E_CANTINIT, "can't retrieve group info")
	}
	return info, nil
}
