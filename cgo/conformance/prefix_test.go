//go:build cgo

package conformance

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	chdf5 "github.com/crhntr/hdf5/cgo"
	"github.com/crhntr/hdf5/go"
)

// TestFilePrefixes compares how the two packages locate the external data
// files (HDF5_EXTFILE_PREFIX, WithExternalFilePrefix) and the virtual
// dataset source files (HDF5_VDS_PREFIX, WithVirtualPrefix) of
// go/testdata/dset.h5. libhdf5 reads the environment variables once, when
// the library is initialized, so every case runs in a child process
// (TestFilePrefixChild) with its own environment.
func TestFilePrefixes(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	for _, c := range [][2]string{{"dset.h5", "a"}, {"dset_src.h5", "b"}, {"dset_ext.raw", "c"}} {
		b, err := os.ReadFile(filepath.Join(root, "go/testdata", c[0]))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, c[1]), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c[1], c[0]), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A copy of the raw data file with other values, to see which prefix
	// wins.
	raw, err := os.ReadFile(filepath.Join(root, "go/testdata/dset_ext.raw"))
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(raw[16:], 7)
	if err := os.MkdirAll(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "dset_ext.raw"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "a", "dset.h5")
	b, c, d := filepath.Join(dir, "b"), filepath.Join(dir, "c"), filepath.Join(dir, "d")

	for i, tc := range []struct {
		envEfile, envVDS, optEfile, optVDS string
	}{
		{},
		{envEfile: c},
		{envEfile: "${ORIGIN}/../c"},
		{optEfile: d},
		{optEfile: "${ORIGIN}/../d"},
		{envEfile: c, optEfile: d},
		{envEfile: filepath.Join(dir, "nothing"), optEfile: d},
		{envVDS: b},
		{envVDS: filepath.Join(dir, "nothing") + "::" + b},
		{envVDS: c},
		{optVDS: b},
		{optVDS: "${ORIGIN}/../b"},
		{envVDS: c, optVDS: b},
		{envVDS: b, optVDS: c},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestFilePrefixChild$", "-test.v")
			cmd.Env = append(os.Environ(),
				"HDF5_EXTFILE_PREFIX="+tc.envEfile, "HDF5_VDS_PREFIX="+tc.envVDS,
				"PREFIX_FILE="+name, "PREFIX_OPT_EFILE="+tc.optEfile, "PREFIX_OPT_VDS="+tc.optVDS)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%+v: %v\n%s", tc, err, out)
			}
		})
	}
}

// TestFilePrefixChild reads /external and /vds_xfile of PREFIX_FILE with
// both packages (run by TestFilePrefixes in a child process).
func TestFilePrefixChild(t *testing.T) {
	name := os.Getenv("PREFIX_FILE")
	if name == "" {
		t.Skip("run by TestFilePrefixes")
	}
	var hopts []hdf5.Option
	var copts []chdf5.Option
	if p := os.Getenv("PREFIX_OPT_EFILE"); p != "" {
		hopts = append(hopts, hdf5.WithExternalFilePrefix(p))
		copts = append(copts, chdf5.WithExternalFilePrefix(p))
	}
	if p := os.Getenv("PREFIX_OPT_VDS"); p != "" {
		hopts = append(hopts, hdf5.WithVirtualPrefix(p))
		copts = append(copts, chdf5.WithVirtualPrefix(p))
	}
	cf, err := chdf5.Open(name, copts...)
	if err != nil {
		t.Fatal(err)
	}
	defer cf.Close()
	hf, err := hdf5.Open(name, hopts...)
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()
	for _, path := range []string{"/external", "/vds_xfile"} {
		cd, err := cf.OpenDataset(path)
		if err != nil {
			t.Fatal(err)
		}
		hd, err := hf.OpenDataset(path)
		if err != nil {
			t.Fatal(err)
		}
		cv, cerr := chdf5.Read[int32](cd)
		hv, herr := hdf5.Read[int32](hd)
		cd.Close()
		switch {
		case (cerr == nil) != (herr == nil):
			t.Errorf("%s: libhdf5 error %v, hdf5 error %v", path, cerr, herr)
		case !reflect.DeepEqual(cv, hv):
			t.Errorf("%s: libhdf5 %v, hdf5 %v", path, cv, hv)
		default:
			t.Logf("%s: %v (error: %v)", path, hv, herr)
		}
	}
}
