//go:build cgo

package golden

/*
#cgo pkg-config: hdf5
#include <hdf5.h>
*/
import "C"

import "fmt"

// LibraryVersion returns the version of the libhdf5 the cgo package is
// linked with (H5get_libversion), e.g. "libhdf5 1.14.6".
func LibraryVersion() string {
	var major, minor, release C.unsigned
	C.H5get_libversion(&major, &minor, &release)
	return fmt.Sprintf("libhdf5 %d.%d.%d", major, minor, release)
}
