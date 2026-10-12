package hdf5

// The public file-level object API (see API.md): the root group and
// opening objects by absolute path or by address.

// rootLoc is the location of the root group (H5G_root_loc) without
// loading its object header.
func (f *File) rootLoc() *gLoc { return &gLoc{f: f, addr: f.sblock.rootAddr, path: "/"} }

// Root returns the root group. If the root group's object header cannot be
// loaded, the error is reported by the group's methods.
func (f *File) Root() *Group {
	g := &Group{f: f, addr: f.sblock.rootAddr, path: "/"}
	if loc, err := f.gMkroot(); err == nil {
		if oh, err := f.oProtect(loc.addr); err == nil {
			g.oh = oh
		}
	}
	return g
}

// Open opens the object at path (absolute, or relative to the root group),
// following soft and external links, like H5Oopen.
func (f *File) Open(path string) (Object, error) { return openObjectName(f.rootLoc(), path) }

// OpenGroup opens the group at path, like H5Gopen2.
func (f *File) OpenGroup(path string) (*Group, error) { return openGroupName(f.rootLoc(), path) }

// OpenDataset opens the dataset at path, like H5Dopen2.
func (f *File) OpenDataset(path string) (*Dataset, error) {
	return openDatasetName(f.rootLoc(), path)
}

// OpenDatatype opens the committed datatype at path, like H5Topen2.
func (f *File) OpenDatatype(path string) (*Datatype, error) {
	return openDatatypeName(f.rootLoc(), path)
}

// OpenAddress opens the object whose object header is at addr (as when
// dereferencing an object reference, H5Oopen_by_token). The object's Path
// is "".
func (f *File) OpenAddress(addr uint64) (Object, error) {
	return openObjectAt(&gLoc{f: f, addr: addr})
}
