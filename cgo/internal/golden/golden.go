//go:build cgo

// Package golden records what libhdf5 (through chdf5) reads from every file
// of the conformance corpus as fingerprints (h5.Fingerprint), so that the
// pure Go module's tests can compare the pure Go reader with libhdf5
// without cgo (go/golden_test.go). cmd/hdf5-golden writes the record to
// go/testdata/golden/corpus.txt.gz; conformance.TestGoldenUpToDate checks
// that the committed file matches the live libhdf5 output.
//
// The record walks every file the way the conformance suite
// (cgo/conformance/compare_test.go) compares it, and holds what the suite
// compares, with the same exclusions: object times, Layout.Version,
// Layout.ChunkIndex and FillValueInfo.Version are left out, Path() is
// recorded only where the suite compares it. The allowed differences of
// the suite's allow-list are applied by the reader of the record.
//
// # Format
//
// A text file of tab-separated lines. Strings (paths, names) are quoted
// with strconv.Quote, fingerprints are 16 hex digits, "E" stands for an
// error, "-" for "not compared". A token starting with "#" is a comment
// for humans (e.g. why libhdf5 cannot read a dataset); it ends the line
// and is not compared.
//
//	hdf5-golden 1 <libhdf5 version> files <n> blocks <m>
//
// then one block per file and mode, sorted by mode then file name:
//
//	F <file> <mode> O|E          file (relative to the repository root); mode
//	                             "corpus" (read from the cgo/conformance
//	                             directory) or "efl" (read from the file's
//	                             directory, for files with external data
//	                             files); O when the file opens
//	S <fp>                       Superblock()
//	O <path> <Path()|-> <info|E> <kind> dup
//	O <path> <Path()|-> <info|E> <kind> <comment|E> <nattrs|E>
//	                             an object (kind group, dataset, datatype);
//	                             "dup" when the object (by address) was
//	                             already recorded; info is ObjectInfo
//	                             without times
//	AL <path> <names> <O|E>      attribute names (in iteration order), and
//	                             whether the iteration ended with an error
//	A <path@name> <datatype> <dataspace> <values|E> <Read[T]|E|->
//	G <path> <NumLinks|E> <links by creation order|E> <links|E>
//	N <path> <datatype>          committed datatype
//	D <path> <datatype> <dataspace> <layout|E> <filters|E> <fill value|E>
//	V <path> <values|E|big> <Read[T]|E|-> <hyperslab|E|-> <out of bounds O|E|->
//	R <path[i]> <O|E> <info|E|-> OpenAddress of an object reference
//	W <path> <link> obj|nil|E    a Walk visit: an object, no object (soft,
//	                             external, user-defined link), an error
//	T <path> soft|external O|E [<info|E>]
//	                             soft/external link target; an external
//	                             link target's object lines follow
//
// Read[T] is Read[float64] for integer and float data, Read[string] for
// strings, "-" otherwise and for data over 1 MiB. Values are read for
// datasets up to 64 MiB ("big" otherwise); the hyperslab is the
// conformance suite's strided selection (start 1, stride 2, count
// min(3, dim/2) in every dimension, for simple dataspaces of rank 1 to 3
// whose dimensions are all at least 2), the out-of-bounds selection
// starts at the dimensions.
package golden

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	chdf5 "github.com/crhntr/hdf5/cgo"
	"github.com/crhntr/hdf5/go/h5"
)

// Limits shared with the conformance suite.
const (
	MaxReadBytes  = 64 << 20 // datasets whose values are read
	MaxReadTBytes = 1 << 20  // data also read with Read[T]
	MaxRefs       = 4        // object references followed per dataset
)

// Modes of a file block.
const (
	ModeCorpus = "corpus"
	ModeEFL    = "efl"
)

