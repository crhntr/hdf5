package hdf5

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test-only encoder: translation of the compress direction of src/H5Znbit.c
// (H5Z__nbit_compress_one_byte, _one_nooptype, _one_atomic, _one_array,
// _one_compound and H5Z__nbit_compress), driven by the same cd_values layout
// as the decoder.
// ---------------------------------------------------------------------------

type nbitEnc struct {
	data   []byte
	buffer []byte
	j      int
	bufLen uint
	parms  []uint32
	pi     int
}

func (e *nbitEnc) next() uint32 {
	v := e.parms[e.pi]
	e.pi++
	return v
}

// h5zNbitCompressOneByte is H5Z__nbit_compress_one_byte.
func (e *nbitEnc) compressOneByte(dataOffset int, k, beginI, endI uint32, p *h5zNbitParmsAtomic, datatypeLen uint) {
	var datLen uint
	val := uint32(e.data[dataOffset+int(k)])
	if beginI != endI {
		if k == beginI {
			datLen = 8 - (datatypeLen-uint(p.precision)-uint(p.offset))%8
		} else if k == endI {
			datLen = 8 - uint(p.offset)%8
			val = uint32(byte(val >> (8 - datLen)))
		} else {
			datLen = 8
		}
	} else {
		val = uint32(byte(val >> (uint(p.offset) % 8)))
		datLen = uint(p.precision)
	}

	if e.bufLen > datLen {
		e.buffer[e.j] |= byte((val & ^(^uint32(0) << datLen)) << (e.bufLen - datLen))
		e.bufLen -= datLen
	} else {
		e.buffer[e.j] |= byte((val >> (datLen - e.bufLen)) & ^(^uint32(0) << e.bufLen))
		datLen -= e.bufLen
		e.j++
		e.bufLen = 8
		if datLen == 0 {
			return
		}
		e.buffer[e.j] = byte((val & ^(^uint32(0) << datLen)) << (e.bufLen - datLen))
		e.bufLen -= datLen
	}
}

// compressOneNooptype is H5Z__nbit_compress_one_nooptype.
func (e *nbitEnc) compressOneNooptype(dataOffset int, size uint32) {
	for i := 0; i < int(size); i++ {
		val := uint32(e.data[dataOffset+i])
		datLen := uint(8)
		e.buffer[e.j] |= byte((val >> (datLen - e.bufLen)) & ^(^uint32(0) << e.bufLen))
		datLen -= e.bufLen
		e.j++
		e.bufLen = 8
		if datLen == 0 {
			continue
		}
		e.buffer[e.j] = byte((val & ^(^uint32(0) << datLen)) << (e.bufLen - datLen))
		e.bufLen -= datLen
	}
}

// compressOneAtomic is H5Z__nbit_compress_one_atomic.
func (e *nbitEnc) compressOneAtomic(dataOffset int, p *h5zNbitParmsAtomic) {
	datatypeLen := uint(p.size * 8)
	if p.order == h5zNbitOrderLE {
		var beginI uint32
		if (p.precision+p.offset)%8 != 0 {
			beginI = (p.precision + p.offset) / 8
		} else {
			beginI = (p.precision+p.offset)/8 - 1
		}
		endI := p.offset / 8
		for k := int(int32(beginI)); k >= int(int32(endI)); k-- {
			e.compressOneByte(dataOffset, uint32(k), beginI, endI, p, datatypeLen)
		}
	} else {
		beginI := (uint32(datatypeLen) - p.precision - p.offset) / 8
		var endI uint32
		if p.offset%8 != 0 {
			endI = (uint32(datatypeLen) - p.offset) / 8
		} else {
			endI = (uint32(datatypeLen)-p.offset)/8 - 1
		}
		for k := int(int32(beginI)); k <= int(int32(endI)); k++ {
			e.compressOneByte(dataOffset, uint32(k), beginI, endI, p, datatypeLen)
		}
	}
}

