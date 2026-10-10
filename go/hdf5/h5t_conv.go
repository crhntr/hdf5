package hdf5

// Conversion of raw file data to canonical Go values (see h5/value.go).
//
// This is the Go counterpart of the H5T_conv_* functions the C library runs
// when data is read in the file datatype and converted to a native memory
// type: src/H5Tconv_integer.c (H5T__conv_i_i), src/H5Tconv_float.c
// (H5T__conv_f_f), src/H5Tconv_string.c (H5T__conv_s_s),
// src/H5Tconv_bitfield.c, src/H5Tconv_enum.c, src/H5Tconv_compound.c,
// src/H5Tconv_array.c, src/H5Tconv_vlen.c with src/H5Tvlen.c (disk vlen
// access), and src/H5Tconv_reference.c with src/H5Tref.c and src/H5Rint.c
// (disk references).
//
// A datatype is compiled once into a tree of converter functions which is
// then applied to every element.

import (
	"bytes"
	"errors"
	"math"
	"math/bits"

	"github.com/HDFGroup/hdf5/go/h5"
)

// h5tConvFunc converts one element (exactly Size bytes of the file
// datatype) to its Go value.
type h5tConvFunc func(elem []byte) (any, error)

// h5tDecodeValues converts n elements of raw file data (in the file datatype
// dt, dt.Size bytes each) into the canonical Go values described in
// h5/value.go. Variable-length data and old-style references are resolved
// through f's global heap.
func h5tDecodeValues(f *File, dt *h5.Datatype, buf []byte, n uint64) ([]any, error) {
	if dt == nil || dt.Size == 0 {
		return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "invalid datatype")
	}
	hi, need := bits.Mul64(n, dt.Size)
	if hi != 0 || need > uint64(len(buf)) {
		return nil, errorf(H5E_DATATYPE, H5E_OVERFLOW, "buffer of %d bytes too small for %d elements of %d bytes", len(buf), n, dt.Size)
	}
	conv, err := h5tCompile(f, dt)
	if err != nil {
		return nil, err
	}
	out := make([]any, n)
	for i := uint64(0); i < n; i++ {
		elem := buf[i*dt.Size : (i+1)*dt.Size]
		v, err := conv(elem)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// h5tCompile builds the converter for dt (the role of H5T_path_find:
// choosing the conversion function for a source/destination pair).
func h5tCompile(f *File, dt *h5.Datatype) (h5tConvFunc, error) {
	if dt == nil || dt.Size == 0 {
		return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "invalid datatype")
	}
	switch dt.Class {
	case h5.ClassInteger:
		return h5tCompileInteger(dt), nil
	case h5.ClassFloat:
		return h5tCompileFloat(dt)
	case h5.ClassTime, h5.ClassOpaque:
		return func(elem []byte) (any, error) {
			return append([]byte{}, elem...), nil
		}, nil
	case h5.ClassString:
		return h5tCompileString(dt)
	case h5.ClassBitfield:
		return func(elem []byte) (any, error) {
			return h5tIntegerBits(dt, elem), nil
		}, nil
	case h5.ClassEnum:
		return h5tCompileEnum(dt)
	case h5.ClassCompound:
		return h5tCompileCompound(f, dt)
	case h5.ClassArray:
		return h5tCompileArray(f, dt)
	case h5.ClassVlen:
		return h5tCompileVlen(f, dt)
	case h5.ClassReference:
		return h5tCompileReference(f, dt)
	}
	return nil, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "unknown datatype class %d", dt.Class)
}

/* ------------------------------------------------------------------------
 * Integers and bitfields (H5T__conv_i_i, H5T__conv_b_b)
 */

// h5tLittleEndian returns elem in little-endian byte order ("put the data
// in little endian order so our loops aren't so complicated"). The result
// may alias elem when no reordering is needed.
func h5tLittleEndian(order h5.ByteOrder, elem []byte) []byte {
	if order != h5.OrderBE {
		return elem
	}
	s := make([]byte, len(elem))
	for i := range elem {
		s[len(elem)-1-i] = elem[i]
	}
	return s
}

// h5tIntegerBits extracts the Precision bits at Offset of an integer or
// bitfield element (after byte order normalization) as an unsigned value,
// sign extended to 64 bits for signed integers with fewer than 64 bits of
// precision. Precisions above 64 bits yield the low 64 bits. Bits outside
// the element are treated as zero.
func h5tIntegerBits(dt *h5.Datatype, elem []byte) uint64 {
	s := h5tLittleEndian(dt.Order, elem)
	total := uint64(len(s)) * 8
	off, prec := uint64(dt.Offset), uint64(dt.Precision)
	if off >= total || prec == 0 {
		return 0
	}
	if off+prec > total {
		prec = total - off
	}
	v := h5tBitGetD(s, off, min(prec, 64))
	if dt.Class == h5.ClassInteger && dt.Sign == h5.Sign2 && prec < 64 && v>>(prec-1)&1 != 0 {
		/* Negative: extend the sign bit (H5T__conv_i_i, signed to wider signed) */
		v |= ^uint64(0) << prec
	}
	return v
}

// h5tCompileInteger converts an integer to the Go type selected by its size
// and sign: int8/16/32/64 and uint8/16/32/64, int64/uint64 otherwise.
func h5tCompileInteger(dt *h5.Datatype) h5tConvFunc {
	signed := dt.Sign == h5.Sign2
	return func(elem []byte) (any, error) {
		v := h5tIntegerBits(dt, elem)
		if signed {
			switch dt.Size {
			case 1:
				return int8(v), nil
			case 2:
				return int16(v), nil
			case 4:
				return int32(v), nil
			}
			return int64(v), nil
		}
		switch dt.Size {
		case 1:
			return uint8(v), nil
		case 2:
			return uint16(v), nil
		case 4:
			return uint32(v), nil
		}
		return v, nil
	}
}

