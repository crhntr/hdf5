package hdf5

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// h5dumpFailures lists the corpus files (relative to the repository root)
// on which "h5dump -H" from HDF5 1.14.6 fails: deliberately corrupt test
// inputs, member files of family/multi/split files opened without their
// driver, files with an "open for writing" status flag, subfiling files.
// The object header sweep only checks that these do not panic.
var h5dumpFailures = map[string]bool{}

func init() {
	for _, name := range []string{
		"c++/test/th5s.h5",
		"test/cve_2020_10812.h5",
		"test/testfiles/bad_compound.h5",
		"test/testfiles/bad_offset.h5",
		"test/testfiles/corrupt_stab_msg.h5",
		"test/testfiles/cve_2020_10810.h5",
		"test/testfiles/cve_2020_10812.h5",
		"test/testfiles/family_v16-000000.h5",
		"test/testfiles/family_v16-000001.h5",
		"test/testfiles/family_v16-000002.h5",
		"test/testfiles/family_v16-000003.h5",
		"test/testfiles/memleak_H5O_dtype_decode_helper_H5Odtype.h5",
		"test/testfiles/multi_file_v16-r.h5",
		"test/testfiles/multi_file_v16-s.h5",
		"test/testfiles/tbogus.h5",
		"test/testfiles/th5s.h5",
		"test/testfiles/tmisc38a.h5",
		"test/testfiles/tmisc38b.h5",
		"tools/test/h5dump/testfiles/3790_infinite_loop.h5",
		"tools/test/h5dump/testfiles/err_attr_dspace.h5",
		"tools/test/h5dump/testfiles/tCVE_2018_11206_fill_new.h5",
		"tools/test/h5dump/testfiles/tCVE_2018_11206_fill_old.h5",
		"tools/test/h5dump/testfiles/tCVE-2021-37501_attr_decode.h5",
		"tools/test/h5dump/testfiles/test_subfiling_precreate_rank_0.h5",
		"tools/test/h5dump/testfiles/test_subfiling_stripe_sizes.h5",
		"tools/test/h5dump/testfiles/tfamily00000.h5",
		"tools/test/h5dump/testfiles/tfamily00001.h5",
		"tools/test/h5dump/testfiles/tfamily00002.h5",
		"tools/test/h5dump/testfiles/tfamily00003.h5",
		"tools/test/h5dump/testfiles/tfamily00004.h5",
		"tools/test/h5dump/testfiles/tfamily00005.h5",
		"tools/test/h5dump/testfiles/tfamily00006.h5",
		"tools/test/h5dump/testfiles/tfamily00007.h5",
		"tools/test/h5dump/testfiles/tfamily00008.h5",
		"tools/test/h5dump/testfiles/tfamily00009.h5",
		"tools/test/h5dump/testfiles/tfamily00010.h5",
		"tools/test/h5dump/testfiles/tmulti-b.h5",
		"tools/test/h5dump/testfiles/tmulti-g.h5",
		"tools/test/h5dump/testfiles/tmulti-l.h5",
		"tools/test/h5dump/testfiles/tmulti-o.h5",
		"tools/test/h5dump/testfiles/tmulti-r.h5",
		"tools/test/h5dump/testfiles/tmulti-s.h5",
		"tools/test/h5dump/testfiles/tsplit_file-m.h5",
		"tools/test/h5dump/testfiles/tsplit_file-r.h5",
		"tools/test/h5repack/testfiles/h5repack_CVE-2018-14460.h5",
		"tools/test/h5repack/testfiles/h5repack_CVE-2018-17432.h5",
		"tools/test/h5repack/testfiles/h5repack_named_dtypes.h5",
		"tools/test/h5stat/testfiles/h5stat_err_old_fill.h5",
		"tools/test/h5stat/testfiles/h5stat_err_old_layout.h5",
		"tools/test/h5stat/testfiles/h5stat_err_refcount.h5",
		"tools/test/h5stat/testfiles/h5stat_newgrat.h5",
		"tools/test/misc/testfiles/family_file00000.h5",
		"tools/test/misc/testfiles/family_file00001.h5",
		"tools/test/misc/testfiles/family_file00002.h5",
		"tools/test/misc/testfiles/family_file00003.h5",
		"tools/test/misc/testfiles/family_file00004.h5",
		"tools/test/misc/testfiles/family_file00005.h5",
		"tools/test/misc/testfiles/family_file00006.h5",
		"tools/test/misc/testfiles/family_file00007.h5",
		"tools/test/misc/testfiles/family_file00008.h5",
		"tools/test/misc/testfiles/family_file00009.h5",
		"tools/test/misc/testfiles/family_file00010.h5",
		"tools/test/misc/testfiles/family_file00011.h5",
		"tools/test/misc/testfiles/family_file00012.h5",
		"tools/test/misc/testfiles/family_file00013.h5",
		"tools/test/misc/testfiles/family_file00014.h5",
		"tools/test/misc/testfiles/family_file00015.h5",
		"tools/test/misc/testfiles/family_file00016.h5",
		"tools/test/misc/testfiles/family_file00017.h5",
		"tools/test/misc/testfiles/h5clear_fsm_persist_greater.h5",
		"tools/test/misc/testfiles/h5clear_fsm_persist_less.h5",
		"tools/test/misc/testfiles/h5clear_fsm_persist_noclose.h5",
		"tools/test/misc/testfiles/h5clear_fsm_persist_user_greater.h5",
		"tools/test/misc/testfiles/h5clear_log_v3.h5",
		"tools/test/misc/testfiles/h5clear_sec2_v3.h5",
		"tools/test/misc/testfiles/h5clear_status_noclose.h5",
		"tools/test/misc/testfiles/latest_h5clear_log_v3.h5",
		"tools/test/misc/testfiles/latest_h5clear_sec2_v3.h5",
	} {
		h5dumpFailures[name] = true
	}
}

