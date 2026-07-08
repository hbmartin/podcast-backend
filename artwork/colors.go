package artwork

import (
	"fmt"
	"image"
	"math"
)

// Colors is the palette served at discover/images/metadata/{uuid}.json; the
// client reads exactly these three values.
type Colors struct {
	Background     string
	TintForLightBg string
	TintForDarkBg  string
}

// FallbackColors is served when artwork cannot be fetched or decoded.
var FallbackColors = Colors{
	Background:     "#3D3D3D",
	TintForLightBg: "#F44336",
	TintForDarkBg:  "#F44336",
}

// ExtractColors derives a background color (dominant/average tone) and two
// tints readable on light and dark backgrounds from a cover image.
func ExtractColors(img image.Image) Colors {
	r, g, b := averageColor(img)
	h, s, l := rgbToHSL(r, g, b)

	// tints need some saturation to read as a color rather than gray
	tintS := math.Max(s, 0.45)
	tintForLight := hexFromHSL(h, tintS, math.Min(l, 0.45))
	tintForDark := hexFromHSL(h, tintS, math.Max(l, 0.55))

	return Colors{
		Background:     hexFromRGB(r, g, b),
		TintForLightBg: tintForLight,
		TintForDarkBg:  tintForDark,
	}
}

// averageColor samples the image on a coarse grid (at most ~64x64 points).
func averageColor(img image.Image) (uint8, uint8, uint8) {
	bounds := img.Bounds()
	if bounds.Empty() {
		return 0x3d, 0x3d, 0x3d
	}

	stepX := max(1, bounds.Dx()/64)
	stepY := max(1, bounds.Dy()/64)

	var sumR, sumG, sumB, count uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			r, g, b, _ := img.At(x, y).RGBA() // 16-bit channels
			sumR += uint64(r >> 8)
			sumG += uint64(g >> 8)
			sumB += uint64(b >> 8)
			count++
		}
	}
	if count == 0 {
		return 0x3d, 0x3d, 0x3d
	}
	return uint8(sumR / count), uint8(sumG / count), uint8(sumB / count)
}

func hexFromRGB(r, g, b uint8) string {
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

func hexFromHSL(h, s, l float64) string {
	r, g, b := hslToRGB(h, s, l)
	return hexFromRGB(r, g, b)
}

// rgbToHSL converts 8-bit RGB to hue [0,360), saturation and lightness [0,1].
func rgbToHSL(r8, g8, b8 uint8) (h, s, l float64) {
	r := float64(r8) / 255
	g := float64(g8) / 255
	b := float64(b8) / 255

	maxC := math.Max(r, math.Max(g, b))
	minC := math.Min(r, math.Min(g, b))
	l = (maxC + minC) / 2

	if maxC == minC {
		return 0, 0, l
	}

	d := maxC - minC
	if l > 0.5 {
		s = d / (2 - maxC - minC)
	} else {
		s = d / (maxC + minC)
	}

	switch maxC {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2

	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}

	return uint8(math.Round((r + m) * 255)),
		uint8(math.Round((g + m) * 255)),
		uint8(math.Round((b + m) * 255))
}