/* ------------------------------------------------------------------------
 * Floating point (H5T__conv_f_f)
 */

// h5tAtomicFloat is the subset of H5T_atomic_t used by H5T__conv_f_f.
type h5tAtomicFloat struct {
	size   uint64
	order  h5.ByteOrder
	offset uint64
	prec   uint64
	lsbPad h5.Pad
	msbPad h5.Pad
	sign   uint64
	epos   uint64
	esize  uint64
	mpos   uint64
	msize  uint64
	ebias  uint64
	norm   h5.Norm
	pad    h5.Pad
}

func h5tAtomicFloatOf(dt *h5.Datatype) h5tAtomicFloat {
	return h5tAtomicFloat{
		size: dt.Size, order: dt.Order, offset: uint64(dt.Offset), prec: uint64(dt.Precision),
		lsbPad: dt.LSBPad, msbPad: dt.MSBPad,
		sign: uint64(dt.Float.SignPos), epos: uint64(dt.Float.ExpPos), esize: uint64(dt.Float.ExpSize),
		mpos: uint64(dt.Float.MantPos), msize: uint64(dt.Float.MantSize), ebias: dt.Float.ExpBias,
		norm: dt.Float.Norm, pad: dt.Float.IntPad,
	}
}

// IEEE 754 binary32 / binary64 little-endian layouts (H5T_IEEE_F32LE,
// H5T_IEEE_F64LE).
var (
	h5tIEEEF32LE = h5tAtomicFloat{size: 4, order: h5.OrderLE, prec: 32, sign: 31, epos: 23, esize: 8, mpos: 0, msize: 23, ebias: 127, norm: h5.NormImplied}
	h5tIEEEF64LE = h5tAtomicFloat{size: 8, order: h5.OrderLE, prec: 64, sign: 63, epos: 52, esize: 11, mpos: 0, msize: 52, ebias: 1023, norm: h5.NormImplied}
)

// sameLayout reports whether a and b differ at most in byte order order
// (LE/BE), in which case the C library converts with H5T__conv_order.
func (a h5tAtomicFloat) sameLayout(b h5tAtomicFloat) bool {
	if a.order != h5.OrderLE && a.order != h5.OrderBE {
		return false
	}
	a.order = b.order
	return a == b
}

// h5tCompileFloat converts a floating-point type to float32 when it is an
// IEEE single, and to float64 otherwise (IEEE doubles are reinterpreted,
// other layouts are converted with H5T__conv_f_f).
func h5tCompileFloat(dt *h5.Datatype) (h5tConvFunc, error) {
	src := h5tAtomicFloatOf(dt)
	switch {
	case src.sameLayout(h5tIEEEF32LE):
		return func(elem []byte) (any, error) {
			return math.Float32frombits(le32(h5tLittleEndian(src.order, elem))), nil
		}, nil
	case src.sameLayout(h5tIEEEF64LE):
		return func(elem []byte) (any, error) {
			return math.Float64frombits(le64(h5tLittleEndian(src.order, elem))), nil
		}, nil
	}

	/* H5T_CONV_INIT checks */
	if src.order != h5.OrderLE && src.order != h5.OrderBE && src.order != h5.OrderVAX {
		return nil, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "unsupported byte order")
	}
	if 8*8-1 < src.esize {
		return nil, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "exponent field is too large")
	}
	/* The bit fields must lie within the element (checked by the datatype
	 * decoder; repeated here so the bit operations can't go out of range) */
	nbits := src.size * 8
	if src.sign >= nbits || src.esize == 0 || src.epos+src.esize > nbits || src.msize == 0 || src.mpos+src.msize > nbits {
		return nil, errorf(H5E_DATATYPE, H5E_BADVALUE, "invalid floating-point layout")
	}
	if src.order == h5.OrderVAX && src.size%2 != 0 {
		return nil, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "VAX order requires an even size")
	}

	dst := h5tIEEEF64LE
	st := &h5tConvFFState{}
	return func(elem []byte) (any, error) {
		d, err := h5tConvFF(&src, &dst, elem, st)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(le64(d)), nil
	}, nil
}

// h5tConvFFState holds the variables of H5T__conv_f_f that are carried from
// one element to the next.
type h5tConvFFState struct {
	msize uint64 /*useful size of mantissa in src*/
}

