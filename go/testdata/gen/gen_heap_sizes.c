/*
 * Generator for testdata/heap_size2.h5 and testdata/heap_addr2.h5: files
 * with 2-byte lengths and 2-byte addresses, the only sizes for which the
 * header sizes of a global heap collection (H5HG_SIZEOF_HDR) and of a
 * local heap (H5HL_SIZEOF_HDR) are not already a multiple of eight before
 * alignment.
 *
 *   h5cc -o /tmp/gen_heap_sizes gen_heap_sizes.c && (cd go/testdata && /tmp/gen_heap_sizes)
 *
 * heap_size2.h5: H5Pset_sizes(8, 2); /vs: variable-length string {3},
 *                "alpha", "be", "gamma-delta" (one global heap collection).
 * heap_addr2.h5: H5Pset_sizes(2, 4); groups /g1 and /g1/sub, int32 scalar
 *                dataset /g1/d (5): old-style groups whose names live in
 *                local heaps.
 */
#include "hdf5.h"

int
main(void)
{
    {
        hid_t fcpl = H5Pcreate(H5P_FILE_CREATE);
        H5Pset_sizes(fcpl, 8, 2);
        hid_t       f = H5Fcreate("heap_size2.h5", H5F_ACC_TRUNC, fcpl, H5P_DEFAULT);
        hid_t       t = H5Tcopy(H5T_C_S1);
        hsize_t     d[1] = {3};
        const char *v[3] = {"alpha", "be", "gamma-delta"};
        H5Tset_size(t, H5T_VARIABLE);
        hid_t s  = H5Screate_simple(1, d, NULL);
        hid_t ds = H5Dcreate2(f, "vs", t, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        H5Dwrite(ds, t, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
        H5Dclose(ds);
        H5Sclose(s);
        H5Tclose(t);
        H5Fclose(f);
        H5Pclose(fcpl);
    }
    {
        hid_t fcpl = H5Pcreate(H5P_FILE_CREATE);
        H5Pset_sizes(fcpl, 2, 4);
        hid_t f   = H5Fcreate("heap_addr2.h5", H5F_ACC_TRUNC, fcpl, H5P_DEFAULT);
        hid_t g   = H5Gcreate2(f, "g1", H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        hid_t sub = H5Gcreate2(g, "sub", H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        hid_t s   = H5Screate(H5S_SCALAR);
        hid_t ds  = H5Dcreate2(g, "d", H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        int   v   = 5;
        H5Dwrite(ds, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, &v);
        H5Dclose(ds);
        H5Sclose(s);
        H5Gclose(sub);
        H5Gclose(g);
        H5Fclose(f);
        H5Pclose(fcpl);
    }
    return 0;
}
