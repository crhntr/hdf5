package hdf5

// Iteration index types and orders, from src/H5public.h (H5_index_t and
// H5_iter_order_t). They are shared by the group (H5G) and attribute (H5A)
// iteration code.

// h5Index is H5_index_t: the index used to iterate over links or
// attributes.
type h5Index int

const (
	h5IndexName     h5Index = 0 /* Index on names                     */
	h5IndexCrtOrder h5Index = 1 /* Index on creation order            */
)

// h5IterOrder is H5_iter_order_t: the order in which to iterate over an
// index.
type h5IterOrder int

const (
	h5IterInc    h5IterOrder = 0 /* Increasing order                  */
	h5IterDec    h5IterOrder = 1 /* Decreasing order                  */
	h5IterNative h5IterOrder = 2 /* No particular order, whatever is fastest */
)
