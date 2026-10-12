package hdf5

// Translated from src/H5Oshmesg.c (decode only): the shared message table
// message (superblock extension).

// h5oShmesgDecode is H5O__shmesg_decode: decode a shared message table
// message.
func h5oShmesgDecode(f *File, p *image) (*h5oShmesgTable, error) {
	mesg := &h5oShmesgTable{}

	/* Retrieve version, table address, and number of indexes */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	mesg.version = p.uint8()
	if p.overflow(int(f.sizeofAddr)) {
		return nil, errDecodeOverflow()
	}
	mesg.addr = p.addr()
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	mesg.nindexes = p.uint8()
	return mesg, nil
}
