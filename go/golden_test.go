package hdf5

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crhntr/hdf5/go/h5"
)

// The golden file holds fingerprints (h5.Fingerprint) of what libhdf5
// 1.14.6, through the cgo package, reads from every file of the corpus
// the conformance suite compares, walked the way the suite walks them. It
// is written by `cd ../cgo && make golden` (cgo/internal/golden documents
// the format) and checked against live libhdf5 output by the conformance
// test TestGoldenUpToDate. TestGolden reads the same files with this
// package, records them the same way and compares: the libhdf5 oracle
// without cgo, for coverage and mutation testing of the pure Go module.
const goldenFile = "testdata/golden/corpus.txt.gz"

// Limits of the conformance suite (cgo/internal/golden).
const (
	goldenMaxReadBytes  = 64 << 20
	goldenMaxReadTBytes = 1 << 20
	goldenMaxRefs       = 4
)

type goldenBlock struct {
	rel, mode string
	lines     []string // starting with the F line
}

func loadGolden(t testing.TB) (header []string, blocks []goldenBlock) {
	t.Helper()
	f, err := os.Open(goldenFile)
	require.NoError(t, err)
	defer f.Close()
	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if header == nil {
			header = strings.Split(line, "\t")
			require.Equal(t, "hdf5-golden", header[0], "golden file header")
			require.Equal(t, "1", header[1], "golden file format version")
			continue
		}
		if strings.HasPrefix(line, "F\t") {
			fields := strings.Split(line, "\t")
			require.Len(t, fields, 4, line)
			rel, err := strconv.Unquote(fields[1])
			require.NoError(t, err, line)
			blocks = append(blocks, goldenBlock{rel: rel, mode: fields[2]})
		}
		require.NotEmpty(t, blocks, "line before the first file: %s", line)
		b := &blocks[len(blocks)-1]
		b.lines = append(b.lines, line)
	}
	require.NoError(t, sc.Err())
	require.NoError(t, zr.Close())
	return header, blocks
}

// goldenFiles lists the files the golden file must cover (as
// cgo/internal/golden.Files), relative to the repository root.
func goldenFiles(t testing.TB, root string) []string {
	files := corpusFiles(t)
	for _, pattern := range []string{"go/testdata/*.h5", "cgo/testdata/*.h5", "go/testdata/fixtures/*/*.h5"} {
		extra, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		files = append(files, extra...)
	}
	rels := make([]string, len(files))
	for i, f := range files {
		rel, err := filepath.Rel(root, f)
		require.NoError(t, err)
		rels[i] = filepath.ToSlash(rel)
	}
	slices.Sort(rels)
	return rels
}

// TestGolden compares what this package reads from every corpus file with
// what libhdf5 read (the golden file): open or not, superblock, the link
// walk, every object's info, comment, attributes and links, the datasets'
// metadata, values, a strided hyperslab, Read[T], the targets of soft and
// external links and of object references. The differences the conformance
// suite allows are allowed (see goldenRecorder). The first differing entry
// of each file is reported.
func TestGolden(t *testing.T) {
	root := repoRoot(t)
	header, blocks := loadGolden(t)
	t.Logf("golden file: %s, %d blocks", strings.Join(header[2:], " "), len(blocks))

	// The golden file covers exactly the corpus of the checkout.
	var recorded []string
	for _, b := range blocks {
		if b.mode == "corpus" {
			recorded = append(recorded, b.rel)
		}
	}
	slices.Sort(recorded)
	present := goldenFiles(t, root)
	for _, rel := range recorded {
		if _, ok := slices.BinarySearch(present, rel); !ok {
			t.Errorf("%s: in the golden file but missing from the checkout", rel)
		}
	}
	for _, rel := range present {
		if _, ok := slices.BinarySearch(recorded, rel); !ok {
			t.Errorf("%s: not in the golden file; regenerate it (cd cgo && make golden)", rel)
		}
	}
	require.Equal(t, strconv.Itoa(len(recorded)), header[4], "file count of the header")

	t.Run("corpus", func(t *testing.T) {
		for _, b := range blocks {
			if b.mode != "corpus" {
				continue
			}
			t.Run(b.rel, func(t *testing.T) {
				t.Parallel()
				compareGolden(t, filepath.Join(root, filepath.FromSlash(b.rel)), b)
			})
		}
	})
	// Files with external data files, read from their directory (the
	// relative external file names are looked up in the current
	// directory).
	for _, b := range blocks {
		if b.mode != "efl" {
			continue
		}
		t.Run("efl/"+b.rel, func(t *testing.T) {
			name := filepath.Join(root, filepath.FromSlash(b.rel))
			t.Chdir(filepath.Dir(name))
			compareGolden(t, name, b)
		})
	}
}

