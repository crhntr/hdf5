//go:build cgo

package conformance

import (
	"path"
	"strings"
)

// allowance is a known, justified difference between hdf5 and libhdf5.
// Every entry must say why it is not a bug of the pure Go reader.
type allowance struct {
	file   string // path.Match pattern on the file name relative to the repository root ("" for any)
	object string // object path prefix ("" for any)
	aspect string // what is compared (see the comparer), "" for any
	text   string // substring of the mismatch message, "" for any
	why    string // the justification, reported in the summary
}

var allowList = []allowance{
	{
		// H5Lget_info2 reports a value size of 0 and H5Lget_val no data for
		// a link of a user-defined class that is not registered (no query
		// callback, H5L__get_val_real), so libhdf5 cannot provide the raw
		// link value hdf5 reads from the link message.
		aspect: "link", text: ".UserData\": nil-ness: libhdf5 []byte(nil)",
		why: "libhdf5 does not expose the data of unregistered user-defined links",
	},
	{
		aspect: "links", text: ".UserData\": nil-ness: libhdf5 []byte(nil)",
		why: "libhdf5 does not expose the data of unregistered user-defined links",
	},
	{
		// Only with a libhdf5 built with szip (API.md "Known
		// differences"): the szip decoder is not translated (it is not part
		// of the HDF5 sources), hdf5 returns ErrUnsupported.
		text: "szip filter not supported",
		why:  "szip decoding is not implemented in hdf5",
	},
	{
		// Only with a libhdf5 built without szip (API.md "Known
		// differences"): H5Pget_filter2 reports the name of an unnamed
		// filter from the registered filter class.
		aspect: "filters", text: `libhdf5 "Unknown library filter", hdf5 "szip"`,
		why: "libhdf5 without szip does not name the szip filter",
	},
}

// allowedDifference reports whether a mismatch is on the allow-list and
// why.
func allowedDifference(file, object, aspect, msg string) (string, bool) {
	for _, a := range allowList {
		if ok, _ := path.Match(a.file, file); a.file != "" && !ok {
			continue
		}
		if a.object != "" && !strings.HasPrefix(object, a.object) {
			continue
		}
		if a.aspect != "" && a.aspect != aspect {
			continue
		}
		if a.text != "" && !strings.Contains(msg, a.text) {
			continue
		}
		return a.why, true
	}
	return "", false
}
