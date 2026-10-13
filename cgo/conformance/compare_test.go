//go:build cgo

package conformance

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"testing"

	chdf5 "github.com/crhntr/hdf5/cgo"
	"github.com/crhntr/hdf5/cgo/internal/golden"
	"github.com/crhntr/hdf5/go"
	"github.com/crhntr/hdf5/go/h5"
)

// maxReadBytes bounds the size of the datasets whose values are compared
// (their metadata and a hyperslab are compared regardless of size).
const maxReadBytes = 64 << 20

// maxReadTBytes bounds the size of the datasets also read with Read[T].
const maxReadTBytes = 1 << 20

// stats counts what was compared.
type stats struct {
	files, filesBothFail          int
	objects, attributes, datasets int
	datasetsRead, hyperslabs      int
	values                        int
	bothFailFiles                 []string       // files neither package opens
	skipped                       map[string]int // reason -> count
	allowed                       map[string]int // allow-list entry -> count
}

func newStats() *stats {
	return &stats{skipped: map[string]int{}, allowed: map[string]int{}}
}

func (s *stats) skip(reason string) { s.skipped[reason]++ }

func (s *stats) add(o *stats) {
	s.files += o.files
	s.filesBothFail += o.filesBothFail
	s.bothFailFiles = append(s.bothFailFiles, o.bothFailFiles...)
	s.objects += o.objects
	s.attributes += o.attributes
	s.datasets += o.datasets
	s.datasetsRead += o.datasetsRead
	s.hyperslabs += o.hyperslabs
	s.values += o.values
	for k, v := range o.skipped {
		s.skipped[k] += v
	}
	for k, v := range o.allowed {
		s.allowed[k] += v
	}
}

func (s *stats) summary() string {
	return fmt.Sprintf("%d objects, %d attributes, %d datasets (%d read in full, %d hyperslabs), %d values",
		s.objects, s.attributes, s.datasets, s.datasetsRead, s.hyperslabs, s.values)
}

// table renders the final report.
func (s *stats) table() string {
	var b strings.Builder
	row := func(k string, v int) { fmt.Fprintf(&b, "  %-58s %8d\n", k, v) }
	b.WriteString("\nconformance summary\n")
	row("files compared", s.files)
	row("files neither package opens", s.filesBothFail)
	row("objects", s.objects)
	row("attributes", s.attributes)
	row("datasets", s.datasets)
	row("datasets read in full", s.datasetsRead)
	row("hyperslabs read", s.hyperslabs)
	row("values compared", s.values)
	section := func(title string, m map[string]int) {
		if len(m) == 0 {
			return
		}
		b.WriteString(title + "\n")
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			row("  "+k, m[k])
		}
	}
	section("skipped (both packages agree):", s.skipped)
	section("allowed differences:", s.allowed)
	if len(s.bothFailFiles) > 0 {
		sort.Strings(s.bothFailFiles)
		b.WriteString("files neither package opens:\n")
		for _, f := range s.bothFailFiles {
			b.WriteString("    " + f + "\n")
		}
	}
	return b.String()
}

// totals accumulates the stats of the parallel subtests.
type totals struct {
	mu sync.Mutex
	s  *stats
}

func (t *totals) add(s *stats) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.s.add(s)
}

// comparer compares one file opened with both packages.
type comparer struct {
	t    *testing.T
	rel  string // file name relative to the repository root (allow-list key)
	st   *stats
	done map[uint64]bool // objects of the file whose contents were compared, by address

	// The files, for OpenAddress on object references; nil when comparing
	// objects of another file (external link targets).
	cf *chdf5.File
	hf *hdf5.File
}

// mismatch reports a difference: a test failure unless the allow-list
// covers it.
func (c *comparer) mismatch(object, aspect, format string, args ...any) {
	c.t.Helper()
	msg := fmt.Sprintf(format, args...)
	if why, ok := allowedDifference(c.rel, object, aspect, msg); ok {
		c.st.allowed[why]++
		c.t.Logf("allowed difference: %s %s (%s): %s", object, aspect, why, msg)
		return
	}
	c.t.Errorf("%s: %s: %s", object, aspect, msg)
}

