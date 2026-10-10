package hdf5

// Decoding helpers.
//
// The C library decodes metadata with pointer-walking macros over a buffer
// ("image"): UINT16DECODE(p, v), H5F_addr_decode(f, &p, &addr), ... guarded
// by H5_IS_BUFFER_OVERFLOW(p, size, end). The image type below is the Go
// equivalent: a cursor over a byte slice with the same operations. Translated
// code should keep the C structure:
//
//	if H5_IS_BUFFER_OVERFLOW(image, 4, end)       ->  if p.overflow(4) { return errorf(..., H5E_OVERFLOW, "ran off end of input buffer while decoding") }
//	UINT32DECODE(image, x)                        ->  x = p.uint32()
//	H5F_addr_decode(f, &image, &addr)             ->  addr = p.addr()
//	H5F_DECODE_LENGTH(f, image, len)              ->  len = p.length()
//	UINT64DECODE_VAR(image, v, n)                 ->  v = p.uintVar(n)
//	image += n                                    ->  p.skip(n)
//	image - (const uint8_t*)_image                ->  p.off
//
// The accessors never panic: a read past the end records a sticky error
// (p.err) and returns zero, so code that forgets a bounds check still fails
// safely. Translated code is expected to check p.overflow(n) before decoding
// as the C code does, and to return p.err (via p.done()) at the end.

import (
	"encoding/binary"
	"math"
)

// haddr is the Go spelling of haddr_t.
type haddr = uint64

// haddrUndef is HADDR_UNDEF.
const haddrUndef haddr = math.MaxUint64

// addrDefined is H5_addr_defined / H5F_addr_defined.
func addrDefined(a haddr) bool { return a != haddrUndef }

// image is a bounds-checked cursor over a metadata buffer.
type image struct {
	buf []byte
	off int
	// Address and length sizes of the file the image belongs to
	// (H5F_SIZEOF_ADDR / H5F_SIZEOF_SIZE).
	sizeofAddr uint8
	sizeofSize uint8
	err        error
}

// newImage wraps buf using the address/length sizes of f.
func (f *File) newImage(buf []byte) *image {
	return &image{buf: buf, sizeofAddr: f.sizeofAddr, sizeofSize: f.sizeofSize}
}

// overflow is H5_IS_BUFFER_OVERFLOW(p, n, end): true when n more bytes are
// not available. It also records the condition in p.err.
func (p *image) overflow(n int) bool {
	if n < 0 || p.off+n > len(p.buf) || p.off+n < p.off {
		if p.err == nil {
			p.err = errorf(H5E_INTERNAL, H5E_OVERFLOW, "ran off end of input buffer while decoding (offset %d, need %d, have %d)", p.off, n, len(p.buf))
		}
		return true
	}
	return false
}

// done returns the sticky error, if any.
func (p *image) done() error { return p.err }

// remaining returns the number of bytes left.
func (p *image) remaining() int { return len(p.buf) - p.off }

// rest returns the unread bytes without advancing.
func (p *image) rest() []byte { return p.buf[p.off:] }

// sub returns a new image over the next n bytes (advancing p by n), or an
// image with a sticky error when n bytes are not available.
func (p *image) sub(n int) *image {
	q := &image{sizeofAddr: p.sizeofAddr, sizeofSize: p.sizeofSize}
	if p.overflow(n) {
		q.err = p.err
		return q
	}
	q.buf = p.buf[p.off : p.off+n]
	p.off += n
	return q
}

func (p *image) skip(n int) {
	if p.overflow(n) {
		return
	}
	p.off += n
}

// bytes returns the next n bytes (a sub-slice of the buffer, not a copy).
func (p *image) bytes(n int) []byte {
	if p.overflow(n) {
		return nil
	}
	b := p.buf[p.off : p.off+n]
	p.off += n
	return b
}

func (p *image) uint8() uint8 {
	if p.overflow(1) {
		return 0
	}
	v := p.buf[p.off]
	p.off++
	return v
}

