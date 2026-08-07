# goat

[![Go Reference](https://pkg.go.dev/badge/github.com/nmeilick/goat.svg)](https://pkg.go.dev/github.com/nmeilick/goat)
![Go 1.25+](https://img.shields.io/badge/go-1.25+-00ADD8?logo=go&logoColor=white)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

goat (the Go AST Transformer) moves functions, methods, types, constants and
variables between files of the same Go package, and fixes the imports on both
sides. It is built for one job: splitting a large Go file into smaller scoped
files — `file.go` becomes `file_access.go`, `file_compression.go`, and so on.

Every move is backed up before anything is written, verified to still compile
afterwards, and can be undone with `goat restore`.

## Install

```sh
go install github.com/nmeilick/goat@latest
```

Or build from source:

```sh
git clone https://github.com/nmeilick/goat.git
cd goat
make build    # produces ./bin/goat
```

Requires Go 1.25 or newer.

## Quick start

See what a file contains and what is safe to move:

```console
$ goat symbols file.go
SYMBOL           KIND  LINES  USED BY
FileStat         func     42  ChangeFileOwner
ChangeFileOwner  func     35
FileModify       func     28  used by: other.go
```

Preview a move as a diff, without touching anything:

```console
$ goat --dry-run move FileStat,ChangeFileOwner --from file.go --to file_access.go
```

Then apply it:

```console
$ goat move FileStat,ChangeFileOwner --from file.go --to file_access.go
moved 2 declarations: file.go → file_access.go (FileStat, ChangeFileOwner)
backup 20260807-012915-a1b2c3d4 (undo with 'goat restore')
```

The two functions land in `file_access.go` with the imports they need, and
leave `file.go` behind. Changed your mind:

```console
$ goat restore
```

## Commands

- `goat symbols <file.go>` — list a file's declarations with kind, size and
  who uses them. `--json` for machine-readable output. Alias: `ls`.
- `goat move <SYMBOL...> --from <file.go> --to <file.go>` — move symbols.
  The destination may be a new or existing file in the same directory.
- `goat restore [run-id]` — revert a previous run. `--list` shows available
  backups.

Useful variations:

```sh
goat move 'File*' '!FileModify' -f file.go -t file_access.go   # glob + exclusion
goat move ChangeFileOwner --with-deps -f file.go -t file_access.go
```

- `'File*'` moves every symbol starting with `File`; `'!FileModify'` excludes
  one. Quote these against shell expansion.
- `--with-deps` also moves symbols used *only* by the moving set,
  transitively. Anything used from another file or a test file stays put.
- Methods are named `Type.Method` (`File.Stat`), because unrelated types can
  share a method name.
- `--dry-run` (`-n`) is a root flag and goes before the command:
  `goat -n move ...`.

Run `goat <command> --help` for the full reference of each command.

## Safety

goat never silently breaks your code:

- The package must compile before a move is planned at all.
- Everything is computed in memory; the first write happens only after a
  backup of all touched files exists under
  `~/.local/state/goat/backups` (`$XDG_STATE_HOME/goat/backups`).
- Writes are atomic and preserve file modes and symlinks.
- After writing, goat re-compiles the result. If anything fails, it restores
  the backup automatically.
- `--dry-run` writes nothing and creates no backup.

## What goat will not do

- Move symbols across packages or directories — same package, same directory.
- Move a declaration out of a `const`/`var` group when the rest of the group
  depends on it (iota groups move whole or not at all).
- Move `init` functions.
- Read or write `_test.go` files, or files using cgo.
- Guess: anything ambiguous is refused with an explanation and exit code 1,
  before any file is touched.

Exit codes: `0` success, `1` failure (no files touched), `2` usage error.

## Development

```sh
make check   # fmt-check + vet + all tests
make test    # tests only
make build   # build bin/goat
make smoke   # build and run bin/goat version
```

Tests live next to the code (`cmd/`, `internal/goat/`), with golden
input/output trees under `internal/goat/testdata/`.

## License

[MIT](LICENSE)
