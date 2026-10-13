package hdf5

// Translated from src/H5Znbit.c (decompression direction only).
//
// The cd_values layout (written by H5Z__set_local_nbit, see H5Z__set_parms_*)
// is:
//
//	cd_values[0]  total number of cd_values
//	cd_values[1]  1 if no compression was needed (all types full precision)
//	cd_values[2]  number of elements in the chunk
//	cd_values[3..] description of the datatype:
//	    atomic:    H5Z_NBIT_ATOMIC, size, order, precision, offset
//	    array:     H5Z_NBIT_ARRAY, total size, <description of the base type>
//	    compound:  H5Z_NBIT_COMPOUND, size, nmembers, then for every member
//	               its byte offset in the compound followed by its description
//	    no-op:     H5Z_NBIT_NOOPTYPE, size
//
// The H5Z__calc_parms_* / H5Z__set_parms_* / can_apply / set_local functions
// run at dataset creation time (they need a datatype and a property list) and
// are not needed to interpret the cd_values stored in the file.
//
// The C decompressor reads cd_values and the compressed buffer without bounds
// checks. Here every access is checked: a corrupt parameter block or a
// truncated chunk makes the filter fail instead of reading out of range.

// H5Z_NBIT_* local macros.
const (
	h5zNbitAtomic   = 1    /* Atomic datatype class: integer/floating-point */
	h5zNbitArray    = 2    /* Array datatype class */
	h5zNbitCompound = 3    /* Compound datatype class */
	h5zNbitNooptype = 4    /* Other datatype class: nbit does no compression */
	h5zNbitMaxNParm = 4096 /* Max number of parameters for filter */
	h5zNbitOrderLE  = 0    /* Little endian for datatype byte order */
	h5zNbitOrderBE  = 1    /* Big endian for datatype byte order */
)

// h5zNbitParmsAtomic is parms_atomic: the parameters needed for
// compressing/decompressing one nbit atomic datatype (integer or
// floating-point).
type h5zNbitParmsAtomic struct {
	size      uint32 /* size of datatype */
	order     uint32 /* datatype endianness order */
	precision uint32 /* datatype precision */
	offset    uint32 /* datatype offset */
}

// h5zNbitState carries the arguments that the C decompression functions pass
// by pointer: the output data, the compressed buffer with the current byte
// index j and the number of bits still unused in the current byte (buf_len),
// the parameter array with its current index (parms_index), and the first
// error encountered.
type h5zNbitState struct {
	data   []byte
	buffer []byte
	j      int
	bufLen uint
	parms  []uint32
	pi     int
	err    error
}

func (s *h5zNbitState) fail(major errMajor, minor errMinor, format string, args ...any) {
	if s.err == nil {
		s.err = errorf(major, minor, format, args...)
	}
}

// peek is parms[*parms_index].
func (s *h5zNbitState) peek() uint32 {
	if s.pi < 0 || s.pi >= len(s.parms) {
		s.fail(H5E_PLINE, H5E_CANTFILTER, "nbit parameters are truncated")
		return 0
	}
	return s.parms[s.pi]
}

// next is parms[(*parms_index)++].
func (s *h5zNbitState) next() uint32 {
	v := s.peek()
	s.pi++
	return v
}

// bufByte is buffer[*j].
func (s *h5zNbitState) bufByte() byte {
	if s.j < 0 || s.j >= len(s.buffer) {
		s.fail(H5E_PLINE, H5E_CANTFILTER, "nbit compressed data is truncated")
		return 0
	}
	return s.buffer[s.j]
}

// setData is data[idx] = v (or data[idx] |= v if or is set).
func (s *h5zNbitState) setData(idx int, v byte, or bool) {
	if idx < 0 || idx >= len(s.data) {
		s.fail(H5E_PLINE, H5E_CANTFILTER, "nbit decompressed data does not fit the datatype size")
		return
	}
	if or {
		s.data[idx] |= v
	} else {
		s.data[idx] = v
	}
}

// h5zNbitNextByte is H5Z__nbit_next_byte.
func h5zNbitNextByte(s *h5zNbitState) {
	s.j++
	s.bufLen = 8
}

