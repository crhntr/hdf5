/*
 * Generator for the fractal heap test files in this directory (the
 * repository corpus has no heap with 'huge' objects or I/O filters).
 *
 *   h5cc -o /tmp/fheap_gen fheap_gen.c && (cd go/hdf5/testdata && /tmp/fheap_gen)
 *
 * fheap_huge.h5:     group "/dense" with dense link storage: soft links
 *                    "link000".."link199" -> "/target000".., a soft link
 *                    "longvalue" whose value is 6000 'v's and a soft link
 *                    named with 5000 'n's (both stored as 'huge' heap
 *                    objects), and dense attributes "a00".."a09" (int32
 *                    scalars i*i) plus "big" (8000 uint8, value i%251,
 *                    a 'huge' heap object).
 * fheap_filtered.h5: group "/filtered" created with the deflate filter and
 *                    dense link storage (the link heap is filtered): soft
 *                    links "link000".."link299" -> "/target000".. and a
 *                    soft link named with 5000 'n's.
 */
#include "hdf5.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static char *
repeat(char c, size_t n)
{
    char *s = malloc(n + 1);
    memset(s, c, n);
    s[n] = '\0';
    return s;
}

int
main(void)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST);

    /* fheap_huge.h5 */
    {
        hid_t f    = H5Fcreate("fheap_huge.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);
        hid_t gcpl = H5Pcreate(H5P_GROUP_CREATE);
        H5Pset_link_phase_change(gcpl, 0, 0);
        H5Pset_attr_phase_change(gcpl, 0, 0);
        hid_t g = H5Gcreate2(f, "dense", H5P_DEFAULT, gcpl, H5P_DEFAULT);
        char  name[64], target[64];
        for (int i = 0; i < 200; i++) {
            snprintf(name, sizeof(name), "link%03d", i);
            snprintf(target, sizeof(target), "/target%03d", i);
            H5Lcreate_soft(target, g, name, H5P_DEFAULT, H5P_DEFAULT);
        }
        char *longvalue = repeat('v', 6000);
        H5Lcreate_soft(longvalue, g, "longvalue", H5P_DEFAULT, H5P_DEFAULT);
        char *longname = repeat('n', 5000);
        H5Lcreate_soft("/short", g, longname, H5P_DEFAULT, H5P_DEFAULT);

        hid_t scalar = H5Screate(H5S_SCALAR);
        for (int i = 0; i < 10; i++) {
            int v = i * i;
            snprintf(name, sizeof(name), "a%02d", i);
            hid_t a = H5Acreate2(g, name, H5T_NATIVE_INT32, scalar, H5P_DEFAULT, H5P_DEFAULT);
            H5Awrite(a, H5T_NATIVE_INT32, &v);
            H5Aclose(a);
        }
        hsize_t        dims[1] = {8000};
        hid_t          space   = H5Screate_simple(1, dims, NULL);
        unsigned char *big     = malloc(8000);
        for (int i = 0; i < 8000; i++)
            big[i] = (unsigned char)(i % 251);
        hid_t a = H5Acreate2(g, "big", H5T_NATIVE_UINT8, space, H5P_DEFAULT, H5P_DEFAULT);
        H5Awrite(a, H5T_NATIVE_UINT8, big);
        H5Aclose(a);

        H5Sclose(space);
        H5Sclose(scalar);
        H5Gclose(g);
        H5Pclose(gcpl);
        H5Fclose(f);
        free(big);
        free(longvalue);
        free(longname);
    }

    /* fheap_filtered.h5 */
    {
        hid_t f    = H5Fcreate("fheap_filtered.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);
        hid_t gcpl = H5Pcreate(H5P_GROUP_CREATE);
        H5Pset_link_phase_change(gcpl, 0, 0);
        H5Pset_deflate(gcpl, 6);
        hid_t g = H5Gcreate2(f, "filtered", H5P_DEFAULT, gcpl, H5P_DEFAULT);
        char  name[64], target[64];
        for (int i = 0; i < 300; i++) {
            snprintf(name, sizeof(name), "link%03d", i);
            snprintf(target, sizeof(target), "/target%03d", i);
            H5Lcreate_soft(target, g, name, H5P_DEFAULT, H5P_DEFAULT);
        }
        char *longname = repeat('n', 5000);
        H5Lcreate_soft("/short", g, longname, H5P_DEFAULT, H5P_DEFAULT);
        H5Gclose(g);
        H5Pclose(gcpl);
        H5Fclose(f);
        free(longname);
    }

    H5Pclose(fapl);
    return 0;
}
