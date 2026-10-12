package hdf5

// Translated from src/H5Ocont.c (decode only): the object header
// continuation message.

// h5oContDecode is H5O__cont_decode: decode the raw header continuation
// message.
func h5oContDecode(f *File, p *image) (*h5oCont, error) {
	/* Allocate space for the message */
	cont := &h5oCont{}

	/* Decode */
	if p.overflow(int(f.sizeofAddr)) {
		return nil, errDecodeOverflow()
	}
	cont.addr = p.addr()
	if p.overflow(int(f.sizeofSize)) {
		return nil, errDecodeOverflow()
	}
	cont.size = p.length()
	return cont, nil
}
