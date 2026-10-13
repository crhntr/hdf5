package hdf5

// Translated from src/H5Fsuper.c and src/H5Fsuper_cache.c (read side only).

import (
	"bytes"

	"github.com/crhntr/hdf5/go/h5"
)

// H5F_SIGNATURE / H5F_SIGNATURE_LEN
var h5fSignature = []byte("\211HDF\r\n\032\n")

const h5fSignatureLen = 8

// Superblock versions (H5Fprivate.h).
const (
	hdf5SuperblockVersionDef    = 0
	hdf5SuperblockVersion1      = 1
	hdf5SuperblockVersion2      = 2
	hdf5SuperblockVersion3      = 3
	hdf5SuperblockVersionLatest = hdf5SuperblockVersion3

	hdf5FreespaceVersion    = 0
	hdf5ObjectdirVersion    = 0
	hdf5SharedheaderVersion = 0
	hdf5DriverinfoVersion0  = 0

	hdf5BtreeChunkIKDef  = 32 // HDF5_BTREE_CHUNK_IK_DEF
	hdf5BtreeSnodeIKDef  = 16 // HDF5_BTREE_SNODE_IK_DEF
	hdf5BtreeSymLeafKDef = 4  // HDF5_BTREE_SYM_LEAF_K_DEF
)

// Superblock status flags (H5Fpkg.h).
const (
	h5fSuperWriteAccess     = 0x01
	h5fSuperFileOK          = 0x02
	h5fSuperSWMRWriteAccess = 0x04
	h5fSuperAllFlags        = h5fSuperWriteAccess | h5fSuperFileOK | h5fSuperSWMRWriteAccess
)

// H5F_SUPERBLOCK_FIXED_SIZE: signature + version.
const h5fSuperblockFixedSize = h5fSignatureLen + 1

// Driver info block header size (H5F_DRVINFOBLOCK_HDR_SIZE).
const h5fDrvinfoblockHdrSize = 16

// superblock is H5F_super_t.
type superblock struct {
	superVers   uint8
	statusFlags uint8
	sizeofAddr  uint8
	sizeofSize  uint8
	symLeafK    uint
	btreeK      [2]uint
	baseAddr    haddr // absolute base address for rel. addrs. (superblock location)
	extAddr     haddr // relative address of superblock extension
	driverAddr  haddr // file driver information block address
	rootAddr    haddr // root group address
	rootEnt     *h5gEntry
	storedEOF   haddr // EOF address stored in the superblock (udata->stored_eof)
}

// drvinfo is H5O_drvinfo_t: the raw driver information block.
type drvinfo struct {
	name string // 8-character driver name/version
	buf  []byte
}

// h5fSuperblockVarlenSize is H5F_SUPERBLOCK_VARLEN_SIZE(v, sizeof_addr, sizeof_size).
func h5fSuperblockVarlenSize(v uint8, sizeofAddr, sizeofSize int) int {
	common := 2 + 1 + 3 + 1 + 4 + 4 // H5F_SUPERBLOCK_VARLEN_SIZE_COMMON: versions, reserved, shared vers/sizes, reserved, sym leaf k + btree k, status flags
	switch {
	case v == 0:
		return common + 4*sizeofAddr + h5gSizeofEntry(sizeofAddr, sizeofSize)
	case v == 1:
		return common + 2 + 2 + 4*sizeofAddr + h5gSizeofEntry(sizeofAddr, sizeofSize)
	default:
		return 2 + 1 + 4*sizeofAddr + 4 // sizes, flags, 4 addresses, checksum
	}
}

// locateSignature is H5FD_locate_signature: search for the file signature at
// address zero and then at powers of two >= 512 (user blocks).
func (f *File) locateSignature() (haddr, error) {
	addr := f.fd.EOF()
	maxpow := uint(0)
	for ; addr != 0; maxpow++ {
		addr >>= 1
	}
	if maxpow < 9 {
		maxpow = 9
	}
	buf := make([]byte, h5fSignatureLen)
	for n := uint(8); n < maxpow; n++ {
		if n == 8 {
			addr = 0
		} else {
			addr = haddr(1) << n
		}
		if addr+h5fSignatureLen > f.fd.EOF() {
			break
		}
		if _, err := f.fd.ReadAt(buf, int64(addr)); err != nil {
			return haddrUndef, wrapf(err, H5E_IO, H5E_CANTINIT, "unable to read file signature")
		}
		if bytes.Equal(buf, h5fSignature) {
			return addr, nil
		}
	}
	return haddrUndef, nil
}

