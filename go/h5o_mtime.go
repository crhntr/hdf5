package hdf5

// Translated from src/H5Omtime.c (decode only): the old (string) and new
// (32-bit seconds) modification time messages.

import "time"

// h5oMtimeVersion is H5O_MTIME_VERSION: the current version of the new
// mtime message.
const h5oMtimeVersion = 1

// h5oMtimeNewDecode is H5O__mtime_new_decode: decode a new modification
// time message.
func h5oMtimeNewDecode(f *File, p *image) (h5oMtime, error) {
	/* decode */
	if p.overflow(1) {
		return 0, errDecodeOverflow()
	}
	if p.uint8() != h5oMtimeVersion {
		return 0, errorf(H5E_OHDR, H5E_CANTLOAD, "bad version number for mtime message")
	}

	/* Skip reserved bytes */
	if p.overflow(3) {
		return 0, errDecodeOverflow()
	}
	p.skip(3)

	/* Get the time_t from the file */
	if p.overflow(4) {
		return 0, errDecodeOverflow()
	}
	tmpTime := p.uint32()

	/* The return value */
	return h5oMtime(tmpTime), nil
}

// h5oMtimeDecode is H5O__mtime_decode: decode an old-style modification
// time message ("YYYYMMDDhhmmss" UTC plus two reserved bytes).
func h5oMtimeDecode(f *File, p *image) (h5oMtime, error) {
	/* Buffer should have 14 message bytes and 2 reserved bytes */
	if p.overflow(16) {
		return 0, errDecodeOverflow()
	}
	b := p.rest()
	for i := 0; i < 14; i++ {
		if b[i] < '0' || b[i] > '9' {
			return 0, errorf(H5E_OHDR, H5E_CANTINIT, "badly formatted modification time message")
		}
	}

	/* Convert YYYYMMDDhhmmss UTC to a time_t. */
	d := func(i int) int { return int(b[i] - '0') }
	year := d(0)*1000 + d(1)*100 + d(2)*10 + d(3)
	mon := d(4)*10 + d(5)
	mday := d(6)*10 + d(7)
	hour := d(8)*10 + d(9)
	minute := d(10)*10 + d(11)
	sec := d(12)*10 + d(13)
	/* (H5_make_time: mktime() corrected for the local time zone, i.e. the
	 * fields are taken as UTC; out-of-range fields are normalized as by
	 * mktime) */
	theTime := time.Date(year, time.Month(mon), mday, hour, minute, sec, 0, time.UTC).Unix()
	p.skip(16)

	/* The return value */
	return h5oMtime(theTime), nil
}
