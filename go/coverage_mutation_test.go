package hdf5

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
)

// TestMutatedMessages reads corrupted copies of generated files in the
// latest format: the messages of the version 2 object headers (the
// superblock extension included) and the virtual dataset mappings in the
// global heap are changed one byte at a time, with the checksums
// recomputed so that the change reaches the decoders. Every read must
// succeed or fail cleanly. cgo/conformance (TestMessageMutations) runs the
// same mutations through both packages and checks that hdf5 succeeds and
// fails where libhdf5 does, with the same values.
func TestMutatedMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	var variants, opened int
	for _, name := range []string{"cover.h5", "cover_sohm.h5", "dset.h5"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range mutateImage(data) {
			variants++
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s %s: panic: %v\n%s", name, v.name, r, debug.Stack())
					}
				}()
				f, err := OpenReader(bytes.NewReader(v.data), int64(len(v.data)))
				if err != nil {
					return
				}
				defer f.Close()
				opened++
				exerciseFile(f)
			}()
		}
	}
	t.Logf("%d variants, %d opened", variants, opened)
}

// exerciseFile reads everything of f through the public API, ignoring
// errors; datasets of more than 1 MiB are not read.
func exerciseFile(f *File) {
	_ = f.Superblock()
	for v, err := range f.Root().Walk() {
		if err != nil {
			return
		}
		if v.Object == nil {
			continue
		}
		_, _ = v.Object.Info()
		_, _ = v.Object.Comment()
		_, _ = v.Object.NumAttributes()
		for a, err := range v.Object.Attributes() {
			if err != nil {
				break
			}
			_ = a.Dataspace()
			_, _ = a.ReadValues()
		}
		d, ok := v.Object.(*Dataset)
		if !ok {
			continue
		}
		_, _ = d.Layout()
		_, _ = d.Filters()
		_, _ = d.FillValue()
		dt, ds := d.Datatype(), d.Dataspace()
		if dt == nil || ds.NumElems() > 1<<20 || ds.NumElems()*dt.Size > 1<<20 {
			continue
		}
		_, _ = d.ReadValues()
		if ds.Class == h5.SpaceSimple && len(ds.Dims) > 0 {
			sel := h5.Hyperslab{Start: make([]uint64, len(ds.Dims)), Count: make([]uint64, len(ds.Dims))}
			for i, n := range ds.Dims {
				sel.Count[i] = min(n, 2)
			}
			_, _ = d.ReadHyperslab(sel)
		}
	}
}

type imageMutation struct {
	name string
	data []byte
}

// mutateImage returns the corrupted variants of a file image (see
// TestMutatedMessages): the first 16 bytes of the first two messages of
// each type in the version 2 object headers inverted one at a time and
// their first byte set to 0..5, and the first 64 bytes of the checksummed
// global heap objects inverted.
func mutateImage(data []byte) []imageMutation {
	var out []imageMutation
	seen := map[byte]int{} /* (the first two messages of each type) */
	for off := 0; ; {
		i := bytes.Index(data[off:], []byte("OHDR"))
		if i < 0 {
			break
		}
		hdr := off + i
		off = hdr + 4
		p := hdr + 4
		if p+2 > len(data) || data[p] != 2 {
			continue
		}
		flags := data[p+1]
		p += 2
		if flags&0x20 != 0 {
			p += 16
		}
		if flags&0x10 != 0 {
			p += 4
		}
		n := 1 << (flags & 3)
		if p+n > len(data) {
			continue
		}
		size := 0
		for k := range n {
			size |= int(data[p+k]) << (8 * k)
		}
		p += n
		end := p + size
		if size == 0 || end+4 > len(data) || checksumLookup3(data[hdr:end], 0) != binary.LittleEndian.Uint32(data[end:]) {
			continue
		}
		hsize := 4
		if flags&0x04 != 0 {
			hsize = 6
		}
		for p+hsize <= end {
			typ, msize := data[p], int(binary.LittleEndian.Uint16(data[p+1:]))
			p += hsize
			if p+msize > end {
				break
			}
			mo := p
			add := func(what string, change func(b []byte)) {
				b := append([]byte(nil), data...)
				change(b[mo : mo+msize])
				binary.LittleEndian.PutUint32(b[end:], checksumLookup3(b[hdr:end], 0))
				out = append(out, imageMutation{fmt.Sprintf("message %#x@%d/%s", typ, mo, what), b})
			}
			if typ != byte(h5oNullID) && typ != byte(h5oContID) && seen[typ] < 2 {
				seen[typ]++
				for j := 0; j < msize && j < 16; j++ {
					add(fmt.Sprintf("flip%d", j), func(b []byte) { b[j] ^= 0xff })
				}
				for v := byte(0); v <= 5 && msize > 0; v++ {
					add(fmt.Sprintf("byte0=%d", v), func(b []byte) { b[0] = v })
				}
			}
			p += msize
		}
	}
	for off := 0; ; {
		i := bytes.Index(data[off:], []byte("GCOL"))
		if i < 0 {
			break
		}
		col := off + i
		off = col + 4
		if col+16 > len(data) || data[col+4] != 1 {
			continue
		}
		end := col + int(binary.LittleEndian.Uint64(data[col+8:]))
		if end > len(data) || end < col+16 {
			continue
		}
		for p := col + 16; p+16 <= end; {
			idx := binary.LittleEndian.Uint16(data[p:])
			size := int(binary.LittleEndian.Uint64(data[p+8:]))
			obj := p + 16
			if idx == 0 || size < 5 || obj+size > end {
				break
			}
			if checksumLookup3(data[obj:obj+size-4], 0) == binary.LittleEndian.Uint32(data[obj+size-4:]) {
				for j := 0; j < size-4 && j < 64; j++ {
					b := append([]byte(nil), data...)
					b[obj+j] ^= 0xff
					binary.LittleEndian.PutUint32(b[obj+size-4:], checksumLookup3(b[obj:obj+size-4], 0))
					out = append(out, imageMutation{fmt.Sprintf("heap object@%d/flip%d", obj, j), b})
				}
			}
			p = obj + (size+7)&^7
		}
	}
	return out
}
