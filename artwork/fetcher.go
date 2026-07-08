// Package artwork fetches podcast cover images and derives theme colors for
// the discover/images/metadata endpoint.
package artwork

import (
	"context"
	"fmt"
	"image"
	"io"
	"net/http"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// maxImageBytes caps artwork downloads; podcast covers are typically <2 MB.
const maxImageBytes = 8 << 20

// ImageFetcher retrieves and decodes a cover image. The production
// implementation dials the publisher's CDN; tests supply fixtures.
type ImageFetcher interface {
	FetchImage(ctx context.Context, url string) (image.Image, error)
}

type HTTPImageFetcher struct {
	Client    *http.Client
	UserAgent string
}

func NewHTTPImageFetcher() *HTTPImageFetcher {
	return &HTTPImageFetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		UserAgent: "podcast-backend/1.0 (+https://github.com/hbmartin/podcast-backend)",
	}
}

func (f *HTTPImageFetcher) FetchImage(ctx context.Context, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork fetch returned status %d", resp.StatusCode)
	}

	img, _, err := image.Decode(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, fmt.Errorf("artwork decode failed: %w", err)
	}
	return img, nil
}
