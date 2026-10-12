package hdf5

// Translated from src/H5FAstat.c. Fixed array metadata statistics
// functions.

// h5faStat is H5FA_stat_t: fixed array metadata statistics info.
type h5faStat struct {
	/* Non-stored (i.e. computed) fields */
	hdrSize  uint64 /* Size of header */
	dblkSize uint64 /* Size of data block */

	/* Stored fields */
	nelmts uint64 /* # of elements */
}

// getStats is H5FA_get_stats: query the metadata stats of an array.
func (fa *h5fa) getStats() h5faStat {
	/* Copy fixed array statistics */
	return h5faStat{
		hdrSize:  fa.hdr.hdrSize,
		dblkSize: fa.hdr.dblkSize,
		nelmts:   fa.hdr.nelmts,
	}
}
