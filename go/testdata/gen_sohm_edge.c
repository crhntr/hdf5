/*
 * Generator for sohm_edge.h5, a file whose shared object header message
 * table is at the limits of the SOHM code (go/mutation_sohm_test.go):
 *
 *   h5cc -o /tmp/gen_sohm_edge gen_sohm_edge.c && (cd go/testdata && /tmp/gen_sohm_edge)
 *
 * The table has 8 indexes (H5O_SHMESG_MAX_NINDEXES): index 0 shares
 * datatypes, index 1 dataspaces, index 2 attributes, the others no
 * message type. The lists hold at most 4 messages (the B-tree to list
 * cutoff is 2): /d0 has 4 attributes a0..a3 (int32, scalar, never
 * written: 0), shared in the heap: the attribute list is full. /d0../d3
 * (int8, int16, int32, int64) share one dataspace {4}. Every dataset
 * holds 1, 2, 3, 4 (+ 10 times its number).
 */
#include "hdf5.h"
#include <stdio.h>
#include <stdlib.h>

int
main(void)
{
    hid_t fcpl = H5Pcreate(H5P_FILE_CREATE);
    if (H5Pset_shared_mesg_nindexes(fcpl, 8) < 0)
        return 1;
    if (H5Pset_shared_mesg_index(fcpl, 0, H5O_SHMESG_DTYPE_FLAG, 1) < 0 ||
        H5Pset_shared_mesg_index(fcpl, 1, H5O_SHMESG_SDSPACE_FLAG, 1) < 0 ||
        H5Pset_shared_mesg_index(fcpl, 2, H5O_SHMESG_ATTR_FLAG, 1) < 0)
        return 2;
    for (unsigned i = 3; i < 8; i++)
        if (H5Pset_shared_mesg_index(fcpl, i, H5O_SHMESG_NONE_FLAG, 1) < 0)
            return 3;
    if (H5Pset_shared_mesg_phase_change(fcpl, 4, 2) < 0)
        return 4;
    hid_t file = H5Fcreate("sohm_edge.h5", H5F_ACC_TRUNC, fcpl, H5P_DEFAULT);
    if (file < 0)
        return 5;
    hid_t   types[4] = {H5T_STD_I8LE, H5T_STD_I16LE, H5T_STD_I32LE, H5T_STD_I64LE};
    hsize_t dims[1]  = {4};
    hid_t   space    = H5Screate_simple(1, dims, NULL);
    for (int i = 0; i < 4; i++) {
        char name[8];
        int  v[4] = {1 + 10 * i, 2 + 10 * i, 3 + 10 * i, 4 + 10 * i};
        snprintf(name, sizeof name, "d%d", i);
        hid_t d = H5Dcreate2(file, name, types[i], space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        if (d < 0 || H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v) < 0)
            return 6;
        if (i == 0)
            for (int k = 0; k < 4; k++) {
                char  aname[8];
                hid_t as = H5Screate(H5S_SCALAR);
                snprintf(aname, sizeof aname, "a%d", k);
                /* (not written: writing an attribute shares its message
                 *  again, which turns the full list into a B-tree) */
                hid_t a = H5Acreate2(d, aname, H5T_STD_I32LE, as, H5P_DEFAULT, H5P_DEFAULT);
                H5Aclose(a);
                H5Sclose(as);
            }
        H5Dclose(d);
    }
    H5Sclose(space);
    H5Fclose(file);
    H5Pclose(fcpl);
    return 0;
}