// h5tConvFF is the body of the H5T__conv_f_f conversion loop for one
// element: convert the floating-point value in elem (layout src) to the
// layout dst, returning the destination bytes. No user exception handler
// is installed, so the default actions apply.
func h5tConvFF(src, dst *h5tAtomicFloat, elem []byte, st *h5tConvFFState) ([]byte, error) {
	var (
		expo         int64 /*exponent			*/
		expoMax      = (int64(1) << dst.esize) - 1
		mpos         uint64 /*offset to useful mant is src	*/
		mrsh         uint64 /*amount to right shift mantissa*/
		carry        bool   /*carry after rounding mantissa	*/
		implied      uint64 /*destination implied bits	*/
		denormalized bool   /*is either source or destination denormalized?*/
		bitno        int64  /*bit number			*/
	)
	s := append([]byte(nil), elem...)
	d := make([]byte, dst.size)

	/*
	 * Put the data in little endian order so our loops aren't so
	 * complicated.  We'll do all the conversion stuff assuming
	 * little endian and then we'll fix the order at the end.
	 */
	if src.order == h5.OrderBE {
		halfSize := src.size / 2
		for i := uint64(0); i < halfSize; i++ {
			s[src.size-(i+1)], s[i] = s[i], s[src.size-(i+1)]
		}
	} else if src.order == h5.OrderVAX {
		tsize := src.size
		for i := uint64(0); i < tsize; i += 4 {
			tmp1 := s[i]
			tmp2 := s[i+1]

			s[i] = s[(tsize-2)-i]
			s[i+1] = s[(tsize-1)-i]

			s[(tsize-2)-i] = tmp1
			s[(tsize-1)-i] = tmp2
		}
	}

	/*
	 * Check for special cases: +0, -0, +Inf, -Inf, NaN
	 */
	if h5tBitFind(s, src.mpos, src.msize, h5tBitLSB, true) < 0 {
		if h5tBitFind(s, src.epos, src.esize, h5tBitLSB, true) < 0 {
			/* +0 or -0 */
			h5tBitCopy(d, dst.sign, s, src.sign, 1)
			h5tBitSet(d, dst.epos, dst.esize, false)
			h5tBitSet(d, dst.mpos, dst.msize, false)
			return h5tConvFFPadding(dst, d), nil
		} else if h5tBitFind(s, src.epos, src.esize, h5tBitLSB, false) < 0 {
			/* +Inf or -Inf */
			h5tBitCopy(d, dst.sign, s, src.sign, 1)
			h5tBitSet(d, dst.epos, dst.esize, true)
			h5tBitSet(d, dst.mpos, dst.msize, false)
			/*If the destination no implied mantissa bit, we'll need to set
			 *the 1st bit of mantissa to 1.  The Intel-Linux long double is
			 *this case.*/
			if dst.norm == h5.NormNone {
				h5tBitSet(d, dst.mpos+dst.msize-1, 1, true)
			}
			return h5tConvFFPadding(dst, d), nil
		}
	} else if src.norm == h5.NormNone && h5tBitFind(s, src.mpos, src.msize-1, h5tBitLSB, true) < 0 &&
		h5tBitFind(s, src.epos, src.esize, h5tBitLSB, false) < 0 {
		/*This is a special case for the source of no implied mantissa bit.
		 *If the exponent bits are all 1s and only the 1st bit of mantissa
		 *is set to 1.  It's infinity. The Intel-Linux "long double" is this case.*/
		/* +Inf or -Inf */
		h5tBitCopy(d, dst.sign, s, src.sign, 1)
		h5tBitSet(d, dst.epos, dst.esize, true)
		h5tBitSet(d, dst.mpos, dst.msize, false)
		if dst.norm == h5.NormNone {
			h5tBitSet(d, dst.mpos+dst.msize-1, 1, true)
		}
		return h5tConvFFPadding(dst, d), nil
	} else if h5tBitFind(s, src.epos, src.esize, h5tBitLSB, false) < 0 {
		/* NaN */
		/* There are many NaN values, so we just set all bits of
		 * the significand. */
		h5tBitCopy(d, dst.sign, s, src.sign, 1)
		h5tBitSet(d, dst.epos, dst.esize, true)
		h5tBitSet(d, dst.mpos, dst.msize, true)
		return h5tConvFFPadding(dst, d), nil
	}

	/*
	 * Get the exponent as an unsigned quantity from the section of
	 * the source bit field where it's located.     Don't worry about
	 * the exponent bias yet.
	 */
	expo = int64(h5tBitGetD(s, src.epos, src.esize))

	if expo == 0 {
		denormalized = true
	}

	/*
	 * Set markers for the source mantissa, excluding the leading `1'
	 * (might be implied).
	 */
	implied = 1
	mpos = src.mpos
	mrsh = 0
	if 0 == expo || src.norm == h5.NormNone {
		if bitno = h5tBitFind(s, src.mpos, src.msize, h5tBitMSB, true); bitno > 0 {
			st.msize = uint64(bitno)
		} else if 0 == bitno {
			st.msize = 1
			h5tBitSet(s, src.mpos, 1, false)
		}
	} else if src.norm == h5.NormImplied {
		st.msize = src.msize
	} else {
		return nil, errorf(H5E_DATATYPE, H5E_CANTCONVERT, "normalization method not implemented yet")
	}
	msize := st.msize

	/*
	 * The sign for the destination is the same as the sign for the
	 * source in all cases.
	 */
	h5tBitCopy(d, dst.sign, s, src.sign, 1)

	/*
	 * Calculate the true source exponent by adjusting according to
	 * the source exponent bias.
	 */
	if 0 == expo || src.norm == h5.NormNone {
		/* (size_t arithmetic, as in the C code) */
		expo -= int64((src.ebias - 1) + (src.msize - uint64(bitno)))
	} else if src.norm == h5.NormImplied {
		expo -= int64(src.ebias)
	} else {
		return nil, errorf(H5E_DATATYPE, H5E_CANTCONVERT, "normalization method not implemented yet")
	}

	/*
	 * If the destination is not normalized then right shift the
	 * mantissa by one.
	 */
	if dst.norm == h5.NormNone {
		mrsh++
	}

	/*
	 * Calculate the destination exponent by adding the destination
	 * bias and clipping by the minimum and maximum possible
	 * destination exponent values.
	 */
	expo += int64(dst.ebias)

	if expo < -int64(dst.msize) {
		/* The exponent is way too small.  Result is zero. */
		expo = 0
		h5tBitSet(d, dst.mpos, dst.msize, false)
		msize = 0
	} else if expo <= 0 {
		/*
		 * The exponent is too small to fit in the exponent field,
		 * but by shifting the mantissa to the right we can
		 * accommodate that value.  The mantissa of course is no
		 * longer normalized.
		 */
		mrsh += uint64(1 - expo)
		expo = 0
		denormalized = true
	} else if expo >= expoMax {
		/*
		 * The exponent is too large to fit in the available region
		 * or it results in the maximum possible value.     Use positive
		 * or negative infinity instead unless the application
		 * specifies something else.
		 */
		expo = expoMax
		h5tBitSet(d, dst.mpos, dst.msize, false)
		msize = 0
	}

	/*
	 * If the destination mantissa is smaller than the source
	 * mantissa then round the source mantissa. Rounding may cause a
	 * carry in which case the exponent has to be re-evaluated for
	 * overflow.  That is, if `carry' is clear then the implied
	 * mantissa bit is `1', else it is `10' binary.
	 */
	if msize > 0 && mrsh <= dst.msize && mrsh+msize > dst.msize {
		cut := mrsh + msize - dst.msize
		/* If the 1st bit being cut off is set and source isn't denormalized.*/
		if h5tBitGetD(s, (mpos+cut)-1, 1) != 0 && !denormalized {
			/* Don't do rounding if exponent is 111...110 and mantissa is 111...11.
			 * To do rounding and increment exponent in this case will create an infinity value.*/
			if h5tBitFind(s, mpos+cut, msize-cut, h5tBitLSB, false) >= 0 || expo < expoMax-1 {
				carry = h5tBitInc(s, mpos+cut-1, 1+msize-cut)
				if carry {
					implied = 2
				}
			}
		} else if h5tBitGetD(s, (mpos+cut)-1, 1) != 0 && denormalized {
			/* For either source or destination, denormalized value doesn't increment carry.*/
			h5tBitInc(s, mpos+cut-1, 1+msize-cut)
		}
	} else {
		carry = false
	}

	/*
	 * Write the mantissa to the destination
	 */
	if mrsh > dst.msize+1 {
		h5tBitSet(d, dst.mpos, dst.msize, false)
	} else if mrsh == dst.msize+1 {
		h5tBitSet(d, dst.mpos+1, dst.msize-1, false)
		h5tBitSet(d, dst.mpos, 1, true)
	} else if mrsh == dst.msize {
		h5tBitSet(d, dst.mpos, dst.msize, false)
		h5tBitSetD(d, dst.mpos, min(2, dst.msize), implied)
	} else {
		if mrsh > 0 {
			h5tBitSet(d, dst.mpos+dst.msize-mrsh, mrsh, false)
			h5tBitSetD(d, dst.mpos+dst.msize-mrsh, 2, implied)
		}
		if mrsh+msize >= dst.msize {
			h5tBitCopy(d, dst.mpos, s, mpos+msize+mrsh-dst.msize, dst.msize-mrsh)
		} else {
			h5tBitCopy(d, dst.mpos+dst.msize-(mrsh+msize), s, mpos, msize)
			h5tBitSet(d, dst.mpos, dst.msize-(mrsh+msize), false)
		}
	}

	/* Write the exponent */
	if carry {
		expo++
		if expo >= expoMax {
			/*
			 * The exponent is too large to fit in the available
			 * region or it results in the maximum possible value.
			 * Use positive or negative infinity instead unless the
			 * application specifies something else.
			 */
			expo = expoMax
			h5tBitSet(d, dst.mpos, dst.msize, false)
		}
	}

	h5tBitSetD(d, dst.epos, dst.esize, uint64(expo))
	return h5tConvFFPadding(dst, d), nil
}

