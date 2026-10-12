/*
 * Generator for the attribute / shared message test files in this
 * directory (the repository corpus has a single object with dense
 * attribute storage, no attribute stored in the shared object header
 * message heap and no list-type shared message index with attributes).
 *
 *   h5cc -o /tmp/attr_gen attr_gen.c && (cd go/testdata && /tmp/attr_gen)
 *
 * Both files use the latest format. Attribute "attr_KK" created at step i
 * (0-based) has KK = (i * 7) % N, an int32 scalar value 1000 + KK, so that
 * name order and creation order differ.
 *
 * attr_dense.h5 (no shared messages):
 *   group "/tracked":   N = 30 dense attributes, creation order tracked but
 *                       not indexed (no creation order v2 B-tree);
 *   group "/indexed":   N = 30 dense attributes, creation order tracked and
 *                       indexed;
 *   group "/untracked": N = 20 dense attributes, creation order not tracked;
 *   group "/compact":   N = 5 compact attributes, creation order tracked;
 *   group "/compact_untracked": N = 5 compact attributes, not tracked.
 *
 * attr_sohm.h5: shared object header messages, index 0 for attributes,
 *   index 1 for datatypes and dataspaces, phase change list max 8 / B-tree
 *   min 6, minimum message size 0 (every message is shared):
 *   group "/dense":     N = 30 dense attributes, creation order tracked and
 *                       indexed (attribute index is a v2 B-tree);
 *   group "/compact":   N = 3 compact attributes "attr_00".."attr_02" (the
 *                       same messages as those of "/dense": reference count
 *                       2), plus "vlstr" (vlen string scalar "shared vlen
 *                       string") and "cmpd" (compound {int32 a; double b}
 *                       with 2 elements {1, 0.5}, {2, 1.5});
 *   dataset "/dset":    int32 4x5 dataset, value i, with attribute "attr_00".
 *   The datatype/dataspace index holds 6 messages (int32, vlen string and
 *   compound datatypes; scalar, (2) and (4, 5) dataspaces): a list; the
 *   attribute index holds 32: a v2 B-tree.
 */
#include "hdf5.h"
#include <stdio.h>
#include <stdlib.h>

static void
check(herr_t status, const char *what)
{
    if (status < 0) {
        fprintf(stderr, "%s failed\n", what);
        exit(1);
    }
}

static void
add_int_attrs(hid_t obj, int n, int first, int count)
{
    for (int i = first; i < first + count; i++) {
        char  name[32];
        int   k     = (i * 7) % n;
        int   value = 1000 + k;
        hid_t space = H5Screate(H5S_SCALAR);
        snprintf(name, sizeof(name), "attr_%02d", k);
        hid_t attr = H5Acreate2(obj, name, H5T_STD_I32LE, space, H5P_DEFAULT, H5P_DEFAULT);
        check(attr, "H5Acreate2");
        check(H5Awrite(attr, H5T_NATIVE_INT, &value), "H5Awrite");
        H5Aclose(attr);
        H5Sclose(space);
    }
}

static void
make_group(hid_t file, const char *name, unsigned corder, int n)
{
    hid_t gcpl = H5Pcreate(H5P_GROUP_CREATE);
    if (corder)
        check(H5Pset_attr_creation_order(gcpl, corder), "H5Pset_attr_creation_order");
    hid_t g = H5Gcreate2(file, name, H5P_DEFAULT, gcpl, H5P_DEFAULT);
    check(g, "H5Gcreate2");
    add_int_attrs(g, n, 0, n);
    H5Gclose(g);
    H5Pclose(gcpl);
}

