package hdf5

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gcolImage builds a global heap collection image (8-byte lengths) holding
// the given objects (index -> data) followed by the free space object.
func gcolImage(size int, objs [][2]any) []byte {
	b := []byte("GCOL")
	b = append(b, 1, 0, 0, 0)
	b = binary.LittleEndian.AppendUint64(b, uint64(size))
	for _, o := range objs {
		idx, data := o[0].(int), o[1].([]byte)
		b = binary.LittleEndian.AppendUint16(b, uint16(idx))
		b = binary.LittleEndian.AppendUint16(b, 1)
		b = append(b, 0, 0, 0, 0)
		b = binary.LittleEndian.AppendUint64(b, uint64(len(data)))
		b = append(b, data...)
		for len(b)%8 != 0 {
			b = append(b, 0)
		}
	}
	free := size - len(b)
	if free >= 16 {
		b = binary.LittleEndian.AppendUint16(b, 0)
		b = binary.LittleEndian.AppendUint16(b, 0)
		b = append(b, 0, 0, 0, 0)
		b = binary.LittleEndian.AppendUint64(b, uint64(free))
	}
	return append(b, make([]byte, size-len(b))...)
}

func TestGlobalHeapDeserialize(t *testing.T) {
	f := testFile()
	img := gcolImage(4096, [][2]any{{1, []byte("hello")}, {3, []byte{}}, {7, []byte("0123456789abcdef")}})
	heap, err := f.hgCacheHeapDeserialize(img)
	require.NoError(t, err)
	assert.Equal(t, uint64(4096), heap.size)
	assert.Equal(t, uint64(8), heap.nused)
	assert.Equal(t, uint64(5), heap.obj[1].size)
	assert.Equal(t, 1, heap.obj[1].nrefs)
	assert.Equal(t, -1, heap.obj[2].begin)
	assert.Equal(t, uint64(4096-16-24-16-32), heap.obj[0].size)

	// Read through the cache.
	const addr = 1 << 20
	_, err = f.cached("H5HG", addr, func() (any, error) { return heap, nil })
	require.NoError(t, err)
	b, err := f.hgRead(h5hgT{addr: addr, idx: 1})
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), b)
	b[0] = 'j'
	b, err = f.hgRead(h5hgT{addr: addr, idx: 1})
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), b, "hgRead returns a copy")
	b, err = f.hgRead(h5hgT{addr: addr, idx: 3})
	require.NoError(t, err)
	assert.Empty(t, b)
	b, err = f.hgRead(h5hgT{addr: addr, idx: 7})
	require.NoError(t, err)
	assert.Equal(t, []byte("0123456789abcdef"), b)
	n, err := f.hgGetObjSize(h5hgT{addr: addr, idx: 7})
	require.NoError(t, err)
	assert.Equal(t, uint64(16), n)

	for _, idx := range []uint32{0, 2, 8, 1 << 20} {
		_, err = f.hgRead(h5hgT{addr: addr, idx: idx})
		assert.Error(t, err, "index %d", idx)
	}

	// A heap that ends with less than an object header of free space.
	heap, err = f.hgCacheHeapDeserialize(gcolImage(4096, [][2]any{{1, make([]byte, 4096-16-16-8)}}))
	require.NoError(t, err)
	assert.Equal(t, uint64(8), heap.obj[0].size)

	// A collection larger than the minimum size.
	heap, err = f.hgCacheHeapDeserialize(gcolImage(8192, [][2]any{{2, make([]byte, 5000)}}))
	require.NoError(t, err)
	assert.Equal(t, uint64(5000), heap.obj[2].size)
}

