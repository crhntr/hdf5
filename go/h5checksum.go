package hdf5

// Translated from src/H5checksum.c.

// lookup3Rot is H5_lookup3_rot.
func lookup3Rot(x uint32, k uint) uint32 { return (x << k) ^ (x >> (32 - k)) }

// lookup3Mix is H5_lookup3_mix.
func lookup3Mix(a, b, c uint32) (uint32, uint32, uint32) {
	a -= c
	a ^= lookup3Rot(c, 4)
	c += b
	b -= a
	b ^= lookup3Rot(a, 6)
	a += c
	c -= b
	c ^= lookup3Rot(b, 8)
	b += a
	a -= c
	a ^= lookup3Rot(c, 16)
	c += b
	b -= a
	b ^= lookup3Rot(a, 19)
	a += c
	c -= b
	c ^= lookup3Rot(b, 4)
	b += a
	return a, b, c
}

// lookup3Final is H5_lookup3_final.
func lookup3Final(a, b, c uint32) uint32 {
	c ^= b
	c -= lookup3Rot(b, 14)
	a ^= c
	a -= lookup3Rot(c, 11)
	b ^= a
	b -= lookup3Rot(a, 25)
	c ^= b
	c -= lookup3Rot(b, 16)
	a ^= c
	a -= lookup3Rot(c, 4)
	b ^= a
	b -= lookup3Rot(a, 14)
	c ^= b
	c -= lookup3Rot(b, 24)
	return c
}

// checksumLookup3 is H5_checksum_lookup3: Bob Jenkins' lookup3 hash of key
// with the given initial value.
func checksumLookup3(k []byte, initval uint32) uint32 {
	length := len(k)
	var a, b, c uint32
	a = 0xdeadbeef + uint32(length) + initval
	b, c = a, a

	/*--------------- all but the last block: affect some 32 bits of (a,b,c) */
	for length > 12 {
		a += uint32(k[0])
		a += uint32(k[1]) << 8
		a += uint32(k[2]) << 16
		a += uint32(k[3]) << 24
		b += uint32(k[4])
		b += uint32(k[5]) << 8
		b += uint32(k[6]) << 16
		b += uint32(k[7]) << 24
		c += uint32(k[8])
		c += uint32(k[9]) << 8
		c += uint32(k[10]) << 16
		c += uint32(k[11]) << 24
		a, b, c = lookup3Mix(a, b, c)
		length -= 12
		k = k[12:]
	}

	/*-------------------------------- last block: affect all 32 bits of (c) */
	switch length { /* all the case statements fall through */
	case 12:
		c += uint32(k[11]) << 24
		fallthrough
	case 11:
		c += uint32(k[10]) << 16
		fallthrough
	case 10:
		c += uint32(k[9]) << 8
		fallthrough
	case 9:
		c += uint32(k[8])
		fallthrough
	case 8:
		b += uint32(k[7]) << 24
		fallthrough
	case 7:
		b += uint32(k[6]) << 16
		fallthrough
	case 6:
		b += uint32(k[5]) << 8
		fallthrough
	case 5:
		b += uint32(k[4])
		fallthrough
	case 4:
		a += uint32(k[3]) << 24
		fallthrough
	case 3:
		a += uint32(k[2]) << 16
		fallthrough
	case 2:
		a += uint32(k[1]) << 8
		fallthrough
	case 1:
		a += uint32(k[0])
	case 0:
		return c
	}

	return lookup3Final(a, b, c)
}

// checksumMetadata is H5_checksum_metadata: the checksum used for all
// checksummed metadata in the file format (lookup3 with initval 0).
func checksumMetadata(data []byte, initval uint32) uint32 {
	return checksumLookup3(data, initval)
}

// checksumFletcher32 is H5_checksum_fletcher32, the checksum used by the
// Fletcher32 filter.
func checksumFletcher32(data []byte) uint32 {
	_len := len(data)
	length := _len / 2 /* Length in 16-bit words */
	var sum1, sum2 uint32

	/* Compute checksum for pairs of bytes */
	/* (the magic "360" value is the largest number of sums that can be
	 *  performed without numeric overflow)
	 */
	for length != 0 {
		tlen := length
		if tlen > 360 {
			tlen = 360
		}
		length -= tlen
		for {
			sum1 += uint32(uint16(data[0])<<8) | uint32(data[1])
			data = data[2:]
			sum2 += sum1
			tlen--
			if tlen == 0 {
				break
			}
		}
		sum1 = (sum1 & 0xffff) + (sum1 >> 16)
		sum2 = (sum2 & 0xffff) + (sum2 >> 16)
	}

	/* Check for odd # of bytes */
	if _len%2 != 0 {
		sum1 += uint32(uint16(data[0]) << 8)
		sum2 += sum1
		sum1 = (sum1 & 0xffff) + (sum1 >> 16)
		sum2 = (sum2 & 0xffff) + (sum2 >> 16)
	}

	/* Second reduction step to reduce sums to 16 bits */
	sum1 = (sum1 & 0xffff) + (sum1 >> 16)
	sum2 = (sum2 & 0xffff) + (sum2 >> 16)

	return (sum2 << 16) | sum1
}
