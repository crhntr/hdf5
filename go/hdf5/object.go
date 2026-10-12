package hdf5

// The public object API (see API.md): the Object interface implemented by
// Group, Dataset and Datatype, and attributes. It is built on the object
// header code (h5o*.go), path traversal (h5g_traverse.go, h5g_loc.go) and
// the attribute module (h5a.go).

import (
	"errors"
	"iter"
	"sync"

	"github.com/HDFGroup/hdf5/go/h5"
)

// Object is an object in a file: a group, a dataset or a committed
// datatype.
type Object interface {
	// Path returns the path the object was opened by ("/" for the root
	// group, "" for objects opened by address).
	Path() string
	// Info returns the object's address, type, reference count, header
	// version, number of attributes and times.
	Info() (h5.ObjectInfo, error)
	// Attributes yields the object's attributes in name order
	// (H5_INDEX_NAME, H5_ITER_INC); a non-nil error ends the sequence.
	Attributes() iter.Seq2[*Attribute, error]
	// Attribute returns the attribute called name.
	Attribute(name string) (*Attribute, error)
	// NumAttributes returns the number of attributes of the object.
	NumAttributes() (int, error)
	// Comment returns the object's comment ("" when it has none).
	Comment() (string, error)
}

// Dataset is a dataset. The dataset methods are in dataset.go. A Dataset
// is safe for concurrent use.
type Dataset struct {
	f    *File  // file holding the dataset (an external file when opened through an external link)
	oh   *h5o   // the dataset's object header (cached; must not be modified)
	addr haddr  // address of the object header
	path string // path the dataset was opened by ("" when opened by address)

	// The in-core dataset (H5D_shared_t), opened by newDataset.
	open    sync.Once
	shared  *h5dShared
	openErr error

	// mu serializes the uses of shared: reading a virtual dataset opens
	// source datasets and patches the mapping selections in place.
	mu sync.Mutex
}

// Datatype is a committed (named) datatype.
type Datatype struct {
	f    *File        // file holding the datatype
	oh   *h5o         // the datatype's object header (cached; must not be modified)
	addr haddr        // address of the object header
	path string       // path the datatype was opened by ("" when opened by address)
	dt   *h5.Datatype // the decoded datatype message
}

// Attribute is an attribute of an object. The attribute methods other than
// Name are in attribute.go.
type Attribute struct {
	f    *File    // file holding the object
	obj  *h5o     // object header of the object the attribute belongs to
	attr *h5oAttr // the attribute message
	name string   // the attribute's name
}

// Name returns the attribute's name.
func (a *Attribute) Name() string { return a.name }

// objHeader returns the object header of an object, loading it when the
// object was created without one.
func objHeader(f *File, oh *h5o, addr haddr) (*h5o, error) {
	if oh != nil {
		return oh, nil
	}
	if f == nil {
		return nil, errorf(H5E_OHDR, H5E_BADVALUE, "no file for object")
	}
	return f.oProtect(addr)
}

// objInfo is H5O_get_info for an open object.
func objInfo(f *File, oh *h5o, addr haddr) (h5.ObjectInfo, error) {
	oh, err := objHeader(f, oh, addr)
	if err != nil {
		return h5.ObjectInfo{}, err
	}
	return f.oGetInfo(oh)
}

// objComment is H5G_loc_get_comment for an open object.
func objComment(f *File, oh *h5o, addr haddr) (string, error) {
	oh, err := objHeader(f, oh, addr)
	if err != nil {
		return "", err
	}
	s, _, err := f.oGetComment(oh)
	return s, err
}

// newAttribute wraps an attribute message.
func newAttribute(f *File, oh *h5o, attr *h5oAttr) *Attribute {
	return &Attribute{f: f, obj: oh, attr: attr, name: attr.name}
}

// objAttributes is H5Aiterate2 (H5_INDEX_NAME, H5_ITER_INC) for an open
// object.
func objAttributes(f *File, oh *h5o, addr haddr) iter.Seq2[*Attribute, error] {
	return func(yield func(*Attribute, error) bool) {
		oh, err := objHeader(f, oh, addr)
		if err != nil {
			yield(nil, err)
			return
		}
		err = f.aIterate(oh, func(attr *h5oAttr) error {
			if !yield(newAttribute(f, oh, attr), nil) {
				return errBIterStop
			}
			return nil
		})
		if err != nil && !errors.Is(err, errBIterStop) {
			yield(nil, err)
		}
	}
}