// sameErr checks that both packages succeed or both fail. It reports true
// when both succeeded.
func (c *comparer) sameErr(object, aspect string, cerr, herr error) bool {
	c.t.Helper()
	switch {
	case cerr == nil && herr == nil:
		return true
	case cerr != nil && herr != nil:
		return false
	case cerr != nil:
		c.mismatch(object, aspect, "libhdf5 fails (%v) but hdf5 succeeds", cerr)
	default:
		c.mismatch(object, aspect, "libhdf5 succeeds but hdf5 fails: %v", herr)
	}
	return false
}

// equal reports a mismatch when want and got differ (reflect.DeepEqual).
func (c *comparer) equal(object, aspect string, want, got any) bool {
	c.t.Helper()
	if reflect.DeepEqual(want, got) {
		return true
	}
	c.mismatch(object, aspect, "%s\nlibhdf5: %s\nhdf5:    %s",
		diffDeep("", reflect.ValueOf(want), reflect.ValueOf(got)), dump(want), dump(got))
	return false
}

// diffDeep describes the first difference between two values of the same
// type (as reflect.DeepEqual sees it), with its path.
func diffDeep(path string, a, b reflect.Value) string {
	differ := func() string {
		return fmt.Sprintf("first difference at %q: libhdf5 %s, hdf5 %s", path, short(a.Interface()), short(b.Interface()))
	}
	if !a.IsValid() || !b.IsValid() {
		if a.IsValid() != b.IsValid() {
			return fmt.Sprintf("first difference at %q: validity", path)
		}
		return ""
	}
	if a.Type() != b.Type() {
		return differ()
	}
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return differ()
			}
			return ""
		}
		return diffDeep(path, a.Elem(), b.Elem())
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if d := diffDeep(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i)); d != "" {
				return d
			}
		}
		return ""
	case reflect.Slice, reflect.Array:
		if a.Kind() == reflect.Slice && a.IsNil() != b.IsNil() {
			return fmt.Sprintf("first difference at %q: nil-ness: libhdf5 %s, hdf5 %s", path, short(a.Interface()), short(b.Interface()))
		}
		if a.Len() != b.Len() {
			return differ()
		}
		for i := 0; i < a.Len(); i++ {
			if d := diffDeep(fmt.Sprintf("%s[%d]", path, i), a.Index(i), b.Index(i)); d != "" {
				return d
			}
		}
		return ""
	case reflect.Map:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			return differ()
		}
		return ""
	}
	if !reflect.DeepEqual(a.Interface(), b.Interface()) {
		return differ()
	}
	return ""
}

func dump(v any) string {
	s := fmt.Sprintf("%+v", v)
	if dt, ok := v.(*h5.Datatype); ok && dt != nil {
		s = fmt.Sprintf("%+v", *dt)
	}
	if len(s) > 2000 {
		s = s[:2000] + "..."
	}
	return s
}

