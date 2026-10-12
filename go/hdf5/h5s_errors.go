package hdf5

// Error minor codes used by the dataspace module that errors.go does not
// define.
const (
	H5E_CANTINSERT errMinor = "H5E_CANTINSERT"
	H5E_CANTDELETE errMinor = "H5E_CANTDELETE"
)