func TestGlobalHeapCorrupt(t *testing.T) {
	f := testFile()
	good := gcolImage(4096, [][2]any{{1, []byte("hello")}, {2, []byte("world")}})
	_, err := f.hgCacheHeapDeserialize(good)
	require.NoError(t, err)

	mut := func(fn func(b []byte) []byte) []byte { return fn(append([]byte(nil), good...)) }
	bad := map[string][]byte{
		"signature": mut(func(b []byte) []byte { b[0] = 'X'; return b }),
		"version":   mut(func(b []byte) []byte { b[4] = 2; return b }),
		"too small": mut(func(b []byte) []byte { binary.LittleEndian.PutUint64(b[8:], 4088); return b }),
		"too large": mut(func(b []byte) []byte { binary.LittleEndian.PutUint64(b[8:], 8192); return b }),
		"overrun":   mut(func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16+8:], 5000); return b }),
		"huge size": mut(func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16+8:], 1<<63); return b }),
		"partial":   mut(func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16+48+8:], 100); return b }),
		"unaligned": mut(func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16+48+8:], 4096-16-48-4); return b }),
		"short":     good[:100],
		"zero free": mut(func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16+48+8:], 0); return b }),
	}
	for name, img := range bad {
		_, err := f.hgCacheHeapDeserialize(img)
		assert.Error(t, err, name)
	}

	rng := rand.New(rand.NewSource(1))
	for k := 0; k < 5000; k++ {
		b := append([]byte(nil), good[:128]...)
		for j := 0; j < 1+rng.Intn(4); j++ {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		b = append(b, good[128:]...)
		assert.NotPanics(t, func() { _, _ = f.hgCacheHeapDeserialize(b) })
	}
}

// Decode every global heap collection found (by signature) in the corpus.
func TestGlobalHeapCorpusSweep(t *testing.T) {
	var files, ok, failed int
	for _, path := range corpusFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		if !bytes.Contains(raw, []byte("GCOL\x01")) {
			continue
		}
		f, err := Open(path, WithExternalLinks(false))
		if err != nil {
			continue
		}
		files++
		for off := 0; ; {
			i := bytes.Index(raw[off:], []byte("GCOL\x01"))
			if i < 0 {
				break
			}
			abs := uint64(off + i)
			off += i + 4
			if abs < f.baseAddr {
				continue
			}
			var heap *h5hgHeap
			assert.NotPanics(t, func() { heap, err = f.hgProtect(abs - f.baseAddr) })
			if err != nil {
				failed++
				t.Logf("%s@%d: %v", filepath.Base(path), abs, err)
				continue
			}
			ok++
			for idx := uint32(1); uint64(idx) < heap.nused; idx++ {
				if heap.obj[idx].begin < 0 {
					continue
				}
				b, err := f.hgRead(h5hgT{addr: heap.addr, idx: idx})
				require.NoError(t, err)
				assert.Len(t, b, int(heap.obj[idx].size))
			}
		}
		f.Close()
	}
	t.Logf("%d files, %d global heap collections decoded, %d failed", files, ok, failed)
	assert.Greater(t, ok, 40)
}

func TestGlobalHeapCorpus(t *testing.T) {
	f, err := Open(filepath.Join(repoRoot(t), "tools/test/h5dump/testfiles/tvlstr.h5"))
	require.NoError(t, err)
	defer f.Close()

	// /Dataset1 holds 4 disk vlen strings at 2048: {length, heap address,
	// heap index}.
	data, err := f.readAt(2048, 64)
	require.NoError(t, err)
	p := f.newImage(data)
	var ids []h5hgT
	var lens []uint32
	for i := 0; i < 4; i++ {
		lens = append(lens, p.uint32())
		ids = append(ids, h5hgT{addr: p.addr(), idx: p.uint32()})
	}
	require.NoError(t, p.done())

	b, err := f.hgRead(ids[0])
	require.NoError(t, err)
	assert.Equal(t, "Four score and seven years ago our forefathers brought forth on this continent a new nation,", string(b))
	assert.Equal(t, int(lens[0]), len(b))
	n, err := f.hgGetObjSize(ids[1])
	require.NoError(t, err)
	assert.Equal(t, uint64(lens[1]), n)

	// The collection is cached.
	h1, err := f.hgProtect(ids[0].addr)
	require.NoError(t, err)
	h2, err := f.hgProtect(ids[1].addr)
	require.NoError(t, err)
	if ids[0].addr == ids[1].addr {
		assert.Same(t, h1, h2)
	}
	assert.Equal(t, ids[0].addr, h1.addr)

	// The NULL string (element 3) has address 0, which is not a heap.
	assert.Equal(t, haddr(0), ids[3].addr)
	_, err = f.hgRead(ids[3])
	assert.Error(t, err)
	_, err = f.hgRead(h5hgT{addr: haddrUndef, idx: 1})
	assert.Error(t, err)
	_, err = f.hgRead(h5hgT{addr: f.eof() + 10, idx: 1})
	assert.Error(t, err)
}
