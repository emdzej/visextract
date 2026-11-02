package vise

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdzej/visextract/internal/inflate"
	"github.com/emdzej/visextract/internal/source"
)

// compress makes a VISE stream: deflate, then the bytes of each 16-bit word swapped.
func compress(data []byte) []byte {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	w.Write(data)
	w.Close()
	return inflate.Swap(buf.Bytes())
}

func le32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }

// item appends one item frame: header, padding, the 0xff run, the name, and the body.
func item(b []byte, typ, id, parent uint32, name string, body []byte) []byte {
	b = le32(le32(le32(b, typ), id), parent)
	b = append(b, make([]byte, hdrToAnchor-12)...)
	b = append(b, bytes.Repeat([]byte{0xff}, anchorLen)...)
	b = append(b, 0, byte(len(name)), byte(len(name)>>8))
	b = append(b, name...)
	return append(b, body...)
}

func fileBody(size, csize, offset, crc uint32, ft uint64) []byte {
	b := make([]byte, fileBodySize)
	binary.LittleEndian.PutUint32(b[0x04:], size)
	binary.LittleEndian.PutUint32(b[0x08:], csize)
	binary.LittleEndian.PutUint32(b[0x0c:], 2)
	binary.LittleEndian.PutUint32(b[0x10:], offset)
	binary.LittleEndian.PutUint32(b[0x14:], 0x20)
	binary.LittleEndian.PutUint64(b[0x28:], ft)
	binary.LittleEndian.PutUint32(b[0x49:], crc)
	return b
}

// A synthetic archive: a stub "program", segment 1 with an item list (a folder, a file in it, a file at
// the top, an item of another type), and SETUP.002 with the compressed files.
func TestSynthetic(t *testing.T) {
	dir := t.TempDir()
	a1, a2 := []byte("hello from a VISE archive\n"), []byte("second file")
	c1, c2 := compress(a1), compress(a2)
	seg2 := le32(le32(le32(le32(nil, magic), 0x1234), 2), 0)
	off1 := uint32(len(seg2))
	seg2 = append(seg2, c1...)
	off2 := uint32(len(seg2))
	seg2 = append(seg2, c2...)

	ft := uint64(11644473600+1000000000) * 10000000 // 2001-09-09 01:46:40 UTC
	var body []byte
	body = append(body, 3, 'a', 'b', 'c', 0x40|2, 'x', 'y', 0) // three strings (the 2nd keeps 6 bits)
	body = append(body, 0, 0)                                  // no support files
	body = append(body, make([]byte, 40)...)                   // other installer data
	body = item(body, 1, 10, 0, "Data", make([]byte, 0x19))
	body = item(body, 2, 11, 10, "a.txt", fileBody(uint32(len(a1)), uint32(len(c1)), off1, crc32.ChecksumIEEE(a1), ft))
	body = item(body, 7, 12, 0, "Shortcut", make([]byte, 30))
	body = item(body, 2, 13, 0, "b.txt", fileBody(uint32(len(a2)), uint32(len(c2)), off2, crc32.ChecksumIEEE(a2), ft))
	body = append(body, make([]byte, 64)...)

	exe := append([]byte("MZ"), make([]byte, 0x200)...)
	seg1 := uint32(len(exe))
	exe = le32(le32(le32(le32(exe, magic), 0x1234), 1), uint32(16+len(body)))
	exe = append(exe, body...)
	exe = le32(le32(exe, magic), seg1)
	os.WriteFile(filepath.Join(dir, "SETUP.EXE"), exe, 0o644)
	os.WriteFile(filepath.Join(dir, "SETUP.002"), seg2, 0o644)
	os.WriteFile(filepath.Join(dir, "OTHER.EXE"), []byte("MZ not vise"), 0o644)

	src, err := source.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenAny(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Data", "Data/a.txt", "b.txt"}
	if len(a.Entries) != len(want) {
		t.Fatalf("%d entries, want %d", len(a.Entries), len(want))
	}
	for i, w := range want {
		if a.Entries[i].Path != w {
			t.Errorf("entry %d: %q, want %q", i, a.Entries[i].Path, w)
		}
	}
	for i, data := range [][]byte{a1, a2} {
		e := &a.Entries[i+1]
		got, err := a.Extract(e)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("%s: wrong contents", e.Path)
		}
		if !e.ModTime.Equal(time.Unix(1000000000, 0)) {
			t.Errorf("%s: time %v", e.Path, e.ModTime)
		}
	}
	a.Entries[1].CRC ^= 1
	if _, err := a.Extract(&a.Entries[1]); err == nil {
		t.Error("CRC mismatch not reported")
	}
}

func TestUnsafeName(t *testing.T) {
	for _, n := range []string{"..", ".", "a/b", `a\b`, "c:x", ""} {
		if safeName(n) {
			t.Errorf("%q accepted", n)
		}
	}
}

// A real archive: VISEXTRACT_TEST_SOURCE=<disc image or folder>; VISEXTRACT_TEST_SUMMARY=<the expected
// Summary line> is checked when set. Every file is extracted and checked against its CRC.
func TestReal(t *testing.T) {
	p := os.Getenv("VISEXTRACT_TEST_SOURCE")
	if p == "" {
		t.Skip("VISEXTRACT_TEST_SOURCE not set")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	a, err := OpenAny(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := os.Getenv("VISEXTRACT_TEST_SUMMARY"); want != "" && a.Summary() != want {
		t.Fatalf("summary %q, want %q", a.Summary(), want)
	}
	for i := range a.Entries {
		if e := &a.Entries[i]; !e.Dir {
			if _, err := a.Extract(e); err != nil {
				t.Error(err)
			}
		}
	}
}