// compareGolden records a file with this package and compares the record
// with the golden block.
func compareGolden(t *testing.T, name string, b goldenBlock) {
	r := &goldenRecorder{done: map[uint64]bool{}, golden: b.lines}
	func() {
		defer func() {
			if p := recover(); p != nil {
				r.line("panic", fmt.Sprint(p))
			}
		}()
		r.file(name, b.rel, b.mode)
	}()
	want, got := b.lines, r.lines
	for i := range max(len(want), len(got)) {
		switch {
		case i >= len(got):
			t.Errorf("entry %d missing (hdf5 recorded %d entries, libhdf5 %d)\nlibhdf5: %s", i, len(got), len(want), want[i])
			return
		case i >= len(want):
			t.Errorf("extra entry %d (hdf5 recorded %d entries, libhdf5 %d)\nhdf5:    %s", i, len(got), len(want), got[i])
			return
		case !goldenMatch(want[i], got[i]):
			kind, path, _ := strings.Cut(got[i], "\t")
			path, _, _ = strings.Cut(path, "\t")
			t.Errorf("entry %d (%s %s) differs in %s\nlibhdf5: %s\nhdf5:    %s",
				i, kind, path, goldenDiffFields(want[i], got[i]), want[i], got[i])
			return
		}
	}
}

// goldenFields names the fields of the golden lines (after the kind).
var goldenFields = map[string][]string{
	"F":  {"file", "mode", "open"},
	"S":  {"superblock"},
	"O":  {"path", "Path()", "info", "object type", "comment", "number of attributes"},
	"AL": {"path", "attribute names", "attribute iteration"},
	"A":  {"path", "datatype", "dataspace", "values", "Read[T]"},
	"G":  {"path", "number of links", "links by creation order", "links"},
	"N":  {"path", "committed datatype"},
	"D":  {"path", "datatype", "dataspace", "layout", "filters", "fill value"},
	"V":  {"path", "values", "Read[T]", "hyperslab", "out-of-bounds hyperslab"},
	"R":  {"path", "OpenAddress", "info"},
	"W":  {"path", "link", "object"},
	"T":  {"path", "link type", "open", "info"},
}

// goldenDiffFields names the fields in which a recorded line differs from
// the golden one.
func goldenDiffFields(want, got string) string {
	w, g := strings.Split(want, "\t"), strings.Split(got, "\t")
	names := goldenFields[w[0]]
	var diff []string
	for i := 1; i < max(len(w), len(g)); i++ {
		name := fmt.Sprintf("field %d", i)
		if i-1 < len(names) {
			name = names[i-1]
		}
		switch {
		case i < len(w) && strings.HasPrefix(w[i], "#"):
			continue
		case i >= len(w) || i >= len(g):
			diff = append(diff, name+" (missing)")
		case !goldenMatch(w[i], g[i]):
			diff = append(diff, name)
		}
	}
	if len(diff) == 0 {
		return "kind"
	}
	return strings.Join(diff, ", ")
}

// goldenMatch compares a golden line with a recorded one. In the recorded
// line, "*" matches any token and "a|b" either a or b (allowed
// differences); "#" tokens of the golden line are comments.
func goldenMatch(want, got string) bool {
	if want == got {
		return true
	}
	w := strings.Split(want, "\t")
	for i, tok := range w {
		if strings.HasPrefix(tok, "#") {
			w = w[:i]
			break
		}
	}
	g := strings.Split(got, "\t")
	if len(w) != len(g) {
		return false
	}
	for i := range w {
		if w[i] == g[i] || g[i] == "*" {
			continue
		}
		if !strings.HasPrefix(g[i], `"`) && slices.Contains(strings.Split(g[i], "|"), w[i]) {
			continue
		}
		return false
	}
	return true
}

// goldenRecorder records a file like cgo/internal/golden does with
// chdf5. Where the conformance suite allows a difference
// (cgo/conformance/allow_test.go) it records the alternatives:
//
//   - a link of an unregistered user-defined class may have no UserData
//     in libhdf5 (aspects "link" and "links");
//   - an unnamed szip filter may be named "Unknown library filter" by a
//     libhdf5 built without szip;
//   - reading szip-compressed data fails with "szip filter not supported"
//     where a libhdf5 built with szip reads it.
//
// The walk differs as in the suite: this package's Walk ends at the first
// error, where libhdf5 must fail too; the links libhdf5 walks after that
// are recorded through the parent group's Link and File.Open (with the
// paths taken from the golden block).
type goldenRecorder struct {
	lines  []string
	done   map[uint64]bool
	f      *File
	golden []string
}

