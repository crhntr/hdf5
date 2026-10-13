package hdf5

// Tests for the filters (h5z.go, h5z_nbit.go, h5z_scaleoffset.go,
// h5z_szip.go, h5z_deflate.go) written from the mutants gremlins left
// alive. The datasets are assembled byte by byte (see
// mutation_h5o_test.go): one chunk holding the whole dataset, stored as
// given, behind the filter pipeline given; the expectations are what
// libhdf5 1.14.6 reads (H5Dread, H5Pget_filter2) from the same bytes (set
// HDF5_MUTATION_DUMP to a directory to have the tests write the files
// there).

import (
	"encoding/binary"
	"encoding/hex"
	"math/rand"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plFilter is one filter of a filter pipeline message.
type plFilter struct {
	id    uint16
	flags uint16
	name  []byte /* encoded name, with its padding (filter IDs >= 256 only) */
	cd    []uint32
}

// plineMsg is a version 2 filter pipeline message.
func plineMsg(filters ...plFilter) ohMsg {
	b := []byte{2, byte(len(filters))}
	for _, fl := range filters {
		b = binary.LittleEndian.AppendUint16(b, fl.id)
		if fl.id >= h5zFilterReserved {
			b = binary.LittleEndian.AppendUint16(b, uint16(len(fl.name)))
		}
		b = binary.LittleEndian.AppendUint16(b, fl.flags)
		b = binary.LittleEndian.AppendUint16(b, uint16(len(fl.cd)))
		b = append(b, fl.name...)
		for _, v := range fl.cd {
			b = binary.LittleEndian.AppendUint32(b, v)
		}
	}
	return ohMsg{typ: h5oPlineID, data: b}
}

// filteredLayout is a version 4 layout message for a dataset of the given
// dimensions stored in one chunk (single chunk index with filters) at
// addr, nbytes long as stored, written with the given filter mask.
func filteredLayout(addr uint64, nbytes int, mask uint32, elemSize uint64, dims ...uint64) ohMsg {
	l := []byte{4, 2, h5oLayoutChunkSingleIndexWithFilter, byte(len(dims) + 1), 8}
	for _, d := range append(dims, elemSize) {
		l = le64a(l, d)
	}
	l = le64a(append(l, h5dChunkIdxSingle), uint64(nbytes))
	l = binary.LittleEndian.AppendUint32(l, mask)
	return layoutMsg(le64a(l, addr))
}

// filteredDataset opens a file whose dataset /obj (datatype message dt
// of element size elemSize, dimensions dims) is the stored chunk chunk
// (written with filter mask mask) behind the filters.
func filteredDataset(t *testing.T, dt []byte, elemSize uint64, dims []uint64, chunk []byte, mask uint32, filters ...plFilter) *Dataset {
	t.Helper()
	f, _ := buildObject(t, func(b *h5Builder) uint64 {
		addr := b.put(chunk)
		dtype := ohMsg{typ: h5oDtypeID, data: dt}
		return b.put(ohdr2(2, v2Msgs(spaceMsg(dims...), dtype, plineMsg(filters...), filteredLayout(addr, len(chunk), mask, elemSize, dims...))))
	})
	d, err := f.OpenDataset("/obj")
	require.NoError(t, err)
	return d
}

// readRaw reads the whole dataset as stored in memory (no conversion).
func readRaw(t *testing.T, d *Dataset) ([]byte, error) {
	t.Helper()
	ds, err := d.dataset()
	require.NoError(t, err)
	return d.f.dRead(ds, nil)
}

// dtUint is a datatype message for a little-endian unsigned integer of
// size bytes with the given precision and bit offset.
func dtUint(size, precision, offset int) []byte {
	b := []byte{0x10, 0, 0, 0}
	b = binary.LittleEndian.AppendUint32(b, uint32(size))
	b = binary.LittleEndian.AppendUint16(b, uint16(offset))
	return binary.LittleEndian.AppendUint16(b, uint16(precision))
}

// dtString is a datatype message for a fixed-length, null-terminated
// ASCII string of size bytes.
func dtString(size int) []byte {
	return binary.LittleEndian.AppendUint32([]byte{0x13, 0, 0, 0}, uint32(size))
}

// dtArray is a version 3 datatype message for an array of n elements of
// base (of size baseSize).
func dtArray(n, baseSize int, base []byte) []byte {
	b := binary.LittleEndian.AppendUint32([]byte{0x3a, 0, 0, 0}, uint32(n*baseSize))
	b = binary.LittleEndian.AppendUint32(append(b, 1), uint32(n))
	return append(b, base...)
}

// dtMember is a member of a compound datatype.
type dtMember struct {
	name   string
	offset int
	dt     []byte
}

// dtCompound is a version 3 datatype message for a compound datatype of
// size bytes (less than 256).
func dtCompound(size int, members ...dtMember) []byte {
	b := binary.LittleEndian.AppendUint32([]byte{0x36, byte(len(members)), 0, 0}, uint32(size))
	for _, m := range members {
		b = append(append(b, m.name...), 0, byte(m.offset))
		b = append(b, m.dt...)
	}
	return b
}

// nbitFilter is the nbit filter with the parameters cd.
func nbitFilter(cd []uint32) plFilter { return plFilter{id: uint16(h5zFilterNbit), cd: cd} }

// TestMutationNbitCompound: nbit-compressed compound datasets (three
// elements), compressed with the translation of H5Z__nbit_compress in
// h5z_nbit_test.go. libhdf5 reads the original data back from
//   - a compound whose members fill it completely (u16 of precision 12
//     at 0, u16 at 2): the member size check of
//     H5Z__nbit_decompress_one_compound accepts a member ending at the
//     end of the compound (h5z_nbit.go:333);
//   - a compound whose first member is an array of strings (no-op base
//     type) followed by a u16 of precision 10 at bit offset 3: the
//     parameters of the array's base type are skipped
//     (h5z_nbit.go:307).
//
// It fails to read the dataset whose parameters give the first member a
// size of 0 (and a precision of 0, which reads no bit): "compound member
// size overflowed compound size" (h5z_nbit.go:337).
func TestMutationNbitCompound(t *testing.T) {
	for _, c := range []struct {
		name string
		dt   []byte
		typ  *nbType
		want string /* what libhdf5 reads (hex) */
	}{
		{"packed",
			dtCompound(4, dtMember{"a", 0, dtUint(2, 12, 0)}, dtMember{"b", 2, dtUint(2, 16, 0)}),
			nbCompound(4, nbMember{0, nbAtomic(2, 0, 12, 0)}, nbMember{2, nbAtomic(2, 0, 16, 0)}),
			"5907a21dee09ae74950db512"},
		{"array_of_strings",
			dtCompound(6, dtMember{"s", 0, dtArray(2, 2, dtString(2))}, dtMember{"v", 4, dtUint(2, 10, 3)}),
			nbCompound(6, nbMember{0, nbArray(2, nbNoop(2))}, nbMember{4, nbAtomic(2, 0, 10, 3)}),
			"f3ff4d45b0031e429ed6900e4b6d1aad281b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(7))
			data := make([]byte, 3*c.typ.size)
			for i := range 3 {
				c.typ.fill(rng, data[i*c.typ.size:(i+1)*c.typ.size])
			}
			cd := c.typ.cdValues(3)
			require.Zero(t, cd[1])
			d := filteredDataset(t, c.dt, uint64(c.typ.size), []uint64{3}, nbitCompress(append([]byte(nil), data...), 3, cd), 0, nbitFilter(cd))
			got, err := readRaw(t, d)
			require.NoError(t, err)
			assert.Equal(t, c.want, hex.EncodeToString(got))
			assert.Equal(t, data, got)
		})
	}
	t.Run("zero_size_member", func(t *testing.T) {
		dt := dtCompound(4, dtMember{"a", 0, dtUint(2, 12, 0)}, dtMember{"b", 2, dtUint(2, 16, 0)})
		cd := []uint32{0, 0, 1, h5zNbitCompound, 4, 2, 0, h5zNbitAtomic, 0, 0, 0, 0, 2, h5zNbitAtomic, 2, 0, 16, 0}
		cd[0] = uint32(len(cd))
		d := filteredDataset(t, dt, 4, []uint64{1}, []byte{1, 2, 3, 4}, 0, nbitFilter(cd))
		_, err := readRaw(t, d)
		require.Error(t, err)
	})
}

