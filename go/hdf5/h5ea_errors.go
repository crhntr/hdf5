package hdf5

// Minor error classes used by the extensible / fixed array chunk index code
// (src/H5Dearray.c, src/H5Dfarray.c) that errors.go does not define.
const (
	H5E_ALREADYINIT   errMinor = "H5E_ALREADYINIT"
	H5E_UNINITIALIZED errMinor = "H5E_UNINITIALIZED"
	H5E_CALLBACK      errMinor = "H5E_CALLBACK"
)