// h5tConvFFPadding is the "padding:" tail of the H5T__conv_f_f loop: set
// the external padding areas and put the destination in its byte order.
func h5tConvFFPadding(dst *h5tAtomicFloat, d []byte) []byte {
	/*
	 * Set external padding areas
	 */
	if dst.offset > 0 {
		h5tBitSet(d, 0, dst.offset, dst.lsbPad == h5.PadOne)
	}
	if dst.offset+dst.prec != 8*dst.size {
		h5tBitSet(d, dst.offset+dst.prec, 8*dst.size-(dst.offset+dst.prec), dst.msbPad == h5.PadOne)
	}

	/*
	 * Put the destination in the correct byte order.  See note at
	 * beginning of loop.
	 */
	if dst.order == h5.OrderBE {
		halfSize := dst.size / 2
		for i := uint64(0); i < halfSize; i++ {
			d[dst.size-(i+1)], d[i] = d[i], d[dst.size-(i+1)]
		}
	}
	return d
}

/* ------------------------------------------------------------------------
 * Strings (H5T__conv_s_s)
 */

// h5tCompileString converts a fixed-length string, removing its padding:
// NullTerm stops at the first NUL, NullPad strips trailing NULs and
// SpacePad strips trailing spaces.
func h5tCompileString(dt *h5.Datatype) (h5tConvFunc, error) {
	switch dt.StrPad {
	case h5.StrNullTerm:
		return func(elem []byte) (any, error) {
			if i := bytes.IndexByte(elem, 0); i >= 0 {
				elem = elem[:i]
			}
			return string(elem), nil
		}, nil
	case h5.StrNullPad:
		return func(elem []byte) (any, error) {
			return string(bytes.TrimRight(elem, "\x00")), nil
		}, nil
	case h5.StrSpacePad:
		return func(elem []byte) (any, error) {
			return string(bytes.TrimRight(elem, " ")), nil
		}, nil
	}
	return nil, errorf(H5E_DATATYPE, H5E_UNSUPPORTED, "unsupported character padding %d", dt.StrPad)
}

/* ------------------------------------------------------------------------
 * Enumerations (H5T__conv_enum)
 */

// h5tCompileEnum converts an enumeration value to h5.Enum, looking up the
// member name by value ("" when the value is not a member).
func h5tCompileEnum(dt *h5.Datatype) (h5tConvFunc, error) {
	if dt.Base == nil {
		return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "enum datatype has no base type")
	}
	if len(dt.EnumNames) != len(dt.EnumValues) {
		return nil, errorf(H5E_DATATYPE, H5E_BADVALUE, "enum names and values do not match")
	}
	names := make(map[int64]string, len(dt.EnumValues))
	for i := len(dt.EnumValues) - 1; i >= 0; i-- {
		names[dt.EnumValues[i]] = dt.EnumNames[i]
	}
	base := dt.Base
	return func(elem []byte) (any, error) {
		v := h5tEnumRawValue(base, elem)
		return h5.Enum{Name: names[v], Value: v}, nil
	}, nil
}

