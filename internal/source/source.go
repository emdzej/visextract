// Package source gives read access to the files of an installer's disc: a host directory, or an ISO 9660
// image (2048-byte sectors, or raw 2352-byte MODE1/MODE2 sectors), without mounting it.
package source

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source is a set of files with case-insensitive, '/'-separated names.
type Source interface {
	// Open returns the file's contents for positional reads, and its size.
	Open(name string) (io.ReaderAt, int64, error)
	// List returns the names of the files (not directories) in a directory ("" = the top).
	List(dir string) ([]string, error)
	Describe() string
	Close() error
}

// Open opens a directory or a disc image.
func Open(path string) (Source, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return &dirSource{root: path}, nil
	}
	return openImage(path)
}

// ---- host directory ----

type dirSource struct{ root string }

func (d *dirSource) resolve(name string) (string, error) {
	cur := d.root
	for _, part := range strings.Split(strings.Trim(filepath.ToSlash(name), "/"), "/") {
		if part == "" {
			continue
		}
		ents, err := os.ReadDir(cur)
		if err != nil {
			return "", err
		}
		found := ""
		for _, e := range ents {
			if e.Name() == part {
				found = e.Name()
				break
			}
			if found == "" && strings.EqualFold(e.Name(), part) {
				found = e.Name()
			}
		}
		if found == "" {
			return "", fmt.Errorf("%s: %w", name, os.ErrNotExist)
		}
		cur = filepath.Join(cur, found)
	}
	return cur, nil
}

type fileReaderAt struct{ f *os.File }

func (r fileReaderAt) ReadAt(p []byte, off int64) (int, error) { return r.f.ReadAt(p, off) }

func (d *dirSource) Open(name string) (io.ReaderAt, int64, error) {
	p, err := d.resolve(name)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return fileReaderAt{f}, st.Size(), nil // closed with the process: a run opens a handful of files
}

