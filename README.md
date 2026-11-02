# visextract

Unpacks **MindVision Installer VISE** (Windows) archives without running the installer: a single static
binary for Linux, macOS and Windows. Point it at the CD image, a folder with the installer's files, or
the setup program itself, and it writes out what the installer would have installed, with every file
checked against the archive's CRC-32.

Installer VISE was a common installer builder in the late 1990s and early 2000s, best known on the Mac
and also used on Windows. A Windows installer of this kind ships as a setup program (`SETUP.EXE`,
`ONISETUP.EXE`, ...) plus segment files named after it (`.002`, `.003`, ...). visextract helps when the
installer won't run on a modern system, or when you only want the files.

## Install

Download the archive for your system from the
[releases](https://github.com/emdzej/visextract/releases), or build from source (Go 1.22+):

```sh
go install github.com/emdzej/visextract@latest
```

## Use

```sh
visextract list    "Oni (USA).iso"            # folders and files: size, compressed size, CRC, date, path
visextract extract "Oni (USA).iso" oni        # everything into ./oni
visextract extract "Oni (USA).iso" oni GameDataFolder/IGMD readme.txt   # only these paths
visextract summary D:\                        # files=95 bytes=641025194 hash=9b4bdbfa
visextract version
```

- **Source**: an ISO 9660 image (plain or raw 2352-byte sectors, Joliet names when present), a folder (a
  mounted CD, or the files copied off it), or the setup program's path. The segment files must sit next to
  the setup program.
- **Setup program**: found automatically among the `.exe` files at the top of the source (names with
  "setup" or "install" first). Use `-setup NAME` to choose one.
- **extract** keeps the archive's folder tree and modification times. It refuses to overwrite existing
  files unless given `-f`, and writes each file as `<name>.part`, renamed only once it is complete.
- **summary** prints one line (file count, bytes, and a hash over paths, sizes and CRCs) for comparing
  archives or readers.

Exit status: 0 on success, 1 on an error (a damaged file, a missing segment), 2 for wrong usage.

## Limits

visextract was built against a real archive (the Oni CD, 2000) and reads what that format version holds.
It does not handle files stored in the setup program itself, files spanning several segment files
(multi-disc sets), or install-time choices (components, conditions, `%Variables%`): every file item is
extracted as it sits in the archive's tree. It reports these cases instead of guessing. Archives from
other VISE versions may not parse; issues with a description of the installer are welcome.

## Format

The archive layout and the compression are documented in [docs/format.md](docs/format.md) and
[docs/compression.md](docs/compression.md).

## License

MIT, see [LICENSE](LICENSE). Installer VISE is a product of MindVision Software; visextract is not
affiliated with them.
