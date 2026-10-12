package hdf5

// Translated from src/H5Zscaleoffset.c (decompression direction only).
//
// The C code works in the byte order of the machine ("memory order"):
// the packed bit stream is decoded into native integers, the fill value /
// minimum are applied with native arithmetic, and the result is byte-swapped
// to the dataset's byte order if that differs from the native one
// (need_convert). The Go translation fixes the memory order to little-endian
// (H5T_native_order_g == H5T_ORDER_LE), which gives exactly the C results on
// every host because the packed stream and the cd_values/minval encodings are
// numeric and host independent.
//
// The H5Z_scaleoffset_postdecompress_{1,2,3} macros are instantiated per C
// type in C; here the per-type behaviour is expressed on zero-extended 64-bit
// values truncated to the element size, which is the same arithmetic modulo
// 2^(8*size) (for the integer types) and per-width float32/float64 arithmetic
// (for the floating-point types).
//
// The compress side, can_apply/set_local and the fill-value setters are not
// translated; only what is needed to interpret the cd_values stored in the
// file is.

import (
	"encoding/binary"
	"math"
	"strconv"
)

// H5Z_SCALEOFFSET_* local macros.
const (
	h5zScaleoffsetTotalNParms     = 20 /* Total number of parameters for filter */
	h5zScaleoffsetParmScaletype   = 0  /* "User" parameter for scale type */
	h5zScaleoffsetParmScalefactor = 1  /* "User" parameter for scale factor */
	h5zScaleoffsetParmNelmts      = 2  /* "Local" parameter for number of elements in the chunk */
	h5zScaleoffsetParmClass       = 3  /* "Local" parameter for datatype class */
	h5zScaleoffsetParmSize        = 4  /* "Local" parameter for datatype size */
	h5zScaleoffsetParmSign        = 5  /* "Local" parameter for integer datatype sign */
	h5zScaleoffsetParmOrder       = 6  /* "Local" parameter for datatype byte order */
	h5zScaleoffsetParmFilavail    = 7  /* "Local" parameter for dataset fill value existence */
	h5zScaleoffsetParmFilval      = 8  /* "Local" parameter for start location to store dataset fill value */

	h5zScaleoffsetClsInteger = 0 /* Integer (datatype class) */
	h5zScaleoffsetClsFloat   = 1 /* Floating-point (datatype class) */

	h5zScaleoffsetSgnNone = 0 /* Unsigned integer type */
	h5zScaleoffsetSgn2    = 1 /* Two's complement signed integer type */

	h5zScaleoffsetOrderLE = 0 /* Little endian (datatype byte order) */
	h5zScaleoffsetOrderBE = 1 /* Big endian (datatype byte order) */

	h5zScaleoffsetFillUndefined = 0 /* Fill value is not defined */
	h5zScaleoffsetFillDefined   = 1 /* Fill value is defined */

	/* H5Z_SO_scale_type_t */
	h5zSoFloatDscale = 0
	h5zSoFloatEscale = 1
	h5zSoInt         = 2

	/* Length of the minbits/minval header in front of the packed data */
	h5zScaleoffsetBufOffset = 21
)

// h5zScaleoffsetT is enum H5Z_scaleoffset_t.
type h5zScaleoffsetT int

const (
	h5zScaleoffsetTBad h5zScaleoffsetT = iota
	h5zScaleoffsetTUchar
	h5zScaleoffsetTUshort
	h5zScaleoffsetTUint
	h5zScaleoffsetTUlong
	h5zScaleoffsetTUlongLong
	h5zScaleoffsetTSchar
	h5zScaleoffsetTShort
	h5zScaleoffsetTInt
	h5zScaleoffsetTLong
	h5zScaleoffsetTLongLong
	h5zScaleoffsetTFloat
	h5zScaleoffsetTDouble
)

// h5zScaleoffsetParmsAtomic is parms_atomic.
type h5zScaleoffsetParmsAtomic struct {
	size     uint32 /* datatype size */
	minbits  uint32 /* minimum bits to compress one value of such datatype */
	memOrder uint32 /* current memory endianness order */
}

