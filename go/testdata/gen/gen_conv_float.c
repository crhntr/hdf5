/*
 * Generator for testdata/conv_float.h5: floating-point datasets in layouts
 * that are neither IEEE single nor double, which the pure Go reader
 * converts to float64 with its translation of H5T__conv_f_f, and next to
 * each the float64 values libhdf5 converts them to.
 *
 *   h5cc -o /tmp/gen_conv_float gen_conv_float.c && (cd go/testdata && /tmp/gen_conv_float)
 *
 * For each layout L below the file has, for each set S of bit patterns
 * (round: exponents within the range of normalized doubles, no source
 * denormals; all: the special values, the exponent limits of the layout
 * and of double, denormals and mantissas that round up; rand: random bit
 * patterns):
 *
 *   /L_S         the patterns in layout L (written without conversion)
 *   /L_S.expect  H5T_IEEE_F64LE: what H5Dread of /L_S into
 *                H5T_NATIVE_DOUBLE gives (one H5T__conv_f_f call for the
 *                whole dataset, whose loop carries state from one element
 *                to the next)
 *
 * and, to show where that state starts over:
 *
 *   /quad_array         quad array {2} {3}: {denormal, 1.111..1 (rounds
 *                       up to 2 unless an earlier element of the same
 *                       conversion was denormalized)}, {1.111..1,
 *                       1.111..1}, {1.111..1, denormal} (H5T__conv_array
 *                       converts each array element with its own
 *                       H5T__conv_f_f call)
 *   /quad_array.expect  H5Dread into an array {2} of doubles, flattened
 *   /quad_vlen          vlen of quad {3}: the same three sequences
 *                       (H5T__conv_vlen: one call per sequence)
 *   /quad_vlen.expect   H5Dread into a vlen of doubles, flattened
 *
 * Layouts (sign bit, exponent position/size/bias, mantissa position/size,
 * size in bytes, precision, offset, byte order, normalization):
 *
 *   e4m3      1 byte, s 7, e 3/4 bias 7, m 0/3
 *   bf16      2 bytes, s 15, e 7/8 bias 127, m 0/7
 *   f24       3 bytes, s 23, e 16/7 bias 63, m 0/16
 *   f24be     4 bytes big-endian, precision 24 at offset 8 (MSB padding
 *             zero, LSB padding one), s 31, e 24/7 bias 63, m 8/16
 *   e15m48    8 bytes, s 63, e 48/15 bias 16383, m 0/48
 *   quad      16 bytes, IEEE binary128: s 127, e 112/15 bias 16383, m 0/112
 *   quadbe    the same, big-endian
 *   x87       10 bytes: s 79, e 64/15 bias 16383, m 0/64, no implied
 *             mantissa bit (the x87 extended format, but not in the 16
 *             bytes of the native long double: libhdf5 converts a type
 *             equal to H5T_NATIVE_LDOUBLE with the compiler's conversion
 *             instead of H5T__conv_f_f)
 *   top       16 bytes, s 0, e 1/15 bias 16383, m 16/112 (the mantissa
 *             ends at the last bit of the element)
 *   etop      4 bytes, s 0, e 24/8 bias 127, m 1/23 (the exponent ends at
 *             the last bit of the element)
 *   e63       16 bytes, s 127, e 64/63 bias 2^31-1, m 0/64 (the largest
 *             exponent field H5T__conv_f_f converts)
 *   vax32     H5T_VAX_F32
 *   vax64     H5T_VAX_F64
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "hdf5.h"

#define CHECK(x)                                                                                             \
    do {                                                                                                     \
        if ((x) < 0) {                                                                                       \
            fprintf(stderr, "%s:%d: %s failed\n", __FILE__, __LINE__, #x);                                   \
            exit(1);                                                                                         \
        }                                                                                                    \
    } while (0)

typedef struct {
    const char *name;
    size_t      size, prec, offset;
    size_t      spos, epos, esize, mpos, msize, ebias;
    H5T_order_t order;
    H5T_norm_t  norm;
    hid_t       predef; /* a predefined type to copy instead (VAX) */
} layout_t;

#define MAXN 4096
#define MAXSIZE 16

static uint64_t rng = 0x9e3779b97f4a7c15ULL;

