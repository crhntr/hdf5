//go:build cgo

package chdf5

/*
#cgo pkg-config: hdf5
#include <stdio.h>
#include "chdf5.h"

void chdf5_names_free(chdf5_names *l) {
    for (size_t i = 0; i < l->n; i++)
        free(l->names[i]);
    free(l->names);
    l->names = NULL;
    l->n = l->cap = 0;
}

static int names_add(chdf5_names *l, const char *name) {
    if (l->n == l->cap) {
        size_t cap = l->cap ? 2 * l->cap : 16;
        char **p = realloc(l->names, cap * sizeof *p);
        if (!p)
            return -1;
        l->names = p;
        l->cap = cap;
    }
    if (!(l->names[l->n] = strdup(name)))
        return -1;
    l->n++;
    return 0;
}

static herr_t link_cb(hid_t g, const char *name, const H5L_info2_t *info, void *data) {
    (void)g; (void)info;
    return names_add((chdf5_names *)data, name) < 0 ? -1 : 0;
}

int chdf5_link_names(hid_t g, int idx_type, chdf5_names *out) {
    hsize_t idx = 0;
    return H5Literate2(g, (H5_index_t)idx_type, H5_ITER_INC, &idx, link_cb, out) < 0 ? -1 : 0;
}

static herr_t attr_cb(hid_t obj, const char *name, const H5A_info_t *info, void *data) {
    (void)obj; (void)info;
    return names_add((chdf5_names *)data, name) < 0 ? -1 : 0;
}

int chdf5_attr_names(hid_t obj, chdf5_names *out) {
    hsize_t idx = 0;
    return H5Aiterate2(obj, H5_INDEX_NAME, H5_ITER_INC, &idx, attr_cb, out) < 0 ? -1 : 0;
}

typedef struct {
    char  *buf;
    size_t len, cap;
} sbuf;

static void sb_add(sbuf *b, const char *s) {
    size_t n = strlen(s);
    if (b->len + n + 1 > b->cap) {
        size_t cap = b->cap ? b->cap : 256;
        while (cap < b->len + n + 1)
            cap *= 2;
        char *p = realloc(b->buf, cap);
        if (!p)
            return;
        b->buf = p;
        b->cap = cap;
    }
    memcpy(b->buf + b->len, s, n + 1);
    b->len += n;
}

static herr_t err_cb(unsigned n, const H5E_error2_t *e, void *data) {
    sbuf *b = (sbuf *)data;
    char  maj[160] = "", min[160] = "", line[1024];
    H5Eget_msg(e->maj_num, NULL, maj, sizeof maj);
    H5Eget_msg(e->min_num, NULL, min, sizeof min);
    snprintf(line, sizeof line, "\n  #%03u: %s line %u in %s(): %s (%s / %s)", n,
             e->file_name ? e->file_name : "?", e->line, e->func_name ? e->func_name : "?",
             e->desc ? e->desc : "", maj, min);
    sb_add(b, line);
    return 0;
}

char *chdf5_error_stack(void) {
    sbuf  b  = {NULL, 0, 0};
    hid_t st = H5Eget_current_stack();
    if (st < 0)
        return NULL;
    H5Ewalk2(st, H5E_WALK_DOWNWARD, err_cb, &b);
    H5Eclose_stack(st);
    return b.buf;
}

void chdf5_errors_off(void) { H5Eset_auto2(H5E_DEFAULT, NULL, NULL); }

static void fill_oinfo(hid_t loc, const H5O_info2_t *i, chdf5_oinfo *o) {
    haddr_t addr = HADDR_UNDEF;
    H5VLnative_token_to_addr(loc, i->token, &addr);
    o->addr      = addr;
    o->type      = (int)i->type;
    o->rc        = i->rc;
    o->num_attrs = i->num_attrs;
    o->atime     = (int64_t)i->atime;
    o->mtime     = (int64_t)i->mtime;
    o->ctime     = (int64_t)i->ctime;
    o->btime     = (int64_t)i->btime;
}

int chdf5_get_oinfo(hid_t id, int with_hdr, chdf5_oinfo *o) {
    H5O_info2_t i;
    memset(o, 0, sizeof *o);
    if (H5Oget_info3(id, &i, H5O_INFO_ALL) < 0)
        return -1;
    fill_oinfo(id, &i, o);
    if (with_hdr) {
        H5O_native_info_t n;
        if (H5Oget_native_info(id, &n, H5O_NATIVE_INFO_HDR) < 0)
            return -1;
        o->hdr_version = n.hdr.version;
    }
    return 0;
}

int chdf5_get_oinfo_by_name(hid_t loc, const char *name, hid_t lapl, chdf5_oinfo *o) {
    H5O_info2_t i;
    memset(o, 0, sizeof *o);
    if (H5Oget_info_by_name3(loc, name, &i, H5O_INFO_BASIC, lapl) < 0)
        return -1;
    fill_oinfo(loc, &i, o);
    return 0;
}

int chdf5_get_linfo(hid_t g, const char *name, hid_t lapl, chdf5_linfo *o) {
    H5L_info2_t i;
    memset(o, 0, sizeof *o);
    if (H5Lget_info2(g, name, &i, lapl) < 0)
        return -1;
    o->type         = (int)i.type;
    o->corder_valid = i.corder_valid ? 1 : 0;
    o->corder       = i.corder;
    o->cset         = (int)i.cset;
    o->addr         = HADDR_UNDEF;
    if (i.type == H5L_TYPE_HARD) {
        if (H5VLnative_token_to_addr(g, i.u.token, &o->addr) < 0)
            return -1;
    }
    else
        o->val_size = i.u.val_size;
    return 0;
}

int chdf5_ref2(hid_t loc, H5R_ref_t *ref, int *type, haddr_t *addr, hid_t *space, char **attr_name) {
    H5O_token_t tok;
    *type      = (int)H5Rget_type(ref);
    *space     = H5I_INVALID_HID;
    *attr_name = NULL;
    *addr      = HADDR_UNDEF;
    if (*type < 0)
        return -1;
    // H5R_ref_t is an opaque buffer holding an H5R_ref_priv_t, whose first
    // member (for every reference type) is the object token. Reading it
    // avoids opening the referenced object, which may have been deleted or
    // live in another file.
    memcpy(&tok, ref, sizeof tok);
    if (H5VLnative_token_to_addr(loc, tok, addr) < 0)
        return -1;
    if (*type == H5R_DATASET_REGION1 || *type == H5R_DATASET_REGION2) {
        *space = H5Ropen_region(ref, H5P_DEFAULT, H5P_DEFAULT);
        if (*space < 0)
            return -1;
    }
    else if (*type == H5R_ATTR) {
        ssize_t n = H5Rget_attr_name(ref, NULL, 0);
        if (n < 0)
            return -1;
        *attr_name = malloc((size_t)n + 1);
        if (!*attr_name || H5Rget_attr_name(ref, *attr_name, (size_t)n + 1) < 0)
            return -1;
    }
    return 0;
}

hid_t chdf5_open_addr(hid_t loc, haddr_t addr) {
    H5O_token_t tok;
    if (H5VLnative_addr_to_token(loc, addr, &tok) < 0)
        return H5I_INVALID_HID;
    return H5Oopen_by_token(loc, tok);
}

static herr_t deny_cb(const char *parent_file_name, const char *parent_group_name,
                      const char *child_file_name, const char *child_object_name, unsigned *acc_flags,
                      hid_t fapl_id, void *op_data) {
    (void)parent_file_name; (void)parent_group_name; (void)child_file_name;
    (void)child_object_name; (void)acc_flags; (void)fapl_id; (void)op_data;
    return -1;
}

int chdf5_deny_elinks(hid_t lapl) { return H5Pset_elink_cb(lapl, deny_cb, NULL) < 0 ? -1 : 0; }
*/
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"
)

