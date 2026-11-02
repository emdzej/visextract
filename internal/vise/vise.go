// Package vise reads MindVision Installer VISE (Windows) archives: a setup program whose PE overlay is
// segment 1 (the installer's own data and the item list), and segment files <stem>.002, <stem>.003, ...
// holding the compressed files. The format is described in docs/format.md.
package vise

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path"
	"strings"
	"time"

	"github.com/emdzej/visextract/internal/inflate"
	"github.com/emdzej/visextract/internal/source"
)

const (
	magic        = 0x56495345 // "ESIV"
	anchorLen    = 32         // 0xff bytes before every item's name
	hdrToAnchor  = 0x46       // item header (type, id, parent) to the 0xff run
	typeFolder   = 1
	typeFile     = 2
	fileBodySize = 0x4d // file fields after the name, up to and including the CRC
)

// Entry is a folder or a file of the archive.
type Entry struct {
	Path    string // '/'-separated, relative to the install folder
	Dir     bool
	Size    uint32 // uncompressed
	CSize   uint32 // compressed
	Segment uint32 // 2 = <stem>.002
	Offset  uint32 // in the segment file
	CRC     uint32 // CRC-32 (IEEE) of the uncompressed bytes
	ModTime time.Time
}

// Archive is an opened VISE archive.
type Archive struct {
	Setup   string // the setup program's name in the source
	Entries []Entry
	src     source.Source
	stem    string
}

// ErrNotVise means the file is not an Installer VISE setup program (or not one this reader knows).
var ErrNotVise = errors.New("not an Installer VISE setup program")

func u32(b []byte, p int) uint32 { return binary.LittleEndian.Uint32(b[p:]) }
func u16(b []byte, p int) uint16 { return binary.LittleEndian.Uint16(b[p:]) }

func readAll(src source.Source, name string) ([]byte, error) {
	r, size, err := src.Open(name)
	if err != nil {
		return nil, err
	}
	b := make([]byte, size)
	if _, err := r.ReadAt(b, 0); err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}

// IsSetup reports whether the file ends with the VISE trailer ("ESIV" + the offset of segment 1).
func IsSetup(src source.Source, name string) bool {
	r, size, err := src.Open(name)
	if err != nil || size < 8 {
		return false
	}
	var t [8]byte
	if _, err := r.ReadAt(t[:], size-8); err != nil && err != io.EOF {
		return false
	}
	return u32(t[:], 0) == magic
}

// OpenAny opens the first .exe at the top of the source that is a VISE setup program with an item list
// (a disc can carry other VISE-built programs: the Oni CD's AUTOPLAY.EXE has the trailer but no items).
// Names containing "setup" or "install" are tried first.
func OpenAny(src source.Source) (*Archive, error) {
	names, err := src.List("")
	if err != nil {
		return nil, err
	}
	var first, rest []string
	for _, n := range names {
		if !strings.EqualFold(path.Ext(n), ".exe") || !IsSetup(src, n) {
			continue
		}
		if l := strings.ToLower(n); strings.Contains(l, "setup") || strings.Contains(l, "install") {
			first = append(first, n)
		} else {
			rest = append(rest, n)
		}
	}
	var errs []string
	for _, n := range append(first, rest...) {
		a, err := Open(src, n)
		if err == nil {
			return a, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("%s: no Installer VISE setup program found", src.Describe())
	}
	return nil, fmt.Errorf("%s: no usable setup program (%s)", src.Describe(), strings.Join(errs, "; "))
}

// Open reads the item list from the setup program `setup` in src.
func Open(src source.Source, setup string) (*Archive, error) {
	d, err := readAll(src, setup)
	if err != nil {
		return nil, err
	}
	if len(d) < 8 || u32(d, len(d)-8) != magic {
		return nil, fmt.Errorf("%s: %w", setup, ErrNotVise)
	}
	seg := int(u32(d, len(d)-4))
	if seg+16 > len(d) || u32(d, seg) != magic || u32(d, seg+8) != 1 {
		return nil, fmt.Errorf("%s: %w (no segment 1)", setup, ErrNotVise)
	}
	hi := len(d)
	if end := seg + int(u32(d, seg+12)); end < hi {
		hi = end
	}
	// Segment 1: three length-prefixed strings (the second's length byte keeps 6 bits), then the setup
	// program's own support files, then the installer's data with the item list.
	p := seg + 16
	skip := func(n int) bool { p += n; return p < hi }
	if !skip(1+int(d[p])) || !skip(1+int(d[p]&0x3f)) || !skip(1+int(d[p])) || !skip(0) {
		return nil, fmt.Errorf("%s: segment 1 cut short", setup)
	}
	nsup := int(u16(d, p))
	p += 2
	for i := 0; i < nsup; i++ {
		if !skip(1+int(d[p])) || !skip(12) || p+4 > hi || !skip(4+int(u32(d, p))) {
			return nil, fmt.Errorf("%s: segment 1 cut short", setup)
		}
	}
	a := &Archive{Setup: setup, src: src, stem: strings.TrimSuffix(setup, path.Ext(setup))}
	if err := a.parseItems(d, p, hi); err != nil {
		return nil, fmt.Errorf("%s: %w", setup, err)
	}
	return a, nil
}

// safeName rejects names that could leave the output folder or aren't one path component.
func safeName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, "/\\:\x00")
}

