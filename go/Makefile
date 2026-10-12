# Build helpers for the pure Go module github.com/crhntr/hdf5/go. The cgo
# module (libhdf5 wrapper and conformance suite) has its own Makefile in
# ../cgo.

.PHONY: all test vet fmt

all: vet test

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l . && test -z "$$(gofmt -l .)"
