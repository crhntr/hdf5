/*
 * Generator for the "old style" (symbol table) group test files used by
 * h5g_stab_test.go. Build and run with the HDF5 1.14 C library:
 *
 *     h5cc -o gen_stab gen_stab.c && ./gen_stab
 *
 * stab_deep.h5     sym_k(ik=2, lk=2): every B-tree node has at most 4
 *                  children and every symbol table node at most 4 entries,
 *                  so the 500-link group /big has a B-tree several levels
 *                  deep.
 * stab_default.h5  default K values (ik=16, lk=4); /big has 1000 links, which
 *                  needs a two-level B-tree.
 *
 * In both files /big holds links named "link%04d" (inserted in a scrambled
 * order): every third link (i % 3 == 0) is a hard link to /dset, the others
 * are soft links whose value is "/target%d". The root group also holds /dset
 * and an empty group /empty.
 */
#include <stdio.h>
#include <stdlib.h>
#include "hdf5.h"

static int
make(const char *path, unsigned ik, unsigned lk, int nlinks)
{
    hid_t   fcpl = H5Pcreate(H5P_FILE_CREATE);
    hid_t   file, big, empty, space, dset;
    hsize_t dims[1] = {4};
    int     data[4] = {1, 2, 3, 4};
    int     i;

    if (ik && lk)
        H5Pset_sym_k(fcpl, ik, lk);
    file  = H5Fcreate(path, H5F_ACC_TRUNC, fcpl, H5P_DEFAULT);
    space = H5Screate_simple(1, dims, NULL);
    dset  = H5Dcreate2(file, "dset", H5T_STD_I32LE, space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
    H5Dwrite(dset, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, data);
    empty = H5Gcreate2(file, "empty", H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
    big   = H5Gcreate2(file, "big", H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
    for (i = 0; i < nlinks; i++) {
        /* scramble the insertion order (7919 is prime, coprime to nlinks) */
        int  j = (int)(((long)i * 7919) % nlinks);
        char name[32], target[32];

        snprintf(name, sizeof(name), "link%04d", j);
        if (j % 3 == 0)
            H5Lcreate_hard(file, "/dset", big, name, H5P_DEFAULT, H5P_DEFAULT);
        else {
            snprintf(target, sizeof(target), "/target%d", j);
            H5Lcreate_soft(target, big, name, H5P_DEFAULT, H5P_DEFAULT);
        }
    }
    H5Gclose(big);
    H5Gclose(empty);
    H5Dclose(dset);
    H5Sclose(space);
    H5Fclose(file);
    H5Pclose(fcpl);
    return 0;
}

int
main(void)
{
    make("stab_deep.h5", 2, 2, 500);
    make("stab_default.h5", 0, 0, 1000);
    return 0;
}
