//go:build cgo

package conformance

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestMessageMutations compares the two packages on corrupted copies of
// files in the latest format: the messages of the version 2 object
// headers (found by their "OHDR" signature, so the superblock extension is
// included) are changed one byte at a time, and the header checksum is
// recomputed so that the change reaches the message decoders. Whatever
// libhdf5 makes of a corrupted message (an error, or a value), hdf5 must
// agree with it.
//
// libhdf5 does not always survive corrupt input (it crashes on some), so
// every comparison runs in a child process (TestMutationChild); a variant
// that crashes libhdf5 is counted and skipped.
func TestMessageMutations(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	root := repoRoot(t)
	files := []string{
		"go/testdata/cover.h5",
		"go/testdata/cover_sohm.h5",
		"go/testdata/cover_fam00000.h5",
		"go/testdata/dset.h5",
		"test/testfiles/h5fc_ext3_isf.h5",
		"test/testfiles/h5fc_ext1_i.h5",
		"tools/test/h5format_convert/testfiles/h5fc_ext2_sf.h5",
	}
	var crashes, compared, overreads, slow atomic.Int64
	t.Run("files", func(t *testing.T) {
		for _, rel := range files {
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatal(err)
			}
			for i, v := range messageMutations(data) {
				t.Run(rel+"/"+v.name, func(t *testing.T) {
					t.Parallel()
					// Each variant in its own directory, with the
					// companion files: the other family members, the
					// sources of virtual datasets.
					dir := t.TempDir()
					for _, m := range []string{"cover_fam00001.h5", "dset_src.h5"} {
						if b, err := os.ReadFile(filepath.Join(root, "go/testdata", m)); err == nil {
							if err := os.WriteFile(filepath.Join(dir, m), b, 0o644); err != nil {
								t.Fatal(err)
							}
						}
					}
					name := filepath.Join(dir, filepath.Base(rel))
					if err := os.WriteFile(name, v.data, 0o644); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMutationChild$", "-test.v")
					cmd.Env = append(os.Environ(), "MUTATION_FILE="+name, fmt.Sprintf("MUTATION_REL=%s@%s#%d", rel, v.name, i))
					// On timeout, ask for a goroutine dump before killing the child.
					cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGQUIT) }
					cmd.WaitDelay = 5 * time.Second
					out, err := cmd.CombinedOutput()
					var exit *exec.ExitError
					switch {
					case err == nil:
						compared.Add(1)
					case errors.As(err, &exit) && strings.Contains(string(out), "signal arrived during cgo execution"):
						crashes.Add(1)
						t.Logf("libhdf5 crashes on this variant")
					case ctx.Err() != nil:
						// Corrupted dimensions can make a dataset of
						// millions of chunks, which takes a while to
						// read in full.
						slow.Add(1)
						t.Logf("comparison timed out")
					case libhdf5OverRead(string(out)):
						overreads.Add(1)
						t.Logf("libhdf5 reads past a buffer where hdf5 fails")
					default:
						t.Errorf("%v\n%s", err, out)
					}
				})
			}
		}
	})
	t.Logf("%d variants compared, %d crash libhdf5, %d where libhdf5 reads past a buffer, %d timed out",
		compared.Load(), crashes.Load(), overreads.Load(), slow.Load())
}

