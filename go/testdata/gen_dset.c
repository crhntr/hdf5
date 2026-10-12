/*
 * Generator for dset.h5, dset_src.h5 and dset_ext.raw: datasets for the
 * read paths the repository corpus does not exercise (no corpus file has a
 * single chunk index, partial edge chunks stored unfiltered, a virtual
 * dataset whose sources are in the same file, ...).
 *
 *   h5cc -o /tmp/gen_dset gen_dset.c && (cd go/testdata && /tmp/gen_dset)
 *
 * Datasets of dset.h5 (latest file format; int32 = H5T_STD_I32LE):
 *
 *   /single_unfilt   int32 {4,5}, chunk {4,5}: single chunk index, value r*5+c
 *   /single_filt     the same with shuffle + deflate (filtered single chunk)
 *   /single_unalloc  int32 {4,5}, chunk {4,5}, fill value 7, never written
 *   /edge_fa         int32 {7,9}, chunk {3,4}, shuffle + deflate, partial
 *                    edge chunks not filtered (fixed array index), r*9+c
 *   /edge_ea         int32 {7,9}/{UNLIM,9}, same (extensible array index)
 *   /fill_partial    int16 {10,10}, chunk {3,3}, fill value -1, only the
 *                    block [2..5]x[2..5] written with r*10+c
 *   /fill_never      the same with H5D_FILL_TIME_NEVER (unwritten chunks
 *                    read as whatever the buffer held: zeros here)
 *   /contig_unalloc  int32 {6}, contiguous, fill value 42, never written
 *   /contig_undef    int32 {6}, contiguous, undefined fill value, never
 *                    written (reading fails)
 *   /compact         int16 {3,4}, compact, r*4+c
 *   /external        int32 {10}, external file dset_ext.raw in two segments
 *                    (offset 16, 12 bytes; offset 100, 28 bytes), value 100+i
 *   /vds_src_a       int32 {5}, value 10+i;  /vds_src_b int32 {5}, 20+i
 *   /vds             int32 {12}, virtual, fill value -9: [0..4] <- "."
 *                    /vds_src_a, [6..10] <- "." /vds_src_b
 *   /vds_p0, /vds_p1 int32 {3}: 30+i, 40+i
 *   /vds_printf      int32 {0}/{UNLIM} virtual, fill -1: blocks of 3 in the
 *                    unlimited dimension <- "." /vds_p%b (printf mapping)
 *   /vds_u           int32 {4}/{UNLIM}: 50+i
 *   /vds_unlim       int32 {0}/{UNLIM} virtual: [0..) <- "." /vds_u[0..)
 *   /vds_xfile       int32 {4} virtual: <- dset_src.h5 /src (60+i)
 *   /vds_missing     int32 {4} virtual, fill 5: <- "missing.h5" /nothing
 *   /vlen_fill       vlen int32 {6}, chunk {2}, only element 0..1 written
 *                    ({1}, {2,3}); unwritten elements are the null sequence
 */
#include "hdf5.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static hid_t
space1(hsize_t n, hsize_t max)
{
    hsize_t d = n, m = max;
    return H5Screate_simple(1, &d, &m);
}