// Generate writes the record of the files (Files(root)) to w. The files
// with external data files are recorded twice: as read from the current
// directory (ModeCorpus) and as read from their own directory (ModeEFL,
// for which Generate changes the working directory of the process and
// restores it).
func Generate(w io.Writer, root string) error {
	files := Files(root)
	var blocks, efl []*recorder
	for _, f := range files {
		r, err := record(root, f, ModeCorpus)
		if err != nil {
			return err
		}
		blocks = append(blocks, r)
		if r.external {
			efl = append(efl, r)
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, c := range efl {
		if err := os.Chdir(filepath.Dir(c.name)); err != nil {
			return err
		}
		r, err := record(root, c.name, ModeEFL)
		if err := os.Chdir(wd); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		blocks = append(blocks, r)
	}
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "hdf5-golden\t1\t%s\tfiles\t%d\tblocks\t%d\n", LibraryVersion(), len(files), len(blocks))
	for _, r := range blocks {
		for _, l := range r.lines {
			bw.WriteString(l)
			bw.WriteByte('\n')
		}
	}
	return bw.Flush()
}

// record records one file.
func record(root, name, mode string) (*recorder, error) {
	rel, err := filepath.Rel(root, name)
	if err != nil {
		return nil, err
	}
	r := &recorder{name: name, done: map[uint64]bool{}}
	r.file(name, filepath.ToSlash(rel), mode)
	return r, nil
}

// recorder records one file (or, for the target of an external link, the
// objects of another file: f is nil then).
type recorder struct {
	name     string
	lines    []string
	done     map[uint64]bool // objects whose contents were recorded, by address
	f        *chdf5.File
	external bool // a dataset stores its data in external files
}

func (r *recorder) line(fields ...string) {
	r.lines = append(r.lines, strings.Join(fields, "\t"))
}

func q(s string) string { return strconv.Quote(s) }

func fp(v any) string { return h5.Fingerprint(v) }

// tok returns fp(v), or "E" when err is not nil.
func tok(v any, err error) string {
	if err != nil {
		return "E"
	}
	return fp(v)
}

func okTok(err error) string {
	if err != nil {
		return "E"
	}
	return "O"
}

// open opens a file with chdf5 like the conformance suite: as is, else
// with the family, multi or split driver its name suggests.
func open(name string) (*chdf5.File, error) {
	f, err := chdf5.Open(name)
	if err == nil {
		return f, nil
	}
	if alt := openWithDriver(name); alt != nil {
		return alt, nil
	}
	return nil, err
}

func (r *recorder) file(name, rel, mode string) {
	f, err := open(name)
	r.line("F", q(rel), mode, okTok(err))
	if err != nil {
		return
	}
	defer f.Close()
	r.f = f
	r.line("S", fp(f.Superblock()))
	r.object("/", f.Root(), true)
	for v, err := range f.Root().Walk() {
		status := "nil"
		switch {
		case err != nil:
			status = "E"
		case v.Object != nil:
			status = "obj"
		}
		r.line("W", q(v.Path), fp(v.Link), status)
		switch {
		case err != nil:
		case v.Object != nil:
			r.object(v.Path, v.Object, true)
			v.Object.Close()
		default:
			r.linkTarget(v.Path, v.Link)
		}
	}
}

// infoTok returns the fingerprint of the object's info without the times
// (which the conformance suite compares only when both packages have them,
// and the pure Go reader always has).
func infoTok(o chdf5.Object) (string, h5.ObjectInfo, bool) {
	info, err := o.Info()
	if err != nil {
		return "E", info, false
	}
	info.ATime, info.MTime, info.CTime, info.BTime = 0, 0, 0, 0
	return fp(info), info, true
}

func kindOf(o chdf5.Object) string {
	switch o.(type) {
	case *chdf5.Group:
		return "group"
	case *chdf5.Dataset:
		return "dataset"
	case *chdf5.Datatype:
		return "datatype"
	}
	return "other"
}

func (r *recorder) object(path string, o chdf5.Object, withPath bool) {
	objPath := "-"
	if withPath {
		objPath = q(o.Path())
	}
	it, info, ok := infoTok(o)
	if ok && r.done[info.Address] {
		r.line("O", q(path), objPath, it, kindOf(o), "dup")
		return
	}
	if ok {
		r.done[info.Address] = true
	}
	comment, err := o.Comment()
	ct := tok(comment, err)
	n, err := o.NumAttributes()
	nt := "E"
	if err == nil {
		nt = strconv.Itoa(n)
	}
	r.line("O", q(path), objPath, it, kindOf(o), ct, nt)
	r.attributes(path, o)
	switch o := o.(type) {
	case *chdf5.Group:
		r.group(path, o)
	case *chdf5.Dataset:
		r.dataset(path, o)
	case *chdf5.Datatype:
		r.line("N", q(path), fp(o.Datatype()))
	}
}

func (r *recorder) attributes(path string, o chdf5.Object) {
	var as []*chdf5.Attribute
	var iterErr error
	for a, err := range o.Attributes() {
		if err != nil {
			iterErr = err
			break
		}
		as = append(as, a)
	}
	defer func() {
		for _, a := range as {
			a.Close()
		}
	}()
	names := make([]string, len(as))
	for i, a := range as {
		names[i] = a.Name()
	}
	r.line("AL", q(path), fp(names), okTok(iterErr))
	for _, a := range as {
		r.attribute(path+"@"+a.Name(), a)
	}
}

func (r *recorder) attribute(path string, a *chdf5.Attribute) {
	dt, space := a.Datatype(), a.Dataspace()
	vals, err := a.ReadValues()
	vt, rt := "E", "-"
	if err == nil {
		vt = fp(vals)
		rt = readT(dt, space.NumElems(),
			func() ([]float64, error) { return chdf5.ReadAttribute[float64](a) },
			func() ([]string, error) { return chdf5.ReadAttribute[string](a) })
	}
	r.line("A", q(path), fp(dt), fp(space), vt, rt)
}

// readT records Read[float64] of numeric data and Read[string] of strings.
func readT(dt *h5.Datatype, n uint64, f func() ([]float64, error), s func() ([]string, error)) string {
	if n*dt.Size > MaxReadTBytes {
		return "-"
	}
	switch {
	case dt.Class == h5.ClassInteger || dt.Class == h5.ClassFloat:
		v, err := f()
		if len(v) == 0 {
			v = []float64{} // compared by length and bits
		}
		return tok(v, err)
	case dt.Class == h5.ClassString || dt.IsVlenString():
		v, err := s()
		return tok(v, err)
	}
	return "-"
}

func collect(seq func(func(h5.Link, error) bool)) ([]h5.Link, error) {
	var out []h5.Link
	for l, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, l)
	}
	return out, nil
}

