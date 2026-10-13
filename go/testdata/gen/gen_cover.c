/*
 * Generator for testdata/cover.h5 and testdata/cover_sohm.h5: inputs for
 * the read paths that `go test -cover` found unexercised by the corpus.
 * Both files are also compared with libhdf5 by cgo/conformance (every
 * go/testdata/*.h5 is).
 *
 *   h5cc -o /tmp/gen_cover gen_cover.c && (cd go/testdata && /tmp/gen_cover)
 *
 * cover.h5 (latest file format; int32 = H5T_STD_I32LE):
 *
 *   /ea_sparse       int32 {300}/{UNLIM}, chunk {2}, fill -7, only the
 *                    chunks of elements 0..3, 100..101 and 298..299 written
 *                    with value i (extensible array index with unallocated
 *                    data blocks and super blocks)
 *   /ea_sparse_filt  the same with deflate (filtered elements: the
 *                    H5D__earray_filt_fill "missing" element)
 *   /fa_paged        int32 {3000}, chunk {1}, fill -3, only elements 0,
 *                    1500 and 2999 written with value i (paged fixed array
 *                    data block with uninitialized pages)
 *   /fa_paged_filt   the same with deflate
 *   /vlen_str_fill   vlen string {6}, chunk {2}, fill "fill!", only
 *                    elements 0..1 written ("a", "bc")
 *   /vlen_str_contig vlen string {3}, contiguous, fill "abc", never written
 *   /fixed_fill_contig int16 {2,3}, contiguous, fill 11, never written
 *   /null_space      int32 with a null dataspace, attribute "null_attr"
 *                    (int32, null dataspace) and "scalar_attr" (int32 5)
 *   /empty_2d        int32 {0,3}/{UNLIM,3}, chunk {4,3}
 *   /target          int32 {6,8}: r*8+c
 *   /regref_old      H5T_STD_REF_DSETREG {5}: points (1,2),(3,4),(5,7);
 *                    hyperslab start {1,1} stride {2,3} count {2,2};
 *                    none; all; a single point (0,0) -- all on /target
 *   /regref_new      H5T_STD_REF {7}: the same five regions, an object
 *                    reference to /target and an attribute reference to
 *                    /null_space "scalar_attr"
 *   /objref_old      H5T_STD_REF_OBJ {2}: /target, /dtype
 *   /dtype           committed compound {a: int32, b: float64} with
 *                    attribute "note" = "committed"
 *   /typed           dataset of /dtype {2}: {1, 0.5}, {2, 1.5}
 *   /vds_all         int32 {6,8} virtual: all <- "." /target all
 *   /vds_all_src     int32 {2,8} virtual: hyperslab rows 1..2 of the
 *                    virtual dataset <- "." /target rows 3..4
 *   /vds_src_all     int32 {4,8} virtual, fill -1: rows 1..2 <- "."
 *                    /target_rows (int32 {2,8}, value 100+r*8+c), source
 *                    selection "all"
 *   /group_attrs     group with attributes "x" (int32 {2}: 1,2) and "y"
 *                    (float64 scalar 2.5)
 *
 * cover_fam00000.h5, cover_fam00001.h5: a family file (2048-byte members)
 * in the latest format, with the driver info message in the superblock
 * extension; dataset /x int32 {300}: 3*i.
 *
 * cover_sohm.h5: shared datatype/dataspace/attribute messages kept in v2
 * B-tree indexes from the start (H5Pset_shared_mesg_phase_change(0, 0)),
 * 12 datasets /d00../d11 (int32 {i+1}, value i*10+j) with attribute "a"
 * (int32 {2}: i, -i) and 12 with the same shared datatype /e00../e11.
 */
