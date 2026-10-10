package hdf5

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test-only encoder: translation of the compress direction of
// src/H5Zscaleoffset.c (H5Z__filter_scaleoffset compress branch,
// H5Z__scaleoffset_precompress_i / _fd with the max_min/check/modify macros,
// H5Z__scaleoffset_log2, H5Z__scaleoffset_compress[_one_atomic|_one_byte]).
//
// The C code runs these on native (little-endian here) memory; the encoder
// takes values in the dataset byte order and converts first, as C does.
// ---------------------------------------------------------------------------

// soLog2 is H5Z__scaleoffset_log2: ceiling of log2(num).
func soLog2(num uint64) uint32 {
	var v uint32
	lowerBound := uint64(1)
	val := num
	for val >>= 1; val != 0; val >>= 1 {
		v++
		lowerBound <<= 1
	}
	if num == lowerBound {
		return v
	}
	return v + 1
}

func soMask(size int) uint64 {
	if size >= 8 {
		return ^uint64(0)
	}
	return 1<<(8*uint(size)) - 1
}

func soSignExtend(v uint64, size int) int64 {
	shift := 64 - 8*uint(size)
	return int64(v<<shift) >> shift
}

// soEncodeInts is H5Z__scaleoffset_precompress_i. raw holds the element bit
// patterns zero-extended to uint64. It returns the (possibly modified)
// elements, minbits and minval. On the "range too large" early exits minbits
// is the full width and elements are unmodified.
func soEncodeInts(raw []uint64, size int, signed bool, minbits uint32, filavail bool, fill uint64) ([]uint64, uint32, uint64) {
	W := uint32(size * 8)
	mask := soMask(size)
	out := append([]uint64(nil), raw...)

	less := func(a, b uint64) bool {
		if signed {
			return soSignExtend(a, size) < soSignExtend(b, size)
		}
		return a < b
	}

	isFill := func(r uint64) bool { return filavail && r == fill }

	// min/max over the non-fill elements (min = max = 0 if there are none)
	var minv, maxv uint64
	first := true
	for _, r := range raw {
		if isFill(r) {
			continue
		}
		if first {
			minv, maxv = r, r
			first = false
			continue
		}
		if less(maxv, r) {
			maxv = r
		}
		if less(r, minv) {
			minv = r
		}
	}
	if !filavail && len(raw) > 0 {
		minv, maxv = raw[0], raw[0]
		for _, r := range raw {
			if less(maxv, r) {
				maxv = r
			}
			if less(r, minv) {
				minv = r
			}
		}
	}

	if minbits == 0 { // H5Z_SO_INT_MINBITS_DEFAULT: calculate minbits
		diff := (maxv - minv) & mask
		if diff > mask-2 {
			return out, W, 0
		}
		span := (maxv - minv + 1) & mask
		if filavail {
			minbits = soLog2(span + 1)
		} else {
			minbits = soLog2(span)
		}
	}
	if minbits != W {
		for i, r := range raw {
			if isFill(r) {
				out[i] = (uint64(1)<<minbits - 1) & mask
			} else {
				out[i] = (r - minv) & mask
			}
		}
	}
	return out, minbits, minv
}

