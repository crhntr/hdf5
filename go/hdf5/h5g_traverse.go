package hdf5

// Translated from src/H5Gtraverse.c: resolving a path, component by
// component, from a group location, following soft links and external
// links (user-defined links whose class is known, see h5l.go).
//
// Mounting files (H5Fmount) is not supported, so the mount point traversal
// step of H5G__traverse_special is omitted; intermediate group creation
// (H5G_CRT_INTMD_GROUP) is a write operation and is not translated.

import "github.com/HDFGroup/hdf5/go/h5"

// Traversal targets (H5G_TARGET_* in H5Gprivate.h).
const (
	h5gTargetNormal = 0x0000 /* H5G_TARGET_NORMAL */
	h5gTargetSlink  = 0x0001 /* H5G_TARGET_SLINK: don't follow a soft link as the last component */
	h5gTargetMount  = 0x0002 /* H5G_TARGET_MOUNT */
	h5gTargetUdlink = 0x0004 /* H5G_TARGET_UDLINK: don't follow a user-defined link as the last component */
	h5gTargetExists = 0x0008 /* H5G_TARGET_EXISTS: only check whether the object exists */
)

// gLoc is H5G_loc_t: the location of an object, an object location
// (H5O_loc_t: the file and the object header address) plus the path the
// object was reached by (the "user path" of its H5G_name_t; "" when
// unknown, e.g. for objects opened by address).
type gLoc struct {
	f    *File
	addr haddr
	path string
}

// gTravCtx holds the part of the API context (H5CX) used by path
// traversal: the number of soft / user-defined links that may still be
// traversed (H5CX_get_nlinks / H5CX_set_nlinks).
type gTravCtx struct {
	nlinks int
}

// h5gTraverse is H5G_traverse_t: the operator called on the last component
// of a traversed path. grpLoc is the group holding the link (nil when the
// path was just "." or "/"), name the last component, lnk the link (nil
// when the group has no such link) and objLoc the location of the object
// the link resolves to (nil when it does not exist).
type h5gTraverse func(grpLoc *gLoc, name string, lnk *h5oLink, objLoc *gLoc) error

// h5gComponent is H5G__component: skip leading slashes in name and return
// the rest of the name and the length of its first component.
func h5gComponent(name string) (string, int) {
	for len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}
	n := 0
	for n < len(name) && name[n] != '/' {
		n++
	}
	return name, n
}

// h5gNameSet is H5G_name_set: the user path of an object reached through
// the link name in the group whose path is loc (H5G__build_fullpath).
func h5gNameSet(loc, name string) string {
	/* Create the object's user path, if a user path exists in the location */
	if loc == "" {
		return ""
	}
	if loc[len(loc)-1] != '/' {
		return loc + "/" + name /* Add separator, if the prefix doesn't end in one */
	}
	return loc + name
}

// gTraverseSlink is H5G__traverse_slink: traverse a soft link. objLoc (the
// location of the link's target) is updated in place; its path is kept (the
// path the object was reached by is the one through the soft link). The
// returned value reports whether the object exists (only meaningful with
// h5gTargetExists; without it a dangling link is an error).
func gTraverseSlink(ctx *gTravCtx, grpLoc *gLoc, lnk *h5oLink, objLoc *gLoc, target uint) (bool, error) {
	/* Clone the group location, so we can track the names properly */
	/* ("tracking the names properly" means to ignore the effects of the
	 *  link traversal on the object's & group's paths - QAK)
	 */
	tmpGrpLoc := *grpLoc

	/* Set up user data for traversal callback */
	chkExists := target&h5gTargetExists != 0
	exists := false

	/* Traverse the link */
	/* (H5G__traverse_slink_cb) */
	err := gTraverseReal(ctx, &tmpGrpLoc, lnk.softName, target, func(_ *gLoc, _ string, _ *h5oLink, resolved *gLoc) error {
		/* Check for dangling soft link */
		if resolved == nil {
			if chkExists {
				exists = false
				return nil
			}
			return errorf(H5E_SYM, H5E_NOTFOUND, "component not found")
		}

		/* Copy new location information for resolved object */
		/* (the object location only: the object's group hier. path is restored) */
		objLoc.f = resolved.f
		objLoc.addr = resolved.addr

		/* Indicate that the object exists */
		exists = true
		return nil
	})
	if err != nil {
		return false, wrapf(err, H5E_SYM, H5E_NOTFOUND, "unable to follow symbolic link")
	}
	return exists, nil
}

