package hdf5

// Bounded-work test for extensible / fixed array iteration on corrupt
// headers: every mutation of the array header or of the block it points to
// must finish after a number of element visits that the file could actually
// hold (a header field must not make the iteration run for ever).

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var errTooMuchWork = errors.New("iteration visited more elements than the file can hold")

func TestArrayIterationBounded(t *testing.T) {
	for _, name := range []string{"dset.h5", "chunkidx.h5"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		require.NoError(t, err)
		for _, magic := range []string{h5eaHdrMagic, h5faHdrMagic} {
			for from := 0; ; {
				i := bytes.Index(data[from:], []byte(magic))
				if i < 0 {
					break
				}
				addr := from + i
				from += i + 4

				/* (the header, and the index block / data block it points to) */
				starts := []int{addr}
				if blk := int(le64(data[addr+h5eaHdrAddrOff(magic):])); blk > 0 && blk+120 < len(data) {
					starts = append(starts, blk)
				}
				for _, start := range starts {
					for _, val := range []byte{0xff, 0x7f, 0x40, 0x20, 0x00, 0x01} {
						for off := 0; off < 120; off++ {
							mutateAndIterate(t, name, data, magic, haddr(addr), start+off, val)
						}
					}
				}
			}
		}
	}
}

// h5eaHdrAddrOff is the offset of the index block / data block address in
// a header with 8-byte lengths and addresses (EA: after 6 bytes of
// parameters and 6 lengths; FA: after 4 bytes and 1 length).
func h5eaHdrAddrOff(magic string) int {
	if magic == h5eaHdrMagic {
		return 4 + 2 + 6 + 6*8
	}
	return 4 + 2 + 2 + 8
}

func mutateAndIterate(t *testing.T, name string, data []byte, magic string, addr haddr, pos int, val byte) {
	b := append([]byte(nil), data...)
	if pos >= len(b) {
		return
	}
	b[pos] = val
	done := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- errors.New("panic")
			}
		}()
		f, err := OpenReader(bytes.NewReader(b), int64(len(b)), WithChecksumVerification(false))
		if err != nil {
			done <- nil
			return
		}
		defer f.Close()
		var visits uint64
		op := func(uint64, any) error {
			visits++
			if visits > uint64(len(b)) {
				return errTooMuchWork
			}
			return nil
		}
		if magic == h5eaHdrMagic {
			ea, err := f.eaOpen(addr, h5dEarrayClsChunk(), &h5dEarrayCtx{fileAddrLen: int(f.sizeofAddr)})
			if err == nil {
				err = ea.iterateAllocated(op)
			}
			done <- err
		} else {
			fa, err := f.faOpen(addr, h5dFarrayClsChunk(), &h5dFarrayCtx{fileAddrLen: int(f.sizeofAddr)})
			if err == nil && addrDefined(fa.hdr.dblkAddr) { /* (as h5dFarrayIdxIterate) */
				err = fa.iterate(op)
			}
			done <- err
		}
	}()
	select {
	case err := <-done:
		require.NotErrorIs(t, err, errTooMuchWork, "%s %s@%d byte %d=%#x", name, magic, addr, pos, val)
		if err != nil {
			require.NotEqual(t, "panic", err.Error(), "%s %s@%d byte %d=%#x", name, magic, addr, pos, val)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s %s@%d byte %d=%#x: timed out", name, magic, addr, pos, val)
	}
}