// soEncodeFloats is H5Z__scaleoffset_precompress_fd (variable-minimum-bits
// method) for T = float32 or float64. It returns the integer-valued elements
// (as bit patterns), minbits and minval (the bits of min).
func soEncodeFloats[T float32 | float64](vals []T, D int32, filavail bool, fill T, bitSize int) ([]uint64, uint32, uint64) {
	W := uint32(bitSize)
	pow := T(h5zScaleoffsetPow10(D, bitSize))
	powNegD := math.Pow(10, -float64(D))

	isFill := func(x T) bool {
		return filavail && math.Abs(float64(x-fill)) < powNegD
	}

	var minv, maxv T
	first := true
	for _, x := range vals {
		if isFill(x) {
			continue
		}
		if first {
			minv, maxv = x, x
			first = false
			continue
		}
		if x > maxv {
			maxv = x
		}
		if x < minv {
			minv = x
		}
	}
	if !filavail {
		minv, maxv = vals[0], vals[0]
		for _, x := range vals {
			maxv = max(maxv, x)
			minv = min(minv, x)
		}
	}

	// check_3
	if math.Round(float64(maxv*pow-minv*pow)) > math.Ldexp(1, bitSize-1) {
		return nil, W, 0
	}
	span := uint64(int64(math.Round(float64(maxv*pow-minv*pow)))) + 1
	var minbits uint32
	if filavail {
		minbits = soLog2(span + 1)
	} else {
		minbits = soLog2(span)
	}

	out := make([]uint64, len(vals))
	if minbits != W {
		for i, x := range vals {
			var q int64
			if filavail && math.Abs(float64(x-fill)) < powNegD {
				q = int64(uint64(1)<<minbits - 1)
			} else {
				q = int64(math.Round(float64(x*pow - minv*pow)))
			}
			if bitSize == 32 {
				out[i] = uint64(uint32(int32(q)))
			} else {
				out[i] = uint64(q)
			}
		}
	}
	var minBits uint64
	switch any(minv).(type) {
	case float32:
		minBits = uint64(math.Float32bits(float32(minv)))
	default:
		minBits = math.Float64bits(float64(minv))
	}
	return out, minbits, minBits
}

// soPack is H5Z__scaleoffset_compress (with _one_atomic/_one_byte), memory
// order little-endian.
func soPack(vals []uint64, size int, minbits uint32, bufSize int) []byte {
	buffer := make([]byte, bufSize)
	dtypeLen := uint32(size * 8)
	j := 0
	bufLen := uint32(8)
	for _, v := range vals {
		beginI := uint32(size) - 1 - (dtypeLen-minbits)/8
		for k := int(beginI); k >= 0; k-- {
			b := uint32(byte(v >> (8 * uint(k))))
			var datLen uint32
			if uint32(k) == beginI {
				datLen = 8 - (dtypeLen-minbits)%8
			} else {
				datLen = 8
			}
			if bufLen > datLen {
				buffer[j] |= byte((b & ^(^uint32(0) << datLen)) << (bufLen - datLen))
				bufLen -= datLen
			} else {
				buffer[j] |= byte((b >> (datLen - bufLen)) & ^(^uint32(0) << bufLen))
				datLen -= bufLen
				j++
				bufLen = 8
				if datLen == 0 {
					continue
				}
				buffer[j] = byte((b & ^(^uint32(0) << datLen)) << (bufLen - datLen))
				bufLen -= datLen
			}
		}
	}
	return buffer
}

// soAssemble builds the chunk as H5Z__filter_scaleoffset writes it: 4 bytes
// minbits, 1 byte sizeof(minval), 8 bytes minval, zero padding up to 21, the
// packed values.
func soAssemble(vals []uint64, size int, minbits uint32, minval uint64) []byte {
	nbytes := len(vals) * size
	sizeOut := h5zScaleoffsetBufOffset + nbytes*int(minbits)/(size*8) + 1
	out := make([]byte, sizeOut)
	binary.LittleEndian.PutUint32(out, minbits)
	out[4] = 8
	binary.LittleEndian.PutUint64(out[5:], minval)
	if minbits == uint32(size*8) {
		for i, v := range vals {
			h5zScaleoffsetStore(out[h5zScaleoffsetBufOffset:], i, size, v)
		}
		return out
	}
	if minbits != 0 {
		copy(out[h5zScaleoffsetBufOffset:], soPack(vals, size, minbits, sizeOut-h5zScaleoffsetBufOffset))
	}
	return out
}

