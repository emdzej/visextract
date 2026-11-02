# Installer VISE archive format

This describes MindVision **Installer VISE for Windows** archives as far as visextract reads them. It
was worked out from one real archive, the Oni CD (Bungie West / Gathering of Developers, 2000, volume
`001228_1801`), and from that disc's setup program. Every file record carries a CRC-32, and all 95 files
of that archive decompress to it. Other VISE versions may differ: fields marked *unknown* were not needed,
and the reader rejects what it hasn't seen rather than guessing.

All numbers are little-endian. Addresses in parentheses are code in the Oni CD's `ONISETUP.EXE`
(image base 0x400000), for anyone who wants to check.

The compression is described separately in [compression.md](compression.md).

## Files

An archive is split into **segments**:

| File | Segment | Contents |
|---|---|---|
| `SETUP.EXE` (any name) | 1 | A small Win32 setup program. Its PE overlay is segment 1: the installer engine and its data, including the item list |
| `SETUP.002`, `SETUP.003`, ... | 2, 3, ... | The compressed files, back to back |

The segment files are named after the setup program: same stem, extension `.002`, `.003` and so on.

## Finding segment 1

The setup program ends with an 8-byte trailer:

| Offset from the end | Size | Field |
|---|---|---|
| -8 | 4 | `"ESIV"` (0x56495345) |
| -4 | 4 | file offset of segment 1 |

The program reads its own file this way at start-up (0x401a80: seek to the end minus 8, check the magic,
read the offset). Other programs built with VISE can carry the trailer too: on the Oni CD, `AUTOPLAY.EXE`
has one but no item list. visextract tries each candidate and keeps the first one that parses.

## Segment header

Every segment starts with 16 bytes:

| Offset | Size | Field |
|---|---|---|
| 0x00 | 4 | `"ESIV"` |
| 0x04 | 4 | archive id, the same in every segment of an archive (Oni: `0x3a07bc24`) |
| 0x08 | 4 | segment number (1, 2, ...) |
| 0x0c | 4 | in segment 1: its length in bytes, header included. In segment 2: *unknown* (not its length) |

File offsets in the item list are offsets in the segment **file**, header included: the first file in
`.002` starts at 0x10.

## Segment 1

After the header, in order:

1. **Three strings**, each a length byte followed by the bytes. Only the low 6 bits of the second string's
   length byte count (`& 0x3f`, 0x401040). Their meaning is *unknown*.
2. **Support files** the setup program unpacks for itself into a temporary folder before it loads the
   engine (0x4016a0). A u16 count, then for each file:

   | Size | Field |
   |---|---|
   | 1 + n | name (length byte + bytes) |
   | 2 | DOS date |
   | 2 | DOS time |
   | 4 | *unknown* (0) |
   | 4 | *unknown* (0) |
   | 4 | compressed size |
   | csize | compressed data ([compression.md](compression.md)) |

   On the Oni CD: `vise32ex.dll` (the installer engine: the setup program loads it and calls its export
   `ViseMain`), `English.vlg` (UI strings, an INI-style text file), `dsetup.dll`, `jpeg.dll`. The first
   file is the one that gets loaded.

3. **The installer's data**: dialogs, strings, variables, registry entries, shortcuts, actions, and
   the item list. visextract only reads the item list, so the rest is *unknown* here.

## Items

Folders, files and everything else the installer creates are items. Each item has the same frame:

| Offset | Size | Field |
|---|---|---|
| 0x00 | 4 | type: 1 folder, 2 file. Other values (shortcuts, registry entries, actions, ...) are skipped |
| 0x04 | 4 | item id |
| 0x08 | 4 | parent item id, 0 = the install folder itself |
| 0x0c | 0x3a | *unknown* |
| 0x46 | 32 | 0xff bytes |
| 0x66 | 1 | 0 |
| 0x67 | 2 | name length n |
| 0x69 | n | name (one path component; ANSI) |
| 0x69 + n | | body, which depends on the type |

The bodies have different lengths for different types, and visextract doesn't know them all. So it finds
items by their run of exactly 32 0xff bytes followed by a 0, a length and a printable name, then reads the
frame back from 0x46 bytes before the run. Parents come before their children in the list. Item ids are
not consecutive (the installer seems to number items as they are added to the project), so paths are
built from the parent ids only, never from the order.

### Folder body

0x19 bytes after the name. Nothing in it is needed to extract.

### File body

| Offset (after the name) | Size | Field |
|---|---|---|
| 0x00 | 4 | 0 |
| 0x04 | 4 | uncompressed size |
| 0x08 | 4 | compressed size |
| 0x0c | 4 | segment number (2 = `.002`) |
| 0x10 | 4 | offset in the segment file |
| 0x14 | 4 | Windows file attributes (Oni: 0x21, read-only + archive) |
| 0x18 | 8 | FILETIME, probably the creation time |
| 0x20 | 8 | FILETIME, *unknown* |
| 0x28 | 8 | FILETIME, last write time (UTC). For `readme.txt` it equals the file's date on the CD |
| 0x30 | 2 | DOS date of the same moment, local time |
| 0x32 | 2 | DOS time |
| 0x34 | 0x15 | *unknown* |
| 0x49 | 4 | CRC-32 (IEEE, as zlib's `crc32`) of the uncompressed file. Not aligned |

A FILETIME counts 100-ns intervals since 1601-01-01 00:00 UTC.

## What visextract doesn't handle

- **Files in segment 1.** The format allows them (segment number 1), but the offsets' base is *unknown*.
  None occur on the Oni CD.
- **Files spanning segments.** Multi-disc archives probably split large files across segment files.
  visextract refuses a file whose data runs past the end of its segment.
- **Install-time choices**: components, languages, conditions, `%Variables%` in paths. visextract
  extracts every file item as it sits in the tree. Item names on the Oni CD are plain; a name containing a
  variable would be written literally.
- **Names with `/`, `\`, `:` or `..`** are rejected, so an archive can't write outside the output folder.

## The Oni CD, for reference

```
visextract summary "Oni (USA).iso"
files=95 bytes=641025194 hash=9b4bdbfa
```

The hash is the CRC-32 of the lines `"<path> <size> <crc as 8 hex digits>\n"` over every file in archive
order. It is a quick way to check that another reader sees the same table.