func (r *recorder) group(path string, g *chdf5.Group) {
	n, err := g.NumLinks()
	nt := "E"
	if err == nil {
		nt = strconv.Itoa(n)
	}
	crt, cerr := collect(g.LinksByCreationOrder())
	links, lerr := collect(g.Links())
	r.line("G", q(path), nt, tok(crt, cerr), tok(links, lerr))
}

func (r *recorder) dataset(path string, d *chdf5.Dataset) {
	dt, space := d.Datatype(), d.Dataspace()
	layout, lerr := d.Layout()
	layout.Version, layout.ChunkIndex = 0, 0
	filters, ferr := d.Filters()
	fill, fverr := d.FillValue()
	fill.Version = 0
	r.line("D", q(path), fp(dt), fp(space), tok(layout, lerr), tok(filters, ferr), tok(fill, fverr))
	if len(layout.External) > 0 {
		r.external = true
	}
	if dt == nil {
		return
	}
	vt, rt := "big", "-"
	var vals []any
	var reason string
	if space.NumElems()*dt.Size <= MaxReadBytes {
		var err error
		vals, err = d.ReadValues()
		vt = tok(vals, err)
		if err != nil {
			reason = readFailure(filters, layout)
		}
		// Read[T] is ReadValues + h5.ConvertValues: convert the values
		// read rather than read the dataset again, unless the read failed.
		rt = readT(dt, space.NumElems(),
			func() ([]float64, error) {
				if err != nil {
					return chdf5.Read[float64](d)
				}
				return h5.ConvertValues[float64](vals)
			},
			func() ([]string, error) {
				if err != nil {
					return chdf5.Read[string](d)
				}
				return h5.ConvertValues[string](vals)
			})
	}
	ht, ot := hyperslabs(d, space)
	fields := []string{"V", q(path), vt, rt, ht, ot}
	if reason != "" {
		fields = append(fields, "#"+reason)
	}
	r.line(fields...)
	r.refs(path, vals)
}