// soCD builds the 20 cd_values of H5Z__set_local_scaleoffset.
func soCD(scaleType uint32, scaleFactor int32, nelmts int, class uint32, size int, signed bool, order uint32, filavail bool, fill uint64) []uint32 {
	cd := make([]uint32, h5zScaleoffsetTotalNParms)
	cd[h5zScaleoffsetParmScaletype] = scaleType
	cd[h5zScaleoffsetParmScalefactor] = uint32(scaleFactor)
	cd[h5zScaleoffsetParmNelmts] = uint32(nelmts)
	cd[h5zScaleoffsetParmClass] = class
	cd[h5zScaleoffsetParmSize] = uint32(size)
	if signed {
		cd[h5zScaleoffsetParmSign] = h5zScaleoffsetSgn2
	}
	cd[h5zScaleoffsetParmOrder] = order
	if filavail {
		cd[h5zScaleoffsetParmFilavail] = 1
		cd[h5zScaleoffsetParmFilval] = uint32(fill)
		cd[h5zScaleoffsetParmFilval+1] = uint32(fill >> 32)
		if size < 8 {
			cd[h5zScaleoffsetParmFilval+1] = 0
		}
	}
	return cd
}

// soToBytes lays raw element values out in the dataset byte order.
func soToBytes(raw []uint64, size int, order uint32) []byte {
	out := make([]byte, len(raw)*size)
	for i, v := range raw {
		h5zScaleoffsetStore(out, i, size, v)
	}
	if order == h5zScaleoffsetOrderBE {
		h5zScaleoffsetConvert(out, uint32(len(raw)), uint32(size))
	}
	return out
}

func soRandomInts(rng *rand.Rand, n, size int, signed bool, bitsUsed int, base int64) []uint64 {
	raw := make([]uint64, n)
	for i := range raw {
		var off uint64
		if bitsUsed > 0 {
			off = rng.Uint64() >> (64 - uint(bitsUsed))
		}
		raw[i] = (uint64(base) + off) & soMask(size)
	}
	return raw
}

func TestFilterScaleoffsetInteger(t *testing.T) {
	rng := rand.New(rand.NewSource(20))
	for _, order := range []uint32{h5zScaleoffsetOrderLE, h5zScaleoffsetOrderBE} {
		for _, size := range []int{1, 2, 4, 8} {
			for _, signed := range []bool{false, true} {
				for _, filavail := range []bool{false, true} {
					for _, bitsUsed := range []int{0, 1, 3, 7, 9, size*8 - 2, size * 8 / 2} {
						if bitsUsed < 0 || bitsUsed > size*8-2 {
							continue
						}
						for _, base := range []int64{0, 5, -7, 1000} {
							n := 1 + rng.Intn(300)
							raw := soRandomInts(rng, n, size, signed, bitsUsed, base)
							var fill uint64
							if filavail {
								fill = rng.Uint64() & soMask(size)
								// sprinkle fill values among the data
								for i := range raw {
									if rng.Intn(4) == 0 {
										raw[i] = fill
									}
								}
							}

							enc, minbits, minval := soEncodeInts(raw, size, signed, 0, filavail, fill)
							if minbits == uint32(size*8) {
								continue // full precision, see TestFilterScaleoffsetFullPrecision
							}
							chunk := soAssemble(enc, size, minbits, minval)
							cd := soCD(h5zSoInt, 0, n, h5zScaleoffsetClsInteger, size, signed, order, filavail, fill)

							out, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
							require.NoError(t, err, "order %d size %d signed %v fill %v bits %d base %d", order, size, signed, filavail, bitsUsed, base)
							require.Equal(t, soToBytes(raw, size, order), out,
								"order %d size %d signed %v fill %v bits %d base %d minbits %d", order, size, signed, filavail, bitsUsed, base, minbits)
						}
					}
				}
			}
		}
	}
}

func TestFilterScaleoffsetIntegerUserMinbits(t *testing.T) {
	// scale_factor > 0 is the user's minimum number of bits: the writer packs
	// exactly that many bits per value.
	rng := rand.New(rand.NewSource(21))
	for _, size := range []int{2, 4} {
		for _, order := range []uint32{h5zScaleoffsetOrderLE, h5zScaleoffsetOrderBE} {
			raw := soRandomInts(rng, 100, size, true, 10, -300)
			enc, minbits, minval := soEncodeInts(raw, size, true, 12, false, 0)
			require.Equal(t, uint32(12), minbits)
			chunk := soAssemble(enc, size, minbits, minval)
			cd := soCD(h5zSoInt, 12, 100, h5zScaleoffsetClsInteger, size, true, order, false, 0)
			out, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
			require.NoError(t, err)
			assert.Equal(t, soToBytes(raw, size, order), out)
		}
	}
}