static uint64_t
next(void)
{
    rng ^= rng << 13;
    rng ^= rng >> 7;
    rng ^= rng << 17;
    return rng;
}

/* A pattern in little-endian bit order (bit 0 = LSB of byte 0) of the
 * layout's precision field, before the byte order is applied. */
typedef struct {
    uint8_t b[MAXSIZE];
} pat_t;

static void
setbits(pat_t *p, size_t pos, size_t n, const uint8_t *src)
{
    for (size_t i = 0; i < n; i++) {
        size_t bit = (src[i / 8] >> (i % 8)) & 1;
        size_t d   = pos + i;
        if (bit)
            p->b[d / 8] |= (uint8_t)(1u << (d % 8));
        else
            p->b[d / 8] &= (uint8_t)~(1u << (d % 8));
    }
}

static void
setu64(pat_t *p, size_t pos, size_t n, uint64_t v)
{
    uint8_t src[8];
    for (int i = 0; i < 8; i++)
        src[i] = (uint8_t)(v >> (8 * i));
    setbits(p, pos, n < 64 ? n : 64, src);
}

typedef struct {
    pat_t  p[MAXN];
    size_t n;
} set_t;

/* Mantissa kinds of the "round" and "all" sets. */
enum { M_ZERO, M_ONE, M_ALL, M_TOP, M_HALF, M_CUT, M_CUTONLY, M_RAND, M_NKINDS };

static void
mantissa(const layout_t *l, int kind, uint8_t *m)
{
    size_t nb = (l->msize + 7) / 8;
    memset(m, 0, MAXSIZE);
    switch (kind) {
        case M_ZERO:
            break;
        case M_ONE:
            m[0] = 1;
            break;
        case M_ALL:
            memset(m, 0xff, nb);
            break;
        case M_TOP:
            m[(l->msize - 1) / 8] |= (uint8_t)(1u << ((l->msize - 1) % 8));
            break;
        case M_HALF: /* every other bit */
            memset(m, 0x55, nb);
            break;
        case M_CUT: /* all ones down to the first bit a double does not keep */
            memset(m, 0xff, nb);
            if (l->msize > 53)
                for (size_t i = 0; i < l->msize - 53; i++)
                    m[i / 8] &= (uint8_t)~(1u << (i % 8));
            break;
        case M_CUTONLY: /* only the first bit a double does not keep */
            if (l->msize > 52) {
                size_t i = l->msize - 53;
                m[i / 8] |= (uint8_t)(1u << (i % 8));
            }
            else
                m[0] = 1;
            break;
        case M_RAND:
            for (size_t i = 0; i < nb; i++)
                m[i] = (uint8_t)next();
            break;
    }
    /* clear the bits above msize */
    for (size_t i = l->msize; i < 8 * MAXSIZE; i++)
        m[i / 8] &= (uint8_t)~(1u << (i % 8));
}

static void
add(set_t *s, const layout_t *l, int sign, uint64_t expo, int mkind)
{
    uint8_t m[MAXSIZE];
    pat_t   p;
    if (s->n >= MAXN) {
        fprintf(stderr, "too many patterns\n");
        exit(1);
    }
    memset(&p, 0, sizeof p);
    mantissa(l, mkind, m);
    setbits(&p, l->mpos, l->msize, m);
    setu64(&p, l->epos, l->esize, expo);
    setu64(&p, l->spos, 1, (uint64_t)sign);
    s->p[s->n++] = p;
}

/* exponent field values whose unbiased exponents are k in [lo, hi] */
static void
add_range(set_t *s, const layout_t *l, int64_t lo, int64_t hi, int sign, int64_t emin, int64_t emax)
{
    for (int64_t k = lo; k <= hi; k++) {
        int64_t e = (int64_t)l->ebias + k;
        if (e < emin || e > emax)
            continue;
        for (int mk = 0; mk < M_NKINDS; mk++)
            add(s, l, sign, (uint64_t)e, mk);
    }
}

