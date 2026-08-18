// Command generate renders Fledge's app icon at every browser-facing size.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const (
	masterSize  = 1024
	supersample = 4
)

var outputs = []struct {
	name string
	size int
}{
	{"icon-1024.png", 1024},
	{"icon-512.png", 512},
	{"icon-180.png", 180},
	{"favicon-32.png", 32},
	{"favicon-16.png", 16},
}

var palette = struct {
	bg, surface, line, fg, cyan, orange, purple color.NRGBA
}{
	bg:      hex(0x282a36),
	surface: hex(0x21222c),
	line:    hex(0x44475a),
	fg:      hex(0xf8f8f2),
	cyan:    hex(0x8be9fd),
	orange:  hex(0xffb86c),
	purple:  hex(0xbd93f9),
}

type point struct{ x, y float64 }

func main() {
	out := flag.String("out", "internal/web/assets", "output directory")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}

	master := downsample(render(masterSize*supersample), masterSize)
	for _, output := range outputs {
		icon := master
		if output.size != masterSize {
			icon = downsample(master, output.size)
		}

		path := filepath.Join(*out, output.name)
		file, err := os.Create(path)
		if err != nil {
			fatal(err)
		}
		if err := png.Encode(file, icon); err != nil {
			_ = file.Close()
			fatal(err)
		}
		if err := file.Close(); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote %s\n", path)
	}
}

func render(size int) *image.NRGBA {
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	radius := float64(size) * 0.22

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !insideRoundedSquare(x, y, size, radius) {
				continue
			}

			t := float64(y) / float64(size-1)
			base := mix(palette.surface, palette.bg, t)
			glow := math.Max(0, 1-math.Hypot(float64(x)/float64(size)-0.22, float64(y)/float64(size)-0.12)/0.72)
			canvas.SetNRGBA(x, y, mix(base, palette.line, glow*0.20))
		}
	}

	// The nest is deliberately simple enough to survive at favicon scale.
	drawCurve(canvas, size, []point{{0.25, 0.70}, {0.38, 0.77}, {0.62, 0.77}, {0.75, 0.70}}, 0.035, palette.orange)
	drawCurve(canvas, size, []point{{0.29, 0.77}, {0.41, 0.84}, {0.59, 0.84}, {0.71, 0.77}}, 0.033, palette.orange)

	leftWing := bezierShape(
		[]point{{0.49, 0.62}, {0.40, 0.53}, {0.27, 0.28}, {0.14, 0.25}},
		[]point{{0.14, 0.25}, {0.13, 0.40}, {0.22, 0.56}, {0.43, 0.68}},
		[]point{{0.43, 0.68}, {0.46, 0.68}, {0.48, 0.65}, {0.49, 0.62}},
	)
	rightWing := bezierShape(
		[]point{{0.51, 0.62}, {0.60, 0.51}, {0.73, 0.24}, {0.86, 0.20}},
		[]point{{0.86, 0.20}, {0.87, 0.38}, {0.78, 0.56}, {0.57, 0.68}},
		[]point{{0.57, 0.68}, {0.54, 0.68}, {0.52, 0.65}, {0.51, 0.62}},
	)
	drawPolygon(canvas, size, leftWing, palette.cyan)
	drawPolygon(canvas, size, rightWing, palette.purple)

	// A small bright breast/head joins the two wings without turning the mark
	// into a detailed bird illustration.
	drawCircle(canvas, size, point{0.50, 0.59}, 0.066, palette.fg)
	drawCircle(canvas, size, point{0.55, 0.52}, 0.048, palette.fg)
	drawPolygon(canvas, size, []point{{0.58, 0.50}, {0.65, 0.53}, {0.58, 0.55}}, palette.orange)

	return canvas
}

func bezierShape(curves ...[]point) []point {
	var shape []point
	for _, curve := range curves {
		for step := 0; step <= 24; step++ {
			t := float64(step) / 24
			shape = append(shape, cubic(curve[0], curve[1], curve[2], curve[3], t))
		}
	}
	return shape
}

func cubic(a, b, c, d point, t float64) point {
	u := 1 - t
	return point{
		x: u*u*u*a.x + 3*u*u*t*b.x + 3*u*t*t*c.x + t*t*t*d.x,
		y: u*u*u*a.y + 3*u*u*t*b.y + 3*u*t*t*c.y + t*t*t*d.y,
	}
}

func drawCurve(dst *image.NRGBA, size int, controls []point, width float64, fill color.NRGBA) {
	previous := controls[0]
	for step := 1; step <= 40; step++ {
		t := float64(step) / 40
		current := cubic(controls[0], controls[1], controls[2], controls[3], t)
		drawLine(dst, size, previous, current, width, fill)
		previous = current
	}
}

