package hdf5

// Error classes used by the dataset module (h5d_*.go) that the other
// modules did not need.

const (
	H5E_EFL   errMajor = "H5E_EFL"
	H5E_PLIST errMajor = "H5E_PLIST"
)

const (
	H5E_CANTFIND  errMinor = "H5E_CANTFIND"
	H5E_BADSELECT errMinor = "H5E_BADSELECT"
)