// h5zScaleoffsetGetType is H5Z__scaleoffset_get_type: the specific integer
// type based on datatype size and sign, or the floating-point type based on
// size. (sizeof(long) == sizeof(long long) == 8, so the t_long_long variants
// are never chosen, as on LP64 C builds.)
func h5zScaleoffsetGetType(dtypeClass, dtypeSize, dtypeSign uint32) (h5zScaleoffsetT, error) {
	typ := h5zScaleoffsetTBad

	if dtypeClass == h5zScaleoffsetClsInteger {
		if dtypeSign == h5zScaleoffsetSgnNone { /* unsigned integer */
			switch dtypeSize {
			case 1:
				typ = h5zScaleoffsetTUchar
			case 2:
				typ = h5zScaleoffsetTUshort
			case 4:
				typ = h5zScaleoffsetTUint
			case 8:
				typ = h5zScaleoffsetTUlong
			default:
				return h5zScaleoffsetTBad, errorf(H5E_PLINE, H5E_BADTYPE, "cannot find matched memory datatype")
			}
		}

		if dtypeSign == h5zScaleoffsetSgn2 { /* signed integer */
			switch dtypeSize {
			case 1:
				typ = h5zScaleoffsetTSchar
			case 2:
				typ = h5zScaleoffsetTShort
			case 4:
				typ = h5zScaleoffsetTInt
			case 8:
				typ = h5zScaleoffsetTLong
			default:
				return h5zScaleoffsetTBad, errorf(H5E_PLINE, H5E_BADTYPE, "cannot find matched memory datatype")
			}
		}
	}

	if dtypeClass == h5zScaleoffsetClsFloat {
		switch dtypeSize {
		case 4:
			typ = h5zScaleoffsetTFloat
		case 8:
			typ = h5zScaleoffsetTDouble
		default:
			return h5zScaleoffsetTBad, errorf(H5E_PLINE, H5E_BADTYPE, "cannot find matched memory datatype")
		}
	}

	return typ, nil
}

// h5zScaleoffsetGetFilval is H5Z_scaleoffset_get_filval_1/_2: the fill value
// stored from cd_values[H5Z_SCALEOFFSET_PARM_FILVAL] on, as the numeric (or,
// for floating-point types, bit pattern) value of the memory representation,
// least significant 32-bit word first, truncated to size bytes.
func h5zScaleoffsetGetFilval(size int, cdValues []uint32) uint64 {
	var v uint64
	for i := 0; i*4 < size; i++ {
		v |= uint64(cdValues[h5zScaleoffsetParmFilval+i]) << (32 * uint(i))
	}
	if size < 8 {
		v &= 1<<(8*uint(size)) - 1
	}
	return v
}

// h5zScaleoffsetConvert is H5Z__scaleoffset_convert: change the byte order of
// buf, either from little-endian to big-endian or from big-endian to
// little-endian.
func h5zScaleoffsetConvert(buf []byte, dNelmts uint32, dtypeSize uint32) {
	if dtypeSize > 1 {
		for i := uint64(0); i < uint64(dNelmts)*uint64(dtypeSize); i += uint64(dtypeSize) {
			for j := uint32(0); j < dtypeSize/2; j++ {
				/* swap pair of bytes */
				a, b := i+uint64(j), i+uint64(dtypeSize)-1-uint64(j)
				buf[a], buf[b] = buf[b], buf[a]
			}
		}
	}
}