/* ------------------------------------------------------------------------
 * Compounds and arrays (H5T__conv_struct, H5T__conv_array)
 */

// h5tCompileCompound converts a compound value field by field.
func h5tCompileCompound(f *File, dt *h5.Datatype) (h5tConvFunc, error) {
	type field struct {
		name   string
		offset uint64
		size   uint64
		conv   h5tConvFunc
	}
	fields := make([]field, len(dt.Members))
	for i, m := range dt.Members {
		if m.Type == nil {
			return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "compound member %q has no datatype", m.Name)
		}
		if m.Offset+m.Type.Size > dt.Size || m.Offset+m.Type.Size < m.Offset {
			return nil, errorf(H5E_DATATYPE, H5E_BADVALUE, "member type extends outside its parent compound type")
		}
		conv, err := h5tCompile(f, m.Type)
		if err != nil {
			return nil, err
		}
		fields[i] = field{name: m.Name, offset: m.Offset, size: m.Type.Size, conv: conv}
	}
	return func(elem []byte) (any, error) {
		c := h5.Compound{Fields: make([]h5.Field, len(fields))}
		for i, fl := range fields {
			v, err := fl.conv(elem[fl.offset : fl.offset+fl.size])
			if err != nil {
				return nil, err
			}
			c.Fields[i] = h5.Field{Name: fl.name, Value: v}
		}
		return c, nil
	}, nil
}

// h5tCompileArray converts an array value to a []any of NumElems base
// values in row-major order.
func h5tCompileArray(f *File, dt *h5.Datatype) (h5tConvFunc, error) {
	if dt.Base == nil {
		return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "array datatype has no base type")
	}
	nelem := uint64(1)
	for _, d := range dt.ArrayDims {
		hi, lo := bits.Mul64(nelem, d)
		if hi != 0 {
			return nil, errorf(H5E_DATATYPE, H5E_OVERFLOW, "array datatype size overflows")
		}
		nelem = lo
	}
	hi, total := bits.Mul64(nelem, dt.Base.Size)
	if hi != 0 || total > dt.Size {
		return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "array elements (%d x %d bytes) exceed the array size %d", nelem, dt.Base.Size, dt.Size)
	}
	conv, err := h5tCompile(f, dt.Base)
	if err != nil {
		return nil, err
	}
	bsize := dt.Base.Size
	return func(elem []byte) (any, error) {
		out := make([]any, nelem)
		for i := uint64(0); i < nelem; i++ {
			v, err := conv(elem[i*bsize : (i+1)*bsize])
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}, nil
}

/* ------------------------------------------------------------------------
 * Variable-length data (H5T__conv_vlen with the disk vlen class of
 * src/H5Tvlen.c and the native blob callbacks of src/H5VLnative_blob.c)
 */

// h5tVlenDiskSize is the size of a disk vlen element: the sequence length
// followed by a global heap ID.
func (f *File) h5tVlenDiskSize() uint64 { return 4 + uint64(f.h5hgHeapIDSize()) }

// h5tVlenDiskIsnull is H5T__vlen_disk_isnull (H5VL__native_blob_specific,
// H5VL_BLOB_ISNULL): a disk vlen is "nil" when its heap address is 0.
func (f *File) h5tVlenDiskIsnull(vl []byte) bool {
	/* Skip the sequence's length */
	p := f.newImage(vl[4:])
	/* Get the heap address */
	addr := p.addr()
	/* Check if heap address is 'nil' */
	return addr == 0
}

// h5tVlenDiskGetlen is H5T__vlen_disk_getlen: the number of elements of the
// sequence (different from the blob size).
func h5tVlenDiskGetlen(vl []byte) uint64 { return uint64(le32(vl)) }

// h5tVlenDiskRead is H5T__vlen_disk_read: retrieve the sequence's bytes
// (len bytes) from the global heap (H5VL__native_blob_get).
func (f *File) h5tVlenDiskRead(vl []byte, size uint64) ([]byte, error) {
	/* Skip the length of the sequence */
	return f.h5vlNativeBlobGet(vl[4:], size)
}

// h5vlNativeBlobGet is H5VL__native_blob_get: read the global heap object
// named by the blob ID id, which must be size bytes long.
func (f *File) h5vlNativeBlobGet(id []byte, size uint64) ([]byte, error) {
	/* Get the heap information */
	p := f.newImage(id)
	var hobjid h5hgT
	hobjid.addr = p.addr()
	hobjid.idx = p.uint32()
	if err := p.done(); err != nil {
		return nil, err
	}

	/* Check if this sequence actually has any data */
	if hobjid.addr > 0 {
		/* Verify the size is correct */
		hobjSize, err := f.hgGetObjSize(hobjid)
		if err != nil {
			return nil, wrapf(err, H5E_DATATYPE, H5E_CANTGET, "can't get object size")
		}
		if hobjSize != size {
			return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "Expected global heap object size does not match")
		}

		/* Read the VL information from disk */
		buf, err := f.hgRead(hobjid)
		if err != nil {
			return nil, wrapf(err, H5E_DATATYPE, H5E_READERROR, "unable to read VL information")
		}
		return buf, nil
	}
	return make([]byte, size), nil
}

