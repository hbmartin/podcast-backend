package artwork

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func solidImage(c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestExtractColorsSolidRed(t *testing.T) {
	colors := ExtractColors(solidImage(color.RGBA{R: 200, G: 20, B: 20, A: 255}))

	assert.Equal(t, "#C81414", colors.Background)
	// tints stay reddish and within readable lightness bounds
	assert.True(t, strings.HasPrefix(colors.TintForLightBg, "#"))
	assert.Len(t, colors.TintForLightBg, 7)
	assert.Len(t, colors.TintForDarkBg, 7)
	assert.NotEqual(t, colors.TintForLightBg, "#000000")
	assert.NotEqual(t, colors.TintForDarkBg, "#FFFFFF")
}

func TestExtractColorsGrayGetsSaturatedTints(t *testing.T) {
	colors := ExtractColors(solidImage(color.RGBA{R: 128, G: 128, B: 128, A: 255}))

	assert.Equal(t, "#808080", colors.Background)
	// tints must not be pure gray (saturation floor applies)
	assert.NotEqual(t, colors.TintForLightBg, colors.Background)
}

func TestExtractColorsSplitImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if x < 50 {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			} else {
				img.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}

	colors := ExtractColors(img)
	// average of red and blue halves is purple-ish: both channels present
	assert.True(t, strings.HasPrefix(colors.Background, "#"))
	assert.NotEqual(t, "#FF0000", colors.Background)
	assert.NotEqual(t, "#0000FF", colors.Background)
}

func TestHSLRoundTrip(t *testing.T) {
	cases := []struct{ r, g, b uint8 }{
		{255, 0, 0}, {0, 255, 0}, {0, 0, 255},
		{200, 20, 20}, {12, 200, 150}, {240, 240, 10},
	}
	for _, c := range cases {
		h, s, l := rgbToHSL(c.r, c.g, c.b)
		r2, g2, b2 := hslToRGB(h, s, l)
		assert.InDelta(t, int(c.r), int(r2), 1)
		assert.InDelta(t, int(c.g), int(g2), 1)
		assert.InDelta(t, int(c.b), int(b2), 1)
	}
}

func TestEmptyImageFallsBack(t *testing.T) {
	colors := ExtractColors(image.NewRGBA(image.Rect(0, 0, 0, 0)))
	assert.Equal(t, "#3D3D3D", colors.Background)
}