func oneLine(err error) string {
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// noPanic turns a panic of the code under test into a test failure.
func noPanic(t *testing.T) {
	if r := recover(); r != nil {
		t.Errorf("panic: %v\n%s", r, debug.Stack())
	}
}

// fileOptions are the options both packages open a file with.
type fileOptions struct {
	hdf5  []hdf5.Option
	chdf5 []chdf5.Option
}

// compareFile opens name with both packages and compares everything.
// rel identifies the file in reports and in the allow-list.
func compareFile(t *testing.T, name, rel string, opts fileOptions) *stats {
	st := newStats()
	defer noPanic(t)
	c := &comparer{t: t, rel: rel, st: st, done: map[uint64]bool{}}

	cf, cerr := chdf5.Open(name, opts.chdf5...)
	hf, herr := hdf5.Open(name, opts.hdf5...)
	if hf != nil {
		defer hf.Close()
		// chdf5.Open opens the root group (H5Fopen, then H5Oget_info3 on
		// "/"), which loads its object header; hdf5.Open loads it lazily.
		if _, err := hf.Root().Info(); err != nil {
			herr = err
		}
	}
	if cerr != nil && herr == nil {
		// hdf5 detects multi-file drivers from the superblock, libhdf5
		// needs to be told (see API.md "Options").
		if alt, driver := openWithDriver(name, hf, opts.chdf5); alt != nil {
			st.allowed["hdf5 detects the "+driver+" driver, libhdf5 is given it"]++
			cf, cerr = alt, nil
		}
	}
	if cf != nil {
		defer cf.Close()
	}
	if !c.sameErr("/", "open", cerr, herr) {
		if cerr != nil && herr != nil {
			st.filesBothFail++
			st.bothFailFiles = append(st.bothFailFiles, rel)
			st.skip("file: neither package opens it")
			t.Logf("neither package opens the file: libhdf5: %s", oneLine(cerr))
		}
		return st
	}
	st.files++

	c.cf, c.hf = cf, hf
	c.equal("/", "superblock", cf.Superblock(), hf.Superblock())
	c.compareObject("/", cf.Root(), hf.Root(), true)
	c.compareWalk(cf, hf)
	t.Logf("%s", st.summary())
	return st
}

// openWithDriver opens a file that hdf5 opened by detecting a family,
// multi or split driver with the matching chdf5 driver.
func openWithDriver(name string, hf *hdf5.File, opts []chdf5.Option) (*chdf5.File, string) {
	try := func(n, driver string) (*chdf5.File, string) {
		f, err := chdf5.Open(n, append(append([]chdf5.Option(nil), opts...), chdf5.WithDriver(driver))...)
		if err != nil {
			return nil, ""
		}
		return f, driver
	}
	switch {
	case strings.HasSuffix(name, "-s.h5"):
		return try(strings.TrimSuffix(name, "-s.h5"), "multi")
	case strings.HasSuffix(name, "-m.h5"):
		return try(strings.TrimSuffix(name, "-m.h5"), "split")
	}
	if p, ok := familyPattern(name); ok {
		return try(p, "family")
	}
	return nil, ""
}

// familyPattern turns the name of the first member of a family
// ("name00000.h5", "name-000000.h5") into a printf pattern.
func familyPattern(name string) (string, bool) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	i := len(base)
	for i > 0 && base[i-1] >= '0' && base[i-1] <= '9' {
		i--
	}
	digits := base[i:]
	if len(digits) == 0 || strings.Trim(digits, "0") != "" {
		return "", false
	}
	return fmt.Sprintf("%s%%0%dd%s", base[:i], len(digits), ext), true
}

type cVisit struct {
	v   chdf5.Visit
	err error
}

// compareWalk compares the recursive link walks from the root.
func (c *comparer) compareWalk(cf *chdf5.File, hf *hdf5.File) {
	var hv []hdf5.Visit
	var herr error
	for v, err := range hf.Root().Walk() {
		if err != nil {
			herr = err
			break
		}
		hv = append(hv, v)
	}
	var cv []cVisit
	for v, err := range cf.Root().Walk() {
		cv = append(cv, cVisit{v, err})
	}
	defer func() {
		for _, v := range cv {
			if v.v.Object != nil {
				v.v.Object.Close()
			}
		}
	}()

	for i, h := range hv {
		if i >= len(cv) {
			c.mismatch(h.Path, "walk", "hdf5 reports more links than libhdf5 (%d)", len(cv))
			return
		}
		cc := cv[i]
		if cc.v.Path != h.Path {
			c.mismatch(h.Path, "walk", "link %d: libhdf5 %q, hdf5 %q", i, cc.v.Path, h.Path)
			return
		}
		c.equal(h.Path, "link", cc.v.Link, h.Link)
		switch {
		case cc.err != nil && h.Object == nil && h.Link.Type == h5.LinkHard:
			// An object of a class neither package can open.
			c.st.skip("object: unknown object class")
		case cc.err != nil:
			c.mismatch(h.Path, "walk", "libhdf5 cannot open the object (%v) but hdf5 can", cc.err)
		case (cc.v.Object == nil) != (h.Object == nil):
			c.mismatch(h.Path, "walk", "object: libhdf5 %T, hdf5 %T", cc.v.Object, h.Object)
		case h.Object != nil:
			c.compareObject(h.Path, cc.v.Object, h.Object, true)
		default:
			c.compareLinkTarget(cf, hf, h.Path, h.Link)
		}
	}
	if herr != nil {
		// hdf5 ends the walk at the first error (H5Lvisit2); libhdf5 must
		// fail on the same link.
		i := len(hv)
		if i < len(cv) && cv[i].err != nil {
			c.st.skip("walk: object both fail to open (hdf5 walk ends, rest compared by path)")
			c.t.Logf("hdf5 walk ends at %q: libhdf5: %s; hdf5: %s", cv[i].v.Path, oneLine(cv[i].err), oneLine(herr))
			for _, cc := range cv[i+1:] {
				c.compareByPath(cf, hf, cc)
			}
			return
		}
		at := "end"
		if i < len(cv) {
			at = cv[i].v.Path
		}
		c.mismatch(at, "walk", "hdf5 walk fails: %v", herr)
		return
	}
	if len(cv) > len(hv) {
		c.mismatch(cv[len(hv)].v.Path, "walk", "libhdf5 reports %d links, hdf5 %d", len(cv), len(hv))
	}
}

