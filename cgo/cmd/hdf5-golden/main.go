//go:build cgo

// Command hdf5-golden writes the golden file of the pure Go module's tests,
// go/testdata/golden/corpus.txt.gz: fingerprints of what libhdf5 (through
// the cgo package) reads from every file of the conformance corpus (see
// package internal/golden for the format). Run it from the cgo module
// directory:
//
//	make golden     # go run ./cmd/hdf5-golden
//	make fixtures   # go run ./cmd/hdf5-golden -fixtures: rewrite go/testdata/fixtures first
//
// With -fixtures it first writes the files of package internal/fixtures
// whose formats are reproducible (no superblock extension), without object
// times so that they are identical every time, to
// go/testdata/fixtures/<format>/.
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	chdf5 "github.com/crhntr/hdf5/cgo"
	"github.com/crhntr/hdf5/cgo/internal/fixtures"
	"github.com/crhntr/hdf5/cgo/internal/golden"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("hdf5-golden: ")
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	root := flag.String("root", golden.RepoRoot(wd), "root of the HDF5 repository checkout")
	out := flag.String("o", "", "output file (default <root>/go/testdata/golden/corpus.txt.gz)")
	writeFixtures := flag.Bool("fixtures", false, "write go/testdata/fixtures first")
	flag.Parse()
	if *root == "" {
		log.Fatal("not inside an HDF5 repository checkout (use -root)")
	}
	if *out == "" {
		*out = filepath.Join(*root, "go", "testdata", "golden", "corpus.txt.gz")
	}
	if *writeFixtures {
		n := 0
		for _, f := range fixtures.Formats {
			if !f.Reproducible() {
				continue
			}
			n++
			dir := filepath.Join(*root, "go", "testdata", "fixtures", f.Name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Fatal(err)
			}
			if err := fixtures.Write(dir, f, chdf5.WithoutTimes()); err != nil {
				log.Fatal(err)
			}
		}
		log.Printf("wrote %d fixture directories", n)
	}

	var text bytes.Buffer
	if err := golden.Generate(&text, *root); err != nil {
		log.Fatal(err)
	}
	var gz bytes.Buffer
	zw, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := zw.Write(text.Bytes()); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, gz.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	header, _, _ := strings.Cut(text.String(), "\n")
	fmt.Printf("%s: %d lines, %d bytes (%d gzipped)\n%s\n", *out,
		strings.Count(text.String(), "\n"), text.Len(), gz.Len(), header)
}