func drawLine(dst *image.NRGBA, size int, a, b point, width float64, fill color.NRGBA) {
	ax, ay := a.x*float64(size), a.y*float64(size)
	bx, by := b.x*float64(size), b.y*float64(size)
	radius := width * float64(size) / 2
	minX := max(0, int(math.Floor(math.Min(ax, bx)-radius)))
	maxX := min(size-1, int(math.Ceil(math.Max(ax, bx)+radius)))
	minY := max(0, int(math.Floor(math.Min(ay, by)-radius)))
	maxY := min(size-1, int(math.Ceil(math.Max(ay, by)+radius)))

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if segmentDistance(float64(x)+0.5, float64(y)+0.5, ax, ay, bx, by) <= radius {
				dst.SetNRGBA(x, y, fill)
			}
		}
	}
}

func segmentDistance(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	if dx == 0 && dy == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func drawCircle(dst *image.NRGBA, size int, center point, radius float64, fill color.NRGBA) {
	cx, cy, r := center.x*float64(size), center.y*float64(size), radius*float64(size)
	for y := max(0, int(cy-r)); y <= min(size-1, int(cy+r)); y++ {
		for x := max(0, int(cx-r)); x <= min(size-1, int(cx+r)); x++ {
			if math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) <= r {
				dst.SetNRGBA(x, y, fill)
			}
		}
	}
}

func drawPolygon(dst *image.NRGBA, size int, polygon []point, fill color.NRGBA) {
	for y := 0; y < size; y++ {
		scanY := (float64(y) + 0.5) / float64(size)
		var intersections []float64
		for i, a := range polygon {
			b := polygon[(i+1)%len(polygon)]
			if (a.y <= scanY && b.y > scanY) || (b.y <= scanY && a.y > scanY) {
				intersections = append(intersections, a.x+(scanY-a.y)*(b.x-a.x)/(b.y-a.y))
			}
		}
		sort.Float64s(intersections)
		for i := 0; i+1 < len(intersections); i += 2 {
			from := max(0, int(math.Ceil(intersections[i]*float64(size)-0.5)))
			to := min(size-1, int(math.Floor(intersections[i+1]*float64(size)-0.5)))
			for x := from; x <= to; x++ {
				dst.SetNRGBA(x, y, fill)
			}
		}
	}
}

func insideRoundedSquare(x, y, size int, radius float64) bool {
	px, py := float64(x)+0.5, float64(y)+0.5
	cx := math.Max(radius, math.Min(float64(size)-radius, px))
	cy := math.Max(radius, math.Min(float64(size)-radius, py))
	return math.Hypot(px-cx, py-cy) <= radius
}

func downsample(src *image.NRGBA, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := float64(src.Bounds().Dx()) / float64(size)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			left, right := float64(x)*scale, float64(x+1)*scale
			top, bottom := float64(y)*scale, float64(y+1)*scale
			var red, green, blue, alpha, weight float64

			for sy := int(math.Floor(top)); sy < int(math.Ceil(bottom)); sy++ {
				if sy < 0 || sy >= src.Bounds().Dy() {
					continue
				}
				yWeight := math.Min(bottom, float64(sy+1)) - math.Max(top, float64(sy))
				for sx := int(math.Floor(left)); sx < int(math.Ceil(right)); sx++ {
					if sx < 0 || sx >= src.Bounds().Dx() {
						continue
					}
					xWeight := math.Min(right, float64(sx+1)) - math.Max(left, float64(sx))
					pixelWeight := xWeight * yWeight
					c := src.NRGBAAt(sx, sy)
					a := float64(c.A) / 255
					red += float64(c.R) * a * pixelWeight
					green += float64(c.G) * a * pixelWeight
					blue += float64(c.B) * a * pixelWeight
					alpha += float64(c.A) * pixelWeight
					weight += pixelWeight
				}
			}

			if alpha == 0 || weight == 0 {
				continue
			}
			a := alpha / weight
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(red / (alpha / 255)),
				G: uint8(green / (alpha / 255)),
				B: uint8(blue / (alpha / 255)),
				A: uint8(a),
			})
		}
	}

	return dst
}

func mix(a, b color.NRGBA, ratio float64) color.NRGBA {
	ratio = math.Max(0, math.Min(1, ratio))
	return color.NRGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*ratio),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*ratio),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*ratio),
		A: 0xff,
	}
}

func hex(value uint32) color.NRGBA {
	return color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 0xff}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "generate icon:", err)
	os.Exit(1)
}
