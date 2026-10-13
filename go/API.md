# Go API shared by `hdf5` (pure Go) and `chdf5` (cgo)

In this document `hdf5` is the pure Go reader, `github.com/crhntr/hdf5/go`,
and `chdf5` is the cgo wrapper around libhdf5, `github.com/crhntr/hdf5/cgo`.
Both are `package hdf5`; `chdf5` is the name the cgo package is imported as
where both are used (`chdf5 "github.com/crhntr/hdf5/cgo"`, as in
`cgo/conformance`). A program switches implementations by changing only the
import path.

Both packages expose the same reading API so that `conformance` can drive
them through one interface. Types from package `h5`
(`github.com/crhntr/hdf5/go/h5`) are the shared vocabulary. Everything
below is read-only; `chdf5` additionally offers a small writing API used to
generate test fixtures (see "Package-specific API").

```go
// Opening
func Open(name string, opts ...Option) (*File, error)   // read-only
type Option func(*options)                               // package-specific option set, see below
func (f *File) Close() error                             // also closes the external-link / VDS source files opened through f
func (f *File) Name() string
func (f *File) Superblock() h5.SuperblockInfo
func (f *File) Root() *Group
func (f *File) Open(path string) (Object, error)          // absolute path, follows soft/external links (like H5Oopen)
func (f *File) OpenGroup(path string) (*Group, error)
func (f *File) OpenDataset(path string) (*Dataset, error) // error if the object is not a dataset
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
func (g *Group) Walk() iter.Seq2[Visit, error]            // see "Walk" below
type Visit struct {
    Path   string    // absolute path
    Link   h5.Link
    Object Object    // nil for soft/external/user-defined/dangling links
}

// Datasets
func (d *Dataset) Datatype() *h5.Datatype                           // H5Dget_type
func (d *Dataset) Dataspace() h5.Dataspace                          // H5Dget_space (VDS: extent derived from the sources at open)
func (d *Dataset) Layout() (h5.Layout, error)                       // H5Pget_layout/chunk/external/virtual_*, H5Dget_offset
func (d *Dataset) Filters() ([]h5.Filter, error)                    // H5Pget_filter2, pipeline order; nil when none
func (d *Dataset) FillValue() (h5.FillValueInfo, error)             // H5Pget_fill_value/fill_time/alloc_time, H5Pfill_value_defined
func (d *Dataset) ReadValues() ([]any, error)                       // whole dataset as canonical values (h5/value.go), row-major
func (d *Dataset) ReadHyperslab(sel h5.Hyperslab) ([]any, error)    // selected elements, row-major; simple dataspaces only
func (d *Dataset) ReadInto(dst any) error                           // dst: pointer to a slice of a supported Go type (see Read)
func Read[T any](d *Dataset) ([]T, error)                          // ReadValues + h5.ConvertValues[T]

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

`ReadValues` returns one value per element, one value for a scalar
dataspace and an empty (non-nil) slice for a null dataspace or an empty
selection. All the methods of `hdf5.Dataset` are safe for concurrent use
(reads are serialized per dataset). As with H5Dopen, opening a dataset
(`Open`, `OpenDataset`, `Walk`) decodes its datatype, dataspace, layout,
fill value and filter messages, so a dataset libhdf5 cannot open fails
there.

## Supported Go types for `Read[T]` / `ReadInto`

The conversion from canonical values to `T` lives in `h5.ConvertValues[T]`
and `h5.ConvertInto` (h5/convert.go) so both packages behave identically:

| T                                   | from                                            |
|-------------------------------------|-------------------------------------------------|
| int8..int64, uint8..uint64          | any integer/enum/bitfield value (range checked) |
| float32, float64                    | any integer or float value                      |
| string                              | string values; []byte opaque as string; enum names ("" for a value that is not a member) |
| []byte                              | opaque / time; strings                          |
| bool                                | integer or enum value 0/1                       |
| struct                              | h5.Compound: fields matched by `hdf5:"name"` tag (`hdf5:"-"` skips), then by exact name, then case-insensitively |
| []E (slice element)                 | h5.Array / vlen sequence → nested slices        |
| h5.ObjectRef, h5.RegionRef, h5.AttrRef, h5.Enum, h5.Compound | identity         |
| any                                 | identity                                        |

## Walk

`Group.Walk` visits every link below the group: depth first, the links of
each group in name order, a group's links right after the link to it. A
group reached again through another hard link is reported (with its
Object) but not descended into, which cuts hard-link cycles (as h5ls -r).
Soft, external and user-defined links are reported with a nil Object and not
followed.

Error handling differs: `hdf5` ends the sequence at the first error (the
H5Lvisit2 model); `chdf5` reports an object that cannot be opened as a
Visit with that error and continues with the next link.

## Options

`hdf5.Open` (and `hdf5.OpenReader` for an `io.ReaderAt`) accepts:

| option                                | libhdf5 equivalent                         | default                       |
|---------------------------------------|--------------------------------------------|-------------------------------|
| `WithChecksumVerification(bool)`      | (always on in libhdf5)                     | on                            |
| `WithExternalLinks(bool)`             | (H5L traversal of external links)          | on                            |
| `WithExternalLinkPrefix(string)`      | H5Pset_elink_prefix                        | `$HDF5_EXT_PREFIX`            |
| `WithExternalFilePrefix(string)`      | H5Pset_efile_prefix                        | `$HDF5_EXTFILE_PREFIX`        |
| `WithVirtualPrefix(string)`           | H5Pset_virtual_prefix                      | `$HDF5_VDS_PREFIX`            |

`chdf5.Open` accepts `WithExternalLinks`, `WithExternalLinkPrefix`,
`WithExternalFilePrefix`, `WithVirtualPrefix`, plus
`WithDriver("family"|"multi"|"split")` and `WithFamilyMemberSize(size)`.
`hdf5` detects family and multi/split files from the superblock's driver
information instead.

Relative external data file (EFL) names are looked up in the current
directory only, unless a prefix is given (`WithExternalFilePrefix`,
`HDF5_EXTFILE_PREFIX`; `${ORIGIN}` at its start is the directory of the
HDF5 file), in both packages. As in H5D__build_file_prefix, a non-empty
`HDF5_EXTFILE_PREFIX` (`HDF5_VDS_PREFIX`) is used instead of the
`WithExternalFilePrefix` (`WithVirtualPrefix`) option. Note that libhdf5
reads `HDF5_EXTFILE_PREFIX` and `HDF5_VDS_PREFIX` once, when the library is
initialized, and `hdf5` when a dataset is opened.

Virtual dataset source files are located as H5F_prefix_open_file does: an
absolute name as is, then `HDF5_VDS_PREFIX`, the `WithVirtualPrefix` prefix
when `HDF5_VDS_PREFIX` is not set (`${ORIGIN}` at its start is the
directory of the virtual dataset's file),
the directory of the virtual dataset's file, the current directory. A
missing source file or dataset is not an error: its elements read as the
fill value. Source files stay open until the file is closed. The VDS view
and printf gap access properties are not exposed (libhdf5 defaults:
`H5D_VDS_LAST_AVAILABLE`, gap 0).

## Package-specific API

`hdf5` only:

- `OpenReader(r io.ReaderAt, size int64, opts ...Option)`: a file image.
- `Error` (with HDF5 major/minor classes), the sentinels `ErrNotHDF5`,
  `ErrNotFound`, `ErrUnsupported`, `ErrOverflow`, `ErrBadChecksum` (use
  `errors.Is`) and `IsNotFound(err)`.

`chdf5` only:

- `Close()` on every Object and Attribute (libhdf5 identifiers; objects are
  also released by a runtime cleanup), `ID()` on objects.
- `FilterAvailable(id h5.FilterID) bool` (H5Zfilter_avail).
- The writing API used to build fixtures: `Create(name, ...CreateOption)`
  with `WithLatestFormat`, `WithLibverBounds`, `WithUserblock`, `WithSizes`,
  `WithSymK`, `WithIstoreK`, `WithRootCreationOrder`,
  `WithFileSpaceStrategy`, `WithFileSpacePageSize`, `WithSharedMessages`,
  `WithoutTimes`;
  `(*File).CreateGroup`, `(*File).Flush`; `(*Group).CreateGroup` (with
  `WithCreationOrder`, `WithLinkPhaseChange`), `CreateSoftLink`,
  `CreateHardLink`, `CreateExternalLink`; `WriteDataset[T]` and
  `WriteAttribute[T]` (with `WithChunk`, `WithMaxDims`, `WithDeflate`,
  `WithShuffle`, `WithFletcher32`, `WithFillValue`, `WithCompact`,
  `WithContiguous`, `WithAllocTime`, `WithVariableLengthStrings`);
  `SetComment`.

## Canonical values

See h5/value.go. Both packages must produce identical `[]any` for the same
dataset/attribute, and identical `h5` descriptions: that is what
`conformance` compares with `assert.Equal`. Floating point NaN payloads are
compared by bit pattern. `h5.WriteCanonical` / `h5.Fingerprint` (h5/fingerprint.go)
encode these values and descriptions deterministically with the same
notion of equality; the golden file of the pure Go tests
(go/testdata/golden) stores such fingerprints.

Conventions both packages follow:

- `Dataspace.MaxDims` is nil when the dataspace message stores no maximum
  dimensions; `Dataspace.Version` is the message version.
- `Layout.Address` is `^uint64(0)` (HADDR_UNDEF) with `AddressDefined`
  false for unallocated contiguous storage; `Layout.Size` is then the size
  of the data (number of elements × datatype size), otherwise the
  allocated size.
- `Layout.Virtual` lists the mappings in order with the source file and
  dataset names as stored in the file (printf-style `%b` included) and the
  selections of the creation property list (as H5Pget_virtual_vspace /
  H5Pget_virtual_srcspace return them): virtual selections have the
  dataset's rank, source selections the rank they were stored with ("all"
  and "none" selections: 0).
- `Selection.Points` / `Blocks` are nil when empty. A hyperslab selection
  with an unlimited count or block has no block list: `Unlimited` holds the
  regular hyperslab (H5Sget_regular_hyperslab, `h5.Unlimited` entries,
  Stride and Block always set).
- `Filter.Name` is the name stored in the pipeline message, else the name of
  the library's filter class (`deflate`, `shuffle`, `fletcher32`, `szip`,
  `nbit`, `scaleoffset`), else `Unknown library filter` for an unknown id
  below 256 and `""` otherwise.
- `FillValueInfo.Value` is the raw user-defined fill value in the dataset's
  datatype, nil when there is none or when the datatype has variable-length
  or reference parts.
- Region references of the old (`H5T_STD_REF_DSETREG`) kind carry the rank
  of the referenced dataset.

### Fields the conformance suite ignores

These fields hold a value read from the file in `hdf5` but are not
available through the libhdf5 API, so `chdf5` leaves them 0:

| field                    | why                                                                  |
|--------------------------|----------------------------------------------------------------------|
| `Layout.Version`         | the layout message version has no getter (H5Pget_layout only reports the class) |
| `Layout.ChunkIndex`      | the chunk index type (H5D_chunk_index_t) is internal to libhdf5     |
| `FillValueInfo.Version`  | the fill value message version has no getter                         |

Known differences that are not field values (the suite must allow for
them):

| what                                  | hdf5                                    | chdf5                                  |
|---------------------------------------|-----------------------------------------|----------------------------------------|
| `Path()` of an object reached through an external link | the path in the target file (the traversal's location) | the path as given by the caller |
| `Path()` of an object from `OpenAddress` | `""`                                  | H5Iget_name (a path, or `""`)          |
| `Filter.Name` of an unnamed szip filter | `szip`                                | `Unknown library filter` when libhdf5 is built without szip |
| `Link.UserData` of a user-defined link whose class is not registered | the raw link value | nil (H5Lget_val returns no data) |
| family / multi / split files opened by a member name | detected from the superblock | need `WithDriver` |
| reading szip-compressed data          | `ErrUnsupported`                        | works when libhdf5 has szip            |
| VDS sources whose datatype differs from the virtual dataset's other than by byte order | different size: `ErrUnsupported`; same size: elements copied unconverted | converted by H5T_convert |
| VDS sources in another file with variable-length or reference elements | `ErrUnsupported` | read                       |
