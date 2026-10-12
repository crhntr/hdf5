package hdf5

// Translated from src/H5FDmulti.c (read side only).
//
// The multi driver (and the split driver, which is a multi driver with two
// members) dispatches I/O to several files depending on the address region
// being accessed: each memory usage type (superblock, B-tree, raw data,
// global heap, local heap, object header) is mapped to a member file that
// owns an address range starting at memb_addr. Addresses are absolute within
// the virtual address space; the member file offset is addr - memb_addr.

import (
	"math"
	"strings"
)

// h5fdMem is H5FD_mem_t: the memory usage types of the virtual file driver.
type h5fdMem uint8

const (
	h5fdMemDefault h5fdMem = iota // H5FD_MEM_DEFAULT: error or free-list
	h5fdMemSuper                  // H5FD_MEM_SUPER: superblock data
	h5fdMemBtree                  // H5FD_MEM_BTREE: B-tree data
	h5fdMemDraw                   // H5FD_MEM_DRAW: raw data (default)
	h5fdMemGheap                  // H5FD_MEM_GHEAP: global heap data
	h5fdMemLheap                  // H5FD_MEM_LHEAP: local heap data
	h5fdMemOhdr                   // H5FD_MEM_OHDR: object header data
	h5fdMemNTypes                 // H5FD_MEM_NTYPES
)

// multiDriver is H5FD_multi_t (together with its H5FD_multi_fapl_t): the
// member files of a multi/split file.
type multiDriver struct {
	name     string                     // base name that replaces "%s" in the name templates
	membMap  [h5fdMemNTypes]h5fdMem     // memory usage map
	membName [h5fdMemNTypes]string      // name generators
	membAddr [h5fdMemNTypes]haddr       // starting address per member
	membNext [h5fdMemNTypes]haddr       // address of next member
	membEOA  [h5fdMemNTypes]haddr       // EOA for individual files
	memb     [h5fdMemNTypes]*sec2Driver // member files (nil: not open)
	relax    bool                       // less stringent error checking
}

// uniqueMembers is the UNIQUE_MEMBERS macro: the member file types in usage
// type order, each once, after mapping H5FD_MEM_DEFAULT entries to
// themselves.
func h5fdMultiUniqueMembers(m *[h5fdMemNTypes]h5fdMem) []h5fdMem {
	var seen [h5fdMemNTypes]bool
	var out []h5fdMem
	for it := h5fdMemSuper; it < h5fdMemNTypes; it++ {
		lv := m[it]
		if lv == h5fdMemDefault {
			lv = it
		}
		if lv == h5fdMemDefault || lv >= h5fdMemNTypes {
			continue
		}
		if seen[lv] {
			continue
		}
		seen[lv] = true
		out = append(out, lv)
	}
	return out
}