// compareByPath compares a link the libhdf5 walk reports after the point
// where the hdf5 walk ended with an error, through the parent group's Link
// and File.Open.
func (c *comparer) compareByPath(cf *chdf5.File, hf *hdf5.File, cc cVisit) {
	path := cc.v.Path
	if path == "" {
		return // a group whose links libhdf5 cannot iterate
	}
	i := strings.LastIndex(path, "/")
	dir, base := path[:i], path[i+1:]
	if dir == "" {
		dir = "/"
	}
	hg, err := hf.OpenGroup(dir)
	if err != nil {
		c.mismatch(path, "walk", "libhdf5 lists the link but hdf5 cannot open its group: %v", err)
		return
	}
	hl, err := hg.Link(base)
	if err != nil {
		c.mismatch(path, "link", "libhdf5 reports the link but hdf5 fails: %v", err)
		return
	}
	c.equal(path, "link", cc.v.Link, hl)
	if hl.Type != h5.LinkHard {
		c.compareLinkTarget(cf, hf, path, hl)
		return
	}
	ho, herr := hf.Open(path)
	if !c.sameErr(path, "open", cc.err, herr) {
		return
	}
	c.compareObject(path, cc.v.Object, ho, true)
}

// compareLinkTarget follows a soft or external link with both packages.
func (c *comparer) compareLinkTarget(cf *chdf5.File, hf *hdf5.File, path string, l h5.Link) {
	if l.Type != h5.LinkSoft && l.Type != h5.LinkExternal {
		return
	}
	co, cerr := cf.Open(path)
	ho, herr := hf.Open(path)
	if co != nil {
		defer co.Close()
	}
	if !c.sameErr(path, "open "+l.Type.String()+" link", cerr, herr) {
		if cerr != nil && herr != nil {
			c.st.skip("link: dangling " + l.Type.String() + " link (both fail)")
		}
		return
	}
	if l.Type == h5.LinkSoft {
		// The target is compared where its hard link is walked.
		c.compareInfo(path, co, ho)
		return
	}
	// Objects in another file: Path differs by design (API.md), and the
	// target is not walked.
	sub := &comparer{t: c.t, rel: c.rel, st: c.st, done: map[uint64]bool{}}
	sub.compareObject(path, co, ho, false)
}

// compareInfo compares Info, ignoring the times one package does not have.
func (c *comparer) compareInfo(path string, co chdf5.Object, ho hdf5.Object) (h5.ObjectInfo, bool) {
	ci, cerr := co.Info()
	hi, herr := ho.Info()
	if !c.sameErr(path, "info", cerr, herr) {
		return ci, false
	}
	for _, p := range [][2]*int64{{&ci.ATime, &hi.ATime}, {&ci.MTime, &hi.MTime}, {&ci.CTime, &hi.CTime}, {&ci.BTime, &hi.BTime}} {
		if *p[0] == 0 || *p[1] == 0 {
			*p[0], *p[1] = 0, 0
		}
	}
	return ci, c.equal(path, "info", ci, hi)
}