// compressOneArray is H5Z__nbit_compress_one_array.
func (e *nbitEnc) compressOneArray(dataOffset int) {
	totalSize := e.next()
	baseClass := e.next()
	switch baseClass {
	case h5zNbitAtomic:
		var p h5zNbitParmsAtomic
		p.size = e.next()
		p.order = e.next()
		p.precision = e.next()
		p.offset = e.next()
		n := totalSize / p.size
		for i := uint32(0); i < n; i++ {
			e.compressOneAtomic(dataOffset+int(i*p.size), &p)
		}
	case h5zNbitArray:
		baseSize := e.parms[e.pi]
		n := totalSize / baseSize
		begin := e.pi
		for i := uint32(0); i < n; i++ {
			e.compressOneArray(dataOffset + int(i*baseSize))
			e.pi = begin
		}
	case h5zNbitCompound:
		baseSize := e.parms[e.pi]
		n := totalSize / baseSize
		begin := e.pi
		for i := uint32(0); i < n; i++ {
			e.compressOneCompound(dataOffset + int(i*baseSize))
			e.pi = begin
		}
	case h5zNbitNooptype:
		e.pi++
		e.compressOneNooptype(dataOffset, totalSize)
	default:
		panic("bad class")
	}
}

// compressOneCompound is H5Z__nbit_compress_one_compound.
func (e *nbitEnc) compressOneCompound(dataOffset int) {
	e.pi++ /* skip size of compound datatype */
	nmembers := e.next()
	for i := uint32(0); i < nmembers; i++ {
		memberOffset := e.next()
		memberClass := e.next()
		switch memberClass {
		case h5zNbitAtomic:
			var p h5zNbitParmsAtomic
			p.size = e.next()
			p.order = e.next()
			p.precision = e.next()
			p.offset = e.next()
			e.compressOneAtomic(dataOffset+int(memberOffset), &p)
		case h5zNbitArray:
			e.compressOneArray(dataOffset + int(memberOffset))
		case h5zNbitCompound:
			e.compressOneCompound(dataOffset + int(memberOffset))
		case h5zNbitNooptype:
			size := e.next()
			e.compressOneNooptype(dataOffset+int(memberOffset), size)
		default:
			panic("bad class")
		}
	}
}

// nbitCompress is H5Z__nbit_compress.
func nbitCompress(data []byte, dNelmts uint32, parms []uint32) []byte {
	e := &nbitEnc{data: data, buffer: make([]byte, len(data)+16), bufLen: 8, parms: parms}
	switch parms[3] {
	case h5zNbitAtomic:
		p := h5zNbitParmsAtomic{size: parms[4], order: parms[5], precision: parms[6], offset: parms[7]}
		for i := uint32(0); i < dNelmts; i++ {
			e.compressOneAtomic(int(i*p.size), &p)
		}
	case h5zNbitArray:
		size := parms[4]
		e.pi = 4
		for i := uint32(0); i < dNelmts; i++ {
			e.compressOneArray(int(i * size))
			e.pi = 4
		}
	case h5zNbitCompound:
		size := parms[4]
		e.pi = 4
		for i := uint32(0); i < dNelmts; i++ {
			e.compressOneCompound(int(i * size))
			e.pi = 4
		}
	default:
		panic("bad class")
	}
	/* Update the size to the new value after compression.  If there are any
	 * bits hanging over in the last byte, increment the value by 1. */
	return e.buffer[:e.j+1]
}

// ---------------------------------------------------------------------------
// Datatype descriptions used to drive the encoder, with the parameter
// generation of H5Z__set_parms_{atomic,array,compound,nooptype}.
// ---------------------------------------------------------------------------

type nbMember struct {
	offset int
	typ    *nbType
}

type nbType struct {
	class     int // h5zNbit* class
	size      int // total size in bytes
	order     int // atomic
	precision int
	offset    int
	base      *nbType    // array
	members   []nbMember // compound
}

