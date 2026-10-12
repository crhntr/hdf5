package hdf5

// The public group API (see API.md), built on the group code translated
// from src/H5G*.c and the link code from src/H5L*.c.

import (
	"errors"
	"iter"

	"github.com/HDFGroup/hdf5/go/h5"
)

// Group is a group.
type Group struct {
	f    *File  // file holding the group (an external file when opened through an external link)
	oh   *h5o   // the group's object header (cached; must not be modified); nil if it could not be loaded by File.Root
	addr haddr  // address of the object header
	path string // path the group was opened by ("/" for the root group, "" when opened by address)
}

// Visit is one link reported by Group.Walk.
type Visit struct {
	Path   string  // absolute path of the link
	Link   h5.Link // the link
	Object Object  // the object a hard link points to; nil for soft, external and user-defined links
}

// loc returns the group's location.
func (g *Group) loc() *gLoc { return &gLoc{f: g.f, addr: g.addr, path: g.path} }

// header returns the group's object header.
func (g *Group) header() (*h5o, error) { return objHeader(g.f, g.oh, g.addr) }

// Path returns the path the group was opened by.
func (g *Group) Path() string { return g.path }

// Info returns the group's object information.
func (g *Group) Info() (h5.ObjectInfo, error) { return objInfo(g.f, g.oh, g.addr) }

// Attributes yields the group's attributes in name order.
func (g *Group) Attributes() iter.Seq2[*Attribute, error] {
	return objAttributes(g.f, g.oh, g.addr)
}

// Attribute returns the group's attribute called name.
func (g *Group) Attribute(name string) (*Attribute, error) {
	return objAttribute(g.f, g.oh, g.addr, name)
}

// NumAttributes returns the number of attributes of the group.
func (g *Group) NumAttributes() (int, error) { return objNumAttributes(g.f, g.oh, g.addr) }

// Comment returns the group's comment ("" when it has none).
func (g *Group) Comment() (string, error) { return objComment(g.f, g.oh, g.addr) }

// links yields the links of the group in the given index, in increasing
// order (H5Literate2 with H5_ITER_INC).
func (g *Group) links(idxType h5Index) iter.Seq2[h5.Link, error] {
	return func(yield func(h5.Link, error) bool) {
		oh, err := g.header()
		if err != nil {
			yield(h5.Link{}, err)
			return
		}
		err = g.f.gObjIterate(oh, idxType, h5IterInc, 0, nil, func(lnk *h5oLink) error {
			/* Retrieve the info for the link */
			info, err := gLinkToInfo(lnk)
			if err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get info for link")
			}
			if !yield(info, nil) {
				return errBIterStop
			}
			return nil
		})
		if err != nil && !errors.Is(err, errBIterStop) {
			yield(h5.Link{}, wrapf(err, H5E_SYM, H5E_BADITER, "error iterating over links"))
		}
	}
}

// Links yields the links of the group in increasing name order (strcmp
// order), like H5Literate2(H5_INDEX_NAME, H5_ITER_INC). A non-nil error
// ends the sequence.
func (g *Group) Links() iter.Seq2[h5.Link, error] { return g.links(h5IndexName) }

// LinksByCreationOrder yields the links of the group in increasing
// creation order, like H5Literate2(H5_INDEX_CRT_ORDER, H5_ITER_INC). It
// fails when the group does not track the creation order of its links.
func (g *Group) LinksByCreationOrder() iter.Seq2[h5.Link, error] {
	return g.links(h5IndexCrtOrder)
}

// NumLinks returns the number of links in the group (H5Gget_info).
func (g *Group) NumLinks() (int, error) {
	oh, err := g.header()
	if err != nil {
		return 0, err
	}
	info, err := g.f.gObjInfo(oh)
	if err != nil {
		return 0, err
	}
	return int(info.nlinks), nil
}

// Link returns the link name (relative to the group; intermediate links
// are followed but the last one is not), like H5Lget_info2.
func (g *Group) Link(name string) (h5.Link, error) {
	info, _, err := g.f.lGetInfo(g.loc(), name)
	return info, err
}

// Open opens the object name (relative to the group), following soft and
// external links, like H5Oopen.
func (g *Group) Open(name string) (Object, error) { return openObjectName(g.loc(), name) }

// OpenGroup opens the group name (relative to the group), like H5Gopen2.
func (g *Group) OpenGroup(name string) (*Group, error) { return openGroupName(g.loc(), name) }

// OpenDataset opens the dataset name (relative to the group), like
// H5Dopen2.
func (g *Group) OpenDataset(name string) (*Dataset, error) { return openDatasetName(g.loc(), name) }

// OpenDatatype opens the committed datatype name (relative to the group),
// like H5Topen2.
func (g *Group) OpenDatatype(name string) (*Datatype, error) {
	return openDatatypeName(g.loc(), name)
}

// Walk yields every link below the group, recursively: depth first, the
// links of each group in name order, each group's links right after the
// link to the group. Like H5Lvisit2 (and h5ls -r), a group reached again
// through another hard link is reported but not descended into again,
// which cuts hard-link cycles; soft, external and user-defined links are
// reported and not followed. A non-nil error ends the sequence.
func (g *Group) Walk() iter.Seq2[Visit, error] {
	return func(yield func(Visit, error) bool) {
		err := gVisit(g.loc(), ".", h5IndexName, h5IterInc, func(grpLoc *gLoc, path string, lnk *h5oLink) error {
			info, err := gLinkToInfo(lnk)
			if err != nil {
				return wrapf(err, H5E_SYM, H5E_CANTGET, "unable to get info for link")
			}
			v := Visit{Path: h5gNameSet(g.path, path), Link: info}
			if g.path == "" {
				v.Path = path
			}
			if lnk.typ == h5.LinkHard {
				objLoc, err := gLinkToLoc(grpLoc, lnk)
				if err != nil {
					return err
				}
				objLoc.path = v.Path
				obj, err := openObjectAt(objLoc)
				if err != nil {
					/* (an object of unknown class is reported without an Object, as
					 *  H5Lvisit2 does not open the objects) */
					if typ, terr := objLoc.f.oObjType(objLoc.addr); terr != nil || typ != h5.ObjectUnknown {
						return err
					}
					obj = nil
				}
				v.Object = obj
			}
			if !yield(v, nil) {
				return errBIterStop
			}
			return nil
		})
		if err != nil && !errors.Is(err, errBIterStop) {
			yield(Visit{}, err)
		}
	}
}