func (r *goldenRecorder) line(fields ...string) {
	r.lines = append(r.lines, strings.Join(fields, "\t"))
}

func gq(s string) string { return strconv.Quote(s) }

func gfp(v any) string { return h5.Fingerprint(v) }

// gerr is the token of an error: "E", or "*" for the allowed szip
// difference.
func gerr(err error) string {
	if strings.Contains(err.Error(), "szip filter not supported") {
		return "*"
	}
	return "E"
}

func gtok(v any, err error) string {
	if err != nil {
		return gerr(err)
	}
	return gfp(v)
}

func gok(err error) string {
	if err != nil {
		return gerr(err)
	}
	return "O"
}

// linkTok fingerprints a link, with the alternative of no UserData for a
// user-defined link.
func linkTok(l h5.Link) string {
	s := gfp(l)
	if userDefined(l) {
		l.UserData = nil
		s += "|" + gfp(l)
	}
	return s
}

func userDefined(l h5.Link) bool {
	return l.Type != h5.LinkHard && l.Type != h5.LinkSoft && l.Type != h5.LinkExternal && l.UserData != nil
}

func linksTok(links []h5.Link, err error, allowUserData bool) string {
	if err != nil {
		return gerr(err)
	}
	s := gfp(links)
	if allowUserData && slices.ContainsFunc(links, userDefined) {
		alt := slices.Clone(links)
		for i := range alt {
			if userDefined(alt[i]) {
				alt[i].UserData = nil
			}
		}
		s += "|" + gfp(alt)
	}
	return s
}

func filtersTok(filters []h5.Filter, err error) string {
	if err != nil {
		return gerr(err)
	}
	s := gfp(filters)
	unnamedSzip := func(f h5.Filter) bool { return f.ID == h5.FilterSzip && f.Name == "szip" }
	if slices.ContainsFunc(filters, unnamedSzip) {
		alt := slices.Clone(filters)
		for i := range alt {
			if unnamedSzip(alt[i]) {
				alt[i].Name = "Unknown library filter"
			}
		}
		s += "|" + gfp(alt)
	}
	return s
}

func (r *goldenRecorder) file(name, rel, mode string) {
	f, err := Open(name)
	if err == nil {
		// chdf5.Open opens the root group; Open loads it lazily.
		if _, err = f.Root().Info(); err != nil {
			f.Close()
		}
	}
	r.line("F", gq(rel), mode, gok(err))
	if err != nil {
		return
	}
	defer f.Close()
	r.f = f
	r.line("S", gfp(f.Superblock()))
	r.object("/", f.Root(), true)

	var visits []Visit
	var werr error
	for v, err := range f.Root().Walk() {
		if err != nil {
			werr = err
			break
		}
		visits = append(visits, v)
	}
	for _, v := range visits {
		status := "nil"
		switch {
		case v.Object != nil:
			status = "obj"
		case v.Link.Type == h5.LinkHard:
			status = "E" // an object of a class neither package opens
		}
		r.line("W", gq(v.Path), linkTok(v.Link), status)
		switch {
		case v.Object != nil:
			r.object(v.Path, v.Object, true)
		case status == "nil":
			r.linkTarget(v.Path, v.Link)
		}
	}
	if werr == nil {
		return
	}
	// The walk ended with an error: libhdf5 must fail on the same link;
	// the links it walks after that are recorded by path.
	var paths []string
	for _, l := range r.golden {
		if rest, ok := strings.CutPrefix(l, "W\t"); ok {
			p, _, _ := strings.Cut(rest, "\t")
			p, err := strconv.Unquote(p)
			if err != nil {
				p = "?"
			}
			paths = append(paths, p)
		}
	}
	i := len(visits)
	if i >= len(paths) {
		r.line("W", gq("<end>"), "hdf5 walk fails: "+werr.Error())
		return
	}
	r.line("W", gq(paths[i]), "*", "E")
	for _, p := range paths[i+1:] {
		r.byPath(p)
	}
}

