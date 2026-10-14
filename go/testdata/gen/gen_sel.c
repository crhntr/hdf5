/* Generates testdata/h5s_selections.json: dataspace selections encoded with
 * H5Sencode2 (earliest and latest format) together with what the C library
 * reports about them (npoints, bounds, block/point lists, the
 * H5Ssel_iter_get_seq_list sequences), plus project_intersection,
 * shape_same and intersect_block results, selection offsets
 * (H5Soffset_simple), the clipping of unlimited selections and a few
 * extent facts.
 *
 *   h5cc -o /tmp/gen_sel gen_sel.c && /tmp/gen_sel > ../h5s_selections.json
 *
 * Some of the facts come from functions of the library that are not part
 * of the public API but are exported by libhdf5 (declared below); they are
 * the C functions the Go code translates.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "hdf5.h"

/* Library-internal functions (src/H5Iprivate.h, src/H5Sprivate.h) */
typedef struct H5S_t H5S_t;
extern void   *H5I_object(hid_t id);
extern htri_t  H5S_select_is_contiguous(const H5S_t *space);
extern htri_t  H5S_select_is_single(const H5S_t *space);
extern herr_t  H5S_get_select_offset(const H5S_t *space, hsize_t *offset);
extern herr_t  H5S_hyper_clip_unlim(H5S_t *space, hsize_t clip_size);
extern hsize_t H5S_hyper_get_first_inc_block(const H5S_t *space, hsize_t clip_size, bool *partial);
extern hsize_t H5S_get_npoints_max(const H5S_t *ds);
extern htri_t  H5S_extent_equal(const H5S_t *ds1, const H5S_t *ds2);

#define CHECK(x)                                                                                             \
    do {                                                                                                     \
        if ((x) < 0) {                                                                                       \
            fprintf(stderr, "failed: %s (line %d)\n", #x, __LINE__);                                         \
            exit(1);                                                                                         \
        }                                                                                                    \
    } while (0)

static int first_case = 1;

static void
print_arr(const hsize_t *a, int n)
{
    printf("[");
    for (int i = 0; i < n; i++)
        printf("%s%llu", i ? "," : "", (unsigned long long)a[i]);
    printf("]");
}

static void print_info(hid_t sid);

/* Print "enc_<name>": the H5Sencode2 bytes, and "dec_<name>": what the
 * library reports about the H5Sdecode of them. */
static void
print_encoding(hid_t sid, hid_t fapl, const char *name)
{
    size_t nalloc = 0;
    CHECK(H5Sencode2(sid, NULL, &nalloc, fapl));
    unsigned char *buf = malloc(nalloc);
    CHECK(H5Sencode2(sid, buf, &nalloc, fapl));
    printf(",\"enc_%s\":\"", name);
    for (size_t i = 0; i < nalloc; i++)
        printf("%02x", buf[i]);
    printf("\",\"dec_%s\":{", name);
    hid_t dec = H5Sdecode(buf);
    CHECK(dec);
    print_info(dec);
    printf("}");
    H5Sclose(dec);
    free(buf);
}

/* Print the facts about a dataspace as JSON object members. */
static void
print_info(hid_t sid)
{
    int              rank = H5Sget_simple_extent_ndims(sid);
    hsize_t          dims[32], start[32], end[32];
    H5S_sel_type     type = H5Sget_select_type(sid);
    hssize_t         npoints = H5Sget_select_npoints(sid);

    CHECK(H5Sget_simple_extent_dims(sid, dims, NULL));
    printf("\"dims\":");
    print_arr(dims, rank);
    printf(",\"type\":%d,\"npoints\":%llu", (int)type, (unsigned long long)npoints);
    printf(",\"contiguous\":%s,\"single\":%s",
           H5S_select_is_contiguous((H5S_t *)H5I_object(sid)) > 0 ? "true" : "false",
           H5S_select_is_single((H5S_t *)H5I_object(sid)) > 0 ? "true" : "false");

    if (type == H5S_SEL_NONE)
        return;
    if (H5Sget_select_bounds(sid, start, end) >= 0) {
        printf(",\"low\":");
        print_arr(start, rank);
        printf(",\"high\":");
        print_arr(end, rank);
    }
    if (type == H5S_SEL_HYPERSLABS) {
        htri_t reg = H5Sis_regular_hyperslab(sid);
        printf(",\"regular\":%s", reg > 0 ? "true" : "false");
        if (npoints == (hssize_t)H5S_UNLIMITED)
            return;
        hssize_t nblocks = H5Sget_select_hyper_nblocks(sid);
        hsize_t *b       = malloc(sizeof(hsize_t) * 2 * rank * (nblocks + 1));
        CHECK(H5Sget_select_hyper_blocklist(sid, 0, (hsize_t)nblocks, b));
        printf(",\"blocks\":[");
        for (hssize_t i = 0; i < nblocks; i++) {
            printf("%s[", i ? "," : "");
            print_arr(b + 2 * rank * i, rank);
            printf(",");
            print_arr(b + 2 * rank * i + rank, rank);
            printf("]");
        }
        printf("]");
        free(b);
    }
    if (type == H5S_SEL_POINTS) {
        hssize_t n = H5Sget_select_elem_npoints(sid);
        hsize_t *b = malloc(sizeof(hsize_t) * rank * (n + 1));
        CHECK(H5Sget_select_elem_pointlist(sid, 0, (hsize_t)n, b));
        printf(",\"points\":[");
        for (hssize_t i = 0; i < n; i++) {
            printf("%s", i ? "," : "");
            print_arr(b + rank * i, rank);
        }
        printf("]");
        free(b);
    }

    /* Sequences for 4-byte elements */
    {
        hid_t   it = H5Ssel_iter_create(sid, 4, 0);
        size_t  nseq, nelem;
        hsize_t off[256];
        size_t  len[256];
        int     firstseq = 1;
        CHECK(it);
        printf(",\"seqs\":[");
        do {
            CHECK(H5Ssel_iter_get_seq_list(it, 256, (size_t)-1, &nseq, &nelem, off, len));
            for (size_t i = 0; i < nseq; i++) {
                printf("%s[%llu,%llu]", firstseq ? "" : ",", (unsigned long long)off[i],
                       (unsigned long long)len[i]);
                firstseq = 0;
            }
        } while (nseq > 0);
        printf("]");
        H5Ssel_iter_close(it);
    }
}

/* print_info plus the encodings and their decodings */
static void
print_facts(hid_t sid)
{
    hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
    print_info(sid);
    CHECK(H5Pset_libver_bounds(fapl, H5F_LIBVER_EARLIEST, H5F_LIBVER_LATEST));
    print_encoding(sid, fapl, "earliest");
    CHECK(H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST));
    print_encoding(sid, fapl, "latest");
    H5Pclose(fapl);
}

