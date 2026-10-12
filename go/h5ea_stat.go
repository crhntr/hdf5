package hdf5

// Translated from src/H5EAstat.c. Extensible array metadata statistics
// functions.

// h5eaStat is H5EA_stat_t: extensible array metadata statistics info.
type h5eaStat struct {
	/* Non-stored (i.e. computed) fields */
	computed struct {
		hdrSize      uint64 /* Size of header */
		nindexBlks   uint64 /* # of index blocks (should be 0 or 1) */
		indexBlkSize uint64 /* Size of index blocks allocated */
	}

	/* Stored fields */
	stored struct {
		nsuperBlks   uint64 /* # of super blocks */
		superBlkSize uint64 /* Size of super blocks allocated */
		ndataBlks    uint64 /* # of data blocks */
		dataBlkSize  uint64 /* Size of data blocks allocated */
		maxIdxSet    uint64 /* Highest element index stored (+1 - i.e. if element 0 has been set, this value with be '1', if no elements have been stored, this value will be '0') */
		nelmts       uint64 /* # of elements "realized" */
	}
}

// getStats is H5EA_get_stats: query the metadata stats of an array.
func (ea *h5ea) getStats() h5eaStat {
	hdr := ea.hdr

	/* Copy extensible array statistics */
	var stats h5eaStat
	stats.computed.hdrSize = hdr.hdrSize
	stats.computed.nindexBlks = hdr.nindexBlks
	stats.computed.indexBlkSize = hdr.indexBlkSize
	stats.stored.nsuperBlks = hdr.nsuperBlks
	stats.stored.superBlkSize = hdr.superBlkSize
	stats.stored.ndataBlks = hdr.ndataBlks
	stats.stored.dataBlkSize = hdr.dataBlkSize
	stats.stored.maxIdxSet = hdr.maxIdxSet
	stats.stored.nelmts = hdr.nelmts
	return stats
}
