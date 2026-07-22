package crawler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticResolver struct {
	addresses []netip.Addr
}

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.addresses, nil
}

type recordingDialer struct {
	addresses []string
}

func (d *recordingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.addresses = append(d.addresses, address)
	return nil, errors.New("test dial stopped")
}

func TestValidateFeedURLRejectsUnsafeSchemesAndLiteralAddresses(t *testing.T) {
	for _, rawURL := range []string{
		"file:///etc/passwd",
		"http://127.0.0.1/admin",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/admin",
		"http://[64:ff9b::7f00:1]/admin",
		"http://[2002:7f00:1::]/admin",
		"https://user:pass@example.com/feed",
	} {
		t.Run(rawURL, func(t *testing.T) {
			assert.Error(t, ValidateFeedURL(rawURL))
		})
	}
	assert.NoError(t, ValidateFeedURL("https://example.com/feed.xml"))
}

func TestSafeFeedDialRejectsPrivateDNSAnswers(t *testing.T) {
	dialer := &recordingDialer{}
	dial := safeFeedDialContext(staticResolver{addresses: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("127.0.0.1"),
	}}, dialer, false)

	conn, err := dial(context.Background(), "tcp", "example.com:443")
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Empty(t, dialer.addresses, "no address is dialed when any DNS answer is unsafe")
}

func TestSafeFeedDialUsesResolvedPublicAddress(t *testing.T) {
	resolver := staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
	var gotAddress string
	dialer := dialContextFunc(func(_ context.Context, _, address string) (net.Conn, error) {
		gotAddress = address
		return nil, errors.New("test dial stopped")
	})

	_, err := safeFeedDialContext(resolver, dialer, false)(context.Background(), "tcp", "example.com:443")
	require.Error(t, err)
	assert.Equal(t, "93.184.216.34:443", gotAddress)
}

func TestFeedRedirectsAreRevalidated(t *testing.T) {
	fetcher := NewHTTPFetcher()
	private, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/admin", nil)
	require.NoError(t, err)
	assert.Error(t, fetcher.Client.CheckRedirect(private, []*http.Request{{}}))

	public, err := http.NewRequest(http.MethodGet, "https://example.com/feed.xml", nil)
	require.NoError(t, err)
	assert.NoError(t, fetcher.Client.CheckRedirect(public, []*http.Request{{}}))
	assert.Error(t, fetcher.Client.CheckRedirect(public, make([]*http.Request, 10)))
}

func TestSafeFeedDialRejectsAlternateLoopbackNotation(t *testing.T) {
	dialer := &recordingDialer{}
	dial := safeFeedDialContext(staticResolver{addresses: []netip.Addr{
		netip.MustParseAddr("127.0.0.1"),
	}}, dialer, false)

	_, err := dial(context.Background(), "tcp", "2130706433:80")

	require.Error(t, err)
	assert.Empty(t, dialer.addresses)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func (f dialContextFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}
