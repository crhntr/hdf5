package hdf5

// Translated from src/H5Gloc.c (read side only): operations on objects
// located by a path relative to a group location.

import "github.com/HDFGroup/hdf5/go/h5"

// gLocFind is H5G_loc_find: find an object from a location (following
// soft and external links). ctx is the traversal context of an enclosing
// traversal, or nil.
func gLocFind(ctx *gTravCtx, loc *gLoc, name string) (*gLoc, error) {
	/* Check args. */
	if name == "" {
		return nil, errorf(H5E_SYM, H5E_BADVALUE, "invalid object name")
	}

	/* Traverse group hierarchy to locate object */
	/* (H5G__loc_find_cb) */
	var objLocOut *gLoc
	err := gTraverse(ctx, loc, name, h5gTargetNormal, func(_ *gLoc, name string, _ *h5oLink, objLoc *gLoc) error {
		/* Check if the name in this group resolved to a valid object */
		if objLoc == nil {
			return errorf(H5E_SYM, H5E_NOTFOUND, "object '%s' doesn't exist", name)
		}

		/* Take ownership of the object's group location */
		l := *objLoc
		objLocOut = &l
		return nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't find object")
	}
	return objLocOut, nil
}

// gLocFindByIdx is H5G_loc_find_by_idx: find the object the n-th link (in
// the given index order) of the group groupName (relative to loc) points
// to, following the link.
func gLocFindByIdx(loc *gLoc, groupName string, idxType h5Index, order h5IterOrder, n uint64) (*gLoc, error) {
	var out *gLoc

	/* Traverse group hierarchy to locate object */
	/* (H5G__loc_find_by_idx_cb) */
	ctx := &gTravCtx{nlinks: h5lNumLinks}
	err := gTraverse(ctx, loc, groupName, h5gTargetNormal, func(_ *gLoc, _ string, _ *h5oLink, objLoc *gLoc) error {
		/* Check if the name in this group resolved to a valid link */
		if objLoc == nil {
			return errorf(H5E_SYM, H5E_NOTFOUND, "group doesn't exist")
		}

		/* Query link */
		grpOh, err := objLoc.f.oProtect(objLoc.addr)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_NOTFOUND, "link not found")
		}
		fndLnk, err := objLoc.f.gObjLookupByIdx(grpOh, idxType, order, n)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_NOTFOUND, "link not found")
		}

		/* Build the initial object location for the link */
		l, err := gLinkToLoc(objLoc, fndLnk)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTINIT, "cannot initialize object location")
		}

		/* Perform any special traversals that the link needs */
		/* (soft links, user-defined links, file mounting, etc.) */
		/* (may modify the object location) */
		objExists := false
		if err := gTraverseSpecial(ctx, objLoc, fndLnk, h5gTargetNormal, true, l, &objExists); err != nil {
			return wrapf(err, H5E_LINK, H5E_TRAVERSE, "special link traversal failed")
		}
		out = l
		return nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't find object")
	}
	return out, nil
}

// gLocInfo is H5G_loc_info: the object information of the object name
// (relative to loc).
func gLocInfo(loc *gLoc, name string) (h5.ObjectInfo, error) {
	var oinfo h5.ObjectInfo

	/* Traverse group hierarchy to locate object */
	/* (H5G__loc_info_cb) */
	err := gTraverse(nil, loc, name, h5gTargetNormal, func(_ *gLoc, _ string, _ *h5oLink, objLoc *gLoc) error {
		/* Check if the name in this group resolved to a valid link */
		if objLoc == nil {
			return errorf(H5E_SYM, H5E_NOTFOUND, "name doesn't exist")
		}

		/* Query object information */
		info, err := objLoc.f.oGetInfoByAddr(objLoc.addr)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTGET, "can't get object info")
		}
		oinfo = info
		return nil
	})
	if err != nil {
		return h5.ObjectInfo{}, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't find object")
	}
	return oinfo, nil
}

// gLocGetComment is H5G_loc_get_comment: the comment of the object name
// (relative to loc); "" when it has none.
func gLocGetComment(loc *gLoc, name string) (string, error) {
	var comment string

	/* Traverse group hierarchy to locate object */
	/* (H5G__loc_get_comment_cb) */
	err := gTraverse(nil, loc, name, h5gTargetNormal, func(_ *gLoc, _ string, _ *h5oLink, objLoc *gLoc) error {
		/* Check if the name in this group resolved to a valid link */
		if objLoc == nil {
			return errorf(H5E_SYM, H5E_NOTFOUND, "name doesn't exist")
		}

		/* Query object comment */
		oh, err := objLoc.f.oProtect(objLoc.addr)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTGET, "can't get comment")
		}
		s, ok, err := objLoc.f.oGetComment(oh)
		if err != nil {
			return err
		}
		if ok {
			comment = s
		}
		return nil
	})
	if err != nil {
		return "", wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't find object")
	}
	return comment, nil
}

// gLocExists is H5G_loc_exists: whether the object name (relative to loc)
// exists, following soft and external links (a dangling link does not
// exist).
func gLocExists(loc *gLoc, name string) (bool, error) {
	var exists bool

	/* Traverse group hierarchy to locate object */
	/* (H5G__loc_exists_cb) */
	err := gTraverse(nil, loc, name, h5gTargetExists, func(_ *gLoc, _ string, _ *h5oLink, objLoc *gLoc) error {
		/* Check if the name in this group resolved to a valid object */
		exists = objLoc != nil
		return nil
	})
	if err != nil {
		return false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't check if object exists")
	}
	return exists, nil
}
