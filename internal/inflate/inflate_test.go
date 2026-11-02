package inflate

import (
	"bytes"
	"compress/flate"
	"math/rand"
	"testing"
)

func vise(t *testing.T, data []byte, level int) []byte {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, level)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	w.Close()
	return Swap(buf.Bytes())
}

func TestDecode(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	random := make([]byte, 100000)
	rng.Read(random)
	text := bytes.Repeat([]byte("Installer VISE stores its files as deflate in swapped 16-bit words. "), 3000)
	for _, tc := range []struct {
		name  string
		data  []byte
		level int
	}{
		{"text", text, flate.BestCompression},
		{"random (stored blocks)", random, flate.NoCompression},
		{"odd length", text[:12345], flate.DefaultCompression},
		{"empty", nil, flate.DefaultCompression},
	} {
		src := vise(t, tc.data, tc.level)
		got, err := Decode(src, len(tc.data))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !bytes.Equal(got, tc.data) {
			t.Fatalf("%s: output differs", tc.name)
		}
		if len(tc.data) > 0 {
			if _, err := Decode(src, len(tc.data)+1); err == nil {
				t.Fatalf("%s: short output accepted", tc.name)
			}
			if _, err := Decode(src, len(tc.data)-1); err == nil {
				t.Fatalf("%s: long output accepted", tc.name)
			}
		}
	}
	if _, err := Decode(vise(t, text, 9)[:100], len(text)); err == nil {
		t.Fatal("truncated stream accepted")
	}
}

func TestSwap(t *testing.T) {
	if got := Swap([]byte{1, 2, 3}); !bytes.Equal(got, []byte{2, 1, 0, 3}) {
		t.Fatalf("Swap = %v", got)
	}
}
