package hdf5

// Tests of the fixed / extensible array chunk index lookups against
// testdata/chunkidx.h5 (written by testdata/gen_chunkidx.c, see there) and
// of the extensible / fixed array decoding against h5debug for every
// corpus file holding such arrays.

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type chunkidxLine struct {
	scaled []uint64
	undef  bool
	addr   haddr // iter lines only
	nbytes uint64
	mask   uint32
	raw    []byte // chunk lines only
}

// readChunkidxExpected parses testdata/chunkidx_expected.txt.
func readChunkidxExpected(t *testing.T) (chunks, iters map[string][]chunkidxLine) {
	t.Helper()
	fh, err := os.Open("testdata/chunkidx_expected.txt")
	require.NoError(t, err)
	defer fh.Close()
	chunks, iters = map[string][]chunkidxLine{}, map[string][]chunkidxLine{}
	parseScaled := func(s string) []uint64 {
		var out []uint64
		for _, p := range strings.Split(s, ",") {
			v, err := strconv.ParseUint(p, 10, 64)
			require.NoError(t, err)
			out = append(out, v)
		}
		return out
	}
	num := func(s string) uint64 {
		v, err := strconv.ParseUint(s, 10, 64)
		require.NoError(t, err)
		return v
	}
	var cur string
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch f[0] {
		case "dataset":
			cur = f[1]
		case "chunk":
			l := chunkidxLine{scaled: parseScaled(f[1])}
			if f[2] == "UNDEF" {
				l.undef = true
			} else {
				l.nbytes, l.mask = num(f[2]), uint32(num(f[3]))
				l.raw, err = hex.DecodeString(f[4])
				require.NoError(t, err)
			}
			chunks[cur] = append(chunks[cur], l)
		case "iter":
			iters[cur] = append(iters[cur], chunkidxLine{scaled: parseScaled(f[1]), addr: num(f[2]), nbytes: num(f[3]), mask: uint32(num(f[4]))})
		}
	}
	require.NoError(t, sc.Err())
	return chunks, iters
}