static void
build_sets(const layout_t *l, set_t *round, set_t *all, set_t *rnd)
{
    uint64_t emaxf = l->esize >= 64 ? UINT64_MAX : (((uint64_t)1 << l->esize) - 1); /* all ones */
    int64_t  emax  = l->esize >= 63 ? INT64_MAX : (int64_t)emaxf - 1;                /* largest finite */

    round->n = all->n = rnd->n = 0;

    /* round: unbiased exponents -1022..1023 that the layout has, a few of
     * each region (both signs) */
    for (int sign = 0; sign < 2; sign++) {
        add_range(round, l, -1022, -1018, sign, 1, emax);
        add_range(round, l, -3, 3, sign, 1, emax);
        add_range(round, l, 1019, 1023, sign, 1, emax);
    }
    /* the largest finite values and the largest exponent with every
     * mantissa (rounding at the top of the range) */
    for (int sign = 0; sign < 2; sign++)
        for (int mk = 0; mk < M_NKINDS; mk++)
            add(round, l, sign, (uint64_t)emax, mk);

    /* all: zeros, infinities, NaNs, the smallest and largest exponents,
     * the double limits, denormals */
    for (int sign = 0; sign < 2; sign++) {
        for (int mk = 0; mk < M_NKINDS; mk++) {
            add(all, l, sign, 0, mk);
            add(all, l, sign, 1, mk);
            add(all, l, sign, 2, mk);
            add(all, l, sign, emaxf, mk);
            add(all, l, sign, (uint64_t)emax, mk);
            add(all, l, sign, (uint64_t)emax - 1, mk);
        }
        add_range(all, l, -1080, -1070, sign, 1, emax);
        add_range(all, l, -1027, -1020, sign, 1, emax);
        add_range(all, l, 1022, 1025, sign, 1, emax);
        add_range(all, l, -1, 1, sign, 1, emax);
    }

    /* rand: random bits */
    for (int i = 0; i < 512; i++) {
        pat_t p;
        memset(&p, 0, sizeof p);
        for (size_t j = 0; j < MAXSIZE; j++)
            p.b[j] = (uint8_t)next();
        /* keep the bits of the precision only */
        for (size_t j = l->prec; j < 8 * MAXSIZE; j++)
            p.b[j / 8] &= (uint8_t)~(1u << (j % 8));
        rnd->p[rnd->n++] = p;
    }
}

static hid_t
make_type(const layout_t *l)
{
    hid_t t;
    if (l->predef > 0)
        return H5Tcopy(l->predef);
    t = H5Tcopy(H5T_IEEE_F64LE);
    CHECK(t);
    /* a small layout that fits any size first, then the real one */
    CHECK(H5Tset_fields(t, 7, 3, 4, 0, 3));
    CHECK(H5Tset_ebias(t, 7));
    CHECK(H5Tset_precision(t, 8));
    CHECK(H5Tset_offset(t, 0));
    CHECK(H5Tset_size(t, l->size));
    CHECK(H5Tset_offset(t, l->offset));
    CHECK(H5Tset_precision(t, l->prec));
    CHECK(H5Tset_fields(t, l->spos, l->epos, l->esize, l->mpos, l->msize));
    CHECK(H5Tset_ebias(t, l->ebias));
    CHECK(H5Tset_norm(t, l->norm));
    CHECK(H5Tset_order(t, l->order));
    if (l->offset > 0)
        CHECK(H5Tset_pad(t, H5T_PAD_ONE, H5T_PAD_ZERO));
    return t;
}

/* The element bytes of pattern p: the precision field at the layout's
 * offset, padding as set in the type, in the type's byte order. */
static void
element(const layout_t *l, hid_t t, const pat_t *p, uint8_t *out)
{
    pat_t e;
    memset(&e, 0, sizeof e);
    if (l->predef > 0) {
        /* VAX: the pattern is in the VAX bit layout of H5T__conv_f_f's
         * little-endian view; swap 16-bit words back (H5T_ORDER_VAX) */
        size_t n = l->size;
        memcpy(e.b, p->b, n);
        for (size_t i = 0; i < n; i += 4) {
            uint8_t t1 = e.b[i], t2 = e.b[i + 1];
            e.b[i]         = e.b[(n - 2) - i];
            e.b[i + 1]     = e.b[(n - 1) - i];
            e.b[(n - 2) - i] = t1;
            e.b[(n - 1) - i] = t2;
        }
        memcpy(out, e.b, n);
        return;
    }
    setbits(&e, l->offset, l->prec, p->b);
    if (l->offset > 0) { /* LSB padding one */
        uint8_t ones[MAXSIZE];
        memset(ones, 0xff, sizeof ones);
        setbits(&e, 0, l->offset, ones);
    }
    (void)t;
    if (l->order == H5T_ORDER_BE) {
        for (size_t i = 0; i < l->size; i++)
            out[i] = e.b[l->size - 1 - i];
    }
    else
        memcpy(out, e.b, l->size);
}

