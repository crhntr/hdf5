package hdf5

// Temporary stubs for functionality supplied by later translation waves.
// Each is replaced by a real implementation in its own file; keep this file
// compiling by deleting a stub when its replacement lands.

// sbLoad is H5FD_sb_load: let the driver decode its driver information
// (family member size, multi driver layout). Implemented in h5fd_family.go /
// h5fd_multi.go.
func (f *File) sbLoad(d *drvinfo) error {
	switch d.name {
	case "NCSAfami", "NCSAmult", "NCSAmulti":
		return errorf(H5E_VFL, H5E_UNSUPPORTED, "file driver %q not supported yet", d.name)
	}
	return nil
}

// superExtRead decodes the superblock extension object header messages
// (driver info, B-tree K values, shared message table). Implemented in
// h5f_superext.go once object headers are available.
func (f *File) superExtRead() error { return nil }