// TestChunkidxGenerated looks up every chunk of the datasets of
// testdata/chunkidx.h5 and compares the raw chunk bytes at the address found
// with the ones H5Dread_chunk returned, and iterates over the indices.
func TestChunkidxGenerated(t *testing.T) {
	chunks, iters := readChunkidxExpected(t)
	deflate := &h5oPline{version: 2, nused: 1, filter: []h5zFilterInfo{{id: h5zFilterDeflate, cdValues: []uint32{6}}}}
	unlim := h5sUnlimitedDim

	// Layouts and dataspace maxima from h5debug chunkidx.h5 <object header>.
	tests := []struct {
		name     string
		idxType  uint8
		idxAddr  haddr
		chunkDim []uint64
		maxDims  []uint64
		pline    *h5oPline
		nchunks  int  // allocated chunks
		swizzled bool // unlimited dimension is not the first one
	}{
		{"ea_paged", h5dChunkIdxEarray, 479, []uint64{1, 1}, []uint64{unlim}, nil, 13, false},
		{"ea_filt_unlim1", h5dChunkIdxEarray, 1839, []uint64{2, 3, 4}, []uint64{6, unlim}, deflate, 11, true},
		{"ea_unlim_mid", h5dChunkIdxEarray, 1911, []uint64{2, 2, 3, 2}, []uint64{4, unlim, 6}, nil, 10, true},
		{"fa_paged", h5dChunkIdxFarray, 56694, []uint64{1, 1}, []uint64{3000}, nil, 2, false},
		{"fa_filt_paged", h5dChunkIdxFarray, 81095, []uint64{1, 4}, []uint64{2100}, deflate, 12, false},
	}
	for _, verify := range []bool{true, false} {
		f, err := Open("testdata/chunkidx.h5", WithChecksumVerification(verify))
		require.NoError(t, err)
		defer f.Close()
		for _, tc := range tests {
			t.Run(fmt.Sprintf("%s/verify=%v", tc.name, verify), func(t *testing.T) {
				layout := testChunkedLayout(tc.idxType, tc.idxAddr, tc.chunkDim...)
				getAddr := f.dEarrayIdxGetAddr
				iterate := f.dEarrayIdxIterate
				if tc.idxType == h5dChunkIdxFarray {
					getAddr = f.dFarrayIdxGetAddr
					iterate = f.dFarrayIdxIterate
				}

				nalloc := 0
				for _, c := range chunks[tc.name] {
					addr, nbytes, mask, err := getAddr(layout, tc.pline, tc.maxDims, c.scaled)
					require.NoError(t, err, "chunk %v", c.scaled)
					if c.undef {
						assert.Equal(t, haddrUndef, addr, "chunk %v", c.scaled)
						assert.Equal(t, uint64(0), nbytes, "chunk %v", c.scaled)
						continue
					}
					nalloc++
					require.True(t, addrDefined(addr), "chunk %v", c.scaled)
					assert.Equal(t, c.nbytes, nbytes, "chunk %v", c.scaled)
					assert.Equal(t, c.mask, mask, "chunk %v", c.scaled)
					raw, err := f.readAt(addr, int(nbytes))
					require.NoError(t, err)
					assert.Equal(t, c.raw, raw, "chunk %v", c.scaled)
				}
				assert.Equal(t, tc.nchunks, nalloc)

				// Iteration visits the allocated chunks in index order, as
				// H5Dchunk_iter does; the C library reports wrong coordinates
				// when the unlimited dimension is not the first one (see
				// h5dEarrayIdxIterateCb), so compare those only otherwise.
				var it []chunkidxLine
				require.NoError(t, iterate(layout, tc.pline, tc.maxDims, func(scaled []uint64, addr haddr, nbytes uint64, mask uint32) error {
					it = append(it, chunkidxLine{scaled: scaled, addr: addr, nbytes: nbytes, mask: mask})
					a2, n2, m2, err := getAddr(layout, tc.pline, tc.maxDims, scaled)
					require.NoError(t, err)
					assert.Equal(t, addr, a2, "chunk %v", scaled)
					assert.Equal(t, nbytes, n2, "chunk %v", scaled)
					assert.Equal(t, mask, m2, "chunk %v", scaled)
					return nil
				}))
				want := iters[tc.name]
				require.Len(t, it, len(want))
				for i := range want {
					assert.Equal(t, want[i].addr, it[i].addr)
					assert.Equal(t, want[i].nbytes, it[i].nbytes)
					assert.Equal(t, want[i].mask, it[i].mask)
					if !tc.swizzled {
						assert.Equal(t, want[i].scaled, it[i].scaled)
					}
				}
			})
		}
	}

	// The paged data blocks of /ea_paged: super blocks 13 and 14 have
	// 2048-element data blocks of two 1024-element pages; data block 5 of
	// super block 13 has only its first page initialized.
	f, err := Open("testdata/chunkidx.h5")
	require.NoError(t, err)
	defer f.Close()
	ea := testEaOpen(t, f, 479)
	hdr := ea.hdr
	require.Equal(t, uint64(1024), hdr.dblkPageNelmts)
	iblock, err := h5eaIblockProtect(hdr)
	require.NoError(t, err)
	sblkAddr := iblock.sblkAddrs[13-iblock.nsblks]
	require.True(t, addrDefined(sblkAddr))
	sblock, err := h5eaSblockProtect(hdr, sblkAddr, 13)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), sblock.dblkNpages)
	assert.Equal(t, uint64(1), sblock.dblkPageInitSize)
	assert.Equal(t, uint64(2048), sblock.dblkNelmts)
	assert.True(t, h5vmBitGet(sblock.pageInit, 4*2+0))
	assert.True(t, h5vmBitGet(sblock.pageInit, 4*2+1))
	assert.True(t, h5vmBitGet(sblock.pageInit, 5*2+0))
	assert.False(t, h5vmBitGet(sblock.pageInit, 5*2+1))
	// chunk 141500 is element 141500 (one element per chunk); its neighbour
	// in the uninitialized page reads as the fill value.
	v, err := ea.get(141500)
	require.NoError(t, err)
	assert.True(t, addrDefined(v.(haddr)))
	v, err = ea.get(131060 + 4 + 5*2048 + 1500)
	require.NoError(t, err)
	assert.Equal(t, haddrUndef, v)
}