func TestFilterScaleoffsetKnownVector(t *testing.T) {
	// uint16 values 100, 101, 105, 107 -> minval 100, span 8 -> minbits 3,
	// stored offsets 0,1,5,7 = 000 001 101 111 = 0b0000_1101_11xx_xxxx
	chunk := make([]byte, 21+3)
	chunk[0] = 3 // minbits
	chunk[4] = 8 // sizeof(minval)
	chunk[5] = 100
	chunk[21] = 0b00000110
	chunk[22] = 0b11110000
	cd := soCD(h5zSoInt, 0, 4, h5zScaleoffsetClsInteger, 2, false, h5zScaleoffsetOrderLE, false, 0)
	out, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
	require.NoError(t, err)
	assert.Equal(t, []byte{100, 0, 101, 0, 105, 0, 107, 0}, out)

	cd[h5zScaleoffsetParmOrder] = h5zScaleoffsetOrderBE
	out, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
	require.NoError(t, err)
	assert.Equal(t, []byte{0, 100, 0, 101, 0, 105, 0, 107}, out)

	// with a fill value of 7 (int16): offset 7 = 0b111 = (1<<3)-1 is the marker
	cd = soCD(h5zSoInt, 0, 4, h5zScaleoffsetClsInteger, 2, true, h5zScaleoffsetOrderLE, true, 0xfffe /* -2 */)
	out, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
	require.NoError(t, err)
	assert.Equal(t, []byte{100, 0, 101, 0, 105, 0, 0xfe, 0xff}, out)

	// minbits == 0: all elements equal minval
	chunk = make([]byte, 22)
	chunk[4] = 8
	chunk[5] = 0xf6 // -10 as int8
	chunk[6], chunk[7], chunk[8], chunk[9], chunk[10], chunk[11], chunk[12] = 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff
	cd = soCD(h5zSoInt, 0, 6, h5zScaleoffsetClsInteger, 1, true, h5zScaleoffsetOrderLE, false, 0)
	out, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
	require.NoError(t, err)
	assert.Equal(t, []byte{0xf6, 0xf6, 0xf6, 0xf6, 0xf6, 0xf6}, out)
}

func TestFilterScaleoffsetFullPrecision(t *testing.T) {
	// scale_factor == size*8: "no need to process data", the buffer is
	// returned untouched.
	cd := soCD(h5zSoInt, 32, 3, h5zScaleoffsetClsInteger, 4, false, h5zScaleoffsetOrderLE, false, 0)
	in := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	out, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, in)
	require.NoError(t, err)
	assert.Equal(t, in, out)

	// scale_factor > size*8 is invalid
	cd = soCD(h5zSoInt, 33, 3, h5zScaleoffsetClsInteger, 4, false, h5zScaleoffsetOrderLE, false, 0)
	_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, in)
	assert.ErrorIs(t, err, &Error{Major: H5E_ARGS, Minor: H5E_BADVALUE})

	// a stored minbits equal to the type width is rejected by the C decoder
	// ("minimum number of bits exceeds size of type")
	raw := []uint64{0, 0xffffffff, 12345}
	enc, minbits, minval := soEncodeInts(raw, 4, false, 0, false, 0)
	require.Equal(t, uint32(32), minbits)
	cd = soCD(h5zSoInt, 0, 3, h5zScaleoffsetClsInteger, 4, false, h5zScaleoffsetOrderLE, false, 0)
	_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, soAssemble(enc, 4, minbits, minval))
	assert.ErrorIs(t, err, &Error{Major: H5E_ARGS, Minor: H5E_BADVALUE})
}