// compareObject compares an object (and, once per address, its contents).
func (c *comparer) compareObject(path string, co chdf5.Object, ho hdf5.Object, comparePath bool) {
	if comparePath {
		if co.Path() != ho.Path() {
			c.mismatch(path, "path", "libhdf5 %q, hdf5 %q", co.Path(), ho.Path())
		}
	}
	info, ok := c.compareInfo(path, co, ho)
	if ok {
		if c.done[info.Address] {
			return
		}
		c.done[info.Address] = true
	}
	c.st.objects++

	cc, cerr := co.Comment()
	hc, herr := ho.Comment()
	if c.sameErr(path, "comment", cerr, herr) {
		c.equal(path, "comment", cc, hc)
	}
	cn, cerr := co.NumAttributes()
	hn, herr := ho.NumAttributes()
	if c.sameErr(path, "number of attributes", cerr, herr) {
		c.equal(path, "number of attributes", cn, hn)
	}
	c.compareAttributes(path, co, ho)

	switch h := ho.(type) {
	case *hdf5.Group:
		cg, ok := co.(*chdf5.Group)
		if !ok {
			c.mismatch(path, "object type", "libhdf5 %T, hdf5 group", co)
			return
		}
		c.compareGroup(path, cg, h)
	case *hdf5.Dataset:
		cd, ok := co.(*chdf5.Dataset)
		if !ok {
			c.mismatch(path, "object type", "libhdf5 %T, hdf5 dataset", co)
			return
		}
		c.compareDataset(path, cd, h)
	case *hdf5.Datatype:
		ct, ok := co.(*chdf5.Datatype)
		if !ok {
			c.mismatch(path, "object type", "libhdf5 %T, hdf5 datatype", co)
			return
		}
		c.equal(path, "committed datatype", ct.Datatype(), h.Datatype())
	}
}

func (c *comparer) compareGroup(path string, cg *chdf5.Group, hg *hdf5.Group) {
	cn, cerr := cg.NumLinks()
	hn, herr := hg.NumLinks()
	if c.sameErr(path, "number of links", cerr, herr) {
		c.equal(path, "number of links", cn, hn)
	}
	collect := func(seq func(func(h5.Link, error) bool)) ([]h5.Link, error) {
		var out []h5.Link
		for l, err := range seq {
			if err != nil {
				return out, err
			}
			out = append(out, l)
		}
		return out, nil
	}
	cl, cerr := collect(cg.LinksByCreationOrder())
	hl, herr := collect(hg.LinksByCreationOrder())
	if c.sameErr(path, "links by creation order", cerr, herr) {
		c.equal(path, "links by creation order", cl, hl)
	}
	cl, cerr = collect(cg.Links())
	hl, herr = collect(hg.Links())
	if c.sameErr(path, "links", cerr, herr) {
		c.equal(path, "links", cl, hl)
	}
}

func (c *comparer) compareAttributes(path string, co chdf5.Object, ho hdf5.Object) {
	var cas []*chdf5.Attribute
	var cerr error
	for a, err := range co.Attributes() {
		if err != nil {
			cerr = err
			break
		}
		cas = append(cas, a)
	}
	defer func() {
		for _, a := range cas {
			a.Close()
		}
	}()
	var has []*hdf5.Attribute
	var herr error
	for a, err := range ho.Attributes() {
		if err != nil {
			herr = err
			break
		}
		has = append(has, a)
	}
	names := func(n int, name func(int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = name(i)
		}
		return out
	}
	cnames := names(len(cas), func(i int) string { return cas[i].Name() })
	hnames := names(len(has), func(i int) string { return has[i].Name() })
	if !c.sameErr(path, "attributes", cerr, herr) && cerr != nil && herr != nil {
		c.st.skip("attribute: iteration fails in both")
	}
	if !c.equal(path, "attribute names", cnames, hnames) {
		return
	}
	for i := range cas {
		c.compareAttribute(path+"@"+cnames[i], cas[i], has[i])
	}
}