// h5debugBin returns the path of the h5debug tool of the reference HDF5
// installation, skipping the test if it is not installed.
func h5debugBin(t *testing.T, name string) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local/hdf5-1.14.6/bin", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not installed", p)
	}
	return p
}

// h5debugDump is the parsed output of an h5debug run: the "Name: value"
// fields and the "#n: value" entries of each section.
type h5debugDump struct {
	fields   map[string]string
	sections map[string]map[uint64]string
}

var h5debugEntryRE = regexp.MustCompile(`^\s+(?:Element|Address) #(\d+):\s+(.*)$`)

func runH5debug(t *testing.T, bin, file string, args ...uint64) (*h5debugDump, error) {
	t.Helper()
	argv := []string{file}
	for _, a := range args {
		argv = append(argv, strconv.FormatUint(a, 10))
	}
	cmd := exec.Command(bin, argv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("h5debug %v: %v: %s", argv, err, stderr.String())
	}
	d := &h5debugDump{fields: map[string]string{}, sections: map[string]map[uint64]string{}}
	section := ""
	for _, line := range strings.Split(string(out), "\n") {
		if m := h5debugEntryRE.FindStringSubmatch(line); m != nil {
			n, _ := strconv.ParseUint(m[1], 10, 64)
			if d.sections[section] == nil {
				d.sections[section] = map[uint64]string{}
			}
			d.sections[section][n] = strings.TrimSpace(m[2])
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(line), ":") {
			section = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			d.fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return d, nil
}

func (d *h5debugDump) num(t *testing.T, key string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(d.fields[key], 10, 64)
	require.NoError(t, err, "field %q", key)
	return v
}

// h5debugElmt formats an array element as the class' debug callback
// (H5D__earray_debug / H5D__earray_filt_debug ...) prints it.
func h5debugElmt(v any) string {
	switch e := v.(type) {
	case haddr:
		return strconv.FormatUint(e, 10)
	case h5dEarrayFiltElmt:
		return fmt.Sprintf("{%d, %d, %x}", e.addr, e.nbytes, e.filterMask)
	case h5dFarrayFiltElmt:
		return fmt.Sprintf("{%d, %d, %x}", e.addr, e.nbytes, e.filterMask)
	}
	return fmt.Sprintf("%v", v)
}

// h5debugLayout is the chunked layout of a dataset as h5debug prints the
// object header.
type h5debugLayout struct {
	ohdr     uint64
	idxType  uint8
	idxAddr  haddr
	chunkDim []uint64
	maxDims  []uint64
	filtered bool
}

var (
	h5lsLocationRE = regexp.MustCompile(`Location:\s+\d+:(\d+)`)
	braceListRE    = regexp.MustCompile(`\{([^}]*)\}`)
)

// h5debugLayouts lists the datasets of a file with a fixed or extensible
// array chunk index (object locations from h5ls -r -v, layouts from h5debug
// of each object header).
func h5debugLayouts(t *testing.T, h5ls, h5debug, file string) []h5debugLayout {
	t.Helper()
	out, err := exec.Command(h5ls, "-r", "-v", file).Output()
	if err != nil {
		return nil
	}
	parseList := func(s string) []uint64 {
		m := braceListRE.FindStringSubmatch(s)
		if m == nil {
			return nil
		}
		var v []uint64
		for _, p := range strings.Split(m[1], ",") {
			p = strings.TrimSpace(p)
			if p == "UNLIM" {
				v = append(v, h5sUnlimitedDim)
				continue
			}
			n, err := strconv.ParseUint(p, 10, 64)
			if err != nil {
				return nil
			}
			v = append(v, n)
		}
		return v
	}
	var layouts []h5debugLayout
	for _, m := range h5lsLocationRE.FindAllStringSubmatch(string(out), -1) {
		ohdr, _ := strconv.ParseUint(m[1], 10, 64)
		dout, err := exec.Command(h5debug, file, m[1]).Output()
		if err != nil {
			continue
		}
		l := h5debugLayout{ohdr: ohdr}
		ok := false
		for _, line := range strings.Split(string(dout), "\n") {
			k, v, _ := strings.Cut(line, ":")
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			switch {
			case k == "Index Type" && v == "Extensible Array":
				l.idxType, ok = h5dChunkIdxEarray, true
			case k == "Index Type" && v == "Fixed Array":
				l.idxType, ok = h5dChunkIdxFarray, true
			case k == "Index address":
				l.idxAddr, _ = strconv.ParseUint(v, 10, 64)
			case k == "Dim Max":
				l.maxDims = parseList(v)
			case k == "Size" && strings.HasPrefix(v, "{"):
				l.chunkDim = parseList(v)
			case strings.Contains(line, "`filter pipeline'"):
				l.filtered = true
			}
		}
		if ok && addrDefined(l.idxAddr) && l.chunkDim != nil && l.maxDims != nil {
			layouts = append(layouts, l)
		}
	}
	return layouts
}

// TestEarrayFarrayH5debug compares, for every extensible and fixed array
// chunk index in the corpus (and in testdata/chunkidx.h5), every element
// with the element h5debug prints when dumping the array's index block,
// super blocks and data blocks, and checks the chunk index lookups against
// the elements.
func TestEarrayFarrayH5debug(t *testing.T) {
	h5debug := h5debugBin(t, "h5debug")
	h5ls := h5debugBin(t, "h5ls")

	files := corpusFiles(t)
	files = append(files, filepath.Join(repoRoot(t), "go/testdata/chunkidx.h5"))
	nEA, nFA, nSblk, nPagedFA := 0, 0, 0, 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		if !bytes.Contains(data, []byte(h5eaHdrMagic)) && !bytes.Contains(data, []byte(h5faHdrMagic)) {
			continue
		}
		layouts := h5debugLayouts(t, h5ls, h5debug, file)
		if len(layouts) == 0 {
			continue
		}
		rel, _ := filepath.Rel(repoRoot(t), file)
		f, err := Open(file)
		require.NoError(t, err, rel)
		for _, l := range layouts {
			t.Run(fmt.Sprintf("%s@%d", rel, l.idxAddr), func(t *testing.T) {
				var pline *h5oPline
				if l.filtered {
					pline = &h5oPline{version: 2, nused: 1}
				}
				layout := testChunkedLayout(l.idxType, l.idxAddr, l.chunkDim...)
				if l.idxType == h5dChunkIdxEarray {
					nEA++
					nSblk += checkEarrayH5debug(t, f, h5debug, file, l, layout, pline)
				} else {
					nFA++
					if checkFarrayH5debug(t, f, h5debug, file, l, layout, pline) {
						nPagedFA++
					}
				}
			})
		}
		f.Close()
	}
	t.Logf("compared %d extensible arrays (%d super blocks) and %d fixed arrays (%d paged) with h5debug", nEA, nSblk, nFA, nPagedFA)
	assert.NotZero(t, nEA)
	assert.NotZero(t, nFA)
	assert.NotZero(t, nSblk)
	assert.NotZero(t, nPagedFA)
}

// checkEarrayH5debug compares an extensible array with h5debug and returns
// the number of super blocks compared.
func checkEarrayH5debug(t *testing.T, f *File, h5debug, file string, l h5debugLayout, layout *h5oLayout, pline *h5oPline) int {
	ea := testEaOpen(t, f, l.idxAddr)
	hdr := ea.hdr

	hd, err := runH5debug(t, h5debug, file, l.idxAddr, l.ohdr)
	require.NoError(t, err)
	assert.Equal(t, hd.num(t, "Highest element index stored (+1)"), hdr.maxIdxSet)
	assert.Equal(t, hd.num(t, "Number of super blocks created"), hdr.nsuperBlks)
	assert.Equal(t, hd.num(t, "Number of data blocks created"), hdr.ndataBlks)
	assert.Equal(t, hd.num(t, "Header size"), hdr.size)
	if !addrDefined(hdr.idxBlkAddr) {
		return 0
	}
	assert.Equal(t, hd.num(t, "Index Block Address"), hdr.idxBlkAddr)

	// Expected elements, by array index.
	want := map[uint64]string{}
	ib, err := runH5debug(t, h5debug, file, hdr.idxBlkAddr, l.idxAddr, l.ohdr)
	require.NoError(t, err)
	for i, v := range ib.sections["Elements in Index Block"] {
		want[i] = v
	}
	iblock, err := h5eaIblockProtect(hdr)
	require.NoError(t, err)
	assert.Equal(t, ib.num(t, "Index Block size"), iblock.size)
	dumpDblock := func(addr haddr, start, nelmts uint64) {
		db, err := runH5debug(t, h5debug, file, addr, l.idxAddr, nelmts, l.ohdr)
		require.NoError(t, err)
		for i, v := range db.sections["Elements"] {
			want[start+i] = v
		}
	}
	for u, s := range ib.sections["Data Block Addresses in Index Block"] {
		a, _ := strconv.ParseUint(s, 10, 64)
		assert.Equal(t, a, iblock.dblkAddrs[u])
		if !addrDefined(a) {
			continue
		}
		// (the data block's super block and position in it)
		for si := uint64(0); si < iblock.nsblks; si++ {
			info := hdr.sblkInfo[si]
			if u >= info.startDblk && u < info.startDblk+info.ndblks {
				dumpDblock(a, uint64(hdr.idxBlkElmts)+info.startIdx+(u-info.startDblk)*info.dblkNelmts, info.dblkNelmts)
			}
		}
	}
	nsblk := 0
	for u, s := range ib.sections["Super Block Addresses in Index Block"] {
		a, _ := strconv.ParseUint(s, 10, 64)
		assert.Equal(t, a, iblock.sblkAddrs[u])
		if !addrDefined(a) {
			continue
		}
		sblkIdx := iblock.nsblks + u
		sb, err := runH5debug(t, h5debug, file, a, l.idxAddr, sblkIdx, l.ohdr)
		require.NoError(t, err)
		sblock, err := h5eaSblockProtect(hdr, a, sblkIdx)
		require.NoError(t, err)
		nsblk++
		assert.Equal(t, sb.num(t, "Super Block size"), sblock.size)
		info := hdr.sblkInfo[sblkIdx]
		for d, s := range sb.sections["Data Block Addresses in Super Block"] {
			da, _ := strconv.ParseUint(s, 10, 64)
			assert.Equal(t, da, sblock.dblkAddrs[d])
			// (h5debug can't dump paged data blocks; those are covered by
			// TestChunkidxGenerated)
			if addrDefined(da) && sblock.dblkNpages == 0 {
				dumpDblock(da, uint64(hdr.idxBlkElmts)+info.startIdx+d*info.dblkNelmts, info.dblkNelmts)
			}
		}
	}

	// Every element h5debug printed matches; an element of an unallocated
	// block is the fill value.
	nset := 0
	for idx, w := range want {
		if idx >= hdr.maxIdxSet {
			continue
		}
		v, err := ea.get(idx)
		require.NoError(t, err)
		assert.Equal(t, w, h5debugElmt(v), "element %d", idx)
		if !strings.HasPrefix(w, "18446744073709551615") && !strings.HasPrefix(w, "{18446744073709551615") {
			nset++
		}
	}

	// The chunk index yields the elements h5debug printed.
	n := 0
	require.NoError(t, f.dEarrayIdxIterate(layout, pline, l.maxDims, func(scaled []uint64, addr haddr, nbytes uint64, mask uint32) error {
		a2, _, _, err := f.dEarrayIdxGetAddr(layout, pline, l.maxDims, scaled)
		require.NoError(t, err)
		assert.Equal(t, addr, a2, "chunk %v", scaled)
		n++
		return nil
	}))
	if len(want) == int(hdr.maxIdxSet) { // (all elements were dumped)
		assert.Equal(t, nset, n)
	}
	return nsblk
}

// checkFarrayH5debug compares a fixed array with h5debug and reports
// whether its data block is paged.
func checkFarrayH5debug(t *testing.T, f *File, h5debug, file string, l h5debugLayout, layout *h5oLayout, pline *h5oPline) bool {
	fa := testFaOpen(t, f, l.idxAddr)
	hdr := fa.hdr

	hd, err := runH5debug(t, h5debug, file, l.idxAddr, l.ohdr)
	require.NoError(t, err)
	assert.Equal(t, hd.num(t, "Number of elements in Fixed Array"), hdr.nelmts)
	assert.Equal(t, hd.num(t, "Header size"), hdr.size)
	assert.Equal(t, hd.num(t, "Max. # of elements in data block page"), uint64(1)<<hdr.maxDblkPageNelmtsBits)
	if !addrDefined(hdr.dblkAddr) {
		return false
	}
	assert.Equal(t, hd.num(t, "Fixed Array Data Block Address"), hdr.dblkAddr)

	// The array has one element per chunk of the maximum dataspace.
	nchunks := uint64(1)
	for u, m := range l.maxDims {
		nchunks *= (m + l.chunkDim[u] - 1) / l.chunkDim[u]
	}
	assert.Equal(t, nchunks, hdr.nelmts)

	dblock, err := h5faDblockProtect(hdr, hdr.dblkAddr)
	require.NoError(t, err)
	db, err := runH5debug(t, h5debug, file, hdr.dblkAddr, l.idxAddr, l.ohdr)
	if err != nil && dblock.npages > 0 {
		// H5FA__dblock_debug does not advance the page address past an
		// empty page, so it fails (bad checksum) on a data block with an
		// initialized page after an empty one; TestChunkidxGenerated
		// covers those.
		hole := false
		for p := uint64(1); p < dblock.npages; p++ {
			if h5vmBitGet(dblock.dblkPageInit, p) && !h5vmBitGet(dblock.dblkPageInit, p-1) {
				hole = true
			}
		}
		if hole {
			t.Logf("h5debug can't dump this paged data block: %v", err)
			return true
		}
	}
	require.NoError(t, err)
	assert.Equal(t, db.num(t, "Data Block size"), dblock.size)
	assert.Equal(t, db.num(t, "Number of pages in Data Block"), dblock.npages)

	want := map[uint64]string{}
	if dblock.npages == 0 {
		want = db.sections["Elements"]
	} else {
		// (H5FA__dblock_debug does not advance the page address past an
		// empty page, so only the pages before the first empty one are
		// printed correctly)
		for p := uint64(0); p < dblock.npages; p++ {
			sec, ok := db.sections[fmt.Sprintf("Elements in page %d", p)]
			if !ok || !h5vmBitGet(dblock.dblkPageInit, p) {
				for q := p; q < dblock.npages; q++ {
					if !h5vmBitGet(dblock.dblkPageInit, q) {
						for i := q * dblock.dblkPageNelmts; i < min((q+1)*dblock.dblkPageNelmts, hdr.nelmts); i++ {
							want[i] = "18446744073709551615"
							if pline != nil {
								want[i] = "{18446744073709551615, 0, 0}"
							}
						}
					}
				}
				break
			}
			for i, v := range sec {
				want[p*dblock.dblkPageNelmts+i] = v
			}
		}
	}
	require.NotEmpty(t, want)
	for idx, w := range want {
		v, err := fa.get(idx)
		require.NoError(t, err)
		assert.Equal(t, w, h5debugElmt(v), "element %d", idx)
	}

	// The chunk index lookups agree with the array.
	require.NoError(t, f.dFarrayIdxIterate(layout, pline, l.maxDims, func(scaled []uint64, addr haddr, nbytes uint64, mask uint32) error {
		a2, _, _, err := f.dFarrayIdxGetAddr(layout, pline, l.maxDims, scaled)
		require.NoError(t, err)
		assert.Equal(t, addr, a2, "chunk %v", scaled)
		idx := h5vmArrayOffsetPre(len(scaled), maxDown(l), scaled)
		if w, ok := want[idx]; ok {
			assert.Equal(t, w, h5debugElmt(mustGet(t, fa, idx)))
		}
		return nil
	}))
	return dblock.npages > 0
}

func maxDown(l h5debugLayout) []uint64 {
	rank := len(l.maxDims)
	mc := make([]uint64, rank)
	for u := range mc {
		mc[u] = (l.maxDims[u] + l.chunkDim[u] - 1) / l.chunkDim[u]
	}
	down := make([]uint64, rank)
	h5vmArrayDown(rank, mc, down)
	return down
}

func mustGet(t *testing.T, fa *h5fa, idx uint64) any {
	v, err := fa.get(idx)
	require.NoError(t, err)
	return v
}