func soFloatCase[T float32 | float64](t *testing.T, rng *rand.Rand, order uint32, D int32, filavail bool, scale T, offset T) {
	t.Helper()
	size := 4
	bitSize := 32
	var zero T
	if any(zero) == any(float64(0)) {
		size, bitSize = 8, 64
	}
	n := 1 + rng.Intn(200)
	step := T(math.Pow(10, -float64(D)))
	fill := T(-999.5)
	vals := make([]T, n)
	nfill := 0
	for i := range vals {
		if filavail && rng.Intn(4) == 0 {
			vals[i] = fill
			nfill++
			continue
		}
		vals[i] = offset + T(rng.Float64())*scale + T(rng.Intn(3))*step
	}
	if nfill == len(vals) {
		vals[0] = offset
	}

	var fillBits uint64
	if size == 4 {
		fillBits = uint64(math.Float32bits(float32(fill)))
	} else {
		fillBits = math.Float64bits(float64(fill))
	}

	enc, minbits, minval := soEncodeFloats(vals, D, filavail, fill, bitSize)
	require.NotNil(t, enc)
	require.Less(t, minbits, uint32(bitSize))
	chunk := soAssemble(enc, size, minbits, minval)
	cd := soCD(h5zSoFloatDscale, D, n, h5zScaleoffsetClsFloat, size, false, order, filavail, fillBits)

	out, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
	require.NoError(t, err)
	require.Len(t, out, n*size)
	if order == h5zScaleoffsetOrderBE {
		h5zScaleoffsetConvert(out, uint32(n), uint32(size))
	}
	for i, v := range vals {
		var got T
		if size == 4 {
			got = T(math.Float32frombits(binary.LittleEndian.Uint32(out[4*i:])))
		} else {
			got = T(math.Float64frombits(binary.LittleEndian.Uint64(out[8*i:])))
		}
		if filavail && v == fill {
			require.Equal(t, fill, got, "element %d should be the fill value", i)
			continue
		}
		// the D-scale method keeps D decimal digits: error <= 0.5 * 10^-D
		// plus the rounding of the float type
		eps := math.Pow(2, -52)
		if size == 4 {
			eps = math.Pow(2, -23)
		}
		tol := 0.5*float64(step) + 8*eps*(math.Abs(float64(offset))+float64(scale)+1)
		require.InDelta(t, float64(v), float64(got), tol, "element %d D=%d order %d", i, D, order)
	}
}

func TestFilterScaleoffsetFloat(t *testing.T) {
	rng := rand.New(rand.NewSource(22))
	for _, order := range []uint32{h5zScaleoffsetOrderLE, h5zScaleoffsetOrderBE} {
		for _, filavail := range []bool{false, true} {
			for D := int32(0); D <= 5; D++ {
				for rep := 0; rep < 8; rep++ {
					soFloatCase[float32](t, rng, order, D, filavail, 100, -20)
					soFloatCase[float64](t, rng, order, D, filavail, 100, -20)
					soFloatCase[float64](t, rng, order, D, filavail, 1e6, 5e5)
					soFloatCase[float32](t, rng, order, D, filavail, 0.5, 3)
				}
			}
		}
	}
}

func TestFilterScaleoffsetFloatKnownVector(t *testing.T) {
	// float32, D = 2: values 1.25, 3.5, 2.0 -> min 1.25, scaled offsets
	// round((x-1.25)*100) = 0, 225, 75 -> span 226 -> minbits 8
	chunk := make([]byte, 21+3)
	chunk[0] = 8
	chunk[4] = 8
	binary.LittleEndian.PutUint64(chunk[5:], uint64(math.Float32bits(1.25)))
	chunk[21], chunk[22], chunk[23] = 0, 225, 75
	cd := soCD(h5zSoFloatDscale, 2, 3, h5zScaleoffsetClsFloat, 4, false, h5zScaleoffsetOrderLE, false, 0)
	out, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
	require.NoError(t, err)
	assert.Equal(t, float32(1.25), math.Float32frombits(binary.LittleEndian.Uint32(out)))
	assert.Equal(t, float32(3.5), math.Float32frombits(binary.LittleEndian.Uint32(out[4:])))
	assert.Equal(t, float32(2.0), math.Float32frombits(binary.LittleEndian.Uint32(out[8:])))

	// double with fill value defined: marker (1<<8)-1 = 255 gives the fill
	chunk = make([]byte, 21+3)
	chunk[0] = 8
	chunk[4] = 8
	binary.LittleEndian.PutUint64(chunk[5:], math.Float64bits(-4.0))
	chunk[21], chunk[22], chunk[23] = 40, 255, 3
	cd = soCD(h5zSoFloatDscale, 1, 3, h5zScaleoffsetClsFloat, 8, false, h5zScaleoffsetOrderBE, true, math.Float64bits(1e9))
	out, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, chunk)
	require.NoError(t, err)
	h5zScaleoffsetConvert(out, 3, 8)
	forty, three, minusFour := 40.0, 3.0, -4.0
	assert.Equal(t, forty/10+minusFour, math.Float64frombits(binary.LittleEndian.Uint64(out)))
	assert.Equal(t, 1e9, math.Float64frombits(binary.LittleEndian.Uint64(out[8:])))
	assert.Equal(t, three/10+minusFour, math.Float64frombits(binary.LittleEndian.Uint64(out[16:])))
}

