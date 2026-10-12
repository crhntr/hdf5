# Translating HDF5 from C to Go

This directory holds a Go module, `github.com/HDFGroup/hdf5/go`, with two
implementations of HDF5 reading:

| package      | what                                                        | dependencies            |
|--------------|-------------------------------------------------------------|-------------------------|
| `h5`         | shared vocabulary: datatype/dataspace/link/layout/value types | stdlib                  |
| `hdf5`       | pure Go reader, translated from `../src`                    | stdlib (+ testify in tests) |
| `chdf5`      | cgo wrapper around libhdf5 (1.14.x) exposing the same API   | libhdf5 via pkg-config  |
| `conformance`| tests that read every file with both and compare            | both                    |

## Rules for translated code (package `hdf5`)

1. **One Go file per C file.** `src/H5Odtype.c` becomes `hdf5/h5o_dtype.go`,
   `src/H5B2cache.c` becomes `hdf5/h5b2_cache.go`, and so on. Put a header
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
   a panic. Where the C code has an `assert`, either keep the condition as an
   error check or drop it; never panic.
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
   named files under `../test/testfiles`, `../tools/test/*/testfiles`) over
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

See `hdf5/h5o_types.go` for the in-core object header message structs
(`h5oStab`, `h5oLinfo`, `h5oLayout`, ...) and the object header API,
`hdf5/h5b2.go`, `hdf5/h5fa.go`, `hdf5/h5ea.go`, `hdf5/h5hf.go`,
`hdf5/h5hl.go`, `hdf5/h5hg.go`, `hdf5/h5b.go`, `hdf5/h5s.go`, `hdf5/h5t.go`,
`hdf5/h5z.go` for the module entry points.

Iteration sentinels (from `h5b.go`): an iterate operator returns
`errBIterStop` to stop early without error (the C `H5_ITER_STOP`), and a
`bFind` leaf callback returns `errBNotFound` to report that the child does
not hold the key (the C `*found = false`).

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
`conformance/allow_test.go`, each with its justification. `TestFixtures`
compares files generated with `chdf5`'s writing API to cover the
combinations the corpus lacks. Run it with

    make conformance

(or `PKG_CONFIG_PATH=... go test -v ./conformance`); `make test` runs it
non-verbosely. A mismatch in the pure Go reader is fixed by translating the
C code path that libhdf5 takes, not by special-casing the file.