func nbAtomic(size, order, precision, offset int) *nbType {
	return &nbType{class: h5zNbitAtomic, size: size, order: order, precision: precision, offset: offset}
}

func nbNoop(size int) *nbType { return &nbType{class: h5zNbitNooptype, size: size} }

func nbArray(n int, base *nbType) *nbType {
	return &nbType{class: h5zNbitArray, size: n * base.size, base: base}
}

func nbCompound(size int, members ...nbMember) *nbType {
	return &nbType{class: h5zNbitCompound, size: size, members: members}
}

// params appends the cd_values describing t.
func (t *nbType) params(out []uint32) []uint32 {
	switch t.class {
	case h5zNbitAtomic:
		return append(out, h5zNbitAtomic, uint32(t.size), uint32(t.order), uint32(t.precision), uint32(t.offset))
	case h5zNbitArray:
		return t.base.params(append(out, h5zNbitArray, uint32(t.size)))
	case h5zNbitCompound:
		out = append(out, h5zNbitCompound, uint32(t.size), uint32(len(t.members)))
		for _, m := range t.members {
			out = m.typ.params(append(out, uint32(m.offset)))
		}
		return out
	}
	return append(out, h5zNbitNooptype, uint32(t.size))
}

// fullPrecision reports whether every atomic part uses its full width (the
// need_not_compress flag).
func (t *nbType) fullPrecision() bool {
	switch t.class {
	case h5zNbitAtomic:
		return t.offset == 0 && t.precision == t.size*8
	case h5zNbitArray:
		return t.base.fullPrecision()
	case h5zNbitCompound:
		for _, m := range t.members {
			if !m.typ.fullPrecision() {
				return false
			}
		}
	}
	return true
}

// cdValues is the cd_values array of H5Z__set_local_nbit.
func (t *nbType) cdValues(nelmts int) []uint32 {
	cd := []uint32{0, 0, uint32(nelmts)}
	cd = t.params(cd)
	cd[0] = uint32(len(cd))
	if t.fullPrecision() {
		cd[1] = 1
	}
	return cd
}

// fill writes random data for one element of type t into dst (len t.size),
// keeping only the bits nbit preserves (precision/offset of atomic types;
// everything else of the compound is random "no-op" payload or padding that
// the filter drops, which is left zero).
func (t *nbType) fill(rng *rand.Rand, dst []byte) {
	switch t.class {
	case h5zNbitAtomic:
		var v uint64
		if t.precision > 0 {
			v = rng.Uint64() >> (64 - uint(t.precision))
		}
		v <<= uint(t.offset)
		for i := 0; i < t.size; i++ {
			b := byte(v >> (8 * uint(i)))
			if t.order == h5zNbitOrderLE {
				dst[i] = b
			} else {
				dst[t.size-1-i] = b
			}
		}
	case h5zNbitArray:
		for i := 0; i < t.size/t.base.size; i++ {
			t.base.fill(rng, dst[i*t.base.size:(i+1)*t.base.size])
		}
	case h5zNbitCompound:
		for _, m := range t.members {
			m.typ.fill(rng, dst[m.offset:m.offset+m.typ.size])
		}
	default:
		rng.Read(dst)
	}
}

func nbitRoundTrip(t *testing.T, typ *nbType, nelmts int, rng *rand.Rand) {
	t.Helper()
	data := make([]byte, nelmts*typ.size)
	for i := 0; i < nelmts; i++ {
		typ.fill(rng, data[i*typ.size:(i+1)*typ.size])
	}
	cd := typ.cdValues(nelmts)
	require.Len(t, cd, int(cd[0]))

	packed := nbitCompress(data, uint32(nelmts), cd)
	if !typ.fullPrecision() {
		require.Less(t, len(packed), len(data)+1, "nbit should not expand data")
	}
	out, err := h5zFilterNbitFunc(h5zFlagReverse, cd, packed)
	if typ.fullPrecision() {
		// "no need to compress": the filter is the identity
		require.NoError(t, err)
		assert.Equal(t, packed, out)
		return
	}
	require.NoError(t, err)
	assert.Equal(t, data, out)
}

