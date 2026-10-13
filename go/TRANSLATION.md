# Translating HDF5 from C to Go

Two Go modules in this repository implement HDF5 reading:

| module / package                        | dir              | what                                                          | dependencies                |
|-----------------------------------------|------------------|---------------------------------------------------------------|-----------------------------|
| `github.com/crhntr/hdf5/go/h5`          | `go/h5`          | shared vocabulary: datatype/dataspace/link/layout/value types | stdlib                      |
| `github.com/crhntr/hdf5/go` (`hdf5`)    | `go`             | pure Go reader, translated from `src`                         | stdlib (+ testify in tests) |
| `github.com/crhntr/hdf5/cgo` (`chdf5`)  | `cgo`            | cgo wrapper around libhdf5 (1.14.x) exposing the same API     | libhdf5 via pkg-config      |
| `github.com/crhntr/hdf5/cgo/conformance`| `cgo/conformance`| tests that read every file with both and compare              | both                        |

`github.com/crhntr/hdf5/go` and `github.com/crhntr/hdf5/cgo` are both
`package hdf5`; where both are imported the cgo one is named `chdf5`, and
that is how this document refers to it. Paths below are relative to the
repository root.

## Rules for translated code (module `github.com/crhntr/hdf5/go`)

1. **One Go file per C file.** `src/H5Odtype.c` becomes `go/h5o_dtype.go`,
   `src/H5B2cache.c` becomes `go/h5b2_cache.go`, and so on. Put a header
   comment `// Translated from src/H5Odtype.c.` at the top. Read-only paths
   only: encode/serialize/insert/remove/free functions are not translated.
2. **One Go function per C function**, in the same order, with the C name in
   a doc comment: `// h5oDtypeDecodeHelper is H5O__dtype_decode_helper.`
   Names: drop the `H5X__` / `H5X_` prefix into a lower-case module prefix
   and camel-case the rest (`H5O__dtype_decode_helper` → `h5oDtypeDecodeHelper`,
   `H5HF__man_dblock_protect` → `h5hfManDblockProtect`). Types: `H5B2_hdr_t` →
   `h5b2Hdr`, `H5O_layout_t` → `h5oLayout`. Constants: `H5O_LAYOUT_VERSION_3` →
   `h5oLayoutVersion3`.
3. **Keep the C structure and comments.** Keep the block comments and the
   order of checks. `HGOTO_ERROR(H5E_X, H5E_Y, ..., "msg")` becomes
   `return ..., errorf(H5E_X, H5E_Y, "msg")` (or `wrapf(err, ...)` when an
   inner call failed); the `done:` label and `FUNC_ENTER/LEAVE` macros
   disappear. Keep every `H5_IS_BUFFER_OVERFLOW` check as `if p.overflow(n)`.
   Keep the version/flag validation; corrupt files must produce an error, never
   a panic. Where the C code has an `assert`, drop it, or keep the condition
   as an error check only when the C code goes on to read out of bounds or
   crash: the reference is a release build of libhdf5, in which `assert`
   (and `H5_CHECKED_ASSIGN`) checks nothing, so what the assertion would
   reject is accepted and hdf5 must accept it too (the SOHM table version
   in H5SM__cache_table_deserialize, a version 2 shared message of the SOHM
   type in H5O__shared_decode, the 32-bit single chunk size in
   H5O__layout_decode). Never panic.
4. **Decoding** uses the `image` cursor in `h5decode.go` (see the mapping
   table there). Raw data (dataset elements) is decoded with
   `encoding/binary` directly.
5. **Memory/cache.** `H5AC_protect` becomes `f.cached(kind, addr, load)` where
   `load` reads and decodes the structure; unprotect/flush/dirty flags vanish.
   `H5FL_*`/`H5MM_*` allocation is `&T{}` / `make`. Decoded structures are
   immutable once cached; do not mutate a cached value.
6. **Sizes.** `H5F_SIZEOF_ADDR(f)` is `f.sizeofAddr`, `H5F_SIZEOF_SIZE(f)` is
   `f.sizeofSize`; `haddr_t` is `haddr` (`uint64`), `HADDR_UNDEF` is
   `haddrUndef`, `hsize_t` is `uint64`. Check that any size read from the file
   that drives an allocation is bounded (by the file size `f.eof()` or a
   reasonable constant) before `make`.
7. **No globals, no init-time registration, no goroutines, no unsafe, no cgo,
   no third-party packages.** Standard library only (`compress/zlib` for
   deflate, `math`, `encoding/binary`, `sort`, `strings`, `iter`, ...).