int
main(void)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    check(H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST), "H5Pset_libver_bounds");

    /* attr_dense.h5 */
    hid_t file = H5Fcreate("attr_dense.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);
    check(file, "H5Fcreate");
    make_group(file, "tracked", H5P_CRT_ORDER_TRACKED, 30);
    make_group(file, "indexed", H5P_CRT_ORDER_TRACKED | H5P_CRT_ORDER_INDEXED, 30);
    make_group(file, "untracked", 0, 20);
    make_group(file, "compact", H5P_CRT_ORDER_TRACKED, 5);
    make_group(file, "compact_untracked", 0, 5);
    H5Fclose(file);

    /* attr_sohm.h5 */
    hid_t fcpl = H5Pcreate(H5P_FILE_CREATE);
    check(H5Pset_shared_mesg_nindexes(fcpl, 2), "H5Pset_shared_mesg_nindexes");
    check(H5Pset_shared_mesg_index(fcpl, 0, H5O_SHMESG_ATTR_FLAG, 0), "H5Pset_shared_mesg_index");
    check(H5Pset_shared_mesg_index(fcpl, 1, H5O_SHMESG_DTYPE_FLAG | H5O_SHMESG_SDSPACE_FLAG, 0),
          "H5Pset_shared_mesg_index");
    check(H5Pset_shared_mesg_phase_change(fcpl, 8, 6), "H5Pset_shared_mesg_phase_change");
    file = H5Fcreate("attr_sohm.h5", H5F_ACC_TRUNC, fcpl, fapl);
    check(file, "H5Fcreate");
    make_group(file, "dense", H5P_CRT_ORDER_TRACKED | H5P_CRT_ORDER_INDEXED, 30);

    hid_t g = H5Gcreate2(file, "compact", H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
    check(g, "H5Gcreate2");
    {
        /* attr_00, attr_07, attr_14 at steps 0..2 with n = 30: the same messages as in /dense */
        add_int_attrs(g, 30, 0, 3);

        hid_t       space = H5Screate(H5S_SCALAR);
        hid_t       vls   = H5Tcopy(H5T_C_S1);
        const char *str   = "shared vlen string";
        check(H5Tset_size(vls, H5T_VARIABLE), "H5Tset_size");
        hid_t attr = H5Acreate2(g, "vlstr", vls, space, H5P_DEFAULT, H5P_DEFAULT);
        check(attr, "H5Acreate2");
        check(H5Awrite(attr, vls, &str), "H5Awrite");
        H5Aclose(attr);
        H5Tclose(vls);
        H5Sclose(space);

        typedef struct {
            int    a;
            double b;
        } cmpd_t;
        cmpd_t  data[2] = {{1, 0.5}, {2, 1.5}};
        hsize_t dims[1] = {2};
        hid_t   mt      = H5Tcreate(H5T_COMPOUND, sizeof(cmpd_t));
        H5Tinsert(mt, "a", HOFFSET(cmpd_t, a), H5T_NATIVE_INT);
        H5Tinsert(mt, "b", HOFFSET(cmpd_t, b), H5T_NATIVE_DOUBLE);
        hid_t ft = H5Tcreate(H5T_COMPOUND, 12);
        H5Tinsert(ft, "a", 0, H5T_STD_I32LE);
        H5Tinsert(ft, "b", 4, H5T_IEEE_F64LE);
        space = H5Screate_simple(1, dims, NULL);
        attr  = H5Acreate2(g, "cmpd", ft, space, H5P_DEFAULT, H5P_DEFAULT);
        check(attr, "H5Acreate2");
        check(H5Awrite(attr, mt, data), "H5Awrite");
        H5Aclose(attr);
        H5Sclose(space);
        H5Tclose(ft);
        H5Tclose(mt);
    }
    H5Gclose(g);

    {
        hsize_t dims[2] = {4, 5};
        int     buf[20];
        for (int i = 0; i < 20; i++)
            buf[i] = i;
        hid_t space = H5Screate_simple(2, dims, NULL);
        hid_t dset  = H5Dcreate2(file, "dset", H5T_STD_I32LE, space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT);
        check(dset, "H5Dcreate2");
        check(H5Dwrite(dset, H5T_NATIVE_INT, H5S_ALL, H5S_ALL, H5P_DEFAULT, buf), "H5Dwrite");
        add_int_attrs(dset, 30, 0, 1);
        H5Dclose(dset);
        H5Sclose(space);
    }
    H5Fclose(file);
    H5Pclose(fcpl);
    H5Pclose(fapl);
    return 0;
}
