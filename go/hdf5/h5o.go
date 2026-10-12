package hdf5

// Translated from src/H5O.c (read side only). The public H5O API functions
// are thin wrappers that locate the object and call into src/H5Oint.c; the
// internal equivalents of the read-only ones are below. The object header
// machinery itself is in h5o_cache.go (src/H5Ocache.c), h5o_int.go
// (src/H5Oint.c), h5o_message.go (src/H5Omessage.c), h5o_shared.go
// (src/H5Oshared.c) and the per-message h5o_*.go files.

import "github.com/HDFGroup/hdf5/go/h5"

// oGetInfoByAddr is the core of H5Oget_info3 / H5Oget_info_by_name3 once the
// object has been located: the object information for the object header at
// addr.
func (f *File) oGetInfoByAddr(addr haddr) (h5.ObjectInfo, error) {
	/* Retrieve the object's information */
	oh, err := f.oProtect(addr)
	if err != nil {
		return h5.ObjectInfo{}, wrapf(err, H5E_OHDR, H5E_CANTPROTECT, "unable to load object header")
	}
	info, err := f.oGetInfo(oh)
	if err != nil {
		return h5.ObjectInfo{}, wrapf(err, H5E_OHDR, H5E_CANTGET, "can't retrieve object info")
	}
	return info, nil
}

// oGetComment is the core of H5Oget_comment (H5G_loc_get_comment): the
// object's comment (name message), and false when it has none.
func (f *File) oGetComment(oh *h5o) (string, bool, error) {
	/* Get the comment, if there is one */
	if !f.msgExists(oh, h5oNameID) {
		return "", false, nil
	}
	v, err := f.msgRead(oh, h5oNameID)
	if err != nil {
		return "", false, wrapf(err, H5E_OHDR, H5E_CANTGET, "can't get comment")
	}
	return string(v.(h5oName)), true, nil
}
