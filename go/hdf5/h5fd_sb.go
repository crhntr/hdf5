package hdf5

// Translated from H5FD_sb_load in src/H5FD.c, with the family and multi
// drivers of src/H5FDfamily.c (h5fd_family.go) and src/H5FDmulti.c
// (h5fd_multi.go).

import "os"

// sbLoad is H5FD_sb_load: let the driver named in the driver information
// block decode its information and, for the family and multi drivers,
// replace f.fd with a driver that reads the member files.
//
// The C library checks here that the file was opened with the driver named
// in the block ("family driver should be used"); the Go reader instead
// switches to the named driver. Drivers whose files are a single flat file
// ("NCSAsec2", "NCSAcore", "NCSAlog", "NCSAstdi", ...) need nothing.
func (f *File) sbLoad(d *drvinfo) error {
	switch d.name {
	case "NCSAfami":
		return f.sbLoadFamily(d)
	case "NCSAmult":
		return f.sbLoadMulti(d)
	}
	return nil
}

// openMemberDriver recognises the names under which a family or multi/split
// file is opened but which cannot be opened as a single flat file, and
// returns the driver that reads the whole file set:
//
//   - a family member name pattern with one %d conversion, e.g.
//     "family_v16-%06d.h5" (as passed to H5Pset_fapl_family);
//   - the base name of a multi/split file, e.g. "tmulti" for the members
//     "tmulti-s.h5", "tmulti-b.h5", ... or "tsplit_file-m.h5" /
//     "tsplit_file-r.h5": the member holding the superblock is opened and
//     (as for any file) sbLoad then opens the other members;
//   - the name of a member of a family written without a driver information
//     block (HDF5 1.6), e.g. "family_v16-000000.h5": the superblock's EOF
//     address is beyond the end of the file and further members exist.
//
// It returns a nil driver (and nil error) for any other name, which the
// caller opens as a flat file; names of family members with driver
// information and of multi members holding the superblock need no help
// since sbLoad replaces the driver once the superblock has been read.
func openMemberDriver(name string, o *openOptions) (fileDriver, error) {
	if h5fdFamilyIsPattern(name) {
		fam, err := h5fdFamilyOpen(name, o.familyMemberSize)
		if err != nil {
			return nil, err
		}
		return fam, nil
	}

	st, err := os.Stat(name)
	if err != nil {
		if m := h5fdMultiOpenBase(name); m != nil {
			return m, nil
		}
		return nil, nil
	}
	if !st.Mode().IsRegular() {
		return nil, nil
	}

	/* A family member without driver information: only worth looking at when
	 * the name ends in a member number and a second member exists.
	 */
	pattern, ok := h5fdFamilyPatternFromMember(name)
	if !ok {
		return nil, nil
	}
	next, _, _ := h5fdFamilyMemberName(pattern, 1)
	if _, err := os.Stat(next); err != nil {
		return nil, nil
	}
	sec2, err := h5fdSec2OpenMember(name)
	if err != nil {
		return nil, nil
	}
	probe := &File{name: name, fd: sec2, opts: *o, skipEOFCheck: true}
	if err := probe.superRead(); err != nil {
		sec2.Close()
		return nil, nil
	}
	if probe.fd == fileDriver(sec2) && probe.drvinfo == nil {
		if err := probe.sbLoadNoDriverInfo(); err != nil {
			probe.fd.Close()
			return nil, err
		}
	}
	if probe.fd == fileDriver(sec2) {
		sec2.Close()
		return nil, nil
	}
	return probe.fd, nil
}
