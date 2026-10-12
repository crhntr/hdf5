package hdf5

// Translated from src/H5Oname.c (decode only): the object comment message.

// h5oNameDecode is H5O__name_decode: decode a name (comment) message.
func h5oNameDecode(f *File, p *image) (h5oName, error) {
	/* (H5MM_strndup(p, p_size - 1): the string ends at the first NUL or
	 * before the last byte of the message) */
	n := p.remaining()
	if n < 1 {
		return "", errDecodeOverflow()
	}
	s := p.stringN(n - 1)
	return h5oName(s), nil
}
