package hdf5

// Translated from src/H5Obtreek.c (decode only): the v1 B-tree 'K' values
// message (superblock extension).

// h5oBtreekVersion is H5O_BTREEK_VERSION: the current version of the
// message.
const h5oBtreekVersion = 0

// h5oBtreekDecode is H5O__btreek_decode: decode a shared message table
// message and return a pointer to a new native message struct.
func h5oBtreekDecode(f *File, p *image) (*h5oBtreek, error) {
	/* Version of message */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	if p.uint8() != h5oBtreekVersion {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Allocate space for message */
	mesg := &h5oBtreek{}

	/* Retrieve non-default B-tree 'K' values */
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	mesg.btreeK[h5bChunkID] = uint(p.uint16())
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	mesg.btreeK[h5bSnodeID] = uint(p.uint16())
	if p.overflow(2) {
		return nil, errDecodeOverflow()
	}
	mesg.symLeafK = uint(p.uint16())
	return mesg, nil
}
