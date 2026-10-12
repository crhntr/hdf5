package hdf5

// Translated from src/H5FDfamily.c (read side only).
//
// The family driver presents a series of files as one linear address space.
// All members are logically the same size (memb_size); the address addr
// lives in member addr/memb_size at offset addr%memb_size.

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// h5fdFamDefMemSize is H5FD_FAM_DEF_MEM_SIZE (100 MiB).
const h5fdFamDefMemSize uint64 = 100 * 1024 * 1024

// h5fFamilyDefault is H5F_FAMILY_DEFAULT: "use the member size saved in the
// superblock".
const h5fFamilyDefault uint64 = 0

// familyDriver is H5FD_family_t: a set of member files read as one file.
type familyDriver struct {
	memb     []*sec2Driver // member files, in order (nmembs == len(memb))
	membSize uint64        // actual size of each member file
	pmemSize uint64        // member size passed in from property (openOptions.familyMemberSize)
	name     string        // name generator printf format
}

// h5fdFamilyGetDefaultPrintfFilename is
// H5FD__family_get_default_printf_filename: given a filename, return the
// filename modified into the printf-style format of this driver. For
// example, "file1.h5" becomes "file1-%06d.h5", which allows member
// filenames such as "file1-000000.h5", "file1-000001.h5", etc.
func h5fdFamilyGetDefaultPrintfFilename(oldFilename string) (string, error) {
	const suffix = "-%06d"

	if len(oldFilename) == 0 {
		return "", errorf(H5E_VFL, H5E_BADVALUE, "invalid filename")
	}

	/* Determine if filename contains a ".h5" extension. */
	if i := strings.Index(oldFilename, ".h5"); i >= 0 {
		/* Insert the printf format between the filename and ".h5" extension. */
		return oldFilename[:i] + suffix + ".h5", nil
	}
	/* If the filename doesn't contain a ".h5" extension, but contains
	 * AN extension, just insert the printf format before that extension.
	 */
	if i := strings.LastIndex(oldFilename, "."); i >= 0 {
		return oldFilename[:i] + suffix + oldFilename[i:], nil
	}
	/* If the filename doesn't contain an extension at all, just insert
	 * the printf format at the end of the filename.
	 */
	return oldFilename + suffix, nil
}