func (c *comparer) compareAttribute(path string, ca *chdf5.Attribute, ha *hdf5.Attribute) {
	c.st.attributes++
	c.equal(path, "datatype", ca.Datatype(), ha.Datatype())
	c.equal(path, "dataspace", ca.Dataspace(), ha.Dataspace())
	cv, cerr := ca.ReadValues()
	hv, herr := ha.ReadValues()
	if !c.sameErr(path, "read", cerr, herr) {
		if cerr != nil && herr != nil {
			c.st.skip("attribute: both fail to read")
			c.t.Logf("%s: neither package reads the attribute: libhdf5: %s", path, oneLine(cerr))
		}
		return
	}
	if d := diffValues(cv, hv); d != "" {
		c.mismatch(path, "values", "%s", d)
		return
	}
	c.st.values += len(cv)
	c.compareReadT(path, ca.Datatype(), ca.Dataspace().NumElems(),
		func() ([]float64, error) { return chdf5.ReadAttribute[float64](ca) },
		func() ([]float64, error) { return hdf5.ReadAttribute[float64](ha) },
		func() ([]string, error) { return chdf5.ReadAttribute[string](ca) },
		func() ([]string, error) { return hdf5.ReadAttribute[string](ha) })
}

// compareReadT compares Read[float64] for numeric types and Read[string]
// for strings.
func (c *comparer) compareReadT(path string, dt *h5.Datatype, n uint64,
	cf, hf func() ([]float64, error), cs, hs func() ([]string, error)) {
	if n*dt.Size > maxReadTBytes {
		return
	}
	switch {
	case dt.Class == h5.ClassInteger || dt.Class == h5.ClassFloat:
		cv, cerr := cf()
		hv, herr := hf()
		if c.sameErr(path, "Read[float64]", cerr, herr) {
			if len(cv) != len(hv) {
				c.mismatch(path, "Read[float64]", "length: libhdf5 %d, hdf5 %d", len(cv), len(hv))
				return
			}
			for i := range cv {
				if math.Float64bits(cv[i]) != math.Float64bits(hv[i]) {
					c.mismatch(path, "Read[float64]", "[%d]: libhdf5 %v, hdf5 %v", i, cv[i], hv[i])
					return
				}
			}
		}
	case dt.Class == h5.ClassString || dt.IsVlenString():
		cv, cerr := cs()
		hv, herr := hs()
		if c.sameErr(path, "Read[string]", cerr, herr) {
			c.equal(path, "Read[string]", cv, hv)
		}
	}
}

func (c *comparer) compareDataset(path string, cd *chdf5.Dataset, hd *hdf5.Dataset) {
	c.st.datasets++
	dt := cd.Datatype()
	c.equal(path, "datatype", dt, hd.Datatype())
	space := cd.Dataspace()
	c.equal(path, "dataspace", space, hd.Dataspace())

	cl, cerr := cd.Layout()
	hl, herr := hd.Layout()
	if c.sameErr(path, "layout", cerr, herr) {
		// Not available through the libhdf5 API (API.md).
		hl.Version, hl.ChunkIndex = 0, 0
		c.equal(path, "layout", cl, hl)
	}
	cfl, cerr := cd.Filters()
	hfl, herr := hd.Filters()
	if c.sameErr(path, "filters", cerr, herr) {
		c.equal(path, "filters", cfl, hfl)
	}
	cfv, cerr := cd.FillValue()
	hfv, herr := hd.FillValue()
	if c.sameErr(path, "fill value", cerr, herr) {
		hfv.Version = 0
		c.equal(path, "fill value", cfv, hfv)
	}

	if dt == nil {
		return
	}
	if space.NumElems()*dt.Size > maxReadBytes {
		c.st.skip(fmt.Sprintf("dataset values: larger than %d MiB (metadata and hyperslab compared)", maxReadBytes>>20))
	} else {
		cv, cerr := cd.ReadValues()
		hv, herr := hd.ReadValues()
		if c.sameErr(path, "read", cerr, herr) {
			if d := diffValues(cv, hv); d != "" {
				c.mismatch(path, "values", "%s", d)
			} else {
				c.st.datasetsRead++
				c.st.values += len(cv)
				c.compareRefs(path, cv, hv)
			}
		} else if cerr != nil && herr != nil {
			reason := readFailure(cd, cfl, cl)
			c.st.skip("dataset values: " + reason)
			c.t.Logf("%s: neither package reads the dataset (%s): libhdf5: %s", path, reason, oneLine(cerr))
		}
		c.compareReadT(path, dt, space.NumElems(),
			func() ([]float64, error) { return chdf5.Read[float64](cd) },
			func() ([]float64, error) { return hdf5.Read[float64](hd) },
			func() ([]string, error) { return chdf5.Read[string](cd) },
			func() ([]string, error) { return hdf5.Read[string](hd) })
	}
	c.compareHyperslabs(path, cd, hd, space)
}

