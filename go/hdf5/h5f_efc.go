package hdf5

// Translated from src/H5Fefc.c (the external file cache, read side) and
// H5F_prefix_open_file / H5F__build_name / H5F__getenv_prefix_name from
// src/H5Fint.c: locating and opening the target files of external links
// (and of virtual dataset mappings).
//
// The external file cache of a file is File.extFiles: files opened through
// it are keyed by their absolute path and stay open until the file holding
// the cache is closed (File.Close closes them).

import (
	"os"
	"path/filepath"
	"strings"
)

// H5F_prefix_open_t: the kind of prefix used to search for a file.
type h5fPrefixOpen int

const (
	h5fPrefixVDS   h5fPrefixOpen = 0 /* Virtual dataset prefix */
	h5fPrefixElink h5fPrefixOpen = 1 /* External link prefix */
)

// openWithOptions is H5F_open for a file opened through another one: it
// opens name read-only with the access properties o (those of the primary
// file, as H5F_get_access_plist(primary_file) in the C code).
func openWithOptions(name string, o openOptions) (*File, error) {
	fd, err := openDriver(name, &o)
	if err != nil {
		return nil, err
	}
	f, err := openWithDriver(name, fd, o)
	if err != nil {
		fd.Close()
		return nil, err
	}
	return f, nil
}

// efcOpen is H5F__efc_open with try = true: open the file name through the
// external file cache of f. It returns a nil file (and the reason) when the
// file cannot be opened. A file that is already in the cache is returned
// from it; f itself is returned when name is f's own file (the C library
// shares the open file in that case).
func (f *File) efcOpen(name string) (*File, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}

	/* Check if the file is the primary file itself */
	if f.name != "" {
		if self, err := filepath.Abs(f.name); err == nil && self == abs {
			return f, nil
		}
	}

	/* Search the skip list for name if the skip list exists, create the skip
	 * list otherwise */
	f.mu.Lock()
	ent := f.extFiles[abs]
	f.mu.Unlock()
	if ent != nil {
		return ent, nil
	}

	/* Check that the file exists before trying to open it (H5F_open with
	 * try = true) */
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		if err == nil {
			err = os.ErrNotExist
		}
		return nil, err
	}

	/* Open the file */
	file, err := openWithOptions(abs, f.opts)
	if err != nil {
		return nil, err
	}

	/* Add the file to the cache */
	f.mu.Lock()
	defer f.mu.Unlock()
	if ent := f.extFiles[abs]; ent != nil {
		file.Close()
		return ent, nil
	}
	if f.extFiles == nil {
		f.extFiles = make(map[string]*File)
	}
	f.extFiles[abs] = file
	return file, nil
}

// h5fGetenvPrefixNames is the loop over H5F__getenv_prefix_name: the
// pathnames in a list separated by the environment delimiter (':').
func h5fGetenvPrefixNames(envPrefix string) []string {
	return strings.Split(envPrefix, ":")
}

// h5fBuildName is H5F__build_name: prepend prefix to fileName.
func h5fBuildName(prefix, fileName string) string {
	if prefix == "" || strings.HasSuffix(prefix, string(filepath.Separator)) || strings.HasSuffix(prefix, "/") {
		return prefix + fileName
	}
	return prefix + string(filepath.Separator) + fileName
}

// prefixOpenFile is H5F_prefix_open_file (with try = false): locate and
// open the file fileName referenced from f (the primary file), searching,
// in order: the name itself when absolute, the prefixes in the environment
// variable (HDF5_EXT_PREFIX or HDF5_VDS_PREFIX), the prefix from the
// access properties (propPrefix), the directory of f ("extpath"), the name
// relative to the current directory, and the directory of f's resolved
// name.
func (f *File) prefixOpenFile(prefixType h5fPrefixOpen, propPrefix, fileName string) (*File, error) {
	var (
		srcFile *File
		lastErr error
	)
	try := func(name string) {
		if srcFile != nil {
			return
		}
		file, err := f.efcOpen(name)
		if err != nil {
			lastErr = err
			return
		}
		srcFile = file
	}

	/* Copy the file name to use */
	tempFileName := fileName

	/* Target file_name is an absolute pathname: see RM for detailed description */
	if filepath.IsAbs(fileName) || strings.HasPrefix(fileName, "/") {
		/* Try opening file */
		try(fileName)

		/* Adjust temporary file name if file not opened */
		if srcFile == nil {
			/* Get last component of file_name */
			tempFileName = fileName[strings.LastIndexAny(fileName, "/"+string(filepath.Separator))+1:]
		}
	}

	/* Try searching from paths set in the environment variable */
	if srcFile == nil {
		/* Get the appropriate environment variable */
		var envPrefix string
		switch prefixType {
		case h5fPrefixVDS:
			envPrefix = os.Getenv("HDF5_VDS_PREFIX")
		case h5fPrefixElink:
			envPrefix = os.Getenv("HDF5_EXT_PREFIX")
		default:
			return nil, errorf(H5E_FILE, H5E_BADTYPE, "prefix type is not sensible")
		}

		/* If environment variable is defined, iterate through prefixes it defines */
		if envPrefix != "" {
			/* Loop over prefixes in environment variable */
			for _, outPrefixName := range h5fGetenvPrefixNames(envPrefix) {
				if outPrefixName != "" {
					/* Try opening file */
					try(h5fBuildName(outPrefixName, tempFileName))

					/* Leave if file was opened */
					if srcFile != nil {
						break
					}
				}
			}
		}
	}

	/* Try searching from property list */
	if srcFile == nil && propPrefix != "" {
		/* Construct name to open */
		try(h5fBuildName(propPrefix, tempFileName))
	}

	/* Try searching from main file's "extpath": see description in H5F_open() & H5_build_extpath() */
	if srcFile == nil && f.name != "" {
		if abs, err := filepath.Abs(f.name); err == nil {
			/* Construct name to open */
			try(h5fBuildName(filepath.Dir(abs), tempFileName))
		}
	}

	/* Try the relative file_name stored in temp_file_name */
	if srcFile == nil {
		try(tempFileName)
	}

	/* try the 'resolved' name for the virtual file */
	if srcFile == nil && f.name != "" {
		actualFileName := f.name
		if resolved, err := filepath.EvalSymlinks(f.name); err == nil {
			actualFileName = resolved
		}

		/* get last component of file_name */
		dir := ""
		if i := strings.LastIndexAny(actualFileName, "/"+string(filepath.Separator)); i >= 0 {
			/* Truncate filename portion from actual file name path */
			dir = actualFileName[:i]
		}

		/* Try opening with the resolved name */
		try(h5fBuildName(dir, tempFileName))
	}

	/* See is we should return an error */
	if srcFile == nil {
		if lastErr != nil {
			return nil, wrapf(lastErr, H5E_FILE, H5E_CANTOPENFILE, "can't open file '%s'", fileName)
		}
		return nil, errorf(H5E_FILE, H5E_CANTOPENFILE, "can't open file '%s'", fileName)
	}
	return srcFile, nil
}