int
main(void)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST);

    /* Source file for /vds_xfile */
    {
        hid_t f = H5Fcreate("dset_src.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);
        hid_t s = space1(4, 4);
        hid_t d = H5Dcreate2(f, "/src", H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        int   v[4] = {60, 61, 62, 63};
        H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
        H5Dclose(d);
        H5Sclose(s);
        H5Fclose(f);
    }

    /* External raw file: 16 bytes junk, 3 ints, junk up to 100, 7 ints */
    {
        FILE *fp = fopen("dset_ext.raw", "wb");
        unsigned char junk[128];
        int  i;
        memset(junk, 0xEE, sizeof junk);
        fwrite(junk, 1, 16, fp);
        for (i = 0; i < 3; i++) {
            unsigned char b[4] = {(unsigned char)(100 + i), 0, 0, 0};
            fwrite(b, 1, 4, fp);
        }
        fwrite(junk, 1, 100 - 28, fp);
        for (i = 3; i < 10; i++) {
            unsigned char b[4] = {(unsigned char)(100 + i), 0, 0, 0};
            fwrite(b, 1, 4, fp);
        }
        fclose(fp);
    }

    hid_t file = H5Fcreate("dset.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);

    /* Single chunk index */
    {
        hsize_t dims[2] = {4, 5};
        int     v[20], i;
        for (i = 0; i < 20; i++)
            v[i] = i;
        hid_t s = H5Screate_simple(2, dims, NULL);
        for (int filt = 0; filt < 2; filt++) {
            hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
            H5Pset_chunk(dcpl, 2, dims);
            if (filt) {
                H5Pset_shuffle(dcpl);
                H5Pset_deflate(dcpl, 6);
            }
            hid_t d = H5Dcreate2(file, filt ? "single_filt" : "single_unfilt", H5T_STD_I32LE, s, H5P_DEFAULT,
                                 dcpl, H5P_DEFAULT);
            H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
            H5Dclose(d);
            H5Pclose(dcpl);
        }
        hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
        int   fill = 7;
        H5Pset_chunk(dcpl, 2, dims);
        H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill);
        hid_t d = H5Dcreate2(file, "single_unalloc", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* Partial edge chunks stored unfiltered */
    {
        hsize_t dims[2] = {7, 9}, maxu[2] = {H5S_UNLIMITED, 9}, chunk[2] = {3, 4};
        int     v[63], i;
        for (i = 0; i < 63; i++)
            v[i] = i;
        for (int ea = 0; ea < 2; ea++) {
            hid_t s    = H5Screate_simple(2, dims, ea ? maxu : NULL);
            hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
            H5Pset_chunk(dcpl, 2, chunk);
            H5Pset_chunk_opts(dcpl, H5D_CHUNK_DONT_FILTER_PARTIAL_CHUNKS);
            H5Pset_shuffle(dcpl);
            H5Pset_deflate(dcpl, 6);
            hid_t d = H5Dcreate2(file, ea ? "edge_ea" : "edge_fa", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl,
                                 H5P_DEFAULT);
            H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
            H5Dclose(d);
            H5Pclose(dcpl);
            H5Sclose(s);
        }
    }

    /* Fill values in unwritten chunks */
    {
        hsize_t dims[2] = {10, 10}, chunk[2] = {3, 3}, start[2] = {2, 2}, count[2] = {4, 4};
        short   v[16], fill = -1;
        int     r, c;
        for (r = 0; r < 4; r++)
            for (c = 0; c < 4; c++)
                v[r * 4 + c] = (short)((r + 2) * 10 + c + 2);
        for (int never = 0; never < 2; never++) {
            hid_t s    = H5Screate_simple(2, dims, NULL);
            hid_t m    = H5Screate_simple(2, count, NULL);
            hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
            H5Pset_chunk(dcpl, 2, chunk);
            H5Pset_fill_value(dcpl, H5T_NATIVE_SHORT, &fill);
            if (never)
                H5Pset_fill_time(dcpl, H5D_FILL_TIME_NEVER);
            hid_t d = H5Dcreate2(file, never ? "fill_never" : "fill_partial", H5T_STD_I16LE, s, H5P_DEFAULT,
                                 dcpl, H5P_DEFAULT);
            H5Sselect_hyperslab(s, H5S_SELECT_SET, start, NULL, count, NULL);
            H5Dwrite(d, H5T_NATIVE_SHORT, m, s, H5P_DEFAULT, v);
            H5Dclose(d);
            H5Pclose(dcpl);
            H5Sclose(m);
            H5Sclose(s);
        }
    }

    /* Unallocated contiguous storage */
    {
        hid_t s    = space1(6, 6);
        hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
        int   fill = 42;
        H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill);
        H5Dclose(H5Dcreate2(file, "contig_unalloc", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pset_fill_value(dcpl, H5T_NATIVE_INT, NULL);
        H5Dclose(H5Dcreate2(file, "contig_undef", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* Compact */
    {
        hsize_t dims[2] = {3, 4};
        short   v[12];
        int     i;
        for (i = 0; i < 12; i++)
            v[i] = (short)i;
        hid_t s    = H5Screate_simple(2, dims, NULL);
        hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
        H5Pset_layout(dcpl, H5D_COMPACT);
        hid_t d = H5Dcreate2(file, "compact", H5T_STD_I16LE, s, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        H5Dwrite(d, H5T_NATIVE_SHORT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* External file list (the raw file is written above) */
    {
        hid_t s    = space1(10, 10);
        hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
        H5Pset_external(dcpl, "dset_ext.raw", 16, 12);
        H5Pset_external(dcpl, "dset_ext.raw", 100, 28);
        H5Dclose(H5Dcreate2(file, "external", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* Virtual datasets */
    {
        int i, v[5];
        for (int k = 0; k < 2; k++) {
            hid_t s = space1(5, 5);
            hid_t d = H5Dcreate2(file, k ? "vds_src_b" : "vds_src_a", H5T_STD_I32LE, s, H5P_DEFAULT,
                                 H5P_DEFAULT, H5P_DEFAULT);
            for (i = 0; i < 5; i++)
                v[i] = (k ? 20 : 10) + i;
            H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
            H5Dclose(d);
            H5Sclose(s);
        }

        hid_t   vs = space1(12, 12), ss = space1(5, 5);
        hid_t   dcpl = H5Pcreate(H5P_DATASET_CREATE);
        int     fill = -9;
        hsize_t start = 0, count = 5;
        H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill);
        H5Sselect_hyperslab(vs, H5S_SELECT_SET, &start, NULL, &count, NULL);
        H5Pset_virtual(dcpl, vs, ".", "/vds_src_a", ss);
        start = 6;
        H5Sselect_hyperslab(vs, H5S_SELECT_SET, &start, NULL, &count, NULL);
        H5Pset_virtual(dcpl, vs, ".", "/vds_src_b", ss);
        H5Dclose(H5Dcreate2(file, "vds", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pclose(dcpl);
        H5Sclose(vs);
        H5Sclose(ss);

        /* printf mapping */
        for (int k = 0; k < 2; k++) {
            hid_t s = space1(3, 3);
            char  name[16];
            snprintf(name, sizeof name, "vds_p%d", k);
            hid_t d = H5Dcreate2(file, name, H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
            for (i = 0; i < 3; i++)
                v[i] = (k ? 40 : 30) + i;
            H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
            H5Dclose(d);
            H5Sclose(s);
        }
        vs   = space1(0, H5S_UNLIMITED);
        ss   = space1(3, 3);
        dcpl = H5Pcreate(H5P_DATASET_CREATE);
        fill = -1;
        H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill);
        {
            hsize_t st = 0, stride = 3, cnt = H5S_UNLIMITED, blk = 3;
            H5Sselect_hyperslab(vs, H5S_SELECT_SET, &st, &stride, &cnt, &blk);
        }
        H5Pset_virtual(dcpl, vs, ".", "/vds_p%b", ss);
        H5Dclose(H5Dcreate2(file, "vds_printf", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pclose(dcpl);
        H5Sclose(vs);
        H5Sclose(ss);

        /* non-printf unlimited mapping */
        {
            hsize_t chunk = 2;
            hid_t   s     = space1(4, H5S_UNLIMITED);
            hid_t   cp    = H5Pcreate(H5P_DATASET_CREATE);
            H5Pset_chunk(cp, 1, &chunk);
            hid_t d = H5Dcreate2(file, "vds_u", H5T_STD_I32LE, s, H5P_DEFAULT, cp, H5P_DEFAULT);
            for (i = 0; i < 4; i++)
                v[i] = 50 + i;
            H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v);
            H5Dclose(d);
            H5Pclose(cp);
            H5Sclose(s);
        }
        vs   = space1(0, H5S_UNLIMITED);
        ss   = space1(0, H5S_UNLIMITED);
        dcpl = H5Pcreate(H5P_DATASET_CREATE);
        {
            hsize_t st = 0, cnt = 1, blk = H5S_UNLIMITED;
            H5Sselect_hyperslab(vs, H5S_SELECT_SET, &st, NULL, &cnt, &blk);
            H5Sselect_hyperslab(ss, H5S_SELECT_SET, &st, NULL, &cnt, &blk);
        }
        H5Pset_virtual(dcpl, vs, ".", "/vds_u", ss);
        H5Dclose(H5Dcreate2(file, "vds_unlim", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pclose(dcpl);
        H5Sclose(vs);
        H5Sclose(ss);

        /* other file / missing file */
        vs   = space1(4, 4);
        ss   = space1(4, 4);
        dcpl = H5Pcreate(H5P_DATASET_CREATE);
        H5Pset_virtual(dcpl, vs, "dset_src.h5", "/src", ss);
        H5Dclose(H5Dcreate2(file, "vds_xfile", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pclose(dcpl);
        dcpl = H5Pcreate(H5P_DATASET_CREATE);
        fill = 5;
        H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill);
        H5Pset_virtual(dcpl, vs, "missing.h5", "/nothing", ss);
        H5Dclose(H5Dcreate2(file, "vds_missing", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT));
        H5Pclose(dcpl);
        H5Sclose(vs);
        H5Sclose(ss);
    }

    /* vlen with unwritten chunks */
    {
        hid_t   vt = H5Tvlen_create(H5T_STD_I32LE), mt = H5Tvlen_create(H5T_NATIVE_INT);
        hsize_t chunk = 2, n = 2;
        hid_t   s    = space1(6, 6);
        hid_t   dcpl = H5Pcreate(H5P_DATASET_CREATE);
        H5Pset_chunk(dcpl, 1, &chunk);
        hid_t d = H5Dcreate2(file, "vlen_fill", vt, s, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        int   a[1] = {1}, b[2] = {2, 3};
        hvl_t w[2] = {{1, a}, {2, b}};
        hsize_t st = 0;
        hid_t   m  = H5Screate_simple(1, &n, NULL);
        H5Sselect_hyperslab(s, H5S_SELECT_SET, &st, NULL, &n, NULL);
        H5Dwrite(d, mt, m, s, H5P_DEFAULT, w);
        H5Dclose(d);
        H5Sclose(m);
        H5Pclose(dcpl);
        H5Sclose(s);
        H5Tclose(vt);
        H5Tclose(mt);
    }

    H5Fclose(file);
    H5Pclose(fapl);
    return 0;
}