// gTraverseUd is H5G__traverse_ud: traverse a user-defined link with its
// class's traversal callback. objLoc is replaced by the location the
// callback returns. It returns false (no error) when the object does not
// exist and target has h5gTargetExists.
func gTraverseUd(ctx *gTravCtx, grpLoc *gLoc, lnk *h5oLink, objLoc *gLoc, target uint) (bool, error) {
	/* Get the link class for this type of link. */
	linkClass := h5lFindClass(lnk.typ)
	if linkClass == nil {
		return false, errorf(H5E_SYM, H5E_NOTREGISTERED, "unable to get UD link class")
	}

	/* Set up location for user-defined callback.  Use a copy of our current
	 * grp_loc. */
	grpLocCopy := *grpLoc

	/* User-defined callback function */
	newLoc, err := linkClass.travFunc(lnk.name, &grpLocCopy, lnk.udData, ctx)

	/* Check for failing to locate the object */
	if err != nil {
		/* Check if we just needed to know if the object exists */
		if target&h5gTargetExists != 0 {
			/* Indicate that the object doesn't exist */
			return false, nil
		}
		/* else, we really needed to open the object */
		return false, wrapf(err, H5E_SYM, H5E_BADID, "traversal callback returned invalid ID")
	}

	/* Copy new object's location information */
	/* (the target file is held open by the external file cache of the
	 *  file holding the link) */
	*objLoc = *newLoc
	return true, nil
}

// gTraverseSpecial is H5G__traverse_special: perform the special
// traversals a link needs (soft links, user-defined links). objExists is
// updated in place.
func gTraverseSpecial(ctx *gTravCtx, grpLoc *gLoc, lnk *h5oLink, target uint, lastComp bool, objLoc *gLoc, objExists *bool) error {
	/* If we found a symbolic link then we should follow it.  But if this
	 * is the last component of the name and the H5G_TARGET_SLINK bit of
	 * TARGET is set then we don't follow it.
	 */
	if lnk.typ == h5.LinkSoft && (target&h5gTargetSlink == 0 || !lastComp) {
		/* Decrement # of links and range check */
		if ctx.nlinks <= 0 {
			return errorf(H5E_LINK, H5E_NLINKS, "too many links")
		}
		ctx.nlinks--

		/* Traverse soft link */
		exists, err := gTraverseSlink(ctx, grpLoc, lnk, objLoc, target&h5gTargetExists)
		if err != nil {
			return wrapf(err, H5E_LINK, H5E_TRAVERSE, "symbolic link traversal failed")
		}
		*objExists = exists
	}

	/*
	 * If we found a user-defined link then we should follow it.  But if this
	 * is the last component of the name and the H5G_TARGET_UDLINK bit of
	 * TARGET is set then we don't follow it.
	 */
	if lnk.typ >= h5lTypeUdMin && (target&h5gTargetUdlink == 0 || !lastComp) {
		/* Decrement # of links and range check */
		if ctx.nlinks <= 0 {
			return errorf(H5E_LINK, H5E_NLINKS, "too many links")
		}
		ctx.nlinks--

		/* Traverse user-defined link */
		exists, err := gTraverseUd(ctx, grpLoc, lnk, objLoc, target&h5gTargetExists)
		if err != nil {
			return wrapf(err, H5E_LINK, H5E_TRAVERSE, "user-defined link traversal failed")
		}
		if !exists {
			*objExists = false
		}
	}

	/*
	 * Resolve mount points to the mounted group.
	 * (Mounting files is not supported: nothing to do.)
	 */
	return nil
}