8. **Tests** use `github.com/stretchr/testify`. Prefer tests that open files
   from the repository corpus (`corpusFiles(t)` in `corpus_test.go`, and the
   named files under `test/testfiles`, `tools/test/*/testfiles`, opened
   through `repoRoot(t)`, and the generated files in `go/testdata`) over
   synthetic byte arrays, and assert decoded values that can be checked with
   `h5dump` (the `.ddl` files next to the h5dump test files are its expected
   output).
9. **Go style on top.** Multiple return values instead of out-parameters,
   `[]byte` slices instead of pointer+len, `error` instead of `herr_t`,
   `bool` instead of `htri_t`, `iter.Seq`/`iter.Seq2` for iteration callbacks
   in the public API (internal iteration may keep the C callback style).
   `gofmt` and `go vet` must be clean.

## Shared contracts

The modules talk to each other through the signatures below. When a module
is not translated yet, a stub with that exact signature exists and returns
`ErrUnsupported`; the agent translating the module replaces the stub file.

See `go/h5o_types.go` for the in-core object header message structs
(`h5oStab`, `h5oLinfo`, `h5oLayout`, ...) and the object header API,
`go/h5b2.go`, `go/h5fa.go`, `go/h5ea.go`, `go/h5hf.go`, `go/h5hl.go`,
`go/h5hg.go`, `go/h5b.go`, `go/h5s.go`, `go/h5t.go`, `go/h5z.go` for the
module entry points.

Iteration sentinels (from `h5b.go`): an iterate operator returns
`errBIterStop` to stop early without error (the C `H5_ITER_STOP`), and a
`bFind` leaf callback returns `errBNotFound` to report that the child does
not hold the key (the C `*found = false`).

## C read paths that are not translated

Reading never converts between datatypes (values are decoded from the
file's datatype, `h5t_conv.go`), so the parts of the C read path that
exist for datatype conversion are not translated:

- `src/H5Dscatgath.c`: `H5D__scatgath_read` (and its `H5D__gather_file`,
  `H5D__scatter_mem`) is the `single_read` of a dataset only when the file
  and memory datatypes differ or a data transform is set
  (`H5D__dset_ioinfo_init`); without conversion the C library reads through
  `H5D__select_read`, as `h5dSelectIO` does. The vlen fill path of
  `H5D__fill` scatters converted values; here the fill value bytes are
  replicated in the file's form. `go/h5d_scatgath.go` was removed for that
  reason.
- The chunk cache (`H5D__chunk_lock`): a chunk is read and unfiltered for
  every access; a chunk that does not exist is filled through
  `h5dNonexistentRead`, which applies the checks of `H5D__fill_init` /
  `H5D__fill_refill_vl` that the cached path also applies.
- `H5S_select_construct_projection`, `H5D__create_piece_mem_map_1d` and the
  other shortcuts the C library takes when the memory and file selections
  have the same shape; the chunk mapping always projects the intersection
  (`h5dCreatePieceMemMap`). The selection operations those shortcuts use
  (`h5sSelectShapeSame`, `h5sSelectIsRegular`, `h5sSelectAdjustS`, ...) are
  translated and checked against libhdf5 in `h5s_test.go` and
  `coverage_selection_test.go`.

## Verification

`conformance` opens every HDF5 file in the repository with both packages
and compares: superblock info, the full link tree, object info, comments,
attribute names / datatypes / dataspaces / values, dataset datatypes /
dataspaces / layouts / filters / fill values / values (as `h5.Value`
trees, floats compared by bit pattern) and hyperslabs. libhdf5 is the
reference: files, objects and data the C library refuses (deliberately
corrupt test inputs, missing filters or external files) must fail in both,
and a file the C library opens must open. The only differences allowed are
the fields API.md lists as ignored and the entries of the allow-list in
`cgo/conformance/allow_test.go`, each with its justification. `TestFixtures`
compares files generated with `chdf5`'s writing API to cover the
combinations the corpus lacks. `TestMessageMutations` compares the two
packages on corrupted copies of latest-format files (object header
messages and virtual dataset mappings changed one byte at a time, checksums
recomputed), each in a child process since libhdf5 crashes on some; it
skips the variants on which libhdf5 crashes or reads past a buffer where
hdf5 reports the inconsistency. `TestFilePrefixes` compares the lookup of
external data files and virtual dataset sources under
`HDF5_EXTFILE_PREFIX` / `HDF5_VDS_PREFIX` (read by libhdf5 when it is
initialized, hence child processes). Run it with

    cd cgo && make conformance

(or `PKG_CONFIG_PATH=... go test -v ./conformance` in `cgo`); `make test`
in `cgo` runs it non-verbosely. A mismatch in the pure Go reader is fixed by
translating the C code path that libhdf5 takes, not by special-casing the
file.
