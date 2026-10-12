package hdf5

// Translated from src/H5Lexternal.c: the external link class, plus
// H5Lunpack_elink_val from src/H5L.c.
//
// An external link's value is a version/flags byte followed by two
// NUL-terminated strings: the name of the target file and the path of the
// object in that file. Traversing the link opens the target file (searched
// for as H5F_prefix_open_file does, see h5f_efc.go), which stays open, in
// the external file cache of the file holding the link, until that file is
// closed.

import (
	"bytes"

	"github.com/HDFGroup/hdf5/go/h5"
)

// External link format (H5Lpkg.h / H5Lprivate.h).
const (
	h5lExtVersion  = 0    /* H5L_EXT_VERSION: version of external link format */
	h5lExtFlagsAll = 0x01 /* H5L_EXT_FLAGS_ALL: valid flag bits */
)

// h5lExternLinkClass is H5L_EXTERN_LINK_CLASS: the default external link
// class.
func h5lExternLinkClass() *h5lClass {
	return &h5lClass{
		id:        h5.LinkExternal,   /* Link type id number            */
		comm:      "external",        /* Link name for debugging        */
		travFunc:  h5lExternTraverse, /* The actual traversal function  */
		queryFunc: h5lExternQuery,    /* Query callback                 */
	}
}

// h5lCString returns the NUL-terminated string at the start of b (all of b
// when there is no NUL) and the bytes after the terminator.
func h5lCString(b []byte) (string, []byte) {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i]), b[i+1:]
	}
	return string(b), nil
}

// h5lExternTraverse is H5L__extern_traverse: the default traversal function
// for external links. It opens the target file (unless following external
// links is disabled with WithExternalLinks(false)) and returns the location
// of the object the link points to in that file.
func h5lExternTraverse(_ string, curGroup *gLoc, udata []byte, ctx *gTravCtx) (*gLoc, error) {
	/* Sanity checks */
	if len(udata) == 0 {
		return nil, errorf(H5E_LINK, H5E_CANTDECODE, "empty external link value")
	}

	/* Check external link version & flags */
	if (udata[0]>>4)&0x0F > h5lExtVersion {
		return nil, errorf(H5E_LINK, H5E_CANTDECODE, "bad version number for external link")
	}
	if (udata[0]&0x0F)&^h5lExtFlagsAll != 0 {
		return nil, errorf(H5E_LINK, H5E_CANTDECODE, "bad flags for external link")
	}

	/* Gather some information from the external link's user data */
	fileName, rest := h5lCString(udata[1:])
	objName, _ := h5lCString(rest)

	/* Get the location for the group holding the external link */
	f := curGroup.f

	/* (The file access property list of the parent file is used for the
	 *  target file; reading through external links can be disabled.) */
	if !f.opts.followExternalLinks {
		return nil, errorf(H5E_LINK, H5E_CANTOPENFILE, "unable to open external file, external link file name = '%s': following external links is disabled", fileName)
	}

	/* Search for the target file */
	extFile, err := f.prefixOpenFile(h5fPrefixElink, f.opts.extLinkPrefix, fileName)
	if err != nil {
		return nil, wrapf(err, H5E_LINK, H5E_CANTOPENFILE, "unable to open external file, external link file name = '%s'", fileName)
	}

	/* Retrieve the "group location" for the file's root group */
	rootLoc, err := extFile.gRootLoc()
	if err != nil {
		return nil, wrapf(err, H5E_LINK, H5E_BADVALUE, "unable to create location for file")
	}

	/* Open the object referenced in the external file */
	loc, err := oOpenName(ctx, rootLoc, objName)
	if err != nil {
		return nil, wrapf(err, H5E_LINK, H5E_CANTOPENOBJ, "unable to open object")
	}
	return loc, nil
}

// h5lExternQuery is H5L__extern_query: the query function for external
// links: the link value, verbatim.
func h5lExternQuery(_ string, udata []byte) ([]byte, error) {
	if len(udata) == 0 {
		return nil, errorf(H5E_LINK, H5E_CANTDECODE, "empty external link value")
	}

	/* Check external link version & flags */
	if (udata[0]>>4)&0x0F != h5lExtVersion {
		return nil, errorf(H5E_LINK, H5E_CANTDECODE, "bad version number for external link")
	}
	if (udata[0]&0x0F)&^h5lExtFlagsAll != 0 {
		return nil, errorf(H5E_LINK, H5E_CANTDECODE, "bad flags for external link")
	}

	/* Copy the udata verbatim */
	return append([]byte(nil), udata...), nil
}

// h5lUnpackElinkVal is H5Lunpack_elink_val (src/H5L.c): given the "link
// value" of an external link, return its flags, the target file name and
// the object path.
func h5lUnpackElinkVal(extLinkval []byte) (flags uint8, filename, objPath string, err error) {
	/* Sanity check external link buffer */
	if len(extLinkval) == 0 {
		return 0, "", "", errorf(H5E_ARGS, H5E_BADVALUE, "not an external link linkval buffer")
	}
	lnkVersion := (extLinkval[0] >> 4) & 0x0F
	lnkFlags := extLinkval[0] & 0x0F
	if lnkVersion > h5lExtVersion {
		return 0, "", "", errorf(H5E_LINK, H5E_CANTDECODE, "bad version number for external link")
	}
	if lnkFlags&^h5lExtFlagsAll != 0 {
		return 0, "", "", errorf(H5E_LINK, H5E_CANTDECODE, "bad flags for external link")
	}
	linkSize := len(extLinkval)
	if linkSize <= 2 {
		return 0, "", "", errorf(H5E_ARGS, H5E_BADVALUE, "not a valid external link buffer")
	}

	/* Try to do some error checking.  If the last character in the linkval
	 * (the last character of obj_path) isn't NULL, then something's wrong.
	 */
	if extLinkval[linkSize-1] != 0 {
		return 0, "", "", errorf(H5E_ARGS, H5E_BADVALUE, "linkval buffer is not NULL-terminated")
	}

	/* We're now guaranteed that strlen won't segfault, since the buffer has
	 * at least one NULL in it.
	 */
	n := bytes.IndexByte(extLinkval[1:], 0)

	/* If the first NULL we found was at the very end of the buffer, then
	 * this external link value has no object name and is invalid.
	 */
	if n+1 >= linkSize-1 {
		return 0, "", "", errorf(H5E_ARGS, H5E_BADVALUE, "linkval buffer doesn't contain an object path")
	}

	/* If we got here then the buffer contains (at least) two strings packed
	 * in the correct way.  Assume it's correct and return pointers to the
	 * filename and object path.
	 */
	filename = string(extLinkval[1 : 1+n])
	objPath, _ = h5lCString(extLinkval[1+n+1:])

	/* Set the flags to return */
	return lnkFlags, filename, objPath, nil
}