// compareHyperslabs reads a fixed strided pattern (start 1, stride 2, count
// min(3, dim/2) in every dimension) and an out-of-bounds selection.
func (c *comparer) compareHyperslabs(path string, cd *chdf5.Dataset, hd *hdf5.Dataset, space h5.Dataspace) {
	rank := space.Rank()
	if space.Class != h5.SpaceSimple || rank < 1 || rank > 3 {
		return
	}
	sel := h5.Hyperslab{Start: make([]uint64, rank), Stride: make([]uint64, rank), Count: make([]uint64, rank), Block: make([]uint64, rank)}
	ok := true
	for i, d := range space.Dims {
		if d < 2 {
			ok = false
		}
		sel.Start[i], sel.Stride[i], sel.Count[i], sel.Block[i] = 1, 2, min(3, d/2), 1
	}
	if ok {
		cv, cerr := cd.ReadHyperslab(sel)
		hv, herr := hd.ReadHyperslab(sel)
		if c.sameErr(path, "hyperslab", cerr, herr) {
			if d := diffValues(cv, hv); d != "" {
				c.mismatch(path, "hyperslab values", "%s", d)
			} else {
				c.st.hyperslabs++
				c.st.values += len(cv)
			}
		}
	}
	out := h5.Hyperslab{Start: append([]uint64(nil), space.Dims...), Count: make([]uint64, rank)}
	for i := range out.Count {
		out.Count[i] = 1
	}
	_, cerr := cd.ReadHyperslab(out)
	_, herr := hd.ReadHyperslab(out)
	c.sameErr(path, "out-of-bounds hyperslab", cerr, herr)
}

// compareRefs opens the first few objects referenced by an object
// reference dataset by address with both packages (Path differs by design:
// API.md).
func (c *comparer) compareRefs(path string, cv, hv []any) {
	n := 0
	for i, v := range cv {
		r, ok := v.(h5.ObjectRef)
		if !ok || r.Undefined || r.Address == 0 {
			continue
		}
		if n++; n > 4 {
			return
		}
		c.compareOpenAddress(fmt.Sprintf("%s[%d]", path, i), r.Address)
	}
}

func (c *comparer) compareOpenAddress(path string, addr uint64) {
	if c.cf == nil {
		return
	}
	co, cerr := c.cf.OpenAddress(addr)
	ho, herr := c.hf.OpenAddress(addr)
	if co != nil {
		defer co.Close()
	}
	if c.sameErr(path, "OpenAddress", cerr, herr) {
		c.compareInfo(path, co, ho)
	}
}

// readFailure classifies why libhdf5 refuses a dataset's data.
func readFailure(cd *chdf5.Dataset, filters []h5.Filter, l h5.Layout) string {
	for _, f := range filters {
		if !chdf5.FilterAvailable(f.ID) {
			name, _, _ := strings.Cut(f.Name, ";")
			if i := strings.Index(name, " ("); i > 0 {
				name = name[:i]
			}
			if f.ID == h5.FilterSzip {
				name = "szip"
			}
			return fmt.Sprintf("filter %d (%s) not available", f.ID, name)
		}
	}
	for _, x := range l.External {
		if _, err := os.Stat(x.Name); err != nil {
			return "external data file not found"
		}
	}
	if l.Class == h5.LayoutVirtual {
		return "virtual dataset sources"
	}
	return "libhdf5 read error"
}

// filesOf lists the files to compare: the repository corpus, the pure Go
// package's test files and the committed fixtures (golden.Files).
func filesOf(t *testing.T) (root string, files []string) {
	root = repoRoot(t)
	return root, golden.Files(root)
}
