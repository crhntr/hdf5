package hdf5

import (
	"os"
	"testing"
)

// quickMode reports whether the test suite should run in its fast
// configuration: HDF5_QUICK_TESTS set to any non-empty value (so that tools
// that cannot pass -short, like the gremlins mutation tester, can still ask
// for it) or go test -short. Quick mode skips tests whose only oracle is a
// run of the reference tools (h5dump, h5debug, h5ls) and shortens the
// random corruption loops; every comparison with hard-coded values, .ddl
// files and fixtures still runs.
func quickMode() bool {
	return os.Getenv("HDF5_QUICK_TESTS") != "" || testing.Short()
}

// quickCount returns full, or quick in quick mode (iteration counts of
// seeded corruption loops).
func quickCount(full, quick int) int {
	if quickMode() {
		return quick
	}
	return full
}

// quickStride returns 1 in full mode and stride in quick mode (the step of
// loops that visit every byte of a region).
func quickStride(stride int) int {
	return quickCount(1, stride)
}