func TestFilterNbitAtomic(t *testing.T) {
	rng := rand.New(rand.NewSource(10))
	for _, order := range []int{h5zNbitOrderLE, h5zNbitOrderBE} {
		for _, size := range []int{1, 2, 3, 4, 8} {
			for prec := 1; prec <= size*8; prec++ {
				for _, off := range []int{0, 1, 3, 7, 8, size*8 - prec} {
					if off < 0 || prec+off > size*8 {
						continue
					}
					typ := nbAtomic(size, order, prec, off)
					for _, n := range []int{1, 2, 5, 33} {
						nbitRoundTrip(t, typ, n, rng)
					}
				}
			}
		}
	}
}

func TestFilterNbitKnownVector(t *testing.T) {
	// 4 unsigned 12-bit values stored LE in uint16: 0xabc, 0x123, 0xfff, 0x001
	// pack MSB first into 48 bits: abc 123 fff 001
	typ := nbAtomic(2, h5zNbitOrderLE, 12, 0)
	cd := typ.cdValues(4)
	assert.Equal(t, []uint32{8, 0, 4, 1, 2, 0, 12, 0}, cd)
	data := []byte{0xbc, 0x0a, 0x23, 0x01, 0xff, 0x0f, 0x01, 0x00}
	packed := []byte{0xab, 0xc1, 0x23, 0xff, 0xf0, 0x01}
	out, err := h5zFilterNbitFunc(h5zFlagReverse, cd, packed)
	require.NoError(t, err)
	assert.Equal(t, data, out)
	assert.Equal(t, packed, nbitCompress(data, 4, cd)[:6])

	// the same with a 4-bit offset in a big-endian 16-bit field: the value
	// occupies bits 4..15 of the (BE) integer
	typ = nbAtomic(2, h5zNbitOrderBE, 12, 4)
	cd = typ.cdValues(4)
	data = []byte{0xab, 0xc0, 0x12, 0x30, 0xff, 0xf0, 0x00, 0x10}
	out, err = h5zFilterNbitFunc(h5zFlagReverse, cd, packed)
	require.NoError(t, err)
	assert.Equal(t, data, out)
}

func TestFilterNbitArrayAndCompound(t *testing.T) {
	rng := rand.New(rand.NewSource(11))

	i32p20 := nbAtomic(4, h5zNbitOrderLE, 20, 3)
	u8p5 := nbAtomic(1, h5zNbitOrderLE, 5, 2)
	i16be := nbAtomic(2, h5zNbitOrderBE, 11, 2)
	f64 := nbAtomic(8, h5zNbitOrderLE, 40, 12)

	cases := map[string]*nbType{
		"array of atomic":     nbArray(5, i16be),
		"array of array":      nbArray(12, nbArray(3, u8p5)),
		"array of 2d array":   nbArray(2, nbArray(4, nbAtomic(4, h5zNbitOrderBE, 17, 9))),
		"compound of atomics": nbCompound(12, nbMember{0, i32p20}, nbMember{4, u8p5}, nbMember{6, i16be}),
		"compound with gap":   nbCompound(24, nbMember{0, f64}, nbMember{16, i32p20}),
		"compound with noop":  nbCompound(8, nbMember{0, u8p5}, nbMember{2, nbNoop(3)}, nbMember{6, u8p5}),
		"compound with array": nbCompound(16, nbMember{0, u8p5}, nbMember{2, nbArray(4, i16be)}, nbMember{12, i32p20}),
		"nested compound":     nbCompound(32, nbMember{0, nbCompound(8, nbMember{0, u8p5}, nbMember{4, i32p20})}, nbMember{16, f64}, nbMember{8, i16be}),
		"array of compound":   nbArray(3, nbCompound(8, nbMember{0, u8p5}, nbMember{4, i32p20})),
	}
	for name, typ := range cases {
		t.Run(name, func(t *testing.T) {
			require.False(t, typ.fullPrecision())
			for _, n := range []int{1, 2, 7, 100} {
				nbitRoundTrip(t, typ, n, rng)
			}
		})
	}

	// everything full precision: need_not_compress
	typ := nbCompound(8, nbMember{0, nbAtomic(4, h5zNbitOrderLE, 32, 0)}, nbMember{4, nbAtomic(4, h5zNbitOrderBE, 32, 0)})
	require.True(t, typ.fullPrecision())
	nbitRoundTrip(t, typ, 5, rng)
}