#include "hdf5.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(x)                                                                                             \
    do {                                                                                                     \
        if ((x) < 0) {                                                                                       \
            fprintf(stderr, "failed: %s (line %d)\n", #x, __LINE__);                                         \
            exit(1);                                                                                         \
        }                                                                                                    \
    } while (0)

/* write value i to the elements [lo, hi] of a 1-d int dataset */
static void
write_range(hid_t d, hsize_t lo, hsize_t hi)
{
    hsize_t n = hi - lo + 1, start = lo;
    int    *v = malloc(sizeof(int) * n);
    for (hsize_t i = 0; i < n; i++)
        v[i] = (int)(lo + i);
    hid_t fs = H5Dget_space(d);
    hid_t ms = H5Screate_simple(1, &n, NULL);
    CHECK(H5Sselect_hyperslab(fs, H5S_SELECT_SET, &start, NULL, &n, NULL));
    CHECK(H5Dwrite(d, H5T_NATIVE_INT, ms, fs, H5P_DEFAULT, v));
    H5Sclose(ms);
    H5Sclose(fs);
    free(v);
}

static void
gen_cover(hid_t fapl)
{
    hid_t file = H5Fcreate("cover.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);
    CHECK(file);

    /* Sparse extensible array indexes */
    for (int filt = 0; filt < 2; filt++) {
        hsize_t dims = 300, maxd = H5S_UNLIMITED, chunk = 2;
        int     fill = -7;
        hid_t   s    = H5Screate_simple(1, &dims, &maxd);
        hid_t   dcpl = H5Pcreate(H5P_DATASET_CREATE);
        CHECK(H5Pset_chunk(dcpl, 1, &chunk));
        CHECK(H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill));
        if (filt)
            CHECK(H5Pset_deflate(dcpl, 6));
        hid_t d = H5Dcreate2(file, filt ? "ea_sparse_filt" : "ea_sparse", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl,
                             H5P_DEFAULT);
        CHECK(d);
        write_range(d, 0, 3);
        write_range(d, 100, 101);
        write_range(d, 298, 299);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* Paged fixed array indexes */
    for (int filt = 0; filt < 2; filt++) {
        hsize_t dims = 3000, chunk = 1;
        int     fill = -3;
        hid_t   s    = H5Screate_simple(1, &dims, NULL);
        hid_t   dcpl = H5Pcreate(H5P_DATASET_CREATE);
        CHECK(H5Pset_chunk(dcpl, 1, &chunk));
        CHECK(H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill));
        if (filt)
            CHECK(H5Pset_deflate(dcpl, 6));
        hid_t d = H5Dcreate2(file, filt ? "fa_paged_filt" : "fa_paged", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl,
                             H5P_DEFAULT);
        CHECK(d);
        write_range(d, 0, 0);
        write_range(d, 1500, 1500);
        write_range(d, 2999, 2999);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* Variable-length string fill values */
    {
        hid_t vstr = H5Tcopy(H5T_C_S1);
        CHECK(H5Tset_size(vstr, H5T_VARIABLE));
        {
            hsize_t     dims = 6, chunk = 2, two = 2, start = 0;
            const char *fill = "fill!";
            const char *v[2] = {"a", "bc"};
            hid_t       s    = H5Screate_simple(1, &dims, NULL);
            hid_t       dcpl = H5Pcreate(H5P_DATASET_CREATE);
            CHECK(H5Pset_chunk(dcpl, 1, &chunk));
            CHECK(H5Pset_fill_value(dcpl, vstr, &fill));
            hid_t d = H5Dcreate2(file, "vlen_str_fill", vstr, s, H5P_DEFAULT, dcpl, H5P_DEFAULT);
            CHECK(d);
            hid_t ms = H5Screate_simple(1, &two, NULL);
            CHECK(H5Sselect_hyperslab(s, H5S_SELECT_SET, &start, NULL, &two, NULL));
            CHECK(H5Dwrite(d, vstr, ms, s, H5P_DEFAULT, v));
            H5Sclose(ms);
            H5Dclose(d);
            H5Pclose(dcpl);
            H5Sclose(s);
        }
        {
            hsize_t     dims = 3;
            const char *fill = "abc";
            hid_t       s    = H5Screate_simple(1, &dims, NULL);
            hid_t       dcpl = H5Pcreate(H5P_DATASET_CREATE);
            CHECK(H5Pset_fill_value(dcpl, vstr, &fill));
            CHECK(H5Pset_alloc_time(dcpl, H5D_ALLOC_TIME_LATE));
            hid_t d = H5Dcreate2(file, "vlen_str_contig", vstr, s, H5P_DEFAULT, dcpl, H5P_DEFAULT);
            CHECK(d);
            H5Dclose(d);
            H5Pclose(dcpl);
            H5Sclose(s);
        }
        H5Tclose(vstr);
    }
    {
        hsize_t dims[2] = {2, 3};
        short   fill    = 11;
        hid_t   s       = H5Screate_simple(2, dims, NULL);
        hid_t   dcpl    = H5Pcreate(H5P_DATASET_CREATE);
        CHECK(H5Pset_fill_value(dcpl, H5T_NATIVE_SHORT, &fill));
        hid_t d = H5Dcreate2(file, "fixed_fill_contig", H5T_STD_I16LE, s, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        CHECK(d);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* Null and empty dataspaces */
    {
        hid_t s = H5Screate(H5S_NULL);
        hid_t d = H5Dcreate2(file, "null_space", H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        hid_t a = H5Acreate2(d, "null_attr", H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(a);
        H5Aclose(a);
        hid_t sc = H5Screate(H5S_SCALAR);
        int   five = 5;
        a          = H5Acreate2(d, "scalar_attr", H5T_STD_I32LE, sc, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(H5Awrite(a, H5T_NATIVE_INT, &five));
        H5Aclose(a);
        H5Sclose(sc);
        H5Dclose(d);
        H5Sclose(s);
    }
    {
        hsize_t dims[2] = {0, 3}, maxd[2] = {H5S_UNLIMITED, 3}, chunk[2] = {4, 3};
        hid_t   s    = H5Screate_simple(2, dims, maxd);
        hid_t   dcpl = H5Pcreate(H5P_DATASET_CREATE);
        CHECK(H5Pset_chunk(dcpl, 2, chunk));
        hid_t d = H5Dcreate2(file, "empty_2d", H5T_STD_I32LE, s, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        CHECK(d);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(s);
    }

    /* Region and object references */
    hid_t target;
    {
        hsize_t dims[2] = {6, 8};
        int     v[48];
        for (int i = 0; i < 48; i++)
            v[i] = i;
        hid_t s = H5Screate_simple(2, dims, NULL);
        target  = H5Dcreate2(file, "target", H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(target);
        CHECK(H5Dwrite(target, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v));
        H5Sclose(s);
    }
    /* Committed datatype */
    {
        hid_t ct = H5Tcreate(H5T_COMPOUND, 12);
        CHECK(H5Tinsert(ct, "a", 0, H5T_STD_I32LE));
        CHECK(H5Tinsert(ct, "b", 4, H5T_IEEE_F64LE));
        CHECK(H5Tcommit2(file, "dtype", ct, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT));
        hid_t sc = H5Screate(H5S_SCALAR);
        hid_t st = H5Tcopy(H5T_C_S1);
        H5Tset_size(st, 9);
        hid_t a = H5Acreate2(ct, "note", st, sc, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(H5Awrite(a, st, "committed"));
        H5Aclose(a);
        H5Tclose(st);
        H5Sclose(sc);

        struct {
            int    a;
            double b;
        } v[2] = {{1, 0.5}, {2, 1.5}};
        hid_t mt = H5Tcreate(H5T_COMPOUND, sizeof v[0]);
        H5Tinsert(mt, "a", 0, H5T_NATIVE_INT);
        H5Tinsert(mt, "b", sizeof(double), H5T_NATIVE_DOUBLE);
        hsize_t n = 2;
        hid_t   s = H5Screate_simple(1, &n, NULL);
        hid_t   d = H5Dcreate2(file, "typed", ct, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        CHECK(H5Dwrite(d, mt, H5S_ALL, H5S_ALL, H5P_DEFAULT, v));
        H5Dclose(d);
        H5Sclose(s);
        H5Tclose(mt);
        H5Tclose(ct);
    }
    {
        /* the regions: 0 points, 1 hyperslab, 2 none, 3 all, 4 one point */
        hid_t   regs[5];
        hsize_t pts[3][2] = {{1, 2}, {3, 4}, {5, 7}}, one[1][2] = {{0, 0}};
        hsize_t start[2] = {1, 1}, stride[2] = {2, 3}, count[2] = {2, 2};
        for (int i = 0; i < 5; i++)
            regs[i] = H5Dget_space(target);
        CHECK(H5Sselect_elements(regs[0], H5S_SELECT_SET, 3, &pts[0][0]));
        CHECK(H5Sselect_hyperslab(regs[1], H5S_SELECT_SET, start, stride, count, NULL));
        CHECK(H5Sselect_none(regs[2]));
        CHECK(H5Sselect_all(regs[3]));
        CHECK(H5Sselect_elements(regs[4], H5S_SELECT_SET, 1, &one[0][0]));

        hdset_reg_ref_t old[5];
        for (int i = 0; i < 5; i++)
            CHECK(H5Rcreate(&old[i], file, "target", H5R_DATASET_REGION, regs[i]));
        hsize_t n = 5;
        hid_t   s = H5Screate_simple(1, &n, NULL);
        hid_t   d = H5Dcreate2(file, "regref_old", H5T_STD_REF_DSETREG, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        CHECK(H5Dwrite(d, H5T_STD_REF_DSETREG, H5S_ALL, H5S_ALL, H5P_DEFAULT, old));
        H5Dclose(d);
        H5Sclose(s);

        H5R_ref_t nr[7];
        for (int i = 0; i < 5; i++)
            CHECK(H5Rcreate_region(file, "target", regs[i], H5P_DEFAULT, &nr[i]));
        CHECK(H5Rcreate_object(file, "target", H5P_DEFAULT, &nr[5]));
        CHECK(H5Rcreate_attr(file, "null_space", "scalar_attr", H5P_DEFAULT, &nr[6]));
        n = 7;
        s = H5Screate_simple(1, &n, NULL);
        d = H5Dcreate2(file, "regref_new", H5T_STD_REF, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        CHECK(H5Dwrite(d, H5T_STD_REF, H5S_ALL, H5S_ALL, H5P_DEFAULT, nr));
        H5Dclose(d);
        H5Sclose(s);
        for (int i = 0; i < 7; i++)
            H5Rdestroy(&nr[i]);
        for (int i = 0; i < 5; i++)
            H5Sclose(regs[i]);

        hobj_ref_t obj[2];
        CHECK(H5Rcreate(&obj[0], file, "target", H5R_OBJECT, -1));
        CHECK(H5Rcreate(&obj[1], file, "dtype", H5R_OBJECT, -1));
        n = 2;
        s = H5Screate_simple(1, &n, NULL);
        d = H5Dcreate2(file, "objref_old", H5T_STD_REF_OBJ, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        CHECK(H5Dwrite(d, H5T_STD_REF_OBJ, H5S_ALL, H5S_ALL, H5P_DEFAULT, obj));
        H5Dclose(d);
        H5Sclose(s);
    }

    /* Virtual datasets with "all" selections */
    {
        hsize_t dims[2] = {6, 8};
        hid_t   vs      = H5Screate_simple(2, dims, NULL);
        hid_t   ss      = H5Screate_simple(2, dims, NULL);
        hid_t   dcpl    = H5Pcreate(H5P_DATASET_CREATE);
        CHECK(H5Pset_virtual(dcpl, vs, ".", "/target", ss));
        hid_t d = H5Dcreate2(file, "vds_all", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        CHECK(d);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(vs);
        H5Sclose(ss);
    }
    {
        hsize_t vdims[2] = {2, 8}, sdims[2] = {6, 8}, start[2] = {3, 0}, cnt[2] = {2, 8};
        hid_t   vs   = H5Screate_simple(2, vdims, NULL);
        hid_t   ss   = H5Screate_simple(2, sdims, NULL);
        hid_t   dcpl = H5Pcreate(H5P_DATASET_CREATE);
        CHECK(H5Sselect_hyperslab(ss, H5S_SELECT_SET, start, NULL, cnt, NULL));
        CHECK(H5Pset_virtual(dcpl, vs, ".", "/target", ss));
        hid_t d = H5Dcreate2(file, "vds_all_src", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        CHECK(d);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(vs);
        H5Sclose(ss);
    }
    {
        hsize_t sdims[2] = {2, 8}, vdims[2] = {4, 8}, start[2] = {1, 0}, cnt[2] = {2, 8};
        int     v[16], fill = -1;
        for (int i = 0; i < 16; i++)
            v[i] = 100 + i;
        hid_t ss = H5Screate_simple(2, sdims, NULL);
        hid_t d  = H5Dcreate2(file, "target_rows", H5T_STD_I32LE, ss, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        CHECK(H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v));
        H5Dclose(d);
        hid_t vs   = H5Screate_simple(2, vdims, NULL);
        hid_t dcpl = H5Pcreate(H5P_DATASET_CREATE);
        CHECK(H5Pset_fill_value(dcpl, H5T_NATIVE_INT, &fill));
        CHECK(H5Sselect_hyperslab(vs, H5S_SELECT_SET, start, NULL, cnt, NULL));
        CHECK(H5Pset_virtual(dcpl, vs, ".", "/target_rows", ss));
        d = H5Dcreate2(file, "vds_src_all", H5T_STD_I32LE, vs, H5P_DEFAULT, dcpl, H5P_DEFAULT);
        CHECK(d);
        H5Dclose(d);
        H5Pclose(dcpl);
        H5Sclose(vs);
        H5Sclose(ss);
    }

    /* Group attributes */
    {
        hid_t   g = H5Gcreate2(file, "group_attrs", H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        hsize_t two = 2;
        int     x[2] = {1, 2};
        double  y    = 2.5;
        hid_t   s    = H5Screate_simple(1, &two, NULL);
        hid_t   a    = H5Acreate2(g, "x", H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(H5Awrite(a, H5T_NATIVE_INT, x));
        H5Aclose(a);
        H5Sclose(s);
        s = H5Screate(H5S_SCALAR);
        a = H5Acreate2(g, "y", H5T_IEEE_F64LE, s, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(H5Awrite(a, H5T_NATIVE_DOUBLE, &y));
        H5Aclose(a);
        H5Sclose(s);
        H5Gclose(g);
    }

    H5Dclose(target);
    CHECK(H5Fclose(file));
}

static void
gen_sohm(hid_t fapl)
{
    hid_t fcpl = H5Pcreate(H5P_FILE_CREATE);
    CHECK(H5Pset_shared_mesg_nindexes(fcpl, 2));
    CHECK(H5Pset_shared_mesg_index(fcpl, 0, H5O_SHMESG_DTYPE_FLAG | H5O_SHMESG_SDSPACE_FLAG, 1));
    CHECK(H5Pset_shared_mesg_index(fcpl, 1, H5O_SHMESG_ATTR_FLAG, 1));
    CHECK(H5Pset_shared_mesg_phase_change(fcpl, 0, 0));
    hid_t file = H5Fcreate("cover_sohm.h5", H5F_ACC_TRUNC, fcpl, fapl);
    CHECK(file);
    hid_t ct = H5Tcopy(H5T_STD_I32LE);
    CHECK(H5Tcommit2(file, "shared_type", ct, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT));
    for (int i = 0; i < 12; i++) {
        char    name[8];
        hsize_t n = (hsize_t)i + 1, two = 2;
        int     v[12], av[2] = {i, -i};
        for (int j = 0; j < 12; j++)
            v[j] = i * 10 + j;
        hid_t s = H5Screate_simple(1, &n, NULL);
        snprintf(name, sizeof name, "d%02d", i);
        hid_t d = H5Dcreate2(file, name, H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        CHECK(H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v));
        hid_t as = H5Screate_simple(1, &two, NULL);
        hid_t a  = H5Acreate2(d, "a", H5T_STD_I32LE, as, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(H5Awrite(a, H5T_NATIVE_INT, av));
        H5Aclose(a);
        H5Sclose(as);
        H5Dclose(d);
        snprintf(name, sizeof name, "e%02d", i);
        d = H5Dcreate2(file, name, ct, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        CHECK(d);
        CHECK(H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v));
        H5Dclose(d);
        H5Sclose(s);
    }
    H5Tclose(ct);
    H5Pclose(fcpl);
    CHECK(H5Fclose(file));
}

/* A family file in the latest format: the driver info is a message of the
 * superblock extension */
static void
gen_family(void)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    CHECK(H5Pset_fapl_family(fapl, 2048, H5P_DEFAULT));
    CHECK(H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST));
    hid_t file = H5Fcreate("cover_fam%05d.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);
    CHECK(file);
    hsize_t n = 300;
    int     v[300];
    for (int i = 0; i < 300; i++)
        v[i] = 3 * i;
    hid_t s = H5Screate_simple(1, &n, NULL);
    hid_t d = H5Dcreate2(file, "x", H5T_STD_I32LE, s, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
    CHECK(d);
    CHECK(H5Dwrite(d, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, v));
    H5Dclose(d);
    H5Sclose(s);
    CHECK(H5Fclose(file));
    H5Pclose(fapl);
}

int
main(void)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    CHECK(H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST));
    gen_cover(fapl);
    gen_sohm(fapl);
    gen_family();
    H5Pclose(fapl);
    return 0;
}
