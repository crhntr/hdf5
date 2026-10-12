# Build helpers for the cgo module github.com/crhntr/hdf5/cgo. HDF5_DIR
# points at an installed libhdf5 1.14 (with lib/pkgconfig/hdf5.pc).
HDF5_DIR ?= $(HOME)/.local/hdf5-1.14.6
export PKG_CONFIG_PATH := $(HDF5_DIR)/lib/pkgconfig:$(PKG_CONFIG_PATH)

.PHONY: all test conformance vet fmt

all: vet test

test:
	go test ./...

# Compare the pure Go reader with libhdf5 over the repository corpus and
# generated fixtures; prints the per-file summaries and the final tables.
conformance:
	go test -count=1 -v -run 'TestCorpus|TestExternalFilesCurrentDirectory|TestFixtures' ./conformance

vet:
	go vet ./...

fmt:
	gofmt -l . && test -z "$$(gofmt -l .)"