// h5fdMultiSbDecode is H5FD_multi_sb_decode: decode the driver information
// ("NCSAmult") and set up the member files. The information is, in order:
//
//   - six bytes mapping the memory types SUPER..OHDR to the member type that
//     holds them, and two unused bytes;
//   - for each unique member in usage type order, its starting address and
//     its end-of-address marker, as unsigned 64-bit little endian integers;
//   - for each unique member in usage type order, its name template as a NUL
//     terminated string padded to a multiple of 8 bytes.
//
// The members are opened by (*multiDriver).openAll once the base name is
// known.
func h5fdMultiSbDecode(buf []byte) (*multiDriver, error) {
	d := &multiDriver{relax: true}

	/* Set default values */
	var memberAddr, memberEOA [h5fdMemNTypes]haddr
	for mt := range memberAddr {
		memberAddr[mt] = haddrUndef
		memberEOA[mt] = haddrUndef
	}

	/*
	 * Read the map and count the unique members.
	 */
	if len(buf) < 8 {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	var m [h5fdMemNTypes]h5fdMem
	for i := 0; i < 6; i++ {
		m[i+1] = h5fdMem(buf[i])
		if m[i+1] >= h5fdMemNTypes {
			return nil, errorf(H5E_FILE, H5E_BADRANGE, "file resource type out of range")
		}
	}
	unique := h5fdMultiUniqueMembers(&m)
	nseen := len(unique)
	p := buf[8:]

	/* Decode Address and EOA values */
	if len(p) < nseen*2*8 {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	for _, mt := range unique {
		memberAddr[mt] = h5fdMultiU64LE(p)
		memberEOA[mt] = h5fdMultiU64LE(p[8:])
		p = p[16:]
	}

	/* Decode name templates */
	var memberName [h5fdMemNTypes]string
	for _, mt := range unique {
		end := 0
		for end < len(p) && p[end] != 0 {
			end++
		}
		if end >= len(p) {
			return nil, errorf(H5E_FILE, H5E_OVERFLOW, "unterminated member name template")
		}
		memberName[mt] = string(p[:end])
		n := end + 1
		n = (n + 7) &^ 7
		if n > len(p) {
			n = len(p)
		}
		p = p[n:]
	}

	/* Commit the mapping, the member starting addresses and the name
	 * templates (the freshly created driver has nothing to close).
	 */
	d.membMap = m
	d.membAddr = memberAddr
	d.membName = memberName
	d.membEOA = memberEOA
	return d, nil
}

// openAll is the tail of H5FD_multi_sb_decode and H5FD_multi_open: compute
// the derived properties and open the member files. name is the base name
// substituted for "%s" in the name templates.
func (d *multiDriver) openAll(name string) error {
	d.name = name
	d.computeNext()

	/* Open all necessary files */
	if err := d.openMembers(); err != nil {
		d.Close()
		return wrapf(err, H5E_INTERNAL, H5E_BADVALUE, "open_members() failed")
	}

	/* The superblock member is required (H5FD_multi_open). */
	sm := d.membMap[h5fdMemSuper]
	if sm == h5fdMemDefault {
		sm = h5fdMemSuper
	}
	if d.memb[sm] == nil {
		d.Close()
		return errorf(H5E_FILE, H5E_CANTOPENFILE, "unable to open the member file holding the superblock (%q)", d.membFileName(sm))
	}
	return nil
}

func h5fdMultiU64LE(b []byte) uint64 {
	_ = b[7]
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

// computeNext is compute_next: compute the memb_next[] values of the file
// based on the file's member map and the member starting addresses.
func (d *multiDriver) computeNext() {
	for mt := range d.membNext {
		d.membNext[mt] = haddrUndef
	}

	unique := h5fdMultiUniqueMembers(&d.membMap)
	for _, mt1 := range unique {
		for _, mt2 := range unique {
			if d.membAddr[mt1] < d.membAddr[mt2] &&
				(d.membNext[mt1] == haddrUndef || d.membNext[mt1] > d.membAddr[mt2]) {
				d.membNext[mt1] = d.membAddr[mt2]
			}
		}
		if d.membNext[mt1] == haddrUndef {
			d.membNext[mt1] = haddrUndef - 1 /*last member (HADDR_MAX)*/
		}
	}
}

// h5fdMultiExpandName is the snprintf(tmp, sizeof(tmp), template, name) of
// open_members: "%s" in the member name template is replaced by the file
// name; "%%" is a literal percent sign.
func h5fdMultiExpandName(template, name string) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(template); i++ {
		c := template[i]
		if c != '%' {
			sb.WriteByte(c)
			continue
		}
		i++
		if i >= len(template) {
			return "", errorf(H5E_VFL, H5E_BADVALUE, "invalid member name template %q", template)
		}
		switch template[i] {
		case 's':
			sb.WriteString(name)
		case '%':
			sb.WriteByte('%')
		default:
			return "", errorf(H5E_VFL, H5E_BADVALUE, "invalid member name template %q", template)
		}
	}
	if sb.Len() >= 1024 /*H5FD_MULT_MAX_FILE_NAME_LEN*/ {
		return "", errorf(H5E_VFL, H5E_BADVALUE, "filename is too long and would be truncated")
	}
	return sb.String(), nil
}

// membFileName returns the file name of member mt ("" if the template is
// invalid).
func (d *multiDriver) membFileName(mt h5fdMem) string {
	s, _ := h5fdMultiExpandName(d.membName[mt], d.name)
	return s
}

// openMembers is open_members: open all members which are not opened yet.
// With relax set a member that cannot be opened is tolerated (reads from its
// address range fail).
func (d *multiDriver) openMembers() error {
	nerrors := 0
	for _, mt := range h5fdMultiUniqueMembers(&d.membMap) {
		if d.memb[mt] != nil {
			continue /*already open*/
		}
		if d.membName[mt] == "" {
			return errorf(H5E_VFL, H5E_BADVALUE, "file resource type not set")
		}
		tmp, err := h5fdMultiExpandName(d.membName[mt], d.name)
		if err != nil {
			return err
		}
		m, err := h5fdSec2OpenMember(tmp)
		if err != nil {
			if !d.relax {
				nerrors++
			}
			continue
		}
		d.memb[mt] = m
	}
	if nerrors != 0 {
		return errorf(H5E_INTERNAL, H5E_BADVALUE, "error opening member files")
	}
	return nil
}

// ReadAt is H5FD_multi_read: find the member whose address range contains
// the address and read from it. The offset is the absolute address in the
// virtual address space, reinterpreted as unsigned (the addresses of the
// high members exceed 2^62 and, for the default multi layout, 2^63).
func (d *multiDriver) ReadAt(buf []byte, off int64) (int, error) {
	addr := uint64(off)

	/* Find the file to which this address belongs */
	hi := h5fdMemDefault
	var startAddr haddr
	for mt := h5fdMemSuper; mt < h5fdMemNTypes; mt++ {
		mmt := d.membMap[mt]
		if mmt == h5fdMemDefault {
			mmt = mt
		}
		if mmt == h5fdMemDefault || mmt >= h5fdMemNTypes {
			return 0, errorf(H5E_VFL, H5E_BADRANGE, "file resource type out of range")
		}

		if d.membAddr[mmt] > addr {
			continue
		}
		if d.membAddr[mmt] >= startAddr {
			startAddr = d.membAddr[mmt]
			hi = mmt
		}
	}
	if hi == h5fdMemDefault {
		return 0, errorf(H5E_VFL, H5E_READERROR, "no member file for address %d", addr)
	}

	/* Read from that member */
	m := d.memb[hi]
	if m == nil {
		return 0, errorf(H5E_VFL, H5E_READERROR, "member file %q for address %d is not open", d.membFileName(hi), addr)
	}
	rel := addr - startAddr
	if rel+uint64(len(buf)) < rel || rel+uint64(len(buf)) > d.membEOA[hi] {
		return 0, errorf(H5E_IO, H5E_OVERFLOW, "addr overflow, addr = %d, size = %d, eoa = %d", rel, len(buf), d.membEOA[hi])
	}
	if err := h5fdSec2Read(m, buf, rel); err != nil {
		return 0, err
	}
	return len(buf), nil
}

// EOF is H5FD_multi_get_eof for H5FD_MEM_DEFAULT: the maximum over the open
// members of the member's starting address plus its file size. (The C code
// guesses the end of a member that cannot be opened from the address of the
// next member when the driver is "relaxed"; a missing member is ignored
// here, reads from its range fail with a "not open" error.)
func (d *multiDriver) EOF() uint64 {
	var eof haddr
	for _, mt := range h5fdMultiUniqueMembers(&d.membMap) {
		m := d.memb[mt]
		if m == nil {
			continue
		}
		tmpEOF := m.size
		if tmpEOF > 0 {
			if tmpEOF > math.MaxUint64-1-d.membAddr[mt] {
				tmpEOF = math.MaxUint64 - 1
			} else {
				tmpEOF += d.membAddr[mt]
			}
		}
		if tmpEOF > eof {
			eof = tmpEOF
		}
	}
	return eof
}

// Close is H5FD_multi_close: close all the members.
func (d *multiDriver) Close() error {
	var first error
	for mt, m := range d.memb {
		if m == nil {
			continue
		}
		if err := m.Close(); err != nil && first == nil {
			first = err
		}
		d.memb[mt] = nil
	}
	return first
}

// baseFromMemberName derives the base name of a multi/split file from the
// name f was opened with. The user may pass the base name itself (e.g.
// "tmulti") or the name of one of the member files (e.g. "tmulti-s.h5"); in
// the latter case the suffix of the member name template that matches is
// stripped. The template of the member holding the superblock is tried
// first.
func (d *multiDriver) baseFromMemberName(name string) string {
	sm := d.membMap[h5fdMemSuper]
	if sm == h5fdMemDefault {
		sm = h5fdMemSuper
	}
	order := append([]h5fdMem{sm}, h5fdMultiUniqueMembers(&d.membMap)...)
	for _, mt := range order {
		tmpl := d.membName[mt]
		i := strings.Index(tmpl, "%s")
		if i != 0 || strings.Contains(tmpl[2:], "%") {
			/* only templates of the form "%s<suffix>" can be inverted */
			continue
		}
		if suffix := tmpl[2:]; suffix != "" && len(name) > len(suffix) && strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

// sbLoadMulti is the multi part of H5FD_sb_load: decode the driver
// information and replace f.fd (the flat superblock member) with a driver
// that reads all the member files.
func (f *File) sbLoadMulti(d *drvinfo) error {
	if f.name == "" {
		return errorf(H5E_VFL, H5E_UNSUPPORTED, "multi driver requires a file name")
	}
	if _, ok := f.fd.(*multiDriver); ok {
		return nil /* already opened as a multi file */
	}

	md, err := h5fdMultiSbDecode(d.buf)
	if err != nil {
		return wrapf(err, H5E_VFL, H5E_CANTDECODE, "unable to decode driver information")
	}
	if err := md.openAll(md.baseFromMemberName(f.name)); err != nil {
		return wrapf(err, H5E_VFL, H5E_CANTDECODE, "unable to decode driver information")
	}
	f.fd.Close()
	f.fd = md
	return nil
}

// h5fdMultiOpenBase opens the superblock member of the multi/split file whose
// base name is name (no extension), trying the member names "%s-s.h5" (the
// default multi layout), "%s-m.h5" (the split layout used by the HDF5 test
// files) and "%s.meta" (the default split metadata name). It returns nil
// when none of them exists.
func h5fdMultiOpenBase(name string) *sec2Driver {
	for _, suffix := range []string{"-s.h5", "-m.h5", ".meta"} {
		if m, err := h5fdSec2OpenMember(name + suffix); err == nil {
			return m
		}
	}
	return nil
}
