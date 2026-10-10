package hdf5

// STUB — to be replaced by the translation of src/H5Z.c (H5Z_pipeline, read
// direction only), src/H5Zdeflate.c, src/H5Zshuffle.c, src/H5Zfletcher32.c,
// src/H5Znbit.c and src/H5Zscaleoffset.c.

// h5zPipeline is H5Z_pipeline with flags = H5Z_FLAG_REVERSE: run the
// filters of pline backwards over buf (a chunk as stored in the file) and
// return the decoded chunk. filterMask has a bit set for each filter in the
// pipeline that was skipped when the chunk was written (H5Z_FLAG_OPTIONAL).
func h5zPipeline(pline *h5oPline, filterMask uint32, buf []byte) ([]byte, error) {
	return nil, errorf(H5E_PLINE, H5E_UNSUPPORTED, "filters not translated yet")
}