// ohdrKnownFailures lists corpus files that h5dump reads but this package
// cannot, with the reason.
var ohdrKnownFailures = map[string]string{
	// The root group's object header is only in the metadata cache image
	// (the superblock extension's H5O_MDCI_MSG_ID message), which is not
	// translated.
	"tools/test/misc/testfiles/h5clear_mdc_image.h5": "metadata cache image",
}

// ohdrWalkResult is what walkObjectHeaders found in one file.
type ohdrWalkResult struct {
	headers  int            // object headers loaded
	messages map[uint16]int // decoded messages, by type
	errs     []error        // decode errors
	ignored  int            // undecodable messages flagged "was unknown"
}

// walkObjectHeaders loads the root object header of f and every object
// reachable from it through symbol tables and link messages (hard links
// only, at most limit headers), decoding every message.
func walkObjectHeaders(t testing.TB, f *File, limit int) ohdrWalkResult {
	res := ohdrWalkResult{messages: map[uint16]int{}}
	seen := map[haddr]bool{}
	queue := []haddr{f.sblock.rootAddr}
	if addrDefined(f.sblock.extAddr) {
		queue = append(queue, f.sblock.extAddr)
	}
	for len(queue) > 0 && res.headers < limit {
		addr := queue[0]
		queue = queue[1:]
		if seen[addr] || !addrDefined(addr) {
			continue
		}
		seen[addr] = true
		oh, err := f.oProtect(addr)
		if err != nil {
			res.errs = append(res.errs, fmt.Errorf("header %d: %w", addr, err))
			continue
		}
		res.headers++
		for _, m := range oh.mesgs {
			native, err := f.h5oLoadNative(oh, m)
			if err != nil && m.flags&h5oMsgFlagWasUnknown != 0 {
				// A message that an earlier library version did not know
				// and marked as such: the C library ignores its contents
				// (e.g. pre-release file space info messages).
				res.ignored++
				continue
			}
			if err != nil {
				res.errs = append(res.errs, fmt.Errorf("header %d message %#x: %w", addr, m.typeID, err))
				continue
			}
			res.messages[m.typeID]++
			switch v := native.(type) {
			case *h5oLink:
				if v.typ == 0 {
					queue = append(queue, v.addr)
				}
			case *h5oStab:
				err := f.gStabIterate(v, func(name string, ent *h5gEntry) error {
					queue = append(queue, ent.header)
					return nil
				})
				if err != nil {
					res.errs = append(res.errs, fmt.Errorf("header %d symbol table: %w", addr, err))
				}
			}
		}
		if addr == f.sblock.extAddr {
			continue
		}
		if _, err := f.oGetInfo(oh); err != nil {
			res.errs = append(res.errs, fmt.Errorf("header %d info: %w", addr, err))
		}
	}
	return res
}

func TestObjectHeaderCorpus(t *testing.T) {
	root := repoRoot(t)
	files := corpusFiles(t)
	require.NotEmpty(t, files)

	var clean, withErrs, openFail, skipped int
	totals := map[uint16]int{}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		known := h5dumpFailures[rel]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v\n%s", rel, r, debug.Stack())
				}
			}()
			f, err := Open(path, WithExternalLinks(false))
			if err != nil {
				if known || errors.Is(err, ErrNotHDF5) {
					skipped++
					return
				}
				openFail++
				t.Errorf("%s: open: %v", rel, err)
				return
			}
			defer f.Close()
			res := walkObjectHeaders(t, f, 500)
			if known {
				skipped++
				return
			}
			for k, v := range res.messages {
				totals[k] += v
			}
			if len(res.errs) == 0 {
				clean++
				return
			}
			withErrs++
			for _, e := range res.errs {
				switch {
				case errors.Is(e, ErrUnsupported):
					// A module that is not translated yet (selections
					// for virtual datasets, the shared message heap).
					t.Logf("%s: %v", rel, e)
				case ohdrKnownFailures[rel] != "":
					t.Logf("%s (%s): %v", rel, ohdrKnownFailures[rel], e)
				default:
					t.Errorf("%s: %v", rel, e)
				}
			}
		}()
	}
	var ids []int
	for k := range totals {
		ids = append(ids, int(k))
	}
	sort.Ints(ids)
	var sb strings.Builder
	for _, k := range ids {
		fmt.Fprintf(&sb, " %#x:%d", k, totals[uint16(k)])
	}
	t.Logf("corpus: %d files, %d clean, %d with decode errors, %d open failures, %d skipped (h5dump fails or not HDF5)", len(files), clean, withErrs, openFail, skipped)
	t.Logf("decoded messages by type:%s", sb.String())
	assert.Zero(t, openFail)
	assert.GreaterOrEqual(t, clean, 360)
}
