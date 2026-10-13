package hdf5

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

// Error paths of the public API (dataset.go, attribute.go, read.go,
// object.go) on copies of testdata/cover.h5 whose object headers were
// changed in memory.

// patchMessage changes the first message of type typ of the version 2
// object header at addr in the file image b (with change) and updates the
// header's checksum; the test fails when there is no such message.
func patchMessage(t *testing.T, b []byte, addr uint64, typ byte, change func(msg []byte)) {
	t.Helper()
	require.Equal(t, "OHDR", string(b[addr:addr+4]))
	require.Equal(t, byte(2), b[addr+4])
	flags := b[addr+5]
	p := addr + 6
	if flags&0x20 != 0 {
		p += 16
	}
	if flags&0x10 != 0 {
		p += 4
	}
	n := uint64(1) << (flags & 3)
	var size uint64
	for k := range n {
		size |= uint64(b[p+k]) << (8 * k)
	}
	p += n
	end := p + size
	hsize := uint64(4)
	if flags&0x04 != 0 {
		hsize = 6
	}
	for p+hsize <= end {
		mtyp, msize := b[p], uint64(binary.LittleEndian.Uint16(b[p+1:]))
		p += hsize
		if mtyp == typ {
			change(b[p : p+msize])
			binary.LittleEndian.PutUint32(b[end:], checksumLookup3(b[addr:end], 0))
			return
		}
		p += msize
	}
	t.Fatalf("no message of type %#x in the object header at %d", typ, addr)
}

// patchedCover opens a copy of cover.h5 in which change was applied to
// the message typ of the object at path.
func patchedCover(t *testing.T, path string, typ byte, change func(msg []byte)) *File {
	t.Helper()
	b, err := os.ReadFile("testdata/cover.h5")
	require.NoError(t, err)
	f := openTestdata(t, "cover.h5")
	obj, err := f.Open(path)
	require.NoError(t, err)
	info, err := obj.Info()
	require.NoError(t, err)
	patchMessage(t, b, info.Address, typ, change)
	g, err := OpenReader(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	t.Cleanup(func() { g.Close() })
	return g
}

func TestDatasetThatCannotBeOpened(t *testing.T) {
	/* A dataset whose layout, datatype or dataspace message cannot be
	 * decoded cannot be opened (H5D__open_oid), whichever way it is
	 * reached; its object header can */
	for _, c := range []struct {
		typ    uint16
		change func(m []byte)
	}{
		{h5oLayoutID, func(m []byte) { m[0] = 9 }},
		{h5oDtypeID, func(m []byte) { m[0] = 0x0f }},
		{h5oSdspaceID, func(m []byte) { m[0] = 9 }},
	} {
		f := patchedCover(t, "/target", byte(c.typ), c.change)
		_, err := f.OpenDataset("/target")
		assert.Error(t, err, "message %#x", c.typ)
		_, err = f.Open("/target")
		assert.Error(t, err)
		_, err = f.Root().OpenDataset("target")
		assert.Error(t, err)
		var walked bool
		for _, err := range f.Root().Walk() {
			if err != nil {
				walked = true
				assert.Contains(t, err.Error(), "unable to open dataset")
				break
			}
		}
		assert.True(t, walked, "the walk stops at the dataset")

		/* The object references to it still resolve to its header */
		info, err := f.oGetInfoByAddr(func() haddr {
			g := openTestdata(t, "cover.h5")
			o, err := g.OpenDataset("/target")
			require.NoError(t, err)
			i, err := o.Info()
			require.NoError(t, err)
			return i.Address
		}())
		require.NoError(t, err)
		assert.Equal(t, h5.ObjectDataset, info.Type)
	}

	/* A Dataset that was never opened (the zero value) */
	var zero Dataset
	assert.Nil(t, zero.Datatype())
	assert.Equal(t, h5.Dataspace{}, zero.Dataspace())
	_, err := zero.Layout()
	assert.Error(t, err)
	_, err = zero.Filters()
	assert.Error(t, err)
	_, err = zero.FillValue()
	assert.Error(t, err)
	_, err = zero.ReadValues()
	assert.Error(t, err)
	_, err = zero.ReadHyperslab(h5.Hyperslab{Start: []uint64{0}, Count: []uint64{1}})
	assert.Error(t, err)
	var v []int32
	assert.Error(t, zero.ReadInto(&v))
	_, err = Read[int32](&zero)
	assert.Error(t, err)
}

func TestAttributeErrors(t *testing.T) {
	f := openTestdata(t, "cover.h5")
	d, err := f.OpenDataset("/null_space")
	require.NoError(t, err)
	a, err := d.Attribute("scalar_attr")
	require.NoError(t, err)

	/* An attribute whose data does not match its dataspace */
	bad := *a.attr
	bad.data = bad.data[:2]
	ba := newAttribute(f, nil, &bad)
	_, err = ba.ReadValues()
	assert.Error(t, err)
	var v []int32
	assert.Error(t, ba.ReadInto(&v))
	_, err = ReadAttribute[int32](ba)
	assert.Error(t, err)

	/* An attribute without a dataspace (not decoded) */
	bad.ds = nil
	assert.Equal(t, h5.Dataspace{}, newAttribute(f, nil, &bad).Dataspace())

	_, err = d.Attribute("missing")
	assert.ErrorIs(t, err, ErrNotFound)
}
