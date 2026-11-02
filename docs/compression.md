# VISE compression

Every compressed file in an Installer VISE archive, including the setup program's own support files, is
one stream: **plain DEFLATE** ([RFC 1951](https://www.rfc-editor.org/rfc/rfc1951)), stored in 16-bit
little-endian words **whose two bytes are swapped**.

To decode, swap every pair of bytes (pad a stream of odd length with one zero byte first) and inflate the
result as raw deflate: no zlib or gzip header, no trailer. visextract does exactly that with Go's
`compress/flate`. In Python:

```python
b = bytearray(data) + (b"\0" if len(data) & 1 else b"")
b[0::2], b[1::2] = b[1::2], b[0::2]
out = zlib.decompressobj(-15).decompress(bytes(b))
```

The item's uncompressed size bounds the output, and the CRC-32 in the item record checks it.

## Evidence

The decoder lives in the setup program (Oni CD's `ONISETUP.EXE`, image base 0x400000): a function with
four modes (0x401ee0: 1 init, 2 compress, 3 decompress, 4 free). Its compressor half carries zlib's debug
strings (`FlushBlock`, `Static trees will be used`).

- **The swap.** The bit reader takes the input as 16-bit words, exchanges each word's bytes (0x4037e0),
  and feeds the result LSB-first into the bit buffer, as RFC 1951 does. In effect the bytes are consumed
  in the order 1, 0, 3, 2, 5, 4, ...
- **A standard window.** The decoder's distance table ends at base 24577 (code 29) and its length table at
  258 (code 285), as in RFC 1951. The 64 KB buffers the decoder allocates are input and output buffers,
  not a larger window.
- **The data.** Swapped and inflated with Python's `zlib`, all 95 files and the 4 support files of the Oni
  CD decode, and their CRC-32s match.

## Block types

The setup program's block dispatcher (0x404d70) handles type 2 (dynamic Huffman codes) and type 0
(stored, 0x404b80). It returns an error for type 1 (fixed codes). The Oni CD's streams use dynamic blocks
only. visextract accepts all three types as a standard decoder does: after the swap, a stored block is
byte-aligned in the swapped stream. Whether MindVision's encoder writes stored blocks that way has not
been seen on real data. A mismatch would show up as a CRC error, never as silently wrong output.
