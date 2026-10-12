package hdf5

// Stub for the source dataset lookup of virtual datasets.
//
// TODO(integration): replace this file once the group traversal of the
// public API (file.go / group.go) lands. The real implementation is the
// C library's H5F_prefix_open_file + H5D__open_name as used by
// H5D__virtual_open_source_dset:
//
//   - fileName "." means the virtual dataset's own file f.
//   - Otherwise open the source file the way H5F_prefix_open_file does with
//     H5F_PREFIX_VDS: an absolute name is tried as is; a relative name is
//     tried with the virtual prefix (f.h5dBuildFilePrefix(f.opts.vdsPrefix),
//     i.e. WithVirtualPrefix / HDF5_VDS_PREFIX, "${ORIGIN}" expanded), then
//     relative to the directory of f (f.dir), then as given (relative to the
//     current directory). Keep opened files in f.extFiles so they are closed
//     with f.
//   - Look up dsetName (an absolute path in the source file) with the group
//     traversal, check that the object is a dataset and dOpen its header.
//
// A missing source file or dataset is not an error for a virtual dataset
// (its elements read as the fill value): return (nil, nil, nil), or an
// error matching ErrNotFound. Any other error aborts the read.
func (f *File) dVirtualOpenSource(fileName, dsetName string) (srcFile *File, ds *h5dShared, err error) {
	return nil, nil, errorf(H5E_DATASET, H5E_UNSUPPORTED, "virtual dataset source %q in %q: source dataset lookup not implemented yet", dsetName, fileName)
}
