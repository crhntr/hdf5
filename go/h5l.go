package hdf5

// Translated from src/H5L.c and src/H5Lint.c (read side only): link
// classes, and querying links (H5Lget_info, H5Lget_val, H5Lexists).
//
// The C library keeps a global table of registered link classes
// (H5L_table_g) into which the external link class is registered when the
// library is initialized (H5L_register_external). There are no globals
// here: h5lFindClass knows the one built-in user-defined class, external
// links. Other user-defined link classes cannot be registered, so their
// links can be listed but not traversed, as in a C program that has not
// registered the class.

import "github.com/crhntr/hdf5/go/h5"

// Link type limits (H5Lpublic.h): H5L_TYPE_BUILTIN_MAX.
const h5lTypeBuiltinMax = h5.LinkSoft /* Maximum value link value for "built-in" link types */

// H5L_NUM_LINKS: default maximum number of soft or user-defined links to
// traverse when resolving a path (H5L_ACS_NLINKS_DEF).
const h5lNumLinks = 16

// h5lClass is H5L_class_t (the read-side callbacks).
type h5lClass struct {
	id   h5.LinkType /* Link type ID              */
	comm string      /* Comment for debugging     */
	/* Callback during link traversal: the location of the object the link
	 * points to. curGroup is the group holding the link; ctx carries the
	 * number of links that may still be traversed (the H5CX nlinks). */
	travFunc func(linkName string, curGroup *gLoc, udata []byte, ctx *gTravCtx) (*gLoc, error)
	/* Callback for queries: the link value as reported to users. */
	queryFunc func(linkName string, udata []byte) ([]byte, error)
}

// h5lFindClass is H5L_find_class: the link class with the given type ID,
// or nil when no such class is registered.
func h5lFindClass(id h5.LinkType) *h5lClass {
	/* Get the index in the global table */
	/* (only the external link class, H5L_EXTERN_LINK_CLASS, is registered) */
	if id == h5.LinkExternal {
		return h5lExternLinkClass()
	}
	return nil
}

// lGetValReal is H5L__get_val_real: the value of a soft or user-defined
// link (the soft link path, or the user-defined link data as returned by
// the class's query callback; nil when the class is not registered).
func lGetValReal(lnk *h5oLink) ([]byte, error) {
	/* Check for soft link */
	if lnk.typ == h5.LinkSoft {
		/* Copy to output buffer */
		return []byte(lnk.softName), nil
	}

	/* Check for user-defined link */
	if lnk.typ >= h5lTypeUdMin {
		/* Get the link class for this type of link.  It's okay if the class
		 * isn't registered, though--we just can't give any more information
		 * about it
		 */
		linkClass := h5lFindClass(lnk.typ)
		if linkClass != nil && linkClass.queryFunc != nil {
			val, err := linkClass.queryFunc(lnk.name, lnk.udData)
			if err != nil {
				return nil, wrapf(err, H5E_LINK, H5E_CALLBACK, "query callback returned failure")
			}
			return val, nil
		}
		return nil, nil
	}
	return nil, errorf(H5E_LINK, H5E_BADTYPE, "object is not a symbolic or user-defined link")
}

// lGetVal is H5L__get_val: the value of the soft or user-defined link name
// (relative to loc). The final link is not followed.
func (f *File) lGetVal(loc *gLoc, name string) ([]byte, error) {
	var val []byte

	/* Traverse the group hierarchy to locate the object to get info about */
	/* (H5L__get_val_cb) */
	err := gTraverse(nil, loc, name, h5gTargetSlink|h5gTargetUdlink, func(_ *gLoc, name string, lnk *h5oLink, _ *gLoc) error {
		/* Check if the name in this group resolved to a valid link */
		if lnk == nil {
			return errorf(H5E_LINK, H5E_NOTFOUND, "'%s' doesn't exist", name)
		}

		/* Retrieve the value for the link */
		v, err := lGetValReal(lnk)
		if err != nil {
			return wrapf(err, H5E_LINK, H5E_CANTGET, "can't retrieve link value")
		}
		val = v
		return nil
	})
	if err != nil {
		return nil, wrapf(err, H5E_LINK, H5E_NOTFOUND, "name doesn't exist")
	}
	return val, nil
}

// lExists is H5L__exists: whether the link name exists (relative to loc;
// every intermediate component must exist). The final link is not
// followed, so dangling soft links exist.
func (f *File) lExists(loc *gLoc, name string) (bool, error) {
	/* A path of "/" will always exist in a file */
	if name == "/" {
		return true, nil
	}

	/* Traverse the group hierarchy to locate the object to get info about */
	/* (H5L__exists_final_cb) */
	var exists bool
	err := gTraverse(nil, loc, name, h5gTargetSlink|h5gTargetUdlink, func(_ *gLoc, _ string, lnk *h5oLink, _ *gLoc) error {
		/* Check if the name in this group resolved to a valid link */
		exists = lnk != nil
		return nil
	})
	if err != nil {
		return false, wrapf(err, H5E_LINK, H5E_EXISTS, "link doesn't exist")
	}
	return exists, nil
}

// lGetInfo is H5L_get_info: information about the link name (relative to
// loc). The final link is not followed. The link message is returned too.
func (f *File) lGetInfo(loc *gLoc, name string) (h5.Link, *h5oLink, error) {
	var (
		info  h5.Link
		found *h5oLink
	)

	/* Traverse the group hierarchy to locate the object to get info about */
	/* (H5L__get_info_cb) */
	err := gTraverse(nil, loc, name, h5gTargetSlink|h5gTargetUdlink, func(_ *gLoc, _ string, lnk *h5oLink, _ *gLoc) error {
		/* Check if the name in this group resolved to a valid link */
		if lnk == nil {
			return errorf(H5E_LINK, H5E_NOTFOUND, "name doesn't exist")
		}

		/* Get information from the link */
		i, err := gLinkToInfo(lnk)
		if err != nil {
			return wrapf(err, H5E_LINK, H5E_CANTGET, "can't get link info")
		}
		info, found = i, lnk
		return nil
	})
	if err != nil {
		return h5.Link{}, nil, wrapf(err, H5E_LINK, H5E_EXISTS, "name doesn't exist")
	}
	return info, found, nil
}