// h5fdFamilyMemberName is the snprintf(memb_name, name, idx) of
// H5FD__family_open for a pattern with (at most) one integer conversion
// (%d, %i or %u with optional 0 flag and width; %% is a literal percent).
// ok is false when the pattern contains any other conversion. verbs is the
// number of integer conversions in the pattern.
func h5fdFamilyMemberName(pattern string, idx uint) (name string, verbs int, ok bool) {
	var sb strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '%' {
			sb.WriteByte(c)
			continue
		}
		i++
		if i >= len(pattern) {
			return "", 0, false
		}
		if pattern[i] == '%' {
			sb.WriteByte('%')
			continue
		}
		zero := false
		if pattern[i] == '0' {
			zero = true
			i++
		}
		w := i
		for i < len(pattern) && pattern[i] >= '0' && pattern[i] <= '9' {
			i++
		}
		width := 0
		if i > w {
			n, err := strconv.Atoi(pattern[w:i])
			if err != nil || n > 255 {
				return "", 0, false
			}
			width = n
		}
		if i >= len(pattern) || (pattern[i] != 'd' && pattern[i] != 'i' && pattern[i] != 'u') {
			return "", 0, false
		}
		verbs++
		if verbs > 1 {
			return "", 0, false
		}
		s := strconv.FormatUint(uint64(idx), 10)
		for pad := width - len(s); pad > 0; pad-- {
			if zero {
				sb.WriteByte('0')
			} else {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(s)
	}
	return sb.String(), verbs, true
}

// h5fdFamilyIsPattern reports whether name is a valid family member name
// generator, i.e. contains exactly one integer conversion.
func h5fdFamilyIsPattern(name string) bool {
	_, verbs, ok := h5fdFamilyMemberName(name, 0)
	return ok && verbs == 1
}

// h5fdFamilyPatternFromMember derives the member name generator from the
// name of a member file: the trailing run of digits before the extension is
// replaced by a zero padded %d conversion of the same width, so
// "family_v16-000000.h5" becomes "family_v16-%06d.h5". ok is false when the
// file name has no such digit run.
func h5fdFamilyPatternFromMember(name string) (pattern string, ok bool) {
	dir, base := filepath.Split(name)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	n := len(stem)
	for n > 0 && stem[n-1] >= '0' && stem[n-1] <= '9' {
		n--
	}
	width := len(stem) - n
	if width == 0 {
		return "", false
	}
	esc := func(s string) string { return strings.ReplaceAll(s, "%", "%%") }
	return esc(dir) + esc(stem[:n]) + "%0" + strconv.Itoa(width) + "d" + esc(ext), true
}

// h5fdSec2OpenMember opens one member file read-only (H5FD_open with the
// sec2 driver).
func h5fdSec2OpenMember(name string) (*sec2Driver, error) {
	osf, err := os.Open(name)
	if err != nil {
		return nil, wrapf(err, H5E_FILE, H5E_CANTOPENFILE, "unable to open member file")
	}
	st, err := osf.Stat()
	if err != nil {
		osf.Close()
		return nil, wrapf(err, H5E_FILE, H5E_CANTOPENFILE, "unable to stat member file")
	}
	if !st.Mode().IsRegular() {
		osf.Close()
		return nil, errorf(H5E_FILE, H5E_CANTOPENFILE, "member file is not a regular file")
	}
	return &sec2Driver{f: osf, size: uint64(st.Size())}, nil
}

// h5fdSec2Read is H5FD__sec2_read for a member file: bytes past the end of
// the physical file are zero filled (the address is valid but nothing has
// been written there).
func h5fdSec2Read(m *sec2Driver, buf []byte, off uint64) error {
	if off >= m.size {
		clear(buf)
		return nil
	}
	n, err := m.f.ReadAt(buf, int64(off))
	if err != nil && err != io.EOF {
		return wrapf(err, H5E_IO, H5E_READERROR, "file read failed")
	}
	clear(buf[n:])
	return nil
}

// h5fdFamilySbDecode is H5FD__family_sb_decode: decode the driver
// information block ("NCSAfami": the member file size as 8 bytes, little
// endian) and update the driver accordingly.
func (d *familyDriver) sbDecode(buf []byte) error {
	/* Read member file size. Skip name template for now although it's saved. */
	if len(buf) < 8 {
		return errorf(H5E_VFL, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	msize := uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 |
		uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48 | uint64(buf[7])<<56

	/* Default - use the saved member size */
	if d.pmemSize == h5fFamilyDefault {
		d.pmemSize = msize
	}

	/* Check if member size from file access property is correct */
	if msize != d.pmemSize {
		return errorf(H5E_VFL, H5E_BADVALUE,
			"Family member size should be %d.  But the size from file access property is %d",
			msize, d.pmemSize)
	}

	/* Update member file size to the size saved in the superblock.
	 * That's the size intended to be. (H5FD__family_open set memb_size to
	 * the physical size of the first member, which is smaller than the
	 * logical member size when the family consists of a single short
	 * member; the superblock value wins.)
	 */
	if msize == 0 {
		return errorf(H5E_VFL, H5E_BADVALUE, "invalid family member size 0")
	}
	d.membSize = msize
	return nil
}

// h5fdFamilyOpen is H5FD__family_open for read-only access: open the family
// of files whose names are generated from the printf-style pattern name.
// pmemSize is the member size from the file access properties
// (h5fFamilyDefault if not set). Opening stops at the first missing member.
func h5fdFamilyOpen(name string, pmemSize uint64) (*familyDriver, error) {
	if name == "" {
		return nil, errorf(H5E_ARGS, H5E_BADVALUE, "invalid file name")
	}
	d := &familyDriver{membSize: h5fdFamDefMemSize, pmemSize: pmemSize, name: name}
	if pmemSize != 0 {
		d.membSize = pmemSize
	}

	/* Check that names are unique */
	m0, v0, ok0 := h5fdFamilyMemberName(name, 0)
	m1, _, ok1 := h5fdFamilyMemberName(name, 1)
	if !ok0 || !ok1 {
		return nil, errorf(H5E_VFL, H5E_BADVALUE, "invalid family file name pattern %q", name)
	}
	if m0 == m1 || v0 == 0 {
		def, err := h5fdFamilyGetDefaultPrintfFilename(name)
		if err != nil {
			return nil, wrapf(err, H5E_VFL, H5E_CANTGET, "can't get default printf-style filename")
		}
		d.name = def
	}

	/* Open all the family members */
	for {
		memb, _, _ := h5fdFamilyMemberName(d.name, uint(len(d.memb)))

		/* Attempt to open file. If the first file cannot be opened then
		 * fail; otherwise an open failure means that we've reached the
		 * last member.
		 */
		m, err := h5fdSec2OpenMember(memb)
		if err != nil {
			if len(d.memb) == 0 {
				return nil, wrapf(err, H5E_VFL, H5E_CANTOPENFILE, "unable to open member file")
			}
			break
		}
		d.memb = append(d.memb, m)
	}

	/* If the file is reopened and there's only one member file existing,
	 * this file may be smaller than the size specified through
	 * H5Pset_fapl_family(). Update the actual member size.
	 */
	if eof := d.memb[0].size; eof != 0 {
		d.membSize = eof
	}
	return d, nil
}

// ReadAt is H5FD__family_read: read from each member the request spans.
func (d *familyDriver) ReadAt(buf []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errorf(H5E_IO, H5E_BADVALUE, "negative file offset")
	}
	addr := uint64(off)
	size := len(buf)
	done := 0

	/* Read from each member */
	for done < size {
		u := addr / d.membSize
		sub := addr % d.membSize

		/* Number of bytes of this request that live in member u */
		req := uint64(size - done)
		if tempreq := d.membSize - sub; tempreq < req {
			req = tempreq
		}

		if u >= uint64(len(d.memb)) {
			return done, errorf(H5E_VFL, H5E_READERROR, "member file read failed: address %d is beyond the last family member", addr)
		}
		if err := h5fdSec2Read(d.memb[u], buf[done:done+int(req)], sub); err != nil {
			return done, wrapf(err, H5E_VFL, H5E_READERROR, "member file read failed")
		}

		addr += req
		done += int(req)
	}
	return done, nil
}

// EOF is H5FD__family_get_eof: the number of members before the last
// non-empty member times the member size, plus the size of that member.
func (d *familyDriver) EOF() uint64 {
	var eof uint64

	/* Find the last member that has a non-zero EOF and break out of the
	 * loop with `i' equal to that member. If all members have zero EOF
	 * then exit loop with i==0.
	 */
	i := len(d.memb) - 1
	for ; i >= 0; i-- {
		if eof = d.memb[i].size; eof != 0 {
			break
		}
		if i == 0 {
			break
		}
	}
	if i < 0 {
		return 0
	}

	/* The file size is the number of members before the i'th member plus
	 * the size of the i'th member.
	 */
	return eof + uint64(i)*d.membSize
}

// Close is H5FD__family_close: close all the members.
func (d *familyDriver) Close() error {
	var first error
	for _, m := range d.memb {
		if m == nil {
			continue
		}
		if err := m.Close(); err != nil && first == nil {
			first = err
		}
	}
	d.memb = nil
	return first
}

// openFamilyFromName opens the family whose first member is the file f was
// opened with (or whose name generator f was opened with).
func (f *File) openFamilyFromName() (*familyDriver, error) {
	if f.name == "" {
		return nil, errorf(H5E_VFL, H5E_UNSUPPORTED, "family driver requires a file name")
	}
	pattern := f.name
	if !h5fdFamilyIsPattern(pattern) {
		if p, ok := h5fdFamilyPatternFromMember(f.name); ok {
			pattern = p
		}
		/* otherwise h5fdFamilyOpen generates the default "-%06d" names */
	}
	return h5fdFamilyOpen(pattern, f.opts.familyMemberSize)
}

// sbLoadFamily is the family part of H5FD_sb_load: make sure f reads
// through a family driver and let the driver decode its information block.
func (f *File) sbLoadFamily(d *drvinfo) error {
	if fam, ok := f.fd.(*familyDriver); ok {
		if err := fam.sbDecode(d.buf); err != nil {
			return wrapf(err, H5E_VFL, H5E_CANTDECODE, "unable to decode driver information")
		}
		return nil
	}

	/* The file was opened as a single flat file (the first member): reopen
	 * it as a family and replace the flat driver.
	 */
	fam, err := f.openFamilyFromName()
	if err != nil {
		return err
	}
	if err := fam.sbDecode(d.buf); err != nil {
		fam.Close()
		return wrapf(err, H5E_VFL, H5E_CANTDECODE, "unable to decode driver information")
	}
	f.fd.Close()
	f.fd = fam
	return nil
}

// sbLoadNoDriverInfo handles family files written without a driver
// information block (e.g. by HDF5 1.6): when the superblock's end-of-file
// address lies beyond the physical file and the file name ends in a member
// number, the file is reopened as a family whose member size is that of the
// first member (or openOptions.familyMemberSize). The file is left alone
// when no family covers the stored EOF.
func (f *File) sbLoadNoDriverInfo() error {
	if f.name == "" || f.sblock == nil {
		return nil
	}
	if _, ok := f.fd.(*sec2Driver); !ok {
		return nil
	}
	if f.baseAddr+f.sblock.storedEOF <= f.fd.EOF() {
		return nil
	}
	pattern, ok := h5fdFamilyPatternFromMember(f.name)
	if !ok {
		return nil
	}
	fam, err := h5fdFamilyOpen(pattern, f.opts.familyMemberSize)
	if err != nil {
		return nil
	}
	if f.opts.familyMemberSize != 0 {
		fam.membSize = f.opts.familyMemberSize
	}
	if len(fam.memb) < 2 || fam.EOF() < f.baseAddr+f.sblock.storedEOF {
		fam.Close()
		return nil
	}
	f.fd.Close()
	f.fd = fam
	return nil
}