static void
write_set(hid_t file, const layout_t *l, const char *sname, const set_t *s)
{
    hid_t    t = make_type(l), space, ds;
    hsize_t  dims[1] = {s->n};
    uint8_t *raw     = calloc(s->n, l->size > 8 ? l->size : 8);
    double  *conv    = calloc(s->n, sizeof(double) > l->size ? sizeof(double) : l->size);
    char     name[64];

    for (size_t i = 0; i < s->n; i++)
        element(l, t, &s->p[i], raw + i * l->size);

    CHECK(space = H5Screate_simple(1, dims, NULL));
    snprintf(name, sizeof name, "%s_%s", l->name, sname);
    CHECK(ds = H5Dcreate2(file, name, t, space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT));
    CHECK(H5Dwrite(ds, t, H5S_ALL, H5S_ALL, H5P_DEFAULT, raw));
    CHECK(H5Dclose(ds));

    /* libhdf5's conversion when reading the dataset */
    snprintf(name, sizeof name, "%s_%s", l->name, sname);
    CHECK(ds = H5Dopen2(file, name, H5P_DEFAULT));
    CHECK(H5Dread(ds, H5T_NATIVE_DOUBLE, H5S_ALL, H5S_ALL, H5P_DEFAULT, conv));
    CHECK(H5Dclose(ds));
    snprintf(name, sizeof name, "%s_%s.expect", l->name, sname);
    CHECK(ds = H5Dcreate2(file, name, H5T_IEEE_F64LE, space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT));
    CHECK(H5Dwrite(ds, H5T_NATIVE_DOUBLE, H5S_ALL, H5S_ALL, H5P_DEFAULT, conv));
    CHECK(H5Dclose(ds));

    CHECK(H5Sclose(space));
    CHECK(H5Tclose(t));
    free(raw);
    free(conv);
}

static void
write_expect(hid_t file, const char *name, const double *v, size_t n)
{
    hid_t   space, ds;
    hsize_t dims[1] = {n};
    CHECK(space = H5Screate_simple(1, dims, NULL));
    CHECK(ds = H5Dcreate2(file, name, H5T_IEEE_F64LE, space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT));
    CHECK(H5Dwrite(ds, H5T_NATIVE_DOUBLE, H5S_ALL, H5S_ALL, H5P_DEFAULT, v));
    CHECK(H5Dclose(ds));
    CHECK(H5Sclose(space));
}

