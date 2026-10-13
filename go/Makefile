# Build helpers for the pure Go module github.com/crhntr/hdf5/go. The cgo
# module (libhdf5 wrapper and conformance suite) has its own Makefile in
# ../cgo.

.PHONY: all test test-quick vet fmt

all: vet test

test:
	go test ./...

test-quick:
	HDF5_QUICK_TESTS=1 go test -count=1 .

vet:
	go vet ./...

fmt:
	gofmt -l . && test -z "$$(gofmt -l .)"