// h5zScaleoffsetLoad reads the size-byte little-endian element at index i.
func h5zScaleoffsetLoad(data []byte, i, size int) uint64 {
	e := data[i*size : (i+1)*size]
	switch size {
	case 1:
		return uint64(e[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(e))
	case 4:
		return uint64(binary.LittleEndian.Uint32(e))
	}
	return binary.LittleEndian.Uint64(e)
}

// h5zScaleoffsetStore writes the low size bytes of v little-endian at index i.
func h5zScaleoffsetStore(data []byte, i, size int, v uint64) {
	e := data[i*size : (i+1)*size]
	switch size {
	case 1:
		e[0] = byte(v)
	case 2:
		binary.LittleEndian.PutUint16(e, uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(e, uint32(v))
	default:
		binary.LittleEndian.PutUint64(e, v)
	}
}

// h5zScaleoffsetPostdecompressI is H5Z__scaleoffset_postdecompress_i (the
// H5Z_scaleoffset_postdecompress_1 and _2 macros, and the t_schar block):
// add the minimum back, or substitute the fill value for the values that were
// marked with the all-ones pattern.
func h5zScaleoffsetPostdecompressI(data []byte, dNelmts uint32, typ h5zScaleoffsetT, filavail uint32, cdValues []uint32, minbits uint32, minval uint64) {
	var size int
	switch typ {
	case h5zScaleoffsetTUchar, h5zScaleoffsetTSchar:
		size = 1
	case h5zScaleoffsetTUshort, h5zScaleoffsetTShort:
		size = 2
	case h5zScaleoffsetTUint, h5zScaleoffsetTInt:
		size = 4
	case h5zScaleoffsetTUlong, h5zScaleoffsetTUlongLong, h5zScaleoffsetTLong, h5zScaleoffsetTLongLong:
		size = 8
	default:
		return
	}

	if filavail == h5zScaleoffsetFillDefined { /* fill value defined */
		filval := h5zScaleoffsetGetFilval(size, cdValues)
		marker := uint64(1)<<minbits - 1 /* (type)((1 << minbits) - 1) */
		for i := 0; i < int(dNelmts); i++ {
			v := h5zScaleoffsetLoad(data, i, size)
			if v == marker {
				v = filval
			} else {
				v += minval
			}
			h5zScaleoffsetStore(data, i, size, v)
		}
	} else { /* fill value undefined */
		for i := 0; i < int(dNelmts); i++ {
			h5zScaleoffsetStore(data, i, size, h5zScaleoffsetLoad(data, i, size)+minval)
		}
	}
}

// h5zScaleoffsetPow10 is pow_fun((type)10, (type)D_val) for an integral D_val:
// the correctly rounded value of 10^D in the given floating-point width.
func h5zScaleoffsetPow10(d int32, bitSize int) float64 {
	f, _ := strconv.ParseFloat("1e"+strconv.FormatInt(int64(d), 10), bitSize)
	return f
}

// h5zScaleoffsetPostdecompressFD is H5Z__scaleoffset_postdecompress_fd
// (H5Z_scaleoffset_postdecompress_3 with the _get_min, _modify_3 and
// _modify_4 macros): undo the D-scaling of floating-point data.
func h5zScaleoffsetPostdecompressFD(data []byte, dNelmts uint32, typ h5zScaleoffsetT, filavail uint32, cdValues []uint32, minbits uint32, minval uint64, dVal int32) error {
	switch typ {
	case h5zScaleoffsetTFloat:
		/* retrieve min value from corresponding position */
		minF := math.Float32frombits(uint32(minval))
		pow := float32(h5zScaleoffsetPow10(dVal, 32))
		var filval float32
		defined := filavail == h5zScaleoffsetFillDefined
		if defined {
			filval = math.Float32frombits(uint32(h5zScaleoffsetGetFilval(4, cdValues)))
		}
		marker := int32(uint32(1)<<minbits - 1)
		for i := 0; i < int(dNelmts); i++ {
			v := int32(uint32(h5zScaleoffsetLoad(data, i, 4)))
			var out float32
			if defined && v == marker {
				out = filval
			} else {
				q := float32(float32(v) / pow)
				out = float32(q + minF)
			}
			h5zScaleoffsetStore(data, i, 4, uint64(math.Float32bits(out)))
		}

	case h5zScaleoffsetTDouble:
		minF := math.Float64frombits(minval)
		pow := h5zScaleoffsetPow10(dVal, 64)
		var filval float64
		defined := filavail == h5zScaleoffsetFillDefined
		if defined {
			filval = math.Float64frombits(h5zScaleoffsetGetFilval(8, cdValues))
		}
		marker := int64(uint64(1)<<minbits - 1)
		for i := 0; i < int(dNelmts); i++ {
			v := int64(h5zScaleoffsetLoad(data, i, 8))
			var out float64
			if defined && v == marker {
				out = filval
			} else {
				q := float64(float64(v) / pow)
				out = float64(q + minF)
			}
			h5zScaleoffsetStore(data, i, 8, math.Float64bits(out))
		}

	default:
		return errorf(H5E_PLINE, H5E_BADTYPE, "cannot find matched floating-point datatype")
	}
	return nil
}

// h5zScaleoffsetNextByte is H5Z__scaleoffset_next_byte.
func h5zScaleoffsetNextByte(j *int, bufLen *uint32) {
	*j++
	*bufLen = 8
}

// h5zScaleoffsetDecompressOneByte is H5Z__scaleoffset_decompress_one_byte.
// buffer bounds are verified by the caller (the compressed buffer is known to
// hold ceil(d_nelmts*minbits/8) bytes); out-of-range reads are reported
// through the returned error rather than a panic.
func h5zScaleoffsetDecompressOneByte(data []byte, dataOffset int, k, beginI uint32, buffer []byte, j *int, bufLen *uint32, p *h5zScaleoffsetParmsAtomic, dtypeLen uint32) error {
	var datLen uint32 /* dat_len is the number of bits to be copied in each data byte */

	/* initialize value and bits of unsigned char to be copied */
	if *j >= len(buffer) {
		return errorf(H5E_PLINE, H5E_CANTFILTER, "scaleoffset compressed data is truncated")
	}
	val := uint32(buffer[*j])
	if k == beginI {
		datLen = 8 - (dtypeLen-p.minbits)%8
	} else {
		datLen = 8
	}

	if *bufLen > datLen {
		data[dataOffset+int(k)] = byte((val >> (*bufLen - datLen)) & ^(^uint32(0) << datLen))
		*bufLen -= datLen
	} else {
		data[dataOffset+int(k)] = byte((val & ^(^uint32(0) << *bufLen)) << (datLen - *bufLen))
		datLen -= *bufLen
		h5zScaleoffsetNextByte(j, bufLen)
		if datLen == 0 {
			return nil
		}

		if *j >= len(buffer) {
			return errorf(H5E_PLINE, H5E_CANTFILTER, "scaleoffset compressed data is truncated")
		}
		val = uint32(buffer[*j])
		data[dataOffset+int(k)] |= byte((val >> (*bufLen - datLen)) & ^(^uint32(0) << datLen))
		*bufLen -= datLen
	}
	return nil
}

// h5zScaleoffsetDecompressOneAtomic is H5Z__scaleoffset_decompress_one_atomic.
func h5zScaleoffsetDecompressOneAtomic(data []byte, dataOffset int, buffer []byte, j *int, bufLen *uint32, p *h5zScaleoffsetParmsAtomic) error {
	/* begin_i: the index of byte having first significant bit */
	dtypeLen := p.size * 8

	if p.memOrder == h5zScaleoffsetOrderLE { /* little endian */
		beginI := p.size - 1 - (dtypeLen-p.minbits)/8

		for k := int(beginI); k >= 0; k-- {
			if err := h5zScaleoffsetDecompressOneByte(data, dataOffset, uint32(k), beginI, buffer, j, bufLen, p, dtypeLen); err != nil {
				return err
			}
		}
	} else { /* big endian */
		beginI := (dtypeLen - p.minbits) / 8

		for k := int(beginI); k <= int(p.size-1); k++ {
			if err := h5zScaleoffsetDecompressOneByte(data, dataOffset, uint32(k), beginI, buffer, j, bufLen, p, dtypeLen); err != nil {
				return err
			}
		}
	}
	return nil
}

// h5zScaleoffsetDecompress is H5Z__scaleoffset_decompress.
func h5zScaleoffsetDecompress(data []byte, dNelmts uint32, buffer []byte, p *h5zScaleoffsetParmsAtomic) error {
	/* i: index of data, j: index of buffer,
	   buf_len: number of bits to be filled in current byte */

	/* must initialize to zeros */
	for i := range data {
		data[i] = 0
	}

	/* initialization before the loop */
	j := 0
	bufLen := uint32(8)

	/* decompress */
	for i := 0; i < int(dNelmts); i++ {
		if err := h5zScaleoffsetDecompressOneAtomic(data, i*int(p.size), buffer, &j, &bufLen, p); err != nil {
			return err
		}
	}
	return nil
}

// h5zFilterScaleoffsetFunc is H5Z__filter_scaleoffset with H5Z_FLAG_REVERSE.
func h5zFilterScaleoffsetFunc(flags uint, cdValues []uint32, buf []byte) ([]byte, error) {
	/* check arguments */
	if len(cdValues) != h5zScaleoffsetTotalNParms {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid scaleoffset number of parameters")
	}
	if flags&h5zFlagReverse == 0 {
		return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED, "scaleoffset is only supported in the read direction")
	}

	/* Check if memory byte order matches dataset datatype byte order
	 * (memory is little-endian byte order) */
	needConvert := cdValues[h5zScaleoffsetParmOrder] == h5zScaleoffsetOrderBE

	/* copy filter parameters to local variables */
	dNelmts := cdValues[h5zScaleoffsetParmNelmts]
	dtypeClass := cdValues[h5zScaleoffsetParmClass]
	dtypeSign := cdValues[h5zScaleoffsetParmSign]
	filavail := cdValues[h5zScaleoffsetParmFilavail]
	scaleFactor := int32(cdValues[h5zScaleoffsetParmScalefactor])
	scaleType := cdValues[h5zScaleoffsetParmScaletype]

	/* check and assign proper values set by user to related parameters
	 * scale type can be H5Z_SO_FLOAT_DSCALE (0), H5Z_SO_FLOAT_ESCALE (1) or H5Z_SO_INT (other)
	 * H5Z_SO_FLOAT_DSCALE : floating-point type, variable-minimum-bits method,
	 *                      scale factor is decimal scale factor
	 * H5Z_SO_FLOAT_ESCALE : floating-point type, fixed-minimum-bits method,
	 *                      scale factor is the fixed minimum number of bits
	 * H5Z_SO_INT          : integer type, scale_factor is minimum number of bits
	 */
	if dtypeClass == h5zScaleoffsetClsFloat { /* floating-point type */
		if scaleType != h5zSoFloatDscale && scaleType != h5zSoFloatEscale {
			return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid scale type")
		}
	}

	if dtypeClass == h5zScaleoffsetClsInteger { /* integer type */
		if scaleType != h5zSoInt {
			return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid scale type")
		}

		/* if scale_factor is less than 0 for integer, library will reset it to 0
		 * in this case, library will calculate the minimum-bits
		 */
		if scaleFactor < 0 {
			scaleFactor = 0
		}
	}

	/* fixed-minimum-bits method is not implemented and is forbidden */
	if scaleType == h5zSoFloatEscale {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "E-scaling method not supported")
	}

	var dVal int32                     /* decimal scale factor */
	if scaleType == h5zSoFloatDscale { /* floating-point type, variable-minimum-bits */
		dVal = scaleFactor
	} else { /* integer type, or floating-point type with fixed-minimum-bits method */
		if scaleFactor > int32(cdValues[h5zScaleoffsetParmSize]*8) {
			return nil, errorf(H5E_ARGS, H5E_BADVALUE, "minimum number of bits exceeds maximum")
		}

		/* no need to process data */
		if scaleFactor == int32(cdValues[h5zScaleoffsetParmSize]*8) {
			return buf, nil
		}
	}

	/* prepare parameters to pass to compress/decompress functions */
	var p h5zScaleoffsetParmsAtomic
	p.size = cdValues[h5zScaleoffsetParmSize]
	p.memOrder = h5zScaleoffsetOrderLE

	/* input; decompress */

	/* retrieve values of minbits and minval from input compressed buffer
	 * retrieve them corresponding to how they are stored during compression
	 */
	if len(buf) < 5 {
		return nil, errorf(H5E_PLINE, H5E_CANTFILTER, "scaleoffset chunk is too small for its header")
	}
	minbits := binary.LittleEndian.Uint32(buf)
	if minbits >= p.size*8 {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "minimum number of bits exceeds size of type")
	}

	/* retrieval of minval takes into consideration situation where sizeof
	 * unsigned long long (datatype of minval) may change from compression
	 * to decompression, only smaller size is used
	 */
	minvalSize := int(buf[4])
	if minvalSize > 8 {
		minvalSize = 8
	}
	if len(buf) < 5+minvalSize {
		return nil, errorf(H5E_PLINE, H5E_CANTFILTER, "scaleoffset chunk is too small for its header")
	}
	var minval uint64
	for i := 0; i < minvalSize; i++ {
		minval |= uint64(buf[5+i]) << (uint(i) * 8)
	}

	p.minbits = minbits

	/* get the memory type (the C code does this after decompression; it is
	 * done first here because it also rejects every element size the
	 * decompressor could not handle safely) */
	typ, err := h5zScaleoffsetGetType(dtypeClass, p.size, dtypeSign)
	if err != nil {
		return nil, wrapf(err, H5E_PLINE, H5E_BADTYPE, "cannot use C integer datatype for cast")
	}
	if typ == h5zScaleoffsetTBad {
		return nil, errorf(H5E_PLINE, H5E_BADTYPE, "cannot use C integer datatype for cast")
	}

	/* calculate size of output buffer after decompression */
	sizeOut := uint64(dNelmts) * uint64(p.size)
	if sizeOut > 1<<32-1 {
		return nil, errorf(H5E_RESOURCE, H5E_CANTALLOC, "memory allocation failed for scaleoffset decompression")
	}

	/* the packed values start behind the header */
	var packed []byte
	if len(buf) > h5zScaleoffsetBufOffset {
		packed = buf[h5zScaleoffsetBufOffset:]
	}
	if minbits != 0 && (uint64(dNelmts)*uint64(minbits)+7)/8 > uint64(len(packed)) {
		return nil, errorf(H5E_PLINE, H5E_CANTFILTER, "scaleoffset compressed data is truncated")
	}

	/* allocate memory space for decompressed buffer */
	outbuf := make([]byte, sizeOut)

	/* (The "special case: minbits equal to full precision" of the C code is
	 * unreachable: minbits >= size*8 was rejected above.) */

	/* decompress the buffer if minbits not equal to zero */
	if minbits != 0 {
		if err := h5zScaleoffsetDecompress(outbuf, dNelmts, packed, &p); err != nil {
			return nil, err
		}
	}
	/* else: fill value is not defined and all data elements have the same
	 * value; outbuf is all zeros already */

	/* postprocess after decompression */
	if dtypeClass == h5zScaleoffsetClsInteger {
		h5zScaleoffsetPostdecompressI(outbuf, dNelmts, typ, filavail, cdValues, minbits, minval)
	}

	if dtypeClass == h5zScaleoffsetClsFloat {
		if scaleType == 0 { /* variable-minimum-bits method */
			if err := h5zScaleoffsetPostdecompressFD(outbuf, dNelmts, typ, filavail, cdValues, minbits, minval, dVal); err != nil {
				return nil, wrapf(err, H5E_PLINE, H5E_BADTYPE, "post-decompression failed")
			}
		}
	}

	/* after postprocess, convert to dataset datatype endianness order if needed */
	if needConvert {
		h5zScaleoffsetConvert(outbuf, dNelmts, p.size)
	}

	return outbuf, nil
}