// h5tCompileVlen converts a variable-length sequence to []any and a
// variable-length string to string (as a C string: up to the first NUL).
func h5tCompileVlen(f *File, dt *h5.Datatype) (h5tConvFunc, error) {
	if dt.Base == nil {
		return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "vlen datatype has no base type")
	}
	if dt.Size < f.h5tVlenDiskSize() {
		return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "vlen datatype size %d is too small for a disk sequence", dt.Size)
	}
	isString := dt.VlenKind == h5.VlenString
	var baseConv h5tConvFunc
	if !isString {
		var err error
		if baseConv, err = h5tCompile(f, dt.Base); err != nil {
			return nil, err
		}
	}
	baseSize := dt.Base.Size
	return func(s []byte) (any, error) {
		/* Check for "nil" source sequence */
		if f.h5tVlenDiskIsnull(s) {
			if isString {
				return "", nil
			}
			return []any{}, nil
		}

		/* Get length of element sequences */
		seqLen := h5tVlenDiskGetlen(s)
		hi, srcSize := bits.Mul64(seqLen, baseSize)
		if hi != 0 || srcSize > f.eof() {
			return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "bad sequence length")
		}

		/* Read in VL sequence */
		conv, err := f.h5tVlenDiskRead(s, srcSize)
		if err != nil {
			return nil, wrapf(err, H5E_DATATYPE, H5E_READERROR, "can't read VL data")
		}
		if isString {
			if i := bytes.IndexByte(conv, 0); i >= 0 {
				conv = conv[:i]
			}
			return string(conv), nil
		}
		out := make([]any, seqLen)
		for i := uint64(0); i < seqLen; i++ {
			v, err := baseConv(conv[i*baseSize : (i+1)*baseSize])
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}, nil
}

/* ------------------------------------------------------------------------
 * References (H5T__conv_ref with the disk reference classes of
 * src/H5Tref.c, and src/H5Rint.c)
 */

// H5R_IS_EXTERNAL: set when encoding a reference to an external file.
const h5rIsExternal = 0x1

// H5R_ENCODE_HEADER_SIZE: reference type and flags.
const h5rEncodeHeaderSize = 2

// H5O_MAX_TOKEN_SIZE (sizeof(H5O_token_t)).
const h5oMaxTokenSize = 16

// h5tCompileReference converts references: object references (H5R_OBJECT1)
// to h5.ObjectRef, dataset region references (H5R_DATASET_REGION1) to
// h5.RegionRef, and revised references (H5R_OBJECT2, H5R_DATASET_REGION2,
// H5R_ATTR) to h5.ObjectRef, h5.RegionRef or h5.AttrRef according to the
// type encoded in each element.
func h5tCompileReference(f *File, dt *h5.Datatype) (h5tConvFunc, error) {
	switch dt.RefType {
	case h5.RefObject1:
		if dt.Size < uint64(f.sizeofAddr) {
			return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "object reference size %d is too small", dt.Size)
		}
		return f.h5tRefObjDiskRead, nil
	case h5.RefRegion1:
		if dt.Size < uint64(f.h5hgHeapIDSize()) {
			return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "region reference size %d is too small", dt.Size)
		}
		return f.h5tRefDsetregDiskRead, nil
	case h5.RefObject2, h5.RefRegion2, h5.RefAttr:
		if dt.Size < h5rEncodeHeaderSize+4+uint64(f.h5hgHeapIDSize()) {
			return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "reference size %d is too small", dt.Size)
		}
		rtype := dt.RefType
		return func(elem []byte) (any, error) { return f.h5tRefDiskRead(rtype, elem) }, nil
	}
	return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "invalid reference type %d", dt.RefType)
}

// h5tRefObjDiskRead is H5T__ref_obj_disk_isnull + H5T__ref_obj_disk_read
// (H5R__decode_token_obj_compat): an object reference is the address of
// the object header. The "nil" reference has address 0; an undefined
// address (all bits set) is also reported as Undefined.
func (f *File) h5tRefObjDiskRead(elem []byte) (any, error) {
	p := f.newImage(elem)
	/* Get the object address */
	addr := p.addr()
	if err := p.done(); err != nil {
		return nil, err
	}
	/* Check if heap address is 'nil' */
	if addr == 0 {
		return h5.ObjectRef{Undefined: true}, nil
	}
	if !addrDefined(addr) {
		return h5.ObjectRef{Address: addr, Undefined: true}, nil
	}
	return h5.ObjectRef{Address: addr}, nil
}

// h5tRefDsetregDiskRead is H5T__ref_dsetreg_disk_isnull +
// H5T__ref_dsetreg_disk_read (H5R__decode_token_region_compat): a dataset
// region reference is a global heap ID; the heap object holds the
// dataset's object header address followed by the serialized selection.
func (f *File) h5tRefDsetregDiskRead(elem []byte) (any, error) {
	/* Check if heap address is 'nil' */
	if addr := f.newImage(elem).addr(); addr == 0 {
		return h5.RegionRef{Undefined: true}, nil
	}

	/* Read from heap */
	data, err := f.h5rDecodeHeap(elem)
	if err != nil {
		return nil, wrapf(err, H5E_ARGS, H5E_BADTYPE, "invalid location identifier")
	}

	/* Get object address */
	tokenSize := int(f.sizeofAddr)
	if len(data) < tokenSize {
		return nil, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Ran off end of buffer while deserializing")
	}
	p := f.newImage(data)
	addr := p.addr()
	ref := h5.RegionRef{Address: addr, Undefined: !addrDefined(addr)}

	/* Unserialize the selection */
	if p.remaining() <= 0 {
		return nil, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Ran off end of buffer while deserializing")
	}
	sel, err := h5sSelectDeserialize(f, f.newImage(p.rest()))
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			/* The selection decoder is not available: keep the address */
			return ref, nil
		}
		return nil, wrapf(err, H5E_REFERENCE, H5E_CANTDECODE, "can't deserialize selection")
	}
	ref.Selection, err = h5rSelectionPublic(sel, 0)
	if err != nil {
		return nil, err
	}
	return ref, nil
}