// h5zNbitDecompressOneByte is H5Z__nbit_decompress_one_byte.
func h5zNbitDecompressOneByte(s *h5zNbitState, dataOffset int, k, beginI, endI uint32, p *h5zNbitParmsAtomic, datatypeLen uint) {
	var datLen uint /* dat_len is the number of bits to be copied in each data byte */
	var datOffset uint

	/* initialize value and bits of unsigned char to be copied */
	val := uint32(s.bufByte())
	if s.err != nil {
		return
	}

	if beginI != endI { /* significant bits occupy >1 unsigned char */
		if k == beginI {
			datLen = 8 - (datatypeLen-uint(p.precision)-uint(p.offset))%8
		} else if k == endI {
			datLen = 8 - uint(p.offset)%8
			datOffset = 8 - datLen
		} else {
			datLen = 8
		}
	} else { /* all significant bits in one unsigned char */
		datOffset = uint(p.offset) % 8
		datLen = uint(p.precision)
	}

	if s.bufLen > datLen {
		s.setData(dataOffset+int(k), byte(((val>>(s.bufLen-datLen))&^(^uint32(0)<<datLen))<<datOffset), false)
		s.bufLen -= datLen
	} else {
		s.setData(dataOffset+int(k), byte(((val&^(^uint32(0)<<s.bufLen))<<(datLen-s.bufLen))<<datOffset), false)
		datLen -= s.bufLen
		h5zNbitNextByte(s)
		if datLen == 0 {
			return
		}

		val = uint32(s.bufByte())
		if s.err != nil {
			return
		}
		s.setData(dataOffset+int(k), byte(((val>>(s.bufLen-datLen))&^(^uint32(0)<<datLen))<<datOffset), true)
		s.bufLen -= datLen
	}
}

// h5zNbitDecompressOneNooptype is H5Z__nbit_decompress_one_nooptype.
func h5zNbitDecompressOneNooptype(s *h5zNbitState, dataOffset int, size uint32) {
	for i := 0; i < int(size) && s.err == nil; i++ {
		/* initialize value and bits of unsigned char to be copied */
		val := uint32(s.bufByte())
		if s.err != nil {
			return
		}
		datLen := uint(8)

		s.setData(dataOffset+i, byte((val&^(^uint32(0)<<s.bufLen))<<(datLen-s.bufLen)), false)
		datLen -= s.bufLen
		h5zNbitNextByte(s)
		if datLen == 0 {
			continue
		}

		val = uint32(s.bufByte())
		if s.err != nil {
			return
		}
		s.setData(dataOffset+i, byte((val>>(s.bufLen-datLen))&^(^uint32(0)<<datLen)), true)
		s.bufLen -= datLen
	}
}

// h5zNbitDecompressOneAtomic is H5Z__nbit_decompress_one_atomic.
func h5zNbitDecompressOneAtomic(s *h5zNbitState, dataOffset int, p *h5zNbitParmsAtomic) {
	/* begin_i: the index of byte having first significant bit
	   end_i: the index of byte having last significant bit */
	var beginI, endI uint32

	datatypeLen := uint(p.size * 8)

	if p.order == h5zNbitOrderLE { /* little endian */
		/* calculate begin_i and end_i */
		if (p.precision+p.offset)%8 != 0 {
			beginI = (p.precision + p.offset) / 8
		} else {
			beginI = (p.precision+p.offset)/8 - 1
		}
		endI = p.offset / 8

		for k := int(int32(beginI)); k >= int(int32(endI)) && s.err == nil; k-- {
			h5zNbitDecompressOneByte(s, dataOffset, uint32(k), beginI, endI, p, datatypeLen)
		}
	} else { /* big endian */
		/* calculate begin_i and end_i */
		beginI = (uint32(datatypeLen) - p.precision - p.offset) / 8
		if p.offset%8 != 0 {
			endI = (uint32(datatypeLen) - p.offset) / 8
		} else {
			endI = (uint32(datatypeLen)-p.offset)/8 - 1
		}

		for k := int(int32(beginI)); k <= int(int32(endI)) && s.err == nil; k++ {
			h5zNbitDecompressOneByte(s, dataOffset, uint32(k), beginI, endI, p, datatypeLen)
		}
	}
}

// h5zNbitCheckAtomicParms is the "Check values of precision and offset" test.
func h5zNbitCheckAtomicParms(s *h5zNbitState, p *h5zNbitParmsAtomic) {
	if p.precision > p.size*8 || (p.precision+p.offset) > p.size*8 {
		s.fail(H5E_PLINE, H5E_BADTYPE, "invalid datatype precision/offset")
	}
}

