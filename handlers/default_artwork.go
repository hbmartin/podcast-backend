package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type generatedArtwork struct {
	bytes []byte
	etag  string
}

var defaultArtworkCache sync.Map

// These are AppTheme.userEpisodeColor's exact light/dark mappings from the
// iOS ThemeColors.json. Variant one is AppTheme's theme-independent gray.
var defaultArtworkColors = map[string][8]color.RGBA{
	"light": {
		hexColor("8F97A4"), hexColor("F43E37"), hexColor("03A9F4"), hexColor("78D549"),
		hexColor("FEC635"), hexColor("FF9D3B"), hexColor("5D31C4"), hexColor("E93673"),
	},
	"dark": {
		hexColor("8F97A4"), hexColor("DF514C"), hexColor("1BA0DC"), hexColor("7FBF5F"),
		hexColor("EABD49"), hexColor("EB9D4F"), hexColor("9D79F2"), hexColor("D24D7A"),
	},
}

func hexColor(value string) color.RGBA {
	r, _ := strconv.ParseUint(value[0:2], 16, 8)
	g, _ := strconv.ParseUint(value[2:4], 16, 8)
	b, _ := strconv.ParseUint(value[4:6], 16, 8)
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xff}
}

// GetDefaultArtwork serves the finite, generated user-episode artwork set.
func (h Handlers) GetDefaultArtwork(w http.ResponseWriter, r *http.Request) {
	theme := r.PathValue("theme")
	palette, ok := defaultArtworkColors[theme]
	if !ok {
		http.NotFound(w, r)
		return
	}
	size, err := strconv.Atoi(r.PathValue("size"))
	if err != nil || (size != 280 && size != 960) {
		http.NotFound(w, r)
		return
	}
	file := r.PathValue("file")
	if !strings.HasSuffix(file, ".png") {
		http.NotFound(w, r)
		return
	}
	variant, err := strconv.Atoi(strings.TrimSuffix(file, ".png"))
	if err != nil || variant < 1 || variant > len(palette) {
		http.NotFound(w, r)
		return
	}

	key := theme + "/" + strconv.Itoa(size) + "/" + strconv.Itoa(variant)
	value, _ := defaultArtworkCache.LoadOrStore(key, generateDefaultArtwork(size, palette[variant-1]))
	asset := value.(generatedArtwork)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", asset.etag)
	if r.Header.Get("If-None-Match") == asset.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.bytes)))
	_, _ = w.Write(asset.bytes)
}

