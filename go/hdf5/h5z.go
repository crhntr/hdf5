package hdf5

// Translated from src/H5Z.c (H5Z_pipeline, read direction only).
//
// Only the decode ("read", H5Z_FLAG_REVERSE) direction of the filter pipeline
// is translated. The registered-filter table of the C library is replaced by
// a switch on the filter id over the filters that ship with the library
// (H5Z_FILTER_DEFLATE, SHUFFLE, FLETCHER32, SZIP, NBIT and SCALEOFFSET).
// Dynamically loaded plugin filters (H5PL) are not supported: a pipeline that
// uses an unknown filter fails exactly like the C library does when the
// plugin cannot be found.

// h5zEDC is H5Z_EDC_t: whether error detection (checksums) is performed when
// reading.
type h5zEDC int

const (
	h5zErrorEDC   h5zEDC = -1
	h5zDisableEDC h5zEDC = 0
	h5zEnableEDC  h5zEDC = 1
	h5zNoEDC      h5zEDC = 2
)

// h5zMaxNFilters is H5Z_MAX_NFILTERS.
const h5zMaxNFilters = 32

// h5zFilterFunc is the type of an H5Z_func_t filter callback in the decode
// direction. flags are the H5Z_FLAG_* bits (always including
// h5zFlagReverse), cdValues are the client data values of the filter in the
// pipeline message, and buf is the data to filter (nbytes == len(buf)). The
// returned slice is the filtered data (the C filters return the new size in
// bytes, 0 on failure, and update buf/buf_size; both are folded into the
// returned slice and error).
type h5zFilterFunc func(flags uint, cdValues []uint32, buf []byte) ([]byte, error)

// h5zFindFilter is H5Z__find_idx followed by the lookup in H5Z_table_g: it
// returns the filter callback registered for id, or nil if the filter is not
// registered.
func h5zFindFilter(id int32) h5zFilterFunc {
	switch id {
	case h5zFilterDeflate:
		return h5zFilterDeflateFunc
	case h5zFilterShuffle:
		return h5zFilterShuffleFunc
	case h5zFilterFletcher32:
		return h5zFilterFletcher32Func
	case h5zFilterSzip:
		return h5zFilterSzipFunc
	case h5zFilterNbit:
		return h5zFilterNbitFunc
	case h5zFilterScaleoffset:
		return h5zFilterScaleoffsetFunc
	}
	return nil
}

// h5zPipeline is H5Z_pipeline with flags = H5Z_FLAG_REVERSE: run the
// filters of pline backwards over buf (a chunk as stored in the file) and
// return the decoded chunk. filterMask has a bit set for each filter in the
// pipeline that was skipped when the chunk was written (H5Z_FLAG_OPTIONAL).
//
// Error detection (the Fletcher32 checksum) is enabled, i.e. edc_read is
// H5Z_ENABLE_EDC; see h5zPipelineEDC.
func h5zPipeline(pline *h5oPline, filterMask uint32, buf []byte) ([]byte, error) {
	return h5zPipelineEDC(pline, filterMask, h5zEnableEDC, buf)
}

// h5zPipelineEDC is H5Z_pipeline with flags = H5Z_FLAG_REVERSE and an
// explicit edc_read. There is no H5Z_cb_t callback (the default of a dataset
// transfer property list has none), so any filter failure aborts the
// pipeline.
func h5zPipelineEDC(pline *h5oPline, filterMask uint32, edcRead h5zEDC, buf []byte) ([]byte, error) {
	if pline == nil {
		return buf, nil
	}
	if pline.nused > h5zMaxNFilters || pline.nused > len(pline.filter) {
		return nil, errorf(H5E_PLINE, H5E_BADVALUE, "invalid number of filters in pipeline")
	}
	if len(buf) == 0 {
		return nil, errorf(H5E_PLINE, H5E_BADSIZE, "empty buffer passed to filter pipeline")
	}

	for i := pline.nused; i > 0; i-- {
		idx := i - 1
		if filterMask&(uint32(1)<<uint(idx)) != 0 {
			continue /* filter excluded */
		}

		/* If the filter isn't registered, fail. (The C library first tries to
		 * load it dynamically through H5PL; plugins are not supported here.)
		 * Note that H5Z_FLAG_OPTIONAL does not help on the read side.
		 */
		filter := &pline.filter[idx]
		fn := h5zFindFilter(filter.id)
		if fn == nil {
			/* Print out the filter name to give more info.  But the name is
			 * optional for the filter */
			if filter.name != "" {
				return nil, errorf(H5E_PLINE, H5E_READERROR, "required filter '%s' is not registered", filter.name)
			}
			return nil, errorf(H5E_PLINE, H5E_READERROR, "required filter (name unavailable) is not registered")
		}

		tmpFlags := uint(h5zFlagReverse) | uint(filter.flags)
		if edcRead == h5zDisableEDC {
			tmpFlags |= h5zFlagSkipEdc
		}
		out, err := fn(tmpFlags, filter.cdValues, buf)
		if err != nil || len(out) == 0 {
			/* new_nbytes == 0 and no callback: failure */
			if err == nil {
				return nil, errorf(H5E_PLINE, H5E_READERROR, "filter returned failure during read")
			}
			return nil, wrapf(err, H5E_PLINE, H5E_READERROR, "filter returned failure during read")
		}
		buf = out
	}

	return buf, nil
}
