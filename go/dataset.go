package hdf5

// The public dataset API (see API.md), built on the dataset module
// (h5d_int.go, h5d_io.go and the layout files, translated from src/H5D*.c).

import (
	"github.com/crhntr/hdf5/go/h5"
)

// dataset returns the in-core dataset, opening it (H5D__open_oid) on first
// use.
func (d *Dataset) dataset() (*h5dShared, error) {
	d.open.Do(func() {
		oh, err := objHeader(d.f, d.oh, d.addr)
		if err != nil {
			d.openErr = wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open dataset")
			return
		}
		ds, err := d.f.dOpen(oh)
		if err != nil {
			d.openErr = wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open dataset")
			return
		}
		d.shared = ds
	})
	return d.shared, d.openErr
}

// Datatype describes the dataset's datatype (H5Dget_type). It returns nil
// if the datatype message cannot be decoded.
func (d *Dataset) Datatype() *h5.Datatype {
	if ds, err := d.dataset(); err == nil {
		return ds.datatype()
	}
	/* (the dataset could not be opened: read the datatype message alone) */
	oh, err := objHeader(d.f, d.oh, d.addr)
	if err != nil {
		return nil
	}
	v, err := d.f.msgRead(oh, h5oDtypeID)
	if err != nil {
		return nil
	}
	dt, _ := v.(*h5.Datatype)
	return dt
}

// Dataspace describes the dataset's dataspace (H5Dget_space). For a virtual
// dataset with unlimited mappings the current dimensions are those derived
// from the source datasets when the dataset was opened. It returns the zero
// Dataspace if the dataspace message cannot be decoded.
func (d *Dataset) Dataspace() h5.Dataspace {
	if ds, err := d.dataset(); err == nil {
		d.mu.Lock()
		defer d.mu.Unlock()
		return ds.dataspace()
	}
	/* (the dataset could not be opened: read the dataspace message alone) */
	oh, err := objHeader(d.f, d.oh, d.addr)
	if err != nil {
		return h5.Dataspace{}
	}
	v, err := d.f.msgRead(oh, h5oSdspaceID)
	if err != nil {
		return h5.Dataspace{}
	}
	ext, ok := v.(*h5sExtent)
	if !ok || ext == nil {
		return h5.Dataspace{}
	}
	return ext.public()
}

// Layout describes the dataset's storage layout (H5Pget_layout,
// H5Pget_chunk, H5Dget_offset, H5Dget_storage_size, H5Pget_external and
// the H5Pget_virtual_* queries). Version and ChunkIndex are the values
// stored in the layout message.
func (d *Dataset) Layout() (h5.Layout, error) {
	ds, err := d.dataset()
	if err != nil {
		return h5.Layout{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return ds.layoutInfo()
}

// Filters lists the filter pipeline (H5Pget_nfilters / H5Pget_filter2), in
// pipeline order; nil when the dataset has no filter pipeline. A filter
// stored without a name has the name of the library's filter class.
func (d *Dataset) Filters() ([]h5.Filter, error) {
	ds, err := d.dataset()
	if err != nil {
		return nil, err
	}
	return ds.filters(), nil
}

// FillValue describes the fill value properties (H5Pget_alloc_time,
// H5Pget_fill_time, H5Pfill_value_defined, H5Pget_fill_value). Value holds
// the user-defined fill value as raw bytes in the dataset's datatype, or
// nil when there is none or the datatype has variable-length or reference
// parts. Version is the version of the fill value message.
func (d *Dataset) FillValue() (h5.FillValueInfo, error) {
	ds, err := d.dataset()
	if err != nil {
		return h5.FillValueInfo{}, err
	}
	return ds.fillValue(ds.f)
}

// ReadValues reads the whole dataset as canonical values (h5/value.go) in
// row-major order: one value per element, one for a scalar dataspace, none
// for a null dataspace.
func (d *Dataset) ReadValues() ([]any, error) {
	ds, err := d.dataset()
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	vals, err := ds.f.dReadValues(ds, nil)
	if err != nil {
		return nil, err
	}
	if vals == nil {
		vals = []any{}
	}
	return vals, nil
}

// ReadHyperslab reads the elements selected by the regular hyperslab sel
// (H5Sselect_hyperslab on the dataset's dataspace), in row-major order. sel
// must have the rank of the dataspace (which must be simple) and lie within
// its current dimensions.
func (d *Dataset) ReadHyperslab(sel h5.Hyperslab) ([]any, error) {
	ds, err := d.dataset()
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if ds.space.extent.typ != h5.SpaceSimple {
		return nil, errorf(H5E_DATASPACE, H5E_BADTYPE, "%s: hyperslab selection on a dataspace that is not simple", d.path)
	}
	vals, err := ds.f.dReadHyperslab(ds, sel)
	if err != nil {
		return nil, err
	}
	if vals == nil {
		vals = []any{}
	}
	return vals, nil
}

// ReadInto reads the whole dataset into dst, a pointer to a slice of a
// supported Go type (see Read and h5.ConvertInto).
func (d *Dataset) ReadInto(dst any) error {
	vals, err := d.ReadValues()
	if err != nil {
		return err
	}
	return h5.ConvertInto(vals, dst)
}
