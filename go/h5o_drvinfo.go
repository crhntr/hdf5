package hdf5

// Translated from src/H5Odrvinfo.c (decode only): the driver info message
// (superblock extension).

// h5oDrvinfoVersion is H5O_DRVINFO_VERSION: the current version of the
// message.
const h5oDrvinfoVersion = 0

// h5oDrvinfoDecode is H5O__drvinfo_decode: decode a driver info message and
// return a pointer to a new native message struct.
func h5oDrvinfoDecode(f *File, p *image) (*h5oDrvinfo, error) {
	/* Version of message */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	if p.uint8() != h5oDrvinfoVersion {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Allocate space for message */
	mesg := &h5oDrvinfo{}

	/* Retrieve driver name */
	if p.overflow(8) {
		return nil, errDecodeOverflow()
	}
	mesg.name = string(p.bytes(8))

	/* Decode buffer size */
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	n := int(p.uint16())
	if 0 == n {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "message length can't be zero")
	}

	/* Copy encoded driver info into buffer */
	if p.overflow(n) {
		return nil, errDecodeOverflow()
	}
	mesg.buf = append([]byte(nil), p.bytes(n)...)
	return mesg, nil
}