// h5zNbitDecompressOneArray is H5Z__nbit_decompress_one_array.
//
// As in the C code, the parameter index is rewound to the start of the base
// type description after every element of an array of arrays/compounds, so an
// array-of-array or array-of-compound member that is followed by further
// compound members is not parsed correctly (the library has the same
// limitation when it compresses such data); the supported shapes are the ones
// the C test-suite exercises.
func h5zNbitDecompressOneArray(s *h5zNbitState, dataOffset int) {
	totalSize := s.next()
	baseClass := s.next()
	if s.err != nil {
		return
	}

	switch baseClass {
	case h5zNbitAtomic:
		var p h5zNbitParmsAtomic
		p.size = s.next()
		p.order = s.next()
		p.precision = s.next()
		p.offset = s.next()
		if s.err != nil {
			return
		}

		/* Check values of precision and offset */
		h5zNbitCheckAtomicParms(s, &p)
		if s.err != nil {
			return
		}
		if p.size == 0 {
			s.fail(H5E_PLINE, H5E_BADTYPE, "invalid datatype size")
			return
		}

		n := totalSize / p.size
		for i := uint32(0); i < n && s.err == nil; i++ {
			h5zNbitDecompressOneAtomic(s, dataOffset+int(i)*int(p.size), &p)
		}

	case h5zNbitArray:
		baseSize := s.peek() /* read in advance */
		if s.err != nil {
			return
		}
		if baseSize == 0 {
			s.fail(H5E_PLINE, H5E_BADTYPE, "invalid datatype size")
			return
		}
		n := totalSize / baseSize /* number of base_type elements inside the array datatype */
		beginIndex := s.pi
		for i := uint32(0); i < n; i++ {
			h5zNbitDecompressOneArray(s, dataOffset+int(i)*int(baseSize))
			if s.err != nil {
				s.err = wrapf(s.err, H5E_PLINE, H5E_CANTFILTER, "can't decompress array")
				return
			}
			s.pi = beginIndex
		}

	case h5zNbitCompound:
		baseSize := s.peek() /* read in advance */
		if s.err != nil {
			return
		}
		if baseSize == 0 {
			s.fail(H5E_PLINE, H5E_BADTYPE, "invalid datatype size")
			return
		}
		n := totalSize / baseSize /* number of base_type elements inside the array datatype */
		beginIndex := s.pi
		for i := uint32(0); i < n; i++ {
			h5zNbitDecompressOneCompound(s, dataOffset+int(i)*int(baseSize))
			if s.err != nil {
				s.err = wrapf(s.err, H5E_PLINE, H5E_CANTFILTER, "can't decompress compound")
				return
			}
			s.pi = beginIndex
		}

	case h5zNbitNooptype:
		s.pi++ /* skip size of no-op type */
		h5zNbitDecompressOneNooptype(s, dataOffset, totalSize)

	default:
		s.fail(H5E_PLINE, H5E_BADTYPE, "unknown nbit datatype class %d", baseClass)
	}
}

// h5zNbitDecompressOneCompound is H5Z__nbit_decompress_one_compound.
func h5zNbitDecompressOneCompound(s *h5zNbitState, dataOffset int) {
	var usedSize uint32

	size := s.next()
	nmembers := s.next()

	for i := uint32(0); i < nmembers && s.err == nil; i++ {
		memberOffset := s.next()
		memberClass := s.next()

		/* Check for overflow */
		memberSize := s.peek()
		if s.err != nil {
			return
		}
		prevUsedSize := usedSize
		usedSize += memberSize
		if usedSize > size {
			s.fail(H5E_PLINE, H5E_BADVALUE, "compound member size overflowed compound size")
			return
		}
		if usedSize <= prevUsedSize {
			s.fail(H5E_PLINE, H5E_BADVALUE, "compound member size overflowed compound size")
			return
		}
		if memberOffset+memberSize > size {
			s.fail(H5E_PLINE, H5E_BADRANGE, "compound member offset overflowed compound size")
			return
		}
		switch memberClass {
		case h5zNbitAtomic:
			var p h5zNbitParmsAtomic
			p.size = memberSize
			/* Advance past member size */
			s.pi++
			p.order = s.next()
			p.precision = s.next()
			p.offset = s.next()
			if s.err != nil {
				return
			}

			/* Check values of precision and offset */
			h5zNbitCheckAtomicParms(s, &p)
			if s.err != nil {
				return
			}

			h5zNbitDecompressOneAtomic(s, dataOffset+int(memberOffset), &p)

		case h5zNbitArray:
			h5zNbitDecompressOneArray(s, dataOffset+int(memberOffset))
			if s.err != nil {
				s.err = wrapf(s.err, H5E_PLINE, H5E_CANTFILTER, "can't decompress array")
				return
			}

		case h5zNbitCompound:
			h5zNbitDecompressOneCompound(s, dataOffset+int(memberOffset))
			if s.err != nil {
				s.err = wrapf(s.err, H5E_PLINE, H5E_CANTFILTER, "can't decompress compound")
				return
			}

		case h5zNbitNooptype:
			/* Advance past member size */
			s.pi++
			h5zNbitDecompressOneNooptype(s, dataOffset+int(memberOffset), memberSize)

		default:
			s.fail(H5E_PLINE, H5E_BADTYPE, "unknown nbit datatype class %d", memberClass)
		}
	}
}