// objAttribute is H5Aopen for an open object.
func objAttribute(f *File, oh *h5o, addr haddr, name string) (*Attribute, error) {
	oh, err := objHeader(f, oh, addr)
	if err != nil {
		return nil, err
	}
	attr, err := f.aOpenByName(oh, name)
	if err != nil {
		return nil, err
	}
	return newAttribute(f, oh, attr), nil
}

// objNumAttributes is the attribute count of H5Oget_info for an open
// object.
func objNumAttributes(f *File, oh *h5o, addr haddr) (int, error) {
	oh, err := objHeader(f, oh, addr)
	if err != nil {
		return 0, err
	}
	n, err := f.aCount(oh)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// Path returns the path the dataset was opened by.
func (d *Dataset) Path() string { return d.path }

// Info returns the dataset's object information.
func (d *Dataset) Info() (h5.ObjectInfo, error) { return objInfo(d.f, d.oh, d.addr) }

// Attributes yields the dataset's attributes in name order.
func (d *Dataset) Attributes() iter.Seq2[*Attribute, error] {
	return objAttributes(d.f, d.oh, d.addr)
}

// Attribute returns the dataset's attribute called name.
func (d *Dataset) Attribute(name string) (*Attribute, error) {
	return objAttribute(d.f, d.oh, d.addr, name)
}

// NumAttributes returns the number of attributes of the dataset.
func (d *Dataset) NumAttributes() (int, error) { return objNumAttributes(d.f, d.oh, d.addr) }

// Comment returns the dataset's comment ("" when it has none).
func (d *Dataset) Comment() (string, error) { return objComment(d.f, d.oh, d.addr) }

// Path returns the path the datatype was opened by.
func (t *Datatype) Path() string { return t.path }

// Info returns the datatype's object information.
func (t *Datatype) Info() (h5.ObjectInfo, error) { return objInfo(t.f, t.oh, t.addr) }

// Attributes yields the datatype's attributes in name order.
func (t *Datatype) Attributes() iter.Seq2[*Attribute, error] {
	return objAttributes(t.f, t.oh, t.addr)
}

// Attribute returns the datatype's attribute called name.
func (t *Datatype) Attribute(name string) (*Attribute, error) {
	return objAttribute(t.f, t.oh, t.addr, name)
}

// NumAttributes returns the number of attributes of the datatype.
func (t *Datatype) NumAttributes() (int, error) { return objNumAttributes(t.f, t.oh, t.addr) }

// Comment returns the datatype's comment ("" when it has none).
func (t *Datatype) Comment() (string, error) { return objComment(t.f, t.oh, t.addr) }

// Datatype returns the committed datatype.
func (t *Datatype) Datatype() *h5.Datatype { return t.dt }

// oOpenName is H5O_open_name (src/H5Oint.c): locate the object name
// (relative to loc, following links) and check that it is an object of a
// known class.
func oOpenName(ctx *gTravCtx, loc *gLoc, name string) (*gLoc, error) {
	/* Find the object's location */
	objLoc, err := gLocFind(ctx, loc, name)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_NOTFOUND, "object not found")
	}

	/* Open the object */
	if _, err := objLoc.f.oObjClass(objLoc.addr); err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTOPENOBJ, "unable to open object")
	}
	return objLoc, nil
}

// openObjectAt opens the object at a location as a Group, Dataset or
// Datatype, according to its class (H5O_open_by_loc).
func openObjectAt(loc *gLoc) (Object, error) {
	oh, err := loc.f.oProtect(loc.addr)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTOPENOBJ, "unable to open object")
	}
	objClass, err := loc.f.oObjClassReal(oh)
	if err != nil {
		return nil, wrapf(err, H5E_OHDR, H5E_CANTOPENOBJ, "unable to open object")
	}
	switch objClass.typ {
	case h5.ObjectGroup:
		return &Group{f: loc.f, oh: oh, addr: loc.addr, path: loc.path}, nil
	case h5.ObjectDataset:
		return newDataset(loc.f, oh, loc.addr, loc.path)
	case h5.ObjectNamedDatatype:
		return newDatatype(loc, oh)
	}
	return nil, errorf(H5E_OHDR, H5E_BADTYPE, "unknown object type")
}