// parseItems finds the items by their 32 x 0xff runs (the bodies have no common length) and reads each
// item's frame back from 0x46 bytes before the run. Parents come before their children.
func (a *Archive) parseItems(d []byte, lo, hi int) error {
	anchor := bytes.Repeat([]byte{0xff}, anchorLen)
	paths := map[uint32]string{0: ""}
	for p := lo + hdrToAnchor; p+anchorLen+3 < hi; {
		i := bytes.Index(d[p:hi], anchor)
		if i < 0 {
			break
		}
		p += i
		q := p
		for q < hi && d[q] == 0xff {
			q++
		}
		if q-p != anchorLen || q+3 > hi || d[q] != 0 || p-hdrToAnchor < lo {
			p = q
			continue
		}
		n := int(u16(d, q+1))
		name := q + 3
		if n == 0 || n >= 256 || name+n > hi || !printable(d[name:name+n]) {
			p = q
			continue
		}
		h := p - hdrToAnchor
		typ, id, parent := u32(d, h), u32(d, h+4), u32(d, h+8)
		p = name + n
		if typ != typeFolder && typ != typeFile {
			continue // shortcuts, registry entries, actions
		}
		nm := string(d[name : name+n])
		pp, ok := paths[parent]
		if !ok {
			return fmt.Errorf("item %q: unknown parent %d", nm, parent)
		}
		if !safeName(nm) {
			return fmt.Errorf("item %q: unsafe name", nm)
		}
		e := Entry{Path: path.Join(pp, nm)}
		if typ == typeFolder {
			e.Dir = true
			paths[id] = e.Path
		} else {
			b := name + n
			if b+fileBodySize > hi {
				return fmt.Errorf("%s: record cut short", e.Path)
			}
			e.Size, e.CSize = u32(d, b+0x04), u32(d, b+0x08)
			e.Segment, e.Offset = u32(d, b+0x0c), u32(d, b+0x10)
			ft := uint64(u32(d, b+0x28)) | uint64(u32(d, b+0x2c))<<32
			e.ModTime = filetime(ft)
			e.CRC = u32(d, b+0x49)
		}
		a.Entries = append(a.Entries, e)
	}
	if len(a.Entries) == 0 {
		return errors.New("no items in the archive")
	}
	return nil
}

func printable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

// filetime converts a Windows FILETIME (100 ns since 1601-01-01 UTC).
func filetime(ft uint64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	const epochDiff = 11644473600 // seconds from 1601 to 1970
	return time.Unix(int64(ft/10000000)-epochDiff, int64(ft%10000000)*100).UTC()
}

// SegmentName is the file holding segment n (n >= 2).
func (a *Archive) SegmentName(n uint32) string { return fmt.Sprintf("%s.%03d", a.stem, n) }

// Extract returns a file's bytes, checked against its size and CRC-32.
func (a *Archive) Extract(e *Entry) ([]byte, error) {
	if e.Dir {
		return nil, fmt.Errorf("%s: is a folder", e.Path)
	}
	if e.Size == 0 {
		return []byte{}, nil
	}
	if e.Segment < 2 {
		return nil, fmt.Errorf("%s: files stored in segment 1 are not supported", e.Path)
	}
	seg := a.SegmentName(e.Segment)
	r, size, err := a.src.Open(seg)
	if err != nil {
		return nil, fmt.Errorf("%s: segment %s: %w", e.Path, seg, err)
	}
	if int64(e.Offset)+int64(e.CSize) > size {
		return nil, fmt.Errorf("%s: past the end of %s (a file spanning segments is not supported)", e.Path, seg)
	}
	c := make([]byte, e.CSize)
	if _, err := r.ReadAt(c, int64(e.Offset)); err != nil && err != io.EOF {
		return nil, fmt.Errorf("%s: %w", e.Path, err)
	}
	out, err := inflate.Decode(c, int(e.Size))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.Path, err)
	}
	if crc32.ChecksumIEEE(out) != e.CRC {
		return nil, fmt.Errorf("%s: CRC mismatch", e.Path)
	}
	return out, nil
}

// Summary is one line over every file's path, size and CRC (in archive order), for comparing readers:
// files=<n> bytes=<total> hash=<CRC-32 of "path size crc\n" lines>.
func (a *Archive) Summary() string {
	var h uint32
	var total uint64
	files := 0
	for _, e := range a.Entries {
		if e.Dir {
			continue
		}
		h = crc32.Update(h, crc32.IEEETable, []byte(fmt.Sprintf("%s %d %08x\n", e.Path, e.Size, e.CRC)))
		total += uint64(e.Size)
		files++
	}
	return fmt.Sprintf("files=%d bytes=%d hash=%08x", files, total, h)
}