static void
emit(const char *name, hid_t sid)
{
    printf("%s\n  {\"name\":\"%s\",", first_case ? "" : ",", name);
    first_case = 0;
    print_facts(sid);
    printf("}");
}

static hid_t
simple(int rank, const hsize_t *dims)
{
    hid_t sid = H5Screate_simple(rank, dims, NULL);
    CHECK(sid);
    return sid;
}

static hid_t
hyper(int rank, const hsize_t *dims, const hsize_t *start, const hsize_t *stride, const hsize_t *count,
      const hsize_t *block)
{
    hid_t sid = simple(rank, dims);
    CHECK(H5Sselect_hyperslab(sid, H5S_SELECT_SET, start, stride, count, block));
    return sid;
}

/* OR together blocks given as {start..., end...} */
static hid_t
blocks(int rank, const hsize_t *dims, int n, const hsize_t *se)
{
    hid_t sid = simple(rank, dims);
    for (int i = 0; i < n; i++) {
        hsize_t blk[32], ones[32];
        for (int u = 0; u < rank; u++) {
            blk[u]  = se[2 * rank * i + rank + u] - se[2 * rank * i + u] + 1;
            ones[u] = 1;
        }
        CHECK(H5Sselect_hyperslab(sid, i ? H5S_SELECT_OR : H5S_SELECT_SET, se + 2 * rank * i, NULL, ones,
                                  blk));
    }
    return sid;
}

static hid_t
points(int rank, const hsize_t *dims, int n, const hsize_t *coord)
{
    hid_t sid = simple(rank, dims);
    CHECK(H5Sselect_elements(sid, H5S_SELECT_SET, (size_t)n, coord));
    return sid;
}

