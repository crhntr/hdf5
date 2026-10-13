# Build helpers for the cgo module github.com/crhntr/hdf5/cgo. HDF5_DIR
# points at an installed libhdf5 1.14 (with lib/pkgconfig/hdf5.pc).
HDF5_DIR ?= $(HOME)/.local/hdf5-1.14.6
export PKG_CONFIG_PATH := $(HDF5_DIR)/lib/pkgconfig:$(PKG_CONFIG_PATH)

.PHONY: all test conformance golden fixtures vet fmt

all: vet test

# -count=1: the tests read the corpus through libhdf5, which the go test
# cache does not see.
test:
	go test -count=1 ./...

# Compare the pure Go reader with libhdf5 over the repository corpus and
# generated fixtures; prints the per-file summaries and the final tables.
conformance:
	go test -count=1 -v -run 'TestCorpus|TestExternalFilesCurrentDirectory|TestFixtures' ./conformance

# Regenerate the golden file of the pure Go module's tests
# (../go/testdata/golden/corpus.txt.gz): fingerprints of what libhdf5 reads
# from every corpus file. Needed whenever the corpus, the committed fixtures
# or the canonical encoding (../go/h5/fingerprint.go) change; the
# conformance test TestGoldenUpToDate fails until it is done.
golden:
	go run ./cmd/hdf5-golden

# Rewrite the committed fixtures (../go/testdata/fixtures, written without
# object times so that they are reproducible), then the golden file.
fixtures:
	go run ./cmd/hdf5-golden -fixtures

vet:
	go vet ./...

fmt:
	gofmt -l . && test -z "$$(gofmt -l .)"
