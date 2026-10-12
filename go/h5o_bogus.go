package hdf5

// Translated from src/H5Obogus.c (decode only): the "bogus" test messages.
// The classes are only registered in H5O_msg_class_g when the C library is
// built with H5O_ENABLE_BOGUS (not the default), so files containing bogus
// messages see them as unknown messages; the decoder is kept for
// completeness.

// h5oBogusValue is H5O_BOGUS_VALUE.
const h5oBogusValue = 0xdeadbeef

// h5oBogusDecode is H5O__bogus_decode: decode a "bogus" message.
func h5oBogusDecode(f *File, p *image) (*h5oBogus, error) {
	/* Allocate the bogus message */
	mesg := &h5oBogus{}

	if p.overflow(4) {
		return nil, errDecodeOverflow()
	}
	mesg.u = p.uint32()

	/* Validate the bogus info */
	if mesg.u != h5oBogusValue {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "invalid bogus value :-)")
	}
	return mesg, nil
}
