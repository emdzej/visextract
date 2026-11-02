# AGENTS.md

Guidance for coding agents working in this repository. Humans: see [README.md](README.md).

## What this is

visextract is a small Go CLI (MIT) that unpacks MindVision Installer VISE (Windows) archives: list,
extract, summary. It has no dependencies outside the Go standard library and builds as a static binary for
Linux, macOS and Windows (amd64, arm64). It came out of OpenOni (a reimplementation of the game Oni),
whose CD keeps the whole game in a VISE archive. OpenOni has its own C reader of the same format
(`src/vise.c`), and the two must agree (see Testing).

## Layout

| Path | What |
|---|---|
| `main.go` | The CLI: commands, flags, writing files. `version` is set by the release build |
| `internal/vise` | The archive: the setup program's trailer, segment 1, the item list, extraction with CRC checks, `Summary` |
| `internal/inflate` | The compression: byte swap + `compress/flate` |
| `internal/source` | Where the files come from: a host folder or an ISO 9660 image (plain/raw sectors, Joliet), case-insensitive names |
| `docs/format.md`, `docs/compression.md` | The format, with what is known, what is inferred and what is unknown |
| `.github/workflows/` | `ci.yml` (vet, test, gofmt on Linux/macOS/Windows), `release.yml` (builds and publishes on a tag) |

## Rules

- **Standard library only.** Keep the module free of dependencies.
- **Never guess silently.** A field or case the format docs mark unknown is reported as unsupported, not
  extracted on assumptions. Output is always CRC-checked; never write a file that failed its check.
- **Path safety.** Item names come from untrusted archives: keep `safeName` (no `/`, `\`, `:`, `..`) and
  write only below the output folder.
- **Docs follow the code.** A format change or finding goes into `docs/` in the same change: layouts with
  offsets, the evidence (an address in a setup program, a test on real data), and what is still unknown.
- No game or installer data in the repository (tests build their archives synthetically).

## Testing

```sh
gofmt -l .                 # must print nothing
go vet ./... && go test ./...
```

`TestSynthetic` builds a setup program and a segment in memory. For a real archive:

```sh
VISEXTRACT_TEST_SOURCE="$HOME/Downloads/Oni (USA).iso" \
VISEXTRACT_TEST_SUMMARY="files=95 bytes=641025194 hash=9b4bdbfa" go test -run TestReal -v ./internal/vise
```

`visextract summary <source>` must print the same line as OpenOni's `openoni-extract --summary` and
`tools/vise.py --summary` for the same source. A full extraction must be byte-identical to theirs
(`diff -r`).

## Releases

Plain semver tags, **no `v` prefix**. Bump `var version` in `main.go`, commit, then
`git tag 0.1.0 && git push origin 0.1.0`. `release.yml` refuses a tag that doesn't match `main.go`. It
builds the six targets (`CGO_ENABLED=0`, `-trimpath`, version from the tag), packs each with LICENSE and
README.md (`.tar.gz`; `.zip` for Windows), writes `.sha256` files and `SHA256SUMS`, smoke-tests the
Linux binary's `version`, and creates the GitHub Release. `workflow_dispatch` builds the same files as
artifacts only (version `<main.go version>-snapshot.<sha>`).

## Commits

Imperative subject, body explaining why when non-obvious.