int
main(void)
{
    hid_t s[64];
    int   ns = 0;

    printf("{\"cases\":[");

    {
        hsize_t d[] = {20}, st[] = {2}, sr[] = {5}, c[] = {3}, b[] = {2};
        emit("1d_regular", s[ns++] = hyper(1, d, st, sr, c, b));
    }
    {
        hsize_t d[] = {10, 10}, st[] = {2, 2}, c[] = {1, 1}, b[] = {6, 6};
        emit("2d_block", s[ns++] = hyper(2, d, st, NULL, c, b));
    }
    {
        hsize_t d[] = {10, 12}, st[] = {1, 0}, sr[] = {4, 3}, c[] = {2, 3}, b[] = {2, 2};
        emit("2d_regular", s[ns++] = hyper(2, d, st, sr, c, b));
    }
    {
        hsize_t d[] = {4, 5, 6}, st[] = {0, 1, 0}, sr[] = {2, 2, 1}, c[] = {2, 2, 6}, b[] = {1, 1, 1};
        emit("3d_regular_merge", s[ns++] = hyper(3, d, st, sr, c, b));
    }
    {
        hsize_t d[] = {4, 5, 6}, st[] = {1, 0, 0}, sr[] = {2, 1, 1}, c[] = {2, 1, 1}, b[] = {1, 5, 6};
        emit("3d_regular_flat", s[ns++] = hyper(3, d, st, sr, c, b));
    }
    {
        hsize_t d[] = {8, 10};
        hsize_t se[] = {0, 0, 1, 1, 1, 1, 3, 4, 5, 0, 5, 9};
        emit("2d_irregular", s[ns++] = blocks(2, d, 3, se));
    }
    {
        hsize_t d[] = {8, 10};
        hsize_t se[] = {0, 0, 1, 1, 0, 4, 1, 5};
        emit("2d_irregular_regular", s[ns++] = blocks(2, d, 2, se));
    }
    {
        hsize_t d[] = {4, 4, 4};
        hsize_t se[] = {0, 0, 0, 1, 1, 1, 1, 1, 1, 2, 2, 3, 3, 0, 2, 3, 3, 2};
        emit("3d_irregular", s[ns++] = blocks(3, d, 3, se));
    }
    {
        hsize_t d[] = {10, 10};
        hsize_t c[] = {6, 9, 2, 2, 8, 4, 1, 6, 3, 3, 3, 4, 2, 8};
        emit("2d_points", s[ns++] = points(2, d, 7, c));
    }
    {
        hsize_t d[] = {3, 4, 2};
        emit("3d_all", s[ns++] = simple(3, d));
    }
    {
        hsize_t d[] = {5, 5};
        s[ns] = simple(2, d);
        CHECK(H5Sselect_none(s[ns]));
        emit("2d_none", s[ns++]);
    }
    {
        hsize_t d[] = {(hsize_t)1 << 33}, st[] = {((hsize_t)1 << 32) + 5}, sr[] = {7}, c[] = {3}, b[] = {2};
        emit("1d_big_regular", s[ns++] = hyper(1, d, st, sr, c, b));
    }
    {
        hsize_t d[]  = {(hsize_t)1 << 33};
        hsize_t se[] = {((hsize_t)1 << 32) + 1, ((hsize_t)1 << 32) + 3, ((hsize_t)1 << 32) + 7,
                        ((hsize_t)1 << 32) + 7, ((hsize_t)1 << 32) + 20, ((hsize_t)1 << 32) + 24};
        emit("1d_big_irregular", s[ns++] = blocks(1, d, 3, se));
    }
    {
        hsize_t d[]  = {70000};
        hsize_t se[] = {1, 3, 66000, 66010};
        emit("1d_mid_irregular", s[ns++] = blocks(1, d, 2, se));
    }
    {
        hsize_t d[] = {(hsize_t)1 << 33};
        hsize_t c[] = {5, ((hsize_t)1 << 32) + 9, 6};
        emit("1d_big_points", s[ns++] = points(1, d, 3, c));
    }
    {
        hsize_t d[] = {10, 10}, mx[] = {H5S_UNLIMITED, 10}, st[] = {1, 8}, sr[] = {4, 1},
                c[] = {H5S_UNLIMITED, 1}, b[] = {2, 2};
        s[ns] = H5Screate_simple(2, d, mx);
        CHECK(H5Sselect_hyperslab(s[ns], H5S_SELECT_SET, st, sr, c, b));
        emit("2d_unlim_count", s[ns++]);
    }
    {
        hsize_t d[] = {10, 10}, mx[] = {H5S_UNLIMITED, 10}, st[] = {3, 0}, sr[] = {1, 1}, c[] = {1, 1},
                b[] = {H5S_UNLIMITED, 10};
        s[ns] = H5Screate_simple(2, d, mx);
        CHECK(H5Sselect_hyperslab(s[ns], H5S_SELECT_SET, st, sr, c, b));
        emit("2d_unlim_block", s[ns++]);
    }
    /* Selections of the same shape but different types, with more than one
     * block (shape_same iterates over their blocks) */
    {
        hsize_t d[] = {10, 10};
        hsize_t c[] = {0, 0, 0, 2};
        emit("2d_points_pair", s[ns++] = points(2, d, 2, c));
    }
    {
        hsize_t d[]  = {8, 10};
        hsize_t se[] = {5, 5, 5, 5, 5, 7, 5, 7};
        emit("2d_blocks_pair", s[ns++] = blocks(2, d, 2, se));
    }
    {
        hsize_t d[] = {2, 10, 10};
        hsize_t c[] = {1, 0, 0, 1, 0, 2};
        emit("3d_points_pair", s[ns++] = points(3, d, 2, c));
    }
    {
        hsize_t d[] = {10, 10};
        hsize_t c[] = {0, 0, 0, 3};
        emit("2d_points_far", s[ns++] = points(2, d, 2, c));
    }
    {
        hsize_t d[] = {1, 2};
        emit("1x2_all", s[ns++] = simple(2, d));
    }
    {
        hsize_t d[] = {1, 2};
        hsize_t c[] = {0, 0, 0, 1};
        emit("1x2_points", s[ns++] = points(2, d, 2, c));
    }
    {
        hsize_t d[]  = {3, 4};
        hsize_t se[] = {0, 0, 0, 1, 2, 0, 2, 1};
        emit("3x4_blocks", s[ns++] = blocks(2, d, 2, se));
    }
    {
        hsize_t d[]  = {3, 4};
        hsize_t se[] = {0, 2, 0, 3, 2, 2, 2, 3};
        emit("3x4_blocks_shifted", s[ns++] = blocks(2, d, 2, se));
    }
    {
        hsize_t d[] = {3, 4};
        hsize_t c[] = {0, 2, 0, 3, 2, 2, 2, 3};
        emit("3x4_points_shifted", s[ns++] = points(2, d, 4, c));
    }
    /* Pairs with the same bounds and number of elements but different
     * shapes, as points and as hyperslabs (shape_same must look past the
     * bounds) */
    {
        hsize_t d[] = {4, 4};
        hsize_t c[] = {0, 0, 1, 1};
        emit("2d_points_diag", s[ns++] = points(2, d, 2, c));
    }
    {
        hsize_t d[] = {4, 4};
        hsize_t c[] = {0, 1, 1, 0};
        emit("2d_points_anti", s[ns++] = points(2, d, 2, c));
    }
    {
        hsize_t d[]  = {4, 4};
        hsize_t se[] = {0, 0, 0, 0, 1, 1, 1, 1};
        emit("2d_hyper_diag", s[ns++] = blocks(2, d, 2, se));
    }
    {
        hsize_t d[]  = {4, 4};
        hsize_t se[] = {0, 1, 0, 1, 1, 0, 1, 0};
        emit("2d_hyper_anti", s[ns++] = blocks(2, d, 2, se));
    }
    {
        /* the 2d_hyper_diag elements listed the other way round */
        hsize_t d[] = {4, 4};
        hsize_t c[] = {1, 0, 0, 1};
        emit("2d_points_swap", s[ns++] = points(2, d, 2, c));
    }
    /* A 2D selection spanning two rows against a 1D one of its column
     * pattern (the bounds of the extra dimension are not flat) */
    {
        hsize_t d[] = {4, 4};
        hsize_t c[] = {0, 0, 1, 2};
        emit("2d_points_step", s[ns++] = points(2, d, 2, c));
    }
    {
        hsize_t d[] = {10}, st[] = {0}, sr[] = {2}, c[] = {2}, b[] = {1};
        emit("1d_stride2", s[ns++] = hyper(1, d, st, sr, c, b));
    }
    /* A regular grid as points and as a hyperslab with a count > 1 in
     * every dimension */
    {
        hsize_t d[] = {4, 4};
        hsize_t c[] = {0, 0, 0, 2, 2, 0, 2, 2};
        emit("2d_points_grid", s[ns++] = points(2, d, 4, c));
    }
    {
        hsize_t d[] = {4, 4}, st[] = {0, 0}, sr[] = {2, 2}, c[] = {2, 2}, b[] = {1, 1};
        emit("2d_regular_grid", s[ns++] = hyper(2, d, st, sr, c, b));
    }
    /* 2d_irregular in a plane of a 3D extent and shifted in either
     * dimension */
    {
        hsize_t d[]  = {2, 8, 10};
        hsize_t se[] = {1, 0, 0, 1, 1, 1, 1, 1, 1, 1, 3, 4, 1, 5, 0, 1, 5, 9};
        emit("3d_irregular_flat", s[ns++] = blocks(3, d, 3, se));
    }
    {
        hsize_t d[]  = {9, 10};
        hsize_t se[] = {1, 0, 2, 1, 2, 1, 4, 4, 6, 0, 6, 9};
        emit("2d_irregular_shift_row", s[ns++] = blocks(2, d, 3, se));
    }
    {
        hsize_t d[]  = {8, 11};
        hsize_t se[] = {0, 1, 1, 2, 1, 2, 3, 5, 5, 1, 5, 10};
        emit("2d_irregular_shift_col", s[ns++] = blocks(2, d, 3, se));
    }
    /* Single blocks that are contiguous in the extent: whole rows (the
     * "large contiguous" case of H5S__hyper_is_contiguous) and part of a
     * row (the "small contiguous" case) */
    {
        hsize_t d[] = {6, 8}, st[] = {1, 0}, c[] = {1, 1}, b[] = {2, 8};
        emit("2d_rows", s[ns++] = hyper(2, d, st, NULL, c, b));
    }
    {
        hsize_t d[] = {6, 8}, st[] = {2, 3}, c[] = {1, 1}, b[] = {1, 4};
        emit("2d_row_part", s[ns++] = hyper(2, d, st, NULL, c, b));
    }
    /* A strided 1D selection for intersect_block */
    {
        hsize_t d[] = {20}, st[] = {0}, sr[] = {4}, c[] = {3}, b[] = {1};
        emit("1d_stride4", s[ns++] = hyper(1, d, st, sr, c, b));
    }
    /* The largest rank: irregular hyperslabs that differ by an offset in
     * the first dimension. (Not points: H5S__point_add allocates rank + 1
     * coordinates from a free list of at most H5S_MAX_RANK, which corrupts
     * the heap for a rank 32 point selection.) */
    for (int k = 0; k < 2; k++) {
        /* blocks {0..0, 0}, {0..0, 2..3} (+k in the first dimension) */
        hsize_t d[32], se[2 * 2 * 32];
        for (int u = 0; u < 32; u++) {
            d[u]          = u < 31 ? 3 : 4; /* (3^31 * 4 elements) */
            se[u]         = 0;
            se[32 + u]    = 0;
            se[64 + u]    = 0;
            se[96 + u]    = 0;
        }
        se[0] = se[32] = se[64] = se[96] = (hsize_t)k;
        se[64 + 31] = 2;
        se[96 + 31] = 3;
        emit(k ? "32d_irregular_shift" : "32d_irregular", s[ns++] = blocks(32, d, 2, se));
    }

    /* Projections: src (same rank as sis), dst (same npoints as src), sis */
    printf("],\"projections\":[");
    {
        struct {
            const char *name;
            hid_t       src, dst, sis;
        } p[16];
        int     np = 0;
        hsize_t d6[] = {6, 6}, d36[] = {36}, d2[] = {8, 10};

        {
            /* all 6x6 -> all 36, sis = 2x3 block */
            hsize_t st[] = {1, 2}, c[] = {1, 1}, b[] = {2, 3};
            p[np].name = "all_to_1d";
            p[np].src  = simple(2, d6);
            p[np].dst  = simple(1, d36);
            p[np].sis  = hyper(2, d6, st, NULL, c, b);
            np++;
        }
        {
            /* regular strided src -> block dst */
            hsize_t st[] = {0, 0}, sr[] = {2, 2}, c[] = {3, 3}, b[] = {1, 1};
            hsize_t dst[] = {2, 3}, dc[] = {1, 1}, db[] = {3, 3};
            hsize_t se[] = {0, 0, 3, 3, 4, 4, 5, 5};
            p[np].name = "strided_to_block";
            p[np].src  = hyper(2, d6, st, sr, c, b);
            p[np].dst  = hyper(2, d2, dst, NULL, dc, db);
            p[np].sis  = blocks(2, d6, 2, se);
            np++;
        }
        {
            /* irregular src -> 1d regular dst, sis irregular */
            hsize_t se[]  = {0, 0, 1, 5, 3, 1, 4, 2};
            hsize_t ds[]  = {1}, dsr[] = {2}, dc[] = {16}, db[] = {1};
            hsize_t sis[] = {1, 1, 3, 1, 0, 4, 5, 4};
            p[np].name    = "irregular";
            p[np].src     = blocks(2, d6, 2, se);
            p[np].dst     = hyper(1, d36, ds, dsr, dc, db);
            p[np].sis     = blocks(2, d6, 2, sis);
            np++;
        }
        {
            /* points src */
            hsize_t c[]   = {0, 0, 1, 1, 2, 2, 3, 3};
            hsize_t ds[]  = {10}, dc[] = {1}, db[] = {4};
            hsize_t st[]  = {1, 0}, sc[] = {2, 6}, sb[] = {1, 1};
            p[np].name    = "points_src";
            p[np].src     = points(2, d6, 4, c);
            p[np].dst     = hyper(1, d36, ds, NULL, dc, db);
            p[np].sis     = hyper(2, d6, st, NULL, sc, sb);
            np++;
        }
        {
            /* sis as points */
            hsize_t c[]   = {0, 1, 2, 3, 5, 5}; /* (sorted: H5S_hyper_add_span_element needs increasing order) */
            p[np].name    = "points_sis";
            p[np].src     = simple(2, d6);
            p[np].dst     = simple(1, d36);
            p[np].sis     = points(2, d6, 3, c);
            np++;
        }
        {
            /* points src, none of which is in sis */
            hsize_t c[]  = {0, 0, 1, 1, 2, 2, 3, 3};
            hsize_t ds[] = {10}, dc[] = {1}, db[] = {4};
            hsize_t st[] = {4, 0}, sc[] = {1, 1}, sb[] = {2, 6};
            p[np].name   = "points_src_disjoint";
            p[np].src    = points(2, d6, 4, c);
            p[np].dst    = hyper(1, d36, ds, NULL, dc, db);
            p[np].sis    = hyper(2, d6, st, NULL, sc, sb);
            np++;
        }
        {
            /* 1D src onto a 2D dst: the intersection covers part of a
             * row, whole rows, and whole rows followed by part of one */
            hsize_t se[] = {1, 2, 6, 17, 20, 20, 24, 32};
            p[np].name   = "1d_to_2d_rows";
            p[np].src    = simple(1, d36);
            p[np].dst    = simple(2, d6);
            p[np].sis    = blocks(1, d36, 4, se);
            np++;
        }
        for (int i = 0; i < np; i++) {
            hid_t r = H5Sselect_project_intersection(p[i].src, p[i].dst, p[i].sis);
            CHECK(r);
            printf("%s\n  {\"name\":\"%s\",\"src\":{", i ? "," : "", p[i].name);
            print_facts(p[i].src);
            printf("},\"dst\":{");
            print_facts(p[i].dst);
            printf("},\"sis\":{");
            print_facts(p[i].sis);
            printf("},\"result\":{");
            print_facts(r);
            printf("}}");
        }
    }

    /* shape_same over all pairs of the limited cases above */
    printf("],\"shape_same\":[");
    {
        int first = 1;
        for (int i = 0; i < ns; i++)
            for (int j = 0; j < ns; j++) {
                if (H5Sget_select_npoints(s[i]) == (hssize_t)H5S_UNLIMITED ||
                    H5Sget_select_npoints(s[j]) == (hssize_t)H5S_UNLIMITED)
                    continue;
                htri_t r = H5Sselect_shape_same(s[i], s[j]);
                CHECK(r);
                printf("%s[%d,%d,%s]", first ? "" : ",", i, j, r ? "true" : "false");
                first = 0;
            }
    }

    /* intersect_block for a few blocks against all limited cases */
    printf("],\"intersect_block\":[");
    {
        int     first = 1;
        hsize_t blk[][2][3] = {{{0, 0, 0}, {0, 0, 0}},   {{2, 2, 2}, {3, 3, 3}}, {{1, 0, 0}, {1, 9, 9}},
                               {{5, 5, 5}, {9, 9, 9}},   {{0, 4, 1}, {7, 4, 1}}, {{4, 1, 0}, {4, 1, 0}},
                               {{6, 2, 0}, {9, 3, 0}},   {{5, 5, 5}, {6, 6, 6}}};
        for (int i = 0; i < ns; i++) {
            if (H5Sget_select_npoints(s[i]) == (hssize_t)H5S_UNLIMITED || H5Sget_simple_extent_ndims(s[i]) > 3)
                continue;
            for (int k = 0; k < (int)(sizeof(blk) / sizeof(blk[0])); k++) {
                htri_t r = H5Sselect_intersect_block(s[i], blk[k][0], blk[k][1]);
                CHECK(r);
                printf("%s[%d,%d,%s]", first ? "" : ",", i, k, r ? "true" : "false");
                first = 0;
            }
        }
        printf("],\"blocks\":[");
        for (int k = 0; k < (int)(sizeof(blk) / sizeof(blk[0])); k++) {
            printf("%s[", k ? "," : "");
            print_arr(blk[k][0], 3);
            printf(",");
            print_arr(blk[k][1], 3);
            printf("]");
        }
    }

    /* H5Sselect_adjust (H5S_select_adjust_s) of the limited cases by their
     * low bounds (moving the selection to the origin) and, when the moved
     * selection stays within the extent, by -1 in every dimension; for the
     * "none" selection, whose bounds H5Sselect_adjust cannot get, the
     * result is not reported */
    printf("],\"adjust\":[");
    {
        int first = 1;
        for (int i = 0; i < ns; i++) {
            hsize_t  low[32], high[32];
            hssize_t off[2][32];
            int      rank = H5Sget_simple_extent_ndims(s[i]);
            if (H5Sget_select_npoints(s[i]) == (hssize_t)H5S_UNLIMITED ||
                H5Sget_select_type(s[i]) == H5S_SEL_NONE)
                continue;
            hsize_t dims[32];
            int     nk = 2;
            CHECK(H5Sget_select_bounds(s[i], low, high));
            CHECK(H5Sget_simple_extent_dims(s[i], dims, NULL));
            for (int u = 0; u < rank; u++) {
                off[0][u] = (hssize_t)low[u];
                off[1][u] = -1;
                if (high[u] + 1 >= dims[u])
                    nk = 1; /* (moving up would leave the extent) */
            }
            for (int k = 0; k < nk; k++) {
                hid_t c = H5Scopy(s[i]);
                CHECK(c);
                CHECK(H5Sselect_adjust(c, off[k]));
                printf("%s\n  {\"case\":%d,\"offset\":[", first ? "" : ",", i);
                for (int u = 0; u < rank; u++)
                    printf("%s%lld", u ? "," : "", (long long)off[k][u]);
                printf("],\"result\":{");
                print_info(c);
                printf("}}");
                first = 0;
                H5Sclose(c);
            }
        }
    }

    /* Selection offsets (H5Soffset_simple) moving the limited selections to
     * the edges of the extent and one past them: H5Sselect_valid,
     * H5Sget_select_bounds, H5S_get_select_offset (the linear offset of the
     * first element) and, for the valid ones, the sequences */
    printf("],\"offsets\":[");
    {
        int first = 1;
        for (int i = 0; i < ns; i++) {
            hsize_t  low[32], high[32], dims[32];
            hssize_t off[6][32];
            int      rank = H5Sget_simple_extent_ndims(s[i]);
            if (H5Sget_select_npoints(s[i]) == (hssize_t)H5S_UNLIMITED ||
                H5Sget_select_type(s[i]) == H5S_SEL_NONE || rank > 3)
                continue;
            CHECK(H5Sget_select_bounds(s[i], low, high));
            CHECK(H5Sget_simple_extent_dims(s[i], dims, NULL));
            memset(off, 0, sizeof(off));
            /* to the end of the extent in the last dimension, and one past */
            off[0][rank - 1] = (hssize_t)(dims[rank - 1] - 1 - high[rank - 1]);
            off[1][rank - 1] = off[0][rank - 1] + 1;
            /* to the start of the extent in the first dimension, and one before */
            off[2][0] = -(hssize_t)low[0];
            off[3][0] = off[2][0] - 1;
            /* both at once, and one past in the first dimension */
            off[4][0]        = (hssize_t)(dims[0] - 1 - high[0]);
            off[4][rank - 1] = -(hssize_t)low[rank - 1];
            off[5][0]        = off[4][0] + 1;
            off[5][rank - 1] = off[4][rank - 1];
            for (int k = 0; k < 6; k++) {
                hid_t   c = H5Scopy(s[i]);
                hsize_t lin;
                htri_t  valid;
                CHECK(c);
                CHECK(H5Soffset_simple(c, off[k]));
                valid = H5Sselect_valid(c);
                CHECK(valid);
                printf("%s\n  {\"case\":%d,\"offset\":[", first ? "" : ",", i);
                for (int u = 0; u < rank; u++)
                    printf("%s%lld", u ? "," : "", (long long)off[k][u]);
                printf("],\"valid\":%s", valid ? "true" : "false");
                H5E_BEGIN_TRY
                {
                    if (H5Sget_select_bounds(c, low, high) >= 0) {
                        printf(",\"low\":");
                        print_arr(low, rank);
                        printf(",\"high\":");
                        print_arr(high, rank);
                    }
                    if (H5S_get_select_offset((H5S_t *)H5I_object(c), &lin) >= 0)
                        printf(",\"elem_offset\":%llu", (unsigned long long)lin);
                }
                H5E_END_TRY
                if (valid) {
                    hid_t   it = H5Ssel_iter_create(c, 4, 0);
                    size_t  nseq, nelem;
                    hsize_t soff[256];
                    size_t  slen[256];
                    int     firstseq = 1;
                    CHECK(it);
                    printf(",\"seqs\":[");
                    do {
                        CHECK(H5Ssel_iter_get_seq_list(it, 256, (size_t)-1, &nseq, &nelem, soff, slen));
                        for (size_t q = 0; q < nseq; q++) {
                            printf("%s[%llu,%llu]", firstseq ? "" : ",", (unsigned long long)soff[q],
                                   (unsigned long long)slen[q]);
                            firstseq = 0;
                        }
                    } while (nseq > 0);
                    printf("]");
                    H5Ssel_iter_close(it);
                }
                printf("}");
                first = 0;
                H5Sclose(c);
            }
        }
    }

    /* H5S_hyper_clip_unlim of unlimited selections, and
     * H5S_hyper_get_first_inc_block for those with an unlimited count. The
     * sequences are reported when there are few elements. */
    printf("],\"clips\":[");
    {
        struct {
            int     rank;
            hsize_t start[2], stride[2], count[2], block[2];
            int     nclip;
            hsize_t clip[8];
        } u[] = {
            {1, {1}, {4}, {H5S_UNLIMITED}, {2}, 8, {0, 1, 2, 5, 6, 7, 9, 10}},
            {1, {3}, {4}, {H5S_UNLIMITED}, {2}, 4, {4, 5, 8, 9}},
            {1, {3}, {4}, {H5S_UNLIMITED}, {2}, 1, {(hsize_t)-2}},
            {1, {0}, {4}, {H5S_UNLIMITED}, {2}, 1, {(hsize_t)-5}},
            {1,
             {0},
             {(hsize_t)1 << 62},
             {H5S_UNLIMITED},
             {10},
             2,
             {((hsize_t)1 << 63) + 5, ((hsize_t)1 << 63) + 10}},
            {1, {2}, {1}, {1}, {H5S_UNLIMITED}, 3, {1, 2, 6}},
            {2, {1, 8}, {4, 1}, {H5S_UNLIMITED, 1}, {2, 2}, 4, {5, 6, 7, 9}},
            {2, {3, 0}, {1, 1}, {1, 1}, {H5S_UNLIMITED, 10}, 2, {3, 5}},
        };
        int first = 1;
        for (int i = 0; i < (int)(sizeof(u) / sizeof(u[0])); i++) {
            hsize_t d[2] = {10, 10}, mx[2] = {H5S_UNLIMITED, 10};
            hid_t   sid = H5Screate_simple(u[i].rank, d, mx);
            CHECK(sid);
            CHECK(H5Sselect_hyperslab(sid, H5S_SELECT_SET, u[i].start, u[i].stride, u[i].count, u[i].block));
            for (int k = 0; k < u[i].nclip; k++) {
                hid_t    c = H5Scopy(sid);
                hssize_t npoints;
                hsize_t  low[2], high[2];
                CHECK(c);
                CHECK(H5S_hyper_clip_unlim((H5S_t *)H5I_object(c), u[i].clip[k]));
                npoints = H5Sget_select_npoints(c);
                printf("%s\n  {\"rank\":%d,\"start\":", first ? "" : ",", u[i].rank);
                first = 0;
                print_arr(u[i].start, u[i].rank);
                printf(",\"stride\":");
                print_arr(u[i].stride, u[i].rank);
                printf(",\"count\":");
                print_arr(u[i].count, u[i].rank);
                printf(",\"block\":");
                print_arr(u[i].block, u[i].rank);
                printf(",\"clip\":%llu,\"type\":%d,\"npoints\":%llu", (unsigned long long)u[i].clip[k],
                       (int)H5Sget_select_type(c), (unsigned long long)npoints);
                if (u[i].count[0] == H5S_UNLIMITED) {
                    bool    partial = false;
                    hsize_t inc =
                        H5S_hyper_get_first_inc_block((H5S_t *)H5I_object(sid), u[i].clip[k], &partial);
                    printf(",\"first_inc_block\":%llu,\"partial\":%s", (unsigned long long)inc,
                           partial ? "true" : "false");
                }
                if (H5Sget_select_type(c) == H5S_SEL_HYPERSLABS) {
                    CHECK(H5Sget_select_bounds(c, low, high));
                    printf(",\"low\":");
                    print_arr(low, u[i].rank);
                    printf(",\"high\":");
                    print_arr(high, u[i].rank);
                    printf(",\"regular\":%s", H5Sis_regular_hyperslab(c) > 0 ? "true" : "false");
                    if (npoints <= 200) {
                        hid_t   it = H5Ssel_iter_create(c, 1, 0);
                        size_t  nseq, nelem;
                        hsize_t soff[256];
                        size_t  slen[256];
                        int     firstseq = 1;
                        CHECK(it);
                        printf(",\"seqs\":[");
                        do {
                            CHECK(H5Ssel_iter_get_seq_list(it, 256, (size_t)-1, &nseq, &nelem, soff, slen));
                            for (size_t q = 0; q < nseq; q++) {
                                printf("%s[%llu,%llu]", firstseq ? "" : ",", (unsigned long long)soff[q],
                                       (unsigned long long)slen[q]);
                                firstseq = 0;
                            }
                        } while (nseq > 0);
                        printf("]");
                        H5Ssel_iter_close(it);
                    }
                }
                printf("}");
                H5Sclose(c);
            }
            H5Sclose(sid);
        }
    }

    /* Extents: the encoded extent message version of new dataspaces of
     * each class (H5S_create), H5S_get_npoints_max and H5S_extent_equal */
    printf("],\"extents\":[");
    {
        struct {
            int     cls, rank;
            hsize_t dims[2], max[2];
        } e[] = {
            {H5S_NULL, 0, {0}, {0}},
            {H5S_SCALAR, 0, {0}, {0}},
            {H5S_SIMPLE, 2, {3, 4}, {3, 4}},
            {H5S_SIMPLE, 2, {3, 5}, {3, 5}},
            {H5S_SIMPLE, 2, {3, 4}, {H5S_UNLIMITED, 4}},
            {H5S_SIMPLE, 2, {2, 4}, {H5S_UNLIMITED, 4}},
            {H5S_SIMPLE, 2, {3, 4}, {6, 4}},
            {H5S_SIMPLE, 1, {3}, {3}},
        };
        int   n = (int)(sizeof(e) / sizeof(e[0]));
        hid_t sids[16];
        hid_t fapl = H5Pcreate(H5P_FILE_ACCESS);
        CHECK(H5Pset_libver_bounds(fapl, H5F_LIBVER_EARLIEST, H5F_LIBVER_LATEST));
        for (int i = 0; i < n; i++) {
            size_t         nalloc = 0;
            unsigned char *buf;
            if (e[i].cls == H5S_SIMPLE)
                sids[i] = H5Screate_simple(e[i].rank, e[i].dims, e[i].max);
            else
                sids[i] = H5Screate((H5S_class_t)e[i].cls);
            CHECK(sids[i]);
            CHECK(H5Sencode2(sids[i], NULL, &nalloc, fapl));
            buf = malloc(nalloc);
            CHECK(H5Sencode2(sids[i], buf, &nalloc, fapl));
            /* (type, version, sizeof(size), extent size, then the extent
             * message, which starts with its version) */
            printf("%s\n  {\"class\":%d,\"dims\":", i ? "," : "", e[i].cls);
            print_arr(e[i].dims, e[i].rank);
            printf(",\"max\":");
            print_arr(e[i].max, e[i].rank);
            printf(",\"version\":%d,\"npoints_max\":%llu,\"equal\":[", buf[7],
                   (unsigned long long)H5S_get_npoints_max((H5S_t *)H5I_object(sids[i])));
            for (int j = 0; j <= i; j++)
                printf("%s%s", j ? "," : "",
                       H5S_extent_equal((H5S_t *)H5I_object(sids[i]), (H5S_t *)H5I_object(sids[j])) > 0
                           ? "true"
                           : "false");
            printf("]}");
            free(buf);
        }
        for (int i = 0; i < n; i++)
            H5Sclose(sids[i]);
        H5Pclose(fapl);
    }

    /* An irregular hyperslab encoding with a zero-sized block (end = start -
     * 1), which H5S__hyper_deserialize ORs into the selection as a no-op:
     * the latest encoding of a 1D selection of three blocks with the end of
     * its second block changed */
    printf("],\"decoded\":[");
    {
        hsize_t        d[]    = {20};
        hsize_t        se[]   = {1, 3, 7, 9, 12, 12};
        hid_t          sid    = blocks(1, d, 3, se);
        hid_t          fapl   = H5Pcreate(H5P_FILE_ACCESS);
        size_t         nalloc = 0;
        unsigned char *buf, *sel;
        hid_t          dec;
        CHECK(H5Pset_libver_bounds(fapl, H5F_LIBVER_LATEST, H5F_LIBVER_LATEST));
        CHECK(H5Sencode2(sid, NULL, &nalloc, fapl));
        buf = malloc(nalloc);
        CHECK(H5Sencode2(sid, buf, &nalloc, fapl));
        /* selection: type(4) version(4) flags(1) enc_size(1) rank(4)
         * nblocks(enc_size) then start/end pairs of enc_size bytes */
        sel = buf + 7 + (buf[3] | buf[4] << 8 | buf[5] << 16 | buf[6] << 24);
        if (sel[4] != 3 || sel[9] != 2) {
            fprintf(stderr, "unexpected encoding\n");
            exit(1);
        }
        sel[14 + 2 + 2 * 2 + 2] = 6; /* end of the second block: 9 -> 6 */
        dec = H5Sdecode(buf);
        CHECK(dec);
        printf("\n  {\"name\":\"1d_zero_block\",\"dims\":[20],\"enc_latest\":\"");
        for (size_t i = 0; i < nalloc; i++)
            printf("%02x", buf[i]);
        printf("\",\"dec_latest\":{");
        print_info(dec);
        printf("}}");
        H5Sclose(dec);
        H5Sclose(sid);
        H5Pclose(fapl);
        free(buf);
    }
    printf("]}\n");
    return 0;
}