// gTraverseReal is H5G__traverse_real: traverse a path from a location
// and call op on the last component (or on the location itself when the
// path has no components, e.g. "." or "/").
func gTraverseReal(ctx *gTravCtx, loc *gLoc, name string, target uint, op h5gTraverse) error {
	/*
	 * Where does the searching start?  For absolute names it starts at the
	 * root of the file; for relative names it starts at CWG.
	 */
	/* Check if we need to get the root group's entry */
	start := *loc
	if len(name) > 0 && name[0] == '/' {
		/* Look up root group for starting location */
		rootLoc, err := loc.f.gRootLoc()
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open root group")
		}
		start = *rootLoc
	}

	/* Deep copy of the starting location to group location */
	grpLoc := start

	/* Traverse the path */
	for {
		var nchars int
		if name, nchars = h5gComponent(name); name == "" {
			break
		}

		/*
		 * Copy the component name into a null-terminated buffer so
		 * we can pass it down to the other symbol table functions.
		 */
		comp := name[:nchars]

		/*
		 * The special name `.' is a no-op.
		 */
		if comp == "." {
			name = name[nchars:]
			continue
		}

		/* Check if this is the last component of the name */
		lastComp := false
		if s, _ := h5gComponent(name[nchars:]); s == "" {
			lastComp = true
		}

		/* Get information for object in current group */
		grpOh, err := grpLoc.f.oProtect(grpLoc.addr)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't look up component")
		}
		lnk, lookupStatus, err := grpLoc.f.gObjLookup(grpOh, comp)
		if err != nil {
			return wrapf(err, H5E_SYM, H5E_NOTFOUND, "can't look up component")
		}
		objExists := false

		/* If the lookup was OK, build object location and traverse special links, etc. */
		var objLoc *gLoc
		if lookupStatus {
			/* Build object location from the link */
			if objLoc, err = gLinkToLoc(&grpLoc, lnk); err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTINIT, "cannot initialize object location")
			}

			/* Assume object exists */
			objExists = true

			/* Perform any special traversals that the link needs */
			/* (soft links, user-defined links, file mounting, etc.) */
			if err := gTraverseSpecial(ctx, &grpLoc, lnk, target, lastComp, objLoc, &objExists); err != nil {
				return wrapf(err, H5E_LINK, H5E_TRAVERSE, "special link traversal failed")
			}
		}

		/* Check for last component in name provided */
		if lastComp {
			/* Set callback parameters appropriately, based on link being found */
			var (
				cbLnk *h5oLink
				cbLoc *gLoc
			)
			if lookupStatus {
				cbLnk = lnk
				if objExists {
					cbLoc = objLoc
				}
			}

			/* Call 'operator' routine */
			if err := op(&grpLoc, comp, cbLnk, cbLoc); err != nil {
				return wrapf(err, H5E_SYM, H5E_CALLBACK, "traversal operator failed")
			}
			return nil
		}

		/* Handle lookup failures now */
		if !lookupStatus {
			/* (Creating intermediate groups is a write operation) */
			return errorf(H5E_SYM, H5E_NOTFOUND, "component not found")
		}

		/*
		 * Advance to the next component of the path.
		 */

		/* Transfer "ownership" of the object's information to the group object */
		grpLoc = *objLoc

		/* Advance to next component in string */
		name = name[nchars:]
	}

	/* Call 'operator' routine */
	/* If we've fallen through to here, the name must be something like just '.'
	 * and we should issue the callback on that. -QAK
	 * Since we don't have a group location or a link to the object we pass in
	 * NULL.
	 */
	if err := op(nil, ".", nil, &grpLoc); err != nil {
		return wrapf(err, H5E_SYM, H5E_CANTNEXT, "traversal operator failed")
	}
	return nil
}

// gTraverse is H5G_traverse: traverse a path from loc, calling op on the
// last component. ctx is the traversal context of an enclosing traversal
// (nil for a new one, which may follow up to H5L_NUM_LINKS soft and
// user-defined links). The number of links that may be traversed is
// restored when the traversal ends.
func gTraverse(ctx *gTravCtx, loc *gLoc, name string, target uint, op h5gTraverse) error {
	/* Check args */
	if name == "" {
		return errorf(H5E_SYM, H5E_NOTFOUND, "no name given")
	}
	if loc == nil {
		return errorf(H5E_SYM, H5E_NOTFOUND, "no starting location")
	}
	if op == nil {
		return errorf(H5E_SYM, H5E_NOTFOUND, "no operation provided")
	}

	/* Retrieve the original # of soft / UD links that are able to be traversed
	 * (So that multiple calls to H5G_traverse don't incorrectly look
	 *  like they've traversed too many.  Nested calls, like in H5L__move(),
	 *  may need their own mechanism to set & reset the # of links to traverse)
	 */
	if ctx == nil {
		ctx = &gTravCtx{nlinks: h5lNumLinks}
	}
	origNlinks := ctx.nlinks

	/* Go perform "real" traversal */
	err := gTraverseReal(ctx, loc, name, target, op)

	/* Reset the # of soft / UD links that can be traversed */
	ctx.nlinks = origNlinks

	if err != nil {
		return wrapf(err, H5E_SYM, H5E_NOTFOUND, "internal path traversal failed")
	}
	return nil
}