// Hyperslabs returns the conformance suite's strided selection of a
// dataspace (ok false when it does not apply) and its out-of-bounds
// selection (ok false when the dataspace is not simple of rank 1 to 3).
func Hyperslabs(space h5.Dataspace) (strided h5.Hyperslab, stridedOK bool, out h5.Hyperslab, outOK bool) {
	rank := space.Rank()
	if space.Class != h5.SpaceSimple || rank < 1 || rank > 3 {
		return strided, false, out, false
	}
	strided = h5.Hyperslab{Start: make([]uint64, rank), Stride: make([]uint64, rank), Count: make([]uint64, rank), Block: make([]uint64, rank)}
	stridedOK = true
	for i, d := range space.Dims {
		if d < 2 {
			stridedOK = false
		}
		strided.Start[i], strided.Stride[i], strided.Count[i], strided.Block[i] = 1, 2, min(3, d/2), 1
	}
	out = h5.Hyperslab{Start: append([]uint64(nil), space.Dims...), Count: make([]uint64, rank)}
	for i := range out.Count {
		out.Count[i] = 1
	}
	return strided, stridedOK, out, true
}

func hyperslabs(d *chdf5.Dataset, space h5.Dataspace) (string, string) {
	strided, sok, out, ook := Hyperslabs(space)
	ht, ot := "-", "-"
	if sok {
		ht = tok(d.ReadHyperslab(strided))
	}
	if ook {
		_, err := d.ReadHyperslab(out)
		ot = okTok(err)
	}
	return ht, ot
}

// refs follows the first MaxRefs defined object references of a dataset's
// values with OpenAddress (in the file being recorded only).
func (r *recorder) refs(path string, vals []any) {
	if r.f == nil {
		return
	}
	n := 0
	for i, v := range vals {
		ref, ok := v.(h5.ObjectRef)
		if !ok || ref.Undefined || ref.Address == 0 {
			continue
		}
		if n++; n > MaxRefs {
			return
		}
		p := q(fmt.Sprintf("%s[%d]", path, i))
		o, err := r.f.OpenAddress(ref.Address)
		if err != nil {
			r.line("R", p, "E", "-")
			continue
		}
		it, _, _ := infoTok(o)
		o.Close()
		r.line("R", p, "O", it)
	}
}

// linkTarget records the target of a soft or external link.
func (r *recorder) linkTarget(path string, l h5.Link) {
	if l.Type != h5.LinkSoft && l.Type != h5.LinkExternal {
		return
	}
	o, err := r.f.Open(path)
	if err != nil {
		r.line("T", q(path), l.Type.String(), "E")
		return
	}
	defer o.Close()
	if l.Type == h5.LinkSoft {
		it, _, _ := infoTok(o)
		r.line("T", q(path), l.Type.String(), "O", it)
		return
	}
	r.line("T", q(path), l.Type.String(), "O")
	sub := &recorder{done: map[uint64]bool{}}
	sub.object(path, o, false)
	r.lines = append(r.lines, sub.lines...)
}

// readFailure classifies why libhdf5 refuses a dataset's data (as the
// conformance suite does).
func readFailure(filters []h5.Filter, l h5.Layout) string {
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

// openWithDriver opens a family, multi or split file by the name of one of
// its members with the matching chdf5 driver.
func openWithDriver(name string) *chdf5.File {
	try := func(n, driver string) *chdf5.File {
		f, err := chdf5.Open(n, chdf5.WithDriver(driver))
		if err != nil {
			return nil
		}
		return f
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
	return nil
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