// TestMutationNbitNoCompression: nbit parameters whose need-not-compress
// flag (cd_values[1]) is set make H5Z__filter_nbit return the chunk
// unchanged whatever their number, as long as it matches cd_values[0]:
// libhdf5 reads the stored bytes of the u8 dataset {4} through
// parameters {2, 1} and {3, 1, 4} and through 4096 and 4097 parameters
// (h5z_nbit.go:465).
func TestMutationNbitNoCompression(t *testing.T) {
	long := func(n int) []uint32 {
		cd := make([]uint32, n)
		cd[0], cd[1] = uint32(n), 1
		return cd
	}
	for _, c := range []struct {
		name string
		cd   []uint32
	}{
		{"two", []uint32{2, 1}},
		{"three", []uint32{3, 1, 4}},
		{"4096", long(4096)},
		{"4097", long(4097)},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := filteredDataset(t, u8TypeBytes(), 1, []uint64{4}, []byte{9, 8, 7, 6}, 0, nbitFilter(c.cd))
			got, err := readRaw(t, d)
			require.NoError(t, err)
			assert.Equal(t, []byte{9, 8, 7, 6}, got)
		})
	}
}

// TestMutationNbitOutputBound: nbit parameters describing a chunk of
// 65536 elements of 65536 bytes (4 GiB) are refused before the output is
// allocated (h5z_nbit.go:397; libhdf5 would allocate the 4 GiB and read
// past the compressed chunk).
func TestMutationNbitOutputBound(t *testing.T) {
	cd := []uint32{8, 0, 65536, h5zNbitAtomic, 65536, 0, 8, 0}
	_, err := h5zFilterNbitFunc(h5zFlagReverse, cd, []byte{1, 2, 3})
	require.ErrorContains(t, err, "memory allocation failed for nbit decompression")
}

