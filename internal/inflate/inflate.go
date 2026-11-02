// Package inflate decodes the compressed streams of MindVision Installer VISE archives: plain DEFLATE
// (RFC 1951), read in 16-bit little-endian words whose two bytes are swapped. See docs/compression.md.
package inflate

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
)

// ErrCorrupt is returned for a stream that isn't valid, or that doesn't decode to the expected size.
var ErrCorrupt = errors.New("inflate: corrupt or truncated stream")

// Swap returns src with the two bytes of every 16-bit word exchanged (a short last word gets a 0).
// It is its own inverse.
func Swap(src []byte) []byte {
	b := make([]byte, len(src)+len(src)&1)
	copy(b, src)
	for i := 0; i < len(b); i += 2 {
		b[i], b[i+1] = b[i+1], b[i]
	}
	return b
}

// Decode decompresses one VISE stream. size is the expected uncompressed size: more or less is an error.
func Decode(src []byte, size int) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(Swap(src)))
	defer r.Close()
	out := make([]byte, size)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, ErrCorrupt
	}
	var extra [1]byte
	if n, err := r.Read(extra[:]); n != 0 || (err != nil && err != io.EOF) {
		return nil, ErrCorrupt
	}
	return out, nil
}