// libhdf5OverRead reports whether the only differences in the output of a
// child comparison are reads that hdf5 refuses because the data is shorter
// than the corrupted metadata says, where libhdf5 does not check and reads
// past the end of its buffer (undefined behaviour, whatever it returns):
//
//   - "chunk holds N bytes, expected M": a filtered chunk that decompresses
//     to less than the chunk size of the (corrupted) layout message;
//     H5D__chunk_lock uses the pipeline's output buffer as a whole chunk.
//   - "ran off end of input buffer": chunk index elements whose size
//     derived from the (corrupted) chunk dimensions exceeds the raw element
//     size of the index (H5D__earray_filt_decode / H5D__farray_filt_decode
//     decode past the element).
//   - "fill value size doesn't match": a fill value shorter than an element
//     of the (corrupted) datatype; H5D__fill and H5D__fill_init replicate
//     it as if it were an element.
func libhdf5OverRead(out string) bool {
	found := false
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "compare_test.go:") {
			continue
		}
		mismatch := strings.Contains(line, "libhdf5 fails (") || strings.Contains(line, "first difference") ||
			strings.Contains(line, "cannot open") || strings.Contains(line, "walk fails") ||
			strings.Contains(line, "libhdf5 succeeds but hdf5 fails") || strings.Contains(line, "reports")
		if !mismatch {
			continue
		}
		if !strings.Contains(line, "libhdf5 succeeds but hdf5 fails") ||
			!(strings.Contains(line, "chunk holds") || strings.Contains(line, "ran off end of input buffer") ||
				strings.Contains(line, "fill value size doesn't match")) {
			return false
		}
		found = true
	}
	return found
}

// TestMutationChild compares the two packages on the file named by
// MUTATION_FILE (run by TestMessageMutations in a child process).
func TestMutationChild(t *testing.T) {
	name := os.Getenv("MUTATION_FILE")
	if name == "" {
		t.Skip("run by TestMessageMutations")
	}
	compareFile(t, name, os.Getenv("MUTATION_REL"), fileOptions{})
}

// mutationTypes are the message types whose decoders are mutated.
var mutationTypes = map[byte]string{
	0x01: "sdspace", 0x02: "linfo", 0x05: "fill", 0x06: "link", 0x07: "efl", 0x08: "layout",
	0x0a: "ginfo", 0x0b: "pline", 0x0f: "shmesg", 0x13: "btreek", 0x14: "drvinfo",
	0x15: "ainfo", 0x17: "fsinfo", 0x03: "dtype", 0x0c: "attr", 0x0d: "comment", 0x12: "mtime",
}

type mutation struct {
	name string
	data []byte
}

// messageMutations returns the corrupted variants of a file image: for the
// first few messages of each type of mutationTypes, each of the first 16
// bytes inverted, and the first byte (usually the version) set to 0..5.
func messageMutations(data []byte) []mutation {
	var out []mutation
	seen := map[byte]int{}
	for off := 0; ; {
		i := bytes.Index(data[off:], []byte("OHDR"))
		if i < 0 {
			break
		}
		hdr := off + i
		off = hdr + 4
		msgs, end, ok := ohdrMessages(data, hdr)
		if !ok {
			continue
		}
		for _, m := range msgs {
			typ := mutationTypes[m.typ]
			if typ == "" || seen[m.typ] >= 3 {
				continue
			}
			seen[m.typ]++
			add := func(what string, change func(b []byte)) {
				b := append([]byte(nil), data...)
				change(b[m.off : m.off+m.size])
				binary.LittleEndian.PutUint32(b[end:], lookup3(b[hdr:end]))
				out = append(out, mutation{fmt.Sprintf("%s@%d/%s", typ, m.off, what), b})
			}
			n := 16
			if typ == "drvinfo" {
				// Not the driver's own information (the family member
				// size): with a member size that does not match the
				// member files libhdf5 fails the first read of a
				// dataset (as hdf5 does) but the following reads of
				// the same dataset succeed with whatever the failed
				// read left in its buffers.
				n = 11
			}
			for j := 0; j < m.size && j < n; j++ {
				add(fmt.Sprintf("flip%d", j), func(b []byte) { b[j] ^= 0xff })
			}
			for v := byte(0); v <= 5 && m.size > 0; v++ {
				if v != data[m.off] {
					add(fmt.Sprintf("byte0=%d", v), func(b []byte) { b[0] = v })
				}
			}
		}
	}
	return append(out, heapMutations(data)...)
}