// soCd is the scaleoffset parameter block for nelmts unsigned integers of
// size bytes, the minimum number of bits computed by the filter and no
// fill value.
func soCd(nelmts, size uint32) []uint32 {
	cd := make([]uint32, h5zScaleoffsetTotalNParms)
	cd[h5zScaleoffsetParmScaletype] = h5zSoInt
	cd[h5zScaleoffsetParmNelmts] = nelmts
	cd[h5zScaleoffsetParmClass] = h5zScaleoffsetClsInteger
	cd[h5zScaleoffsetParmSize] = size
	return cd
}

// soHeader is the header of a scaleoffset chunk: minbits, the size of
// the minimum value and its bytes.
func soHeader(minbits uint32, minval ...byte) []byte {
	return append(append(binary.LittleEndian.AppendUint32(nil, minbits), byte(len(minval))), minval...)
}

// TestMutationScaleoffsetHeader: scaleoffset-compressed u32 datasets {4}
// whose chunks hold minbits 0 (all values equal to the minimum) and a
// minimum value of fewer than 8 bytes, which H5Z__filter_scaleoffset
// reads as stored. libhdf5 reads
//   - 0 0 0 0 from a 5-byte chunk with a minimum value of no byte
//     (h5z_scaleoffset.go:472);
//   - 0x0105 four times from a chunk with the 2-byte minimum 05 01
//     followed by 0xff bytes (h5z_scaleoffset.go:485, 492);
//   - 7 four times from a 13-byte chunk with the 8-byte minimum 7
//     (h5z_scaleoffset.go:488).
//
// A chunk ending inside its minimum value is refused (libhdf5 reads past
// the chunk; h5z_scaleoffset.go:488).
func TestMutationScaleoffsetHeader(t *testing.T) {
	for _, c := range []struct {
		name  string
		chunk []byte
		want  []uint32
	}{
		{"no_minimum", soHeader(0), []uint32{0, 0, 0, 0}},
		{"short_minimum", append(soHeader(0, 5, 1), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff), []uint32{0x105, 0x105, 0x105, 0x105}},
		{"minimum_only", soHeader(0, 7, 0, 0, 0, 0, 0, 0, 0), []uint32{7, 7, 7, 7}},
		{"truncated_minimum", append(binary.LittleEndian.AppendUint32(nil, 0), 8, 7, 0, 0, 0), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			so := plFilter{id: uint16(h5zFilterScaleoffset), cd: soCd(4, 4)}
			d := filteredDataset(t, dtUint(4, 32, 0), 4, []uint64{4}, c.chunk, 0, so)
			got, err := Read[uint32](d)
			if c.want == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestMutationScaleoffsetOutputBound: the size of the decompressed chunk
// (h5z_scaleoffset.go:511) and the check, made before the output is
// allocated, that the chunk holds the packed values
// (h5z_scaleoffset.go:520). (libhdf5 allocates the output and reads past
// the chunk.)
//   - 2^24 one-byte elements of 4 bits need 8 MiB of packed values: a
//     chunk holding 1 MiB is refused as truncated without allocating the
//     16 MiB output;
//   - 2^32 - 1 one-byte elements of 1 bit: refused as truncated (the
//     output size is allowed);
//   - 2^31 two-byte elements (4 GiB): refused as too large.
func TestMutationScaleoffsetOutputBound(t *testing.T) {
	chunk := append(soHeader(4, 0, 0, 0, 0, 0, 0, 0, 0), make([]byte, 1<<20)...)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, soCd(1<<24, 1), chunk)
	runtime.ReadMemStats(&after)
	require.ErrorContains(t, err, "scaleoffset compressed data is truncated")
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))

	chunk = append(soHeader(1, 0, 0, 0, 0, 0, 0, 0, 0), make([]byte, 16)...)
	_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, soCd(1<<32-1, 1), chunk)
	require.ErrorContains(t, err, "scaleoffset compressed data is truncated")

	_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, soCd(1<<31, 2), chunk)
	require.ErrorContains(t, err, "memory allocation failed for scaleoffset decompression")
}

