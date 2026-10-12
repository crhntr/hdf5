/* Helpers shared by the cgo files of package chdf5 (definitions in the
 * preamble of helpers.go). They keep libhdf5 callbacks and unions on the C
 * side so the Go code only deals with plain structs. */
#ifndef CHDF5_H
#define CHDF5_H

#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include "hdf5.h"

/* A growable list of malloc'd strings. */
typedef struct {
    char  **names;
    size_t  n, cap;
} chdf5_names;

void chdf5_names_free(chdf5_names *l);
int  chdf5_link_names(hid_t g, int idx_type, chdf5_names *out);
int  chdf5_attr_names(hid_t obj, chdf5_names *out);

/* Formatted copy of the current default error stack (malloc'd, may be NULL);
 * clears the stack. */
char *chdf5_error_stack(void);
void  chdf5_errors_off(void);

typedef struct {
    haddr_t  addr;
    int      type;
    unsigned rc;
    unsigned hdr_version;
    hsize_t  num_attrs;
    int64_t  atime, mtime, ctime, btime;
} chdf5_oinfo;

int chdf5_get_oinfo(hid_t id, int with_hdr, chdf5_oinfo *o);
int chdf5_get_oinfo_by_name(hid_t loc, const char *name, hid_t lapl, chdf5_oinfo *o);

typedef struct {
    int      type;
    int      corder_valid;
    int64_t  corder;
    int      cset;
    haddr_t  addr;
    size_t   val_size;
} chdf5_linfo;

int chdf5_get_linfo(hid_t g, const char *name, hid_t lapl, chdf5_linfo *o);

/* Reference read as H5R_ref_t: type, object address, region/attribute. */
int chdf5_ref2(hid_t loc, H5R_ref_t *ref, int *type, haddr_t *addr, hid_t *space, char **attr_name);

/* Open an object by address. */
hid_t chdf5_open_addr(hid_t loc, haddr_t addr);

/* Deny external link traversal (H5Pset_elink_cb). */
int chdf5_deny_elinks(hid_t lapl);

/* Read a pointer / size_t stored at p (vlen string and hvl_t elements). */
static inline void *chdf5_ptr_at(const void *p) { void *q; memcpy(&q, p, sizeof q); return q; }
static inline size_t chdf5_size_at(const void *p) { size_t q; memcpy(&q, p, sizeof q); return q; }
static inline void chdf5_put_ptr(void *p, const void *q) { memcpy(p, &q, sizeof q); }

#endif
