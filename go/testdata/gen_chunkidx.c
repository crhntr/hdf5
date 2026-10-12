/*
 * Generator for chunkidx.h5 and chunkidx_expected.txt (the repository corpus
 * has no extensible array with paged data blocks, no filtered extensible
 * array, no extensible array whose unlimited dimension is not the first one
 * and no fixed array with uninitialized data block pages).
 *
 *   h5cc -o /tmp/gen_chunkidx gen_chunkidx.c && (cd go/testdata && /tmp/gen_chunkidx)
 *
 * Datasets (all created with the latest file format, so chunked datasets
 * with one unlimited dimension use an extensible array index and chunked
 * datasets with fixed maximum dimensions use a fixed array index):
 *
 *   /ea_paged      int8 {400000}/{UNLIM}, chunk {1}: chunks 0..5, 20, 100,
 *                  140000, 140001, 141000, 141500 and 300000 written
 *                  (chunks >= 131060 are in super blocks whose 2048-element
 *                  data blocks are paged; data block 4 of super block 13 has
 *                  both pages initialized, data block 5 only its first).
 *   /ea_filt_unlim1 int32 {6,10}/{6,UNLIM}, chunk {2,3}, deflate: all chunks
 *                  written except (scaled) {1,2}.
 *   /ea_unlim_mid  int16 {4,5,6}/{4,UNLIM,6}, chunk {2,2,3}: every chunk
 *                  written except (scaled) {0,1,1} and {1,2,0}.
 *   /fa_paged      int8 {3000}/{3000}, chunk {1}: chunks 5 and 2999 written
 *                  (3 pages, page 1 never initialized, last page holds 952).
 *   /fa_filt_paged int32 {2100}/{2100}, chunk {1}, deflate: chunks 0..10 and
 *                  2050 written.
 *
 * chunkidx_expected.txt lists, for every dataset, the object header address
 * and then one line per chunk inside the current extent (for /ea_paged:
 * chunks 0..119 and the 20 chunks around each chunk written past 139990):
 *   chunk <scaled coords, comma-separated> UNDEF
 *   chunk <scaled coords, comma-separated> <size> <filter mask> <raw chunk bytes, hex>
 * from H5Dget_chunk_storage_size and H5Dread_chunk (which look the chunk up
 * with the index's get_addr callback), followed by the chunks reported by
 * H5Dchunk_iter ("iter" lines: scaled coords, address, size, filter mask).
 *
 * (H5Dget_chunk_info_by_coord and H5Dchunk_iter are not used for the
 * expected coordinates: in HDF5 1.14.6 both derive the coordinates of a
 * chunk from H5D__earray_idx_iterate, which steps them in the wrong order
 * when the unlimited dimension is not the first one, so for
 * /ea_filt_unlim1 and /ea_unlim_mid the "iter" coordinates are wrong.)
 */
#include "hdf5.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static FILE *out;

static hid_t
mkdset(hid_t file, const char *name, hid_t type, int rank, const hsize_t *dims, const hsize_t *maxdims,
       const hsize_t *chunk, int deflate)
{
    hid_t space = H5Screate_simple(rank, dims, maxdims);
    hid_t dcpl  = H5Pcreate(H5P_DATASET_CREATE);
    H5Pset_chunk(dcpl, rank, chunk);
    if (deflate)
        H5Pset_deflate(dcpl, 6);
    hid_t dset = H5Dcreate2(file, name, type, space, H5P_DEFAULT, dcpl, H5P_DEFAULT);
    H5Pclose(dcpl);
    H5Sclose(space);
    return dset;
}

/* Write the chunk at scaled coordinates sc (whole chunk, value v). */
static void
write_chunk(hid_t dset, hid_t memtype, size_t eltsize, int rank, const hsize_t *chunk, const hsize_t *sc,
            int v)
{
    hsize_t start[8], count[8], dims[8], n = 1;
    hid_t   fspace = H5Dget_space(dset);
    H5Sget_simple_extent_dims(fspace, dims, NULL);
    for (int i = 0; i < rank; i++) {
        start[i] = sc[i] * chunk[i];
        count[i] = chunk[i];
        if (start[i] + count[i] > dims[i]) /* (partial edge chunk) */
            count[i] = dims[i] - start[i];
        n *= count[i];
    }
    H5Sselect_hyperslab(fspace, H5S_SELECT_SET, start, NULL, count, NULL);
    hid_t          mspace = H5Screate_simple(rank, count, NULL);
    unsigned char *buf    = malloc(n * eltsize);
    for (hsize_t i = 0; i < n * eltsize; i++)
        buf[i] = (unsigned char)(v + (int)i);
    H5Dwrite(dset, memtype, mspace, fspace, H5P_DEFAULT, buf);
    free(buf);
    H5Sclose(mspace);
    H5Sclose(fspace);
}