// mu serializes every call into libhdf5, which is not built thread-safe.
var mu sync.Mutex

func init() {
	mu.Lock()
	defer mu.Unlock()
	C.H5open()
	C.chdf5_errors_off()
}

// Error is a libhdf5 failure: the operation that failed and the text of the
// HDF5 error stack at that point.
type Error struct {
	Op    string
	Stack string
}

func (e *Error) Error() string {
	if e.Stack == "" {
		return "chdf5: " + e.Op
	}
	return "chdf5: " + e.Op + ":" + e.Stack
}

// h5err builds an Error from the current HDF5 error stack (and clears it).
// The caller must hold mu.
func h5err(format string, args ...any) error {
	e := &Error{Op: fmt.Sprintf(format, args...)}
	if s := C.chdf5_error_stack(); s != nil {
		e.Stack = strings.TrimRight(C.GoString(s), "\n")
		C.free(unsafe.Pointer(s))
	}
	return e
}

// takeNames converts and frees a chdf5_names list.
func takeNames(l *C.chdf5_names) []string {
	defer C.chdf5_names_free(l)
	if l.n == 0 {
		return nil
	}
	ptrs := unsafe.Slice(l.names, int(l.n))
	out := make([]string, len(ptrs))
	for i, p := range ptrs {
		out[i] = C.GoString(p)
	}
	return out
}

// cstr returns a C copy of s and a function freeing it.
func cstr(s string) (*C.char, func()) {
	c := C.CString(s)
	return c, func() { C.free(unsafe.Pointer(c)) }
}

const haddrUndef = ^uint64(0)
