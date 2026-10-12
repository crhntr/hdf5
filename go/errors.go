package hdf5

import (
	"errors"
	"fmt"
)

// Major and minor error classes, mirroring the H5E_* values used by the C
// library's HGOTO_ERROR calls. They are strings here because they only serve
// diagnostics; the values are the names of the C constants.
type (
	errMajor string
	errMinor string
)

const (
	H5E_FILE      errMajor = "H5E_FILE"
	H5E_SYM       errMajor = "H5E_SYM"
	H5E_OHDR      errMajor = "H5E_OHDR"
	H5E_BTREE     errMajor = "H5E_BTREE"
	H5E_HEAP      errMajor = "H5E_HEAP"
	H5E_DATATYPE  errMajor = "H5E_DATATYPE"
	H5E_DATASPACE errMajor = "H5E_DATASPACE"
	H5E_DATASET   errMajor = "H5E_DATASET"
	H5E_EFL       errMajor = "H5E_EFL"
	H5E_PLIST     errMajor = "H5E_PLIST"
	H5E_ATTR      errMajor = "H5E_ATTR"
	H5E_LINK      errMajor = "H5E_LINK"
	H5E_PLINE     errMajor = "H5E_PLINE"
	H5E_IO        errMajor = "H5E_IO"
	H5E_ARGS      errMajor = "H5E_ARGS"
	H5E_SOHM      errMajor = "H5E_SOHM"
	H5E_REFERENCE errMajor = "H5E_REFERENCE"
	H5E_EARRAY    errMajor = "H5E_EARRAY"
	H5E_FARRAY    errMajor = "H5E_FARRAY"
	H5E_STORAGE   errMajor = "H5E_STORAGE"
	H5E_INTERNAL  errMajor = "H5E_INTERNAL"
	H5E_RESOURCE  errMajor = "H5E_RESOURCE"
	H5E_VFL       errMajor = "H5E_VFL"
	H5E_CACHE     errMajor = "H5E_CACHE"
)

const (
	H5E_OVERFLOW     errMinor = "H5E_OVERFLOW"
	H5E_BADVALUE     errMinor = "H5E_BADVALUE"
	H5E_BADRANGE     errMinor = "H5E_BADRANGE"
	H5E_BADTYPE      errMinor = "H5E_BADTYPE"
	H5E_CANTDECODE   errMinor = "H5E_CANTDECODE"
	H5E_CANTINIT     errMinor = "H5E_CANTINIT"
	H5E_CANTOPENFILE errMinor = "H5E_CANTOPENFILE"
	H5E_NOTHDF5      errMinor = "H5E_NOTHDF5"
	H5E_NOTFOUND     errMinor = "H5E_NOTFOUND"
	H5E_READERROR    errMinor = "H5E_READERROR"
	H5E_VERSION      errMinor = "H5E_VERSION"
	H5E_BADMESG      errMinor = "H5E_BADMESG"
	H5E_UNSUPPORTED  errMinor = "H5E_UNSUPPORTED"
	H5E_CANTLOAD     errMinor = "H5E_CANTLOAD"
	H5E_CANTPROTECT  errMinor = "H5E_CANTPROTECT"
	H5E_CANTGET      errMinor = "H5E_CANTGET"
	H5E_CANTCONVERT  errMinor = "H5E_CANTCONVERT"
	H5E_CANTFILTER   errMinor = "H5E_CANTFILTER"
	H5E_NOFILTER     errMinor = "H5E_NOFILTER"
	H5E_BADSIZE      errMinor = "H5E_BADSIZE"
	H5E_BADITER      errMinor = "H5E_BADITER"
	H5E_CANTALLOC    errMinor = "H5E_CANTALLOC"
	H5E_LINKCOUNT    errMinor = "H5E_LINKCOUNT"
	H5E_NLINKS       errMinor = "H5E_NLINKS"
	H5E_TRAVERSE     errMinor = "H5E_TRAVERSE"
	H5E_EXISTS       errMinor = "H5E_EXISTS"
	H5E_CANTSET      errMinor = "H5E_CANTSET"
	H5E_SYSTEM       errMinor = "H5E_SYSTEM"
	H5E_BADID        errMinor = "H5E_BADID"
	H5E_CANTCOMPARE  errMinor = "H5E_CANTCOMPARE"
	H5E_CANTCOPY     errMinor = "H5E_CANTCOPY"
	H5E_CANTSELECT   errMinor = "H5E_CANTSELECT"
	H5E_CANTCOUNT    errMinor = "H5E_CANTCOUNT"
	H5E_CANTNEXT     errMinor = "H5E_CANTNEXT"
	H5E_CANTCLOSEOBJ errMinor = "H5E_CANTCLOSEOBJ"
	H5E_CANTOPENOBJ  errMinor = "H5E_CANTOPENOBJ"
	H5E_CANTRELEASE  errMinor = "H5E_CANTRELEASE"
	H5E_CANTLOCK     errMinor = "H5E_CANTLOCK"
	H5E_CANTUNLOCK   errMinor = "H5E_CANTUNLOCK"
	H5E_CANTCLIP     errMinor = "H5E_CANTCLIP"
	H5E_CANTMODIFY   errMinor = "H5E_CANTMODIFY"
	H5E_CANTCREATE   errMinor = "H5E_CANTCREATE"
	H5E_ALIGNMENT    errMinor = "H5E_ALIGNMENT"
	H5E_CANTSEEK     errMinor = "H5E_CANTSEEK"
	H5E_SEEKERROR    errMinor = "H5E_SEEKERROR"
	H5E_CANTCHECKSUM errMinor = "H5E_CANTCHECKSUM"
	H5E_BADCHECKSUM  errMinor = "H5E_BADCHECKSUM"
	H5E_CANTOPERATE  errMinor = "H5E_CANTOPERATE"

	// Added by later modules (object headers, groups, attributes, arrays, dataspaces).
	H5E_NOSPACE       errMinor = "H5E_NOSPACE"
	H5E_ALREADYINIT   errMinor = "H5E_ALREADYINIT"
	H5E_UNINITIALIZED errMinor = "H5E_UNINITIALIZED"
	H5E_CALLBACK      errMinor = "H5E_CALLBACK"
	H5E_CANTINSERT    errMinor = "H5E_CANTINSERT"
	H5E_CANTDELETE    errMinor = "H5E_CANTDELETE"
	H5E_CANTLIST      errMinor = "H5E_CANTLIST"
	H5E_CANTSORT      errMinor = "H5E_CANTSORT"
	H5E_NOTREGISTERED errMinor = "H5E_NOTREGISTERED"
	H5E_PATH          errMinor = "H5E_PATH"
	H5E_CANTFIND      errMinor = "H5E_CANTFIND"
	H5E_BADSELECT     errMinor = "H5E_BADSELECT"

	// Added by the conformance fixes (file open checks).
	H5E_TRUNCATED errMinor = "H5E_TRUNCATED"
)

