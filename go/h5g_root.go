package hdf5

// Translated from src/H5Groot.c (read side only): the root group of a
// file.
//
// The C library opens the root group when the file is opened (H5G_mkroot,
// called by H5F_open) and keeps it in the shared file struct. Here the root
// group is located on demand from the superblock; its object header is
// cached like any other.

// gMkroot is H5G_mkroot with create_root = false, for a file opened
// read-only: the location of the root group, whose object header is loaded
// to check that it can be opened.
//
// The superblock (versions 0 and 1) also caches the addresses of the root
// group's symbol table in its root symbol table entry. When the file is
// opened for writing, the C library checks the symbol table message
// against them (H5G__stab_valid) and repairs the message; for read-only
// access, as here, the cached addresses are not used (they are dropped when
// the root group has no symbol table message, e.g. after an external link
// was added to it), so a file whose root symbol table message is corrupt
// cannot be read, as with the C library.
func (f *File) gMkroot() (*gLoc, error) {
	/* Create root group object location from f */
	rootLoc := &gLoc{f: f, addr: f.sblock.rootAddr}

	/*
	 * Open the root object as a group.
	 */
	if _, err := f.oProtect(rootLoc.addr); err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_CANTOPENOBJ, "unable to open root group")
	}

	/* Actions to take if the symbol table information is cached */
	/* (When the root symbol table entry caches the symbol table but the
	 *  root group has no symbol table message, e.g. after an external link
	 *  was added to it, the C library drops the cached information; it is
	 *  never used for read-only access, so there is nothing to do.) */

	/* Create the path names for the root group's entry */
	rootLoc.path = "/"
	return rootLoc, nil
}

// gRootLoc is H5G_root_loc: the location of the root group of the file.
func (f *File) gRootLoc() (*gLoc, error) {
	/* Retrieve the root location */
	loc, err := f.gMkroot()
	if err != nil {
		return nil, wrapf(err, H5E_SYM, H5E_BADVALUE, "unable to create location for file")
	}
	return loc, nil
}