// superRead is H5F__super_read: locate and decode the superblock, the driver
// information block and the superblock extension, and set the file's
// address/length sizes and base address.
func (f *File) superRead() error {
	superAddr, err := f.locateSignature()
	if err != nil {
		return err
	}
	if !addrDefined(superAddr) {
		return &Error{Major: H5E_FILE, Minor: H5E_NOTHDF5, Msg: "file signature not found"}
	}

	// Read the fixed part plus the largest possible variable part and let the
	// decoder consume what it needs (the C code reads
	// H5F_SUPERBLOCK_SPEC_READ_SIZE first and then the final size).
	const maxVarlen = 15 + 2 + 2 + 4*32 + (32 + 32 + 8 + 2*32) // v1 with 32-byte addresses/lengths
	n := h5fSuperblockFixedSize + maxVarlen
	if rem := f.fd.EOF() - superAddr; uint64(n) > rem {
		n = int(rem)
	}
	buf := make([]byte, n)
	if _, err := f.fd.ReadAt(buf, int64(superAddr)); err != nil {
		return wrapf(err, H5E_FILE, H5E_READERROR, "unable to read superblock")
	}
	p := &image{buf: buf}
	sblock, err := f.cacheSuperblockDeserialize(p)
	if err != nil {
		return err
	}
	f.sblock = sblock
	f.sizeofAddr = sblock.sizeofAddr
	f.sizeofSize = sblock.sizeofSize
	f.symLeafK = sblock.symLeafK
	f.btreeK = sblock.btreeK

	/* Check if superblock address is different from base address and adjust
	 * base address and "end of file" address if so.
	 */
	if superAddr != sblock.baseAddr {
		/* If the superblock moved in the file, adjust the EOF */
		sblock.storedEOF -= sblock.baseAddr - superAddr
		/* Adjust base address for offsets of the HDF5 data in the file */
		sblock.baseAddr = superAddr
	}
	f.baseAddr = sblock.baseAddr

	/* Decode the optional driver information block */
	if addrDefined(sblock.driverAddr) {
		d, err := f.cacheDrvrinfoDeserialize(sblock.driverAddr)
		if err != nil {
			return err
		}
		f.drvinfo = d
		if err := f.sbLoad(d); err != nil {
			return err
		}
	}

	/*
	 * Make sure that the data is not truncated. One case where this is
	 * possible is if the first file of a family of files was opened
	 * individually.
	 *
	 * The EOF check is skipped when the private property
	 * H5F_ACS_SKIP_EOF_CHECK_NAME exists in the fapl (here: skipEOFCheck).
	 *
	 * (The C library checks before decoding the driver information block;
	 *  here the check comes after it because sbLoad switches a family or
	 *  multi member opened by name to the driver of the whole set, whose
	 *  EOF is the one to check.)
	 */
	if !f.skipEOFCheck {
		/* H5FD_get_eof: relative to the base address */
		eof := uint64(0)
		if e := f.fd.EOF(); e > f.baseAddr {
			eof = e - f.baseAddr
		}
		/* (Account for the stored EOA being absolute offset -QAK) */
		if eof+sblock.baseAddr < sblock.storedEOF {
			return errorf(H5E_FILE, H5E_TRUNCATED, "truncated file: eof = %d, sblock->base_addr = %d, stored_eof = %d",
				eof, sblock.baseAddr, sblock.storedEOF)
		}
	}

	/*
	 * Tell the file driver how much address space has already been
	 * allocated so that it knows how to allocate additional memory.
	 */
	/* (H5F__set_eoa: reads beyond the EOA fail, see File.checkEOA; the
	 *  multi driver keeps the end of address of each member) */
	f.eoa = sblock.storedEOF
	if _, ok := f.fd.(*multiDriver); ok {
		f.eoa = haddrUndef
	}
	if fam, ok := f.fd.(*familyDriver); ok {
		fam.setEOA(sblock.storedEOF)
	}

	/* Check for the superblock extension and decode the messages that
	 * matter for reading: driver info, B-tree 'K' values and the shared
	 * message table.
	 */
	if addrDefined(sblock.extAddr) {
		if err := f.superExtRead(); err != nil {
			return err
		}
	}
	return nil
}

