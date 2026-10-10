package hdf5

// STUB — to be replaced by the translation of the superblock extension
// handling in H5F__super_read (src/H5Fsuper.c).

// superExtRead decodes the superblock extension object header messages
// that matter for reading: driver info (H5O_DRVINFO_ID), B-tree 'K' values
// (H5O_BTREEK_ID) and the shared message table (H5O_SHMESG_ID, which sets
// f.sohm).
func (f *File) superExtRead() error { return nil }
