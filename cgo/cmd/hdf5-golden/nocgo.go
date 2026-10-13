//go:build !cgo

// Command hdf5-golden needs cgo and libhdf5 (see main.go).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "hdf5-golden: built without cgo; libhdf5 is required")
	os.Exit(1)
}