func generateDefaultArtwork(size int, background color.RGBA) generatedArtwork {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fillRGBA(img, img.Bounds(), background)

	x0, x1 := size*22/100, size*78/100
	y0, y1 := size*15/100, size*83/100
	fold := size * 15 / 100

	shadow := image.NewAlpha(img.Bounds())
	dx, dy := size/80, size/35
	fillDocumentAlpha(shadow, x0+dx, y0+dy, x1+dx, y1+dy, fold, 115)
	shadow = blurAlpha(shadow, maxInt(2, size/45))
	compositeAlpha(img, shadow, color.RGBA{A: 85})

	fillDocument(img, x0, y0, x1, y1, fold, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	// The folded corner is slightly shaded to preserve the paper structure.
	fillTriangle(img, image.Pt(x1-fold, y0), image.Pt(x1-fold, y0+fold), image.Pt(x1, y0+fold), color.RGBA{R: 0xe8, G: 0xeb, B: 0xef, A: 0xff})

	line := color.RGBA{R: 0xd8, G: 0xdc, B: 0xe2, A: 0xff}
	lineX0, lineX1 := x0+size*8/100, x1-size*8/100
	lineHeight := maxInt(2, size/90)
	for _, y := range []int{y0 + size*31/100, y0 + size*40/100, y0 + size*49/100} {
		fillRoundedRect(img, image.Rect(lineX0, y, lineX1, y+lineHeight), lineHeight/2, line)
	}

	var buffer bytes.Buffer
	_ = png.Encode(&buffer, img)
	data := buffer.Bytes()
	sum := sha256.Sum256(data)
	return generatedArtwork{bytes: data, etag: `"` + hex.EncodeToString(sum[:]) + `"`}
}

func fillRGBA(img *image.RGBA, bounds image.Rectangle, c color.RGBA) {
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func fillDocument(img *image.RGBA, x0, y0, x1, y1, fold int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		end := x1
		if y < y0+fold {
			end = x1 - fold + (y - y0)
		}
		for x := x0; x < end; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func fillDocumentAlpha(img *image.Alpha, x0, y0, x1, y1, fold int, alpha uint8) {
	for y := maxInt(0, y0); y < minInt(y1, img.Bounds().Max.Y); y++ {
		end := x1
		if y < y0+fold {
			end = x1 - fold + (y - y0)
		}
		for x := maxInt(0, x0); x < minInt(end, img.Bounds().Max.X); x++ {
			img.SetAlpha(x, y, color.Alpha{A: alpha})
		}
	}
}

func fillTriangle(img *image.RGBA, a, b, c image.Point, fill color.RGBA) {
	for y := a.Y; y <= b.Y; y++ {
		width := y - a.Y
		for x := a.X; x <= minInt(c.X, a.X+width); x++ {
			img.SetRGBA(x, y, fill)
		}
	}
}

func fillRoundedRect(img *image.RGBA, rect image.Rectangle, radius int, c color.RGBA) {
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if radius <= 0 || (x >= rect.Min.X+radius && x < rect.Max.X-radius) || (y >= rect.Min.Y+radius && y < rect.Max.Y-radius) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func blurAlpha(src *image.Alpha, radius int) *image.Alpha {
	if radius < 1 {
		return src
	}
	bounds := src.Bounds()
	tmp, dst := image.NewAlpha(bounds), image.NewAlpha(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		sum := 0
		for x := -radius; x <= radius; x++ {
			if x >= 0 && x < bounds.Max.X {
				sum += int(src.AlphaAt(x, y).A)
			}
		}
		for x := 0; x < bounds.Max.X; x++ {
			tmp.SetAlpha(x, y, color.Alpha{A: uint8(sum / (2*radius + 1))})
			if left := x - radius; left >= 0 {
				sum -= int(src.AlphaAt(left, y).A)
			}
			if right := x + radius + 1; right < bounds.Max.X {
				sum += int(src.AlphaAt(right, y).A)
			}
		}
	}
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		sum := 0
		for y := -radius; y <= radius; y++ {
			if y >= 0 && y < bounds.Max.Y {
				sum += int(tmp.AlphaAt(x, y).A)
			}
		}
		for y := 0; y < bounds.Max.Y; y++ {
			dst.SetAlpha(x, y, color.Alpha{A: uint8(sum / (2*radius + 1))})
			if top := y - radius; top >= 0 {
				sum -= int(tmp.AlphaAt(x, top).A)
			}
			if bottom := y + radius + 1; bottom < bounds.Max.Y {
				sum += int(tmp.AlphaAt(x, bottom).A)
			}
		}
	}
	return dst
}

func compositeAlpha(dst *image.RGBA, mask *image.Alpha, c color.RGBA) {
	for y := mask.Bounds().Min.Y; y < mask.Bounds().Max.Y; y++ {
		for x := mask.Bounds().Min.X; x < mask.Bounds().Max.X; x++ {
			a := uint16(mask.AlphaAt(x, y).A) * uint16(c.A) / 255
			if a == 0 {
				continue
			}
			base := dst.RGBAAt(x, y)
			inv := 255 - a
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((uint16(c.R)*a + uint16(base.R)*inv) / 255),
				G: uint8((uint16(c.G)*a + uint16(base.G)*inv) / 255),
				B: uint8((uint16(c.B)*a + uint16(base.B)*inv) / 255), A: 0xff,
			})
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