// byPath records a link libhdf5 walks after the point where this
// package's walk ended with an error.
func (r *goldenRecorder) byPath(path string) {
	if path == "" {
		r.line("W", gq(""), "*", "*") // a group whose links libhdf5 cannot iterate
		return
	}
	i := strings.LastIndex(path, "/")
	dir, base := path[:i], path[i+1:]
	if dir == "" {
		dir = "/"
	}
	g, err := r.f.OpenGroup(dir)
	if err != nil {
		r.line("W", gq(path), "hdf5 cannot open the group: "+err.Error())
		return
	}
	l, err := g.Link(base)
	if err != nil {
		r.line("W", gq(path), "hdf5 cannot get the link: "+err.Error())
		return
	}
	if l.Type != h5.LinkHard {
		r.line("W", gq(path), linkTok(l), "nil")
		r.linkTarget(path, l)
		return
	}
	o, err := r.f.Open(path)
	if err != nil {
		r.line("W", gq(path), linkTok(l), "E")
		return
	}
	r.line("W", gq(path), linkTok(l), "obj")
	r.object(path, o, true)
}

func goldenInfo(o Object) (string, h5.ObjectInfo, bool) {
	info, err := o.Info()
	if err != nil {
		return gerr(err), info, false
	}
	info.ATime, info.MTime, info.CTime, info.BTime = 0, 0, 0, 0
	return gfp(info), info, true
}

func goldenKind(o Object) string {
	switch o.(type) {
	case *Group:
		return "group"
	case *Dataset:
		return "dataset"
	case *Datatype:
		return "datatype"
	}
	return "other"
}

func (r *goldenRecorder) object(path string, o Object, withPath bool) {
	objPath := "-"
	if withPath {
		objPath = gq(o.Path())
	}
	it, info, ok := goldenInfo(o)
	if ok && r.done[info.Address] {
		r.line("O", gq(path), objPath, it, goldenKind(o), "dup")
		return
	}
	if ok {
		r.done[info.Address] = true
	}
	comment, err := o.Comment()
	ct := gtok(comment, err)
	n, err := o.NumAttributes()
	nt := "E"
	if err == nil {
		nt = strconv.Itoa(n)
	}
	r.line("O", gq(path), objPath, it, goldenKind(o), ct, nt)
	r.attributes(path, o)
	switch o := o.(type) {
	case *Group:
		r.group(path, o)
	case *Dataset:
		r.dataset(path, o)
	case *Datatype:
		r.line("N", gq(path), gfp(o.Datatype()))
	}
}

func (r *goldenRecorder) attributes(path string, o Object) {
	var as []*Attribute
	var iterErr error
	for a, err := range o.Attributes() {
		if err != nil {
			iterErr = err
			break
		}
		as = append(as, a)
	}
	names := make([]string, len(as))
	for i, a := range as {
		names[i] = a.Name()
	}
	r.line("AL", gq(path), gfp(names), gok(iterErr))
	for _, a := range as {
		r.attribute(path+"@"+a.Name(), a)
	}
}

func (r *goldenRecorder) attribute(path string, a *Attribute) {
	dt, space := a.Datatype(), a.Dataspace()
	vals, err := a.ReadValues()
	vt, rt := gtok(vals, err), "-"
	if err == nil {
		rt = goldenReadT(dt, space.NumElems(),
			func() ([]float64, error) { return ReadAttribute[float64](a) },
			func() ([]string, error) { return ReadAttribute[string](a) })
	}
	r.line("A", gq(path), gfp(dt), gfp(space), vt, rt)
}

func goldenReadT(dt *h5.Datatype, n uint64, f func() ([]float64, error), s func() ([]string, error)) string {
	if n*dt.Size > goldenMaxReadTBytes {
		return "-"
	}
	switch {
	case dt.Class == h5.ClassInteger || dt.Class == h5.ClassFloat:
		v, err := f()
		if len(v) == 0 {
			v = []float64{}
		}
		return gtok(v, err)
	case dt.Class == h5.ClassString || dt.IsVlenString():
		v, err := s()
		return gtok(v, err)
	}
	return "-"
}

func goldenCollect(seq func(func(h5.Link, error) bool)) ([]h5.Link, error) {
	var out []h5.Link
	for l, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, l)
	}
	return out, nil
}

func (r *goldenRecorder) group(path string, g *Group) {
	n, err := g.NumLinks()
	nt := "E"
	if err == nil {
		nt = strconv.Itoa(n)
	}
	crt, cerr := goldenCollect(g.LinksByCreationOrder())
	links, lerr := goldenCollect(g.Links())
	r.line("G", gq(path), nt, linksTok(crt, cerr, false), linksTok(links, lerr, true))
}

