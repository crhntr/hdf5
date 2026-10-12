package hdf5

// Translated from src/H5Ostab.c (decode only): the symbol table message.

// h5oStabDecode is H5O__stab_decode: decode a symbol table message.
func h5oStabDecode(f *File, p *image) (*h5oStab, error) {
	stab := &h5oStab{}

	if p.overflow(int(f.sizeofAddr)) {
		return nil, errDecodeOverflow()
	}
	stab.btreeAddr = p.addr()
	if p.overflow(int(f.sizeofAddr)) {
		return nil, errDecodeOverflow()
	}
	stab.heapAddr = p.addr()
	return stab, nil
}
