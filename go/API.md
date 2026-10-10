# Go API shared by `hdf5` (pure Go) and `chdf5` (cgo)

Both packages expose the same reading API so that `conformance` can drive
them through one interface. Types from package `h5` are the shared
vocabulary. Everything below is read-only; `chdf5` additionally offers a
small writing API used to generate test fixtures.

```go
// Opening
func Open(name string, opts ...Option) (*File, error)   // read-only
type Option func(*options)                               // package-specific option set, see below
func (f *File) Close() error
func (f *File) Name() string
func (f *File) Superblock() h5.SuperblockInfo
func (f *File) Root() *Group
func (f *File) Open(path string) (Object, error)          // absolute path, follows soft/external links (like H5Oopen)
func (f *File) OpenGroup(path string) (*Group, error)
func (f *File) OpenDataset(path string) (*Dataset, error)
func (f *File) OpenDatatype(path string) (*Datatype, error)
func (f *File) OpenAddress(addr uint64) (Object, error)   // object header at addr (dereferencing object references)

// Objects
type Object interface {
    Path() string                              // path the object was opened by ("/" for the root)
    Info() (h5.ObjectInfo, error)              // address, type, refcount, header version, nattrs, times
    Attributes() iter.Seq2[*Attribute, error]  // in name order (H5_INDEX_NAME, H5_ITER_INC); a non-nil error ends the sequence
    Attribute(name string) (*Attribute, error)
    NumAttributes() (int, error)
    Comment() (string, error)                  // "" when none
}
type Group struct{ ... }      // implements Object
type Dataset struct{ ... }    // implements Object
type Datatype struct{ ... }   // committed (named) datatype, implements Object

// Groups
func (g *Group) Links() iter.Seq2[h5.Link, error]        // name order (strcmp increasing), like H5Literate(H5_INDEX_NAME, H5_ITER_INC)
func (g *Group) LinksByCreationOrder() iter.Seq2[h5.Link, error] // error if creation order is not tracked
func (g *Group) NumLinks() (int, error)
func (g *Group) Link(name string) (h5.Link, error)         // does not follow the link
func (g *Group) Open(name string) (Object, error)         // relative path, follows links
func (g *Group) OpenGroup(name string) (*Group, error)
func (g *Group) OpenDataset(name string) (*Dataset, error)
func (g *Group) OpenDatatype(name string) (*Datatype, error)
func (g *Group) Walk() iter.Seq2[Visit, error]            // recursive, depth first, links in name order; hard-link
                                                          // cycles are cut by object address (like h5ls -r);
                                                          // soft/external links are reported as links and not followed
type Visit struct {
    Path   string    // absolute path
    Link   h5.Link
    Object Object    // nil for soft/external/dangling links
}

// Datasets
func (d *Dataset) Datatype() *h5.Datatype
func (d *Dataset) Dataspace() h5.Dataspace
func (d *Dataset) Layout() (h5.Layout, error)
func (d *Dataset) Filters() ([]h5.Filter, error)
func (d *Dataset) FillValue() (h5.FillValueInfo, error)
func (d *Dataset) ReadValues() ([]any, error)                       // whole dataset as canonical values (h5/value.go), row-major
func (d *Dataset) ReadHyperslab(sel h5.Hyperslab) ([]any, error)    // selected elements, row-major
func (d *Dataset) ReadInto(dst any) error                           // dst: pointer to a slice of a supported Go type (see Read)
func Read[T any](d *Dataset) ([]T, error)                          // ReadInto(&[]T)

// Attributes
func (a *Attribute) Name() string
func (a *Attribute) Datatype() *h5.Datatype
func (a *Attribute) Dataspace() h5.Dataspace
func (a *Attribute) ReadValues() ([]any, error)
func (a *Attribute) ReadInto(dst any) error
func ReadAttribute[T any](a *Attribute) ([]T, error)

// Committed datatypes
func (t *Datatype) Datatype() *h5.Datatype
```

## Supported Go types for `Read[T]` / `ReadInto`

The conversion from canonical values to `T` lives in `h5.ConvertValues[T]`
(h5/convert.go) so both packages behave identically:

| T                                   | from                                            |
|-------------------------------------|-------------------------------------------------|
| int8..int64, uint8..uint64          | any integer/enum/bitfield value (range checked) |
| float32, float64                    | any integer or float value                      |
| string                              | string values; []byte opaque as string          |
| []byte                              | opaque / time                                   |
| bool                                | integer 0/1                                     |
| struct                              | h5.Compound: fields matched by `hdf5:"name"` tag, then by exact name, then case-insensitively |
| []E (slice element)                 | h5.Array / vlen sequence → nested slices        |
| h5.ObjectRef, h5.RegionRef, h5.AttrRef, h5.Enum, h5.Compound | identity         |
| any                                 | identity                                        |

## Options

`hdf5.Open` accepts `WithChecksumVerification`, `WithExternalLinks`,
`WithExternalLinkPrefix`, `WithExternalFilePrefix`, `WithVirtualPrefix`.
`chdf5.Open` accepts the same names where libhdf5 has an equivalent property
(H5Pset_elink_prefix, H5Pset_efile_prefix, H5Pset_virtual_prefix) and a
`WithDriver("family"|"multi"|"split")` option.

## Canonical values

See h5/value.go. Both packages must produce identical `[]any` for the same
dataset/attribute: that is what `conformance` compares with
`assert.Equal`. Floating point NaN payloads are compared by bit pattern.