// h5rDecodeHeap is H5R__decode_heap: read the data a reference stores in
// the global heap.
func (f *File) h5rDecodeHeap(buf []byte) ([]byte, error) {
	bufSize := f.h5hgHeapIDSize()
	/* Don't decode if buffer size isn't big enough */
	if len(buf) < bufSize {
		return nil, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}

	/* Get the heap information */
	p := f.newImage(buf)
	var hobjid h5hgT
	hobjid.addr = p.addr()
	if !addrDefined(hobjid.addr) || hobjid.addr == 0 {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "Undefined reference pointer")
	}
	hobjid.idx = p.uint32()

	/* Read the information from disk */
	data, err := f.hgRead(hobjid)
	if err != nil {
		return nil, wrapf(err, H5E_REFERENCE, H5E_READERROR, "Unable to read reference data")
	}
	return data, nil
}

// h5tRefDiskRead is H5T__ref_disk_isnull + H5T__ref_disk_getsize +
// H5T__ref_disk_read followed by H5R__decode: decode a revised (HDF5 1.12+)
// reference stored on disk. Non-external object references are stored
// directly in the element; the other kinds store the header in the element
// and the rest of the encoded reference in the global heap.
func (f *File) h5tRefDiskRead(rtype h5.RefType, src []byte) (any, error) {
	/* --- H5T__ref_disk_isnull --- */
	/* Try to check encoded reference type */
	if src[0] == 0 {
		/* Skip the size / header and check if the blob ID is "nil" */
		p := f.newImage(src[h5rEncodeHeaderSize+4:])
		if p.addr() == 0 {
			switch rtype {
			case h5.RefRegion2:
				return h5.RegionRef{Undefined: true}, nil
			case h5.RefAttr:
				return h5.AttrRef{Undefined: true}, nil
			}
			return h5.ObjectRef{Undefined: true}, nil
		}
	}

	/* --- H5T__ref_disk_getsize --- */
	refType := int(int8(src[0]))
	if refType <= h5rBadtype || refType >= h5rMaxtype {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid reference type")
	}
	flags := src[1]

	var buf []byte
	if flags&h5rIsExternal == 0 && h5.RefType(refType) == h5.RefObject2 {
		/* Can do a direct copy and skip blob decoding */
		buf = src
	} else {
		/* Retrieve encoded data size */
		encSize := uint64(le32(src[h5rEncodeHeaderSize:]))
		if encSize > f.eof() {
			return nil, errorf(H5E_DATATYPE, H5E_BADSIZE, "invalid encoded reference size")
		}

		/* --- H5T__ref_disk_read --- */
		/* Copy header manually */
		buf = make([]byte, h5rEncodeHeaderSize, h5rEncodeHeaderSize+encSize)
		copy(buf, src[:h5rEncodeHeaderSize])

		/* Skip the size and retrieve blob */
		blob, err := f.h5vlNativeBlobGet(src[h5rEncodeHeaderSize+4:], encSize)
		if err != nil {
			return nil, wrapf(err, H5E_DATATYPE, H5E_CANTGET, "unable to get blob")
		}
		buf = append(buf, blob...)
	}
	return f.h5rDecode(buf)
}

// h5rDecode is H5R__decode: decode an encoded revised reference.
func (f *File) h5rDecode(buf []byte) (any, error) {
	/* Don't decode if buffer size isn't big enough */
	if len(buf) < h5rEncodeHeaderSize {
		return nil, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}

	/* Set new reference */
	refType := int(int8(buf[0]))
	if refType <= h5rBadtype || refType >= h5rMaxtype {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid reference type")
	}

	/* Set flags */
	flags := buf[1]
	p := buf[h5rEncodeHeaderSize:]

	/* Decode object token */
	token, n, err := h5rDecodeObjToken(p)
	if err != nil {
		return nil, wrapf(err, H5E_REFERENCE, H5E_CANTDECODE, "Cannot decode object address")
	}
	p = p[n:]
	addr := f.h5vlNativeTokenToAddr(token)
	undefined := !addrDefined(addr)

	/* We do not need to store the filename if the reference is internal */
	if flags&h5rIsExternal != 0 {
		/* Decode file name (external references are resolved by the
		 * caller; the name is not part of the decoded value) */
		_, n, err := h5rDecodeString(p)
		if err != nil {
			return nil, wrapf(err, H5E_REFERENCE, H5E_CANTDECODE, "Cannot decode filename")
		}
		p = p[n:]
	}

	switch h5.RefType(refType) {
	case h5.RefObject2:
		return h5.ObjectRef{Address: addr, Undefined: undefined}, nil

	case h5.RefRegion2:
		/* Decode dataspace */
		sel, err := h5rDecodeRegion(f, p)
		if err != nil {
			return nil, wrapf(err, H5E_REFERENCE, H5E_CANTDECODE, "Cannot decode region")
		}
		return h5.RegionRef{Address: addr, Undefined: undefined, Selection: sel}, nil

	case h5.RefAttr:
		/* Decode attribute name */
		name, _, err := h5rDecodeString(p)
		if err != nil {
			return nil, wrapf(err, H5E_REFERENCE, H5E_CANTDECODE, "Cannot decode attribute name")
		}
		return h5.AttrRef{Address: addr, Undefined: undefined, Name: name}, nil
	}
	return nil, errorf(H5E_REFERENCE, H5E_UNSUPPORTED, "internal error (invalid reference type)")
}

