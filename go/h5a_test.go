package hdf5

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"testing"

	"github.com/crhntr/hdf5/go/h5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attrObject is an object header reached by attrWalk.
type attrObject struct {
	path string
	oh   *h5o
}

// attrWalk loads the root object header of f and every object reachable
// from it through hard links (symbol tables, link messages and dense link
// storage), at most limit headers, in breadth first order. Errors loading a
// header or a group are returned with the objects found.
func attrWalk(f *File, limit int) ([]attrObject, []error) {
	type item struct {
		path string
		addr haddr
	}
	var (
		objs []attrObject
		errs []error
	)
	seen := map[haddr]bool{}
	queue := []item{{"/", f.sblock.rootAddr}}
	join := func(dir, name string) string {
		if dir == "/" {
			return "/" + name
		}
		return dir + "/" + name
	}
	for len(queue) > 0 && len(objs) < limit {
		it := queue[0]
		queue = queue[1:]
		if seen[it.addr] || !addrDefined(it.addr) {
			continue
		}
		seen[it.addr] = true
		oh, err := f.oProtect(it.addr)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.path, err))
			continue
		}
		objs = append(objs, attrObject{it.path, oh})
		addLink := func(l *h5oLink) {
			if l.typ == h5.LinkHard {
				queue = append(queue, item{join(it.path, l.name), l.addr})
			}
		}
		for _, m := range oh.mesgs {
			switch m.typeID {
			case h5oStabID, h5oLinkID, h5oLinfoID:
			default:
				continue
			}
			native, err := f.h5oLoadNative(oh, m)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: message %#x: %w", it.path, m.typeID, err))
				continue
			}
			switch v := native.(type) {
			case *h5oLink:
				addLink(v)
			case *h5oStab:
				err := f.gStabIterate(v, func(name string, ent *h5gEntry) error {
					queue = append(queue, item{join(it.path, name), ent.header})
					return nil
				})
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: symbol table: %w", it.path, err))
				}
			case *h5oLinfo:
				if !addrDefined(v.fheapAddr) {
					continue
				}
				err := denseLinks(f, v, addLink)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: dense links: %w", it.path, err))
				}
			}
		}
	}
	return objs, errs
}

// denseLinks calls op on every link stored in the dense link storage of
// a group (the link messages in the fractal heap, through the name index).
func denseLinks(f *File, linfo *h5oLinfo, op func(l *h5oLink)) error {
	fheap, err := f.hfOpen(linfo.fheapAddr)
	if err != nil {
		return err
	}
	bt2, err := f.b2Open(linfo.nameBt2Addr, h5gBt2Name(), nil)
	if err != nil {
		return err
	}
	return bt2.iterate(func(rec any) error {
		r := rec.(*h5gDenseBt2NameRec)
		obj, err := fheap.read(r.id[:])
		if err != nil {
			return err
		}
		l, err := h5oLinkDecode(f, f.newImage(obj))
		if err != nil {
			return err
		}
		op(l)
		return nil
	})
}

// objectAttrs collects the attributes of an object through aIterate.
func objectAttrs(f *File, oh *h5o) ([]*h5oAttr, error) {
	var attrs []*h5oAttr
	err := f.aIterate(oh, func(attr *h5oAttr) error {
		attrs = append(attrs, attr)
		return nil
	})
	return attrs, err
}

// h5dumpAttrNames runs "h5dump -A" on path and returns the names of all
// attributes it prints (h5dump prints each object once, and the names
// without escaping), or ok=false when
// h5dump is not installed or fails on the file.
func h5dumpAttrNames(t *testing.T, path string) (names []string, ok bool) {
	bin := filepath.Join(os.Getenv("HOME"), ".local/hdf5-1.14.6/bin/h5dump")
	if _, err := os.Stat(bin); err != nil {
		return nil, false
	}
	cmd := exec.Command(bin, "-A", path)
	cmd.Dir = filepath.Dir(path)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	re := regexp.MustCompile(`(?m)^\s*ATTRIBUTE "(.*)" \{$`)
	for _, m := range re.FindAllSubmatch(out, -1) {
		names = append(names, string(m[1]))
	}
	return names, true
}