func (r *goldenRecorder) dataset(path string, d *Dataset) {
	dt, space := d.Datatype(), d.Dataspace()
	layout, lerr := d.Layout()
	// Not available through the libhdf5 API (API.md).
	layout.Version, layout.ChunkIndex = 0, 0
	filters, ferr := d.Filters()
	fill, fverr := d.FillValue()
	fill.Version = 0
	r.line("D", gq(path), gfp(dt), gfp(space), gtok(layout, lerr), filtersTok(filters, ferr), gtok(fill, fverr))
	if dt == nil {
		return
	}
	vt, rt := "big", "-"
	var vals []any
	if space.NumElems()*dt.Size <= goldenMaxReadBytes {
		var err error
		vals, err = d.ReadValues()
		vt = gtok(vals, err)
		// Read[T] is ReadValues + h5.ConvertValues (read.go): convert
		// the values read rather than read the dataset again (datasets
		// of many small chunks are slow to read), unless the read failed.
		rt = goldenReadT(dt, space.NumElems(),
			func() ([]float64, error) {
				if err != nil {
					return Read[float64](d)
				}
				return h5.ConvertValues[float64](vals)
			},
			func() ([]string, error) {
				if err != nil {
					return Read[string](d)
				}
				return h5.ConvertValues[string](vals)
			})
	}
	ht, ot := "-", "-"
	if space.Class == h5.SpaceSimple && space.Rank() >= 1 && space.Rank() <= 3 {
		rank := space.Rank()
		sel := h5.Hyperslab{Start: make([]uint64, rank), Stride: make([]uint64, rank), Count: make([]uint64, rank), Block: make([]uint64, rank)}
		ok := true
		for i, n := range space.Dims {
			if n < 2 {
				ok = false
			}
			sel.Start[i], sel.Stride[i], sel.Count[i], sel.Block[i] = 1, 2, min(3, n/2), 1
		}
		if ok {
			ht = gtok(d.ReadHyperslab(sel))
		}
		out := h5.Hyperslab{Start: slices.Clone(space.Dims), Count: make([]uint64, rank)}
		for i := range out.Count {
			out.Count[i] = 1
		}
		_, err := d.ReadHyperslab(out)
		ot = gok(err)
	}
	r.line("V", gq(path), vt, rt, ht, ot)
	r.refs(path, vals)
}

func (r *goldenRecorder) refs(path string, vals []any) {
	if r.f == nil {
		return
	}
	n := 0
	for i, v := range vals {
		ref, ok := v.(h5.ObjectRef)
		if !ok || ref.Undefined || ref.Address == 0 {
			continue
		}
		if n++; n > goldenMaxRefs {
			return
		}
		p := gq(fmt.Sprintf("%s[%d]", path, i))
		o, err := r.f.OpenAddress(ref.Address)
		if err != nil {
			r.line("R", p, gerr(err), "-")
			continue
		}
		it, _, _ := goldenInfo(o)
		r.line("R", p, "O", it)
	}
}

func (r *goldenRecorder) linkTarget(path string, l h5.Link) {
	if l.Type != h5.LinkSoft && l.Type != h5.LinkExternal {
		return
	}
	o, err := r.f.Open(path)
	if err != nil {
		r.line("T", gq(path), l.Type.String(), gerr(err))
		return
	}
	if l.Type == h5.LinkSoft {
		it, _, _ := goldenInfo(o)
		r.line("T", gq(path), l.Type.String(), "O", it)
		return
	}
	r.line("T", gq(path), l.Type.String(), "O")
	// Objects in another file: Path differs by design (API.md), and the
	// target is not walked.
	sub := &goldenRecorder{done: map[uint64]bool{}}
	sub.object(path, o, false)
	r.lines = append(r.lines, sub.lines...)
}

func TestGoldenMatch(t *testing.T) {
	assert.True(t, goldenMatch("V\t\"/d\"\tE\t-\t#filter 4 (szip) not available", "V\t\"/d\"\tE\t-"))
	assert.True(t, goldenMatch("V\t\"/d\"\tab\tcd", "V\t\"/d\"\t*\tcd"))
	assert.True(t, goldenMatch("G\t\"/\"\t1\tE\tab", "G\t\"/\"\t1\tE\tcd|ab"))
	assert.False(t, goldenMatch("G\t\"/\"\t1\tE\tab", "G\t\"/\"\t1\tE\tcd|ef"))
	assert.False(t, goldenMatch("W\t\"a|b\"\tx\tobj", "W\t\"a\"\tx\tobj"))
	assert.False(t, goldenMatch("O\t\"/\"\tx", "O\t\"/\"\tx\tdup"))
	assert.Equal(t, "values, hyperslab", goldenDiffFields("V\t\"/d\"\ta\tb\tc\tO", "V\t\"/d\"\tx\tb\ty\tO"))
	assert.Equal(t, "comment (missing), number of attributes (missing)",
		goldenDiffFields("O\t\"/\"\t\"/\"\ti\tgroup\tc\t0", "O\t\"/\"\t\"/\"\ti\tgroup"))
}