// h5rDecodeObjToken is H5R__decode_obj_token: decode an object token (a
// size byte followed by the token). It returns the token and the number of
// bytes consumed.
func h5rDecodeObjToken(buf []byte) ([h5oMaxTokenSize]byte, int, error) {
	var token [h5oMaxTokenSize]byte

	/* Don't decode if buffer size isn't big enough */
	if len(buf) < 1 {
		return token, 0, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}

	/* Get token size */
	tokenSize := int(buf[0])
	if tokenSize > h5oMaxTokenSize {
		return token, 0, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Invalid token size (%d)", tokenSize)
	}
	if len(buf) < 1+tokenSize {
		return token, 0, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}

	/* Decode token */
	copy(token[:], buf[1:1+tokenSize])
	return token, 1 + tokenSize, nil
}

// h5vlNativeTokenToAddr is H5VL_native_token_to_addr: the address encoded
// in a native object token.
func (f *File) h5vlNativeTokenToAddr(token [h5oMaxTokenSize]byte) haddr {
	n := min(int(f.sizeofAddr), h5oMaxTokenSize)
	return f.newImage(token[:]).addrLen(n)
}

// h5rDecodeString is H5R__decode_string: decode a string stored as a
// 16-bit length followed by the characters. It returns the string and the
// number of bytes consumed.
func h5rDecodeString(buf []byte) (string, int, error) {
	/* Don't decode if buffer size isn't big enough */
	if len(buf) < 2 {
		return "", 0, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}

	/* Get the string length */
	stringLen := int(le16(buf))
	if len(buf) < 2+stringLen {
		return "", 0, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}

	/* Copy the string */
	return string(buf[2 : 2+stringLen]), 2 + stringLen, nil
}

// h5rDecodeRegion is H5R__decode_region: decode a selection stored as its
// serialized size, the extent rank and the serialized selection.
func h5rDecodeRegion(f *File, buf []byte) (h5.Selection, error) {
	/* Don't decode if buffer size isn't big enough */
	if len(buf) < 2*4 {
		return h5.Selection{}, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}

	/* Decode the selection size */
	bufSize := uint64(le32(buf)) + 4

	/* Decode the extent rank */
	rank := le32(buf[4:])
	bufSize += 4

	/* Don't decode if buffer size isn't big enough */
	if uint64(len(buf)) < bufSize {
		return h5.Selection{}, errorf(H5E_REFERENCE, H5E_CANTDECODE, "Buffer size is too small")
	}
	if rank > h5sMaxRank {
		return h5.Selection{}, errorf(H5E_REFERENCE, H5E_CANTSET, "can't set extent rank for selection")
	}

	/* Deserialize the selection (dataspaces need the extent rank information) */
	sel, err := h5sSelectDeserialize(f, f.newImage(buf[8:]))
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			/* The selection decoder is not available */
			return h5.Selection{}, nil
		}
		return h5.Selection{}, wrapf(err, H5E_REFERENCE, H5E_CANTDECODE, "can't deserialize selection")
	}
	return h5rSelectionPublic(sel, rank)
}

// h5rMaxSelectionBlocks bounds the number of blocks a regular hyperslab
// selection is expanded to.
const h5rMaxSelectionBlocks = 1 << 24

// h5rSelectionPublic converts a decoded selection to the shared vocabulary
// (h5.Selection). Regular hyperslabs are expanded into their blocks in
// row-major order. rank is the extent rank when known (0 otherwise).
func h5rSelectionPublic(sel *h5sSelection, rank uint32) (h5.Selection, error) {
	if sel == nil {
		return h5.Selection{Type: h5.SelAll, Rank: int(rank)}, nil
	}
	out := h5.Selection{Rank: int(sel.rank)}
	if out.Rank == 0 {
		out.Rank = int(rank)
	}
	switch sel.typ {
	case h5sSelNone:
		out.Type = h5.SelNone
	case h5sSelAll:
		out.Type = h5.SelAll
	case h5sSelPoints:
		out.Type = h5.SelPoints
		out.Points = make([][]uint64, len(sel.points))
		for i, pt := range sel.points {
			out.Points[i] = append([]uint64(nil), pt...)
		}
	case h5sSelHyperslabs:
		out.Type = h5.SelHyperslab
		if !sel.regular {
			out.Blocks = make([][2][]uint64, len(sel.blocks))
			for i, b := range sel.blocks {
				out.Blocks[i] = [2][]uint64{append([]uint64(nil), b[0]...), append([]uint64(nil), b[1]...)}
			}
			break
		}
		nd := len(sel.start)
		if len(sel.count) != nd || (sel.stride != nil && len(sel.stride) != nd) || (sel.block != nil && len(sel.block) != nd) {
			return h5.Selection{}, errorf(H5E_DATASPACE, H5E_BADVALUE, "inconsistent hyperslab selection")
		}
		total := uint64(1)
		for _, c := range sel.count {
			hi, lo := bits.Mul64(total, c)
			if hi != 0 || lo > h5rMaxSelectionBlocks {
				return h5.Selection{}, errorf(H5E_DATASPACE, H5E_BADRANGE, "too many blocks in hyperslab selection")
			}
			total = lo
		}
		idx := make([]uint64, nd)
		for n := uint64(0); n < total; n++ {
			start := make([]uint64, nd)
			end := make([]uint64, nd)
			for d := 0; d < nd; d++ {
				stride, block := uint64(1), uint64(1)
				if sel.stride != nil {
					stride = sel.stride[d]
				}
				if sel.block != nil {
					block = sel.block[d]
				}
				start[d] = sel.start[d] + idx[d]*stride
				end[d] = start[d] + block - 1
			}
			out.Blocks = append(out.Blocks, [2][]uint64{start, end})
			/* Advance the row-major block index */
			for d := nd - 1; d >= 0; d-- {
				idx[d]++
				if idx[d] < sel.count[d] {
					break
				}
				idx[d] = 0
			}
		}
	default:
		return h5.Selection{}, errorf(H5E_DATASPACE, H5E_BADVALUE, "invalid selection type %d", sel.typ)
	}
	return out, nil
}
