package artwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 200, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestFetchImagePNG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes(t))
	}))
	defer server.Close()

	img, err := NewHTTPImageFetcher().FetchImage(context.Background(), server.URL+"/art.png")
	assert.NoError(t, err)
	assert.Equal(t, 10, img.Bounds().Dx())
}

func TestFetchImageErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Write([]byte("not an image"))
		}
	}))
	defer server.Close()

	fetcher := NewHTTPImageFetcher()

	_, err := fetcher.FetchImage(context.Background(), server.URL+"/404")
	assert.Error(t, err)

	_, err = fetcher.FetchImage(context.Background(), server.URL+"/garbage")
	assert.Error(t, err)
}