func TestFilterScaleoffsetErrors(t *testing.T) {
	cd := soCD(h5zSoInt, 0, 4, h5zScaleoffsetClsInteger, 2, false, h5zScaleoffsetOrderLE, false, 0)
	good := make([]byte, 24)
	good[0], good[4] = 3, 8

	_, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, good)
	require.NoError(t, err)

	t.Run("number of parameters", func(t *testing.T) {
		_, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd[:19], good)
		assert.ErrorIs(t, err, &Error{Major: H5E_ARGS, Minor: H5E_BADVALUE})
	})
	t.Run("scale type", func(t *testing.T) {
		bad := append([]uint32(nil), cd...)
		bad[h5zScaleoffsetParmScaletype] = h5zSoFloatDscale
		_, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, bad, good)
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADVALUE})

		f := soCD(h5zSoFloatEscale, 5, 4, h5zScaleoffsetClsFloat, 4, false, 0, false, 0)
		_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, f, good)
		assert.ErrorContains(t, err, "E-scaling")

		f = soCD(h5zSoInt, 5, 4, h5zScaleoffsetClsFloat, 4, false, 0, false, 0)
		_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, f, good)
		assert.ErrorContains(t, err, "invalid scale type")
	})
	t.Run("minbits too large", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[0] = 16
		_, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, bad)
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADVALUE})
	})
	t.Run("truncated", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[0] = 9
		_, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, bad[:22]) // needs 4*9 bits = 5 bytes
		assert.ErrorIs(t, err, &Error{Minor: H5E_CANTFILTER})
		_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, good[:4])
		assert.Error(t, err)
		_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, cd, nil)
		assert.Error(t, err)
	})
	t.Run("unsupported element size", func(t *testing.T) {
		bad := soCD(h5zSoInt, 0, 4, h5zScaleoffsetClsInteger, 3, false, 0, false, 0)
		_, err := h5zFilterScaleoffsetFunc(h5zFlagReverse, bad, good)
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADTYPE})
		bad = soCD(h5zSoInt, 0, 4, h5zScaleoffsetClsInteger, 16, false, 0, false, 0)
		_, err = h5zFilterScaleoffsetFunc(h5zFlagReverse, bad, good)
		assert.ErrorIs(t, err, &Error{Minor: H5E_BADTYPE})
	})
	t.Run("random parameters never panic", func(t *testing.T) {
		rng := rand.New(rand.NewSource(23))
		for i := 0; i < 5000; i++ {
			bad := append([]uint32(nil), cd...)
			for k := 0; k < 1+rng.Intn(3); k++ {
				bad[rng.Intn(10)] = uint32(rng.Intn(70)) - uint32(rng.Intn(3))
			}
			if uint64(bad[h5zScaleoffsetParmNelmts])*uint64(bad[h5zScaleoffsetParmSize]) > 1<<20 {
				continue
			}
			buf := make([]byte, rng.Intn(60))
			rng.Read(buf)
			_, _ = h5zFilterScaleoffsetFunc(h5zFlagReverse, bad, buf)
		}
	})
}
