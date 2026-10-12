package hdf5

// Error classes used by the group, link and public object code (h5g_*.go,
// h5l*.go, file.go, group.go, object.go) that are not in errors.go (kept
// separate to avoid merge conflicts).

const (
	H5E_CANTSORT      errMinor = "H5E_CANTSORT"
	H5E_CANTLIST      errMinor = "H5E_CANTLIST"
	H5E_NOTREGISTERED errMinor = "H5E_NOTREGISTERED"
	H5E_CALLBACK      errMinor = "H5E_CALLBACK"
	H5E_PATH          errMinor = "H5E_PATH"
	H5E_CANTINSERT    errMinor = "H5E_CANTINSERT"
)