static herr_t
iter_cb(const hsize_t *offset, unsigned filter_mask, haddr_t addr, hsize_t size, void *op_data)
{
    const hsize_t *chunk = ((const hsize_t **)op_data)[0];
    int            rank  = (int)(size_t)((const hsize_t **)op_data)[1];
    fprintf(out, "iter ");
    for (int i = 0; i < rank; i++)
        fprintf(out, "%s%llu", i ? "," : "", (unsigned long long)(offset[i] / chunk[i]));
    fprintf(out, " %llu %llu %u\n", (unsigned long long)addr, (unsigned long long)size, filter_mask);
    return 0;
}

static void
dump(hid_t file, const char *name, int rank, const hsize_t *chunk, const hsize_t *probe, size_t nprobe)
{
    hid_t       dset = H5Dopen2(file, name, H5P_DEFAULT);
    hid_t       space = H5Dget_space(dset);
    hsize_t     dims[8];
    H5O_info2_t oinfo;
    haddr_t     ohdr = HADDR_UNDEF;

    H5Sget_simple_extent_dims(space, dims, NULL);
    H5Oget_info3(dset, &oinfo, H5O_INFO_BASIC);
    H5VLnative_token_to_addr(file, oinfo.token, &ohdr);
    fprintf(out, "dataset %s %llu\n", name, (unsigned long long)ohdr);

    hsize_t nch[8], total = 1;
    for (int i = 0; i < rank; i++) {
        nch[i] = (dims[i] + chunk[i] - 1) / chunk[i];
        total *= nch[i];
    }
    if (probe)
        total = nprobe;
    for (hsize_t c = 0; c < total; c++) {
        hsize_t sc[8], off[8], rem = probe ? probe[c] : c;
        for (int i = rank - 1; i >= 0; i--) {
            sc[i] = rem % nch[i];
            rem /= nch[i];
            off[i] = sc[i] * chunk[i];
        }
        hsize_t size = 0;
        fprintf(out, "chunk ");
        for (int i = 0; i < rank; i++)
            fprintf(out, "%s%llu", i ? "," : "", (unsigned long long)sc[i]);
        H5E_BEGIN_TRY
        {
            if (H5Dget_chunk_storage_size(dset, off, &size) < 0)
                size = 0;
        }
        H5E_END_TRY
        if (size == 0) {
            fprintf(out, " UNDEF\n");
            continue;
        }
        uint32_t       mask = 0;
        unsigned char *buf  = malloc(size);
        if (H5Dread_chunk(dset, H5P_DEFAULT, off, &mask, buf) < 0)
            exit(1);
        fprintf(out, " %llu %u ", (unsigned long long)size, (unsigned)mask);
        for (hsize_t i = 0; i < size; i++)
            fprintf(out, "%02x", buf[i]);
        fprintf(out, "\n");
        free(buf);
    }

    const hsize_t *op_data[2] = {chunk, (const hsize_t *)(size_t)rank};
    H5Dchunk_iter(dset, H5P_DEFAULT, iter_cb, op_data);

    H5Sclose(space);
    H5Dclose(dset);
}

