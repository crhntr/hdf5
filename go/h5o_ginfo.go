package hdf5

// Translated from src/H5Oginfo.c (decode only): the group info message.

/* This is the initial version of the message */
const h5oGinfoVersion = 0

/* Flags for group flag encoding */
const (
	h5oGinfoStorePhaseChange  = 0x01
	h5oGinfoStoreEstEntryInfo = 0x02
	h5oGinfoAllFlags          = h5oGinfoStorePhaseChange | h5oGinfoStoreEstEntryInfo
)

// h5oGinfoDecode is H5O__ginfo_decode: decode a group info message.
func h5oGinfoDecode(f *File, p *image) (*h5oGinfo, error) {
	// Default group creation property values (H5G_CRT_GINFO_* in
	// H5Gprivate.h; local here so they cannot clash with the group module).
	const (
		h5gCrtGinfoMaxCompact    = 8
		h5gCrtGinfoMinDense      = 6
		h5gCrtGinfoEstNumEntries = 4
		h5gCrtGinfoEstNameLen    = 8
	)

	/* Version of message */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	if p.uint8() != h5oGinfoVersion {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for message")
	}

	/* Allocate space for message */
	ginfo := &h5oGinfo{}

	/* Get the flags for the group */
	if p.overflow(1) {
		return nil, errDecodeOverflow()
	}
	flags := p.uint8()
	if flags&^h5oGinfoAllFlags != 0 {
		return nil, errorf(H5E_OHDR, H5E_CANTLOAD, "bad flag value for message")
	}
	ginfo.storeLinkPhaseChange = flags&h5oGinfoStorePhaseChange != 0
	ginfo.storeEstEntryInfo = flags&h5oGinfoStoreEstEntryInfo != 0

	/* Get the max. # of links to store compactly & the min. # of links to store densely */
	if ginfo.storeLinkPhaseChange {
		if p.overflow(2 * 2) {
			return nil, errDecodeOverflow()
		}
		ginfo.maxCompact = p.uint16()
		ginfo.minDense = p.uint16()
	} else {
		ginfo.maxCompact = h5gCrtGinfoMaxCompact
		ginfo.minDense = h5gCrtGinfoMinDense
	}

	/* Get the estimated # of entries & name lengths */
	if ginfo.storeEstEntryInfo {
		if p.overflow(2 * 2) {
			return nil, errDecodeOverflow()
		}
		ginfo.estNumEntries = p.uint16()
		ginfo.estNameLen = p.uint16()
	} else {
		ginfo.estNumEntries = h5gCrtGinfoEstNumEntries
		ginfo.estNameLen = h5gCrtGinfoEstNameLen
	}
	return ginfo, nil
}
