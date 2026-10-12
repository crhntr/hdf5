# Go module build helpers. HDF5_DIR points at an installed libhdf5 1.14
# (with lib/pkgconfig/hdf5.pc) for the cgo packages.
HDF5_DIR ?= $(HOME)/.local/hdf5-1.14.6
export PKG_CONFIG_PATH := $(HDF5_DIR)/lib/pkgconfig:$(PKG_CONFIG_PATH)

.PHONY: all test test-pure test-cgo vet fmt

all: vet test

test: test-pure test-cgo

test-pure:
	go test ./h5 ./hdf5

test-cgo:
	go test ./chdf5 $(if $(wildcard conformance),./conformance)

vet:
	go vet ./...

fmt:
	gofmt -l . && test -z "$$(gofmt -l .)"