int
main(void)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST);
    hid_t file = H5Fcreate("chunkidx.h5", H5F_ACC_TRUNC, H5P_DEFAULT, fapl);

    /* /ea_paged */
    {
        hsize_t dims[1] = {400000}, maxdims[1] = {H5S_UNLIMITED}, chunk[1] = {1};
        hid_t   d = mkdset(file, "ea_paged", H5T_NATIVE_INT8, 1, dims, maxdims, chunk, 0);
        hsize_t which[] = {0, 1, 2, 3, 4, 5, 20, 100, 140000, 140001, 141000, 141500, 300000};
        for (size_t i = 0; i < sizeof(which) / sizeof(which[0]); i++)
            write_chunk(d, H5T_NATIVE_INT8, 1, 1, chunk, &which[i], (int)i);
        H5Dclose(d);
    }

    /* /ea_filt_unlim1 */
    {
        hsize_t dims[2] = {6, 10}, maxdims[2] = {6, H5S_UNLIMITED}, chunk[2] = {2, 3};
        hid_t   d = mkdset(file, "ea_filt_unlim1", H5T_NATIVE_INT32, 2, dims, maxdims, chunk, 1);
        for (hsize_t i = 0; i < 3; i++)
            for (hsize_t j = 0; j < 4; j++) {
                hsize_t sc[2] = {i, j};
                if (i == 1 && j == 2)
                    continue;
                write_chunk(d, H5T_NATIVE_INT32, 4, 2, chunk, sc, (int)(i * 10 + j));
            }
        H5Dclose(d);
    }

    /* /ea_unlim_mid */
    {
        hsize_t dims[3] = {4, 5, 6}, maxdims[3] = {4, H5S_UNLIMITED, 6}, chunk[3] = {2, 2, 3};
        hid_t   d = mkdset(file, "ea_unlim_mid", H5T_NATIVE_INT16, 3, dims, maxdims, chunk, 0);
        for (hsize_t i = 0; i < 2; i++)
            for (hsize_t j = 0; j < 3; j++)
                for (hsize_t k = 0; k < 2; k++) {
                    hsize_t sc[3] = {i, j, k};
                    if ((i == 0 && j == 1 && k == 1) || (i == 1 && j == 2 && k == 0))
                        continue;
                    write_chunk(d, H5T_NATIVE_INT16, 2, 3, chunk, sc, (int)(i * 100 + j * 10 + k));
                }
        H5Dclose(d);
    }

    /* /fa_paged */
    {
        hsize_t dims[1] = {3000}, chunk[1] = {1};
        hid_t   d = mkdset(file, "fa_paged", H5T_NATIVE_INT8, 1, dims, dims, chunk, 0);
        hsize_t which[] = {5, 2999};
        for (size_t i = 0; i < sizeof(which) / sizeof(which[0]); i++)
            write_chunk(d, H5T_NATIVE_INT8, 1, 1, chunk, &which[i], (int)i + 1);
        H5Dclose(d);
    }

    /* /fa_filt_paged */
    {
        hsize_t dims[1] = {2100}, chunk[1] = {1};
        hid_t   d = mkdset(file, "fa_filt_paged", H5T_NATIVE_INT32, 1, dims, dims, chunk, 1);
        for (hsize_t i = 0; i <= 10; i++)
            write_chunk(d, H5T_NATIVE_INT32, 4, 1, chunk, &i, (int)i);
        hsize_t last = 2050;
        write_chunk(d, H5T_NATIVE_INT32, 4, 1, chunk, &last, 77);
        H5Dclose(d);
    }

    H5Fclose(file);
    H5Pclose(fapl);

    /* Dump the expected chunk information */
    out  = fopen("chunkidx_expected.txt", "w");
    file = H5Fopen("chunkidx.h5", H5F_ACC_RDONLY, H5P_DEFAULT);
    {
        hsize_t c1[1] = {1}, c2[2] = {2, 3}, c3[3] = {2, 2, 3};
        /* (probe the interesting parts of the 400000-chunk /ea_paged only) */
        hsize_t probe[4096];
        size_t  nprobe = 0;
        for (hsize_t i = 0; i < 120; i++)
            probe[nprobe++] = i;
        for (hsize_t i = 139990; i < 140010; i++)
            probe[nprobe++] = i;
        for (hsize_t i = 140990; i < 141010; i++)
            probe[nprobe++] = i;
        for (hsize_t i = 141490; i < 141510; i++)
            probe[nprobe++] = i;
        for (hsize_t i = 299990; i < 300010; i++)
            probe[nprobe++] = i;
        dump(file, "ea_paged", 1, c1, probe, nprobe);
        dump(file, "ea_filt_unlim1", 2, c2, NULL, 0);
        dump(file, "ea_unlim_mid", 3, c3, NULL, 0);
        dump(file, "fa_paged", 1, c1, NULL, 0);
        dump(file, "fa_filt_paged", 1, c1, NULL, 0);
    }
    H5Fclose(file);
    fclose(out);
    return 0;
}