func (d *dirSource) List(dir string) ([]string, error) {
	p, err := d.resolve(dir)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (d *dirSource) Describe() string { return d.root + " (folder)" }
func (d *dirSource) Close() error     { return nil }

// ---- ISO 9660 image ----

type isoEntry struct {
	name  string
	lba   uint32
	size  uint32
	isDir bool
}

type isoSource struct {
	path   string
	f      *os.File
	sector int64 // 2048 or 2352
	data   int64 // offset of the 2048 data bytes within a sector
	root   isoEntry
	joliet bool
}

func openImage(path string) (Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s := &isoSource{path: path, f: f}
	for _, layout := range [][2]int64{{2048, 0}, {2352, 16}, {2352, 24}} {
		s.sector, s.data = layout[0], layout[1]
		if ok, _ := s.readVolumes(); ok {
			return s, nil
		}
	}
	f.Close()
	return nil, fmt.Errorf("%s: not an ISO 9660 image", path)
}

// read reads len(p) bytes at off from the extent starting at logical block lba, skipping raw sector headers.
func (s *isoSource) read(lba uint32, off int64, p []byte) (int, error) {
	if s.sector == 2048 {
		return s.f.ReadAt(p, int64(lba)*2048+off)
	}
	done := 0
	for done < len(p) {
		pos := off + int64(done)
		sec, in := int64(lba)+pos/2048, pos%2048
		n := int(2048 - in)
		if n > len(p)-done {
			n = len(p) - done
		}
		m, err := s.f.ReadAt(p[done:done+n], sec*s.sector+s.data+in)
		done += m
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

func (s *isoSource) readVolumes() (bool, error) {
	var pvd, svd []byte
	for lba := uint32(16); lba < 64; lba++ {
		b := make([]byte, 2048)
		if _, err := s.read(lba, 0, b); err != nil {
			return false, err
		}
		if string(b[1:6]) != "CD001" {
			return false, nil
		}
		switch b[0] {
		case 1:
			pvd = b
		case 2:
			if b[88] == 0x25 && b[89] == 0x2f && (b[90] == 0x40 || b[90] == 0x43 || b[90] == 0x45) {
				svd = b // Joliet
			}
		}
		if b[0] == 255 {
			break
		}
	}
	vd := pvd
	if svd != nil {
		vd, s.joliet = svd, true
	}
	if vd == nil {
		return false, nil
	}
	r := vd[156:190]
	s.root = isoEntry{lba: binary.LittleEndian.Uint32(r[2:]), size: binary.LittleEndian.Uint32(r[10:]), isDir: true}
	return true, nil
}

func (s *isoSource) dir(e isoEntry) ([]isoEntry, error) {
	if e.size > 64<<20 {
		return nil, errors.New("directory too large")
	}
	b := make([]byte, e.size)
	if _, err := s.read(e.lba, 0, b); err != nil && err != io.EOF {
		return nil, err
	}
	var out []isoEntry
	for p := 0; p < len(b); {
		n := int(b[p])
		if n == 0 { // records don't cross sectors: the rest of this one is padding
			p = (p/2048 + 1) * 2048
			continue
		}
		if p+n > len(b) || n < 34 {
			break
		}
		r := b[p : p+n]
		fl := int(r[32])
		if 33+fl > n {
			break
		}
		id := r[33 : 33+fl]
		p += n
		if fl == 1 && (id[0] == 0 || id[0] == 1) {
			continue // "." and ".."
		}
		name := decodeName(id, s.joliet)
		out = append(out, isoEntry{
			name:  name,
			lba:   binary.LittleEndian.Uint32(r[2:]),
			size:  binary.LittleEndian.Uint32(r[10:]),
			isDir: r[25]&2 != 0,
		})
	}
	return out, nil
}

func decodeName(id []byte, joliet bool) string {
	var name string
	if joliet {
		var sb strings.Builder
		for i := 0; i+1 < len(id); i += 2 {
			sb.WriteRune(rune(binary.BigEndian.Uint16(id[i:])))
		}
		name = sb.String()
	} else {
		name = string(bytes.TrimRight(id, "\x00"))
	}
	if i := strings.LastIndexByte(name, ';'); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSuffix(name, ".")
}

func (s *isoSource) find(name string) (isoEntry, error) {
	cur := s.root
	for _, part := range strings.Split(strings.Trim(filepath.ToSlash(name), "/"), "/") {
		if part == "" {
			continue
		}
		ents, err := s.dir(cur)
		if err != nil {
			return isoEntry{}, err
		}
		ok := false
		for _, e := range ents {
			if strings.EqualFold(e.name, part) {
				cur, ok = e, true
				break
			}
		}
		if !ok {
			return isoEntry{}, fmt.Errorf("%s: %w", name, os.ErrNotExist)
		}
	}
	return cur, nil
}

type isoFile struct {
	s *isoSource
	e isoEntry
}

func (f isoFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(f.e.size) {
		return 0, io.EOF
	}
	short := false
	if rest := int64(f.e.size) - off; int64(len(p)) > rest {
		p, short = p[:rest], true
	}
	n, err := f.s.read(f.e.lba, off, p)
	if err == nil && short {
		err = io.EOF
	}
	return n, err
}

func (s *isoSource) Open(name string) (io.ReaderAt, int64, error) {
	e, err := s.find(name)
	if err != nil {
		return nil, 0, err
	}
	if e.isDir {
		return nil, 0, fmt.Errorf("%s: is a directory", name)
	}
	return isoFile{s, e}, int64(e.size), nil
}

func (s *isoSource) List(dir string) ([]string, error) {
	e, err := s.find(dir)
	if err != nil {
		return nil, err
	}
	ents, err := s.dir(e)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, x := range ents {
		if !x.isDir {
			out = append(out, x.name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *isoSource) Describe() string {
	kind := "ISO 9660"
	if s.joliet {
		kind += " + Joliet"
	}
	return fmt.Sprintf("%s (%s, %d-byte sectors)", s.path, kind, s.sector)
}

func (s *isoSource) Close() error { return s.f.Close() }
