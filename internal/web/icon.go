package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"
)

const placeholderSize = 512

var (
	placeholderOnce sync.Once
	placeholderPNG  []byte
)

// PlaceholderIcon returns a generic app tile for builds with no extractable
// icon, because the manifest requires both image assets.
func PlaceholderIcon() []byte {
	placeholderOnce.Do(func() {
		var buf bytes.Buffer
		if err := png.Encode(&buf, drawPlaceholder()); err != nil {
			placeholderPNG = nil
			return
		}
		placeholderPNG = buf.Bytes()
	})
	return placeholderPNG
}

// drawPlaceholder paints a rounded app tile with a purple-to-pink gradient,
// matching the palette the install pages use.
func drawPlaceholder() image.Image {
	const radius = placeholderSize * 0.22

	top := color.NRGBA{0xbd, 0x93, 0xf9, 0xff}
	bottom := color.NRGBA{0xff, 0x79, 0xc6, 0xff}
	tile := image.NewNRGBA(image.Rect(0, 0, placeholderSize, placeholderSize))

	for y := 0; y < placeholderSize; y++ {
		ratio := float64(y) / float64(placeholderSize-1)
		row := color.NRGBA{
			R: lerp(top.R, bottom.R, ratio),
			G: lerp(top.G, bottom.G, ratio),
			B: lerp(top.B, bottom.B, ratio),
			A: 0xff,
		}
		for x := 0; x < placeholderSize; x++ {
			row.A = cornerAlpha(x, y, radius)
			tile.SetNRGBA(x, y, row)
		}
	}

	return tile
}

// cornerAlpha antialiases the rounded corners by measuring how far a pixel sits
// outside the corner arc. A pixel in either middle band is inside the tile.
func cornerAlpha(x, y int, radius float64) uint8 {
	px, py := float64(x)+0.5, float64(y)+0.5

	cx, insideX := cornerCenter(px, radius)
	cy, insideY := cornerCenter(py, radius)
	if insideX || insideY {
		return 0xff
	}

	distance := math.Hypot(px-cx, py-cy)
	if distance <= radius-0.5 {
		return 0xff
	}
	if distance >= radius+0.5 {
		return 0x00
	}

	return uint8((radius + 0.5 - distance) * 255)
}

// cornerCenter returns the arc centre for one axis, and whether the coordinate
// falls in the straight middle band where no rounding applies.
func cornerCenter(value, radius float64) (float64, bool) {
	switch {
	case value < radius:
		return radius, false
	case value > placeholderSize-radius:
		return placeholderSize - radius, false
	default:
		return value, true
	}
}

func lerp(from, to uint8, ratio float64) uint8 {
	return uint8(float64(from) + (float64(to)-float64(from))*ratio)
}