// TestMutationPipelineMaxFilters: a pipeline of H5Z_MAX_NFILTERS (32)
// filters, every one a shuffle of 1-byte elements, which leaves the data
// as it is: libhdf5 decodes the pipeline message (h5o_pline.go:44) and
// reads the stored bytes through it (h5z.go:76).
func TestMutationPipelineMaxFilters(t *testing.T) {
	filters := make([]plFilter, h5zMaxNFilters)
	for i := range filters {
		filters[i] = plFilter{id: uint16(h5zFilterShuffle), cd: []uint32{1}}
	}
	d := filteredDataset(t, u8TypeBytes(), 1, []uint64{4}, []byte{9, 8, 7, 6}, 0, filters...)
	got, err := readRaw(t, d)
	require.NoError(t, err)
	assert.Equal(t, []byte{9, 8, 7, 6}, got)
}

// TestMutationSzipErrors: the errors of the szip filter, whose decoder is
// not translated (the libhdf5 used as the reference is built without
// szip, so it reports the filter as not registered): the parameters are
// checked, the read direction is the only one supported, and the 4-byte
// header holding the size of the uncompressed block must be present
// (h5z_szip.go:26, 31, 39).
func TestMutationSzipErrors(t *testing.T) {
	cd := []uint32{4, 32, 8, 16}
	_, err := h5zFilterSzipFunc(0, cd, []byte{1, 2, 3, 4, 5})
	require.ErrorContains(t, err, "szip filter not supported (")
	assert.NotContains(t, err.Error(), "compressed bytes")
	_, err = h5zFilterSzipFunc(h5zFlagReverse, cd, []byte{1, 2, 3})
	require.ErrorContains(t, err, "szip chunk too small for its header")
	_, err = h5zFilterSzipFunc(h5zFlagReverse, cd, []byte{8, 0, 0, 0})
	require.ErrorContains(t, err, "0 compressed bytes expanding to 8)")
	_, err = h5zFilterSzipFunc(h5zFlagReverse, cd, []byte{8, 0, 0, 0, 1, 2})
	require.ErrorContains(t, err, "2 compressed bytes expanding to 8)")
}

// TestMutationDeflateOutputBound: the bound on the size of an inflated
// chunk (h5z_deflate.go; HDF5 chunks hold less than 4 GiB, and the C code
// fails to allocate more), checked with a smaller bound: a stream
// inflating to 16 bytes is read whole under a bound of 16 and refused
// under a bound of 15.
func TestMutationDeflateOutputBound(t *testing.T) {
	data := []byte("0123456789abcdef")
	stream := zlibEncode(t, data, 6)
	got, err := h5zInflate(stream, 16)
	require.NoError(t, err)
	assert.Equal(t, data, got)
	_, err = h5zInflate(stream, 15)
	require.ErrorContains(t, err, "memory allocation failed for deflate uncompression")
}