// h5zNbitDecompress is H5Z__nbit_decompress: decompress dNelmts elements of
// the datatype described by parms[3:] from buffer into a new data array of
// dNelmts*parms[4] bytes.
func h5zNbitDecompress(dNelmts uint32, buffer []byte, parms []uint32) ([]byte, error) {
	/* may not have to initialize to zeros */
	sizeOut := uint64(dNelmts) * uint64(parms[4])
	if sizeOut > 1<<32-1 {
		return nil, errorf(H5E_RESOURCE, H5E_CANTALLOC, "memory allocation failed for nbit decompression")
	}
	s := &h5zNbitState{
		data:   make([]byte, sizeOut),
		buffer: buffer,
		j:      0,
		bufLen: 8, /* initialization before the loop */
		parms:  parms,
	}

	switch parms[3] {
	case h5zNbitAtomic:
		var p h5zNbitParmsAtomic
		p.size = parms[4]
		p.order = parms[5]
		p.precision = parms[6]
		p.offset = parms[7]

		/* Check values of precision and offset */
		h5zNbitCheckAtomicParms(s, &p)
		if s.err != nil {
			return nil, s.err
		}

		for i := uint32(0); i < dNelmts && s.err == nil; i++ {
			h5zNbitDecompressOneAtomic(s, int(i)*int(p.size), &p)
		}

	case h5zNbitArray:
		size := int(parms[4])
		s.pi = 4 /* set the index before goto function call */
		for i := uint32(0); i < dNelmts; i++ {
			h5zNbitDecompressOneArray(s, int(i)*size)
			if s.err != nil {
				return nil, wrapf(s.err, H5E_PLINE, H5E_CANTFILTER, "can't decompress array")
			}
			s.pi = 4
		}

	case h5zNbitCompound:
		size := int(parms[4])
		s.pi = 4 /* set the index before goto function call */
		for i := uint32(0); i < dNelmts; i++ {
			h5zNbitDecompressOneCompound(s, int(i)*size)
			if s.err != nil {
				return nil, wrapf(s.err, H5E_PLINE, H5E_CANTFILTER, "can't decompress compound")
			}
			s.pi = 4
		}

	default:
		s.fail(H5E_PLINE, H5E_BADTYPE, "unknown nbit datatype class %d", parms[3])
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.data, nil
}

// h5zFilterNbitFunc is H5Z__filter_nbit with H5Z_FLAG_REVERSE.
func h5zFilterNbitFunc(flags uint, cdValues []uint32, buf []byte) ([]byte, error) {
	/* check arguments
	 * cd_values[0] stores actual number of parameters in cd_values[]
	 */
	if len(cdValues) == 0 || uint64(len(cdValues)) != uint64(cdValues[0]) {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid nbit aggression level")
	}

	/* check if need to do nbit compress or decompress
	 * cd_values[1] stores the flag if true indicating no need to compress
	 */
	/* (The C code reads cd_values[1], and then cd_values[2..7], whatever
	 * the number of parameters: fewer than these are refused here. The
	 * number of parameters is not bounded otherwise: H5Z_NBIT_MAX_NPARMS
	 * only bounds the parameters H5Z__set_local_nbit writes.) */
	if len(cdValues) < 2 {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid nbit number of parameters")
	}
	if cdValues[1] != 0 {
		return buf, nil
	}
	if len(cdValues) < 8 {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid nbit number of parameters")
	}
	if flags&h5zFlagReverse == 0 {
		return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED, "nbit is only supported in the read direction")
	}

	/* copy a filter parameter to d_nelmts */
	dNelmts := cdValues[2]

	/* input; decompress */
	/* cd_values[4] stores datatype size */
	out, err := h5zNbitDecompress(dNelmts, buf, cdValues)
	if err != nil {
		return nil, wrapf(err, H5E_PLINE, H5E_CANTFILTER, "can't decompress buffer")
	}
	return out, nil
}
