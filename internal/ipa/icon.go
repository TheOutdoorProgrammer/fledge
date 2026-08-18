package ipa

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"path"
	"strings"
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// extractIcon returns the largest app icon in the bundle as a standard PNG, or
// nil when there is nothing usable. A missing icon is never fatal: the install
// page falls back to a placeholder.
func extractIcon(archive *zip.Reader, bundle string, declared []string) []byte {
	entry := bestIconEntry(archive, bundle, declared)
	if entry == nil {
		return nil
	}

	raw, err := readMember(archive, entry.Name)
	if err != nil {
		return nil
	}

	decoded, err := decodeApplePNG(raw)
	if err != nil {
		return nil
	}

	return decoded
}

// bestIconEntry picks the highest-resolution icon in the bundle root. Xcode
// writes several scale variants and the uncompressed size orders them reliably
// enough without decoding every candidate.
func bestIconEntry(archive *zip.Reader, bundle string, declared []string) *zip.File {
	var best *zip.File
	for _, file := range archive.File {
		if path.Dir(file.Name) != bundle || !strings.HasSuffix(file.Name, ".png") {
			continue
		}
		if !iconNameMatches(path.Base(file.Name), declared) {
			continue
		}
		if best == nil || file.UncompressedSize64 > best.UncompressedSize64 {
			best = file
		}
	}
	return best
}

// iconNameMatches tests a bundle-root PNG against the declared icon base names,
// falling back to Xcode's AppIcon convention for apps that ship icons only in
// an asset catalog.
func iconNameMatches(name string, declared []string) bool {
	if len(declared) == 0 {
		return strings.HasPrefix(name, "AppIcon")
	}
	for _, base := range declared {
		if strings.HasPrefix(name, base) {
			return true
		}
	}
	return false
}

// decodeApplePNG converts the CgBI variant Xcode writes into a PNG a browser
// can render; standard PNGs pass through. CgBI differs three ways: raw deflate
// with no zlib wrapper, BGRA channel order, and premultiplied colour.
func decodeApplePNG(raw []byte) ([]byte, error) {
	chunks, err := splitPNGChunks(raw)
	if err != nil {
		return nil, err
	}
	if !hasChunk(chunks, "CgBI") {
		return raw, nil
	}

	rebuilt, err := rewrapChunks(chunks)
	if err != nil {
		return nil, err
	}

	decoded, err := png.Decode(bytes.NewReader(rebuilt))
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	if err := png.Encode(&out, unswizzle(decoded)); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

type pngChunk struct {
	kind string
	data []byte
}

func splitPNGChunks(raw []byte) ([]pngChunk, error) {
	if len(raw) < len(pngSignature) || !bytes.Equal(raw[:len(pngSignature)], pngSignature) {
		return nil, errors.New("not a png")
	}

	var chunks []pngChunk
	for offset := len(pngSignature); offset+8 <= len(raw); {
		length := int(binary.BigEndian.Uint32(raw[offset:]))
		kind := string(raw[offset+4 : offset+8])
		start := offset + 8
		if length < 0 || start+length+4 > len(raw) {
			return nil, errors.New("truncated png chunk")
		}
		chunks = append(chunks, pngChunk{kind: kind, data: raw[start : start+length]})
		if kind == "IEND" {
			break
		}
		offset = start + length + 4
	}

	return chunks, nil
}

func hasChunk(chunks []pngChunk, kind string) bool {
	for _, chunk := range chunks {
		if chunk.kind == kind {
			return true
		}
	}
	return false
}

// rewrapChunks drops the CgBI marker and re-deflates the concatenated IDAT
// payload inside a zlib container, which is all the stdlib decoder needs to
// take over the scanline unfiltering.
func rewrapChunks(chunks []pngChunk) ([]byte, error) {
	var compressed bytes.Buffer
	for _, chunk := range chunks {
		if chunk.kind == "IDAT" {
			compressed.Write(chunk.data)
		}
	}

	inflated, err := io.ReadAll(flate.NewReader(&compressed))
	if err != nil {
		return nil, err
	}

	var rewrapped bytes.Buffer
	writer := zlib.NewWriter(&rewrapped)
	if _, err := writer.Write(inflated); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.Write(pngSignature)
	wroteData := false
	for _, chunk := range chunks {
		switch chunk.kind {
		case "CgBI":
			continue
		case "IDAT":
			if wroteData {
				continue
			}
			wroteData = true
			writeChunk(&out, "IDAT", rewrapped.Bytes())
			continue
		}
		writeChunk(&out, chunk.kind, chunk.data)
	}

	return out.Bytes(), nil
}

func writeChunk(out *bytes.Buffer, kind string, data []byte) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	out.Write(header[:])

	digest := crc32.NewIEEE()
	out.WriteString(kind)
	digest.Write([]byte(kind))
	out.Write(data)
	digest.Write(data)

	binary.BigEndian.PutUint32(header[:], digest.Sum32())
	out.Write(header[:])
}

// unswizzle converts the decoded pixels from Apple's premultiplied BGRA back to
// straight RGBA. The stdlib decoded the bytes as RGBA, so the red and blue
// channels arrive transposed.
func unswizzle(src image.Image) image.Image {
	bounds := src.Bounds()
	out := image.NewNRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			alpha := uint8(a >> 8)
			offset := out.PixOffset(x, y)
			out.Pix[offset+0] = unpremultiply(uint8(b>>8), alpha)
			out.Pix[offset+1] = unpremultiply(uint8(g>>8), alpha)
			out.Pix[offset+2] = unpremultiply(uint8(r>>8), alpha)
			out.Pix[offset+3] = alpha
		}
	}

	return out
}

func unpremultiply(value, alpha uint8) uint8 {
	if alpha == 0 {
		return 0
	}
	scaled := int(value) * 255 / int(alpha)
	if scaled > 255 {
		return 255
	}
	return uint8(scaled)
}