func TestFilterNbitErrors(t *testing.T) {
	typ := nbAtomic(4, h5zNbitOrderLE, 17, 2)
	cd := typ.cdValues(50)
	data := make([]byte, 200)
	rand.New(rand.NewSource(12)).Read(data)
	packed := nbitCompress(data, 50, cd)

	_, err := h5zFilterNbitFunc(h5zFlagReverse, cd, packed)
	require.NoError(t, err)

	t.Run("cd_nelmts != cd_values[0]", func(t *testing.T) {
		bad := append([]uint32(nil), cd...)
		bad[0]++
		_, err := h5zFilterNbitFunc(h5zFlagReverse, bad, packed)
		assert.ErrorIs(t, err, &Error{Major: H5E_ARGS, Minor: H5E_BADVALUE})
		_, err = h5zFilterNbitFunc(h5zFlagReverse, nil, packed)
		assert.Error(t, err)
	})
	t.Run("truncated data", func(t *testing.T) {
		_, err := h5zFilterNbitFunc(h5zFlagReverse, cd, packed[:len(packed)/2])
		assert.ErrorIs(t, err, &Error{Major: H5E_PLINE, Minor: H5E_CANTFILTER})
		_, err = h5zFilterNbitFunc(h5zFlagReverse, cd, nil)
		assert.Error(t, err)
	})
	t.Run("precision/offset out of range", func(t *testing.T) {
		bad := append([]uint32(nil), cd...)
		bad[6] = 40
		_, err := h5zFilterNbitFunc(h5zFlagReverse, bad, packed)
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADTYPE})
		bad = append([]uint32(nil), cd...)
		bad[7] = 20
		_, err = h5zFilterNbitFunc(h5zFlagReverse, bad, packed)
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADTYPE})
	})
	t.Run("corrupt compound/array parameters never panic", func(t *testing.T) {
		comp := nbCompound(16, nbMember{0, nbArray(3, nbAtomic(2, h5zNbitOrderBE, 9, 1))}, nbMember{8, nbAtomic(4, h5zNbitOrderLE, 20, 3)})
		cd := comp.cdValues(10)
		d := make([]byte, 160)
		rand.New(rand.NewSource(13)).Read(d)
		pk := nbitCompress(d, 10, cd)
		rng := rand.New(rand.NewSource(14))
		for i := 0; i < 5000; i++ {
			bad := append([]uint32(nil), cd...)
			for k := 0; k < 1+rng.Intn(3); k++ {
				idx := 3 + rng.Intn(len(bad)-3)
				switch rng.Intn(3) {
				case 0:
					bad[idx] = uint32(rng.Intn(40))
				case 1:
					bad[idx] = 0
				default:
					bad[idx] = rng.Uint32() >> 22
				}
			}
			if bad[2] > 1000 { // keep the allocation small
				bad[2] = 1000
			}
			if uint64(bad[2])*uint64(bad[4]) > 1<<20 {
				continue
			}
			pkv := pk
			if rng.Intn(3) == 0 {
				pkv = pk[:rng.Intn(len(pk))]
			}
			_, _ = h5zFilterNbitFunc(h5zFlagReverse, bad, pkv)
		}
	})
}
