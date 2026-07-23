package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// FetchResult is one conditional GET of a feed.
type FetchResult struct {
	// Body is nil when NotModified is true.
	Body         io.ReadCloser
	ETag         string
	LastModified string
	NotModified  bool
}

// Fetcher retrieves feed documents. The production implementation dials out;
// tests supply fixtures — nothing in the test suite performs network IO.
type Fetcher interface {
	Fetch(ctx context.Context, url string, etag string, lastModified string) (*FetchResult, error)
}

// HTTPFetcher fetches feeds over HTTP with conditional-request support.
type HTTPFetcher struct {
	Client               *http.Client
	UserAgent            string
	AllowPrivateNetworks bool
}

type ipResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type contextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

var blockedFeedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// NewHTTPFetcher returns the production-safe feed client. Passing true is a
// test-only escape hatch for local end-to-end fixtures; callers should leave
// it unset in deployed environments.
func NewHTTPFetcher(allowPrivateNetworks ...bool) *HTTPFetcher {
	allowPrivate := len(allowPrivateNetworks) > 0 && allowPrivateNetworks[0]
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A proxy would resolve and dial the destination outside this process,
	// bypassing the address checks below.
	transport.Proxy = nil
	transport.DialContext = safeFeedDialContext(net.DefaultResolver, dialer, allowPrivate)

	return &HTTPFetcher{
		Client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many feed redirects")
				}
				return validateFeedURL(req.URL.String(), allowPrivate)
			},
		},
		UserAgent:            "podcast-backend/1.0 (+https://github.com/hbmartin/podcast-backend)",
		AllowPrivateNetworks: allowPrivate,
	}
}

// ValidateFeedURL rejects non-HTTP URLs and literal addresses that are not
// public. Hostnames are resolved and revalidated by the guarded dialer for
// every connection, including redirects, which closes DNS-rebinding paths.
func ValidateFeedURL(rawURL string) error {
	return validateFeedURL(rawURL, false)
}

func validateFeedURL(rawURL string, allowPrivate bool) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("invalid feed URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("feed URL scheme %q is not allowed", parsed.Scheme)
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("feed URL must have a host and no user info")
	}
	if addr, err := netip.ParseAddr(parsed.Hostname()); err == nil && !allowPrivate && !isPublicFeedAddress(addr) {
		return fmt.Errorf("feed URL address %s is not public", addr)
	}
	return nil
}

func isPublicFeedAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range blockedFeedNetworks {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func safeFeedDialContext(resolver ipResolver, dialer contextDialer, allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid feed dial address: %w", err)
		}

		var addresses []netip.Addr
		if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
			addresses = []netip.Addr{literal}
		} else {
			addresses, err = resolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve feed host: %w", err)
			}
		}
		if len(addresses) == 0 {
			return nil, errors.New("feed host resolved to no addresses")
		}
		for _, addr := range addresses {
			if !allowPrivate && !isPublicFeedAddress(addr) {
				return nil, fmt.Errorf("feed host resolved to non-public address %s", addr)
			}
		}

		var dialErr error
		for _, addr := range addresses {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, fmt.Errorf("dial feed host: %w", dialErr)
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string, etag string, lastModified string) (*FetchResult, error) {
	if err := validateFeedURL(rawURL, f.AllowPrivateNetworks); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNotModified {
		resp.Body.Close()
		return &FetchResult{NotModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("feed fetch returned status %d", resp.StatusCode)
	}

	return &FetchResult{
		Body:         resp.Body,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}