func (p *image) uint16() uint16 {
	if p.overflow(2) {
		return 0
	}
	v := binary.LittleEndian.Uint16(p.buf[p.off:])
	p.off += 2
	return v
}

func (p *image) uint32() uint32 {
	if p.overflow(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(p.buf[p.off:])
	p.off += 4
	return v
}

func (p *image) uint64() uint64 {
	if p.overflow(8) {
		return 0
	}
	v := binary.LittleEndian.Uint64(p.buf[p.off:])
	p.off += 8
	return v
}

// int32 is INT32DECODE.
func (p *image) int32() int32 { return int32(p.uint32()) }

// uintVar is UINT64DECODE_VAR(p, v, n) / H5_DECODE_LENGTH_LEN: decode an
// n-byte little-endian unsigned integer. Bytes beyond the eighth are ignored
// (as in the C macro).
func (p *image) uintVar(n int) uint64 {
	if p.overflow(n) {
		return 0
	}
	var v uint64
	for i := 0; i < n; i++ {
		if i < 8 {
			v |= uint64(p.buf[p.off+i]) << (8 * uint(i))
		}
	}
	p.off += n
	return v
}

// length is H5F_DECODE_LENGTH(f, p, v).
func (p *image) length() uint64 { return p.uintVar(int(p.sizeofSize)) }

// addr is H5F_addr_decode(f, &p, &addr): an all-0xff address decodes as
// HADDR_UNDEF.
func (p *image) addr() haddr { return p.addrLen(int(p.sizeofAddr)) }

// addrLen is H5F_addr_decode_len(addr_len, &p, &addr).
func (p *image) addrLen(addrLen int) haddr {
	if p.overflow(addrLen) {
		return haddrUndef
	}
	allFF := true
	var a haddr
	for u := 0; u < addrLen; u++ {
		c := p.buf[p.off+u]
		if c != 0xff {
			allFF = false
		}
		if u < 8 {
			a |= haddr(c) << (8 * uint(u))
		}
	}
	p.off += addrLen
	if allFF {
		return haddrUndef
	}
	return a
}

// cstring decodes a NUL-terminated string from the current position
// (H5MM_strdup((const char*)p) followed by p += strlen+1). It fails if no NUL
// is found before the end of the buffer.
func (p *image) cstring() string {
	for i := p.off; i < len(p.buf); i++ {
		if p.buf[i] == 0 {
			s := string(p.buf[p.off:i])
			p.off = i + 1
			return s
		}
	}
	p.overflow(len(p.buf) - p.off + 1)
	return ""
}

// stringN decodes exactly n bytes as a string, stopping at the first NUL
// (strndup semantics).
func (p *image) stringN(n int) string {
	b := p.bytes(n)
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// align is H5_ALIGN / "pad to multiple of n" on the current offset.
func (p *image) align(n int) {
	if n <= 1 {
		return
	}
	rem := p.off % n
	if rem != 0 {
		p.skip(n - rem)
	}
}

// h5Align is the H5O_ALIGN_OLD(X) macro: round X up to a multiple of 8.
func h5AlignOld(x uint64) uint64 { return (x + 7) &^ 7 }

// Little-endian decode helpers for raw data buffers.
func le16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

// uintVarLen decodes an n-byte little-endian integer from b (no cursor).
func uintVarLen(b []byte, n int) uint64 {
	var v uint64
	for i := 0; i < n && i < len(b); i++ {
		if i < 8 {
			v |= uint64(b[i]) << (8 * uint(i))
		}
	}
	return v
}

// varLenBytes is H5VM_limit_enc_size / "number of bytes needed to encode a
// value": returns the smallest of 1, 2, 4, 8 that can hold v.
func varLenBytes(v uint64) int {
	switch {
	case v < 1<<8:
		return 1
	case v < 1<<16:
		return 2
	case v < 1<<32:
		return 4
	}
	return 8
}