// newDataset opens the dataset whose header is oh (H5D_open): like
// H5D__open_oid, the datatype, dataspace, fill value, layout and filter
// messages are decoded now, so a dataset libhdf5 cannot open fails here.
func newDataset(f *File, oh *h5o, addr haddr, path string) (*Dataset, error) {
	d := &Dataset{f: f, oh: oh, addr: addr, path: path}
	if _, err := d.dataset(); err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "unable to open dataset")
	}
	return d, nil
}

// newDatatype opens the committed datatype whose header is oh
// (H5T_open): the datatype message is decoded.
func newDatatype(loc *gLoc, oh *h5o) (*Datatype, error) {
	native, err := loc.f.msgRead(oh, h5oDtypeID)
	if err != nil {
		return nil, wrapf(err, H5E_DATATYPE, H5E_CANTOPENOBJ, "unable to load type message from object header")
	}
	dt, ok := native.(*h5.Datatype)
	if !ok {
		return nil, errorf(H5E_DATATYPE, H5E_CANTOPENOBJ, "unexpected datatype message %T", native)
	}
	return &Datatype{f: loc.f, oh: oh, addr: loc.addr, path: loc.path, dt: dt}, nil
}

// openObjectName opens the object name relative to loc (H5Oopen),
// following links.
func openObjectName(loc *gLoc, name string) (Object, error) {
	objLoc, err := oOpenName(nil, loc, name)
	if err != nil {
		return nil, err
	}
	return openObjectAt(objLoc)
}

// openGroupName is H5G__open_name: open the group name relative to loc.
func openGroupName(loc *gLoc, name string) (*Group, error) {
	grpLoc, oh, err := gOpenName(loc, name)
	if err != nil {
		return nil, err
	}
	return &Group{f: grpLoc.f, oh: oh, addr: grpLoc.addr, path: grpLoc.path}, nil
}

// openDatasetName is H5D__open_name: open the dataset name relative to
// loc.
func openDatasetName(loc *gLoc, name string) (*Dataset, error) {
	/* Find the dataset object */
	dsetLoc, err := gLocFind(nil, loc, name)
	if err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_NOTFOUND, "not found")
	}

	/* Check that the object found is the correct type */
	objType, err := dsetLoc.f.oObjType(dsetLoc.addr)
	if err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTGET, "can't get object type")
	}
	if objType != h5.ObjectDataset {
		return nil, errorf(H5E_DATASET, H5E_BADTYPE, "not a dataset")
	}

	/* Open the dataset */
	oh, err := dsetLoc.f.oProtect(dsetLoc.addr)
	if err != nil {
		return nil, wrapf(err, H5E_DATASET, H5E_CANTOPENOBJ, "can't open dataset")
	}
	return newDataset(dsetLoc.f, oh, dsetLoc.addr, dsetLoc.path)
}

// openDatatypeName is H5T__open_name: open the committed datatype name
// relative to loc.
func openDatatypeName(loc *gLoc, name string) (*Datatype, error) {
	/* Find the named datatype object header and read the datatype message */
	typeLoc, err := gLocFind(nil, loc, name)
	if err != nil {
		return nil, wrapf(err, H5E_DATATYPE, H5E_NOTFOUND, "not found")
	}

	/* Check that the object found is the correct type */
	objType, err := typeLoc.f.oObjType(typeLoc.addr)
	if err != nil {
		return nil, wrapf(err, H5E_DATATYPE, H5E_CANTGET, "can't get object type")
	}
	if objType != h5.ObjectNamedDatatype {
		return nil, errorf(H5E_DATATYPE, H5E_BADTYPE, "not a named datatype")
	}

	/* Open it */
	oh, err := typeLoc.f.oProtect(typeLoc.addr)
	if err != nil {
		return nil, wrapf(err, H5E_DATATYPE, H5E_CANTOPENOBJ, "unable to open named datatype")
	}
	return newDatatype(typeLoc, oh)
}