// attrSweepKnown lists corpus files whose attributes cannot all be read,
// with the reason (files that libhdf5 cannot read either).
var attrSweepKnown = map[string]string{}

func TestAttributeCorpus(t *testing.T) {
	root := repoRoot(t)
	files := corpusFiles(t)
	require.NotEmpty(t, files)

	var (
		nfiles, nobjects, nattrs, ndense, nsharedAttrs, nsharedParts int
		ncompared, nmismatch                                         int
	)
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		known := h5dumpFailures[rel] && rel != "tools/test/h5stat/testfiles/h5stat_newgrat.h5"
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v\n%s", rel, r, debug.Stack())
				}
			}()
			f, err := Open(path, WithExternalLinks(false))
			if err != nil {
				return
			}
			defer f.Close()
			report := func(format string, args ...any) {
				msg := fmt.Sprintf("%s: ", rel) + fmt.Sprintf(format, args...)
				if known || attrSweepKnown[rel] != "" {
					t.Log(msg)
				} else {
					t.Error(msg)
				}
			}
			objs, errs := attrWalk(f, 20000)
			for _, err := range errs {
				if ohdrKnownFailures[rel] == "" {
					report("%v", err)
				}
			}
			nfiles++
			var names []string
			for _, obj := range objs {
				nobjects++
				count, err := f.aCount(obj.oh)
				if err != nil {
					report("%s: count: %v", obj.path, err)
					continue
				}
				attrs, err := objectAttrs(f, obj.oh)
				if err != nil {
					report("%s: iterate: %v", obj.path, err)
					continue
				}
				if ainfo, _ := f.h5aGetAinfo(obj.oh); ainfo != nil && addrDefined(ainfo.fheapAddr) {
					ndense++
				}
				if uint64(len(attrs)) != count {
					report("%s: aCount = %d, aIterate saw %d attributes", obj.path, count, len(attrs))
				}
				for i, attr := range attrs {
					nattrs++
					names = append(names, attr.name)
					if attr.shared != nil && attr.shared.typ == h5oShareTypeSohm {
						nsharedAttrs++
					}
					if attr.dtShared != nil || attr.dsShared != nil {
						nsharedParts++
					}
					if i > 0 && !(attrs[i-1].name < attr.name) {
						report("%s: attributes out of order: %q before %q", obj.path, attrs[i-1].name, attr.name)
					}
					opened, err := f.aOpenByName(obj.oh, attr.name)
					if !assert.NoError(t, err, "%s: %s: open %q", rel, obj.path, attr.name) {
						continue
					}
					assert.Equal(t, attr.name, opened.name)
					assert.Equal(t, attr.data, opened.data, "%s: %s: %q", rel, obj.path, attr.name)
					assert.Equal(t, attr.dt, opened.dt, "%s: %s: %q", rel, obj.path, attr.name)
					vals, err := f.aRead(opened)
					if err != nil {
						report("%s: read %q: %v", obj.path, attr.name, err)
						continue
					}
					assert.Len(t, vals, int(h5sExtentNelem(opened.ds)))
				}
				_, err = f.aOpenByName(obj.oh, "\x00no such attribute")
				assert.ErrorIs(t, err, ErrNotFound, "%s: %s", rel, obj.path)
			}
			if known || len(objs) >= 20000 {
				return
			}
			dumped, ok := h5dumpAttrNames(t, path)
			if !ok {
				return
			}
			ncompared++
			got := append([]string(nil), names...)
			sort.Strings(got)
			sort.Strings(dumped)
			if !assert.Equal(t, dumped, got, "%s: attribute names (h5dump -A)", rel) {
				nmismatch++
			}
		}()
	}
	t.Logf("attributes: %d files, %d objects, %d attributes (%d objects with dense storage, %d attributes shared in the SOHM heap, %d with shared datatype/dataspace); %d files compared with h5dump -A, %d mismatches",
		nfiles, nobjects, nattrs, ndense, nsharedAttrs, nsharedParts, ncompared, nmismatch)
	assert.Positive(t, ndense)
	assert.Positive(t, nsharedParts)
}
