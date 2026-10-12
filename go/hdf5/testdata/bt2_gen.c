/*
 * Generator for the v2 B-tree test file in this directory (the repository
 * corpus has no creation-order indexes and no filtered chunk index).
 *
 *   h5cc -o /tmp/bt2_gen bt2_gen.c && (cd go/hdf5/testdata && /tmp/bt2_gen)
 *
 * bt2_corder.h5: group "/links" with dense link storage and an indexed
 *                creation order: soft links "l0000".."l0299" -> "/t0000"..
 *                (v2 B-tree types 5 and 6, depth 1);
 *                dataset "/filtered": int32 40x40, max dims unlimited,
 *                chunks 1x2, deflate, value i*40+j (v2 B-tree type 11),
 *                with 50 dense attributes "attr00".."attr49" (int32 scalar
 *                value i) with an indexed creation order (types 8 and 9);
 *                dataset "/plain": int32 30x30, max dims unlimited, chunks
 *                1x1, no filters, value i*30+j (type 10, depth 1).
 */
#include "hdf5.h"
#include <stdio.h>
#include <stdlib.h>

int
main(void)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST);
    hid_t f = H5Fcreate("bt2_corder.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);

    /* Links */
    {
        hid_t gcpl = H5Pcreate(H5P_GROUP_CREATE);
        H5Pset_link_creation_order(gcpl, H5P_CRT_ORDER_TRACKED | H5P_CRT_ORDER_INDEXED);
        H5Pset_link_phase_change(gcpl, 0, 0);
        hid_t g = H5Gcreate2(f, "links", H5P_DEFAULT, gcpl, H5P_DEFAULT);
        for (int i = 0; i < 300; i++) {
            char name[32], target[32];
            snprintf(name, sizeof name, "l%04d", i);
            snprintf(target, sizeof target, "/t%04d", i);
            H5Lcreate_soft(target, g, name, H5P_DEFAULT, H5P_DEFAULT);
        }
        H5Gclose(g);
        H5Pclose(gcpl);
    }

    /* Filtered chunked dataset with dense attributes */
    {
        hsize_t dims[2] = {40, 40}, maxdims[2] = {H5S_UNLIMITED, H5S_UNLIMITED}, chunk[2] = {1, 2};
        hid_t   space = H5Screate_simple(2, dims, maxdims);
        hid_t   dcpl  = H5Pcreate(H5P_DATASET_CREATE);
        H5Pset_chunk(dcpl, 2, chunk);
        H5Pset_deflate(dcpl, 6);
        H5Pset_attr_creation_order(dcpl, H5P_CRT_ORDER_TRACKED | H5P_CRT_ORDER_INDEXED);
        H5Pset_attr_phase_change(dcpl, 0, 0);
        hid_t d   = H5Dcreate2(f, "filtered", H5T_STD_I32LE, space, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        int  *buf = malloc(40 * 40 * sizeof(int));
        for (int i = 0; i < 40 * 40; i++)
            buf[i] = i;
        H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, buf);
        free(buf);

        hid_t ascalar = H5Screate(H5S_SCALAR);
        for (int i = 0; i < 50; i++) {
            char name[32];
            snprintf(name, sizeof name, "attr%02d", i);
            hid_t a = H5Acreate2(d, name, H5T_STD_I32LE, ascalar, H5P_DEFAULT, H5P_DEFAULT);
            H5Awrite(a, H5T_NATIVE_INT, &i);
            H5Aclose(a);
        }
        H5Sclose(ascalar);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(space);
    }

    /* Unfiltered chunked dataset */
    {
        hsize_t dims[2] = {30, 30}, maxdims[2] = {H5S_UNLIMITED, H5S_UNLIMITED}, chunk[2] = {1, 1};
        hid_t   space = H5Screate_simple(2, dims, maxdims);
        hid_t   dcpl  = H5Pcreate(H5P_DATASET_CREATE);
        H5Pset_chunk(dcpl, 2, chunk);
        hid_t d   = H5Dcreate2(f, "plain", H5T_STD_I32LE, space, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        int  *buf = malloc(30 * 30 * sizeof(int));
        for (int i = 0; i < 30 * 30; i++)
            buf[i] = i;
        H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, buf);
        free(buf);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(space);
    }

    H5Fclose(f);
    H5Pclose(fapl);
    return 0;
}