// cacheSuperblockDeserialize is H5F__cache_superblock_deserialize preceded
// by H5F__superblock_prefix_decode and H5F__cache_superblock_verify_chksum.
func (f *File) cacheSuperblockDeserialize(p *image) (*superblock, error) {
	sblock := &superblock{}

	/* --- H5F__superblock_prefix_decode --- */

	/* Skip over signature (already checked when locating the superblock) */
	if p.overflow(h5fSignatureLen) {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	p.skip(h5fSignatureLen)

	/* Superblock version */
	if p.overflow(1) {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
	}
	sblock.superVers = p.uint8()
	if sblock.superVers > hdf5SuperblockVersionLatest {
		return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad superblock version number")
	}

	/* Determine the size of addresses & size of offsets, for computing the
	 * variable-sized portion of the superblock.
	 */
	if sblock.superVers < hdf5SuperblockVersion2 {
		if p.overflow(6) {
			return nil, errorf(H5E_FILE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		sblock.sizeofAddr = p.buf[p.off+4]
		sblock.sizeofSize = p.buf[p.off+5]
	} else {
		if p.overflow(2) {
			return nil, errorf(H5E_FILE, H5E_OVERFLOW, "ran off end of input buffer while decoding")
		}
		sblock.sizeofAddr = p.buf[p.off+0]
		sblock.sizeofSize = p.buf[p.off+1]
	}
	if sblock.sizeofAddr != 2 && sblock.sizeofAddr != 4 && sblock.sizeofAddr != 8 &&
		sblock.sizeofAddr != 16 && sblock.sizeofAddr != 32 {
		return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad byte number in an address")
	}
	if sblock.sizeofSize != 2 && sblock.sizeofSize != 4 && sblock.sizeofSize != 8 &&
		sblock.sizeofSize != 16 && sblock.sizeofSize != 32 {
		return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad byte number for object size")
	}
	p.sizeofAddr = sblock.sizeofAddr
	p.sizeofSize = sblock.sizeofSize

	/* --- H5F__cache_superblock_get_final_load_size: trim the image to the
	 * actual superblock size --- */
	variableSize := h5fSuperblockVarlenSize(sblock.superVers, int(sblock.sizeofAddr), int(sblock.sizeofSize))
	if variableSize == 0 {
		return nil, errorf(H5E_FILE, H5E_BADVALUE, "variable size can't be zero")
	}
	total := h5fSuperblockFixedSize + variableSize
	if total > len(p.buf) {
		return nil, errorf(H5E_FILE, H5E_OVERFLOW, "truncated superblock")
	}
	p.buf = p.buf[:total]

	/* --- H5F__cache_superblock_verify_chksum --- */
	if sblock.superVers >= hdf5SuperblockVersion2 && f.opts.verifyChecksums {
		computed := checksumMetadata(p.buf[:total-4], 0)
		stored := le32(p.buf[total-4:])
		if computed != stored {
			return nil, errorf(H5E_FILE, H5E_BADCHECKSUM, "incorrect metadata checksum for superblock (stored %#x, computed %#x)", stored, computed)
		}
	}

	/* --- H5F__cache_superblock_deserialize --- */

	/* Check for older version of superblock format */
	if sblock.superVers < hdf5SuperblockVersion2 {
		/* Freespace version (hard-wired) */
		if hdf5FreespaceVersion != p.uint8() {
			return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad free space version number")
		}
		/* Root group version number (hard-wired) */
		if hdf5ObjectdirVersion != p.uint8() {
			return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad object directory version number")
		}
		/* Skip over reserved byte */
		p.skip(1)
		/* Shared header version number (hard-wired) */
		if hdf5SharedheaderVersion != p.uint8() {
			return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad shared-header format version number")
		}
		/* Skip over size of file addresses (already decoded) */
		p.skip(1)
		/* Skip over size of file sizes (already decoded) */
		p.skip(1)
		/* Skip over reserved byte */
		p.skip(1)

		/* Various B-tree sizes */
		symLeafK := p.uint16()
		if symLeafK == 0 {
			return nil, errorf(H5E_FILE, H5E_BADRANGE, "bad symbol table leaf node 1/2 rank")
		}
		sblock.symLeafK = uint(symLeafK)

		/* Need 'get' call to set other array values */
		snodeBtreeK := p.uint16()
		if snodeBtreeK == 0 {
			return nil, errorf(H5E_FILE, H5E_BADRANGE, "bad 1/2 rank for btree internal nodes")
		}
		sblock.btreeK[h5bSnodeID] = uint(snodeBtreeK)

		/* File status flags (not really used yet) */
		statusFlags := p.uint32()
		if statusFlags > 255 {
			return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad superblock status flags")
		}
		sblock.statusFlags = uint8(statusFlags)
		if sblock.statusFlags&^h5fSuperAllFlags != 0 {
			return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad flag value for superblock")
		}

		/* If the superblock version # is greater than 0, read in the indexed
		 * storage B-tree internal 'K' value
		 */
		var chunkBtreeK uint
		if sblock.superVers > hdf5SuperblockVersionDef {
			chunkBtreeK = uint(p.uint16())
			/* Reserved bytes are present only in version 1 */
			if sblock.superVers == hdf5SuperblockVersion1 {
				p.skip(2)
			}
		} else {
			chunkBtreeK = hdf5BtreeChunkIKDef
		}
		sblock.btreeK[h5bChunkID] = chunkBtreeK

		/* Remainder of "variable-sized" portion of superblock */
		sblock.baseAddr = p.addr()
		sblock.extAddr = p.addr()
		sblock.storedEOF = p.addr()
		sblock.driverAddr = p.addr()
		if err := p.done(); err != nil {
			return nil, err
		}

		/* Decode the root group symbol table entry */
		ent, err := h5gEntDecode(p)
		if err != nil {
			return nil, wrapf(err, H5E_FILE, H5E_CANTDECODE, "can't decode root group symbol table entry")
		}
		sblock.rootEnt = ent

		/* Set the root group address to the correct value */
		sblock.rootAddr = ent.header

		/* NOTE: Driver info block is decoded separately, later */
	} else {
		/* Skip over size of file addresses (already decoded) */
		p.skip(1)
		/* Skip over size of file sizes (already decoded) */
		p.skip(1)

		/* File status flags (not really used yet) */
		sblock.statusFlags = p.uint8()
		if sblock.statusFlags&^h5fSuperAllFlags != 0 {
			return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad flag value for superblock")
		}

		/* Base, superblock extension, end of file & root group object header addresses */
		sblock.baseAddr = p.addr()
		sblock.extAddr = p.addr()
		sblock.storedEOF = p.addr()
		sblock.rootAddr = p.addr()

		/* checksum verification already done in verify_chksum cb */
		/* Decode checksum */
		_ = p.uint32()

		/* The Driver Information Block may not appear with the version
		 * 2 super block.  Thus we set the driver_addr field of the in
		 * core representation of the super block HADDR_UNDEF to prevent
		 * any attempt to load the Driver Information Block.
		 */
		sblock.driverAddr = haddrUndef

		/* v2+ superblocks have no B-tree 'K' values in the superblock; the
		 * defaults apply unless the superblock extension overrides them. */
		sblock.symLeafK = hdf5BtreeSymLeafKDef
		sblock.btreeK[h5bSnodeID] = hdf5BtreeSnodeIKDef
		sblock.btreeK[h5bChunkID] = hdf5BtreeChunkIKDef
	}

	/* Size check */
	if err := p.done(); err != nil {
		return nil, wrapf(err, H5E_FILE, H5E_BADVALUE, "bad decoded superblock size")
	}
	return sblock, nil
}

// cacheDrvrinfoDeserialize is H5F__cache_drvrinfo_get_final_load_size +
// H5F__drvrinfo_prefix_decode + H5F__cache_drvrinfo_deserialize: read the
// driver information block at addr.
func (f *File) cacheDrvrinfoDeserialize(addr haddr) (*drvinfo, error) {
	p, err := f.readImage(addr, h5fDrvinfoblockHdrSize)
	if err != nil {
		return nil, wrapf(err, H5E_FILE, H5E_CANTLOAD, "unable to load driver info block")
	}

	/* Version number */
	drvVers := p.uint8()
	if drvVers != hdf5DriverinfoVersion0 {
		return nil, errorf(H5E_FILE, H5E_BADVALUE, "bad driver information block version number")
	}
	/* Reserved bytes */
	p.skip(3)
	/* Driver info size */
	dlen := p.uint32()
	/* Driver name and/or version */
	d := &drvinfo{name: string(p.bytes(8))}
	if err := p.done(); err != nil {
		return nil, err
	}

	/* Validate and decode driver information */
	d.buf, err = f.readAt(addr+h5fDrvinfoblockHdrSize, int(dlen))
	if err != nil {
		return nil, wrapf(err, H5E_FILE, H5E_CANTLOAD, "unable to load driver info block")
	}
	return d, nil
}

// Superblock returns a description of the file's superblock.
func (f *File) Superblock() h5.SuperblockInfo {
	s := f.sblock
	return h5.SuperblockInfo{
		Version:           s.superVers,
		SizeOfOffsets:     s.sizeofAddr,
		SizeOfLengths:     s.sizeofSize,
		BaseAddress:       s.baseAddr,
		ExtensionAddress:  s.extAddr,
		DriverInfoAddress: s.driverAddr,
		RootGroupAddress:  s.rootAddr,
		EndOfFileAddress:  s.storedEOF,
		StatusFlags:       s.statusFlags,
		SymLeafK:          s.symLeafK,
		SnodeBtreeK:       s.btreeK[h5bSnodeID],
		ChunkBtreeK:       s.btreeK[h5bChunkID],
		DriverName:        f.driverName(),
	}
}

// driverName returns the 8-character driver identifier from the driver
// information block ("NCSAfami", "NCSAmult", ...) or "" when absent.
func (f *File) driverName() string {
	if f.drvinfo == nil {
		return ""
	}
	return f.drvinfo.name
}