/* /quad_array and /quad_vlen (see above) */
static void
write_containers(hid_t file, const layout_t *quad)
{
    static set_t s;
    hid_t        t = make_type(quad), at, avt, vt, vvt, space, ds;
    hsize_t      adims[1] = {2}, dims[1] = {3};
    uint8_t      raw[3][2][16];
    double       conv[6];
    hvl_t        vl[3], vlr[3];
    pat_t        den, up;
    int          which[3][2] = {{0, 1}, {1, 1}, {1, 0}};

    /* the patterns: a denormal and 1.111..1 (all mantissa bits set) */
    s.n = 0;
    add(&s, quad, 0, 0, M_ALL);
    add(&s, quad, 0, quad->ebias, M_ALL);
    den = s.p[0];
    up  = s.p[1];
    for (int i = 0; i < 3; i++)
        for (int j = 0; j < 2; j++)
            element(quad, t, which[i][j] ? &up : &den, raw[i][j]);

    CHECK(space = H5Screate_simple(1, dims, NULL));
    CHECK(at = H5Tarray_create2(t, 1, adims));
    CHECK(avt = H5Tarray_create2(H5T_NATIVE_DOUBLE, 1, adims));
    CHECK(ds = H5Dcreate2(file, "quad_array", at, space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT));
    CHECK(H5Dwrite(ds, at, H5S_ALL, H5S_ALL, H5P_DEFAULT, raw));
    CHECK(H5Dread(ds, avt, H5S_ALL, H5S_ALL, H5P_DEFAULT, conv));
    CHECK(H5Dclose(ds));
    write_expect(file, "quad_array.expect", conv, 6);

    CHECK(vt = H5Tvlen_create(t));
    CHECK(vvt = H5Tvlen_create(H5T_NATIVE_DOUBLE));
    for (int i = 0; i < 3; i++) {
        vl[i].len = 2;
        vl[i].p   = raw[i];
    }
    CHECK(ds = H5Dcreate2(file, "quad_vlen", vt, space, H5P_DEFAULT, H5P_DEFAULT, H5P_DEFAULT));
    CHECK(H5Dwrite(ds, vt, H5S_ALL, H5S_ALL, H5P_DEFAULT, vl));
    CHECK(H5Dread(ds, vvt, H5S_ALL, H5S_ALL, H5P_DEFAULT, vlr));
    for (int i = 0; i < 3; i++)
        memcpy(conv + 2 * i, vlr[i].p, 2 * sizeof(double));
    CHECK(H5Treclaim(vvt, space, H5P_DEFAULT, vlr));
    CHECK(H5Dclose(ds));
    write_expect(file, "quad_vlen.expect", conv, 6);

    CHECK(H5Tclose(vvt));
    CHECK(H5Tclose(vt));
    CHECK(H5Tclose(avt));
    CHECK(H5Tclose(at));
    CHECK(H5Tclose(t));
    CHECK(H5Sclose(space));
}

int
main(void)
{
    static set_t round, all, rnd;
    layout_t     layouts[] = {
        {"e4m3", 1, 8, 0, 7, 3, 4, 0, 3, 7, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"bf16", 2, 16, 0, 15, 7, 8, 0, 7, 127, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"f24", 3, 24, 0, 23, 16, 7, 0, 16, 63, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"f24be", 4, 24, 8, 31, 24, 7, 8, 16, 63, H5T_ORDER_BE, H5T_NORM_IMPLIED, 0},
        {"e15m48", 8, 64, 0, 63, 48, 15, 0, 48, 16383, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"quad", 16, 128, 0, 127, 112, 15, 0, 112, 16383, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"quadbe", 16, 128, 0, 127, 112, 15, 0, 112, 16383, H5T_ORDER_BE, H5T_NORM_IMPLIED, 0},
        {"x87", 10, 80, 0, 79, 64, 15, 0, 64, 16383, H5T_ORDER_LE, H5T_NORM_NONE, 0},
        {"top", 16, 128, 0, 0, 1, 15, 16, 112, 16383, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"etop", 4, 32, 0, 0, 24, 8, 1, 23, 127, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"e63", 16, 128, 0, 127, 64, 63, 0, 64, ((size_t)1 << 31) - 1, H5T_ORDER_LE, H5T_NORM_IMPLIED, 0},
        {"vax32", 4, 32, 0, 31, 23, 8, 0, 23, 129, H5T_ORDER_VAX, H5T_NORM_IMPLIED, 0},
        {"vax64", 8, 64, 0, 63, 52, 11, 0, 52, 1025, H5T_ORDER_VAX, H5T_NORM_IMPLIED, 0},
    };
    size_t nl = sizeof layouts / sizeof layouts[0];
    hid_t  file;

    layouts[nl - 2].predef = H5T_VAX_F32;
    layouts[nl - 1].predef = H5T_VAX_F64;

    CHECK(file = H5Fcreate("conv_float.h5", H5F_ACC_TRUNC, H5P_DEFAULT, H5P_DEFAULT));
    for (size_t i = 0; i < nl; i++) {
        build_sets(&layouts[i], &round, &all, &rnd);
        write_set(file, &layouts[i], "round", &round);
        write_set(file, &layouts[i], "all", &all);
        write_set(file, &layouts[i], "rand", &rnd);
    }
    write_containers(file, &layouts[5]); /* quad */
    CHECK(H5Fclose(file));
    return 0;
}