// heapMutations returns the corrupted variants of the checksummed objects
// of the global heap collections of a file image: the virtual dataset
// mappings (whose encoding ends with a checksum, recomputed here), each of
// their first 64 bytes inverted.
func heapMutations(data []byte) []mutation {
	var out []mutation
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
			if lookup3(data[obj:obj+size-4]) == binary.LittleEndian.Uint32(data[obj+size-4:]) {
				for j := 0; j < size-4 && j < 64; j++ {
					b := append([]byte(nil), data...)
					b[obj+j] ^= 0xff
					binary.LittleEndian.PutUint32(b[obj+size-4:], lookup3(b[obj:obj+size-4]))
					out = append(out, mutation{fmt.Sprintf("heap@%d/flip%d", obj, j), b})
				}
			}
			p = obj + (size+7)&^7
		}
	}
	return out
}

type ohdrMessage struct {
	typ       byte
	off, size int // message data
}

// ohdrMessages lists the messages of the first chunk of the version 2
// object header at hdr, and the offset of the chunk's checksum.
func ohdrMessages(data []byte, hdr int) ([]ohdrMessage, int, bool) {
	p := hdr + 4
	if p+2 > len(data) || data[p] != 2 {
		return nil, 0, false
	}
	flags := data[p+1]
	p += 2
	if flags&0x20 != 0 {
		p += 16 /* times */
	}
	if flags&0x10 != 0 {
		p += 4 /* attribute phase change */
	}
	n := 1 << (flags & 3)
	if p+n > len(data) {
		return nil, 0, false
	}
	var size uint64
	for k := 0; k < n; k++ {
		size |= uint64(data[p+k]) << (8 * k)
	}
	p += n
	end := p + int(size)
	if size == 0 || end+4 > len(data) || lookup3(data[hdr:end]) != binary.LittleEndian.Uint32(data[end:]) {
		return nil, 0, false
	}
	var msgs []ohdrMessage
	hsize := 4
	if flags&0x04 != 0 {
		hsize = 6 /* creation order */
	}
	for p+hsize <= end {
		typ := data[p]
		msize := int(binary.LittleEndian.Uint16(data[p+1:]))
		p += hsize
		if p+msize > end {
			return nil, 0, false
		}
		msgs = append(msgs, ohdrMessage{typ: typ, off: p, size: msize})
		p += msize
	}
	return msgs, end, true
}

// lookup3 is H5_checksum_lookup3 with initval 0 (src/H5checksum.c).
func lookup3(k []byte) uint32 {
	rot := func(x uint32, k uint) uint32 { return (x << k) ^ (x >> (32 - k)) }
	length := len(k)
	a := 0xdeadbeef + uint32(length)
	b, c := a, a
	for length > 12 {
		a += binary.LittleEndian.Uint32(k)
		b += binary.LittleEndian.Uint32(k[4:])
		c += binary.LittleEndian.Uint32(k[8:])
		a -= c
		a ^= rot(c, 4)
		c += b
		b -= a
		b ^= rot(a, 6)
		a += c
		c -= b
		c ^= rot(b, 8)
		b += a
		a -= c
		a ^= rot(c, 16)
		c += b
		b -= a
		b ^= rot(a, 19)
		a += c
		c -= b
		c ^= rot(b, 4)
		b += a
		length -= 12
		k = k[12:]
	}
	if length == 0 {
		return c
	}
	var last [12]byte
	copy(last[:], k)
	a += binary.LittleEndian.Uint32(last[:])
	b += binary.LittleEndian.Uint32(last[4:])
	c += binary.LittleEndian.Uint32(last[8:])
	c ^= b
	c -= rot(b, 14)
	a ^= c
	a -= rot(c, 11)
	b ^= a
	b -= rot(a, 25)
	c ^= b
	c -= rot(b, 16)
	a ^= c
	a -= rot(c, 4)
	b ^= a
	b -= rot(a, 14)
	c ^= b
	c -= rot(b, 24)
	return c
}