// Error is the error type returned by this package. It carries the HDF5 error
// classes of the failing C-equivalent call (as HGOTO_ERROR would record them)
// and wraps the underlying cause, if any.
type Error struct {
	Major errMajor
	Minor errMinor
	Msg   string
	Err   error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("hdf5: %s (%s/%s): %v", e.Msg, e.Major, e.Minor, e.Err)
	}
	return fmt.Sprintf("hdf5: %s (%s/%s)", e.Msg, e.Major, e.Minor)
}

func (e *Error) Unwrap() error { return e.Err }

// Is lets errors.Is match on Major/Minor only (Msg empty) so callers can
// test for e.g. &Error{Minor: H5E_NOTFOUND}.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	if t.Major != "" && t.Major != e.Major {
		return false
	}
	if t.Minor != "" && t.Minor != e.Minor {
		return false
	}
	return t.Msg == "" || t.Msg == e.Msg
}

// errorf is the Go spelling of HGOTO_ERROR(major, minor, ..., "fmt", args...).
func errorf(major errMajor, minor errMinor, format string, args ...any) error {
	return &Error{Major: major, Minor: minor, Msg: fmt.Sprintf(format, args...)}
}

// wrapf is HGOTO_ERROR with the failing inner error attached.
func wrapf(err error, major errMajor, minor errMinor, format string, args ...any) error {
	return &Error{Major: major, Minor: minor, Msg: fmt.Sprintf(format, args...), Err: err}
}

// Sentinel errors for common conditions, usable with errors.Is.
var (
	ErrNotHDF5     = &Error{Major: H5E_FILE, Minor: H5E_NOTHDF5}
	ErrNotFound    = &Error{Minor: H5E_NOTFOUND}
	ErrUnsupported = &Error{Minor: H5E_UNSUPPORTED}
	ErrOverflow    = &Error{Minor: H5E_OVERFLOW}
	ErrBadChecksum = &Error{Minor: H5E_BADCHECKSUM}
)

// IsNotFound reports whether err is a not-found error.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
