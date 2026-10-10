package hdf5

// STUB — to be replaced by the translation of the sb_decode callbacks of
// src/H5FDfamily.c and src/H5FDmulti.c (plus the address mapping of the
// multi/split driver).

// sbLoad is H5FD_sb_load: let the driver named in the driver information
// block decode its information and, for the family and multi drivers,
// replace f.fd with a driver that reads the member files.
func (f *File) sbLoad(d *drvinfo) error {
	switch d.name {
	case "NCSAfami", "NCSAmult", "NCSAmulti":
		return errorf(H5E_VFL, H5E_UNSUPPORTED, "file driver %q not supported yet", d.name)
	}
	return nil
}
